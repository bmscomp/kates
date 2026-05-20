#!/bin/bash
# ──────────────────────────────────────────────────────────────────────────────
# connectivity-test.sh — Kates Cluster Connectivity Verification Suite
#
# Runs a structured set of probes against the deployed Kates stack and emits
# JSON-lines output for each test, consumed by the kates CLI for rendering.
#
# Required environment variables:
#   KAFKA_BOOTSTRAP  — Kafka bootstrap FQDN:port  (e.g. krafter-kafka-bootstrap.kafka.svc.cluster.local:9092)
#   KATES_API        — Kates API FQDN:port        (e.g. kates.kates.svc.cluster.local:8080)
#   CLUSTER_DOMAIN   — Cluster DNS domain          (e.g. cluster.local)
#   KAFKA_NS         — Kafka namespace             (e.g. kafka)
#   APP_NS           — Kates app namespace         (e.g. kates)
#
# Optional:
#   TOPOLOGY         — "single" or "isolated" (default: "isolated")
#   SCHEMA_REGISTRY  — Schema Registry URL (empty = skip)
#   DRY_RUN          — "true" to print test plan without executing
# ──────────────────────────────────────────────────────────────────────────────
set -uo pipefail

# ── Defaults ──────────────────────────────────────────────────────────────────
KAFKA_BOOTSTRAP="${KAFKA_BOOTSTRAP:-}"
KATES_API="${KATES_API:-}"
CLUSTER_DOMAIN="${CLUSTER_DOMAIN:-cluster.local}"
KAFKA_NS="${KAFKA_NS:-kafka}"
APP_NS="${APP_NS:-kates}"
TOPOLOGY="${TOPOLOGY:-isolated}"
SCHEMA_REGISTRY="${SCHEMA_REGISTRY:-}"
DRY_RUN="${DRY_RUN:-false}"

# ── State ─────────────────────────────────────────────────────────────────────
TOTAL=0
PASSED=0
FAILED=0
START_TIME=$(date +%s%N 2>/dev/null || date +%s)

# ── Helpers ───────────────────────────────────────────────────────────────────

# Extract host and port from a HOST:PORT string
split_host_port() {
    local input="$1"
    HOST="${input%:*}"
    PORT="${input##*:}"
}

# Emit a JSON result line
emit() {
    local test_name="$1"
    local status="$2"
    local elapsed="$3"
    local detail="${4:-}"
    if [ -n "$detail" ]; then
        printf '{"test":"%s","status":"%s","elapsed":"%s","detail":"%s"}\n' \
            "$test_name" "$status" "$elapsed" "$detail"
    else
        printf '{"test":"%s","status":"%s","elapsed":"%s"}\n' \
            "$test_name" "$status" "$elapsed"
    fi
}

# Run a single test. Usage: run_test "test_name" command...
run_test() {
    local name="$1"
    shift
    TOTAL=$((TOTAL + 1))

    if [ "$DRY_RUN" = "true" ]; then
        emit "$name" "SKIP" "0ms" "dry-run"
        return 0
    fi

    local t_start
    t_start=$(date +%s%N 2>/dev/null || date +%s)

    local output
    if output=$("$@" 2>&1); then
        local t_end
        t_end=$(date +%s%N 2>/dev/null || date +%s)
        local elapsed_ms=$(( (t_end - t_start) / 1000000 ))
        emit "$name" "PASS" "${elapsed_ms}ms"
        PASSED=$((PASSED + 1))
        return 0
    else
        local t_end
        t_end=$(date +%s%N 2>/dev/null || date +%s)
        local elapsed_ms=$(( (t_end - t_start) / 1000000 ))
        # Sanitize output for JSON (replace quotes and newlines)
        local sanitized
        sanitized=$(echo "$output" | tr '\n' ' ' | tr '"' "'" | head -c 200)
        emit "$name" "FAIL" "${elapsed_ms}ms" "$sanitized"
        FAILED=$((FAILED + 1))
        return 1
    fi
}

# ── Validation ────────────────────────────────────────────────────────────────
if [ -z "$KAFKA_BOOTSTRAP" ]; then
    echo '{"error":"KAFKA_BOOTSTRAP environment variable is required"}'
    exit 1
fi
if [ -z "$KATES_API" ]; then
    echo '{"error":"KATES_API environment variable is required"}'
    exit 1
fi

# ══════════════════════════════════════════════════════════════════════════════
# Test Suite
# ══════════════════════════════════════════════════════════════════════════════

split_host_port "$KAFKA_BOOTSTRAP"
KAFKA_HOST="$HOST"
KAFKA_PORT="$PORT"

split_host_port "$KATES_API"
KATES_HOST="$HOST"
KATES_PORT="$PORT"

# ── 1. DNS Resolution ────────────────────────────────────────────────────────
run_test "dns_kafka_bootstrap" \
    nslookup "$KAFKA_HOST" || true

run_test "dns_kates_api" \
    nslookup "$KATES_HOST" || true

run_test "dns_cluster_domain" \
    bash -c "grep -q 'search' /etc/resolv.conf && grep '$CLUSTER_DOMAIN' /etc/resolv.conf" || true

# ── 2. TCP Connectivity ──────────────────────────────────────────────────────
run_test "tcp_kafka_${KAFKA_PORT}" \
    nc -z -w 5 "$KAFKA_HOST" "$KAFKA_PORT"

run_test "tcp_kates_api_${KATES_PORT}" \
    nc -z -w 5 "$KATES_HOST" "$KATES_PORT"

# ── 3. Kafka Broker Validation ───────────────────────────────────────────────
run_test "kafka_broker_metadata" \
    bash -c "kcat -b '$KAFKA_BOOTSTRAP' -L -t '__consumer_offsets' -J 2>/dev/null | jq -e '.brokers | length > 0'"

run_test "kafka_topics_list" \
    bash -c "kcat -b '$KAFKA_BOOTSTRAP' -L -J 2>/dev/null | jq -e '.topics[] | select(.topic==\"kates-events\")'"

# ── 4. Kates API Health ──────────────────────────────────────────────────────
run_test "api_health" \
    bash -c "curl -sf -o /dev/null -w '%{http_code}' http://${KATES_HOST}:${KATES_PORT}/api/health | grep -q 200"

run_test "api_ready" \
    bash -c "curl -sf -o /dev/null -w '%{http_code}' http://${KATES_HOST}:${KATES_PORT}/q/health/ready | grep -q 200"

run_test "api_live" \
    bash -c "curl -sf -o /dev/null -w '%{http_code}' http://${KATES_HOST}:${KATES_PORT}/q/health/live | grep -q 200"

run_test "api_cluster" \
    bash -c "curl -sf -o /dev/null -w '%{http_code}' http://${KATES_HOST}:${KATES_PORT}/api/cluster | grep -q 200"

# ── 5. Cross-Namespace Network (isolated topology only) ──────────────────────
if [ "$TOPOLOGY" = "isolated" ] && [ "$KAFKA_NS" != "$APP_NS" ]; then
    run_test "crossns_kafka" \
        nc -z -w 5 "krafter-kafka-bootstrap.${KAFKA_NS}.svc.${CLUSTER_DOMAIN}" "$KAFKA_PORT"

    run_test "crossns_kates" \
        nc -z -w 5 "kates.${APP_NS}.svc.${CLUSTER_DOMAIN}" "$KATES_PORT"
fi

# ── 6. Schema Registry (if deployed) ─────────────────────────────────────────
if [ -n "$SCHEMA_REGISTRY" ]; then
    run_test "schema_registry" \
        bash -c "curl -sf -o /dev/null -w '%{http_code}' '${SCHEMA_REGISTRY}/apis/ccompat/v7/subjects' | grep -q 200"
fi

# ══════════════════════════════════════════════════════════════════════════════
# Summary
# ══════════════════════════════════════════════════════════════════════════════
END_TIME=$(date +%s%N 2>/dev/null || date +%s)
TOTAL_MS=$(( (END_TIME - START_TIME) / 1000000 ))

printf '{"summary":{"total":%d,"passed":%d,"failed":%d,"elapsed":"%sms"}}\n' \
    "$TOTAL" "$PASSED" "$FAILED" "$TOTAL_MS"

if [ "$FAILED" -gt 0 ]; then
    exit 1
fi
exit 0

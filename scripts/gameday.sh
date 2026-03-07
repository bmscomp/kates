#!/bin/bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "${SCRIPT_DIR}/common.sh"

KATES_API="${KATES_API_URL:-http://localhost:30083}"
PROMETHEUS_URL="${PROMETHEUS_URL:-http://localhost:30090}"
NAMESPACE="kafka"
RESULTS_FILE="/tmp/gameday-$(date +%Y%m%d-%H%M%S).json"

# GameDay phases
PHASES=("pre-flight" "baseline" "chaos-inject" "chaos-observe" "chaos-recover" "post-flight" "report")

log_phase() {
    echo ""
    echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
    info "📋 PHASE: $1"
    echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
}

check_metric() {
    local query="$1"
    local description="$2"
    local result
    result=$(curl -s "${PROMETHEUS_URL}/api/v1/query" --data-urlencode "query=${query}" | python3 -c "import sys,json; r=json.load(sys.stdin); print(r['data']['result'][0]['value'][1] if r['data']['result'] else 'N/A')" 2>/dev/null || echo "N/A")
    echo "  ${description}: ${result}"
    echo "${description}=${result}" >> "${RESULTS_FILE}.metrics"
}

wait_for_recovery() {
    local timeout="${1:-300}"
    local elapsed=0
    info "Waiting for cluster recovery (timeout: ${timeout}s)..."

    while [ $elapsed -lt $timeout ]; do
        local under_replicated
        under_replicated=$(kubectl exec -n kafka krafter-brokers-alpha-0 -- \
            /opt/kafka/bin/kafka-metadata.sh --snapshot /var/lib/kafka/data-0/__cluster_metadata-0/00000000000000000000.log \
            --under-replicated 2>/dev/null | wc -l || echo "999")

        if [ "$under_replicated" -eq 0 ] 2>/dev/null; then
            info "✅ Cluster recovered in ${elapsed}s"
            return 0
        fi

        sleep 10
        elapsed=$((elapsed + 10))
    done

    warn "⚠️  Recovery timeout after ${timeout}s"
    return 1
}

phase_preflight() {
    log_phase "PRE-FLIGHT CHECKS"

    info "Checking Kafka cluster health..."
    kubectl wait kafka/krafter --for=condition=Ready --timeout=30s -n ${NAMESPACE}
    echo "  ✅ Kafka cluster is ready"

    info "Checking broker count..."
    local broker_count
    broker_count=$(kubectl get pods -n ${NAMESPACE} -l strimzi.io/kind=Kafka,strimzi.io/cluster=krafter --field-selector=status.phase=Running --no-headers | wc -l | tr -d ' ')
    echo "  Brokers running: ${broker_count}"
    [ "$broker_count" -ge 3 ] || { echo "  ❌ Expected at least 3 brokers"; exit 1; }

    info "Checking Kates API..."
    local health
    health=$(curl -s -o /dev/null -w "%{http_code}" "${KATES_API}/api/health" 2>/dev/null || echo "000")
    if [ "$health" = "200" ]; then
        echo "  ✅ Kates API reachable"
    else
        warn "  ⚠️  Kates API unreachable (HTTP ${health}) — continuing without API tests"
    fi

    info "Checking Prometheus..."
    local prom_health
    prom_health=$(curl -s -o /dev/null -w "%{http_code}" "${PROMETHEUS_URL}/-/ready" 2>/dev/null || echo "000")
    if [ "$prom_health" = "200" ]; then
        echo "  ✅ Prometheus reachable"
    else
        warn "  ⚠️  Prometheus unreachable — metrics checks will be skipped"
    fi

    echo "PRE-FLIGHT=PASS" > "${RESULTS_FILE}.metrics"
}

phase_baseline() {
    log_phase "BASELINE PERFORMANCE"

    info "Running baseline load test (100k records)..."
    local baseline_response
    baseline_response=$(curl -s -X POST "${KATES_API}/api/tests" \
        -H "Content-Type: application/json" \
        -d '{"testType":"LOAD","numRecords":100000,"partitions":3}' 2>/dev/null || echo '{"id":"skip"}')

    local test_id
    test_id=$(echo "$baseline_response" | python3 -c "import sys,json; print(json.load(sys.stdin).get('id','skip'))" 2>/dev/null || echo "skip")

    if [ "$test_id" != "skip" ]; then
        info "Baseline test started: ${test_id}"
        info "Waiting for baseline to complete (timeout: 120s)..."
        local elapsed=0
        while [ $elapsed -lt 120 ]; do
            local status
            status=$(curl -s "${KATES_API}/api/tests/${test_id}" 2>/dev/null | \
                python3 -c "import sys,json; print(json.load(sys.stdin).get('status','UNKNOWN'))" 2>/dev/null || echo "UNKNOWN")
            [ "$status" = "DONE" ] && break
            [ "$status" = "FAILED" ] && { warn "Baseline test failed"; break; }
            sleep 5
            elapsed=$((elapsed + 5))
        done

        info "Collecting baseline metrics..."
        check_metric "kafka_server_brokertopicmetrics_messagesin_total" "baseline_messages_in"
        check_metric "kafka_server_brokertopicmetrics_bytesin_total" "baseline_bytes_in"
    else
        warn "Skipping baseline — Kates API not available"
    fi

    echo "BASELINE=COMPLETE" >> "${RESULTS_FILE}.metrics"
}

phase_chaos_inject() {
    log_phase "CHAOS INJECTION"

    local experiment="${1:-kafka-pod-delete}"
    info "Injecting chaos experiment: ${experiment}"

    if kubectl get chaosengine -n ${NAMESPACE} 2>/dev/null; then
        kubectl apply -f "config/litmus/experiments/${experiment}.yaml" -n ${NAMESPACE} 2>/dev/null || \
            warn "Experiment ${experiment} not found — using pod delete fallback"
    fi

    # Fallback: manual pod disruption
    info "Executing pod disruption on broker-alpha..."
    kubectl delete pod krafter-brokers-alpha-0 -n ${NAMESPACE} --grace-period=30 2>/dev/null || \
        warn "Could not delete broker pod (may not exist)"

    echo "CHAOS_INJECTED=$(date -u +%Y-%m-%dT%H:%M:%SZ)" >> "${RESULTS_FILE}.metrics"

    info "Chaos injected — observing for 60s..."
    sleep 60
}

phase_chaos_observe() {
    log_phase "CHAOS OBSERVATION"

    info "Collecting chaos impact metrics..."
    check_metric "kafka_controller_kafkacontroller_offlinepartitionscount" "offline_partitions"
    check_metric "kafka_server_replicamanager_underreplicatedpartitions" "under_replicated"
    check_metric "sum(kafka_consumergroup_lag_sum)" "total_consumer_lag"
    check_metric "rate(kafka_server_brokertopicmetrics_messagesin_total[1m])" "messages_in_rate"

    info "Checking ISR status..."
    local isr_shrunk
    isr_shrunk=$(kubectl exec -n kafka krafter-brokers-sigma-0 -- \
        /opt/kafka/bin/kafka-topics.sh --bootstrap-server localhost:9092 --describe --under-replicated-partitions 2>/dev/null | wc -l || echo "0")
    echo "  Under-replicated partitions (from broker): ${isr_shrunk}"

    echo "OBSERVE=COMPLETE" >> "${RESULTS_FILE}.metrics"
}

phase_chaos_recover() {
    log_phase "RECOVERY VALIDATION"

    wait_for_recovery 300

    info "Post-recovery metrics..."
    check_metric "kafka_controller_kafkacontroller_offlinepartitionscount" "post_recovery_offline"
    check_metric "kafka_server_replicamanager_underreplicatedpartitions" "post_recovery_under_replicated"

    info "Checking all brokers are running..."
    kubectl wait pod -l strimzi.io/kind=Kafka -n ${NAMESPACE} --for=condition=Ready --timeout=120s 2>/dev/null || \
        warn "Some broker pods not ready"

    echo "RECOVERY=COMPLETE" >> "${RESULTS_FILE}.metrics"
}

phase_postflight() {
    log_phase "POST-FLIGHT VALIDATION"

    info "Running post-chaos load test (50k records)..."
    local post_response
    post_response=$(curl -s -X POST "${KATES_API}/api/tests" \
        -H "Content-Type: application/json" \
        -d '{"testType":"LOAD","numRecords":50000,"partitions":3}' 2>/dev/null || echo '{"id":"skip"}')

    local test_id
    test_id=$(echo "$post_response" | python3 -c "import sys,json; print(json.load(sys.stdin).get('id','skip'))" 2>/dev/null || echo "skip")

    if [ "$test_id" != "skip" ]; then
        info "Post-chaos test: ${test_id}"
        sleep 60
    fi

    info "Final cluster health check..."
    kubectl wait kafka/krafter --for=condition=Ready --timeout=30s -n ${NAMESPACE}
    echo "  ✅ Kafka cluster healthy after GameDay"

    info "Checking alerts..."
    local firing
    firing=$(curl -s "${PROMETHEUS_URL}/api/v1/alerts" 2>/dev/null | \
        python3 -c "import sys,json; alerts=json.load(sys.stdin).get('data',{}).get('alerts',[]); firing=[a for a in alerts if a['state']=='firing']; print(len(firing))" 2>/dev/null || echo "N/A")
    echo "  Firing alerts: ${firing}"

    echo "POSTFLIGHT=COMPLETE" >> "${RESULTS_FILE}.metrics"
}

phase_report() {
    log_phase "GAMEDAY REPORT"

    echo ""
    echo "┌──────────────────────────────────────────────────────────┐"
    echo "│                    GAMEDAY REPORT                        │"
    echo "├──────────────────────────────────────────────────────────┤"

    if [ -f "${RESULTS_FILE}.metrics" ]; then
        while IFS='=' read -r key value; do
            printf "│  %-30s │ %-23s │\n" "$key" "$value"
        done < "${RESULTS_FILE}.metrics"
    fi

    echo "├──────────────────────────────────────────────────────────┤"
    echo "│  Results saved to: ${RESULTS_FILE}.metrics"
    echo "└──────────────────────────────────────────────────────────┘"
    echo ""

    info "✅ GameDay complete!"
}

usage() {
    echo "Usage: $0 [options]"
    echo ""
    echo "Runs an automated GameDay validation pipeline:"
    echo "  1. Pre-flight checks (cluster, API, Prometheus)"
    echo "  2. Baseline performance test"
    echo "  3. Chaos injection (pod disruption)"
    echo "  4. Impact observation (metrics collection)"
    echo "  5. Recovery validation"
    echo "  6. Post-chaos performance test"
    echo "  7. Final report"
    echo ""
    echo "Options:"
    echo "  --experiment NAME   Chaos experiment to use (default: kafka-pod-delete)"
    echo "  --skip-baseline     Skip baseline performance test"
    echo "  --skip-chaos        Skip chaos injection (observation only)"
    echo "  --help              Show this help"
    echo ""
    echo "Environment:"
    echo "  KATES_API_URL       Kates API URL (default: http://localhost:30083)"
    echo "  PROMETHEUS_URL      Prometheus URL (default: http://localhost:30090)"
}

EXPERIMENT="kafka-pod-delete"
SKIP_BASELINE=false
SKIP_CHAOS=false

while [[ $# -gt 0 ]]; do
    case $1 in
        --experiment) EXPERIMENT="$2"; shift 2 ;;
        --skip-baseline) SKIP_BASELINE=true; shift ;;
        --skip-chaos) SKIP_CHAOS=true; shift ;;
        --help) usage; exit 0 ;;
        *) echo "Unknown option: $1"; usage; exit 1 ;;
    esac
done

echo ""
echo "╔══════════════════════════════════════════════════════════════╗"
echo "║          KATES AUTOMATED GAMEDAY VALIDATION                  ║"
echo "╠══════════════════════════════════════════════════════════════╣"
echo "║  Experiment:  ${EXPERIMENT}"
echo "║  API:         ${KATES_API}"
echo "║  Prometheus:  ${PROMETHEUS_URL}"
echo "║  Started:     $(date -u +%Y-%m-%dT%H:%M:%SZ)"
echo "╚══════════════════════════════════════════════════════════════╝"

phase_preflight

if [ "$SKIP_BASELINE" = false ]; then
    phase_baseline
fi

if [ "$SKIP_CHAOS" = false ]; then
    phase_chaos_inject "$EXPERIMENT"
    phase_chaos_observe
    phase_chaos_recover
fi

phase_postflight
phase_report

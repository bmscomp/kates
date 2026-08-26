#!/bin/bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "${SCRIPT_DIR}/common.sh"

echo "🔌 Starting Port Forwarding..."

# Namespaces this script manages. Kates moved to its own namespace when the
# chart started creating one; this script kept forwarding from 'kafka', found
# nothing, printed a skip line and exited 0 — so `kates health` answered
# "connection refused" with no hint that the forward had never started.
KAFKA_NS="${KAFKA_NS:-kafka}"
KATES_NS="${KATES_NS:-kates}"
MONITORING_NS="${MONITORING_NS:-${KAFKA_NS}}"

# Kill existing forwards for every namespace we manage, not just kafka —
# otherwise a stale kates forward keeps the local port and the new one dies.
for ns in "${KAFKA_NS}" "${KATES_NS}" "${MONITORING_NS}"; do
    pkill -f "kubectl port-forward.*-n ${ns}" 2>/dev/null || true
done
sleep 1

FORWARDED=0
SKIPPED=0

forward() {
    local label=$1
    local emoji=$2
    local svc=$3
    local local_port=$4
    local remote_port=$5
    local ns=$6

    if svc_exists "$svc" "$ns"; then
        echo "${emoji} Forwarding ${label}: http://localhost:${local_port}"
        kubectl port-forward "svc/${svc}" "${local_port}:${remote_port}" -n "${ns}" > /dev/null 2>&1 &
        FORWARDED=$((FORWARDED + 1))
    else
        warn "  ⏭️  ${label} not deployed in namespace '${ns}' — skipping"
        SKIPPED=$((SKIPPED + 1))
    fi
}

forward "Grafana"           "📊" monitoring-grafana                          30080 80   "${MONITORING_NS}"
forward "Kafka UI"          "🖥️ " kafka-ui                                   30081 8080 "${KAFKA_NS}"
forward "Apicurio Registry" "📚" apicurio-registry                          30082 8080 "${KAFKA_NS}"
forward "Kates API"         "🧪" kates                                      30083 8080 "${KATES_NS}"
forward "Prometheus"        "🔥" monitoring-kube-prometheus-prometheus       30090 9090 "${MONITORING_NS}"
forward "Jaeger UI"         "🔍" jaeger-query                               30086 16686 "${KAFKA_NS}"
forward "Litmus UI"         "⚡" chaos-litmus-frontend-service              9091  9091 "${KAFKA_NS}"
forward "Headlamp"          "🔭" headlamp                                   30084 80   "${KAFKA_NS}"

echo ""
if [ "${FORWARDED}" -eq 0 ]; then
    # Zero forwards is a failure, not a tidy summary: every command that follows
    # will report "connection refused" and look like the service is broken.
    error "❌ Port forwarding: nothing was forwarded (${SKIPPED} skipped)"
    echo "   Nothing is deployed in the namespaces above, or the names have changed."
    echo "   Check with:  kubectl get svc -A | grep -E 'kates|kafka'"
    exit 1
fi
info "✅ Port forwarding: ${FORWARDED} active, ${SKIPPED} skipped"
echo "Forwards run in background — use 'pkill -f kubectl.port-forward' to stop."

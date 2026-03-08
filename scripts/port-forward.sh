#!/bin/bash
set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "${SCRIPT_DIR}/common.sh"

echo "🔌 Starting Port Forwarding..."

# Kill existing port-forwards
pkill -f "kubectl port-forward" 2>/dev/null || true
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

forward "Grafana"           "📊" monitoring-grafana                          30080 80   monitoring
forward "Kafka UI"          "🖥️ " kafka-ui                                   30081 8080 kafka
forward "Apicurio Registry" "📚" apicurio-registry                          30082 8080 apicurio
forward "Kates API"         "🧪" kates                                      30083 8080 kates
forward "Prometheus"        "🔥" monitoring-kube-prometheus-prometheus       30090 9090 monitoring
forward "Jaeger UI"         "🔍" jaeger-query                               30086 16686 monitoring
forward "Litmus UI"         "⚡" chaos-litmus-frontend-service              9091  9091 litmus

echo ""
info "✅ Port forwarding: ${FORWARDED} active, ${SKIPPED} skipped"
echo "Forwards run in background — use 'pkill -f kubectl.port-forward' to stop."

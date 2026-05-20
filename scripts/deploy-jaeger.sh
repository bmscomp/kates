#!/bin/bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "${SCRIPT_DIR}/common.sh"
source "${SCRIPT_DIR}/../versions.env"

info "Deploying Jaeger (distributed tracing)..."

ensure_namespace kafka

if helm status jaeger -n kafka &>/dev/null && \
   kubectl rollout status deployment/jaeger -n kafka --timeout=5s &>/dev/null; then
    warn "Jaeger is already deployed and running — skipping"
    exit 0
fi

info "Adding Helm repository..."
helm repo add jaegertracing https://jaegertracing.github.io/helm-charts 2>/dev/null || true
helm repo update jaegertracing

VALUES_FILE="config/monitoring/jaeger-values.yaml"
OFFLINE_VALUES="config/monitoring/jaeger-values-offline.yaml"

HELM_ARGS=(
    upgrade --install jaeger jaegertracing/jaeger
    --version "${JAEGER_CHART_VERSION}"
    --namespace kafka
    --values "${VALUES_FILE}"
    --timeout 5m
)

if [ -f "${OFFLINE_VALUES}" ]; then
    HELM_ARGS+=(--values "${OFFLINE_VALUES}")
    info "Using offline values overlay"
fi

info "Installing Jaeger chart v${JAEGER_CHART_VERSION}..."
helm "${HELM_ARGS[@]}"

info "Patching health probes for Jaeger v2 (chart hardcodes v1 admin port 14269)..."
kubectl patch deployment jaeger -n kafka --type=json -p '[
  {"op": "replace", "path": "/spec/template/spec/containers/0/livenessProbe", "value": {"httpGet": {"path": "/", "port": 16686}, "initialDelaySeconds": 10, "periodSeconds": 15, "failureThreshold": 5}},
  {"op": "replace", "path": "/spec/template/spec/containers/0/readinessProbe", "value": {"httpGet": {"path": "/", "port": 16686}, "initialDelaySeconds": 5, "periodSeconds": 10, "failureThreshold": 3}}
]'

info "Verifying Jaeger is ready..."
kubectl rollout status deployment/jaeger -n kafka --timeout=120s || \
    warn "Jaeger pods may still be starting"

info "✅ Jaeger deployment complete!"
info "   UI:        http://localhost:30086"
info "   OTLP gRPC: jaeger-collector.kafka.svc:4317"
info "   OTLP HTTP: jaeger-collector.kafka.svc:4318"

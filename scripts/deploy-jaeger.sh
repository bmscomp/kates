#!/bin/bash
set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "${SCRIPT_DIR}/common.sh"
source "${SCRIPT_DIR}/../versions.env"

info "Deploying Jaeger (distributed tracing)..."

ensure_namespace monitoring

if helm status jaeger -n monitoring &>/dev/null; then
    warn "Jaeger is already deployed — upgrading"
fi

info "Adding Helm repository..."
helm repo add jaegertracing https://jaegertracing.github.io/helm-charts 2>/dev/null || true
helm repo update jaegertracing

VALUES_FILE="config/monitoring/jaeger-values.yaml"
OFFLINE_VALUES="config/monitoring/jaeger-values-offline.yaml"

HELM_ARGS=(
    upgrade --install jaeger jaegertracing/jaeger
    --version "${JAEGER_CHART_VERSION}"
    --namespace monitoring
    --values "${VALUES_FILE}"
    --timeout 5m
    --wait
)

if [ -f "${OFFLINE_VALUES}" ]; then
    HELM_ARGS+=(--values "${OFFLINE_VALUES}")
    info "Using offline values overlay"
fi

info "Installing Jaeger chart v${JAEGER_CHART_VERSION}..."
helm "${HELM_ARGS[@]}"

info "Verifying Jaeger is ready..."
kubectl wait --for=condition=available --timeout=120s deployment/jaeger -n monitoring 2>/dev/null || \
    kubectl wait --for=condition=Ready pod -l app.kubernetes.io/name=jaeger -n monitoring --timeout=120s 2>/dev/null || \
    warn "Jaeger pods may still be starting"

info "✅ Jaeger deployment complete!"
info "   UI:        http://localhost:30086"
info "   OTLP gRPC: jaeger-collector.monitoring.svc:4317"
info "   OTLP HTTP: jaeger-collector.monitoring.svc:4318"

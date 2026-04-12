#!/bin/bash
set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT_DIR="${SCRIPT_DIR}/.."
source "${SCRIPT_DIR}/common.sh"

ENV="${ENV:-kind}"
CHART_DIR="${ROOT_DIR}/charts/monitoring"
RELEASE_NAME="monitoring"
NAMESPACE="monitoring"

info "Deploying monitoring stack (env=${ENV})..."

# Build Helm dependencies (kube-prometheus-stack subchart)
info "Building Helm chart dependencies..."
helm dependency build "${CHART_DIR}" 2>/dev/null || true

# Select the values file for the environment
VALUES_FILE="${CHART_DIR}/values-${ENV}.yaml"
if [ ! -f "${VALUES_FILE}" ]; then
    VALUES_FILE="${CHART_DIR}/values-kind.yaml"
    warn "No values-${ENV}.yaml found, falling back to values-kind.yaml"
fi

info "Installing/upgrading monitoring stack with Helm..."
info "  Chart:       ${CHART_DIR}"
info "  Release:     ${RELEASE_NAME}"
info "  Namespace:   ${NAMESPACE}"
info "  Environment: ${ENV}"
info "  Values:      ${VALUES_FILE}"

helm upgrade --install "${RELEASE_NAME}" "${CHART_DIR}" \
    --namespace "${NAMESPACE}" --create-namespace \
    -f "${VALUES_FILE}" \
    --timeout 10m \
    --wait

info "Waiting for Grafana to be ready..."
kubectl wait --for=condition=Ready pods \
    -l "app.kubernetes.io/name=grafana" \
    -n "${NAMESPACE}" --timeout=120s || true

info "✅ Monitoring deployment complete (env=${ENV})!"
echo ""
echo "  Grafana:    http://localhost:30080 (admin/admin)"
echo "  Prometheus: http://localhost:30090"

#!/bin/bash
set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT_DIR="${SCRIPT_DIR}/.."
source "${SCRIPT_DIR}/common.sh"
source "${SCRIPT_DIR}/../versions.env"

ENV="${ENV:-kind}"
CHART_DIR="${ROOT_DIR}/charts/kafka-cluster"
RELEASE_NAME="kafka-cluster"
NAMESPACE="kafka"

info "Deploying Kafka cluster (env=${ENV})..."

ensure_namespace "${NAMESPACE}"

# Build Helm dependencies (Strimzi operator subchart)
info "Building Helm chart dependencies..."
helm dependency build "${CHART_DIR}" 2>/dev/null || true

# Kind-specific prerequisites: storage classes for zone-aware pools
if [ "${ENV}" = "kind" ]; then
    info "Applying Kind storage classes..."
    kubectl apply -f "${ROOT_DIR}/config/storage/storage-classes.yaml"
fi

# Build the values file chain based on environment
VALUES_ARGS=()
case "${ENV}" in
    kind)
        VALUES_ARGS+=(-f "${CHART_DIR}/values-dev.yaml" -f "${CHART_DIR}/values-kind.yaml")
        ;;
    dev)
        VALUES_ARGS+=(-f "${CHART_DIR}/values-dev.yaml")
        ;;
    staging)
        VALUES_ARGS+=(-f "${CHART_DIR}/values-staging.yaml")
        ;;
    prod)
        VALUES_ARGS+=(-f "${CHART_DIR}/values-prod.yaml")
        ;;
    *)
        if [ -f "${CHART_DIR}/values-${ENV}.yaml" ]; then
            VALUES_ARGS+=(-f "${CHART_DIR}/values-${ENV}.yaml")
        else
            error "Unknown environment '${ENV}' and no values-${ENV}.yaml found"
            exit 1
        fi
        ;;
esac

info "Installing/upgrading Kafka cluster with Helm..."
info "  Chart:       ${CHART_DIR}"
info "  Release:     ${RELEASE_NAME}"
info "  Namespace:   ${NAMESPACE}"
info "  Environment: ${ENV}"
info "  Values:      ${VALUES_ARGS[*]}"

helm upgrade --install "${RELEASE_NAME}" "${CHART_DIR}" \
    --namespace "${NAMESPACE}" \
    "${VALUES_ARGS[@]}" \
    --timeout 10m \
    --wait

info "Waiting for Kafka cluster to be ready..."
kubectl wait kafka/krafter --for=condition=Ready --timeout=300s -n "${NAMESPACE}" || {
    warn "Kafka not ready within timeout — check pod status:"
    kubectl get pods -n "${NAMESPACE}" -l strimzi.io/cluster=krafter
    exit 1
}

info "Waiting for user secrets to be created..."
kubectl wait kafkauser --all --for=condition=Ready --timeout=60s -n "${NAMESPACE}" 2>/dev/null || true

info "✅ Kafka deployment complete (env=${ENV})!"
echo ""
echo "  Run Helm tests:    helm test ${RELEASE_NAME} -n ${NAMESPACE}"
echo "  Check cluster:     kubectl get kafka -n ${NAMESPACE}"
echo "  Check pods:        kubectl get pods -n ${NAMESPACE}"

#!/bin/bash
set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT_DIR="${SCRIPT_DIR}/.."
source "${SCRIPT_DIR}/common.sh"

ENV="${ENV:-kind}"
CHART_DIR="${ROOT_DIR}/charts/kates"
RELEASE_NAME="kates"
NAMESPACE="kates"

info "Deploying Kates (env=${ENV})..."

ensure_namespace "${NAMESPACE}"

# Build Helm dependencies
info "Building Helm chart dependencies..."
helm dependency build "${CHART_DIR}" 2>/dev/null || true

# Copy Kafka SASL credentials from kafka namespace
if kubectl get secret kates-backend -n kafka &>/dev/null; then
    info "Copying Kafka SASL credentials to ${NAMESPACE}..."
    kubectl get secret kates-backend -n kafka -o json \
        | jq 'del(.metadata.namespace,.metadata.resourceVersion,.metadata.uid,.metadata.creationTimestamp,.metadata.annotations,.metadata.labels,.metadata.managedFields,.metadata.ownerReferences)' \
        | kubectl apply -n "${NAMESPACE}" -f -
else
    warn "Secret kates-backend not found in kafka namespace — Kafka auth may fail"
fi

# Kind-specific: ensure image is loaded
if [ "${ENV}" = "kind" ]; then
    KATES_IMAGE="${KATES_IMAGE:-kates:latest}"
    if docker image inspect "${KATES_IMAGE}" >/dev/null 2>&1; then
        info "Loading ${KATES_IMAGE} into Kind..."
        kind load docker-image "${KATES_IMAGE}" --name "${KIND_CLUSTER_NAME:-panda}" 2>/dev/null || true
    else
        warn "Image ${KATES_IMAGE} not found locally — will use existing image in cluster"
    fi
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

info "Installing/upgrading Kates with Helm..."
info "  Chart:       ${CHART_DIR}"
info "  Release:     ${RELEASE_NAME}"
info "  Namespace:   ${NAMESPACE}"
info "  Environment: ${ENV}"
info "  Values:      ${VALUES_ARGS[*]}"

helm upgrade --install "${RELEASE_NAME}" "${CHART_DIR}" \
    --namespace "${NAMESPACE}" \
    "${VALUES_ARGS[@]}" \
    --timeout 5m \
    --wait

info "✅ Kates deployment complete (env=${ENV})!"
echo ""
echo "  Run Helm tests:    helm test ${RELEASE_NAME} -n ${NAMESPACE}"
echo "  Check pods:        kubectl get pods -n ${NAMESPACE}"
echo "  API health:        kubectl port-forward svc/kates 8080:8080 -n ${NAMESPACE}"
echo "                     curl http://localhost:8080/api/health"

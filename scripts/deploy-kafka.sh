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

# Adopt pre-existing KafkaTopics/KafkaUsers into this Helm release.
# These may have been created by kubectl apply (client-side) in a prior run,
# which sets a different field manager. Without adoption, Helm's server-side
# apply will fail with field ownership conflicts on managed labels.
info "Adopting existing Kafka resources into Helm release..."
for kind in kafkatopics kafkausers; do
    for resource in $(kubectl get "${kind}" -n "${NAMESPACE}" -o name 2>/dev/null); do
        kubectl annotate "${resource}" -n "${NAMESPACE}" \
            meta.helm.sh/release-name="${RELEASE_NAME}" \
            meta.helm.sh/release-namespace="${NAMESPACE}" \
            --overwrite 2>/dev/null || true
        kubectl label "${resource}" -n "${NAMESPACE}" \
            app.kubernetes.io/managed-by=Helm \
            --overwrite 2>/dev/null || true
        # Transfer field ownership from kubectl-client-side-apply to Helm
        kubectl get "${resource}" -n "${NAMESPACE}" -o yaml 2>/dev/null \
            | kubectl apply --server-side --force-conflicts --field-manager=helm -f - 2>/dev/null || true
    done
done

helm upgrade --install "${RELEASE_NAME}" "${CHART_DIR}" \
    --namespace "${NAMESPACE}" \
    "${VALUES_ARGS[@]}" \
    --timeout 5m

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

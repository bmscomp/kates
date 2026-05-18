#!/bin/bash
set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT_DIR="${SCRIPT_DIR}/.."
source "${SCRIPT_DIR}/common.sh"

ENV="${ENV:-kind}"
CHART_DIR="${ROOT_DIR}/charts/kates"
RELEASE_NAME="kates"
NAMESPACE="${NAMESPACE:-kafka}"
CLUSTER_NAME="${CLUSTER_NAME:-krafter}"

info "Deploying Kates (env=${ENV})..."

ensure_namespace "${NAMESPACE}"

# Auto-detect Kafka cluster name if not explicitly provided (and fallback to krafter)
DETECTED_CLUSTER=$(kubectl get kafka -n "${NAMESPACE}" -o custom-columns=NAME:.metadata.name --no-headers 2>/dev/null | head -n1 || true)
if [ -n "${DETECTED_CLUSTER}" ] && [ "${DETECTED_CLUSTER}" != "${CLUSTER_NAME}" ]; then
    CLUSTER_NAME="${DETECTED_CLUSTER}"
    info "Auto-detected Kafka cluster: ${CLUSTER_NAME}"
fi

# Build Helm dependencies
info "Building Helm chart dependencies..."
helm dependency build "${CHART_DIR}" 2>/dev/null || true

# Ensure KafkaUser exists before attempting to copy secret
"${SCRIPT_DIR}/ensure-kafka-user.sh" || warn "Could not ensure KafkaUser — copying secret may fail"

# Copy Kafka SASL credentials if namespaces differ
if [ "${NAMESPACE}" != "kafka" ]; then
    if kubectl get secret kates-backend -n kafka &>/dev/null; then
        info "Copying Kafka SASL credentials to ${NAMESPACE}..."
        kubectl get secret kates-backend -n kafka -o json \
            | jq 'del(.metadata.namespace,.metadata.resourceVersion,.metadata.uid,.metadata.creationTimestamp,.metadata.annotations,.metadata.labels,.metadata.managedFields,.metadata.ownerReferences)' \
            | kubectl apply -n "${NAMESPACE}" -f -
    else
        warn "Secret kates-backend not found in kafka namespace — Kafka auth may fail"
    fi
fi

# Kind-specific: ensure released image is available in the cluster
if [ "${ENV}" = "kind" ]; then
    KATES_IMAGE="${KATES_IMAGE:-ghcr.io/bmscomp/kates:1.11.0}"
    info "Ensuring ${KATES_IMAGE} is available in Kind..."
    if ! docker image inspect "${KATES_IMAGE}" >/dev/null 2>&1; then
        info "Pulling ${KATES_IMAGE} from registry..."
        docker pull "${KATES_IMAGE}"
    fi
    kind load docker-image "${KATES_IMAGE}" --name "${KIND_CLUSTER_NAME:-panda}" 2>/dev/null || true
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

# Customize the Kates backend connection URL based on cluster config
KAFKA_BOOTSTRAP="${CLUSTER_NAME}-kafka-bootstrap.${NAMESPACE}.svc:9092"
info "  Bootstrap:   ${KAFKA_BOOTSTRAP}"

helm upgrade --install "${RELEASE_NAME}" "${CHART_DIR}" \
    --namespace "${NAMESPACE}" \
    "${VALUES_ARGS[@]}" \
    --set kafka.bootstrapServers="${KAFKA_BOOTSTRAP}" \
    --timeout 5m


info "✅ Kates deployment complete (env=${ENV})!"
echo ""
echo "  Run Helm tests:    helm test ${RELEASE_NAME} -n ${NAMESPACE}"
echo "  Check pods:        kubectl get pods -n ${NAMESPACE}"
echo "  API health:        kubectl port-forward svc/kates 8080:8080 -n ${NAMESPACE}"
echo "                     curl http://localhost:8080/api/health"

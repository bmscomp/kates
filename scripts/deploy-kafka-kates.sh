#!/bin/bash
set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT_DIR="${SCRIPT_DIR}/.."
source "${SCRIPT_DIR}/common.sh"

CHART_DIR="${ROOT_DIR}/charts/kafka-cluster"
RELEASE_NAME="kafka-cluster"
NAMESPACE="kafka"
DETECTED_VALUES="${ROOT_DIR}/.build/values-detected-kates.yaml"

info "Deploying Kafka cluster using Kates generated values..."

ensure_namespace "${NAMESPACE}"

# 1. Detect cluster and generate values
mkdir -p "${ROOT_DIR}/.build"
info "Detecting cluster configuration using kates CLI..."
kates detect --generate-values --values-output "${DETECTED_VALUES}"

# 2. Build dependencies
info "Building Helm chart dependencies..."
helm dependency build "${CHART_DIR}" 2>/dev/null || true

# 3. Adopt existing resources
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
        kubectl get "${resource}" -n "${NAMESPACE}" -o yaml 2>/dev/null \
            | kubectl apply --server-side --force-conflicts --field-manager=helm -f - 2>/dev/null || true
    done
done

# 4. Deploy using Helm, overriding strimziOperator.enabled=false since operator is already on cluster
info "Installing/upgrading Kafka cluster with Helm..."
info "  Release:    ${RELEASE_NAME}"
info "  Namespace:  ${NAMESPACE}"
info "  Values:     ${DETECTED_VALUES}"

helm upgrade --install "${RELEASE_NAME}" "${CHART_DIR}" \
    --namespace "${NAMESPACE}" \
    -f "${DETECTED_VALUES}" \
    --set strimziOperator.enabled=false \
    --timeout 10m

# 5. Wait for cluster
info "Waiting for Kafka cluster to be ready..."
kubectl wait kafka/krafter --for=condition=Ready --timeout=600s -n "${NAMESPACE}" || {
    warn "Kafka not ready within 10 min — checking pod status:"
    kubectl get pods -n "${NAMESPACE}" -l strimzi.io/cluster=krafter
    warn "Operator log tail:"
    kubectl logs -n "${NAMESPACE}" -l strimzi.io/kind=cluster-operator --tail=10 2>/dev/null || true
    
    RUNNING=$(kubectl get pods -n "${NAMESPACE}" -l strimzi.io/cluster=krafter \
        --field-selector=status.phase=Running --no-headers 2>/dev/null | wc -l | tr -d ' ')
    TOTAL=$(kubectl get pods -n "${NAMESPACE}" -l strimzi.io/cluster=krafter \
        --no-headers 2>/dev/null | wc -l | tr -d ' ')
    
    if [ "${RUNNING}" -eq "${TOTAL}" ] && [ "${TOTAL}" -gt 0 ]; then
        warn "All ${TOTAL} pods Running — operator still reconciling in background. Continuing."
    else
        error "Only ${RUNNING}/${TOTAL} pods Running. Aborting."
        exit 1
    fi
}

info "Waiting for user secrets to be created..."
kubectl wait kafkauser --all --for=condition=Ready --timeout=60s -n "${NAMESPACE}" 2>/dev/null || true

info "✅ Kafka deployment complete using Kates detected values!"
echo ""
echo "  Run Helm tests:    helm test ${RELEASE_NAME} -n ${NAMESPACE}"
echo "  Check cluster:     kubectl get kafka -n ${NAMESPACE}"
echo "  Check pods:        kubectl get pods -n ${NAMESPACE}"

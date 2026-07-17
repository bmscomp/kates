#!/bin/bash
# deploy-kafka-generic.sh — Deploy Kafka to any Kubernetes cluster
#
# Complete pipeline: detect → review → deploy → wait → verify
#
# Usage:
#   ./scripts/deploy-kafka-generic.sh               # interactive (prompts before deploy)
#   ./scripts/deploy-kafka-generic.sh --yes          # non-interactive (auto-approve)
#   ./scripts/deploy-kafka-generic.sh -f extra.yaml  # merge additional values overlay
#   ./scripts/deploy-kafka-generic.sh --skip-tests   # skip Helm test after deploy

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT_DIR="${SCRIPT_DIR}/.."
source "${SCRIPT_DIR}/common.sh"

CHART_DIR="${ROOT_DIR}/charts/kafka-cluster"
RELEASE_NAME="kafka-cluster"
NAMESPACE="kafka"
DETECTED_VALUES="${ROOT_DIR}/.build/values-detected.yaml"
AUTO_OVERLAY=""

# Parse arguments
AUTO_APPROVE=false
EXTRA_VALUES=""
SKIP_TESTS=false
RUN_TESTS=false
while [[ $# -gt 0 ]]; do
    case "$1" in
        --yes|-y)       AUTO_APPROVE=true; shift ;;
        --values|-f)    EXTRA_VALUES="$2"; shift 2 ;;
        --skip-tests)   SKIP_TESTS=true; shift ;;
        --run-tests)    RUN_TESTS=true; SKIP_TESTS=false; shift ;;
        -h|--help)
            echo "Usage: $0 [--yes] [--values extra.yaml] [--skip-tests] [--run-tests]"
            echo ""
            echo "Deploy Kafka to any Kubernetes cluster using auto-detected configuration."
            echo ""
            echo "Options:"
            echo "  -y, --yes          Skip review prompt (non-interactive)"
            echo "  -f, --values FILE  Merge additional values overlay on top of detected values"
            echo "  --skip-tests       Skip Helm test after deployment"
            echo "  --run-tests        Force Helm tests even when auto-skip would apply (e.g. Kind)"
            echo "  -h, --help         Show this help"
            exit 0
            ;;
        *) echo "Unknown option: $1"; exit 1 ;;
    esac
done

SECONDS=0

# ── Step 1: Detect ────────────────────────────────────────────────────────────
bold ""
bold "═══════════════════════════════════════════════════════════════════════════"
bold "  Kafka Cluster Deployment — Generic Kubernetes"
bold "═══════════════════════════════════════════════════════════════════════════"
echo ""

info "Step 1/6: Detecting cluster configuration..."
if command -v kates &> /dev/null; then
    info "  Using kates CLI for cluster detection"
    kates detect --generate-values --values-output "${DETECTED_VALUES}" --quiet
elif [ -x "${ROOT_DIR}/build/kates" ]; then
    info "  Using local kates binary for cluster detection"
    "${ROOT_DIR}/build/kates" detect --generate-values --values-output "${DETECTED_VALUES}" --quiet
else
    info "  kates binary not found. Building it now..."
    cd "${ROOT_DIR}" && make cli-build >/dev/null
    "${ROOT_DIR}/cli/dist/kates" detect --generate-values --values-output "${DETECTED_VALUES}" --quiet
fi

# ── Step 2: Review ────────────────────────────────────────────────────────────
echo ""
info "Step 2/6: Review generated configuration"
echo ""
bold "Generated values: ${DETECTED_VALUES}"
echo "─────────────────────────────────────────────"
cat "${DETECTED_VALUES}"
echo "─────────────────────────────────────────────"

if [ -n "${EXTRA_VALUES}" ]; then
    if [ -f "${EXTRA_VALUES}" ]; then
        info "Additional overlay: ${EXTRA_VALUES}"
    else
        error "Extra values file not found: ${EXTRA_VALUES}"
        exit 1
    fi
fi

# Resolve environment/provider overlay automatically.
# Priority: explicit ENV (from Makefile) -> detected provider.
TARGET_ENV="${ENV:-}"
if [ -z "${TARGET_ENV}" ]; then
    TARGET_ENV=$(awk '/^# Provider:/{print $3; exit}' "${DETECTED_VALUES}" 2>/dev/null || true)
fi

if [ -n "${TARGET_ENV}" ]; then
    if [ -f "${CHART_DIR}/values-${TARGET_ENV}.yaml" ]; then
        AUTO_OVERLAY="${CHART_DIR}/values-${TARGET_ENV}.yaml"
    elif [ "${TARGET_ENV}" = "kind" ] && [ -f "${CHART_DIR}/values-kind.yaml" ]; then
        AUTO_OVERLAY="${CHART_DIR}/values-kind.yaml"
    elif [ "${TARGET_ENV}" = "generic" ] && [ -f "${CHART_DIR}/values-generic.yaml" ]; then
        AUTO_OVERLAY="${CHART_DIR}/values-generic.yaml"
    fi
fi

if [ -n "${AUTO_OVERLAY}" ]; then
    info "Auto overlay: ${AUTO_OVERLAY} (env/provider=${TARGET_ENV})"
fi

# In many corporate Kind setups, Helm test images from GHCR fail certificate
# validation. Keep deployment green by skipping tests by default on Kind unless
# explicitly forced with --run-tests.
if [ "${TARGET_ENV}" = "kind" ] && [ "${SKIP_TESTS}" != true ] && [ "${RUN_TESTS}" != true ]; then
    warn "Kind environment detected — skipping Helm tests by default"
    warn "Use --run-tests to force Helm tests on Kind"
    SKIP_TESTS=true
fi

if [ "${AUTO_APPROVE}" != true ]; then
    echo ""
    warn "Review the configuration above."
    echo -n "  Press Enter to deploy, or Ctrl+C to abort... "
    read -r
fi

# ── Step 3: Dependencies ─────────────────────────────────────────────────────
echo ""
info "Step 3/6: Building Helm chart dependencies..."
helm dependency build "${CHART_DIR}" 2>/dev/null || true

# Extract cluster domain from the Kubernetes environment
CLUSTER_DOMAIN=$(get_cluster_domain "$AUTO_APPROVE")
info "  Cluster Domain: ${CLUSTER_DOMAIN}"

# ── Step 4: Deploy ────────────────────────────────────────────────────────────
echo ""
info "Step 4/6: Deploying Kafka cluster..."

# Ensure namespace exists
kubectl create namespace "${NAMESPACE}" --dry-run=client -o yaml | kubectl apply -f - > /dev/null 2>&1

# Install Strimzi Operator if not present (separate release required to avoid CRD chicken-and-egg)
# Skip if the detect output already confirmed Strimzi is running
if grep -A1 'strimziOperator:' "${DETECTED_VALUES}" 2>/dev/null | grep -q 'enabled: false'; then
    info "  Strimzi Operator already managed by pipeline — skipping"
else
    # Check if Strimzi CRD is missing OR if the operator deployment is missing
    operator_exists=false
    if kubectl get deployment -A -o custom-columns=NAME:.metadata.name 2>/dev/null | grep -q "strimzi-cluster-operator"; then
        operator_exists=true
    elif kubectl get deployment -n kafka -o custom-columns=NAME:.metadata.name 2>/dev/null | grep -q "strimzi-cluster-operator"; then
        operator_exists=true
    elif kubectl get deployment -n strimzi-operator -o custom-columns=NAME:.metadata.name 2>/dev/null | grep -q "strimzi-cluster-operator"; then
        operator_exists=true
    fi

    if ! kubectl get crd kafkas.kafka.strimzi.io &>/dev/null || [ "$operator_exists" = false ]; then
        if [ "$operator_exists" = false ] && kubectl get crd kafkas.kafka.strimzi.io &>/dev/null; then
            warn "  Strimzi CRDs are present, but Strimzi Operator deployment is not running!"
            info "  Installing Strimzi Kafka Operator to manage existing CRDs..."
        else
            info "  Strimzi CRDs not found. Installing Strimzi Kafka Operator in strimzi-operator namespace..."
        fi
        kubectl create namespace "strimzi-operator" --dry-run=client -o yaml | kubectl apply -f - > /dev/null 2>&1
        helm upgrade --install strimzi-operator oci://quay.io/strimzi-helm/strimzi-kafka-operator \
            --version 1.1.0 \
            --namespace "strimzi-operator" \
            --set watchAnyNamespace=true \
            --set replicas=1 \
            --set kubernetesServiceDnsDomain="${CLUSTER_DOMAIN}" \
            --timeout 5m --wait
        kubectl wait --for=condition=Established crd kafkas.kafka.strimzi.io --timeout=60s
    else
        info "  Strimzi CRDs and Operator deployment already present"
    fi
fi

# Adopt pre-existing Kafka resources into Helm release
info "  Adopting existing resources into Helm release..."
for kind in kafkatopics kafkausers; do
    for resource in $(kubectl get "${kind}" -n "${NAMESPACE}" -o name 2>/dev/null); do
        kubectl annotate "${resource}" -n "${NAMESPACE}" \
            meta.helm.sh/release-name="${RELEASE_NAME}" \
            meta.helm.sh/release-namespace="${NAMESPACE}" \
            --overwrite 2>/dev/null || true
        kubectl label "${resource}" -n "${NAMESPACE}" \
            app.kubernetes.io/managed-by=Helm \
            --overwrite 2>/dev/null || true
    done
done

# Build values chain
VALUES_ARGS=(-f "${DETECTED_VALUES}")
[ -n "${AUTO_OVERLAY}" ] && VALUES_ARGS+=(-f "${AUTO_OVERLAY}")
[ -n "${EXTRA_VALUES}" ] && VALUES_ARGS+=(-f "${EXTRA_VALUES}")

info "  Release:    ${RELEASE_NAME}"
info "  Namespace:  ${NAMESPACE}"
info "  Values:     ${VALUES_ARGS[*]}"
echo ""

helm upgrade --install "${RELEASE_NAME}" "${CHART_DIR}" \
    --namespace "${NAMESPACE}" \
    "${VALUES_ARGS[@]}" \
    --set global.clusterDomain="${CLUSTER_DOMAIN}" \
    --timeout 10m

# ── Step 5: Wait ──────────────────────────────────────────────────────────────
echo ""
info "Step 5/6: Waiting for Kafka cluster to be ready..."
info "  (First deployment performs a KRaft voter-format upgrade — allow up to 10 min)"

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

info "Waiting for user secrets..."
kubectl wait kafkauser --all --for=condition=Ready --timeout=60s -n "${NAMESPACE}" 2>/dev/null || true

# ── Step 6: Verify ────────────────────────────────────────────────────────────
echo ""
if [ "${SKIP_TESTS}" != true ]; then
    info "Step 6/6: Running Helm tests..."
    helm test "${RELEASE_NAME}" -n "${NAMESPACE}" --timeout 5m || {
        warn "Some Helm tests failed — check results above"
    }
else
    info "Step 6/6: Helm tests skipped (--skip-tests)"
fi

# ── Done ──────────────────────────────────────────────────────────────────────
echo ""
bold "═══════════════════════════════════════════════════════════════════════════"
info "✅ Kafka cluster deployed successfully in $(elapsed)"
bold "═══════════════════════════════════════════════════════════════════════════"
echo ""
echo "  Cluster:       kubectl get kafka -n ${NAMESPACE}"
echo "  Pods:          kubectl get pods -n ${NAMESPACE}"
echo "  Node pools:    kubectl get kafkanodepools -n ${NAMESPACE}"
echo "  Topics:        kubectl get kafkatopics -n ${NAMESPACE}"
echo "  Users:         kubectl get kafkausers -n ${NAMESPACE}"
echo "  Helm tests:    helm test ${RELEASE_NAME} -n ${NAMESPACE}"
echo "  Generated:     ${DETECTED_VALUES}"
echo ""

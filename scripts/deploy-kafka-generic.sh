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

# Parse arguments
AUTO_APPROVE=false
EXTRA_VALUES=""
SKIP_TESTS=false
while [[ $# -gt 0 ]]; do
    case "$1" in
        --yes|-y)       AUTO_APPROVE=true; shift ;;
        --values|-f)    EXTRA_VALUES="$2"; shift 2 ;;
        --skip-tests)   SKIP_TESTS=true; shift ;;
        -h|--help)
            echo "Usage: $0 [--yes] [--values extra.yaml] [--skip-tests]"
            echo ""
            echo "Deploy Kafka to any Kubernetes cluster using auto-detected configuration."
            echo ""
            echo "Options:"
            echo "  -y, --yes          Skip review prompt (non-interactive)"
            echo "  -f, --values FILE  Merge additional values overlay on top of detected values"
            echo "  --skip-tests       Skip Helm test after deployment"
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
"${SCRIPT_DIR}/detect-cluster-config.sh" -o "${DETECTED_VALUES}"

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

# ── Step 4: Deploy ────────────────────────────────────────────────────────────
echo ""
info "Step 4/6: Deploying Kafka cluster..."

# Ensure namespace exists
kubectl create namespace "${NAMESPACE}" --dry-run=client -o yaml | kubectl apply -f - > /dev/null 2>&1

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
[ -n "${EXTRA_VALUES}" ] && VALUES_ARGS+=(-f "${EXTRA_VALUES}")

info "  Release:    ${RELEASE_NAME}"
info "  Namespace:  ${NAMESPACE}"
info "  Values:     ${VALUES_ARGS[*]}"
echo ""

helm upgrade --install "${RELEASE_NAME}" "${CHART_DIR}" \
    --namespace "${NAMESPACE}" \
    "${VALUES_ARGS[@]}" \
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

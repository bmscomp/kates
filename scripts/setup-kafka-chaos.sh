#!/bin/bash
set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "${SCRIPT_DIR}/common.sh"
source "${SCRIPT_DIR}/../versions.env"

EXPERIMENTS_DIR="config/litmus/experiments"
RBAC_FILE="config/litmus/kafka-rbac.yaml"

bold "Setting up Kafka Chaos Testing Environment"
echo ""

# Step 1: Verify prerequisites
step "Step 1: Verifying cluster state..."
if ! kubectl get kafka krafter -n kafka &>/dev/null; then
    error "Kafka cluster 'krafter' not found in namespace 'kafka'"
    echo "Please deploy Kafka first: make kafka"
    exit 1
fi
if ! kubectl get pods -n litmus -l app.kubernetes.io/component=litmus-server --field-selector=status.phase=Running &>/dev/null; then
    error "LitmusChaos portal not running"
    echo "Please deploy LitmusChaos first: make litmus"
    exit 1
fi
info "✓ Kafka cluster and Litmus portal are running"

# Step 2: Register chaos infrastructure
echo ""
step "Step 2: Registering chaos infrastructure..."

INFRA_MANIFEST="config/litmus/chaos-litmus-chaos-enable.yml"
if [ ! -f "${INFRA_MANIFEST}" ]; then
    error "Infrastructure manifest not found at ${INFRA_MANIFEST}"
    echo ""
    echo "To generate it:"
    echo "  1. Run: make chaos-ui"
    echo "  2. Open http://localhost:9091 (admin/litmus)"
    echo "  3. Go to Environments → Create → 'kafka-chaos'"
    echo "  4. Add Infrastructure → copy the manifest"
    echo "  5. Save to ${INFRA_MANIFEST}"
    exit 1
fi

info "Patching manifest image references (Litmus v${LITMUS_VERSION})..."
PATCHED_MANIFEST=$(mktemp)
sed -e "s|litmuschaos.docker.scarf.sh/litmuschaos/chaos-operator:3.23.0|litmuschaos/chaos-operator:${LITMUS_VERSION}|g" \
    -e "s|litmuschaos.docker.scarf.sh/litmuschaos/chaos-runner:3.23.0|litmuschaos/chaos-runner:${LITMUS_VERSION}|g" \
    -e "s|litmuschaos.docker.scarf.sh/litmuschaos/chaos-exporter:3.23.0|litmuschaos/chaos-exporter:${LITMUS_VERSION}|g" \
    -e "s|litmuschaos.docker.scarf.sh/litmuschaos/litmusportal-subscriber:3.23.0|litmuschaos/litmusportal-subscriber:${LITMUS_VERSION}|g" \
    -e "s|litmuschaos.docker.scarf.sh/litmuschaos/litmusportal-event-tracker:3.23.0|litmuschaos/litmusportal-event-tracker:${LITMUS_VERSION}|g" \
    -e "s|litmuschaos/litmusportal-subscriber:3.23.0|litmuschaos/litmusportal-subscriber:${LITMUS_VERSION}|g" \
    -e "s|litmuschaos/litmusportal-event-tracker:3.23.0|litmuschaos/litmusportal-event-tracker:${LITMUS_VERSION}|g" \
    "${INFRA_MANIFEST}" > "${PATCHED_MANIFEST}"

info "Applying infrastructure manifest..."
kubectl apply -f "${PATCHED_MANIFEST}" 2>&1 | grep -v "^Warning:" || true
rm -f "${PATCHED_MANIFEST}"

info "Waiting for chaos infrastructure to be ready..."
kubectl wait --for=condition=available deployment/chaos-operator-ce -n litmus --timeout=120s 2>/dev/null || true
kubectl wait --for=condition=available deployment/workflow-controller -n litmus --timeout=120s 2>/dev/null || true
kubectl wait --for=condition=available deployment/subscriber -n litmus --timeout=120s 2>/dev/null || true
info "✓ Chaos infrastructure registered"

# Step 3: Install ChaosExperiments for Kafka
echo ""
step "Step 3: Installing chaos experiment CRDs from ${EXPERIMENTS_DIR}/..."

EXPERIMENT_COUNT=0
for exp_file in "${EXPERIMENTS_DIR}"/*.yaml; do
    [ -f "${exp_file}" ] || continue
    exp_name=$(basename "${exp_file}" .yaml)
    info "  Installing: ${exp_name}"
    sed "s|IMAGE_VERSION|${LITMUS_VERSION}|g" "${exp_file}" | kubectl apply -f -
    EXPERIMENT_COUNT=$((EXPERIMENT_COUNT + 1))
done
info "✓ ${EXPERIMENT_COUNT} chaos experiments installed"

# Step 4: Create RBAC for chaos in kafka namespace
echo ""
step "Step 4: Setting up RBAC for kafka namespace..."

kubectl apply -f "${RBAC_FILE}"
info "✓ RBAC configured for kafka namespace"

echo ""
info "✅ Kafka Chaos Environment Ready!"
echo ""
echo "Run chaos experiments:"
echo "  make chaos-kafka-pod-delete            # Kill random broker pod"
echo "  make chaos-kafka-network-partition      # Isolate broker from cluster"
echo "  make chaos-kafka-cpu-stress             # CPU pressure on broker"
echo "  make chaos-kafka-memory-stress          # Memory pressure on broker"
echo "  make chaos-kafka-io-stress              # Disk I/O stress on broker"
echo "  make chaos-kafka-dns-error              # DNS resolution failures"
echo "  make chaos-kafka-node-drain             # Drain node hosting broker"
echo "  make chaos-kafka-all                    # Run all experiments"
echo ""
echo "Monitor: kubectl get chaosresults -n kafka"

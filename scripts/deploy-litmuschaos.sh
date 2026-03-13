#!/bin/bash
set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "${SCRIPT_DIR}/common.sh"

LITMUS_CHART_DIR="${CHARTS_DIR}/litmus"

info "Deploying LitmusChaos..."

require_chart "${LITMUS_CHART_DIR}" "litmus"

# Skip if already running
if deployment_exists chaos-litmus-server litmus; then
    if kubectl rollout status deployment/chaos-litmus-server -n litmus --timeout=5s &>/dev/null; then
        warn "LitmusChaos portal is already deployed and running — skipping"
        exit 0
    fi
fi

ensure_namespace litmus

# Step 1: Deploy MongoDB first (disable other components)
step "Step 1: Deploying MongoDB first..."
helm upgrade --install chaos "${LITMUS_CHART_DIR}" \
  --namespace litmus \
  --values config/litmus/litmus-values.yaml \
  --set portal.frontend.replicas=0 \
  --set portal.server.replicas=0 \
  --set portal.server.authServer.replicas=0 \
  --timeout 10m \
  --wait

# Wait for MongoDB to be fully ready
info "Waiting for MongoDB to be ready (this may take a few minutes)..."
if kubectl get statefulset chaos-mongodb -n litmus &>/dev/null; then
    kubectl rollout status statefulset/chaos-mongodb -n litmus --timeout=600s
elif kubectl get deployment chaos-mongodb -n litmus &>/dev/null; then
    kubectl rollout status deployment/chaos-mongodb -n litmus --timeout=600s
else
    warn "Waiting for MongoDB pods to be ready..."
    kubectl wait --for=condition=Ready pod -l app.kubernetes.io/component=mongodb -n litmus --timeout=600s
fi

info "Waiting additional 15s for MongoDB to stabilize..."
sleep 15

# Step 2: Deploy remaining Litmus components
step "Step 2: Deploying Litmus portal components..."
helm upgrade --install chaos "${LITMUS_CHART_DIR}" \
  --namespace litmus \
  --values config/litmus/litmus-values.yaml \
  --timeout 10m \
  --wait

info "Waiting for LitmusChaos portal to be ready..."
kubectl wait --for=condition=available --timeout=300s \
  deployment/chaos-litmus-server -n litmus

# Create ServiceMonitor for Prometheus integration
info "Creating ServiceMonitor for Prometheus integration..."
cat <<EOF | kubectl apply -f -
apiVersion: monitoring.coreos.com/v1
kind: ServiceMonitor
metadata:
  name: litmus-chaos-exporter
  namespace: litmus
  labels:
    app: chaos-exporter
    release: monitoring
spec:
  selector:
    matchLabels:
      app: chaos-exporter
  namespaceSelector:
    matchNames:
      - litmus
  endpoints:
  - port: tcp-metrics
    path: /metrics
    interval: 30s
EOF

# Step 3: Install Litmus CRDs (ChaosEngine, ChaosResult, ChaosExperiment)
step "Step 3: Installing Litmus chaos CRDs..."
ROOT_DIR="$(cd "${SCRIPT_DIR}/.." && pwd)"
kubectl apply -f "${ROOT_DIR}/config/litmus/chaos-litmus-chaos-enable.yml" 2>/dev/null || true
kubectl apply -f "${ROOT_DIR}/config/litmus/kafka-litmus-chaos-enable.yml" 2>/dev/null || true

info "Waiting for CRDs to be established..."
kubectl wait --for=condition=Established crd/chaosengines.litmuschaos.io --timeout=60s 2>/dev/null || true
kubectl wait --for=condition=Established crd/chaosresults.litmuschaos.io --timeout=60s 2>/dev/null || true
kubectl wait --for=condition=Established crd/chaosexperiments.litmuschaos.io --timeout=60s 2>/dev/null || true

# Step 4: Apply chaos RBAC for Kates and Kafka namespaces
step "Step 4: Applying chaos RBAC..."
kubectl apply -f "${ROOT_DIR}/config/litmus/kates-chaos-rbac.yaml"
kubectl apply -f "${ROOT_DIR}/config/litmus/kafka-rbac.yaml"

# Step 5: Deploy chaos experiment definitions (blueprints only, no auto-triggered engines)
step "Step 5: Deploying chaos experiment definitions..."
for f in "${ROOT_DIR}"/config/litmus/experiments/*.yaml; do
    if grep -q "kind: ChaosExperiment" "$f" && ! grep -q "kind: ChaosEngine" "$f"; then
        kubectl apply -f "$f" 2>/dev/null || true
    fi
done

# Step 6: Pre-load Litmus runner images into Kind (offline environments)
step "Step 6: Loading Litmus experiment images into Kind..."
LITMUS_IMAGES=(
    "litmuschaos/go-runner:latest"
    "litmuschaos/chaos-operator-ce:latest"
    "litmuschaos/chaos-exporter:latest"
)
for img in "${LITMUS_IMAGES[@]}"; do
    if docker image inspect "$img" &>/dev/null; then
        kind load docker-image "$img" --name panda 2>/dev/null || true
    else
        info "Image $img not found locally — pulling..."
        docker pull "$img" 2>/dev/null && kind load docker-image "$img" --name panda 2>/dev/null || \
            warn "Could not load $img — chaos experiments may fail with ImagePullBackOff"
    fi
done

echo ""
info "✅ LitmusChaos deployment complete!"
echo ""
echo "Chaos infrastructure:"
echo "  ✓ Litmus portal (Helm chart)"
echo "  ✓ CRDs installed (ChaosEngine, ChaosResult, ChaosExperiment)"
echo "  ✓ RBAC applied (kates + kafka namespaces)"
echo "  ✓ Chaos experiments deployed"
echo ""
echo "Access UI: make chaos-ui → http://localhost:9091 (admin/litmus)"

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
  --values config/litmus-values.yaml \
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
  --values config/litmus-values.yaml \
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

echo ""
info "✅ LitmusChaos deployment complete!"
echo ""
echo "Next steps:"
echo "  1. Deploy experiments: kubectl apply -f config/litmus/experiments/"
echo "  2. Access UI: make chaos-ui → http://localhost:9091 (admin/litmus)"

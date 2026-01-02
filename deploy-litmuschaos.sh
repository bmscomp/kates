#!/bin/bash
set -e

# Colors for output
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

CHARTS_DIR="./charts"
LITMUS_CHART_DIR="${CHARTS_DIR}/litmus"

echo -e "${GREEN}Deploying LitmusChaos from local chart...${NC}"

# Check if local chart exists
if [ ! -d "${LITMUS_CHART_DIR}" ]; then
    echo -e "${YELLOW}Error: Litmus chart not found at ${LITMUS_CHART_DIR}${NC}"
    echo "Please run ./download-litmus-charts.sh first to download the charts"
    exit 1
fi

# Detect Apple Silicon (arm64) hosts which need x86_64 Litmus images
ARCH=$(uname -m)
if [[ "${ARCH}" == "arm64" && -z "${SKIP_APPLE_SILICON_LITMUS_FIX}" ]]; then
  echo -e "${GREEN}Apple Silicon detected (arm64). Ensuring linux/amd64 Litmus images are loaded...${NC}"
  ./force-platform-load.sh
  echo -e "${GREEN}Litmus images for amd64 loaded successfully.${NC}"
fi

# Create namespace
kubectl create namespace litmus --dry-run=client -o yaml | kubectl apply -f -

# Step 1: Deploy MongoDB first (disable other components)
echo -e "${GREEN}Step 1: Deploying MongoDB first...${NC}"
helm upgrade --install chaos "${LITMUS_CHART_DIR}" \
  --namespace litmus \
  --values config/litmus-values.yaml \
  --set portal.frontend.replicas=0 \
  --set portal.server.replicas=0 \
  --set portal.server.authServer.replicas=0 \
  --timeout 10m \
  --wait

# Wait for MongoDB to be fully ready
echo -e "${GREEN}Waiting for MongoDB StatefulSet to be ready (this may take a few minutes)...${NC}"
kubectl rollout status statefulset/chaos-mongodb -n litmus --timeout=600s

# Additional wait to ensure MongoDB is accepting connections
echo -e "${GREEN}Waiting additional 30s for MongoDB to stabilize...${NC}"
sleep 30

# Step 2: Deploy remaining Litmus components
echo -e "${GREEN}Step 2: Deploying Litmus portal components...${NC}"
helm upgrade --install chaos "${LITMUS_CHART_DIR}" \
  --namespace litmus \
  --values config/litmus-values.yaml \
  --timeout 10m \
  --wait

# Wait for operator to be ready
echo -e "${GREEN}Waiting for LitmusChaos operator to be ready...${NC}"
kubectl wait --for=condition=available --timeout=600s \
  deployment/chaos-operator-ce -n litmus 2>/dev/null || \
  kubectl wait --for=condition=available --timeout=600s \
  deployment -l app.kubernetes.io/component=operator -n litmus

# Create ServiceMonitor for Prometheus integration
echo -e "${GREEN}Creating ServiceMonitor for Prometheus integration...${NC}"
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
echo -e "${GREEN}LitmusChaos deployment complete!${NC}"
echo ""
echo "LitmusChaos chaos engineering platform is now installed."
echo ""
echo "Next steps:"
echo "  1. Deploy sample experiments: kubectl apply -f config/litmus-experiments/"
echo "  2. View chaos metrics in Grafana"
echo "  3. Check operator logs: kubectl logs -n litmus -l app.kubernetes.io/component=operator"
echo ""
echo "=== Accessing LitmusChaos UI ==="
echo "The UI is enabled. To access it:"
echo "  1. Run port-forward: make chaos-ui"
echo "  2. Open browser: http://localhost:9091"
echo "  3. Default credentials:"
echo "     Username: admin"
echo "     Password: litmus"
echo ""
echo "To run a quick test:"
echo "  kubectl apply -f config/litmus-experiments/pod-delete.yaml"


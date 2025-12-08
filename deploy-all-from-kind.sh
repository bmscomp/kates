#!/bin/bash
set -e

# Colors for output
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
RED='\033[0;31m'
NC='\033[0m' # No Color

echo -e "${GREEN}========================================${NC}"
echo -e "${GREEN}Deploying Full Stack with Local Images${NC}"
echo -e "${GREEN}========================================${NC}"
echo ""

# Step 1: Load images into kind
echo -e "${BLUE}Step 1: Loading images from local registry into Kind cluster${NC}"
./load-images-to-kind.sh

echo ""
echo -e "${BLUE}Step 2: Deploying Monitoring Stack (Prometheus & Grafana)${NC}"
echo -e "${GREEN}Installing Prometheus and Grafana...${NC}"
helm repo remove prometheus-community 2>/dev/null || true
helm repo add prometheus-community https://prometheus-community.github.io/helm-charts
helm repo update

helm upgrade --install monitoring prometheus-community/kube-prometheus-stack \
  --version 79.11.0 \
  --namespace monitoring \
  --create-namespace \
  --values config/monitoring.yaml \
  --wait

echo -e "${GREEN}Applying custom dashboards...${NC}"
kubectl apply -f config/custom-dashboard.yaml

echo ""
echo -e "${BLUE}Step 3: Deploying Kafka (Strimzi)${NC}"
./deploy-kafka.sh

echo ""
echo -e "${BLUE}Step 4: Deploying Kafka UI${NC}"
./deploy-kafka-ui.sh

echo ""
echo -e "${BLUE}Step 5: Deploying LitmusChaos${NC}"
./deploy-litmuschaos.sh

echo ""
echo -e "${GREEN}========================================${NC}"
echo -e "${GREEN}✅ Full Stack Deployment Complete!${NC}"
echo -e "${GREEN}========================================${NC}"
echo ""
echo "Services deployed:"
echo "  ✓ Prometheus & Grafana (Monitoring)"
echo "  ✓ Kafka Cluster (Strimzi KRaft mode)"
echo "  ✓ Kafka UI"
echo "  ✓ LitmusChaos"
echo ""
echo "Access points:"
echo "  - Grafana: http://localhost:30080 (admin/admin)"
echo "  - Kafka UI: http://localhost:30081"
echo "  - Litmus UI: Run 'make chaos-ui' then http://localhost:9091 (admin/litmus)"
echo ""
echo "Port forwarding:"
echo "  ./port-forward.sh  # Forward all services"
echo ""
echo "Verify deployments:"
echo "  kubectl get pods -A"
echo ""
echo "Quick chaos test:"
echo "  kubectl apply -f config/litmus-experiments/pod-delete.yaml"
echo ""

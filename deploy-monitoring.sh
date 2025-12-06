#!/bin/bash
set -e

# Colors for output
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
RED='\033[0;31m'
NC='\033[0m' # No Color

echo -e "${BLUE}=== Deploying Monitoring Stack ===${NC}"
echo ""

# Check dependencies
command -v kubectl >/dev/null 2>&1 || { echo >&2 "kubectl is required but not installed. Aborting."; exit 1; }
command -v helm >/dev/null 2>&1 || { echo >&2 "helm is required but not installed. Aborting."; exit 1; }

# Check if cluster exists
if ! kubectl cluster-info >/dev/null 2>&1; then
    echo -e "${RED}Error: No Kubernetes cluster found${NC}"
    echo "Please create the cluster first with: ./launch.sh"
    exit 1
fi

echo -e "${GREEN}✓ Kubernetes cluster found${NC}"
echo ""

# Source proxy configuration if it exists
if [ -f "proxy/proxy.conf" ]; then
    echo -e "${GREEN}Loading proxy configuration...${NC}"
    set -a
    source proxy/proxy.conf
    set +a
fi

# Add Helm repo
echo -e "${GREEN}Adding Prometheus Helm repository...${NC}"
helm repo remove prometheus-community 2>/dev/null || true
helm repo add prometheus-community https://prometheus-community.github.io/helm-charts

if ! helm repo update prometheus-community >/dev/null 2>&1; then
    echo -e "${YELLOW}⚠️  Warning: Unable to update prometheus-community repo (likely offline or cached-only mode).${NC}"
    echo -e "${YELLOW}    Using existing local index.${NC}"
fi

echo ""

# Install Prometheus & Grafana
echo -e "${GREEN}Installing Prometheus and Grafana...${NC}"
helm upgrade --install monitoring prometheus-community/kube-prometheus-stack \
  --version 79.12.0 \
  --namespace monitoring \
  --create-namespace \
  --values config/monitoring.yaml \
  --wait

echo ""
echo -e "${GREEN}✓ Prometheus and Grafana installed${NC}"
echo ""

# Apply custom dashboards
echo -e "${GREEN}Applying custom dashboards...${NC}"
kubectl apply -f config/custom-dashboard.yaml

echo ""
echo -e "${GREEN}✓ Custom dashboards applied${NC}"
echo ""

# Wait for pods to be ready
echo -e "${GREEN}Waiting for monitoring pods to be ready...${NC}"
kubectl wait --for=condition=ready pod -l app.kubernetes.io/name=grafana -n monitoring --timeout=120s 2>/dev/null || true
kubectl wait --for=condition=ready pod -l app.kubernetes.io/name=prometheus -n monitoring --timeout=120s 2>/dev/null || true

echo ""
echo -e "${GREEN}=== Monitoring Stack Deployed Successfully ===${NC}"
echo ""
echo "Access your monitoring tools:"
echo -e "  ${BLUE}Grafana:${NC}     http://localhost:30080 (admin/admin)"
echo -e "  ${BLUE}Prometheus:${NC}  http://localhost:30090"
echo ""
echo "Available dashboards:"
echo "  - Kubernetes Cluster Monitoring"
echo "  - Node Exporter"
echo "  - Kafka Monitoring (after Kafka deployment)"
echo ""
echo "To access Grafana via port-forward:"
echo -e "  ${GREEN}kubectl port-forward svc/monitoring-grafana 30080:80 -n monitoring${NC}"
echo ""

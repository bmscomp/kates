#!/bin/bash
set -e

# Colors for output
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

CHARTS_DIR="./charts"
MONITORING_CHART_DIR="${CHARTS_DIR}/kube-prometheus-stack"

echo -e "${GREEN}Deploying Monitoring Stack (Prometheus & Grafana) from local chart...${NC}"

# Check if local chart exists
if [ ! -d "${MONITORING_CHART_DIR}" ]; then
    echo -e "${YELLOW}Error: kube-prometheus-stack chart not found at ${MONITORING_CHART_DIR}${NC}"
    echo "Please run ./download-charts.sh first to download the charts"
    exit 1
fi

# Create namespace
kubectl create namespace monitoring --dry-run=client -o yaml | kubectl apply -f -

echo -e "${GREEN}Installing Prometheus and Grafana from local chart...${NC}"
helm upgrade --install monitoring "${MONITORING_CHART_DIR}" \
  --namespace monitoring \
  --values config/monitoring.yaml \
  --timeout 10m \
  --wait

echo -e "${GREEN}Applying custom dashboards...${NC}"
kubectl apply -f config/custom-dashboard.yaml

echo -e "${GREEN}Monitoring deployment complete!${NC}"

#!/bin/bash
set -e

# Colors for output
GREEN='\033[0;32m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

echo -e "${BLUE}=== Launching Kind Cluster ===${NC}"
echo ""

# Check dependencies
command -v kind >/dev/null 2>&1 || { echo >&2 "kind is required but not installed. Aborting."; exit 1; }
command -v kubectl >/dev/null 2>&1 || { echo >&2 "kubectl is required but not installed. Aborting."; exit 1; }

# Source proxy configuration if it exists
if [ -f "proxy/proxy.conf" ]; then
    echo -e "${GREEN}Loading proxy configuration...${NC}"
    set -a
    source proxy/proxy.conf
    set +a
fi

# Create Cluster
echo -e "${GREEN}Creating Kind cluster 'panda'...${NC}"
kind delete cluster --name panda 2>/dev/null || true
kind create cluster --config config/cluster.yaml --name panda

echo ""
echo -e "${GREEN}✓ Kind cluster 'panda' created successfully!${NC}"
echo ""
echo "Cluster nodes:"
kubectl get nodes
echo ""
echo "Next steps:"
echo -e "  ${GREEN}make monitoring${NC}  - Install Prometheus & Grafana"
echo -e "  ${GREEN}make deploy${NC}      - Deploy Kafka"
echo -e "  ${GREEN}make all${NC}         - Deploy full stack"
echo ""

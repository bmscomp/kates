#!/bin/bash
set -e

# Colors for output
GREEN='\033[0;32m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

echo -e "${GREEN}========================================${NC}"
echo -e "${GREEN}Full Stack Launch${NC}"
echo -e "${GREEN}========================================${NC}"
echo ""

# Step 1: Start Kind cluster
echo -e "${BLUE}Step 1: Starting Kind Cluster${NC}"
./start-cluster.sh

echo ""
echo -e "${BLUE}Step 2: Deploying Monitoring Stack${NC}"
./deploy-monitoring.sh

echo ""
echo -e "${GREEN}========================================${NC}"
echo -e "${GREEN}✅ Launch Complete!${NC}"
echo -e "${GREEN}========================================${NC}"
echo ""
echo "Services deployed:"
echo "  ✓ Kind Cluster (panda)"
echo "  ✓ Local Docker Registry (localhost:5001)"
echo "  ✓ Prometheus & Grafana (Monitoring)"
echo ""
echo "Next steps:"
echo "  - Deploy Kafka: make deploy-kafka"
echo "  - Deploy Litmus: make chaos-install"
echo "  - Deploy full stack: make deploy-all"
echo ""

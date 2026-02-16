#!/bin/bash
set -e

# Colors for output
GREEN='\033[0;32m'
NC='\033[0m' # No Color

echo -e "${GREEN}Deploying Kafka UI...${NC}"

# Deploy Kafka UI
kubectl apply -f config/kafka-ui.yaml

# Wait for deployment
echo -e "${GREEN}Waiting for Kafka UI to be ready...${NC}"
kubectl wait --for=condition=available --timeout=120s deployment/kafka-ui -n kafka

echo -e "${GREEN}Kafka UI deployment complete!${NC}"
echo ""
echo "Setting up port-forwarding (required for Mac with Kind)..."
# Kill existing port-forward if any
pkill -f "port-forward.*kafka-ui" 2>/dev/null || true
# Start port-forward in background
kubectl port-forward -n kafka svc/kafka-ui 30081:8080 > /dev/null 2>&1 &
sleep 2

echo ""
echo "Access Kafka UI at: http://localhost:30081"
echo ""
echo "Features:"
echo "  - Browse topics and partitions"
echo "  - View message content"
echo "  - Monitor consumer groups"
echo "  - View broker metrics"
echo "  - KRaft mode fully supported"

#!/bin/bash
set -e

# Colors for output
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
RED='\033[0;31m'
NC='\033[0m' # No Color

echo -e "${GREEN}========================================${NC}"
echo -e "${GREEN}Fixing Corrupted Image Load Issues${NC}"
echo -e "${GREEN}========================================${NC}"
echo ""

KIND_CLUSTER_NAME="panda"

# List of all required images
IMAGES=(
    # Kafka Stack
    "provectuslabs/kafka-ui:latest"
    "quay.io/strimzi/operator:0.49.0"
    "quay.io/strimzi/kafka:0.49.0-kafka-4.1.1"
    "quay.io/strimzi/kafka:0.49.0-kafka-4.0.0"
    
    # Prometheus Stack
    "quay.io/prometheus/prometheus:v3.1.0"
    "quay.io/prometheus/alertmanager:v0.28.1"
    "quay.io/prometheus/node-exporter:v1.8.2"
    "quay.io/prometheus-operator/prometheus-operator:v0.79.2"
    "quay.io/prometheus-operator/prometheus-config-reloader:v0.79.2"
    "quay.io/prometheus-operator/admission-webhook:v0.79.2"
    
    # Grafana
    "docker.io/grafana/grafana:11.4.0"
    "quay.io/kiwigrid/k8s-sidecar:1.27.6"
    
    # Kube State Metrics
    "registry.k8s.io/kube-state-metrics/kube-state-metrics:v2.14.0"
    
    # Webhook
    "registry.k8s.io/ingress-nginx/kube-webhook-certgen:v1.6.5"
    
    # LitmusChaos
    "litmuschaos/chaos-operator:3.23.0"
    "litmuschaos/chaos-runner:3.23.0"
    "litmuschaos/chaos-exporter:3.23.0"
    "litmuschaos/litmusportal-subscriber:3.23.0"
    "litmuschaos/litmusportal-event-tracker:3.23.0"
    "docker.io/mongo:8.0"
)

echo -e "${BLUE}Step 1: Cleaning up potentially corrupted images${NC}"
echo ""

for image in "${IMAGES[@]}"; do
    echo -e "${YELLOW}Processing: ${image}${NC}"
    
    # Remove the image from local Docker (if exists)
    if docker images --format "{{.Repository}}:{{.Tag}}" | grep -q "^${image}$"; then
        echo "  Removing from local Docker..."
        docker rmi -f "${image}" 2>/dev/null || true
    fi
    
    # Pull fresh copy
    echo "  Pulling fresh copy..."
    if docker pull "${image}"; then
        echo -e "  ${GREEN}✓ Pulled successfully${NC}"
    else
        echo -e "  ${RED}✗ Failed to pull, skipping...${NC}"
        continue
    fi
    
    # Load into Kind with retry logic
    echo "  Loading into Kind..."
    
    success=false
    for attempt in 1 2 3; do
        if kind load docker-image "${image}" --name "${KIND_CLUSTER_NAME}" 2>&1; then
            success=true
            echo -e "  ${GREEN}✓ Loaded successfully${NC}"
            break
        else
            if [ $attempt -lt 3 ]; then
                echo -e "  ${YELLOW}Attempt $attempt failed, retrying...${NC}"
                sleep 2
            else
                echo -e "  ${RED}✗ Failed after 3 attempts${NC}"
            fi
        fi
    done
    
    echo ""
done

echo ""
echo -e "${GREEN}========================================${NC}"
echo -e "${GREEN}Image Cleanup and Reload Complete!${NC}"
echo -e "${GREEN}========================================${NC}"
echo ""
echo "Verify images in Kind nodes:"
echo "  docker exec panda-control-plane crictl images"
echo ""

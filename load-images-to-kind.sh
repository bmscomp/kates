#!/bin/bash
set -e

# Colors for output
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
RED='\033[0;31m'
NC='\033[0m' # No Color

REGISTRY="localhost:5001"
KIND_CLUSTER_NAME="panda"

# Temporarily unset proxy for Docker operations
unset HTTP_PROXY HTTPS_PROXY http_proxy https_proxy

echo -e "${GREEN}Loading images from local registry into Kind cluster '${KIND_CLUSTER_NAME}'...${NC}"

# Check if kind cluster exists
if ! kind get clusters | grep -q "^${KIND_CLUSTER_NAME}$"; then
    echo -e "${RED}Error: Kind cluster '${KIND_CLUSTER_NAME}' not found${NC}"
    echo "Please run ./launch.sh first to create the cluster"
    exit 1
fi

# Check if local registry is running
if ! curl -s http://${REGISTRY}/v2/_catalog > /dev/null 2>&1; then
    echo -e "${RED}Error: Local registry is not running at ${REGISTRY}${NC}"
    echo "Please run ./setup-registry.sh first"
    exit 1
fi

# Function to pull from local registry and load into kind
load_from_local_registry() {
    local image=$1
    local local_image="${REGISTRY}/${image}"
    
    echo -e "${BLUE}Processing: ${image}${NC}"
    
    # Check if image already exists in kind cluster
    if docker exec "${KIND_CLUSTER_NAME}-control-plane" crictl images 2>/dev/null | grep -q "${image}"; then
        echo -e "${YELLOW}  Image already exists in kind cluster, skipping...${NC}"
        return 0
    fi
    
    # Pull from local registry
    echo "  Pulling from local registry..."
    docker pull "${local_image}"
    
    # Tag back to original name (kind load expects original image name)
    docker tag "${local_image}" "${image}"
    
    # Load into kind cluster
    echo "  Loading into kind cluster..."
    kind load docker-image "${image}" --name "${KIND_CLUSTER_NAME}"
    
    echo -e "${GREEN}✓ Loaded ${image}${NC}"
}

echo ""
echo -e "${GREEN}=== Kafka UI Images ===${NC}"
load_from_local_registry "provectuslabs/kafka-ui:latest"

echo ""
echo -e "${GREEN}=== Strimzi Operator Images ===${NC}"
load_from_local_registry "quay.io/strimzi/operator:0.49.0"

echo ""
echo -e "${GREEN}=== Strimzi Kafka Images ===${NC}"
load_from_local_registry "quay.io/strimzi/kafka:0.49.0-kafka-4.1.1"
load_from_local_registry "quay.io/strimzi/kafka:0.49.0-kafka-4.0.0"

echo ""
echo -e "${GREEN}=== Prometheus Stack Images ===${NC}"
load_from_local_registry "quay.io/prometheus/prometheus:v3.1.0"
load_from_local_registry "quay.io/prometheus/alertmanager:v0.28.1"
load_from_local_registry "quay.io/prometheus-operator/prometheus-operator:v0.79.2"
load_from_local_registry "quay.io/prometheus-operator/prometheus-config-reloader:v0.79.2"
load_from_local_registry "quay.io/prometheus-operator/admission-webhook:v0.79.2"
load_from_local_registry "quay.io/prometheus/node-exporter:v1.8.2"

echo ""
echo -e "${GREEN}=== Grafana Images ===${NC}"
load_from_local_registry "docker.io/grafana/grafana:11.4.0"
load_from_local_registry "quay.io/kiwigrid/k8s-sidecar:1.27.6"

echo ""
echo -e "${GREEN}=== Kube State Metrics Images ===${NC}"
load_from_local_registry "registry.k8s.io/kube-state-metrics/kube-state-metrics:v2.14.0"

echo ""
echo -e "${GREEN}=== Webhook Certgen Images ===${NC}"
load_from_local_registry "registry.k8s.io/ingress-nginx/kube-webhook-certgen:v1.6.5"

echo ""
echo -e "${GREEN}=== LitmusChaos Images ===${NC}"
load_from_local_registry "litmuschaos/chaos-operator:3.23.0"
load_from_local_registry "litmuschaos/chaos-runner:3.23.0"
load_from_local_registry "litmuschaos/chaos-exporter:3.23.0"
load_from_local_registry "litmuschaos/litmusportal-subscriber:3.23.0"
load_from_local_registry "litmuschaos/litmusportal-event-tracker:3.23.0"

echo ""
echo -e "${GREEN}=== LitmusChaos Portal Images (from scarf.sh) ===${NC}"
# Note: These use scarf.sh registry which tracks downloads
echo -e "${YELLOW}Note: Pulling portal images from litmuschaos.docker.scarf.sh${NC}"
docker pull litmuschaos.docker.scarf.sh/litmuschaos/litmusportal-auth-server:3.23.0 || true
docker pull litmuschaos.docker.scarf.sh/litmuschaos/litmusportal-frontend:3.23.0 || true
docker pull litmuschaos.docker.scarf.sh/litmuschaos/litmusportal-server:3.23.0 || true
docker pull litmuschaos.docker.scarf.sh/litmuschaos/mongo:6 || true

# MongoDB from docker.io (compatible with chart)
docker pull docker.io/bitnami/mongodb:latest || true
docker pull docker.io/litmuschaos/mongo:6 || true

# Load portal images
kind load docker-image litmuschaos.docker.scarf.sh/litmuschaos/litmusportal-auth-server:3.23.0 --name "${KIND_CLUSTER_NAME}" || true
kind load docker-image litmuschaos.docker.scarf.sh/litmuschaos/litmusportal-frontend:3.23.0 --name "${KIND_CLUSTER_NAME}" || true
kind load docker-image litmuschaos.docker.scarf.sh/litmuschaos/litmusportal-server:3.23.0 --name "${KIND_CLUSTER_NAME}" || true
kind load docker-image litmuschaos.docker.scarf.sh/litmuschaos/mongo:6 --name "${KIND_CLUSTER_NAME}" || true
kind load docker-image docker.io/bitnami/mongodb:latest --name "${KIND_CLUSTER_NAME}" || true
kind load docker-image docker.io/litmuschaos/mongo:6 --name "${KIND_CLUSTER_NAME}" || true

echo ""
echo -e "${GREEN}=== MongoDB Init Container ===${NC}"
docker pull docker.io/bitnamilegacy/os-shell:12-debian-12-r51 || true
kind load docker-image docker.io/bitnamilegacy/os-shell:12-debian-12-r51 --name "${KIND_CLUSTER_NAME}" || true

echo ""
echo -e "${GREEN}========================================${NC}"
echo -e "${GREEN}All images have been loaded into Kind cluster '${KIND_CLUSTER_NAME}'!${NC}"
echo -e "${GREEN}========================================${NC}"
echo ""
echo "To verify images loaded in kind nodes, you can exec into a node:"
echo "  docker exec -it ${KIND_CLUSTER_NAME}-control-plane crictl images"
echo ""

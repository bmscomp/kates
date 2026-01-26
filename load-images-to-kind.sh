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

# Detect platform and set explicit platform for Docker pulls
# This ensures we get the correct architecture for kind nodes
ARCH=$(uname -m)
if [ "$ARCH" = "arm64" ] || [ "$ARCH" = "aarch64" ]; then
    PLATFORM="--platform linux/arm64"
else
    PLATFORM="--platform linux/amd64"
fi

# Temporarily unset proxy for Docker operations
unset HTTP_PROXY HTTPS_PROXY http_proxy https_proxy

echo -e "${GREEN}========================================${NC}"
echo -e "${GREEN}Loading Images into Kind Cluster${NC}"
echo -e "${GREEN}========================================${NC}"
echo ""

# Check if kind cluster exists
if ! kind get clusters | grep -q "^${KIND_CLUSTER_NAME}$"; then
    echo -e "${RED}Error: Kind cluster '${KIND_CLUSTER_NAME}' not found${NC}"
    echo "Please run 'make cluster' or './start-cluster.sh' first"
    exit 1
fi

# Check if local registry is running
if ! curl -s http://${REGISTRY}/v2/_catalog > /dev/null 2>&1; then
    echo -e "${RED}Error: Local registry is not running at ${REGISTRY}${NC}"
    echo "Please run './setup-registry.sh' first"
    exit 1
fi

echo -e "${BLUE}Loading images from local registry (${REGISTRY}) into Kind cluster '${KIND_CLUSTER_NAME}'...${NC}"
echo ""

# Function to pull from local registry and load into kind
load_from_local_registry() {
    local image=$1
    local local_image="${REGISTRY}/${image}"
    
    echo -e "${BLUE}Processing: ${image}${NC}"
    
    # Check if image already exists in kind cluster
    # Extract image name and tag for precise matching
    local image_name="${image%%:*}"
    local image_tag="${image##*:}"
    if docker exec "${KIND_CLUSTER_NAME}-control-plane" crictl images 2>/dev/null | grep "${image_name}" | grep -q "${image_tag}"; then
        echo -e "${YELLOW}  Image already exists in kind cluster, skipping...${NC}"
        return 0
    fi
    
    # Pull from local registry (with platform specification for Kind compatibility)
    echo "  Pulling from local registry..."
    docker pull ${PLATFORM} "${local_image}"
    
    # Tag back to original name (kind load expects original image name)
    docker tag "${local_image}" "${image}"
    
    # Load into kind cluster
    echo "  Loading into kind cluster..."
    kind load docker-image "${image}" --name "${KIND_CLUSTER_NAME}"
    
    echo -e "${GREEN}✓ Loaded ${image}${NC}"
}

echo ""
echo -e "${GREEN}=== Kafka UI Images ===${NC}"
load_from_local_registry "provectuslabs/kafka-ui:v0.7.2"

echo ""
echo -e "${GREEN}=== Strimzi Operator Images ===${NC}"
load_from_local_registry "quay.io/strimzi/operator:0.49.0"

echo ""
echo -e "${GREEN}=== Strimzi Kafka Images ===${NC}"
load_from_local_registry "quay.io/strimzi/operator:0.49.0"
load_from_local_registry "quay.io/strimzi/kafka:0.49.0-kafka-4.1.1"
load_from_local_registry "apicurio/apicurio-registry-kafkasql:2.2.5.Final"

# Velero & MinIO
load_from_local_registry "velero/velero:v1.17.1"
load_from_local_registry "velero/velero-plugin-for-aws:v1.10.0"
load_from_local_registry "docker.io/minio/minio:RELEASE.2024-09-22T00-33-43Z"
load_from_local_registry "docker.io/minio/mc:RELEASE.2024-09-16T17-43-14Z"
load_from_local_registry "docker.io/bitnamilegacy/kubectl:1.17.1"

echo ""
echo -e "${GREEN}=== Prometheus Stack Images ===${NC}"
load_from_local_registry "quay.io/prometheus/prometheus:v3.9.1"
load_from_local_registry "quay.io/prometheus/alertmanager:v0.28.1"
load_from_local_registry "quay.io/prometheus-operator/prometheus-operator:v0.79.2"
load_from_local_registry "quay.io/prometheus-operator/prometheus-config-reloader:v0.79.2"
load_from_local_registry "quay.io/prometheus-operator/admission-webhook:v0.79.2"
load_from_local_registry "quay.io/prometheus/node-exporter:v1.8.2"

echo ""
echo -e "${GREEN}=== Grafana Images ===${NC}"
load_from_local_registry "docker.io/grafana/grafana:12.3.1"
# Updated to v2.2.3 - newer version works without manifest corruption
load_from_local_registry "quay.io/kiwigrid/k8s-sidecar:2.2.3"

echo ""
echo -e "${GREEN}=== Kube State Metrics Images ===${NC}"
load_from_local_registry "registry.k8s.io/kube-state-metrics/kube-state-metrics:v2.14.0"

echo ""
echo -e "${GREEN}=== Webhook Certgen Images ===${NC}"
load_from_local_registry "registry.k8s.io/ingress-nginx/kube-webhook-certgen:v1.6.5"

echo ""
echo -e "${GREEN}=== LitmusChaos Images ===${NC}"
load_from_local_registry "litmuschaos/chaos-operator:3.25.0"
load_from_local_registry "litmuschaos/chaos-runner:3.25.0"
load_from_local_registry "litmuschaos/chaos-exporter:3.25.0"
load_from_local_registry "litmuschaos/litmusportal-subscriber:3.24.0"
load_from_local_registry "litmuschaos/litmusportal-event-tracker:3.24.0"

echo ""
echo -e "${GREEN}=== LitmusChaos Portal Images (from scarf.sh) ===${NC}"
# Note: These are pulled from local registry where scarf.sh images were pushed
echo -e "${BLUE}Loading portal images from local registry...${NC}"

# Function to load scarf.sh images from local registry
load_scarf_image() {
    local image=$1
    local local_image="${REGISTRY}/${image}"
    
    echo -e "${BLUE}Processing: ${image}${NC}"
    
    # Check if image already exists in kind cluster
    # Extract image name and tag for precise matching
    local image_name="${image%%:*}"
    local image_tag="${image##*:}"
    if docker exec "${KIND_CLUSTER_NAME}-control-plane" crictl images 2>/dev/null | grep "${image_name}" | grep -q "${image_tag}"; then
        echo -e "${YELLOW}  Image already exists in kind cluster, skipping...${NC}"
        return 0
    fi
    
    # Pull from local registry
    echo "  Pulling from local registry..."
    if docker pull "${local_image}" 2>/dev/null; then
        # Tag back to original name
        docker tag "${local_image}" "${image}"
        # Load into kind
        echo "  Loading into kind cluster..."
        kind load docker-image "${image}" --name "${KIND_CLUSTER_NAME}"
        echo -e "${GREEN}✓ Loaded ${image}${NC}"
    else
        echo -e "${YELLOW}  Image not in local registry, skipping...${NC}"
    fi
}


load_scarf_image "litmuschaos.docker.scarf.sh/litmuschaos/litmusportal-auth-server:3.24.0"
load_scarf_image "litmuschaos.docker.scarf.sh/litmuschaos/litmusportal-frontend:3.24.0"
load_scarf_image "litmuschaos.docker.scarf.sh/litmuschaos/litmusportal-server:3.24.0"
load_scarf_image "litmuschaos.docker.scarf.sh/litmuschaos/mongo:6"

echo ""
echo -e "${GREEN}=== MongoDB Dependencies ===${NC}"
load_from_local_registry "docker.io/bitnami/mongodb:latest"

load_from_local_registry "docker.io/litmuschaos/mongo:6"
load_from_local_registry "docker.io/bitnamilegacy/os-shell:12-debian-12-r51"

echo ""
echo -e "${GREEN}========================================${NC}"
echo -e "${GREEN}✅ Image Loading Complete!${NC}"
echo -e "${GREEN}========================================${NC}"
echo ""
echo "All images have been loaded into Kind cluster '${KIND_CLUSTER_NAME}'!"
echo ""
echo "Verify images in cluster:"
echo "  docker exec -it ${KIND_CLUSTER_NAME}-control-plane crictl images"
echo ""
echo "Next steps:"
echo "  - Deploy monitoring: make monitoring"
echo "  - Deploy full stack: make deploy-all"
echo ""

#!/bin/bash
set -e

# Colors for output
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

REGISTRY="localhost:5001"

# Detect platform
ARCH=$(uname -m)
if [ "$ARCH" = "arm64" ] || [ "$ARCH" = "aarch64" ]; then
    PLATFORM="--platform linux/arm64"
else
    PLATFORM="--platform linux/amd64"
fi

# Temporarily unset proxy for Docker operations to avoid timeout issues
unset HTTP_PROXY HTTPS_PROXY http_proxy https_proxy

echo -e "${GREEN}Pulling all LitmusChaos images to local registry...${NC}"
echo -e "${YELLOW}Unified versions matching chaos-litmus-chaos-enable.yml manifest${NC}"
echo ""

# Check if registry is running
if ! curl -s http://${REGISTRY}/v2/_catalog > /dev/null 2>&1; then
    echo -e "${YELLOW}Local registry is not running. Please run ./setup-registry.sh first${NC}"
    exit 1
fi

# Function to pull, tag, and push an image
push_to_local_registry() {
    local image=$1
    local local_image="${REGISTRY}/${image}"
    
    echo -e "${BLUE}Processing: ${image}${NC}"
    
    # Pull from public registry with platform specification
    docker pull ${PLATFORM} ${image}
    
    # Tag for local registry
    docker tag ${image} ${local_image}
    
    # Push to local registry
    docker push ${local_image}
    
    echo -e "${GREEN}✓ Pushed: ${local_image}${NC}"
}

echo ""
echo "=== LitmusChaos Core Images (3.23.0) ==="
push_to_local_registry "litmuschaos/chaos-operator:3.23.0"
push_to_local_registry "litmuschaos/chaos-runner:3.23.0"
push_to_local_registry "litmuschaos/chaos-exporter:3.23.0"
push_to_local_registry "litmuschaos/go-runner:3.23.0"

echo ""
echo "=== LitmusChaos Portal Components (3.23.0) ==="
push_to_local_registry "litmuschaos/litmusportal-subscriber:3.23.0"
push_to_local_registry "litmuschaos/litmusportal-event-tracker:3.23.0"

echo ""
echo "=== LitmusChaos Portal Images (from scarf.sh) ==="
# Portal Images (from scarf.sh) - re-tagged to simple names for local registry
echo -e "${BLUE}Processing: litmuschaos.docker.scarf.sh/litmuschaos/litmusportal-auth-server:3.23.0${NC}"
docker pull ${PLATFORM} litmuschaos.docker.scarf.sh/litmuschaos/litmusportal-auth-server:3.23.0
docker tag litmuschaos.docker.scarf.sh/litmuschaos/litmusportal-auth-server:3.23.0 ${REGISTRY}/litmuschaos/litmusportal-auth-server:3.23.0
docker push ${REGISTRY}/litmuschaos/litmusportal-auth-server:3.23.0
echo -e "${GREEN}✓ Pushed: ${REGISTRY}/litmuschaos/litmusportal-auth-server:3.23.0${NC}"

echo -e "${BLUE}Processing: litmuschaos.docker.scarf.sh/litmuschaos/litmusportal-frontend:3.23.0${NC}"
docker pull ${PLATFORM} litmuschaos.docker.scarf.sh/litmuschaos/litmusportal-frontend:3.23.0
docker tag litmuschaos.docker.scarf.sh/litmuschaos/litmusportal-frontend:3.23.0 ${REGISTRY}/litmuschaos/litmusportal-frontend:3.23.0
docker push ${REGISTRY}/litmuschaos/litmusportal-frontend:3.23.0
echo -e "${GREEN}✓ Pushed: ${REGISTRY}/litmuschaos/litmusportal-frontend:3.23.0${NC}"

echo -e "${BLUE}Processing: litmuschaos.docker.scarf.sh/litmuschaos/litmusportal-server:3.23.0${NC}"
docker pull ${PLATFORM} litmuschaos.docker.scarf.sh/litmuschaos/litmusportal-server:3.23.0
docker tag litmuschaos.docker.scarf.sh/litmuschaos/litmusportal-server:3.23.0 ${REGISTRY}/litmuschaos/litmusportal-server:3.23.0
docker push ${REGISTRY}/litmuschaos/litmusportal-server:3.23.0
echo -e "${GREEN}✓ Pushed: ${REGISTRY}/litmuschaos/litmusportal-server:3.23.0${NC}"

echo ""
echo "=== Workflow Controller ==="
echo -e "${BLUE}Processing: litmuschaos.docker.scarf.sh/litmuschaos/workflow-controller:v3.3.1${NC}"
docker pull ${PLATFORM} litmuschaos.docker.scarf.sh/litmuschaos/workflow-controller:v3.3.1
docker tag litmuschaos.docker.scarf.sh/litmuschaos/workflow-controller:v3.3.1 ${REGISTRY}/litmuschaos/workflow-controller:v3.3.1
docker push ${REGISTRY}/litmuschaos/workflow-controller:v3.3.1
echo -e "${GREEN}✓ Pushed: ${REGISTRY}/litmuschaos/workflow-controller:v3.3.1${NC}"

echo ""
echo "=== MongoDB Images ==="
echo -e "${BLUE}Processing: litmuschaos.docker.scarf.sh/litmuschaos/mongo:6${NC}"
docker pull ${PLATFORM} litmuschaos.docker.scarf.sh/litmuschaos/mongo:6
docker tag litmuschaos.docker.scarf.sh/litmuschaos/mongo:6 ${REGISTRY}/litmuschaos/mongo:6
docker push ${REGISTRY}/litmuschaos/mongo:6
echo -e "${GREEN}✓ Pushed: ${REGISTRY}/litmuschaos/mongo:6${NC}"

echo ""
echo "=== MongoDB Dependencies ==="
push_to_local_registry "docker.io/bitnami/mongodb:latest"
push_to_local_registry "docker.io/bitnamilegacy/os-shell:12-debian-12-r51"

echo ""
echo -e "${GREEN}All LitmusChaos images have been pushed to local registry!${NC}"
echo ""
echo "To view all images in the registry:"
echo "  curl http://${REGISTRY}/v2/_catalog | jq"
echo ""
echo "LitmusChaos images in registry:"
curl -s http://${REGISTRY}/v2/_catalog | jq '.repositories | map(select(contains("litmus"))) | length'

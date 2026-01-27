#!/bin/bash
set -e

# Colors for output
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
RED='\033[0;31m'
NC='\033[0m' # No Color

echo -e "${GREEN}========================================${NC}"
echo -e "${GREEN}Pulling LitmusChaos Images to Local Registry${NC}"
echo -e "${GREEN}========================================${NC}"
echo ""

REGISTRY="localhost:5001"
PLATFORM="--platform linux/arm64"

# Function to pull and push to local registry
push_to_local_registry() {
    local image=$1
    local registry_image="${REGISTRY}/${image}"
    
    echo -e "${YELLOW}Processing: ${image}${NC}"
    
    # Pull the image
    if docker pull ${PLATFORM} "${image}"; then
        echo -e "  ${GREEN}✓ Pulled${NC}"
    else
        echo -e "  ${RED}✗ Failed to pull${NC}"
        return 1
    fi
    
    # Tag for local registry
    docker tag "${image}" "${registry_image}"
    
    # Push to local registry
    if docker push "${registry_image}"; then
        echo -e "  ${GREEN}✓ Pushed to ${REGISTRY}${NC}"
    else
        echo -e "  ${RED}✗ Failed to push${NC}"
        return 1
    fi
    
    echo ""
}

echo -e "${BLUE}Pulling Core LitmusChaos Images${NC}"
echo ""

# Core LitmusChaos components (docker.io)
push_to_local_registry "litmuschaos/chaos-operator:3.24.0"
push_to_local_registry "litmuschaos/chaos-runner:3.24.0"
push_to_local_registry "litmuschaos/chaos-exporter:3.24.0"
push_to_local_registry "litmuschaos/go-runner:3.24.0"
push_to_local_registry "litmuschaos/litmusportal-subscriber:3.24.0"
push_to_local_registry "litmuschaos/litmusportal-event-tracker:3.24.0"

echo -e "${BLUE}Pulling LitmusChaos Portal Images (scarf.sh)${NC}"
echo ""

# Portal components from scarf.sh (required by Helm chart)
echo -e "${BLUE}Processing: litmuschaos.docker.scarf.sh/litmuschaos/litmusportal-auth-server:3.24.0${NC}"
docker pull ${PLATFORM} litmuschaos.docker.scarf.sh/litmuschaos/litmusportal-auth-server:3.24.0
docker tag litmuschaos.docker.scarf.sh/litmuschaos/litmusportal-auth-server:3.24.0 ${REGISTRY}/litmuschaos/litmusportal-auth-server:3.24.0
docker push ${REGISTRY}/litmuschaos/litmusportal-auth-server:3.24.0
echo -e "${GREEN}✓ Pushed: ${REGISTRY}/litmuschaos/litmusportal-auth-server:3.24.0${NC}"

echo -e "${BLUE}Processing: litmuschaos.docker.scarf.sh/litmuschaos/litmusportal-frontend:3.24.0${NC}"
docker pull ${PLATFORM} litmuschaos.docker.scarf.sh/litmuschaos/litmusportal-frontend:3.24.0
docker tag litmuschaos.docker.scarf.sh/litmuschaos/litmusportal-frontend:3.24.0 ${REGISTRY}/litmuschaos/litmusportal-frontend:3.24.0
docker push ${REGISTRY}/litmuschaos/litmusportal-frontend:3.24.0
echo -e "${GREEN}✓ Pushed: ${REGISTRY}/litmuschaos/litmusportal-frontend:3.24.0${NC}"

echo -e "${BLUE}Processing: litmuschaos.docker.scarf.sh/litmuschaos/litmusportal-server:3.24.0${NC}"
docker pull ${PLATFORM} litmuschaos.docker.scarf.sh/litmuschaos/litmusportal-server:3.24.0
docker tag litmuschaos.docker.scarf.sh/litmuschaos/litmusportal-server:3.24.0 ${REGISTRY}/litmuschaos/litmusportal-server:3.24.0
docker push ${REGISTRY}/litmuschaos/litmusportal-server:3.24.0
echo -e "${GREEN}✓ Pushed: ${REGISTRY}/litmuschaos/litmusportal-server:3.24.0${NC}"

echo -e "${BLUE}Processing: litmuschaos.docker.scarf.sh/litmuschaos/workflow-controller:v3.3.1${NC}"
docker pull ${PLATFORM} litmuschaos.docker.scarf.sh/litmuschaos/workflow-controller:v3.3.1
docker tag litmuschaos.docker.scarf.sh/litmuschaos/workflow-controller:v3.3.1 ${REGISTRY}/litmuschaos/workflow-controller:v3.3.1
docker push ${REGISTRY}/litmuschaos/workflow-controller:v3.3.1
echo -e "${GREEN}✓ Pushed: ${REGISTRY}/litmuschaos/workflow-controller:v3.3.1${NC}"

echo ""
echo -e "${BLUE}Pulling MongoDB Images${NC}"
echo ""

# MongoDB (litmuschaos version for standalone mode)
echo -e "${BLUE}Processing: litmuschaos.docker.scarf.sh/litmuschaos/mongo:6${NC}"
docker pull ${PLATFORM} litmuschaos.docker.scarf.sh/litmuschaos/mongo:6
docker tag litmuschaos.docker.scarf.sh/litmuschaos/mongo:6 ${REGISTRY}/litmuschaos/mongo:6
docker push ${REGISTRY}/litmuschaos/mongo:6
echo -e "${GREEN}✓ Pushed: ${REGISTRY}/litmuschaos/mongo:6${NC}"

# MongoDB (Bitnami version for replicaset mode - required by Helm chart)
push_to_local_registry "docker.io/bitnami/mongodb:latest"

# MongoDB volume permissions helper
push_to_local_registry "docker.io/bitnamilegacy/os-shell:12-debian-12-r51"

echo ""
echo -e "${GREEN}========================================${NC}"
echo -e "${GREEN}All LitmusChaos Images Pulled!${NC}"
echo -e "${GREEN}========================================${NC}"
echo ""
echo "Next step: Load images into Kind cluster"
echo "  ./load-litmus-images.sh"
echo ""

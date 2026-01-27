#!/bin/bash
set -e

# Colors for output
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
RED='\033[0;31m'
NC='\033[0m' # No Color

echo -e "${GREEN}========================================${NC}"
echo -e "${GREEN}Loading LitmusChaos Images to Kind${NC}"
echo -e "${GREEN}========================================${NC}"
echo ""

KIND_CLUSTER_NAME="panda"

# Unified LitmusChaos images - versions match chaos-litmus-chaos-enable.yml manifest
IMAGES=(
    # Core LitmusChaos components (3.23.0 - matches manifest)
    "litmuschaos/chaos-operator:3.24.0"
    "litmuschaos/chaos-runner:3.24.0"
    "litmuschaos/chaos-exporter:3.24.0"
    "litmuschaos/go-runner:3.24.0"
    
    # Portal components (3.23.0 - matches manifest)
    "litmuschaos/litmusportal-subscriber:3.24.0"
    "litmuschaos/litmusportal-event-tracker:3.24.0"
    
    # Portal Images from scarf.sh (3.23.0 - matches manifest)
    "litmuschaos.docker.scarf.sh/litmuschaos/litmusportal-auth-server:3.24.0"
    "litmuschaos.docker.scarf.sh/litmuschaos/litmusportal-frontend:3.24.0"
    "litmuschaos.docker.scarf.sh/litmuschaos/litmusportal-server:3.24.0"
    
    # Workflow controller
    "litmuschaos.docker.scarf.sh/litmuschaos/workflow-controller:v3.3.1"
    
    # MongoDB
    "litmuschaos.docker.scarf.sh/litmuschaos/mongo:6"
    
    # MongoDB dependencies
    "docker.io/mongo:8.0"
    "docker.io/bitnamilegacy/os-shell:12-debian-12-r51"
)

echo -e "${BLUE}Loading LitmusChaos images into Kind cluster${NC}"
echo ""

for image in "${IMAGES[@]}"; do
    echo -e "${YELLOW}Processing: ${image}${NC}"
    
    # Check if image exists in Kind
    if docker exec "${KIND_CLUSTER_NAME}-control-plane" crictl images 2>/dev/null | grep -q "${image}"; then
        echo -e "  ${GREEN}✓ Already in Kind${NC}"
        continue
    fi
    
    # Check if image exists locally in Docker
    if ! docker images --format "{{.Repository}}:{{.Tag}}" | grep -q "^${image}$"; then
        echo -e "  ${BLUE}Pulling from remote...${NC}"
        if docker pull "${image}"; then
            echo -e "  ${GREEN}✓ Pulled${NC}"
        else
            echo -e "  ${RED}✗ Failed to pull, skipping...${NC}"
            continue
        fi
    else
        echo -e "  ${GREEN}✓ Already in Docker${NC}"
    fi
    
    # Load into Kind
    echo -e "  ${BLUE}Loading into Kind...${NC}"
    if kind load docker-image "${image}" --name "${KIND_CLUSTER_NAME}"; then
        echo -e "  ${GREEN}✓ Loaded into Kind${NC}"
    else
        echo -e "  ${RED}✗ Failed to load${NC}"
    fi
    
    echo ""
done

echo ""
echo -e "${GREEN}========================================${NC}"
echo -e "${GREEN}Image Loading Complete!${NC}"
echo -e "${GREEN}========================================${NC}"
echo ""
echo "Now restart LitmusChaos pods:"
echo "  kubectl delete pods -n litmus --all"
echo ""
echo "Monitor pod status:"
echo "  kubectl get pods -n litmus -w"
echo ""

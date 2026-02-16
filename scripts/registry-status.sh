#!/bin/bash
set -e

# Colors for output
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
RED='\033[0;31m'
NC='\033[0m' # No Color

REGISTRY_NAME="kind-registry"
REGISTRY_PORT="5001"
REGISTRY_URL="localhost:${REGISTRY_PORT}"

echo -e "${BLUE}=== Local Docker Registry Status ===${NC}"
echo ""

# Check if registry container exists
if [ ! "$(docker ps -aq -f name=${REGISTRY_NAME})" ]; then
    echo -e "${RED}✗ Registry container does not exist${NC}"
    echo "Run ./setup-registry.sh to create it"
    exit 1
fi

# Check if registry is running
if [ "$(docker ps -q -f name=${REGISTRY_NAME})" ]; then
    echo -e "${GREEN}✓ Registry is running${NC}"
    echo "  Container: ${REGISTRY_NAME}"
    echo "  URL: http://${REGISTRY_URL}"
else
    echo -e "${YELLOW}⚠ Registry container exists but is stopped${NC}"
    echo "Run: docker start ${REGISTRY_NAME}"
    exit 1
fi

echo ""

# Check registry connectivity
if curl -s http://${REGISTRY_URL}/v2/_catalog > /dev/null 2>&1; then
    echo -e "${GREEN}✓ Registry is accessible${NC}"
else
    echo -e "${RED}✗ Registry is not accessible${NC}"
    exit 1
fi

echo ""
echo -e "${BLUE}=== Registry Contents ===${NC}"

# Get catalog
CATALOG=$(curl -s http://${REGISTRY_URL}/v2/_catalog)
IMAGE_COUNT=$(echo $CATALOG | jq -r '.repositories | length')

echo "Total images: ${IMAGE_COUNT}"
echo ""

if [ "$IMAGE_COUNT" -gt 0 ]; then
    echo "Images in registry:"
    echo $CATALOG | jq -r '.repositories[]' | while read repo; do
        # Get tags for each repository
        TAGS=$(curl -s http://${REGISTRY_URL}/v2/${repo}/tags/list | jq -r '.tags[]?' 2>/dev/null || echo "")
        if [ -n "$TAGS" ]; then
            echo "  - ${repo}:${TAGS}"
        else
            echo "  - ${repo}"
        fi
    done
else
    echo -e "${YELLOW}No images in registry${NC}"
    echo "Run ./pull-images.sh to populate the registry"
fi

echo ""
echo -e "${BLUE}=== Network Configuration ===${NC}"

# Check if connected to kind network
if docker network inspect kind 2>/dev/null | grep -q ${REGISTRY_NAME}; then
    echo -e "${GREEN}✓ Registry is connected to 'kind' network${NC}"
else
    echo -e "${YELLOW}⚠ Registry is not connected to 'kind' network${NC}"
    echo "Run: docker network connect kind ${REGISTRY_NAME}"
fi

echo ""
echo -e "${BLUE}=== Quick Commands ===${NC}"
echo "View catalog:  curl http://${REGISTRY_URL}/v2/_catalog | jq"
echo "Stop registry: docker stop ${REGISTRY_NAME}"
echo "Start registry: docker start ${REGISTRY_NAME}"
echo "Remove registry: ./cleanup-registry.sh"

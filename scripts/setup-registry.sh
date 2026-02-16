#!/bin/bash
set -e

# Colors for output
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

REGISTRY_NAME="kind-registry"
REGISTRY_PORT="5001"

echo -e "${GREEN}Setting up local Docker registry...${NC}"

# Check if registry is already running
if [ "$(docker ps -q -f name=${REGISTRY_NAME})" ]; then
    echo -e "${YELLOW}Registry '${REGISTRY_NAME}' is already running${NC}"
    echo "Registry available at: localhost:${REGISTRY_PORT}"
    exit 0
fi

# Check if registry container exists but is stopped
if [ "$(docker ps -aq -f name=${REGISTRY_NAME})" ]; then
    echo -e "${YELLOW}Starting existing registry container...${NC}"
    docker start ${REGISTRY_NAME}
    echo -e "${GREEN}Registry started successfully${NC}"
    echo "Registry available at: localhost:${REGISTRY_PORT}"
    exit 0
fi

# Create registry container
echo -e "${GREEN}Creating new registry container...${NC}"
docker run -d \
  --name ${REGISTRY_NAME} \
  --restart=always \
  -p ${REGISTRY_PORT}:5000 \
  -v kind-registry-data:/var/lib/registry \
  registry:2

echo -e "${GREEN}Registry created and started successfully${NC}"
echo "Registry available at: localhost:${REGISTRY_PORT}"
echo ""
echo "To verify registry is working:"
echo "  curl http://localhost:${REGISTRY_PORT}/v2/_catalog"

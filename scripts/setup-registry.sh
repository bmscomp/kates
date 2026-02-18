#!/bin/bash
set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "${SCRIPT_DIR}/common.sh"

REGISTRY_NAME="kind-registry"
REGISTRY_PORT="5001"

info "Setting up local Docker registry..."

# Check if registry is already running
if [ "$(docker ps -q -f name=${REGISTRY_NAME})" ]; then
    warn "Registry '${REGISTRY_NAME}' is already running"
    echo "Registry available at: localhost:${REGISTRY_PORT}"
    exit 0
fi

# Check if registry container exists but is stopped
if [ "$(docker ps -aq -f name=${REGISTRY_NAME})" ]; then
    warn "Starting existing registry container..."
    docker start ${REGISTRY_NAME}
    info "Registry started successfully"
    echo "Registry available at: localhost:${REGISTRY_PORT}"
    exit 0
fi

# Create registry container
info "Creating new registry container..."
docker run -d \
  --name ${REGISTRY_NAME} \
  --restart=always \
  -p ${REGISTRY_PORT}:5000 \
  -v kind-registry-data:/var/lib/registry \
  registry:2

info "Registry created and started successfully"
echo "Registry available at: localhost:${REGISTRY_PORT}"
echo ""
echo "To verify registry is working:"
echo "  curl http://localhost:${REGISTRY_PORT}/v2/_catalog"

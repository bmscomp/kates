#!/bin/bash
set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "${SCRIPT_DIR}/common.sh"

REGISTRY_NAME="kind-registry"

warn "Cleaning up local Docker registry..."

if [ "$(docker ps -aq -f name=${REGISTRY_NAME})" ]; then
    step "Stopping registry container..."
    docker stop ${REGISTRY_NAME} 2>/dev/null || true
    step "Removing registry container..."
    docker rm ${REGISTRY_NAME} 2>/dev/null || true
    info "✓ Registry container removed"
else
    warn "Registry container does not exist"
fi

step "Removing registry data volume..."
docker volume rm kind-registry-data 2>/dev/null || true

step "Cleaning up Docker network..."
docker network rm kind 2>/dev/null || true

echo ""
info "✅ Registry cleanup complete"

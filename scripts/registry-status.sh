#!/bin/bash
set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "${SCRIPT_DIR}/common.sh"

REGISTRY_NAME="kind-registry"
REGISTRY_PORT="5001"
REGISTRY_URL="localhost:${REGISTRY_PORT}"

bold "Local Docker Registry Status"
echo ""

if [ ! "$(docker ps -aq -f name=${REGISTRY_NAME})" ]; then
    error "✗ Registry container does not exist"
    echo "Run ./setup-registry.sh to create it"
    exit 1
fi

if [ "$(docker ps -q -f name=${REGISTRY_NAME})" ]; then
    info "✓ Registry is running"
    echo "  Container: ${REGISTRY_NAME}"
    echo "  URL: http://${REGISTRY_URL}"
else
    warn "⚠ Registry container exists but is stopped"
    echo "Run: docker start ${REGISTRY_NAME}"
    exit 1
fi

echo ""

if curl -s http://${REGISTRY_URL}/v2/_catalog > /dev/null 2>&1; then
    info "✓ Registry is accessible"
else
    error "✗ Registry is not accessible"
    exit 1
fi

echo ""
bold "Registry Contents"

CATALOG=$(curl -s http://${REGISTRY_URL}/v2/_catalog)
IMAGE_COUNT=$(echo $CATALOG | jq -r '.repositories | length')

echo "Total images: ${IMAGE_COUNT}"
echo ""

if [ "$IMAGE_COUNT" -gt 0 ]; then
    echo "Images in registry:"
    echo $CATALOG | jq -r '.repositories[]' | while read repo; do
        TAGS=$(curl -s http://${REGISTRY_URL}/v2/${repo}/tags/list | jq -r '.tags[]?' 2>/dev/null || echo "")
        if [ -n "$TAGS" ]; then
            echo "  - ${repo}:${TAGS}"
        else
            echo "  - ${repo}"
        fi
    done
else
    warn "No images in registry"
    echo "Run ./pull-images.sh to populate the registry"
fi

echo ""
bold "Network Configuration"

if docker network inspect kind 2>/dev/null | grep -q ${REGISTRY_NAME}; then
    info "✓ Registry is connected to 'kind' network"
else
    warn "⚠ Registry is not connected to 'kind' network"
    echo "Run: docker network connect kind ${REGISTRY_NAME}"
fi

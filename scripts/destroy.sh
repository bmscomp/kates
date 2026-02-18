#!/bin/bash
set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "${SCRIPT_DIR}/common.sh"

warn "⚠️  This will destroy the Kind cluster and all deployed services."
echo ""
echo "The following resources will be removed:"
echo "  • Kind cluster '${KIND_CLUSTER_NAME}'"
echo "  • All port-forwards"
echo "  • Docker registry container 'kind-registry'"
echo "  • Registry data volume"
echo ""

if [ "${FORCE:-}" != "1" ]; then
    read -r -p "Are you sure? [y/N] " response
    if [[ ! "$response" =~ ^[Yy]$ ]]; then
        echo "Aborted."
        exit 0
    fi
fi

echo ""

# Kill all port-forwards
step "Stopping port-forwards..."
pkill -f "kubectl port-forward" 2>/dev/null || true

# Delete Kind cluster
step "Deleting Kind cluster '${KIND_CLUSTER_NAME}'..."
kind delete cluster --name "${KIND_CLUSTER_NAME}" 2>/dev/null || true

# Stop and remove the Docker registry
step "Removing Docker registry..."
docker stop kind-registry 2>/dev/null || true
docker rm kind-registry 2>/dev/null || true

# Remove registry data volume
step "Removing registry data volume..."
docker volume rm kind-registry-data 2>/dev/null || true

# Clean up the kind docker network
step "Cleaning up Docker network..."
docker network rm kind 2>/dev/null || true

echo ""
info "✅ Cluster and all resources destroyed."
echo ""
echo "To start fresh: make all"

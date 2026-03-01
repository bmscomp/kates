#!/bin/bash
set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "${SCRIPT_DIR}/common.sh"

require_cmd kind
require_cmd kubectl

info "Starting Kind Cluster Setup..."

# Source proxy configuration if it exists
if [ -f "${SCRIPT_DIR}/../proxy/proxy.conf" ]; then
    info "Loading proxy configuration..."
    set -a
    source "${SCRIPT_DIR}/../proxy/proxy.conf"
    set +a
fi

# Create cluster only if it doesn't exist; recover kubeconfig if stale
if kind get clusters 2>/dev/null | grep -q "^${KIND_CLUSTER_NAME}$"; then
    if kubectl cluster-info --context "kind-${KIND_CLUSTER_NAME}" >/dev/null 2>&1; then
        info "Cluster '${KIND_CLUSTER_NAME}' already running — skipping creation"
    else
        warn "Cluster '${KIND_CLUSTER_NAME}' exists but API unreachable — refreshing kubeconfig"
        kind export kubeconfig --name "${KIND_CLUSTER_NAME}"
    fi
else
    info "Creating Kind cluster..."
    kind create cluster --config "${SCRIPT_DIR}/../config/cluster.yaml" --name "${KIND_CLUSTER_NAME}"
fi

echo ""
info "✅ Kind Cluster Setup Complete!"
echo ""
echo "Cluster '${KIND_CLUSTER_NAME}' is ready!"
echo ""
echo "Next steps:"
echo "  - Deploy monitoring: make monitoring"
echo "  - Deploy full stack: make deploy-all"
echo "  - Check cluster: kubectl get nodes"

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

# Setup local Docker registry
info "Setting up local Docker registry..."
"${SCRIPT_DIR}/setup-registry.sh"

# Connect registry to kind network
info "Connecting registry to kind network..."
docker network create kind 2>/dev/null || true
docker network connect kind kind-registry 2>/dev/null || true

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

# Document the local registry in the cluster
info "Configuring cluster to use local registry..."
cat <<EOF | kubectl apply -f -
apiVersion: v1
kind: ConfigMap
metadata:
  name: local-registry-hosting
  namespace: kube-public
data:
  localRegistryHosting.v1: |
    host: "localhost:5001"
    help: "https://kind.sigs.k8s.io/docs/user/local-registry/"
EOF

echo ""
info "✅ Kind Cluster Setup Complete!"
echo ""
echo "Cluster '${KIND_CLUSTER_NAME}' is ready!"
echo ""
echo "Next steps:"
echo "  - Deploy monitoring: make monitoring"
echo "  - Deploy full stack: make deploy-all"
echo "  - Check cluster: kubectl get nodes"

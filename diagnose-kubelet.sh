#!/bin/bash
# Diagnostic script to check kubelet status in Kind nodes

set -e

CLUSTER_NAME="${1:-panda}"

echo "🔍 Diagnosing kubelet issues in Kind cluster: $CLUSTER_NAME"
echo ""

# Check if cluster exists
if ! kind get clusters | grep -q "^${CLUSTER_NAME}$"; then
    echo "❌ Cluster '$CLUSTER_NAME' not found"
    echo "Available clusters:"
    kind get clusters
    exit 1
fi

# Get all nodes
NODES=$(docker ps --filter "name=${CLUSTER_NAME}" --format "{{.Names}}")

if [ -z "$NODES" ]; then
    echo "❌ No nodes found for cluster '$CLUSTER_NAME'"
    exit 1
fi

echo "Found nodes: $NODES"
echo ""

for node in $NODES; do
    echo "=========================================="
    echo "📊 Diagnostics for: $node"
    echo "=========================================="
    
    echo ""
    echo "--- Kubelet Status ---"
    docker exec "$node" systemctl status kubelet --no-pager || echo "Failed to get kubelet status"
    
    echo ""
    echo "--- Kubelet Logs (last 50 lines) ---"
    docker exec "$node" journalctl -u kubelet --no-pager -n 50 || echo "Failed to get kubelet logs"
    
    echo ""
    echo "--- Container Runtime Info ---"
    docker exec "$node" crictl --runtime-endpoint=/run/containerd/containerd.sock info || echo "Failed to get runtime info"
    
    echo ""
    echo "--- Cgroup Driver ---"
    docker exec "$node" cat /var/lib/kubelet/kubeadm-flags.env 2>/dev/null || echo "Kubelet flags file not found"
    
    echo ""
    echo "--- Static Pods Status ---"
    docker exec "$node" ls -la /etc/kubernetes/manifests/ 2>/dev/null || echo "No manifests directory"
    
    echo ""
    echo "=========================================="
    echo ""
done

echo "✅ Diagnostic complete!"

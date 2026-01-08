#!/bin/bash
set -e

# Colors for output
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

echo -e "${GREEN}Patching containerd configuration on kind nodes to use local registry...${NC}"
echo ""

CLUSTER_NAME="panda"
NODES=("${CLUSTER_NAME}-control-plane" "${CLUSTER_NAME}-worker" "${CLUSTER_NAME}-worker2")

for node in "${NODES[@]}"; do
    echo -e "${YELLOW}Patching node: ${node}${NC}"
    
    # Update containerd config to use kind-registry:5000 endpoint
    docker exec "${node}" sed -i 's|endpoint = \["http://localhost:5001"\]|endpoint = ["http://kind-registry:5000"]|g' /etc/containerd/config.toml
    
    # Restart containerd to apply changes
    docker exec "${node}" systemctl restart containerd
    
    echo -e "${GREEN}✓ Node ${node} patched and containerd restarted${NC}"
done

echo ""
echo -e "${GREEN}All nodes patched successfully!${NC}"
echo ""
echo "Kind nodes can now pull images from the local registry at localhost:5001"
echo "The registry is accessible within the cluster at kind-registry:5000"
echo ""
echo "Test image pull:"
echo "  docker exec ${CLUSTER_NAME}-control-plane crictl pull localhost:5001/provectuslabs/kafka-ui:v0.7.2"

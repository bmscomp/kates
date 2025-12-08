#!/bin/bash
set -e

# Colors for output
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

echo -e "${GREEN}Starting Kind Cluster Setup...${NC}"

# Check dependencies
command -v kind >/dev/null 2>&1 || { echo >&2 "kind is required but not installed. Aborting."; exit 1; }
command -v kubectl >/dev/null 2>&1 || { echo >&2 "kubectl is required but not installed. Aborting."; exit 1; }

# Source proxy configuration if it exists
if [ -f "proxy/proxy.conf" ]; then
    echo "Loading proxy configuration..."
    set -a
    source proxy/proxy.conf
    set +a
fi

# Setup local Docker registry
echo -e "${GREEN}Setting up local Docker registry...${NC}"
./setup-registry.sh

# Connect registry to kind network (create network if it doesn't exist)
echo -e "${GREEN}Connecting registry to kind network...${NC}"
docker network create kind 2>/dev/null || true
docker network connect kind kind-registry 2>/dev/null || true

# Pull and push images to local registry
echo -e "${GREEN}Populating local registry with required images...${NC}"
./pull-images.sh

# Create Cluster
echo -e "${GREEN}Creating Kind cluster...${NC}"
kind delete cluster --name panda || true
kind create cluster --config config/cluster.yaml --name panda

# Document the local registry in the cluster
echo -e "${GREEN}Configuring cluster to use local registry...${NC}"
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
echo -e "${GREEN}========================================${NC}"
echo -e "${GREEN}✅ Kind Cluster Setup Complete!${NC}"
echo -e "${GREEN}========================================${NC}"
echo ""
echo "Cluster 'panda' is ready!"
echo ""
echo "Next steps:"
echo "  - Deploy monitoring: make monitoring"
echo "  - Deploy full stack: make deploy-all"
echo "  - Check cluster: kubectl get nodes"
echo ""

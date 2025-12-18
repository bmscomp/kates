#!/bin/bash
set -e

# Colors for output
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
RED='\033[0;31m'
NC='\033[0m' # No Color

echo -e "${GREEN}========================================${NC}"
echo -e "${GREEN}Litmus Offline Installation Setup${NC}"
echo -e "${GREEN}========================================${NC}"
echo ""

# Step 1: Setup local registry
echo -e "${BLUE}Step 1/3: Setting up local Docker registry...${NC}"
if ! curl -s http://localhost:5001/v2/_catalog > /dev/null 2>&1; then
    echo "Starting local registry..."
    ./setup-registry.sh
else
    echo -e "${GREEN}✓ Local registry already running${NC}"
fi

# Step 2: Download Helm charts
echo ""
echo -e "${BLUE}Step 2/3: Downloading Litmus Helm charts...${NC}"
if [ ! -d "./charts/litmus" ]; then
    ./download-litmus-charts.sh
else
    echo -e "${YELLOW}Charts already downloaded. To re-download, run: rm -rf ./charts/litmus && ./download-litmus-charts.sh${NC}"
fi

# Step 3: Pull all Litmus images
echo ""
echo -e "${BLUE}Step 3/3: Pulling all Litmus images to local registry...${NC}"
./pull-litmus-images.sh

echo ""
echo -e "${GREEN}========================================${NC}"
echo -e "${GREEN}Offline Setup Complete!${NC}"
echo -e "${GREEN}========================================${NC}"
echo ""
echo "All Litmus components have been downloaded and cached locally."
echo ""
echo "Summary:"
echo "  ✓ Local Docker registry running at localhost:5001"
echo "  ✓ Litmus Helm charts downloaded to ./charts/litmus"
echo "  ✓ All container images cached in local registry"
echo ""
echo "Next steps:"
echo "  1. Deploy Litmus: ./deploy-litmuschaos-offline.sh"
echo "  2. Or use Makefile: make litmus-offline"
echo ""
echo "You can now install Litmus completely offline without any external pulls!"

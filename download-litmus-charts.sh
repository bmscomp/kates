#!/bin/bash
set -e

# Colors for output
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

CHARTS_DIR="./charts"
LITMUS_CHART_DIR="${CHARTS_DIR}/litmus"

echo -e "${GREEN}Downloading Litmus Helm charts for offline installation...${NC}"

# Create charts directory if it doesn't exist
mkdir -p "${CHARTS_DIR}"

# Add Litmus Helm repository
echo -e "${BLUE}Adding LitmusChaos Helm repository...${NC}"
helm repo add litmuschaos https://litmuschaos.github.io/litmus-helm/ 2>/dev/null || true
helm repo update

# Download Litmus chart
echo -e "${BLUE}Downloading Litmus chart version 3.23.0...${NC}"
rm -rf "${LITMUS_CHART_DIR}"
helm pull litmuschaos/litmus --version 3.23.0 --untar --untardir "${CHARTS_DIR}"

# Verify download
if [ -d "${LITMUS_CHART_DIR}" ]; then
    echo -e "${GREEN}✓ Litmus chart downloaded successfully to ${LITMUS_CHART_DIR}${NC}"
    echo ""
    echo "Chart contents:"
    ls -la "${LITMUS_CHART_DIR}"
else
    echo -e "${YELLOW}Failed to download Litmus chart${NC}"
    exit 1
fi

echo ""
echo -e "${GREEN}Chart download complete!${NC}"
echo ""
echo "The Litmus Helm chart is now available locally at:"
echo "  ${LITMUS_CHART_DIR}"
echo ""
echo "You can now install Litmus offline using:"
echo "  ./deploy-litmuschaos-offline.sh"

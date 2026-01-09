#!/bin/bash
set -e

# Colors for output
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

CHARTS_DIR="./charts"
APICURIO_CHART_DIR="${CHARTS_DIR}/apicurio-registry"

echo -e "${GREEN}Deploying Apicurio Registry from local chart...${NC}"

# Check if local chart exists
if [ ! -d "${APICURIO_CHART_DIR}" ]; then
    echo -e "${YELLOW}Error: Apicurio chart not found at ${APICURIO_CHART_DIR}${NC}"
    echo "Please run ./download-apicurio-chart.sh first to download the charts"
    exit 1
fi

# Create namespace if strictly needed (usually part of kafka or separate)
# We deploy to default or separate? Let's use 'apicurio' namespace for isolation, 
# or 'kafka' to be close to Strimzi. Let's use 'apicurio'.
kubectl create namespace apicurio --dry-run=client -o yaml | kubectl apply -f -

# Install Apicurio Registry
echo -e "${GREEN}Installing Apicurio Registry...${NC}"
helm upgrade --install apicurio-registry "${APICURIO_CHART_DIR}" \
  --namespace apicurio \
  --values config/apicurio-values-offline.yaml \
  --timeout 10m \
  --wait

echo -e "${GREEN}Apicurio Registry deployment complete!${NC}"
echo ""
echo "To access Apicurio Registry:"
echo "  kubectl port-forward -n apicurio svc/apicurio-registry 8080:8080"
echo "  Then visit: http://localhost:8080"
echo ""

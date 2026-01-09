#!/bin/bash
set -e
GREEN='\033[0;32m'
NC='\033[0m'

CHARTS_DIR="./charts"
APICURIO_CHART_DIR="${CHARTS_DIR}/apicurio-registry"

echo -e "${GREEN}Downloading Apicurio Registry Helm Chart...${NC}"

# Ensure charts directory exists
# Add touk Helm repo (reliable alternative for Apicurio)
helm repo remove touk 2>/dev/null || true
helm repo add touk https://helm-charts.touk.pl/public/

# Download chart
echo -e "${GREEN}Fetching chart from touk repo...${NC}"
helm pull touk/apicurio-registry --untar --untardir "${CHARTS_DIR}"

# Rename to standard name if needed
# List to see what we got
ls -F "${CHARTS_DIR}"

if [ -d "${CHARTS_DIR}/apicurio-registry" ]; then
    rm -rf "${APICURIO_CHART_DIR}"
    mv "${CHARTS_DIR}/apicurio-registry" "${APICURIO_CHART_DIR}"
elif [ -d "${CHARTS_DIR}/apicurio-registry-helm" ]; then
     rm -rf "${APICURIO_CHART_DIR}"
     mv "${CHARTS_DIR}/apicurio-registry-helm" "${APICURIO_CHART_DIR}"
fi

echo -e "${GREEN}Chart downloaded to ${APICURIO_CHART_DIR}${NC}"
ls -F "${APICURIO_CHART_DIR}"

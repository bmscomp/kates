#!/bin/bash
set -e

# Colors for output
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

CHARTS_DIR="./charts"

echo -e "${GREEN}Downloading Helm charts for offline installation...${NC}"

# Create charts directory if it doesn't exist
mkdir -p "${CHARTS_DIR}"

# Download Litmus chart
echo -e "${BLUE}Downloading Litmus chart...${NC}"
helm repo add litmuschaos https://litmuschaos.github.io/litmus-helm/ 2>/dev/null || true
helm repo update litmuschaos
rm -rf "${CHARTS_DIR}/litmus"
helm pull litmuschaos/litmus --version 3.23.0 --untar --untardir "${CHARTS_DIR}"

# Download Strimzi (Kafka) chart
echo -e "${BLUE}Downloading Strimzi Kafka Operator chart...${NC}"
helm repo add strimzi https://strimzi.io/charts/ 2>/dev/null || true
helm repo update strimzi
rm -rf "${CHARTS_DIR}/strimzi-kafka-operator"
helm pull strimzi/strimzi-kafka-operator --version 0.49.0 --untar --untardir "${CHARTS_DIR}"

# Download kube-prometheus-stack chart
echo -e "${BLUE}Downloading kube-prometheus-stack chart...${NC}"
helm repo add prometheus-community https://prometheus-community.github.io/helm-charts 2>/dev/null || true
helm repo update prometheus-community
rm -rf "${CHARTS_DIR}/kube-prometheus-stack"
helm pull prometheus-community/kube-prometheus-stack --version 79.11.0 --untar --untardir "${CHARTS_DIR}"

# Verify downloads
echo ""
echo -e "${GREEN}Downloaded charts:${NC}"
ls -la "${CHARTS_DIR}"

echo ""
echo -e "${GREEN}Chart download complete!${NC}"
echo ""
echo "Charts are now available locally at: ${CHARTS_DIR}/"
echo "  - litmus"
echo "  - strimzi-kafka-operator"
echo "  - kube-prometheus-stack"

#!/bin/bash
set -e

# Colors
GREEN='\033[0;32m'
NC='\033[0m'

CHARTS_DIR="./charts"
mkdir -p "${CHARTS_DIR}"

# Velero Chart
echo -e "${GREEN}Downloading Velero Chart...${NC}"
helm repo add vmware-tanzu https://vmware-tanzu.github.io/helm-charts
helm repo update
helm pull vmware-tanzu/velero --destination "${CHARTS_DIR}" --untar

# MinIO Chart (Using Bitnami for stability/consistency)
echo -e "${GREEN}Downloading MinIO Chart...${NC}"
helm repo add bitnami https://charts.bitnami.com/bitnami
helm repo update
helm pull bitnami/minio --destination "${CHARTS_DIR}" --untar

echo -e "${GREEN}Charts downloaded successfully!${NC}"
ls -l "${CHARTS_DIR}" | grep -E "velero|minio"

#!/bin/bash
set -e

# Colors
GREEN='\033[0;32m'
BLUE='\033[0;34m'
NC='\033[0m'

NAMESPACE="velero"
CHARTS_DIR="./charts"

echo -e "${BLUE}Deploying MinIO and Velero...${NC}"

# Create namespace
kubectl create namespace ${NAMESPACE} --dry-run=client -o yaml | kubectl apply -f -

# Deploy MinIO
echo -e "${GREEN}Installing MinIO...${NC}"
helm upgrade --install minio "${CHARTS_DIR}/minio" \
  --namespace ${NAMESPACE} \
  --values config/minio-values-offline.yaml \
  --wait \
  --timeout 5m

echo -e "${GREEN}MinIO deployed successfully.${NC}"

# Deploy Velero
echo -e "${GREEN}Installing Velero...${NC}"
helm upgrade --install velero "${CHARTS_DIR}/velero" \
  --namespace ${NAMESPACE} \
  --values config/velero-values-offline.yaml \
  --wait \
  --timeout 5m

echo -e "${GREEN}Velero deployment complete!${NC}"
echo ""
echo "Verify status:"
echo "  kubectl get pods -n ${NAMESPACE}"
echo ""
echo "Trigger backup:"
echo "  velero backup create kafka-backup-manual --include-namespaces kafka --wait"

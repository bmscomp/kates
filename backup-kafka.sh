#!/bin/bash
set -e

# Colors
GREEN='\033[0;32m'
NC='\033[0m'

BACKUP_NAME="kafka-configs-$(date +%Y%m%d-%H%M%S)"

echo -e "${GREEN}Triggering Velero backup: ${BACKUP_NAME}...${NC}"

# Ensure velero CLI is available (or use kubectl plugin if needed, but assuming velero CLI is installed or aliased)
# If velero is not in path, we might need instructions. For now assumes velero is installed.
# If not, we can run it via a pod? No, CLI is standard. 
# Alternatively, I can create a Schedule on demand or a Backup resource via kubectl.

kubectl create -f - <<EOF
apiVersion: velero.io/v1
kind: Backup
metadata:
  name: ${BACKUP_NAME}
  namespace: velero
spec:
  includedNamespaces:
  - kafka
  - apicurio
  includedResources:
  - kafkatopics
  - kafkausers
  - kafkanodepools
  - kafkas
  - configmaps
  - secrets
  ttl: 72h
  storageLocation: default
EOF

echo -e "${GREEN}Backup CR created: ${BACKUP_NAME}${NC}"
echo "Use 'velero backup describe ${BACKUP_NAME}' or check status with kubectl:"
echo "kubectl get backup -n velero ${BACKUP_NAME} -o wide"

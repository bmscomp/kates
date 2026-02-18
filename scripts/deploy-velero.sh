#!/bin/bash
set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "${SCRIPT_DIR}/common.sh"

NAMESPACE="velero"

info "Deploying MinIO and Velero..."

# Skip if already running
if deployment_exists velero velero; then
    if kubectl rollout status deployment/velero -n velero --timeout=5s &>/dev/null; then
        warn "Velero is already deployed and running — skipping"
        exit 0
    fi
fi

ensure_namespace ${NAMESPACE}

info "Installing MinIO..."
helm upgrade --install minio "${CHARTS_DIR}/minio" \
  --namespace ${NAMESPACE} \
  --values config/minio-values-offline.yaml \
  --wait \
  --timeout 5m

info "MinIO deployed successfully."

info "Installing Velero..."
helm upgrade --install velero "${CHARTS_DIR}/velero" \
  --namespace ${NAMESPACE} \
  --values config/velero-values-offline.yaml \
  --wait \
  --timeout 5m

info "✅ Velero deployment complete!"
echo ""
echo "Verify:  kubectl get pods -n ${NAMESPACE}"
echo "Backup:  velero backup create kafka-backup-manual --include-namespaces kafka --wait"

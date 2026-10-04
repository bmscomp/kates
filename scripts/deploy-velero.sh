#!/bin/bash
set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "${SCRIPT_DIR}/common.sh"

NAMESPACE="kafka"

info "Deploying SeaweedFS and Velero..."

# Skip if already running
if deployment_exists velero velero; then
    if kubectl rollout status deployment/velero -n velero --timeout=5s &>/dev/null; then
        warn "Velero is already deployed and running — skipping"
        exit 0
    fi
fi

ensure_namespace ${NAMESPACE}

# The object store Velero writes to. It was MinIO until MinIO stopped serving
# its images anonymously; config/velero/seaweedfs.yaml says why and what
# replaces it. A Job's pod template cannot change in place, so the bucket Job
# is recreated on every run; it passes at once when the bucket is there.
info "Installing SeaweedFS..."
kubectl delete job velero-seaweedfs-bucket -n ${NAMESPACE} --ignore-not-found >/dev/null
kubectl apply -n ${NAMESPACE} -f config/velero/seaweedfs.yaml
kubectl rollout status deployment/velero-seaweedfs -n ${NAMESPACE} --timeout=5m
if ! kubectl wait --for=condition=complete job/velero-seaweedfs-bucket -n ${NAMESPACE} --timeout=5m; then
    error "The velero bucket was not created — the Job's last lines:"
    kubectl logs job/velero-seaweedfs-bucket -n ${NAMESPACE} --tail=20 >&2 || true
    exit 1
fi

info "SeaweedFS deployed successfully."

# Not removed automatically: uninstalling the old chart deletes its volume, and
# with it every backup Velero wrote there.
if helm status minio -n ${NAMESPACE} &>/dev/null; then
    warn "The MinIO release from an earlier 'make velero' is still installed. Velero no longer"
    warn "writes to it; its backups stay there until you remove it: helm uninstall minio -n ${NAMESPACE}"
fi

info "Installing Velero..."
helm upgrade --install velero "${CHARTS_DIR}/velero" \
  --namespace ${NAMESPACE} \
  --values config/velero/velero-values.yaml \
  --wait \
  --timeout 5m

info "✅ Velero deployment complete!"
echo ""
echo "Verify:  kubectl get pods -n ${NAMESPACE}"
echo "Backup:  velero backup create kafka-backup-manual --include-namespaces kafka --wait"

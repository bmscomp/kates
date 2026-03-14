#!/bin/bash
set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "${SCRIPT_DIR}/common.sh"
source "${SCRIPT_DIR}/../versions.env"

info "Deploying cert-manager ${CERT_MANAGER_VERSION}..."

ensure_namespace cert-manager

if kubectl get deployment cert-manager -n cert-manager &>/dev/null && \
   kubectl rollout status deployment/cert-manager -n cert-manager --timeout=5s &>/dev/null; then
    warn "cert-manager is already running — skipping deploy"
    exit 0
fi

info "Adding Jetstack Helm repo..."
helm repo add jetstack https://charts.jetstack.io 2>/dev/null || true
helm repo update jetstack

info "Installing cert-manager..."
helm upgrade --install cert-manager jetstack/cert-manager \
  --version "${CERT_MANAGER_VERSION}" \
  --namespace cert-manager \
  --set crds.enabled=true \
  --set image.pullPolicy=IfNotPresent \
  --set webhook.image.pullPolicy=IfNotPresent \
  --set cainjector.image.pullPolicy=IfNotPresent \
  --set startupapicheck.image.pullPolicy=IfNotPresent \
  --timeout 5m \
  --wait

info "Waiting for cert-manager webhook to be ready..."
kubectl wait --for=condition=Ready pods -l app.kubernetes.io/instance=cert-manager \
  -n cert-manager --timeout=120s

info "Creating self-signed ClusterIssuer..."
kubectl apply -f - <<'EOF'
apiVersion: cert-manager.io/v1
kind: ClusterIssuer
metadata:
  name: selfsigned-issuer
spec:
  selfSigned: {}
EOF

info "Waiting for ClusterIssuer to be ready..."
kubectl wait --for=condition=Ready clusterissuer/selfsigned-issuer --timeout=30s

info "✅ cert-manager ${CERT_MANAGER_VERSION} deployed with self-signed ClusterIssuer"

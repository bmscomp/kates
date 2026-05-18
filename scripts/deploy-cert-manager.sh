#!/bin/bash
set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "${SCRIPT_DIR}/common.sh"
source "${SCRIPT_DIR}/../versions.env"

info "Deploying cert-manager ${CERT_MANAGER_VERSION}..."

ensure_namespace kafka

EXISTING_CERT_MANAGER_INFO=$(kubectl get deployment -A -o custom-columns=NS:.metadata.namespace,NAME:.metadata.name --no-headers 2>/dev/null | awk '/cert-manager/ {print $1, $2}' | head -n1)
EXISTING_CERT_MANAGER_NS=$(echo "$EXISTING_CERT_MANAGER_INFO" | awk '{print $1}')
EXISTING_CERT_MANAGER_NAME=$(echo "$EXISTING_CERT_MANAGER_INFO" | awk '{print $2}')

if [ -n "${EXISTING_CERT_MANAGER_NS}" ]; then
    info "✅ cert-manager is already deployed in namespace: ${EXISTING_CERT_MANAGER_NS} — skipping helm install"
else
    info "Adding Jetstack Helm repo..."
    helm repo add jetstack https://charts.jetstack.io 2>/dev/null || true
    helm repo update jetstack

    info "Installing cert-manager..."
    helm upgrade --install cert-manager jetstack/cert-manager \
      --version "${CERT_MANAGER_VERSION}" \
      --namespace kafka \
      --set crds.enabled=true \
      --set image.pullPolicy=IfNotPresent \
      --set webhook.image.pullPolicy=IfNotPresent \
      --set cainjector.image.pullPolicy=IfNotPresent \
      --set startupapicheck.image.pullPolicy=IfNotPresent \
      --set resources.requests.cpu=100m \
      --set resources.requests.memory=256Mi \
      --set resources.limits.cpu=500m \
      --set resources.limits.memory=512Mi \
      --set webhook.resources.requests.cpu=100m \
      --set webhook.resources.requests.memory=256Mi \
      --set webhook.resources.limits.cpu=500m \
      --set webhook.resources.limits.memory=512Mi \
      --set cainjector.resources.requests.cpu=100m \
      --set cainjector.resources.requests.memory=256Mi \
      --set cainjector.resources.limits.cpu=500m \
      --set cainjector.resources.limits.memory=512Mi \
      --timeout 5m

    EXISTING_CERT_MANAGER_NS="kafka"
    EXISTING_CERT_MANAGER_NAME="cert-manager"
fi

info "Waiting for cert-manager to be ready..."
kubectl rollout status deployment/${EXISTING_CERT_MANAGER_NAME} -n "${EXISTING_CERT_MANAGER_NS}" --timeout=120s

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

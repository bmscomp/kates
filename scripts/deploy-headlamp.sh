#!/bin/bash
set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "${SCRIPT_DIR}/common.sh"

info "Deploying Headlamp..."

if deployment_exists headlamp headlamp; then
    if kubectl rollout status deployment/headlamp -n headlamp --timeout=5s &>/dev/null; then
        warn "Headlamp is already deployed and running — skipping"
        exit 0
    fi
fi

ensure_namespace headlamp

info "Installing Headlamp via Helm..."
helm upgrade --install headlamp "${CHARTS_DIR}/headlamp" \
    --namespace headlamp \
    --wait \
    --timeout 120s

info "Waiting for Headlamp to be ready..."
kubectl rollout status deployment/headlamp -n headlamp --timeout=120s

TOKEN=$(kubectl create token headlamp -n headlamp --duration=8760h 2>/dev/null || echo "")

info "✅ Headlamp deployment complete!"
echo ""
info "Access via port-forward:"
echo "  kubectl port-forward svc/headlamp 30084:80 -n headlamp"
echo "  Open: http://localhost:30084"
if [ -n "$TOKEN" ]; then
    echo ""
    info "Auth token (valid 1 year):"
    echo "  $TOKEN"
fi

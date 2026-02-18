#!/bin/bash
set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "${SCRIPT_DIR}/common.sh"

APICURIO_CHART_DIR="${CHARTS_DIR}/apicurio-registry"

info "Deploying Apicurio Registry..."

require_chart "${APICURIO_CHART_DIR}" "apicurio-registry"

# Skip if already running
if deployment_exists apicurio-registry apicurio; then
    if kubectl rollout status deployment/apicurio-registry -n apicurio --timeout=5s &>/dev/null; then
        warn "Apicurio Registry is already deployed and running — skipping"
        exit 0
    fi
fi

ensure_namespace apicurio

info "Installing Apicurio Registry..."
helm upgrade --install apicurio-registry "${APICURIO_CHART_DIR}" \
  --namespace apicurio \
  --values config/apicurio-values-offline.yaml \
  --timeout 10m \
  --wait

info "✅ Apicurio Registry deployment complete!"

#!/bin/bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "${SCRIPT_DIR}/common.sh"

APICURIO_CHART_DIR="${CHARTS_DIR}/apicurio-registry"

info "Deploying Apicurio Registry..."

require_chart "${APICURIO_CHART_DIR}" "apicurio-registry"

# Skip if already running
if deployment_exists apicurio-registry kafka; then
    if kubectl rollout status deployment/apicurio-registry -n kafka --timeout=5s &>/dev/null; then
        warn "Apicurio Registry is already deployed and running — skipping"
        exit 0
    fi
fi

ensure_namespace kafka

# The chart reads the Kafka SASL JAAS config directly from the Strimzi-managed
# KafkaUser Secret (key sasl.jaas.config) via a secretKeyRef — no need to fetch
# or inject the password here. Just make sure the Strimzi User Operator has
# produced the Secret before the pod starts (it mounts it at boot).
info "Waiting for the apicurio-registry KafkaUser Secret..."
kubectl wait secret/apicurio-registry -n kafka \
  --for=jsonpath='{.data.sasl\.jaas\.config}' --timeout=120s \
  || warn "KafkaUser Secret not ready yet — the pod will retry until it appears"

info "Installing Apicurio Registry..."
helm upgrade --install apicurio-registry "${APICURIO_CHART_DIR}" \
  --namespace kafka \
  --values config/apicurio/apicurio-values.yaml \
  --timeout 10m

info "✅ Apicurio Registry deployment complete!"

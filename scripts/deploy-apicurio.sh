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

ensure_namespace kafka

info "Fetching Apicurio Registry Kafka credentials..."
# Wait for the Strimzi User Operator to generate the secret
kubectl wait secret/apicurio-registry -n kafka --for=jsonpath='{.data.password}' --timeout=60s || true
APICURIO_KAFKA_PASSWORD=$(kubectl get secret apicurio-registry -n kafka -o jsonpath='{.data.password}' 2>/dev/null | base64 -d || echo "")

if [ -z "$APICURIO_KAFKA_PASSWORD" ]; then
    warn "Apicurio Registry Kafka password not found. It may fail to connect."
fi

info "Installing Apicurio Registry..."
helm upgrade --install apicurio-registry "${APICURIO_CHART_DIR}" \
  --namespace kafka \
  --values config/apicurio/apicurio-values.yaml \
  --set "env[0].name=KAFKASQL_SECURITY_PROTOCOL" \
  --set "env[0].value=SASL_PLAINTEXT" \
  --set "env[1].name=KAFKASQL_SASL_MECHANISM" \
  --set "env[1].value=SCRAM-SHA-512" \
  --set "env[2].name=KAFKASQL_SASL_JAAS_CONFIG" \
  --set "env[2].value=org.apache.kafka.common.security.scram.ScramLoginModule required username=\"apicurio-registry\" password=\"${APICURIO_KAFKA_PASSWORD}\";" \
  --timeout 10m

info "✅ Apicurio Registry deployment complete!"

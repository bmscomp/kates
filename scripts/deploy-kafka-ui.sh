#!/bin/bash
set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "${SCRIPT_DIR}/common.sh"

info "Deploying Kafka UI..."

# Skip if already running
if deployment_exists kafka-ui kafka; then
    if kubectl rollout status deployment/kafka-ui -n kafka --timeout=5s &>/dev/null; then
        warn "Kafka UI is already deployed and running — skipping"
        exit 0
    fi
fi

kubectl apply -f config/kafka-ui/kafka-ui.yaml

info "Waiting for Kafka UI to be ready..."
kubectl wait --for=condition=available --timeout=120s deployment/kafka-ui -n kafka

info "✅ Kafka UI deployment complete!"

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

# Wait for Strimzi Entity Operator to create the kafka-ui Secret
info "Waiting for kafka-ui Secret (created by Strimzi Entity Operator)..."
MAX_SECRET_WAIT=180
ELAPSED=0
while ! kubectl get secret kafka-ui -n kafka &>/dev/null; do
    if [ $ELAPSED -ge $MAX_SECRET_WAIT ]; then
        error "Timed out waiting for kafka-ui Secret after ${MAX_SECRET_WAIT}s"
        error "Ensure KafkaUser 'kafka-ui' is applied and the Entity Operator is running:"
        error "  kubectl get pods -n kafka -l strimzi.io/name=krafter-entity-operator"
        error "  kubectl get kafkauser kafka-ui -n kafka"
        exit 1
    fi
    sleep 5
    ELAPSED=$((ELAPSED + 5))
    info "  still waiting... (${ELAPSED}s)"
done
info "Secret kafka-ui found"

kubectl apply -f config/kafka-ui/kafka-ui.yaml

info "Waiting for Kafka UI to be ready..."
kubectl wait --for=condition=available --timeout=120s deployment/kafka-ui -n kafka

info "✅ Kafka UI deployment complete!"


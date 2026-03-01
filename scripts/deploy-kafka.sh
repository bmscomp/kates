#!/bin/bash
set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "${SCRIPT_DIR}/common.sh"

STRIMZI_CHART_DIR="${CHARTS_DIR}/strimzi-kafka-operator"

info "Deploying Kafka Strimzi Cluster..."

require_chart "${STRIMZI_CHART_DIR}" "strimzi-kafka-operator"

ensure_namespace kafka

# Skip if Kafka cluster is already running and healthy
if kubectl get kafka krafter -n kafka &>/dev/null && \
   kubectl wait kafka/krafter --for=condition=Ready --timeout=5s -n kafka &>/dev/null; then
    warn "Kafka cluster 'krafter' is already running and ready — skipping deploy"
    echo "To force redeploy, run: kubectl delete kafka krafter -n kafka"
    exit 0
fi

info "Installing Strimzi Operator from local chart..."
helm upgrade --install strimzi-kafka-operator "${STRIMZI_CHART_DIR}" \
  --namespace kafka \
  --set watchAnyNamespace=true \
  --set defaultImageTag=0.50.1 \
  --set image.imagePullPolicy=IfNotPresent \
  --timeout 10m \
  --wait

info "Applying Metrics Configuration..."
kubectl apply -f config/kafka-metrics.yaml

info "Creating Zone-Specific Storage Classes..."
kubectl apply -f config/storage-classes.yaml

# Cleanup old cluster if exists (before applying new one)
kubectl delete kafka my-cluster -n kafka --ignore-not-found
kubectl delete kafkanodepool dual-role -n kafka --ignore-not-found
kubectl delete pvc -l strimzi.io/cluster=krafter -n kafka --ignore-not-found
kubectl delete pvc -l strimzi.io/cluster=my-cluster -n kafka --ignore-not-found

info "Deploying Kafka Cluster (KRaft)..."
kubectl apply -f config/kafka.yaml

info "Applying Kafka Dashboards..."
kubectl apply -f config/kafka-dashboard.yaml
kubectl apply -f config/kafka-performance-dashboard.yaml
kubectl apply -f config/kafka-jvm-dashboard.yaml
kubectl apply -f config/kafka-perf-test-dashboard.yaml
kubectl apply -f config/kafka-working-dashboard.yaml
kubectl apply -f config/kafka-comprehensive-dashboard.yaml
kubectl apply -f config/kafka-all-metrics-dashboard.yaml
kubectl apply -f config/kafka-perf-global-dashboard.yaml

info "Waiting for Kafka cluster to be ready (this may take a few minutes)..."
kubectl wait kafka/krafter --for=condition=Ready --timeout=300s -n kafka 

info "✅ Kafka deployment complete!"
echo "Check the 'Kafka Cluster Health' dashboard in Grafana."

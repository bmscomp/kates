#!/bin/bash
set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "${SCRIPT_DIR}/common.sh"
source "${SCRIPT_DIR}/../versions.env"

info "Deploying Kafka Strimzi Cluster..."

ensure_namespace kafka

# Skip if Kafka cluster is already running and healthy
if kubectl get kafka krafter -n kafka &>/dev/null && \
   kubectl wait kafka/krafter --for=condition=Ready --timeout=5s -n kafka &>/dev/null; then
    warn "Kafka cluster 'krafter' is already running and ready — skipping deploy"
    echo "To force redeploy, run: kubectl delete kafka krafter -n kafka"
    exit 0
fi

info "Installing Strimzi Operator from remote chart (v${STRIMZI_VERSION})..."
helm repo add strimzi https://strimzi.io/charts/ 2>/dev/null || true
helm repo update strimzi
helm upgrade --install strimzi-kafka-operator strimzi/strimzi-kafka-operator \
  --version "${STRIMZI_VERSION}" \
  --namespace kafka \
  --set watchAnyNamespace=true \
  --set image.imagePullPolicy=IfNotPresent \
  --timeout 10m \
  --wait

# Drain Cleaner requires cert-manager for webhook TLS (optional component).
# Uncomment if cert-manager is installed.
# info "Installing Strimzi Drain Cleaner..."
# helm upgrade --install strimzi-drain-cleaner strimzi/strimzi-drain-cleaner \
#   --version 1.5.0 \
#   --namespace strimzi-drain-cleaner \
#   --create-namespace \
#   --set certManager.create=true \
#   --set image.imagePullPolicy=IfNotPresent \
#   --wait || warn "Drain Cleaner install skipped"

info "Applying Metrics Configuration..."
kubectl apply -f config/kafka/kafka-metrics.yaml

info "Creating Zone-Specific Storage Classes..."
kubectl apply -f config/storage/storage-classes.yaml

# Cleanup old cluster if exists (before applying new one)
kubectl delete kafka my-cluster -n kafka --ignore-not-found
kubectl delete kafkanodepool dual-role -n kafka --ignore-not-found
kubectl delete pvc -l strimzi.io/cluster=krafter -n kafka --ignore-not-found
kubectl delete pvc -l strimzi.io/cluster=my-cluster -n kafka --ignore-not-found

info "Deploying Kafka Cluster (KRaft)..."
kubectl apply -f config/kafka/kafka.yaml

info "Applying Kafka Dashboards..."
kubectl apply -f config/monitoring/kafka-dashboard.yaml
kubectl apply -f config/monitoring/kafka-performance-dashboard.yaml
kubectl apply -f config/monitoring/kafka-jvm-dashboard.yaml
kubectl apply -f config/monitoring/kafka-perf-test-dashboard.yaml
kubectl apply -f config/monitoring/kafka-working-dashboard.yaml
kubectl apply -f config/monitoring/kafka-comprehensive-dashboard.yaml
kubectl apply -f config/monitoring/kafka-all-metrics-dashboard.yaml
kubectl apply -f config/monitoring/kafka-perf-global-dashboard.yaml

info "Waiting for Kafka cluster to be ready (this may take a few minutes)..."
kubectl wait kafka/krafter --for=condition=Ready --timeout=300s -n kafka 

info "Applying Kafka Users (SCRAM credentials)..."
kubectl apply -f config/kafka/kafka-users.yaml

info "Applying Kafka Topics..."
kubectl apply -f config/kafka/kafka-topics.yaml

info "Waiting for user secrets to be created..."
kubectl wait kafkauser --all --for=condition=Ready --timeout=60s -n kafka

info "✅ Kafka deployment complete!"
echo "Check the 'Kafka Cluster Health' dashboard in Grafana."

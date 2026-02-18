#!/bin/bash
set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "${SCRIPT_DIR}/common.sh"
source "${SCRIPT_DIR}/../versions.env"
source "${SCRIPT_DIR}/test-common.sh"

TOPIC="perf-test"
NAMESPACE="performance"

bold "Kafka Baseline Performance Test (1M messages)"
echo ""

ensure_namespace "${NAMESPACE}"
cleanup_previous_jobs "baseline"

info "Creating test topic '${TOPIC}'..."
kubectl run kafka-topics-tmp --image="${KAFKA_IMAGE}" \
  --rm -i --restart=Never -n kafka -- \
  bin/kafka-topics.sh --create \
  --bootstrap-server "${BOOTSTRAP}" \
  --topic "${TOPIC}" \
  --partitions 3 \
  --replication-factor 3 \
  --if-not-exists

info "Deploying producer (1 million messages)..."
cat <<EOF | kubectl apply -f -
apiVersion: batch/v1
kind: Job
metadata:
  name: baseline-producer-1
  namespace: ${NAMESPACE}
  labels:
    perf-test: baseline
    perf-role: producer
spec:
  backoffLimit: 0
  template:
    metadata:
      labels:
        app: kafka-perf-producer
    spec:
      restartPolicy: Never
      containers:
      - name: producer
        image: ${KAFKA_IMAGE}
        command:
        - /bin/bash
        - -c
        - |
          bin/kafka-producer-perf-test.sh \
            --topic ${TOPIC} \
            --num-records 1000000 \
            --record-size 1024 \
            --throughput -1 \
            --producer-props \
              bootstrap.servers=${BOOTSTRAP} \
              acks=all \
              batch.size=16384 \
              linger.ms=10
EOF

warn "Waiting for producer to start..."
sleep 5

info "Monitoring producer..."
kubectl wait --for=condition=complete --timeout=600s job/baseline-producer-1 -n "${NAMESPACE}" 2>/dev/null || true

info "Producer Results:"
kubectl logs -n "${NAMESPACE}" job/baseline-producer-1 | tail -20

info "Deploying consumer..."
cat <<EOF | kubectl apply -f -
apiVersion: batch/v1
kind: Job
metadata:
  name: baseline-consumer-1
  namespace: ${NAMESPACE}
  labels:
    perf-test: baseline
    perf-role: consumer
spec:
  backoffLimit: 0
  template:
    metadata:
      labels:
        app: kafka-perf-consumer
    spec:
      restartPolicy: Never
      containers:
      - name: consumer
        image: ${KAFKA_IMAGE}
        command:
        - /bin/bash
        - -c
        - |
          bin/kafka-consumer-perf-test.sh \
            --topic ${TOPIC} \
            --bootstrap-server ${BOOTSTRAP} \
            --messages 1000000 \
            --threads 1 \
            --group perf-test-group \
            --show-detailed-stats
EOF

warn "Waiting for consumer to complete..."
kubectl wait --for=condition=complete --timeout=600s job/baseline-consumer-1 -n "${NAMESPACE}" 2>/dev/null || true

info "Consumer Results:"
kubectl logs -n "${NAMESPACE}" job/baseline-consumer-1 | tail -20

show_cleanup_hint "baseline" "${TOPIC}"

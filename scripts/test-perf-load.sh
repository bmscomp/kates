#!/bin/bash
set -e

# Load Testing — validates system under expected production workload
#
# Environment variables:
#   PRODUCERS          Number of concurrent producers (default: 5)
#   CONSUMERS          Number of concurrent consumers (default: 3)
#   RECORDS_PER_PROD   Records each producer sends (default: 200000)
#   RECORD_SIZE        Record size in bytes (default: 1024)
#   THROUGHPUT_PER     Per-producer target msg/sec (default: 5000)
#   TOPIC              Topic name (default: load-test)

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "${SCRIPT_DIR}/common.sh"
source "${SCRIPT_DIR}/../versions.env"
source "${SCRIPT_DIR}/test-common.sh"

PRODUCERS="${PRODUCERS:-5}"
CONSUMERS="${CONSUMERS:-3}"
RECORDS_PER_PROD="${RECORDS_PER_PROD:-200000}"
RECORD_SIZE="${RECORD_SIZE:-1024}"
THROUGHPUT_PER="${THROUGHPUT_PER:-5000}"
TOPIC="${TOPIC:-load-test}"
TOTAL_RECORDS=$((PRODUCERS * RECORDS_PER_PROD))
RECORDS_PER_CONSUMER=$((TOTAL_RECORDS / CONSUMERS))

bold "LOAD TESTING"
echo ""
echo "Configuration:"
echo "  Producers:      $PRODUCERS (each sending $RECORDS_PER_PROD records)"
echo "  Consumers:      $CONSUMERS"
echo "  Record size:    $RECORD_SIZE bytes"
echo "  Target rate:    $THROUGHPUT_PER msg/sec per producer ($((PRODUCERS * THROUGHPUT_PER)) aggregate)"
echo "  Topic:          $TOPIC"
echo ""

cleanup_previous_jobs "load"
create_test_topic "$TOPIC"

info "Deploying $PRODUCERS producer Jobs..."
for i in $(seq 1 "$PRODUCERS"); do
cat <<EOF | kubectl apply -f -
apiVersion: batch/v1
kind: Job
metadata:
  name: load-producer-$i
  namespace: $NAMESPACE
  labels:
    perf-test: load
    perf-role: producer
spec:
  backoffLimit: 0
  template:
    spec:
      restartPolicy: Never
      containers:
      - name: producer
        image: $IMAGE
        imagePullPolicy: IfNotPresent
        command: ["/bin/bash", "-c"]
        args:
        - |
          bin/kafka-producer-perf-test.sh \
            --topic $TOPIC \
            --num-records $RECORDS_PER_PROD \
            --record-size $RECORD_SIZE \
            --throughput $THROUGHPUT_PER \
            --producer-props \
              bootstrap.servers=$BOOTSTRAP \
              acks=all \
              batch.size=65536 \
              linger.ms=5 \
              compression.type=lz4
EOF
done

info "Deploying $CONSUMERS consumer Jobs..."
for i in $(seq 1 "$CONSUMERS"); do
cat <<EOF | kubectl apply -f -
apiVersion: batch/v1
kind: Job
metadata:
  name: load-consumer-$i
  namespace: $NAMESPACE
  labels:
    perf-test: load
    perf-role: consumer
spec:
  backoffLimit: 0
  template:
    spec:
      restartPolicy: Never
      containers:
      - name: consumer
        image: $IMAGE
        imagePullPolicy: IfNotPresent
        command: ["/bin/bash", "-c"]
        args:
        - |
          bin/kafka-consumer-perf-test.sh \
            --topic $TOPIC \
            --bootstrap-server $BOOTSTRAP \
            --messages $RECORDS_PER_CONSUMER \
            --group load-test-group \
            --show-detailed-stats
EOF
done

wait_for_jobs "load" "producer" "$PRODUCERS"
print_job_results "load" "producer" "$PRODUCERS"

wait_for_jobs "load" "consumer" "$CONSUMERS"
print_job_results "load" "consumer" "$CONSUMERS"

show_cleanup_hint "load" "$TOPIC"

#!/bin/bash
set -e

# Load Testing — validates system under expected production workload
# Deploys multiple concurrent producer and consumer Jobs
#
# Environment variables:
#   PRODUCERS          Number of concurrent producers (default: 5)
#   CONSUMERS          Number of concurrent consumers (default: 3)
#   RECORDS_PER_PROD   Records each producer sends (default: 200000)
#   RECORD_SIZE        Record size in bytes (default: 1024)
#   THROUGHPUT_PER     Per-producer target msg/sec (default: 5000)
#   TOPIC              Topic name (default: load-test)

GREEN='\033[0;32m'
YELLOW='\033[1;33m'
RED='\033[0;31m'
NC='\033[0m'

PRODUCERS="${PRODUCERS:-5}"
CONSUMERS="${CONSUMERS:-3}"
RECORDS_PER_PROD="${RECORDS_PER_PROD:-200000}"
RECORD_SIZE="${RECORD_SIZE:-1024}"
THROUGHPUT_PER="${THROUGHPUT_PER:-5000}"
TOPIC="${TOPIC:-load-test}"
NAMESPACE="kafka"
IMAGE="quay.io/strimzi/kafka:0.49.0-kafka-4.1.1"
BOOTSTRAP="krafter-kafka-bootstrap.kafka.svc:9092"
TOTAL_RECORDS=$((PRODUCERS * RECORDS_PER_PROD))
RECORDS_PER_CONSUMER=$((TOTAL_RECORDS / CONSUMERS))

echo -e "${GREEN}╔════════════════════════════════════════╗${NC}"
echo -e "${GREEN}║        LOAD TESTING                    ║${NC}"
echo -e "${GREEN}╚════════════════════════════════════════╝${NC}"
echo ""
echo "Configuration:"
echo "  Producers:      $PRODUCERS (each sending $RECORDS_PER_PROD records)"
echo "  Consumers:      $CONSUMERS"
echo "  Record size:    $RECORD_SIZE bytes"
echo "  Target rate:    $THROUGHPUT_PER msg/sec per producer (${PRODUCERS}x = $((PRODUCERS * THROUGHPUT_PER)) aggregate)"
echo "  Topic:          $TOPIC"
echo ""

# Create topic
echo -e "${GREEN}Creating topic '$TOPIC'...${NC}"
kubectl exec -n $NAMESPACE krafter-pool-alpha-0 -- \
  bin/kafka-topics.sh --create --if-not-exists \
    --bootstrap-server localhost:9092 \
    --topic $TOPIC \
    --partitions 3 \
    --replication-factor 3 \
    --config min.insync.replicas=2

# Deploy producer Jobs
echo -e "${GREEN}Deploying $PRODUCERS producer Jobs...${NC}"
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
        imagePullPolicy: Never
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

# Deploy consumer Jobs
echo -e "${GREEN}Deploying $CONSUMERS consumer Jobs...${NC}"
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
        imagePullPolicy: Never
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

# Wait for producers
echo -e "${YELLOW}Waiting for producers to complete (timeout 600s)...${NC}"
for i in $(seq 1 "$PRODUCERS"); do
  kubectl wait --for=condition=complete --timeout=600s "job/load-producer-$i" -n $NAMESPACE 2>/dev/null || true
done

# Print producer results
echo -e "${GREEN}Producer Results:${NC}"
for i in $(seq 1 "$PRODUCERS"); do
  echo -e "${YELLOW}--- Producer $i ---${NC}"
  kubectl logs -n $NAMESPACE "job/load-producer-$i" | tail -5
done

# Wait for consumers
echo -e "${YELLOW}Waiting for consumers to complete (timeout 600s)...${NC}"
for i in $(seq 1 "$CONSUMERS"); do
  kubectl wait --for=condition=complete --timeout=600s "job/load-consumer-$i" -n $NAMESPACE 2>/dev/null || true
done

# Print consumer results
echo -e "${GREEN}Consumer Results:${NC}"
for i in $(seq 1 "$CONSUMERS"); do
  echo -e "${YELLOW}--- Consumer $i ---${NC}"
  kubectl logs -n $NAMESPACE "job/load-consumer-$i" | tail -5
done

echo ""
echo -e "${GREEN}Load test completed!${NC}"
echo ""
echo "Cleanup:"
echo "  kubectl delete jobs -n $NAMESPACE -l perf-test=load"
echo "  kubectl exec -n $NAMESPACE krafter-pool-alpha-0 -- bin/kafka-topics.sh --delete --bootstrap-server localhost:9092 --topic $TOPIC"

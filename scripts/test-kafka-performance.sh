#!/bin/bash
set -e

# Colors for output
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

echo -e "${GREEN}Starting Kafka Performance Test...${NC}"

# Create performance namespace
echo -e "${GREEN}Creating performance namespace...${NC}"
kubectl create namespace performance --dry-run=client -o yaml | kubectl apply -f -

# Create test topic
echo -e "${GREEN}Creating test topic 'perf-test'...${NC}"
kubectl run kafka-topics-tmp --image=quay.io/strimzi/kafka:0.49.0-kafka-4.1.1 \
  --rm -i --restart=Never -n kafka -- \
  bin/kafka-topics.sh --create \
  --bootstrap-server krafter-kafka-bootstrap.kafka.svc:9092 \
  --topic perf-test \
  --partitions 3 \
  --replication-factor 3 \
  --if-not-exists

# Deploy Producer
echo -e "${GREEN}Deploying producer (1 million messages)...${NC}"
cat <<EOF | kubectl apply -f -
apiVersion: batch/v1
kind: Job
metadata:
  name: kafka-producer-perf-test
  namespace: performance
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
        image: quay.io/strimzi/kafka:0.49.0-kafka-4.1.1
        command:
        - /bin/bash
        - -c
        - |
          echo "Starting producer performance test..."
          bin/kafka-producer-perf-test.sh \
            --topic perf-test \
            --num-records 1000000 \
            --record-size 1024 \
            --throughput -1 \
            --producer-props \
              bootstrap.servers=krafter-kafka-bootstrap.kafka.svc:9092 \
              acks=all \
              batch.size=16384 \
              linger.ms=10
          echo "Producer test completed!"
EOF

# Wait for producer to start
echo -e "${YELLOW}Waiting for producer to start...${NC}"
sleep 5

# Monitor producer progress
echo -e "${GREEN}Monitoring producer (this may take a few minutes)...${NC}"
kubectl wait --for=condition=complete --timeout=600s job/kafka-producer-perf-test -n performance 2>/dev/null || true

# Get producer logs
echo -e "${GREEN}Producer Results:${NC}"
kubectl logs -n performance job/kafka-producer-perf-test | tail -20

# Deploy Consumer
echo -e "${GREEN}Deploying consumer...${NC}"
cat <<EOF | kubectl apply -f -
apiVersion: batch/v1
kind: Job
metadata:
  name: kafka-consumer-perf-test
  namespace: performance
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
        image: quay.io/strimzi/kafka:0.49.0-kafka-4.1.1
        command:
        - /bin/bash
        - -c
        - |
          echo "Starting consumer performance test..."
          bin/kafka-consumer-perf-test.sh \
            --topic perf-test \
            --bootstrap-server krafter-kafka-bootstrap.kafka.svc:9092 \
            --messages 1000000 \
            --threads 1 \
            --group perf-test-group \
            --show-detailed-stats
          echo "Consumer test completed!"
EOF

# Wait for consumer
echo -e "${YELLOW}Waiting for consumer to complete...${NC}"
kubectl wait --for=condition=complete --timeout=600s job/kafka-consumer-perf-test -n performance 2>/dev/null || true

# Get consumer logs
echo -e "${GREEN}Consumer Results:${NC}"
kubectl logs -n performance job/kafka-consumer-perf-test | tail -20

echo -e "${GREEN}Performance test completed!${NC}"
echo ""
echo "To view detailed logs:"
echo "  Producer: kubectl logs -n performance job/kafka-producer-perf-test"
echo "  Consumer: kubectl logs -n performance job/kafka-consumer-perf-test"
echo ""
echo "To cleanup:"
echo "  kubectl delete namespace performance"
echo "  kubectl run kafka-topics-tmp --image=quay.io/strimzi/kafka:0.49.0-kafka-4.1.1 --rm -i --restart=Never -n kafka -- bin/kafka-topics.sh --delete --bootstrap-server krafter-kafka-bootstrap.kafka.svc:9092 --topic perf-test"

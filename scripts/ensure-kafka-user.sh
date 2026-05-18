#!/bin/bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "${SCRIPT_DIR}/common.sh"

CLUSTER_NAME="krafter"

USER_NAME="kates-backend"
KAFKA_NS="kafka"
info "Ensuring KafkaUser ${USER_NAME} exists in namespace ${KAFKA_NS}..."

cat <<EOF | kubectl apply -f -
apiVersion: kafka.strimzi.io/v1
kind: KafkaUser
metadata:
  name: ${USER_NAME}
  namespace: ${KAFKA_NS}
  labels:
    strimzi.io/cluster: ${CLUSTER_NAME}
  annotations:
    kates.io/reconcile-trigger: "$(date +%s)"
spec:
  authentication:
    type: scram-sha-512
  authorization:
    type: simple
    acls:
      - resource:
          type: topic
          name: "*"
          patternType: literal
        operations:
          - Read
          - Write
          - Create
          - Describe
          - DescribeConfigs
EOF

info "Waiting for Strimzi to generate the secret ${USER_NAME}..."
# Wait up to 30 seconds for the secret to be created
for i in {1..15}; do
    if kubectl get secret "${USER_NAME}" -n "${KAFKA_NS}" &>/dev/null; then
        info "KafkaUser secret generated successfully!"
        exit 0
    fi
    sleep 2
done

error "Timeout waiting for Strimzi to generate the secret ${USER_NAME} in namespace ${KAFKA_NS}"
exit 1

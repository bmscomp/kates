#!/bin/bash
set -e

GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
RED='\033[0;31m'
NC='\033[0m'

echo -e "${GREEN}========================================${NC}"
echo -e "${GREEN}Setting up Kafka Chaos Testing Environment${NC}"
echo -e "${GREEN}========================================${NC}"
echo ""

# Step 1: Verify prerequisites
echo -e "${BLUE}Step 1: Verifying cluster state...${NC}"
if ! kubectl get kafka krafter -n kafka &>/dev/null; then
    echo -e "${RED}Error: Kafka cluster 'krafter' not found in namespace 'kafka'${NC}"
    echo "Please deploy Kafka first: make kafka"
    exit 1
fi
if ! kubectl get pods -n litmus -l app.kubernetes.io/component=litmus-server --field-selector=status.phase=Running &>/dev/null; then
    echo -e "${RED}Error: LitmusChaos portal not running${NC}"
    echo "Please deploy LitmusChaos first: make litmus"
    exit 1
fi
echo -e "${GREEN}✓ Kafka cluster and Litmus portal are running${NC}"

# Step 2: Register chaos infrastructure
echo ""
echo -e "${BLUE}Step 2: Registering chaos infrastructure...${NC}"

INFRA_MANIFEST="config/litmus/chaos-litmus-chaos-enable.yml"
if [ ! -f "${INFRA_MANIFEST}" ]; then
    echo -e "${RED}Error: Infrastructure manifest not found at ${INFRA_MANIFEST}${NC}"
    echo ""
    echo "To generate it:"
    echo "  1. Run: make chaos-ui"
    echo "  2. Open http://localhost:9091 (admin/litmus)"
    echo "  3. Go to Environments → Create → 'kafka-chaos'"
    echo "  4. Add Infrastructure → copy the manifest"
    echo "  5. Save to ${INFRA_MANIFEST}"
    exit 1
fi

# Fix imagePullPolicy, versions, and image names for offline Kind
echo -e "  Patching manifest for offline deployment..."
PATCHED_MANIFEST=$(mktemp)
sed -e 's/imagePullPolicy: IfNotPresent/imagePullPolicy: Never/g' \
    -e 's|litmuschaos.docker.scarf.sh/litmuschaos/chaos-operator:3.23.0|litmuschaos/chaos-operator:3.24.0|g' \
    -e 's|litmuschaos.docker.scarf.sh/litmuschaos/chaos-runner:3.23.0|litmuschaos/chaos-runner:3.24.0|g' \
    -e 's|litmuschaos.docker.scarf.sh/litmuschaos/chaos-exporter:3.23.0|litmuschaos/chaos-exporter:3.24.0|g' \
    -e 's|litmuschaos.docker.scarf.sh/litmuschaos/litmusportal-subscriber:3.23.0|litmuschaos/litmusportal-subscriber:3.24.0|g' \
    -e 's|litmuschaos.docker.scarf.sh/litmuschaos/litmusportal-event-tracker:3.23.0|litmuschaos/litmusportal-event-tracker:3.24.0|g' \
    -e 's|litmuschaos/litmusportal-subscriber:3.23.0|litmuschaos/litmusportal-subscriber:3.24.0|g' \
    -e 's|litmuschaos/litmusportal-event-tracker:3.23.0|litmuschaos/litmusportal-event-tracker:3.24.0|g' \
    "${INFRA_MANIFEST}" > "${PATCHED_MANIFEST}"

echo -e "  Applying infrastructure manifest..."
kubectl apply -f "${PATCHED_MANIFEST}" 2>&1 | grep -v "^Warning:" || true
rm -f "${PATCHED_MANIFEST}"

echo -e "  Waiting for chaos infrastructure to be ready..."
kubectl wait --for=condition=available deployment/chaos-operator-ce -n litmus --timeout=120s 2>/dev/null || true
kubectl wait --for=condition=available deployment/workflow-controller -n litmus --timeout=120s 2>/dev/null || true
kubectl wait --for=condition=available deployment/subscriber -n litmus --timeout=120s 2>/dev/null || true
echo -e "${GREEN}✓ Chaos infrastructure registered${NC}"

# Step 3: Install ChaosExperiments for Kafka
echo ""
echo -e "${BLUE}Step 3: Installing chaos experiment CRDs...${NC}"

# Install pod-delete experiment (the most common one for Kafka)
cat <<EOF | kubectl apply -f -
apiVersion: litmuschaos.io/v1alpha1
kind: ChaosExperiment
metadata:
  name: pod-delete
  namespace: kafka
spec:
  definition:
    scope: Namespaced
    permissions:
      - apiGroups: [""]
        resources: ["pods"]
        verbs: ["create","delete","get","list","patch","update","deletecollection"]
      - apiGroups: [""]
        resources: ["events"]
        verbs: ["create","get","list","patch","update"]
      - apiGroups: [""]
        resources: ["pods/log"]
        verbs: ["get","list","watch"]
      - apiGroups: ["batch"]
        resources: ["jobs"]
        verbs: ["create","list","get","delete","deletecollection"]
      - apiGroups: ["litmuschaos.io"]
        resources: ["chaosengines","chaosexperiments","chaosresults"]
        verbs: ["create","list","get","patch","update","delete"]
    image: "litmuschaos/go-runner:3.24.0"
    imagePullPolicy: Never
    args:
      - -name
      - pod-delete
    command:
      - ./experiments
    env:
      - name: TOTAL_CHAOS_DURATION
        value: '30'
      - name: CHAOS_INTERVAL
        value: '10'
      - name: FORCE
        value: 'false'
      - name: LIB
        value: 'litmus'
    labels:
      name: pod-delete
      app.kubernetes.io/part-of: litmus
EOF

# Install pod-network-partition experiment
cat <<EOF | kubectl apply -f -
apiVersion: litmuschaos.io/v1alpha1
kind: ChaosExperiment
metadata:
  name: pod-network-partition
  namespace: kafka
spec:
  definition:
    scope: Namespaced
    permissions:
      - apiGroups: [""]
        resources: ["pods"]
        verbs: ["create","delete","get","list","patch","update","deletecollection"]
      - apiGroups: [""]
        resources: ["events"]
        verbs: ["create","get","list","patch","update"]
      - apiGroups: [""]
        resources: ["pods/log","pods/exec"]
        verbs: ["get","list","watch","create"]
      - apiGroups: ["batch"]
        resources: ["jobs"]
        verbs: ["create","list","get","delete","deletecollection"]
      - apiGroups: ["litmuschaos.io"]
        resources: ["chaosengines","chaosexperiments","chaosresults"]
        verbs: ["create","list","get","patch","update","delete"]
      - apiGroups: ["networking.k8s.io"]
        resources: ["networkpolicies"]
        verbs: ["create","delete","list","get"]
    image: "litmuschaos/go-runner:3.24.0"
    imagePullPolicy: Never
    args:
      - -name
      - pod-network-partition
    command:
      - ./experiments
    env:
      - name: TOTAL_CHAOS_DURATION
        value: '60'
      - name: LIB
        value: 'litmus'
    labels:
      name: pod-network-partition
      app.kubernetes.io/part-of: litmus
EOF

# Install pod-cpu-hog experiment
cat <<EOF | kubectl apply -f -
apiVersion: litmuschaos.io/v1alpha1
kind: ChaosExperiment
metadata:
  name: pod-cpu-hog
  namespace: kafka
spec:
  definition:
    scope: Namespaced
    permissions:
      - apiGroups: [""]
        resources: ["pods"]
        verbs: ["create","delete","get","list","patch","update","deletecollection"]
      - apiGroups: [""]
        resources: ["events"]
        verbs: ["create","get","list","patch","update"]
      - apiGroups: [""]
        resources: ["pods/log","pods/exec"]
        verbs: ["get","list","watch","create"]
      - apiGroups: ["batch"]
        resources: ["jobs"]
        verbs: ["create","list","get","delete","deletecollection"]
      - apiGroups: ["litmuschaos.io"]
        resources: ["chaosengines","chaosexperiments","chaosresults"]
        verbs: ["create","list","get","patch","update","delete"]
    image: "litmuschaos/go-runner:3.24.0"
    imagePullPolicy: Never
    args:
      - -name
      - pod-cpu-hog
    command:
      - ./experiments
    env:
      - name: TOTAL_CHAOS_DURATION
        value: '60'
      - name: CPU_CORES
        value: '1'
    labels:
      name: pod-cpu-hog
      app.kubernetes.io/part-of: litmus
EOF

echo -e "${GREEN}✓ Chaos experiments installed: pod-delete, pod-network-partition, pod-cpu-hog${NC}"

# Step 4: Create RBAC for chaos in kafka namespace
echo ""
echo -e "${BLUE}Step 4: Setting up RBAC for kafka namespace...${NC}"

cat <<EOF | kubectl apply -f -
apiVersion: v1
kind: ServiceAccount
metadata:
  name: litmus-admin
  namespace: kafka
---
apiVersion: rbac.authorization.k8s.io/v1
kind: Role
metadata:
  name: litmus-admin
  namespace: kafka
rules:
  - apiGroups: [""]
    resources: ["pods","events","pods/log","pods/exec"]
    verbs: ["create","list","get","patch","update","delete","deletecollection"]
  - apiGroups: ["batch"]
    resources: ["jobs"]
    verbs: ["create","list","get","patch","update","delete","deletecollection"]
  - apiGroups: ["apps"]
    resources: ["deployments","statefulsets","daemonsets","replicasets"]
    verbs: ["list","get","patch","update"]
  - apiGroups: ["litmuschaos.io"]
    resources: ["chaosengines","chaosexperiments","chaosresults"]
    verbs: ["create","list","get","patch","update","delete"]
  - apiGroups: ["kafka.strimzi.io"]
    resources: ["kafkas"]
    verbs: ["get","list"]
  - apiGroups: ["networking.k8s.io"]
    resources: ["networkpolicies"]
    verbs: ["create","delete","list","get"]
---
apiVersion: rbac.authorization.k8s.io/v1
kind: RoleBinding
metadata:
  name: litmus-admin
  namespace: kafka
roleRef:
  apiGroup: rbac.authorization.k8s.io
  kind: Role
  name: litmus-admin
subjects:
  - kind: ServiceAccount
    name: litmus-admin
    namespace: kafka
EOF

echo -e "${GREEN}✓ RBAC configured for kafka namespace${NC}"

echo ""
echo -e "${GREEN}========================================${NC}"
echo -e "${GREEN}✅ Kafka Chaos Environment Ready!${NC}"
echo -e "${GREEN}========================================${NC}"
echo ""
echo "Run chaos experiments:"
echo ""
echo "  # Delete a Kafka broker pod (33% of brokers)"
echo "  make chaos-kafka-pod-delete"
echo ""
echo "  # Network partition a Kafka broker"
echo "  make chaos-kafka-network-partition"
echo ""
echo "  # CPU stress on a Kafka broker"
echo "  make chaos-kafka-cpu-stress"
echo ""
echo "  # Run all Kafka chaos experiments"
echo "  make chaos-kafka-all"
echo ""
echo "Monitor results:"
echo "  kubectl get chaosengines -n kafka"
echo "  kubectl get chaosresults -n kafka"
echo "  kubectl describe chaosresult -n kafka"
echo ""

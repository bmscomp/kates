#!/bin/bash
set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "${SCRIPT_DIR}/common.sh"
source "${SCRIPT_DIR}/../versions.env"

bold "Setting up Kafka Chaos Testing Environment"
echo ""

# Step 1: Verify prerequisites
step "Step 1: Verifying cluster state..."
if ! kubectl get kafka krafter -n kafka &>/dev/null; then
    error "Kafka cluster 'krafter' not found in namespace 'kafka'"
    echo "Please deploy Kafka first: make kafka"
    exit 1
fi
if ! kubectl get pods -n litmus -l app.kubernetes.io/component=litmus-server --field-selector=status.phase=Running &>/dev/null; then
    error "LitmusChaos portal not running"
    echo "Please deploy LitmusChaos first: make litmus"
    exit 1
fi
info "✓ Kafka cluster and Litmus portal are running"

# Step 2: Register chaos infrastructure
echo ""
step "Step 2: Registering chaos infrastructure..."

INFRA_MANIFEST="config/litmus/chaos-litmus-chaos-enable.yml"
if [ ! -f "${INFRA_MANIFEST}" ]; then
    error "Infrastructure manifest not found at ${INFRA_MANIFEST}"
    echo ""
    echo "To generate it:"
    echo "  1. Run: make chaos-ui"
    echo "  2. Open http://localhost:9091 (admin/litmus)"
    echo "  3. Go to Environments → Create → 'kafka-chaos'"
    echo "  4. Add Infrastructure → copy the manifest"
    echo "  5. Save to ${INFRA_MANIFEST}"
    exit 1
fi

info "Patching manifest image references (Litmus v${LITMUS_VERSION})..."
PATCHED_MANIFEST=$(mktemp)
sed -e "s|litmuschaos.docker.scarf.sh/litmuschaos/chaos-operator:3.23.0|litmuschaos/chaos-operator:${LITMUS_VERSION}|g" \
    -e "s|litmuschaos.docker.scarf.sh/litmuschaos/chaos-runner:3.23.0|litmuschaos/chaos-runner:${LITMUS_VERSION}|g" \
    -e "s|litmuschaos.docker.scarf.sh/litmuschaos/chaos-exporter:3.23.0|litmuschaos/chaos-exporter:${LITMUS_VERSION}|g" \
    -e "s|litmuschaos.docker.scarf.sh/litmuschaos/litmusportal-subscriber:3.23.0|litmuschaos/litmusportal-subscriber:${LITMUS_VERSION}|g" \
    -e "s|litmuschaos.docker.scarf.sh/litmuschaos/litmusportal-event-tracker:3.23.0|litmuschaos/litmusportal-event-tracker:${LITMUS_VERSION}|g" \
    -e "s|litmuschaos/litmusportal-subscriber:3.23.0|litmuschaos/litmusportal-subscriber:${LITMUS_VERSION}|g" \
    -e "s|litmuschaos/litmusportal-event-tracker:3.23.0|litmuschaos/litmusportal-event-tracker:${LITMUS_VERSION}|g" \
    "${INFRA_MANIFEST}" > "${PATCHED_MANIFEST}"

info "Applying infrastructure manifest..."
kubectl apply -f "${PATCHED_MANIFEST}" 2>&1 | grep -v "^Warning:" || true
rm -f "${PATCHED_MANIFEST}"

info "Waiting for chaos infrastructure to be ready..."
kubectl wait --for=condition=available deployment/chaos-operator-ce -n litmus --timeout=120s 2>/dev/null || true
kubectl wait --for=condition=available deployment/workflow-controller -n litmus --timeout=120s 2>/dev/null || true
kubectl wait --for=condition=available deployment/subscriber -n litmus --timeout=120s 2>/dev/null || true
info "✓ Chaos infrastructure registered"

# Step 3: Install ChaosExperiments for Kafka
echo ""
step "Step 3: Installing chaos experiment CRDs..."

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
    image: "litmuschaos/go-runner:${LITMUS_VERSION}"
    imagePullPolicy: IfNotPresent
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
    image: "litmuschaos/go-runner:${LITMUS_VERSION}"
    imagePullPolicy: IfNotPresent
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
    image: "litmuschaos/go-runner:${LITMUS_VERSION}"
    imagePullPolicy: IfNotPresent
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

info "✓ Chaos experiments installed: pod-delete, pod-network-partition, pod-cpu-hog"

# Step 4: Create RBAC for chaos in kafka namespace
echo ""
step "Step 4: Setting up RBAC for kafka namespace..."

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

info "✓ RBAC configured for kafka namespace"

echo ""
info "✅ Kafka Chaos Environment Ready!"
echo ""
echo "Run chaos experiments:"
echo "  make chaos-kafka-pod-delete"
echo "  make chaos-kafka-network-partition"
echo "  make chaos-kafka-cpu-stress"
echo "  make chaos-kafka-all"
echo ""
echo "Monitor: kubectl get chaosresults -n kafka"

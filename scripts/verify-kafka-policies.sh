#!/bin/bash
# Verify the Kafka cluster's policy posture: Kyverno admission, the Strimzi
# Cluster Operator, the Kafka CR, and whether a pod with no grant can open a
# socket to the brokers.
#
# Exit status: 0 when every check passes, 1 when one fails, 2 when none failed
# but the network probe could not reach a verdict.
#
# Environment:
#   KAFKA_NAMESPACE  namespace of the Kafka cluster (kafka)
#   KAFKA_CLUSTER    name of the Kafka cluster (krafter)
#   PROBE_NAMESPACE  namespace with no grant to the brokers to probe from (default)
#   PROBE_PORT       listener port to probe (9092)
#   PROBE_IMAGE      probe image (the kafka-cluster chart's testImages.kubectl,
#                    the kates-tester image its Helm tests run)
set -uo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "${SCRIPT_DIR}/common.sh"

NAMESPACE="${KAFKA_NAMESPACE:-kafka}"
CLUSTER_NAME="${KAFKA_CLUSTER:-krafter}"
PROBE_NAMESPACE="${PROBE_NAMESPACE:-default}"
PROBE_PORT="${PROBE_PORT:-9092}"
PROBE_IMAGE="${PROBE_IMAGE:-$(awk '/^testImages:/{f=1; next} f && /^[^ #]/{f=0} f && $1 == "kubectl:" {gsub(/"/, "", $2); print $2; exit}' "${CHARTS_DIR}/kafka-cluster/values.yaml")}"
BOOTSTRAP="${CLUSTER_NAME}-kafka-bootstrap.${NAMESPACE}.svc"

CONTROL_POD="kates-netpol-control"
PROBE_POD="kates-netpol-probe"

failures=0
inconclusive=0

cleanup() {
    kubectl delete pod "${CONTROL_POD}" -n "${NAMESPACE}" --ignore-not-found --wait=false >/dev/null 2>&1 || true
    kubectl delete pod "${PROBE_POD}" -n "${PROBE_NAMESPACE}" --ignore-not-found --wait=false >/dev/null 2>&1 || true
}
trap cleanup EXIT

# probe <pod> <namespace> <labels as a YAML flow mapping>
#
# Runs one probe pod to completion and prints its verdict:
#   open     the TCP connection to ${BOOTSTRAP}:${PROBE_PORT} succeeded
#   timeout  the name resolved and the connection timed out: packets dropped
#   refused  the name resolved and the connection failed at once (refused or
#            unreachable): something answered, which a policy drop does not
#   dns      the bootstrap name did not resolve
#   none     the pod was not admitted, did not start or did not finish
# The pod runs as non-root with no capabilities and resource limits, so that
# restricted Pod Security admission and the Kyverno policies admit it.
probe() {
    local pod=$1 ns=$2 labels=$3 verdict
    kubectl delete pod "${pod}" -n "${ns}" --ignore-not-found >/dev/null 2>&1
    if ! kubectl apply -f - >/dev/null <<EOF
apiVersion: v1
kind: Pod
metadata:
  name: ${pod}
  namespace: ${ns}
  labels: ${labels}
spec:
  restartPolicy: Never
  automountServiceAccountToken: false
  securityContext:
    runAsNonRoot: true
    runAsUser: 1000
    seccompProfile:
      type: RuntimeDefault
  containers:
    - name: probe
      image: ${PROBE_IMAGE}
      env:
        - name: HOST
          value: "${BOOTSTRAP}"
        - name: PORT
          value: "${PROBE_PORT}"
      command: ["bash", "-c"]
      args:
        - |
          getent hosts "\$HOST" >/dev/null || { echo PROBE=dns; exit 0; }
          timeout 5 bash -c 'exec 3<>"/dev/tcp/\$0/\$1"' "\$HOST" "\$PORT" 2>/dev/null
          case \$? in
            0) echo PROBE=open ;;
            124) echo PROBE=timeout ;;
            *) echo PROBE=refused ;;
          esac
      securityContext:
        allowPrivilegeEscalation: false
        readOnlyRootFilesystem: true
        capabilities:
          drop: ["ALL"]
      resources:
        requests:
          cpu: 10m
          memory: 32Mi
        limits:
          cpu: 200m
          memory: 64Mi
EOF
    then
        echo none
        return
    fi
    kubectl wait pod/"${pod}" -n "${ns}" --for=jsonpath='{.status.phase}'=Succeeded --timeout=300s >/dev/null 2>&1
    verdict=$(kubectl logs pod/"${pod}" -n "${ns}" 2>/dev/null | sed -n 's/^PROBE=//p' | tail -n 1)
    kubectl delete pod "${pod}" -n "${ns}" --ignore-not-found --wait=false >/dev/null 2>&1
    echo "${verdict:-none}"
}

info "=========================================================="
info "🔍 Verifying Kafka Generic Cluster & Policy Compliance..."
info "=========================================================="

# 1. Check for Kyverno Policy Violations
info "Step 1: Checking for Kyverno policy blocks in the ${NAMESPACE} namespace..."
if kubectl get events -n "${NAMESPACE}" | grep -i "kyverno" | grep -i "block\|reject\|fail" >/dev/null 2>&1; then
    warn "⚠️  Found potential Kyverno policy rejections in ${NAMESPACE} events:"
    kubectl get events -n "${NAMESPACE}" | grep -i "kyverno" | grep -i "block\|reject\|fail"
else
    info "✅ No Kyverno blocks detected in ${NAMESPACE} namespace events."
fi

# 2. Check Strimzi Operator Status, in whichever namespace it runs (the
# isolated topology puts it in strimzi-operator, not beside the cluster).
# One line per pod: namespace, name, phase and Ready condition. A namespace
# passes when one of its operator pods is Running and Ready, so an Evicted pod
# not yet garbage-collected, or a surge pod still Pending during a rollout,
# does not fail a healthy operator.
info "Step 2: Checking Strimzi Operator health..."
OPERATORS=$(kubectl get pods -A -l strimzi.io/kind=cluster-operator \
    -o jsonpath='{range .items[*]}{.metadata.namespace}{" "}{.metadata.name}{" "}{.status.phase}{" "}{.status.conditions[?(@.type=="Ready")].status}{"\n"}{end}' 2>/dev/null || true)
UNHEALTHY=$(echo "${OPERATORS}" | awk 'NF {seen[$1] = 1} $3 == "Running" && $4 == "True" {ok[$1] = 1} END {for (ns in seen) if (!(ns in ok)) print ns}')
if [ -z "${OPERATORS}" ]; then
    error "❌ No Strimzi Operator pod found in any namespace."
    failures=$((failures + 1))
elif [ -n "${UNHEALTHY}" ]; then
    error "❌ No Strimzi Operator pod is Running and Ready in: ${UNHEALTHY//$'\n'/ }. It may be blocked by a policy:"
    echo "${OPERATORS}"
    failures=$((failures + 1))
else
    info "✅ Strimzi Operator is Running and Ready ($(echo "${OPERATORS}" | awk '$3 == "Running" && $4 == "True" {s = s (n++ ? ", " : "") $1 "/" $2} END {print s}'))."
fi

# 3. Check Kafka Cluster CR Status
info "Step 3: Checking Kafka Cluster ('${CLUSTER_NAME}') readiness..."
if kubectl get kafka "${CLUSTER_NAME}" -n "${NAMESPACE}" >/dev/null 2>&1; then
    KAFKA_STATE=$(kubectl get kafka "${CLUSTER_NAME}" -n "${NAMESPACE}" -o jsonpath='{.status.conditions[?(@.type=="Ready")].status}' 2>/dev/null || true)
    if [ "$KAFKA_STATE" = "True" ]; then
        info "✅ Kafka cluster '${CLUSTER_NAME}' is Ready."
    else
        warn "⚠️  Kafka cluster '${CLUSTER_NAME}' is NotReady."
        kubectl get kafka "${CLUSTER_NAME}" -n "${NAMESPACE}" -o jsonpath='{.status.conditions[?(@.type=="NotReady")].message}' 2>/dev/null || true
        echo ""
        error "❌ Cluster creation is blocked or pending."
        failures=$((failures + 1))
    fi
else
    error "❌ Kafka CR '${CLUSTER_NAME}' does not exist in ${NAMESPACE}."
    failures=$((failures + 1))
fi

# 4. Can a pod with no grant open a socket to the brokers?
#
# A failed connection proves a policy blocks it only once everything else that
# fails a connection is ruled out. So the same probe first runs from a pod the
# chart always admits (kates.io/test-pod=true in the Kafka namespace); the
# pod in PROBE_NAMESPACE then counts as blocked only for a timeout after a
# successful lookup, from a namespace with no NetworkPolicy of its own that
# could have dropped its egress. Cluster-wide policies of a CNI (Calico
# GlobalNetworkPolicy, Cilium's clusterwide policies) are not checked.
info "Step 4: Probing ${BOOTSTRAP}:${PROBE_PORT} from an admitted pod, then from ${PROBE_NAMESPACE}..."
if [ -z "${PROBE_IMAGE}" ]; then
    warn "⚠️  No probe image: set PROBE_IMAGE (the kafka-cluster chart's testImages.kubectl)."
    inconclusive=1
else
    control=$(probe "${CONTROL_POD}" "${NAMESPACE}" '{app.kubernetes.io/name: kates-netpol-probe, kates.io/test-pod: "true"}')
    if [ "${control}" != "open" ]; then
        warn "⚠️  Inconclusive: the control probe in ${NAMESPACE} did not connect (${control}), so the brokers, DNS or the probe pod itself is not working and a failed probe elsewhere would prove nothing."
        inconclusive=1
    elif OWN=$(kubectl get networkpolicy -n "${PROBE_NAMESPACE}" -o name 2>/dev/null) && [ -n "${OWN}" ]; then
        warn "⚠️  Inconclusive: ${PROBE_NAMESPACE} has NetworkPolicies of its own, which could drop the probe's egress. Set PROBE_NAMESPACE to a namespace with none."
        inconclusive=1
    else
        verdict=$(probe "${PROBE_POD}" "${PROBE_NAMESPACE}" '{app.kubernetes.io/name: kates-netpol-probe}')
        case "${verdict}" in
            timeout)
                info "✅ Blocked: a pod in ${PROBE_NAMESPACE} resolved ${BOOTSTRAP} and its connection to port ${PROBE_PORT} timed out."
                ;;
            open)
                error "❌ Not blocked: a pod in ${PROBE_NAMESPACE} with no grant connected to ${BOOTSTRAP}:${PROBE_PORT}."
                if [ -z "$(kubectl get networkpolicy -n "${NAMESPACE}" -o name 2>/dev/null)" ]; then
                    error "   ${NAMESPACE} has no NetworkPolicy at all: the values chain sets networkPolicy.enabled=false (values-kind.yaml and values-dev.yaml do)."
                else
                    error "   Strimzi's generated ${CLUSTER_NAME}-network-policy-kafka admits every pod to a listener without networkPolicyPeers; set them on each entry of kafka.listeners."
                fi
                failures=$((failures + 1))
                ;;
            refused)
                warn "⚠️  Inconclusive: the connection from ${PROBE_NAMESPACE} was refused or unreachable, not dropped. A CNI that rejects instead of dropping looks like this, and so does a broker that is not listening."
                inconclusive=1
                ;;
            dns)
                warn "⚠️  Inconclusive: the pod in ${PROBE_NAMESPACE} could not resolve ${BOOTSTRAP}."
                inconclusive=1
                ;;
            *)
                warn "⚠️  Inconclusive: the probe pod in ${PROBE_NAMESPACE} was not admitted or did not finish (admission, image pull or scheduling)."
                inconclusive=1
                ;;
        esac
    fi
fi

info "=========================================================="
if [ "${failures}" -gt 0 ]; then
    error "❌ Cluster Policy Verification failed: ${failures} check(s) failed."
    exit 1
elif [ "${inconclusive}" -ne 0 ]; then
    warn "⚠️  Cluster Policy Verification incomplete: the network probe reached no verdict."
    exit 2
fi
info "🎉 Cluster Policy Verification Completed!"
info "=========================================================="

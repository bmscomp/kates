#!/usr/bin/env bash
# End-to-end validation of the NATIVE backend on a local Kind cluster.
#
# Builds what is in your working tree — CLI and native image — puts it on the
# node, deploys the chart against it, and then proves the thing actually works
# rather than merely starting: chart tests, the endpoints ahead-of-time
# compilation breaks first, and a real benchmark driven through the CLI.
#
# Every step is something that has bitten this repo before:
#
#   * pullPolicy Never          so the kubelet cannot substitute the published
#                               image and let you test somebody else's bytes
#   * playbook catalog check    classpath YAML is silently dropped from a native
#                               image whose resource patterns miss it — the API
#                               returns 200 with an empty list
#   * error-body check          an unregistered DTO serialises as {} in native
#                               only, and JVM tests never see it
#   * a real test run           the engine path exercises reflection, JNI
#                               compression and the Kafka client together
#
# Usage:
#   scripts/native-e2e.sh [--skip-kafka] [--skip-build] [--keep]
#
#   --skip-kafka   assume a Kafka cluster is already running in $KAFKA_NS
#   --skip-build   reuse an existing kates:native-local image
#   --keep         leave the release installed when the script finishes
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT_DIR="${SCRIPT_DIR}/.."
source "${SCRIPT_DIR}/common.sh"

cd "${ROOT_DIR}"

CLUSTER_NAME="${CLUSTER_NAME:-panda}"
KATES_NS="${KATES_NS:-kates}"
KATES_RELEASE="${KATES_RELEASE:-kates}"
KAFKA_NS="${KAFKA_NS:-kafka}"
IMAGE="${IMAGE:-kates:native-local}"
LOCAL_PORT="${LOCAL_PORT:-18080}"

SKIP_KAFKA=false
SKIP_BUILD=false
KEEP=false
for arg in "$@"; do
    case "${arg}" in
        --skip-kafka) SKIP_KAFKA=true ;;
        --skip-build) SKIP_BUILD=true ;;
        --keep)       KEEP=true ;;
        -h|--help)    sed -n '2,30p' "${BASH_SOURCE[0]}"; exit 0 ;;
        *) error "Unknown argument: ${arg}"; exit 2 ;;
    esac
done

PF_PID=""
cleanup() {
    [ -n "${PF_PID}" ] && kill "${PF_PID}" 2>/dev/null || true
    if [ "${KEEP}" = false ] && [ "${INSTALLED:-false}" = true ]; then
        echo ""
        warn "Removing the test release (pass --keep to leave it running)..."
        helm uninstall "${KATES_RELEASE}" -n "${KATES_NS}" >/dev/null 2>&1 || true
    fi
}
trap cleanup EXIT

fail() {
    error "❌ $*"
    echo "" >&2
    echo "Diagnostics:" >&2
    kubectl get pods -n "${KATES_NS}" 2>&1 | sed 's/^/  /' >&2 || true
    kubectl logs -n "${KATES_NS}" -l "app.kubernetes.io/instance=${KATES_RELEASE}" --tail=60 2>&1 \
        | sed 's/^/  /' >&2 || true
    exit 1
}

# ── Step 0: prerequisites ────────────────────────────────────────────────────
bold "Native end-to-end · ${IMAGE}"
echo ""
step "Step 0: Checking prerequisites..."
for cmd in docker kind kubectl helm go curl; do
    require_cmd "${cmd}"
done

if ! kind get clusters 2>/dev/null | grep -qx "${CLUSTER_NAME}"; then
    error "Kind cluster '${CLUSTER_NAME}' not found."
    echo "  Create it first:  make cluster"
    exit 1
fi
kubectl cluster-info --context "kind-${CLUSTER_NAME}" >/dev/null 2>&1 \
    || fail "cannot reach the cluster context kind-${CLUSTER_NAME}"

# The GraalVM compiler is the one step here with a hard memory floor. Docker
# Desktop defaults below this produce an opaque OOM kill that reads like a
# compiler crash, so check before spending twenty minutes finding out.
if [ "${SKIP_BUILD}" = false ]; then
    DOCKER_MEM_BYTES=$(docker info --format '{{.MemTotal}}' 2>/dev/null || echo 0)
    DOCKER_MEM_GB=$(( DOCKER_MEM_BYTES / 1024 / 1024 / 1024 ))
    if [ "${DOCKER_MEM_GB}" -gt 0 ] && [ "${DOCKER_MEM_GB}" -lt 8 ]; then
        warn "Docker reports ${DOCKER_MEM_GB}GB of memory; the native build wants ~8GB."
        warn "If it dies without an error, raise the VM memory limit and retry."
    fi
fi
info "✓ prerequisites present, cluster '${CLUSTER_NAME}' reachable"

# ── Step 1: the CLI, from this working tree ──────────────────────────────────
echo ""
step "Step 1: Building the Kates CLI..."
(cd cli && go build -ldflags="-s -w" -o dist/kates .)
KATES_CLI="${ROOT_DIR}/cli/dist/kates"
info "✓ $("${KATES_CLI}" version 2>/dev/null | head -1 || echo "built ${KATES_CLI}")"

# ── Step 2: the native image, from this working tree ─────────────────────────
echo ""
if [ "${SKIP_BUILD}" = true ]; then
    step "Step 2: Reusing existing ${IMAGE} (--skip-build)..."
    docker image inspect "${IMAGE}" >/dev/null 2>&1 \
        || { error "${IMAGE} not found locally"; exit 1; }
else
    step "Step 2: Building ${IMAGE} (GraalVM, ~15-25 min)..."
    docker build -f kates/Dockerfile.native -t "${IMAGE}" .
fi
docker image inspect "${IMAGE}" --format '   {{.Id}}  {{.Size}} bytes  ({{.Created}})'

step "        Loading it onto the node..."
kind load docker-image "${IMAGE}" --name "${CLUSTER_NAME}"
info "✓ ${IMAGE} is on the node"

# ── Step 3: Kafka ────────────────────────────────────────────────────────────
echo ""
if [ "${SKIP_KAFKA}" = true ]; then
    step "Step 3: Skipping Kafka deployment (--skip-kafka)..."
else
    step "Step 3: Deploying Kafka..."
    if kubectl get kafka -n "${KAFKA_NS}" >/dev/null 2>&1 \
        && [ -n "$(kubectl get kafka -n "${KAFKA_NS}" --no-headers 2>/dev/null)" ]; then
        info "✓ a Kafka cluster already exists in ${KAFKA_NS}"
    else
        make kafka-deploy
    fi
fi

# ── Step 4: deploy the chart against the local native image ──────────────────
echo ""
step "Step 4: Deploying the chart with values-native-local.yaml..."
helm upgrade --install "${KATES_RELEASE}" charts/kates \
    -n "${KATES_NS}" --create-namespace \
    -f charts/kates/values-native-local.yaml \
    --timeout 8m \
    || fail "helm upgrade failed"
INSTALLED=true

kubectl rollout status "deployment/${KATES_RELEASE}" -n "${KATES_NS}" --timeout=300s \
    || fail "the deployment never became available"

# Prove the running pod is on the image just built — the entire point of
# pullPolicy: Never, and the check that catches a silent registry substitution.
RUNNING_IMAGE=$(kubectl get pod -n "${KATES_NS}" -l "app.kubernetes.io/instance=${KATES_RELEASE}" \
    -o jsonpath='{.items[0].spec.containers[0].image}')
[ "${RUNNING_IMAGE}" = "${IMAGE}" ] \
    || fail "pod is running ${RUNNING_IMAGE}, not ${IMAGE}"
info "✓ pod is running ${RUNNING_IMAGE}"

# ── Step 5: chart tests ──────────────────────────────────────────────────────
echo ""
step "Step 5: Running the chart's own tests..."
helm test "${KATES_RELEASE}" -n "${KATES_NS}" --timeout 3m --logs \
    || fail "helm test failed"
info "✓ chart tests passed"

# ── Step 6: the endpoints AOT compilation breaks ─────────────────────────────
echo ""
step "Step 6: Checking the paths native builds break..."
kubectl port-forward -n "${KATES_NS}" "svc/${KATES_RELEASE}" "${LOCAL_PORT}:8080" >/dev/null 2>&1 &
PF_PID=$!
BASE="http://localhost:${LOCAL_PORT}"
for _ in $(seq 1 30); do
    curl -sf "${BASE}/q/health/live" >/dev/null 2>&1 && break
    sleep 1
done
curl -sf "${BASE}/q/health/live" >/dev/null 2>&1 || fail "port-forward never became usable"

curl -sf "${BASE}/q/health/ready" >/dev/null || fail "readiness is down — check Flyway migrations"
info "  ok   readiness (all migrations applied, including the non-transactional V20)"

PLAYBOOKS="$(curl -sf "${BASE}/api/disruptions/playbooks" || true)"
case "${PLAYBOOKS}" in
    ""|"[]") fail "playbook catalog is EMPTY — playbooks/*.yaml missing from the image.
   Check quarkus.native.resources.includes in application.properties." ;;
esac
info "  ok   playbook catalog loaded from classpath YAML"

NOT_FOUND="$(curl -s "${BASE}/api/tests/does-not-exist" || true)"
echo "${NOT_FOUND}" | grep -q '"message"' \
    || fail "404 body is empty — ApiError is not registered for reflection: ${NOT_FOUND}"
info "  ok   error bodies serialise"

# ── Step 7: a real benchmark, through the CLI ────────────────────────────────
echo ""
step "Step 7: Running a benchmark through the CLI against the native backend..."

# Size the test topic from the cluster in front of us. The CLI defaults to
# replication-factor 3 / min-isr 2, which a single-broker Kind cluster cannot
# satisfy — topic creation fails and the run reads as a backend bug.
BROKERS=$(kubectl get pods -n "${KAFKA_NS}" -l strimzi.io/broker-role=true --no-headers 2>/dev/null | wc -l | tr -d ' ')
[ "${BROKERS}" -gt 0 ] 2>/dev/null || BROKERS=1
RF=$(( BROKERS < 3 ? BROKERS : 3 ))
MIN_ISR=$(( RF > 1 ? RF - 1 : 1 ))
info "  ${BROKERS} broker(s) → replication-factor ${RF}, min-isr ${MIN_ISR}"

# lz4 on purpose: compression is JNI, and a native image that failed to ship or
# initialise those libraries only finds out here, minutes into a run.
"${KATES_CLI}" test create \
    --url "${BASE}" \
    --type LOAD \
    --topic native-e2e \
    --records 10000 \
    --record-size 512 \
    --compression lz4 \
    --replication-factor "${RF}" \
    --min-isr "${MIN_ISR}" \
    --wait \
    || fail "the benchmark did not complete — this exercises reflection, the JNI
   compression libraries and the Kafka client together, so read the pod logs"
info "✓ benchmark completed on the native backend"

echo ""
bold "✅ Native end-to-end passed"
echo ""
echo "  Image:    ${IMAGE}"
echo "  Release:  ${KATES_RELEASE} (namespace ${KATES_NS})"
if [ "${KEEP}" = true ]; then
    echo "  Kept running. Port-forward with:"
    echo "    kubectl port-forward -n ${KATES_NS} svc/${KATES_RELEASE} 8080:8080"
fi
echo ""

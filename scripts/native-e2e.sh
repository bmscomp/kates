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
    echo "Pods:" >&2
    kubectl get pods -n "${KATES_NS}" 2>&1 | sed 's/^/  /' >&2 || true

    # Logs from THIS deploy's newest pod only. Selecting on the instance label
    # alone also matches pods left over from earlier releases, and a crash-loop
    # is easy to misread when most of the output belongs to a pod that has been
    # running happily for a month.
    POD=$(kubectl get pods -n "${KATES_NS}" \
        -l "app.kubernetes.io/instance=${KATES_RELEASE}" \
        --sort-by=.metadata.creationTimestamp \
        -o jsonpath='{.items[-1:].metadata.name}' 2>/dev/null || true)
    LOGS=""
    if [ -n "${POD}" ]; then
        LOGS=$(kubectl logs -n "${KATES_NS}" "${POD}" -c kates --tail=200 2>&1 || true)
    fi

    # Name the failure when its signature is recognisable, because the cause is
    # otherwise buried in a few hundred lines of Kafka connection warnings that
    # are a symptom of the pod not starting, not the reason for it.
    echo "" >&2
    case "${LOGS}" in
        *FlywayValidateException*|*"checksum mismatch"*)
            error "Cause: a migration file was edited after this database applied it."
            echo "  The schema history and the file genuinely disagree, so Flyway refuses to" >&2
            echo "  boot. values-native-local.yaml sets flyway.repairAtStart for exactly this" >&2
            echo "  case — check you deployed with it. To repair by hand:" >&2
            echo "" >&2
            echo "    helm upgrade ${KATES_RELEASE} charts/kates -n ${KATES_NS} \\" >&2
            echo "      -f charts/kates/values-native-local.yaml --set flyway.repairAtStart=true" >&2
            ;;
        *"Failed to obtain JDBC connection"*|*"Connection to"*"refused"*)
            error "Cause: the database is unreachable — check ${KATES_RELEASE}-postgresql-0."
            ;;
        *OutOfMemoryError*|*"Java heap space"*)
            error "Cause: the container ran out of memory — raise resources.limits.memory."
            ;;
        *UnsatisfiedLinkError*)
            error "Cause: a JNI library is missing from the image (compression)."
            echo "  Check the native-libs extraction stage in kates/Dockerfile.native." >&2
            ;;
        *)
            error "No recognised failure signature; the last 60 log lines follow."
            ;;
    esac

    echo "" >&2
    echo "Last 60 lines from ${POD:-<no pod>}:" >&2
    echo "${LOGS}" | tail -60 | sed 's/^/  /' >&2
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
    warn "  built $(docker image inspect --format '{{.Created}}' "${IMAGE}") — anything"
    warn "  changed in kates/src since then is NOT in this image."
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

# Stamp the image ID into the pod template. The tag never changes, so without
# this the manifest is byte-identical between runs, Kubernetes sees no reason to
# roll, and the OLD pod keeps serving the OLD binary — you rebuild, redeploy,
# and test the bytes you were trying to replace. The Makefile already warns
# about this for the JVM image ("a plain helm upgrade would keep the old pod,
# running the old bytes, very much alive"); this script was doing exactly that.
IMAGE_ID="$(docker image inspect --format '{{.Id}}' "${IMAGE}")"
info "  image id: ${IMAGE_ID}"

helm upgrade --install "${KATES_RELEASE}" charts/kates \
    -n "${KATES_NS}" --create-namespace \
    -f charts/kates/values-native-local.yaml \
    --set-string podAnnotations.kates-image-id="${IMAGE_ID}" \
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

# The tag matching proves nothing on its own — it is the same tag it always is.
# The stamp is what proves the pod was created for THIS build.
RUNNING_ID=$(kubectl get pod -n "${KATES_NS}" -l "app.kubernetes.io/instance=${KATES_RELEASE}" \
    -o jsonpath='{.items[0].metadata.annotations.kates-image-id}')
[ "${RUNNING_ID}" = "${IMAGE_ID}" ] \
    || fail "the running pod was built from a different image.
   expected ${IMAGE_ID}
   running  ${RUNNING_ID:-<no stamp>}
   An old pod survived the upgrade; everything after this would test stale bytes."
info "✓ pod is running ${RUNNING_IMAGE} from this build"

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

# Everything under /api is authenticated (apiKey.enabled defaults to true), and
# the chart generates the key into a Secret. Read it rather than turning auth
# off: a native image can break the auth filter as easily as anything else, and
# a run that skipped it would not notice.
API_KEY="$(kubectl get secret "${KATES_RELEASE}-api-key" -n "${KATES_NS}" \
    -o jsonpath='{.data.api-key}' 2>/dev/null | base64 -d 2>/dev/null || true)"
[ -n "${API_KEY}" ] || fail "could not read the API key from secret ${KATES_RELEASE}-api-key"
info "  ok   API key read from ${KATES_RELEASE}-api-key"

# Returns the body, but only after asserting the STATUS. Conflating the two is
# how a 401 came to be reported as an empty playbook catalog — curl -sf fails
# silently on any 4xx, leaving an empty string that looks exactly like a feature
# that did not ship.
api_get() {  # api_get <path> <expected-status> <what-it-proves>
    _path="$1"; _expected="$2"; _what="$3"
    _body="$(mktemp)"
    _status="$(curl -s -o "${_body}" -w '%{http_code}' \
        -H "X-API-Key: ${API_KEY}" "${BASE}${_path}" || echo "000")"
    if [ "${_status}" != "${_expected}" ]; then
        _preview="$(head -c 300 "${_body}" 2>/dev/null || true)"
        rm -f "${_body}"
        case "${_status}" in
            401|403) fail "${_path} returned ${_status}: the API key was rejected.
   The key comes from secret ${KATES_RELEASE}-api-key; if the release was
   upgraded since the pod started, the running pod may hold an older one." ;;
            000)     fail "${_path} could not be reached — the port-forward died." ;;
            *)       fail "${_path} returned ${_status}, expected ${_expected} (${_what}): ${_preview}" ;;
        esac
    fi
    cat "${_body}"
    rm -f "${_body}"
}

PLAYBOOKS="$(api_get /api/disruptions/playbooks 200 'playbook catalog')"
case "$(echo "${PLAYBOOKS}" | tr -d '[:space:]')" in
    ""|"[]") fail "the playbook catalog is EMPTY.
   The endpoint answered 200, so the app is fine — playbooks/*.yaml were not
   shipped in the image. Check quarkus.native.resources.includes." ;;
esac
info "  ok   playbook catalog loaded from classpath YAML"

NOT_FOUND="$(api_get /api/tests/does-not-exist 404 'error body serialisation')"
echo "${NOT_FOUND}" | grep -q '"message"' \
    || fail "the 404 body has no fields — ApiError is not registered for reflection.
   Body was: ${NOT_FOUND}"
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
# The CLI's exit code is the authoritative verdict — cmd/test.go returns an
# error when a run ends FAILED, with a comment saying that exiting 0 there is
# "how CI stayed green while load tests failed". Discarding it with `|| true`
# reproduced exactly that: a run that ended FAILED, followed by
# "✅ Native end-to-end passed".
# tee to a FILE, not to /dev/tty: there is no controlling terminal in CI or
# when make's output is redirected, and tee then fails on every run.
RUN_LOG="$(mktemp)"
set +e
RUN_OUTPUT="$("${KATES_CLI}" test create \
    --url "${BASE}" \
    --api-key "${API_KEY}" \
    --type LOAD \
    --topic native-e2e \
    --records 10000 \
    --record-size 512 \
    --compression lz4 \
    --replication-factor "${RF}" \
    --min-isr "${MIN_ISR}" \
    --wait 2>&1 | tee "${RUN_LOG}")"
CLI_STATUS=${PIPESTATUS[0]}
set -e
rm -f "${RUN_LOG}"

# Strip ANSI before parsing: the id is printed in a styled column, and the
# escape sequence sits between "ID" and the value.
RUN_ID="$(printf '%s' "${RUN_OUTPUT}" \
    | sed 's/\x1b\[[0-9;]*m//g' \
    | sed -n 's/.*ID[[:space:]]\{2,\}\([0-9a-f]\{8\}\).*/\1/p' \
    | head -1)"

if [ "${CLI_STATUS}" -ne 0 ]; then
    error "❌ the benchmark did not finish cleanly (CLI exit ${CLI_STATUS})"
    if [ -n "${RUN_ID}" ]; then
        # The reason lives on the run, not in the progress view. Printing the
        # per-task status and error is the difference between "the benchmark
        # failed" and "the producer could not authenticate to the broker".
        echo "" >&2
        echo "Task detail for run ${RUN_ID}:" >&2
        api_get "/api/tests/${RUN_ID}" 200 'final run state' \
            | tr ',' '\n' \
            | grep -E '"(taskId|status|error|phaseName)"' \
            | sed 's/^/  /' >&2 || true
        echo "" >&2
        echo "  Full detail:  ${KATES_CLI} test get ${RUN_ID} --url ${BASE} --api-key <key>" >&2
    fi
    fail "benchmark run ${RUN_ID:-<unknown>} did not succeed"
fi
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

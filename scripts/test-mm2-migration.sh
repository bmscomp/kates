#!/usr/bin/env bash
# End-to-end cross-version migration test: Kafka 2.x or 3.x → Kafka 4.x,
# through MirrorMaker 2, in a real cluster, asserting real data.
#
# WHY THIS EXISTS: every cheaper check passes on a broken mirror. `helm install`
# succeeds against a source that does not exist. The KafkaMirrorMaker2 CR
# reports Ready while the MirrorSourceConnector cannot read a single record —
# Ready means the Connect workers are up, nothing more. Even "the connector is
# RUNNING" is satisfied by a connector reading nothing. The only statement that
# cannot be faked is: these specific records were written to the old cluster,
# and these specific records came back off the new one.
#
# So that is what this does. It stands up a genuinely old broker, produces a
# known corpus, commits a consumer offset, mirrors it, and then reads it back
# out of the target and compares — every distinct record, not just a count.
#
# Topology: ONE kind cluster. The legacy source lives in its own namespace, the
# 4.x target is the in-repo `krafter` cluster. A two-cluster topology is more
# faithful and is documented in docs/tutorials/11-migrating-kafka-2x-to-4x.md,
# but it is not what a laptop or a 2-core runner can hold.
#
# Usage:
#   scripts/test-mm2-migration.sh --source-version 2.8.2
#   scripts/test-mm2-migration.sh --source-version 3.9.1
#   scripts/test-mm2-migration.sh --source-version 3.9.1 --policy default --keep
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=scripts/common.sh
source "${SCRIPT_DIR}/common.sh"
ROOT_DIR="$(cd "${SCRIPT_DIR}/.." && pwd)"

# ── Defaults ────────────────────────────────────────────────────────────────
SOURCE_VERSION="3.9.1"
POLICY="identity"
TARGET_NS="kafka"
TARGET_CLUSTER="krafter"
MM2_RELEASE="mm2-migration"
LEGACY_RELEASE="legacy"
LEGACY_NS=""
TOPIC="kates.orders"
GROUP="kates-migration-consumer"
MESSAGES=200
TIMEOUT=600
KEEP="false"
SKIP_BUILD="false"
LEGACY_IMAGE=""

TARGET_USER="kates-mm2"
TARGET_SECRET="kates-mm2"

usage() {
    cat <<'EOF'
Usage: scripts/test-mm2-migration.sh [options]

Options:
  --source-version VER   Legacy Kafka version: 2.8.2 (ZooKeeper) or 3.9.1 (KRaft)
                         Default: 3.9.1
  --policy MODE          identity (preserve topic names, the migration case) or
                         default (rename to <alias>.<topic>). Default: identity
  --namespace NS         Namespace for the legacy source
                         Default: kafka-legacy-<2x|3x>
  --target-namespace NS  Namespace of the 4.x target cluster. Default: kafka
  --topic NAME           Topic to migrate. Default: kates.orders
  --messages N           Records to produce. Default: 200
  --timeout SECONDS      Per-phase wait budget (integer). Default: 600
  --legacy-image IMAGE   Override the source broker image
  --skip-build           Do not build/load the 2.x image (assume it is present)
  --keep                 Leave everything running for inspection
  -h, --help             Show this help

Phases (each fails independently and loudly):
   1  preflight — kind cluster, Strimzi operator, target Kafka Ready
   2  deploy the legacy source and wait for it to serve
   3  seed: create the topic, produce a known corpus
   4  commit a consumer-group offset on the source
   5  install MirrorMaker 2 with the matching migration preset
   6  wait for the CR Ready AND every connector RUNNING
   7  wait for the replicated topic to materialise on the target
   8  consume from the target; compare COUNT and CONTENT
   9  verify consumer-group offset translation
  10  cutover rehearsal — stop the source connector, confirm the target freezes
  11  report

Requires: a running kind cluster with the Strimzi operator and the krafter
cluster deployed (make cluster && make deploy-strimzi && make deploy-kafka).
EOF
}

need_arg() { # <flag> <remaining-argc>
    if [ "$2" -lt 2 ]; then
        error "Option $1 needs a value."
        usage
        exit 1
    fi
}
is_int() { [[ "$1" =~ ^[0-9]+$ ]]; }

while [ $# -gt 0 ]; do
    case "$1" in
        --source-version)   need_arg "$1" $#; SOURCE_VERSION="$2"; shift 2 ;;
        --policy)           need_arg "$1" $#; POLICY="$2"; shift 2 ;;
        --namespace)        need_arg "$1" $#; LEGACY_NS="$2"; shift 2 ;;
        --target-namespace) need_arg "$1" $#; TARGET_NS="$2"; shift 2 ;;
        --topic)            need_arg "$1" $#; TOPIC="$2"; shift 2 ;;
        --messages)         need_arg "$1" $#; MESSAGES="$2"; shift 2 ;;
        --timeout)          need_arg "$1" $#; TIMEOUT="$2"; shift 2 ;;
        --legacy-image)     need_arg "$1" $#; LEGACY_IMAGE="$2"; shift 2 ;;
        --skip-build)       SKIP_BUILD="true"; shift ;;
        --keep)             KEEP="true"; shift ;;
        -h|--help)          usage; exit 0 ;;
        *) error "Unknown option: $1"; usage; exit 1 ;;
    esac
done

require_cmd kubectl
require_cmd helm

is_int "${MESSAGES}" || { error "--messages must be an integer, got '${MESSAGES}'"; exit 1; }
is_int "${TIMEOUT}"  || { error "--timeout must be an integer number of seconds, got '${TIMEOUT}'"; exit 1; }
case "${POLICY}" in identity|default) ;; *) error "--policy must be identity or default"; exit 1 ;; esac

case "${SOURCE_VERSION}" in
    2.*) ERA="2x"; MODE="zookeeper"; ERA_VALUES="values-kafka-2x.yaml"; PRESET="values-migrate-2x.yaml" ;;
    3.*) ERA="3x"; MODE="kraft";     ERA_VALUES="values-kafka-3x.yaml"; PRESET="values-migrate-3x.yaml" ;;
    *)
        error "Unsupported --source-version '${SOURCE_VERSION}'."
        error "This test covers the 2.x and 3.x → 4.x paths. Kafka 4.x → 4.x is"
        error "not a cross-version migration, and anything below 2.1 cannot be"
        error "mirrored by a 4.x client at all (KIP-896)."
        exit 1 ;;
esac
[ -n "${LEGACY_NS}" ] || LEGACY_NS="kafka-legacy-${ERA}"

REPLICATED_TOPIC="${TOPIC}"
[ "${POLICY}" = "default" ] && REPLICATED_TOPIC="legacy.${TOPIC}"

# The 4.x client image is the ONE the workers run — that is what makes its
# verdicts evidence. It comes from the repo pin, with no literal fallback: a
# fallback would be a seventh version site that nothing checks.
if [ ! -f "${ROOT_DIR}/versions.env" ]; then
    error "versions.env not found at ${ROOT_DIR} — cannot determine the Kafka client image."
    exit 1
fi
# shellcheck source=versions.env
source "${ROOT_DIR}/versions.env"
CLIENT_IMAGE="quay.io/strimzi/kafka:${STRIMZI_KAFKA_VERSION}"

# ── Result tracking ─────────────────────────────────────────────────────────
# A migration test whose output has to be interpreted is a migration test
# nobody runs twice. Every assertion lands in this table.
RESULTS=()
FAILURES=0

record() { # <status> <name> <detail>
    RESULTS+=("$1|$2|$3")
    case "$1" in
        PASS) info  "  ✅ $2 — $3" ;;
        FAIL) error "  ❌ $2 — $3"; FAILURES=$((FAILURES + 1)) ;;
        SKIP) warn  "  ⏭  $2 — $3" ;;
    esac
}

phase() { echo ""; bold "══ $* "; }

MM2_CR="${MM2_RELEASE}-mirror-maker2"
CLI_ERR="$(mktemp -t mm2-cli.XXXXXX)"
MIRROR_VALUES="$(mktemp -t mm2-values.XXXXXX)"
CREATED_NS="false"

dump_diagnostics() {
    echo ""
    error "─── diagnostics ───"
    kubectl -n "${TARGET_NS}" get kafkamirrormaker2 "${MM2_CR}" -o yaml 2>/dev/null \
        | sed -n '/^status:/,$p' | head -60 || true
    echo ""
    kubectl -n "${TARGET_NS}" logs -l strimzi.io/name="${MM2_CR}-mirrormaker2" \
        --tail=80 2>/dev/null || true
    echo ""
    kubectl -n "${LEGACY_NS}" get pods 2>/dev/null || true
    if [ -s "${CLI_ERR}" ]; then
        echo ""
        error "─── last client stderr ───"
        tail -20 "${CLI_ERR}"
    fi
}

cleanup() {
    local rc=$?
    rm -f "${CLI_ERR}" "${MIRROR_VALUES}"
    if [ "${KEEP}" = "true" ]; then
        echo ""
        warn "--keep: leaving everything in place."
        warn "  source:  kubectl -n ${LEGACY_NS} get all"
        warn "  mirror:  kubectl -n ${TARGET_NS} get kafkamirrormaker2"
        warn "  remove:  helm uninstall ${MM2_RELEASE} -n ${TARGET_NS}; helm uninstall ${LEGACY_RELEASE} -n ${LEGACY_NS}; kubectl delete ns ${LEGACY_NS}"
        return "$rc"
    fi
    echo ""
    step "Cleaning up..."
    helm uninstall "${MM2_RELEASE}" -n "${TARGET_NS}" >/dev/null 2>&1 || true
    # keepOnDelete annotates the CR with resource-policy: keep, so uninstall
    # alone leaves the mirror running — which is right in production and wrong
    # for a test that must not leak into the next run.
    kubectl -n "${TARGET_NS}" delete kafkamirrormaker2 "${MM2_CR}" \
        --ignore-not-found --timeout=60s >/dev/null 2>&1 || true
    helm uninstall "${LEGACY_RELEASE}" -n "${LEGACY_NS}" >/dev/null 2>&1 || true
    # Only a namespace this run created. A pre-existing one is somebody's, and
    # a preflight abort must not take it with it.
    if [ "${CREATED_NS}" = "true" ]; then
        kubectl delete namespace "${LEGACY_NS}" --ignore-not-found --timeout=120s >/dev/null 2>&1 || true
    fi
    return "$rc"
}
trap cleanup EXIT

# ── Running Kafka CLI commands inside the cluster ───────────────────────────
#
# Every client below is a throwaway pod. Three things about the helper are
# load-bearing, none of them obvious:
#
#   • The `kates.io/test-pod` label. The kafka-cluster chart's NetworkPolicy
#     denies broker ingress by default, and that label is the selector it
#     allows. Without it every client times out against a healthy cluster.
#   • The sentinel. `kubectl run --rm` prints its deletion notice on STDOUT,
#     and the pod name carries ${RANDOM} — so anything parsed numerically out
#     of the raw stream picked up those digits and compared two numbers that
#     were never the same measurement. Output is cut at the marker, by awk,
#     which unlike a sed range also works when the marker is the first line.
#   • stderr is kept, not discarded. It goes to a file the diagnostics print,
#     because "the consumer was denied" and "nothing was replicated" produce
#     identical stdout and only stderr tells them apart.
#
# --pod-running-timeout: kubectl gives up attaching after 60s by default and
# falls back to (empty) logs, which on a cold image pull reads as an empty
# measurement rather than a slow one.
KATES_EOF_MARKER="__KATES_CLI_EOF__"
cut_at_marker() { awk -v m="${KATES_EOF_MARKER}" '$0 == m { exit } { print }'; }

kafka_cli() { # <namespace> <image> <command...>
    local ns="$1" image="$2"; shift 2
    kubectl -n "${ns}" run "mm2-cli-${RANDOM}" --rm -i --restart=Never \
        --image="${image}" \
        --labels="kates.io/test-pod=true,app.kubernetes.io/part-of=kates" \
        --env=LOG_DIR=/tmp \
        --pod-running-timeout=5m \
        --command -- /bin/sh -c "$* ; echo ${KATES_EOF_MARKER}" 2>>"${CLI_ERR}" \
        | cut_at_marker || true
}

# The target needs SCRAM credentials. They are injected from the Secret as an
# environment variable inside the pod — never interpolated into the command
# line, where they would be readable in `kubectl get pod -o yaml` and in the
# API audit log for the lifetime of every throwaway pod.
kafka_cli_target() { # <command...>
    local name="mm2-cli-${RANDOM}"
    local cmd="$* ; echo ${KATES_EOF_MARKER}"
    local overrides
    overrides=$(python3 - "${name}" "${CLIENT_IMAGE}" "${TARGET_SECRET}" "${cmd}" <<'PY'
import json, sys
name, image, secret, cmd = sys.argv[1:5]
print(json.dumps({"spec": {"containers": [{
    "name": name, "image": image,
    "command": ["/bin/sh", "-c", cmd],
    "env": [
        {"name": "LOG_DIR", "value": "/tmp"},
        {"name": "TARGET_PW", "valueFrom": {"secretKeyRef": {"name": secret, "key": "password"}}},
    ],
    "stdin": True,
}]}}))
PY
)
    kubectl -n "${TARGET_NS}" run "${name}" --rm -i --restart=Never \
        --image="${CLIENT_IMAGE}" \
        --labels="kates.io/test-pod=true,app.kubernetes.io/part-of=kates" \
        --pod-running-timeout=5m \
        --overrides="${overrides}" 2>>"${CLI_ERR}" \
        | cut_at_marker || true
}

# Writes /tmp/t.properties inside a target pod from $TARGET_PW. Runs first in
# every target command. The single quotes are the point: $TARGET_PW must reach
# the pod unexpanded and be resolved THERE, from the injected env var.
# shellcheck disable=SC2016
TARGET_CFG='PW=$(printf "%s" "$TARGET_PW" | sed "s/[\\\\\"]/\\\\&/g"); echo security.protocol=SASL_PLAINTEXT > /tmp/t.properties; echo sasl.mechanism=SCRAM-SHA-512 >> /tmp/t.properties; printf "%s\n" "sasl.jaas.config=org.apache.kafka.common.security.scram.ScramLoginModule required username=\"'"${TARGET_USER}"'\" password=\"$PW\";" >> /tmp/t.properties;'
TARGET_PROPS="/tmp/t.properties"

# GetOffsetShell moved from the Scala `kafka.tools` package to
# `org.apache.kafka.tools` in Kafka 3.4, and `kafka-get-offsets.sh` appeared
# alongside it. This test spans 2.8 through 4.3, so it needs both spellings:
# the 2.8 image has only the old class, the 4.x image has only the new script,
# and calling the wrong one fails with "could not find or load main class".
#
# awk prints NOTHING when it saw no input rows. An END block runs on empty
# input and would otherwise print OFFSETSUM=0 — turning "the tool failed" into
# "the topic is empty", which is the one confusion the caller must be able to
# tell apart.
sum_end_offsets() { # <bootstrap> <topic> [extra client args]
    local bootstrap="$1" topic="$2"; shift 2
    printf '%s' "if [ -x /opt/kafka/bin/kafka-get-offsets.sh ]; then \
            /opt/kafka/bin/kafka-get-offsets.sh --bootstrap-server ${bootstrap} --topic ${topic} $* ; \
          else \
            /opt/kafka/bin/kafka-run-class.sh kafka.tools.GetOffsetShell --bootstrap-server ${bootstrap} --topic ${topic} $* ; \
          fi | awk -F: '/^${topic}:[0-9]+:[0-9]+\$/ {s+=\$3; n++} END {if (n>0) printf \"OFFSETSUM=%d\\n\", s}'"
}

# Empty output means the measurement failed, which is NOT an offset of zero.
extract_offset_sum() { sed -n 's/^OFFSETSUM=\([0-9][0-9]*\)$/\1/p' | tail -1; }

bold "MirrorMaker 2 cross-version migration test"
echo "  source          Kafka ${SOURCE_VERSION} (${MODE}) in ${LEGACY_NS}"
echo "  target          Kafka 4.x (${TARGET_CLUSTER}) in ${TARGET_NS}"
echo "  client          ${CLIENT_IMAGE}"
echo "  policy          ${POLICY} — ${TOPIC} lands as ${REPLICATED_TOPIC}"
echo "  corpus          ${MESSAGES} records"

# ── Phase 1: preflight ──────────────────────────────────────────────────────
phase "1/11  preflight"

if ! kubectl cluster-info >/dev/null 2>&1; then
    error "No reachable Kubernetes cluster. Run 'make cluster' first."
    exit 1
fi
record PASS "cluster reachable" "$(kubectl config current-context)"

if ! kubectl get crd kafkamirrormaker2s.kafka.strimzi.io >/dev/null 2>&1; then
    error "The KafkaMirrorMaker2 CRD is not installed."
    error "Run: make deploy-strimzi"
    exit 1
fi
record PASS "Strimzi CRDs" "kafkamirrormaker2s.kafka.strimzi.io present"

if ! kubectl -n "${TARGET_NS}" wait "kafka/${TARGET_CLUSTER}" \
        --for=condition=Ready --timeout=300s >/dev/null 2>&1; then
    error "Target cluster ${TARGET_CLUSTER} in ${TARGET_NS} is not Ready."
    error "Run: make deploy-kafka"
    exit 1
fi
record PASS "target Kafka Ready" "${TARGET_CLUSTER} in ${TARGET_NS}"

if ! kubectl -n "${TARGET_NS}" get secret "${TARGET_SECRET}" >/dev/null 2>&1; then
    error "Secret ${TARGET_SECRET} not found in ${TARGET_NS}."
    error "The kafka-cluster chart provisions the KafkaUser '${TARGET_USER}'."
    exit 1
fi
record PASS "target credentials" "secret ${TARGET_SECRET}"

if ! command -v python3 >/dev/null 2>&1; then
    error "python3 is required (it builds the pod override that injects the target credential)."
    exit 1
fi

TARGET_BOOTSTRAP="${TARGET_CLUSTER}-kafka-bootstrap.${TARGET_NS}.svc.cluster.local:9092"

# ── Phase 2: the legacy source ──────────────────────────────────────────────
phase "2/11  deploying the Kafka ${SOURCE_VERSION} source"

if [ "${ERA}" = "2x" ] && [ "${SKIP_BUILD}" = "false" ] && [ -z "${LEGACY_IMAGE}" ]; then
    # No official Apache image exists below 3.7.0, so 2.x needs one built.
    step "Building the 2.x image (no upstream image exists for this line)..."
    if command -v docker >/dev/null 2>&1; then
        "${SCRIPT_DIR}/build-legacy-kafka-image.sh" --version "${SOURCE_VERSION}" --load
    else
        error "docker not found and --skip-build not given."
        error "Build elsewhere and pass --legacy-image, or install docker."
        exit 1
    fi
fi

if kubectl get namespace "${LEGACY_NS}" >/dev/null 2>&1; then
    warn "  namespace ${LEGACY_NS} already exists — reusing it, and leaving it behind on cleanup"
else
    kubectl create namespace "${LEGACY_NS}" >/dev/null
    CREATED_NS="true"
fi

HELM_LEGACY=(upgrade --install "${LEGACY_RELEASE}" "${ROOT_DIR}/charts/legacy-kafka"
    -n "${LEGACY_NS}"
    -f "${ROOT_DIR}/charts/legacy-kafka/${ERA_VALUES}"
    -f "${ROOT_DIR}/charts/legacy-kafka/values-kind.yaml"
    --set "topics[0].name=${TOPIC}"
    --set "topics[0].partitions=3"
    --set "topics[0].replicationFactor=1"
    --wait --timeout "${TIMEOUT}s")
[ -n "${LEGACY_IMAGE}" ] && HELM_LEGACY+=(--set "kafka.image=${LEGACY_IMAGE}")

if helm "${HELM_LEGACY[@]}"; then
    record PASS "legacy source deployed" "Kafka ${SOURCE_VERSION} (${MODE})"
else
    record FAIL "legacy source deployed" "helm install failed"
    kubectl -n "${LEGACY_NS}" get pods || true
    kubectl -n "${LEGACY_NS}" logs "sts/${LEGACY_RELEASE}-legacy-kafka" --tail=50 || true
    exit 1
fi

SOURCE_BOOTSTRAP="${LEGACY_RELEASE}-legacy-kafka-bootstrap.${LEGACY_NS}.svc.cluster.local:9092"
LEGACY_IMAGE_EFFECTIVE=$(kubectl -n "${LEGACY_NS}" get sts "${LEGACY_RELEASE}-legacy-kafka" \
    -o jsonpath='{.spec.template.spec.containers[0].image}')

if helm test "${LEGACY_RELEASE}" -n "${LEGACY_NS}" --timeout "${TIMEOUT}s" >/dev/null 2>&1; then
    record PASS "legacy source serving" "${SOURCE_BOOTSTRAP}"
else
    record FAIL "legacy source serving" "helm test failed — the broker is not answering"
    kubectl -n "${LEGACY_NS}" logs "${LEGACY_RELEASE}-legacy-kafka-test-broker" || true
    exit 1
fi

# ── Phase 3: seed the source ────────────────────────────────────────────────
phase "3/11  producing ${MESSAGES} records to ${TOPIC} on the source"

# The source's OWN client, because a 4.x client cannot talk to a 2.0 broker and
# we want any such failure attributed to the broker, not to the tooling.
PRODUCE_SCRIPT="i=1; while [ \$i -le ${MESSAGES} ]; do echo \"migrate-\$i\"; i=\$((i+1)); done | /opt/kafka/bin/kafka-console-producer.sh --bootstrap-server ${SOURCE_BOOTSTRAP} --topic ${TOPIC} && echo PRODUCED_OK"

if kafka_cli "${LEGACY_NS}" "${LEGACY_IMAGE_EFFECTIVE}" "${PRODUCE_SCRIPT}" | grep -q PRODUCED_OK; then
    record PASS "corpus produced" "${MESSAGES} records → ${TOPIC}"
else
    record FAIL "corpus produced" "the producer did not report success"
    dump_diagnostics
    exit 1
fi

SOURCE_END_OFFSETS=$(kafka_cli "${LEGACY_NS}" "${LEGACY_IMAGE_EFFECTIVE}" \
    "$(sum_end_offsets "${SOURCE_BOOTSTRAP}" "${TOPIC}")" | tr -d '\r' | extract_offset_sum || true)

if [ -n "${SOURCE_END_OFFSETS}" ] && [ "${SOURCE_END_OFFSETS}" -ge "${MESSAGES}" ]; then
    record PASS "source end offsets" "${SOURCE_END_OFFSETS} across all partitions"
else
    record FAIL "source end offsets" "expected >= ${MESSAGES}, measured '${SOURCE_END_OFFSETS:-<no reading>}'"
fi

# ── Phase 4: a consumer group to translate ──────────────────────────────────
phase "4/11  committing a consumer-group offset on the source"

CONSUME_HALF=$((MESSAGES / 2))
# The count is emitted behind a marker rather than as a bare number: kafka_cli's
# output is a pod's stdout, and a bare digit is indistinguishable from anything
# else the CLI decided to print.
GROUP_SCRIPT="/opt/kafka/bin/kafka-console-consumer.sh --bootstrap-server ${SOURCE_BOOTSTRAP} --topic ${TOPIC} --group ${GROUP} --from-beginning --timeout-ms 60000 --max-messages ${CONSUME_HALF} >/dev/null 2>&1; N=\$(/opt/kafka/bin/kafka-consumer-groups.sh --bootstrap-server ${SOURCE_BOOTSTRAP} --describe --group ${GROUP} 2>/dev/null | grep -c \"^${GROUP} \"); echo \"GROUPPARTS=\$N\""

GROUP_PARTITIONS=$(kafka_cli "${LEGACY_NS}" "${LEGACY_IMAGE_EFFECTIVE}" "${GROUP_SCRIPT}" \
    | tr -d '\r' | sed -n 's/^GROUPPARTS=\([0-9][0-9]*\)$/\1/p' | tail -1 || true)
if [ -n "${GROUP_PARTITIONS}" ] && [ "${GROUP_PARTITIONS}" -gt 0 ]; then
    record PASS "source consumer group" "${GROUP} committed on ${GROUP_PARTITIONS} partition(s)"
else
    record FAIL "source consumer group" "no committed offset — offset translation cannot be tested"
fi

# ── Phase 5: MirrorMaker 2 ──────────────────────────────────────────────────
phase "5/11  installing MirrorMaker 2 (${POLICY} policy, preflight on)"

# The mirror is described in a values file rather than a wall of --set flags:
# Helm's --set parser treats a backslash as an escape, so `kates\..*` arrives
# as `kates..*` — a regex that happens to still match, which is worse than one
# that fails. A values file is read verbatim.
cat > "${MIRROR_VALUES}" <<EOF
replicationPolicy:
  mode: ${POLICY}
mirrors:
  - source:
      alias: legacy
      bootstrapServers: "${SOURCE_BOOTSTRAP}"
      kafkaVersion: "${SOURCE_VERSION}"
      authentication:
        type: ""
    topicsPattern: 'kates\\..*'
    groupsPattern: '.*'
    sourceConnector:
      tasksMax: 2
      config:
        replication.factor: 1
        offset-syncs.topic.replication.factor: 1
        sync.topic.acls.enabled: "false"
        sync.topic.configs.enabled: "true"
        refresh.topics.interval.seconds: 20
    checkpointConnector:
      tasksMax: 1
      config:
        checkpoints.topic.replication.factor: 1
        sync.group.offsets.enabled: "true"
        sync.group.offsets.interval.seconds: 10
        emit.checkpoints.interval.seconds: 10
        refresh.groups.interval.seconds: 20
EOF

if helm upgrade --install "${MM2_RELEASE}" "${ROOT_DIR}/charts/mirror-maker2" \
        -n "${TARGET_NS}" \
        -f "${ROOT_DIR}/charts/mirror-maker2/${PRESET}" \
        -f "${MIRROR_VALUES}" \
        --timeout "${TIMEOUT}s"; then
    record PASS "MirrorMaker 2 installed" "preflight passed — the source answered a 4.x client"
else
    record FAIL "MirrorMaker 2 installed" "helm install failed (preflight, or the CR itself)"
    kubectl -n "${TARGET_NS}" logs "job/${MM2_CR}-preflight" 2>/dev/null || true
    dump_diagnostics
    exit 1
fi

# ── Phase 6: Ready, and actually running ────────────────────────────────────
phase "6/11  waiting for the CR and the connectors"

if kubectl -n "${TARGET_NS}" wait "kafkamirrormaker2/${MM2_CR}" \
        --for=condition=Ready --timeout="${TIMEOUT}s" >/dev/null 2>&1; then
    record PASS "CR Ready" "Connect workers are up"
else
    record FAIL "CR Ready" "did not become Ready in ${TIMEOUT}s"
    dump_diagnostics
    exit 1
fi

# Ready says nothing about the mirror. The connector status does.
DEADLINE=$(( $(date +%s) + TIMEOUT ))
CONNECTOR_STATES=""
while :; do
    CONNECTOR_STATES=$(kubectl -n "${TARGET_NS}" get "kafkamirrormaker2/${MM2_CR}" \
        -o jsonpath='{range .status.connectors[*]}{.name}={.connector.state}{"\n"}{end}' 2>/dev/null || true)
    RUNNING=$(printf '%s\n' "${CONNECTOR_STATES}" | grep -c '=RUNNING' || true)
    [ "${RUNNING}" -ge 2 ] && break
    if [ "$(date +%s)" -ge "${DEADLINE}" ]; then
        record FAIL "connectors RUNNING" "only ${RUNNING} of 2 running after ${TIMEOUT}s"
        echo "${CONNECTOR_STATES}"
        dump_diagnostics
        exit 1
    fi
    sleep 10
done
record PASS "connectors RUNNING" "$(echo "${CONNECTOR_STATES}" | tr '\n' ' ')"

# ── Phase 7: the replicated topic ───────────────────────────────────────────
phase "7/11  waiting for ${REPLICATED_TOPIC} to appear on the target"

DEADLINE=$(( $(date +%s) + TIMEOUT ))
FOUND="false"
LIST=""
while :; do
    LIST=$(kafka_cli_target "${TARGET_CFG} /opt/kafka/bin/kafka-topics.sh --bootstrap-server ${TARGET_BOOTSTRAP} --command-config ${TARGET_PROPS} --list" | tr -d '\r')
    if printf '%s\n' "${LIST}" | grep -Fqx "${REPLICATED_TOPIC}"; then
        FOUND="true"; break
    fi
    if [ "$(date +%s)" -ge "${DEADLINE}" ]; then break; fi
    sleep 15
done

if [ "${FOUND}" = "true" ]; then
    record PASS "replicated topic exists" "${REPLICATED_TOPIC} on the target"
else
    record FAIL "replicated topic exists" "${REPLICATED_TOPIC} never appeared"
    echo "  topics on the target:"; printf '%s\n' "${LIST}" | sed 's/^/    /' | head -30
    warn "  If you see legacy.${TOPIC}, the replication policy in force is not ${POLICY}."
    dump_diagnostics
    exit 1
fi

# ── Phase 8: the data itself ────────────────────────────────────────────────
phase "8/11  reading the corpus back off the target"

# A NAMED consumer group. Without --group the console consumer joins
# console-consumer-<random>, which no ACL grant covers, and the resulting
# GroupAuthorizationException reads as "nothing replicated".
CONSUME="${TARGET_CFG} /opt/kafka/bin/kafka-console-consumer.sh --bootstrap-server ${TARGET_BOOTSTRAP} --consumer.config ${TARGET_PROPS} --topic ${REPLICATED_TOPIC} --group kates-migration-verify-\$RANDOM --from-beginning --timeout-ms 120000 --max-messages $((MESSAGES * 2))"

CORPUS=$(kafka_cli_target "${CONSUME}" | tr -d '\r' | grep '^migrate-[0-9]*$' || true)
LINES=$(printf '%s\n' "${CORPUS}" | grep -c '^migrate-' || true)
DISTINCT=$(printf '%s\n' "${CORPUS}" | sort -u | grep -c '^migrate-' || true)

# Distinct values, not lines. MirrorMaker 2 is at-least-once, so a retry can
# duplicate records; a line count would let a duplicated corpus with gaps pass.
if [ "${DISTINCT:-0}" -ge "${MESSAGES}" ]; then
    record PASS "record count" "${DISTINCT} distinct of ${MESSAGES} received (${LINES} lines — duplicates are at-least-once, not a fault)"
else
    record FAIL "record count" "only ${DISTINCT} distinct of ${MESSAGES} arrived (${LINES} lines)"
fi

# Content: every expected suffix 1..N is present. This is the assertion that
# min-and-max cannot make — a corpus missing 2..N-1 has the same endpoints.
MISSING=$(comm -23 \
    <(seq 1 "${MESSAGES}" | sed 's/^/migrate-/' | sort) \
    <(printf '%s\n' "${CORPUS}" | sort -u) | head -5 | tr '\n' ' ' || true)
if [ -z "${MISSING// /}" ]; then
    record PASS "record content" "every record 1..${MESSAGES} present on the target"
else
    record FAIL "record content" "missing from the target: ${MISSING}..."
fi

# ── Phase 9: offset translation ─────────────────────────────────────────────
phase "9/11  verifying consumer-group offset translation"

DEADLINE=$(( $(date +%s) + TIMEOUT ))
TRANSLATED="false"
OUT=""
while :; do
    OUT=$(kafka_cli_target "${TARGET_CFG} /opt/kafka/bin/kafka-consumer-groups.sh --bootstrap-server ${TARGET_BOOTSTRAP} --command-config ${TARGET_PROPS} --describe --group ${GROUP}" | tr -d '\r')
    if printf '%s\n' "${OUT}" | grep -q "${REPLICATED_TOPIC}"; then
        TRANSLATED="true"; break
    fi
    [ "$(date +%s)" -ge "${DEADLINE}" ] && break
    sleep 15
done

if [ "${TRANSLATED}" = "true" ]; then
    record PASS "offset translation" "${GROUP} has a committed position on ${REPLICATED_TOPIC}"
    printf '%s\n' "${OUT}" | head -6 | sed 's/^/      /'
else
    record FAIL "offset translation" "${GROUP} never appeared on the target"
    warn "  Check sync.group.offsets.enabled, groupsPattern, and the target user's group ACLs."
fi

# ── Phase 10: cutover rehearsal ─────────────────────────────────────────────
phase "10/11  cutover rehearsal — stopping the source connector"

TARGET_OFFSET_CMD=$(sum_end_offsets "${TARGET_BOOTSTRAP}" "${REPLICATED_TOPIC}" "--command-config ${TARGET_PROPS}")

if helm upgrade "${MM2_RELEASE}" "${ROOT_DIR}/charts/mirror-maker2" \
        -n "${TARGET_NS}" --reuse-values \
        -f "${ROOT_DIR}/charts/mirror-maker2/values-cutover.yaml" \
        --timeout "${TIMEOUT}s" >/dev/null 2>&1; then
    record PASS "cutover applied" "source connector stopped, checkpoint connector still running"
else
    record FAIL "cutover applied" "helm upgrade with values-cutover.yaml failed"
fi

# The baseline is taken AFTER the cutover has settled, not before it is applied.
# Sampling first put every record still in flight from phase 3 between the two
# readings, and attributed them to "the source connector did not stop".
step "  letting the cutover settle before taking a baseline..."
sleep 45
OFFSETS_BEFORE=$(kafka_cli_target "${TARGET_CFG} ${TARGET_OFFSET_CMD}" | tr -d '\r' | extract_offset_sum || true)

# Produce MORE to the source. With the source connector stopped, none of it may
# reach the target — that is what makes a cutover a cutover rather than a pause
# with extra steps.
kafka_cli "${LEGACY_NS}" "${LEGACY_IMAGE_EFFECTIVE}" \
    "i=1; while [ \$i -le 50 ]; do echo \"after-cutover-\$i\"; i=\$((i+1)); done | /opt/kafka/bin/kafka-console-producer.sh --bootstrap-server ${SOURCE_BOOTSTRAP} --topic ${TOPIC}" >/dev/null || true
sleep 45

OFFSETS_AFTER=$(kafka_cli_target "${TARGET_CFG} ${TARGET_OFFSET_CMD}" | tr -d '\r' | extract_offset_sum || true)

# Three outcomes, not two — and a sanity floor on the baseline. After phase 8
# the target provably holds at least MESSAGES records, so a baseline below that
# is a broken measurement, whatever number it happens to be.
if [ -z "${OFFSETS_BEFORE}" ] || [ -z "${OFFSETS_AFTER}" ]; then
    record FAIL "cutover froze the target" \
        "could not read end offsets (before='${OFFSETS_BEFORE:-<none>}' after='${OFFSETS_AFTER:-<none>}') — the measurement failed, not necessarily the cutover"
elif [ "${OFFSETS_BEFORE}" -lt "${MESSAGES}" ]; then
    record FAIL "cutover froze the target" \
        "baseline ${OFFSETS_BEFORE} is below the ${MESSAGES} records phase 8 already read — the offset tool is not measuring what it should"
elif [ "${OFFSETS_BEFORE}" = "${OFFSETS_AFTER}" ]; then
    record PASS "cutover froze the target" "end offsets unchanged at ${OFFSETS_AFTER} after 50 more source records"
else
    record FAIL "cutover froze the target" \
        "offsets moved ${OFFSETS_BEFORE} → ${OFFSETS_AFTER} — the source connector did not stop"
fi

# ── Phase 11: report ────────────────────────────────────────────────────────
phase "11/11  report"

echo ""
printf '  %-6s %-28s %s\n' "RESULT" "ASSERTION" "DETAIL"
printf '  %-6s %-28s %s\n' "------" "----------------------------" "------"
for r in "${RESULTS[@]}"; do
    printf '  %-6s %-28s %s\n' "${r%%|*}" "$(echo "$r" | cut -d'|' -f2)" "$(echo "$r" | cut -d'|' -f3)"
done
echo ""

if [ "${FAILURES}" -eq 0 ]; then
    info "✅ Kafka ${SOURCE_VERSION} → 4.x migration verified end to end (${POLICY} policy)"
    exit 0
fi
error "❌ ${FAILURES} assertion(s) failed for the Kafka ${SOURCE_VERSION} → 4.x path"
dump_diagnostics
exit 1

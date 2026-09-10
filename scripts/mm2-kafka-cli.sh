#!/usr/bin/env bash
# Run a Kafka CLI command against the in-repo cluster, as the MM2 user — from a
# throwaway pod on the broker network, with the credential injected from its
# Secret, using whichever offset tool the target's Kafka line ships.
#
# WHY THIS EXISTS: "is the mirror replicating?" has exactly one honest answer —
# the target's end offsets, read twice, moving — and "what did it create?" has
# exactly one honest source, the brokers. Neither the CR's Ready condition nor
# `kubectl get kafkatopics` can say either (the Topic Operator is
# unidirectional; MirrorMaker's topics never become KafkaTopic resources).
# Getting those numbers by hand needs a pod the broker NetworkPolicy admits,
# the SCRAM password, and the right tool for the version: kafka-get-offsets.sh
# on 3.4+, the kafka.tools.GetOffsetShell class before that — calling the wrong
# one prints nothing useful. This does all of it and prints one line.
#
# Usage:
#   scripts/mm2-kafka-cli.sh topics                       # list topics on the target
#   scripts/mm2-kafka-cli.sh offsets <topic>              # sum of end offsets
#   scripts/mm2-kafka-cli.sh offsets <topic> --partitions # per-partition lines too
#   scripts/mm2-kafka-cli.sh groups                       # list consumer groups
#   scripts/mm2-kafka-cli.sh group <group>                # describe one group
#
# Options (all subcommands):
#   --namespace NS   Namespace of the Kafka cluster (default: kafka)
#   --cluster NAME   Strimzi cluster name (default: krafter)
#   --user NAME      KafkaUser whose Secret holds the SCRAM password (default: kates-mm2)
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=scripts/common.sh
source "${SCRIPT_DIR}/common.sh"
ROOT_DIR="$(cd "${SCRIPT_DIR}/.." && pwd)"

NAMESPACE="kafka"
CLUSTER="krafter"
USER_NAME="kates-mm2"
SHOW_PARTITIONS="false"
SUBCOMMAND=""
ARG=""

usage() { sed -n '/^# Usage:/,/^set -euo/p' "${BASH_SOURCE[0]}" | sed '$d' | sed 's/^# \{0,1\}//'; }

while [ $# -gt 0 ]; do
    case "$1" in
        --namespace)  [ $# -ge 2 ] || { usage; exit 1; }; NAMESPACE="$2"; shift 2 ;;
        --cluster)    [ $# -ge 2 ] || { usage; exit 1; }; CLUSTER="$2"; shift 2 ;;
        --user)       [ $# -ge 2 ] || { usage; exit 1; }; USER_NAME="$2"; shift 2 ;;
        --partitions) SHOW_PARTITIONS="true"; shift ;;
        -h|--help)    usage; exit 0 ;;
        -*)           error "Unknown option: $1"; usage; exit 1 ;;
        *)
            if [ -z "${SUBCOMMAND}" ]; then SUBCOMMAND="$1"
            elif [ -z "${ARG}" ]; then ARG="$1"
            else error "Unexpected argument: $1"; usage; exit 1; fi
            shift ;;
    esac
done

case "${SUBCOMMAND}" in
    topics|groups) ;;
    offsets|group) [ -n "${ARG}" ] || { error "${SUBCOMMAND} needs an argument"; usage; exit 1; } ;;
    *) usage; exit 1 ;;
esac

require_cmd kubectl
require_cmd python3
# shellcheck source=versions.env
source "${ROOT_DIR}/versions.env"
IMAGE="quay.io/strimzi/kafka:${STRIMZI_KAFKA_VERSION}"
BOOTSTRAP="${CLUSTER}-kafka-bootstrap.${NAMESPACE}.svc.cluster.local:9092"
MARKER="__KATES_CLI_EOF__"
CC="--bootstrap-server ${BOOTSTRAP} --command-config /tmp/c.properties"

case "${SUBCOMMAND}" in
    topics)  KAFKA_CMD="/opt/kafka/bin/kafka-topics.sh ${CC} --list" ;;
    groups)  KAFKA_CMD="/opt/kafka/bin/kafka-consumer-groups.sh ${CC} --list" ;;
    group)   KAFKA_CMD="/opt/kafka/bin/kafka-consumer-groups.sh ${CC} --describe --group ${ARG}" ;;
    offsets) KAFKA_CMD="if [ -x /opt/kafka/bin/kafka-get-offsets.sh ]; then /opt/kafka/bin/kafka-get-offsets.sh ${CC} --topic ${ARG}; else /opt/kafka/bin/kafka-run-class.sh kafka.tools.GetOffsetShell ${CC} --topic ${ARG}; fi" ;;
esac

# The password is injected from the Secret as an env var and turned into a
# client.properties INSIDE the pod — never interpolated into the command line,
# where it would be readable from the pod spec for as long as the pod exists.
# It is escaped for the JAAS string, and written with printf, not echo: dash's
# echo interprets backslashes.
# shellcheck disable=SC2016
CMD='PW=$(printf "%s" "$TARGET_PW" | sed "s/[\\\\\"]/\\\\&/g"); echo security.protocol=SASL_PLAINTEXT > /tmp/c.properties; echo sasl.mechanism=SCRAM-SHA-512 >> /tmp/c.properties; printf "%s\n" "sasl.jaas.config=org.apache.kafka.common.security.scram.ScramLoginModule required username=\"'"${USER_NAME}"'\" password=\"$PW\";" >> /tmp/c.properties; '"${KAFKA_CMD}"'; echo '"${MARKER}"

NAME="mm2-cli-${RANDOM}"
OVERRIDES=$(python3 - "${NAME}" "${IMAGE}" "${USER_NAME}" "${CMD}" <<'PY'
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

ERR="$(mktemp -t mm2-cli.XXXXXX)"
trap 'rm -f "${ERR}"' EXIT
OUT=$(kubectl -n "${NAMESPACE}" run "${NAME}" --rm -i --restart=Never \
    --image="${IMAGE}" \
    --labels="kates.io/test-pod=true,app.kubernetes.io/part-of=kates" \
    --pod-running-timeout=5m \
    --overrides="${OVERRIDES}" 2>"${ERR}" \
    | awk -v m="${MARKER}" '$0 == m { exit } { print }' | tr -d '\r' || true)

case "${SUBCOMMAND}" in
    offsets)
        ROWS=$(printf '%s\n' "${OUT}" | grep -E "^${ARG}:[0-9]+:[0-9]+$" || true)
        if [ -z "${ROWS}" ]; then
            error "no offsets returned for ${ARG} on ${BOOTSTRAP} — does the topic exist, and can ${USER_NAME} read it?"
            printf '%s\n' "${OUT}" | head -10 >&2
            grep -v '^pod .* deleted$' "${ERR}" | tail -5 >&2 || true
            exit 1
        fi
        [ "${SHOW_PARTITIONS}" = "true" ] && printf '%s\n' "${ROWS}"
        echo "${ARG} end offsets: $(printf '%s\n' "${ROWS}" | awk -F: '{s+=$3} END {print s+0}')"
        ;;
    *)
        if [ -z "${OUT}" ]; then
            error "no output from the broker at ${BOOTSTRAP} as ${USER_NAME}:"
            grep -v '^pod .* deleted$' "${ERR}" | tail -8 >&2 || true
            exit 1
        fi
        printf '%s\n' "${OUT}"
        ;;
esac

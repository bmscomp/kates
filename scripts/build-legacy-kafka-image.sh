#!/usr/bin/env bash
# Build the legacy Kafka image used as a MirrorMaker 2 migration source.
#
# WHY THIS EXISTS: the official Apache Kafka image starts at 3.7.0 (KIP-975).
# The 2.x line — which is precisely what a "2.x → 4.x migration" needs on the
# source side — has no upstream image at all, and no third-party one with an
# arm64 variant. Dockerfile.legacy-kafka builds one from the Apache tarball;
# this script drives it and loads the result into the kind cluster, which is
# the step everyone forgets and then spends ten minutes debugging ImagePullBackOff.
#
# Usage:
#   scripts/build-legacy-kafka-image.sh                       # 2.8.2, build only
#   scripts/build-legacy-kafka-image.sh --version 2.8.2 --load
#   scripts/build-legacy-kafka-image.sh --version 3.9.1 --scala 2.13 --jre 17 --load
#   scripts/build-legacy-kafka-image.sh --version 2.8.2 --platform linux/amd64,linux/arm64 --push
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=scripts/common.sh
source "${SCRIPT_DIR}/common.sh"
ROOT_DIR="${SCRIPT_DIR}/.."

KAFKA_VERSION="2.8.2"
SCALA_VERSION="2.13"
JRE_VERSION=""
REGISTRY="ghcr.io/bmscomp"
IMAGE_NAME="kates-legacy-kafka"
LOAD="false"
PUSH="false"
PLATFORM=""
OS_RELEASE="jammy"

usage() {
    cat <<'EOF'
Usage: scripts/build-legacy-kafka-image.sh [options]

Options:
  --version VER     Kafka version to package (default: 2.8.2)
  --scala VER       Scala build of the tarball (default: 2.13)
  --jre VER         Temurin JRE major version (default: 11 for 2.x, 17 for 3.x)
  --registry REG    Image registry/namespace (default: ghcr.io/bmscomp)
  --platform P      Buildx platform(s), e.g. linux/arm64 or a comma list
                    (default: the host's)
  --os-release R    Ubuntu release of the Temurin base image (default: jammy)
  --load            Load the image into the kind cluster after building
  --push            Push the image to the registry (multi-platform capable;
                    mutually exclusive with --load — a pushed manifest list
                    is not in the local daemon for kind to load)
  -h, --help        Show this help

Notes:
  • Kafka 2.x needs a Java 8/11 runtime. Java 17 is not supported before 3.0
    and will fail at start-up in ways that look like a config error.
  • The tarball is checksum-verified inside the build; a bad download fails
    the build rather than producing a broken image.
  • The base image is pinned to an Ubuntu release, not the floating tag: the
    floating one moved to a release that ships a default user at uid 1000,
    which is the uid this image runs as.
EOF
}

while [ $# -gt 0 ]; do
    case "$1" in
        --version)  KAFKA_VERSION="$2"; shift 2 ;;
        --scala)    SCALA_VERSION="$2"; shift 2 ;;
        --jre)      JRE_VERSION="$2";   shift 2 ;;
        --registry) REGISTRY="$2";      shift 2 ;;
        --platform) PLATFORM="$2";      shift 2 ;;
        --os-release) OS_RELEASE="$2";  shift 2 ;;
        --load)     LOAD="true";  shift ;;
        --push)     PUSH="true";  shift ;;
        -h|--help)  usage; exit 0 ;;
        *) error "Unknown option: $1"; usage; exit 1 ;;
    esac
done

require_cmd docker

if [ "${PUSH}" = "true" ] && [ "${LOAD}" = "true" ]; then
    error "--push and --load are mutually exclusive: a pushed (possibly multi-platform)"
    error "image is not in the local daemon, so there is nothing for kind to load."
    exit 1
fi
if [ "${PUSH}" = "true" ] && ! docker buildx version >/dev/null 2>&1; then
    error "--push needs docker buildx (docker buildx version failed)."
    exit 1
fi

# Java support windows, not preferences: Kafka 2.x supports Java 8 and 11 only;
# 3.x added 17. Picking wrong here produces a start-up crash that reads like a
# broker misconfiguration, so it is decided here rather than left to the user.
if [ -z "${JRE_VERSION}" ]; then
    case "${KAFKA_VERSION}" in
        2.*) JRE_VERSION="11" ;;
        *)   JRE_VERSION="17" ;;
    esac
fi

IMAGE="${REGISTRY}/${IMAGE_NAME}:${KAFKA_VERSION}"

bold "Building ${IMAGE}"
info "  Kafka ${KAFKA_VERSION} (scala ${SCALA_VERSION}) on Temurin JRE ${JRE_VERSION}"

BUILD_ARGS=(
    --build-arg "KAFKA_VERSION=${KAFKA_VERSION}"
    --build-arg "SCALA_VERSION=${SCALA_VERSION}"
    --build-arg "JRE_VERSION=${JRE_VERSION}"
    --build-arg "UBUNTU_RELEASE=${OS_RELEASE}"
    -f "${ROOT_DIR}/Dockerfile.legacy-kafka"
    -t "${IMAGE}"
)
[ -n "${PLATFORM}" ] && BUILD_ARGS+=(--platform "${PLATFORM}")

verify_image() {
    step "Verifying the image actually contains Kafka ${KAFKA_VERSION}..."
    # The main Kafka jar is kafka_<scala>-<version>.jar; anchor on that shape
    # so a classifier jar sorting first cannot make the check pass or fail
    # for the wrong reason.
    local found
    found=$(docker run --rm --entrypoint sh "${IMAGE}" -c \
        'ls /opt/kafka/libs/ | grep -E "^kafka_[0-9.]+-[0-9.]+\.jar$" | head -1 | sed -E "s|^kafka_[0-9.]+-([0-9.]+)\.jar$|\1|"')
    if [ "${found}" != "${KAFKA_VERSION}" ]; then
        error "❌ image reports Kafka '${found}', expected ${KAFKA_VERSION}"
        exit 1
    fi
    info "✅ image contains Kafka ${found}"
}

if [ "${PUSH}" = "true" ]; then
    docker buildx build "${BUILD_ARGS[@]}" --push "${ROOT_DIR}"
    info "✅ pushed ${IMAGE}"
    # A pushed manifest is not in the local store; verification would pull it
    # back (and cannot run a foreign-arch image at all). Verify by pulling the
    # host's platform explicitly, which is the one docker run can execute.
    docker pull --quiet "${IMAGE}" >/dev/null
    verify_image
else
    docker build "${BUILD_ARGS[@]}" "${ROOT_DIR}"
    info "✅ built ${IMAGE}"
    verify_image
fi

if [ "${LOAD}" = "true" ]; then
    require_cmd kind
    require_cluster
    step "Loading ${IMAGE} into kind cluster '${KIND_CLUSTER_NAME}'..."
    kind load docker-image "${IMAGE}" --name "${KIND_CLUSTER_NAME}"
    info "✅ loaded into kind"
fi

bold "Use it:"
echo "  helm upgrade --install legacy charts/legacy-kafka -n kafka-legacy --create-namespace \\"
echo "    -f charts/legacy-kafka/values-kafka-2x.yaml -f charts/legacy-kafka/values-kind.yaml \\"
echo "    --set kafka.image=${IMAGE}"

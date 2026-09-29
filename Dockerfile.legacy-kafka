# A migration-source Kafka for versions that have no container image.
#
# WHY THIS EXISTS: the official Apache Kafka image (KIP-975) starts at 3.7.0.
# Below that there is nothing upstream, and every third-party 2.x image is
# either amd64-only, unmaintained, or both — which is fatal on Apple Silicon,
# where the kind nodes are arm64.
#
# Kafka is pure Java, so the fix is not a port: it is a JRE plus the Apache
# convenience tarball. The result runs on any architecture the base image
# supports, and its layout matches the official image (/opt/kafka/bin,
# /opt/kafka/config) so charts/legacy-kafka can point at either one without
# knowing the difference.
#
# The tarball is verified against the Apache SHA-512 checksum published
# alongside it. A build that silently accepts a corrupted or substituted
# archive is worse than a build that fails.
#
# Build:
#   docker build -f Dockerfile.legacy-kafka \
#     --build-arg KAFKA_VERSION=2.8.2 --build-arg SCALA_VERSION=2.13 \
#     -t ghcr.io/bmscomp/kates-legacy-kafka:2.8.2 .
#
# Or: scripts/build-legacy-kafka-image.sh --version 2.8.2 --load

ARG JRE_VERSION=11
# The Ubuntu release is pinned, not just the JRE. The floating `-jre` tag
# moved to Ubuntu 24.04+, which ships a default `ubuntu` account at uid/gid
# 1000 — and 1000 is the uid this image (and the official apache/kafka image)
# runs as, so `groupadd -g 1000` below failed the build outright. jammy (22.04)
# has no such account; the runtime stage still checks, in case that changes.
ARG UBUNTU_RELEASE=jammy

# ─── Stage 1: fetch and verify the Apache tarball ────────────────────────────
FROM eclipse-temurin:${JRE_VERSION}-jre-${UBUNTU_RELEASE} AS download

# 2.8.2 is the last 2.x release. Scala 2.13 is the default build for that line.
ARG KAFKA_VERSION=2.8.2
ARG SCALA_VERSION=2.13
# archive.apache.org, not dlcdn: old releases are moved off the CDN, and a
# mirror that 404s at build time is the failure mode this pins away.
ARG KAFKA_BASE_URL=https://archive.apache.org/dist/kafka

USER root
RUN set -eux; \
    apt-get update && apt-get install -y --no-install-recommends curl ca-certificates; \
    rm -rf /var/lib/apt/lists/*; \
    TARBALL="kafka_${SCALA_VERSION}-${KAFKA_VERSION}.tgz"; \
    URL="${KAFKA_BASE_URL}/${KAFKA_VERSION}/${TARBALL}"; \
    curl -fsSL -o "/tmp/${TARBALL}" "${URL}"; \
    curl -fsSL -o "/tmp/${TARBALL}.sha512" "${URL}.sha512"; \
    cd /tmp; \
    # Apache publishes this file in two shapes depending on the release:
    # `<hash>  <file>` (sha512sum format) or `<file>: <HASH IN GROUPS>`.
    # Normalise both to the first before checking, so the verification is not
    # quietly skipped on one of them.
    if grep -qi '^[0-9a-f]\{128\}' "${TARBALL}.sha512"; then \
      awk '{print $1"  '"${TARBALL}"'"}' "${TARBALL}.sha512" > sum.txt; \
    else \
      tr -d ' \n' < "${TARBALL}.sha512" | sed 's/.*://' | tr 'A-Z' 'a-z' \
        | awk '{print $1"  '"${TARBALL}"'"}' > sum.txt; \
    fi; \
    sha512sum -c sum.txt; \
    mkdir -p /opt/kafka; \
    tar -xzf "${TARBALL}" -C /opt/kafka --strip-components=1; \
    rm -f "${TARBALL}" "${TARBALL}.sha512" sum.txt; \
    # Windows scripts and the source jars are dead weight in a container.
    rm -rf /opt/kafka/bin/windows /opt/kafka/site-docs

# ─── Stage 2: runtime ────────────────────────────────────────────────────────
FROM eclipse-temurin:${JRE_VERSION}-jre-${UBUNTU_RELEASE}

ARG KAFKA_VERSION=2.8.2
ARG SCALA_VERSION=2.13

LABEL org.opencontainers.image.title="kates-legacy-kafka" \
      org.opencontainers.image.description="Apache Kafka ${KAFKA_VERSION} as a MirrorMaker 2 migration source — lab use only" \
      org.opencontainers.image.version="${KAFKA_VERSION}" \
      org.opencontainers.image.source="https://github.com/bmscomp/kates" \
      org.opencontainers.image.licenses="Apache-2.0" \
      io.kates.kafka-version="${KAFKA_VERSION}" \
      io.kates.scala-version="${SCALA_VERSION}" \
      io.kates.purpose="migration-source"

COPY --from=download /opt/kafka /opt/kafka

# uid/gid 1000 matches the official apache/kafka image, so charts/legacy-kafka
# can apply one securityContext to both. If the base image already owns 1000
# (newer Ubuntu releases ship a default `ubuntu` user there), evict it rather
# than fail: what matters is that uid 1000 is the Kafka user, not its name.
RUN set -eux; \
    if existing=$(getent passwd 1000 | cut -d: -f1) && [ -n "$existing" ]; then userdel -r "$existing" 2>/dev/null || userdel "$existing" 2>/dev/null || true; fi; \
    if getent passwd 1000 >/dev/null; then echo "uid 1000 is still taken; cannot make it the Kafka user" >&2; exit 1; fi; \
    if getent group 1000 >/dev/null; then groupdel "$(getent group 1000 | cut -d: -f1)" || true; fi; \
    groupadd -g 1000 appuser; \
    useradd -u 1000 -g 1000 -M -s /sbin/nologin appuser; \
    mkdir -p /var/lib/kafka/data /var/lib/zookeeper/data; \
    chown -R 1000:1000 /opt/kafka /var/lib/kafka /var/lib/zookeeper

ENV PATH="/opt/kafka/bin:${PATH}" \
    KAFKA_VERSION="${KAFKA_VERSION}" \
    LOG_DIR="/tmp"

USER 1000:1000
WORKDIR /opt/kafka

# No ENTRYPOINT on purpose: charts/legacy-kafka supplies the command, because
# server.properties has to be generated from the pod's own identity.
CMD ["/opt/kafka/bin/kafka-server-start.sh", "/opt/kafka/config/server.properties"]

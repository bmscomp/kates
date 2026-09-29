FROM debian:trixie-slim

# trixie, not bookworm: bookworm left Debian's regular security support in
# June 2026, and on trixie the image carries about half the OS-package CVEs.
# default-jre-headless is OpenJDK 21 there; the Kafka 4.x tools need 17 or
# newer (their jars are Java 17 bytecode), so a base whose default JRE is
# older cannot run them.
RUN apt-get update && apt-get install -y --no-install-recommends \
    bash \
    curl \
    jq \
    kcat \
    netcat-openbsd \
    dnsutils \
    ca-certificates \
    default-jre-headless \
    && rm -rf /var/lib/apt/lists/*

# Install kubectl — auto-detect architecture for multi-arch builds
RUN ARCH=$(dpkg --print-architecture) && \
    case "$ARCH" in \
      amd64) KUBECTL_ARCH="amd64" ;; \
      arm64) KUBECTL_ARCH="arm64" ;; \
      *)     echo "Unsupported arch: $ARCH" && exit 1 ;; \
    esac && \
    curl -LO "https://dl.k8s.io/release/$(curl -L -s https://dl.k8s.io/release/stable.txt)/bin/linux/${KUBECTL_ARCH}/kubectl" && \
    chmod +x kubectl && \
    mv kubectl /usr/local/bin/

# Install Kafka scripts.
#
# The version is an ARG so the LABEL at the bottom cannot drift from what is
# actually installed — a hardcoded version in two places is a description that
# silently goes stale on the next bump. It follows the Kafka the charts deploy
# (versions.env STRIMZI_KAFKA_VERSION); scripts/check-versions.sh holds the two
# together, because a toolbox left on 3.7 against 4.x brokers is how this image
# collected the 3.7 line's CVEs.
#
# The tarball is checked against Apache's published SHA-512 (the .sha512 file
# next to it on archive.apache.org). site-docs and the Windows scripts are
# dropped: nothing in a Linux toolbox reads them.
#
# Then every jar listed in kafka-lib-overrides.txt is swapped for the patched
# release from Maven Central, each checked against a pinned SHA-256. The list
# explains which CVEs each swap clears and why it is tied to one Kafka release;
# MAVEN_REPO points the downloads at a mirror where Maven Central is not
# reachable.
#
# The tarball comes from Apache's CDN, which carries only current releases, and
# falls back to archive.apache.org, which carries every release but serves it
# at a trickle: 84 KB/s when measured, so a CI build sat on the 130 MB tarball
# until the job timed out. The pinned SHA-512 makes the source irrelevant to
# what gets installed. Every download retries, and one that stalls (under
# 10 KB/s for a minute) is dropped and retried.
ARG KAFKA_VERSION=4.3.1
ARG KAFKA_SCALA_VERSION=2.13
ARG KAFKA_SHA512=c7d7b2318cb51aa0c61d3246a51c349210073c5c9b754947ef965a439f2f939e8600f204e134a75ac31faf3829c9370960ef7c6a9886c8a1dbf0339a21f4c54c
ARG MAVEN_REPO=https://repo1.maven.org/maven2
WORKDIR /tmp
COPY kafka-lib-overrides.txt ./
RUN set -eu; \
    fetch() { curl -fsSL --retry 5 --retry-delay 5 --retry-all-errors --speed-limit 10240 --speed-time 60 "$@"; }; \
    tgz="kafka_${KAFKA_SCALA_VERSION}-${KAFKA_VERSION}.tgz"; \
    fetch -O "https://dlcdn.apache.org/kafka/${KAFKA_VERSION}/${tgz}" \
      || fetch -O "https://archive.apache.org/dist/kafka/${KAFKA_VERSION}/${tgz}"; \
    printf '%s  %s\n' "${KAFKA_SHA512}" "${tgz}" > "${tgz}.sha512"; \
    sha512sum -c "${tgz}.sha512"; \
    tar -xzf "${tgz}"; \
    mv "kafka_${KAFKA_SCALA_VERSION}-${KAFKA_VERSION}" /opt/kafka; \
    rm -rf "${tgz}" "${tgz}.sha512" /opt/kafka/site-docs /opt/kafka/bin/windows; \
    listed=$(sed -n 's/^kafka //p' kafka-lib-overrides.txt); \
    if [ "${listed}" != "${KAFKA_VERSION}" ]; then \
      echo "kafka-lib-overrides.txt is written for Kafka ${listed}, not ${KAFKA_VERSION}: review it against the new tarball" >&2; \
      exit 1; \
    fi; \
    grep -v -e '^#' -e '^kafka ' -e '^$' kafka-lib-overrides.txt > overrides; \
    while read -r old path sum; do \
      if [ ! -f "/opt/kafka/libs/${old}" ]; then \
        echo "Kafka ${KAFKA_VERSION} does not ship ${old}: review kafka-lib-overrides.txt" >&2; \
        exit 1; \
      fi; \
      rm "/opt/kafka/libs/${old}"; \
      new="/opt/kafka/libs/${path##*/}"; \
      fetch -o "${new}" "${MAVEN_REPO}/${path}"; \
      printf '%s  %s\n' "${sum}" "${new}" > jar.sha256; \
      sha256sum -c jar.sha256; \
    done < overrides; \
    rm -f overrides jar.sha256 kafka-lib-overrides.txt

ENV PATH=$PATH:/opt/kafka/bin

RUN groupadd -g 1000 kates && \
    useradd -u 1000 -g kates -m -s /bin/bash kates

USER 1000
WORKDIR /app
CMD ["bash"]

# Labels last: a LABEL change invalidates every following instruction in the
# build cache, and descriptions get edited far more often than the tooling does.
#
# What this image IS matters here, because the name invites the wrong guess:
# "kates-tester" is not a load generator and runs no tests of its own. It is the
# toolbox the Helm charts mount into test hooks and CRD hook Jobs — see
# testImages.kubectl / testImages.kafka in charts/*/values.yaml. Describing it
# as a workload runner would send people to the wrong image.
LABEL org.opencontainers.image.title="kates-tester" \
      org.opencontainers.image.description="Toolbox image for the Kates Helm charts — the container behind chart test hooks, CRD hook Jobs and in-cluster diagnostics. Ships kubectl, the Apache Kafka ${KAFKA_VERSION} CLI scripts on the PATH, kcat, jq, netcat, dig and a headless JRE on Debian trixie-slim. Runs as UID 1000, entrypoint is a shell — it generates no load and runs no tests by itself." \
      org.opencontainers.image.source="https://github.com/bmscomp/kates" \
      org.opencontainers.image.url="https://github.com/bmscomp/kates" \
      org.opencontainers.image.documentation="https://github.com/bmscomp/kates/blob/main/docs/book/README.md" \
      org.opencontainers.image.vendor="bmscomp" \
      org.opencontainers.image.licenses="Apache-2.0" \
      org.opencontainers.image.base.name="docker.io/library/debian:trixie-slim" \
      io.kates.kafka.version="${KAFKA_VERSION}"

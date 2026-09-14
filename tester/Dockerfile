FROM debian:bookworm-slim

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
# silently goes stale on the next bump.
ARG KAFKA_VERSION=3.7.0
ARG KAFKA_SCALA_VERSION=2.13
RUN curl -LO "https://archive.apache.org/dist/kafka/${KAFKA_VERSION}/kafka_${KAFKA_SCALA_VERSION}-${KAFKA_VERSION}.tgz" && \
    tar -xzf "kafka_${KAFKA_SCALA_VERSION}-${KAFKA_VERSION}.tgz" && \
    mv "kafka_${KAFKA_SCALA_VERSION}-${KAFKA_VERSION}" /opt/kafka && \
    rm "kafka_${KAFKA_SCALA_VERSION}-${KAFKA_VERSION}.tgz"

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
      org.opencontainers.image.description="Toolbox image for the Kates Helm charts — the container behind chart test hooks, CRD hook Jobs and in-cluster diagnostics. Ships kubectl, the Apache Kafka ${KAFKA_VERSION} CLI scripts on the PATH, kcat, jq, netcat, dig and a headless JRE on Debian bookworm-slim. Runs as UID 1000, entrypoint is a shell — it generates no load and runs no tests by itself." \
      org.opencontainers.image.source="https://github.com/bmscomp/kates" \
      org.opencontainers.image.url="https://github.com/bmscomp/kates" \
      org.opencontainers.image.documentation="https://github.com/bmscomp/kates/blob/main/docs/book/README.md" \
      org.opencontainers.image.vendor="bmscomp" \
      org.opencontainers.image.licenses="Apache-2.0" \
      org.opencontainers.image.base.name="docker.io/library/debian:bookworm-slim" \
      io.kates.kafka.version="${KAFKA_VERSION}"

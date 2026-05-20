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

# Install kubectl
RUN curl -LO "https://dl.k8s.io/release/$(curl -L -s https://dl.k8s.io/release/stable.txt)/bin/linux/amd64/kubectl" && \
    chmod +x kubectl && \
    mv kubectl /usr/local/bin/

# Install Kafka CLI tools (matching project kafkaVersion: 4.2.0)
RUN curl -LO "https://archive.apache.org/dist/kafka/4.2.0/kafka_2.13-4.2.0.tgz" && \
    tar -xzf kafka_2.13-4.2.0.tgz && \
    mv kafka_2.13-4.2.0 /opt/kafka && \
    rm kafka_2.13-4.2.0.tgz

ENV PATH=$PATH:/opt/kafka/bin

RUN groupadd -g 1000 kates && \
    useradd -u 1000 -g kates -m -s /bin/bash kates

# Copy connectivity test scripts
COPY scripts/ /app/scripts/
RUN chmod +x /app/scripts/*.sh

USER 1000
WORKDIR /app
CMD ["/app/scripts/connectivity-test.sh"]

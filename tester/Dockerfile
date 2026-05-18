FROM alpine:3.19

RUN apk add --no-cache \
    bash \
    curl \
    jq \
    kcat \
    netcat-openbsd

# Install kubectl
RUN curl -LO "https://dl.k8s.io/release/$(curl -L -s https://dl.k8s.io/release/stable.txt)/bin/linux/amd64/kubectl" && \
    chmod +x kubectl && \
    mv kubectl /usr/local/bin/

WORKDIR /app
CMD ["bash"]

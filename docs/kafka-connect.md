# Kafka Connect — Enterprise Image

Production-ready Kafka Connect image with pre-installed CDC, Schema Registry,
and JDBC connectors for enterprise data integration pipelines.

## Quick Start

```bash
# Pull from GHCR
docker pull ghcr.io/bmscomp/connect:3.0.2

# Or build locally
make connect-build
```

## Image Details

| Property | Value |
|----------|-------|
| **Base Image** | `quay.io/strimzi/kafka:1.0.0-kafka-4.2.0` |
| **Kafka Version** | 4.2.0 |
| **Strimzi Version** | 1.0.0 |
| **Architecture** | `linux/amd64`, `linux/arm64` |
| **License** | Apache 2.0 |

### Registries

| Registry | Image |
|----------|-------|
| GitHub Container Registry | `ghcr.io/bmscomp/connect:<version>` |
| Docker Hub | `bmscomp/connect:<version>` |

### Tag Convention

Tags follow the **Debezium version** bundled in the image:

```
ghcr.io/bmscomp/connect:3.0.2          # Debezium 3.0.2.Final
ghcr.io/bmscomp/connect:latest         # latest build from main
ghcr.io/bmscomp/connect:abc1234        # commit SHA
```

## Included Plugins

### Debezium CDC Connectors (v3.0.2.Final)

| Connector | Class | Use Case |
|-----------|-------|----------|
| PostgreSQL | `io.debezium.connector.postgresql.PostgresConnector` | Logical replication CDC from PostgreSQL |
| MySQL | `io.debezium.connector.mysql.MySqlConnector` | Binlog-based CDC from MySQL/MariaDB |
| MongoDB | `io.debezium.connector.mongodb.MongoDbConnector` | Change stream CDC from MongoDB |
| SQL Server | `io.debezium.connector.sqlserver.SqlServerConnector` | Change Tracking CDC from SQL Server |

### Apicurio Schema Registry (v2.5.11.Final)

| JAR | Purpose |
|-----|---------|
| `apicurio-registry-utils-converter` | Kafka Connect converter for Apicurio Registry |
| `apicurio-registry-serdes-avro-serde` | Avro serialization/deserialization |
| `apicurio-registry-serdes-jsonschema-serde` | JSON Schema serialization/deserialization |
| `apicurio-registry-serdes-protobuf-serde` | Protobuf serialization/deserialization |

### Aiven JDBC Connector (v6.12.0)

| Connector | Class | Use Case |
|-----------|-------|----------|
| JDBC Source | `io.aiven.connect.jdbc.JdbcSourceConnector` | Poll-based ingestion from SQL databases |
| JDBC Sink | `io.aiven.connect.jdbc.JdbcSinkConnector` | Write-back to SQL databases |

> **License**: Aiven JDBC connector is licensed under Apache 2.0, unlike the
> Confluent JDBC connector which uses the Confluent Community License.

### Built-in Connectors (from Kafka)

| Connector | Class | Use Case |
|-----------|-------|----------|
| MirrorSource | `o.a.k.connect.mirror.MirrorSourceConnector` | Cross-cluster replication |
| MirrorCheckpoint | `o.a.k.connect.mirror.MirrorCheckpointConnector` | Consumer offset sync |
| MirrorHeartbeat | `o.a.k.connect.mirror.MirrorHeartbeatConnector` | Replication health check |

## Usage with Strimzi

### Using the pre-built image (recommended)

```yaml
apiVersion: kafka.strimzi.io/v1
kind: KafkaConnect
metadata:
  name: my-connect
spec:
  version: 4.2.0
  replicas: 3
  image: ghcr.io/bmscomp/connect:3.0.2
  bootstrapServers: my-cluster-kafka-bootstrap:9093
  tls:
    trustedCertificates:
      - secretName: my-cluster-cluster-ca-cert
        pattern: "*.crt"
  config:
    group.id: my-connect-cluster
    offset.storage.topic: my-connect-offsets
    config.storage.topic: my-connect-configs
    status.storage.topic: my-connect-status
    config.storage.replication.factor: 3
    offset.storage.replication.factor: 3
    status.storage.replication.factor: 3
```

### Using with Helm chart

```bash
# In values.yaml
kafkaConnect:
  enabled: true
  image: "ghcr.io/bmscomp/connect:3.0.2"
```

## Building Locally

```bash
# Build the image
make connect-build

# Build with a specific Debezium version
docker build \
  --build-arg DEBEZIUM_VERSION=3.0.2.Final \
  -t connect:3.0.2 \
  -f Dockerfile.connect .

# Push to GHCR
make connect-push
```

## CI/CD Pipeline

The image is automatically built and published via GitHub Actions:

```
publish-connect.yml
├── meta           → extract version from Dockerfile ARG
├── build          → native amd64 + arm64 builds (no QEMU)
├── merge          → create multi-arch manifest
├── sign           → cosign keyless signing (tagged releases)
└── verify         → pull and validate plugins
```

### Triggering a build

```bash
# Tag-based release
git tag v1.0.0 && git push --tags

# Manual dispatch with custom Debezium version
gh workflow run publish-connect.yml \
  -f debezium_version=3.0.2.Final \
  -f platforms=linux/amd64,linux/arm64
```

## Security

- **Base image**: Strimzi Kafka (Red Hat UBI-based, minimal attack surface)
- **Non-root**: Runs as UID 1001
- **Signing**: Images are signed with [Cosign](https://github.com/sigstore/cosign) keyless signing
- **No Confluent dependencies**: All plugins use Apache 2.0 licensed components
- **Verification**: `cosign verify ghcr.io/bmscomp/connect:3.0.2`

## Upgrading Debezium

1. Update `DEBEZIUM_VERSION` in `Dockerfile.connect`
2. Push to main or tag a release
3. CI builds and publishes the new image
4. Update `kafkaConnect.image` in your Helm values

```bash
# Example: upgrade to Debezium 3.1.0
sed -i 's/DEBEZIUM_VERSION=.*/DEBEZIUM_VERSION=3.1.0.Final/' Dockerfile.connect
git commit -am "chore: bump Debezium to 3.1.0"
git tag v1.1.0 && git push --tags
```

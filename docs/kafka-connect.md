# Kafka Connect — Enterprise Image

> For complete Kafka Connect architecture, CDC pipelines, connector lifecycle, and operational procedures, see [Kafka Connect & CDC Pipelines](book/21-kafka-connect.md).

Production-ready Kafka Connect image with pre-installed CDC, Schema Registry, and JDBC connectors for enterprise data integration pipelines.

## Quick Start

```bash
# Pull from GHCR
docker pull ghcr.io/bmscomp/connect:3.6.0

# Or build locally
make connect-build
```

## Image Details

| Property | Value |
|----------|-------|
| **Base Image** | `quay.io/strimzi/kafka:1.1.0-kafka-4.3.0` |
| **Kafka Version** | 4.3.0 |
| **Strimzi Version** | 1.1.0 |
| **Architecture** | `linux/amd64`, `linux/arm64` |
| **License** | Apache 2.0 |

### Registries

| Registry | Image |
|----------|-------|
| GitHub Container Registry | `ghcr.io/bmscomp/connect:<version>` |
| Docker Hub | `bmscomp/connect:<version>` |

## Pre-installed Plugins

| Plugin | Version | Purpose |
|--------|---------|---------|
| **Debezium PostgreSQL** | 3.6.0.Final | CDC from PostgreSQL (logical replication) |
| **Debezium MySQL** | 3.6.0.Final | CDC from MySQL/MariaDB (binlog) |
| **Debezium MongoDB** | 3.6.0.Final | CDC from MongoDB (change streams) |
| **Debezium SQL Server** | 3.6.0.Final | CDC from SQL Server (CT tables) |
| **Debezium Oracle** | 3.6.0.Final | CDC from Oracle (LogMiner/XStream) |
| **Debezium Db2** | 3.6.0.Final | CDC from IBM Db2 (ASN capture) |
| **Debezium Scripting** | 3.6.0.Final | SMT for filtering and routing with Groovy 5 JSR-223 |
| **Apicurio Registry Converter** | 3.3.0 | Schema Registry integration (Avro, JSON Schema, Protobuf) |
| **Debezium JDBC Sink** | 3.6.0.Final | Upsert sink for SQL databases |
| **Aiven JDBC** | 6.10.0 | Generic JDBC source (table polling) and sink |

## Building the Image

```bash
# Build for local architecture
make connect-build

# Build multi-arch and push to registry
make connect-push

# Releases are published by CI on version tags (publish-connect.yml)
git tag v3.0.3 && git push origin v3.0.3
```

### Adding Plugins

Edit the `Dockerfile` in `connect/` to add additional plugins:

```dockerfile
# Add a new connector plugin
USER root:root
RUN mkdir -p /opt/kafka/plugins/my-connector && \
    curl -fsSL https://example.com/my-connector-1.0.jar \
    -o /opt/kafka/plugins/my-connector/my-connector-1.0.jar
USER 1001
```

## Versioning

The image version follows `<major>.<minor>.<patch>`:
- **Major**: Kafka or Strimzi base image upgrade
- **Minor**: New connector plugin added
- **Patch**: Plugin version bumps or bug fixes

## See Also

- [Kafka Connect & CDC Pipelines](book/21-kafka-connect.md) — Architecture, deployment, configuration, and operations
- [Tutorial 9: Kafka Connect Working Examples](tutorials/09-kafka-connect-working-examples.md) — Hands-on CDC + JDBC walkthrough
- [Tutorial 10: Source/Sink Quick Runbook](tutorials/kafka-connect-simple-source-sink-demo.md) — Minimal source-to-sink demo

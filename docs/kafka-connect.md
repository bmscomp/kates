# Kafka Connect — Enterprise Image

> For complete Kafka Connect architecture, CDC pipelines, connector lifecycle, and operational procedures, see [Chapter 21: Kafka Connect & CDC Pipelines](book/21-kafka-connect.md).

Production-ready Kafka Connect image with pre-installed CDC, Schema Registry, and JDBC connectors for enterprise data integration pipelines.

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

## Pre-installed Plugins

| Plugin | Version | Purpose |
|--------|---------|---------|
| **Debezium PostgreSQL** | 3.1.1.Final | CDC from PostgreSQL (logical replication) |
| **Debezium MySQL** | 3.1.1.Final | CDC from MySQL/MariaDB (binlog) |
| **Debezium MongoDB** | 3.1.1.Final | CDC from MongoDB (change streams) |
| **Debezium SQL Server** | 3.1.1.Final | CDC from SQL Server (CT tables) |
| **Apicurio Registry Converter** | 3.0.6 | Schema Registry integration (Avro, JSON Schema, Protobuf) |
| **Confluent JDBC Connector** | 10.8.0 | Source/Sink for any JDBC-compatible database |

## Building the Image

```bash
# Build for local architecture
make connect-build

# Build multi-arch and push to registry
make connect-push

# Tag and release
make connect-release VERSION=3.0.3
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

- [Chapter 21: Kafka Connect & CDC Pipelines](book/21-kafka-connect.md) — Architecture, deployment, configuration, and operations
- [Tutorial 9: Kafka Connect Working Examples](tutorials/09-kafka-connect-working-examples.md) — Hands-on CDC + JDBC walkthrough
- [Tutorial 10: Source/Sink Quick Runbook](tutorials/kafka-connect-simple-source-sink-demo.md) — Minimal source-to-sink demo

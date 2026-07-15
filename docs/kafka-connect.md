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
| **Debezium PostgreSQL** | 3.6.0.Final | CDC from PostgreSQL (logical replication) |
| **Debezium MySQL** | 3.6.0.Final | CDC from MySQL/MariaDB (binlog) |
| **Debezium MongoDB** | 3.6.0.Final | CDC from MongoDB (change streams) |
| **Debezium SQL Server** | 3.6.0.Final | CDC from SQL Server (CT tables) |
| **Debezium Oracle** | 3.6.0.Final | CDC from Oracle (LogMiner/XStream) |
| **Debezium Db2** | 3.6.0.Final | CDC from IBM Db2 (ASN capture) |
| **Debezium Scripting** | 3.6.0.Final | `Filter` and `ContentBasedRouter` SMTs with Groovy 5 JSR-223, embedded in every Debezium connector directory |
| **Apicurio Registry Converter** | 3.3.0 | Schema Registry integration (Avro, JSON Schema, Protobuf) |
| **Debezium JDBC Sink** | 3.6.0.Final | Upsert sink for SQL databases |

> **Why the scripting jars live inside each connector directory:** Kafka Connect
> loads every plugin-path entry in an isolated classloader. A standalone
> `debezium-scripting` directory cannot see `debezium-core`, so
> `io.debezium.transforms.Filter` and `io.debezium.transforms.ContentBasedRouter`
> fail plugin scanning and configs referencing them are rejected with
> "class not found". Per the Debezium docs, the scripting and Groovy jars are
> copied into each `debezium-*` plugin directory at image build time.

## Confluent Compatibility

Confluent Platform components are commercially licensed and are **not** bundled.
Use these open-source equivalents, all included in this image or in Kafka itself:

| Confluent component | Bundled alternative |
|---------------------|---------------------|
| `io.confluent.connect.transforms.Filter$Value` | `io.debezium.transforms.Filter` (Groovy condition on any field), or Kafka's `org.apache.kafka.connect.transforms.Filter` with `predicates` (topic name, header, tombstone) |
| Confluent JDBC source/sink (`kafka-connect-jdbc`) | Debezium CDC connectors (log-based source) and `io.debezium.connector.jdbc.JdbcSinkConnector` (sink) |
| Confluent Schema Registry converters | Apicurio Registry converters (also serve Confluent-compatible clients via the `ccompat` API) |

Example — drop delete events with the Debezium filter SMT:

```properties
transforms=filter
transforms.filter.type=io.debezium.transforms.Filter
transforms.filter.language=jsr223.groovy
transforms.filter.condition=value.op != 'd'
```

> **Debezium 3.x note:** `snapshot.mode: never` was removed. Use
> `snapshot.mode: no_data` (valid modes: `always`, `initial`, `initial_only`,
> `no_data`, `when_needed`, `configuration_based`, `custom`).

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

# Kafka Connect — Debezium CDC, Apicurio, Aiven JDBC & S3

**A batteries-included Kafka Connect image built on Strimzi, with the CDC and schema-registry
plugins already in place.**

[![License](https://img.shields.io/badge/license-Apache%202.0-blue?logo=apache)](https://github.com/bmscomp/kates/blob/main/LICENSE)
[![Source](https://img.shields.io/badge/source-github.com%2Fbmscomp%2Fkates-181717?logo=github)](https://github.com/bmscomp/kates)

Building a Connect image is the tedious part of every CDC project: download the connectors,
unpack each into its own plugin directory, match converter versions to your registry, get
the classpath right, repeat on every upgrade. This image is that work, done and versioned.

---

## What this image is

The [Strimzi Kafka](https://strimzi.io/) Connect image with ten plugin directories
pre-installed under `/opt/kafka/plugins`. Everything else — entrypoint, configuration
model, KRaft support, security — is inherited unchanged from the Strimzi base, so it drops
into an existing Strimzi `KafkaConnect` resource with a single `image:` line.

**Base:** `quay.io/strimzi/kafka:1.2.0-kafka-4.3.1`

## Pre-installed plugins

| Plugin directory | Artifact | Version |
|---|---|---|
| `debezium-postgres` | Debezium PostgreSQL source | 3.6.2.Final |
| `debezium-mysql` | Debezium MySQL source | 3.6.2.Final |
| `debezium-mongodb` | Debezium MongoDB source | 3.6.2.Final |
| `debezium-sqlserver` | Debezium SQL Server source | 3.6.2.Final |
| `debezium-jdbc` | Debezium JDBC **sink** | 3.6.2.Final |
| `aiven-jdbc` | Aiven JDBC source **and** sink | 6.10.0 |
| `aiven-s3-sink` | Aiven S3 **sink** — archive topics to Amazon S3 | 3.4.3 |
| `aiven-s3-source` | Aiven S3 **source** — replay S3 objects into Kafka | 3.4.3 |
| `apicurio-converter` | Apicurio Registry converters — Avro, JSON Schema, Protobuf | 3.3.0 |
| `debezium-scripting` | Debezium scripting + Groovy (`groovy`, `groovy-jsr223`, `groovy-json`) | 3.6.2.Final / Groovy 5.0.7 |

### Everything here is open source — and what that costs you

Every jar in this image is open source: 262 of them, under Apache-2.0, MIT, BSD, LGPL,
CDDL/EPL, or GPLv2 with Oracle's Universal FOSS Exception (MySQL Connector/J). Nothing
here requires a purchase, a subscription, or a click-through agreement. Three things are
therefore deliberately absent.

- **No Oracle Database support — neither CDC nor sink.** Oracle's JDBC driver is free of
  charge and free to redistribute under the Oracle Free Use Terms, but it ships as a
  binary with no source and no OSI licence. Both the Debezium JDBC sink and the Aiven
  JDBC connector bundle a copy in their release archives (`ojdbc11` and `ojdbc8`); this
  image deletes both, which is what lets the sentence above say *every* jar. The
  connectors' Oracle dialect classes remain, so nothing is patched or forked — an Oracle
  JDBC URL simply fails with `No suitable driver`. To restore it, layer the driver in:

  ```dockerfile
  FROM ghcr.io/bmscomp/connect:3.6.2
  ARG OJDBC=https://repo1.maven.org/maven2/com/oracle/database/jdbc/ojdbc11/23.26.1.0.0/ojdbc11-23.26.1.0.0.jar
  ADD --chown=1001:1001 ${OJDBC} /opt/kafka/plugins/aiven-jdbc/
  ```

  Debezium's Oracle *CDC* connector needs the same driver, so it is out for the same
  reason. IBM Db2 is out for a stronger one: `db2jcc` falls under IBM's International
  Program Licence Agreement, which does not permit this kind of redistribution at all.

- **No Confluent `kafka-connect-s3`.** The S3 connectors here are Aiven's, under
  Apache-2.0 — connector, AWS SDK for Java v2 and all. Confluent's ship under the
  Confluent Community Licence, which is source-available rather than open source: it
  does permit redistribution, but it forbids using the software to offer a competing
  hosted service. Pulling it into this image would hand that restriction to everyone
  downstream, so it stays out. (Confluent's Avro serializer and schema-registry client
  jars, which the Aiven S3 connectors depend on, are the repository's Apache-2.0 client
  libraries and are unaffected.)
- **No `ENV`, no `EXPOSE`, no `ENTRYPOINT` override.** Everything comes from the Strimzi
  base image, on purpose — this image adds plugins and nothing else, so Strimzi's own
  behaviour is never surprising.

---

## Tags

The tag encodes **both** the Debezium version and the Kafka base:

| Tag | Meaning |
|---|---|
| `3.6.2-kafka-4.3.1` | Fully qualified — Debezium 3.6.2 on Strimzi Kafka 4.3.1 |
| `3.6.2` | Debezium version alias |
| `latest` | Latest default-branch build |
| `<short-sha>` | Exact commit |

The compound tag exists because of a real failure mode: tagging by Debezium version alone
meant a Kafka base bump silently changed the contents of an already-published tag. Pin the
compound form in production.

Published for `linux/amd64` and `linux/arm64`; release tags are cosign-signed.

---

## Quick start

### With Strimzi (the intended path)

```yaml
apiVersion: kafka.strimzi.io/v1beta2
kind: KafkaConnect
metadata:
  name: connect
  annotations:
    strimzi.io/use-connector-resources: "true"
spec:
  version: 4.3.0
  image: ghcr.io/bmscomp/connect:3.6.2-kafka-4.3.1
  replicas: 3
  bootstrapServers: krafter-kafka-bootstrap.kafka.svc:9092
  config:
    group.id: kates-connect-cluster
    config.storage.replication.factor: 3
    offset.storage.replication.factor: 3
    status.storage.replication.factor: 3
```

### With the chart

The `connect-cluster` chart wires the image, KafkaUser provisioning with ACLs, TLS,
NetworkPolicies, the REST service, HPA, PDB and connector CRs:

```
helm install connect oci://ghcr.io/bmscomp/charts/connect-cluster \
  --version 1.3.1 \
  --namespace kafka \
  --set kafka.clusterName=krafter
```

### Verifying the plugins are loaded

```
kubectl exec -n kafka deploy/connect-connect -- \
  curl -s localhost:8083/connector-plugins | jq -r '.[].class'
```

The Connect REST API listens on **8083**.

---

## Configuration

This image adds no configuration of its own — it is configured exactly like any Strimzi
Connect deployment, through the `KafkaConnect` CR's `config` block or, if you run it
standalone, through Connect's usual environment variables.

The settings the chart applies by default, worth knowing because they are opinionated:

| Setting | Default | Why |
|---|---|---|
| `producer.acks` | `all` | CDC pipelines should not lose writes |
| `producer.enable.idempotence` | `true` | No duplicates on retry |
| `exactly.once.source.support` | `enabled` | EOS for source connectors |
| `consumer.auto.offset.reset` | `earliest` | Sinks replay rather than skip |
| `key`/`value.converter` | `JsonConverter` (schemas on) | Swap for the Apicurio converters when using a registry |

To use the schema registry converters instead:

```
value.converter=io.apicurio.registry.utils.converter.AvroConverter
value.converter.apicurio.registry.url=http://apicurio-registry.kafka.svc:8080/apis/registry/v3
```

Runs as **UID 1001**, inherited from the Strimzi base.

---

## Extending the image

Layer your own plugins — each in its own directory under `/opt/kafka/plugins`:

```dockerfile
FROM ghcr.io/bmscomp/connect:3.6.2-kafka-4.3.1
USER root:root
COPY --chown=1001:0 my-connector/ /opt/kafka/plugins/my-connector/
USER 1001
```

To change a bundled version instead, the Dockerfile takes build args —
`DEBEZIUM_VERSION`, `APICURIO_VERSION`, `AIVEN_JDBC_VERSION`, `GROOVY_VERSION`:

```
docker build -f Dockerfile.connect --build-arg DEBEZIUM_VERSION=3.6.2.Final -t connect:custom .
```

---

## Documentation

- **[Kafka Connect — Enterprise Image](https://github.com/bmscomp/kates/blob/main/docs/kafka-connect.md)** — this image in detail: plugins, building, versioning
- [Kafka Connect & CDC Pipelines](https://github.com/bmscomp/kates/blob/main/docs/book/21-kafka-connect.md) — the book chapter
- [Operating Kafka Connect](https://github.com/bmscomp/kates/blob/main/docs/book/operating-kafka-connect.md) — day-two operations
- [Working examples: CDC + JDBC](https://github.com/bmscomp/kates/blob/main/docs/tutorials/09-kafka-connect-working-examples.md) — end-to-end tutorial
- [Source/sink quick runbook](https://github.com/bmscomp/kates/blob/main/docs/tutorials/kafka-connect-simple-source-sink-demo.md)
- [`connect-cluster` chart reference](https://github.com/bmscomp/kates/blob/main/charts/connect-cluster/README.md)

Upstream: [Debezium](https://debezium.io/documentation/) ·
[Apicurio Registry](https://www.apicur.io/registry/docs/) ·
[Aiven JDBC connector](https://github.com/Aiven-Open/jdbc-connector-for-apache-kafka) ·
[Strimzi](https://strimzi.io/documentation/)

## Source and build

- Repository — <https://github.com/bmscomp/kates>
- Dockerfile — [`Dockerfile.connect`](https://github.com/bmscomp/kates/blob/main/Dockerfile.connect) (repo root)
- Build workflow — [`.github/workflows/publish-connect.yml`](https://github.com/bmscomp/kates/blob/main/.github/workflows/publish-connect.yml)
- Issues — <https://github.com/bmscomp/kates/issues>

## Related images

| Image | Purpose |
|---|---|
| [`bmscomp/kates`](https://hub.docker.com/r/bmscomp/kates) | The Kates backend — Kafka performance and chaos testing |
| [`bmscomp/kates-tester`](https://hub.docker.com/r/bmscomp/kates-tester) | kubectl + Kafka CLI toolbox used by the Helm chart test hooks |

## License

[Apache License 2.0](https://github.com/bmscomp/kates/blob/main/LICENSE) — Copyright 2026 KATES Contributors.
Bundled plugins carry their own licences; Debezium, Apicurio and the Aiven connector are
Apache-2.0.

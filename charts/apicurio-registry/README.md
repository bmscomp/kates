# apicurio-registry

Apicurio Registry 3.x (API & schema registry) with KafkaSQL storage, backed by
the Strimzi cluster deployed by this repository's `kafka-cluster` chart. The
chart deploys the registry backend only — there is **no bundled Kafka broker**.

## Contract with the kafka-cluster chart

| What | Provided by | Default |
|---|---|---|
| Bootstrap service | `kafka-cluster` (Strimzi) | `krafter-kafka-bootstrap.kafka.svc:9092` |
| Client credentials | `KafkaUser apicurio-registry` (SCRAM-SHA-512) → Secret with `sasl.jaas.config` | Secret `apicurio-registry` |
| Topic ACLs | `kafka-cluster` values (`users.items`): `kafkasql-`/`__apicurio` prefixes | enabled |
| Journal topics | this chart (`kafkaTopics.create`): `kafkasql-journal`, `kafkasql-snapshots` as `KafkaTopic` CRs | created in ns `kafka` |

The registry **refuses to start** if the journal/snapshots topics do not use
`cleanup.policy=delete` with infinite retention. The chart provisions them
correctly; `kafkaSql.topicVerificationOverride=true` exists only as an escape
hatch for externally managed topics.

## Registry 3.x specifics baked in

- Image comes from `quay.io/apicurio/apicurio-registry` (Docker Hub carries
  the legacy 2.x line). Pin with `image.digest` for reproducible deploys.
- Health (`/health/live`, `/health/ready`) and `/metrics` are served on the
  Quarkus **management port 9000**, not 8080. Probes, the Service, the
  NetworkPolicy, and monitoring all target it.
- Each replica independently replays the journal into an embedded H2 store:
  scale horizontally without consumer-group coordination; never set a fixed
  `APICURIO_KAFKASQL_CONSUMER_GROUP_ID`.

## Common overrides

```yaml
replicaCount: 3
pdb:
  enabled: true
kafkaTopics:
  replicas: 3
  minInsyncReplicas: 2
networkPolicy:
  enabled: true
global:
  kafka:
    bootstrapServers:
      - my-cluster-kafka-bootstrap.kafka.svc:9092
```

Production journal topics need `kafkaTopics.replicas: 3` and
`minInsyncReplicas: 2` (defaults are sized for single-broker kind clusters).

Values are validated by `values.schema.json` — unknown keys fail loudly at
install time rather than being silently ignored.

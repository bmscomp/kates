# apicurio-registry

Apicurio Registry 3.x (API & schema registry) with KafkaSQL storage, backed by
the Strimzi cluster deployed by this repository's `kafka-cluster` chart. The
chart deploys the registry backend only — there is **no bundled Kafka broker**.

## Contract with the kafka-cluster chart

| What | Provided by | Default |
|---|---|---|
| Bootstrap service | `kafka-cluster` (Strimzi) | `krafter-kafka-bootstrap.kafka.svc:9092` |
| Client credentials | `KafkaUser apicurio-registry` (SCRAM-SHA-512) → Secret with `sasl.jaas.config` | Secret `apicurio-registry` |
| Topic ACLs | `kafka-cluster`'s **platform profile** (`users.items` in `profiles/platform.yaml`): `kafkasql-`, `__apicurio` and `registry-` prefixes, group prefix `apicurio` | enabled |
| Journal topics | this chart (`kafkaTopics.create`): `kafkasql-journal`, `kafkasql-snapshots` as `KafkaTopic` CRs | created in ns `kafka` |

Since kafka-cluster 1.0 the `apicurio-registry` KafkaUser is part of the
`platform` profile rather than the base values, so the Kafka release must be
installed with `-f kafka-cluster/values-platform.yaml`, after
`helm pull oci://ghcr.io/bmscomp/charts/kafka-cluster --untar` (or
`profile: platform`) — a base install provisions no platform users at all.

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

## Pod security, images and labels

Every pod the chart renders (registry, UI, Helm test) passes Pod Security
Standards `restricted` with the defaults, with CPU and memory requests and a
memory limit. `image.registry` is the registry of every image, the Helm test's
`tests.image` (`curl/curl:8.7.1`) included, so a mirror needs
`apicurio/apicurio-registry`, `apicurio/apicurio-registry-ui` (with
`ui.enabled`) and `curl/curl`. `imagePullSecrets` reach every pod.
`podLabels` adds labels to every pod; the chart's own labels win on a clash.
The Helm test pod is tuned with `tests.podSecurityContext`,
`tests.securityContext` and `tests.resources`.

Values are validated by `values.schema.json` — unknown keys fail loudly at
install time rather than being silently ignored.

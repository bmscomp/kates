# legacy-kafka

A deliberately **old** Kafka, deployed as plain StatefulSets, whose only purpose
is to be the cluster a MirrorMaker 2 migration replicates *from*.

> **This is not a production Kafka and must never be used as one.** Single
> broker, RF1, `emptyDir` storage, no rack awareness, no PodDisruptionBudget, no
> TLS. It exists to be replicated from and then deleted. For a real cluster, use
> [`charts/kafka-cluster`](../kafka-cluster/).

## Why it exists

Strimzi 1.2.0 runs Kafka 4.x and validates `spec.version` against its supported
set. A Kafka 2.x or 3.x cluster therefore **cannot** be expressed as a `Kafka`
CR in this repo at all — which means the source side of a cross-version
migration test has nowhere to come from. This chart is that missing half.

## Modes

| `mode` | Kafka | Shape |
|:---|:---|:---|
| `zookeeper` | 2.x (and 3.x if you insist) | ZooKeeper StatefulSet + broker StatefulSet |
| `kraft` | 3.3+ | one combined broker+controller StatefulSet, no ZooKeeper |
| `""` (default) | any | **derived from `kafka.version`**: `zookeeper` below 3.3.0, `kraft` from 3.3.0 |

The image follows the same rule when `kafka.image` is empty: the official
multi-arch `apache/kafka:<version>` from 3.7.0 (KIP-975), and below that the
image `Dockerfile.legacy-kafka` builds, `<legacyImageRegistry>/kates-legacy-kafka:<version>`
(`ghcr.io/bmscomp` by default). So one flag deploys any line:

```bash
helm upgrade --install legacy charts/legacy-kafka -n kafka-legacy --create-namespace \
  --set kafka.version=3.5.2        # → KRaft, ghcr.io/bmscomp/kates-legacy-kafka:3.5.2 (build it first)
  --set kafka.version=2.8.2        # → ZooKeeper, the built image
  --set kafka.version=3.9.1        # → KRaft, apache/kafka:3.9.1
```

Explicit `mode` and `kafka.image` values still win, which is what the two era
overlays below do.

ZooKeeper runs from the **same image** as the broker — the Kafka tarball ships
ZooKeeper, so there is no second version pin that can drift away from the
pairing this chart exists to reproduce.

## Install

**Kafka 3.9.1 (KRaft)** — uses the official multi-arch Apache image, nothing to
build:

```bash
helm upgrade --install legacy charts/legacy-kafka \
  -n kafka-legacy-3x --create-namespace \
  -f charts/legacy-kafka/values-kafka-3x.yaml \
  -f charts/legacy-kafka/values-kind.yaml
```

**Kafka 2.8.2 (ZooKeeper)** — needs the image built first, because no official
Apache image exists below 3.7.0 and no third-party 2.x image ships arm64:

```bash
scripts/build-legacy-kafka-image.sh --version 2.8.2 --load    # build + load into kind

helm upgrade --install legacy charts/legacy-kafka \
  -n kafka-legacy-2x --create-namespace \
  -f charts/legacy-kafka/values-kafka-2x.yaml \
  -f charts/legacy-kafka/values-kind.yaml
```

Then:

```bash
helm test legacy -n kafka-legacy-2x
```

The test runs `kafka-broker-api-versions.sh` and `kafka-topics.sh` from the
cluster's **own** image, and asserts every pre-created topic is listed. That matters: a 4.x client cannot talk to a pre-2.1
broker at all ([KIP-896](https://cwiki.apache.org/confluence/x/K5sODg)), and
would report the refusal as a generic timeout — so the test would be measuring
the client rather than the broker.

## Wiring it into MirrorMaker 2

The chart prints the exact block to paste, and annotates the bootstrap Service
with the same information:

```bash
kubectl -n kafka-legacy-2x get svc legacy-legacy-kafka-bootstrap \
  -o jsonpath='{.metadata.annotations.kates\.io/bootstrap-address}'
```

```yaml
mirrors:
  - source:
      alias: legacy
      bootstrapServers: legacy-legacy-kafka-bootstrap.kafka-legacy-2x.svc.cluster.local:9092
      kafkaVersion: "2.8.2"          # checked against the KIP-896 floor
      authentication:
        type: ""                     # PLAINTEXT
```

`kafkaVersion` is not documentation. The `mirror-maker2` chart compares it
against the Kafka 2.1 floor that 4.x clients enforce and refuses to render below
it — turning a runtime `UNSUPPORTED_VERSION` (which surfaces an hour later as
"the connector is Running but nothing replicates") into a `helm install` that
fails in two seconds.

## Authentication

PLAINTEXT by default, on purpose: this chart varies **one** thing, the Kafka
version, and adding an auth mechanism would smuggle a second variable into a
test designed to isolate the first.

`-f values-sasl.yaml` adds a SASL/PLAIN listener on 9094. SASL/PLAIN rather than
SCRAM because it configures identically on 2.x (ZooKeeper) and 3.x (KRaft) —
SCRAM credentials would have to be written through `zookeeper-shell` on one and
the storage formatter on the other. Keep SASL usernames alphanumeric: they
become `user_<name>=` keys inside a tokenized JAAS config string.

## Talking to it by hand

```bash
kubectl -n kafka-legacy-2x run producer --rm -i --restart=Never \
  --image=ghcr.io/bmscomp/kates-legacy-kafka:2.8.2 --env=LOG_DIR=/tmp --command -- \
  /opt/kafka/bin/kafka-console-producer.sh \
    --bootstrap-server legacy-legacy-kafka-bootstrap:9092 --topic kates.orders
```

Two flags are not optional. `--command` makes the arguments replace the image's
ENTRYPOINT rather than being appended to it (the official `apache/kafka` image
has one; the built 2.x image does not, which is why the mistake is easy to make
on one and not the other). `LOG_DIR=/tmp` keeps the CLI's log4j output off the
install tree, which is read-only in these images.

## How the config is produced

`server.properties` is **generated at start-up**, not shipped, because
`advertised.listeners` has to name the pod that is actually running and a
StatefulSet ordinal is only knowable from inside the pod. The generated file
lands in `/tmp` (an `emptyDir`), which is what lets the container filesystem
stay read-only. The full effective config is echoed to the pod log at start-up,
minus anything matching `password`:

```bash
kubectl -n kafka-legacy-2x logs sts/legacy-legacy-kafka | sed -n '/effective server.properties/,/────/p'
```

## Storage

`persistence.enabled: false` (the default) is an `emptyDir` on both the broker
and — in ZooKeeper mode — the ensemble, so a restart starts clean. Setting it
to `true` gives **both** a PersistentVolumeClaim: ZooKeeper holds the topic
metadata, ISR state and broker registrations, and persisting the broker's
partition data without them leaves a broker holding logs for topics the
cluster no longer believes exist.

Decide before the first install. `volumeClaimTemplates` are immutable on a
StatefulSet, so flipping the flag on an existing release fails the upgrade —
uninstall and reinstall.

## Guardrails

The chart refuses to render, rather than failing later inside a broker, when:

- `mode` is neither `kraft`, `zookeeper` nor empty, or `kafka.version` does not look like `x.y[.z]` (the mode and the image derive from it)
- `kafka.replicationFactor` exceeds `kafka.replicas` — the internal topics would never be creatable
- `mode: kraft` is set explicitly with a Kafka version below 3.3.0
- every listener is disabled, or SASL is enabled with no users

## Values

See [`values.yaml`](values.yaml) — every key is commented — and
[`values.schema.json`](values.schema.json), which is strict (`additionalProperties: false`),
so a typo fails the install instead of being silently ignored.

| Overlay | For |
|:---|:---|
| `values-kafka-2x.yaml` | Kafka 2.8.2, ZooKeeper mode, the built image — a documented example of the 2.x shape (the same result as `--set kafka.version=2.8.2`, plus era config and topics) |
| `values-kafka-3x.yaml` | Kafka 3.9.1, KRaft mode, the official image — the 3.x shape, likewise |
| `values-kind.yaml` | Small heaps and requests for a laptop or a 2-core runner |
| `values-sasl.yaml` | Adds the SASL/PLAIN listener |

Combine era first, then environment: `-f values-kafka-2x.yaml -f values-kind.yaml`.

## Cleaning up

```bash
helm uninstall legacy -n kafka-legacy-2x
kubectl delete namespace kafka-legacy-2x
```

Nothing is annotated `helm.sh/resource-policy: keep` — this chart is meant to be
thrown away, and a migration test that starts from a previous run's leftovers is
a false pass waiting to happen.

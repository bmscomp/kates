# Installation

Every way to install this chart, what each one needs, and how to know it
worked. For what the values mean, see [Configuration](configuration.md).

## Prerequisites

| | Why |
|---|---|
| A Strimzi Cluster Operator watching the release namespace | The chart renders a `KafkaMirrorMaker2` custom resource; the operator is what turns it into pods. Nothing happens without one. It is its own release here — [`charts/strimzi-operator`](../../strimzi-operator/), which also owns the Strimzi CRDs |
| A **target** Kafka cluster, reachable from that namespace | MirrorMaker's Connect runtime runs against the target and stores its config, offset and status topics there |
| A **source** Kafka cluster of 2.1 or newer | Below that, a 4.x client cannot read it at all — the chart refuses at render time |
| Credentials for both ends | See [the credential contract](../README.md#the-credential-contract-read-this). This is the step that most often stalls a first install |
| Network reachability from the target's namespace to the source | NetworkPolicy is on by default in the production profile and denies everything not named |

Check the first and third before typing anything else:

```bash
kubectl get pods -A -l strimzi.io/kind=cluster-operator
kafka-broker-api-versions.sh --bootstrap-server "$SOURCE" | head -1
```

## Choosing an Install Path

The chart serves four topologies and they are not interchangeable. Pick the row
first — the chart README's
[topology table](../README.md#which-topology-is-this-decide-before-anything-else)
explains what each one costs if you pick wrong.

| You are… | Start from |
|---|---|
| Smoke-testing the chart on one cluster | `values-kind.yaml` (source == target, a loopback) |
| Migrating from Kafka 2.8 | `values-migrate-2x.yaml` → [the guide](migration-2.8-to-4.x.md) |
| Migrating from Kafka 3.x | `values-migrate-3x.yaml` → [the guide](migration-3.x-to-4.x.md) |
| Migrating from a 4.x cluster beside the target | `values-migrate-4x.yaml` |
| Running permanent replication for disaster recovery | `values-prod.yaml` |
| Consolidating several sources into one target | `values-fan-in.yaml` |

Two modifiers compose with any of them: `values-readonly-source.yaml` for a
source you may only read, and `values-scale.yaml` for a large estate.

## First Install

**From a checkout, build the chart's one dependency first.** `mirror-maker2` is
built on the [`kafka-common`](../../kafka-common/) library chart, and
`charts/*/charts/` is generated rather than committed — so on a fresh clone
every `helm install`, `upgrade`, `template` and `lint` on this page fails with
*"found in Chart.yaml, but missing in charts/ directory: kafka-common"* until
you run, once:

```bash
helm dependency build charts/mirror-maker2
```

A packaged chart already carries it; this is only for rendering from the
repository.

### The loopback, to prove the chart works

Source and target are the same cluster. Nothing external is required, so this
is the fastest way to confirm the operator, the credentials and the namespace
are right before a real source is involved:

```bash
helm install mm2 charts/mirror-maker2 \
  -n kafka \
  -f charts/mirror-maker2/values-kind.yaml
```

**A loopback must run under the `default` replication policy. Under `identity` it
replicates its own output back to itself, forever, and nothing in Kafka refuses
it.** The overlay sets this correctly; do not layer `identity` onto it.

### A real mirror

```bash
helm install mm2 charts/mirror-maker2 \
  -n kafka \
  -f charts/mirror-maker2/values-prod.yaml \
  --set 'mirrors[0].source.bootstrapServers=kafka-east.example.com:9094' \
  --set 'mirrors[0].source.kafkaVersion=3.9.1' \
  --set 'mirrors[0].source.alias=east'
```

Setting the source on the command line is fine for a first look. For anything
you will keep, put it in a values file you can review and version — the source
address, its version and its credentials are the parts of this deployment most
worth having in git.

### With the pre-flight probe

Off by default, on in the migration presets. It runs a real handshake against
the real source with the real client, before any workers are created, and
reports DNS resolution, TCP reachability and the broker's advertised protocol
versions:

```bash
helm install mm2 charts/mirror-maker2 -n kafka \
  -f charts/mirror-maker2/values-prod.yaml \
  --set preflight.enabled=true \
  --set preflight.failOnError=true
```

Turn it on for the first install against any source you have not mirrored
before. It converts a class of failure that otherwise appears as "the connector
is `RUNNING` and nothing arrives" into a Job that fails in twenty seconds with
the reason.

## Verifying the Install

Three checks, in increasing order of how much they prove. Run all three — the
first two can pass while nothing is being replicated.

```bash
# 1. The workers exist (weakest — says nothing about replication)
kubectl get pods -n kafka -l strimzi.io/cluster=mm2

# 2. The operator's own verdict on the connectors
kubectl get kafkamirrormaker2 mm2 -n kafka \
  -o jsonpath='{range .status.connectors[*]}{.name}{"\t"}{.connector.state}{"\n"}{end}'

# 3. Data is arriving (the only answer that cannot be faked)
kafka-get-offsets.sh --bootstrap-server "$TARGET" --topic <a mirrored topic>
```

**`Ready` on the `KafkaMirrorMaker2` resource does not mean it is
replicating.** It means the operator successfully created the workers. A mirror
with wrong ACLs, a pattern that matches nothing, or a source it cannot read
reports `Ready` indefinitely. Check 3 is the one that matters.

The Helm tests do check 3 for you, and move real records to prove it:

```bash
helm test mm2 -n kafka
```

## Upgrading

`helm upgrade` converges the custom resource; the operator rolls the workers.
Two settings are exceptions that must be decided before the first install
rather than changed later:

- **`offsetSyncs.location`** — flipping it on a running mirror restarts offset
  translation from an empty topic. Checkpoints regress until it catches up, and
  a failover in that window resumes consumers in the wrong place.
- **`replicationPolicy.mode`** — changing it renames every replicated topic. The
  old ones do not disappear; you get two copies under different names and
  consumers reading the wrong one.

Everything else — replicas, resources, patterns, task counts, monitoring — is a
normal upgrade.

## Uninstalling

The chart sets `keepOnDelete: true`, so `helm uninstall` leaves the
`KafkaMirrorMaker2` resource behind. That is deliberate: an accidental
`helm uninstall` should not tear down replication that something depends on.
Removing it is therefore two steps:

```bash
helm uninstall mm2 -n kafka
kubectl delete kafkamirrormaker2 mm2 -n kafka
```

Or, in this repository, as one command:

```bash
kates migrate mirror remove --release mm2 --namespace kafka --yes
```

Deleting the resource stops replication. It does not delete the replicated
topics on the target, the `mm2-offset-syncs` topic, or the consumer offsets the
checkpoint connector wrote — which is what you want after a cutover, and what
to remember if you are cleaning up a failed attempt.

## When the Install Fails

| `helm install` says | Meaning |
|---|---|
| `declares Kafka <v>, below the 2.1.0 floor` | The source is below the KIP-896 protocol floor. Two-hop migration; see [Migrating a Legacy Kafka Source](../../../docs/book/23-legacy-source-migration.md) |
| `source.kafkaVersion is not declared` | `compatibility.requireDeclaredVersion` is on and the source does not say what it is. Declare it — an undeclared source cannot be checked |
| `declares neither source.clusterName nor source.bootstrapServers` | The overlay expects you to name the source. Failing here is the point: the fallback would have mirrored the target to itself |
| `resolves to a floating tag` | An image is `:latest` or has no tag. Pin it — see [Configuration](configuration.md#images) |
| a replication factor exceeds `target.brokerCount` | The factors are set for a bigger cluster than you have. Fix them, or the connector would fail creating topics later |

Failures after a successful install — connectors stalled, no data, translation
not happening — are the [runbook](../../../docs/mirror-maker2-runbook.md)'s
territory.

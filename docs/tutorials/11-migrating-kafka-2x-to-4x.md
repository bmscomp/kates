# Tutorial 11: Migrating Kafka 2.x to 4.x with MirrorMaker 2

This tutorial migrates a real Apache Kafka **2.8.2** cluster onto Kafka **4.3.1**
using MirrorMaker 2 — topics, records, and consumer offsets — and then rehearses
the cutover. Every step runs locally in your kind cluster, and every claim it
makes is checked by `scripts/test-mm2-migration.sh`, which does the same thing
unattended.

The 2.x path is the interesting one. Somewhere between 2.0 and 2.1 there is a
cliff that a 4.x client cannot climb, and nothing about the failure looks like a
version problem.

**Level:** Advanced · **Duration:** 60 min · **Prerequisites:** [Tutorial 10](10-mirror-maker2-installation.md)

---

## The Constraint That Shapes Everything

[KIP-896](https://cwiki.apache.org/confluence/x/K5sODg) removed the pre-2.1
client protocol API versions in Kafka 4.0. MirrorMaker 2's consumer *is* a 4.x
client — the Connect workers run `spec.version: 4.3.1` — so:

| Source broker | A 4.x MirrorMaker 2 can read it? |
|---|---|
| 2.1 and newer | Yes |
| 2.0.x and older | **No.** `UNSUPPORTED_VERSION`, no workaround |

Below 2.1 you migrate in two hops: legacy → an intermediate Kafka 3.x cluster →
4.x. It is a protocol removal, not a configuration, and there is no flag.

This tutorial uses **2.8.2** — the last 2.x release, comfortably above the floor
and genuinely old: ZooKeeper, the 2.8 inter-broker protocol, the pre-KRaft world.

## What You Will Build

```text
  kafka-legacy-2x namespace              kafka namespace
  ┌───────────────────────────┐          ┌──────────────────────────────┐
  │  ZooKeeper 3.5            │          │  krafter (Kafka 4.3.1, KRaft)│
  │  Kafka 2.8.2 broker       │          │                              │
  │                           │  MM2     │  kates.orders   ◄────────────┼─ same name
  │  kates.orders  ───────────┼─────────►│  (identity policy)           │
  │  group: kates-migration   │          │  group offsets translated    │
  └───────────────────────────┘          └──────────────────────────────┘
```

Two namespaces, one kind cluster. A two-cluster topology is more faithful and is
discussed at the end; it is not what a laptop holds comfortably.

## The One-Command Way

Everything this tutorial builds by hand is one pair of versions to the CLI:

```bash
kates migrate run --from 2.8.2
```

`run` stands up the 2.8.2 source (building its image first, since no official
image exists below 3.7.0), installs MirrorMaker 2 towards the primary as it
runs, seeds the corpus, verifies records and offsets, rehearses the cutover,
tears everything down, and prints the report shown in Step 8. `kates migrate
plan --from 2.8.2` shows what would be created without creating it; `kates
migrate up --from 2.8.2` stops after the mirror is running so you can do the
verification and cutover steps yourself (`kates migrate status`, `verify`,
`cutover`, `down`). The steps below are what those commands do, one at a time,
with the chart-level commands they wrap — read them to understand the
migration, run the CLI to repeat it.

## Prerequisites

```bash
export LEGACY_NS=kafka-legacy-2x
export KAFKA_NS=kafka
export TOPIC=kates.orders
export GROUP=kates-migration-consumer
```

```bash
make cluster && make kafka-deploy
kubectl -n "${KAFKA_NS}" get kafka krafter
kubectl -n "${KAFKA_NS}" get secret kates-mm2
```

`make kafka-deploy` reconciles the Strimzi operator from
`charts/strimzi-operator` first and the `krafter` cluster second, layering
`values-platform.yaml` — which is where the `kates-mm2` user this tutorial
authenticates with comes from.

---

## Step 1: Build the 2.8.2 Image

There is no official Apache Kafka container image below 3.7.0 — the official
images start at KIP-975 — and no third-party 2.x image ships an arm64 variant,
which is fatal on Apple Silicon. So the source image is built from the Apache
tarball on a Temurin JRE. Kafka is pure Java, so that is architecture-neutral:

```bash
scripts/build-legacy-kafka-image.sh --version 2.8.2 --load
```

Output:

```text
Building ghcr.io/bmscomp/kates-legacy-kafka:2.8.2
  Kafka 2.8.2 (scala 2.13) on Temurin JRE 11
...
✅ image contains Kafka 2.8.2
✅ loaded into kind
```

The JRE choice is not arbitrary: Kafka 2.x supports Java 8 and 11 only. Java 17
support arrived in 3.0, and getting it wrong produces a start-up crash that
reads like a broker misconfiguration. The script picks the right one from the
version you asked for.

The tarball is checksum-verified inside the build. A build that silently accepts
a corrupted archive is worse than one that fails.

## Step 2: Deploy the 2.8.2 Source

```bash
helm upgrade --install legacy charts/legacy-kafka \
  -n "${LEGACY_NS}" --create-namespace \
  -f charts/legacy-kafka/values-kafka-2x.yaml \
  -f charts/legacy-kafka/values-kind.yaml \
  --set "topics[0].name=${TOPIC}" \
  --wait --timeout 10m
```

Two StatefulSets come up: ZooKeeper, then the broker. 2.x has no KRaft, so
ZooKeeper is not optional — and it runs from the *same image* as the broker,
because the Kafka tarball ships ZooKeeper and a separate image would be a second
version pin that can drift out of the pairing this is meant to reproduce.

Confirm the broker is genuinely serving, not merely listening:

```bash
helm test legacy -n "${LEGACY_NS}" --logs
```

Output (abridged):

```text
── API versions (proves the wire, not just the port) ──
legacy-legacy-kafka-0.legacy-legacy-kafka-headless.kafka-legacy-2x.svc.cluster.local:9092 (id: 0 rack: null) -> (
	Produce(0): 0 to 9 [usable: 9],
	Fetch(1): 0 to 12 [usable: 12],
	...
── topics ──
__consumer_offsets
kates.audit
kates.orders
kates.payments
✅ legacy Kafka 2.8.2 (zookeeper) is serving at legacy-legacy-kafka-bootstrap.kafka-legacy-2x.svc.cluster.local:9092
```

Those ranges are the KIP-896 floor made visible. A client negotiates down to
the highest version the broker lists for each API — but only as far as the
lowest version it still implements. A 4.x client dropped the versions that a
pre-2.1 broker tops out at, so against a 2.0 broker several of those ranges
would have no overlap at all. Against 2.8.2 they all do.

That test runs the **2.8.2** client, from the cluster's own image. A 4.x
`kafka-topics.sh` against an old broker would report its refusal as a generic
timeout, and the test would be measuring the client rather than the broker.

Look at the generated config while you are here — it is echoed at start-up:

```bash
kubectl -n "${LEGACY_NS}" logs sts/legacy-legacy-kafka \
  | sed -n '/effective server.properties/,/────/p'
```

You will see `inter.broker.protocol.version=2.8` and
`log.message.format.version=2.8`. This is a real 2.8 broker, not a 4.x one
wearing a label.

## Step 3: Seed Data and a Consumer Group

Produce a known corpus:

```bash
kubectl -n "${LEGACY_NS}" run seed --rm -i --restart=Never \
  --image=ghcr.io/bmscomp/kates-legacy-kafka:2.8.2 \
  --env=LOG_DIR=/tmp --command -- /bin/sh -c "
    i=1; while [ \$i -le 200 ]; do echo \"migrate-\$i\"; i=\$((i+1)); done |
    /opt/kafka/bin/kafka-console-producer.sh \
      --bootstrap-server legacy-legacy-kafka-bootstrap:9092 --topic ${TOPIC}"
```

Then commit a consumer-group offset — this is what offset translation will have
to move, and a migration tested without one is a migration that will surprise you
on cutover day:

```bash
kubectl -n "${LEGACY_NS}" run consumer --rm -i --restart=Never \
  --image=ghcr.io/bmscomp/kates-legacy-kafka:2.8.2 \
  --env=LOG_DIR=/tmp --command -- /bin/sh -c "
    /opt/kafka/bin/kafka-console-consumer.sh \
      --bootstrap-server legacy-legacy-kafka-bootstrap:9092 \
      --topic ${TOPIC} --group ${GROUP} --from-beginning \
      --timeout-ms 30000 --max-messages 100 >/dev/null 2>&1
    /opt/kafka/bin/kafka-consumer-groups.sh \
      --bootstrap-server legacy-legacy-kafka-bootstrap:9092 \
      --describe --group ${GROUP}"
```

The group has consumed 100 of the 200 records and committed a position on each
of the topic's three partitions — the `describe` output shows one row per
partition, with `CURRENT-OFFSET` well short of `LOG-END-OFFSET`. Those are the
numbers offset translation has to carry across.

## Step 4: Install MirrorMaker 2

Since 0.8.0 the `mirror-maker2` chart is built on the `kafka-common` library
chart, declared as a `file://` dependency, and `charts/*/charts/` is generated
rather than committed. Nothing renders until that dependency is resolved — not
`helm template`, not `helm lint`, not `helm upgrade`. The build is idempotent,
so running it once here covers the cutover upgrade in Step 7 as well:

```bash
helm dependency build charts/mirror-maker2

helm upgrade --install mm2 charts/mirror-maker2 \
  -n "${KAFKA_NS}" \
  -f charts/mirror-maker2/values-migrate-2x.yaml \
  --set "mirrors[0].source.bootstrapServers=legacy-legacy-kafka-bootstrap.${LEGACY_NS}.svc.cluster.local:9092" \
  --wait --timeout 10m
```

Before the CR is created, a preflight Job runs. Read its output — it is the most
informative thirty seconds of the whole migration:

```bash
kubectl -n "${KAFKA_NS}" logs job/mm2-mirror-maker2-preflight
```

```text
MirrorMaker 2 preflight — client quay.io/strimzi/kafka:1.2.0-kafka-4.3.1

══ source: legacy ── legacy-legacy-kafka-bootstrap.kafka-legacy-2x.svc.cluster.local:9092  [plaintext]
  ✅ HANDSHAKE broker answered ApiVersions to a 4.3.1 client
               legacy-legacy-kafka-0.legacy-legacy-kafka-headless...:9092 (id: 0 rack: null) -> (

✅ preflight: every source is reachable by a 4.3.1 client
```

That handshake is the KIP-896 check performed for real, by the exact client the
workers will use. It is the difference between knowing 2.8.2 works and assuming
it does. Had it failed, the verdict line would have said which way — `PROTOCOL`
(below the floor), `DNS`, `NETWORK`, `AUTH` or `TLS` — because each of those
fails the same handshake with a different exception, and each needs a different
fix. The Job is kept after a successful run so the log is always there to read.

### What the preset decided for you

`values-migrate-2x.yaml` is a set of migration decisions, not tuning:

| Setting | Why |
|---|---|
| `replicationPolicy.mode: identity` | Topic names are preserved. Consumers repoint and find `kates.orders`, not `legacy.kates.orders`. |
| `mirrors[0].source.kafkaVersion: "2.8.2"` | Checked against the 2.1 floor at render time, before anything deploys. |
| `compatibility.requireDeclaredVersion: true` | An undeclared source version is refused outright. |
| `preflight.enabled: true` | The handshake above. |
| `sync.group.offsets.enabled: "true"` | Consumers can resume on the target. Without it the migration is data-only. |
| `sync.topic.configs.enabled: "true"` | Retention and cleanup policy travel with the topic. |
| `sync.topic.acls.enabled: "false"` | The target's ACLs are managed by the `kafka-cluster` chart; a 2.x ACL model does not map cleanly onto a 4.x one. |
| `groupId: mirror-maker2-migrate-2x` | Keeps the `mirror-maker2` prefix the `kates-mm2` user is granted, so the workers can join their own Connect group. |
| RF1 and `brokerCount: 1` | The kind target. In a real migration, set both to the target's actual broker count. |

## Step 5: Watch It Replicate

```bash
kubectl -n "${KAFKA_NS}" wait kafkamirrormaker2/mm2-mirror-maker2 \
  --for=condition=Ready --timeout=300s

kubectl -n "${KAFKA_NS}" get kafkamirrormaker2 mm2-mirror-maker2 \
  -o jsonpath='{range .status.connectors[*]}{.name}{"\t"}{.connector.state}{"\n"}{end}'
```

Now the topic. Note what you are looking for:

```bash
make mm2-topics | grep "${TOPIC}"
```

`kates.orders` — **not** `legacy.kates.orders`. That is the identity policy
working, and it is also a check on it: if the policy were silently not in force,
the topic would be there under the prefixed name instead.

(`make mm2-topics` asks the brokers. `kubectl get kafkatopics` would show
nothing: the Strimzi Topic Operator is unidirectional, and topics MirrorMaker
creates directly in Kafka never become `KafkaTopic` resources.)

Read the records back off the 4.x cluster:

```bash
PW=$(kubectl -n "${KAFKA_NS}" get secret kates-mm2 -o jsonpath='{.data.password}' | base64 -d)

kubectl -n "${KAFKA_NS}" run verify --rm -i --restart=Never \
  --image=quay.io/strimzi/kafka:1.2.0-kafka-4.3.1 \
  --labels="kates.io/test-pod=true" --env=LOG_DIR=/tmp --command -- /bin/sh -c "
    echo security.protocol=SASL_PLAINTEXT > /tmp/c.properties
    echo sasl.mechanism=SCRAM-SHA-512 >> /tmp/c.properties
    echo 'sasl.jaas.config=org.apache.kafka.common.security.scram.ScramLoginModule required username=\"kates-mm2\" password=\"${PW}\";' >> /tmp/c.properties
    /opt/kafka/bin/kafka-console-consumer.sh \
      --bootstrap-server krafter-kafka-bootstrap:9092 \
      --consumer.config /tmp/c.properties \
      --topic ${TOPIC} --group kates-tutorial-verify \
      --from-beginning --timeout-ms 60000 --max-messages 200 \
      2>/dev/null | wc -l"
```

Two hundred records, written by a Kafka 2.8.2 producer, read by a Kafka 4.3.1
consumer, having crossed a protocol boundary that did not exist five years ago.

## Step 6: Verify Offset Translation

This is the half of a migration people discover they skipped on cutover morning:
all the data is on the target, and no consumer knows where to resume, so every
one of them replays from the beginning or skips to the end.

```bash
kubectl -n "${KAFKA_NS}" run offsets --rm -i --restart=Never \
  --image=quay.io/strimzi/kafka:1.2.0-kafka-4.3.1 \
  --labels="kates.io/test-pod=true" --env=LOG_DIR=/tmp --command -- /bin/sh -c "
    echo security.protocol=SASL_PLAINTEXT > /tmp/c.properties
    echo sasl.mechanism=SCRAM-SHA-512 >> /tmp/c.properties
    echo 'sasl.jaas.config=org.apache.kafka.common.security.scram.ScramLoginModule required username=\"kates-mm2\" password=\"${PW}\";' >> /tmp/c.properties
    /opt/kafka/bin/kafka-consumer-groups.sh \
      --bootstrap-server krafter-kafka-bootstrap:9092 \
      --command-config /tmp/c.properties --describe --group ${GROUP}"
```

The group exists on the target with a committed position near 100 — the offset
it held on the source, translated.

> **Important.** "Near" is the correct word. Translation is approximate by design: MirrorMaker
> maps positions through the `offset-syncs` topic at record granularity and lags
> the data. A consumer moved onto a translated offset may re-read a small number
> of records. MirrorMaker 2 is at-least-once — plan for duplicates rather than
> being surprised by them.

## Step 7: Rehearse the Cutover

Do not skip this on a real migration. It is the only step where the cost of
being wrong is data.

Check lag first — zero, not "low". With metrics on, that is
`kafka_connect_mirror_source_connector_replication_latency_ms_max` at its
floor. Without them, compare end offsets on both sides — the target's, twice,
must equal the source's and must not move between readings:

```bash
scripts/mm2-kafka-cli.sh offsets "${TOPIC}"
sleep 60
scripts/mm2-kafka-cli.sh offsets "${TOPIC}"
```

Then apply the cutover:

```bash
helm upgrade mm2 charts/mirror-maker2 -n "${KAFKA_NS}" \
  -f charts/mirror-maker2/values-migrate-2x.yaml \
  -f charts/mirror-maker2/values-cutover.yaml \
  --set "mirrors[0].source.bootstrapServers=legacy-legacy-kafka-bootstrap.${LEGACY_NS}.svc.cluster.local:9092"
```

```bash
kubectl -n "${KAFKA_NS}" get kafkamirrormaker2 mm2-mirror-maker2 \
  -o jsonpath='{range .status.connectors[*]}{.name}{"\t"}{.connector.state}{"\n"}{end}'
```

```text
legacy->target.MirrorSourceConnector       STOPPED
legacy->target.MirrorCheckpointConnector   RUNNING
```

Source stopped, checkpoint running. Prove the first half by producing 50 more
records to the source and confirming the target does not grow:

```bash
kubectl -n "${LEGACY_NS}" run after --rm -i --restart=Never \
  --image=ghcr.io/bmscomp/kates-legacy-kafka:2.8.2 --env=LOG_DIR=/tmp --command -- /bin/sh -c "
    i=1; while [ \$i -le 50 ]; do echo \"after-cutover-\$i\"; i=\$((i+1)); done |
    /opt/kafka/bin/kafka-console-producer.sh \
      --bootstrap-server legacy-legacy-kafka-bootstrap:9092 --topic ${TOPIC}"
```

Wait 30 seconds and re-run the verify from Step 5. Still 200. The mirror is
genuinely stopped, not paused.

> **Caution.** `stopped`, not `paused`, is deliberate. `stopped` releases the connector's tasks,
> so a worker restart cannot resume the mirror after producers have moved and start
> writing records *behind* the ones your new producers are appending. The
> checkpoint connector stays `RUNNING` because consumers migrate one at a time and
> every one that has not moved yet still needs its offset translated.

## Step 8: Do It All Unattended

Everything above is one command, with assertions:

```bash
kates migrate run --from 2.8.2
```

(`make mm2-migration-test-2x` calls the same thing.) Every phase fails
independently, and the run ends in a table — every assertion recorded, in the
order it is recorded:

```text
  RESULT ASSERTION                    DETAIL
  ------ ---------------------------- ------
  PASS   cluster reachable            kind-panda
  PASS   Strimzi CRDs                 kafkamirrormaker2s.kafka.strimzi.io present
  PASS   target Kafka Ready           krafter in kafka
  PASS   target credentials           secret kates-mm2
  PASS   legacy source deployed       Kafka 2.8.2 (zookeeper)
  PASS   legacy source serving        legacy-legacy-kafka-bootstrap.kafka-legacy-2x.svc.cluster.local:9092
  PASS   corpus produced              200 records → kates.orders
  PASS   source end offsets           200 across all partitions
  PASS   source consumer group        kates-migration-consumer committed on 3 partition(s)
  PASS   MirrorMaker 2 installed      preflight passed — the source answered a 4.x client
  PASS   CR Ready                     Connect workers are up
  PASS   connectors RUNNING           legacy->target.MirrorSourceConnector=RUNNING legacy->target.MirrorCheckpointConnector=RUNNING
  PASS   replicated topic exists      kates.orders on the target
  PASS   record count                 200 distinct of 200 received (200 lines — duplicates are at-least-once, not a fault)
  PASS   record content               every record 1..200 present on the target
  PASS   offset translation           kates-migration-consumer has a committed position on kates.orders
  PASS   cutover applied              source connector stopped, checkpoint connector still running
  PASS   cutover froze the target     end offsets unchanged at 200 after 50 more source records
```

Two of those rows are worth a second look. `record content` is not min-and-max:
it diffs the full expected set against the distinct records read back, so a
corpus missing everything between the first and last record fails. And
`cutover froze the target` has three outcomes, not two — a measurement that
returned nothing is reported as a failed *measurement*, not as a cutover that
did not take, because those need different fixes.

Add `--keep` to leave everything running for inspection; `-o json` gives the
same rows to a pipeline.

## Step 9: Clean Up

```bash
kates migrate down --name m282-431
```

`down` finds the lab by its `kates.io/lab` label and removes exactly what
`up` created — the mirror (and the CR Helm keeps), the source and its
namespace, the lab's topics on the target. By hand, the equivalent is:

```bash
helm uninstall mm2 -n "${KAFKA_NS}"
kubectl -n "${KAFKA_NS}" delete kafkamirrormaker2 mm2-mirror-maker2
helm uninstall legacy -n "${LEGACY_NS}"
kubectl delete namespace "${LEGACY_NS}"
```

---

## Doing This For Real

What changes when the source is a production 2.x cluster rather than a
StatefulSet in the next namespace:

**Two clusters, not two namespaces.** Point `mirrors[0].source.bootstrapServers`
at the real address and make sure it is routable from the target cluster's
network. Widen `networkPolicy.kafka.ports` if it is on a non-standard port.

**Credentials on both ends.** The source is a cluster this chart cannot touch.
Someone must grant the MM2 principal `Read` + `Describe` on the mirrored topics
*and* the consumer groups, and you must create the corresponding Secret in the
MM2 namespace. A missing source ACL does not error — the connector stalls,
`RUNNING`, replicating nothing.

**Real replication factors.** RF1 is not durable. Set
`sourceConnector.config.replication.factor`, the offset-syncs factor, the
checkpoints factor and `target.config.*.storage.replication.factor` to the
target's real durability, and set `target.brokerCount` so the chart checks them.

**Monitoring before you start, not after.** Turn on `metrics.enabled`,
`podMonitors.enabled` and `alerts.enabled` before the first record moves —
the boards arrive with `charts/monitoring`; these are what fill them.
Replication lag is the number the cutover decision rests on, and you want its
history, not a spot reading.

**Sizing.** `tasksMax` above the source's partition count buys nothing — tasks
map to partitions. Start at the partition count, watch heap, and scale workers
rather than tasks once the fetchers are saturated.

## Troubleshooting

| Symptom | Cause | Fix |
|---|---|---|
| `helm install` fails naming the KIP-896 floor | The source is below Kafka 2.1 | Two-hop migration via a 3.x cluster |
| Preflight: `❌ DNS ... does NOT resolve` | Namespace or cluster domain wrong in `bootstrapServers` | Fix the address |
| Preflight: `UNSUPPORTED_VERSION` | The broker really is too old, whatever it claims | Two-hop migration |
| Connectors `RUNNING`, no data | Source-side ACL, or `topicsPattern` matches nothing | Grant Read+Describe; check the regex |
| Topics arrive as `legacy.kates.orders` | Identity policy not in force | Check `replication.policy.class` on **both** connectors |
| Topic count grows without bound | The mirror is replicating its own internal topics | `replicationPolicy.excludeInternalTopics: true` |
| Group has no offsets on the target | `sync.group.offsets.enabled` missing | Set it on the checkpoint connector |
| Connector fails creating topics | RF exceeds the target's brokers | Set `target.brokerCount` and fix the factors |

The full table, with commands, is in the
[MirrorMaker 2 Runbook](../mirror-maker2-runbook.md).

## Next

- [Migrating Kafka 3.x to 4.x](12-migrating-kafka-3x-to-4x.md) — the same
  migration with the protocol risk removed, and what that changes.
- [MirrorMaker 2 Runbook](../mirror-maker2-runbook.md) — cutover checklist and
  rollback.

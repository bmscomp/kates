# Tutorial 12: Migrating Kafka 3.x to 4.x with MirrorMaker 2

This tutorial migrates Apache Kafka **3.9.1** onto Kafka **4.3.1** with
MirrorMaker 2. It is the same shape as the
[2.x migration](11-migrating-kafka-2x-to-4x.md) with one dimension removed —
and the interesting part is exactly *which* dimension, because it changes where
you should be paying attention.

**Level:** Intermediate · **Duration:** 40 min · **Prerequisites:** [Tutorial 10](10-mirror-maker2-installation.md)

---

## What Is Different, and Why It Matters

| | 2.8.2 source | 3.9.1 source |
|---|---|---|
| Protocol floor (KIP-896) | Above it, but the margin is the point | Far above it — a non-issue |
| Metadata store | ZooKeeper | KRaft, like the target |
| Container image | Built from the Apache tarball; nothing exists upstream | Official multi-arch `apache/kafka:3.9.1` |
| Consumer group protocol | Old | Modern — translation is better behaved |
| Where the risk actually is | Protocol compatibility | Throughput, cutover timing, and consumer coordination |

The 3.x path is the one most teams are on, because 3.9 is the last 3.x line and
the natural staging post before 4.x. It is also the one where the migration goes
smoothly enough that people skip the verification — which is why this tutorial
spends its time on the checks rather than the setup.

## What You Will Build

```text
  kafka-legacy-3x namespace              kafka namespace
  ┌───────────────────────────┐          ┌──────────────────────────────┐
  │  Kafka 3.9.1 (KRaft)      │  MM2     │  krafter (Kafka 4.3.1, KRaft)│
  │  kates.orders  ───────────┼─────────►│  kates.orders                │
  │  group: kates-migration   │          │  group offsets translated    │
  └───────────────────────────┘          └──────────────────────────────┘
```

No ZooKeeper anywhere. This is a pure data migration with no metadata-store
change underneath it.

## Prerequisites

```bash
export LEGACY_NS=kafka-legacy-3x
export KAFKA_NS=kafka
export TOPIC=kates.orders
export GROUP=kates-migration-consumer
```

```bash
make cluster && make kafka-deploy
```

`make kafka-deploy` reconciles the Strimzi operator from
`charts/strimzi-operator` first and the `krafter` cluster second, layering
`values-platform.yaml` — which is where the `kates-mm2` user Step 4
authenticates with comes from.

---

## Step 1: Deploy the 3.9.1 Source

No image to build — the official Apache image covers 3.7.0 and up, on both amd64
and arm64:

```bash
helm upgrade --install legacy charts/legacy-kafka \
  -n "${LEGACY_NS}" --create-namespace \
  -f charts/legacy-kafka/values-kafka-3x.yaml \
  -f charts/legacy-kafka/values-kind.yaml \
  --set "topics[0].name=${TOPIC}" \
  --wait --timeout 10m

helm test legacy -n "${LEGACY_NS}" --logs
```

One StatefulSet, combined broker+controller. The chart formats the KRaft storage
directory at start-up with `--ignore-formatted`, so a restart is idempotent —
including after a PVC survives one.

Look at what KRaft mode produced:

```bash
kubectl -n "${LEGACY_NS}" logs sts/legacy-legacy-kafka \
  | sed -n '/effective server.properties/,/────/p'
```

`process.roles=broker,controller`, a `controller.quorum.voters` list, and no
`zookeeper.connect` at all. The controller listener is never advertised — peers
reach it through the voter list, which is why the headless Service sets
`publishNotReadyAddresses: true`. Without that, a KRaft cluster deadlocks: pods
cannot elect a controller because they are unready, and cannot become ready
because there is no controller.

## Step 2: Seed Data and a Consumer Group

```bash
kubectl -n "${LEGACY_NS}" run seed --rm -i --restart=Never \
  --image=apache/kafka:3.9.1 --env=LOG_DIR=/tmp --command -- /bin/sh -c "
    i=1; while [ \$i -le 200 ]; do echo \"migrate-\$i\"; i=\$((i+1)); done |
    /opt/kafka/bin/kafka-console-producer.sh \
      --bootstrap-server legacy-legacy-kafka-bootstrap:9092 --topic ${TOPIC}"

kubectl -n "${LEGACY_NS}" run consumer --rm -i --restart=Never \
  --image=apache/kafka:3.9.1 --env=LOG_DIR=/tmp --command -- /bin/sh -c "
    /opt/kafka/bin/kafka-console-consumer.sh \
      --bootstrap-server legacy-legacy-kafka-bootstrap:9092 \
      --topic ${TOPIC} --group ${GROUP} --from-beginning \
      --timeout-ms 30000 --max-messages 100 >/dev/null 2>&1
    /opt/kafka/bin/kafka-consumer-groups.sh \
      --bootstrap-server legacy-legacy-kafka-bootstrap:9092 \
      --describe --group ${GROUP}"
```

## Step 3: Install MirrorMaker 2

The chart is built on the `kafka-common` library chart, declared as a `file://`
dependency, and `charts/*/charts/` is generated rather than committed — so
resolve it first or nothing renders. It is idempotent, and running it once here
covers the upgrades in Steps 5 and 6 too:

```bash
helm dependency build charts/mirror-maker2

helm upgrade --install mm2 charts/mirror-maker2 \
  -n "${KAFKA_NS}" \
  -f charts/mirror-maker2/values-migrate-3x.yaml \
  --set "mirrors[0].source.bootstrapServers=legacy-legacy-kafka-bootstrap.${LEGACY_NS}.svc.cluster.local:9092" \
  --wait --timeout 10m
```

The preflight Job still runs, and still declares the version:

```bash
kubectl -n "${KAFKA_NS}" logs job/mm2-mirror-maker2-preflight
```

> **Note.** The compatibility gate is a formality on this path — 3.9.1 is far above the 2.1
> floor. It is still enforced, and `requireDeclaredVersion: true` is still set in
> the preset, deliberately: a guard that only applies when it is convenient teaches
> people to ignore it, and the next source someone points this at may not be 3.9.

### What differs in `values-migrate-3x.yaml`

| Setting | 2.x preset | 3.x preset | Why |
|---|---|---|---|
| `sourceConnector.tasksMax` | 2 | 4 | A 3.9 source saturates more fetchers; the bottleneck moves to the target |
| `sync.group.offsets.interval.seconds` | 20 | 10 | The modern group protocol makes tighter translation intervals cheap |
| `emit.checkpoints.interval.seconds` | 20 | 10 | Same |
| `target.groupId` | `mirror-maker2-migrate-2x` | `mirror-maker2-migrate-3x` | Distinct Connect groups, both under the `mirror-maker2` prefix the `kates-mm2` user is granted |

Everything else — identity policy, offset sync, topic-config sync, ACL sync off
— is identical, because those are migration decisions rather than version ones.

## Step 4: Verify Replication

```bash
kubectl -n "${KAFKA_NS}" wait kafkamirrormaker2/mm2-mirror-maker2 \
  --for=condition=Ready --timeout=300s

kubectl -n "${KAFKA_NS}" get kafkamirrormaker2 mm2-mirror-maker2 \
  -o jsonpath='{range .status.connectors[*]}{.name}{"\t"}{.connector.state}{"\n"}{end}'

make mm2-topics | grep "${TOPIC}"
```

`kates.orders`, unprefixed — the identity policy. (`make mm2-topics` asks the
brokers; the Topic Operator is unidirectional, so `kubectl get kafkatopics`
never lists what MirrorMaker creates.)

Read it back:

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

## Step 5: Watch the Numbers That Matter Here

On the 2.x path the risk is "can it read the source at all". Here it is
throughput and timing, so turn on metrics and actually look:

```bash
helm upgrade mm2 charts/mirror-maker2 -n "${KAFKA_NS}" --reuse-values \
  --set metrics.enabled=true \
  --set podMonitors.enabled=true \
  --set alerts.enabled=true
```

The boards themselves arrive with `charts/monitoring` (`dashboards.enabled`,
on by default) — these switches are what give them data.

Three metrics answer three different questions:

| Metric | Question | What to do about it |
|---|---|---|
| `kafka_connect_mirror_source_connector_replication_latency_ms_max` | Is it safe to cut over? | Must be at its floor before you stop producers |
| `kafka_connect_mirror_source_connector_record_age_ms_max` | How far behind are the fetchers? | Rising: raise `tasksMax` toward the partition count |
| `kafka_connect_source_task_metrics_source_record_write_total` | Is anything moving at all? | Flat with connectors `RUNNING` means an ACL or pattern problem |

The Grafana dashboard the chart renders is built around exactly these three,
with the lag panel first because it is the one a cutover decision rests on.

## Step 6: Cut Over

Same sequence as the 2.x path, and the ordering is the part that matters:

```bash
# 1. Stop producers on the source. Confirm end offsets are static — twice,
#    60 seconds apart. A forgotten producer is the usual cause of records
#    stranded on the old cluster.

# 2. Let the mirror drain: one refresh interval plus the observed lag.

# 3. Apply the cutover.
helm upgrade mm2 charts/mirror-maker2 -n "${KAFKA_NS}" \
  -f charts/mirror-maker2/values-migrate-3x.yaml \
  -f charts/mirror-maker2/values-cutover.yaml \
  --set "mirrors[0].source.bootstrapServers=legacy-legacy-kafka-bootstrap.${LEGACY_NS}.svc.cluster.local:9092"

# 4. Move consumers, one group at a time, onto the translated offsets.
# 5. Move producers.
# 6. Retire the mirror.
```

```bash
kubectl -n "${KAFKA_NS}" get kafkamirrormaker2 mm2-mirror-maker2 \
  -o jsonpath='{range .status.connectors[*]}{.name}{"\t"}{.connector.state}{"\n"}{end}'
```

```text
legacy->target.MirrorSourceConnector       STOPPED
legacy->target.MirrorCheckpointConnector   RUNNING
```

> **Caution.** Do not move consumers before step 3. A consumer reading the target while the
> mirror is still writing to it will process records, commit an offset, and then
> have that offset overwritten by a translated one from the checkpoint connector.
> The cutover order exists to make those two writers never overlap.

The full checklist, with the verification between each step, is in the
[MirrorMaker 2 Runbook](../mirror-maker2-runbook.md).

## Step 7: Do It Unattended

```bash
kates migrate run --from 3.9.1
```

And, because the 3.x path is cheap enough to test both policies, the renaming
one as well — with its own lab name so the two can coexist:

```bash
kates migrate run --from 3.9.1 --policy default --name m391-431-default
```

That second run expects `legacy.kates.orders` on the target rather than
`kates.orders` — which is the right shape for disaster recovery, where you need
to know which cluster a record came from, and the wrong one for a migration.
Running both is how you find out that the policy setting is actually taking
effect rather than being ignored.

## Step 8: Clean Up

```bash
kates migrate down --name m391-431
```

or, by hand:

```bash
helm uninstall mm2 -n "${KAFKA_NS}"
kubectl -n "${KAFKA_NS}" delete kafkamirrormaker2 mm2-mirror-maker2
helm uninstall legacy -n "${LEGACY_NS}"
kubectl delete namespace "${LEGACY_NS}"
```

---

## Doing This For Real

Everything in the [2.x tutorial's](11-migrating-kafka-2x-to-4x.md#doing-this-for-real)
"for real" section applies unchanged: real bootstrap addresses, credentials on
both ends, real replication factors, monitoring before the first record moves.

Three things specific to a 3.x source:

**3.9 is a staging post, not just a source.** If you are on 2.x, migrating to
3.9 first and then to 4.x is the supported two-hop path — and this tutorial is
the second hop. The first hop has no protocol floor problem, because a 3.9 client
can still read a 2.0 broker.

**KRaft on both ends means no ZooKeeper decommission.** The migration is data
only. That removes a whole class of failure from the change window, and it is why
this path is usually scheduled as a single evening rather than a project.

**Watch the target, not the source.** A 3.9 source will feed MirrorMaker 2
faster than a 2.8 one, so the bottleneck moves to the target's produce path.
`tasksMax: 4` in the preset is a starting point; raise it toward the source's
partition count, then stop — a task maps to partitions, so more tasks than
partitions buys nothing at all.

## Next

- [MirrorMaker 2 Runbook](../mirror-maker2-runbook.md) — cutover, rollback, and
  the failure table.
- [Migrating Kafka 2.x to 4.x](11-migrating-kafka-2x-to-4x.md) — if you also
  have an older cluster, and to see the protocol floor up close.

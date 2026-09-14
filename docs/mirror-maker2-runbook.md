# MirrorMaker 2 Runbook — Cutover, Rollback, Troubleshooting

Operational reference for a live MirrorMaker 2 mirror: how to finish a
migration, how to back out of one, and what each failure actually means. For the
step-by-step walkthroughs see the
[2.x → 4.x](tutorials/11-migrating-kafka-2x-to-4x.md) and
[3.x → 4.x](tutorials/12-migrating-kafka-3x-to-4x.md) tutorials; for the chart
itself see [charts/mirror-maker2](../charts/mirror-maker2/README.md).

---

## The one thing to internalise

**`Ready` on a `KafkaMirrorMaker2` does not mean it is replicating.** It means
the Connect workers are up. A mirror whose source ACL is missing, whose source
is unreachable, or whose `topicsPattern` matches nothing is `Ready` — forever,
silently. Every check in this runbook exists because that condition is not the
one you care about.

The three questions, in order of how much they tell you:

```bash
NS=kafka; MM2=mm2-mirror-maker2

# 1. Are the workers up?  (weakest signal)
kubectl -n $NS get kafkamirrormaker2 $MM2

# 2. Are the connectors running?  (the operator writes the real answer here)
kubectl -n $NS get kafkamirrormaker2 $MM2 \
  -o jsonpath='{range .status.connectors[*]}{.name}{"\t"}{.connector.state}{"\n"}{end}'

# 3. Is data arriving?  (the only answer that cannot be faked)
kates migrate target topics                        # what the mirror actually created on the target
kates migrate target offsets <replicated-topic>    # its end offsets, summed

# All three at once, for a lab the CLI created:
kates migrate status --name m282-430 --offsets
```

Run 3 twice, a minute apart. If the number does not move, the mirror is not
working, whatever 1 and 2 say.

Two things about that third command. The Topic Operator is unidirectional, so
topics MirrorMaker creates never appear as `KafkaTopic` resources —
`kubectl get kafkatopics` cannot answer this question, only the brokers can.
And on a 4.x cluster the offset tool is `kafka-get-offsets.sh`; the old
`kafka.tools.GetOffsetShell` class was moved to `org.apache.kafka.tools` in
Kafka **3.7.0** ([KAFKA-14581](https://issues.apache.org/jira/browse/KAFKA-14581))
and no longer loads from a 3.7-or-newer distribution, which with stderr
discarded reads as "no offsets" rather than "no such tool".
`kates migrate target offsets` picks the tool from the broker version (and
knows the 2.x tool takes `--broker-list`, not `--bootstrap-server`).

---

## Cutover checklist

A cutover is a sequence, not a switch. Do these in order and do not skip the
verification between them.

### Before you start

- [ ] Replication lag is **zero**, not "low". `MirrorMaker2ReplicationLagHigh`
      is clear and `kafka_connect_mirror_source_connector_replication_latency_ms_max`
      is at its floor.
- [ ] Every topic you care about exists on the target with the record count you
      expect. Compare end offsets on both sides — not topic lists.
- [ ] Consumer groups appear on the target with translated offsets:
      `kafka-consumer-groups.sh --describe --group <g>` against the target.
- [ ] You know which consumers move when, and who owns each one.
- [ ] You have read the rollback section below **before** you need it.

### 1. Stop the producers

Stop writing to the source. Then confirm it, do not assume it:

```bash
# Twice, 60 seconds apart. The numbers must be identical.
# kafka-get-offsets.sh exists from Kafka 3.0 (KIP-635). Against an older
# source, run the same tool without its launcher — see "The source's era".
kafka-get-offsets.sh --bootstrap-server <source> --topic <topic>
```

A producer you forgot about is the single most common cause of records stranded
on the old cluster after a cutover.

### 2. Let the mirror drain

Wait at least one `refresh.topics.interval.seconds` **plus** the observed lag,
then verify the target's end offsets equal the source's.

### 3. Apply the cutover

```bash
helm upgrade mm2 charts/mirror-maker2 -n kafka \
  -f charts/mirror-maker2/values-migrate-2x.yaml \
  -f charts/mirror-maker2/values-cutover.yaml
```

This stops the **source** connector and keeps the **checkpoint** connector
running. Confirm:

```bash
kubectl -n kafka get kafkamirrormaker2 mm2-mirror-maker2 \
  -o jsonpath='{range .status.connectors[*]}{.name}{"\t"}{.connector.state}{"\n"}{end}'
# ...MirrorSourceConnector      STOPPED
# ...MirrorCheckpointConnector  RUNNING
```

> **Why the checkpoint connector stays running.** Consumers migrate one at a
> time, and every one that has not moved yet still needs its committed position
> translated onto the target. Stopping both connectors together is the classic
> cutover mistake: all the data is there, and no consumer knows where to start
> reading it.

### 4. Move the consumers

One group at a time. Each consumer repoints its bootstrap at the target and
starts from the translated offset — do **not** set `auto.offset.reset` to
`earliest` as a shortcut, or the group replays everything.

Verify per group, on the target:

```bash
kafka-consumer-groups.sh --bootstrap-server <target> --describe --group <g>
```

### 5. Move the producers

Repoint producers at the target. Now the source is idle and the target is live.

### 6. Retire the mirror

Only after every consumer has moved and been observed making progress:

```bash
helm uninstall mm2 -n kafka
# keepOnDelete leaves the CR behind on purpose — delete it explicitly.
kubectl -n kafka delete kafkamirrormaker2 mm2-mirror-maker2
```

Keep the source cluster running, read-only, for at least one full retention
period. It is the only rollback you have that does not involve a data merge.

---

## Rollback

**Before producers have moved** — trivial. Remove the cutover overlay:

```bash
helm upgrade mm2 charts/mirror-maker2 -n kafka \
  -f charts/mirror-maker2/values-migrate-2x.yaml
```

`stopped` → `running` resumes from the connector's committed offsets, so nothing
is re-read from the beginning.

**After producers have moved** — this is not a rollback, it is a data merge.
Records written to the target are not replicated backwards, so pointing
producers back at the source leaves two divergent clusters. Your options:

1. **Roll forward.** Usually right: fix the problem on the target.
2. **Reverse mirror.** Install a second MM2 with source and target swapped, let
   it drain, then move producers back. Under an identity policy this needs
   `topicsExcludePattern` care on both mirrors or the two flows feed each other.
3. **Accept the split** and reconcile offline.

Decide which of these you are willing to do *before* step 5 of the cutover.

---

## Failing over and failing back

A **cutover** is planned: the source is healthy, you stop producing, you wait
for lag to reach zero, you switch. A **failover** is not. The source is gone,
whatever had not replicated is not coming, and the only question left is where
consumers resume on the target.

### Failing over

```bash
helm upgrade mm2 charts/mirror-maker2 -n kafka \
  -f charts/mirror-maker2/values-prod.yaml \
  -f charts/mirror-maker2/values-failover.yaml
```

That overlay sets three things, and each of them is a decision:

| | |
|---|---|
| source connector → `stopped` | there is nothing to read. `stopped` rather than `paused` releases the tasks, so a worker restart cannot reconnect to a source that comes back up with a partial log and resume mirroring into a target that has since moved on. |
| checkpoint connector → `running` | consumers that have not moved still need translated positions, and the checkpoints topic is on the target, so it survives the source's loss. Stopping both is the same mistake as in a cutover: all the data, and nobody knows where to start. |
| `autoRestart` → off | restarting into an unreachable cluster produces log noise and alert noise and nothing else. |

Confirm the posture before telling anyone the failover is done:

```bash
kubectl -n kafka get kafkamirrormaker2 mm2-mirror-maker2 \
  -o jsonpath='{range .status.connectors[*]}{.name}{"\t"}{.connector.state}{"\n"}{end}'
# ...MirrorSourceConnector      STOPPED
# ...MirrorCheckpointConnector  RUNNING
```

Then move consumers. Two ways, in order of preference:

1. **The application reads the checkpoints itself**, through
   `RemoteClusterUtils` or the translated offsets in
   `<alias>.checkpoints.internal`. This is the supported path and needs nothing
   from the chart.
2. **Seed the groups on the target** from an offsets ConfigMap, through the
   connector's `alterOffsets`. It applies only while the connector is
   `STOPPED` — which it now is. Produce the ConfigMap with `listOffsets`
   against the checkpoint connector first, then name it on the mirror. The
   overlay leaves that block commented rather than shipping it: a wrong offsets
   ConfigMap silently rewinds or skips every consumer in the group.

Before either, check
[Offset syncs are stale while records flow](#offset-syncs-are-stale-while-records-flow).
A failover onto frozen checkpoints replays everything since they froze, and
nothing warns you at the time.

### Failing back

**Failback is a separate release in the opposite direction.** It is never a
matter of re-enabling the forward one, and the order below is the whole
procedure.

1. **Stop producing to the failover cluster.** Confirm it with end offsets,
   twice, sixty seconds apart. Same discipline as a cutover.
2. **Remove the forward mirror.** Uninstall it, or stop its source connector
   and delete the release. Then confirm — this is the step people skip:

   ```bash
   kubectl -n kafka get kafkamirrormaker2 -o jsonpath='{range .items[*]}{.metadata.name}{"\n"}{range .spec.mirrors[*]}{"  from "}{.source.alias}{"\t"}{.source.bootstrapServers}{"\n"}{end}{end}'
   ```

   No remaining CR may list the *original* cluster's bootstrap as a mirror
   source — that, and not the release name, is what the chart's own guard reads.
3. **Install the reverse mirror** and let it drain. The failover cluster holds
   records the original never saw.

   ```bash
   helm upgrade --install mm2-back charts/mirror-maker2 -n kafka \
     -f charts/mirror-maker2/values-prod.yaml \
     -f charts/mirror-maker2/values-failback.yaml \
     --set target.clusterName=<the original cluster> \
     --set 'mirrors[0].source.clusterName=<the failover cluster>'
   ```

   Both `--set`s are **mandatory**: the overlay ships its source cluster
   empty, and an empty `clusterName` falls back to the chart's default, which
   is also the default target — so a failback installed without them mirrors a
   cluster to itself. Check the rendered bootstraps before you install:

   ```bash
   helm template mm2-back charts/mirror-maker2 -n kafka \
     -f charts/mirror-maker2/values-prod.yaml \
     -f charts/mirror-maker2/values-failback.yaml \
     --set target.clusterName=<the original cluster> \
     --set 'mirrors[0].source.clusterName=<the failover cluster>' \
     | grep bootstrapServers
   # the two must be DIFFERENT clusters
   ```

   It renders **stopped**, on purpose: a failback that starts replicating the
   moment `helm install` returns is one nobody has checked the direction of.
   Start it deliberately with `--set cutover.enabled=false`.
4. **Move consumers back**, using the translated offsets as in a cutover.
5. **Cut over** (`values-cutover.yaml`) and remove the failback release.

### The loop the chart refuses

Under an `IdentityReplicationPolicy` the two directions re-read each other's
output under the same topic names, without end. The chart refuses to render the
reverse mirror while it can see the forward `KafkaMirrorMaker2` in the
namespace:

> `mirror-maker2: kafka/mm2-mirror-maker2 already mirrors FROM the alias
> "target", which is this release's target, and the replication policy is
> identity — the two would re-read each other's output under the same topic
> names, forever.`

Two things to know about that guard. It is a `lookup`, so it only fires against
a live cluster — `helm template`, `--dry-run` and CI find no CRs and pass, which
means **it is not a substitute for step 2**. And
`replicationPolicy.allowBidirectional: true` overrides it, for patterns you have
proved disjoint.

Under the **default** policy the two directions do not loop, because each
prefixes what it writes. They do produce `east.east.orders`, which is its own
kind of incident. Active-active is two releases, one per direction, default
policy only.

---

## Mirroring a source you may only read

MirrorMaker records the source→target offset mapping in
`mm2-offset-syncs.<target-alias>.internal`, and **by default it writes that
topic to the source cluster.** So mirroring is not a read-only operation on the
source unless you make it one — and the failure when it is not is late,
internal, and invisible from the CR:

```text
org.apache.kafka.common.errors.TopicAuthorizationException:
  Authorization failed for topics [mm2-offset-syncs.target.internal]
```

The CR stays `Ready`. `.status.connectors` stays `RUNNING` until the task dies.
Nothing replicates.

### Which ACLs, for which setting

| `offsetSyncs.location` | The SOURCE principal needs |
|---|---|
| `source` (Kafka's default) | Read + Describe on the mirrored topics, Describe on the mirrored groups, Describe on the cluster — **and Create + Write + Describe on `mm2-offset-syncs.*`**. A topic-level `Create` on that prefix is enough; a cluster-wide `Create` is not required. |
| `target` ([KIP-716](https://cwiki.apache.org/confluence/display/KAFKA/KIP-716:+Allow+configuring+the+location+of+the+offset-syncs+topic+with+MirrorMaker2), Kafka 3.0+) | Read + Describe on the mirrored topics, Describe on the mirrored groups, Describe on the cluster. Nothing else. Nothing is written to the source. |

Add `DescribeConfigs` on the mirrored topics when `sync.topic.configs.enabled`
is on — both migration presets turn it on.

The `mm2-offset-syncs.` grant on the **target** user follows the same setting:
the chart emits it only when some mirror keeps that topic there. With the
default location it would name a topic that is not on that cluster.

### Turning it on

```yaml
mirrors:
  - source: { … }
    readOnlySource: true      # shorthand for offsetSyncs.location: target
```

or chart-wide with `offsetSyncs.location: target`, which is what
`charts/mirror-maker2/values-readonly-source.yaml` does:

```bash
helm upgrade --install mm2 charts/mirror-maker2 -n kafka \
  -f charts/mirror-maker2/values-kind.yaml \
  -f charts/mirror-maker2/values-readonly-source.yaml
```

The chart writes the setting onto **both** connectors, because Kafka reads it on
both and a mismatch means the checkpoint connector reads a topic the source
connector never writes — translation then produces nothing, silently. Hand-set
one of them to disagree and the render is refused.

### Why flipping it on a running mirror restarts translation

`offset-syncs.topic.location` decides *where MirrorMaker's sync state lives*,
not how it is written. Changing it points both connectors at a **different,
empty topic**. The old mappings are not migrated; they are simply no longer
read. So:

- checkpoints regress to whatever the new topic can support, which at first is
  nothing;
- `MirrorMaker2OffsetSyncStale` is the signature while it catches up;
- a failover during that window resumes consumers at a position that is stale
  by however long the flip has been in effect.

Decide it **before the first install**. If you must change it on a live mirror,
treat the window as one in which failover is not available, and confirm the new
topic is growing before you say otherwise:

```bash
kafka-get-offsets.sh --bootstrap-server <new location> \
  --topic 'mm2-offset-syncs.<target-alias>.internal'
```

### Verifying it before you need it

```bash
# 1. Both connectors agree, and on what:
kubectl -n kafka get kafkamirrormaker2 mm2-mirror-maker2 \
  -o jsonpath='{.spec.mirrors[*]}' | tr ',' '\n' | grep offset-syncs

# 2. Nothing MirrorMaker owns exists on the source:
kafka-topics.sh --bootstrap-server <source> --list | grep -E '^mm2-|checkpoints'
#    (empty is the answer you want)

# 3. And the topic IS on the target:
kafka-topics.sh --bootstrap-server <target> --list | grep '^mm2-offset-syncs'
```

One more thing a read-only source changes: the `test-replication` Helm test
creates its topic on the source, and that create is denied. That is not a fault
— the topic has to pre-exist. Point the test at one that does with
`tests.replication.topic`, or `topicByAlias` on a fan-in release.

---

## The source's era

Every command in this runbook is written for a current Kafka distribution. Run
them against a 2.x or 3.x source and some of them do not exist, some mean
something different, and one of them refuses outright. The background is in the
book's [Migrating a Legacy Kafka Source](book/23-legacy-source-migration.md)
chapter; what follows is the substitutions.

**Know which era you are in before anything else.** Ask the wire, not the
ticket:

```bash
kafka-broker-api-versions.sh --bootstrap-server "$SRC" | head -1
```

### Reading offsets

`kafka-get-offsets.sh` was introduced in Kafka 3.0 (KIP-635). Before that the
same code existed only as a class:

```bash
# Source 3.0 and newer
kafka-get-offsets.sh --bootstrap-server "$SRC" --topic orders

# Source older than 3.0 — no launcher script, and --broker-list not
# --bootstrap-server
kafka-run-class.sh kafka.tools.GetOffsetShell --broker-list "$SRC" --topic orders
```

This matters in step 1 of the cutover checklist, where the whole point is
proving twice that the source's end offsets have stopped moving. A command that
does not run proves nothing.

Which distribution you run that from matters, and the boundary is **3.7**, not
4.0:

| Distribution running the command | `kafka-run-class.sh kafka.tools.GetOffsetShell` |
|---|---|
| 3.6.2 and older, including every 2.x | works — it is the real implementation |
| 3.7.0 and newer, including 4.x | `ClassNotFoundException` |

The class moved to `org.apache.kafka.tools` in 3.7.0
([KAFKA-14581](https://issues.apache.org/jira/browse/KAFKA-14581)) and — unlike
`JmxTool`, `ClusterTool` and the four other tools that kept a deprecated
forwarder under the old name until 4.0 — it was given no shim and no release
note. It simply stopped existing at 3.7, silently.

So a pre-3.0 source's offsets have to be read from a distribution old enough to
still have the class, which in practice means the source's own. That is the
argument for keeping a source-era toolbox pod, below.

### Listing and describing consumer groups

`kafka-consumer-groups.sh` is stable across the range — `--zookeeper` disappeared
in 2.0, and everything this runbook uses (`--describe`, `--reset-offsets`,
`--to-datetime`, `--dry-run`) has existed since well before 2.8. Two behaviour
changes to know:

- From 4.0, `Admin.describeConsumerGroups` raises `GroupIdNotFoundException`
  for an unknown group instead of returning one in state `DEAD`. Scripts that
  tested for `DEAD` to mean "no such group" now see an error.
- `--bootstrap-server` accepts only comma-separated values from 4.0.
  Space-separated lists were tolerated before.

### ACLs

The 2.x-era form is gone from 4.0 distributions entirely — `--authorizer`,
`--authorizer-properties` and `--zk-tls-config-file` were all removed:

```bash
# Cannot be typed into a 4.x distribution
kafka-acls.sh --authorizer-properties zookeeper.connect=zk:2181 --list

# Works against any era, provided the source brokers have an authorizer
kafka-acls.sh --bootstrap-server "$SRC" --command-config client.properties --list
```

The authorizer class itself changed under the same names: `SimpleAclAuthorizer`
until 2.4, `AclAuthorizer` from 2.4 (the old one removed in 3.0), and
`StandardAuthorizer` in 4.0, which keeps ACLs in the KRaft metadata log rather
than in ZooKeeper. Grants for the mirror's source user go through whichever the
source runs.

### Changing configuration on the source

A 4.x `kafka-configs.sh` uses the `incrementalAlterConfigs` API and **fails
against brokers older than 2.3**. A 2.1 or 2.2 source is inside the mirroring
floor but outside this one, so its configuration has to be changed from its own
era's tooling.

### Keep two toolboxes

```bash
# Source-era pod, for anything that touches the old cluster
kubectl run legacy-tools -n kafka-legacy-2x --restart=Never \
  --image=quay.io/strimzi/kafka:0.32.0-kafka-3.3.1 -- sleep infinity

# Current pod for the target
kubectl run tools -n kafka --restart=Never \
  --image=quay.io/strimzi/kafka:1.2.0-kafka-4.3.1 -- sleep infinity
```

One pod whose tools half-work against one of the two clusters is the worst
option: commands succeed and report things that are not true of the cluster you
meant. Label them by era and use the right one deliberately.

### What does not change

The mirror. MirrorMaker 2 speaks the ordinary client protocol to both ends and
never asks how the source stores its metadata, so ZooKeeper versus KRaft makes
no difference to replication — only to how you administer the source. And
`offsetSyncs.location: target`, the setting that makes a read-only source
possible, is a MirrorMaker-side option: **the source's version does not gate
it.**

---

## Troubleshooting

### A connector task has failed

`MirrorMaker2TaskFailed`. One or more tasks on a mirror connector are in
`FAILED` and have been for two minutes.

**What it actually means.** A connector with three tasks and one failed one
still reports `RUNNING` on the CR and still replicates most partitions. The
partitions the dead task owned simply stop. So this is not "the mirror is down"
— it is "part of the mirror is down, and every aggregate number still looks
fine". Lag on the affected partitions grows without bound; end offsets on the
target for the topics that task owned stop moving while the others keep going.

```bash
NS=kafka; MM2=mm2-mirror-maker2

# Which connector, and which task, and the trace.
kubectl -n $NS get kafkamirrormaker2 $MM2 -o jsonpath='{.status.connectors}' \
  | python3 -m json.tool | less

# Same answer with the reason on one line per task:
kubectl -n $NS get kafkamirrormaker2 $MM2 \
  -o jsonpath='{range .status.connectors[*]}{.name}{"\t"}{.connector.state}{"\n"}{range .tasks[*]}{"  task "}{.id}{"\t"}{.state}{"\t"}{.trace}{"\n"}{end}{end}'
```

The `trace` is the whole diagnosis. Read it before anything else:

| Trace contains | Cause | Fix |
|---|---|---|
| `TopicAuthorizationException` naming `mm2-offset-syncs` | the SOURCE principal cannot create/write the offset-syncs topic, and `offsetSyncs.location` is `source` | ["Mirroring a source you may only read"](#mirroring-a-source-you-may-only-read) |
| `TopicAuthorizationException` naming a data topic | a missing ACL on one end. Which end depends on whether the name is the source's or the replicated one | grant it; the target half is auto-mode's job, the source half is yours |
| `UNSUPPORTED_VERSION` | the source is below the KIP-896 floor | [below](#unsupported_version-in-the-connector-log) |
| `OutOfMemoryError`, `GC overhead` | worker heap, usually with `tasksMax` far above the partition count | [rebalance storms](#workers-restart-repeatedly--rebalance-storms) |
| `TimeoutException` on metadata | the source went away, or egress is denied | [network layer](#denied-at-the-network-layer) |
| `InvalidReplicationFactorException` | `replication.factor` above the target's broker count | set `target.brokerCount` and the chart refuses it at render time |

**The fix, once you know the cause.** Fixing the cause is not enough on its own:
a failed task does not retry forever. `autoRestart` (on by default, capped at
10) restarts it for you; past the cap, or with it off, restart the connector
explicitly:

```bash
kubectl -n $NS annotate kafkamirrormaker2 $MM2 \
  strimzi.io/restart-connector="<alias>->target.MirrorSourceConnector" --overwrite
# or one task:
kubectl -n $NS annotate kafkamirrormaker2 $MM2 \
  strimzi.io/restart-connector-task="<alias>->target.MirrorSourceConnector:0" --overwrite
```

If the same task fails again immediately, the cause is not fixed. A restart loop
that hides a real fault is exactly what `autoRestart.maxRestarts` exists to
stop, so do not raise the cap to make the alert quiet.

### The task error rate is high

`MirrorMaker2HighErrorRate`. Tasks are logging errors above
`alerts.thresholds.errorRatePerSecond` (1/s by default) for five minutes, and
— this is the point — **nothing has failed**. Connect counts an error every time
a task logs one; a task that retries successfully every time still counts.

**What it actually means.** Something is wrong on every record, or every
refresh, and the connector is absorbing it. The two that produce a steady,
unremarkable rate:

- **A missing `AlterConfigs`** on the target user with
  `sync.topic.configs.enabled` on. One `TopicAuthorizationException` per topic
  per refresh interval, forever. See
  [that section below](#topicauthorizationexception-on-every-refresh-connector-still-running).
- **A serialization or record-size error** on a subset of records —
  `RecordTooLargeException` against the target's `max.message.bytes`, most
  often. Those records are dropped or retried into a stall while the rest flow,
  so replication looks alive and is incomplete.

```bash
# What is actually being logged, deduplicated. This is faster than reading it.
kubectl -n $NS logs -l strimzi.io/name=$MM2-mirrormaker2 --tail=2000 \
  | grep -iE 'error|exception' \
  | sed 's/.*\(Exception[A-Za-z.]*\)/\1/' | sort | uniq -c | sort -rn | head

# Errors per connector, so a fan-in release names the leg:
kubectl -n $NS logs -l strimzi.io/name=$MM2-mirrormaker2 --tail=2000 \
  | grep -oE '[a-z0-9-]+->[a-z0-9-]+\.Mirror[A-Za-z]+Connector' | sort | uniq -c
```

If the rate is high and the count of *distinct* exception types is one, it is a
configuration problem and the message names it. If it is many, look at the
workers first — heap pressure produces a broad spray of unrelated errors.

Turning the MirrorMaker logger up is the next step when the log says nothing
useful:

```yaml
logging:
  type: external
  mirrorLevel: DEBUG
```

That prints the topic refresh, what matched the pattern and what did not, and
the offset-sync decisions.

### Offset syncs are stale while records flow

`MirrorMaker2OffsetSyncStale`. **This is the expensive one.** Records are still
arriving on the target, the source connector is healthy, no task has failed —
and the translated consumer positions have not moved for
`alerts.thresholds.offsetSyncStaleMinutes` (30 by default).

**What it actually means, and why the other rules miss it.**
`MirrorMaker2CheckpointStalled` asks whether checkpoints are being *written*.
This one asks whether they are *moving*. A checkpoint task that keeps
re-emitting the same translated position still writes records, so its write rate
stays non-zero and the stall rule stays quiet. Nothing in the CR, the connector
status or the record counts says anything is wrong. The cost lands at the
failover: consumers resume at a position that stopped advancing hours ago and
replay everything since.

Three causes, and one command each distinguishes them.

**1. The source consumer groups stopped committing.** The checkpoint connector
translates *committed* offsets; a group that stopped committing has nothing new
to translate, and this is then not a fault at all.

```bash
# On the SOURCE. Twice, a minute apart — CURRENT-OFFSET must move.
kafka-consumer-groups.sh --bootstrap-server <source> --describe --group <g>
```

If the source groups are idle, the alert is correct and harmless. Silence it for
the window, do not "fix" it.

**2. The checkpoint connector cannot read the offset-syncs topic.** It reads
`mm2-offset-syncs.<target-alias>.internal` from wherever
`offsetSyncs.location` puts it. If the location was changed on a running mirror,
the new topic starts empty and translation restarts from scratch — checkpoints
regress and then sit still until it catches up, which is exactly this signature.

```bash
# Where does the chart think it is? (both connectors must agree — the chart
# refuses a hand-set value that disagrees, so this should be one answer)
kubectl -n $NS get kafkamirrormaker2 $MM2 -o jsonpath='{.spec.mirrors[*]}' \
  | tr ',' '\n' | grep offset-syncs

# Is it there, and is it growing? Run twice.
kafka-get-offsets.sh --bootstrap-server <that cluster> \
  --topic 'mm2-offset-syncs.<target-alias>.internal'
```

Empty and not growing, with `location: source`, usually means the source
principal cannot write it — see
["Mirroring a source you may only read"](#mirroring-a-source-you-may-only-read).

**3. The checkpoint connector's own task is wedged** — running, and producing
the same numbers. Restart it; if the position advances afterwards, that was it:

```bash
kubectl -n $NS annotate kafkamirrormaker2 $MM2 \
  strimzi.io/restart-connector="<alias>->target.MirrorCheckpointConnector" --overwrite
```

**Do not fail over while this is firing.** If you must, treat the translated
offsets as unusable and have consumers start from a position you choose
deliberately — the alternative is a silent replay of everything since the
checkpoints froze.

### Everything is `Ready` and nothing is replicating

The most common failure, and it has five causes. Work down the list — they are
ordered by frequency, not by how clever they are.

| Check | Command | If wrong |
|---|---|---|
| Connectors actually RUNNING | `kubectl -n kafka get kafkamirrormaker2 $MM2 -o jsonpath='{.status.connectors}'` | see the specific states below |
| Source ACLs | on the **source** cluster: does the MM2 user have Read + Describe on the topics *and* groups? | grant them — this chart cannot |
| `topicsPattern` matches | `kubectl -n kafka logs -l strimzi.io/name=$MM2-mirrormaker2 \| grep -i 'topic'` | fix the pattern; it is a regex, not a glob |
| NetworkPolicy | `kubectl -n kafka get networkpolicy` | see "denied at the network layer" below |
| The source is genuinely idle | end offsets on the source, twice | nothing is wrong |

### Reading the preflight verdicts

With `preflight.enabled: true` the install runs one `ApiVersions` handshake per
source, with the workers' own client, and classifies the outcome. The Job
survives success so its log can always be read:

```bash
kubectl -n kafka logs job/mm2-mirror-maker2-preflight
```

| Verdict | Means | Next step |
|---|---|---|
| `✅ HANDSHAKE` | protocol, network and credentials all fine | none |
| `❌ PROTOCOL` | `UNSUPPORTED_VERSION` — the broker is below the KIP-896 floor | two-hop migration, below |
| `❌ DNS` | the bootstrap host does not resolve | namespace or cluster domain in `bootstrapServers` |
| `❌ NETWORK` | resolvable, not reachable | NetworkPolicy egress, the port, or a listener whose advertised address is not on this network |
| `❌ AUTH` | the broker rejected the credentials | the source-side user, its password Secret, or the SASL mechanism |
| `❌ TLS` | the broker expects TLS but `tls.enabled` is false | set `tls.enabled` and `trustedCertificateSecret` |
| `❌ LISTENER` | the broker closed the connection during authentication | the port expects TLS and/or a SASL mechanism the source's `tls` / `authentication` do not configure |
| `⏭ TLS` | a TLS source is reachable; trust and credentials were **not** checked | expected — the probe has no truststore; the workers will use `trustedCertificateSecret` |
| `⏭ CREDENTIAL` / `⏭ AUTH` | the source's Secret was not present yet (first install with `secretSync`) | expected — reachability and protocol were verified; credentials are checked on the next upgrade |

### `UNSUPPORTED_VERSION` in the connector log

The source broker predates Kafka 2.1 and these workers are a 4.x client
([KIP-896](https://cwiki.apache.org/confluence/x/K5sODg)). This is a protocol
removal, not a setting — there is no flag that makes it work.

**Fix:** migrate in two hops. Legacy → an intermediate Kafka 3.x cluster (whose
client can still read the old broker) → 4.x. Or run a MirrorMaker on an older
Kafka line for the first hop.

Turn on `preflight.enabled` and this fails at `helm install` instead of an hour
later.

### Denied at the network layer

Symptom: the connector times out, DNS resolves, and nothing in the Kafka log
shows a connection attempt. MM2 workers are Connect workers but Strimzi labels
them `strimzi.io/kind: KafkaMirrorMaker2`, so a policy written for
`KafkaConnect` does not match them.

```bash
kubectl -n kafka get networkpolicy kafka-brokers -o yaml | grep -A3 KafkaMirrorMaker2
```

If that returns nothing, set `networkPolicies.mirrorMaker2Namespace` on the
`kafka-cluster` chart to the namespace MM2 runs in.

### The mirror is replicating its own output

Symptom under an identity policy: topic count grows without bound; you see
`heartbeats`, `checkpoints`, or `<groupId>-configs` being mirrored.

With names preserved the mirror matches what it produces. Set
`replicationPolicy.excludeInternalTopics: true` (the default), or supply your own
`topicsExcludePattern` covering the MM2 internal topics.

### Topics arrive with the wrong names

`legacy.orders` instead of `orders`, or the reverse. The replication policy is
not the one you think:

```bash
kubectl -n kafka get kafkamirrormaker2 $MM2 \
  -o jsonpath='{.spec.mirrors[0].sourceConnector.config}' | tr ',' '\n' | grep policy
```

`replicationPolicy.mode: identity` must appear as
`replication.policy.class: org.apache.kafka.connect.mirror.IdentityReplicationPolicy`
on **both** connectors. Changing it later does not rename topics that have
already been replicated — you get both sets.

### Consumer groups have no offsets on the target

In order:

1. `sync.group.offsets.enabled: "true"` on the **checkpoint** connector. Without
   it MM2 emits checkpoints but never writes `__consumer_offsets`.
2. `groupsPattern` matches the group.
3. The target user has Read + Describe on the group — on **every** mirrored
   group, whose names the source's consumers chose. The in-repo `kates-mm2`
   user is granted group `*` for exactly this reason; a user built from a
   prefix list will translate the groups it knows about and silently skip the
   rest.
4. Translation lags the data. Wait one `sync.group.offsets.interval.seconds`.

Note that translation is **approximate** by design: offsets are mapped through
the offset-syncs topic at record granularity. A consumer moved onto a translated
offset may re-read a small number of records. MirrorMaker 2 is at-least-once;
plan for duplicates rather than being surprised by them.

### Connector topic creation fails

`replication.factor` exceeds the target's broker count. Set `target.brokerCount`
and the chart refuses to render instead of failing here.

### `TopicAuthorizationException` on every refresh, connector still RUNNING

`sync.topic.configs.enabled` is on (it is in both migration presets) and the
target user lacks `AlterConfigs` on the replicated topics. `Alter` is not
enough — it covers partition changes, not configuration. The `kates-mm2` user
and the chart's auto-mode ACLs both grant it; a hand-written user usually does
not.

### The operator is locked out

`.status.connectors` stays empty, the CR is `Ready`, and `test-connectors` times
out at 240 seconds. The Strimzi Cluster Operator creates connectors over the
Connect REST API on 8083, and with `STRIMZI_NETWORK_POLICY_GENERATION=false` on
the operator the chart's own policy is the only thing admitting it. Check
`networkPolicy.strimziOperatorNamespace` names the namespace the operator
actually runs in.

### Workers restart repeatedly / rebalance storms

`MirrorMaker2RebalanceStorm` fires. Tasks do not replicate during a rebalance, so
this reads as intermittent lag with no failed tasks. Usually heap pressure
(check `MirrorMaker2WorkerHeapHigh`) or a `tasksMax` far above what the source's
partition count justifies.

### Replication is slow but healthy

Raise `sourceConnector.tasksMax` toward the source's partition count — a task
maps to partitions, so more tasks than partitions buys nothing. Then check
whether the bottleneck moved to the target's produce path (`kafka_producer_*`)
or the worker heap.

---

## Reference: what each metric answers

| Metric | Question | Alert |
|---|---|---|
| `kafka_connect_source_task_metrics_source_record_write_total` | Is anything moving at all? | `MirrorMaker2NoRecordsReplicated`, `MirrorMaker2CheckpointStalled` |
| `kafka_connect_mirror_source_connector_replication_latency_ms_max` | Is it safe to cut over? | `MirrorMaker2ReplicationLagHigh` |
| `kafka_connect_mirror_source_connector_record_age_ms_max` | How far behind are the fetchers? | — |
| `kafka_connect_mirror_checkpoint_connector_checkpoint_latency_ms_max` | Are the translated consumer positions still **advancing**? | `MirrorMaker2OffsetSyncStale` |
| `kafka_connect_worker_metrics_connector_failed_task_count` | Is something broken outright? | `MirrorMaker2TaskFailed` |
| `kafka_connect_task_error_metrics_total_errors_logged` | Is it absorbing an error on every record? | `MirrorMaker2HighErrorRate` |
| `kafka_connect_worker_rebalance_metrics_completed_rebalances_total` | Is it stable? | `MirrorMaker2RebalanceStorm` |

Enable them with `metrics.enabled=true`; `alerts.enabled=true` turns them into
PrometheusRules — each carrying a `runbook_url` into the section of this file
that resolves it — and `dashboard.enabled=true` renders a Grafana dashboard
built around exactly these questions, with one collapsed row per source.

Two caveats on the selectors. Rules about a mirror are rendered **once per
source** and carry a `source` label, so a fan-in release names the stuck leg
instead of averaging it away. And these names are the **JMX exporter's**: with
`metrics.type: strimziMetricsReporter` the same numbers are published under
different names, which is why the chart refuses that combination unless
`alerts.allowReporterMetrics` says you have ported the expressions.

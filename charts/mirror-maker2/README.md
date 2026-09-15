# mirror-maker2

Deploys a Strimzi **KafkaMirrorMaker2** cluster for cross-cluster replication —
active/passive DR, aggregation, and **cross-version migration** (Kafka 2.x or
3.x → 4.x). MM2 runs on Kafka Connect, so the worker surface (replicas, JVM,
resources, metrics, NetworkPolicy) matches the `connect-cluster` chart. The
difference: MM2 spans **two Kafka clusters**.

| | |
|---|---|
| Install (loopback smoke test) | `helm install mm2 charts/mirror-maker2 -n kafka -f charts/mirror-maker2/values-kind.yaml` |
| Migrate from Kafka 2.8.2 | `helm install mm2 charts/mirror-maker2 -n kafka -f charts/mirror-maker2/values-migrate-2x.yaml` |
| Migrate from Kafka 3.9.1 | `helm install mm2 charts/mirror-maker2 -n kafka -f charts/mirror-maker2/values-migrate-3x.yaml` |
| Mirror a source you may only **read** | add `-f charts/mirror-maker2/values-readonly-source.yaml` |
| Consolidate several sources | `-f charts/mirror-maker2/values-fan-in.yaml` |
| Cut over | add `-f charts/mirror-maker2/values-cutover.yaml` |
| Fail over (the source is gone) | add `-f charts/mirror-maker2/values-failover.yaml` |
| Prove it end to end | `make mm2-migration-test` |
| See what it replicated | `make mm2-topics` |
| Remove it (including the kept CR) | `make mm2-undeploy` |

**New here?** [`docs/`](docs/) is the task-organised entry point — the
[Migration Plan](docs/migration-plan.md) for moving a cluster onto the 4.x
primary, plus [Installation](docs/installation.md),
[Configuration](docs/configuration.md), and source-specific guides for
[Kafka 2.8](docs/migration-2.8-to-4.x.md) and
[Kafka 3.x](docs/migration-3.x-to-4.x.md). This README is the exhaustive
reference behind them.

## Model (Strimzi v1 API)

- **`target`** — the cluster MM2's Connect runtime runs against; it owns the
  Connect internal config/offset/status topics. Defaults to the in-repo
  `krafter` cluster.
- **`mirrors[]`** — one entry per replication flow, each with its own
  **`source`** cluster (bootstrap + tls + auth + `kafkaVersion`) plus
  `topicsPattern`, `groupsPattern`, and the `sourceConnector` /
  `checkpointConnector` config.

There is no `heartbeatConnector` — the v1 API dropped it. Replication lag comes
from the source connector's own `replication-latency-ms` metric instead.

Out of the box the chart is a **loopback** (source == target == `krafter`,
reading and writing as the same `kates-mm2` user) so it renders and smoke-tests
without a second cluster. Replace `mirrors[].source` with the real cluster for a
genuine mirror.

### Configuring source & target clusters

Both `target` and each `mirrors[].source` accept **either** an in-cluster
reference (`clusterName` + `namespace`, bootstrap FQDN computed —
`<clusterName>-kafka-bootstrap.<namespace>.svc.<clusterDomain>:<9092|9093>`)
**or** an explicit `bootstrapServers` (external / inter-cluster), which wins
when set. The port follows `tls.enabled` (9093) for computed addresses.

| Topology | source | target |
|---|---|---|
| Same cluster (loopback / rename) | `clusterName: krafter, namespace: kafka` | `clusterName: krafter, namespace: kafka` |
| Same cluster, different namespaces | `namespace: kafka-a` | `namespace: kafka-b` |
| Two in-cluster Strimzi clusters | `clusterName: krafter-dr, namespace: kafka-dr` | `clusterName: krafter, namespace: kafka` |
| External / cross-datacenter | `bootstrapServers: kafka-east.example.com:9094` | `clusterName: krafter, namespace: kafka` |
| **Legacy 2.x / 3.x source** | `bootstrapServers: legacy-legacy-kafka-bootstrap.kafka-legacy-2x.svc:9092` + `kafkaVersion: "2.8.2"` | `clusterName: krafter, namespace: kafka` |

## Which topology is this? Decide before anything else

One CR shape serves four jobs, and the configuration that is right for one is
wrong — usually silently wrong — for the next. Find your row first; everything
below reads differently depending on it.

| Topology | What it is for | Overlay | Policy | Pick it wrong and… |
|---|---|---|---|---|
| **Loopback** | smoke-testing the chart on one cluster (source == target). What ships by default. | `values-kind.yaml` / `values-dev.yaml` | `default` **only** | under `identity`, `orders` is replicated to `orders` on the same cluster — the connector's own output, matched again, without end. Nothing excludes it and nothing refuses it; a loopback is the one topology where the policy is not a preference. Under `default` the computed `topicsExcludePattern` is what stops the equivalent (`source.source.orders`), which is why `excludeInternalTopics: false` is not a knob to turn casually. |
| **Migration** | 2.x / 3.x / 4.x → 4.x, one source, finite lifetime, ends in a cutover | `values-migrate-2x\|3x\|4x.yaml`, then `values-cutover.yaml` | `identity` | with `default`, every topic lands as `legacy.orders`. You find out when the repointed consumers subscribe to `orders` and get nothing — after the cutover, with producers already moved. |
| **Fan-in** | several sources consolidated into one target | `values-fan-in.yaml` | `default` | under `identity`, two sources whose patterns overlap write the *same* target topics: interleaved records in one offset space, no error anywhere, and no way to separate them afterwards. The chart refuses the cases it can prove; see [the rails](#the-rails--what-the-chart-refuses-and-why). |
| **DR (active/passive)** | permanent, ends in a failover you hope never to run | `values-prod.yaml`, then `values-failover.yaml`, then `values-failback.yaml` | `default` | with `identity`, a failback re-reads what the forward mirror wrote, under the same names, forever. Stopping the *checkpoint* connector alongside the source one is the other way to get this wrong: all the data, and no consumer position. |

Two things are **modifiers**, not topologies — they compose with any row above:

- **A source you may only read** — `readOnlySource: true` on the mirror (or
  `offsetSyncs.location: target`). Almost every real migration is this.
  [`values-readonly-source.yaml`](values-readonly-source.yaml), and
  [the credential contract](#the-credential-contract-read-this) explains why it
  is not optional.
- **A large estate** — [`values-scale.yaml`](values-scale.yaml): task budget,
  refresh interval and producer batching, in that order of importance. See
  [Scaling means tasks, not replicas](#scaling-means-tasks-not-replicas).

The DR/migration split in more detail, because it is the one that costs money:

| | Disaster recovery / aggregation | Migration |
|---|---|---|
| `replicationPolicy.mode` | `default` — topics land as `<alias>.<topic>`, so a record's origin is visible | `identity` — topics keep their names, so consumers repoint and find them |
| `checkpointConnector` offset sync | on, for failover | on, for cutover |
| Lifetime | permanent | weeks, then removed |
| Ends with | a failover you hope never to run — `values-failover.yaml`, then `values-failback.yaml` | a cutover you rehearse — `values-cutover.yaml` |
| Preset | `values-prod.yaml` | `values-migrate-2x.yaml` / `values-migrate-3x.yaml` |

---

## Cross-version migration (2.x / 3.x → 4.x)

This is the case the chart is built around, and it has three sharp edges that
nothing else in the deployment will warn you about.

### 1. There is a protocol floor, and it is a cliff

[KIP-896](https://cwiki.apache.org/confluence/x/K5sODg) removed the pre-2.1
client protocol API versions in Kafka 4.0. These workers run `version: 4.3.0`,
so they *are* a 4.x client: they can read brokers **2.1 and newer, and nothing
older**. Below that the broker answers `UNSUPPORTED_VERSION`, the
`MirrorSourceConnector` fails, and the CR keeps saying `Ready`.

Declare the source's version and the chart checks it before deploying anything:

```yaml
compatibility:
  enforce: true
  minSourceVersion: "2.1.0"
  requireDeclaredVersion: true      # refuse sources that don't say
mirrors:
  - source:
      alias: legacy
      bootstrapServers: legacy-legacy-kafka-bootstrap.kafka-legacy-2x.svc:9092
      kafkaVersion: "2.8.2"
```

A source below the floor fails `helm install` in two seconds with a message
naming the alias, the version, and the way out. **Below 2.1, migrate in two
hops** — legacy → an intermediate 3.x cluster → 4.x. It is a protocol removal,
not a setting.

### 2. The default policy renames every topic

`DefaultReplicationPolicy` writes `orders` as `legacy.orders` on the target.
That is right for DR aggregation, where you need to know which cluster a record
came from — and wrong for a migration, where the whole point is that consumers
repoint at a new cluster and find the topics they already know.

```yaml
replicationPolicy:
  mode: identity            # topic names are PRESERVED
```

### 3. The computed `topicsExcludePattern`

Unless you set `topicsExcludePattern` on a mirror yourself, the chart computes
one, because there are two ways a mirror ends up consuming its own output:

- **Under identity mode** the replicated topics keep their names, so
  `heartbeats`, `checkpoints`, the offset-syncs topic and the Connect internal
  topics all become eligible for replication. MirrorMaker's stock exclusions
  do not cover them — under the default policy those topics are already renamed
  out of the way.
- **Under the default policy on a loopback** (source == target), `orders` lands
  as `source.orders` on the same cluster, still matches `.*`, and MirrorMaker's
  cycle detection does not catch it because the target alias differs from the
  source alias. It is mirrored again as `source.source.orders`, every refresh
  interval, without end.

So the computed pattern is always:

```text
.*[\-\.]internal,.*\.replica,__.*,<groupId>-.*,<alias>\..*
```

with identity mode appending
`mm2-.*,heartbeats,checkpoints,.*\.heartbeats,.*\.checkpoints\.internal`.
`replicationPolicy.excludeInternalTopics: false` drops all of it and leaves only
MirrorMaker's own defaults.

### Preflight — prove the wire before deploying on it

```yaml
preflight:
  enabled: true
  failOnError: true
```

A pre-install hook Job that probes every source with the **same 4.x client the
workers will use**, so a pass is evidence rather than an assumption. It runs the
`ApiVersions` handshake once per source and classifies the outcome:

| Verdict | Means | Fails the install? |
|---|---|---|
| `✅ HANDSHAKE` | The broker answered a 4.3.0 client — protocol, network and credentials all fine | no |
| `❌ PROTOCOL` | `UNSUPPORTED_VERSION` — below the KIP-896 floor | yes |
| `❌ DNS` | the bootstrap host does not resolve — namespace or cluster domain | yes |
| `❌ NETWORK` | resolvable but unreachable — NetworkPolicy, port, or an unadvertised listener | yes |
| `❌ AUTH` | credentials rejected — the source-side user, its Secret, or the mechanism | yes |
| `❌ TLS` | the broker expects TLS on this port but `tls.enabled` is false | yes |
| `❌ LISTENER` | the broker closed the connection during authentication — the port expects TLS and/or SASL that the source's settings do not configure | yes |
| `⏭ TLS` | a TLS source is reachable and speaking TLS, but the probe has no truststore, so trust and credentials were **not** verified | no |
| `⏭ CREDENTIAL` / `⏭ AUTH` | the source's password Secret is not here yet (`secretSync` copies it after install); name resolution, reachability and the protocol floor were verified without it, the credentials will be on the next upgrade | no |

The `⏭` rows are deliberate: building a truststore (and, for mTLS, a keystore)
is out of scope for a 20-second hook, and a green tick that did not test
anything is worse than none. The Job survives a successful run so its log can
be read:

```bash
kubectl -n kafka logs job/mm2-mirror-maker2-preflight
```

### Migration presets

Both `values-migrate-2x.yaml` and `values-migrate-3x.yaml` set identity policy,
declare the source version, turn preflight on, enable offset translation and
topic-config sync, and size for a single-broker kind target. They differ in the
task count and checkpoint intervals — a 3.9 source feeds fetchers faster and
speaks the modern group protocol, so both can be tighter. Adjust
`mirrors[0].source.bootstrapServers` and the replication factors for a real
target. Pair them with [`charts/legacy-kafka`](../legacy-kafka/), which deploys
the 2.x or 3.x source that Strimzi 1.1.0 cannot.

Full walkthroughs: [2.x → 4.x](../../docs/tutorials/11-migrating-kafka-2x-to-4x.md),
[3.x → 4.x](../../docs/tutorials/12-migrating-kafka-3x-to-4x.md).

### Cutover

`values-cutover.yaml` is the last step of a migration expressed as a values file:

```bash
helm upgrade mm2 charts/mirror-maker2 -n kafka \
  -f charts/mirror-maker2/values-migrate-2x.yaml \
  -f charts/mirror-maker2/values-cutover.yaml
```

It stops the **source** connector and keeps the **checkpoint** connector
running. Stopping both together is the classic cutover mistake: every record is
on the target, and no consumer knows where to start reading it. `stopped` rather
than `paused` matters too — `stopped` releases the connector's tasks, so a
worker restart cannot resume the mirror after producers have moved and write
records behind the ones your new producers are appending.

The state is set through a chart-level `cutover` block rather than per mirror,
because Helm merges maps deeply but **replaces lists wholesale** — an overlay
reaching into `mirrors[0]` would silently discard that mirror's bootstrap,
credentials and connector config, in a file whose whole job is to be applied
under time pressure.

The checklist for *when* to apply it is in
[docs/mirror-maker2-runbook.md](../../docs/mirror-maker2-runbook.md).

### Two clusters, not two namespaces

The tutorials and the e2e test put the legacy source in another namespace of
the same kind cluster, because that fits a laptop. A real migration has the
source on another cluster, and four things change:

1. **Routing.** `bootstrapServers` must be reachable from the target cluster's
   pod network — an external listener (NodePort, LoadBalancer, or a route)
   whose *advertised* addresses are also reachable, not only the bootstrap.
   `kafka-broker-api-versions.sh` from a pod in the MM2 namespace is the test;
   preflight runs exactly that.
2. **Egress.** `networkPolicy.kafka.ports` must include the source's port;
   non-standard ports need adding, or `networkPolicy.extraEgress`. Kafka egress
   defaults to `0.0.0.0/0` on those ports, because the shipped default has to
   work for an external source whose address nobody has declared. Set
   `networkPolicy.kafka.perSource: true` to narrow it to one rule per cluster —
   every source must then be in-cluster or carry `source.egressCIDR`.
3. **Trust.** A TLS source needs its CA in a Secret in the MM2 namespace
   (`tls.trustedCertificateSecret`); a different cluster is a different CA.
4. **Credentials.** The source principal lives on the source cluster and is
   provisioned there, by whoever runs it — see the contract below.

---

## The credential contract (read this)

MM2 needs a `KafkaUser` on **both** ends, and the source-side half **depends on
one setting**: `offsetSyncs.location`.

MirrorMaker records the source→target offset mapping in
`mm2-offset-syncs.<target-alias>.internal`, and by Kafka's default it writes
that topic to the **source** cluster. So a mirror is not a read-only operation
on the source unless you make it one. This chart never used to say that, and
"READ + DESCRIBE" — the contract everyone hands their platform team — is true
for exactly one of the two settings.

### Source side

| `offsetSyncs.location` | What MirrorMaker does to the source | ACLs the SOURCE principal needs |
|---|---|---|
| `source` (Kafka's default) | reads the data **and creates and writes `mm2-offset-syncs.<target-alias>.internal` there** | topic `<mirrored>`: **Read, Describe**<br>topic `mm2-offset-syncs.` (prefix): **Create, Write, Describe**<br>group `<mirrored>`: **Describe**<br>cluster: **Describe** |
| `target` ([KIP-716](https://cwiki.apache.org/confluence/display/KAFKA/KIP-716:+Allow+configuring+the+location+of+the+offset-syncs+topic+with+MirrorMaker2), Kafka 3.0+) | reads, and writes **nothing** | topic `<mirrored>`: **Read, Describe**<br>group `<mirrored>`: **Describe**<br>cluster: **Describe** |

Add `DescribeConfigs` on the mirrored topics when
`sync.topic.configs.enabled` is on (both migration presets turn it on): the
connector reads the source's topic configuration in order to apply it on the
target.

**This is why a read-only source needs `readOnlySource: true`.** With the
default location, a principal that can only read produces no error at
`helm install`, no error in the CR, and a `TopicAuthorizationException` inside
the connector minutes later — the CR still says `Ready`, `.status.connectors`
still says `RUNNING`, and the only symptom is that nothing arrives. Set it
before the first install:

```yaml
mirrors:
  - source: { … }
    readOnlySource: true      # shorthand for offsetSyncs.location: target
```

or chart-wide with `offsetSyncs.location: target`, which is what
[`values-readonly-source.yaml`](values-readonly-source.yaml) does. **Before**
the first install, because flipping it on a running mirror starts translation
from an empty topic: checkpoints regress until it catches up, and a failover
during that window resumes consumers in the wrong place.

The chart puts the setting on **both** connectors — Kafka requires them to
agree, and a mismatch means the checkpoint connector reads a topic the source
connector never writes, so translation silently produces nothing. Hand-set one
of them to something else and the render is refused.

### Target side

| Who provisions it | ACLs |
|---|---|
| `kafka-cluster` chart (`kates-mm2`), or this chart with `kafkaUser.create=true` | topic `<groupId>` (prefix): Read, Write, Create, Describe, DescribeConfigs<br>topic `<alias>.` (prefix, one per mirror): Read, Write, Create, Describe, DescribeConfigs, Alter, **AlterConfigs**<br>topic `mm2-offset-syncs.` (prefix): Read, Write, Create, Describe — **only when some mirror keeps that topic here**<br>group `<groupId>`, `connect-` (prefix): Read, Describe<br>group **`*`**: Read, Describe — when any mirror translates offsets<br>cluster: Describe, DescribeConfigs, Create |

Three of those are worth a sentence each.

`AlterConfigs` (not just `Alter`) is what `sync.topic.configs.enabled` needs —
the connector syncs retention and cleanup policy through
`incrementalAlterConfigs`, and without it logs `TopicAuthorizationException` on
every refresh while staying `RUNNING`. Group `*` is what offset translation
needs: the checkpoint connector writes the committed offsets of *every*
mirrored consumer group, whose names are whatever the source's consumers chose,
so no prefix can cover them — turn `sync.group.offsets.enabled` off, or use
`authorization.mode: custom`, to narrow it.

The `mm2-offset-syncs.` grant now **follows the setting**. Through 0.3.0 it was
unconditional, which meant that with Kafka's default location the target user
carried a grant on a topic that is not on that cluster — inert, and misleading
to anyone auditing the least-privilege set. There is no `heartbeats` grant at
all any more: the v1 API dropped `heartbeatConnector`, so nothing here creates
that topic.

A missing **source** ACL does not error — the `MirrorSourceConnector` stalls and
nothing replicates. The `test-connectors` Helm test and the
`MirrorMaker2NoRecordsReplicated` alert both exist to catch exactly that, and
`preflight.enabled` catches the authentication half of it before install.

`kafkaUser.create=true` with `authorization.mode: auto` derives the target
grants from the source aliases — which only works under the default policy. In
identity mode the replicated topics keep the source's names, so the chart
refuses to render without `kafkaUser.topicGrants` covering them.

### Cross-namespace / inter-cluster checklist

MM2 reads each cluster's `passwordSecret` and TLS cert **from its own
namespace**. When a source or target Kafka cluster is elsewhere, do all of:

1. **Credentials present here.** Enable `secretSync` to copy them on every
   install/upgrade:

   ```yaml
   secretSync:
     enabled: true
     secrets:
       - { name: kates-mm2, fromNamespace: kafka }
       - { name: kates-mm2-source, fromNamespace: kafka-dr }
       - { name: krafter-cluster-ca-cert, fromNamespace: kafka }
       # Both ends called their credential kates-mm2? Rename on copy:
       - { name: kates-mm2, fromNamespace: kafka-legacy41, as: kates-mm2-legacy41 }
   ```

   `as` is the name the copy gets here (default: the source's). Two Strimzi
   clusters shaped by `kafka-cluster` both have a `kates-mm2` user, so the
   source's Secret must land under another name, which the mirror's
   `source.authentication.secretName` then references — `values-migrate-4x.yaml`
   is the worked example. …or annotate the Secrets for kubernetes-reflector, or create them by hand.
   The sync runs as a post-install hook, so on a **first** install the CR is
   created before the copies exist — the operator retries until they appear —
   and the preflight probe runs without the source credential, verifying
   reachability and the protocol floor and saying, explicitly, that the
   credentials will be checked on the next upgrade. (It was tried the other
   way; a pre-install sync needs hook-resource RBAC, and hook resources are
   not cleaned up by `helm uninstall` — a Role granting `get` on Secrets in
   every source namespace, forever, is worse than one unchecked credential.)
2. **CA certs for TLS.** Set `tls.enabled: true` + `trustedCertificateSecret`
   per cluster and make sure that Secret is one of the synced ones.
3. **NetworkPolicy egress.** The default policy allows Kafka egress to any IP on
   `networkPolicy.kafka.ports`. Non-standard port? Add it there. Want it
   narrowed to the actual clusters? `networkPolicy.kafka.perSource: true`, plus
   a `source.egressCIDR` on any source that is not in-cluster.
4. **NetworkPolicy *ingress* on the target.** The `kafka-cluster` chart's
   broker policy admits MM2 via `networkPolicies.mirrorMaker2Namespace`
   (defaults to the Kafka release namespace). MM2 in a different namespace must
   be named there, or the mirror is denied at the network layer while reporting
   itself perfectly healthy.
5. **The operator can reach the workers.** `networkPolicy.strimziOperatorNamespace`
   must name the namespace the Cluster Operator runs in (`strimzi-operator` by
   default). It creates the connectors over the Connect REST API; locked out,
   `.status.connectors` stays empty forever while the CR is `Ready`.
6. **ACLs on both ends** — target managed in-repo, source granted out-of-band.

## Durability

`sourceConnector.config.replication.factor` and the offset-syncs / checkpoints
topic factors must not exceed the target's broker count. Declare
`target.brokerCount` and the chart enforces it at render time instead of letting
the connector fail inside topic creation, minutes after a successful install.

The defaults are RF3; the `values-kind.yaml` / `values-dev.yaml` /
`values-migrate-*.yaml` overlays drop to RF1 for single-broker clusters. RF1 is
**not** durable — never use it for real DR.

## Connector lifecycle

`state`, `autoRestart`, `listOffsets` and `alterOffsets` are passed through to
each connector:

```yaml
mirrors:
  - source: { ... }
    sourceConnector:
      state: running               # running | paused | stopped
      autoRestart: { enabled: true, maxRestarts: 10 }
```

The chart-level `autoRestart` is the default for both connectors. It is on with
a cap of 10: a mirror that dies on a transient source blip and stays dead is the
most common cause of "replication silently stopped over the weekend", but an
unbounded restart loop hides a real fault.

## Naming topics and groups without writing a regex

`topicsPattern` and `groupsPattern` are Java regexes, and hand-escaping one
inside YAML is where these releases go wrong — `orders.payments` matches
`ordersXpayments`, and nobody notices until the wrong topic is mirrored. Give a
**list** instead and the chart compiles it, with the metacharacters quoted:

```yaml
mirrors:
  - source: { … }
    topics: ["kates.orders", "kates.payments"]   # → kates\.orders|kates\.payments
    groups: ["billing"]
```

The patterns still win when both are set, so an existing values file keeps
behaving exactly as it did. One consequence worth knowing: with a **list**, the
mirror selects an exact set of names, so `tests.replication.topic` — which
suffixes its default with the source alias on a multi-source release — would
name a topic the mirror provably does not select. The test falls back to the
mirror's first declared topic; `tests.replication.topicByAlias` chooses a
different one.

## Fan-in — several sources, one target

`mirrors[]` has always been a list. What 0.4.0 makes true is that everything
*around* it is per-source too: the ACL prefixes, the NetworkPolicy egress, the
alerts, the dashboard rows, and the data tests all range over the list rather
than describing the first entry.

```bash
helm upgrade --install mm2 charts/mirror-maker2 -n kafka \
  -f charts/mirror-maker2/values-kind.yaml \
  -f charts/mirror-maker2/values-fan-in.yaml
```

The **alias** is what separates the sources, everywhere it matters: the
replicated topic prefix, the offset-syncs topic, the checkpoints topic, the ACL
grants, the egress rule, the `source` label on every alert, and the test topic
and group. Duplicate aliases are refused for that reason — two mirrors sharing
one would interleave two sources into one set of target topics, and MirrorMaker
would not complain.

Three per-source knobs are worth setting on a real fan-in:

| Value | Why |
|---|---|
| `networkPolicy.kafka.perSource: true` | egress becomes one rule per cluster instead of `0.0.0.0/0`. Every source must then be in-cluster (`clusterName` + `namespace`) or carry `source.egressCIDR`; the render names the one that is missing. |
| `secretSync.secrets[].as` | both sources probably ship a Secret called `kates-mm2`. `as` is the name the copy lands under here. |
| `tests.replication.topicByAlias` | one topic per leg, when the mirrors select exact lists rather than patterns. |

Alerts are rendered **once per source** and carry a `source` label, because the
alternative is an average: a fan-in release with three healthy legs and one
stuck one reads as healthy right up to the cutover. Alert *names* stay
alias-free on purpose — Alertmanager routes and silences on `alertname`, and a
per-source name would fork every receiver.

**Fan-in under `IdentityReplicationPolicy` is the dangerous shape**, which is
why it is a refusal and not a warning. With names preserved, two sources whose
patterns can match one topic write the same target topic: interleaved records,
one offset space, no error. Regex disjointness is not decidable at render time,
so the chart refuses the two cases that certainly overlap — identical patterns,
and `.*` — and leaves the rest to `replicationPolicy.allowIdentityFanIn: true`,
which is your assertion that the patterns really are disjoint.

## Failing over and failing back

A **cutover** is planned: the source is healthy, you stop producing, wait for
lag to reach zero, then switch. A **failover** is not: the source is gone,
whatever had not replicated is not coming, and the only remaining question is
where consumers resume on the target.

```bash
# The source is gone.
helm upgrade mm2 charts/mirror-maker2 -n kafka \
  -f charts/mirror-maker2/values-prod.yaml \
  -f charts/mirror-maker2/values-failover.yaml
```

`values-failover.yaml` stops the source connector (`stopped`, not `paused` —
that releases the tasks, so a worker restart cannot reconnect to a cluster
coming back up with a partial log), keeps the **checkpoint** connector running
so unmoved consumers still get translated positions, and turns `autoRestart`
off, because restarting into an unreachable cluster buys nothing but log noise.

Consumers resume either by reading the checkpoints themselves
(`RemoteClusterUtils`, the supported path, which needs nothing from this chart)
or by seeding the groups on the target from an offsets ConfigMap through the
connector's `alterOffsets` — which applies only while the connector is
`STOPPED`, which it now is. That block is left commented in the overlay: a
wrong offsets ConfigMap rewinds or skips every consumer in the group, and the
name is specific to one incident.

**Failing back is a separate release in the opposite direction**, never a matter
of re-enabling the first one. `values-failback.yaml` renders it, starting
`stopped` so nobody replicates in a direction they have not checked:

```bash
helm upgrade --install mm2-back charts/mirror-maker2 -n kafka \
  -f charts/mirror-maker2/values-prod.yaml \
  -f charts/mirror-maker2/values-failback.yaml \
  --set target.clusterName=<the original cluster> \
  --set 'mirrors[0].source.clusterName=<the failover cluster>'
```

**Both `--set`s are mandatory.** The overlay ships the source cluster empty, and
an empty `clusterName` falls back to the chart's default (`krafter` in `kafka`)
— which is also the default target, so a failback installed without them is a
mirror from a cluster to itself. It renders, and it is not what you want.

The order is the whole procedure, and it is in
[the runbook](../../docs/mirror-maker2-runbook.md#failing-over-and-failing-back).
The rule that makes it safe: **the forward mirror must be gone first.** Under an
identity policy the two directions re-read each other's output under the same
topic names, forever; the chart refuses to render the reverse mirror while it
can see the forward `KafkaMirrorMaker2` in the namespace (a `lookup`, so a dry
render or a cluster-less CI run skips the check and finds nothing).
`replicationPolicy.allowBidirectional: true` overrides it for provably disjoint
patterns. Under the default policy the two do not loop, but they do produce
`east.east.orders`.

Active-active is **two releases, one per direction, `DefaultReplicationPolicy`
only** — documented rather than pretended to be one CR.

## Scaling means tasks, not replicas

The number that decides MirrorMaker's throughput is `tasksMax` **per
connector**, not `replicas`. Connect distributes at most `sum(tasksMax)` tasks
across the workers, so a sixth replica against `sourceConnector.tasksMax: 3` +
`checkpointConnector.tasksMax: 1` gives you two pods with no work — and the CPU
average that triggered the scale-up falls, which an autoscaler reads as success.
`NOTES.txt` prints the two numbers side by side at install for that reason.

So `autoscaling.maxReplicas > sum(tasksMax)` is **refused**, with the arithmetic
in the message. The ways forward, in order:

1. Raise `tasksMax`. Its own ceiling is the number of partitions the pattern
   selects — more tasks than partitions buys nothing at all.
2. Lower `maxReplicas`.
3. `autoscaling.allowIdleWorkers: true`, when you want headroom before a
   `tasksMax` raise and have decided the idle pods are worth it.

`values-scale.yaml` is the profile for a large estate, and it is three changes
in order of how much they matter: a task budget (12 + 4 = 16, with
`maxReplicas: 12` under it), `refresh.topics.interval.seconds` raised from 60 to
600 (a 10 000-partition source rescanned every minute is permanent, measurable
load on both ends), and producer batching — a mirror is a bulk copier, and the
defaults favour latency. Lag-driven autoscaling (KEDA) is the honest answer for
demand-driven scaling and is deliberately **not** in this chart: it needs a
ScaledObject and a metrics source the platform does not require, and a wrong
autoscaler is worse than none.

## Exactly-once

```yaml
target:
  exactlyOnce:
    enabled: false
```

`MirrorSourceConnector` implements the KIP-618 APIs, so Connect's exactly-once
source support is reachable. One flag sets all three halves: the worker property
`exactly.once.source.support` on `spec.target.config`,
`consumer.isolation.level=read_committed` on every mirror's source consumer, and
— under `kafkaUser.create` — the `transactionalId` and cluster `IdempotentWrite`
grants the fencing producer needs. An explicit `consumer.isolation.level` that
contradicts it is refused rather than quietly producing duplicates under a
release that claims EOS.

Off by default, and the trade is real: throughput drops, and the source must be
a version that supports transactional reads. A migration usually wants
at-least-once plus a dedupe-aware verification, which is what this chart's
replication test asserts (**distinct** records received, not a count).

## The rails — what the chart refuses, and why

Every one of these fails in seconds at `helm install`, naming the way out. Every
one of them is something that otherwise fails minutes-to-hours later, inside a
connector, while `kubectl get kafkamirrormaker2` says `Ready`.

| Refused when | Because | Override |
|---|---|---|
| a source declares Kafka `< compatibility.minSourceVersion` (2.1.0) | KIP-896 removed the older protocol versions from 4.x clients; the connector cannot read that broker at all | `compatibility.enforce: false` |
| any `*.replication.factor` exceeds `target.brokerCount` | the connector fails inside topic creation, long after install reports success | leave `brokerCount: 0` |
| `kafkaUser.create` + identity policy + no `topicGrants` | replicated topics keep the source's names, so the ACLs cannot be derived from the alias | supply `topicGrants` |
| **two mirrors share a source alias** | the alias names the topic prefix, the offset-syncs topic and the checkpoints topic — duplicates interleave two sources into one set of target topics, silently | none; rename one |
| **identity policy + two mirrors with the same pattern, or with `.*`** | both write the same target topic names | `replicationPolicy.allowIdentityFanIn: true` |
| **`autoscaling.maxReplicas > sum(tasksMax)`** | Connect never runs more tasks than that; the extra workers idle and hide the real bottleneck | `autoscaling.allowIdleWorkers: true` |
| **a hand-set `offset-syncs.topic.location` that disagrees with the mirror's** | Kafka reads it on *both* connectors; a mismatch means the checkpoint connector reads a topic the source connector never writes | set it once via `offsetSyncs.location` |
| **`target.exactlyOnce.enabled` with a contradicting `consumer.isolation.level`** | EOS requires the source consumer to read only committed records | remove the override |
| **`networkPolicy.kafka.perSource` with a source that is neither in-cluster nor given an `egressCIDR`** | falling back to "anywhere" would defeat the point of turning it on | add `source.egressCIDR`, or `perSource: false` |
| **`alerts.enabled` with `metrics.type: strimziMetricsReporter`** | the rules are written against the JMX exporter's metric names; the reporter names the same numbers differently, so they would install and never fire | `alerts.allowReporterMetrics: true`, once you have ported the exprs |
| `alerts.enabled` or `podMonitors.enabled` without `metrics.enabled` | Strimzi only opens the scrape port when `metricsConfig` is set | none |
| a reverse mirror while the forward one is live, under identity | each direction re-reads the other's output under the same names | `replicationPolicy.allowBidirectional: true` |
| **any image key resolves to a floating tag** (`:latest`, or no tag at all) | what runs then depends on when a pod last restarted rather than on anything recorded here — and for the worker and pre-flight images that is the Kafka client version, the one variable every rail above exists to hold still | none; use a version tag or a `@sha256:` digest |

The last one uses `lookup`, so it only bites against a real cluster; a `helm
template` or a `--dry-run` finds no CRs and passes.

## Observability

| Value | Renders |
|---|---|
| `metrics.enabled` | `metricsConfig` on the CR. `metrics.type: jmxPrometheusExporter` (default) also renders the exporter ConfigMap with MM2-specific rules (`record-age-ms`, `replication-latency-ms`, `byte-rate`, per topic, per group, per source alias) |
| `metrics.type: strimziMetricsReporter` | the operator's native path instead: a Kafka `MetricsReporter` inside the worker publishes Prometheus text itself. No exporter sidecar, no JMX, no ConfigMap — `metrics.allowList` is the whole configuration |
| `podMonitors.enabled` | PodMonitor (requires `metrics.enabled`), pointed at `podMonitors.scrape.<metrics.type>.{port,path}` |
| `alerts.enabled` | PrometheusRule — failed tasks, nothing replicating (dropped while `cutover.enabled`, when that is the plan), replication lag, checkpoint stall, **offset-sync staleness**, error rate, heap, **rebalance storms** |
| `alerts.slo.enabled` | in the same PrometheusRule: four recorded SLIs per source (`mm2:replication_latency_ms:max`, `mm2:checkpoint_latency_ms:max`, `mm2:records_replicated:rate5m`, `mm2:tasks_running:ratio`), the replication error ratio over 5m and 1h windows, and `MirrorMaker2ReplicationSLOBurning` — a multi-window burn-rate alert over `thresholds.replicationLatencyMs` with `slo.target` and `slo.burnRate` |
| `dashboard.enabled` | Grafana dashboard ConfigMap for the sidecar: one board, read top to bottom — a six-stat header, task states, replication, offset translation, errors, the **Replication SLO** row when the recording rules are installed, workers, then two collapsed sections (the client path, and one row per source) |
| `logging.type: external` | a log4j2 ConfigMap with `org.apache.kafka.connect.mirror` broken out, wired into the CR automatically |

Alerts and PodMonitors are capability-guarded (a missing Prometheus Operator CRD
never fails an install) but both **require** `metrics.enabled`, which the chart
enforces loudly — Strimzi only opens the scrape port when `metricsConfig` is
set, so rules evaluated without it would never fire and would look fine.

`podMonitors.scrape` is a value rather than a constant because a PodMonitor
naming a port the pod does not have is the worst failure available here: it
scrapes nothing, reports nothing, and every rule evaluates against no data
forever. If a future operator release renames the reporter's port, that is the
one line to change.

Every rule carries a `runbook_url` built from `alerts.runbookBaseUrl` plus the
anchor of the [runbook](../../docs/mirror-maker2-runbook.md) section that
resolves it, so a fork that vendors the runbook elsewhere repoints one value
instead of patching twelve rules. Two of the thresholds are new and worth
knowing what they distinguish:

| Threshold | The question it answers |
|---|---|
| `checkpointStaleMinutes` (15) | are checkpoints being **written** at all while data flows? Nothing written = consumers will have data on the target and no position to resume from |
| `offsetSyncStaleMinutes` (30) | are checkpoints **moving**? A checkpoint task re-emitting the same translated position keeps its write rate non-zero, so the rule above stays quiet — and the failover then resumes consumers at a position that stopped advancing hours ago |
| `rebalancesPerSecond` (0.1) | are the workers stable? Tasks do not replicate during a rebalance, so a storm reads as intermittent lag with no failed tasks |

Rules **about a mirror** are rendered once per `mirrors[]` entry and carry a
`source` label; rules about the **workers** (heap, rebalances) stay release-wide,
because there is one Connect cluster.

The board is organised as the questions an operator asks in an incident, in
order: is it up, is it lagging, is it erroring (six stats); which connector
lost a task; is data moving and how far behind; will consumers have a position
to resume from; what is being dropped, retried or dead-lettered; how much of
the error budget today has cost; and what the JVM and the rebalances under all
of it are doing. Two sections are collapsed because they answer a second
question — *where* the mirror is slow: the client path (the replication
consumer's fetch against the connector producers' send, latency and buffer)
and one row per source.

Grid positions are computed from a cursor rather than written down, so a
section can be inserted without renumbering the ones below it, and CI proves
the result: no two panels on the same cell, rows in order, and every panel
carrying a description and a query. A board whose panels quietly overlap is
one Grafana rearranges on its own.

The alerts ask "is something wrong now"; the recorded SLIs ask "how has it been
doing". The SLO's objective is deliberately the lag alert's own threshold, so
there is one number to argue about: `slo.target` is the fraction of time the
mirror must stay under it (0.99 leaves about seven hours a month), and the burn
alert fires when that budget is going `slo.burnRate` times faster than a
mirror sitting exactly at the target would spend it — over the last hour *and*
the last five minutes, so it has to be sustained and still happening.

Every series the rules and the dashboard read is checked against the exporter
rules that are meant to produce it. `scripts/check-metric-contract.sh
mirror-maker2` simulates the JMX exporter over the MBean catalogue in
`scripts/metric-contract/mirror-maker2.yaml` — the same first-match, `$N`,
lowercase-and-underscore procedure the agent runs, including the 1.x line's
rule that only a COUNTER keeps a `_total` suffix — and fails on any name no
rule can emit or any label that would carry Kafka's ObjectName quotes;
`ci-mirror-maker2.yml` runs it on every chart change, and its on-demand live
job scrapes a real worker and diffs the catalogue against what the worker
actually exposes. The first such scrape found that 0.4.0's alerts had never
been able to fire: every `connector` label was quoted, and the two `_total`
series they read were published without the suffix. A rule that installs and
never fires is the failure this exists for.

Replication lag is the number that decides whether a cutover is safe. When a
mirror is not replicating and you cannot see why:

```yaml
logging:
  type: external
  mirrorLevel: DEBUG
```

That logger prints the topic refresh, what matched the pattern and what did
not, and the offset-sync decisions.

## Tests

```bash
helm test mm2 -n kafka --logs
```

| Test pod | Asserts | Default |
|---|---|---|
| `…-test-ready` | CR reaches `Ready` | on |
| `…-test-connectors` | every mirror's source **and** checkpoint connector is `RUNNING` — or `PAUSED`/`STOPPED`, which is a cutover, not a fault — and no task is `FAILED` | on |
| `…-test-replication` | create the test topic on the source, produce N records, consume up to 2N from the target under the policy-correct name, compare **distinct** values (at-least-once means duplicates are expected, gaps are not) | off |
| `…-test-offsets` | a consumer group committed on the source appears, translated, on the target — that a position *exists* | off |
| `…-test-failover` | the assertion `test-offsets` stops short of: a group parked on the source, committed part-way, **resumes on the target at the right position** — the un-consumed remainder, not the whole topic and not nothing | off |

`test-ready` alone proves very little: `Ready` means the Connect workers are
up, which is true before a single record crosses and stays true while the
source connector stalls on a missing ACL. `test-connectors` reads
`.status.connectors[]`, which is where the operator writes the real answer.

The three data tests each run **one pod that loops over every mirror**, with one
failure counter and the source alias on every line — a pod per mirror would stop
at the first broken leg, and on a fan-in release "which leg" is the entire
diagnosis. `tests.replication.sourceAlias` narrows them to one mirror;
`tests.replication.topicByAlias` gives a leg its own topic when the mirror
selects an exact list rather than a pattern. `tests.failover` needs
`tests.replication.enabled` — it reuses that test's topic and wants its corpus
sitting in front of its own, because a translated position of 0 proves nothing
about resuming.

They need credentials and are SASL-over-plaintext only; a TLS
source or target makes the chart refuse to render them rather than emit a config
that cannot connect. They read the target with a named group
(`tests.replication.group`) because the console consumer's default random group
is one no ACL grant covers, and they carry the `kates.io/test-pod` label the
`kafka-cluster` broker NetworkPolicy admits. On a **read-only source** the
replication test's topic create is denied, which is not a fault — the topic has
to pre-exist. For the full end-to-end story — including a real
2.x or 3.x broker — use `scripts/test-mm2-migration.sh`:

```bash
make mm2-migration-test          # both legs
scripts/test-mm2-migration.sh --source-version 2.8.2 --keep
```

## Seeing what was replicated

The Topic Operator is unidirectional: topics MirrorMaker creates directly in
Kafka never become `KafkaTopic` resources, so `kubectl get kafkatopics` cannot
show a mirror's output. Ask the brokers instead:

```bash
make mm2-topics
```

## mTLS (turnkey, no external PKI)

`-f values-mtls.yaml` runs MM2 over the `kafka-cluster` mTLS listener (9093)
using Strimzi's built-in Clients CA — it provisions a `type: tls` KafkaUser
(`kates-mm2-tls`, certs issued and auto-rotated by the operator), trusts the
Cluster CA via `krafter-cluster-ca-cert`, and narrows egress to 9093.

```bash
helm install mm2 charts/mirror-maker2 -n kafka -f charts/mirror-maker2/values-mtls.yaml
```

Assumes MM2 is in the same namespace as the cluster (`kafka`); if elsewhere,
also enable `secretSync` for `kates-mm2-tls` + `krafter-cluster-ca-cert`. A real
**external** source needs its own `type: tls` KafkaUser issued by *that*
cluster's Clients CA, with its Cluster CA cert trusted here — for federated trust
across clusters, front Strimzi with cert-manager.

## Values reference

The blocks this chart adds on top of the `connect-cluster` worker surface:

| Block | Purpose | Notable keys |
|---|---|---|
| `target` | The cluster the Connect runtime runs against | `brokerCount` (enables the RF guard), `groupId`, `authentication`, **`exactlyOnce.enabled`** |
| `offsetSyncs` | Where MirrorMaker keeps `mm2-offset-syncs.*` | **`location: source\|target`** — chart-wide default; see [the credential contract](#the-credential-contract-read-this) |
| `mirrors[]` | One replication flow per source | `source.kafkaVersion`, `topicsPattern`, `topicsExcludePattern` (overrides the computed one), `sourceConnector.state`, **`readOnlySource`**, **`offsetSyncs.location`**, **`topics[]` / `groups[]`** (lists compiled to escaped patterns; the `*Pattern` still wins), **`source.egressCIDR`**, **`sourceConnector.version` / `checkpointConnector.version`** |
| `replicationPolicy` | Topic naming, and the fan-in / direction rails | `mode: default\|identity`, `class`, `separator`, `excludeInternalTopics`, **`allowIdentityFanIn`**, **`allowBidirectional`** |
| `compatibility` | The KIP-896 gate | `minSourceVersion`, `requireDeclaredVersion`, `enforce` |
| `preflight` | The pre-install probe | `enabled`, `failOnError`, `image`, `timeoutSeconds` |
| `image` / `imagePullPolicy` / `imagePullSecrets` | What runs, and where it comes from | `image` empty = Strimzi's own map for `version`; **`imagePullPolicy`** covers the pods *this chart* owns (preflight, secret-sync, tests) and not the workers, whose policy is operator-wide (`STRIMZI_IMAGE_PULL_POLICY`). A floating tag — `:latest`, or no tag at all — is refused at render time for every image key |
| `autoRestart` | Default for both connectors | `enabled`, `maxRestarts` |
| `cutover` | Chart-level connector state override (also what the failover/failback overlays set) | `enabled`, `sourceConnectorState`, `checkpointConnectorState` |
| `autoscaling` | HPA on the CR's scale subresource | `maxReplicas` (checked against `sum(tasksMax)`), **`targetMemoryUtilizationPercentage`**, **`allowIdleWorkers`** |
| `kafkaUser` | Optional target-side user | `create`, `authorization.mode: auto\|custom`, `topicGrants`, `groupGrants` |
| `secretSync` | Copy credentials across namespaces (post-install) | `secrets[]` (`name`, `fromNamespace`, optional `as`), **`watch`**, **`schedule`** |
| `networkPolicy` | Default-deny for the workers with explicit flows | `kafka.ports`, **`kafka.perSource`**, `strimziOperatorNamespace`, `operator.enabled`, `dns` |
| `metrics` / `podMonitors` / `alerts` / `dashboard` | Observability | **`metrics.type`**, **`metrics.allowList`**, **`podMonitors.scrape.<type>.{port,path}`**, `alerts.thresholds.*` (incl. **`checkpointStaleMinutes`**, **`offsetSyncStaleMinutes`**, **`rebalancesPerSecond`**), **`alerts.runbookBaseUrl`**, **`alerts.allowReporterMetrics`** |
| `rack` | Zone awareness | `enabled`, `topologyKey`, **`clientRackInitImage`**, **`clientRack`** |
| `jmxOptions` / `connectContainer` / `templateExtra` | The rest of the v1 surface | authenticated JMX; env + securityContext on the worker container; a raw passthrough merged into `spec.template` for the sub-objects this chart does not name |
| `logging` | Worker log levels | `type: inline\|external`, `mirrorLevel`, `rootLevel`, `extraLoggers` |
| `tests` | The data-moving Helm tests | `replication.topic`, **`replication.topicByAlias`**, `replication.group`, `replication.sourceAlias`, `offsetTranslation.group`, **`failover.{group,messages,commitAfter}`** |

**Bold** keys are new in 0.4.0.

Two notes on the last few rows. `rack.clientRack` sets `consumer.client.rack` on
every mirror's source consumer so the workers fetch from the closest **source**
replica — a real cost line on a cross-AZ or cross-region mirror, and it needs
the source brokers to be rack-aware. It is independent of `rack.enabled`, which
is about the *target* topology constraint;
`rack.clientRackInitImage` names the operator's topology-label init container
and is only read when `rack.enabled` is on.

Every key is commented in [`values.yaml`](values.yaml), and
[`values.schema.json`](values.schema.json) rejects typos in the blocks above.

## Overlays

| Overlay | For |
|---|---|
| `values-kind.yaml` | Single worker, RF1, small heap |
| `values-dev.yaml` | Same, with more headroom; helper images pulled `IfNotPresent`, and how to run a locally built worker on kind |
| `values-prod.yaml` | HA workers, RF3, zone spread, monitoring on; digest-pinning, `imagePullSecrets` and a registry-independent pull policy |
| `values-generic.yaml` | Unknown clusters (EKS/GKE/AKS) — no Prometheus CRDs assumed |
| `values-mtls.yaml` | mTLS over the Strimzi Clients CA |
| `values-migrate-2x.yaml` | Kafka 2.8.2 source: identity policy, preflight, offset sync |
| `values-migrate-3x.yaml` | Kafka 3.9.1 source, same shape, tighter intervals |
| `values-migrate-4x.yaml` | A Strimzi-operated 4.x source beside the target (a 4.1.x kept alive under its own operator, or an in-window 4.2.x): SCRAM-SHA-512 as `kates-mm2` on the source's 9092 listener, its credential copied by `secretSync` under another name (`as`). Pass it after `values-kind.yaml` if you use both — that overlay's `mirrors:` is a loopback. |
| `values-cutover.yaml` | The final cutover step — layer on a migration preset |
| `values-readonly-source.yaml` | A source you may only READ: `offsetSyncs.location: target` + `readOnlySource`, so no MirrorMaker write lands on the source. Layer on `values-kind.yaml` or a migration preset |
| `values-fan-in.yaml` | Two sources into one target, default policy, one topic list each — the documented consolidation shape |
| `values-failover.yaml` | The source is gone: source connector `stopped`, checkpoint connector `running`, `autoRestart` off. The DR sibling of `values-cutover.yaml` |
| `values-failback.yaml` | Mirroring back afterwards — a **separate release** in the opposite direction, rendered `stopped` so nobody replicates a direction they have not checked |
| `values-scale.yaml` | A large estate: task budget first, `refresh.topics.interval.seconds` second, producer batching third |

`values-readonly-source.yaml`, `values-fan-in.yaml`, `values-scale.yaml` and
`values-failback.yaml` each carry a `mirrors:` list, and Helm **replaces** lists
rather than merging them — so pass them *after* any overlay that also defines
`mirrors` (the `values-kind.yaml` / `values-migrate-*.yaml` loopbacks), never
before, and expect the later file's mirror to be the whole list.
`values-cutover.yaml` and `values-failover.yaml` are deliberately not like that:
they set only the chart-level `cutover` block, so they can be layered onto a
live release without discarding its bootstrap and credentials — which is the
entire reason `cutover` is a chart-level map instead of a per-mirror field.

## Other notes

**`keepOnDelete` (default true)** annotates the MM2 CR and target KafkaUser with
`helm.sh/resource-policy: keep`, so `helm uninstall` leaves replication running.
The flip side: a later `helm install` with the same release name fails on the
kept objects — `make mm2-undeploy` (release name via `MM2_RELEASE`, default
`mm2`) deletes them, or set `keepOnDelete: false`.

**Autoscaling:** `autoscaling.enabled=true` renders an HPA targeting the MM2 CR
(which exposes the scale subresource). `spec.replicas` is required by the v1
CRD, so on install it is seeded from `minReplicas`; on upgrade the chart reads
the live count back from the CR rather than resetting a scaled-out mirror to
its minimum. `maxReplicas` is checked against `sum(tasksMax)` — see
[Scaling means tasks, not replicas](#scaling-means-tasks-not-replicas).

**Credential rotation.** `secretSync` runs on install and upgrade, so a rotated
credential otherwise needs a `helm upgrade` to reach the workers.
`secretSync.watch: true` adds a CronJob on `secretSync.schedule` (default every
15 minutes) that re-copies them. It is opt-in on purpose: a CronJob that can
read Secrets in another namespace is a real standing grant and should be a
decision, not a default. If the cluster already runs kubernetes-reflector, use
that instead and leave this off.

**MirrorMaker 2 is at-least-once by default.** Duplicates after a retry or
failover are expected, and the tests assert **distinct** record presence rather
than exact counts for that reason. `target.exactlyOnce.enabled` is reachable —
see [Exactly-once](#exactly-once) — and it is off unless you turn it on.

**No heartbeat connector.** The v1 API dropped `heartbeatConnector`, so
`emit.heartbeats.enabled` is a no-op here and no `heartbeats` topic is created
or granted. Running one needs separate `KafkaConnect` + `KafkaConnector`
resources — a different chart's job. Lag comes from the source connector's own
`replication-latency-ms` instead.

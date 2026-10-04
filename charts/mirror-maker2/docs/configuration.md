# Configuration

Organised by what you are trying to achieve. The
[chart README](../README.md#values-reference) has the exhaustive list of every
key; this page is about which ones matter and why, in the order you meet them.

## The Four Decisions That Come First

Everything else is tuning. These four change what the mirror *is*, and three of
them are painful to change afterwards.

| Decision | Value | Changeable later? |
|---|---|---|
| What topology is this? | the overlay you start from | No — it determines the rest |
| Do topic names survive? | `replicationPolicy.mode` | No — changing it renames every replicated topic and leaves the old ones behind |
| May the mirror write to the source? | `offsetSyncs.location` / `readOnlySource` | No — flipping it restarts offset translation from empty |
| Do consumers resume, or restart? | `sync.group.offsets.enabled` | Yes, but discovering it late means a cutover where everything replays |

### Topology

Four jobs, one resource shape, and the configuration that is right for one is
silently wrong for the next. The
[topology table](../README.md#which-topology-is-this-decide-before-anything-else)
in the chart README is the decision; [Installation](installation.md) maps each
row to an overlay.

### Naming: `identity` or `default`

```yaml
replicationPolicy:
  mode: identity        # topics keep their names
  # mode: default       # topics land as <alias>.<topic>
```

- **Migration → `identity`.** The whole point is that consumers repoint and
  find what they know. Under `default` your `orders` topic arrives as
  `legacy.orders`, and you discover it after the cutover, with producers
  already moved.
- **DR and fan-in → `default`.** A record's origin stays visible, and two
  sources cannot collide. Under `identity`, two sources whose patterns overlap
  write the *same* target topics: interleaved records in one offset space, no
  error anywhere, and no way to separate them afterwards.
- **Loopback → `default`, always.** Under `identity` the mirror replicates its
  own output back to itself without end.

The chart refuses the identity fan-in and reverse-mirror cases it can prove.
`allowIdentityFanIn` and `allowBidirectional` exist to override that when you
have reasoned about it, and are not defaults for a reason.

### Whether the mirror writes to the source

By Kafka's default MirrorMaker records its offset mapping in
`mm2-offset-syncs.<target-alias>.internal` **on the source cluster**. So a
mirror is not a read-only operation on the source unless you make it one:

```yaml
mirrors:
  - source: { … }
    readOnlySource: true      # shorthand for offsetSyncs.location: target
```

Get this wrong and there is no error at install, no error on the resource, and
a `TopicAuthorizationException` inside the connector minutes later while
everything still reports `RUNNING`. [The credential
contract](../README.md#the-credential-contract-read-this) has the exact ACL sets
for both settings — it is the section to read before asking anyone to grant you
access.

`offset-syncs.topic.location` arrived in Kafka 3.0, but it is a MirrorMaker-side
setting: **the source's version does not gate it.** A read-only Kafka 2.8 source
is fully supported.

### Whether consumers resume

```yaml
mirrors:
  - checkpointConnector:
      config:
        sync.group.offsets.enabled: "true"
```

Without it the migration is data-only. Every consumer starts from its
`auto.offset.reset` on the target — usually the beginning, occasionally the end,
never where it was. Both migration presets turn it on.

## Connecting to Clusters

Both `target` and each `mirrors[].source` accept either an in-cluster reference
or an explicit address, and the explicit one wins when set:

```yaml
target:
  clusterName: krafter          # bootstrap FQDN is computed
  namespace: kafka

mirrors:
  - source:
      alias: legacy             # appears in metrics, and in topic names under `default`
      bootstrapServers: legacy-kafka.example.com:9092   # explicit wins
      kafkaVersion: "2.8.2"     # declare it — see below
```

The computed form is `<clusterName>-kafka-bootstrap.<namespace>.svc.<clusterDomain>`
on port 9092, or 9093 when `tls.enabled` is set.

**Always declare `source.kafkaVersion`.** It is what lets the chart check the
protocol floor before deploying instead of after, and with
`compatibility.requireDeclaredVersion: true` an undeclared source is refused
outright. A source nobody can name is a source nobody has checked.

## Sizing and Throughput

The single most common sizing mistake is scaling the wrong thing.

**Tasks do the work, not workers.** `tasksMax` on each connector sets the
parallelism; Connect runs at most `sum(tasksMax)` tasks across the cluster.
Adding worker replicas beyond that adds idle pods. Tasks map to partitions, so
`tasksMax` above the source's partition count buys nothing either.

```yaml
mirrors:
  - sourceConnector:
      tasksMax: 8               # start at the source's partition count
```

The order to tune, when throughput is short:

1. `tasksMax` up to the partition count
2. producer batching (`values-scale.yaml` has the settings)
3. `refresh.topics.interval.seconds` — cheaper discovery on a large estate
4. worker `replicas`, once the fetchers are saturated

The chart checks `autoscaling.maxReplicas` against `sum(tasksMax)` and refuses
an HPA that could only ever create idle workers.

## Durability

RF1 is not durable and never should be outside a laptop. Four factors have to
match the target, and they are separate settings:

```yaml
target:
  brokerCount: 3                # enables the chart's guard
  config:
    config.storage.replication.factor: 3
    offset.storage.replication.factor: 3
    status.storage.replication.factor: 3
mirrors:
  - sourceConnector:
      config:
        replication.factor: 3
        offset-syncs.topic.replication.factor: 3
    checkpointConnector:
      config:
        checkpoints.topic.replication.factor: 3
```

Setting `target.brokerCount` is what turns a runtime failure — the connector
cannot create its topics — into a render-time refusal.

## Images

The worker image is best left empty: Strimzi derives it from its own map for
`version`, which is the one source that cannot disagree with the operator
actually reconciling the resource.

```yaml
version: "4.3.1"       # the Kafka line the workers run
image: ""              # Strimzi's image for that version
imagePullPolicy: ""    # for THIS chart's pods, not the workers
```

Two things to know:

- `imagePullPolicy` covers the pods the chart owns — the pre-flight Job,
  secret-sync, the Helm tests — and **not** the MirrorMaker workers. Those pods
  come from the custom resource, which has an `image` field but no pull policy;
  that is operator-wide (`STRIMZI_IMAGE_PULL_POLICY`).
- A floating tag is refused at render time on every image key. `:latest`, or no
  tag at all, makes the Kafka client version depend on when a pod last
  restarted — which can move the workers across the protocol floor the
  compatibility rails exist to hold still. Use a version tag, or a `@sha256:`
  digest.

`values-dev.yaml` and `values-prod.yaml` carry the profile-appropriate settings,
including how to run a locally built worker on Kind and how to resolve a digest
for production.

## Networking

NetworkPolicy is default-deny with explicit flows, so every address the workers
need must be named:

```yaml
networkPolicy:
  enabled: true
  kafka:
    ports: [9092, 9093]         # narrow to [9093] once TLS is on
  dns:                          # pin DNS egress on strict clusters
    namespaceSelector:
      kubernetes.io/metadata.name: kube-system
    podSelector:
      k8s-app: kube-dns
```

For a source outside the cluster, `mirrors[].source.egressCIDR` opens the
specific range rather than the internet. `kafka.perSource` narrows egress per
mirror on a fan-in.

## Observability

Turn this on **before** the first record moves, not after the first problem.
Replication lag is the number a cutover decision rests on, and you want its
history rather than a spot reading.

```yaml
metrics:
  enabled: true
podMonitors:
  enabled: true
alerts:
  enabled: true
```

The alerts that matter during a migration are checkpoint staleness and
offset-sync staleness — both are about translation falling behind, which is the
failure that lets a cutover look fine and resume consumers in the wrong place.
Thresholds are under `alerts.thresholds.*`.

For a mirror that stays (DR, fan-in), the recorded SLIs are the better
long-term read: `mm2:replication_latency_ms:max`, `mm2:checkpoint_latency_ms:max`,
`mm2:records_replicated:rate5m` and `mm2:tasks_running:ratio`, one series per
source, and `MirrorMaker2ReplicationSLOBurning` when the mirror spends more of
its time over `thresholds.replicationLatencyMs` than `alerts.slo.target`
allows. Set the target to the promise you are actually making; the default
99% is a starting point, not a recommendation.

## What the Chart Refuses

Render-time rails, each for a failure that would otherwise appear minutes later
as "`RUNNING`, no data". The full list is in
[the chart README](../README.md#the-rails--what-the-chart-refuses-and-why); the
ones you are most likely to meet:

- a source below the Kafka 2.1 protocol floor, or one that does not declare its
  version
- an identity-policy loopback, or an identity fan-in whose sources overlap
- a reverse mirror while the forward one is live under identity
- replication factors above `target.brokerCount`
- an HPA whose ceiling exceeds `sum(tasksMax)`
- an image with a floating tag
- offset-syncs settings that disagree between the two connectors

Every one of them is something the chart can prove from the values alone. None
of them are style preferences.

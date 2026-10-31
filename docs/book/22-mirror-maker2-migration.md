# Cross-Cluster Replication and Migration

[MirrorMaker 2](appendix-a-glossary.md#gl-mirrormaker-2) is [Kafka Connect](appendix-a-glossary.md#gl-kafka-connect) wearing a specific hat. A `KafkaMirrorMaker2` custom resource *is* a Connect cluster, running two built-in connectors — `MirrorSourceConnector` and `MirrorCheckpointConnector` — whose job is to copy records and consumer offsets from one Kafka cluster to another [@kip382]. Everything you know about operating Connect applies: workers, tasks, rebalances, internal topics, the REST layer.

One thing does not carry over, and it drives the whole design: Connect talks to one cluster, and MirrorMaker 2 talks to two. Every difficulty in this chapter descends from that.

By the end you can tell which of the three uses a mirror serves and write the chart values for a migration. You can also prove a mirror moves data rather than merely reporting Ready, and run the cutover in the one order that keeps consumer positions.

## The Model

Strimzi's v1 API expresses the two clusters as two fields:

```yaml
spec:
  target:                         # where the Connect runtime lives
    alias: target
    bootstrapServers: krafter-kafka-bootstrap.kafka.svc:9092
    groupId: mm2
    configStorageTopic: mm2-configs
    offsetStorageTopic: mm2-offsets
    statusStorageTopic: mm2-status
  mirrors:
    - source:                     # where records are read from
        alias: legacy
        bootstrapServers: kafka-old.example.com:9092
      topicsPattern: "orders\\..*"
      groupsPattern: ".*"
      sourceConnector: { ... }
      checkpointConnector: { ... }
```

The target owns the Connect internal topics and is where the workers' `AdminClient` writes. Each source carries its own bootstrap, TLS and credentials, because the two clusters share nothing — not a CA, not a principal, often not an operator or an owner.

::: {.callout-note title="No heartbeat connector in the v1 API"}
There is no `heartbeatConnector` in the v1 API, and no heartbeats: `emit.heartbeats.enabled` on the source connector is a no-op. Replication lag comes from the source connector's own `replication-latency-ms` metric instead.
:::

## Three Uses, Three Different Configurations

The same CR serves three purposes, and the configuration that is right for one is wrong for the others.

| Use | Topic naming | Offset translation | Lifetime |
|:----|:-------------|:-------------------|:---------|
| Disaster recovery | Prefixed (`legacy.orders`) — you need to know the origin | Essential, for failover | Permanent |
| Aggregation | Prefixed — many sources, one target | Rarely | Permanent |
| Migration | Preserved (`orders`) — consumers repoint and find what they know | Essential, for cutover | Weeks |

The naming difference is the replication policy. `DefaultReplicationPolicy` prefixes; `IdentityReplicationPolicy` does not. In the chart that is one key, `replicationPolicy.mode: identity` or `default`, and `default` is the chart's own default; a migration preset sets identity for you.

## Configuring the Chart

The `mirror-maker2` chart renders the `KafkaMirrorMaker2` resource from its values. A migration's values name the policy, the source and the source's Kafka version, which the chart checks before anything deploys:

```yaml
replicationPolicy:
  mode: identity
compatibility:
  requireDeclaredVersion: true
mirrors:
  - source:
      alias: legacy
      bootstrapServers: kafka-old.example.com:9092
      kafkaVersion: "2.8.2"
    topicsPattern: "orders\\..*"
```

You rarely write those values from scratch. The chart ships presets, values overlays that each make one use's decisions for you:

| Preset | Use | What it sets |
|:-------------------------|:-----------------|:---------------------------------------------------------|
| `values-migrate-2x.yaml` | Migrate from a 2.x source | Identity policy, the declared version, pre-flight on, group offset sync |
| `values-migrate-3x.yaml` | Migrate from a 3.x source | The same decisions for a KRaft source |
| `values-migrate-4x.yaml` | Migrate from a Strimzi-operated 4.x source | The same, with the source's own SCRAM user |
| `values-failover.yaml` | Fail over to the target | Source connector `stopped`, checkpoint connector `running` for translated positions |
| `values-failback.yaml` | Mirror back after a failover | A separate release in the opposite direction |
| `values-fan-in.yaml` | Aggregate several sources | Default policy, one alias per source |
| `values-readonly-source.yaml` | A source you may only read | Every MirrorMaker 2 write lands on the target |
| `values-cutover.yaml` | The last step of a migration | Source connector `stopped`, checkpoint connector `running` |

Apply a migration preset first and the cutover overlay last; the cutover block wins over any per-connector state.

## Verifying a Mirror

Three questions, in increasing order of how much they tell you:

| Question | How | What it proves |
|:---------------------|:--------------------------------------|:-----------------|
| Are the workers up? | The CR's `Ready` condition | Almost nothing |
| Are the connectors running? | `.status.connectors[].connector.state` | The connectors started |
| Is data arriving? | End offsets on the target, read twice | The mirror works |

A test suite that stops at the first question reports success on a dead mirror. The chart's Helm tests span all three. CR readiness and connector state run on every `helm test`; the produce-and-consume round trip and the [offset-translation](appendix-a-glossary.md#gl-offset-translation) check are off by default because they need credentials. `kates migrate run` asks the third question against a real legacy broker and diffs the distinct records read back rather than counting lines.

Two helpers answer the third question by hand. `kates migrate target offsets <topic>` sums a topic's end offsets with whichever offset tool the target's Kafka line ships; run it twice, and a mirror that moves data shows a larger sum the second time. `kates migrate target topics` lists what the mirror created on the target, which `kubectl get kafkatopics` never shows, because the Topic Operator is unidirectional.

## Metrics That Answer Operational Questions

The workers' PodMonitor exports every Connect series, and five of them answer the questions a cutover asks. Each name below starts with `kafka_connect_`; read them in this order:

| Metric | Question |
|:----------------------------------------------|:------------------|
| `source_task_metrics_source_record_write_total` | Is anything moving at all? |
| `mirror_source_connector_replication_latency_ms_max` | Is it safe to cut over? |
| `mirror_source_connector_record_age_ms_max` | How far behind are the fetchers? |
| `worker_metrics_connector_failed_task_count` | Is something broken outright? |
| `worker_rebalance_metrics_completed_rebalances_total` | Is it stable? |

Replication latency is the one a cutover decision rests on, and you want its history rather than a spot reading — which means turning metrics on before the first record moves, not after something looks wrong.

::: {.callout-tip title="Sawtooth lag means rebalances, not a slow source"}
Tasks do not replicate during a rebalance, so a rebalance storm presents as intermittent lag with no failed tasks. If lag is sawtoothing, look at the rebalance rate before you look at the source.
:::

## Why Migration Is the Hard Case

A disaster-recovery mirror runs forever and nobody watches it closely. A migration has a deadline, a cutover, and a rollback that stops being possible partway through. Four things make it sharp.

### The Protocol Floor

Kafka 4.0 removed the client protocol versions older than Kafka 2.1, KIP-896 [@kip896], so a 4.x MirrorMaker 2 reads only from a source broker running 2.1 or newer: the [protocol floor](appendix-a-glossary.md#gl-protocol-floor). Below that the broker answers `UNSUPPORTED_VERSION`. [The Cliff, and Where It Is](23-legacy-source-migration.md#the-cliff-and-where-it-is) covers the two-hop path for anything older.

::: {.callout-warning title="A dead mirror still reports Ready"}
The failure is silent from the outside. The `KafkaMirrorMaker2` reports `Ready`, because `Ready` means the Connect workers started. Only the connector status and the target's record counts show that nothing is crossing.
:::

The `mirror-maker2` chart turns this into a render-time failure. Declare `mirrors[].source.kafkaVersion` and a source below the floor fails `helm install` in seconds, naming the alias and the way out; a source that declares no version passes unchecked unless `compatibility.requireDeclaredVersion` is true. With `preflight.enabled: true`, which the migration presets set, a pre-install Job goes further: it performs the `ApiVersions` handshake against the real source with the workers' own client, so a pass is evidence rather than an assumption.

### Identity Mode Makes the Mirror Self-Matching

Preserve topic names and the mirror becomes eligible to read its own output — `heartbeats`, `checkpoints`, the offset-syncs topic, the Connect internal topics. MirrorMaker's stock exclusions do not cover them, because under the default policy those topics are already renamed out of the way. The symptom is a topic count that grows without bound.

The chart computes an exclusion set in both modes, and identity mode adds MirrorMaker 2's own topic names to it. Read the computed exclusion as five groups, with `legacy` as the alias:

| Pattern | What it excludes |
|:--------|:-----------------|
| `.*[\-\.]internal`, `.*\.replica`, `__.*` | MirrorMaker's own defaults |
| `<groupId>-.*` | The Connect internal topics |
| `legacy\..*` | Anything already carrying the source's prefix, which stops a loopback from re-mirroring `legacy.orders` as `legacy.legacy.orders` |
| `mm2-.*` | The chart's internal-topic names |
| `heartbeats`, `checkpoints`, `.*\.heartbeats`, `.*\.checkpoints\.internal` | MirrorMaker 2's own topics, added in identity mode |

### Offset Translation Is the Half People Skip

`MirrorSourceConnector` moves records. `MirrorCheckpointConnector` moves *positions* — it maps each source [consumer group](appendix-a-glossary.md#gl-consumer-group)'s committed offsets onto the equivalent target offsets, through the `offset-syncs` topic, and writes them to the target's `__consumer_offsets` when `sync.group.offsets.enabled` is true [@kip545]. MirrorMaker 2 ships with it off; the chart turns it on for every mirror. If you write the CR yourself, set it on the checkpoint connector or no group position reaches the target.

Without it a migration is data-only: every record is on the target, and no consumer knows where to resume, so each one replays from the beginning or skips to the end.

Translation is approximate by design: positions map at record granularity and the mapping lags the data, so a consumer moved onto a translated offset may re-read a few records. Plan for duplicates.

### The Cutover Is a Sequence

Cutting over without losing records or consumer positions requires draining the source and stopping connectors in a strict order. Cut over in this order:

1. Wait until replication lag reaches zero.
2. Stop the producers on the source.
3. Confirm the source end offsets are static. If they still move, a producer is alive: go back to step 2.
4. Let the mirror drain for one checkpoint interval (`emit.checkpoints.interval.seconds`, which the migration presets set to 20 seconds for 2.x and 10 for 3.x and 4.x), so the last positions are translated.
5. Stop the source connector and keep the checkpoint connector running.
6. Move consumers onto translated offsets, one group at a time.
7. Move producers to the target.
8. Retire the mirror.

Two steps are the ones that go wrong.

First, stop the source connector and keep the checkpoint connector running. Consumers migrate one at a time, and each one that has not moved yet still needs its position translated. Stopping both together leaves every record on the target and no consumer able to find its place.

Second, set the source connector to `stopped`, not `paused`. `stopped` releases the connector's tasks, so a worker restart cannot resume the mirror after producers have moved — which would write old records *behind* the ones the new producers are appending.

Both are expressed in the chart's `cutover` block, applied as a values overlay on top of whatever migration preset is running.

## Testing a Migration Locally

The pinned Strimzi operator supports only the Kafka lines its release lists, all of them 4.x (see the [Version & Compatibility Matrix](appendix-d-versions.md)), and refuses a `Kafka` resource whose `spec.version` is outside that set. So a 2.x or 3.x cluster cannot be expressed as a `Kafka` custom resource at all. The `legacy-kafka` chart fills that gap: plain StatefulSets running the last 2.x line on ZooKeeper, or the last 3.x line on KRaft, in their own namespace, purely to be replicated from. Which 2.x and 3.x lines those are is pinned in that chart's overlays, and `kates migrate pairs` prints the pairs this cluster can actually stand up.

```bash
kates migrate pairs              # the source-to-target pairs available here
kates migrate run --from 3.9.1   # the 3.x leg
kates migrate run --from 2.8.2   # the 2.x leg
```

::: {.callout-tip title="Try it: Which legacy pairs can panda stand up?"}
On `panda`, the book's three-node Kind cluster, run `kates migrate pairs`, then `kates migrate run --from 3.9.1 --yes`. In the report, find the `record content` and `offset translation` rows. They say whether the records and a consumer group's position crossed, which the CR's `Ready` never does.
:::

Each run stands up the legacy broker, produces a known corpus, commits a consumer offset, mirrors it, reads the records back off the 4.x cluster, verifies offset translation, and rehearses the cutover — reporting every assertion in a table.

::: {.callout-caution title="legacy-kafka is a lab fixture"}
The `legacy-kafka` chart is a lab fixture: single broker, replication factor 1, `emptyDir`, no TLS. It proves protocol and topology compatibility, not durability or throughput. Never point production traffic at it.
:::

## Limits

The chart makes a mirror safe to run, and three things stay outside what it gives you.

MirrorMaker 2 is at-least-once, not exactly-once across clusters: expect duplicates after a retry or a failover.

Nothing replicates backwards. Once producers write to the target, a rollback is a data merge, and the decision to accept that belongs before the cutover.

The chart does not provision the source. Its principal, ACLs and reachability are granted elsewhere, and a missing source ACL shows as a connector that stays `RUNNING` and replicates nothing.

## Further Reading {#where-to-go-next}

The step-by-step procedures live outside the book, next to the code they drive. [Tutorial 10: Installing and Setting Up MirrorMaker 2](../tutorials/10-mirror-maker2-installation.md) covers setup. [Tutorial 11: Migrating Kafka 2.x to 4.x with MirrorMaker 2](../tutorials/11-migrating-kafka-2x-to-4x.md) and [Tutorial 12: Migrating Kafka 3.x to 4.x with MirrorMaker 2](../tutorials/12-migrating-kafka-3x-to-4x.md) walk the two migration paths. [MirrorMaker 2 Runbook — Cutover, Rollback, Troubleshooting](../mirror-maker2-runbook.md) holds the cutover checklist and the failure table, and [mirror-maker2](../../charts/mirror-maker2/README.md) is the chart reference.

## Summary

- MirrorMaker 2 is a Connect cluster that talks to two Kafka clusters; the target owns the internal topics and each source brings its own credentials.
- Identity policy keeps topic names for a migration and makes the mirror eligible to read its own output, so the exclusion set matters.
- The checkpoint connector translates consumer positions; without it a migration moves data and strands every consumer group.
- Cut over by stopping the source connector, keeping the checkpoint connector, and moving one consumer group at a time.

That covers the mirror. When the source is old, the other half of the problem is the source itself: [Migrating a Legacy Kafka Source](23-legacy-source-migration.md) explains what its age changes around the mirror, and it is the chapter to read next.

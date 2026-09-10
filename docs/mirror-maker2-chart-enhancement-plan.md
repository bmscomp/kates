# Plan — Enhancing the `mirror-maker2` Chart

Branch: `feat/mirror-maker2-chart-0.4` (from `main` once the cross-version branch merges). Target: `charts/mirror-maker2` **0.3.0 → 0.4.0**.

**Purpose.** The chart today does one shape very well: *one source, mirrored into the platform's own target, by a principal that can write to both ends*. That is the migration lab, and it works. The shapes it cannot express are the ones real deployments arrive with — a source you may only **read**, **several** sources into one target, a mirror that must be **failed over and back**, and a target that needs the delivery and scaling guarantees production asks about. This plan closes those, using only what the Strimzi `v1` API already offers, and fixes two things the current templates get subtly wrong.

> **Status: IMPLEMENTED** in `charts/mirror-maker2` 0.4.0 (branch `feat/mirror-maker2-cross-version`). All six phases landed; §2 is the study it rested on, kept because it is the reasoning behind each rail.
>
> Two additions the work itself turned up, beyond what §3 planned:
>
> - **A loopback under an identity policy is refused.** The shipped default has source bootstrap == target bootstrap, which is safe under the default policy (the replicated topic is renamed and then excluded) and is an infinite self-replication under identity, where the name is preserved. Nothing in MirrorMaker's own exclusions covers a plain data topic there.
> - **A source that names neither `clusterName` nor `bootstrapServers` is refused.** The bootstrap helper defaulted an unset source to `krafter`/`kafka` — the target — so `values-failback.yaml` shipped a mirror pointed at itself until an operator remembered two `--set`s. It now fails at render with the flag to pass.
>
> Deliberately deferred, with reasons: the Strimzi Metrics Reporter's alert exprs (the reporter's metric names could not be established from the vendored chart, so `alerts.enabled` with that type is refused until someone verifies them — `alerts.allowReporterMetrics` overrides), and the reporter's scrape port, which is a values key with a documented inferred default. Both are one line each once a kind run confirms them.

---

## 1. Why, and what "enhance" means here

`charts/mirror-maker2` is 0.3.0 and already carries the parts that are usually missing: computed internal-topic exclusions for both replication policies, a render-time KIP-896 floor, a pre-install probe that says *why* a source is unreachable, connector lifecycle (`state`, `autoRestart`, `listOffsets`, `alterOffsets`), a cutover overlay, alerts, a dashboard, four Helm tests, and a credential-sync hook. The MirrorMaker 2 work and the migration lab exercised all of it.

What it has not been asked to do is anything other than **that one topology**:

| The shape someone actually has | What the chart does today |
|:---|:---|
| A source cluster you may read but **not write** | Fails: MirrorMaker writes its offset-syncs topic to the *source* by default (§2.3) |
| **Several** sources into one target (consolidation, fan-in) | Renders — the `mirrors` list is honoured — but the target ACLs, the NetworkPolicy egress, the alerts and the tests are written for one |
| Mirror **back** after a failover (failback) | The primitives are exposed (`alterOffsets`, `listOffsets`), the procedure is not |
| "Can you guarantee no duplicates?" | No: exactly-once is not reachable through any value |
| "It is behind, add workers" | An HPA exists and scaling replicas past `tasksMax` adds idle pods (§2.3) |
| An OAuth / custom-auth source | Only `scram-*`, `plain` and `tls` are rendered |

Each row below becomes a value, a rail, or a preset — never a new abstraction. The chart's contract stays: *the `Kafka` CRs are the deployment contract, the chart parameterises them, and the CLI drives the chart.*

---

## 2. The study

### 2.1 What the chart already covers (and this plan does not touch)

Read from the templates, so the plan does not re-plan solved problems: the `mirrors[]` list with per-source alias/bootstrap/TLS/auth/config; `topicsPattern`/`groupsPattern` plus a **computed** `topicsExcludePattern` that stops a loopback re-mirroring its own output under either replication policy; `compatibility.minSourceVersion` enforced with `semverCompare` at render time; the preflight hook Job with its `PROTOCOL → DNS → TLS → AUTH → LISTENER → NETWORK` verdicts; `cutover` as a chart-level block (Helm replaces lists, so a per-mirror overlay would not merge); `autoRestart`; external and inline logging; JMX-exporter metrics, PodMonitors, `PrometheusRule` alerts and a Grafana dashboard; `secretSync` with `as:` renaming; `keepOnDelete`; the auto-derived target `KafkaUser`; NetworkPolicy including operator ingress on 8083; and Helm tests for connectors, an end-to-end replication round trip, and offset translation.

### 2.2 The v1 API surface the chart does not expose

`spec.*` in the pinned CRD is: `clientRackInitImage image jmxOptions jvmOptions livenessProbe logging metricsConfig mirrors rack readinessProbe replicas resources target template tracing version`. Against the chart:

| Not exposed | What it is for | Worth it? |
|:---|:---|:---|
| `metricsConfig.type: strimziMetricsReporter` | the native metrics path, beside `jmxPrometheusExporter` | yes — §3.6 |
| `jmxOptions` | authenticated JMX on the workers | yes, small — §3.8 |
| `clientRackInitImage` | the init container that reads the node label for `rack` | yes, small — the chart exposes `rack` but not the image that makes it work on a custom registry |
| `template.connectContainer` | env vars into the worker container (proxy settings, `KAFKA_OPTS`, JVM agents) | yes — §3.8 |
| `template.apiService`, `headlessService`, `podSet`, `serviceAccount`, `clusterRoleBinding`, `jmxSecret` | annotations/labels on the rest of the operands | partly — one `template.extra` passthrough, not eleven knobs |
| `template.build*`, `buildConfig` | Connect image builds — MM2 does not build plugins | no |
| `mirrors[].sourceConnector.version` / `checkpointConnector.version` | per-connector plugin version (0.49+) | yes, one line — §3.8 |

`spec.mirrors[].source.config` **is** rendered (verified in `kafka-mirror-maker2.yaml`), so per-source consumer settings already have a home. That matters for §3.1 and §3.4.

### 2.3 Two things the templates get wrong today

**The offset-syncs topic is on the source, and the ACL for it is on the target.** MirrorMaker writes `mm2-offset-syncs.<target-alias>.internal` to the **source** cluster by default; `offset-syncs.topic.location` (Kafka 3.0, [KIP-716](https://cwiki.apache.org/confluence/display/KAFKA/KIP-716:+Allow+configuring+the+location+of+the+offset-syncs+topic+with+MirrorMaker2)) is what moves it to the target, and its stated purpose is exactly *"use MirrorMaker 2 even if you have only read access to the source cluster"*. The chart never sets it — while `templates/kafka-user.yaml` grants the **target** user `Read, Write, Create, Describe` on the `mm2-offset-syncs.` prefix. So today:

- with the default location, that target grant is **inert** — the topic is not there — and the *source* principal silently needs `Create` + `Write`, which the README's source-side contract ("READ + DESCRIBE") does not ask for;
- a genuinely read-only source cannot be mirrored at all, and the failure surfaces late, as a `TopicAuthorizationException` inside the connector rather than at install.

The same is true of the `heartbeats` literal grant: v1 removed `heartbeatConnector`, so nothing in this chart creates that topic. Both grants should follow the setting rather than be asserted unconditionally.

**The HPA scales the thing that is not the bottleneck.** `templates/hpa.yaml` scales `replicas` on CPU and memory. MirrorMaker's parallelism is `tasksMax` **per connector**: Connect distributes at most `sum(tasksMax)` tasks across the workers, so an HPA that takes replicas to 6 while `sourceConnector.tasksMax: 3` and `checkpointConnector.tasksMax: 1` leaves **two workers with no work** — and the CPU average that triggered the scale-up falls, which is read as success. Autoscaling MM2 is only meaningful when `tasksMax` is raised with it, and `tasksMax` above the source's partition count is itself inert.

### 2.4 What the migration lab will ask for next

`kates migrate` (see [the CLI migration plan](mirror-maker2-cli-migration-plan.md) and [§3.9 of the multi-version plan](kafka-multi-version-deploy-plan.md)) drives this chart. Its first version writes one mirror from one legacy source. Its next steps need, from the chart: a read-only source path (§3.1) because that is what a customer's real cluster looks like; failback (§3.3) because a rehearsed migration is one you can undo; and per-source everything (§3.2) because `--from` will eventually accept more than one cluster.

---

## 3. Design

### 3.1 Read-only sources — the first-class fix

```yaml
mirrors:
  - source: { … }
    offsetSyncs:
      # source (Kafka's default) | target (KIP-716 — the source needs no write)
      location: source
```

- Rendered into **both** `sourceConnector.config` and `checkpointConnector.config` as `offset-syncs.topic.location`, because Kafka requires the two to agree; a mismatch is a render-time failure rather than a runtime one.
- `kafkaUser` auto-mode follows it: the `mm2-offset-syncs.` grant is emitted **only** when `location: target`, and when it is `source` the NOTES and README say plainly which grants the *source* principal needs (`Create`, `Write`, `Describe` on `mm2-offset-syncs.*`), instead of the current "READ + DESCRIBE" that is true only for `target`.
- A `readOnlySource: true` shorthand on the mirror sets `location: target` and refuses, at render time, any setting that would still write to the source (`sync.topic.acls.enabled: true`, `sync.topic.configs.enabled: true` — both write to the *target*, so they stay legal; the check is about `offset-syncs` and any future source-writing option).
- The preflight probe gains one verdict, `WRITE`: with `location: source` it attempts a metadata-only describe of the offset-syncs topic name and reports whether the principal could create it — the one thing that distinguishes "your credentials work" from "your credentials work and this will actually run".
- New overlay `values-readonly-source.yaml`, and the `heartbeats` grant drops from auto-mode (v1 has no heartbeat connector) with a comment saying why.

### 3.2 Fan-in — several sources, one target

The `mirrors` list already renders N entries; everything *around* it assumes one. Per-source, this becomes true of:

- **ACLs** — auto-mode already ranges over `mirrors` for the `<alias>.` prefixes; it must also range for per-source `topicGrants` (identity mode with two sources needs two prefix sets) and emit one `mm2-offset-syncs` grant per source when that source uses `location: target`.
- **NetworkPolicy** — egress today is a fixed set; it becomes one `to:` entry per source, derived from `clusterName`/`namespace` (in-cluster) or from an explicit `sources[].egress` CIDR + port (external), so a second source does not need the policy disabled wholesale.
- **secretSync** — already a list with `as:`; the README gains the two-source example, since both sources may ship a Secret of the same name.
- **Alerts and the dashboard** — every rule and panel keys on `alias` so a stuck source is named rather than averaged away; the "no records" rule becomes per-source.
- **The replication test** — one round trip per source, each with its own topic, so a fan-in release proves each leg.
- **Render-time rail** — duplicate aliases, and any two sources whose `topicsPattern` can match the same topic under `IdentityReplicationPolicy`, fail with the reason (two mirrors writing one target topic name is corruption, not a configuration).

New overlay `values-fan-in.yaml` with two sources, as the documented shape.

### 3.3 Direction — DR posture, failover and failback

The chart can already stop and start connectors; what it lacks is the *shape* of the reverse direction.

- **`values-failover.yaml`** — the DR sibling of `values-cutover.yaml`: source connector stopped, checkpoint connector running, and the `alterOffsets` block that seeds the consumer groups on the target from the translated checkpoints, so applications restart where they left off.
- **`values-failback.yaml`** — the mirror re-pointed the other way (old target becomes source), with the warning that the two directions must never run at once under `IdentityReplicationPolicy`, and a render-time refusal when a release's target alias equals another live mirror's source alias in the same namespace (checked via `lookup` for existing `KafkaMirrorMaker2` CRs; a dry render skips it, as `lookup` returns empty).
- **Active-active** is documented as *two releases, one per direction, `DefaultReplicationPolicy` only* — with the topic-loop explanation — rather than pretended to be one CR. The chart's job here is the guard-rail and the words, not a new abstraction.
- `NOTES.txt` prints the three commands for the current posture (`cutover`, `failover`, `failback`), so the release itself says what to run next.

### 3.4 Delivery guarantees

```yaml
target:
  exactlyOnce:
    enabled: false      # sets exactly.once.source.support on the worker
```

MirrorSourceConnector implements the KIP-618 APIs, so Connect's exactly-once source support applies; it requires the worker property **and** `consumer.isolation.level=read_committed` on the source consumer ([KAFKA-10339](https://issues.apache.org/jira/browse/KAFKA-10339), [KIP-656](https://cwiki.apache.org/confluence/spaces/KAFKA/pages/158870065/KIP-656+MirrorMaker2+Exactly-once+Semantics)). The chart sets both from the one flag, adds the `Write`/`IdempotentWrite` and transactional-id ACLs auto-mode then needs, and states the cost in the values comment: throughput drops, and the source must be a version that supports transactional reads. Off by default, because at-least-once with the chart's dedupe-aware tests is what a migration wants.

### 3.5 Scaling that means something

- `autoscaling` gains `tasksMax`-awareness: rendering fails when `autoscaling.maxReplicas` exceeds `sum(tasksMax)` across all mirrors, with the arithmetic in the message. A cluster cannot use workers it has no tasks for, and an autoscaler that adds them hides the real bottleneck.
- `sourceConnector.tasksMax` gains a comment stating the real ceiling — the source's partition count — and the NOTES print `sum(tasksMax)` beside `replicas` so the ratio is visible at install.
- A `values-scale.yaml` profile for large estates: higher `tasksMax`, `refresh.topics.interval.seconds` raised from 60 (a 10 000-partition source rescanned every minute is a measurable load on both ends), bigger `jvmOptions`, and `producer.linger.ms`/`batch.size` set for throughput rather than latency.
- KEDA-on-lag is named as the honest answer for demand-driven scaling and left out of the chart: it needs a ScaledObject and a metrics source the platform does not require, and a wrong autoscaler is worse than none.

### 3.6 Observability

- **`metricsConfig.type: strimziMetricsReporter`** as an alternative to the JMX exporter (`metrics.type: jmxPrometheusExporter | strimziMetricsReporter`), with the allow-list config, since it is the operator's native path and drops the exporter sidecar's overhead.
- **Alerts** gain: replication lag over a threshold (`kafka_connect_mirror_source_connector_replication_latency_ms` percentile), offset-sync staleness (checkpoints not advancing while records flow — the failure that makes a failover lose position silently), worker rebalance storms, and per-source variants of the existing rules. Every rule gets a `runbook_url` pointing at the section of `docs/mirror-maker2-runbook.md` that resolves it.
- **Dashboard**: a per-source row, a lag panel, and a "task states" table, so a partially-failed connector is visible without `kubectl`.

### 3.7 Security

- `authentication.type: custom` (OAuth and anything else Strimzi supports through a class) and `oauth`-shaped guidance, rendered through the existing helper so a source or target can use it.
- `jmxOptions.authentication` for the JMX port.
- Secret **rotation**: `secretSync` runs on install and upgrade, so a rotated credential needs an upgrade. The chart documents that plainly and, when `secretSync.watch: true`, adds a small CronJob that re-copies on a schedule — opt-in, because a CronJob that can read Secrets across namespaces is a real grant and should be a choice.
- The auto-mode ACLs get an audit comment per grant explaining *which* connector setting needs it, so the least-privilege set can be trimmed by anyone reading it.

### 3.8 The rest of the v1 surface

One line each, no design: `jmxOptions`, `clientRackInitImage`, `template.connectContainer` (env + securityContext), `mirrors[].*Connector.version`, and `rack` gaining the `consumer.client.rack` wiring so replicas fetch from the closest source replica (a real cost line on cross-AZ mirrors). Plus `template.extra` — a raw passthrough merged into `spec.template` — so the eleven remaining sub-objects need no eleven values.

### 3.9 Tests

- A **fan-in** test (two sources, two topics, one target) and a **read-only-source** test (offset-syncs on the target, source principal without write).
- A **failover** test: stop the source connector, prove the checkpointed group offsets let a consumer resume on the target at the right position — the assertion the offset-translation test stops short of.
- The existing replication test gains a per-source loop rather than a single hard-coded topic.
- `helm unittest`-style negative renders join the CI `chart` job for each new rail: duplicate alias, identity + overlapping patterns, `maxReplicas > sum(tasksMax)`, `offsetSyncs.location` mismatch between connectors, EOS without `read_committed`.

### 3.10 Ergonomics

- `topics:` / `groups:` as **lists** compiled into the regex patterns (`["orders","payments"]` → `orders|payments`, escaped), because hand-escaping Java regex in YAML is where these releases go wrong; the patterns stay available and win when both are set.
- `NOTES.txt` prints: the posture, `sum(tasksMax)` vs `replicas`, the offset-syncs location and what that means for the source principal, and the three next commands.
- README: a decision table for the four topologies (loopback, migration, fan-in, DR), and the source-side ACL contract corrected per §3.1.

---

## 4. Validation and gates

Everything here is render-time; the chart keeps its "fail in seconds, name the way out" contract:

| Rail | Fails when |
|:---|:---|
| KIP-896 floor (exists) | a source below `compatibility.minSourceVersion` |
| RF vs broker count (exists) | replication factor above `target.brokerCount` |
| identity + no `topicGrants` (exists) | auto ACLs cannot be derived |
| duplicate alias | two mirrors share an alias |
| identity overlap | two mirrors' patterns can match one topic name |
| tasks vs replicas | `autoscaling.maxReplicas > sum(tasksMax)` |
| offset-syncs agreement | the two connectors disagree on `offset-syncs.topic.location` |
| EOS coherence | `exactlyOnce.enabled` without `read_committed` on the source consumer |
| reverse-direction clash | a live CR mirrors in the opposite direction under identity (`lookup`) |

CI's `chart` job gains one negative render per rail, and `kubeconform -strict` runs over every new overlay (`values-readonly-source.yaml`, `values-fan-in.yaml`, `values-failover.yaml`, `values-failback.yaml`, `values-scale.yaml`).

---

## 5. Phases and acceptance criteria

**Phase 1 — Correctness** (§2.3, §3.1)
- [ ] `offsetSyncs.location` renders into both connectors; a mismatch fails; `location: target` emits the target ACL and `source` does not
- [ ] `--set mirrors[0].readOnlySource=true` renders a CR that writes nothing to the source; the README's source-side contract matches what the setting needs
- [ ] The `heartbeats` grant is gone from auto-mode; the comment says v1 removed the connector
- [ ] `maxReplicas > sum(tasksMax)` fails with the arithmetic; NOTES print the ratio
- [ ] Every existing overlay renders byte-identically apart from the new fields (a render diff against 0.3.0 proves the fixes are additive)

**Phase 2 — Fan-in** (§3.2)
- [ ] Two sources render: two ACL prefix sets, two NetworkPolicy egress entries, two alert sets, two dashboard rows
- [ ] Duplicate aliases and identity-overlapping patterns are refused
- [ ] The replication test loops per source; a two-source kind run passes both legs

**Phase 3 — Direction** (§3.3)
- [ ] `values-failover.yaml` seeds group offsets from checkpoints; a consumer resumes on the target at the right position (new test)
- [ ] `values-failback.yaml` renders the reverse mirror; the reverse-direction rail refuses it while the forward mirror is live
- [ ] NOTES print the posture and the next command

**Phase 4 — Guarantees, scale, observability** (§3.4–3.6)
- [ ] `exactlyOnce.enabled=true` sets the worker property, the isolation level and the ACLs; incoherent settings are refused
- [ ] `values-scale.yaml` renders and passes kubeconform; `strimziMetricsReporter` renders and its PodMonitor scrapes
- [ ] New alerts fire in a kind run (lag, offset-sync staleness) and every rule carries a `runbook_url`

**Phase 5 — Surface, security, ergonomics** (§3.7, §3.8, §3.10)
- [ ] `jmxOptions`, `clientRackInitImage`, `template.connectContainer`, `template.extra`, connector `version`, `consumer.client.rack` all render
- [ ] `authentication.type: custom` renders for source and target; `secretSync.watch` renders the CronJob and its RBAC
- [ ] `topics: ["a","b"]` compiles to an escaped pattern; the pattern still wins when both are set

**Phase 6 — Docs and the CLI**
- [ ] README topology decision table; runbook sections for the new alerts; the chart table and version matrix regenerated
- [ ] `kates migrate` gains `--read-only-source` (passes through to §3.1) and resolves several `--from` sources into one release (§3.2)

Rough size: Phase 1 ≈ 250 lines of templates + tests; Phase 2 ≈ 350; Phase 3 ≈ 300; Phase 4 ≈ 400; Phase 5 ≈ 200; Phase 6 ≈ 250.

---

## 6. Risks and decisions

- **`offset-syncs.topic.location: target` is not free.** It changes where MirrorMaker stores its sync state, so flipping it on a *running* mirror restarts translation from scratch: checkpoints regress until the new topic catches up. The values comment says so, and the plan changes no default — read-only sources opt in.
- **Fan-in under `IdentityReplicationPolicy` is the dangerous configuration**, which is why the overlap rail is a refusal rather than a warning. Two sources with `DefaultReplicationPolicy` are safe by construction (`<alias>.` prefixes); identity is safe only when the patterns are disjoint, and only the person writing them knows that.
- **Exactly-once costs throughput** and pins the source to a version with transactional reads. Off by default; the tests keep asserting *distinct* records, so at-least-once stays the tested path.
- **The scaling rail will annoy someone** who wants headroom before raising `tasksMax`. The refusal names both numbers and the two ways forward; a `--set autoscaling.allowIdleWorkers=true` escape hatch exists for the case where the operator has decided.
- **Rotation via CronJob is a real grant.** Opt-in, with the RBAC scoped to the named Secrets, and the alternative (kubernetes-reflector) named in the README for clusters that already run one.
- **The chart is not becoming a DR product.** Failover and failback here are *values and rails*; the sequencing, the DNS switch and the application restart are the runbook's, and the plan links rather than absorbs them.
- **Strimzi may move first.** If a future operator adds a first-class field for something this plan implements through `config:` (a heartbeat connector returning, say), the chart follows the API and drops its own knob in the release that can.

## 7. Non-goals

- A heartbeat connector: v1 removed it from `KafkaMirrorMaker2`, and running one needs separate `KafkaConnect` + `KafkaConnector` resources — a different chart's job, named in the README.
- Connect plugin builds (`template.build*`, `buildConfig`): MM2 ships its connectors.
- Bidirectional replication inside one release: two releases, documented, guard-railed.
- KEDA / lag-driven autoscaling in-chart.
- Managing the **source** cluster's `KafkaUser`: it lives on a cluster this chart cannot touch; the chart states the contract and the preflight verifies it.
- Replacing the runbook or the migration plans; this is the chart's half of that work.

# Plan — Cross-Version Replication with MirrorMaker 2 (2.x → 4.x, 3.x → 4.x)

Branch: `feat/mirror-maker2-cross-version` (from `main`). Goal: take `charts/mirror-maker2` from *"renders and smoke-tests against a loopback"* to *"migrates a real Kafka 2.x or 3.x cluster onto the in-repo 4.x cluster, and proves it did"* — with the legacy source clusters, the safety rails, the tests, and the documentation that claim requires.

> **Status: IMPLEMENTED.** Everything below is built and verified through `helm lint` / `helm template` (all overlays) / `kubeconform` against the real Strimzi `KafkaMirrorMaker2` **v1** schema. The live kind runs are driven by `scripts/test-mm2-migration.sh` and `.github/workflows/ci-mirror-maker2.yml`; see [Verification](#9-verification-what-was-actually-run) for exactly what was executed where, and [What the review found](#8-what-the-review-found) for the defects the gates could not see.

> **Status note — 2026-09-19.** Two version premises below have moved. The pin is **Strimzi 1.2.0**, whose window is 4.2.0, 4.2.1, 4.3.0 and **4.3.1** — so §1's "Strimzi 1.1.0 runs Kafka 4.3.0 and nothing else" understates it — and the MM2 workers run `spec.version: 4.3.1`. The KIP-896 argument that follows from it is unchanged: the workers are still a 4.x client and the 2.1 protocol floor still applies. The chart itself is now **0.8.0**, built on the `kafka-common` library, so a render from a checkout needs `helm dependency build charts/mirror-maker2` first. Current reference: [charts/mirror-maker2/README.md](../charts/mirror-maker2/README.md) and [docs/mirror-maker2-runbook.md](mirror-maker2-runbook.md).

---

## 1. The problem, stated precisely

The existing chart replicates. It does not *migrate across versions*, and the difference is not cosmetic:

1. **There is no 2.x or 3.x cluster in this repo to replicate from.** Strimzi 1.1.0 runs Kafka 4.3.0 and nothing else — `spec.version` is validated against the operator's supported set. So the source side of a cross-version test does not exist and cannot be conjured from the operator we have.
2. **The compatibility floor is a cliff, and it is silent.** [KIP-896](https://cwiki.apache.org/confluence/x/K5sODg) removed old client protocol API versions in Kafka 4.0. A 4.x client — and MM2's consumer *is* a 4.x client, because the Connect workers run `spec.version: 4.3.0` — can talk to brokers **2.1 and newer, and nothing older**. Below that the broker answers `UNSUPPORTED_VERSION` and the `MirrorSourceConnector` fails in a way that looks like a network problem. Nothing in the chart says this out loud, and nothing checks it.
3. **The default replication policy renames every topic.** `DefaultReplicationPolicy` writes `orders` as `source.orders` on the target. That is correct for DR aggregation and *wrong* for a migration, where the entire point is that consumers repoint at a new cluster and find the topics they already know. Migrations need `IdentityReplicationPolicy`, and turning it on safely requires excluding MM2's own internal topics from the mirror or the flow eats itself.
4. **A cutover is a sequence, not a state.** Drain, verify lag is zero, stop the source connector, move producers, move consumers onto translated offsets. The v1 CRD has the primitives for this — `sourceConnector.state: running|paused|stopped`, `autoRestart`, `listOffsets`/`alterOffsets` — and the chart exposed none of them.
5. **"It replicated" was never asserted.** The one Helm test waited for `condition=Ready` on the CR. A `KafkaMirrorMaker2` reports Ready when its Connect workers are up — *before* a single record has crossed, and it stays Ready while the source connector stalls on a missing ACL. A test that goes green on a broken mirror is worse than no test.

Everything in this plan is a direct answer to one of those five.

---

## 2. Constraints that shaped the design

| Constraint | Consequence |
|:---|:---|
| Strimzi 1.1.0 supports only Kafka 4.x | The legacy sources cannot be `Kafka` CRs. They are plain StatefulSets in a new `legacy-kafka` chart. |
| KIP-896 floor is broker **2.1** | 2.0.x and older are out of scope *by protocol*, not by choice. The chart now refuses them rather than letting them fail at runtime. |
| No official `apache/kafka` image below 3.7.0 | Kafka 3.9 uses the upstream image. Kafka 2.8 needs one built here — `Dockerfile.legacy-kafka` layers the Apache tarball onto a Temurin JRE, which is architecture-neutral because Kafka is pure Java. |
| Kafka 2.x requires ZooKeeper; 3.9 does not | The legacy chart has two modes (`zookeeper`, `kraft`) rather than two charts. |
| kind on a laptop / a 2-core runner | The e2e runs in **one** cluster with the legacy sources in their own namespaces, RF1 throughout. A two-cluster topology is documented but is not the CI gate. |
| The v1 `KafkaMirrorMaker2` API has no `clusters[]`/`connectCluster` and no `heartbeatConnector` | Values stay shaped like `spec.target` + `mirrors[].source`. There are no heartbeats at all — `emit.heartbeats.enabled` on the source connector is a no-op — so replication lag comes from the source connector's own `replication-latency-ms` metric. |

---

## 3. What gets built

### 3.1 `charts/legacy-kafka` — the source clusters that could not exist

A deliberately small chart whose only job is to be an *old* Kafka that MM2 can read.

```
mode: zookeeper   →  ZooKeeper StatefulSet + Kafka 2.8.2 StatefulSet
mode: kraft       →  single combined-role Kafka 3.9.1 StatefulSet
```

- **Listeners.** `PLAINTEXT` on 9092 by default; optional `SASL_PLAINTEXT` on 9094 with SASL/PLAIN backed by a rendered JAAS Secret. SASL/PLAIN is chosen over SCRAM on purpose: it configures identically on 2.8 (ZooKeeper) and 3.9 (KRaft), so the auth dimension does not entangle with the version dimension.
- **Advertised listeners** are computed per-pod from the downward API against the headless Service, so a client that bootstraps through the ClusterIP Service is handed a resolvable address back.
- **Storage** is `emptyDir` by default — this is a lab source, and a migration test should start from nothing every time. A PVC is one value away.
- **Seeding.** `topics[]` pre-creates topics (partitions, RF, config) through a post-install Job, so a test has something to mirror before MM2 exists.
- **Helm test** runs `kafka-topics.sh --list` against the bootstrap Service using the cluster's own image — i.e. an *era-appropriate* client, which is the only client guaranteed to work with a 2.8 broker.

Explicitly **not** production: no rack awareness, no PDB, single broker by default, `unclean.leader.election` left at the era default. The chart's README says so in the first paragraph. It exists to be replicated *from* and then deleted.

### 3.2 `charts/mirror-maker2` — five additions

**(a) `replicationPolicy` — migrations keep their topic names.**

```yaml
replicationPolicy:
  mode: identity          # default | identity
  class: ""               # explicit class wins over mode
  separator: "."
  excludeInternalTopics: true
```

`mode: identity` renders `replication.policy.class: org.apache.kafka.connect.mirror.IdentityReplicationPolicy` into **both** the source and checkpoint connector configs. In **either** mode the chart computes a `topicsExcludePattern` per mirror: MirrorMaker's stock exclusions plus `<groupId>-.*` (the Connect internal topics) and `<alias>\..*` (anything already carrying this source's prefix — without it the shipped loopback re-mirrors `source.orders` as `source.source.orders` every refresh interval, because MirrorMaker's cycle detection keys on the target alias). Identity mode appends MM2's own topic names (`mm2-.*`, `heartbeats`, `checkpoints`, and their `.internal` forms), because with names preserved the mirror can consume its own output. Getting that exclusion wrong is the classic failure in both modes: the topic count grows without bound. The chart makes it the default behaviour instead of a footnote.

**(b) `compatibility` — the KIP-896 floor, enforced at template time.**

```yaml
compatibility:
  enforce: true
  minSourceVersion: "2.1.0"
  requireDeclaredVersion: false
mirrors:
  - source:
      kafkaVersion: "2.8.2"     # new, optional-but-checked
```

`semverCompare` in `_helpers.tpl` fails the render with a message naming the source alias, its version, and the floor. This turns a runtime `UNSUPPORTED_VERSION` — which surfaces an hour later as "the connector is running but nothing is replicating" — into a `helm install` that exits non-zero in two seconds. `requireDeclaredVersion: true` additionally refuses any source that has not declared its version at all.

A second, related guard: `target.brokerCount` (0 = unknown) is compared against every `replication.factor` in the mirror configs. RF3 against a single-broker kind target is the single most common way this chart is misconfigured, and it fails *late*, inside the connector, as a topic-creation error.

**(c) `preflight` — prove the wire before deploying on it.**

An optional pre-install/pre-upgrade hook Job that runs `kafka-broker-api-versions.sh` from the **4.x** client image against every source bootstrap. It is the same client MM2 will use, so a pass is evidence and not an assumption. It runs the handshake once per source and classifies the failure — `PROTOCOL` (below the KIP-896 floor), `DNS`, `NETWORK`, `AUTH`, `TLS` — because each of those fails the same call with a different exception and each needs a different fix. For TLS sources it has no truststore, so it proves reachability and says explicitly that trust and credentials were not verified rather than implying they were. Off by default (it costs 20 seconds); on in the migration presets, where those 20 seconds are cheap against a failed cutover.

**(d) Connector lifecycle — the cutover primitives.**

`state` (`running` | `paused` | `stopped`) and `autoRestart` are exposed per connector, plus a chart-level `autoRestart` default. `values-cutover.yaml` is the whole final step of a migration expressed as a values file: source connector `stopped`, checkpoint connector `running` so offset translation completes for the consumers that have not moved yet.

**(e) Observability parity with `connect-cluster`.**

`alerts.yaml` (a `PrometheusRule` — replication latency, records-not-flowing, failed tasks, checkpoint stall, heap), `dashboard.yaml` (a Grafana sidecar ConfigMap), a logging ConfigMap for `logging.type: external`, and MM2-specific JMX exporter rules (`record-age-ms`, `replication-latency-ms`, `byte-rate`, `checkpoint-latency`) added to the metrics ConfigMap. Replication lag is the number that tells you whether a cutover is safe; until now the chart did not export it.

### 3.3 Tests that can fail

Four Helm tests, in hook-weight order:

| Test | Asserts | Default |
|:---|:---|:---|
| `…-test-ready` | CR reaches `Ready` | on |
| `…-test-connectors` | every mirror's source **and** checkpoint connector reports `RUNNING` in `.status.connectors[]` (or `PAUSED`/`STOPPED`, which is a cutover, not a fault), with zero failed tasks | on |
| `…-test-replication` | create the test topic on the source, produce N records, consume them from the target under the *policy-correct* name with a named group, assert the **distinct** count | off — needs credentials |
| `…-test-offsets` | a consumer group committed on the source appears, translated, on the target | off |

`test-connectors` is the one that closes the "Ready but stalled" hole: it reads the connector status the operator writes onto the CR, which is where a missing source ACL actually shows up.

### 3.4 `scripts/test-mm2-migration.sh` — the real system

One script, one kind cluster, both migration paths:

```bash
scripts/test-mm2-migration.sh --source-version 2.8.2     # 2.x → 4.x
scripts/test-mm2-migration.sh --source-version 3.9.1     # 3.x → 4.x
scripts/test-mm2-migration.sh --source-version 3.9.1 --policy default
```

Phases, each one failing loudly and independently: preflight (cluster, operator, target `Kafka` Ready, the `kates-mm2` Secret) → deploy `legacy-kafka` → seed topics and produce a known corpus → **record a consumer group offset on the source** → install MM2 with the matching preset → wait for `Ready` and for connectors `RUNNING` → wait for the replicated topic to materialise → consume from the target and compare the *distinct* records against the full expected set (MM2 is at-least-once, so a line count would pass a duplicated corpus with gaps) → verify offset translation with `kafka-consumer-groups.sh --describe` on the target → cutover rehearsal (stop the source connector, confirm the target's end offsets stop growing) → report → teardown (skippable with `--keep`).

It reuses `scripts/common.sh`, takes `--namespace`, `--policy`, `--topic`, `--messages`, `--timeout`, `--legacy-image`, `--skip-build` and `--keep`, and prints a final table of every assertion with a pass/fail mark, because a migration test whose output you have to interpret is a migration test nobody runs twice.

### 3.5 CI

`.github/workflows/ci-mirror-maker2.yml`:

- **`chart` job** (on every PR touching the charts): lint, template every overlay including the new presets, `kubeconform` against the Strimzi CRD catalog, and a set of *negative* renders asserting the safety rails actually bite — a 2.0.1 source must fail the KIP-896 gate, RF3 against `brokerCount: 1` must fail the durability gate, an undeclared version under `requireDeclaredVersion` must be refused, alerts or PodMonitors without metrics must be refused, an auto-ACL user under identity policy with no `topicGrants` must be refused, and `legacy-kafka` must refuse KRaft mode on 2.8.2. A guard that has never been observed to reject anything is not a guard.
- **`migration-e2e` job** (matrix `2.8.2` × `3.9.1`, `workflow_dispatch` + label-gated): kind up, operator, single-broker target, then `scripts/test-mm2-migration.sh`, with logs and CR status uploaded as artifacts on failure.

---

## 4. Documentation

| Document | Answers |
|:---|:---|
| `charts/legacy-kafka/README.md` | What this fake old cluster is, and why you must not run it in production |
| `charts/mirror-maker2/README.md` (rewritten) | The v1 model, the credential contract, **the cross-version section**, replication policy, the safety rails, cutover |
| `docs/tutorials/10-mirror-maker2-installation.md` | Install and set up MM2 from zero on kind, ending in a verified loopback mirror |
| `docs/tutorials/11-migrating-kafka-2x-to-4x.md` | The full 2.8.2 → 4.3.0 migration, command by command, with the expected output at each step |
| `docs/tutorials/12-migrating-kafka-3x-to-4x.md` | The 3.9.1 → 4.3.0 path and how it differs (KRaft, no ZooKeeper, no protocol risk) |
| `docs/mirror-maker2-runbook.md` | Cutover checklist, rollback, the pre-flight verdict table, and a symptom → cause → fix troubleshooting table |
| `docs/book/22-mirror-maker2-migration.md` | The chapter version, wired into `_quarto.yml` |

Plus the two generated gates that adding a chart breaks: the README chart table (`scripts/gen-chart-table.sh`) and the book's version matrix (`scripts/gen-version-matrix.sh`).

---

## 5. Risks and non-goals

- **Below 2.1 is not supported and will not be.** It is a protocol removal, not a configuration. The documented path for 0.10–2.0 is two hops: legacy → an intermediate 3.x cluster → 4.x. The chart says this instead of pretending.
- **The lab source is single-broker, RF1, `emptyDir`.** It proves *protocol and topology* compatibility, not durability or throughput. Nothing in the docs implies otherwise.
- **MM2 is at-least-once.** Duplicates after a failover are expected. The tests assert *content presence*, not exactly-once counts, and the tutorials name this before the cutover step rather than after.
- **Offset translation is approximate.** `MirrorCheckpointConnector` maps offsets through `offset-syncs`, and the mapping is at record granularity with a lag. A consumer moved onto translated offsets may re-read. Stated in the runbook where it matters, at cutover.
- **One kind cluster is not two data centres.** No inter-cluster latency, no partitions, no independent CA trust. The two-cluster topology is documented for staging; the CI gate deliberately is not it.

---

## 6. Sequence

1. `charts/legacy-kafka` + `Dockerfile.legacy-kafka` → renders, and a 2.8.2 broker actually starts.
2. MM2 chart: `replicationPolicy`, `compatibility`, connector lifecycle, presets, schema → all overlays render, negative cases fail.
3. Observability: alerts, dashboard, logging, JMX rules.
4. Helm tests: connectors, replication round trip, offset translation.
5. `scripts/test-mm2-migration.sh` + the CI workflow.
6. Documentation, then regenerate both docs gates.
7. Independent review of the result against the real images, tools and API behaviour — see §8 for why this is a step and not a nicety.

## 7. Acceptance criteria

- [x] `helm lint` + `helm template` clean for `mirror-maker2` (base + 8 overlays) and `legacy-kafka` (base + 4 overlays)
- [x] `kubeconform -strict` clean against the Strimzi v1 CRD schema for every rendered overlay
- [x] A source declaring Kafka 2.0.1 **fails** to render; 2.1.0 renders
- [x] RF3 against `target.brokerCount: 1` **fails** to render
- [x] Identity mode emits the internal-topic exclusions; default mode excludes `<alias>.*` so the shipped loopback cannot loop
- [x] `scripts/test-mm2-migration.sh --help` documents every phase; shellcheck clean
- [x] README chart table and book version matrix regenerated and passing `--check`

---

## 8. What the review found

Three independent read-only review passes over the first implementation — charts against the real container images and Kafka CLI history, scripts and CI against the repo's own conventions, and every document against the code — found defects the render-time gates could not. They are recorded here because each one is a class of failure, not a typo, and the next chart in this repo will meet the same classes.

**Would have failed every install.**
The pre-install probe named the chart's ServiceAccount, an ordinary resource that Helm creates *after* pre-install hooks run, so no probe pod could ever be scheduled. (The first fix moved the credential-sync Job to pre-install as well, so the probe could mount synced Secrets — which made its RBAC hook resources too, and hook resources are not cleaned up by `helm uninstall`; a second pass caught the Role granting `get` on Secrets that would have leaked into every source namespace forever, and the sync went back to post-install with the probe now treating an absent credential as "reachability and protocol verified, credentials not".) Then it called `getent`, which the ubi-minimal client image does not ship, so every source would have been reported as unresolvable. And the 2.x image build used a floating Temurin tag that had moved to an Ubuntu release with a default user at uid 1000 — the uid the image creates — so `groupadd` failed the build. Each of the three was a correct idea executed against an assumption about the environment that nobody had checked.

**Would have passed on a broken mirror.**
The offset tool the migration test called (`kafka.tools.GetOffsetShell`) moved packages in Kafka 3.4 and does not load on the 4.x image; with stderr discarded, that read as an empty offset. `kubectl run --rm` prints its deletion notice on stdout, so the pod name's `$RANDOM` digits landed inside every number the test parsed — making one assertion pass unconditionally and another fail unconditionally. The awk that summed offsets printed `OFFSETSUM=0` on *empty* input, so a failed measurement was indistinguishable from an empty topic. The content check compared only the first and last record. And the cutover baseline was sampled before the cutover, so in-flight records counted as "the source connector did not stop". Every one of these is a test that reports a verdict it did not measure; the fix in each case was to make the absence of a measurement its own, distinct outcome.

**Would have replicated nothing while reporting healthy.**
The `kafka-cluster` NetworkPolicy admitted `KafkaConnect` pods to the brokers but not `KafkaMirrorMaker2` ones, which Strimzi labels differently. The MM2 policy had no rule for the Cluster Operator, which creates the connectors over REST — fine while Strimzi generates its own policy, fatal the day someone turns that off. The migration presets used a Connect group ID outside the prefix the `kates-mm2` user was granted, so the workers could not join their own group. The user lacked `AlterConfigs`, so topic-config sync failed on every refresh, and lacked a wildcard group grant, so offset translation could only cover groups whose names were known in advance. And the shipped loopback, under the default policy, re-mirrored `source.orders` as `source.source.orders` every refresh interval, because MirrorMaker's cycle detection keys on the target alias and the exclusion that would have caught it was only computed in identity mode.

**Documented behaviour the code did not have.**
`kubectl get kafkatopics` was the documented way to see replicated topics; the Topic Operator is unidirectional and never lists them. A logging knob was documented that the CR template dumped into a field the API server pruned. A Helm test was described as asserting `RUNNING` when it also accepted paused and stopped. The runbook's "is data arriving" command used the moved offset class. Sample outputs named a host that could not appear and omitted rows the script emits.

None of these were visible to `helm lint`, `helm template`, `kubeconform`, or shellcheck, all of which passed throughout. They were visible to someone who knew what the images contain, what the tools print, and what Kubernetes does with a pre-install hook — which is the argument for the review pass being a fixed part of the sequence in §6 rather than an afterthought.

## 9. Verification (what was actually run)

Rendering, schema and negative-path checks were executed in full: `helm lint`, `helm template` across every overlay of both charts, `kubeconform -strict` against the live Strimzi `KafkaMirrorMaker2` v1 and `Kafka` schemas, the seven intentional-failure renders, and every embedded pod script parsed by `dash -n` (the pods' `/bin/sh`). The helper functions of the migration test — the output-marker cut, the offset sum, the content diff, the credential escaping — were unit-tested in isolation against crafted inputs, including the empty and error-only cases. The docs gates (`gen-chart-table.sh --check`, `gen-version-matrix.sh --check`, `check-versions.sh` — now with the Kafka line as well as the Strimzi one) and the book style check pass.

The **live kind runs** — `make mm2-migration-test`, both matrix legs — require Docker and kind, which the authoring environment does not have. They are scripted, self-checking, and wired into CI; run them locally with:

```bash
make cluster && make deploy-strimzi && make deploy-kafka
scripts/test-mm2-migration.sh --source-version 2.8.2
scripts/test-mm2-migration.sh --source-version 3.9.1
```

If either leg fails, the script prints the failing phase, the CR status, and the connector status before exiting — start there, then the troubleshooting table in `docs/mirror-maker2-runbook.md`.

## 10. What comes next

The shell that drives the lab — `scripts/test-mm2-migration.sh`, `scripts/build-legacy-kafka-image.sh`, `scripts/mm2-kafka-cli.sh` — is the part of this work most likely to rot, for reasons §8 makes concrete. [Moving the migration tooling into the `kates` CLI](mirror-maker2-cli-migration-plan.md) plans their replacement by a `kates migrate` command family and their deletion.

The chart itself has a next step of its own: the shapes it cannot yet express — a source you may only read, several sources into one target, a mirror that must be failed over and back — and two things its templates get subtly wrong (the offset-syncs topic's location, and an HPA that scales past `tasksMax`). [Enhancing the mirror-maker2 chart](mirror-maker2-chart-enhancement-plan.md) plans 0.4.0 around them.

The other seam this work opened is the single, hard-coded primary cluster: `krafter`, `kafka`, `4.3.0`, spelled out across the CLI and the consumer charts. [Choosing the Strimzi and Kafka versions in `kates deploy`, and running several versions side by side](kafka-multi-version-deploy-plan.md) plans `--operator-scope`, `--strimzi-version` and `--kafka-version` for the primary installation — the Kafka versions offered being exactly what an eligible operator can run — and a `kates clusters` family that deploys additional versions, each in its own namespace: under the primary's operator, under an operator of their own when the operators are namespace-scoped, or with `legacy-kafka` when no Strimzi can run them.

# Plan — Introduce a `mirror-maker2` Chart

Branch: `feat/mirror-maker2-chart` (from `main`). Goal: add `charts/mirror-maker2/`, a first-party chart deploying a Strimzi **KafkaMirrorMaker2** cluster for cross-cluster replication (active/passive DR, migration, aggregation), built to the same bar as the repo's existing Strimzi charts.

> **Status: IMPLEMENTED.** Chart built and verified (lint + all overlays + kubeconform against the real Strimzi CRD, schema, docs gates). **API note:** Strimzi 1.1.0's `KafkaMirrorMaker2` **v1** API differs from the v1beta2 shape this plan first sketched — it has no `connectCluster`/`clusters[]`. The chart uses the correct v1 model: a single **`spec.target`** (alias, bootstrapServers, groupId, config/offset/status storage topics, tls, auth, config) plus **`mirrors[].source`** (per-mirror source cluster) with `sourceConnector` + `checkpointConnector` (no separate `heartbeatConnector` in v1). The credential-contract, durability, and CI/docs points below all still hold.

## Why, and what it is

MirrorMaker 2 runs on Kafka Connect — a `KafkaMirrorMaker2` CR *is* a Connect cluster with three built-in connectors (`MirrorSourceConnector`, `MirrorCheckpointConnector`, `MirrorHeartbeatConnector`). So the operational surface (Connect workers: replicas, JVM, probes, metrics, HPA, NetworkPolicies, Connect internal topics) is nearly identical to `charts/connect-cluster`, and that chart is the template to copy from. The **one structural difference that drives the whole design**: MM2 talks to **two or more Kafka clusters** (a source and a target), whereas Connect talks to one. Everything novel in this chart flows from that.

The repo already has the pieces this leans on: the `strimzi-operator` chart owns the `kafkamirrormaker2s.kafka.strimzi.io` CRD (it's in the operator's CRD bundle and the operator's tests already assert it Established), and `kafka-cluster` provisions per-workload `KafkaUser`s + ACLs (e.g. `kates-connect`, `apicurio-registry`). MM2 is the natural next consumer of both.

## The multi-cluster design (the part that isn't Connect)

The `KafkaMirrorMaker2` CR has three top-level pieces the chart must model as values:

1. **`clusters[]`** — every Kafka cluster involved, each with an `alias`, `bootstrapServers`, and its **own** `tls` + `authentication` (each cluster has separate credentials). Minimum two: a source and a target.
2. **`connectCluster`** — the alias whose brokers host MM2's Connect internal topics (`mm2-configs`, `mm2-offsets`, `mm2-status`). Conventionally the **target**.
3. **`mirrors[]`** — one entry per source→target flow: `sourceCluster`, `targetCluster`, `topicsPattern`, `groupsPattern`, and per-connector config (`sourceConnector` / `checkpointConnector` / `heartbeatConnector`) including the critical `replication.factor`, `offset-syncs.topic.replication.factor`, `sync.group.offsets.enabled`, and refresh intervals.

Proposed values shape (kept close to the CR so it stays legible against upstream docs, with a curated default that mirrors the in-repo `krafter` cluster to itself as a loopback for smoke-testing):

```yaml
clusters:
  - alias: source
    bootstrapServers: ""          # required; external or another in-repo cluster
    tls: { enabled: false, trustedCertificateSecret: "", certificateKey: ca.crt }
    authentication: { type: scram-sha-512, username: kates-mm2-source, secretName: "", secretKey: password }
  - alias: target
    bootstrapServers: krafter-kafka-bootstrap.kafka.svc:9092
    tls: { enabled: false, ... }
    authentication: { type: scram-sha-512, username: kates-mm2, secretName: kates-mm2, secretKey: password }
connectCluster: target           # holds the MM2 Connect internal topics
mirrors:
  - sourceCluster: source
    targetCluster: target
    topicsPattern: ".*"
    groupsPattern: ".*"
    sourceConnector:
      config: { replication.factor: 3, offset-syncs.topic.replication.factor: 3, sync.topic.acls.enabled: "false" }
    checkpointConnector:
      config: { checkpoints.topic.replication.factor: 3, sync.group.offsets.enabled: "true" }
    heartbeatConnector:
      config: { heartbeats.topic.replication.factor: 3 }
```

Everything else (replicas, version/image, jvmOptions, resources, HPA, metrics, NetworkPolicies, topologySpread) is lifted from `connect-cluster` with the same value names, so an operator who knows that chart knows this one.

## Credentials & ACLs — the sharp edge

MM2 needs a `KafkaUser` on **both** ends: on the **source** (READ on mirrored topics + groups, DESCRIBE) and on the **target** (READ/WRITE/CREATE on the replicated topics, and full access to the MM2 internal + offset-sync + checkpoint + heartbeat topics, plus the Connect groups). Two realities:

- **Target is usually in-repo (`krafter`).** Provision its `KafkaUser` + ACLs in `charts/kafka-cluster/values.yaml` (a new `kates-mm2` entry alongside `kates-connect`/`apicurio-registry`), and let this chart reference the resulting Secret — same pattern the other consumers use. The chart's own `kafkaUser` block (copied from connect-cluster) can also create it directly when Kafka is co-located.
- **Source is usually EXTERNAL** (that's the point of replication). The chart **cannot** provision a user on a cluster it doesn't manage — so for the source it consumes a pre-existing Secret (name via values) and documents the exact ACL set the remote operator must grant. `secretSync` (job/reflector, copied from connect-cluster) handles making either Secret present in the MM2 namespace.

This split — provision what we own (target), document what we don't (source) — is the honest contract and must be stated loudly in the README and NOTES, because a silent missing source ACL surfaces only as a stalled `MirrorSourceConnector`.

## File-by-file build (mirror `connect-cluster`)

Copy the structure, adapt the CR and the cluster-plurality:

- `Chart.yaml` — `name: mirror-maker2`, `version: 0.1.0`, `appVersion` tracking the Kafka line (`4.3.0`, from `versions.env STRIMZI_KAFKA_VERSION`), `kubeVersion: ">=1.27.0-0"`, keywords `mirrormaker2`/`replication`/`dr`.
- `values.yaml` — the multi-cluster block above + the connect-cluster passthroughs (replicas, image, version, jvmOptions, resources, autoscaling, topologySpreadConstraints, metrics, networkPolicy, kafkaUser, secretSync, testImages, keepOnDelete).
- `values.schema.json` — strict where it matters (cluster alias/auth enums, mirror required fields); follow connect-cluster's schema.
- `values-{dev,kind,prod,generic}.yaml` — kind: single loopback mirror, RF1, small heap; prod: replicas ≥2, **drain-safe PDB** (apply the P0 lesson — `minAvailable < replicas`), topologySpread, RF3, tightened security, PodMonitor + alerts on.
- `templates/`:
  - `kafka-mirror-maker2.yaml` — the CR (`kafka.strimzi.io/v1`, `kind: KafkaMirrorMaker2`), rendering `clusters[]`, `connectCluster`, `mirrors[]`, plus the Connect worker spec (version, image, replicas-unless-HPA, jvmOptions, resources, metrics config, template/securityContext).
  - `_helpers.tpl`, `serviceaccount.yaml`, `networkpolicies.yaml` (+ default-deny), `podmonitor.yaml`, `alerts.yaml`, `dashboard.yaml`, `hpa.yaml`, `kafka-user.yaml` (target), `kafka-user-secret-sync.yaml`, `metrics-configmap.yaml`, `NOTES.txt`.
  - `tests/` — `test-mm2.yaml` (CR Ready), `test-connectors.yaml` (the three mirror connectors Running), `test-topics.yaml` (a `<source>.<topic>` replicated topic appears on the target).
- `README.md`, `.helmignore`.

## Versioning, CI, and docs gates

- **Version pins.** `appVersion` = Kafka `4.3.0`; the worker image = the repo's connect/kafka image. If `scripts/check-versions.sh` grows to know this chart, wire it; otherwise no new pin site.
- **CI is automatic.** The Helm Lint job's `for chart in charts/*/` loops (lint, template, kubeconform) pick the chart up with no workflow change — provided its Strimzi CR passes kubeconform against the community CRD catalog (it will; `KafkaMirrorMaker2` is in the catalog).
- **Docs gates are NOT automatic.** `scripts/gen-chart-table.sh --check` and `scripts/gen-version-matrix.sh --check` both iterate `charts/*/`, so adding the chart makes them fail until the README chart table and the book's `appendix-d` matrix are regenerated. Run both generators as the final step (same dance as every chart bump in this repo).
- **Umbrella (optional).** Add as an optional `kates-platform` dependency (`condition: mirror-maker2.enabled`, default off), following the `apicurio-registry` precedent.

## Testing

- **Helm tests** (ship with the chart): MM2 CR reaches Ready; the three mirror connectors report Running; a replicated topic materialises on the target.
- **Live smoke (optional, manual `workflow_dispatch`).** A kind job that stands up the in-repo `kafka-cluster`, installs MM2 with a **loopback** mirror (`source` and `target` both = `krafter`, distinct topic prefixes), produces to a source topic, and asserts the `source.<topic>` appears — proves the whole path without needing two clusters. Model it on `ci-apicurio.yml`; keep it manual and single-broker (RF1) to fit a runner. A true two-cluster test is out of scope for CI (resource-prohibitive) and belongs in a staging environment.

## Risks / non-goals

- **Not a general Connect chart** — this deploys MM2 specifically; regular connectors belong in `connect-cluster`.
- **Source-side provisioning is out of scope** by nature (external cluster); the chart documents the required ACLs but cannot create them.
- **Offset/checkpoint durability** depends on `replication.factor` ≥3 and `sync.group.offsets.enabled` on the target — prod overlay must set these, and the README must warn that RF1 (kind) is non-durable.
- **Exactly-once** across clusters is not a MM2 guarantee; the chart should not imply it.

## Sequence

1. Scaffold from `connect-cluster` (Chart.yaml, values, helpers, serviceaccount, NOTES) → lint green.
2. `kafka-mirror-maker2.yaml` CR with `clusters[]`/`connectCluster`/`mirrors[]` + schema → `helm template` + kubeconform green.
3. Credentials: target `KafkaUser` (chart + a `kates-mm2` entry in `kafka-cluster`), secretSync, source-Secret consumption + documented ACLs.
4. Ops surface: NetworkPolicies, PodMonitor, alerts, HPA, topologySpread, prod overlay (drain-safe PDB, RF3, security).
5. Tests (helm tests) + README/NOTES; regenerate the two docs gates.
6. Optional: kates-platform dependency + the manual loopback kind smoke test.

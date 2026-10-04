# Plan — Refactoring the Kafka, Kafka Connect and MirrorMaker 2 Charts

Audit date: 2026-09-17. Branch: `feat/kafka-charts-refactor` (from `main`, after `a9bca9c`).

| Chart | Today | Target | Kind of change |
|:---|:---|:---|:---|
| `charts/kafka-common` | — | **0.1.0** | new `type: library` chart |
| `charts/kafka-cluster` | 0.4.0 | **1.0.0** | breaking: node-pool model, resource names, ownership |
| `charts/connect-cluster` | 1.3.3 | **2.0.0** | breaking: library-based worker, connectors map, auth enum |
| `charts/mirror-maker2` | 0.7.1 | **0.8.0** | minor: moves onto the library, three fixes |
| `charts/strimzi-operator` | 0.2.0 | **0.3.0** | minor: sole CRD owner, takes the drain cleaner and the operator scrape |
| `charts/kates-platform` | 0.6.0 | **0.7.0** | minor: adds `connect-cluster` (off by default) |

> **Status: PROPOSED.** Nothing here is implemented. §2 is the study the plan rests on. Each defect in §2.3 was checked against the code at `a9bca9c`. The schema-level ones were reproduced by rendering the chart and checking the output field by field against the `v1` schemas in the vendored `strimzi-kafka-operator-1.2.0` CRDs. The alert findings come from reading each expression against the chart's own exporter rules (Appendix A). The one claim that needs a live cluster is marked **(confirm in Phase 0)**.

---

## 1. Why, and what "refactor" means here

Three charts deploy CRs for the same operator. Two of them, `connect-cluster` and `mirror-maker2`, deploy the same workload: a Kafka Connect worker group. They share no code, and they have drifted apart.

`mirror-maker2` is the mature one. Over the 0.4 → 0.7 work it gained render-time rails that "fail in seconds and name the way out", replica counts read back with `lookup` under an HPA, `nodeSelector` rendered as node affinity (Strimzi's pod template has no `nodeSelector`), both logging shapes, a choice of metrics type, a metric contract checked in CI, SLO recording rules, a dashboard layout that is computed and tested, and a 1,371-line CI workflow that enforces all of it.

`kafka-cluster` and `connect-cluster` got none of those lessons. The study turned up defects that **fail silently**: fields the API server prunes without an error, alerts that can never fire, a production overlay that blocks its own Kafka traffic, and a tiered-storage flag that does nothing while the backup design assumes it works.

So "refactor" has three concrete goals:

1. **One implementation of each shared concern.** Worker spec, client auth, KafkaUser ACLs, secret sync, NetworkPolicy fragments, monitoring wrappers and rails move into a library chart. `mirror-maker2`'s versions are the starting point because they are the tested ones.
2. **Correct against the API it targets.** Every rendered Strimzi CR is validated against the pinned CRDs across every overlay and feature toggle. Every alert, recording rule and panel is checked against a metric contract.
3. **One owner per resource.** Cluster-scoped singletons (CRDs, drain cleaner, operator scrape) belong to the operator chart. A workload's KafkaUser, NetworkPolicy and dashboard belong to the workload's chart. The cluster chart stops provisioning for its consumers.

The contract stays the same as in the MirrorMaker 2 plans: *the Strimzi CRs are the deployment contract, the charts parameterise them, and the CLI drives the charts.*

---

## 2. The study

### 2.1 Inventory

| | `kafka-cluster` | `connect-cluster` | `mirror-maker2` | `kates` (reference app chart) |
|:---|:---|:---|:---|:---|
| Template lines (templates + tests, excl. NOTES) | 4,108 | 2,001 | 5,164 | — |
| `values.yaml` lines / `# --` annotations | 908 / 50 | 564 / 55 | 748 / 106 | 738 / 222 |
| Schema objects with `additionalProperties: false` | 1 | 0 | 28 (top level `true`) | 0 |
| Overlays | 6 | 4 | 14 | 9 |
| Helm test pods | 11 | 1 (+ connector CRs) | 5 | 3 |
| Alert rules | 17 | 8 | 10 (incl. the SLO burn alert) | 1 file |
| Dashboards | 6 boards in 3 files (2 are Connect boards) | 1 | 2 | 1 (+ Kyverno) |
| Metric contract | — | — | `scripts/metric-contract/mirror-maker2.yaml` | — |
| CI beyond "lint + template defaults" | Kyverno render only | none (`ci-connect.yml` covers the image) | `ci-mirror-maker2.yml`: rails, CRD validation, promtool, layout, render diff, kind e2e, live scrape | `ct.yaml` (only `charts/kates`) |

### 2.2 What already works (and this plan keeps)

- **`kates`**: `global.imageRegistry` / `global.clusterDomain`, the `productionMode` guard that fails a render when insecure defaults would ship, helm-docs `# --` on every key, and Kyverno policies with dev-namespace exceptions. These are the platform conventions the Kafka charts should follow.
- **`kafka-cluster`**: the `metadataVersion ≤ kafkaVersion` rail (`kafka-cluster.validateVersions`), share-group keys gated on Kafka ≥ 4.2 (`kafka-cluster.kafkaConfig`), the `kates.io/kafka-floor` annotation checked by `scripts/check-versions.sh`, `extraLabels`, NetworkPolicy selectors derived from `strimzi-<clusterName>`, the SeaweedFS placeholder-credential guard, and `values-additional.yaml` with its explanation of why each shared resource is off.
- **`connect-cluster`**: auto-derived least-privilege ACLs, the structured `networkPolicy` block, `tests.expectedPlugins` (a broken plugin jar becomes a test failure), the KubernetesSecretConfigProvider always on, and the REST API Service/Ingress.
- **`mirror-maker2`**: `mirror-maker2.validate` (floating tags, KIP-896 floor, RF vs broker count, identity overlap, tasks vs replicas, reverse-direction clash), the `lookup`-preserved `replicas`, `nodeSelector` → `nodeAffinity`, `metrics.type`, `connectContainer`, the preflight Job, `secretSync` with `as:`, the offset-syncs location model, recording rules and SLO, the cursor-computed dashboard grid, runbook anchors, and the chart's `docs/` folder.

### 2.3 Defects found

Severity: **S1** silently loses protection or data path · **S2** a documented feature does nothing, or the CR is rejected · **S3** misleading or dead configuration.

#### `kafka-cluster`

| ID | Sev | Where | What happens |
|:---|:---|:---|:---|
| K1 | S1 | `templates/tiered-storage.yaml`, `values-prod.yaml` | `tieredStorage.enabled` renders only a Secret and a `kafka-tiered-storage-config` ConfigMap that **nothing references**. `spec.kafka.tieredStorage` is never set, so brokers never offload. The properties also name `RemoteLogManager` as the RSM class (it is Kafka's internal manager, not a storage plugin), and the stock Strimzi image has no S3 RSM. `values-prod.yaml` enables it, and `templates/backup.yaml` **excludes broker PVCs** on the premise that tiered storage protects their data (l.8). The prod shape therefore protects broker data with neither mechanism. The backup is narrower still: the Schedule's `labelSelector` (`strimzi.io/controller-role=true`) also filters out the ConfigMaps, Secrets and Strimzi CRs it lists, and with `snapshotVolumes: false`, `defaultVolumesToFsBackup: false` and no fs-backup opt-in annotation anywhere, only the controller PVC *objects* are saved, not their data. The endpoint is wrong too: the SeaweedFS subchart names its Services after the chart (`seaweedfs-filer`, `seaweedfs-s3`), while this chart publishes `<clusterName>-seaweedfs-filer` and prod hardcodes `kafka-cluster-seaweedfs-filer`. |
| K2 | S2 | `nodepool-{brokers,controllers}.yaml` l.79 | `sysctl.enabled` adds `template.pod.initContainers`. The field is not in Strimzi's `PodTemplate`, so the API server prunes it with a warning. The privileged tuner never runs. Rendered: `unknown field .spec.template.pod.initContainers`. |
| K3 | S2 | `values.yaml`, `values-kind.yaml` | Both render **zero** `KafkaNodePool`s. `values-kind.yaml` expects pools from `.build/values-detected.yaml` (`kates detect`) or from `values-dev.yaml` layered under it (`deploy-kafka.sh` l.56); the dev, ci and additional overlays carry two pools and staging/prod four. A plain `helm install` with the base values creates a `Kafka` that can never become Ready, and no rail says why. |
| K4 | S2 | `templates/prometheusrule.yaml` | Read against the chart's own exporter rules, **11 of 17 alerts can never fire**: `KafkaISRShrinkRate` and `KafkaRequestHandlerSaturated` (Yammer meters expose `Count`/`*Rate`, and the `kafka.server` rules match only `Value`), `KafkaLogFlushLatencyHigh` (no rule produces a `LogFlushStats` percentile), both `KafkaRaft*` rules (`raft-metrics` has no `name=` key), both disk rules (`node_filesystem_*{mountpoint=~"/var/lib/kafka.*"}` never matches, because node-exporter sees kubelet volume paths), `StrimziOperatorDown` (the PodMonitor selects nothing, see K5, and its job label `<ns>/cluster-operator-metrics` does not match `.*strimzi.*cluster-operator.*` either), `CruiseControlAnomalyDetected` (no `kafka.cruisecontrol` rule), and both certificate rules (the operator publishes `strimzi_certificate_expiration_timestamp_ms`, per the vendored operator dashboard, not `strimzi_certificate_not_after`). `KafkaActiveControllerCount` (`!= 1` per series) **fires permanently** for every non-leader controller. That leaves 5 working rules. No rule is scoped to its cluster, so two clusters double-fire. |
| K5 | S3 | `templates/podmonitors.yaml` l.5 | `cluster-operator-metrics` selects operator pods in the **Kafka** namespace, where the operator does not run. `kafka-resources-metrics` also selects `KafkaConnect`/`KafkaMirrorMaker2` pods, which their own charts already scrape. |
| K6 | S3 | `values.yaml` l.739–788, l.642, `values-prod.yaml` l.94 | Dead values: `strimziOperator.enabled` and the whole `strimzi-kafka-operator:` block (the chart has no such dependency); `crdUpgrade.image` (the hook uses `testImages.kubectl`); `kafkaConnect.enabled` in the prod and staging overlays (declared in `values.schema.json` l.382 but read by no template); `networkPolicies.monitoringNamespace` and `allowedClientNamespaces` (never used, while the templates hardcode `monitoring`, `kates` and `litmus`). Removing `strimziOperator.enabled` also means editing `ci.yml` l.304, which sets it. |
| K7 | S2 | `templates/crd-upgrade.yaml` | A per-cluster chart holds a cluster-wide CRD write grant and downloads CRDs from GitHub at install time, which breaks air-gapped installs. `curl -sL` has no `-f`, so an HTTP error page is saved and handed to `kubectl apply`, which fails with a YAML error instead of the real cause. This duplicates `strimzi-operator`'s own hook, which already uses `curl -fsSL` and judges a bundle by its content. |
| K8 | S1 | `templates/drain-cleaner.yaml` | A cluster singleton (fixed-name ClusterRole and ValidatingWebhookConfiguration) lives inside a per-cluster chart. `values-prod.yaml` enables it with one replica and no `tls.secretName` or `caBundle`. The webhook cannot be called, `failurePolicy: Ignore` lets every eviction through, and drain protection is silently off. `STRIMZI_DRAIN_ZOOKEEPER` is obsolete, and `STRIMZI_NAMESPACE` limits it to one namespace. |
| K9 | S3 | throughout | Fixed resource names (`kafka-metrics`, `default-deny`, `allow-dns`, `kafka-brokers`, the PodMonitors, `kafka-cluster-alerts`, the dashboards, `full-rebalance`) force **one cluster per namespace**. `values-additional.yaml` documents this as a constraint. |
| K10 | S2 | `templates/rebalance.yaml` l.21 | A standing `add-broker-rebalance` with `brokers: []` is invalid for `add-brokers` mode and sits NotReady. `cruiseControl.autoRebalance` references no rebalance template, so the `goals` list never reaches auto-rebalancing. |
| K11 | S3 | `values.yaml` users, `networkpolicies.yaml`, `grafana-dashboards*.yaml` | The cluster chart provisions for its consumers: KafkaUsers for `kafka-ui`, `apicurio-registry`, `litmus-chaos`, `kates-connect` and `kates-mm2`; NetworkPolicies for Connect and MM2 pods; two Connect dashboards. `connect-cluster` and `mirror-maker2` can create the same KafkaUsers (`kafkaUser.create`), so turning that on is a Helm ownership error. |
| K12 | S3 | `nodepool-controllers.yaml` | Controller pools ignore per-pool `resources`/`jvmOptions` (broker pools honour them). `deleteClaim` comes only from the defaults. Storage is always one JBOD volume. The two templates are about 90% identical. |
| K13 | S3 | `kafka.yaml` l.44 | `lifecycle.preStopSleepSeconds` only sets the Kafka-level `template.pod.terminationGracePeriodSeconds`. Every pool sets its own `template.pod`, which Strimzi uses instead, so the value never reaches a pod **(confirm in Phase 0)**. There is also no preStop hook to sleep in. |
| K14 | S3 | `values.yaml` listeners | The base values expose a `nodeport` external listener on every install. Only the kind overlay removes it. |
| K15 | S3 | `networkpolicies.yaml` | Listener ports are hardcoded (9091–9094, plus ZooKeeper's 2181) rather than derived from `kafka.listeners`, so a custom listener is blocked. |

#### `connect-cluster`

| ID | Sev | Where | What happens |
|:---|:---|:---|:---|
| C1 | S2 | `kafka-connect.yaml` | `autoscaling.enabled=true` omits `spec.replicas`, which is **required** in `v1`. The CR is rejected. Rendered: `missing required .spec.replicas`. `mirror-maker2` already solves this with `lookup`. |
| C2 | S2 | `kafka-connect.yaml` l.135 | `nodeSelector` renders into `template.pod.nodeSelector`, which Strimzi's pod template does not have. It is pruned and pods schedule anywhere. |
| C3 | S2 | `values.schema.json` l.65, l.358 | The schema offers auth `oauth` and `tls-external`, and tracing `jaeger`. The `v1` enums are `tls`/`scram-sha-256`/`scram-sha-512`/`plain`/`custom` and `opentelemetry`, so those renders are rejected. `plain` passes the schema but renders `type: plain` with no username or password. `custom` (the OAuth path in `v1`) is not offered. |
| C4 | S1 | `values-prod.yaml` l.66 | Prod keeps TLS off, so bootstrap is `:9092`, but it narrows Kafka egress to `9093`. **Prod Connect workers cannot reach Kafka.** |
| C5 | S2 | `alerts-connect.yaml` | **4 of 8 rules cannot fire.** `KafkaConnectRebalanceStorm` and `KafkaConnectSourceLag`: exporter 1.x strips `_total` from GAUGE rules, the same bug fixed in MM2 0.7. `KafkaConnectTaskCountMismatch`: the two sides have different label sets and never match. `KafkaConnectWorkerHeapHigh`: `jvm_memory_bytes_*` was renamed to `jvm_memory_*_bytes`. `KafkaConnectWorkerDown` (critical) **fires on any worker that holds no connectors**, which includes every worker of a default install (`connectors: []`). Only the heap rule is scoped to its release, so the others double-fire across Connect groups. The per-connector worker rule's greedy `connector=(.+)>` also captures `orders><` rather than `orders`, which corrupts the `connector` label on every per-connector worker series. |
| C6 | S2 | `secret-reader-rbac.yaml` | The config-provider RBAC exists only when `serviceAccount.create` is true, although Connect runs as the Strimzi-made `<name>-connect` account either way. The default grants get/list/watch on **all** Secrets in the namespace. With `kafka.namespace` set, it also grants all Secrets in the Kafka namespace: CA keys and every other user's password. |
| C7 | S3 | `values.yaml` | `internalTopics.prefix` is never read. `monitoring.enabled` is never read either (only `podMonitors.enabled` gates the PodMonitor, while `monitoring.*` holds its scrape settings). `testConnectors` hardcode the `connect` namespace and `krafter-kafka-bootstrap.kafka.svc`. |
| C8 | S3 | `validate-connectors.yaml` | A pre-install hook Pod (`busybox:1.36`, a pin nothing checks) runs checks whose inputs are all known at render time. |
| C9 | S3 | `values.yaml` l.143 | `priorityClassName: system-cluster-critical` by default for a tenant workload (the same in `mirror-maker2` l.387). |
| C10 | S3 | `Chart.yaml` | `appVersion` is the Debezium line (`3.6.2`), while `mirror-maker2`'s is the Kafka line. Tooling cannot read the Kafka version from either chart consistently. |

#### `mirror-maker2`

| ID | Sev | Where | What happens |
|:---|:---|:---|:---|
| M1 | S2 | `_helpers.tpl` `clusterTlsAuth`, schema `definitions/auth` | `custom` authentication (§3.7 of the 0.4 plan) never landed. The schema refuses it and the helper has no branch for it, so OAuth sources and targets cannot be mirrored. |
| M2 | S1 | `kafka-mirror-maker2.yaml` l.307 | `templateExtra` is written with `toYaml` next to the chart's own `pod:`. `templateExtra.pod` makes a **duplicate `pod` key**, and the last one wins. The chart's security context, affinity, tolerations and spread constraints are then dropped without error. Reproduced with `--set templateExtra.pod.hostUsers=false`. |
| M3 | S3 | l.256 | `terminationGracePeriodSeconds: 30` is hardcoded. |
| M4 | S3 | `values.yaml` | `testImages.kubectl` is `kates-tester:1.21.0` (also in `kafka-cluster`), while `connect-cluster` moved to `1.22.0` in #143. `check-versions.sh` does not compare them. |

### 2.4 Cross-cutting drift

- **Three copies of every helper, each slightly different.** Bootstrap FQDN: `kafka-cluster` uses the release namespace, `connect-cluster` the Connect namespace, `mirror-maker2` the literal `kafka`. Client auth: two implementations with different coverage (C3, M1). Semver normalisation: two identical copies. KafkaUser ACL builders, secret sync, NetworkPolicy fragments, and the PodMonitor, PrometheusRule and dashboard wrappers each exist two or three times.
- **Values vocabulary differs.** `global.clusterDomain` vs `clusterDomain`. `networkPolicies` vs `networkPolicy`. `dashboards` vs `dashboard`. `podMonitors` + `monitoring` vs `podMonitors.scrape`. `keepOnDelete` exists in two charts of three, and `global.imageRegistry` in one.
- **Schemas are permissive.** Nearly nothing sets `additionalProperties: false`, so a misspelled key is accepted silently. Declared-but-unread keys (K6's `kafkaConnect`) show the other side of the same gap: the schema and the templates are not checked against each other.
- **CI renders defaults only, and checks shape, not meaning.** `ci.yml` runs `kubeconform -ignore-missing-schemas` against a community catalogue on each chart's **default** values. The pruned and rejected fields (K2, C1–C3, M2) live behind toggles and overlays that are never rendered. The rest (K3, K4, K10, C5, C6) render on defaults but are semantic errors that no schema check can see. Only `mirror-maker2` has negative renders, a metric contract or promtool.
- **Dashboard sprawl.** Six boards in `kafka-cluster` (two of them for Connect), one in `connect-cluster` — all three Connect boards ship under the same file key, `kafka-connect.json`, which collides in the Grafana sidecar — and eight Kafka JSONs in `charts/monitoring/dashboards` (`kafka-dashboard`, `-comprehensive`, `-working`, `-all-metrics`, …) with overlapping content.
- **The umbrella is incomplete.** `kates-platform` has no `connect-cluster`, and its `mirror-maker2` range (`>=0.2.0`) admits versions with the bugs 0.4–0.7 fixed.

---

## 3. Target architecture

### 3.1 Principles

1. **Nothing is pruned silently.** Every Strimzi CR from every overlay and every documented toggle validates strictly against the pinned CRDs in CI.
2. **Fail at render time, and name the way out.** A misconfiguration that is knowable from values is a `fail`, never a hook Pod and never a NotReady CR an hour later.
3. **Every series a rule or panel reads is proven producible**, statically by a metric contract and live by a scrape job.
4. **One owner per resource.** A resource that must exist exactly once per Kubernetes cluster lives in `strimzi-operator`.
5. **Namespaced names carry the instance.** Two clusters, or two Connect groups, can share a namespace.
6. **MM2's tested implementation wins** when the three charts disagree, unless this plan says otherwise.

### 3.2 Chart topology and ownership

```
charts/
  kafka-common/        NEW  library: names, labels, rails, client auth, KafkaUser,
                            secret sync, Connect-worker spec, HPA, NetworkPolicy
                            fragments, PodMonitor/PrometheusRule/dashboard wrappers
  strimzi-operator/    0.3  operator + CRDs (sole owner) + drain cleaner + operator scrape/alerts
  kafka-cluster/       1.0  Kafka, node pools, topics, users, rebalance templates, quotas,
                            tiered storage, broker-side policies, Kafka observability
  connect-cluster/     2.0  KafkaConnect + KafkaConnectors on kafka-common's worker spec
  mirror-maker2/       0.8  KafkaMirrorMaker2 on kafka-common's worker spec
  kates-platform/      0.7  + connect-cluster (condition, off)
```

| Resource | Owner today | Owner after |
|:---|:---|:---|
| Strimzi CRDs | `strimzi-operator` hook **and** `kafka-cluster` hook | `strimzi-operator` only |
| Drain cleaner (Deployment, webhook, RBAC) | `kafka-cluster` | `strimzi-operator` (upstream `strimzi-drain-cleaner` chart as a conditional dependency, certificates from cert-manager or the chart) |
| Operator PodMonitor and `StrimziOperatorDown` | `kafka-cluster` (selects nothing) | `strimzi-operator` |
| Operator NetworkPolicy | `kafka-cluster` (`operatorPolicy`) or `strimzi-operator` | `strimzi-operator` only (`operatorPolicy` removed from `kafka-cluster`) |
| KafkaUser `kates-connect` | `kafka-cluster` `users[]` (+ optional `connect-cluster`) | `connect-cluster` (`kafkaUser.create: true` by default) |
| KafkaUser `kates-mm2` | `kafka-cluster` `users[]` (+ optional `mirror-maker2`) | `mirror-maker2` |
| KafkaUsers `kafka-ui`, `apicurio-registry`, `litmus-chaos`, `kates-backend` | `kafka-cluster` defaults | `kafka-cluster`, moved from `values.yaml` to a `values-platform.yaml` profile until each consumer chart carries a `kafkaUser` block (Phase 6) |
| Platform topics (`kates-*`, `cdc-*`, `test-sink-topic`) | `kafka-cluster` defaults | `values-platform.yaml` profile |
| NetworkPolicy for Connect / MM2 pods | `kafka-cluster` **and** their own charts | their own charts only; `kafka-cluster` keeps broker **ingress** from declared clients |
| Connect dashboards | `kafka-cluster` (2) + `connect-cluster` (1) | `connect-cluster` (1) |
| Kafka dashboards | `kafka-cluster` (4) + `monitoring` (8 JSONs) | `kafka-cluster`; the `monitoring` Kafka JSONs are deprecated, then removed |

### 3.3 The `kafka-common` library chart

`type: library`, consumed as `file://../kafka-common` and published to `oci://ghcr.io/bmscomp/charts` beside the others. Every template is prefixed `kafka-common.`. Callers pass `(dict "ctx" $ ...)`, never `.Values` paths, so the library assumes no key names.

| Template | Replaces | Notes |
|:---|:---|:---|
| `names.name` / `names.fullname` / `names.namespace` / `names.chart` | three copies | `namespaceOverride` supported everywhere |
| `labels.standard` / `labels.selector` / `labels.component` | three copies | builds a dict with `mergeOverwrite` so duplicate keys cannot occur (the MM2 `componentLabels` lesson), then `extraLabels` |
| `semver` / `versionAtLeast` | `kafka-cluster.semver`, `mirror-maker2.semver` | |
| `image` | `kates.image`, ad-hoc refs | `global.imageRegistry` rewrite, digest-aware |
| `rails.noFloatingTag` | MM2 validate | applied to every image a chart owns |
| `bootstrap` | three variants | one rule: explicit `bootstrapServers` wins, else `<clusterName>-kafka-bootstrap.<namespace>.svc.<domain>:<port>`, where port comes from `tls.enabled` or an explicit `listenerPort`. The **namespace default is a caller argument**, and each chart documents its own. |
| `clientAuth` | `mirror-maker2.clusterTlsAuth`, inline Connect block | `tls`, `scram-sha-256/512`, `plain` (username + passwordSecret), `custom` (`sasl`, `config`, with secrets mounted through `template.connectContainer.volumeMounts`). Fixes C3 and M1. |
| `rails.clientAuth` | — | refuses a `custom` block with neither `sasl: true` + `sasl.*` config nor `ssl.keystore.*` config, `plain`/`scram` with no username, and `tls` auth with no TLS |
| `kafkaUser` | two ACL builders | takes a normalised ACL list plus fragment helpers: `acl.connectInternalTopics`, `acl.workerGroup`, `acl.exactlyOnce`, `acl.offsetSyncs`, `acl.clusterDescribe` |
| `secretSync` | Connect job, MM2 job + CronJob | the MM2 implementation (post-install, tracked RBAC, `as:`, optional `watch`), which Connect then gains |
| `connectWorker.spec` | two worker specs | see below |
| `hpa` | two HPAs | `scaleTargetRef.kind` is a parameter |
| `replicasPreserved` | MM2 inline logic | `lookup` of the live CR under an HPA, with `minReplicas` as the seed |
| `netpol.dns` / `.apiServer` / `.kafkaEgress` / `.monitoringIngress` / `.operatorIngress` / `.workerToWorker` / `.extra` | three hand-written sets | `kafkaEgress` derives its ports from the bootstrap string unless given, which fixes C4 by construction |
| `monitoring.podMonitor` / `.prometheusRule` / `.dashboardConfigMap` | three wrappers | the rule wrapper injects `runbook_url` from a base URL and an anchor. `promSelector` returns the `namespace="…", pod=~"<name>-…"` matcher every expr must carry. |
| `grafana.grid` | MM2 cursor logic | panel positions computed, never written |
| `test.pod` / `test.clientProps` | MM2 test helpers | restricted security context, pull policy, `dash`-safe property writing |

**`connectWorker.spec`** renders the spec body shared by `KafkaConnect` and `KafkaMirrorMaker2`: `version`, `image`, `replicas` (via `replicasPreserved`), `resources`, `jvmOptions`, probes, `logging` (inline or external, MM2's shape), `metricsConfig` (`jmxPrometheusExporter` or `strimziMetricsReporter`), `tracing` (`opentelemetry` only), `rack` + `clientRackInitImage`, `jmxOptions`, and `template`. The template is built as **one dict** and deep-merged (`mustMergeOverwrite`) with the caller's `templateExtra`, then emitted once. That fixes M2 as a class. The pod part covers `securityContext`, `imagePullSecrets`, `priorityClassName`, `terminationGracePeriodSeconds`, `tolerations`, `affinity` (built from `nodeSelector` → node affinity, `nodeAffinity`, `podAntiAffinity`), `topologySpreadConstraints`, `hostUsers` and `tmpDirSizeLimit`. `connectContainer` carries env (tracing env included), `securityContext` (hardened default, merged rather than replaced) and `volumeMounts`. The PDB is emitted only when the effective replica count is above 1.

**Why a library and not a drift check.** The copies are not identical today (§2.4), so a script that diffs them would first have to pick a winner, which is this work anyway. After that the library removes the chance of new drift. The cost is packaging: each consumer chart vendors the library on `helm dependency build`, which CI already runs for `kafka-cluster`.

**Testing a library.** A library cannot be templated alone. `charts/kafka-common/tests/harness/` is a tiny application chart that calls every template, and `helm unittest` suites run against it. The harness is not published.

### 3.4 One values vocabulary

| Concept | `kafka-cluster` today | `connect-cluster` today | `mirror-maker2` today | All three after |
|:---|:---|:---|:---|:---|
| Registry / pull secrets / DNS domain | `global.*` (registry, domain) | `imagePullSecrets`, `clusterDomain` | `imagePullSecrets`, `clusterDomain` | `global.imageRegistry`, `global.imagePullSecrets`, `global.clusterDomain` |
| Prod guard | — | — | — | `productionMode` (as in `kates`) |
| Uninstall safety | always `keep` | `keepOnDelete` | `keepOnDelete` | `keepOnDelete` (Kafka and pools stay `keep` regardless) |
| NetworkPolicy root | `networkPolicies` | `networkPolicy` | `networkPolicy` | `networkPolicy` |
| Scrape | `podMonitors` | `podMonitors` + `monitoring` | `podMonitors` + `monitoring` | `monitoring.podMonitor.{enabled,labels,interval,scrapeTimeout,relabelings}` |
| Metrics format | `kafka.metricsConfig.type` | fixed JMX | `metrics.{enabled,type,allowList}` | `metrics.{enabled,type,allowList}` |
| Alerts | `alerts.{enabled,labels}` | `+ thresholds` | `+ thresholds, slo, runbookBaseUrl` | the MM2 shape |
| Dashboards | `dashboards.*` | `dashboards.*` | `dashboard.*` | `dashboards.{enabled,namespace,label,labelValue,folder}` |
| Test images | `testImages.{kafka,bash,kubectl}` | `testImages.kubectl` | `testImages.kubectl` | `testImages.{kafka,kubectl}`, pinned by `versions.env` and checked |

Old keys stay readable for one major line through a `kafka-common.compat` shim that maps them and prints a `DEPRECATED:` line in NOTES. The schemas keep the old keys for that line, marked `"deprecated": true`.

**Schemas.** Every object the chart owns gets `additionalProperties: false`. Pass-through objects (`config`, `template`, `templateExtra`, `connectContainer`, `build`) stay open. Each chart gets the negative test `strimzi-operator` already has: a misspelled key must fail the render.

---

## 4. `kafka-cluster` 1.0.0

### 4.1 Node pools: one model, working defaults

```yaml
nodePools:
  # Merged under every pool; role defaults are merged over these.
  defaults:
    storage:
      type: jbod
      volumes:
        - { id: 0, type: persistent-claim, size: 100Gi, deleteClaim: false }
    scheduling:
      zoneKey: topology.kubernetes.io/zone
      spread: { enabled: true, maxSkew: 1, whenUnsatisfiable: ScheduleAnyway }
      antiAffinity: { enabled: true, topologyKey: kubernetes.io/hostname }
      tolerations: []
      priorityClassName: ""
    securityContext: {}          # merged over the hardened default
    sysctls: []                  # namespaced sysctls only (see below)
    template: {}                 # raw KafkaNodePool.spec.template, deep-merged last
  roleDefaults:
    controller: { replicas: 3, resources: {...}, jvmOptions: {...}, storage: { volumes: [{ id: 0, size: 10Gi, kraftMetadata: shared }] } }
    broker:     { replicas: 3, resources: {...}, jvmOptions: {...} }
  pools:
    - name: controllers
      roles: [controller]
    - name: brokers
      roles: [broker]
    # A pool may pin a zone and override anything above:
    # - name: brokers-az1
    #   roles: [broker]
    #   zone: az1
    #   replicas: 1
    #   storage: { volumes: [{ id: 0, size: 200Gi, class: gp3-az1 }] }
```

- **Working defaults.** A plain `helm install` renders three controllers and three brokers on the default StorageClass (fixes K3). `kates detect --generate-values` emits `nodePools.pools`, and because Helm replaces lists, the detected pools replace the defaults cleanly.
- **One template.** `templates/nodepools.yaml` renders every pool through `kafka-cluster.nodePool`, with precedence *pool > roleDefaults[role] > defaults*, merged with `mustMergeOverwrite`. Controllers now honour per-pool overrides (K12). **Dual-role pools** (`roles: [controller, broker]`) become possible, which lets kind and `values-additional.yaml` run one pod instead of two.
- **Per-pool storage.** The full `storage` object passes through, including `kraftMetadata: shared`, `volumeAttributesClass`, several JBOD volumes and ephemeral storage for CI.
- **Sysctls, honestly (K2).** The init container is removed. `sysctls` renders into `template.pod.securityContext.sysctls`, which works only for namespaced sysctls. A rail refuses `vm.*` and other node-level keys and names node tuning (a DaemonSet or the node image) as the place for `vm.max_map_count`.
- **Rails.** No pool with the `controller` role, or none with `broker`, fails. So do duplicate pool names, a `zone` without `scheduling.zoneKey`, and `replicas: 0` on the only pool of a role.
- **Compatibility.** `brokerPools`, `controllerPools`, `brokerDefaults` and `controllerDefaults` are translated by `kafka-cluster.legacyPools` for the 1.x line. A NOTES line names the new keys. Setting both shapes at once fails.

### 4.2 The `Kafka` CR surface

- Remove the `strimzi.io/node-pools` and `strimzi.io/kraft` annotations. In `v1` node pools and KRaft are the only mode.
- Expose what the `v1` schema offers and the chart hides: `kafka.image`, `kafka.logging`, `kafka.jmxOptions`, `kafka.quotas`, `maintenanceTimeWindows`, `cruiseControl.apiUsers`, `cruiseControl.template`, and a `kafka.template` pass-through deep-merged with the chart's hardened container context.
- **Quotas.** `kafka.quotas` uses `type: strimzi` with `minAvailableRatioPerVolume`, which stops producers before a disk fills. The prod profile sets `0.1`. The per-user quotas in `users[]` stay as they are.
- **Metrics.** `metrics.type: strimziMetricsReporter` becomes available for Kafka. The JMX exporter stays the default until the contract (§8) covers the reporter's names, the same deferral MM2 made. Cruise Control accepts only `jmxPrometheusExporter`, and `kafka.yaml` l.75 reuses the Kafka type for it today, so the two are rendered separately and Cruise Control always keeps the exporter.
- **Listeners.** The base values drop the NodePort listener (K14) and gain `externalAccess` presets (`nodeport`, `loadbalancer`, `ingress`), each off by default. Prod turns one on explicitly.
- **Lifecycle (K13).** Remove `lifecycle.preStopSleepSeconds`. Add `nodePools.defaults.scheduling.terminationGracePeriodSeconds`, rendered on each pool's `template.pod`, where it takes effect.

### 4.3 Tiered storage and backup (K1)

```yaml
tieredStorage:
  enabled: false
  image: ""                      # REQUIRED when enabled: a Kafka image that carries the RSM plugin
  remoteStorageManager:
    className: ""                # e.g. the Aiven S3 RSM class
    classPath: ""                # e.g. /opt/kafka/plugins/tiered-storage/*
    config: {}                   # storage.* keys; Strimzi adds the rsm.config. prefix
  credentials:
    existingSecret: ""           # mounted as env through template.kafkaContainer.env[].valueFrom
  metadata: { replicationFactor: 3 }
  topicDefault: true             # adds remote.storage.enable=true to every chart-managed topic
  localRetentionMs: 86400000
```

- Render `spec.kafka.tieredStorage` (`type: custom`) and set `spec.kafka.image` from `tieredStorage.image`. Put `remote.log.storage.system.enable`, `rlmm.config.remote.log.metadata.topic.replication.factor` and `log.local.retention.ms` into `kafka.config`. Delete the unreferenced ConfigMap.
- **Rails.** `enabled` without `image`, `className` or credentials fails. An image equal to the operator default fails too, because the stock image has no S3 RSM.
- **SeaweedFS endpoint.** Compute it once, from the subchart's own Service naming (`seaweedfs-s3`, or `<seaweedfs.nameOverride>-s3`; not the release or cluster name), publish it in `<cluster>-object-store` and reuse it everywhere. `values-prod.yaml` stops hardcoding it. A rail refuses two releases enabling the subchart in one namespace, since its names do not carry the instance.
- **Backup follows reality.** The Schedule's `labelSelector` stops filtering the ConfigMaps, Secrets and Strimzi CRs (a separate `orLabelSelectors` entry keeps `strimzi.io/cluster` scoping), and controller volumes get the fs-backup opt-in they need. `backup.yaml` excludes broker PVCs **only** when tiered storage is actually enabled and valid. Otherwise it includes them, and NOTES say which layer protects broker data. `backup.enabled` with neither layer fails under `productionMode`.
- **Helm test `test-tiered-storage`.** Produce past `segment.bytes` to a `remote.storage.enable` topic, wait for `RemoteLogSizeBytes > 0`, and read the offloaded offset back.
- **0.4.x hotfix (Phase 1).** Until the wiring ships, `backup.yaml` stops excluding broker PVCs and NOTES say tiered storage is not active.

### 4.4 Rebalancing (K10)

- Render the `KafkaRebalance` **templates** (`strimzi.io/rebalance-template: "true"`) for `add-brokers` and `remove-brokers`, carrying `rebalance.goals`, throttles and `excludedTopics`. Reference them from `cruiseControl.autoRebalance[].template.name`.
- Remove the standing `add-broker-rebalance`. A full rebalance becomes opt-in (`rebalance.full.enabled`, off), with an optional `strimzi.io/rebalance-auto-approval` annotation.
- **Rail.** A `rebalance.*` setting while `cruiseControl.enabled` is false fails with the reason.

### 4.5 Names and multiple clusters per namespace (K9)

Every namespaced resource is prefixed `<clusterName>-`: metrics ConfigMap, NetworkPolicies, PodMonitors, PrometheusRule, dashboards, rebalance templates, RBAC and the object-store ConfigMap. Default-deny and DNS policies keep the derived `strimzi-<clusterName>` selector.

**Upgrade.** Renaming the metrics ConfigMap changes `spec.kafka.metricsConfig`, which rolls the brokers. `compatibility.legacyResourceNames` (default `false`) keeps the 0.4 names. The CLI sets it to `true` when it upgrades an existing cluster, and the operator clears it in a maintenance window. `values-additional.yaml` loses its one-namespace-per-cluster warning.

### 4.6 Topics and users

- `topics` and `users` become **maps keyed by name** (`topics.items.<name>: {...}`). An overlay can then change one topic's partitions without restating the whole list, which is why `values-additional.yaml` has to turn topics off today. Lists stay accepted in 1.x.
- `topics.defaults` supplies `replicas` (defaulting to the broker count capped at 3), `min.insync.replicas` and optional `remote.storage.enable`. Topics gain `topicName`, per-item `enabled`, and a rail that refuses `replicas` above the rendered broker count.
- `users.items.<name>` gains `template` (Secret labels and annotations, used by reflector setups) and `enabled`.
- **Defaults move.** The platform's topics and users move from `values.yaml` to `values-platform.yaml`, which `deploy-kafka.sh`, the CLI and `kates-platform` layer on. `kates-connect` and `kates-mm2` leave this chart (§3.2).
- `kafka.authorization.superUsers` stops listing `kates-backend` by default. The platform profile grants it explicit ACLs instead **(decision; see §11)**.

### 4.7 NetworkPolicies (K5, K6, K11, K15)

```yaml
networkPolicy:
  enabled: true
  defaultDeny: { enabled: true, selector: {} }        # derived selector when empty
  dns: { enabled: true, namespaceSelector: {}, podSelector: {} }
  apiServer: { enabled: true, ports: [443, 6443], ipBlock: {} }
  monitoring: { namespace: monitoring }                # actually used
  operatorNamespace: strimzi-operator
  clients:                                             # replaces 6 special-cased keys
    - name: kates
      namespace: kates
      podSelector: { app.kubernetes.io/name: kates }
      listeners: [plain]                               # listener NAMES → ports from kafka.listeners
    - name: connect
      namespace: connect
      podSelector: { strimzi.io/kind: KafkaConnect }
      listeners: [plain, tls]
    - name: mirror-maker2
      namespace: kafka
      podSelector: { strimzi.io/kind: KafkaMirrorMaker2 }
      listeners: [plain, tls]
  extraIngress: []
  extraEgress: []
```

- Broker ingress ports are **derived from `kafka.listeners`** by listener name. An unknown listener name in `clients[]` fails the render. Port 2181 goes away.
- Remove the `kafka-connect`, `kafka-mirror-maker` and `strimzi-operator` policies. They belong to the charts that own those pods (§3.2).
- Every rule built from the `monitoring` namespace reads `networkPolicy.monitoring.namespace` (today the value is ignored and `monitoring` is hardcoded).
- The platform `clients[]` list lives in `values-platform.yaml`. The base values allow in-cluster traffic only.

### 4.8 Observability (K4, K5)

- **Exporter rules.** Replace the hand-rolled ConfigMap with the Strimzi 1.2.0 `examples/metrics/kafka-metrics.yaml` rule set, vendored with its source version in a header comment and diffed in CI when `STRIMZI_VERSION` moves. It covers meters (`…PerSec` `Count` as COUNTER), `Percent` meters, timers, and `raft-metrics`.
- **PodMonitors per component, per cluster.** Brokers and controllers, kafka-exporter, cruise-control, and entity-operator (the `healthcheck` port). Each selects on `strimzi.io/cluster: <clusterName>` plus the component label. The operator and Connect/MM2 selectors go away (K5).
- **Alerts rebuilt the MM2 way.** Every expr carries `namespace` + `strimzi_io_cluster`. Every rule carries a `runbook_url` into a new `docs/kafka-cluster-runbook.md`. Thresholds move to `alerts.thresholds`. The replacements:
  - `KafkaActiveControllerCount` → `sum by (namespace, strimzi_io_cluster) (…activecontrollercount) != 1`
  - disk rules → `kubelet_volume_stats_available_bytes / kubelet_volume_stats_capacity_bytes` on `persistentvolumeclaim=~"data-.*-<cluster>-.*"`
  - ISR shrink, handler idle and log flush → the series the upstream rules produce
  - raft → `kafka_server_raftmetrics_*`
  - certificate expiry → `strimzi_certificate_expiration_timestamp_ms` (the name the vendored operator dashboard reads), alerted from `strimzi-operator`
  - `StrimziOperatorDown` → moves to `strimzi-operator`
  - new: under-min-ISR partitions, offline log directories, a broker without controller contact, request-queue saturation, and kafka-exporter lag per group
- **Recording rules and SLO.** `kafka:under_replicated_partitions:sum`, `kafka:produce_p99_ms`, `kafka:disk_free_ratio:min`, and an optional availability SLO (`alerts.slo`) with a multi-window burn-rate alert, as in MM2.
- **Dashboards.** One canonical Kafka board (a health header; brokers; KRaft quorum; topics and partitions; requests; clients and quotas; disk and tiered storage; Cruise Control), built on `kafka-common.grafana.grid`, with thresholds drawn from `alerts.thresholds`. KRaft and Cruise Control become sections, not separate boards. The two Connect boards are removed.
- **Metric contract.** Add `scripts/metric-contract/kafka-cluster.yaml` (§8).

### 4.9 What leaves the chart

| Leaves | Goes to | Note |
|:---|:---|:---|
| `crd-upgrade.yaml`, `crdUpgrade.*` | `strimzi-operator` | K7. A release that still sets `crdUpgrade.enabled` fails with a pointer. |
| `drain-cleaner.yaml`, `drainCleaner.*` | `strimzi-operator` (`drainCleaner.enabled`, upstream chart) | K8. Prod gets 2 replicas, a PDB and cert-manager certificates. |
| `strimziOperator`, `strimzi-kafka-operator:` values | deleted | K6 |
| `podSecurityPolicy` (Kyverno ClusterPolicy, fixed name) | `kyvernoPolicy` in this chart, name-prefixed, using the `kates` chart's key names | The name no longer suggests PSP. It gains `policyExceptions` as in `kates`. |
| `grafana-dashboards-connect.yaml`, the Connect board in `-advanced` | `connect-cluster` | K11 |
| `kafka-connect`, `kafka-mirror-maker` NetworkPolicies | the workload charts | K11 |

### 4.10 Rails

| Rail | Fails when |
|:---|:---|
| metadata ≤ Kafka (exists) | `kafka.metadataVersion` is newer than `kafkaVersion` |
| pools present | no controller-role pool, or no broker-role pool |
| pool shape | duplicate names, both legacy and new pool keys set, `zone` without `zoneKey` |
| sysctls | a non-namespaced sysctl key |
| topic RF | a topic's `replicas` exceeds the broker count |
| internal RF | `offsets.topic.replication.factor`, `transaction.state.log.replication.factor` or `default.replication.factor` exceeds the broker count |
| `min.insync.replicas` | ≥ `default.replication.factor` (no write availability during a roll) |
| tiered storage | enabled without image, class or credentials; enabled with the stock image |
| backup coverage | `productionMode` with `backup.enabled` and no layer protecting broker data |
| listeners | a `clients[].listeners` entry names no listener |
| Cruise Control | `rebalance.*` or `autoRebalance` with `cruiseControl.enabled: false` |
| moved features | `crdUpgrade.enabled` or `drainCleaner.enabled` is set (points at `strimzi-operator`) |
| floating tags | any chart-owned image is `:latest` or untagged |
| productionMode | a placeholder S3 secret, a NodePort listener without TLS, or `deleteClaim: true` |

---

## 5. `connect-cluster` 2.0.0

### 5.1 The worker spec comes from the library

`templates/kafka-connect.yaml` shrinks to metadata, `bootstrapServers`/`groupId`/storage topics, `config`, `build`/`plugins`, and `include "kafka-common.connectWorker.spec"`. This fixes C1 (`replicas` preserved under the HPA), C2 (`nodeSelector` becomes affinity) and the M2 class of bugs in one move, and brings `metrics.type`, `jmxOptions`, `clientRackInitImage`, `connectContainer` and `templateExtra` to Connect.

### 5.2 Kafka connection and authentication (C3)

- `kafka.authentication` goes through `kafka-common.clientAuth`. The type enum is `tls | scram-sha-256 | scram-sha-512 | plain | custom`. `oauth` and `tls-external` are removed, with a migration note mapping `oauth` → `custom`.
- `tracing.type` accepts `opentelemetry` only.
- The bootstrap default namespace stays "the Connect namespace". This is documented beside MM2's (`kafka`), and the README shows both.
- **Exactly-once becomes a first-class flag:** `exactlyOnce.enabled` (default `true`, which keeps today's behaviour). It sets `exactly.once.source.support` and the transactional-ID ACL. Sniffing `extraConfig` goes away, and `extraConfig` may no longer set that key (rail).

### 5.3 Plugins

- `plugins:` renders `spec.plugins` (`v1`, OCI image-volume artifacts). Plugins then ship as images mounted at start-up, with no custom image and no build. A rail requires `.Capabilities.KubeVersion` to be at least the release where the `ImageVolume` feature is available and an explicit `plugins.acknowledgeImageVolume: true`, because the feature gate is a cluster property the chart cannot see.
- `build` stays for registries that need it. `image` stays the recommended path. A table in the README compares the three.
- `tests.expectedPlugins` is derived from `plugins[].expect` when set.
- **`Chart.yaml` (C10):** `appVersion` becomes the Kafka version, as in MM2. The image's plugin set moves to an annotation (`kates.io/connect-image: connect:3.6.2-kafka-4.3.1`), and `check-versions.sh` asserts it matches `images.env`.

### 5.4 Connectors

```yaml
connectorDefaults:            # merged under every connector's spec
  autoRestart: { enabled: true, maxRestarts: 10 }
  config:
    errors.tolerance: none
    errors.log.enable: "true"
connectors:                   # map: overlays can change ONE connector
  orders-cdc:
    class: io.debezium.connector.postgresql.PostgresConnector
    tasksMax: 1
    state: running            # running | paused | stopped
    version: ""               # v1 per-connector plugin version
    listOffsets: {}
    alterOffsets: {}
    deadLetterQueue: { enabled: false, topic: "", replicationFactor: 3 }   # sink only
    config: { ... }
```

- A list still renders in 2.x, with a NOTES deprecation line.
- `deadLetterQueue.enabled` sets `errors.tolerance=all`, `errors.deadletterqueue.topic.name`, RF and headers. It renders a `KafkaTopic` for the DLQ in the Kafka namespace and adds the topic to the auto ACLs.
- **Validation moves to render time (C8).** The hook Pod and its ConfigMap are deleted. `kafka-cluster.validate`-style `fail`s check: the name is DNS-1123, `class` is set, `state` is valid, `tasksMax ≥ 1`, required keys per known class (a table in `_helpers.tpl` for Debezium PostgreSQL/MySQL/MongoDB/SQL Server, Debezium JDBC sink and Aiven JDBC source), no plaintext `password` keys when `productionMode` is on (a `${secrets:…}` reference is required), and a DLQ only on sink classes.
- `testConnectors` go through `tpl`, so `{{ .Release.Namespace }}` and the computed bootstrap can be used in values (C7).

### 5.5 Secrets and RBAC (C6)

- The config-provider Role binds to the Strimzi `<name>-connect` account whether or not `serviceAccount.create` is set. `serviceAccount.create` now controls only the test-pod account.
- **Auto-scoped by default.** The chart scans every connector's config for `${secrets:<ns>/<name>:<key>}` (`regexFindAll`) and renders one Role per referenced namespace, with `resourceNames` limited to the referenced Secrets and `get` only. `rbac.secretNames` adds names. `rbac.allSecrets: true` restores the old breadth, and `productionMode` refuses it.
- The cross-namespace Role never grants namespace-wide access.
- Secret sync moves onto `kafka-common.secretSync` (MM2's implementation), which adds `as:` and the optional rotation CronJob. Its image moves off `testImages` to `secretSync.image`.

### 5.6 Network policy (C4)

- Kafka egress ports are **derived from the rendered bootstrap string** unless `networkPolicy.kafka.ports` is set. **Rail:** a bootstrap port outside the allowed ports fails with both numbers. `values-prod.yaml` is fixed: either TLS on or 9092 allowed. The Kafka chart's internal TLS listener (9093) uses **mutual-TLS** authentication, so turning on TLS alone would fail SCRAM authentication. **The plan picks TLS with `kafka.authentication.type: tls`**, and the `KafkaUser` created by the chart (`kafkaUser.create`) switches to `tls` with it. A rail refuses `kafka.tls.enabled: true` with SCRAM on the computed in-cluster bootstrap (port 9093, mutual TLS in `kafka-cluster`) unless `kafka.bootstrapServers` names a different listener.
- `databaseEgress` becomes `networkPolicy.egress.databases` (same shape). Schema-registry and tracing egress stay as they are.

### 5.7 Observability (C5)

- **Exporter rules.** Use the Strimzi 1.2.0 `connect-metrics` rule set, with COUNTER rules for cumulative `*-total` attributes (the MM2 0.7 fix) and non-greedy, quote-safe `connector`/`task` captures (C5).
- **Alerts** (all scoped with `kafka-common.monitoring.promSelector`, all with `runbook_url` into a new `docs/connect-cluster-runbook.md`):
  - `ConnectWorkerDown`: `up{…} == 0`, or fewer ready workers than replicas for 5 minutes. This replaces the connector-count rule that fires on idle workers.
  - `ConnectConnectorFailed`: `kafka_connect_connector_metrics{status="failed"}`.
  - `ConnectTaskFailed`: per connector.
  - `ConnectTasksNotRunning`: running task count below total task count, per connector.
  - `ConnectSourceIdle`: `rate(…source_record_poll_total[15m]) == 0` for running source connectors. Opt-in, because an idle source can be normal.
  - `ConnectSinkLag`: from kafka-exporter lag on `connect-<connector>` groups.
  - `ConnectErrorsLogged`, `ConnectDeadLetterWrites`, `ConnectOffsetCommitFailures`, `ConnectRebalanceStorm` (on the `_total` COUNTER), `ConnectWorkerHeapHigh` (both JVM metric generations, as in MM2).
- **Recording rules and SLO.** `connect:tasks_running:ratio`, `connect:records_processed:rate5m`, and an optional task-availability SLO.
- **Dashboard.** One board on the grid helper: a health header, connectors and tasks table, source and sink throughput, errors and DLQ, offset commits, workers and rebalances, JVM, and the client path.
- **Metric contract.** Add `scripts/metric-contract/connect-cluster.yaml`.

### 5.8 Tests and preflight

- Split `test-connect.yaml` into tiers: CR Ready, REST reachable from an allowed client, expected plugins, a connector round trip (the existing `testConnectors`), and a DLQ round trip when enabled.
- Adopt MM2's **preflight** Job (`preflight.enabled`, off by default). It reports `PROTOCOL → DNS → TLS → AUTH → LISTENER → NETWORK` verdicts against the Kafka cluster before the CR exists.
- `priorityClassName` defaults to `""` (C9). Prod sets a chart-provided `kates-streaming` PriorityClass (`priorityClass.create`, off by default).

### 5.9 Rails

Floating tags, client auth coherence, a bootstrap port outside the egress ports, `maxReplicas` above the sum of connectors' `tasksMax` (MM2's idle-worker rail, with `allowIdleWorkers`), connector validation (§5.4), `rbac.allSecrets` under `productionMode`, `exactly.once.source.support` in `extraConfig`, `plugins` without the acknowledgement, `internalTopics.*` names that collide across the three topics, and `config.replicationFactor` above `kafka.brokerCount` when that is declared.

---

## 6. `mirror-maker2` 0.8.0

MM2 is the reference, so its changes are few and mostly subtractive.

- **Move onto the library.** `connectWorker.spec`, `clientAuth`, `kafkaUser`, `secretSync`, the netpol fragments, the monitoring wrappers, `grafana.grid`, `test.*` and the rails come from `kafka-common`. About 450 lines of `_helpers.tpl` go. **Gate:** every one of the 14 overlays renders byte-identically, apart from the three fixes below, when compared with the 0.7.1 render diff the CI job already produces.
- **M1:** `custom` authentication for target and sources, through the library. The schema's `definitions/auth` gains `custom`, and the preflight probe reports `AUTH` as "not verified (custom)" rather than failing.
- **M2:** `templateExtra` is deep-merged, never appended. The negative render that reproduced the duplicate key becomes a CI assertion.
- **M3:** `terminationGracePeriodSeconds` becomes a value (default 30).
- **M4:** `testImages.kubectl` aligns to the `versions.env` pin, and `check-versions.sh` asserts all three charts agree.
- `priorityClassName` defaults to `""` (C9), and `values-prod.yaml` keeps the explicit class.
- **Heartbeats, documented across charts.** The README's non-goal ("needs separate KafkaConnect + KafkaConnector resources") gains a working recipe: a `connect-cluster` release with one `MirrorHeartbeatConnector` in `connectors`, which is now expressible.
- `networkPolicy.kafka.perSource` stays. The target egress switches to the library's port derivation.

---

## 7. `strimzi-operator` 0.3.0, `kates-platform` 0.7.0 and `monitoring`

- **`strimzi-operator`.** It takes the drain cleaner (upstream `strimzi-drain-cleaner` chart as a conditional dependency pinned in `versions.env`; `drainCleaner.enabled`; prod: 2 replicas, PDB, cert-manager `Certificate`), the operator PodMonitor, the `StrimziOperatorDown` and certificate-expiry alerts, and sole CRD ownership. Its CRD hook already judges bundles by content and uses `curl -fsSL`. It gains an offline mode that applies the CRDs vendored in the dependency tgz, so no network is needed at install.
- **`kates-platform`.** Add `connect-cluster` (`condition: connect-cluster.enabled`, off). Add `kafka-common` transitively. Tighten ranges to `kafka-cluster >=1.0.0 <2.0.0`, `connect-cluster >=2.0.0 <3.0.0`, `mirror-maker2 >=0.8.0 <1.0.0`. Layer `kafka-cluster/values-platform.yaml` as the umbrella's defaults for the `kafka-cluster` key.
- **`monitoring`.** Mark the eight Kafka JSONs in `charts/monitoring/dashboards/` deprecated in 1.x NOTES, add a `legacyKafkaDashboards.enabled` flag (default `false` after one release), and remove them in the next major. The kates, chaos and benchmark boards stay.

---

## 8. Validation and CI

The MM2 chart job is already the right shape. The plan generalises it rather than inventing a second one.

1. **Strict CRD validation from the pinned CRDs.** Convert `strimzi-kafka-operator-<STRIMZI_VERSION>/crds/*.yaml` to JSON Schema at CI time (`openapi2jsonschema`, output in `.build/schemas/`) and run `kubeconform -strict -schema-location .build/schemas/...` **without** `-ignore-missing-schemas` for `kafka.strimzi.io`. This replaces the community-catalogue lookup for Strimzi kinds in `ci.yml` and `ci-mirror-maker2.yml`. Keep a small `scripts/check-strimzi-crs.py` for the one thing kubeconform cannot say clearly: which field was pruned (Appendix A).
2. **A render matrix, not defaults.** Each chart gets `ci/*.yaml` files, one per documented toggle (HPA, nodeSelector, every auth type, tiered storage, sysctls, reporter metrics, plugins, DLQ, fan-in, …), plus every `values-*.yaml`. Everything is validated as in (1). `ct.yaml` gains the three charts and the library harness.
3. **Negative renders.** For every rail in §4.10, §5.9 and MM2's list, a render that must fail, with the message asserted. This lifts `ci-mirror-maker2.yml`'s "Safety rails must reject bad input" step into a reusable workflow (`.github/workflows/chart-validation.yml`, `workflow_call` with a `chart` input) that all three charts call.
4. **`helm unittest`** for `kafka-common` (through the harness) and for the highest-risk templates: `nodepools.yaml`, `kafka.yaml`, `kafka-connect.yaml`, `connectors.yaml`, and the RBAC scoper. This closes P4 of `docs/chart-enhancement-plan.md`.
5. **Metric contracts** for `kafka-cluster` and `connect-cluster`, wired into `scripts/check-metric-contract.sh` and `make check-metric-contract`. promtool `check rules`, rule unit tests for the SLO rules, dashboard PromQL parsing, layout checks and runbook-anchor resolution all run for the two new charts.
6. **Live scrape jobs** (manual trigger plus weekly), modelled on `metrics-live`: a single-node `kafka-cluster` with the exporter, and a `connect-cluster` with one FileStream connector. Each job diffs the captured metrics against the contract.
7. **Render diff against the previous chart version** (the MM2 step "renders the previous version's overlays additively"). For the breaking majors the job produces a reviewed diff artifact rather than a pass/fail.
8. **Schema typo tests.** A misspelled key must fail for each chart, as `strimzi-operator` already does.
9. **Docs gates.** Regenerate the chart table (`scripts/gen-chart-table.sh`), the version matrix (the generator `docs.yml` calls) and the helm-docs READMEs. `check-chart-test-paths.sh` covers the new test files.

---

## 9. Phases and acceptance criteria

**Phase 0 — Safety net** (before any refactor)
- [ ] `chart-validation.yml` exists and runs strict CRD validation over every overlay and a toggle matrix for the three charts. It reproduces K2, C1, C2, C3 and M2 as **expected failures** listed in the workflow.
- [ ] Golden renders of every overlay for kafka-cluster 0.4.0, connect-cluster 1.3.3 and mirror-maker2 0.7.1 are committed under `.build/golden/` (CI artifact) as the render-diff baseline.
- [ ] Draft metric contracts for `kafka-cluster` and `connect-cluster` list K4 and C5 as known dead references.
- [ ] A kind run confirms K13 (pool `template.pod` replaces the Kafka-level one) and that `strimzi_certificate_expiration_timestamp_ms` is scraped from the operator. Both answers are written back into §2.3.

**Phase 1 — Hotfixes on the current lines** (patch releases, no refactor)
- [ ] kafka-cluster **0.4.1**: `sysctl.enabled` fails with the explanation. `backup.yaml` stops excluding broker PVCs while tiered storage is not wired, and NOTES say so. `KafkaActiveControllerCount` uses `sum`. Dead values are deleted from `values.yaml` and `values-prod.yaml`. The crd-upgrade `curl` gets `-f`. The drain cleaner refuses to render without `tls.secretName` or webhook annotations.
- [ ] connect-cluster **1.3.4**: `replicas` is preserved under the HPA. `nodeSelector` becomes affinity. The schema enum is corrected (and `plain` renders credentials). The prod overlay's Kafka port is fixed, plus the bootstrap-port rail. `KafkaConnectWorkerDown` is replaced. `_total` COUNTER rules are added, and the per-connector capture stops swallowing `><`. The heap alert reads both JVM names.
- [ ] mirror-maker2 **0.7.2**: `templateExtra` is deep-merged, and a CI assertion covers the duplicate key.
- [ ] The Phase 0 expected-failure list is empty for these items.

**Phase 2 — `kafka-common` and MM2 on it**
- [ ] `kafka-common` 0.1.0 is published. Harness unit tests cover every template in §3.3.
- [ ] mirror-maker2 0.8.0 consumes it. All 14 overlays render byte-identically to 0.7.2, apart from M1/M3/M4 and `priorityClassName`. The MM2 CI job passes unchanged apart from new assertions.
- [ ] `custom` auth renders for target and source and passes strict validation.

**Phase 3 — kafka-cluster 1.0.0**
- [ ] `nodePools` model with defaults. A plain `helm install` on kind reaches `Ready`. `kates detect` output (new shape) renders. The legacy keys translate, and the translation is tested.
- [ ] Every namespaced resource is prefixed. Two clusters install into one namespace on kind. `legacyResourceNames` renders the 0.4 names.
- [ ] Tiered storage is wired. `test-tiered-storage` passes against SeaweedFS with an RSM image. The backup/tiered coupling rail is tested both ways.
- [ ] Rebalance templates are referenced by `autoRebalance`. No standing invalid `KafkaRebalance`.
- [ ] Upstream exporter rules are in place. PodMonitors are per component. The metric contract is green. Every alert is scoped, has a runbook anchor, and passes promtool. The canonical dashboard passes the layout check.
- [ ] CRD hook, drain cleaner and operator scrape are removed and point to `strimzi-operator` 0.3.0.
- [ ] `values-platform.yaml` carries the platform topics, users and clients, and the base values are generic.

**Phase 4 — connect-cluster 2.0.0**
- [ ] Worker spec from the library. HPA, nodeSelector, all auth types and reporter metrics validate strictly.
- [ ] The connectors map, DLQ, `version`/`listOffsets`/`alterOffsets`, and render-time validation are in. The hook Pod is gone.
- [ ] Secret RBAC is auto-scoped from `${secrets:…}` references. A connector that references an unlisted Secret gets exactly one `get` grant.
- [ ] `plugins` renders and is gated. `appVersion` is the Kafka version, and the image annotation is checked.
- [ ] Alerts, recording rules, dashboard and metric contract are green. The live scrape job passes. The preflight Job is available.

**Phase 5 — Parity and SLOs**
- [ ] Kafka and Connect SLO sections (optional) mirror MM2's. Runbooks exist for every alert in all three charts.
- [ ] The live scrape jobs for all three charts run weekly.

**Phase 6 — Consumers and docs**
- [ ] `kates detect --generate-values` emits `nodePools`. `kates clusters add` drops the one-namespace rule. The CLI sets `legacyResourceNames` on upgrades and performs KafkaUser adoption (§10).
- [ ] `deploy-kafka.sh`, the Makefile targets and `check-versions.sh` (test images, library version, connect image annotation) are updated.
- [ ] `kates-platform` 0.7.0. The `monitoring` Kafka JSONs are deprecated.
- [ ] Upgrade guides `docs/kafka-cluster-1.0-upgrade.md` and `docs/connect-cluster-2.0-upgrade.md`. Book appendix, chart table and version matrix regenerated. Per-chart `docs/` folders follow MM2's layout.

Rough size: Phase 0 ≈ 600 lines (workflow, contracts, matrix); Phase 1 ≈ 300; Phase 2 ≈ 900 (library and harness) − 450 (MM2 helpers); Phase 3 ≈ 1,800 net (templates, alerts, dashboard, tests); Phase 4 ≈ 1,200; Phase 5 ≈ 300; Phase 6 ≈ 700 (CLI, scripts, docs).

---

## 10. Upgrade and migration

| Change | Chart | Migration |
|:---|:---|:---|
| Pool keys → `nodePools` | kafka-cluster 1.0 | automatic translation in 1.x; `kates detect` regenerates |
| Resource names prefixed | kafka-cluster 1.0 | `compatibility.legacyResourceNames: true` until a maintenance window (the metrics ConfigMap rename rolls brokers) |
| CRD hook / drain cleaner removed | kafka-cluster 1.0 | enable them in `strimzi-operator` 0.3.0 **first**; the rail blocks the Kafka upgrade until those keys are cleared |
| Platform topics/users/clients moved to a profile | kafka-cluster 1.0 | add `-f values-platform.yaml` (the scripts, CLI and umbrella do it) |
| `kates-connect` / `kates-mm2` KafkaUsers change release | kafka-cluster 1.0 → connect 2.0 / MM2 0.8 | **adoption, not recreation** (see below) |
| `oauth`/`tls-external`/`jaeger` removed | connect 2.0 | `oauth` → `custom`; the others had never produced a valid CR |
| `connectors` list → map | connect 2.0 | lists still render in 2.x with a NOTES line |
| Secret RBAC narrowed | connect 2.0 | references are discovered automatically; `rbac.secretNames` covers indirect ones |
| `priorityClassName` default `""` | connect 2.0, MM2 0.8 | prod overlays set it explicitly |

**KafkaUser adoption.** Deleting and recreating a `KafkaUser` makes the User Operator **issue a new SCRAM password**, which breaks every client holding the old one. The 0.4 objects carry `helm.sh/resource-policy: keep`, so removing them from `kafka-cluster` leaves them in place, and the new owner must adopt them:

```sh
kubectl -n kafka annotate kafkauser kates-connect --overwrite \
  meta.helm.sh/release-name=connect-cluster meta.helm.sh/release-namespace=connect
kubectl -n kafka label kafkauser kates-connect --overwrite app.kubernetes.io/managed-by=Helm
helm upgrade --install connect-cluster charts/connect-cluster -n connect --set kafkaUser.create=true
```

The CLI performs this step (Phase 6), and the upgrade guide documents it. `connect-cluster`'s NOTES detect a `KafkaUser` of the same name owned by another release (`lookup`) and print the two commands instead of letting `helm` fail with "cannot be imported".

---

## 11. Risks and decisions

- **A library chart adds packaging steps.** Every consumer needs `helm dependency build`, and the OCI publish must push `kafka-common` first. The publish workflow orders it, and `check-versions.sh` asserts every consumer pins the published version. Alternative considered: a drift-check script. Rejected in §3.3.
- **The 1.0 and 2.0 majors are real breaks.** Compatibility shims cover one major line only, and the rails name every removed key. The majors ship after the Phase 1 patches, so users who cannot move yet still get the S1 fixes.
- **Working pool defaults can surprise someone** who relied on "nothing renders without detect". The defaults are three plus three with no zone pin, which is what a production-leaning install wants. Detected values still replace them wholesale.
- **Tiered storage needs an image this repo does not build yet.** Phase 3 either adds `Dockerfile.kafka-tiered` (Strimzi Kafka plus the Aiven RSM) to the image pipeline, or documents a supported external image. Until then `values-prod.yaml` keeps tiered storage **off**, and backup covers broker PVCs. That is the honest state.
- **Dropping `kates-backend` from `superUsers`** narrows what the backend can do (chaos, admin operations). The platform profile needs an audited ACL set first. If that audit slips, the profile keeps `superUsers` and the base values drop it: the generic chart stops granting it, the platform keeps its behaviour.
- **`ImageVolume` availability varies by cluster.** Plugins stay opt-in behind an acknowledgement. `image` remains the recommended path.
- **Scoped alerts change alert identity.** Alertmanager routes that match on the old names need updating. Each chart's changelog annotation lists the renames.
- **Upstream exporter rules change series names** compared with the hand-rolled set, so any external dashboard reading the old names breaks. The deprecated `monitoring` Kafka JSONs are the main such consumer, which is one more reason to retire them in the same line.
- **Strimzi may move first.** When a future operator adds a field the chart implements through `config:` or a pass-through, the chart follows the API in the release that can, as the MM2 plan already commits.

## 12. Non-goals

- Rewriting `legacy-kafka`, `kafka-ui` or `apicurio-registry`, beyond the KafkaUser hand-over contract in §3.2.
- A general Connect "connector catalogue" chart. `connectors` stays values-driven.
- KEDA or lag-driven autoscaling in any chart. It is named in the READMEs, as the MM2 plan does.
- Managing KafkaUsers on clusters the release does not own (MM2 sources stay a documented contract plus preflight).
- ZooKeeper anything: Strimzi 1.x is KRaft-only, and this plan removes the last references.
- Replacing the MM2 runbook or the migration plans. This plan is the charts' half of that work.

---

## Appendix A — How the findings were verified

Everything below ran against the repository at `a9bca9c` with Helm v3.18.4 and the vendored `charts/strimzi-operator/charts/strimzi-kafka-operator-1.2.0.tgz`.

```sh
# strict field check: every key in a rendered Strimzi CR must exist in the v1 CRD schema
helm template t charts/kafka-cluster -f charts/kafka-cluster/values-dev.yaml \
  --set brokerDefaults.sysctl.enabled=true | python3 check-strimzi-crs.py
#   KafkaNodePool/brokers-dev: unknown field .spec.template.pod.initContainers           (K2)

helm template t charts/connect-cluster --set autoscaling.enabled=true | python3 check-strimzi-crs.py
#   KafkaConnect/t-connect-cluster: missing required .spec.replicas                     (C1)
helm template t charts/connect-cluster --set nodeSelector.pool=connect | python3 check-strimzi-crs.py
#   KafkaConnect/t-connect-cluster: unknown field .spec.template.pod.nodeSelector       (C2)
helm template t charts/connect-cluster --set kafka.authentication.type=oauth | python3 check-strimzi-crs.py
#   enum violation .spec.authentication.type='oauth'                                    (C3)
helm template t charts/connect-cluster --set tracing.type=jaeger | python3 check-strimzi-crs.py
#   enum violation .spec.tracing.type='jaeger'                                          (C3)

# zero node pools on defaults and on the kind overlay                                   (K3)
helm template t charts/kafka-cluster | grep -c 'kind: KafkaNodePool'                     # 0
helm template t charts/kafka-cluster -f charts/kafka-cluster/values-kind.yaml | grep -c 'kind: KafkaNodePool'   # 0

# tiered storage renders no spec.kafka.tieredStorage and nothing references the ConfigMap (K1)
helm template t charts/kafka-cluster --set tieredStorage.enabled=true | grep -c 'tieredStorage:'  # 0

# prod Connect: bootstrap :9092, Kafka egress 9093 only                                 (C4)
helm template t charts/connect-cluster -f charts/connect-cluster/values-prod.yaml | grep bootstrapServers
helm template t charts/connect-cluster -f charts/connect-cluster/values-prod.yaml -s templates/networkpolicies.yaml

# duplicate `pod:` key under spec.template                                              (M2)
helm template t charts/mirror-maker2 --set templateExtra.pod.hostUsers=false \
  -s templates/kafka-mirror-maker2.yaml   # a duplicate-key-aware YAML loader reports ['pod']

# MM2 refuses custom auth                                                               (M1)
helm template t charts/mirror-maker2 --set target.authentication.type=custom
#   target.authentication.type must be one of ... "scram-sha-512", "scram-sha-256", "tls", "plain", ""
```

`check-strimzi-crs.py` (about 40 lines) walks each rendered `kafka.strimzi.io` object against the `v1` `openAPIV3Schema` of the matching CRD and reports unknown fields, missing required fields, enum violations and scalar type mismatches. It is the seed of §8 item 1. The alert findings (K4, C5) come from reading each `expr` against the exporter rules in the chart's own ConfigMap, using the naming behaviour `scripts/metric-contract/mirror-maker2.yaml` documents. Phase 0's contracts turn those readings into a CI result.

## Appendix B — File map

| Today | After |
|:---|:---|
| `kafka-cluster/templates/nodepool-brokers.yaml`, `nodepool-controllers.yaml` | `nodepools.yaml` + `_nodepool.tpl` |
| `kafka-cluster/templates/tiered-storage.yaml` | folded into `kafka.yaml` (+ credentials Secret only when not `existingSecret`) |
| `kafka-cluster/templates/metrics-configmap.yaml` | `metrics-configmap.yaml` (upstream rules, prefixed name) |
| `kafka-cluster/templates/podmonitors.yaml` | `podmonitors.yaml` (per component, via library) |
| `kafka-cluster/templates/prometheusrule.yaml` | `alerts.yaml` + `recording-rules.yaml` |
| `kafka-cluster/templates/grafana-dashboards*.yaml` (3 files, 6 boards) | `dashboard.yaml` (1 board) |
| `kafka-cluster/templates/rebalance.yaml` | `rebalance-templates.yaml` (+ opt-in full rebalance) |
| `kafka-cluster/templates/pod-security-policy.yaml` | `kyverno-policies.yaml` (+ `kyverno-policy-exceptions.yaml`) |
| `kafka-cluster/templates/crd-upgrade.yaml`, `drain-cleaner.yaml` | removed → `strimzi-operator` |
| `kafka-cluster/templates/tests/test-connection.yaml` (921 lines, 9 pods) | `tests/test-{connectivity,produce-consume,authorization,kraft,topics,listeners,nodepools,cruise-control,tiered-storage}.yaml` |
| `connect-cluster/templates/validate-connectors.yaml` | removed → `_validate.tpl` |
| `connect-cluster/templates/kafka-user-secret-sync.yaml` | `secret-sync.yaml` (library) |
| `connect-cluster/templates/secret-reader-rbac.yaml` | `rbac-secrets.yaml` (auto-scoped) + `rbac-tests.yaml` |
| `connect-cluster/templates/{alerts,dashboard,podmonitor,metrics-configmap}-connect.yaml` | `alerts.yaml`, `recording-rules.yaml`, `dashboard.yaml`, `podmonitor.yaml`, `metrics-configmap.yaml` |
| `mirror-maker2/templates/_helpers.tpl` (658 lines) | about 200 lines of MM2-specific helpers; the rest from `kafka-common` |
| — | `charts/kafka-common/` (+ `tests/harness/`), `scripts/metric-contract/{kafka-cluster,connect-cluster}.yaml`, `scripts/check-strimzi-crs.py`, `.github/workflows/chart-validation.yml`, `docs/{kafka-cluster,connect-cluster}-runbook.md`, upgrade guides |

---

## Implementation log

Branch `feat/kafka-charts-refactor`, one commit per phase.

### Phase 0 — safety net

- `scripts/check-strimzi-crs.py` validates every rendered `kafka.strimzi.io` object against the CRDs in the pinned operator chart (`charts/strimzi-operator/charts/strimzi-kafka-operator-*.tgz`). It reports pruned fields, missing required fields, enum and type violations, and duplicate mapping keys in any document.
- `scripts/check-chart-matrix.py` with `scripts/chart-matrix/{kafka-cluster,connect-cluster,mirror-maker2}.yaml` renders every overlay and toggle. It runs the CRD check, kubeconform, per-render assertions, a named semantic check (`kafka-egress-covers-bootstrap`), rails, and `promtool check rules` (`--promtool`). `known: <ID>` marks a defect that must still reproduce, and XPASS fails the run. `make check-chart-matrix` runs it offline.
- `scripts/metric-contract/contract.py` gained several exporters per render (`exporters:`, beans tagged `exporter:`), `external:` series patterns, `known_missing:` and `known_label_defects:` with XFAIL/XPASS/STALE, and a check for captures that run past the bean's closing `>`. The new contracts are `kafka-cluster.yaml` and `connect-cluster.yaml`.
- `.github/workflows/ci-kafka-charts.yml` runs all of the above per chart.
- **Findings the tooling added to §2.3:**
  - **K16:** the Kafka chart's six dashboards read 36 series that no rule produces (invented `raftmanager_*`, `connectorcount`, `*persec` and similar names).
  - **K1:** the tiered-storage ConfigMap also renders `app.kubernetes.io/part-of` twice.
  - **C5:** the contract confirms the connector-label run-on.

### Phase 1 — hotfixes (kafka-cluster 0.4.1, connect-cluster 1.3.4, mirror-maker2 0.7.2)

- **kafka-cluster 0.4.1**
  - The sysctl tuner and a drain cleaner without certificates now fail the render.
  - `drainCleaner.tls.certManager` issues the webhook certificate and injects its CA. Prod uses it with 2 replicas and a PDB.
  - The Velero Schedule scopes by `strimzi.io/cluster` only, and NOTES say that tiered storage is not active.
  - `KafkaActiveControllerCount` sums over the quorum. The hook uses `curl -f`.
  - Dead values are gone. The stale `kafkaConnect` blocks in the prod and staging overlays referenced a removed Strimzi field (`externalConfiguration`) and an image from 2024, so they were dropped rather than moved.
  - `networkPolicies.monitoringNamespace` is wired.
- **connect-cluster 1.3.4**
  - `replicas` is preserved with `lookup` under the HPA, and `nodeSelector` becomes node affinity.
  - The auth enum is `tls/scram-*/plain/custom`, and tracing accepts `opentelemetry` only.
  - The bootstrap-port rail is in, and prod allows 9092.
  - Exporter rules and alerts were rewritten and scoped. The metric contract is clean.
- **mirror-maker2 0.7.2**: `templateExtra` is deep-merged into `spec.template`. The rendered template is semantically identical across every overlay, which was checked by comparing the parsed `spec.template` against 0.7.1.
- **New findings, fixed here:**
  - **K17:** the drain-cleaner webhook selected namespaces by `kates.io/namespace`, a label nothing sets, so it matched no eviction at all.
  - **C11:** thirteen `x | default true` switches in connect-cluster could not be turned off, because `false | default true` is `true` in Helm. They include `networkPolicy.defaultDeny.enabled`, `kafkaUser.authorization.enabled`, `autoRestart.enabled` and both converter `schemas.enable` settings. The `nodeAffinity` value also rendered at the wrong indentation and was pruned.
- **Checked locally:**
  - The chart matrices, metric contracts and promtool pass.
  - Every step of `ci-mirror-maker2.yml`'s `chart` job passes, except the install steps and shellcheck.
  - `ci.yml`'s helm-lint job passes once the chart dependencies are built.
  - `check-versions.sh`, `gen-chart-table.sh --check` and `gen-version-matrix.sh --check` pass.

### Phase 2 — kafka-common 0.1.0, mirror-maker2 0.8.0

- **`charts/kafka-common`**
  - The library holds names and labels, the `enabled` boolean helper, semver, the floating-tag and bootstrap-port rails, bootstrap and client auth (`custom` included), `connectWorker.spec`, HPA, PodMonitor with Strimzi relabelings, `promSelector`, dashboard ConfigMap, NetworkPolicy fragments, KafkaUser, secret sync, and the test helpers.
  - `tests/harness` plus 49 `helm unittest` cases cover every template, including the `lookup`-preserved replicas and the refusals.
  - `make kafka-common-test` runs them, and so does the `library` job in `ci-kafka-charts.yml`. `HELM_UNITTEST_VERSION` is pinned in `versions.env`.
- **mirror-maker2 0.8.0**
  - The chart's names, labels, bootstrap, auth, test helpers and floating-tag rail delegate to the library. The worker spec, HPA, PodMonitor, NetworkPolicy fragments, secret sync and KafkaUser come from it too. `_helpers.tpl` shrank from 658 to 538 lines, and the four templates moved onto the library shrank by about 300 lines.
  - **Render check.** The parsed output of 31 renders (every overlay, layered pairs and toggle combinations) is identical to 0.7.2, apart from the listed changes: the hardened container security context, the `priorityClassName` default, the tester pin, the preflight script's `custom` branch, and `custom` authentication itself.
  - The `ci-mirror-maker2.yml` render-diff step lists the two deliberate removals.
- **Deviations from §3.3.**
  - `grafana.grid` stays in mirror-maker2 for now. It moves when connect-cluster's dashboard is rebuilt in phase 4.
  - The KafkaUser template takes a pre-rendered ACL string, so the per-grant comments survive.
- **Outside the charts.**
  - The `kates` CLI runs `helm dependency build charts/mirror-maker2` before every mirror install, cutover and rollback, in `deploy`, `migrate up` and `migrate mirror`. Helm refuses a chart whose declared dependencies are not built. The migrate fakes script the call. `go test ./cmd/...` passes.
  - CI builds the dependency in `ci.yml`, `ci-mirror-maker2.yml` (all three jobs) and `publish-charts.yml`, which now builds every chart's dependencies before packaging. It already needed to, because `helm package` refuses unbuilt dependencies.
  - `ci.yml` skips `helm template` for library charts.
  - `gen-chart-table.sh` and `gen-version-matrix.sh` handle a chart with no `appVersion`: they aborted silently under `set -e` when `grep` found nothing.
  - `full-local-test.sh` builds dependencies before the umbrella and runs the library's unit tests.
  - `make platform-chart-deps` builds mirror-maker2 first and uses `dependency update`, because a stale `Chart.lock` makes `build` refuse a bumped subchart.
- **New finding, fixed here — M5:** `networkPolicy.operator.enabled=false` and `kafkaUser.authorization.enabled=false` were ignored by the same `| default true` bug as C11.

### Phase 3 — kafka-cluster 1.0.0, strimzi-operator 0.3.0

- **kafka-cluster 1.0.0**
  - Every value the templates read is resolved once, in `_resolve.tpl`: the profile, the 0.4 translations, the listeners, the node pools, topics, users and network-policy clients. Every rail is checked in `_rails.tpl`. The templates only print the result. Without the dashboards, the non-test templates shrank from 3 135 to 2 515 lines.
  - **Node pools.** A single `nodePools` block (`defaults`, `roleDefaults`, `pools[]`) replaces `controllerPools`/`brokerPools` and their defaults. The 0.4 shape is translated with its pool names kept. Setting both shapes, a pool list with no controller or no broker, duplicate names, a zone without `zoneKey`, and non-namespaced sysctls all fail the render.
  - **Platform profile.** kates' topics, users, super user and client grants moved out of the base values into `profiles/platform.yaml`, enabled by `profile: platform` (`values-platform.yaml`). A plain install creates only the chart's `<clusterName>-helm-test` user. Topic replicas and `min.insync.replicas` derive from the broker count.
  - **Names.** Resources are named after the cluster. `compatibility.legacyResourceNames` keeps the 0.4 names through an upgrade.
  - **Listeners.** The base values have the internal listeners only; `kafka.externalAccess` adds the external one. NetworkPolicy ports follow the listeners, and `networkPolicy.clients` replaces the per-namespace switches.
  - **Tiered storage** is wired end to end (`type: custom`, the image, the plugin, credentials, `remote.storage.enable` on the managed topics, egress, a Helm test). The rails refuse it without an image carrying a plugin. Following the decision in §11, `values-prod.yaml` keeps it off and backs up the broker volumes (`backup.volumes: fs-backup`) until such an image exists.
  - **Observability.** The exporter rules are Strimzi's, vendored unchanged; `scripts/check-strimzi-metrics.sh` compares them with upstream. There are PodMonitors for every component, and 24 alerts in groups (availability, replication, KRaft, performance, storage, consumers, Cruise Control), recording rules, an optional SLO burn-rate alert, and a runbook anchor on every alert (`docs/kafka-cluster-runbook.md`). The metric contract now also reads the operator's dashboards: 92 references, all produced.
  - Rebalance templates carry `strimzi.io/rebalance-template` and are wired into `cruiseControl.autoRebalance`. The Velero Schedule includes KafkaRebalances, and a pre-upgrade backup hook runs on every upgrade.
  - `productionMode` refuses a NodePort without TLS, `deleteClaim: true`, network policy off, the placeholder SeaweedFS secret, and a backup that protects no broker data.
  - The strict schema lists every 0.4 key as deprecated. NOTES print a `DEPRECATED` line for each one in use.
  - The single test file became twelve (`test-00` … `test-11`). They authenticate as the scoped `<clusterName>-helm-test` user and use a dedicated egress policy.
  - `tests/` holds 14 `helm unittest` cases for the pool translation and the profile merge.
  - Removed: the CRD upgrade hook, the drain cleaner, the six dashboards, and the operator NetworkPolicy.
- **strimzi-operator 0.3.0**
  - The drain cleaner, templated in the chart, with a cert-manager or existing certificate and a PDB. Its webhook selects namespaces from the watch scope.
  - The operator's PodMonitor and alerts: `StrimziOperatorDown`, failing reconciliations, and certificate expiry.
  - The upstream dashboards are enabled by default.
  - `operatorPolicy.enabled` defaults to true, with the ZooKeeper port dropped.
  - Objects that a kafka-cluster 0.4 release still owns are skipped (checked with `lookup`) and named in NOTES.
- **Checks.**
  - The `kafka-cluster` matrix has 32 renders and 31 rails, including the `kates deploy` shape (the `kates detect` fixture, the kind overlay, the profile and the CLI's `--set` flags) and the full 0.4.1 values file. The new `strimzi-operator` matrix has 8 renders and 5 rails.
  - Both matrices run the `runbook-anchors` check, and so does mirror-maker2's.
  - promtool, kubeconform and the CRD check pass.
  - CI adds the vendored-metrics check, the unit tests and a package-content check.
- **Kates deploy compatibility.** The detected values, the kind overlay, the profile and the CLI's flags render the same brokers, pools, topics, users and policies as 0.4.1. Only four things differ:
  - the Kafka CR's annotations;
  - the metrics ConfigMap name (unless `legacyResourceNames` is set);
  - the unused Kafka pod template;
  - the controller anti-affinity selector, which is now scoped to the cluster.
- **Deviations.**
  - **Dashboards.** kafka-cluster ships none. The operator's upstream dashboards replace them, and the contract proves they read produced series. §5.7's grid dashboard is left for phase 5, if it is still wanted.
  - **Drain cleaner.** It is templated in strimzi-operator rather than taken from the upstream chart, which the pinned operator chart does not vendor.
  - **CRDs.** There is no offline CRD mode, because the CRDs exceed a ConfigMap's size limit.
  - **Platform users.** `kates-connect` and `kates-mm2` stay in the platform profile until their charts own them; the upgrade guide describes the adoption.
- **Outside the charts.**
  - `kates deploy` layers `-f charts/kafka-cluster/values-platform.yaml` over the detected values (`cli/cmd/deploy_components.go`). Without it, the install would have no platform topics or users.
  - The deploy scripts layer the file too, and fall back to `helm dependency update` when an old `Chart.lock` makes `build` refuse.
  - `ci.yml`'s Kyverno step reads `kyvernoPolicy.*`.
  - `make kafka-chart-unittest` and `make check-strimzi-metrics` were added.
  - `docs/kafka-cluster-1.0-upgrade.md` covers the upgrade; the book's installation guide, cluster chapter and upgrade playbook were updated.
  - The CLI still copies a `kafka-metrics` ConfigMap into the Connect namespace. That ConfigMap is now `<clusterName>-kafka-metrics`, so the copy only warns; connect-cluster renders its own. This is left for phase 4.
- **New findings.**
  - **K18:** Strimzi's upstream rule 15 captures `$4` into a quoted label value (`known_label_defects`).
  - **K19:** JMX exporter 1.x appends `_total` to COUNTER series, so Strimzi's rate-style series are `…_count_total`; the alerts use those names.
  - **K20:** a drain-cleaner `objectSelector` on `strimzi.io/kind` never matches an eviction (the object is the Eviction, not the pod), so the webhook uses namespaces only.
  - **Constraint:** strimzi-operator cannot depend on `kafka-common`, because the CLI's operator wrapper copies the chart without `charts/`. Its helpers stay local.

### Phase 4 — connect-cluster 2.0.0

- **The chart is on kafka-common.** The worker spec, client authentication, KafkaUser, secret sync, HPA, PodMonitor and NetworkPolicy fragments are the library's. `_resolve.tpl` normalises the values once: the 1.x translations, the connectors, the secret references, the Kafka ports and the secret-sync list. `_validate.tpl` holds the rails. The worker gains `connectContainer`, `templateExtra` (deep-merged), `jmxOptions`, `rack.clientRackInitImage` and `metrics.type`.
- **Connectors.**
  - `connectors` is a map merged over `connectorDefaults`; a 1.x list still renders.
  - `version`, `listOffsets`, `alterOffsets` and `enabled` are passed through or honoured.
  - `deadLetterQueue` sets the error handling and renders a `KafkaTopic` in the Kafka namespace, which the managed user is granted.
  - Validation happens at render time (C8): name, class, state, `tasksMax`, the required keys of ten known classes, `topics` on sinks, the namespace of every `${secrets:…}` reference, a plugin `version` before Kafka 4.1, and plaintext passwords under `productionMode`. The hook Pod and its unpinned busybox image are gone.
  - `testConnectors` are values templates (C7).
- **Secrets (C6).**
  - The chart grants `get` on exactly the Secrets that connectors and test connectors reference, one Role per namespace, bound to `<release>-connect` whatever `serviceAccount.create` says.
  - `rbac.secretNames` takes `name` or `namespace/name`. `rbac.allSecrets` restores 1.x's breadth, and `productionMode` refuses it.
  - The kind and generic overlays list the two Secrets `kates deploy`'s own connectors read.
- **Kafka connection.**
  - `exactlyOnce.enabled` replaces the `extraConfig` line, which is now refused. `internalTopics.prefix` is read (C7).
  - Kafka egress ports follow the bootstrap unless `networkPolicy.kafka.ports` is set. An explicit bootstrap outside the cluster may reach anywhere.
  - The operator namespace defaults to `strimzi-operator`, where `kates deploy` runs it; 1.x used the Kafka namespace.
  - `values-prod.yaml` follows §5.6: the TLS listener, `tls` authentication, a chart-managed user, and the certificate and CA synced with a rotation CronJob. The rails refuse SCRAM on kafka-cluster's mutual-TLS port, and, under `productionMode`, an unencrypted or unauthenticated connection.
- **Plugins.** `plugins` renders `spec.plugins`, gated on `imageVolumes.acknowledged` and Kubernetes 1.31. `plugins[].expect` feeds the Helm test. `appVersion` is the Kafka version (C10); the image pin is the `kates.io/connect-image` annotation, which `publish-connect.yml` moves with `values.yaml` and `check-versions.sh` compares.
- **Observability (C5).**
  - The exporter rules moved unchanged to `files/metrics/connect-metrics.yaml`, and the pod checksum covers only them. The ConfigMap key is `metrics-config.yml`.
  - 14 alerts in two groups, all scoped, all with runbook anchors (`docs/connect-cluster-runbook.md`):
    - new: `ConnectorFailed`, `DeadLetterWrites`, `DeadLetterFailures`, `OffsetCommitFailures`, `SinkLag` (on the declared sinks' groups), and the task-availability SLO;
    - renamed: `TasksNotRunning`, `ErrorsLogged`, `SourceIdle` (now opt-in).
  - Three recording rules.
  - The dashboard is rebuilt with 37 panels in 7 rows on `kafka-common.grafana.layout`, a new library template with harness tests.
  - The metric contract covers 42 references, all produced.
- **Tests and preflight.**
  - The single test pod became tiers: credentials, CR, workers, REST from inside a worker, expected plugins, declared connectors; then the test topics and connectors; then a wait for every test connector to be RUNNING with all tasks.
  - The preflight Job is available. Its probe moved into the library (`kafka-common.preflight.probe`), and mirror-maker2 uses it too: its rendered preflight script is identical for every overlay.
  - A `kates-streaming` PriorityClass is available (C9).
- **Umbrella.** `kates-platform` gains `connect-cluster` (`>=2.0.0 <3.0.0`, condition, off).
- **Checks.**
  - The `connect-cluster` matrix has 27 renders and 32 rails. Among the renders are both `kates deploy` shapes (with the CLI's flags) and a render made of 1.x keys only. `check-chart-matrix.py` gained `kubeVersion`.
  - 25 `helm unittest` cases (connectors, secret scoping, KafkaConnect, KafkaUser); `make connect-chart-unittest` runs them, and the package check now covers connect-cluster.
  - Render comparison with 1.3.4 on the base, dev, prod, kind and generic shapes: the KafkaConnect spec differs only in the metrics key, the `strimzi.io/kind` label in two selectors, the `priorityClassName` default, and the PDB for one worker. Everything else is listed in the upgrade guide.
- **Deviations.**
  - Alert names keep 1.x's `KafkaConnect` prefix rather than §5.7's `Connect…`, so routes change only where an alert was renamed.
  - `kafkaUser.create` stays `false` by default (§3.2 said `true`): the platform profile still owns `kates-connect` until it is adopted, and `values-prod.yaml` turns it on.
  - There is no dead-letter-queue round trip in the Helm tests: it needs a sink that fails on demand, and the test connectors already depend on the platform's demo database.
  - The weekly live scrape job (§8.6) is not added; it needs a cluster.
  - mirror-maker2's dashboard keeps its hand-positioned layout for now (its render-identity gate); only the probe moved.
- **Outside the charts.**
  - `kates deploy` runs `helm dependency build charts/connect-cluster` before installing (`cli/cmd/deploy_components.go`). Without it, Helm refuses the chart.
  - The CLI still sets 1.x keys (`databaseEgress[0]`, `podMonitors.enabled`). They are translated, and NOTES list them as deprecated.
  - The CLI's copy of a `kafka-metrics` ConfigMap into the Connect namespace remains a no-op warning, since the chart renders its own.
  - Other changes: the Makefile (`connect-chart-deps`, `connect-chart-unittest`, prod and kind lint), `ci.yml` (the dependency build), `ci-kafka-charts.yml` (the package check), `publish-connect.yml`, `check-versions.sh`, the book's Kafka Connect chapters and tutorials, `docs/connect-cluster-2.0-upgrade.md`, and the chart table and version appendix.
- **New findings, fixed here.**
  - **C12:** a chart-managed KafkaUser under exactly-once lacked the leader's transactional ID `connect-cluster-<groupId>` (KIP-618). It granted only the `<groupId>` prefix, so the leader could not write task configurations.
  - **C13:** test KafkaTopics were created in the Connect namespace with `strimzi.io/cluster` set to the Connect cluster, so no Topic Operator ever reconciled them.
  - **C14:** 1.x's Helm-test egress policy had no API-server rule, although the test pod is a `kubectl` pod. It also kept `helm.sh/resource-policy: keep`, so it outlived the release.

### Phase 5 — parity, SLOs and the live jobs

- **SLOs and runbooks.** All three charts now carry the MirrorMaker 2 shape: an optional `alerts.slo` with recorded ratios and a multi-window burn-rate alert (replication latency for MM2, request errors for Kafka, task availability for Connect), and a runbook section per alert. Every chart matrix runs the `runbook-anchors` check, so a heading rename fails the build.
- **Live scrape.** `ci-kafka-charts.yml` gained a `metrics-live` job for kafka-cluster and connect-cluster, weekly and on demand, beside mirror-maker2's (which is now weekly too). It stands up the CI cluster, produces and consumes real traffic, and — for Connect — runs two connectors from Kafka's own distribution (a heartbeat emitter and a mirror of one topic back into the same cluster) that need no external system. Each exporter is captured separately.
  - `check-metric-contract.sh` takes `--scrape` more than once, so a chart with several exporters (brokers, controllers, Cruise Control, the Kafka Exporter) is checked against their union.
  - A contract can list `live.absentOk` patterns: references that exist only under a feature the live cluster does not run (tiered storage on kafka-cluster; every sink-task series on Connect, since every sink in the image needs a database or an object store).
  - The values the job layers are `scripts/metric-contract/live/{kafka-cluster,connect-cluster}.yaml`.
- **The `monitoring` chart (1.2.0).** `legacyKafkaDashboards.enabled` gates the eight hand-written Kafka boards and the Strimzi operator & Connect board; they read series that kafka-cluster 1.0's vendored Strimzi rules do not produce. On by default here, listed as DEPRECATED in NOTES, off in 1.3, removed in 2.0. The Kates application, benchmark, trend and chaos boards are unaffected.
- **`check-versions.sh`** now asserts that every Strimzi chart pins the same `kates-tester` image, and that it is the kates chart's `appVersion` (M4).
- **Docs.** The book's Kafka Connect chapters and both Connect tutorials were brought to 2.0 (file layout, connectors map, secret access, alert names, network policy, the `helm test` order), the observability chapter carries the dashboard deprecation, `appendix-b-troubleshooting.md` describes render-time connector validation, and `docs/kafka-connect.md`'s image table matches the pins it documents.

### Review pass — defects found in phases 3–5 and fixed

Two independent reviews of the branch, one per phase group. Everything below was found by them and fixed in the same commit; the checks that missed each one were extended too.

- **kafka-cluster**
  - **A 0.4 release's `backup.snapshotVolumes`/`defaultVolumesToFsBackup` overrode `backup.volumes` from any layer** (both are `false` in 0.4, so an upgrade backed up no volume data however the overlay asked). `backup.volumes` now defaults to empty — a value set anywhere wins, and empty means the translated pair, then `fs-backup`. `metrics.type` got the same treatment, for the same reason.
  - **The `productionMode` `deleteClaim` rail and the "a volume needs a size" rail could not fire on a pool that does not use JBOD storage**, because the resolver dropped everything but `type` there. A single-volume pool is now built and checked like a JBOD one.
  - **A 0.4 values file layered under `values-prod.yaml` could not render**: 0.4's own `external` listener collided with the `externalAccess` preset. The preset now replaces a listener of the same name or port, and says so in NOTES.
  - **`topics.defaults.config["min.insync.replicas"]` was discarded** — the derived value overwrote it. It is derived only when neither the defaults nor the topic set one.
  - **A 0.4 release's `networkPolicies.connectNamespace` (and the other three) placed no client at all** without the platform profile, leaving a default-deny with nothing admitted. That is now refused, with both ways out in the message.
  - `global.imagePullSecrets` is on the chart's ServiceAccount, so the Helm tests and hooks can pull from a private registry (it was documented and read by nothing).
  - NOTES said the Kyverno policy was created where the `kyverno.io/v1` API is absent.
  - **The produce/consume Helm test could not pass on the chart's own defaults and passed anyway**: it created a one-replica topic while the cluster's `min.insync.replicas` is 2, and discarded the producer's stderr. The topic now takes the cluster's replication factor, and a rejected send or a missing record fails the test.
- **connect-cluster**
  - **The preflight Job carried `kates.io/test-pod`,** so the Helm tests' egress policy selected it — and that policy does not admit Kafka. Every `helm upgrade` with `preflight.enabled` would have failed from the second release on.
  - **Connector, topic and dead-letter-queue names were rendered unquoted**, so a name YAML reads as a number or a boolean (`0123`, `on`) produced a CR the API server rejects.
  - **`alerts.thresholds:` written empty (a key with nothing under it) rendered `expr: … >`**, which invalidates the whole PrometheusRule. The defaults are merged in the resolver now.
  - `KafkaConnectSinkLag` is scoped to the Kafka cluster's namespace, the dashboard's `uid` carries a digest of the release name (two releases sharing a prefix overwrote each other's board), and Tier 5 of the Helm test no longer passes a connector that runs no tasks at all.
  - `monitoring.enabled` is described as what it is — never read, in 1.x or 2.x — rather than as a translated key.
- **scripts**: `check-versions.sh`'s tester-image check passed vacuously when its grep matched nothing; it now requires at least the four pins it knows about.
- **Checks added**: five matrix entries (a 0.4 file with the profile, a 0.4 file under the prod overlay, and three rails), and ten unit tests across the two charts.

### Documentation pass — installation tutorials and chapters

The chart work above left the install path documented as it was before phase 3. This pass brought it back in line, and every command in it was verified by running it (`helm dependency build`, then `helm template` with the exact `-f` chain) rather than by reading the templates.

- **`docs/book/20-installation-guide.md`.** The operator section installed `oci://quay.io/strimzi-helm/strimzi-kafka-operator --version 1.1.0`; it now installs `charts/strimzi-operator` (0.3.0 → Strimzi 1.2.0) with the flags `scripts/deploy-kafka.sh` actually passes, and says what the wrapper adds. A new "Build the Chart Dependencies" step replaces the claim that SeaweedFS was kafka-cluster's only dependency. The install command carries `values-platform.yaml` before the environment overlay, and the ordering rule is stated where a reader meets it. A new section installs Connect 2.0 end to end — overlay chain, the resources the release creates, the 2.0 values names, the three-stage `helm test`. Section 11 was rewritten around `networkPolicy.clients` (the 0.4 `networkPolicies.*` names are now a translation table, including `allowedClientNamespaces`, which never did anything), and the test-suite section covers the profiler plus tiers 1–11 rather than nine.
- **`docs/book/15-kafka-deployment.md`.** Three 1.1.0 OCI installs replaced, the Deploy Script Flow diagram redrawn from the two deploy scripts (operator release first with its own dependency build, then kafka-cluster's, then platform-then-overlay), and the NetworkPolicy tables rebuilt from a render: `generateNetworkPolicy` governs operand policies and never the operator pod, the operator's own policy is the wrapper's `operatorPolicy`, and the kafka namespace has seven policies, not the four stale ones listed.
- **`deploying-strimzi-operator.md`** — `values-generic.yaml` no longer claims to turn dashboards off (it sets nothing; the render is byte-identical to the base), the overlay table gained `values-namespace-scope.yaml`, and the pin count is six.
- **`09-observability.md`** (monitoring 1.2.0 and the `legacyKafkaDashboards.enabled` upgrade), **`appendix-c-cicd.md`** (the Kafka-charts, MirrorMaker 2 and publish-connect workflows, the nine Helm Lint gates, the chart matrix, the metric contract, the unit-test gate and both scheduled live-scrape jobs), **`docs/local-development.md`** (the chart-dependency and validation targets; three descriptions that no longer matched their recipes), **`README.md`** (a from-a-checkout Helm path with operator-first ordering), the MirrorMaker 2 tutorial (a `make` target pair that does not exist, the required `kafka-common` dependency build, and a `diff` example a chart rail now refuses), and the registry pages.
- **A verification pass over the whole diff** then re-derived every count, policy name, port, tier name and job name from renders and workflow files. It caught a `kafka.namespace` recommendation that mirror-maker2 silently ignores (its key is `target.namespace`), a `podSelector` caution that stated the opposite of the rail's behaviour, a release named `krafter` in the README where every other document and `helm test` assume `kafka-cluster`, and a monitoring-namespace contradiction between two chapters — the repository's scripts install the stack into `kafka` while `networkPolicy.monitoring.namespace` defaults to `monitoring`, which is now called out where the choice is made.
- **Two non-doc fixes** followed from it: `values-platform.yaml`'s own header showed the profile *after* the environment overlay, and `make kafka-chart-lint` linted it that way. Both now match the order the scripts use and the docs teach.

### Documentation audit — everything the install-path pass did not cover

The earlier pass fixed how the charts are installed. This one swept the rest of the documentation for places that still describe the pre-refactor world, and it found more than expected: Kafka `4.3.0` and Strimzi `1.1.0` written as current, `helm` commands that cannot run on a fresh checkout, 0.4-era key names taught as current, workflows built on `config/kafka/*.yaml` files nothing reads any more, and tables of counts that no longer match a render.

- **The book.** `02-architecture` and `18-upgrade-playbook` carried the old pins. `12-deployment`'s three cloud overlays taught keys that do not exist in any chart; they were rebuilt from renders, one overlay per chart. `17-security`'s twelve-policy table was the 0.4 set — the chart renders six plus the tests' egress policy. `19-multi-tenancy`'s whole onboarding and decommissioning workflow was `kubectl apply` over `config/kafka/`; it is now `topics.items`, `users.items` and `networkPolicy.clients`, with both quoted alert expressions replaced by the ones the chart actually renders. `10-cli-reference` had two commands that do not exist (`kates baseline set`, `alter-topic --set`) and a Kafka window that predates the 1.2.0 operator. `14-recipes`, `03-cluster` and the book index followed.
- **Connect and MirrorMaker 2 chapters.** Five `helm` invocations of `connect-cluster` had no dependency build; chapters 22 and 23 had six more for `mirror-maker2`, and pointed at migration scripts that `kates migrate` replaced. `appendix-b` still sent readers to `generateNetworkPolicy` and to the operator in the `kafka` namespace.
- **Tutorials.** The Kyverno tutorial's policy counts were all wrong (5 policies / 15 validate / 5 mutate, and the kafka-cluster policy name is per-cluster since 1.0). The chaos tutorial's five `make chaos-kafka-*` targets never existed. Several `kates` invocations used flags and formats the CLI does not have — `report export` has no `-o` and no `json` format. The migration tutorials' target side moved to 4.3.1, which also changes the generated lab names (`m282-430` → `m282-431`), so `down --name m282-430` would not have found the lab.
- **Chart READMEs and the MirrorMaker 2 chart's own docs.** `kates-platform`'s documented install could not run (`helm dependency build` takes one chart, not three). `mirror-maker2/docs/` still described a Strimzi 1.1.0 window. The `apicurio-registry` contract table pointed at `users.items` for a user that lives in the platform profile now.
- **Superseded plan documents** got a dated status note rather than a rewrite: they are records of what was decided, and rewriting them destroys that.

Three repo fixes came out of the audit rather than the docs:

- **The CLI was still passing pre-refactor keys on every deploy** — `networkPolicies.connectNamespace` to kafka-cluster, `databaseEgress[0].*` and `podMonitors.enabled` to connect-cluster. All three translate, so nothing was broken, but every deploy printed deprecation notices for keys this branch renamed. They now use `networkPolicy.clients`, `networkPolicy.egress.databases` and `monitoring.podMonitor`. Each was proved byte-identical against the old key on both overlays the CLI uses; the clients one merges by name, so only the connect grant's namespace moves and every other client keeps its podSelector.
- **`make check-metric-contract` could not run on a fresh checkout** — the same missing-dependency failure that broke CI, in a target that had no chart-deps prerequisite. It has one now, verified by deleting all three `charts/*/charts/` directories first.
- `charts/strimzi-operator` documented seven operator dashboards in both its README and its values comment; it renders nine.

A verification pass then re-derived every count, window, policy name and version in the diff from renders and source files. It caught an over-correction in a plan document — a `4.3.0=…kafka-4.3.1` pair inside a block quoting the 1.1.0 snapshot — and a wrong default export filename, and confirmed the three CLI key migrations change nothing.

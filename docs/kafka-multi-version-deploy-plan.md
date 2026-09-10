# Plan — Strimzi and Kafka Versions in `kates deploy`: Operator Scope, Version Selection, and Several Kafka Versions Side by Side

Branch: `feat/kafka-multi-version` (from `main` after the MirrorMaker 2 branch merges — it depends on `charts/legacy-kafka` and on the `krafter` de-hardcoding it started).

**Purpose.** Several Kafka versions on one cluster exist here for one reason: to test a **MirrorMaker 2 migration from an old Kafka to a new one** — 2.x or 3.x to 4.x, or a line the current operator has dropped to the line it runs. The front door is therefore a pair of versions, `kates migrate up --from 2.8.2 --to 4.3.0` (§3.9); everything else in this plan — operator scope, operator and Kafka version selection, additional clusters and their providers — is the machinery that stands up the two ends of that pair and is designed so the front door can choose it without asking. In five parts that share one foundation:

1. **`kates deploy --operator-scope cluster|namespace`** — the shape of the Strimzi installation is chosen explicitly. *Cluster* scope (today's default) is one operator watching every namespace: its Kafka window is the law, and the CLI tells the user plainly that a Kafka version outside it cannot be installed under Strimzi on this cluster. *Namespace* scope is an operator per Kafka namespace: additional namespaces can carry their own Strimzi — older than the primary's, within the same CRD generation — so Kafka versions the primary's operator has dropped keep running beside the newest ones, two operators, each on its namespace.
2. **`kates deploy --strimzi-version 1.0.1`** — the operator version is chosen at deploy time. Its chart is fetched and *read* before anything is installed, so the CLI knows — from the chart itself — which Kafka versions that operator can run, which CRD API it serves, and whether the installed operator (if any) can be moved to it.
3. **`kates deploy --kafka-version 4.2.0`** — the primary cluster runs the Kafka version you ask for — or, with no flag, the **newest version the selected operator supports** — validated before anything is installed, with every derived setting (metadata version, client images, Connect and MirrorMaker 2 versions, Helm test image) following it. On the command line and in the interactive picker, the Kafka versions offered are exactly the ones an eligible operator can run.
4. **`kates clusters add --name <n> --version <v> [--strimzi-version <s>]`** — additional Kafka clusters of *other* versions alongside the primary, each in its own namespace: provided by the primary's operator when the version is in its window; by a namespace-scoped operator of its own when another eligible Strimzi supports it (namespace scope only); by the `legacy-kafka` chart, without any operator, when no Strimzi can — so a 2.8.2, a 3.9.1, a 4.1.2, a 4.2.1 and a 4.3.0 can all run at once, and `kates clusters list` shows them.
5. **`kates migrate up --from 2.8.2 --to 4.3.0`** — the migration lab in one command: the source at the old version (provider and operator chosen by the rules above), the target at the new one (the platform's primary unless told otherwise), MirrorMaker 2 configured for the pair, a corpus, verification, cutover, teardown — and `kates migrate run` as the CI leg that does all of it and prints one report.

> **Status: FIRST VERSION IMPLEMENTED** (branch `feat/mirror-maker2-cross-version`). §2 is the study it rests on; every constraint there was checked against the Strimzi 1.1.0 chart, the chart templates in this repo, the CLI source, and Strimzi's own statements on running several operators (its April 2025 post on phased upgrades, quoted in §2.3).
>
> Implemented: `pkg/kafkaversion`, `pkg/strimzi` (chart reader, catalogue, pull, generated wrapper, operator discovery, eligibility); `kates deploy --operator-scope|--strimzi-version|--strimzi-chart|--kafka-version|--kafka-name` with the resolution order of §3 run before the first Helm call (window from the chart, newest-Kafka default, short-form metadata version, Connect's version following the primary, installed-operator comparison: same/upgrade/refused downgrade, the 1.0 boundary via `storedVersions`, running clusters checked against the new window, scope changes) and the `-i` picker whose Kafka options follow the chosen operator; `kates versions [strimzi|kafka]`, `kates operators list`; the chart work of §3.7 (namespace-scope overlay, unique-name global bindings, operator policy in the wrapper, hook CRD check by name, kafka floor annotation, share-group gating, derived selectors, `values-additional.yaml`, `extraLabels`, legacy-kafka mode/image by version, MM2 `secretSync[].as` and `values-migrate-4x.yaml`); the `check-versions.sh` assertions of §4; and the migration lab of §3.9 with the **legacy provider** — `kates migrate pairs|plan|up|status|verify|cutover|rollback|down|run` plus the building blocks — wired into CI and the Makefile (the three scripts stay until that CI run is green).
>
> Not yet: Strimzi-operated sources in `up` (in-window, and the dropped line under namespace scope — `plan` describes them, `up` refuses with "not yet implemented"), `--to` other than the primary's version (plan-only), `mirror preflight`, the catalogue rows of `pairs`, the `kates clusters` family (§3.6, including additional co-located operators and `upgrade-operator`), `kates clean` learning the lab releases, Kafka UI's cluster list, the docs of Phase 6 beyond the CLI reference, tutorials 11–12 and the installation guide.
>
> One correction to the study: the operator tarball under `charts/strimzi-operator/charts/` and `Chart.lock` are **gitignored build artifacts**, fetched by `helm dependency build` from the OCI registry, not files committed to the repository. "Pinned" therefore means an exact version fetched once and read from disk afterwards, not a chart that installs with no network on a fresh clone; the CLI runs `helm dependency build` when the tarball is absent, and `check-versions.sh` does the same.

---

## 1. Why, and what "multiple versions" can mean

Today `kates deploy` installs exactly one Kafka: the chart's pinned `kafkaVersion: "4.3.0"`, named `krafter`, in namespace `kafka`, with the name spelled out two dozen times across eleven CLI files and the version in four pin sites guarded by `scripts/check-versions.sh`. There is no flag to change either, and no flag to change the operator that runs it or the namespaces it watches. The MirrorMaker 2 work added the first *second* cluster the repo has ever had — the `legacy-kafka` chart — and every seam it touched (`krafter` literals, single-bootstrap consumers, NetworkPolicies keyed on one cluster name) is a seam this plan has to open properly.

"Multiple versions" hides five different asks, with different answers:

| Ask | Answer | Why |
|:---|:---|:---|
| Run the **operator** at a version other than the pin — older, to reproduce what a customer runs; newer, to try a release before the pin moves | `kates deploy --strimzi-version` | The operator's version decides every Kafka window, so it is chosen first and everything else follows |
| Run the **primary** cluster on a version other than the default | `kates deploy --kafka-version` — any version the *selected* operator supports | The operator decides what it can run; the chart already parameterises the version |
| Run a **second Strimzi cluster** on another version the primary's operator supports | `kates clusters add` — the primary's operator reconciles it (cluster scope), or a co-located operator of the same version does (namespace scope) | Strimzi supports several `Kafka` CRs at different versions under one operator, as long as each is in its window |
| Run a Kafka version the primary's operator has **dropped** (4.1.x today) under Strimzi | `kates deploy --operator-scope namespace`, then `kates clusters add … --strimzi-version 1.0.1` | A second operator is possible only when it watches its own namespaces and shares the CRD generation (§2.3); under a cluster-wide operator it is not, and the CLI says so |
| Run a version **no eligible Strimzi** can run (2.x, 3.x) | `kates clusters add … --provider legacy` | CRDs are one set per Kubernetes cluster, and the operators that ran 3.x speak an API generation this platform cannot host (§2.3); plain StatefulSets need no operator |
| **Test a migration** from any of those old versions to a new one, with MirrorMaker 2, without assembling the pieces by hand | `kates migrate up --from <old> --to <new>` (§3.9) | The pair is the intent; the source's provider and operator follow from the rows above, the target is the primary, and the mirror's configuration is a function of the two versions |

**Two shapes of installation.** Everything else in the plan branches on the scope, so it is worth being exact:

| | `--operator-scope cluster` (default) | `--operator-scope namespace` |
|:---|:---|:---|
| operators | exactly one, in `strimzi-operator`, `watchAnyNamespace: true` | one per Kafka namespace: the primary's in `strimzi-operator` watching the primary's namespaces; each additional Strimzi cluster's *co-located* in its own namespace, watching only it |
| who can run what | the one window | the primary's window, plus the window of every additional operator |
| additional operators | none — a second Strimzi beside a cluster-wide one would reconcile the same namespaces | allowed, older than or equal to the primary's, same CRD generation, adjacent release preferred (§3.1) |
| out-of-window Kafka | **warned and refused under Strimzi**; `legacy` provider only | offered an operator of its own if some eligible Strimzi runs it; `legacy` otherwise |
| CRDs and cluster-scoped RBAC | the operator's release | the **primary's** release, which is therefore always the newest operator on the cluster |

What neither scope can mean: the primary cluster on an out-of-window version. The platform's users, topics, ACLs, Connect, Apicurio and MM2 are Strimzi CRs, and the backend expects a Strimzi-shaped cluster on the primary's operator. Out-of-window versions are additional clusters only, and the plan says so in the error message.

---

## 2. The study

### 2.1 The operator's window is small, explicit, and shipped inside this repo

Strimzi validates `spec.kafka.version` against a compiled-in list. For the pinned 1.1.0 that list is **4.2.0, 4.2.1 and 4.3.0** (default 4.3.0), with 4.1.x removed in that release; every older entry in its `kafka-versions.yaml` is marked `supported: false` and "kept for historical reasons". A custom image does not widen it — the version must still be one the operator supports, and a `Kafka` CR outside the list goes `NotReady` with a message, which is exactly the failure a `--kafka-version` flag must catch *before* Helm runs.

The list is not something the CLI has to know. The vendored operator chart carries it: `charts/strimzi-operator/charts/strimzi-kafka-operator-1.1.0.tgz` → `templates/_kafka_image_map.tpl` renders

```text
STRIMZI_KAFKA_IMAGES
  4.2.0=quay.io/strimzi/kafka:1.1.0-kafka-4.2.0
  4.2.1=quay.io/strimzi/kafka:1.1.0-kafka-4.2.1
  4.3.0=quay.io/strimzi/kafka:1.1.0-kafka-4.3.0
```

into the Cluster Operator Deployment. So the supported set is available **offline** (`helm template charts/strimzi-operator`, parse the env) and **live** (`kubectl get deploy strimzi-cluster-operator -o jsonpath` on the running operator). Both are the operator's own statement; neither is a table in Go. When the operator pin moves, the window moves with it and nothing else needs editing.

Each version also has a KRaft **metadata version** (`4.2.x → 4.2-IV1`, `4.3.0 → 4.3-IV0`). The chart pins `kafka.metadataVersion: 4.2-IV1` — deliberately one step behind 4.3.0, because a metadata version cannot be rolled back and pinning behind preserves the option (`charts/kafka-cluster/values.yaml:35-41`). A version flag has to carry that rule, not just the version.

### 2.2 The chart is one flag away from multi-version, and several resources away from multi-instance

`charts/kafka-cluster` already parameterises the two things that matter: `clusterName` (78 sites across 17 templates go through the `kafka-cluster.clusterName` helper) and `kafkaVersion` (the `Kafka` CR's `spec.kafka.version`, and the Helm-test client image `strimzi/kafka:<strimziVersion>-kafka-<kafkaVersion>` via `_helpers.tpl:73`). A second release with a different name and version renders a second, independent cluster.

What a second release collides with — found by listing every resource the chart creates and asking which are cluster-scoped, fixed-name, or written into *another* namespace:

| Resource | Scope / name | Effect of a second release | Handling |
|:---|:---|:---|:---|
| Drain Cleaner (`ClusterRole`, `ClusterRoleBinding`, `ValidatingWebhookConfiguration`, all named `strimzi-drain-cleaner`) | cluster-scoped, fixed | Helm ownership conflict | already `drainCleaner.enabled: false` by default; additional clusters must never enable it — one webhook serves all clusters |
| CRD upgrade Job + `ClusterRole` (`<clusterName>-crd-upgrade`), which downloads and applies `strimzi-crds-<strimziVersion>.yaml` (`templates/crd-upgrade.yaml:84-90`) | cluster-scoped, name-prefixed | no name conflict — but a release carrying an **older** `strimziVersion` would roll the cluster's CRDs back | `crdUpgrade.enabled=false` for every additional cluster, mandatory: CRDs belong to the newest operator (§2.3) |
| NetworkPolicy `strimzi-operator` written into `networkPolicies.operatorNamespace` (`templates/networkpolicies.yaml:245-249`), egress to pods labelled `strimzi.io/cluster: <clusterName>` | fixed name, *in the operator's namespace* | two clusters pointing at the same operator namespace fight over one policy | move it to the operator wrapper chart, where it is owned once per operator; keep a conditional copy in `kafka-cluster` for standalone use (§3.7) |
| Kyverno `ClusterPolicy` `kafka-pod-security-standards` | cluster-scoped, fixed | conflict | default off; keep off for additional |
| Grafana dashboard ConfigMaps (`kafka-kraft-dashboard`, …) | namespaced, fixed names | conflict in the same namespace; duplicate dashboards across namespaces | `dashboards.enabled=false` for additional clusters |
| `kafka-metrics` ConfigMap, NetworkPolicies `default-deny`/`allow-dns`/`kafka-brokers`/`kafka-controllers`/`kafka-ui`, PodMonitors | namespaced, fixed names | conflict in the same namespace | **one namespace per cluster** — a rule the CLI enforces, not a convention |
| `networkPolicies.defaultDenySelector` / `allowDNSSelector` = `app.kubernetes.io/part-of: strimzi-krafter` | values literal (`values.yaml:557,562`) | a non-`krafter` cluster's pods are not selected: no default-deny, no DNS allowance | derive `strimzi-<clusterName>` in the template |
| `kafka.config` `group.share.enable: true` (`values.yaml:86`) | a Kafka ≥ 4.2 feature | a 4.1.x cluster will not start with it | render it only when `kafkaVersion` ≥ 4.2.0 |
| `users:` (`kates-backend`, `kates-connect`, `kates-mm2`, …) and `topics:` | per-cluster CRs labelled `strimzi.io/cluster` | fine across namespaces | additional clusters get the same user set by default (harmless, and `kates-mm2` on both ends is exactly what a same-operator migration needs) |

Everything else already keys on `clusterName`.

### 2.3 One set of CRDs per Kubernetes cluster — when a second operator is possible, and when it is not

Strimzi's CRDs are cluster-scoped: there is one `kafkas.kafka.strimzi.io`, whichever operators are installed. That single fact decides everything about running two operators, and Strimzi has written down the rules (blog, "Phased upgrades of Strimzi managed Kafka fleets", April 2025):

- The installed CRDs must be those of the **most recent** Strimzi on the cluster. CRDs are backwards compatible *within an API version*: an older operator is fine with newer CRDs (it ignores fields it does not know) — that is the state every Strimzi upgrade passes through — but a newer operator on older CRDs is not.
- The only **tested** concurrent configuration is **two adjacent releases**. More is possible, and explicitly at the user's risk.
- A change of CRD **API version** (`v1beta2` → `v1`) is a wall: every custom resource must be on the last old-API release before the new-API CRDs are installed. Strimzi 0.21 and 0.23 could not coexist; neither can 0.48 and 1.0.
- Each operator needs its **own RBAC names**; the Helm chart's `createGlobalResources: false` is the switch that lets a second release skip the fixed-name cluster-scoped roles and bindings.
- Removing CRDs, even briefly, deletes every Kafka on the cluster. Never let a tool that removes an operator remove CRDs.

Applied to this platform, whose charts render `kafka.strimzi.io/v1` only:

| Operators | Coexist? | Why |
|:---|:---|:---|
| 1.1.0 and 1.0.x | yes — tested by Strimzi (adjacent) | both `v1`-only; 1.1.0's CRDs are a superset; 1.0.x runs 4.1.x that 1.1.0 dropped |
| 1.2.0 and 1.0.x (once 1.2 exists) | possible, untested | same generation, two releases apart — allowed behind a flag, with the warning |
| 1.x and 0.51.0 | no | 0.51 *stores* `v1beta2`; 1.x CRDs remove it |
| 1.x and anything ≤ 0.48 (the operators that ran Kafka 3.x and 2.x) | no | those CRDs are `v1beta2`-only, and these charts do not render `v1beta2` at all |
| any operator beside a **cluster-wide** one | no, whatever the versions | a cluster-wide operator watches every namespace; a second operator would reconcile the same CRs |

So a second operator is a real option — for **namespace-scoped** operators, of the **same CRD generation**, with the **newest one owning the CRDs**. It is not an option for the versions of Strimzi that ran Kafka 2.x and 3.x, whatever the scope, and it is not an option beside a cluster-wide operator. The answer for those versions is the one the MirrorMaker 2 work already built: `charts/legacy-kafka`, plain StatefulSets that need no operator at all. The earlier draft of this plan said "why not a second operator" and stopped; the correct statement is the table above.

Strimzi's own preferred pattern for concurrent operators is a *cluster-wide* pair distinguished by `STRIMZI_CUSTOM_RESOURCE_SELECTOR` labels. It is not used here on purpose: this plan's promise is that cluster scope means one operator, and that the namespace boundary is the isolation boundary — a label selector would make both statements false.

### 2.4 Where `krafter` is spelled out

| Where | Count | What it is |
|:---|:---|:---|
| `cli/cmd/deploy_components.go` | 7 | the Helm release name, the readiness selector `strimzi.io/cluster=krafter`, three bootstrap FQDN strings |
| `cli/cmd/doctor_network.go` (4), `portforward.go` (3), `deploy_plan.go` (2), `clean.go` (2), `deploy_status.go`, `silent_wait.go`, `detect.go`, `auto.go`, `deploy.go` (`RenderValuesWithReserve(report, "krafter", …)`), `pkg/detect/remediation.go` | 17 | selectors, service names, release lists, the values generator's cluster name |
| `charts/kates/values.yaml:317` | 1 | the backend's bootstrap |
| `charts/kafka-ui`, `apicurio-registry`, `connect-cluster`, `mirror-maker2` | ~20 | `clusterName` defaults and CA Secret names — already values, already overridable |
| `config/litmus`, `config/kafka`, `config/kafka-connect` | ~30 | chaos experiments and policy manifests bound to the primary |
| `versions.env` `BOOTSTRAP`, `scripts/test-perf-*.sh`, `verify-kafka-policies.sh` | 6 | the primary's bootstrap |

The chart consumers are already parameterised. The CLI is not. The config manifests are bound to the primary by design and stay that way.

### 2.5 What the version pins guard, and what a flag must not break

`scripts/check-versions.sh` asserts four Kafka sites agree (`versions.env`, `kafka-cluster` `kafkaVersion`, `mirror-maker2` `version` and `appVersion`) and six Strimzi sites. It misses a fifth Kafka site — `charts/connect-cluster/values.yaml` `version: "4.3.0"`, the `KafkaConnect` CR's `spec.version` — which matters once the operator is selectable, because a Connect version outside the operator's window fails exactly like a Kafka one. Those are **defaults**. A `--kafka-version` is a per-deploy override that never writes into a values file, so the gate keeps guarding the defaults and gains one new assertion: the default `kafkaVersion` must be the **newest** entry in the vendored operator's window (rendering the chart and grepping `STRIMZI_KAFKA_IMAGES`). That is both the check that would have caught a Strimzi bump that silently dropped the pinned Kafka line, and the proof that a hand-run `helm install` and a flagless `kates deploy` (§3.3) land on the same version.

### 2.6 The consumers of "the" cluster

The backend (`charts/kates`) binds to one bootstrap; `connect-cluster`, `apicurio-registry` and the MM2 target default to `krafter`; `kafka-ui` takes a **list** of clusters (`values.yaml:123-148`). So: one **primary** cluster remains the thing the platform points at, additional clusters are **data-plane only** until a consumer is explicitly attached to them, and the one consumer that can show them all — Kafka UI — should.

The consumers also decide what the primary's operator must **watch** in namespace scope: `KafkaUser`s and `KafkaTopic`s live in the Kafka namespace, `KafkaConnect`/`KafkaConnector` in the Connect namespace (`connect` in the isolated topology, `kates-stack` in the single one), `KafkaMirrorMaker2` in the MM2 namespace (`kafka` by default). Apicurio and the backend create no Strimzi CRs of their own. The watch list is a function of the topology flags the CLI already has.

### 2.7 How the operator is installed today

`charts/strimzi-operator` is a wrapper: a vendored `charts/strimzi-operator/charts/strimzi-kafka-operator-1.1.0.tgz` as its only dependency (Chart.yaml pins `version: 1.1.0`, Chart.lock records the digest, `helm dependency build` resolves offline from the lock), plus three things of its own — the kates defaults for the subchart (`watchAnyNamespace: true`, resources, timeouts), a strict `values.schema.json`, and a pre-install/pre-upgrade **CRD hook** that closes Helm's `crds/`-never-upgraded gap by downloading `strimzi-crds-<strimziVersion>.yaml` from the GitHub release and server-side-applying it. The hook's bundle URL is built from `values.yaml` `strimziVersion`, which is why that value, the dependency version and `appVersion` must agree, and why `check-versions.sh` exists.

The CLI installs it with no notion of a version or a scope (`cli/cmd/deploy_components.go:51-80`): create the namespace, `helm dependency build charts/strimzi-operator`, `helm upgrade --install strimzi-operator charts/strimzi-operator … --reset-values`, with only the cluster DNS domain passed through. Reconciliation is unconditional — an existing release is upgraded in place so the CRD hook always fires. `images.env` preloads exactly the pinned `quay.io/strimzi/operator:1.1.0` and `kafka:1.1.0-kafka-4.3.0` onto kind. Nothing in the CLI reads the installed operator's version or watch list back; `kates status` shows the pod, not the version.

Two details become load-bearing once the version varies. The hook asserts `EXPECTED_CRDS=10` — an exact count that is true of 1.1.0's bundle and not a property of every release. And the upgrade playbook's "Strimzi Operator Upgrade" still tells the reader to `helm upgrade strimzi-operator oci://quay.io/strimzi-helm/strimzi-kafka-operator --version <new>` directly — bypassing the wrapper, its values and its CRD hook — a leftover from before the wrapper chart that this plan retires.

### 2.8 What changes between Strimzi versions that this platform feels

The platform renders `kafka.strimzi.io/v1` and nothing else (21 sites across the charts: `Kafka`, `KafkaNodePool`, `KafkaUser`, `KafkaTopic`, `KafkaConnect`, `KafkaConnector`, `KafkaMirrorMaker2`, `KafkaRebalance`), and its default Kafka configuration turns on share groups (`group.share.enable: true`, `charts/kafka-cluster/values.yaml:86`), which need Kafka ≥ 4.2 and a metadata version ≥ 4.2. Against the release history (Strimzi `CHANGELOG.md`, the 1.0.0 release notes, and the `kafka-versions.yaml` of 0.51.0 and 1.1.0):

| Strimzi | Kafka window (default in **bold**) | CRD API | What it means here |
|:---|:---|:---|:---|
| 0.48.0 and older | ≤ 4.1.0 | `v1beta2` only | no `v1`: the charts do not render — **below the floor**, in any role |
| 0.49.0, 0.50.x | 4.0.0, 4.0.1, 4.1.0, **4.1.1** | `v1` served, `v1beta2` stored | `v1` works, but no Kafka ≥ 4.2 for the primary, and a different generation from any 1.x — usable only if *every* operator is pre-1.0, which this platform does not support |
| 0.51.0 | 4.1.0, 4.1.1, **4.2.0** | `v1` served, `v1beta2` stored | first operator that can run the primary; Kubernetes ≥ 1.30; resources are *stored* as `v1beta2`, so a later move to 1.x needs Strimzi's API-conversion step, and no 1.x can sit beside it |
| 1.0.0, 1.0.1 | 4.1.0, 4.1.1, 4.1.2, **4.2.0** | `v1` only | `v1beta2` removed from the CRDs; the 1.0 boundary must not be crossed by `helm upgrade` alone; the operator that keeps **4.1.x** alive beside a 1.1.0 primary |
| 1.1.0 (pin) | 4.2.0, 4.2.1, **4.3.0** | `v1` only | today's window; 4.1.x dropped |

The primary's floor is therefore not a number the CLI carries. It is two facts read from any operator chart — *does its Kafka CRD serve `v1`* (`crds/040-Crd-kafka.yaml`) and *does its window contain a Kafka the platform can run* (window ∩ `≥ 4.2.0` ≠ ∅) — plus one fact the platform declares about itself, its Kafka floor (`4.2.0`, because the primary's configuration needs share groups), as a chart annotation. 0.51.0 falls out of that arithmetic today; when the floor moves, the arithmetic moves with it. An **additional** operator has a different floor: it must serve `v1`, share the installed CRDs' generation (§2.3), and be no newer than the primary's; the share-groups constraint does not apply because the chart renders that setting only for Kafka ≥ 4.2 (§2.2).

Three upgrade rules from the same sources shape §3.5: Strimzi does not support **downgrading** an operator; an operator must be upgraded **before** the Kafka it runs, and the new operator's window must contain every Kafka version it will find running — a `Kafka` CR on a dropped version goes `NotReady` with a message and is not reconciled again until its version changes; and crossing **1.0** from a `v1beta2`-stored release requires the documented API-conversion procedure first, which the CLI can verify (every Strimzi CRD's `status.storedVersions` is `[v1]`) but should not perform.

### 2.9 Where an operator chart can come from, and what it tells us

| Source | What | When |
|:---|:---|:---|
| `charts/strimzi-operator/charts/strimzi-kafka-operator-1.1.0.tgz` | the pin, vendored | default; offline; lock-verified |
| `oci://quay.io/strimzi-helm/strimzi-kafka-operator:<v>` | any published version (the same registry the pin is pulled from) | `--strimzi-version` other than the pin, additional operators; needs network |
| `https://strimzi.io/charts/` index | the catalogue: every chart version with its `appVersion` and publish date | `kates versions strimzi`, the interactive picker, the "which operator runs 4.1.2" suggestion |
| `https://github.com/strimzi/strimzi-kafka-operator/releases/download/<v>/strimzi-crds-<v>.yaml` | the CRD bundle the hook applies | already used; the version just follows |

Every chart, whatever its source, carries the three facts the CLI needs without executing anything: its Kafka window (`templates/_kafka_image_map.tpl`, literal `X.Y.Z=` lines under `STRIMZI_KAFKA_IMAGES`, the same file the vendored copy already provides), the CRD versions it serves and stores (`crds/`), and its image tag (`values.yaml` `defaultImageTag`). Reading the tarball is enough; rendering it is not required.

### 2.10 What namespace scope changes in the operator chart

The upstream chart already knows both shapes; the wrapper only ever set one. Read from the vendored templates:

| Setting | Cluster scope (today) | Namespace scope |
|:---|:---|:---|
| `watchAnyNamespace` / `watchNamespaces` | `true` / — → `STRIMZI_NAMESPACE="*"` | `false` / a list (release namespace included by default) → `STRIMZI_NAMESPACE="kafka,connect"` |
| operator RBAC in watched namespaces (`020`, `023`, `031` templates) | three `ClusterRoleBinding`s with fixed names | a `RoleBinding` named `strimzi-cluster-operator` (etc.) **in each watched namespace** — namespaced names, so two operators watching disjoint namespaces never collide |
| cluster-scoped RBAC (`ClusterRole`s `strimzi-cluster-operator-{namespaced,global,leader-election,watched}`, `strimzi-kafka-broker`, `strimzi-entity-operator`, `strimzi-kafka-client`; `ClusterRoleBinding`s `strimzi-cluster-operator`, `…-kafka-broker-delegation`, `…-kafka-client-delegation`) | rendered, `createGlobalResources: true` | rendered by **one** release only; every other operator sets `createGlobalResources: false` and reuses them |
| what `createGlobalResources: false` costs | — | the three fixed-name `ClusterRoleBinding`s are skipped too, so a second operator's ServiceAccount is not bound to `strimzi-cluster-operator-global` (`nodes` list, `storageclasses` get, `clusterrolebindings` CRUD): no rack awareness, no volume-expansion checks. The wrapper can render those three bindings under **unique names** (`strimzi-cluster-operator-<namespace>`) for the additional operator's ServiceAccount, restoring parity (§3.7) |
| leader-election lease | `strimzi-cluster-operator` in the release namespace | same name, but one release namespace per operator, so unique |
| network access to operands | Strimzi's generated policies admit the operator across namespaces already (the primary runs that way today) | a co-located operator is in the same namespace as its cluster — the simplest case |
| CRD hook | applies `strimzi-crds-<strimziVersion>.yaml` on install and upgrade | must run for the **newest** operator only (`crdUpgrade.enabled=false` on the others), or an older operator's install rolls the CRDs back |

Where an additional operator lives is a choice: in its own namespace beside the cluster's, or **in the cluster's namespace**. Co-location wins — `watchNamespaces` can stay empty (the release namespace is the default), the RoleBindings are local, `kates clusters remove` deleting the namespace removes the operator with it, and `kates operators list` reads as "one operator, one namespace, one window". The primary's operator stays in `strimzi-operator` in both scopes, because `clean`, `doctor`, `status`, the MM2 chart's `strimziOperatorNamespace` and the book all assume it; the asymmetry is named in the docs rather than hidden.

---

## 3. Design

Resolution order is the spine of the design, and it is the same for every entry point — flags, the interactive picker, `--dry-run`, CI:

1. **Scope** — `--operator-scope`, else the installed shape (§4), else `cluster`.
2. **Operator version** — `--strimzi-version`, else the pin.
3. **Operator chart** — the vendored tarball for the pin; otherwise pulled into the cache.
4. **Window and API** — read from that chart (§2.9); refuse below the floor (§2.8).
5. **Kafka version** — `--kafka-version` validated against *that* window and the platform floor, else the window's newest.
6. **Installed operators** — none, same, upgrade, scope change, or refused (§3.5).
7. **Derived settings** — metadata version, Connect and MM2 versions, client and Helm-test images, the watch list.

Nothing is installed before step 7 completes; every refusal names the window, the scope, and the way out.

### 3.1 `kates deploy --operator-scope` — one operator, or one per namespace

```text
kates deploy                                     cluster scope: one operator, watchAnyNamespace (today's shape)
kates deploy --operator-scope namespace          the primary's operator watches only the primary's namespaces;
                                                 additional clusters may bring their own operator
```

**Cluster scope** keeps exactly today's installation and adds one promise: the CLI never lets Strimzi be asked for a version it cannot run, and never installs a second operator. When a version outside the window is requested — `--kafka-version` for the primary, or `clusters add --version` — the message is explicit about *why* and about both ways forward:

```text
Kafka 4.1.2 cannot run under Strimzi 1.1.0, the cluster-wide operator on this cluster
(supported: 4.2.0 4.2.1 4.3.0). A cluster-wide operator watches every namespace, so no
second Strimzi can be installed beside it.
  - run it without an operator:   kates clusters add --name legacy41 --version 4.1.2 --provider legacy
  - or give it its own operator, which needs namespace-scoped operators:
        kates deploy --operator-scope namespace
        kates clusters add --name legacy41 --version 4.1.2 --strimzi-version 1.0.1
```

For the primary the first option does not exist (§1), and the message says so.

**Namespace scope** is the shape that unlocks per-namespace operators. The invariants the CLI enforces, all derived from §2.3 and §2.10:

- No operator watches `*`; every operator's watch set is disjoint from every other's; the primary's watch set is the topology's Strimzi namespaces (§2.6). A mixed installation — a cluster-wide operator *and* a namespaced one — is flagged by `kates doctor` and refused by `deploy` until fixed.
- All operators share one CRD generation with the installed CRDs (`v1`-only today, since the primary is ≥ 1.0.0).
- The **primary's operator is the newest** and owns the CRDs and the cluster-scoped RBAC. An additional operator is at most the primary's version (`--strimzi-version` on `clusters add` refuses newer: "to try 1.2.0, upgrade the primary's operator with `kates deploy --strimzi-version 1.2.0`").
- **Adjacent by default.** An additional operator one release behind the primary (1.0.x beside 1.1.0) is what Strimzi tests; two or more behind is allowed only with `--allow-nonadjacent`, printing Strimzi's own caveat. Patch levels do not count as releases.
- Additional operators run with `createGlobalResources: false`, `crdUpgrade.enabled: false`, `watchNamespaces: []` (their own namespace), the wrapper's unique-name global bindings, and the kind-sized resources overlay.

Switching scope on an existing installation is a `helm upgrade` of the primary's operator (the watch list and the binding kinds change; the operator restarts, the Kafkas do not roll): cluster → namespace is allowed after a confirmation; namespace → cluster is refused while any additional operator exists, because the cluster-wide operator would start reconciling their namespaces.

### 3.2 `kates deploy --strimzi-version` — the operator

```text
kates deploy                                              the pin (vendored, offline)
kates deploy --strimzi-version 1.0.1                      a published version, pulled and read first
kates deploy --strimzi-version latest                     newest in the catalogue (warns if newer than the pin)
kates deploy --strimzi-chart ./strimzi-kafka-operator-1.0.1.tgz   an air-gapped mirror; version read from the tarball
```

- **Default: the pin, not "latest".** The operator is the platform's tested contract and the one thing that must install offline; pulling the newest operator on every deploy would make deploys network-bound and unreproducible. This is the deliberate asymmetry with Kafka (§3.3), where the default *is* the newest — because "newest Kafka the operator runs" is a fact of the chosen operator, while "newest operator" is a fact of the internet.
- **Fetch, then read, then decide.** A non-pinned version is `helm pull`ed from the OCI registry into `$XDG_CACHE_HOME/kates/strimzi/` (once per version) and read as a tarball: window, served CRD versions, image tag. Below the floor → refused with the reason ("serves no `v1` API" or "no Kafka ≥ 4.2.0 in its window: 4.0.0 4.0.1 4.1.0 4.1.1"). Newer than the pin → allowed with one line: `Strimzi 1.2.0 is newer than the version this platform release was tested with (1.1.0)`.
- **Install through the wrapper, never around it.** The CLI generates a per-version copy of `charts/strimzi-operator` under the cache — `Chart.yaml` dependency `version` and `appVersion` rewritten to the selected version, the pulled tarball in its `charts/`, no `Chart.lock` — and runs today's `helm upgrade --install … --set strimziVersion=<v>` against that copy, with the scope values from §3.1. The wrapper's values, schema and CRD hook apply unchanged, so the hook fetches the matching `strimzi-crds-<v>.yaml`; `helm list` shows the truth in `APP VERSION`. The pin still installs from the repo directory exactly as now. Additional operators (§3.6) are the same generated wrapper installed as release `strimzi-operator` in the cluster's namespace.
- **Air-gapped.** `--strimzi-chart` takes a local tarball (or an OCI reference on an internal registry) and pairs with the existing `crdUpgrade.url` override; both are printed by `--dry-run` so the mirror's operator can see what will be fetched.

### 3.3 `kates deploy --kafka-version` — the primary

```text
kates deploy                                                        newest version the (selected) operator supports
kates deploy --kafka-version 4.2.1 [--kafka-name krafter] [--kafka-namespace kafka]
kates deploy --strimzi-version 1.0.1 --kafka-version 4.2.0          an explicit pair
kates deploy --kafka-version latest                                 the default, spelled out
```

- **Default: the newest version in the window.** With no `--kafka-version`, `deploy` takes the highest entry of the selected operator's window — for the pin that is 4.3.0, which is also the operator's own default; for `--strimzi-version 1.0.1` it is 4.2.0. The default is *computed*, not pinned: bumping the operator moves it, with no edit in Go. The chart's pinned `kafkaVersion` stays the default for a hand-run `helm install`, and `check-versions.sh` asserts the pin *is* the vendored window's newest entry (§2.5), so the two defaults can never differ — a Strimzi bump that adds a newer Kafka fails the gate until the pins move, which is the moment to move them. The plan output and `--dry-run` always say which version was chosen and why: `Kafka 4.3.0 (newest supported by Strimzi 1.1.0; --kafka-version to choose another)`.
- **Validation before Helm.** The version is checked against the selected operator's window (§4) and the platform floor. An unsupported version fails with the window and the ways forward — a supported version, a different `--strimzi-version` whose window has it (`kates versions strimzi` shows which), or `kates clusters add` for an out-of-window one — the same "fail in seconds, name the way out" contract the MM2 chart's KIP-896 gate set. Under cluster scope the message is the one in §3.1.
- **Derived settings travel with it.** `kafka.metadataVersion` follows the rule the chart documents — the metadata version of the highest supported release *below* the requested minor if there is one, otherwise the version's own — and is emitted in the short form Kafka defines (`4.2`, which Kafka resolves to the newest `4.2-IVn`, and which is the form Strimzi itself uses in its `kafka-versions.yaml`). So no table of `-IV` levels lives anywhere: 4.3.0 → `4.2` (unchanged in effect from today's `4.2-IV1`); 4.2.1 → `4.2` (its own, 4.1 being out of the window); a future `[4.3.0 4.4.0]` window → `4.3` for 4.4.0. The Helm-test client image follows via the chart helper. **Connect and MirrorMaker 2 take the primary's Kafka version** for their `spec.version` unless overridden, because their images are `kafka:<strimzi>-kafka-<version>` pairs that exist only inside the operator's window — the chart defaults (4.3.0) are right for the pin and wrong for any older operator. An explicit override is validated against the same window. `strimziVersion` in `kafka-cluster` and `mirror-maker2` is set from the selected operator for the same reason.
- **`--kafka-name` / `--kafka-namespace`** replace the two dozen literals in the CLI with one `dc.primary` struct (`Name`, `Namespace`, `Version`, `Bootstrap()`), and every consumer install (`kates` chart bootstrap, Connect, Apicurio, MM2 target, Kafka UI cluster list, port-forward, doctor, clean, status) reads from it. `krafter`/`kafka` stay the defaults, so nothing changes for anyone who does not pass the flags.
- **`--dry-run`** prints the scope, the operator version and where its chart came from (vendored, cache, pulled, `--strimzi-chart`), its window, the watch list, the chosen Kafka version and whether it was defaulted, the derived metadata version, the Connect/MM2 versions, and the Helm commands.

### 3.4 `kates versions`, `kates operators`, and the interactive picker

```text
kates versions                          the operators on this cluster (or the pin), their Kafka windows, the legacy range
kates versions strimzi [--all] [--refresh]   the catalogue: published operator versions, newest first
kates versions kafka --strimzi-version 1.0.1  that operator's window (pulled and cached if needed)
kates operators list                    every Cluster Operator on this cluster: namespace, version, scope, watched
                                        namespaces, window, role (primary / additional / foreign)
kates versions --json, kates operators list --json      for scripts and CI
```

`kates versions strimzi` reads the Helm index (through `helm search repo … --versions -o json` against a kates-owned repository config under the cache, so the user's own `helm repo` list is never touched) and shows, per version: publish date, status — `pinned`, `newer than pinned`, `supported`, `eligible as additional operator` (namespace scope: same generation, ≤ the primary's, adjacent or not), or `below floor` — and the Kafka window for every version whose chart is already cached; `--resolve` pulls the rest (a few hundred kilobytes each). Below-floor versions are hidden unless `--all`, because a list of things you cannot install is noise.

```text
STRIMZI   PUBLISHED    KAFKA WINDOW (operator default in brackets)   STATUS
1.1.0     2026-07      4.2.0  4.2.1  [4.3.0]                        pinned — tested with this release; the primary's operator
1.0.1     2026-05      4.1.0  4.1.1  4.1.2  [4.2.0]                 eligible as additional operator (adjacent) — namespace scope
1.0.0     2026-04      4.1.0  4.1.1  4.1.2  [4.2.0]                 eligible as additional operator (adjacent) — namespace scope
0.51.0    2026-03      4.1.0  4.1.1  [4.2.0]                        supported as primary only — stores v1beta2; cannot sit beside 1.x
```

`kates operators list` is discovery (§4), not a registry, and under cluster scope it is one line. Under namespace scope it is the map of who runs what:

```text
NAMESPACE          VERSION  SCOPE       WATCHES           KAFKA WINDOW              ROLE
strimzi-operator   1.1.0    namespaces  kafka, connect    4.2.0 4.2.1 4.3.0         primary — owns CRDs and cluster RBAC
kafka-legacy41     1.0.1    namespaces  kafka-legacy41    4.1.0 4.1.1 4.1.2 4.2.0   additional (adjacent to primary)
```

**The interactive picker** is a new group in the existing `kates deploy -i` form (`huh`, `cli/cmd/deploy_plan.go`): a `Select` for the scope with one line under each option saying what it allows; a `Select` for the Strimzi version, pin preselected, each option carrying its status; then a `Select` for the Kafka version whose options are produced by `OptionsFunc` bound to the operator choice — pick 1.0.1 and the list becomes `4.2.0 (newest) · 4.1.2 · 4.1.1 · 4.1.0`; pick 1.1.0 and it becomes `4.3.0 (newest) · 4.2.1 · 4.2.0`. On a cluster that already has a cluster-wide operator, the Strimzi select is fixed to the installed version (changing it is the upgrade path of §3.5, shown as such), the Kafka options are its window, and the group's description carries the §3.1 sentence: *cluster-wide operator — only these versions can run under Strimzi here; for others use the legacy provider or namespace scope.* The first pull of a version happens inside `OptionsFunc` and is cached; the options never show a version the chosen operator cannot run, which is the whole point of the picker. Flags skip the group; `--yes` never prompts.

### 3.5 Versions and scope on an existing installation

`kates deploy` already reconciles the primary's operator on every run (§2.7). With versions in play, the reconciliation gains a comparison — installed (the operator Deployment's image tag, cross-checked with the release's Helm `APP VERSION`; a mismatch means someone changed one by hand, and the CLI stops) against requested:

| Installed → requested | Action |
|:---|:---|
| none | install |
| same | converge, as today (the hook re-applies the same bundle) |
| older → newer, every running Kafka/Connect/MM2 version inside the new window, no 1.0 boundary crossed | **operator upgrade**, after one confirmation that lists the CRs that will roll (`--yes` in CI). Under namespace scope the confirmation also lists every additional operator and whether it stays adjacent afterwards: *after this upgrade, 1.0.1 in kafka-legacy41 is two releases behind 1.2.0 — untested by Strimzi; upgrade or remove it* |
| older → newer, some running version outside the new window | refused: `krafter runs 4.1.2; Strimzi 1.1.0 supports 4.2.0 4.2.1 4.3.0 — upgrade Kafka first, or go through 1.0.1 (supports both)`; the intermediate version is computed from the catalogue, not guessed. Under namespace scope the check covers only the namespaces this operator watches — a 4.1.2 running under its own 1.0.1 operator is not an obstacle, which is the point of the scope |
| `< 1.0.0` → `≥ 1.0.0` | refused unless every Strimzi CRD's `status.storedVersions` is exactly `[v1]`; points at Strimzi's API-conversion procedure |
| newer → older, **requested by name** | refused: Strimzi does not support operator downgrade; `kates clean` and redeploy is the way back |
| newer than the pin, **nothing requested** | the installed operator is kept, and its window — not the pin's — decides the Kafka version. See below |
| scope change | cluster → namespace after confirmation; namespace → cluster refused while additional operators exist (§3.1) |

The second-to-last row is the difference between a request and a default, and it matters more than it looks. The repository pin is what `kates deploy` uses when nobody says otherwise; a cluster that has moved ahead of the checkout — someone ran `--strimzi-version latest`, or the working copy is simply older — is an ordinary state, not an error. Resolving the pin first and *then* discovering the installed version turns that state into "Strimzi 1.2.0 is installed and 1.1.0 was requested", a refusal quoting a version the operator never typed, and the platform becomes undeployable until they run `kates clean`. So the installed operator is read **before** the version is chosen (step 2b of the resolution order), and a newer one is adopted: source `installed`, action `same`, the window read from that operator's chart, and a note saying so. `--strimzi-version` still wins — including when it loses to the row above.

An **additional** operator is upgraded through its cluster: `kates clusters upgrade-operator --name legacy41 --strimzi-version 1.1.0` applies the same table to that release (no CRD hook — the primary's CRDs are already at least that new), and is refused above the primary's version.

For the primary's Kafka version the same shape applies, one level down: same → converge; **older → newer** inside the window → a rolling upgrade driven by the operator, after a confirmation that says the metadata version stays where it is (so a rollback remains possible) and points at the playbook's sequencing (pause consumers, `make gameday` after); **newer → older** → refused with the playbook's rollback section, because a Kafka downgrade is legal only while the metadata version has not moved, and that is an operator's decision, not a flag's. `--strimzi-version` and `--kafka-version` together on an existing installation run the operator step first and the Kafka step second — the order Strimzi requires.

### 3.6 `kates clusters` — the additional clusters

A new noun, plural, so it does not collide with `kates cluster` (the primary's metadata via the backend):

```text
kates clusters list                     every Kafka in the Kubernetes cluster: name, namespace, version, provider
                                        (strimzi|legacy), operator (namespace/version), ready, bootstrap — discovered
kates clusters add     --name krafter-42 --version 4.2.1 [--namespace kafka-krafter-42] [--ha] [--values f.yaml] [--label k=v]
kates clusters add     --name legacy41   --version 4.1.2 --strimzi-version 1.0.1        namespace scope only
kates clusters add     --name legacy28   --version 2.8.2 --provider legacy [--image …]
kates clusters status  --name …
kates clusters remove  --name … [--yes]           the Kafka, its co-located operator if any, and its namespace if it created it
kates clusters upgrade-operator --name … --strimzi-version …                              namespace scope only
kates clusters attach-ui --name …        append the cluster to Kafka UI's cluster list (Strimzi provider only)
```

`--version` is required here — there is no "newest" default, because the point of an additional cluster is a version that differs from the primary; with no `--version` the command prints `kates versions` and stops. `--label` stamps every release the command creates (through `commonLabels`), which is how `kates migrate` (§3.9) marks the source and target it creates and finds them again.

**Provider selection is a function of the version, the scope, and the operators the cluster has or can have:**

| Requested version | Cluster scope | Namespace scope |
|:---|:---|:---|
| in the primary operator's window | `strimzi` — the cluster-wide operator reconciles it | `strimzi` — a co-located operator at the primary's version is installed with it |
| in the window of an eligible other Strimzi (same generation, ≤ primary's; adjacent unless `--allow-nonadjacent`) but not the primary's | **warned and refused under Strimzi** (§3.1); `--provider legacy` runs it without an operator | `strimzi` with `--strimzi-version <that operator>` — the CLI names the newest eligible one when the flag is missing |
| ≥ 2.1.0, in no eligible window (2.x, 3.x, and 4.0.x today) | `legacy` — the CLI says why no Strimzi can run it (§2.3) | same |
| < 2.1.0 | refused | refused |

The `legacy` provider then picks its own mode from the version — ≥ 3.7.0 the official `apache/kafka:<v>` image (KRaft); ≥ 3.3.0 the image built by `Dockerfile.legacy-kafka` (KRaft, JRE 17); ≥ 2.1.0 the built image with ZooKeeper (JRE 11) — the rule the chart itself will carry (§3.7). The 2.1 floor is a platform decision, not a build limitation: the Dockerfile could package 2.0, but nothing here — not the backend, not MM2, not the 4.x CLI in a pod — could speak to it, so a cluster nobody can reach is not offered.

Under cluster scope `--strimzi-version` on `clusters add` is refused outright — there is one operator, and `kates deploy --strimzi-version` is how its version changes.

**Discovery, not a registry.** `list` is `kubectl get kafka -A` (Strimzi: `spec.kafka.version`, `status.conditions`, the bootstrap Service, and the operator whose watch set contains the namespace) plus Helm releases of `legacy-kafka` (their StatefulSets carry `app.kubernetes.io/version` and `kates.io/kafka-mode`, their bootstrap Service the `kates.io/bootstrap-address` annotation the chart already writes). A ConfigMap registry would be a second source of truth that drifts the first time someone runs `helm uninstall` by hand.

**One namespace per cluster, enforced.** `add` defaults the namespace to `kafka-<name>` and refuses a namespace that already holds a `Kafka` CR, a `legacy-kafka` release, or a Cluster Operator, because §2.2's fixed-name resources make sharing a namespace a Helm ownership error at best and a silent selector overlap at worst — and because in namespace scope the namespace *is* the operator's boundary.

**Additional Strimzi clusters get the shared bits turned off.** `add` layers a values file — `drainCleaner.enabled=false`, `crdUpgrade.enabled=false`, `dashboards.enabled=false`, `podSecurityPolicy.enabled=false`, `networkPolicies.operatorPolicy.enabled=false` — on top of the environment overlay and the user's `--values`, and passes `clusterName`, `kafkaVersion`, `strimziVersion` (the operator that will reconcile it), `networkPolicies.operatorNamespace` (that operator's namespace) and the derived `metadataVersion` the same way `deploy` does. Sizing defaults to the single-node kind overlay unless `--ha`, because two three-zone clusters do not fit a laptop.

### 3.7 Chart work

`charts/strimzi-operator`:
- Scope values: `watchAnyNamespace` and `watchNamespaces` are already passed through; `values-namespace-scope.yaml` documents the additional-operator shape (`createGlobalResources: false`, `crdUpgrade.enabled: false`, kind-sized resources).
- `globalBindings.enabled` (default off; on for additional operators): renders the three cluster-scoped bindings — to `strimzi-cluster-operator-global`, `strimzi-kafka-broker`, `strimzi-kafka-client` — for this release's ServiceAccount under the names `strimzi-cluster-operator-<release namespace>[-…]`, so an additional operator keeps rack awareness and volume-expansion checks without touching upstream's fixed names.
- The operator's NetworkPolicy moves here from `kafka-cluster` (§2.2): one policy per operator, egress to every namespace it watches.
- The CRD hook's `EXPECTED_CRDS=10` becomes "the ten CRDs this platform needs are present" — the named list it already verifies — so a bundle with more CRDs (older releases) or fewer (a future one) is judged by what matters rather than by an exact count. The hook stays disabled on additional operators; the comment says why in one sentence (*CRDs belong to the newest operator*).
- `values.schema.json` keeps its strictness on the wrapper's own keys and stays permissive under `strimzi-kafka-operator:` (it already allows keys it does not list), so subchart keys that exist in one version and not another are the subchart's business.
- `NOTES.txt` prints the operator version installed, its scope and watch list, and, when it differs, the repository's pin.

`charts/kafka-cluster`:
- `Chart.yaml` annotation `kates.io/kafka-floor: "4.2.0"` with the reason (share groups); the CLI reads it for the primary, `check-versions.sh` asserts the default `kafkaVersion` is at or above it.
- `group.share.enable` and the other share-group keys render only when `kafkaVersion` ≥ 4.2.0, so a 4.1.x additional cluster starts.
- `networkPolicies.operatorPolicy.enabled` (default true, for standalone chart users) gates the `strimzi-operator` policy the wrapper now owns; `values-additional.yaml` turns it off.
- `networkPolicies.defaultDenySelector` / `allowDNSSelector` default to a template-derived `app.kubernetes.io/part-of: strimzi-<clusterName>`; an explicit value still wins.
- A `values-additional.yaml` overlay with the shared toggles off, so the same file serves `kates clusters add` and a hand-run `helm install`.
- `values.schema.json` accepts `kafkaVersion` as any `x.y.z` and `metadataVersion` in both the short (`4.2`) and `-IV` forms; the *window* check is the operator's and the CLI's job, not the schema's — the chart must stay usable with a newer operator than the one pinned here.
- `NOTES.txt` prints the version actually deployed, the metadata version chosen, and the operator expected to reconcile it.

`charts/connect-cluster` and `charts/mirror-maker2`: no template change; their `version` (and `strimziVersion`) become values the CLI sets from the resolved pair, and `connect-cluster` `version` joins the pin gate. MM2's `strimziOperatorNamespace` already exists for the NetworkPolicy and is set per scope.

`charts/legacy-kafka`:
- Generalise the two overlays into a rule: `mode` defaults from the version (`< 3.3.0` → `zookeeper`, else `kraft`); `kafka.image` defaults to `apache/kafka:<version>` for `≥ 3.7.0` and `ghcr.io/bmscomp/kates-legacy-kafka:<version>` below, so `--version 3.5.2` needs no overlay. The existing `values-kafka-2x.yaml` / `values-kafka-3x.yaml` remain as documented examples of the two shapes.
- `scripts/build-legacy-kafka-image.sh` (and its planned `kates migrate image build` successor) already accepts any version; nothing to change but the docs.

`charts/kafka-ui`:
- `clusters` becomes a list of `{name, bootstrapServers, namespace, auth}` rendered into Kafbat's multi-cluster config, with the primary first; `attach-ui` appends.

### 3.8 CLI work

- `pkg/kafkaversion`: parse `STRIMZI_KAFKA_IMAGES` from a Deployment JSON, a rendered chart, or a chart tarball's `_kafka_image_map.tpl`; `Window()`, `Newest()`, `Supports(v)`, `MetadataFor(v, window)` (short form), `ProviderFor(v, scope, operators, catalogue)`, semver ordering. Pure functions, table-tested against the 1.1.0 map and against a synthetic future map (to prove the code does not assume three entries).
- `pkg/strimzi`: the catalogue (`helm search repo` with a kates-owned `--repository-config`, cached 24 h, `--refresh`), `Pull(version)` into the cache, `ReadChart(tgz)` → window, served/stored CRD versions, image tag; `Floor(chart, kafkaFloor)` → ok or the reason; `WrapperFor(version, scope)` → the generated wrapper directory (§3.2); `Installed(ctx)` → every Cluster Operator on the cluster with version, scope, watch set, window, role; `Eligible(candidate, primary, crds)` → the §3.1 rules (generation, ≤ primary, adjacency) with the reason when not; `UpgradePath(installed, requested, running)` → allowed, refused-with-reason, or the intermediate version. Tested with recorded index JSON, recorded Deployment lists, and the vendored tarball; the network is behind an interface with a fake.
- `cmd/deploy.go`: `--operator-scope`, `--strimzi-version`, `--strimzi-chart`, `--kafka-version`, `--kafka-name`, `--kafka-namespace`; the `primary` struct; the resolution order at the top of this section runs before the plan is printed, so `--dry-run` shows resolved facts. `deploy_components.go` installs the operator from `WrapperFor` when the version or scope is not the default, computes the watch list from the topology, and reads `dc.primary` everywhere it wrote `krafter`; `deploy_status`, `clean`, `portforward`, `doctor_network`, `silent_wait`, `auto`, `detect` follow. `kates status` and `doctor` show the operator version, scope and, when it differs, the pin; `doctor` flags a mixed scope.
- `cmd/deploy_plan.go`: the scope and versions groups in the interactive form (§3.4).
- `cmd/versions.go`, `cmd/operators.go`: the families in §3.4.
- `cmd/clusters*.go`: the family in §3.6, on `internal/helm` and `internal/kubectl`; `add` installs the co-located operator before the Kafka and waits for it; `remove` uninstalls in the reverse order; `clean` learns to discover Kafka CRs, legacy releases and additional operators rather than carry a fixed release list, and removes the primary's operator last, so `kates clean` removes what `kates clusters add` created and never leaves an operator without its CRDs' owner.
- `kates test helm` `knownComponents` gains `legacy` and treats any discovered Strimzi cluster as testable by name.
- Docs: `doc_entries.yaml`, `tldr`, the "Deployment & Lifecycle" section of the CLI reference, and a new "Choosing the operator scope and versions" section in the installation guide that shows the two shapes, the catalogue, the pairing, and how to see them (`kates versions`, `kates operators list`).

### 3.9 `kates migrate --from X --to Y` — the front door

Everything above exists for one purpose: to stand up an **old** Kafka and a **new** Kafka on one Kubernetes cluster and test the migration between them with MirrorMaker 2. The operators, scopes, providers and windows are the machinery; the user's intent is a pair of versions. So the front door takes the pair and nothing else:

```text
kates migrate pairs                                   every old → new pair this cluster can stand up now, with the provider each source would get
kates migrate plan    --from 2.8.2 [--to 4.3.0]       what `up` would create — providers, operators, namespaces, MM2 settings, requests — no changes
kates migrate up      --from 2.8.2 [--to 4.3.0] [--name m282-430] [--source-provider auto|strimzi|legacy]
                      [--source-strimzi-version 1.0.1] [--policy identity|default] [--topics kates.orders] [--messages 200]
                      [--sasl|--plaintext] [--values-source f.yaml] [--values-mirror f.yaml] [--yes] [-i]
kates migrate status   [--name …] [--watch]           source, target, MM2 CR, connectors, lag, end offsets on both ends
kates migrate verify   [--name …] [-o json]           seed → mirror → read-back diff → offset translation → cutover rehearsal; the report
kates migrate cutover  [--name …] [--yes]             source connector stopped, checkpoints running; proves the target is frozen
kates migrate rollback [--name …] [--yes]
kates migrate down     [--name …] [--yes]             the mirror, the source and its operator, the lab's topics on the target — found by label
kates migrate run     --from 2.8.2 [--to 4.3.0] [--keep] [--skip-build] [-o json]     up → verify → cutover → down: one report, one exit code
```

The building blocks the [CLI-migration plan](mirror-maker2-cli-migration-plan.md) defines — `mirror deploy --from <name>`, `mirror preflight`, `target topics|offsets|groups|group`, `image build`, `source deploy|status|remove` — stay as they are; `up` is composed from them and from `kates clusters add` (§3.6), and `run` is `up` + `verify` + `cutover` + `down`.

**Resolving `--from` — the source.** The same rules as `clusters add`, applied without asking, because the intent (a Kafka at that version, to migrate *from*) is unambiguous:

| `--from` | Source provider | Operator | Notes |
|:---|:---|:---|:---|
| < 2.1.0 | refused | — | below the floor a 4.x MirrorMaker can read (KIP-896) |
| in the primary operator's window | `strimzi` | cluster scope: the primary's; namespace scope: a co-located one at the primary's version | both ends operated; TLS from the source's Cluster CA; `kates-mm2` user on the source |
| in an eligible other operator's window (§3.1) | namespace scope: `strimzi` with that operator — the newest eligible unless `--source-strimzi-version`; cluster scope: `legacy`, printing the §3.1 sentence once | namespace scope: co-located, older than the primary's | the "dropped line" scenario — 4.1.2 under 1.0.1 beside a 1.1.0 primary; `--source-provider strimzi` under cluster scope turns the sentence into the refusal |
| ≥ 2.1.0, in no eligible window (2.x, 3.x, 4.0.x today) | `legacy` | none | ZooKeeper below 3.3.0, the built KRaft image 3.3–3.6, the official image from 3.7.0; `image build --load` runs first when kind lacks the image (`--skip-build` as in the CLI plan) |

**Resolving `--to` — the target.** Omitted, it is the primary as it runs — the platform's own cluster, with the backend, Kafka UI and `kates test` already pointed at it, so a migration that ends there ends where the platform's consumers are. Equal to the primary's version, the same. Another version inside the primary operator's window creates an additional target `<name>-tgt` (§3.6, reconciled as any additional in-window cluster is) and the report says the target is not the platform's primary. Outside that window it is refused with the window and `kates deploy --strimzi-version` named as the way to a newer target. MirrorMaker runs on the target side, `spec.version` = the target's version, reconciled by the target's operator. A `--from` that is not older than `--to` is a warning, not a refusal — MirrorMaker does not care about direction, only the user does.

**What `up` creates, and how it is found again.** The lab is named `m<from>-<to>` with the dots removed (`m282-430`), or `--name`. The source is cluster `<name>-src` in namespace `kafka-<name>-src` (plus its operator, namespace scope), the optional target `<name>-tgt` in `kafka-<name>-tgt`, the mirror release `mm2-<name>` in the target's namespace with group id `mm2-<name>` and alias `source`. Every Helm release carries `kates.io/lab: <name>` and `kates.io/lab-role: source|target|mirror` through `commonLabels` — which `kafka-cluster`, `legacy-kafka` and `mirror-maker2` gain for the purpose — so `status`, `cutover` and `down` discover the lab from the cluster rather than from a state file, and `down` removes exactly what `up` created and nothing else. Corpus topics default to `kates.orders`; a second lab against the same target with an overlapping topic set is refused with `--topics` as the way out, because under the identity policy two mirrors writing one topic name is data corruption, not a test.

**The mirror's values are written from the pair**, by the CLI-migration plan's values writer, on top of the era overlay: `values-migrate-2x.yaml` for a 2.x source, `-3x` for 3.x, and a new `values-migrate-4x.yaml` for a Strimzi source — in-cluster reference, TLS from the source's CA, the `kates-mm2` credential copied by the chart's `secretSync`, both ends operated. Common to all: the identity policy unless `--policy default`, `compatibility.minSourceVersion` from the source version, the preflight Job on, `topicsPattern` from `--topics`, `target.brokerCount` and replication factor from the target's sizing, the source's `mm2reader` SASL secret (legacy, `--sasl`) or plaintext, and the `cutover` overlay applied by `cutover`. `plan` prints the generated file; `--values-mirror` layers on top of it.

**The interactive pair picker** (`up -i`) is built from `pairs`: a `Select` for the old version listing every candidate with its provider and operator — `2.8.2 — legacy (ZooKeeper, built image)`, `3.9.1 — legacy (official image)`, `4.1.2 — Strimzi 1.0.1 in its own namespace` or, under cluster scope, `4.1.2 — legacy (the cluster-wide Strimzi 1.1.0 cannot run it)`, `4.2.1 — Strimzi (primary's operator)`; a `Select` for the new version from the primary operator's window with the primary's version preselected; then policy, topics and message count. What it will create, with the resource requests, is shown before anything is — the `plan` output — and `--yes` skips the confirmation only.

```text
$ kates migrate pairs
FROM (source)   PROVIDER                              TO (target)              NOTE
2.1.0 – 3.2.x   legacy — ZooKeeper, built image       4.2.0 4.2.1 [4.3.0]      image built on first use
3.3.0 – 3.6.x   legacy — KRaft, built image           "                        
3.7.0 – 4.0.x   legacy — KRaft, official image        "
4.1.0 – 4.1.2   Strimzi 1.0.1, own namespace          "                        namespace scope; legacy under cluster scope
4.2.0 – 4.3.0   Strimzi, primary's operator           "                        in-window: both ends on the same operator version
```

**`run` is the CI leg**, and the matrix is the set of source shapes: `2.8.2 → primary` (ZooKeeper, built image), `3.9.1 → primary` (official image, plus the default-policy variant), `4.1.2 → primary` (namespace scope, a 1.0.1 source operator beside the 1.1.0 primary), `4.2.1 → primary` (in-window, the primary's operator on both ends, TLS on). The report keeps the script's rows — cluster reachable, Strimzi CRDs, target Kafka Ready, target credentials, source deployed, source serving, corpus produced, source end offsets, source consumer group, MirrorMaker 2 installed, CR Ready, connectors RUNNING, replicated topic exists, record count, record content, offset translation, cutover applied, cutover froze the target — under a header that states the pair, the providers, the operators and the policy; `-o json` carries it per row; the exit code is `1` on any failed row.

**What the lab proves that the script could not.** The target is the platform's primary, so `kates test` after `cutover` is a valid last row: the platform's own consumers on the migrated cluster. A Strimzi source means users, ACLs and TLS are exercised on *both* ends, not only the target. An operated 4.1.2 source is the real "dropped line" estate rather than a bare StatefulSet standing in for it. And `plan` is a document: what a given pair needs on this cluster, printed before a single pod starts.

**Docs.** Tutorials 11 and 12 become one-command labs — `kates migrate run --from 2.8.2`, `--from 3.9.1` — with the chart-level steps they show today kept under "what it does"; a new tutorial 13 covers `4.1.2 → 4.3.0` with two operators under namespace scope; the book chapter follows.

---

## 4. Version discovery and the gates

| Where a fact comes from | When | Used for |
|:---|:---|:---|
| the vendored operator tarball → `_kafka_image_map.tpl`, `crds/` | default deploy, `--dry-run`, CI, offline | the pin's window and API; the pin gate |
| a pulled operator tarball in `$XDG_CACHE_HOME/kates/strimzi/` | `--strimzi-version` other than the pin; additional operators | that version's window, API and image tag, before install |
| the Helm index at `strimzi.io/charts` (kates-owned repo config, cached 24 h) | `kates versions strimzi`, `--strimzi-version latest`, the picker, the eligible-operator and upgrade-path suggestions | which versions exist and when they were published |
| every Deployment labelled `strimzi.io/kind: cluster-operator`, in any namespace: image tag, env `STRIMZI_NAMESPACE` (`*` or a list), env `STRIMZI_KAFKA_IMAGES` | operators installed | the installed operators, their scope, watch sets and windows — `operators list`, `clusters add`, the scope and upgrade comparisons |
| the Helm release `strimzi-operator` in each such namespace: `APP VERSION` | operators installed | cross-check of the Deployment's tag; whether kates installed it (a Deployment without a wrapper release is *foreign*, listed and left alone) |
| every Strimzi CRD's `status.storedVersions` and served versions | crossing 1.0; admitting an additional operator | the installed CRD generation |
| Helm releases labelled `kates.io/lab=<name>` / `kates.io/lab-role` | `kates migrate status|cutover|down` | the lab's source, target and mirror — found, not recorded |
| `charts/kafka-cluster/Chart.yaml` `kates.io/kafka-floor` | always | the primary's floor |
| `scripts/check-versions.sh` | CI | asserts the default `kafkaVersion` is the newest entry in the vendored window (so the chart's pinned default and the CLI's computed default are one version) and at or above the floor; asserts `connect-cluster` `version` agrees with the other Kafka pins; asserts the vendored tarball's chart version equals the six Strimzi pins; asserts `values-kind.yaml`'s `metadataVersion` is ≤ the default version's metadata |

There is deliberately no version list in Go, in `versions.env`, or in a values file — not of Kafka versions, not of Strimzi versions, not of which Strimzi runs which Kafka, not of which operators can coexist. Each operator chart is the only authority on what it runs and which API it serves; the index is the only authority on which charts exist; the live Deployments are the only authority on which operators run and what they watch; the repo carries the pin's statement verbatim and the CLI reads the others on demand.

---

## 5. Phases and acceptance criteria

**Phase 0 — Version discovery** (`pkg/kafkaversion`, `pkg/strimzi`, `check-versions.sh`)
- [ ] `Window()` returns `[4.2.0 4.2.1 4.3.0]` from the vendored tarball, from a rendered chart and from a captured Deployment JSON; all paths tested
- [ ] `Newest()` returns `4.3.0` from all sources
- [ ] `MetadataFor("4.3.0")` = `4.2`, `MetadataFor("4.2.1")` = `4.2`, `MetadataFor("4.1.2")` under a 1.0.1 window = `4.1`; a synthetic window `[4.3.0 4.4.0]` gives `4.3` for 4.4.0 — the rule, not a table; a kind deploy with `metadataVersion: "4.2"` reports `status.kafkaMetadataVersion: 4.2-IV1`
- [ ] `ReadChart` on the vendored tarball yields window, served `[v1]`, stored `v1`, tag `1.1.0`; on a recorded 0.51.0 tarball yields `[4.1.0 4.1.1 4.2.0]`, served `[v1beta2 v1]`, stored `v1beta2`; on a recorded 0.48.0 tarball `Floor` says "no `v1`"
- [ ] `Installed` parses a recorded Deployment list into operators with scope `cluster`/`namespaces`, watch sets and windows; `Eligible(1.0.1, primary 1.1.0)` = adjacent; `(1.0.1, primary 1.2.0)` = non-adjacent with the caveat; `(0.51.0, primary 1.1.0)` = refused (generation); `(1.2.0, primary 1.1.0)` = refused (newer than primary)
- [ ] The catalogue parses a recorded `helm search repo` JSON; the network interface has a fake; nothing in the test suite touches the network
- [ ] `check-versions.sh` fails on a default `kafkaVersion` outside the vendored window, on one inside it that is not the newest, on a `connect-cluster` `version` that disagrees, and on a vendored tarball whose version differs from the pins (each verified by a deliberate break)

**Phase 1 — `kates deploy` with scope and versions**
- [ ] `kates deploy` with no flags installs the pin from the repo directory, offline, in cluster scope, exactly as today; `--dry-run` shows `scope: cluster`, `Strimzi 1.1.0 (pinned)` and `Kafka 4.3.0 (newest supported…)`
- [ ] `--operator-scope namespace` installs the primary's operator with `STRIMZI_NAMESPACE=kafka,connect` (isolated topology) or `kates-stack` (single); RoleBindings exist in each watched namespace; no ClusterRoleBinding named `strimzi-cluster-operator-namespaced` exists; the primary, Connect and MM2 reconcile
- [ ] Cluster scope, `--kafka-version 4.1.2` for the primary: refused before Helm with the §3.1 message naming the legacy provider (for additional clusters) and namespace scope
- [ ] `--strimzi-version 1.0.1` on a fresh kind: the chart is pulled once, `helm list` shows `APP VERSION 1.0.1`, the CRD hook applied the 1.0.1 bundle, the primary runs 4.2.0 by default, Connect and the Helm-test image are `1.0.1-kafka-4.2.0`; `kates status` shows `Strimzi 1.0.1 (pin: 1.1.0)`
- [ ] `--strimzi-version 0.50.1` fails with "no Kafka ≥ 4.2.0 in its window"; `--strimzi-version 0.48.0` fails with "serves no `v1` API"
- [ ] `--strimzi-version 1.0.1 --kafka-version 4.3.0` fails with the 1.0.1 window and names 1.1.0 as an operator that has it; `--kafka-version 4.1.2` on the pin fails the same way in the other direction
- [ ] `--kafka-version 4.2.1` deploys a 4.2.1 primary; the Helm test image is `1.1.0-kafka-4.2.1`; `--kafka-version latest` equals the flagless deploy
- [ ] `kates deploy -i`: the scope select explains both shapes; choosing 1.0.1 offers `4.2.0 4.1.2 4.1.1 4.1.0` and nothing else; choosing 1.1.0 offers `4.3.0 4.2.1 4.2.0`; on a cluster with a cluster-wide 1.1.0 the operator select is fixed and the description carries the cluster-scope sentence
- [ ] `kates versions strimzi` lists the catalogue with the pin marked and 1.0.x marked eligible-as-additional under namespace scope; `kates versions kafka --strimzi-version 1.0.1` prints its window; `kates operators list` shows one line in cluster scope
- [ ] No `krafter` literal remains in `cli/cmd` outside flag defaults (a test greps for it); `--kafka-name other` produces a working primary named `other`

**Phase 2 — Existing installations**
- [ ] Installed 1.0.1 running 4.2.0, then `kates deploy --strimzi-version 1.1.0`: confirmation lists the CRs, the operator upgrades, the `Kafka` CR stays `Ready` on 4.2.0
- [ ] Then `kates deploy --kafka-version 4.3.0`: confirmation says the metadata version stays `4.2`, the primary rolls to 4.3.0, `status.kafkaMetadataVersion` is still `4.2-IV1`
- [ ] Installed 1.1.0, `--strimzi-version 1.0.1` refused (downgrade); installed 0.51.0 running 4.1.1, `--strimzi-version 1.1.0` refused with 1.0.1 named as the intermediate; installed 0.51.0 with `v1beta2` stored, `--strimzi-version 1.0.1` refused with the conversion pointer
- [ ] `--kafka-version 4.2.1` on a 4.3.0 primary refused with the rollback pointer
- [ ] Cluster → namespace scope on a running installation: confirmation, operator restarts, Kafkas do not roll, `operators list` shows the watch set; namespace → cluster refused while an additional operator exists, allowed once it is removed
- [ ] `kates doctor` on a hand-built mixed installation (one `*` operator plus a namespaced one) reports it; `deploy` refuses until fixed
- [ ] CI: a `strimzi-version` matrix leg (`pin`, `1.0.1`) for the fresh-install path, a scope leg (`namespace`), and one upgrade leg (`1.0.1/4.2.0 → 1.1.0 → 4.3.0`)

**Phase 3 — Charts** (scope overlay, unique-name bindings, operator policy move, floor annotation, share-group gating, hook count, `values-additional.yaml`, derived selectors, legacy generalisation, Kafka UI list)
- [ ] Two `kafka-cluster` releases in two namespaces render without a duplicate cluster-scoped, fixed-name or cross-namespace resource (a CI render + `kubeconform` of both, then a name-collision check that now includes the operator namespace)
- [ ] `kafka-cluster --set kafkaVersion=4.1.2` renders no `group.share.enable`; `4.2.0` does
- [ ] The wrapper with `globalBindings.enabled=true` renders three bindings named after the release namespace and none of upstream's three fixed names; two wrapper releases in two namespaces render without a collision when the second sets `createGlobalResources=false`
- [ ] The CRD hook passes on a bundle with eleven CRDs and fails on one missing `kafkanodepools`
- [ ] `legacy-kafka --set kafka.version=3.5.2` selects KRaft and the built image; `2.8.2` selects ZooKeeper; `3.9.1` the official image — all by rule
- [ ] Kafka UI renders a two-cluster config

**Phase 4 — `kates clusters` and additional operators** (Strimzi provider under both scopes, then legacy)
- [ ] Cluster scope: `add --version 4.2.1` alongside a 4.3.0 primary: both `Ready`, both listed, both pass `kates test helm`; `add --version 4.1.2` refused with the §3.1 message; `add --version 4.1.2 --provider legacy` succeeds and is listed as `legacy`; `add … --strimzi-version 1.0.1` refused outright
- [ ] Namespace scope: `add --name legacy41 --version 4.1.2 --strimzi-version 1.0.1` installs a 1.0.1 operator in `kafka-legacy41` (`createGlobalResources=false`, no CRD hook, unique-name bindings), then a 4.1.2 Kafka reconciled by it; the primary's 1.1.0 operator never touches the namespace; the CRDs stay at 1.1.0; `operators list` shows both with role and adjacency; `add --version 4.1.2` without the flag names 1.0.1; `add … --strimzi-version 1.2.0` refused (newer than primary); a non-adjacent version refused without `--allow-nonadjacent` and warned with it
- [ ] Namespace scope: `add --version 4.2.1` (in the primary's window) installs a co-located 1.1.0 operator and a 4.2.1 Kafka; both listed
- [ ] `add --version 2.8.2` and `--version 3.9.1` reuse `legacy-kafka` under both scopes and are listed with provider `legacy`; the message says why no Strimzi can run them
- [ ] `add` refuses a namespace that already holds a cluster or an operator; `remove --yes` deletes the Kafka, its operator and its namespace if it created them, in that order
- [ ] `kates clean` removes additional clusters and operators it discovers, the primary's operator last
- [ ] `clusters upgrade-operator --name legacy41 --strimzi-version 1.1.0` upgrades that operator in place (4.1.2 outside 1.1.0's window → refused; after moving the cluster to 4.2.0 → allowed)

**Phase 5 — The migration lab: `kates migrate --from/--to`** (with the CLI-migration plan's building blocks)
- [ ] `kates migrate plan --from 2.8.2` prints the source provider (legacy, ZooKeeper, built image), the target (the primary, its version), the mirror values (identity policy, `minSourceVersion 2.1.0`, preflight on, `topicsPattern`), the namespaces and the resource requests — and creates nothing
- [ ] `plan --from 4.1.2` under cluster scope says the source will be `legacy` because the cluster-wide 1.1.0 cannot run it; under namespace scope it names Strimzi 1.0.1 in `kafka-m412-430-src`; `--source-provider strimzi` under cluster scope is refused with the §3.1 message
- [ ] `plan --from 4.2.1` says the source is a Strimzi cluster on the primary's operator (cluster scope) or a co-located operator at the primary's version (namespace scope); `--to 4.2.1` on a 4.3.0 primary creates an additional target; `--to 4.1.2` is refused with the window
- [ ] `up --from 2.8.2` on kind: source Ready, MM2 Ready and connectors RUNNING, labels present on the three releases; `status` finds them by label; `verify` passes the read-back, translation and rehearsal rows; `cutover` freezes the target; `down` removes exactly the labelled releases and the lab topics, and the primary is untouched (its topic and user counts are the same before and after)
- [ ] `up -i` lists the candidates with their providers as `pairs` prints them, preselects the primary's version as the target, and shows the plan before creating
- [ ] A second `up` against the same target with the default topics is refused with `--topics` named; with `--topics kates.lab2.orders` it runs beside the first
- [ ] `run` passes in the CI matrix: `2.8.2`, `3.9.1` (and its default-policy variant), `4.2.1`, and `4.1.2` under namespace scope; each report carries the pair, providers and operators in its header; `--keep` leaves the lab and prints `status`/`down` for it; the script-era `--source-version` is accepted as an alias of `--from`
- [ ] `kates test` runs green against the primary after a `4.2.1 → primary` cutover
- [ ] `attach-ui` adds a lab source to Kafka UI; the primary stays first
- [ ] `kates migrate mirror deploy --from <name>` resolves a cluster *name* through `clusters list`, so a source is `--from m282-430-src`, not a bootstrap FQDN typed by hand

**Phase 6 — Docs**
- [ ] Installation guide: "Choosing the operator scope and versions" — the two shapes and what each allows, the catalogue, the pairing, the floors, the pin's meaning, air-gapped mirrors
- [ ] Tutorials 11 and 12 open with `kates migrate run --from 2.8.2` / `--from 3.9.1` and keep the chart-level steps under "what it does"; a new tutorial 13 covers `4.1.2 → 4.3.0` with two operators under namespace scope; the runbook's "how do I…" answers name `kates migrate status|cutover|rollback`
- [ ] Upgrade playbook: "Strimzi Operator Upgrade" and "Kafka Version Upgrade" drive `kates deploy --strimzi-version` / `--kafka-version` and `clusters upgrade-operator`, and drop the raw OCI `helm upgrade`; `deploying-strimzi-operator.md` "Upgrading the Operator" points the same way and gains a "Several operators" section that quotes Strimzi's rules (§2.3)
- [ ] The version matrix appendix points at `kates versions` and `kates operators list` for the live answer rather than listing windows that move every release

Rough size: Phase 0 ≈ 900 lines with tests (the chart reader, catalogue and operator discovery are new); Phase 1 ≈ 1100 (flags, scope, resolution, wrapper generation, picker, mostly replacing literals); Phase 2 ≈ 500; Phase 3 ≈ 450 of chart changes; Phase 4 ≈ 1200; Phase 5 ≈ 700 on top of the CLI-migration plan's packages (pair resolution, the lab composer, labels, `pairs`, the picker); Phase 6 ≈ 400.

Order of delivery, because the purpose is the lab: Phases 0–1 and the CLI-migration plan's Phases 0–2 first (the primitives), then Phase 5 with the `legacy` provider only — `kates migrate run --from 2.8.2` replaces the script before any operator work lands — then Phases 3–4 widen what `--from` can be, then Phase 2 and the rest.

---

## 6. Risks and decisions

- **The window moves.** Strimzi drops a Kafka line every one or two releases (4.1.x went in 1.1.0; 4.2.x will go). Because the window is read from the operator, a Strimzi bump changes what `--kafka-version` accepts, and what a flagless `kates deploy` picks, without a code change — and `check-versions.sh` fails the bump the moment the chart's pinned default is no longer the window's newest, which is the moment to raise the four pins. A cluster already running a dropped version keeps its pods; the operator stops reconciling it and says so, which is why §3.5 checks running versions before an operator upgrade rather than after. Namespace scope is the answer for the cluster that must keep running the dropped line: it keeps the old operator instead of losing reconciliation.
- **Two operators are Strimzi's tested maximum, and only adjacent ones.** The adjacency rule and `--allow-nonadjacent` encode exactly what Strimzi says it tests; the primary upgrade's confirmation names every additional operator that falls out of adjacency. Nothing stops a user from keeping 1.0.1 alive under a 1.3.0 primary, and nothing pretends Strimzi has tested it.
- **Cluster-scoped RBAC is shared and versioned.** Additional operators run on the primary's `ClusterRole`s (a newer superset, in practice); a Strimzi release that *removed* a rule an older operator still needs would surface as an RBAC error in that operator's log. `kates operators status` shows the operator's last reconciliation error for that reason. The unique-name bindings keep the additional operator's ServiceAccount bound without touching upstream's names.
- **CRDs have one owner and it is the primary's operator.** Every path that could apply CRDs from an older release — the wrapper's hook, `kafka-cluster`'s hook — is disabled for additional operators and clusters, and `clean` removes the primary's operator last. A foreign operator (one kates did not install) is listed and never touched.
- **Non-pinned operators need the network.** The pin installs offline from the vendored tarball; anything else pulls a chart and (through the hook) a CRD bundle. `--strimzi-chart` and `crdUpgrade.url` cover mirrors; `--dry-run` prints both URLs. On kind, `images.env` preloads only the pin's images — another operator's `operator:<v>` and `kafka:<v>-kafka-<k>` images are pulled from quay on first use, documented rather than automated.
- **Newer operators are untested.** The pin is what CI proves; `--strimzi-version` above it is allowed with a warning because trying the next release before the pin moves is a legitimate use of a testing platform, and because refusing it would only push people to the raw `helm upgrade` this plan retires. The warning names the pin.
- **The 1.0 boundary is real.** Operators before 1.0.0 store `v1beta2`; 1.0.0 removes it. The CLI can *see* whether the API-conversion step has happened (`storedVersions`) and refuses to guess; performing the conversion is Strimzi's tool's job and the playbook's territory. It is also why a pre-1.0 operator can never be an *additional* operator here.
- **Kafka 2.x and 3.x under Strimzi are out of reach, in either scope.** The operators that ran them serve `v1beta2` only, and this platform renders `v1` only; there is no CRD that satisfies both. `legacy-kafka` is not a fallback for those versions, it is the design — and the message says so rather than implying a flag would change it.
- **Namespace scope costs an operator per cluster.** Each additional Strimzi cluster is a controller, a broker, an Entity Operator *and* a Cluster Operator (384 Mi on the kind overlay); two of them plus the platform is at the edge of a 6-CPU/16-GB kind. Additional clusters default to single-node sizing, and `clusters add` prints the requests it is about to make. Sharing the primary's operator for in-window additional clusters (`--shared-operator`) would save the pod at the price of a rolling operator restart on every `add`; it is left out until someone needs it.
- **Changing the default scope** to `namespace` would make every fresh install ready for additional operators at the price of an explicit watch list that has to be right for every topology. The plan keeps `cluster` as the default because it is today's shape and the one the book describes; flipping it is a one-line decision once namespace scope has run in CI for a while.
- **Metadata versions do not go backwards.** Choosing 4.3.0's metadata for a 4.3.0 cluster forecloses a rollback to 4.2; the rule pins one behind, as the chart does today, and `--metadata-version` exists for the operator who has decided. Kafka upgrades of the primary are driven by `deploy` with that rule intact (§3.5); Kafka downgrades are refused. In-place Kafka version changes of *additional* clusters (`kates clusters upgrade`) are a separate plan; their *operator* changes are not (§3.5).
- **The lab's target is the primary by default.** It is the only cluster the platform's consumers point at, which makes "the migration ended where the platform lives" a testable statement; it also means `down` must be exact — it removes releases by label and the lab's topics by name, never the primary's users or topics, and Phase 5 counts them before and after to prove it. A lab that must not touch the primary passes `--to` with another in-window version and gets an additional target.
- **One lab at a time is the expected shape; two are allowed when their topics do not overlap.** The refusal exists because the identity policy makes two mirrors into one topic name a corruption, and because a laptop kind rarely fits a second source anyway.
- **The backend stays single-cluster.** Re-pointing it (`kates clusters use`) is one `helm upgrade` of the `kates` chart with a different bootstrap and is easy to add, but it changes what every `kates test` measures; it is left for a follow-up with its own design rather than slipped in here.
- **NetworkPolicies between clusters.** Each cluster's `kafka-brokers` policy admits its own consumers; a second Strimzi cluster is reachable from the primary's namespace only through the `mirrorMaker2Namespace` and test-pod selectors the MM2 work added. That is the right default — clusters are isolated unless a mirror is declared — and the plan does not open it further.
- **Config manifests bound to the primary** (`config/litmus`, `config/kafka`) stay bound. Chaos experiments target one cluster by design; making them multi-cluster is not a version question.
- **Scripts stay on the pin and on cluster scope.** `scripts/deploy-kafka-generic.sh` and `deploy-kafka.sh` keep installing the vendored operator cluster-wide; scope and version selection are CLI features, which is the direction the CLI-migration plan already set.

## 7. Non-goals

- Running an out-of-window version as the **primary**, in either scope.
- A second operator beside a **cluster-wide** one, or operators from two CRD generations on one cluster — both refused, never attempted.
- Strimzi's `STRIMZI_CUSTOM_RESOURCE_SELECTOR` pattern (several cluster-wide operators split by labels); namespace scope is this platform's isolation boundary.
- Operator downgrade, and performing the `v1beta2` → `v1` stored-version conversion — both refused with a pointer.
- Operator versions below the floor for the primary, and pre-1.0 operators as additional operators.
- Kafka 2.x and 3.x under Strimzi; `legacy-kafka` is the design for them.
- In-place Kafka version changes of *additional* clusters — the upgrade playbook's territory; the primary's are driven by `deploy` (§3.5), and additional *operators* by `clusters upgrade-operator`.
- A cluster or operator registry with its own state; discovery from the cluster is the registry.
- Multi-cluster support in the `kates` backend or in `kates test`.

# Admin-less Kates: enhancement plan

**Status: proposed. Nothing in it is implemented yet.**

`kates deploy --adminless` installs Kafka, Kafka Connect with its PostgreSQL, MirrorMaker 2, Apicurio, Kafka UI and the Kates API into namespaces an account already has, for an account that is not cluster-admin. This plan makes that a first-class way to run Kates rather than a deploy flag. A team with the built-in `admin` role in a few namespaces should install the stack on the first try. Every day-2 command should then work, or say plainly what it cannot see. The Kates API should keep as much of its value as namespaced rights allow, chaos included. Whatever does need a cluster admin should go out as one reviewed bundle, not as a chain of refusals.

Scope: the platform on an existing cluster. Creating a Kubernetes cluster without root on the machine (rootless Docker or Podman for kind) is a different problem and is noted at the end.

## 1. Where admin-less stands

Measured on main at v1.36.0 (`0a87b205`).

| | Today |
|---|---|
| Profile choice | One `kubectl auth can-i '*' '*' --all-namespaces` (`cli/cmd/deploy_adminless.go:41`). Yes means full, no means admin-less is offered, and anything in between is not modelled. |
| Install preflight | Strimzi present and watching the namespaces, plus `can-i create` on a hand-kept list of nine core kinds and the Strimzi CRs (`deploy_adminless.go:150`, `:340`). |
| Commands that know the profile | `deploy` only. |
| Charts with an admin-less values path | `kates` (`rbac.scope=namespace`). The other six install on their defaults. |
| Backend scope setting | None. The chart's only effect is `KATES_CHAOS_PROVIDER=noop` (`charts/kates/templates/configmap.yaml:185`). |
| Chaos without admin | None: every disruption step is "skipped" and the report still completes. |
| CI coverage | None: no job runs `--adminless` as a non-admin account. |

## 2. Findings

Severity: **B** means the install fails for a namespace `admin`. **D** means it installs, but a feature is lost or a result is wrong. **M** means a misleading message (a refusal reported as "not installed" or "pass").

### A. Install blockers in the charts

| ID | Finding | Where | Sev |
|---|---|---|---|
| A1 | kafka-cluster and connect-cluster render PodMonitors and PrometheusRules whenever the cluster serves `monitoring.coreos.com/v1`, even when monitoring was not chosen. The built-in `admin` role has no rights on them, and the preflight checks them only when the scrape option is picked. | `charts/kafka-cluster/templates/podmonitors.yaml:11`, `prometheusrule.yaml:21`, values `:589`, `:603`; `charts/connect-cluster/templates/podmonitor.yaml:3`, `alerts.yaml:15`; `cli/cmd/deploy_monitoring.go:48` sets them only to `true` | B |
| A2 | Apicurio is installed with no values. The chart hard-codes namespace `kafka`, cluster `krafter` and `krafter-kafka-bootstrap.kafka.svc`, and creates its KafkaTopics in `kafka`. Any other Kafka namespace is refused or wrong. | `cli/cmd/deploy_components.go:1587`; `charts/apicurio-registry/values.yaml:286-290`, `:317`, `:149-158` | B |
| A3 | Fixed UIDs fail OpenShift's `restricted-v2` SCC. In kafka-cluster they cannot be overridden, because the template merge skips nulls. | `charts/kafka-cluster/templates/_helpers.tpl:144`; connect-cluster `values.yaml:303`; mirror-maker2 `:417`; kafka-ui `:259`; cdc-postgresql `:84`; apicurio `:46`, `:231`; kates `:201`, `:371`, `:619` | B (OpenShift) |
| A4 | kafka-cluster creates two KafkaRebalance templates by default, which the preflight never checks. | `charts/kafka-cluster/templates/rebalance.yaml:14`, values `:351`, `:378` | B |
| A5 | kafka-cluster's viewer Role grants reads on kafkarebalances, kafkaconnects and kafkaconnectors. RBAC lets an account create only a Role it could exercise itself, so the Role fails without Strimzi's aggregated roles. | `charts/kafka-cluster/templates/rbac.yaml:26-54`, values `:665` | B |
| A6 | The default sizing (3 controllers, 3 brokers at 4Gi and 1 CPU, 100Gi disks, plus Cruise Control and the exporter) is about 15Gi of memory and 330Gi of storage. A namespace ResourceQuota will usually refuse it. `--set kafka.replicas` does nothing, because the resolver ignores it. | `charts/kafka-cluster/values.yaml:211-274`, `_resolve.tpl:188` | B (quota) |
| A7 | Every PVC relies on a default StorageClass, and a cluster without one leaves them Pending. The account usually cannot list StorageClasses to tell. | kafka-cluster `values.yaml:69`, cdc-postgresql, kates PostgreSQL `:607` | D |

### B. Install degradations

| ID | Finding | Where | Sev |
|---|---|---|---|
| B1 | Rack awareness is on in kafka-cluster and connect-cluster. The operator, not the user, must then create the `strimzi-<cluster>-kafka-init` ClusterRoleBinding, which a namespace-scoped operator installed without global bindings cannot do. The rebalance goals include `RackAwareGoal`. | kafka-cluster `values.yaml:163`, `:381`; connect-cluster `:285`; `charts/strimzi-operator/templates/global-bindings.yaml:11` | D |
| B2 | NetworkPolicies hard-code the namespaces `monitoring` and `strimzi-operator`, and the CLI sets neither. The cluster's own Prometheus is then cut off, and the operator survives only through Strimzi's own generated policies. | kafka-cluster `values.yaml:557-559`; connect-cluster `:584`, `:596`; mirror-maker2 `:744`, `:772`; apicurio `:149-158`; kafka-ui `:206` | D |
| B3 | Cluster introspection is skipped and the DNS domain is assumed to be `cluster.local`. | `cli/cmd/deploy.go:482-490` | D |
| B4 | The docs say the profile creates no namespace. In fact `ensureDeployNamespace` still tries `kubectl create` when a namespace reads as missing, and Apicurio's call still passes `--create-namespace`, which the seam happens to strip. | `deploy_components.go:108-134`, `:1587`; `docs/book/12-deployment.md:1107` | M |
| B5 | The preflight's resource list is written by hand, so it drifts from what the charts render (A1, A4 and A5 are all missing from it). | `deploy_adminless.go:150-197` | B (root cause) |

### C. Day-2 commands

| ID | Command | What happens without cluster admin | Where | Sev |
|---|---|---|---|---|
| C1 | `clean` | `helm list -A` is refused, so every namespace is "kept: could not list Helm releases". It looks only in its default namespaces unless `--*-ns` is repeated, and it never deletes the Kafka CRs or the PVCs. | `cli/cmd/clean.go:292`, `:361`; `clean_shared.go:80` | M |
| C2 | `deploy status` | Releases it cannot read show as "Not deployed", so the admin's Strimzi always reads "Not deployed". Component rows are hard-coded to the full-profile namespaces. | `cli/cmd/deploy_status.go:162-174`, `:322` | M |
| C3 | `detect`, `auto` | A refused `get nodes` aborts the run ("Cluster introspection failed", exit 0). There is no admin-less collector mode, and the write probes create namespaces. | `cli/pkg/detect/collector.go:150`, `:387`, `:78`; `cli/cmd/detect.go:113` | B |
| C4 | `doctor` | Kyverno checks run by default, and a refused CRD read fails as "Kyverno Installed: CRD not found". | `cli/cmd/doctor.go:167-186` | M |
| C5 | `doctor dns`, `doctor network` | `kubectl run` goes into `default`, which fails. A refused NetworkPolicy list reports **pass**. | `doctor_dns.go:67`; `doctor_network.go:313`, `:453` | B / M |
| C6 | `ports` | `get svc -A` is refused, so it falls back to `--app-ns` and `--kafka-ns` only. Kafka UI, Connect, Apicurio and MirrorMaker 2 elsewhere are not forwarded. | `cli/cmd/portforward.go:950-955` | D |
| C7 | `operators list` | Fails on `get pods -A`. | `cli/pkg/strimzi/installed.go:79`; `cli/cmd/operators.go:57` | B |
| C8 | `kafka connect *` | Falls back to `KATES_KAFKA_NS` or `kafka`, the wrong namespace unless `-n` is given. | `cli/cmd/connect.go:420-433` | D |
| C9 | `security audit`, `security drift` | A refused Kyverno read becomes "Kyverno is not installed", FAIL HIGH. Drift falls back to hard-coded namespaces. | `security.go:217`; `security_drift.go:205`, `:257` | M |
| C10 | `migrate *` | A refused CRD read becomes "KafkaMirrorMaker2 CRD is not installed. Run: kates deploy", and lab namespace creation fails. | `migrate_up.go:206`; `migrate_lab.go:531` | M |
| C11 | `kyverno status/apply/enforce/detect` | Admin-only by nature, but the failure reads as "not installed". | `kyverno.go:49`, `:76` | M |
| C12 | `disruption`, `resilience` | Under the noop provider every step is "skipped", the report completes, and nothing tells the user no fault was injected. | `kates/.../NoOpChaosProvider.java:29`; `DisruptionOrchestrator.java:341` | M |

### D. Backend under `rbac.scope=namespace`

| ID | Finding | Where | Sev |
|---|---|---|---|
| D1 | No backend setting knows the scope. Each feature discovers a 403 on its own, logs it, and degrades differently. | `kates/src/main/resources/application.properties`; `charts/kates/templates/configmap.yaml:185` | root cause |
| D2 | The service account has no Role in its own namespace, so `KatesNode` cannot read its own pod and the NODE_DRAIN guard assumes "may drain it". | `charts/kates/templates/rbac.yaml:97-150`; `KatesNode.java:59` | D |
| D3 | The Role grants no `watch` on pods, so the disruption pod-event timeline is empty. | `K8sPodWatcher.java:186` | D |
| D4 | The orphan reconciler lists NetworkPolicies `inAnyNamespace` at every start, and logs WARN on each 403. | `DisruptionOrphanReconciler.java:115-121` | D |
| D5 | Topology reads Connect and MirrorMaker 2 only in the Kafka namespace, so `--connect-ns` and `--mm2-ns` installs are invisible to it. | `ClusterTopologyService.java:253-1406` | D |
| D6 | `cluster alerts` returns zero alerts when PrometheusRules are forbidden, which looks the same as a healthy cluster. | `ClusterAlertsService.java:64`, `:107` | M |
| D7 | INTEGRATION_CDC discovers services and KafkaConnects `inAnyNamespace`, falls back to the hard-coded `kafka` and `database`, and then cannot create KafkaTopics or KafkaConnectors. | `CdcIntegrationService.java:105-139`, `:239-324` | B (that test) |

### E. Operations and process

| ID | Finding | Sev |
|---|---|---|
| E1 | Nothing records where an admin-less stack was installed, so every later command needs the `--*-ns` flags repeated. | D |
| E2 | What a cluster admin must do arrives as separate refusals across runs (Strimzi install, watch list, aggregate roles, the compliance Role). There is no single request to hand over. | D |
| E3 | No CI job runs the profile as a non-admin account, so every finding above shipped unnoticed. | root cause |
| E4 | The book documents the install path but none of the day-2 behaviour (C1–C12, D2–D7). | M |

## 3. Design principles

1. **Two personas, one handoff.** The platform admin acts once per team, and the team acts every day. Whatever Kates needs from the admin goes out as one reviewed bundle, generated from the same chart versions it will install, and is verified on return.
2. **Capabilities, not a yes/no.** Replace the single `can-i '*' '*'` with a capability matrix per namespace (a SelfSubjectRulesReview in each target namespace, plus a handful of cluster-scoped `can-i`). The profile becomes the set of features that matrix allows. That also covers the account that may create namespaces but not CRDs.
3. **Check what will be rendered.** The preflight derives its resource list from `helm template` of the exact releases and values it is about to install, then runs one `can-i` per kind and namespace. No hand-kept list can drift.
4. **A refusal is never an answer.** Every read that can be forbidden reports `unread (forbidden)`, distinct from "absent", "pass" or "0". One shared classifier in the CLI and one in the backend.
5. **Charts are namespace-safe by construction.** Each chart gets a `values-adminless.yaml` that turns off what a namespace `admin` cannot create, takes its namespaces from values, and runs under `restricted` Pod Security and OpenShift's random UIDs.
6. **Namespaced chaos is chaos.** Pod kills, network partitions, broker scale-downs and rolling restarts need only namespaced verbs in the Kafka namespace, where the team is already `admin`. Only node operations and Litmus stay admin-only.

## 4. The plan

Each wave is one PR, or a few, with its own tests and book changes. Waves 1 and 2 make the install and the commands right. Waves 3 and 4 add what admin-less cannot do today. Wave 5 proves it in CI.

### Wave 1 — the install succeeds on a stock namespace `admin` (fixes A1–A7, B1–B5)

| # | Item | Done when |
|---|---|---|
| 1.1 | **Capability matrix.** A `capabilities` package runs a SelfSubjectRulesReview per target namespace. It adds cluster `can-i` for namespaces, CRDs, ClusterRoles, StorageClasses and nodes, and API discovery for `monitoring.coreos.com`, `security.openshift.io` and `kafka.strimzi.io`. `resolveDeployProfile` uses it instead of `isClusterAdmin`, and the plan screen prints it. | One table drives the profile, the preflight, the chart values and the "left out" list. |
| 1.2 | **Render-then-check preflight.** Render every selected release with `helm template` and its real values. Collect the `(namespace, group/resource)` pairs and check each with `can-i create`. Separately list anything cluster-scoped that rendered, which is always a bug in admin-less. The hand-kept `adminlessGrants` list goes. | A chart change that adds a kind fails the preflight in CI before it fails a user. |
| 1.3 | **`values-adminless.yaml` per chart** (kafka-cluster, connect-cluster, mirror-maker2, apicurio-registry, kafka-ui, cdc-postgresql, kates). PodMonitors, PrometheusRules and the ServiceMonitor are off unless the scrape option is chosen (A1). KafkaRebalance templates are off, or kept when the matrix allows them (A4). kafka-cluster's viewer Role is created only when the matrix holds every grant it names, the same pattern as the compliance Role (A5). Rack awareness is off unless the operator's global bindings exist (B1). | `helm template` of each chart with the overlay renders no cluster-scoped kind, and nothing outside `admin` plus the Strimzi aggregated roles. |
| 1.4 | **Apicurio follows the deploy (A2).** Pass `kafkaTopics.namespace`, `kafkaTopics.clusterName`, `global.kafka.bootstrapServers`, `networkPolicy.kafkaNamespace` and `clientNamespaces` from the resolved namespaces, in both profiles. Drop the stray `--create-namespace`. | Apicurio works with `--kafka-ns team-kafka` and a cluster named other than `krafter`. |
| 1.5 | **Pod Security and OpenShift (A3).** Move every fixed `runAsUser`, `runAsGroup` and `fsGroup` into values. In kafka-cluster that means taking them out of `_helpers.tpl` so a null clears them. Add `--platform openshift`, auto-detected from `security.openshift.io`, which nulls them so the SCC assigns the UIDs. Every pod also passes `pod-security.kubernetes.io/enforce=restricted`. | All seven charts install into a namespace labelled `enforce=restricted`. Rendered with the OpenShift values, no pod names a UID. |
| 1.6 | **Size to the namespace (A6).** Read the namespace's ResourceQuota and LimitRange (namespace `admin` may), and sum the rendered requests, limits and PVC storage against them. Add `--size small|medium|large`: small is 1 controller and 1 broker, 1Gi, 10Gi disks, Cruise Control off. Make `kafka.replicas` either work or be refused. | A quota that cannot fit the plan is reported before Helm runs, with the size that would fit. |
| 1.7 | **Storage class (A7).** Try `get storageclass`. If it is allowed, name the default or stop when there is none. If it is forbidden, say the default is assumed, and add `--storage-class` to every PVC. | No silent Pending PVC. |
| 1.8 | **NetworkPolicy namespaces (B2).** Pass the monitoring and operator namespaces the matrix found, or the ones given on the command line, into every chart. The defaults stop naming `monitoring` and `strimzi-operator` in admin-less. | The cluster's Prometheus scrapes the stack when the scrape option is on. |
| 1.9 | **DNS domain (B3).** Read the cluster domain from `/etc/resolv.conf` of a short-lived pod in the app namespace, not `default`, and fall back to `cluster.local` with a note. | A cluster on `cluster.internal` gets correct bootstrap addresses. |
| 1.10 | **No namespace creation (B4).** In admin-less, a missing namespace stops the plan and goes into the admin bundle (4.1), never into a `kubectl create` attempt. | The book's "creates no namespace" is true. |

### Wave 2 — every command is honest (fixes C1–C12, E1)

| # | Item | Done when |
|---|---|---|
| 2.1 | **Deployment record (E1).** Deploy writes a ConfigMap `kates-deployment` in the app namespace (the account owns it). It records the profile, the resolved namespace map, the release names and chart versions, the capability matrix summary, and what was left out. A `deploymentRecord` loader in the CLI gives every command its namespaces; flags still win. | `kates clean`, `ports`, `deploy status` and `connect` need no `--*-ns` after an admin-less deploy. |
| 2.2 | **Forbidden is not absent.** One `kube.Classify(err, output)` helper returns Forbidden, NotFound or Error. It is used by every `kubectl get` in `cli/cmd` and `cli/pkg/detect`, and a lint test forbids a raw `get` whose error is dropped. | No refusal prints as "not installed", "pass" or "0". |
| 2.3 | **`clean` (C1).** Use the record's releases and namespaces. Uninstall them per namespace, delete the Kafka CRs and their finalizers, offer to delete PVCs, and never touch namespaces, CRDs or ClusterRoles in admin-less. | `kates clean` after an admin-less deploy leaves the namespaces empty of Kates. |
| 2.4 | **`deploy status` (C2).** Rows come from the record. The admin's Strimzi shows as "provided by the cluster (version, watches)" when visible, and "provided by the cluster (not visible to this account)" otherwise. | No "Not deployed" for components the profile does not own. |
| 2.5 | **`detect` and `auto` (C3).** An admin-less collector mode reads only namespaced data, and nodes, StorageClasses and CRDs when the matrix allows. Its write probes run in the app namespace and never create one. A forbidden section shows as `unread` in every renderer. | `kates detect` on a namespace account completes and says what it could not see. |
| 2.6 | **`doctor` (C4, C5).** Kyverno checks are skipped in admin-less with a reason. `dns` and `network` run their pods in the record's namespaces, and a forbidden NetworkPolicy list is `unread`, never `pass`. | `kates doctor` exits 0 on a healthy admin-less stack. |
| 2.7 | **`ports` (C6), `operators list` (C7), `connect` (C8).** `ports` forwards every component in the record. `operators list` falls back to the CRD's presence and the record's watch list. `connect` uses the record's Connect namespace. | All three work with no namespace flags. |
| 2.8 | **`security` and `kyverno` (C9, C11).** A forbidden Kyverno read is `unread`, the grade is not recomputed on it, and drift reads the record's namespaces. `kyverno` subcommands say "needs cluster admin" up front. | No HIGH finding from a refusal. |
| 2.9 | **`migrate` (C10).** A forbidden CRD read says so. Labs need namespaces, so in admin-less `migrate up` refuses up front, and names the namespaces for the admin bundle. | No "CRD is not installed" from a refusal. |
| 2.10 | **Say when chaos is off (C12).** `disruption run`, `playbook run` and `resilience run` refuse under the noop provider unless `--allow-noop` is given, with a message naming the namespaced provider (3.3). Reports carry `faultInjected: false`. | No disruption report reads as passed without a fault. |

### Wave 3 — the backend knows its scope, and chaos works without admin (fixes D1–D7, C12)

| # | Item | Done when |
|---|---|---|
| 3.1 | **`kates.kubernetes.scope=cluster|namespace` (D1).** Set from `rbac.scope` by the chart. A `ScopeAware` bean gives each feature its namespaces: Kafka, Connect, MirrorMaker 2, database, app. Code that would call `inAnyNamespace` uses them instead. `/api/health` and `whoami` report the scope. | No `inAnyNamespace` call runs in namespace scope, which a test enforces. |
| 3.2 | **Roles that match the features (D2, D3, D6, D5).** In namespace scope the chart adds a Role in the app namespace (get its own pod, and events). It adds `watch` pods, `list` prometheusrules and `get`/`list` services and endpoints in the Kafka namespace. It adds read-only Roles on KafkaConnect and KafkaMirrorMaker2 in their namespaces. Each is created only when the deploy's matrix holds the grants; otherwise it goes into the admin bundle. | Topology sees Connect and MirrorMaker 2 in their own namespaces, the pod timeline fills, and the NODE_DRAIN guard knows its own node. |
| 3.3 | **A namespaced chaos provider.** `kates.chaos.provider=namespace` uses the existing `KubernetesChaosProvider` operations that need only namespaced verbs in the Kafka namespace: pod delete (broker kill), NetworkPolicy create and delete (partition), KafkaNodePool and StatefulSet scale (scale-down), and annotation patch (rolling restart). Node drain, Litmus and CPU or I/O stress are refused with a reason. A `chaosRole` in the Kafka namespace grants exactly those verbs, which the team may create because it is `admin` there. Admin-less deploy makes it the default instead of noop. | `kates disruption run` kills a broker and measures recovery on an admin-less stack. Playbooks that need nodes are refused up front. |
| 3.4 | **Orphan reconciler (D4).** Scan only the scope's namespaces, and say once at INFO what it skipped. | No WARN per start in namespace scope. |
| 3.5 | **Alerts (D6).** A forbidden PrometheusRule list returns `unread`, not zero rules. The CLI prints "alert rules not readable by this account". | No silent "0 alerts". |
| 3.6 | **INTEGRATION_CDC (D7).** Namespaces come from 3.1. The test runs where the matrix holds create on KafkaTopic and KafkaConnector (the team's own namespaces under Strimzi's aggregated roles). Otherwise it is refused before it starts, with the missing grants. | The CDC test passes on an admin-less stack with Strimzi's aggregated roles. |

### Wave 4 — the admin handoff (fixes E2)

| # | Item | Done when |
|---|---|---|
| 4.1 | **`kates adminless request`.** From the capability matrix and the plan, it writes one directory for the admin: `README.md` (what is asked, why, and the exact scope of each grant); `namespaces.yaml` (missing namespaces, with labels and optional ResourceQuotas); `strimzi.sh` (the pinned operator install watching the team's namespaces with `createAggregateRoles=true`, or the watch-list change for an existing operator); `rbac.yaml` (aggregate ClusterRoles adding `monitoring.coreos.com` and Strimzi rights to `admin`, RoleBindings for the team, the kates compliance and Connect reader Roles); and `optional/` (a nodes-reader ClusterRole for zone-aware checks, the global bindings for rack awareness). Every file is rendered from the same chart versions the deploy will install. | An admin reviews one directory, applies it, and the team's next `kates deploy --adminless` passes its preflight with no further request. |
| 4.2 | **`kates adminless verify`.** Re-runs the matrix and the render-then-check preflight, and prints the remaining gaps against the request, in the same format. | The admin and the team see the same checklist. |
| 4.3 | **A tenant chart for platforms that onboard many teams.** `charts/kates-tenant`, installed by the admin, holds the same objects as 4.1 for one team, values-driven (namespaces, quota size, which features). | Onboarding a team is one `helm install` by the admin. |

### Wave 5 — prove it (fixes E3, E4)

| # | Item | Done when |
|---|---|---|
| 5.1 | **CI job "admin-less on kind".** Create a kind cluster. As admin, install Strimzi watching `team-*` and apply the 4.1 bundle. Then, as a ServiceAccount kubeconfig bound to `admin` in those namespaces only, run `kates deploy --adminless --yes --size small`. After that run `doctor`, `deploy status`, a LOAD test, a compliance check, a namespaced disruption, then `clean`. Every namespace is labelled `enforce=restricted`. | Red on any refusal or cluster-scoped render. |
| 5.2 | **Chart gate.** For each chart, `helm template -f values-adminless.yaml` passes kubeconform. A script fails on any cluster-scoped kind, and on any kind outside the `admin` plus Strimzi-aggregated set. | Wired into CI · Lint. |
| 5.3 | **OpenShift render check.** Render with `--platform openshift` values and assert no fixed UID. Optionally, a nightly OKD/CRC job. | — |
| 5.4 | **Book.** `12-deployment.md` gets "Running Kates without cluster admin" with the two personas, the request and verify flow, sizes, and a table of what each command does in admin-less. The limits list is rewritten from 3.x. `10-cli-reference.md` gets the new flags and commands, and `appendix-b` the new messages. | The book promises nothing the CI job does not test. |

## 5. Order and size

| Wave | Depends on | Size | Value |
|---|---|---|---|
| 1 | — | L (charts and CLI) | The install works for a namespace `admin`, on OpenShift and under `restricted` Pod Security |
| 2 | 2.1 needs 1.1 | M | Every command after the install tells the truth |
| 3 | 3.2 and 3.3 need 1.1 for the matrix gate | L (backend) | Chaos and full topology without admin |
| 4 | 1.1, 1.2 | M | One admin request instead of a chain of refusals |
| 5 | 5.2 after 1.3, 5.1 after 4.1 | M | It stays fixed |

Start with 1.1, 1.2 and 5.2 together: the capability matrix and the render-then-check preflight make the rest testable, and the chart gate stops new regressions while the waves land.

## 6. Decisions for the maintainer

1. **Chaos default in admin-less.** Should the namespaced provider (3.3) be the default, or opt-in with `--with-chaos=namespace`? It kills real broker pods in the team's own namespace.
2. **Size default.** Should admin-less default to `--size small` (1+1 brokers, no Cruise Control)? That fits typical quotas but is not production-like.
3. **The tenant chart (4.3).** Should it live in this repository and be published with the others?
4. **OpenShift.** Is OpenShift a supported target (CI in 5.3), or render-checked only?

## 7. Not in scope

- **Creating a cluster without root on the machine.** `make cluster` and `kates deploy` build kind with Docker. A rootless Docker or Podman kind provider is a separate plan: the kind provider flag, cgroup v2 delegation, and port mappings above 1024.
- **Litmus without admin.** Litmus needs its CRDs and ClusterRoles. Admin-less chaos is 3.3's namespaced provider instead.
- **Installing Strimzi without admin.** Its CRDs are cluster-scoped. 4.1 asks for it once.

## 8. Verification for every PR

The repository's usual gates: `go build`, `go vet`, cross-builds, `go test ./...`, race tests, golangci-lint over the whole tree, `./mvnw test` with spotless, `check-cli-compat.sh`, `check-book-style.sh` and `check-versions.sh`. From wave 1 onwards, also the chart gate (5.2). From wave 4 onwards, also the admin-less kind job (5.1).

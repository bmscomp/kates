# Kates Without Cluster Admin — Plan (2026-10-21)

**Status: shipped on 2026-10-21.** #439 brought the chart's `rbac.scope=namespace` and `rbac.kafkaNamespaceRole`, the conformity check's handling of refused reads, the `kates ports` fallback and the Deployment Guide's "Running Without Cluster Admin". #441 has the chart point chaos, topology and conformity at `networkPolicy.kafka.namespace` and `kafka.clusterName`, for every scope. #442 puts a fault that names no `targetNamespace` in the Kafka namespace. #443 is the book's Recipe 7, a CI gate on either install. D1, D2 and D3 went the recommended way. Where the shipped work departs from this plan:

- **D4, zones without node access.** They aren't left UNKNOWN. When the node list is refused, the check takes each Kafka pod's zone from the EndpointSlices of Strimzi's headless Service, `<cluster>-kafka-brokers`, and the Role grants `endpointslices` list too. Only `topology.kubernetes.io/zone` comes that way, so another `zoneLabel` stays UNKNOWN. Verified live on `panda` (#439).
- **Orphan recovery.** The chart doesn't set `KATES_CHAOS_ORPHAN_RECOVERY_ENABLED=false`, so a namespaced install still logs its WARN lines at boot.
- **Still open:** cluster-scoped releases still collide on the ClusterRole name, and `kates deploy` still runs `dns-detect` before it checks it can read nodes. A rule's `source` says nodes when the zones came from EndpointSlices; its `detail` says where they came from. The port-forward repair in `kates watch` assumes the `kates` and `kafka` namespaces. The consumer-isolation playbook names `targetNamespace: kates`, which the chaos guard refuses wherever Kates runs in another namespace.

This document is the plan as written before the work.

Goal: a team whose Kubernetes account is **not** cluster-admin (built-in `admin` or `edit` in its own namespace, read access to the Kafka namespace) can install Kates and run performance tests (LOAD, STRESS, SPIKE, ENDURANCE, VOLUME, CAPACITY, ROUND_TRIP, TUNE_*), INTEGRITY tests and conformity checks. A cluster admin has already installed Strimzi and the Kafka cluster. Chaos and disruption stay admin-only.

Source: a 9-agent study (4 mappers, 4 adversarial verifiers, 1 completeness critic over all 132 Kubernetes call sites in `kates/src/main/java`, every `kubectl`/`helm` exec in `cli/`, and every cluster-scoped kind in `charts/kates` and `charts/kates-platform`). The raw result isn't committed.

## What the Study Found

| Area | Kubernetes calls | Without cluster admin today |
|:--|:--|:--|
| Perf and INTEGRITY runs | **None**, from submission to report, on both backends. Topics go through the Kafka Admin API (`TopicService.java:54-72`). | Already work. No Kubernetes change needed. |
| Conformity check | Required: get the `Kafka` CR (`ConformityCheck.java:259-270`). Namespaced lists in the Kafka namespace: pods, KafkaNodePools, PVCs. Cluster-scoped: list nodes (zones, skew, rackAwareness with source cr); list PodMonitors in all namespaces (monitoring.podMonitor). `/version` is public. | A 403 on a list makes the rules that need it UNKNOWN and the report still comes back. A 403 on the `Kafka` CR get becomes **HTTP 503 "Kubernetes API not available"**, no report, exit 2, for every reference. |
| Kates API startup and jobs | Only chaos orphan recovery (all-namespace NetworkPolicy list, StatefulSet and KafkaNodePool lists). Health and readiness never call Kubernetes. Chaos providers are built lazily. | One WARN line per boot each; nothing fails. `kates.chaos.orphan-recovery.enabled=false` silences them, but the chart doesn't expose it. |
| Install (`charts/kates`) | ClusterRole + ClusterRoleBinding (`rbac.yaml:3,81`, on by default, named by fullname only). ServiceMonitor/PrometheusRule when enabled. Everything else is namespaced and covered by `edit`. | **Fails**: a team account can't create the ClusterRole. With the monitoring CRDs present, the ServiceMonitor fails too (no aggregated roles: `charts/strimzi-operator/values.yaml:253`, kube-prometheus-stack `createAggregateClusterRoles: false`). |
| `kates deploy` | Cluster gate, preflight, `get nodes`, namespace creation, Strimzi/cert-manager/monitoring/Litmus installs, deletes other namespaces' `kates` ClusterRole/Binding. | Admin-shaped end to end. Fails early ("Introspection failed"), and creates a busybox `dns-detect` pod in the team namespace on the way. |
| `kates ports` | `kubectl get svc -A` to find the Kates Service (`portforward.go:886`), hard-coded Service `kates`, Secret `kates-api-key`, key `api-key`. | Finds nothing ("No Kates services found"). The manual path works: `kubectl port-forward -n <team> svc/kates` + `kates ctx set --url --api-key`. |
| Test, apply, conformity, cluster check commands | None: plain HTTP to the Kates API with a Bearer key. | Already work once the API is reachable. |

Kafka side, not Kubernetes RBAC: on a cluster that enforces Kafka ACLs, the Kates principal needs topic CREATE/DESCRIBE/WRITE/READ on the test topics, **Group READ on `*`** (group ids carry random 8-hex run ids, so a prefixed ACL can't cover them unless `spec.consumerGroup` is set), TransactionalId on prefix `kates-`, and the outbox topic `kates-test-events` and group `kates-webhooks`. CreateTopics on an existing topic without CREATE answers TOPIC_AUTHORIZATION_FAILED, which `TopicService` (tolerates only TopicExistsException) turns into a failed submission.

## Target Setup

| Who | Does what | Rights needed |
|:--|:--|:--|
| Cluster admin, once | Strimzi, the Kafka cluster, a KafkaUser for Kates and its Secret in the team namespace | cluster-admin |
| Kafka-namespace admin, once per team | Applies the read-only Role + RoleBinding the chart renders for the Kafka namespace | `admin` in the Kafka namespace |
| Team | `helm install kates charts/kates -n <team> --set rbac.scope=namespace …`, then runs tests and conformity checks | `edit` (or `admin`) in the team namespace |

## Changes

1. **Chart: `rbac.scope: cluster | namespace`** (default `cluster`, unchanged behavior).
   - `namespace`: no ClusterRole or ClusterRoleBinding. Renders a Role + RoleBinding in the Kafka namespace (`networkPolicy.kafka.namespace`) bound to the Kates ServiceAccount: `kafka.strimzi.io` kafkas get, kafkanodepools list; core pods list, persistentvolumeclaims list; `monitoring.coreos.com` podmonitors list. Gated by `rbac.kafkaNamespaceRole` (default true) so a team without rights there installs with `false` and hands its admin `helm template … --show-only templates/rbac.yaml`.
   - `namespace` also sets `KATES_CHAOS_PROVIDER=noop` (unless `chaos.provider` is set) and `KATES_CHAOS_ORPHAN_RECOVERY_ENABLED=false`.
   - `values.schema.json`, `README.md`, and `ci.yml` helm-template assertions (no Cluster* kinds; the Role's rules; the two env vars).
2. **Conformity, Kates API** (`ConformityCheck.java`):
   - A 403 on the `Kafka` CR get names the missing grant ("the Kates API's service account may not get kafkas.kafka.strimzi.io in <ns>") instead of "Kubernetes API not available". Status stays 503 so the CLI's exit 2 is unchanged.
   - `monitoring.podMonitor`: on a 403 for the all-namespace list, list PodMonitors in the Kafka namespace. PASS when one there selects every broker pod; otherwise UNKNOWN (one elsewhere can't be ruled out), with a detail that says why.
   - A 403 on the nodes list keeps UNKNOWN, with a detail naming the cluster-scoped grant and the rules it decides.
   - Tests in `ConformityCheckTest` for the three 403 paths (none exist today).
3. **CLI `kates ports`**: when `get svc -A` is refused, list Services in `--app-ns` and `--kafka-ns` instead. Unit test with the existing kubectl stub.
4. **Docs**: a "Running Without Cluster Admin" section (Deployment Guide or Security & Compliance): who does what, the install command, the admin's Role, the Kafka ACLs, what stays admin-only (chaos, `kates deploy`, zones/skew/podMonitor without the cluster grants). The conformity appendix and chapter note which rules need cluster-scoped reads. Chart README row for `rbac.scope`.

Not doing (named, not built): a namespaced `kates deploy`; a namespaced zone source for `zones`/`skew` (EndpointSlices or broker racks — unverified); chaos without admin; the vendored `kates-0.9.1` in `kates-platform` (grants cluster-wide Secret reads — worth its own fix).

## Decisions

- **D1 — Kafka-namespace Role**: rendered by the chart behind `rbac.kafkaNamespaceRole` (recommended), or shipped as a separate manifest.
- **D2 — Role contents**: conformity reads only (recommended, least privilege), or also the reads behind `kates cluster topology` and `alerts` (services, endpoints, networkpolicies, deployments, Strimzi CRs, prometheusrules).
- **D3 — Kafka ACL friction**: document the ACLs (recommended now); or also change code so a topic that exists is accepted without CREATE, and group ids take a fixed `kates-` prefix so a prefixed ACL works.
- **D4 — Zones without node access**: leave UNKNOWN and document (recommended); or prototype EndpointSlice zones.

## Verification

- Unit: chart template assertions in `ci.yml`, `ConformityCheckTest`, CLI `ports` test; the usual local CI mirror before the PR.
- Live, on `panda` if it's up: a `team` namespace, a user bound to `edit` there only (`kubectl --as`), install with `rbac.scope=namespace`, the Kafka-namespace Role applied as admin; then a LOAD run, an INTEGRITY run and `kates conformity check -f minimal.yaml` (zones UNKNOWN expected without the node grant).

Size: chart ~60 lines, Java ~40 + tests, CLI ~20 + test, one docs section. One PR, or two (chart+docs, Java+CLI).

Follow-ups found on the way: the ClusterRole/Binding are named by fullname only, so two teams' `kates` releases collide and `kates deploy` deletes the other namespace's binding (`deploy.go:525-540`); `kates deploy` runs a `dns-detect` pod before it checks it can read nodes.

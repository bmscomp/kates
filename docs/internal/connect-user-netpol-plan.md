# Plan: connect-cluster — managed KafkaUser & default-deny-ready NetworkPolicies

Status: implemented in v1.2.0 · Chart: `charts/connect-cluster`

## 1. Problem

**User creation.** The chart authenticates as `kates-connect` but never creates that identity. The `KafkaUser` (and its ACLs) lives in `charts/kafka-cluster` values (`users.items`), so:

- Installing connect-cluster against any Kafka not deployed by kafka-cluster fails until someone hand-creates a user + secret.
- ACLs are maintained far from the thing that needs them; group/topic names (`groupId`, `<groupId>-offsets/configs/status`, connector topics) are duplicated by hand and drift.
- Multi-tenancy ("one Connect cluster per team") requires editing kafka-cluster values per tenant — the opposite of the chart's independent-lifecycle goal.

**NetworkPolicy.** The current `-connect-extra` policy assumes the namespace is otherwise open. On a cluster where a platform team enforces default-deny (or where we want the chart to bring its own default-deny, as kafka-cluster does):

- There is no default-deny policy in the connect chart itself (kafka-cluster has `networkPolicies.defaultDeny`; connect has nothing).
- Several flows are hardcoded and can't be adapted: DNS egress is `to: []` port 53 (blocked if the platform requires kube-dns pod selectors — some CNIs also need port 5353/9153), API-server egress is `to: []` 443/6443 (some clusters need the kubernetes endpoint CIDR instead), no egress for OTLP tracing endpoint, no egress for Strimzi build (registry push), no generic escape hatch (`extraEgress`/`extraIngress`).
- Ingress 8083 has a `namespaceSelector: {}` catch-all "for kubelet probes" — kubelet traffic is not subject to NetworkPolicy; this rule actually opens the REST API to the whole cluster and defeats default-deny.

## 2. Design

### 2.1 Managed KafkaUser (`kafkaUser.*`)

New template `templates/kafka-user.yaml`, created in the **Kafka namespace** (`connect-cluster.kafkaNamespace`), since the User Operator watches there and Strimzi writes the password Secret next to the Kafka cluster.

```yaml
kafkaUser:
  # Create a KafkaUser for this Connect cluster (requires Strimzi User Operator
  # and authorization enabled on the Kafka cluster for ACLs to apply).
  create: false                 # off by default: backward compatible
  # Name defaults to kafka.authentication.username
  name: ""
  # ACL mode:
  #   auto     — derive least-privilege ACLs from chart values (below)
  #   custom   — use kafkaUser.acls verbatim
  authorization:
    type: simple
    mode: auto
  # Topics connectors read/write, granted Read/Write/Create/Describe.
  # Prefix patterns recommended.
  topicGrants:
    - name: cdc
      patternType: prefix
  # Extra consumer groups beyond connect-<groupId> / <groupId>
  groupGrants: []
  # Used only when mode: custom
  acls: []
  quotas: {}
  # Secret template passthrough (labels/annotations on the generated secret)
  template: {}
  # Make the generated credentials Secret available in the Connect namespace
  # when Kafka lives elsewhere (see "Seamless secret availability" below).
  secretSync:
    enabled: true             # no-op when Kafka and Connect share a namespace
    method: job               # job | reflector
```

**Seamless secret availability.** Goal: `kafkaUser.create=true` is the *only* flag needed — user, ACLs, and password Secret all materialize without manual steps. Strimzi's User Operator writes the credentials Secret in the **Kafka namespace**, but `KafkaConnect.spec.authentication.passwordSecret` must live in the **Connect namespace**. When the two differ:

- `method: job` (default, no extra dependencies): a post-install/post-upgrade hook Job waits for the User Operator to create the Secret (`kubectl wait --for=jsonpath=...`, bounded retries), then copies it into the Connect namespace with chart labels. The existing cross-namespace secret-reader Role already grants read in the Kafka namespace; the hook additionally needs `create/update` on the named Secret in the Connect namespace (tightly scoped via `resourceNames`). Re-runs on every upgrade to pick up password rotation; a `kates.io/synced-from` annotation marks provenance.
- `method: reflector`: if the cluster runs [kubernetes-reflector](https://github.com/emberstack/kubernetes-reflector), the chart only annotates the KafkaUser `template.secret` for auto-reflection — zero hook Jobs, live rotation propagation.
- Same-namespace installs skip all of this: the User Operator's Secret is already where Connect expects it (name defaults line up: user name = secret name = `kafka.authentication.secretName`).

`mode: auto` renders, from existing values (single source of truth):

| Resource | Names | Ops | Why |
|---|---|---|---|
| topic | `<groupId>-offsets`, `-configs`, `-status` (or `internalTopics.*` overrides), literal | Read, Write, Create, Describe, DescribeConfigs | Connect internal topics |
| group | `<groupId>` literal + `connect-` prefix | Read, Describe | worker group + per-connector sink groups |
| topic | each `kafkaUser.topicGrants[]` | Read, Write, Create, Describe | connector data topics |
| transactionalId | `<groupId>` prefix | Write, Describe | `exactly.once.source.support: enabled` (rendered only when that extraConfig is set) |
| cluster | — | Describe, DescribeConfigs | admin client metadata |

Wiring changes in existing templates:

- `kafka-connect.yaml`: no change needed — `kafka.authentication.secretName` already defaults to the username, which is exactly the Secret name Strimzi's User Operator generates. Add a NOTES.txt line and a `fail` guard: `kafkaUser.create=true` with `authentication.type=oauth` is rejected (`tls`/`scram-*` supported).
- `authentication.type: tls`: template sets `spec.authentication.type: tls` on the KafkaUser and the existing `certificateSecret` default (`<username>` secret with `user.crt`/`user.key`) lines up with what Strimzi generates — document this.
- Validation hook (`validate-connectors.yaml`): warn when `kafkaUser.create=false` and `kafka.authentication.secretName` doesn't exist is not checkable at template time — instead, extend the Helm **test** (`test-connect.yaml`) to assert the auth secret exists before curling.
- values.schema.json: full schema for `kafkaUser` incl. enum on `mode`, required `name+patternType` on grants.

Cross-namespace note: KafkaUser goes to the Kafka namespace, but the chart may not own that namespace. Guard with `kafkaUser.create` and document that the release must have RBAC there (same caveat as the existing cross-namespace secret-reader Role).

### 2.2 Default-deny-ready NetworkPolicies (`networkPolicy.*`)

Restructure `templates/networkpolicies.yaml` values — **default-deny by default**: the chart ships its own deny-all policy for the Connect pods and every allowed flow is an explicit, individually configurable rule. Zero-trust out of the box, matching kafka-cluster's `defaultDeny: true` posture; anything not listed below is blocked.

```yaml
networkPolicy:
  enabled: true

  # Deny-all Ingress+Egress for the Connect pods (mirrors kafka-cluster).
  # ON by default — the allow rules below define the complete traffic contract.
  # Set false only if a platform-level default-deny already exists and Kyverno/
  # OPA forbids duplicate deny policies.
  defaultDeny:
    enabled: true
    # selector defaults to the Connect pods only (strimzi.io/name: <fullname>-connect)
    selector: {}

  dns:
    enabled: true
    # Empty = any destination (current behavior). On locked-down clusters set:
    # namespaceSelector: {kubernetes.io/metadata.name: kube-system}
    # podSelector: {k8s-app: kube-dns}
    namespaceSelector: {}
    podSelector: {}
    ports: [53]               # add 5353/9153 if the CNI needs them

  apiServer:
    enabled: true
    ports: [443, 6443]
    # Empty = any destination. For strict clusters, pin the control-plane CIDR:
    # ipBlock: {cidr: 10.0.0.1/32}
    ipBlock: {}

  workerToWorker:
    enabled: true             # 8083 leader forwarding (unchanged)

  kafka:
    enabled: true
    ports: [9092, 9093]       # replaces top-level kafkaPorts (kept as deprecated alias)

  monitoring:
    enabled: true
    namespace: monitoring     # replaces monitoringNamespace (deprecated alias)
    port: 9404

  restApi:
    # Replaces restApiClients + the namespaceSelector:{} catch-all.
    # The catch-all is REMOVED (it defeats default-deny; kubelet probes
    # bypass NetworkPolicy). allowAll restores old behavior if needed.
    allowAll: false
    clients:
      - namespace: kates
        podSelector: {app.kubernetes.io/name: kates}
    operator: true            # Strimzi operator ingress on 8083 (unchanged)

  schemaRegistry:
    enabled: true             # rendered only if schemaRegistry.enabled anyway

  tracing:
    enabled: true             # egress to tracing.endpoint host port (4317/4318), rendered only when endpoint set

  # Escape hatches — raw NetworkPolicy rule fragments appended verbatim
  extraEgress: []
  extraIngress: []
```

Also:

- Split the monolithic policy into `default-deny` (optional) + the existing `-connect-extra` allow policy, matching the kafka-cluster chart layout.
- `databaseEgress` unchanged (already configurable), but its reciprocal ingress policy in the DB namespace gets a toggle (`databaseEgress[].createIngressPolicy`, default true) — platform teams often forbid charts writing policies into other namespaces.
- Deprecation shims in `_helpers.tpl`: `networkPolicy.kafkaPorts` → `networkPolicy.kafka.ports`, `monitoringNamespace` → `monitoring.namespace`, `restApiClients` → `restApi.clients` (old keys win if set, warn in NOTES).

### 2.3 Behavior changes called out explicitly

Two deliberate breaking changes, both security fixes:

1. **Default-deny ships enabled.** Traffic not covered by the allow rules (DNS, API server, Kafka, worker-to-worker, monitoring, REST clients, registry, tracing, `databaseEgress`, `extraEgress`/`extraIngress`) is blocked. Connectors reaching endpoints nobody declared will break on upgrade — that's the point; declare them via `databaseEgress` or `extraEgress`. Escape hatch: `networkPolicy.defaultDeny.enabled=false`.
2. **The `namespaceSelector: {}` catch-all on 8083 is removed** — kubelet probes bypass NetworkPolicy, so it only opened the REST API cluster-wide. In-cluster clients must be listed in `restApi.clients` (or set `restApi.allowAll: true`).

Both get release notes in README/Chart.yaml `artifacthub.io/changes` and an upgrade checklist in NOTES.txt.

## 3. Files touched

| File | Change |
|---|---|
| `templates/kafka-user.yaml` | new — KafkaUser with auto/custom ACLs |
| `templates/kafka-user-secret-sync.yaml` | new — hook Job (or reflector annotations) copying the credentials Secret into the Connect namespace |
| `templates/networkpolicies.yaml` | restructure per §2.2 |
| `templates/networkpolicy-default-deny.yaml` | new — optional default-deny |
| `templates/_helpers.tpl` | ACL builders, deprecation shims, tracing endpoint host/port parser |
| `templates/kafka-connect.yaml` | oauth+kafkaUser guard (`fail`) |
| `templates/tests/test-connect.yaml` | assert auth secret exists |
| `values.yaml`, `values.schema.json` | new keys, deprecated aliases |
| `values-prod.yaml` | pinned DNS/API-server selectors on top of the default deny-all |
| `README.md`, `NOTES.txt` | docs, deprecation warnings |
| `Chart.yaml` | 1.2.0 |

## 4. Test plan

1. `helm template` matrix: defaults (v1.1.0 output + the new default-deny policy, everything else byte-identical apart from deprecation comments), `kafkaUser.create=true` × {scram-512, scram-256, tls}, `mode=custom`, cross-namespace Kafka with `secretSync` (job and reflector variants), pinned DNS/API selectors, `defaultDeny.enabled=false` opt-out, `restApi.allowAll=true`, deprecated aliases still honored.
2. `ct lint` (repo has `ct.yaml`).
3. kind e2e (`values-kind.yaml`): default-deny on, verify workers join group, connector runs, PodMonitor scrapes, `helm test` passes — proves the allow rules are complete.
4. Negative test: unlisted client namespace cannot reach 8083.

## 5. Out of scope

OAuth KafkaUser provisioning, Cilium/other CNI-specific policies, mTLS between workers (Strimzi doesn't support it on the REST API), automatic ACL inference from connector `config` (topic lists are free-form strings; `topicGrants` keeps it explicit).

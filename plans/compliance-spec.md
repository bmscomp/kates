# Compliance: check a deployed Kafka platform against a reference architecture

| | |
| --- | --- |
| **Status** | Proposal, 2026-10-19. Decisions D1–D8 taken the same day (§11): the recommended pick each time. v1 is PRs 1–5; the Kafka and monitoring rules (PRs 6, 6b and 7) are v1.1, after v1 has been used (§10). Two data sources, decided the same day: by default (`spec.kafka.source: cr`) every fact comes from the Kubernetes API and the check never connects to Kafka; `source: brokers` reads `kafka.config` and `kafka.rackAwareness` through the Kafka Admin API instead (§4.4, D8) |
| **Shipped (as of 2026-10-20)** | v1 and v1.1 are on `main`: #407 and #408 (model and reader), #409 (endpoint), #410 (CLI), #411 (sizing), #412 (placement), #413 and #414 (Kafka rules), #415 (monitoring), #416 (live CI check). Since then: #419 (per-topic rules), #420 (offline `lint` and `fmt`), #430 and #432 (pre-flight lint checks), #421 to #424 (fixes from the review in `plans/compliance-enhancements.md`), #431 (the book chapter, Compliance Checks) and #433 (the table layout). This document is the design record: its examples are the 2026-10-19 drafts, and the shipped schema and examples live in `cli/examples/conformity/` |
| **Audience** | The maintainer and contributors |
| **Baseline** | `main` at `0f3fbec` (v1.26.0). No compliance feature exists; `git grep -i compliance` matches only MCP protocol and chaos-provider conformance |
| **Files** | Shipped in `cli/examples/conformity/`: `prod-3az.yaml` (full example), `minimal.yaml`, `kafkareference.v1alpha1.schema.json`, with `kind-ci.yaml` and `payments-krafter.yaml` added since. The drafts below came first |
| **Decided with the user (2026-10-19)** | The Kates API evaluates and the CLI shows. The reference is YAML with a JSON Schema. Severities are fail and warn, with exit 1 on a fail. v1 covers brokers, controllers and node pools; zone spread and anti-affinity; listeners, TLS, auth and topic defaults; versions and monitoring (narrowed the same day: listeners, TLS, auth, topic defaults and monitoring move to v1.1, §10). Each declared value says how it is compared. Waivers live in the file with a reason and an expiry. Nothing is stored. A snapshot generator comes in a later PR. An ultra-mode review the same day cut SARIF, `--strict`, `--all`, the `deleteClaim` rule, the waiver cap and the schema-coverage test (§9) |

Two labels are used throughout:

- **exists today**: on `main` at `0f3fbec`.
- **proposed here**: new in this spec.

Java paths are under `kates/src/main/java/com/bmscomp/kates/`. Go paths are under `cli/`.

---

## 1. Summary

A platform team writes down the Kafka platform it means to run, as a `KafkaReference` file in git: how many brokers and controllers, how big each one must be, how they spread over zones and nodes, which listeners must exist with which TLS and authentication, which broker defaults keep data safe, which versions are allowed, and what monitoring must be in place. `kates compliance check -f reference.yaml` sends the file to the Kates API. The API reads the live cluster read-only, compares every declared constraint, and returns a report. The report lists every gap. The command exits 0 when every `fail` constraint holds, 1 when one does not, and 2 when it could not tell. In CI the same command can also write JUnit, so each gap shows as a failed test.

Why the API, not the CLI, evaluates (the user's choice):

- **The backend already reads every v1 source.** Its ClusterRole grants get/list on `kafkas`, `kafkanodepools`, `pods`, `nodes`, `persistentvolumeclaims` and `podmonitors` (`charts/kates/templates/rbac.yaml:16-21,36-38,48-50`). By default nothing else is needed: the check reads no Kafka data, so it needs no Kafka credentials or ACLs and still works when the brokers are down. A reference that sets `kafka.source: brokers` uses the pooled Kafka Admin client, which already holds the cluster's credentials (`service/KafkaAdminService.java:27,96`).
- **The CLI has neither.** It has no client-go dependency and shells out to `kubectl` (`internal/kubectl/kubectl.go:39-102`). Since every fact is a Kubernetes read, a CLI-side check over `kubectl` stays possible later if Kates is not installed; v1 does not build one.
- **The MCP tool becomes a thin wrapper.** It can be added later over the same endpoint (§7.4).

**Scope of the design.** Only declared constraints are checked, and there is nothing implicit to learn. Every rule names its data source. When a source cannot be read, the rule is UNKNOWN, never a false FAIL. Rules compare platform-neutral facts, so a second platform later needs a new fact collector, not new rules (§5.8).

**Size.** Five PRs ship v1: the model, the endpoint, the CLI, then sizing and placement, the last two independent once the endpoint lands. v1.1 adds the Kafka and monitoring rules as two more, after v1 has been used. The MCP tool, the snapshot and the move to `v1` follow as three more (§10). No new dependency, Kubernetes RBAC or database table is needed.

---

## 2. What exists and what this reuses

| Exists today | What it does | Used here? |
| --- | --- | --- |
| `GET /api/cluster/topology` (`service/ClusterTopologyService.java:115-157`) | Strimzi CRs, pods and Admin data, assembled into one map | **Its clients, not its output.** Each section swallows its own error and returns an empty value (for example node pools at `:591-593`), so "no listeners" and "could not read listeners" look the same. It reads only `volumes[0]` (`:546-551`), reduces affinity to a boolean (`:573`), reads no node zone labels, and takes the controller from DescribeCluster (`:618-622`; MCP caveat `controller-from-describe-cluster`, `cmd/mcp_caveats.go:33`). Compliance reads the raw resources with the same `KubernetesClient`, the same `GenericKubernetesResource` CRD contexts (`:36-100`) |
| `GET /api/cluster/check` (`service/ClusterHealthService.java:235`) | Health: offline and under-replicated partitions, quorum leader | **No.** Health is not architecture. Compliance has no partition-health rules, and `kates cluster check` stays the health command |
| `kates security audit` (`service/SecurityService.java:79-790`) | About 45 fixed-threshold best-practice checks (CIS labels), graded A–F | **No.** It reads one broker's config (`:94-96`), and its findings have no stable ids (drift keys on display names, `:912-980`). The overlap on `min.insync.replicas` and replication factor is deliberate: the audit says what is generally good, and compliance says whether *your declared numbers* are what the Kafka CR declares |
| `kates security pentest` | Six config-only probes | No |
| `kates detect` (`pkg/detect/collector.go:64`) | Pre-deploy: can this Kubernetes cluster host Kafka? | **No.** It shells out to `kubectl`. Unless `ReadOnly` is set, it creates a namespace, a Secret and prober pods to measure things, then deletes them (`:30-35,139-151`), and `detect` does not set it. It only counts Kafka resources (`:435`). Compliance is post-deploy and must never write |
| `kates auto` | Detect, then generate and install values | No |
| `kates test apply --wait` gates (`cmd/apply.go:538-613`) | Latency, throughput, integrity and RTO/RPO gates on a test run | **No; out of scope.** Kates' own perf and SLA gates stay with scenarios |
| `SpecLimits` + `refusal()` (`engine/SpecLimits.java:43`, `engine/TestOrchestrator.java:1139`) and `ApiError.validationFailed(msg, fieldErrors)` (`api/ApiError.java:35`) | Bounded spec validation that returns 400 with one message per field path | **Yes, the pattern.** An invalid reference returns 400 with `fieldErrors` keyed by path (§3.6) |
| `scripts/check-versions.sh`, `versions.env`, `kates.io/kafka-floor` (`charts/kafka-cluster/Chart.yaml:40`) | Keep version pins consistent across repo files | **No; it checks files, not a cluster.** A reference may declare the same floor (`versions.kafka: {min: "4.2.0"}`) |
| kafka-cluster node pools and `kates.io/*` keys | Chart rendering; lab, migration and test-pod markers | **No.** No existing annotation describes intent that compliance could check, and compliance adds none: the reference is a file, not an annotation |
| `cli/cmd/junit.go:12-39` | JUnit XML types, written for disruption reports | **Yes.** The compliance JUnit writer reuses the types |
| `github.com/google/jsonschema-go` (`cli/go.mod:14`) | Already a CLI dependency | **Yes**, in a test: the examples must validate against the schema |
| `Scopes.READ` (`security/Scopes.java:16`) | The read scope for API keys and MCP | **Yes.** The endpoint needs `read` |
| MCP caveats (`cmd/mcp_caveats.go`) | Stable caveat ids attached to tool results | Later, with the MCP tool |

Nothing in Kates today checks a live cluster against declared intent. The closest pieces are the audit's fixed thresholds and detect's pre-deploy verdict (`pkg/detect/types.go:340-351`, pass/fail with no severity).

---

## 3. The reference file

### 3.1 Format

The format is YAML, as a Kubernetes-style document (`apiVersion`, `kind`, `metadata`, `spec`), with a published JSON Schema (draft 2020-12) that editors use for validation and completion through the `# yaml-language-server: $schema=…` header.

The alternatives:

- **CUE** makes constraints native (`count: >=3`), but it is a new language and a new dependency, and it compares `"8Gi"` as a string.
- **Rego** is a policy language: operators would write deny rules rather than declare an architecture.

This would be the first CLI file with `apiVersion`/`kind`, since scenario files have neither (`cmd/apply.go:21-59`). The version field is what lets the format change later without guessing.

### 3.2 Shape

```yaml
apiVersion: conformity.kates.io/v1alpha1
kind: KafkaReference
metadata: {name: <dns label>, description: <text>}
spec:
  target:      {platform: strimzi, namespace, name, clusterId}   # all optional pins
  zoneLabel:   topology.kubernetes.io/zone                      # default
  versions:    {kubernetes, strimzi, kafka}
  controllers: {count, dedicated, requests, limits, storage, zones, skew, perNode, antiAffinity}
  brokers:     {count, requests, limits, storage, zones, skew, perNode, antiAffinity}
  kafka:       {source, rackAwareness, authorization, listeners: {require: [...], every: {...}}, config: {<broker config>: ...}}
  topics:      {replicationFactor, minInsyncReplicas, exclude: [<name prefix>]}
  monitoring:  {metrics, kafkaExporter, podMonitor}
  waivers:     [{rule, reason, by, until}]
```

Every section is optional, but a reference must declare at least one rule. `controllers` and `brokers` take the same fields; `dedicated` is for controllers only. `kafka.source` is a setting, not a rule: `cr` (the default) or `brokers`, and it changes only where `kafka.config` and `kafka.rackAwareness` read from (§4.4).

### 3.3 Constraints

Each leaf is a constraint, written either bare or as a map:

| Written | Means |
| --- | --- |
| `count: 3`, `tls: true`, `type: jbod` | exact (`equals`) |
| `{min: 3}`, `{max: 1}`, `{min: "4.2.0", max: "4.3.9"}` | inclusive bounds; `min` and `max` combine |
| `{oneOf: [tls, scram-sha-512]}` | one of these values |
| `{equals: required, severity: warn}` | the map form, needed only to add `severity` |

- `equals` and `oneOf` stand alone, and a map without an operator is refused. Severity is `fail` unless the map says `warn`.
- Field names that are bounds in themselves are written as constraints too (`skew: {max: 1}`, `perNode: {max: 1}`), so a bare value always means exact.

Each field has one value kind, fixed by the schema:

| Kind | Fields | Compared as |
| --- | --- | --- |
| integer | `count`, `zones`, `skew`, `perNode`, listener `port`, `replicationFactor`, `minInsyncReplicas` | numbers |
| quantity | `requests.*`, `limits.*`, `storage.size` | Kubernetes quantities through fabric8 `Quantity`, so `8Gi` > `8000Mi` and `2` > `500m` |
| version | `versions.*` | dotted numbers; missing parts count as 0. Values must be **strings**: unquoted `4.10` is the number 4.1, and the API refuses a number here with "quote the version" |
| enum | storage `type`, `antiAffinity`, listener `type` and `auth`, `authorization`, `metrics` | exact text, values fixed by the schema |
| string | `storage.class` | exact text |
| boolean | `dedicated`, `tls`, `rackAwareness`, `kafkaExporter`, `podMonitor` | exact |
| config | `kafka.config.<key>` | `min`/`max` parse both sides as numbers. `equals`/`oneOf` compare text, so `false` matches `"false"` and `true`/`false` ignore case |

### 3.4 Rule ids

A rule id is the constraint's path under `spec`:

- `brokers.count`, `controllers.storage.size`, `kafka.listeners.every.tls`.
- Keys that contain dots go in brackets: `kafka.config[min.insync.replicas]`.
- List items go in brackets by their `name`: `kafka.listeners.require[tls]`.

Each leaf constraint is one rule, and a `require` item is one rule (its properties together describe one listener). A rule id is stable for as long as the file keeps its shape. There is no separate id catalogue to learn: the file is the catalogue, and §4 lists every path.

### 3.5 Waivers

```yaml
  waivers:
    - rule: kafka.listeners.every.tls
      reason: In-cluster clients still use the SCRAM listener on 9092; OPS-412 moves them to 9093.
      by: platform-team
      until: 2026-12-31
```

- **All four fields are required.** `until` is a date and is inclusive, in UTC by the API server's clock.
- **Active waiver.** A rule that is FAIL, WARN or UNKNOWN becomes WAIVED. The report keeps its original status, observed value and the waiver, so the gap stays visible, and it stops counting towards the verdict.
- **Expired waiver.** The rule keeps its status, and the report says the waiver expired on that date.
- **Waiver on a passing rule.** The rule is PASS with the note "waiver no longer needed", so stale waivers get cleaned up.
- **Refused with 400:** a waiver that names a rule the file does not declare; two waivers for one rule. A date in the past is accepted, so that an expired waiver makes its rule fail again rather than breaking the check.

### 3.6 Validation

The API is the authority, and the JSON Schema exists for editors.

- **Strict decoding.** The API reads the body with a dedicated reader that has `FAIL_ON_UNKNOWN_PROPERTIES` on. Quarkus' shared mapper ignores unknown keys, so it cannot be used for this.
- **Errors.** A typo, a wrong value kind or a missing operator returns 400 `ApiError.validationFailed`, with `fieldErrors` keyed by path (`spec.brokers.cuont`: "unknown field"). The CLI prints each one as `prod-3az.yaml: spec.brokers.cuont: unknown field`.
- **Bounds** mirror the schema: body ≤ 256 KiB (413 above it), ≤ 200 config keys, ≤ 50 required listeners, ≤ 50 topic `exclude` prefixes, ≤ 200 waivers, ≤ 50 `oneOf` values, and strings of bounded length. Text the API echoes back in the report is the caller's own.
- **Unsupported rules** (§5.8) are refused with 400 naming the rule and the platform.
- **Not-yet-built sections** are refused with 400 until the PR that adds their rules lands (§10).
- **YAML to JSON.** The CLI converts from the `yaml.v3` node tree. A scalar keeps its source text unless YAML tags it as an int, float, bool or null, so `until: 2026-12-31` arrives as `"2026-12-31"`, not as a timestamp.

### 3.7 Full example: three zones, Strimzi KRaft

This is the 2026-10-19 draft of `cli/examples/conformity/prod-3az.yaml`, which has changed since: the shipped file is the reference. The draft validated against the draft schema (§3.9).

```yaml
# yaml-language-server: $schema=https://raw.githubusercontent.com/bmscomp/kates/main/cli/examples/conformity/kafkareference.v1alpha1.schema.json
apiVersion: conformity.kates.io/v1alpha1
kind: KafkaReference
metadata:
  name: prod-3az
  description: Production Kafka in three zones, dedicated controllers, TLS on every listener.
spec:
  # The cluster this reference is for. Kates checks the one cluster it is
  # configured for; if these do not match it, the check stops (exit 2).
  target:
    platform: strimzi # the only platform in v1
    namespace: kafka
    name: krafter
    # clusterId: 4L6g3nShT-eMCtK--X86sw   # optional: pin one cluster, not just a name

  zoneLabel: topology.kubernetes.io/zone # the default

  versions:
    kubernetes: {min: "1.32.0"} # versions are strings: unquoted 1.32 is a number
    strimzi: {min: "1.2.0"}
    kafka: {min: "4.2.0"} # the kafka-cluster chart's own floor (kates.io/kafka-floor)

  controllers:
    count: 3 # exact: a quorum of three; five would be a gap too
    dedicated: true # no node pool has both roles
    requests:
      cpu: {min: 500m}
      memory: {min: 1Gi}
    storage:
      type: {oneOf: [jbod, persistent-claim]}
      size: {min: 10Gi} # bound capacity per controller, all volumes added up
    zones: {min: 3}
    skew: {max: 0} # the same number of controllers in every zone
    perNode: {max: 1}
    antiAffinity: required # required anti-affinity on kubernetes.io/hostname

  brokers:
    count: {min: 3}
    requests:
      cpu: {min: "2"}
      memory: {min: 8Gi}
    limits:
      memory: {min: 8Gi}
    storage:
      type: jbod
      size: {min: 500Gi}
      class: {oneOf: [gp3, premium-rwo]}
    zones: {min: 3}
    skew: {max: 1} # zones differ by one broker at most
    perNode: {max: 1} # the gate; antiAffinity below only warns on the mechanism
    antiAffinity: {equals: required, severity: warn}

  kafka:
    # source: brokers   # read config and racks through the Kafka Admin API; the default, cr, reads the Kafka CR
    rackAwareness: true # spec.kafka.rack uses zoneLabel, and every broker's node has that label
    authorization: {oneOf: [simple, custom]}
    listeners:
      require: # each must exist under this name, with these properties
        - name: tls
          port: 9093
          tls: true
          auth: tls
      every: # every listener, whatever its name
        tls: true
        auth: {oneOf: [tls, scram-sha-512, custom]} # none means anonymous
    config: # what the Kafka CR declares; a key it leaves out fails
      default.replication.factor: {min: 3}
      min.insync.replicas: {min: 2}
      offsets.topic.replication.factor: {min: 3}
      transaction.state.log.min.isr: {min: 2}
      unclean.leader.election.enable: false
      auto.create.topics.enable: false

  monitoring:
    metrics: {oneOf: [jmxPrometheusExporter, strimziMetricsReporter]}
    kafkaExporter: true
    podMonitor: true # a PodMonitor whose selector matches the broker pods

  # Known gaps: still reported, as WAIVED, but they stop failing the check
  # until their date.
  waivers:
    - rule: kafka.listeners.every.tls
      reason: In-cluster clients still use the SCRAM listener on 9092 without TLS; OPS-412 moves them to 9093.
      by: platform-team
      until: 2026-12-31
```

### 3.8 Minimal example

This is `cli/examples/conformity/minimal.yaml`, without its schema header line:

```yaml
apiVersion: conformity.kates.io/v1alpha1
kind: KafkaReference
metadata:
  name: minimal
spec:
  brokers:
    count: {min: 3}
    zones: {min: 3}
    perNode: {max: 1}
```

### 3.9 The JSON Schema and keeping it honest

The draft of `cli/examples/conformity/kafkareference.v1alpha1.schema.json` was checked this way before PR 1 moved it there:

- **Checked against the examples.** It accepts both examples. With `google/jsonschema-go` v0.4.3 (the CLI's own dependency, run in a scratch program), it refused all eight broken variants for the right reason: an unknown key, an unquoted version, `{severity: warn}` with no operator, `equals` with `min`, a waiver without `until`, `dedicated` on brokers, `8GB` as a quantity, and a required listener with only a name.
- **Where it lands.** PR 1 moves it, with the examples, to `cli/examples/conformity/`. That is the precedent `ResilienceExamplesTest` set by reading `cli/examples` from Java (`kates/src/test/java/com/bmscomp/kates/resilience/ResilienceExamplesTest.java:54`), and it gives editors a stable raw URL.
- **Two tests keep the schema, the Java model and the examples in step**, for every field the examples use:
  1. A Go test validates every example against the schema.
  2. A Java test parses every example with the strict reader.

---

## 4. Rule catalogue

Notes for every rule below:

- **Severity.** Every rule defaults to `fail` (D3) and takes `severity: warn` in its map form.
- **Waivers.** Every rule can be waived (§3.5).
- **Role rules.** `<role>` is `controllers` or `brokers`. A node with both roles counts in both.
- **Subjects.** A rule with subjects (pods, PVCs, pools, listeners, brokers) passes only when **every** subject satisfies the constraint. The report names the subjects that do not, up to 50.

### 4.1 Precondition (not a rule)

| Check | Source | Effect |
| --- | --- | --- |
| `target.namespace`, `target.name` (default: the cluster Kates watches, `kates.topology.kafka-namespace` / `kafka-cluster`, `service/ClusterTopologyService.java:108-112`) | Kates config; Kafka CR | A mismatch, or no such Kafka CR, returns 409 and the check exits 2. No rule runs |
| `target.clusterId` | Kafka CR `status.clusterId` | A mismatch returns 409 and the check exits 2 |
| `target.platform` | — | Only `strimzi` in v1 |

### 4.2 Versions

| Rule id | Checks | Source | Observed |
| --- | --- | --- | --- |
| `versions.kubernetes` | Kubernetes server version | Kubernetes API `/version` (`kubernetesClient.getKubernetesVersion()`, as at `ClusterTopologyService.java:162`) | `gitVersion` with the leading `v` dropped and anything after the third number removed (`v1.34.11+k3s1` → `1.34.11`) |
| `versions.strimzi` | The operator version that last reconciled this cluster | Kafka CR `status.operatorLastSuccessfulVersion` | That version; UNKNOWN when absent |
| `versions.kafka` | Kafka version | Kafka CR `status.kafkaVersion` | That version |

### 4.3 Brokers and controllers

**Which pods count:** pods labelled `strimzi.io/cluster=<name>` and `strimzi.io/kind=Kafka`, with `strimzi.io/broker-role=true` (brokers) or `strimzi.io/controller-role=true` (controllers), that are not being deleted. These are the nodes the operator created, Ready or not (D1).

| Rule id | Checks | Source | Observed |
| --- | --- | --- | --- |
| `<role>.count` | How many nodes of the role exist | Kubernetes API: pods | The pod count. The detail adds how many are Ready |
| `controllers.dedicated` | No pool of this cluster has both roles | Strimzi CR: KafkaNodePool `spec.roles` | `true`/`false`; the detail names any combined pools |
| `<role>.requests.cpu`, `.requests.memory`, `.limits.cpu`, `.limits.memory` | Resources of the `kafka` container | Kubernetes API: pod spec (what runs, not what the CR asks for before a roll) | The value on each pod; an unset value is `unset` and fails any constraint. Subjects: pods |
| `<role>.storage.type` | Storage type | Strimzi CR: KafkaNodePool `spec.storage.type` | Per pool. Subjects: pools |
| `<role>.storage.size` | Storage per node | Kubernetes API: the PVCs each pod mounts, `status.capacity.storage` | The sum of a pod's bound PVCs; an unbound PVC counts as 0 and an ephemeral pool as 0. Subjects: pods |
| `<role>.storage.class` | StorageClass name | Kubernetes API: PVC `spec.storageClassName` (the default-class admission plugin fills it in) | Per PVC. Subjects: PVCs |
| `<role>.zones` | How many zones host the role | Kubernetes API: each pod's `spec.nodeName`, then that node's `zoneLabel` label | Distinct zone values. A pod that is not scheduled, or that runs on a node without the label, fails the rule whatever the count, and is named in the detail (D7) |
| `<role>.skew` | Balance across zones | as above | Largest minus smallest pod count per zone, over the zones that host at least one pod. Unplaced pods as above |
| `<role>.perNode` | Pods of the role on one Kubernetes node | Kubernetes API: pods' `spec.nodeName` | The highest count on any one node |
| `<role>.antiAffinity` | The mechanism that keeps pods of the role apart | Kubernetes API: pod `spec.affinity.podAntiAffinity` | Per pod: `required` when a `requiredDuringScheduling…` term on `kubernetes.io/hostname` has a selector that matches the pod's own labels, `preferred` when only a preferred term does, otherwise `none`. Subjects: pods |

### 4.4 Kafka

| Rule id | Checks | Source | Observed |
| --- | --- | --- | --- |
| `kafka.rackAwareness` | Brokers know their zone | Strimzi CR: Kafka `spec.kafka.rack.topologyKey`, then **cr:** the node of each broker pod (Kubernetes API); **brokers:** each broker's rack (Admin `describeCluster`) | `true` when the topology key equals `zoneLabel` and, with `cr`, every broker's node carries that label (where Strimzi takes each rack from) or, with `brokers`, every broker reports a rack; the detail says which part is missing |
| `kafka.authorization` | Authorizer type | Strimzi CR: Kafka `spec.kafka.authorization.type` | The type, or `none` when absent |
| `kafka.listeners.require[<name>]` | The named listener exists with every listed property (`type`, `port`, `tls`, `auth`) | Strimzi CR: Kafka `spec.kafka.listeners` (`auth` is `authentication.type`, or `none`) | The listener's `{type, port, tls, auth}`, or "missing". The detail names each property that differs |
| `kafka.listeners.every.type`, `.tls`, `.auth` | One property on every listener | as above | Per listener. Subjects: listener names |
| `kafka.config[<key>]` | A broker setting | **cr:** Strimzi CR: Kafka `spec.kafka.config`. **brokers:** Admin `describeConfigs` for every broker in one call, defaults included | Compared as text or number (§3.3). **cr:** the value the CR sets; a key it does not set fails with "not set in the Kafka CR", so a goal the reference names must be written down, not left to Kafka's default. **brokers:** the effective value per broker; a key no broker reports fails with "brokers do not report <key>" (a typo or another Kafka version); a sensitive key (Kafka returns no value) is UNKNOWN and never shown. Subjects: brokers |

What each source can say:

| | `cr` (default) | `brokers` |
| --- | --- | --- |
| Kafka access | none; works with the brokers down | Describe and DescribeConfigs on the Cluster resource |
| `kafka.config` sees | what the platform declares, cluster-wide (KafkaNodePools carry no config) | the effective value on every broker: Kafka's defaults, out-of-band `kafka-configs --alter` changes, a broker that has not picked up a change |
| A key nobody set | fails: the goal must be declared | Kafka's default is compared |
| Controller-only nodes | covered by the cluster-wide CR value | out of reach of the Admin client's broker connection |
| `kafka.rackAwareness` | CR key plus node labels | CR key plus the racks brokers report |

Pick `cr` to check that the platform is declared as intended, `brokers` to check that the running brokers agree. A team that wants both writes two references that differ only in `source`. Every other rule reads Kubernetes in both modes; D1 keeps counts on pods.

### 4.5 Monitoring

| Rule id | Checks | Source | Observed |
| --- | --- | --- | --- |
| `monitoring.metrics` | Metrics exposure | Strimzi CR: Kafka `spec.kafka.metricsConfig.type` | The type, or `none` |
| `monitoring.kafkaExporter` | Kafka Exporter deployed | Strimzi CR: Kafka `spec.kafkaExporter` | Present or not |
| `monitoring.podMonitor` | Prometheus is told to scrape the brokers | Kubernetes API: `monitoring.coreos.com/v1` PodMonitors in any namespace | `true` when a PodMonitor's `selector` matches every broker pod's labels and its `namespaceSelector` covers the Kafka namespace (`any: true`, `matchNames` naming it, or no selector with the PodMonitor in that namespace). A missing CRD is `false`, with the detail saying so. Whether a Prometheus selects that PodMonitor is not checked |

### 4.6 Topics

Added on 2026-10-20 (finding `per-topic-durability` in `plans/compliance-enhancements.md`): broker defaults such as `default.replication.factor` say nothing about a topic created with `--replication-factor 2 --config min.insync.replicas=1`.

| Rule id | Checks | Source | Observed |
| --- | --- | --- | --- |
| `topics.replicationFactor` | Each topic's replication factor | Admin `listTopics` (internal topics included), then `describeTopics` | The smallest replica set across the topic's partitions. Subjects: topic names |
| `topics.minInsyncReplicas` | Each topic's `min.insync.replicas` | as above, then one Admin `describeConfigs` over the topic resources | The effective value: the topic's own, or the broker default it inherits. Subjects: topic names |

- **Always Kafka.** The section is read through the Admin API whatever `kafka.source` says, and only when it is declared. Declaring it opens a Kafka connection even with `source: cr`; an unreachable Admin makes both rules UNKNOWN. KafkaTopic CRs are not read: the unidirectional topic operator creates none for topics that clients create.
- **`exclude`** lists topic-name prefixes, at most 50, each 1 to 249 of Kafka's topic-name characters. A topic whose name starts with one is not described. Prefixes, not patterns, so §8's "no regexes" holds; `__` leaves out Kafka's internal topics.
- **At least one rule.** A `topics` section with only `exclude` is refused.
- **Scale.** Subjects stop at 50, as for every rule; the detail reads `N of M topics` with the full counts, and `no topic to check` when every topic is excluded (the rule then passes).

---

## 5. Discovery and evaluation

### 5.1 Steps

1. **Validate** the reference (§3.6). An invalid one returns 400.
2. **Resolve the target.** Read the Kafka CR (`kafka.strimzi.io/v1`) the target names. Kubernetes unreachable returns 503 (as `/topology` does with `KubernetesNotAvailableException`); a mismatch or missing CR returns 409.
3. **Gather facts, only those the declared rules need.** No `monitoring.podMonitor` means no PodMonitor list; no placement rule means no node reads. Sources are read in parallel, each with a 30 s timeout (as `ClusterTopologyService.TIMEOUT_SECONDS`), and the whole check is capped at 60 s.
4. **Evaluate** each rule against the facts: PASS, FAIL (fail severity), WARN (warn severity) or UNKNOWN.
5. **Apply waivers** (§3.5).
6. **Compute the verdict** (§5.6) and return the report.

### 5.2 Facts and their sources

| Fact | Call | Kubernetes RBAC / Kafka ACL |
| --- | --- | --- |
| Kafka CR (spec and status) | get `kafkas` | granted (`rbac.yaml:36-38`) |
| Node pools | list `kafkanodepools` by `strimzi.io/cluster` | granted |
| Pods of the cluster | list `pods` by label | granted (`:16-18`) |
| Kubernetes nodes and their labels | get `nodes` | granted (`:19-21`) |
| PVCs | list `persistentvolumeclaims` in the namespace | granted (`:19-21`) |
| PodMonitors | list `podmonitors`, all namespaces | granted (`:48-50`) |
| Kubernetes version | `/version` | public to authenticated callers |
| Broker racks (`kafka.source: brokers`) | Admin `describeCluster` | Kafka ACL: Describe on Cluster |
| Broker configs (`kafka.source: brokers`) | Admin `describeConfigs`, all brokers, one call | Kafka ACL: DescribeConfigs on Cluster |
| Topics and their configs (`topics`) | Admin `listTopics`, `describeTopics`, one `describeConfigs` over the topics | Kafka ACL: Describe and DescribeConfigs on each topic; a topic the principal cannot Describe is not listed, so not checked |

**No new Kubernetes RBAC.** With the default `kafka.source: cr` the check never opens a Kafka connection and needs no Kafka ACLs. With `brokers` it needs Describe and DescribeConfigs on the Cluster resource, which the security audit already needs; `kates-backend` is a Kafka super user in the platform profile (`charts/kafka-cluster/profiles/platform.yaml:17-19`), and the check does not depend on that.

### 5.3 When a source fails

A failed or timed-out read marks every rule that needs it UNKNOWN, with the error as the detail ("Kubernetes list pods kafka/strimzi.io/cluster=krafter: timed out after 30 s"). A rule is never FAIL because data is missing. This is the one place compliance must differ from `/topology`, which returns empty sections.

### 5.4 Cluster state is context, not a rule

The report header carries:

- the Kafka CR's `Ready` condition;
- `metadata.generation` against `status.observedGeneration`;
- the number of the cluster's pods that are not Ready.

When the cluster is not Ready, or a reconcile is pending, the header says that results may change while it settles. Health stays with `kates cluster check`.

### 5.5 Statuses

| Status | Meaning |
| --- | --- |
| PASS | The constraint holds |
| FAIL | A `fail` constraint does not hold |
| WARN | A `warn` constraint does not hold |
| WAIVED | Not PASS, but an active waiver covers it; the original status is kept |
| UNKNOWN | Its data could not be read |

### 5.6 Verdict and exit codes

The API computes the verdict, so the CLI, CI and MCP agree.

| Verdict | When | CLI exit |
| --- | --- | --- |
| `FAIL` | Any FAIL | 1 |
| `UNKNOWN` | No FAIL, and any UNKNOWN on a `fail` rule | 2 |
| `PASS` | Otherwise | 0 |

A check that cannot start (400, 409, 413, 503, an unreachable API, an unreadable file) also exits 2. A definite gap beats an unknown one (D2): a FAIL alongside an UNKNOWN exits 1.

### 5.7 Cost

A check makes about six Kubernetes reads, plus up to two Admin calls with `kafka.source: brokers`, whatever the cluster size. Declaring `topics` adds three Admin calls whose answers grow with the topic count, as the security audit's do. A CI matrix running many checks at once multiplies that, and v1 adds no rate limit.

### 5.8 Other platforms later

Rules read a platform-neutral fact model:

- nodes, each with roles, pool, readiness, Kubernetes node, zone, resources, volumes and anti-affinity level;
- pools, with their storage type;
- listeners, with type, port, TLS and auth;
- authorizer, rack key and broker racks;
- broker configs, versions and monitoring.

`StrimziFacts` fills it in v1. Another platform needs only its own collector. `charts/legacy-kafka`'s StatefulSet Kafka is the natural second one, then Confluent for Kubernetes or a managed service.

Each collector names the rules it can fill; a reference that declares any other is refused with 400 at validation. `versions.strimzi` and the Strimzi enum values are platform-specific: another platform adds its own `versions.<platform>` and values.

v1 adds no collector interface, since one implementation needs none. The fact model is kept neutral so that the second collector is an addition, not a rewrite.

---

## 6. Report

### 6.1 JSON (the API response, and `-o json`)

```json
{
  "apiVersion": "conformity.kates.io/v1alpha1",
  "kind": "ConformityReport",
  "reference": "prod-3az",
  "target": {"platform": "strimzi", "namespace": "kafka", "name": "krafter", "clusterId": "4L6g3nShT-eMCtK--X86sw"},
  "checkedAt": "2026-10-19T12:00:03Z",
  "cluster": {"ready": true, "generation": 7, "observedGeneration": 7, "podsNotReady": 0},
  "verdict": "FAIL",
  "summary": {"pass": 30, "fail": 6, "warn": 1, "waived": 1, "unknown": 0},
  "results": [
    {
      "rule": "brokers.requests.memory",
      "status": "FAIL",
      "severity": "fail",
      "expected": {"min": "8Gi"},
      "observed": "4Gi",
      "subjects": ["krafter-brokers-3", "krafter-brokers-4", "krafter-brokers-5"],
      "detail": "3 of 3 brokers request 4Gi",
      "source": "kubernetes: pods kafka/strimzi.io/cluster=krafter, container kafka"
    },
    {
      "rule": "kafka.listeners.every.tls",
      "status": "WAIVED",
      "severity": "fail",
      "expected": {"equals": true},
      "observed": false,
      "subjects": ["plain"],
      "detail": "listener plain (9092) has tls: false",
      "source": "strimzi: Kafka kafka/krafter spec.kafka.listeners",
      "waiver": {"status": "FAIL", "reason": "In-cluster clients still use …", "by": "platform-team", "until": "2026-12-31"}
    }
  ]
}
```

- **Order.** Results are sorted FAIL, UNKNOWN, WARN, WAIVED, PASS, then by rule id.
- **`expected`** is the constraint in map form. **`observed`** is a scalar, or for subject rules the failing value.
- **Notes.** A `note` field carries "waiver no longer needed" or "waiver expired on …".

### 6.2 Table (default)

Below is the full example run against the kafka-cluster chart's defaults on a three-zone cluster. The defaults are brokers at 1 CPU and 4Gi with 100Gi each, preferred anti-affinity, and a plain SCRAM listener (`charts/kafka-cluster/values.yaml:93-105,204-291`). The cluster id and the storage class are illustrative.

```
Compliance  prod-3az  →  kafka/krafter  (cluster 4L6g3nShT-eMCtK--X86sw)
Cluster     Ready, generation 7 reconciled, all pods Ready

STATUS  RULE                          EXPECTED                 OBSERVED
FAIL    controllers.antiAffinity      required                 preferred  (3 of 3 pods)
FAIL    brokers.requests.cpu          min 2                    1000m      (3 of 3 pods)
FAIL    brokers.requests.memory       min 8Gi                  4Gi        (3 of 3 pods)
FAIL    brokers.limits.memory         min 8Gi                  4Gi        (3 of 3 pods)
FAIL    brokers.storage.size          min 500Gi                100Gi      (3 of 3 pods)
FAIL    brokers.storage.class         one of gp3, premium-rwo  standard   (3 of 3 PVCs)
WARN    brokers.antiAffinity          required                 preferred  (3 of 3 pods)
WAIVED  kafka.listeners.every.tls     true                     false      (plain)  until 2026-12-31
PASS    30 rules                                                          -o json lists them

FAIL: 6 fail, 1 warn, 1 waived, 30 pass, 0 unknown
```

With no terminal attached the output is plain text, and colour follows the CLI's existing rules.

### 6.3 JUnit (`--output-junit <file>`)

It reuses the types in `cmd/junit.go:12-39`. The suite time is the check's duration. The writer at `:95` puts a Unix timestamp there, which is a bug (§12).

| Report | JUnit |
| --- | --- |
| the report | `<testsuites name="compliance:prod-3az">` |
| top-level section (`versions`, `controllers`, `brokers`, `kafka`, `monitoring`) | `<testsuite>` |
| rule | `<testcase name="brokers.requests.memory" classname="compliance.brokers">` |
| FAIL | `<failure message="min 8Gi, observed 4Gi" type="fail">` with the detail |
| UNKNOWN | `<error>` |
| WARN | passes, with the detail in `<system-out>` |
| WAIVED | `<skipped message="waived until 2026-12-31: …">` |

The `skipped` element needs one new field on `junitTestCase`, and `error` needs another.

---

## 7. Where it runs

### 7.1 API (proposed)

`POST /api/conformity/check`, in a new `api/ComplianceResource.java` with `@RolesAllowed(Scopes.READ)`:

- **Request.** The body is the reference as JSON, ≤ 256 KiB.
- **Response.** 200 with the report whatever the verdict.
- **Errors:** 400 for an invalid reference (`fieldErrors` by path), 409 for a target mismatch or missing Kafka CR, 413 for a body over the limit, 503 when Kubernetes is unreachable.
- **Why POST.** It makes no write anywhere. POST is only because the reference is a body.

The service lives in a new `compliance` package: model, reader, comparisons, facts, rules and evaluator. It stores nothing.

### 7.2 CLI (proposed)

```
kates compliance check -f <reference.yaml> [--output-junit <file>] [-o table|json]
```

- **Sending.** It reads the file, keeps the `yaml.v3` node tree, converts it to JSON (§3.6) and POSTs it.
- **Output.** It prints the table, or with `-o json` the raw report, and exits as §5.6 says.
- **Flags.** It writes JUnit through `--output-junit`, as `disruption run --output-junit` does (`cmd/disruption.go:543`), and does not widen the global `-o`.
- **Registration.** `commandProfiles` gets `"compliance": {"topology"}` (`cmd/command_profiles.go:27`; `TestCommandProfilesCoverEveryCommand` requires it). It also needs the root help text and `cmd/data/doc_entries.yaml`.
- **Book.** `docs/book/03-cluster.md` (concept), `10-cli-reference.md` (the command, plus its row in the exit-code table at `:2493-2528`), `11-api-reference.md` (the endpoint) and `appendix-c-cicd.md` (the CI gate).

The name is top-level `compliance`, not `cluster conform`, because a `snapshot` subcommand follows and `cluster` subcommands also enable the chaos profile (D5).

### 7.3 In CI

```yaml
- name: Kafka platform compliance
  env: {KATES_URL: "${{ secrets.KATES_URL }}", KATES_API_KEY: "${{ secrets.KATES_API_KEY }}"}
  run: kates compliance check -f platform/prod-3az.yaml --output-junit compliance.xml
```

The key needs only the `read` scope.

### 7.4 MCP (later, proposed)

A `compliance_check` tool in `kates mcp`:

- **Input.** The reference as YAML text, ≤ 256 KiB.
- **Output.** The verdict, the summary and the non-PASS results, each with the usual caveats. The reference text is caller data. Observed values and subject names come from the cluster and are wrapped in the server's untrusted markers.
- **Safety.** It fits the server's read-only promise: it starts no load and no fault, and it sits on the same pinned cluster.

### 7.5 Snapshot (later, proposed)

`GET /api/conformity/snapshot` returns a `KafkaReference` built from the current facts, and `kates compliance snapshot > ref.yaml` prints it as YAML:

- Counts and sizes come out as `{min: <observed>}`, and everything else as exact.
- It opens with a comment saying it is a draft.

A raw snapshot passes against the cluster it came from by construction. It is a starting point to tighten, never the gate itself. It needs no new facts.

---

## 8. Security

- **Read-only.** The endpoint makes only Kubernetes get/list calls, plus Kafka Admin list and describe calls with `kafka.source: brokers` or `topics`. It reads no Secrets. It never patches, annotates, creates or deletes anything, and writes nothing to the database. A test asserts the fabric8 mock server saw no request except GET.
- **Kubernetes RBAC.** Nothing new is needed (§5.2). Declaring `podMonitor` on a cluster without the Prometheus Operator yields `false`, not an RBAC error.
- **Kafka access.** None by default. With `kafka.source: brokers`, Describe and DescribeConfigs on the Cluster resource; with `topics`, Describe and DescribeConfigs on the topics (§5.2).
- **API scope.** `read`. Observed values are configuration state that a `read` key can already see through `/topology`, and sensitive broker configs are never shown (§4.4).
- **Untrusted input.** The body is bounded (§3.6) and decoded strictly. There are no regexes, expressions or templating in v1, so the reference can only select from a fixed rule set.
- **Output.** The report echoes the caller's own reference and cluster state that a `read` key can already see through `/topology`.

---

## 8a. The offline compiler: `kates compliance lint` and `fmt`

Added on 2026-10-19 at the user's request; it runs in the CLI with no API and no cluster (the user's choice over a backend endpoint).

- **`lint`** works as a compiler does, phase by phase: YAML syntax, duplicates, the JSON Schema (walked over the YAML node tree, so each finding has a line and column), the checks the API's reader makes beyond the schema, then incoherence. Each finding is a line `file:line:col: severity: message [code]`; exit 1 on an error, 0 with warnings only, 2 on an unreadable file. `check` runs it first and stops on an error before calling the API.
- **Parity:** `cli/internal/compliance/testdata/invalid/` holds one file per case the Java reader refuses (generated from its refusal table), each with the path it is refused at. `TestCompilerRefusesWhatTheReaderRefuses` (Go) and `KafkaReferenceCorpusTest` (Java) both read it, so the offline compiler and the API cannot drift apart.
- **`fmt`** writes one canonical form: keys in schema order (broker config keys alphabetical), two-space indentation, a blank line between spec's sections, each constraint in its shortest form (`{equals: 3}` → `3`, `severity: fail` dropped, `{oneOf: [x]}` → `x`, `{min: x, max: x}` → `x`, only where the schema allows that operator, so a refused file stays refused), comments kept. It removes exact duplicates (a key set twice to the same value, a value repeated in a list, a waiver or listener listed twice) and refuses a key set to two different values. `--check` lists unformatted files; `-w` writes back.
- **Incoherence** is a catalogue of 42 rules designed by four independent lenses and kept only when two adversarial verifiers found no false positive. Errors are references no cluster can pass (zones above the pod count, a skew no split reaches, a request above its limit, a required listener contradicting `every`, …); warnings are satisfiable but moot or near-certain mistakes. An error rule that only fails because of a tolerated constraint (warn, or an active waiver) becomes a `rule-never-passes` warning. `testdata/rules.json` holds, per rule, a trigger and a near miss, run by `TestCatalogue`.

## 9. Out of scope for v1

Each item below is left out on purpose, with the reason:

- **The Kafka and monitoring rules** (`spec.kafka`, `spec.monitoring`). They are v1.1 (PRs 6, 6b and 7), after v1's count, size and topology rules have been used.

- **Stored references and results, scheduled checks, trends.** The user chose stateless; history is the CI log.
- **Snapshot.** A later PR (§7.5).
- ~~**Per-topic rules**~~: added on 2026-10-20 as `topics` (§4.6), opt-in, with prefix excludes and the subject cap.
- **KafkaUsers, ACL content and quotas.** These are security policy, already covered by `kates security`.
- **Connect, MirrorMaker 2, Cruise Control, the entity operator and Drain Cleaner.** Separate components; each would add its own section.
- **StorageClass properties** (volume expansion, reclaim policy). Kates has no `storageclasses` RBAC (`rbac.yaml` has none); this would be the first RBAC addition.
- **PodDisruptionBudgets.** Kates has no PDB RBAC, and the chart sets `maxUnavailable: 1` (`charts/kafka-cluster/values.yaml:179`).
- **Node count, capacity headroom and N+1 per zone.** This is capacity planning, closer to `kates detect`.
- **JVM heap, kubelet versions, NetworkPolicies, certificate expiry** (the last is in `kates security certs`).
- **Strimzi's per-broker ConfigMaps as a third config source.** They need `configmaps` RBAC and still miss Kafka's defaults; `kafka.source: brokers` already sees the effective values.
- **Kafka's own view for counts** (registered brokers, quorum voters). D1 keeps counts on pods in both modes.
- **Rules by pool name.** Rules are by role, which covers counts, sizes and spread without tying a reference to pool names.
- **Custom rules** (CEL, Rego, expressions), and inheritance or overlays (`extends:`). Teams keep one file per environment, or render them with their own tooling.
- **Auto-remediation.** Never; the check is read-only.
- **More than one cluster per call, and any platform other than Strimzi** (§5.8).
- **Kates' own perf and SLA gates.** They stay with scenario files.
- **SARIF output and mapping results to file lines.** Worth it only with GitHub code scanning. Rule ids already name their path in the file, and JUnit plus the exit code cover CI.
- **`--strict` and `--all`.** `severity` already says what fails, and `-o json` lists every rule.
- **A `deleteClaim` rule.** It is a data-safety check, not a count, size or topology goal.
- **A cap on waiver dates.** Code review covers a waiver that runs too long.
- **A test that the full example uses every schema property.** The two example tests catch the drift that matters (§3.9).

---

## 10. Delivery plan

The plan is small PRs, each with its own tests and book changes, merged in order up to PR 3. PRs 4 and 5 can then run in parallel, as can PRs 6 and 7 in v1.1; they touch the same rule registry, so expect small merge conflicts. New fabric8 mock-client test classes need `VertxPerMockClient` (`kates/src/test/java/com/bmscomp/kates/chaos/VertxPerMockClient.java`).

| PR | Scope | Tests |
| --- | --- | --- |
| **1. Model, validation, comparisons** (backend; no endpoint) | `compliance` package: `KafkaReference` records for the **whole** v1 schema, so the contract is reviewed once; `Constraint` (bare or map); value kinds; strict reader; bounds; rule ids; waiver checks. A version comparator that drops build suffixes and reports a parse failure as such. Quantities through fabric8 `Quantity`. Moves the schema and both examples to `cli/examples/conformity/` | Java: the examples parse strictly; a refusal table for every case in §3.9 plus the waiver rules; comparison tables (quantities, versions, config text and numbers). Go: the examples validate against the schema |
| **2. Endpoint, facts, first rules** | `POST /api/conformity/check`, `Scopes.READ`; target resolution (409/503); facts for the Kafka CR, pools, pods and `/version`; rules `<role>.count`, `controllers.dedicated`, `versions.*`; waivers, statuses, verdict, report header. Sections from PRs 4–7 return 400 "not implemented yet" | `@QuarkusTest` resource test (400/409/413/503); evaluator unit tests over hand-built facts; a fabric8 mock collector test asserting GET-only |
| **3. CLI `kates compliance check`** | Node-tree YAML to JSON; table and `-o json`; `--output-junit` (the types in `cmd/junit.go` plus `skipped` and `error` on `junitTestCase`); exits 0/1/2; profiles entry, root help, `doc_entries.yaml`; book sections (§7.2) | `httptest` stub API (never localhost:8080); golden table and JUnit output; one exit-code test per verdict and per refusal |
| **4. Sizing rules** | `requests.*`, `limits.*`, `storage.type/size/class`; PVC facts | Facts from mock pods, PVCs and pools, including JBOD, ephemeral and unbound volumes |
| **5. Placement rules** | `zones`, `skew`, `perNode`, `antiAffinity`; node facts; a small label-selector matcher (matchLabels plus In/NotIn/Exists/DoesNotExist) | Unscheduled pods, unlabeled nodes, combined roles; selector matcher table |
| **6. Kafka rules** (v1.1) | `rackAwareness`, `authorization`, `listeners.require/every`, `config`, with `kafka.source: cr` (the Kafka CR and node labels). The model and schema gain `kafka.source` (`cr` or `brokers`, default `cr`), and the `config` wording in `KafkaReference`'s javadoc, the schema and `prod-3az.yaml` names both sources. `source: brokers` returns 400 "not implemented yet" until PR 6b | A key the CR leaves out, a typed CR value (`false` against `"false"`), a broker on a node without the zone label |
| **6b. `kafka.source: brokers`** (v1.1) | `config` and `rackAwareness` through the pooled Admin client: `describeConfigs` on every broker in one call, `describeCluster` racks | Mock Admin: a broker that differs, a key no broker reports, a sensitive key, an Admin timeout giving UNKNOWN |
| **7. Monitoring rules** (v1.1) | `metrics`, `kafkaExporter`, `podMonitor` (reuses PR 5's matcher) | PodMonitor in another namespace, `any: true`, a missing CRD |
| **8a. Offline compiler** | `kates compliance lint` and `fmt`; `check` lints first; the parity corpus with its Go and Java tests; the incoherence catalogue (§8a) | Corpus parity both sides; catalogue triggers and near misses; formatter idempotence and meaning; command exit codes |
| 8. MCP `compliance_check` (later) | §7.4; one caveat if the evidence calls for it | Tool contract test; an `eval/mcp` case |
| 9. Snapshot (later) | §7.5 | A snapshot passes against the facts it came from; its output validates against the schema |
| 10. `v1` (later) | Rename the apiVersion and keep reading `v1alpha1` for one release (D6) | Both versions parse |

v1 is PRs 1–5 (decided 2026-10-19): the count, size and topology goals. v1.1 is PRs 6, 6b and 7, started after v1 has been used. Until v1.1 lands, a reference that declares `spec.kafka` (rack awareness, authorization, listeners, config) or `spec.monitoring` gets PR 2's 400 "not implemented yet" for that section; `minimal.yaml` passes, `prod-3az.yaml` does not. The book, examples and schema change with each rule PR.

---

## 11. Decisions

Taken 2026-10-19 by the maintainer: the recommended pick each time.

| # | Decision | Decided |
| --- | --- | --- |
| **D1** | What a count counts: the pods the operator created (the architecture), or what Kafka sees live (brokers registered, quorum voters)? | **Pods.** The architecture should not turn into a FAIL during a rolling restart; health is `cluster check`. The detail shows how many are Ready; with v1 Admin-less, Kafka's own view is not read |
| **D2** | Exit code when a run has both a FAIL and an UNKNOWN | **1.** A definite gap is the more useful signal. The UNKNOWN still shows |
| **D3** | Default severity: `fail` everywhere, or a per-rule default (for example monitoring and anti-affinity as warn)? | **`fail` everywhere.** One rule to learn: you declared it, so it gates. `severity: warn` is one key away |
| **D4** | Where the schema and examples live | **`cli/examples/conformity/`.** It follows the `ResilienceExamplesTest` precedent, both test suites can read it, and editors get a stable raw URL |
| **D5** | Names | **`kates compliance check`, `kind: KafkaReference`, `conformity.kates.io/v1alpha1`.** `cluster conform` would pull in the chaos profile and leave no room for `snapshot` |
| **D6** | When to call it `v1` | **After v1.1 (PRs 6, 6b and 7) and one release of real use.** Until then a field may still change shape |
| **D7** | A pod that is not scheduled, or runs on a node without the zone label: FAIL or UNKNOWN for `zones` and `skew`? | **FAIL, naming the pods.** The spread cannot be shown, and a cluster without zone labels does not have zones. A kind cluster without the labels will fail `zones`, which is correct |
| **D8** | Where the Kafka rules read from: the Kafka CR (no Kafka access) or the Kafka Admin API (effective values) | **Both, as `spec.kafka.source: cr \| brokers`, default `cr`** (2026-10-19). One reference language; only `kafka.config` and `kafka.rackAwareness` change source |

---

## 12. Found while surveying (not part of this feature)

- **`cmd/junit.go:95`** sets the suite `time` to the current Unix timestamp instead of a duration, so disruption JUnit reports a run of about 1.8 billion seconds.
- **`SecurityService.compareVersions`** (`service/SecurityService.java:1576-1590`, the CVE check) returns 1 on a parse error, so an unreadable version counts as newer. It also strips non-digits inside a part, so `11+k3s1` reads as `113`. PR 1's comparator could replace it in a follow-up.
- **`kates detect`** has the alias `cluster-check` (`cmd/detect.go:18`), next to the unrelated `kates cluster check`.
- **`/api/cluster/topology`** cannot tell "none" from "could not read" (§2). The CLI and the MCP `cluster_topology` tool show both as empty.

---

## 13. Sources

Paths relative to the repository root, at `0f3fbec`.

- `kates/src/main/java/com/bmscomp/kates/service/ClusterTopologyService.java`: CRD contexts `:36-100`, config `:108-112`, `describeTopology` `:115-157`, Kubernetes version `:159-165`, metrics and PodMonitors `:490-519`, node pools `:523-596`, nodes `:599-688`.
- `kates/src/main/java/com/bmscomp/kates/service/ClusterHealthService.java:235` (`clusterHealthCheck`), verdict `:352-364`.
- `kates/src/main/java/com/bmscomp/kates/service/SecurityService.java`: first broker only `:88-96`, `compareVersions` `:1576-1590`.
- `kates/src/main/java/com/bmscomp/kates/api/ClusterResource.java`: `/check` `:202-208`, `/topology` `:220-226`; `api/ApiError.java:35`; `security/Scopes.java:16`.
- `kates/src/main/java/com/bmscomp/kates/engine/SpecLimits.java:43`; `engine/TestOrchestrator.java:1139`.
- `charts/kates/templates/rbac.yaml:9-75`; `charts/kafka-cluster/values.yaml` (listeners `:93-105`, config `:132-140`, rack `:161-162`, PDB `:179`, node pools `:204-291`, exporter `:322`, metrics and PodMonitor `:561-575`); `charts/kafka-cluster/templates/nodepools.yaml:61` (preferred anti-affinity); `charts/kafka-cluster/profiles/platform.yaml:17-19`; `charts/kafka-cluster/Chart.yaml:40`.
- `cli/cmd/command_profiles.go:27`; `cli/cmd/junit.go:12-39,95`; `cli/cmd/disruption.go:543`; `cli/cmd/root.go:666`; `cli/cmd/apply.go:21-59,538-613`; `cli/cmd/detect.go:18`; `cli/pkg/detect/collector.go:30-35,139-151,435`; `cli/pkg/detect/types.go:340-351`; `cli/internal/kubectl/kubectl.go:39-102`; `cli/cmd/mcp_caveats.go:28-33`; `cli/client/client.go:340,396`; `cli/go.mod:14`.
- `docs/book/10-cli-reference.md:2493-2528` (exit codes).
- `kates/src/test/java/com/bmscomp/kates/resilience/ResilienceExamplesTest.java:54`; `kates/src/test/java/com/bmscomp/kates/chaos/VertxPerMockClient.java`.

**Unverified Strimzi details** (no Strimzi CRD is vendored in the repo; check them in PR 2):

- `Kafka.status.operatorLastSuccessfulVersion`, `status.kafkaVersion` and `status.clusterId`.
- The `strimzi.io/broker-role` and `strimzi.io/controller-role` pod labels. The labels appear in this repo's charts and code; the status fields do not.

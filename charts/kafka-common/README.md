# kafka-common

A Helm **library** chart: the templates the Strimzi charts in this repository share. It renders nothing on its own.

`mirror-maker2` (0.8.0), `kafka-cluster` (1.0.0) and `connect-cluster` (2.0.0) are built on it. The templates were lifted from `mirror-maker2` 0.7, the most heavily tested of the three, so each one carries a fix that the other charts had not yet received. Background and design: [docs/kafka-charts-refactor-plan.md](../../docs/kafka-charts-refactor-plan.md) §3.3.

## Using it

```yaml
# Chart.yaml
dependencies:
  - name: kafka-common
    version: 0.1.0
    repository: file://../kafka-common
```

From a checkout, run `helm dependency build charts/<chart>` before rendering: Helm refuses a chart whose dependencies are declared but not built, and `charts/*/charts/` is gitignored. A packaged chart carries the library inside it. The `kates` CLI builds the dependency itself before installing a chart that needs it.

Every template takes the calling chart's context or explicit arguments, so names and labels are always the caller's.

## Templates

| Template | What it renders | Why it is shared |
|:---|:---|:---|
| `kafka-common.name` · `.fullname` · `.namespace` · `.chart` · `.clusterDomain` | Names. `namespaceOverride` is honoured, and the domain is `global.clusterDomain`, then `clusterDomain`, then `cluster.local`. | These used to be three copies with different defaults. |
| `kafka-common.labels` · `.selectorLabels` | Standard labels, built as a map. | A map cannot hold a duplicate key; YAML silently keeps the last one. |
| `kafka-common.enabled` | `"true"` or nothing, for `(list <value> <default>)`. | `false \| default true` is `true` in Helm, which is how a switch documented as "set false to disable" stays on. |
| `kafka-common.semver` | `4.2-IV1` → `4.2.0`, `2.8` → `2.8.0`. | `semverCompare` rejects the short forms. |
| `kafka-common.rails.noFloatingTag` | Refuses `:latest` and untagged images. | What runs should not depend on when a pod last restarted. |
| `kafka-common.rails.bootstrapPorts` | Refuses a bootstrap port that the Kafka egress rule does not admit. | Otherwise the workers are dropped at the network layer while every pod reports Running. |
| `kafka-common.bootstrap` | `<cluster>-kafka-bootstrap.<ns>.svc.<domain>:<port>`, or an explicit `bootstrapServers`. | The namespace default is the caller's choice. |
| `kafka-common.clientTlsAuth` | The `tls` and `authentication` blocks of a Strimzi Kafka client: `scram-sha-512`, `scram-sha-256`, `plain`, `tls`, and `custom` (the Strimzi v1 form of OAuth). | The v1 API removed `oauth`, and each chart had covered a different subset. |
| `kafka-common.connectWorker.spec` | The spec body shared by `KafkaConnect` and `KafkaMirrorMaker2`. Details below. | Both kinds are the same Connect worker group. |
| `kafka-common.hpa` | An HPA on a Strimzi CR's scale subresource. | |
| `kafka-common.podMonitor` · `.strimziRelabelings` · `.promSelector` · `.dashboardConfigMap` | Monitoring wrappers (a PodMonitor takes one `endpoint` or a list of `endpoints`), the relabelings the Strimzi dashboards expect, and the `namespace=…, pod=~…` matcher every alert carries. | Unscoped alerts fire for every release in a Prometheus. |
| `kafka-common.netpol.*` | NetworkPolicy rule fragments: `dnsEgress`, `apiServerEgress`, `workers`, `operatorIngress`, `monitoringIngress`. | |
| `kafka-common.kafkaUser` | A `KafkaUser`, with the ACL list passed in pre-rendered. | The chart keeps a comment on every grant. |
| `kafka-common.secretSync` | Copies credential Secrets across namespaces. It runs as a post-install/upgrade hook Job with release-tracked RBAC scoped by name; `as:` renames the copy; `watch` adds a rotation CronJob. | Pre-install hooks leak their RBAC. |
| `kafka-common.grafana.layout` | Grafana panels on the 24-column grid, as JSON: rows of panels with `w` and `h`, wrapped into bands, with ids and positions computed; collapsed rows nest their panels. | A hand-numbered `y` lets a conditional or repeated section overlap the next one. |
| `kafka-common.preflight.probe` | The preflight shell function: one ApiVersions handshake with the workers' own Kafka client, classified as PROTOCOL, DNS, TLS, AUTH, LISTENER or NETWORK. | mirror-maker2 probes its sources with it, connect-cluster its Kafka cluster. |
| `kafka-common.test.clientProps` · `.test.authUnsupported` · `.imagePullPolicy` | Helpers for test and preflight pods. | |

### `connectWorker.spec`

This template covers the whole worker surface:

- **Replicas.** Strimzi's `v1` API requires `replicas`. Under an HPA the value is read back from the live CR with `lookup`, so `helm upgrade` never scales a busy group back down.
- **Standard fields:** JVM, probes, JMX, and tracing (`opentelemetry`).
- **Logging.** `inline` passes the loggers through. `external` points at the chart's ConfigMap, and chart-only keys never reach the CR.
- **Metrics.** `metricsConfig` is either the JMX exporter or the Strimzi Metrics Reporter.
- **Rack awareness:** `rack` plus `clientRackInitImage`.
- **`spec.template`**, built as a single map:
  - a PDB only when more than one worker will run;
  - `nodeSelector` rendered as required node affinity, because Strimzi's pod template has no `nodeSelector` field;
  - pod anti-affinity and topology spread on the worker selector;
  - a hardened container security context that the chart's pass-through can override one field at a time.

  The caller's `extra` is **deep-merged** into that map last. mirror-maker2 0.7 wrote its pass-through next to its own keys instead, and lost its entire pod template to a duplicate `pod:` key.

## Tests

The library cannot be rendered by itself. `tests/harness` is an application chart that calls every template, and the `helm unittest` suites in `tests/harness/tests/` assert on the output, refusals included:

```sh
helm plugin install https://github.com/helm-unittest/helm-unittest --version v0.8.2   # versions.env pin
make kafka-common-test
```

The `library` job in `.github/workflows/ci-kafka-charts.yml` runs the suites. `.helmignore` keeps `tests/` out of the packaged library.

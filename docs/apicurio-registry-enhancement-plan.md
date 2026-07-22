# apicurio-registry Chart Enhancement Plan

Audit date: 2026-07-22. Scope: `charts/apicurio-registry/` only. Branch: `feat/apicurio-registry-chart`.

The chart was vendored as a third-party tree and still looks like raw `helm create` scaffold with a kafkasql env block bolted on. It is excluded from `publish-charts.yml` and from the hygiene bar the five core charts meet (schema, README, real probes, securityContext, PDB, tests). This plan promotes it to a first-party chart wired to the in-repo `kafka-cluster` (Strimzi) chart.

> **Status 2026-07-22:** P0-1 ✓ (this branch) — bitnami kafka 17.2.6 dependency, `Chart.lock`, and the vendored `charts/kafka` tree (~100 files, Kafka 3.2-era, ZooKeeper-based) removed; `kafkasql` now always targets the `kafka-cluster` chart's `krafter` cluster, whose values already provision the `apicurio-registry` SCRAM-SHA-512 KafkaUser, ACLs (`kafkasql-` / `__apicurio` topic prefixes), and credentials Secret. Chart version 0.1.5 → 0.2.0.

## Current state

| Area | State | Notes |
|---|---|---|
| Kafka backing | ✓ | external Strimzi via `global.kafka.*` (P0-1, done) |
| Probes | ✗ | liveness/readiness hit `/` — not the Quarkus health endpoints |
| securityContext | ✗ | pod + container contexts empty (repo standard: runAsNonRoot, drop ALL, RO rootfs, seccomp) |
| values.schema.json | ✗ | none — typo'd keys silently ignored |
| README | ✗ | none |
| Resources | ✗ | no default requests/limits |
| PDB | ✗ | none; HPA scaffold has maxReplicas: 100 |
| NetworkPolicy | ~ | exists but hardcodes `kafka`/`connect`/`kates`/`monitoring` namespaces |
| Metrics | ✗ | no ServiceMonitor; registry exposes Prometheus metrics |
| UI | ✗ | Registry 3.x ships a separate `apicurio-registry-ui` image; chart deploys backend only |
| Tests | ~ | scaffold wget on `/`; nothing exercises the API or Kafka path |
| Publishing | ✗ | excluded in `publish-charts.yml` ("vendored third-party") |
| Umbrella | ✗ | not a `kates-platform` dependency |

## P0 — correctness

- **P0-1 Remove bundled bitnami kafka; bind to `kafka-cluster`.** ✓ Done in this branch (see status above).
- **P0-2 Real health probes.** Point liveness at `/health/live` and readiness at `/health/ready` (verify exact paths for Registry 3.3 — Quarkus may prefix `/q/`), with sensible initialDelay/period. A registry that cannot reach Kafka currently reports Ready.
- **P0-3 Pin and verify the image.** Confirm `apicurio/apicurio-registry:3.3.0` is the correct 3.x backend image and registry (upstream publishes 3.x primarily to `quay.io/apicurio`); expose `image.registry`, keep tag defaulting to `appVersion`.
- **P0-4 kafkasql topic provisioning.** Document (or add to `kafka-cluster` values) a `KafkaTopic` for the `kafkasql-journal` topic with compaction settings recommended by upstream, instead of relying on broker auto-create plus the `TOPIC_CONFIGURATION_VERIFICATION_OVERRIDE` escape hatch currently hardcoded to `true` — make that env conditional.

## P1 — repo hygiene bar

- **P1-1 securityContext defaults** matching the other first-party charts (runAsNonRoot, drop ALL capabilities, readOnlyRootFilesystem + emptyDir for `/tmp`, seccompProfile RuntimeDefault).
- **P1-2 values.schema.json** (`additionalProperties: false` — same rationale as strimzi-operator: typo'd keys must fail loudly).
- **P1-3 Default resources** (registry is a Quarkus JVM app; start ~256Mi–1Gi, small CPU request) and a PDB when `replicaCount > 1`; cap HPA maxReplicas at something sane.
- **P1-4 Dynamic-namespace NetworkPolicy.** Replace hardcoded namespace names with values (follow the `fix/networkpolicy-dynamic-namespaces` pattern used elsewhere in the repo); egress port must follow the configured bootstrap port instead of fixed 9092.
- **P1-5 README + NOTES.txt** — document the kafka-cluster contract explicitly: bootstrap service, SCRAM user/Secret name, required ACLs, and the `global.kafka.*` interface consumed by umbrella charts.
- **P1-6 Chart tests** — keep the HTTP test but hit a real API path (`/apis/registry/v3/system/info`); optionally a second test that round-trips an artifact when Kafka is reachable.

## P2 — integration & release

- **P2-1 Publish it.** Remove the `apicurio-registry` exclusion from `publish-charts.yml` (OCI push + cosign, like the rest) once P1 lands; it is already covered by the CI `Lint and template all charts` loop and kubeconform.
- **P2-2 kates-platform dependency.** Add as optional umbrella member (`condition: apicurio-registry.enabled`, `file://../apicurio-registry`), passing `global.kafka.*` from platform values.
- **P2-3 Optional UI deployment** (`ui.enabled`): second Deployment/Service for `apicurio-registry-ui` with ingress path wiring to the backend.
- **P2-4 Metrics ServiceMonitor** gated on `metrics.enabled`, aligned with the monitoring chart's label selectors.
- **P2-5 TLS path.** Support `SASL_SSL`/mTLS against the kafka-cluster `tls` listener (mount the cluster CA cert Secret, truststore env wiring) as an alternative to `SASL_PLAINTEXT`.
- **P2-6 appVersion refresh cadence** — track upstream 3.x releases; bump `appVersion` + chart minor together and let the docs gates (`gen-chart-table`, `gen-version-matrix`) enforce table sync.

## Sequencing

Week 1: P0-2..P0-4 (small, independent). Week 2: P1 as one hygiene pass (mirrors the second-tier chart cleanup done in the chart-enhancements branch). Week 3: P2-1/P2-2 release wiring, then UI/TLS/metrics as demand dictates.

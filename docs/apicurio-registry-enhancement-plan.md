# apicurio-registry Chart Enhancement Plan

Audit date: 2026-07-22. Scope: `charts/apicurio-registry/` only. Branch: `feat/apicurio-registry-chart`.

The chart was vendored as a third-party tree and still looks like raw `helm create` scaffold with a kafkasql env block bolted on. It is excluded from `publish-charts.yml` and from the hygiene bar the five core charts meet (schema, README, real probes, securityContext, PDB, tests). This plan promotes it to a first-party chart wired to the in-repo `kafka-cluster` (Strimzi) chart.

> **Status 2026-07-22:** All three waves landed on `feat/apicurio-registry-chart`. Chart version 0.1.5 → **0.3.0**.
>
> - **Week 1 (P0) ✓** — P0-1 removed the bitnami kafka 17.2.6 dependency, `Chart.lock`, and the vendored `charts/kafka` tree (~100 files, Kafka 3.2-era, ZooKeeper); `kafkasql` targets the `kafka-cluster` `krafter` cluster (SCRAM user, ACLs, Secret already provisioned there). P0-2 probes hit `/health/live` + `/health/ready` on the 3.x management port 9000. P0-3 image moved to `quay.io` with registry override + digest pinning. P0-4 `kafkasql-journal` / `kafkasql-snapshots` provisioned as `KafkaTopic` CRs (`cleanup.policy=delete`, infinite retention); the verification-override env is now opt-in.
> - **Week 2 (P1) ✓** — securityContext (runAsNonRoot 185, seccomp, RO rootfs, emptyDir `/tmp`), `values.schema.json` (`additionalProperties:false`), default resources, PDB, HPA capped at 5, topology spread, configurable probes + startup probe, dynamic-namespace NetworkPolicy, README documenting the kafka-cluster contract, NOTES runbook, and a chart test hitting `/apis/registry/v3/system/info`.
> - **Week 3 (P2) ✓** — removed from the `publish-charts.yml` exclusion (now OCI-pushed + cosign-signed like the rest); added as optional `kates-platform` dependency (`condition: apicurio-registry.enabled`); optional UI Deployment/Service/Ingress (P2-3); ServiceMonitor scraping the management port (P2-4); `SASL_SSL`/mTLS truststore wiring against the cluster `tls` listener (P2-5).
> - **Post-P2 correctness ✓** — `kafkaSql.consumerGroupPrefix` (default `apicurio`) sets `APICURIO_KAFKASQL_CONSUMER_GROUP_PREFIX` so the registry's per-replica consumer group falls within the `kafka-cluster` KafkaUser's group-prefix ACL. Without it the default random-UUID group is denied by the secured cluster and the registry cannot replay its own journal.

## Post-P2 follow-ups (chart 0.4.0)

- **Deploy path realigned ✓** — `config/apicurio/apicurio-values.yaml` and its offline variant, plus `scripts/deploy-apicurio.sh`, were still pinned to the legacy 2.x `apicurio-registry-kafkasql` image and set removed keys (`kafka.enabled`, `global.kafka.properties`) that the new `additionalProperties:false` schema rejects — so the actual deploy would have failed at `helm install`. They now use the chart defaults (quay.io 3.x), the `global.kafka.*` interface, and `service.nodePort` (30082); the script relies on the chart's Secret wiring instead of hand-injecting JAAS env. `service.nodePort` support was added to the chart.
- **Live validation on kind ✓** — new `.github/workflows/ci-apicurio.yml` deploys Strimzi + `kafka-cluster` + this chart via the same scripts `make all` uses, waits for Ready (which by itself proves probes/port 9000/image/SCRAM/journal replay), and round-trips a schema through the ccompat v3 API. Path-filtered to the relevant charts/config plus `workflow_dispatch`. *New heavy job — expect a first-run shake-out on the runner.*
- **Events topic ✓** — `kafkaTopics.events.enabled` provisions `registry-events` (finite retention) for when eventing is turned on; off by default.
- **UI hardening ✓** — UI container defaults to `readOnlyRootFilesystem: true` with tmpfs mounts at nginx's writable paths (`/tmp`, `/var/cache/nginx`, `/var/run`).

### Still open

1. **kafka-ui integration.** Wire `kafka-ui`'s `schemaRegistry.enabled` at this registry so schemas surface in the UI.
2. **Registry auth.** No auth env today; add OIDC when needed.
3. **Publish dry-run.** Trigger `publish-charts` once (tag) to confirm the OCI push + cosign now works for this chart.
4. **UI probe realism.** UI liveness/readiness hit `/`; confirm the 3.x UI image serves a lighter health path.

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

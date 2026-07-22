# Helm Chart Enhancement Plan

Audit date: 2026-07-21. Scope: all first-party charts under `charts/` (vendored `apicurio-registry` tree excluded).

The five core charts (`kates`, `kafka-cluster`, `connect-cluster`, `kates-chaos`, `strimzi-operator`) are mature — full label helpers, probes, securityContexts, PDBs, NetworkPolicies, schemas, tests, READMEs. The real gaps are **release engineering (CI gating + publishing)**, a handful of **concrete bugs**, and **inconsistent hygiene in the second-tier charts**.

## Current state

| Chart | schema | README | .helmignore | tests | Lint | Notes |
|---|---|---|---|---|---|---|
| kates | ✓ | ✓ | ✗ | ✓ | **FAIL** | stale postgresql dep (see P0-1) |
| kafka-cluster | ✓ | ✓ | ✗ | ✓ | ✓ | label helper incomplete (P0-3) |
| connect-cluster | ✓ | ✓ | ✓ | ✓ | ✓ | most polished |
| kates-chaos | ✓ | ✓ | ✓ | ✓ | ✓ | no publish target |
| strimzi-operator | ✓ | ✓ | ✓ | ✓ | ✓ | no publish target |
| kafka-ui | ✓ | ✓ | ✓ | ✓ | ✓ | complete |
| velero | ✓ | ✓ | ✓ | ✗ | ✓ | |
| minio | ✗ | ✓ | ✓ | ✗ | ✓ | |
| headlamp | ✗ | ✗ | ✗ | ✓ | ✓ | |
| monitoring | ✗ | ✗ | ✗ | ✗ | ✓ | |
| kates-platform | ✗ | ✗ | ✗ | ✗ | warn | umbrella; local deps unbuilt |

---

> **Status 2026-07-21:** Week 1 complete — P0-1 ✓ P0-2 ✓ P0-3 ✓ P0-4 ✓ P1-1 ✓, plus kates-platform dependency pins fixed (part of P3-3).
> **Week 3 complete** — P2-4 ✓ (schemas for minio/headlamp/monitoring/kates-platform; README + .helmignore for headlamp/monitoring/kates-platform; smoke tests for velero/minio/monitoring/kates-platform; kates maintainer email; connect-cluster icon; Grafana default-password warning in monitoring NOTES) · P2-5 ✓ (download-charts.sh pulls litmus-core) · P3-1 ✓ (drain-cleaner replicas/PDB/scheduling + optional TLS secret mount, caBundle, webhook annotations) · P3-2 ✓ (productionMode rejects embedded PostgreSQL; scheduling passthrough added for dev use) · P3-3 ✓ (platform-chart-deps/lint/package/push Makefile targets). Versions bumped: kates 0.6.0, kafka-cluster 0.3.0, connect-cluster 1.3.1, headlamp 0.2.0, kates-monitoring 1.1.0, kates-platform 0.3.0, minio 17.0.22, velero 11.3.3 — README table synced. Hygiene matrix now fully ✓ for schema/README/.helmignore/tests/lint on all first-party charts.
> **Week 2 complete** — P1-2 ✓ (kubeconform in CI; caught and fixed 3 real bugs: duplicate label keys in connect-cluster test connectors/topics and kafka-cluster test pods, and invalid null `credential`/`provider` stubs in velero BSL/VSL) · P1-3 ✓ (`publish-charts.yml` tag-triggered OCI publish with cosign; `chaos-chart-push` + `strimzi-chart-push` Makefile targets) · P1-4 ✓ (README chart-table check in CI) · P2-1 ✓ (.helmignore; packages verified overlay-free) · P2-2 ✓ (33 top-level `# --` annotations in kafka-cluster values) · P2-3 ✓ (`productionMode` guard in kates prod/corporate overlays; seaweedfs `existingSecret` + placeholder-credential guard, prod overlay switched to existingSecret). Notes: kates' stale Chart.lock/tgz were never git-tracked, which is why CI lint stayed green while local lint failed. Upstream drain-cleaner mounts webhook TLS certs (`/etc/webhook-certificates`) and sets a `caBundle`; our template does neither — fold into P3-1.

## P0 — Bugs & correctness (do first, ~1 day)

**P0-1. Fix `kates` broken dependency state.** `charts/kates/Chart.lock` + vendored `charts/kates/charts/postgresql-16.7.27.tgz` reference Bitnami postgresql, but `Chart.yaml` has no `dependencies:` block → `helm lint` errors ("chart metadata is missing these dependencies: postgresql") and `helm package` bundles a dead 100KB+ tgz. Action: delete `Chart.lock` and the tgz (the real DB is the self-contained StatefulSet in `templates/postgresql.yaml`), or declare it as a real conditional dependency. Add a lint step that would have caught this (see P1).

**P0-2. Fix drain-cleaner probe/port mismatch.** `charts/kafka-cluster/templates/drain-cleaner.yaml` declares only `containerPort: 8443` (l.74) while liveness/readiness probes target `port: 8080` (l.94, l.100). Verify the app's health port and align: declare `8080` as a named `health` port or point probes at 8443.

**P0-3. Complete `kafka-cluster` label helper.** `charts/kafka-cluster/templates/_helpers.tpl` (l.18–24) omits `app.kubernetes.io/name` and `app.kubernetes.io/instance` — every resource in the chart is missing the two most standard labels (all other charts have them). Also add a fallback/`required` for `kafka-cluster.clusterName` (l.11–13), which renders empty if unset.

**P0-4. Templatize hardcoded Velero namespace.** `charts/kafka-cluster/templates/backup.yaml` l.25 and l.85 hardcode `namespace: velero` → `.Values.backup.veleroNamespace` (default `velero`).

## P1 — CI gating & publishing (biggest systemic gap, ~2–3 days)

**P1-1. Gate every chart on chart changes.** Today `ci.yml` lints/templates only `kates` + `strimzi-operator`; `kafka-cluster` triggers only on `values.yaml` (not its 30 templates); `connect-cluster` and `kates-chaos` have **no** chart-triggered check; `integration.yml` lints all charts but never fires on `charts/**` paths. Action: adopt `helm/chart-testing` (`ct lint` + `ct install` on Kind) with trigger path `charts/**`, or minimally extend `ci.yml`'s matrix to all 11 charts and add `charts/**` to `integration.yml` paths.

**P1-2. Add schema validation of rendered manifests.** No `kubeconform`/`kubeval` anywhere. Add a CI step: `helm template <chart> | kubeconform -strict -ignore-missing-schemas` (Strimzi/Litmus/Kyverno CRDs via `-schema-location`).

**P1-3. Automate chart publishing.** Publishing is manual `make chart-push` / `kafka-chart-push` / `connect-chart-push` (Makefile l.424–724) to `oci://ghcr.io/bmscomp/charts`; **no workflow invokes it**, and `kates-chaos` + `strimzi-operator` have no publish target at all. Action: add a tag-triggered `publish-charts.yml` that packages+pushes all charts OCI (with cosign signing, matching the existing image pipeline), and add the two missing Makefile targets.

**P1-4. Enforce docs/versions in CI.** `scripts/gen-chart-table.sh --check` and `scripts/check-versions.sh` exist — run both on every `charts/**` PR. Adopt `helm-docs` to generate per-chart READMEs from `# --` annotations (see P2-2).

## P2 — Consistency & hygiene (~2 days)

**P2-1. `.helmignore` for kates and kafka-cluster** (both missing; both ship `values-*.yaml` overlays and, for kates, the stale tgz into packages). Copy the one from `connect-cluster`.

**P2-2. Unify values documentation convention.** `kates` has 215 `# --` helm-docs annotations; `kafka-cluster` has 10 (767-line values.yaml documented with free-form comments). Convert `kafka-cluster/values.yaml` to `# --` so helm-docs/Artifact Hub render it.

**P2-3. Secrets hygiene.** Plaintext dev defaults: `charts/kates/values.yaml:437` (`password: kates`) and `charts/kafka-cluster/values.yaml:742` (`secretAccessKey: "change-me-in-prod"`). Action: keep dev defaults but fail template rendering in prod overlays unless `existingSecret` is set (`required` guard keyed on a `productionMode`/overlay value), and document rotation.

**P2-4. Bring second-tier charts to baseline.** Add `values.schema.json` (minio, headlamp, monitoring, kates-platform), `README.md` + `.helmignore` (headlamp, monitoring, kates-platform), and a smoke test in `templates/tests/` (velero, minio, monitoring, kates-platform). Add maintainer email to `kates/Chart.yaml` and fill empty `icon:` in `connect-cluster/Chart.yaml`.

**P2-5. Align `scripts/download-charts.sh`** — it pulls the full `litmus` chart while `kates-chaos` depends on `litmus-core`.

## P3 — Production hardening (~2–3 days)

**P3-1. drain-cleaner availability.** Single replica ValidatingWebhookConfiguration backend = cluster-wide eviction SPOF. Add `replicas`, PDB, and affinity/tolerations passthrough in `drain-cleaner.yaml`.

**P3-2. kates embedded Postgres.** `templates/postgresql.yaml` hardcodes `replicas: 1`, no PDB, no scheduling passthrough. Either add those knobs or document it as dev-only and `required`-guard prod overlays to `externalDatabase`.

**P3-3. kates-platform umbrella.** Local deps (`kafka-cluster`, `kates`) aren't built (`helm lint` warns). Wire `helm dependency build` into Makefile/CI and pin versions.

## P4 — DX & docs (opportunistic)

- Per-chart `CHANGELOG` via `artifacthub.io/changes` annotations (kates-chaos and strimzi-operator already do this — extend to the rest).
- Publish rendered helm-docs to `docs/book` chart appendix; cross-link per-chart READMEs from `docs/kates-chaos-chart.md` pattern.
- Add `helm unittest` for the highest-risk templates (kafka.yaml, kafka-connect.yaml, crd-upgrade hook).

## Suggested sequence

Week 1: P0 (all) + P1-1. Week 2: P1-2..P1-4 + P2-1..P2-3. Week 3: P2-4..P2-5 + P3. P4 ongoing.

Acceptance: `helm lint` green for all 11 charts; every chart change triggers lint+template+kubeconform in CI; tagged release publishes all charts to `oci://ghcr.io/bmscomp/charts`; hygiene matrix above fully ✓.

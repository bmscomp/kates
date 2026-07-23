# strimzi-operator Chart Enhancement Plan — Production Focus

Audit date: 2026-07-23. Scope: `charts/strimzi-operator/` (wrapper over the upstream `strimzi-kafka-operator` 1.1.0 subchart). Reviewed: `values.yaml`, `values-prod.yaml`, `values-{dev,kind,generic}.yaml`, `templates/{crd-upgrade,tests/test-operator,_helpers,NOTES}`, `values.schema.json`, and the vendored subchart `values.yaml`.

This chart is already mature: strict `values.schema.json` (`additionalProperties:false` at the wrapper level), an owned CRD-upgrade hook that closes Helm's crds/ gap with download validation + server-side dry-run, watch-scope assertions in the Helm tests, and behavior-preserving documentation on every non-default. The gaps are almost entirely in the **production overlay** — the wrapper is sound; `values-prod.yaml` is under-built and, in one place, actively unsafe.

> **Status 2026-07-23:** P0, P1 and P2 implemented on `feat/strimzi-operator-prod-ha`.
>
> - **P0 ✓** — `values-prod.yaml` now sets `replicas: 2` (real active/standby HA; leader election was already on) with `topologySpreadConstraints` (hostname + zone, ScheduleAnyway), which also makes the pre-existing `minAvailable: 1` PDB drain-safe instead of a node-drain deadlock. New `scripts/check-pdb-safety.sh` fails the build if any rendered PDB has `minAvailable >= replicas`; wired into the Helm Lint job.
> - **P1 ✓** — wrapper-owned `PodMonitor` (operator's own `:8080/metrics`) + `PrometheusRule` (down / no-leader / reconciliation-failure / restart-loop), gated on new `metrics.podMonitor.enabled` / `alerts.enabled` (default off, on in prod, `release: kafka` labels), added to the strict schema; `JAVA_OPTS -Xmx512m` heap pin under the 768Mi limit; config-gated prod-invariant Helm test (in-cluster PDB-safety + NetworkPolicy presence) with extended test RBAC.
> - **P2 ✓** — watch-scope test verifies the actual scope in both directions (cluster-wide → `*`, scoped → the exact `watchNamespaces` list / release namespace); `featureGates: ""` pinned in prod (reviewed, version-default gates); commented `nodeSelector`/`tolerations` placement scaffold; the airgap recipe expanded to all four egress dependencies (CRD bundle, operator subchart, operator image by digest + imagePullSecrets, operand images via `STRIMZI_*_IMAGES`).
>
> **Post-P2 hardening ✓** — capability-guarded the operator PodMonitor/PrometheusRule (`.Capabilities.APIVersions.Has "monitoring.coreos.com/v1"`) so enabling metrics on a cluster without the Prometheus Operator no longer fails `helm install`, with a loud NOTES warning + a CI step that validates them with the CRD advertised. Fully-airgapped CRD hook: `crdUpgrade.bundleConfigMap` sources the bundle from a user-created ConfigMap (no per-upgrade egress); `crdUpgrade.networkPolicy.enabled` adds a hook-scoped egress NetworkPolicy (DNS + 443) so the hook works in default-deny namespaces.
>
> **Live smoke test ✓** — `.github/workflows/ci-strimzi.yml` (manual `workflow_dispatch`) installs the chart with the **prod overlay** on kind (Prometheus Operator CRDs installed first so the guarded monitors apply; operator memory trimmed but replicas kept at 2) and runs `helm test`, executing the CRD-upgrade hook and every test hook — operator-Available, CRDs-Established, watch-scope, and the drain-safe-PDB + NetworkPolicy invariants — then asserts the PDB/NetworkPolicy/PodMonitor/PrometheusRule objects exist.
>
> **Still open (deliberately not code):** the Grafana dashboard ownership decision (`kafka-cluster` vs. operator — upstream names the 9 ConfigMaps release-independently, so they collide). Enable `dashboards` on exactly one side once ownership is decided.

## Current state (`values-prod.yaml` today)

| Concern | Prod overlay today | Verdict |
|---|---|---|
| Hardening (podSecurityContext, RO rootfs, drop ALL) | ✓ set | good |
| PodDisruptionBudget | ✓ enabled, `minAvailable: 1` | **unsafe** — see P0-1 |
| Replicas / HA | ✗ inherits `replicas: 1` | **gap** — see P0-1/P1-1 |
| Replica anti-affinity / spread | ✗ none | gap (P1-1) |
| priorityClassName | ✓ `system-cluster-critical` | good |
| Reconciliation tuning | ✓ `fullReconciliationIntervalMs: 60000` | good |
| operatorNetworkPolicy | ✓ enabled | good |
| Operator self-metrics (PodMonitor) | ✗ none | gap (P1-2) |
| Alerting (PrometheusRule) | ✗ none | gap (P1-2) |
| Image digest pinning | ✗ tag only (inherits appVersion) | gap (P2-1) |
| Airgapped operand/CRD sourcing | ~ mirror-URL documented only | partial (P2-1/P2-2) |
| featureGates pinning | ✗ inherits upstream default (`""`) | gap (P2-3) |
| JVM heap sizing (`JAVA_OPTS`) | ✗ memory set, heap unpinned | gap (P1-3) |
| Node placement (nodeSelector/tolerations) | ✗ none | gap (P2-4) |
| Watch-scope blast radius | cluster-wide (`watchAnyNamespace: true`) | decision (P2-5) |
| Tests assert prod invariants | ✗ only Available/CRDs/scope | gap (P1-4) |

## P0 — correctness / safety (the overlay is currently dangerous)

**P0-1 The PDB + single-replica trap.** `values-prod.yaml` sets `podDisruptionBudget.enabled: true, minAvailable: 1` while `replicas` stays at the inherited `1`. A budget of "at least 1 available" over a pool of exactly 1 means **no voluntary eviction can ever succeed** — `kubectl drain`, cluster-autoscaler scale-down, and rolling node upgrades all block indefinitely on the operator pod. `unhealthyPodEvictionPolicy: IfHealthyBudget` does not save this: it only relaxes eviction for *unhealthy* pods; a healthy single operator still wedges the drain. This is a production-node-lifecycle hazard shipped as the "production" overlay.

The fix is inseparable from HA: set `replicas: 2`. Leader election is already `enable: true` (its entire reason to exist is active/standby operators), so a second replica costs one idle pod and makes `minAvailable: 1` correct — the standby can be evicted, and node drains work. Ship P0-1 and P1-1 together.

**P0-2 Add a CI guard for the invariant.** Following this chart's own "make the silent failure loud" philosophy (the `watchAnyNamespace` test, the schema rejecting flat keys), add a rendered-manifest check that fails when a PDB is present with `minAvailable ≥ replicas`. A `helm template … -f values-prod.yaml | <assert>` step in `ci.yml`'s helm-lint job catches the trap before it reaches a cluster, and catches anyone re-introducing it.

## P1 — high availability & observability (make "production" mean production)

**P1-1 Genuine operator HA.** With `replicas: 2` (P0-1), spread the replicas so the standby survives the loss of the active's node/zone. Upstream exposes `topologySpreadConstraints` (preferred) and `affinity`. Add to the prod overlay a `topologySpreadConstraints` entry keyed on `kubernetes.io/hostname` (and `topology.kubernetes.io/zone` where multi-AZ), `whenUnsatisfiable: ScheduleAnyway` so a single-node dev cluster still schedules. This is the difference between "leader election configured" and "leader election useful."

**P1-2 Operator self-observability.** Today only the Kafka *operands* are monitored; the cluster operator itself is a blind spot. Two additions, both parent-chart-owned so they don't depend on the punted `dashboards` ownership decision:
- A `PodMonitor` (gated `metrics.enabled`) scraping the operator's metrics port, labeled for the repo's Prometheus selector.
- A `PrometheusRule` (gated `alerts.enabled`) with operator-health alerts: operator Deployment not Available, pod restart/crashloop, reconciliation failure rate, and — high-value given the CRD hook — a CRD-drift / hook-failure signal. Resolve the Grafana dashboard collision (upstream names 9 ConfigMaps release-independently, colliding with `kafka-cluster`) by owning them on exactly one side; document the decision here rather than leaving `dashboards.enabled: false` as a permanent shrug.

**P1-3 Pin the operator JVM heap.** The overlay sets memory requests=limits at 768Mi (Guaranteed QoS, good) but leaves the JVM heap unbounded, so the operator sizes `-Xmx` from cgroup defaults and can either under-use the allocation or approach the limit and OOMKill under reconciliation load. Set `extraEnvs: [{name: JAVA_OPTS, value: "-Xms256m -Xmx512m"}]` (headroom under 768Mi for non-heap/metaspace/threads). Tune with the operator's actual RSS.

**P1-4 Tests that assert the prod invariants.** Extend `templates/tests/test-operator.yaml` (or a prod-only test) to assert what the overlay claims to deliver, so a broken uplift fails `helm test` instead of silently regressing: PDB exists and is consistent with replica count (the P0-1 guard, in-cluster form), NetworkPolicy for the operator exists, the operator Deployment carries the hardened securityContext, and `replicas ≥ 2`. Mirrors the existing watch-scope assertion.

## P2 — supply chain, airgap, and multi-tenancy

**P2-1 Digest-pinned, mirrorable images.** For reproducible and airgapped installs, pin the operator image by digest, not just the appVersion tag. Upstream `image.{registry,repository,name,tag}` plus `defaultImageRegistry/Repository/Tag` and `image.imagePullSecrets` allow this. Add a prod pattern: `defaultImageRegistry: registry.internal` + a digest-pinned operator tag + `imagePullSecrets`. Document that operator supply-chain is only half the story — see P2-2.

**P2-2 Operand images and CRD bundle for true airgap.** Two egress dependencies remain on every install/upgrade: (a) the CRD-upgrade hook `curl`s the bundle URL — already mirrorable via `crdUpgrade.url`, but a fully offline mode should source the bundle from a vendored ConfigMap or a bundle baked into the `kates-tester` image, removing the per-upgrade network gate entirely; (b) the operator pulls **operand** images (Kafka, bridge, connect, etc.) which `defaultImageRegistry` alone does not fully redirect — Strimzi resolves specific operand images via `STRIMZI_KAFKA_IMAGES` and friends. Add an `extraEnvs` / operand-image-map section to the prod overlay for airgapped clusters and document the complete set.

**P2-3 Pin featureGates.** `featureGates: ""` inherits whatever the operator version defaults to, so an operator upgrade can silently flip gate behavior. For production, pin the gate string explicitly to the reviewed set for 1.1.0 so upgrades are deliberate. Expose it in the prod overlay with a comment tying it to the appVersion.

**P2-4 Node placement.** Add optional `nodeSelector` + `tolerations` to the prod overlay so the operator can be pinned to an infra/system node pool (common for cluster-scoped operators), consistent with `priorityClassName: system-cluster-critical` already set.

**P2-5 Watch-scope blast radius (decision, not a default).** `watchAnyNamespace: true` grants cluster-wide reconciliation and RBAC. For multi-tenant clusters that isolate Kafka to specific namespaces, `watchNamespaces: [ns1, ns2]` (with `watchAnyNamespace: false`) shrinks the blast radius. This is a security posture decision — document both, and if adopted, extend the watch-scope test to assert the *expected* scope rather than only `*`.

## Sequencing

- **Week 1 — P0 (ship immediately):** P0-1 (`replicas: 2` + fix the PDB semantics) and P0-2 (CI guard). This removes a live node-drain hazard and is a two-line overlay change plus one CI assertion.
- **Week 2 — P1 (make prod real):** P1-1 spread, P1-3 heap, P1-4 tests as one overlay+test pass; P1-2 observability as its own change (needs the dashboard-ownership decision).
- **Week 3 — P2 (harden the supply chain):** P2-1/P2-2 image + airgap, then P2-3 featureGates, P2-4 placement, P2-5 watch-scope as demand dictates.

No wrapper-template changes are required for P0/P1 except the new PodMonitor/PrometheusRule/tests; everything else is `values-prod.yaml` plus schema entries. Every new key must be added to `values.schema.json` (the chart's contract is that unknown keys fail loudly) and pass `scripts/check-versions.sh` for anything touching the Strimzi version pin.

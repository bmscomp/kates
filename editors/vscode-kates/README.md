# Kates YAML Studio — VS Code Extension

First-class IDE support for authoring, validating, refactoring, and executing all **6 Kates YAML dialects**:

1. **Performance Scenarios** (`scenarios/*.yaml`, `perf-*.yaml`, `load-test.yaml`)
2. **Multi-Phase Test Scenarios & Requests** (`test-request*.yaml` with `WARMUP`, `RAMP`, `STEADY`, `SPIKE`, `COOLDOWN`)
3. **Flow Pipelines** (`flow-*.yaml` with `test` and `wait` steps and grade gates)
4. **Resilience Chaos Tests** (`resilience-*.yaml` with `testRequest`, `chaosSpec`, and `probes`)
5. **Disruption Plans & Playbooks** (`playbooks/*.yaml` with blast-radius safety guards and ISR/lag recovery tracking)
6. **Compliance References** (`conformity/*.yaml` — `KafkaReference` `conformity.kates.io/v1alpha1`)

## Features

- **Strict Unknown-Attribute Detection & Typo Quick-Fixes**: Flags any non-existing attribute at every nesting level and offers 1-click Quick Fixes using Levenshtein closest-key matching and dialect-aware hints (e.g., `records` vs `numRecords`).
- **Deep Cross-Field, Safety-Envelope & Continuity Diagnostics**:
  - `minInsyncReplicas <= replicationFactor`
  - `enableIdempotence` / `enableTransactions` requires `acks: "all"` and `backend: native`
  - `backend: trogdor` incompatibility checks for `INTEGRITY` and `INTEGRATION_CDC` (`incompatible-backend`)
  - Engine reaper deadline warnings (`reaper-deadline` when planned duration exceeds `3600s`, accounting for `2×` read-back duration on `INTEGRITY` runs)
  - Contradictory, impossible, and unevaluable SLA gate checks (`contradictory-sla`, `impossible-sla-gate` vs `parallelProducers × targetThroughput`, `unevaluable-gate` for `maxRtoMs`/`maxRpoMs` on non-`INTEGRITY` runs)
  - Multi-phase `RAMP` continuity warnings (`ramp-without-override`)
  - `FaultLimits` numeric bounds, `chaosDurationSec > 0` requirements, `envOverrides` allow-list (`TARGET_NODE`, `NODE_LABEL`), and Kubernetes label selector parser
  - `ProbeSpec` validation (`cmdProbe` prohibited inside `FaultSpec.probes`, `kafkaProbe` command grammar, numeric comparators)
  - Full `KafkaReference` compiler checks (incoherence, `min > max`, expired waivers, undeclared rule waivers)
- **Inline Ghost-Text Inlay Hints (`textDocument/inlayHint`)**:
  - Shows effective `TestTypeDefaults` inline next to `type: <TEST_TYPE>` (`# defaults: 3p · 5M rec · 900s · acks=all`).
  - Computes estimated workload footprint (`GiB`/`MiB`), aggregate producer rate (`rec/s`), and planned deadline next to `spec:`.
  - Computes total chaos execution windows on `DisruptionPlan` `steps:` and `ResilienceTestRequest` `chaosSpec:`.
  - Displays declared rule and active waiver counts on `KafkaReference` `spec:`.
- **Outline View, Breadcrumbs, Folding & Cross-References (`documentSymbol`, `references`, `rename`, `foldingRange`)**:
  - Hierarchical Outline & Breadcrumb symbols for scenarios, multi-phase stages, flow steps, disruption steps, resilience probes, and compliance sections.
  - Find All References & Atomic Rename (`F2`) across topics (`topic`, `targetTopic`, `isrTrackingTopic`, `produce <topic>`), consumer groups (`consumerGroup`, `lagTrackingGroupId`, `consumer-lag <group>`), and compliance rules/listeners (`spec:` ↔ `spec.waivers[].rule`).
- **Cross-Dialect Refactoring Code Actions (`refactor.rewrite`)**:
  - Convert `spec:` blocks between CLI scenario keys (`records`, `parallelProducers`, `recordSizeBytes`, `durationSeconds`) and wire `TestSpec` keys (`numRecords`, `numProducers`, `recordSize`, `durationMs`).
  - Refactor a standalone performance scenario into a full `ResilienceTestRequest` chaos specification.
  - Generate a compliance waiver entry under `spec.waivers:` for the rule at the cursor.
- **Live Cluster Autocompletion & Blast-Radius Preflight**:
  - When connected to a Kates cluster, autocompletes live Kafka topics, consumer groups, Strimzi namespaces, Kubernetes worker nodes (`TARGET_NODE`), and broker IDs (`targetBrokerId`).
  - `Kates: Run Live Cluster Preflight & Blast-Radius Preview` checks `replicationFactor` / `minInsyncReplicas` against live `BrokerCount` and runs `POST /api/disruptions?dryRun=true` to preview affected pods and partition leaders.
- **Native VS Code Test Explorer, Template Gallery & Scenario Diff**:
  - Discovers Kates YAML files in the workspace Testing sidebar with **Offline Lint & Validate** and **Execute on Cluster** run profiles.
  - `Kates: New YAML File from Template...` scaffolds curated starter templates across all 6 dialects.
  - `Kates: Compare / Diff Scenario Workload & SLA Gates...` renders a side-by-side Markdown diff table of two scenarios' effective parameters and SLA thresholds.

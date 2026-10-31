# Kates Visual Studio / VS Code YAML Plugin — Architecture & Implementation Plan

| | |
| :--- | :--- |
| **Status** | Proposed Design & Implementation Plan |
| **Target IDEs** | Visual Studio Code (`vscode-kates` VSIX) & Visual Studio 2022+ (`Kates.VisualStudio` VSIX via LSP + JSON Schema) |
| **Dialects Covered** | 1. Standard & Performance Test Scenarios (`kates test apply` / `lint`)<br>2. Advanced Multi-Phase Scenarios & API Envelopes (`CreateTestRequest` / `TestScenario`)<br>3. Declarative Flow Pipelines (`kates flow run` / `lint`)<br>4. Chaos & Resilience Tests (`kates resilience run`)<br>5. Disruption Plans & Playbooks (`kates disruption run`)<br>6. Compliance Reference Architecture (`KafkaReference`, `conformity.kates.io/v1alpha1`) |
| **Core Capabilities** | Context-aware auto-completion & snippets, TextMate + LSP semantic highlighting, unknown-attribute error detection with edit-distance quick fixes, cross-field semantic validation, canonical formatting, and inline CodeLens actions |

---

## 1. Executive Summary & Architectural Strategy

Kates uses **six distinct YAML dialects** across testing, chaos engineering, flow orchestration, and platform compliance. While `cli/examples/conformity/kafkareference.v1alpha1.schema.json` exists for compliance files and the Go CLI implements offline linters for scenarios (`cli/cmd/scenario_lint.go`), flows (`cli/cmd/test_lint.go`), and compliance (`cli/internal/compliance/compile.go`), authors currently lack:
1. Published JSON Schemas for the five non-compliance YAML dialects.
2. Offline linters for Resilience (`ResilienceTestRequest` / `FaultSpec` / `ProbeSpec`) and Disruption (`DisruptionPlan` / `DisruptionStep`) YAML files.
3. Real-time IDE auto-completion, hover docs, domain-specific syntax highlighting, and instant unknown-attribute / cross-field diagnostics.

### Dual-Layer Architecture (JSON Schema + `kates lsp` Native Language Server)
Pure JSON Schema (`additionalProperties: false`) catches unknown keys and basic enum/range errors, but **cannot** express Kates' cross-field rules (e.g., `minInsyncReplicas <= replicationFactor`, `enableTransactions: true` requiring `acks: all` and `backend: native`, `rampSteps > 1` only on `RAMP` phases, `FaultLimits` requiring `chaosDurationSec >= 1` on `NETWORK_PARTITION`, or the 22 compliance cross-rule incoherence checks) nor YAML AST pitfalls (duplicate keys, `yes`/`on` YAML 1.1 booleans, unquoted `"4.10"` versions turning into `4.1`).

The plugin therefore pairs **Layer 1: Bundled Draft 2020-12 JSON Schemas** (for zero-config structural completion and schema validation in both VS Code and Visual Studio) with **Layer 2: `kates lsp`** (a lightweight stdio Language Server built directly into the Go CLI binary that runs all AST, Levenshtein unknown-field hinting, semantic cross-field checks, semantic tokens, and canonical formatting in memory).

---

## 2. Complete Specification Audit Across All 6 YAML Dialects

Every attribute, type, default, numeric bound, and cross-field rule below was verified against the Java backend (`kates/src/main/java/com/bmscomp/kates/`) and Go CLI (`cli/`).

### 2.1 Dialect 1: Standard & Performance CLI Scenario Files (`kates test apply` / `kates test lint`)
- **Source of Truth**: `cli/cmd/apply.go:21-65`, `cli/cmd/scenario_lint.go:35-450`, `docs/book/13-scenario-files.md`, `engine/SpecLimits.java:43-115`, `engine/TestTypeDefaults.java:22-120`.
- **Document Shapes**:
  1. **Multi-scenario file**: Top-level `scenarios:` sequence of `TestScenario` maps.
  2. **Single-scenario shorthand**: Top-level `name`, `type`, `backend`, `spec`, `validate` (valid only when `scenarios` is omitted or empty).

| Path | Type | Allowed Values / Bounds | Default | Notes / Cross-Field Constraints |
| :--- | :--- | :--- | :--- | :--- |
| `scenarios` | `seq<TestScenario>` | Non-empty list | — | If non-empty, any sibling top-level key (`name`, `type`, `spec`, etc.) is flagged as `unknown-field` (`"ignored when 'scenarios' is non-empty"`). |
| `name` | `string` | `1..128` chars, no `\0` | `""` | Scenario identifier. |
| `type` | `enum` (required) | `LOAD`, `STRESS`, `SPIKE`, `ENDURANCE`, `VOLUME`, `CAPACITY`, `ROUND_TRIP`, `INTEGRITY`, `TUNE_REPLICATION`, `TUNE_ACKS`, `TUNE_BATCHING`, `TUNE_COMPRESSION`, `TUNE_PARTITIONS`, `INTEGRATION_CDC` | — | Matched case-insensitively by CLI (`strings.ToUpper`), canonical uppercase in completions. |
| `backend` | `enum` | `native`, `trogdor` | `"native"` | Cannot be `"trogdor"` when `spec.enableTransactions: true`. |
| `spec.records` | `int64` | `1..100,000,000` | Type-dependent (`10,000`–`500,000`) | `records * parallelProducers` must not overflow `int64`. Using `numRecords` triggers hint: `use 'records'`. |
| `spec.parallelProducers` | `int` | `1..64` | Type-dependent (`1`, `2`, `4`, `8`) | Using `numProducers` triggers hint: `use 'parallelProducers'`. |
| `spec.recordSizeBytes` | `int` | `1..10,485,760` (10 MiB) | `512` (`VOLUME`: `4096`) | Using `recordSize` triggers hint: `use 'recordSizeBytes'`. |
| `spec.durationSeconds` | `int` | `0` or `1..86,400` (24h) | `0` (`ENDURANCE`: `300`) | Converted to `durationMs = durationSeconds * 1000`. Using `durationMs` or `duration` triggers hint: `use 'durationSeconds'`. |
| `spec.targetThroughput` | `int` | `-1` (unthrottled) or `>= 1` | `-1` (`LOAD`/`ENDURANCE`/`INTEGRITY`: `1000`) | Refused (`!= -1`) on `SPIKE`, `CAPACITY`, `INTEGRATION_CDC`. Using `throughput` triggers hint: `use 'targetThroughput'`. |
| `spec.topic` | `string` | `[a-zA-Z0-9._-]{1,249}` (not `.` or `..`) | Auto-generated | Custom Kafka topic name. |
| `spec.acks` | `string` / `int` | `"0"`, `"1"`, `"all"`, `"-1"` | `"all"` (`SPIKE`: `"1"`) | Unquoted YAML integers `0`, `1`, `-1` are accepted and coerced to strings. |
| `spec.batchSize` | `int` | `1..16,777,216` (16 MiB) | `16384` | Producer batch size in bytes. |
| `spec.lingerMs` | `int` | `0..60,000` | `5` | Producer linger time in ms. |
| `spec.compressionType` | `enum` | `none`, `gzip`, `snappy`, `lz4`, `zstd` | `"none"` (`VOLUME`: `"lz4"`) | Lowercase enum. |
| `spec.numConsumers` | `int` | `1..64` | `1` (`LOAD`: `2`, `INTEGRITY`: `3`) | Number of consumer threads. |
| `spec.replicationFactor` | `int` | `1..10` | `1` (`INTEGRITY`/`TUNE_*`: `3`) | Must be `>= spec.minInsyncReplicas`. |
| `spec.partitions` | `int` | `1..1,000` | `6` (`STRESS`/`SPIKE`/`VOLUME`: `12`) | Topic partition count. |
| `spec.minInsyncReplicas` | `int` | `1..10` | `1` (`INTEGRITY`/`TUNE_*`: `2`) | Error if `> spec.replicationFactor` (or default RF `1` when `replicationFactor` is omitted on types that default RF to `1`!). |
| `spec.consumerGroup` | `string` | `1..255` printable ASCII (`[ -~]`) | Auto-generated | Allowed **only** on `LOAD`, `ENDURANCE`, `INTEGRITY`. |
| `spec.fetchMinBytes` | `int` | `1..16,777,216` | `1` | Allowed **only** on `LOAD`, `ENDURANCE`, `INTEGRITY`. |
| `spec.fetchMaxWaitMs` | `int` | `1..60,000` | `500` | Allowed **only** on `LOAD`, `ENDURANCE`, `INTEGRITY`. |
| `spec.enableIdempotence` | `bool` | `true`, `false` | `false` (`INTEGRITY`: `true`) | Refused on `INTEGRATION_CDC`. When `true`, requires effective `acks` to be `"all"` or `"-1"`. |
| `spec.enableTransactions` | `bool` | `true`, `false` | `false` | Refused on `INTEGRATION_CDC`. Requires `acks: all`/`-1`, `enableIdempotence` not `false`, and `backend: native`. |
| `spec.enableCrc` | `bool` | `true`, `false` | `false` (`INTEGRITY`: `true`) | `enableCrc: true` allowed **only** on `INTEGRITY`. |
| `validate.maxP99LatencyMs` | `float64` | `> 0` | — | Setting `0` emits warning `gate-off` (`"0 disables this gate; omit the field or set a positive limit"`). |
| `validate.maxAvgLatencyMs` | `float64` | `> 0` | — | Setting `0` emits warning `gate-off`. |
| `validate.minThroughputRecPerSec` | `float64` | `> 0` | — | Setting `0` emits warning `gate-off`. |
| `validate.maxErrorRate` | `float64` | `0.0..1.0` | — | Emits warning `gate-not-checked` (`"kates test apply --wait does not check maxErrorRate; use maxDataLossPercent"`). |
| `validate.maxDataLossPercent` | `float64` | `0.0..100.0` | — | `0` is valid and enforces zero data loss. |
| `validate.maxRtoMs` | `float64` | `> 0` | — | Setting `0` emits warning `gate-off`. |
| `validate.maxRpoMs` | `float64` | `> 0` | — | Setting `0` emits warning `gate-off`. |
| `validate.maxOutOfOrder` | `int64` | `>= 0` | — | `0` is valid (zero out-of-order messages allowed). |
| `validate.maxCrcFailures` | `int64` | `>= 0` | — | `0` is valid (zero CRC mismatches allowed). |
| `validate.perTask` | `bool` | `true`, `false` | `false` | Evaluates throughput gate against each producer/consumer task row instead of aggregate. |

---

### 2.2 Dialect 2: Advanced Multi-Phase Scenarios & API Requests (`CreateTestRequest` / `TestScenario`)
- **Source of Truth**: `domain/CreateTestRequest.java:16-70`, `domain/TestScenario.java:16-157`, `domain/ScenarioPhase.java:13-115`, `domain/SlaDefinition.java:11-108`, `domain/TestSpec.java:19-413`, `engine/TestOrchestrator.java:1139-1372`.
- **Where Used**: Standalone multi-phase scenario definitions, `POST /api/tests` request bodies, and nested inside `ResilienceTestRequest.testRequest`.

| Path | Type | Allowed Values / Bounds | Default | Notes / Cross-Field Constraints |
| :--- | :--- | :--- | :--- | :--- |
| `type` | `enum` (`TestType`) | 14 `TestType` values | `LOAD` | Required unless `scenario.type` is set. |
| `backend` | `enum` | `native`, `trogdor` | `"native"` | Overridden by `scenario.backend` if `scenario` is present. |
| `spec` | `TestSpec` (wire) | Wire field names: `numRecords` (`1..100,000,000`), `recordSize` (`1..10,485,760`), `numProducers` (`1..64`), `numConsumers` (`1..64`), `durationMs` (`0` or `1,000..86,400,000`), `throughput` / `targetThroughput` (`-1` or `>= 1`), plus common `TestSpec` keys | `TestSpec.defaults()` | Using CLI scenario names (`records`, `parallelProducers`, `recordSizeBytes`, `durationSeconds`) here is flagged as `unknown-field` with wire-name replacement hints! |
| `scenario.name` | `string` | `1..128` chars, no `\0` | — | Required when `scenario` is used. |
| `scenario.description` | `string` | Free text | `""` | Human description. |
| `scenario.type` | `enum` (`TestType`) | 14 `TestType` values | `LOAD` | Effective test type for the multi-phase run. |
| `scenario.backend` | `enum` | `native`, `trogdor` | `"native"` | Effective backend for the scenario. |
| `scenario.baseSpec` | `TestSpec` (wire) | Same wire fields as `spec` | `TestSpec.defaults()` | When `phases` is non-empty, `consumerGroup`, `fetchMinBytes`, `fetchMaxWaitMs`, and `enableCrc: true` are **refused** (`scenarioInapplicableFields`) because multi-phase runs start no consumers and check no CRCs. |
| `scenario.phases` | `seq<ScenarioPhase>` | Ordered phase list | `[]` | Each phase merges onto `baseSpec` via `TestScenario.resolveSpecForPhase(i)`. |
| `scenario.phases[].name` | `string` | `1..100` chars, no `\0` | `""` | Phase display name (e.g., `"Warm-up"`, `"Step Ramp"`, `"Peak Spike"`). |
| `scenario.phases[].phaseType` | `enum` (required) | `WARMUP`, `RAMP`, `STEADY`, `SPIKE`, `COOLDOWN` | — | Controls phase orchestration behavior. |
| `scenario.phases[].durationMs` | `int64` | `0` or `1,000..86,400,000` | `30,000` | Values `1..999` or `< 0` are refused by `SpecLimits`. |
| `scenario.phases[].targetThroughput` | `int` | `-1` (unlimited) or `>= 1` | `-1` | Overrides `baseSpec` throughput for this phase. |
| `scenario.phases[].rampSteps` | `int` | `1..100` | `1` | **Cross-field rule**: `rampSteps != 1` is **refused** unless `phaseType == RAMP`. When `phaseType == RAMP` and `rampSteps > 1`, resolved phase throughput (if `> 0`) must be `>= rampSteps`. |
| `scenario.phases[].spec` | `TestSpec` (wire) | Per-phase `TestSpec` overrides | `null` | Same multi-phase restrictions as `baseSpec`. |
| `scenario.sla` | `SlaDefinition` | `maxP99LatencyMs` (`> 0`), `maxP999LatencyMs` (`> 0`), `maxAvgLatencyMs` (`> 0`), `minThroughputRecPerSec` (`> 0`), `maxErrorRate` (`0.0..1.0`), `minRecordsProcessed` (`>= 1`), `maxDataLossPercent` (`0.0..100.0`), `maxRtoMs` (`> 0`), `maxRpoMs` (`> 0`) | `SlaDefinition.defaults()` | Graded by `SlaGrader`. |
| `scenario.labels` | `map<string,string>` | Key/value metadata tags | `{}` | Arbitrary string labels. |

---

### 2.3 Dialect 3: Declarative Flow Pipelines (`kates flow run` / `kates flow lint`)
- **Source of Truth**: `cli/cmd/flow.go:27-115`, `cli/cmd/test_lint.go:112-253`.
- **Special Syntax**: Supports `$VAR` and `${VAR}` environment variable interpolation (`os.ExpandEnv`). The plugin must recognize `$VAR` / `${VAR}` tokens inside scalar values without false-positive type errors when env vars represent numbers or strings.

| Path | Type | Allowed Values / Bounds | Default | Notes / Cross-Field Constraints |
| :--- | :--- | :--- | :--- | :--- |
| `name` | `string` | Non-empty string | — | Pipeline name. |
| `steps` | `seq<FlowStep>` | Non-empty sequence (`empty-flow` error if empty) | — | Executed sequentially unless `onFail: stop` halts execution. |
| `steps[].name` | `string` | Step label | `step-N` | Displayed in step progress and JUnit reports. |
| `steps[].action` | `enum` (required) | `test`, `wait` (`expect`, `webhook`, `notify` recognized as `not-implemented` error) | — | Missing `action` emits `missing-field`. Unknown values emit `invalid-enum`. |
| `steps[].type` | `enum` (`TestType`) | 14 `TestType` values | `"LOAD"` | Used when `action: test`. Emits `unused-field` warning when `action: wait`. |
| `steps[].scenario` | `string` | Scenario preset or `"wait:10s"` (Go `time.ParseDuration`) | `""` | When `action: wait` and `scenario` starts with `wait:`, parses duration after `wait:` (`invalid-value` error if invalid Go duration; defaults to `5s` if omitted). |
| `steps[].records` | `int64` | `1..100,000,000` | `0` | Shorthand for `steps[].spec.numRecords` when `spec.numRecords` is unset. |
| `steps[].spec` | `TestSpec` (wire) | Wire field names (`numRecords`, `numProducers`, `recordSize`, `durationMs`, etc.) | — | **Critical distinction**: `FlowStep.Spec` decodes into `client.TestSpec` (wire names), unlike `ScenarioFile`! Using `records`, `parallelProducers`, `recordSizeBytes`, `durationSeconds`, or `throughput` inside `steps[].spec` triggers `flowSpecHints` (`"use 'numRecords'"`, etc.). Emits `unused-field` warning on `wait` steps. |
| `steps[].gate` | `FlowGate` | `minGrade` (`A`..`F`), `maxP99` (`> 0`), `minThroughput` (`> 0`), `maxErrorRate` (`0.0..1.0`) | — | Emits `unused-field` warning on `wait` steps. |
| `steps[].onFail` | `enum` | `continue`, `stop` | `"stop"` | Controls whether pipeline continues after a step failure. |
| `steps[].expect` | `FlowExpect` | `rto`, `rpo`, `dataLoss`, `slaGrade` | — | Currently emits `not-implemented` error (`"steps.expect is parsed but never evaluated; use steps.gate"`). |
| `steps[].playbook`, `config`, `url`, `template` | `string` / `map` | Reserved fields | — | Validated according to step action. |

---

### 2.4 Dialect 4: Chaos & Resilience Test Specifications (`kates resilience run`)
- **Source of Truth**: `cli/cmd/resilience.go:20-125`, `resilience/ResilienceTestRequest.java:17-78`, `chaos/FaultSpec.java:17-239`, `chaos/FaultLimits.java:18-148`, `chaos/ProbeSpec.java:12-90`, `chaos/ProbeExecutor.java:51-158`, `chaos/KafkaProbeChecks.java:84-158`, `chaos/ParsedLabelSelector.java:25-90`.

| Path | Type | Allowed Values / Bounds | Default | Notes / Cross-Field Constraints |
| :--- | :--- | :--- | :--- | :--- |
| `testRequest` | `CreateTestRequest` (required) | Full Dialect 2 `CreateTestRequest` | — | Must specify `testRequest.type` or `testRequest.scenario.type`. |
| `chaosSpec` | `FaultSpec` (required) | See `FaultSpec` fields below | — | Must specify `chaosSpec.experimentName`. |
| `steadyStateSec` | `int` | `>= 1` | `30` | Baseline duration before fault injection. |
| `maxRecoveryWaitSec` | `int` | `>= 1` | `120` | Max wait for cluster recovery & post-chaos probes. |
| `probes` | `seq<ProbeSpec>` | Pre/post resilience probes | `[]` | Unlike `chaosSpec.probes`, top-level `probes` **may** use `cmdProbe`. |
| `chaosSpec.experimentName` | `string` (required) | Non-empty string (e.g., `"kafka-pod-delete"`, `"pod-network-loss"`) | — | Litmus/chaos experiment identifier. |
| `chaosSpec.disruptionType` | `enum` (`DisruptionType`) | `POD_KILL`, `POD_DELETE`, `NETWORK_PARTITION`, `NETWORK_LATENCY`, `CPU_STRESS`, `MEMORY_STRESS`, `IO_STRESS`, `DNS_ERROR`, `DISK_FILL`, `ROLLING_RESTART`, `LEADER_ELECTION`, `SCALE_DOWN`, `NODE_DRAIN` | `POD_DELETE` | Determines fault semantics and `FaultLimits` rules. |
| `chaosSpec.targetNamespace` | `string` | Valid K8s namespace | `"kafka"` | Must match Kafka namespace or Kates namespace. |
| `chaosSpec.targetLabel` | `string` | Valid K8s selector (`ParsedLabelSelector`) | `"strimzi.io/component-type=kafka"` | Supports `key=val`, `key==val`, `key!=val`, `key in (a,b)`, `key notin (a,b)`, `key`, `!key`. Empty/blank string or `<`/`>` operators are errors! |
| `chaosSpec.targetPod` | `string` | Pod name | `null` | Warns on `SCALE_DOWN` if `targetPod` is not the highest-numbered broker in the pool/StatefulSet. |
| `chaosSpec.targetAll` | `bool` | `true`, `false` | `false` | Targets all matching pods instead of 1. |
| `chaosSpec.chaosDurationSec` | `int` | `0..3,600` (or `1..3,600`) | `30` | **Cross-field error** (`FaultLimits.UNDONE_WHEN_DURATION_ENDS`): must be `>= 1` when `disruptionType` is `NETWORK_PARTITION`, `NETWORK_LATENCY`, `CPU_STRESS`, `MEMORY_STRESS`, `IO_STRESS`, `DNS_ERROR`, `DISK_FILL`, or `NODE_DRAIN` (or when `disruptionType` is omitted and `experimentName: node-drain`). Warns if `0` on `ROLLING_RESTART` or `SCALE_DOWN`. |
| `chaosSpec.delayBeforeSec` | `int` | `0..600` | `0` | Delay before injecting fault. |
| `chaosSpec.envOverrides` | `map<string,string>` | Only keys `TARGET_NODE`, `NODE_LABEL` | `{}` | Any key other than `TARGET_NODE` or `NODE_LABEL` is rejected by `FaultLimits`! `NODE_LABEL` value is validated via `ParsedLabelSelector.parse`. Warns if `TARGET_NODE` or `NODE_LABEL` is set to `""` (empty string). |
| `chaosSpec.targetBrokerId` | `int` | `-1` (any) or `>= 0` | `-1` | Targets a specific broker ID. |
| `chaosSpec.networkLatencyMs` | `int` | `1..30,000` | `100` | Used by `NETWORK_LATENCY`. |
| `chaosSpec.fillPercentage` | `int` | `1..100` | `80` | Used by `DISK_FILL`. |
| `chaosSpec.cpuCores` | `int` | `1..64` | `2` | Used by `CPU_STRESS`. |
| `chaosSpec.memoryMb` | `int` | `1..32,768` | `512` | Used by `MEMORY_STRESS`. |
| `chaosSpec.ioWorkers` | `int` | `1..64` | `4` | Used by `IO_STRESS`. |
| `chaosSpec.gracePeriodSec` | `int` | `0..300` | `0` | Used by `POD_DELETE` / `POD_KILL`. |
| `chaosSpec.targetTopic` | `string` | `[a-zA-Z0-9._-]{1,249}` (not `.` or `..`) | `null` | Enables leader-aware targeting (`KafkaIntelligenceService`). |
| `chaosSpec.targetPartition` | `int` | `>= 0` | `0` | Partition whose leader broker is targeted when `targetTopic` is set. |
| `chaosSpec.probes` | `seq<ProbeSpec>` | Litmus/fault-scoped probes | `[]` | **`cmdProbe` is refused inside `FaultSpec.probes`** (`FaultLimits.violations`); use `kafkaProbe` or `k8sProbe`, or move `cmdProbe` to top-level `ResilienceTestRequest.probes`. |
| `probes[].name` | `string` (required) | Non-empty identifier | — | Probe display name. |
| `probes[].type` | `enum` | `kafkaProbe`, `k8sProbe`, `cmdProbe` | `"cmdProbe"` | Any other string is flagged (backend treats unknown types as `cmdProbe`, which fails inside `FaultSpec.probes`). |
| `probes[].mode` | `enum` | `SOT`, `EOT`, `Continuous`, `Edge`, `OnChaos` | `"Continuous"` | Litmus probe execution mode. |
| `probes[].command` | `string` (required) | Depends on `type` | — | **When `type: kafkaProbe`**, validated by `KafkaProbeChecks.problem`: must be `"under-replicated-partitions"`, `"unavailable-partitions"`, `"produce <topic>"`, or `"consumer-lag [<group>]"`. **When `type: k8sProbe`**, `"kafka Ready"` checks the Strimzi `Kafka` CR `Ready=True` condition. |
| `probes[].comparator` | `enum` | Text: `equal`, `contains`, `notContains`<br>Numeric: `==`, `!=`, `>=`, `<=`, `>`, `<` | `"contains"` | Verified against `ProbeExecutor.COMPARATORS`. |
| `probes[].expectedOutput` | `string` (required) | Non-null string; if `comparator` is numeric (`==`, `!=`, `>=`, `<=`, `>`, `<`), must match `[-+]?(\d+(\.\d*)?|\.\d+)([eE][-+]?\d+)?` | — | Missing `expectedOutput` or non-numeric `expectedOutput` with a numeric comparator is an error (`ProbeExecutor.problem`). |
| `probes[].intervalSec` | `int` | `>= 1` | `5` | Probe polling interval in seconds. |
| `probes[].timeoutSec` | `int` | `>= 1` | `10` | Probe execution timeout in seconds. |

---

### 2.5 Dialect 5: Disruption Plans & Playbooks (`kates disruption run`)
- **Source of Truth**: `disruption/DisruptionPlan.java:16-248`, `disruption/DisruptionPlaybookCatalog.java:27-157`, `disruption/DisruptionSafetyGuard.java:121-202`, `kates/src/main/resources/playbooks/*.yaml`.

| Path | Type | Allowed Values / Bounds | Default | Notes / Cross-Field Constraints |
| :--- | :--- | :--- | :--- | :--- |
| `name` | `string` (required) | Non-empty plan/playbook name | — | E.g., `"az-failure"`, `"broker-kill"`. |
| `description` | `string` | Human description | `""` | Shown in `kates disruption playbooks`. |
| `category` | `enum` | `infrastructure`, `network`, `compute`, `storage`, `operations` | `"general"` | Used in playbook catalog files (`playbooks/*.yaml`). |
| `steps` | `seq<DisruptionStep>` (required) | Non-empty list | — | Sequential fault injection steps. |
| `steps[].name` | `string` (required) | Step identifier | — | E.g., `"drain-zone-a"`. |
| `steps[].faultSpec` | `FaultSpec` (required) | Full `FaultSpec` (see Dialect 4) | — | Validated against all `FaultLimits` and `ParsedLabelSelector` rules. |
| `steps[].steadyStateSec` | `int` | `>= 0` | `30` | Pre-fault steady state duration for this step. |
| `steps[].observationWindowSec` | `int` | `>= 1` | `60` | Post-fault observation window for ISR/lag recovery. |
| `steps[].requireRecovery` | `bool` | `true`, `false` | `true` | Whether ISR and consumer lag must recover before proceeding to the next step. |
| `sla` | `SlaDefinition` | Same as Dialect 2 `SlaDefinition` | `SlaDefinition.defaults()` | Warns if constraints are unevaluable (`SlaGrader.unevaluableConstraints`). |
| `testType` | `enum` (`TestType`) | 14 `TestType` values | `LOAD` | Background traffic workload type during disruption. |
| `baselineDurationSec` | `int` | `>= 1` | `60` | Initial baseline traffic duration before Step 1. |
| `isrTrackingTopic` | `string` | Valid Kafka topic name | `null` | Topic monitored for ISR shrinkage and recovery (RTO). |
| `lagTrackingGroupId` | `string` | Consumer group ID | `null` | Consumer group monitored for lag recovery. |
| `isrPollIntervalMs` | `int` | `>= 100` | `2000` | Polling interval for ISR state. |
| `lagPollIntervalMs` | `int` | `>= 100` | `2000` | Polling interval for consumer group lag. |
| `maxAffectedBrokers` | `int` | `-1` (unlimited) or `>= 1` | `-1` | Blast-radius safety cap enforced by `DisruptionSafetyGuard`. |
| `autoRollback` | `bool` | `true`, `false` | `true` | Emits warning if `false` when any step uses `disruptionType: SCALE_DOWN`. |

---

### 2.6 Dialect 6: Compliance Reference Architecture (`KafkaReference`, `conformity.kates.io/v1alpha1`)
- **Source of Truth**: `cli/examples/conformity/kafkareference.v1alpha1.schema.json:1-407`, `cli/internal/compliance/compile.go:35-210`, `cli/internal/compliance/schema.go:1-450`, `cli/internal/compliance/rules_*.go`, `docs/book/appendix-compliance-language.md`.
- **Structure**:
  - `apiVersion: conformity.kates.io/v1alpha1` (required)
  - `kind: KafkaReference` (required)
  - `metadata`: `name` (required DNS label `^[a-z0-9]([-a-z0-9]{0,61}[a-z0-9])?$`), `description`
  - `spec`: `target` (`platform: strimzi`, `namespace`, `name`, `clusterId`), `zoneLabel`, `versions` (`kubernetes`, `strimzi`, `kafka`), `controllers` / `brokers` (`count`, `dedicated` [controllers only], `requests`, `limits`, `storage`, `zones`, `skew`, `perNode`, `antiAffinity`), `kafka` (`source: cr | brokers`, `rackAwareness`, `authorization`, `listeners: {require, every}`, `config`), `topics` (`replicationFactor`, `minInsyncReplicas`, `exclude`), `monitoring` (`metrics`, `kafkaExporter`, `podMonitor`), `waivers` (`[{rule, reason, by, until}]`).
- **Compiler Phases Reused by Plugin**:
  - **23 Structural & Language Codes**: `yaml-syntax`, `empty-document`, `multi-document`, `duplicate-key`, `unknown-field` (with Levenshtein `<= 2` hint), `missing-field`, `invalid-type`, `invalid-enum`, `invalid-pattern`, `invalid-constraint`, `inverted-bounds`, `invalid-quantity`, `invalid-version`, `unquoted-version`, `invalid-date`, `expired-waiver`, `long-waiver` (`> 365` days), `orphan-waiver`, `duplicate-waiver`, `empty-spec`, `empty-listener`, `duplicate-listener`, `redundant-bound` (`min == max` fixable to `equals`), `singular-one-of` (fixable to `equals`).
  - **22 Cross-Rule Incoherence Codes** (with two-pass `rule-never-passes` evaluation when one side is `warn` or waived):
    - *Resources (`rules_resources.go`)*: `requests-exceed-limits`, `heap-exceeds-memory`, `ephemeral-with-size`, `ephemeral-with-class`.
    - *Placement (`rules_placement.go`)*: `zones-exceed-count`, `rack-without-zones`, `zones-without-rack`, `single-zone-spread`, `impossible-skew`, `anti-affinity-exceeds-nodes`.
    - *Durability (`rules_durability.go`)*: `rf-exceeds-brokers`, `isr-exceeds-rf`, `topic-rf-exceeds-brokers`, `topic-isr-exceeds-rf`, `unclean-leader-election`, `controller-quorum-even`, `single-controller`, `plaintext-external-listener`.
    - *Language & Waivers (`rules_language.go`, `rules_waivers.go`)*: `broker-config-require-brokers-source`, `duplicate-topic-exclude`, `broad-topic-exclude`, `rule-never-passes`.

---

## 3. File Detection & Automatic Schema Routing

Because only `KafkaReference` files carry `apiVersion` and `kind`, the plugin implements a **3-tier Document Dialect Router** that runs on document open and on top-level key changes:

| Priority | Detection Mechanism | Routing Rule |
| :--- | :--- | :--- |
| **1. Modeline Header** | `# yaml-language-server: $schema=...` or `# kates-schema: <dialect>` on line 1–3 | Explicit override always wins; provides a Quick Fix to insert/update the header. |
| **2. Content-Based AST Fingerprinting** | Fast top-level YAML mapping key inspection (works on untitled/unsaved buffers and any filename) | - `apiVersion: conformity.kates.io/v1alpha1` or `kind: KafkaReference` → **Dialect 6 (Compliance)**<br>- `testRequest` + `chaosSpec` → **Dialect 4 (Resilience)**<br>- `steps` where `steps[0]` has `faultSpec` (or top-level `maxAffectedBrokers` / `isrTrackingTopic` / `autoRollback`) → **Dialect 5 (Disruption Plan / Playbook)**<br>- `steps` where `steps[0]` has `action` → **Dialect 3 (Flow Pipeline)**<br>- `scenarios` list, or top-level `validate`, or top-level `spec` containing scenario-only keys (`records`, `parallelProducers`, `recordSizeBytes`, `durationSeconds`) → **Dialect 1 (CLI Scenario File)**<br>- Top-level `scenario` (with `phases` / `baseSpec` / `sla`) or top-level `spec` with wire keys (`numRecords`, `numProducers`, `recordSize`, `durationMs`) → **Dialect 2 (Multi-Phase / API Test Request)** |
| **3. Path & Filename Globs** | Fallback when a new YAML file is empty or has only comments | - `**/conformity/*.yaml`, `**/*.compliance.yaml`, `**/kafka-reference*.yaml` → **Dialect 6**<br>- `**/playbooks/*.yaml`, `**/*disruption*.yaml` → **Dialect 5**<br>- `**/*resilience*.yaml`, `**/*chaos*.yaml` → **Dialect 4**<br>- `**/*flow*.yaml`, `**/*pipeline*.yaml` → **Dialect 3**<br>- `**/scenarios/*.yaml`, `**/*.scenario.yaml`, `**/perf-*.yaml`, `**/load-*.yaml` → **Dialect 1** |

---

## 4. Error Detection & Diagnostics Engine

Error detection is designed so **no non-existing attribute, wrong type, or invalid cross-field combination can slip through**.

### 4.1 Unknown / Non-Existing Attribute Detection (`unknown-field`)
1. **Strict Closed Mapping Tables at Every Nesting Level**:
   - Every YAML mapping object across all 6 dialects has `additionalProperties: false` in its JSON Schema AND an explicit allowed-key set in the Go linter.
   - Any key not in the allowed set for that exact YAML path is immediately underlined with a **Red Error squiggle** (`Severity: Error`, `Code: unknown-field`) covering the key token range `(node.Line, node.Column)` to `node.Column + len(node.Value)`.
2. **Dialect-Aware Translation Hints (Wire vs. Scenario Field Names)**:
   - Because Kates uses human-friendly names in `ScenarioFile.spec` (`records`, `parallelProducers`, `recordSizeBytes`, `durationSeconds`, `targetThroughput`) and API wire names in `FlowStep.spec` / `CreateTestRequest.spec` / `TestScenario.baseSpec` (`numRecords`, `numProducers`, `recordSize`, `durationMs`, `throughput`), mixing them up is the #1 authoring mistake.
   - The plugin detects cross-dialect field names and emits targeted messages + **1-click Quick Fix (`CodeAction`) replacements**:
     - In Dialect 1 (`ScenarioFile.spec`): `numRecords` → *"unknown field 'numRecords'; scenario files use 'records' (`numRecords` is the API wire name)"* [Quick Fix: **Rename to `records`**].
     - In Dialect 2/3/4 (`CreateTestRequest.spec`, `baseSpec`, `phases[].spec`, `FlowStep.spec`): `records` → *"unknown field 'records'; API/flow specs use 'numRecords'"* [Quick Fix: **Rename to `numRecords`**].
     - In `validate` vs `sla`: using `maxP999LatencyMs` or `minRecordsProcessed` inside Dialect 1 `validate` hints that those fields belong to `scenario.sla` (`SlaDefinition`); using `maxOutOfOrder`, `maxCrcFailures`, or `perTask` inside `sla` hints that those belong to CLI `validate` (`ValidationSpec`).
3. **Levenshtein Edit-Distance `<= 2` ("Did You Mean?") Suggestions**:
   - Reuses `compliance.Closest(name, allowed)` (`cli/internal/compliance/schema.go:411-449`) across **all 6 dialects**.
   - Example: writing `paritions: 12` produces:
     `unknown field 'paritions' in scenarios[0].spec (did you mean 'partitions'?) [unknown-field]` with a Quick Fix to replace `paritions` with `partitions`.
4. **Ignored Sibling Keys When `scenarios:` Is Non-Empty**:
   - In Dialect 1, if a file has both `scenarios: [...]` and a top-level `type: LOAD` or `spec: ...`, the top-level sibling keys are flagged as `unknown-field` (`"ignored when 'scenarios' is non-empty"`).

### 4.2 YAML Type, Coercion & Syntax Traps
Standard YAML parsers silently coerce values in ways that break Kates specs. The AST walker inspects `yaml.Node.Tag` (`!!str`, `!!int`, `!!float`, `!!bool`, `!!null`, `!!map`, `!!seq`) and `yaml.Node.Style`:
- **Duplicate Keys (`duplicate-key`)**: Flagged at every mapping level before decoding.
- **Quoted Numbers & Booleans (`invalid-type`)**: `"10000"` (`!!str`) where an `int` is expected (`records: "10000"`), or `"true"` where a `bool` is expected, is rejected (except `spec.acks` where both `1` and `"1"` are explicitly normalized).
- **Whole Floats for Integers (`invalid-type`)**: `100.0` or `1e5` (`!!float`) on an `int`/`int64` field (`records: 1e5`, `partitions: 6.0`) is flagged as an error.
- **Unquoted Version Strings (`unquoted-version`)**: In `KafkaReference.spec.versions.*`, writing `kafka: 4.10` (`!!float` → `4.1`) is flagged as Fixable Error `unquoted-version` (`"version must be a quoted string (unquoted 4.10 is read as 4.1)"`) with Quick Fix to wrap in quotes.
- **YAML 1.1 Boolean Traps (`invalid-type`)**: Unquoted `yes`, `no`, `on`, `off` on boolean or string fields are flagged with a clear hint to use `true`/`false` or quote the string.
- **Decimal Byte Suffix Traps (`invalid-quantity`)**: In compliance quantities (`requests.memory`, `storage.size`), `8GB` or `500MB` is flagged with hint `"use binary 'Gi'/'Mi' or decimal 'G'/'M', not 'GB'/'MB'"`.

### 4.3 Domain & Cross-Field Semantic Diagnostics
In addition to existing checks in `kates test lint`, `kates flow lint`, and `kates compliance lint`, we add two new offline linters in the Go CLI (`cli/cmd/resilience_lint.go` & `cli/cmd/disruption_lint.go`) and expose all five through `kates lsp`:

| Dialect | New / Enhanced Offline Semantic Checks | Diagnostic Code | Severity |
| :--- | :--- | :--- | :--- |
| **Dialect 1 & 2 (Scenarios & Multi-Phase)** | Effective `minInsyncReplicas > effective replicationFactor` (accounting for `TestTypeDefaults` when one side is omitted!) | `isr-exceeds-rf` | Error |
| **Dialect 1 & 2** | `enableIdempotence: true` or `enableTransactions: true` when effective `acks` is not `"all"` / `"-1"` (crucial on `SPIKE`, which defaults to `acks: "1"`!) | `idempotence-requires-acks-all` | Error |
| **Dialect 1 & 2** | `enableTransactions: true` combined with `enableIdempotence: false` or `backend: trogdor` | `invalid-transactions-config` | Error |
| **Dialect 1 & 2** | Inapplicable fields per `TestType`: rate on `SPIKE`/`CAPACITY`/`INTEGRATION_CDC`; consumer settings (`consumerGroup`, `fetchMinBytes`, `fetchMaxWaitMs`) on types other than `LOAD`/`ENDURANCE`/`INTEGRITY`; `enableCrc: true` on types other than `INTEGRITY` | `inapplicable-field` | Error |
| **Dialect 2 (Multi-Phase)** | `consumerGroup`, `fetchMinBytes`, `fetchMaxWaitMs`, or `enableCrc: true` in `baseSpec` or `phases[].spec` when `phases` is non-empty | `scenario-inapplicable-field` | Error |
| **Dialect 2 (Multi-Phase)** | `phases[].rampSteps != 1` when `phaseType != RAMP`, or resolved `targetThroughput < rampSteps` | `invalid-ramp-steps` | Error |
| **Dialect 4 & 5 (Resilience & Disruption)** | `FaultLimits` range checks: `chaosDurationSec` (`0..3600`), `delayBeforeSec` (`0..600`), `gracePeriodSec` (`0..300`), `networkLatencyMs` (`1..30000`), `fillPercentage` (`1..100`), `cpuCores` (`1..64`), `memoryMb` (`1..32768`), `ioWorkers` (`1..64`) | `out-of-range` | Error |
| **Dialect 4 & 5** | `chaosDurationSec < 1` on fault types that require a duration (`NETWORK_PARTITION`, `NETWORK_LATENCY`, `CPU_STRESS`, `MEMORY_STRESS`, `IO_STRESS`, `DNS_ERROR`, `DISK_FILL`, `NODE_DRAIN`) | `fault-requires-duration` | Error |
| **Dialect 4 & 5** | `chaosDurationSec: 0` on `ROLLING_RESTART` or `SCALE_DOWN` (observation window overlaps operator roll) | `zero-chaos-duration` | Warning |
| **Dialect 4 & 5** | `envOverrides` keys other than `TARGET_NODE` or `NODE_LABEL` | `invalid-env-override` | Error |
| **Dialect 4 & 5** | `targetLabel` or `envOverrides.NODE_LABEL` fails `ParsedLabelSelector` grammar (empty string, invalid DNS prefix/name, `<` or `>`, unclosed `in (...)`) | `invalid-label-selector` | Error |
| **Dialect 4 & 5** | `cmdProbe` inside `FaultSpec.probes` (only allowed in top-level `ResilienceTestRequest.probes`) | `cmdprobe-in-faultspec` | Error |
| **Dialect 4 & 5** | `ProbeSpec`: unknown `comparator` (not in `equal`, `contains`, `notContains`, `==`, `!=`, `>=`, `<=`, `>`, `<`), missing `expectedOutput`, or non-numeric `expectedOutput` with numeric comparator | `invalid-probe` | Error |
| **Dialect 4 & 5** | `ProbeSpec` with `type: kafkaProbe` where `command` is not one of `under-replicated-partitions`, `unavailable-partitions`, `produce <topic>`, `consumer-lag [<group>]` | `invalid-kafka-probe-command` | Error |
| **Dialect 5 (Disruption)** | Step with `disruptionType: SCALE_DOWN` when plan sets `autoRollback: false` | `scaledown-without-rollback` | Warning |

---

## 5. Auto-Completion & IntelliSense

Auto-completion combines **JSON Schema completion** (for instant structural keys and enums) with **LSP Context-Aware Completion (`textDocument/completion`)**:

### 5.1 Key & Structural Completions
- Suggests only valid keys for the current cursor indentation and parent path, filtering out keys already present in the current YAML mapping.
- Automatically appends `: ` for scalar keys, `:\n  ` for nested object keys (`spec`, `validate`, `sla`, `chaosSpec`, `faultSpec`, `brokers`, `controllers`), and `:\n  - ` for sequence keys (`scenarios`, `phases`, `steps`, `probes`, `waivers`).
- Ranks required fields (`type`, `action`, `phaseType`, `experimentName`, `faultSpec`, `apiVersion`, `kind`) at the top of the completion list with `sortText: "0_"`.

### 5.2 Value & Enum Completions with Rich Documentation
- **`TestType` (14 values)**: Each completion item shows its default `records`, `parallelProducers`, `acks`, `replicationFactor`, `minInsyncReplicas`, and whether it starts consumers (sourced from `TestTypeDefaults.java`).
- **`DisruptionType` (13 values)**: Shows whether `chaosDurationSec >= 1` is mandatory, how pods are targeted, and how `DisruptionSafetyGuard` rolls back the fault.
- **`PhaseType` (5 values)**: `WARMUP`, `RAMP` (auto-inserts `rampSteps: 5`), `STEADY`, `SPIKE`, `COOLDOWN`.
- **`ProbeSpec` Completions**:
  - `type`: `kafkaProbe` (recommended, agentless over Kates AdminClient), `k8sProbe` (checks Strimzi `Kafka` CR `Ready=True`), `cmdProbe` (executes `sh -c` in broker pod; warns if inside `chaosSpec.probes`).
  - `command` when `type == kafkaProbe`: suggests `"under-replicated-partitions"`, `"unavailable-partitions"`, `"produce ${1:kates-test}"`, `"consumer-lag"`, `"consumer-lag ${1:kates-group}"`.
  - `command` when `type == k8sProbe`: suggests `"kafka Ready"` (with automatic `expectedOutput: "Ready=True"`, `comparator: "equal"`).
  - `comparator`: suggests `equal`, `contains`, `notContains`, `==`, `!=`, `>=`, `<=`, `>`, `<` with descriptions distinguishing text vs. numeric comparison.
- **`targetLabel` & `NODE_LABEL` Selector Completions**:
  - Suggests standard Strimzi & Kubernetes selectors:
    - `strimzi.io/component-type=kafka`
    - `strimzi.io/component-type=kafka,strimzi.io/broker-role=true`
    - `strimzi.io/component-type=kafka,strimzi.io/controller-role=true`
    - `strimzi.io/pool-name=${1:brokers}`
    - `topology.kubernetes.io/zone=${1:zone-a}`
- **Compliance (`KafkaReference`) Dynamic Completions**:
  - **`spec.waivers[].rule`**: Dynamically scans the current YAML document AST via `compliance.DeclaredRuleIDs` and suggests **only the exact rule IDs declared in that file** (e.g., `brokers.count`, `kafka.listeners.external`, `kafka.config.min.insync.replicas`), eliminating `orphan-waiver` errors!
  - **`spec.kafka.config.<key>`**: Suggests standard Kafka broker configuration keys (`min.insync.replicas`, `default.replication.factor`, `offsets.topic.replication.factor`, `transaction.state.log.replication.factor`, `transaction.state.log.min.isr`, `unclean.leader.election.enable`, `auto.create.topics.enable`, `log.retention.hours`, `message.max.bytes`, `num.network.threads`, `num.io.threads`, `compression.type`) and notes which keys require `spec.kafka.source: brokers`.
  - **Constraint Map Snippets**: On any compliance leaf rule, suggests both bare value and constraint maps (`{min: $1}`, `{max: $1}`, `{min: $1, max: $2}`, `{oneOf: [$1]}`, `{equals: $1, severity: warn}`).

### 5.3 Scaffolding Snippets
Built-in snippets (`snippets/kates-yaml.json`) matching `cli/cmd/scaffold.go` and `playbooks/*.yaml`:
- `kates-scenario-load`, `kates-scenario-stress`, `kates-scenario-spike`, `kates-scenario-integrity`, `kates-scenario-endurance`
- `kates-multiphase-scenario` (complete `WARMUP` → `RAMP` → `STEADY` → `SPIKE` → `COOLDOWN` envelope with `sla`)
- `kates-flow-pipeline` (multi-step `test` + `wait` + `gate` pipeline)
- `kates-resilience-test` (`testRequest` + `chaosSpec` + `kafkaProbe`s for `under-replicated-partitions` and `unavailable-partitions`)
- `kates-disruption-plan` (multi-step disruption plan with `isrTrackingTopic`, `lagTrackingGroupId`, `maxAffectedBrokers`, and `sla`)
- `kates-compliance-prod3az` and `kates-compliance-minimal` (`KafkaReference` templates)

---

## 6. Text Highlighting (TextMate Grammar + LSP Semantic Tokens)

Standard YAML syntax highlighting treats all keys and string/enum values identically. The plugin adds **two complementary highlighting layers**:

### 6.1 Layer 1: TextMate Injection Grammar (`syntaxes/kates-yaml.tmLanguage.json`)
Injected into `source.yaml` for instant, zero-latency coloring before the LSP even starts:

| Token Category | Pattern / Examples | TextMate Scope | Visual Treatment |
| :--- | :--- | :--- | :--- |
| **Envelope & Structural Headers** | `apiVersion`, `kind`, `metadata`, `scenarios`, `scenario`, `baseSpec`, `phases`, `steps`, `testRequest`, `chaosSpec`, `faultSpec`, `probes`, `validate`, `sla`, `gate`, `waivers` | `keyword.control.directive.kates.yaml` | Bold control keyword accent |
| **Test & Phase Types** | `LOAD`, `STRESS`, `SPIKE`, `ENDURANCE`, `VOLUME`, `CAPACITY`, `ROUND_TRIP`, `INTEGRITY`, `TUNE_*`, `INTEGRATION_CDC`, `WARMUP`, `RAMP`, `STEADY`, `COOLDOWN` | `support.constant.test-type.kates.yaml` | Distinct constant/enum color |
| **Chaos Disruption Types** | `POD_KILL`, `POD_DELETE`, `NETWORK_PARTITION`, `NETWORK_LATENCY`, `CPU_STRESS`, `MEMORY_STRESS`, `IO_STRESS`, `DNS_ERROR`, `DISK_FILL`, `ROLLING_RESTART`, `LEADER_ELECTION`, `SCALE_DOWN`, `NODE_DRAIN` | `entity.name.tag.disruption-type.kates.yaml` | High-visibility alert/chaos accent |
| **Compliance Constraint Operators** | `equals`, `min`, `max`, `oneOf`, `require`, `every` | `keyword.operator.constraint.kates.yaml` | Operator highlight |
| **Severities & Failure Policies** | `fail`, `stop` vs. `warn`, `continue` | `invalid.illegal.severity-fail.kates.yaml` / `markup.changed.severity-warn.kates.yaml` | Red/orange vs. yellow semantic tint |
| **Kubernetes Quantities & Durations** | `500m`, `4`, `8Gi`, `512Mi`, `1Ti`, `wait:10s` | `constant.numeric.quantity.kates.yaml` | Number + distinct unit suffix (`storage.modifier.unit.kates.yaml`) |
| **Environment Variable Interpolations** | `$VAR`, `${VAR_NAME}` in Flow files | `variable.other.env.kates.yaml` | Highlighted variable interpolation |
| **Kafka & Strimzi Identifiers** | `strimzi.io/...`, `topology.kubernetes.io/...`, `conformity.kates.io/v1alpha1`, `KafkaReference` | `support.type.platform.kates.yaml` | Type/interface color |

### 6.2 Layer 2: LSP Semantic Tokens (`textDocument/semanticTokens/full`)
Context-sensitive tokens emitted by `kates lsp` where regex alone is ambiguous:
- **Waived Rules**: In `KafkaReference`, any rule whose path is covered by an active (non-expired) entry in `spec.waivers` receives the `modification` / `deprecated` semantic modifier so operators can visually see at a glance which rules in `spec` are currently waived.
- **Rule ID References**: `spec.waivers[].rule` values (`brokers.count`, `kafka.listeners.external`) are highlighted as `property` + `declaration` links (supporting **Go to Definition** `textDocument/definition` to jump straight from the waiver to the rule line in `spec:`!).
- **Effective Defaults Inlay Hints (`textDocument/inlayHint`)**: Optional subtle inline hints showing effective `TestTypeDefaults` on a scenario (e.g., showing `acks: "1" (SPIKE default)` or `replicationFactor: 3 (INTEGRITY default)` when omitted).

---

## 7. Editor Experience: Hover, CodeActions, Formatting & CodeLens

1. **Rich Hover Documentation (`textDocument/hover`)**:
   - Hovering any field shows its description, allowed range (`SpecLimits` / `FaultLimits`), and **context-aware default** (e.g., hovering `spec` or `parallelProducers` inside a `type: STRESS` scenario displays `Default for STRESS: 8 (LOAD default: 2)`).
   - Hovering a `kafka.config.<key>` in a Compliance file shows whether it can be evaluated from the Strimzi `Kafka` CR (`source: cr`) or requires live Admin API inspection (`source: brokers`).
2. **Quick Fixes (`textDocument/codeAction`)**:
   - **Unknown attribute (`unknown-field`)**: *"Replace with '<closest>' (Did you mean?)"* or *"Replace with '<scenario/wire equivalent>'"*.
   - **Unquoted version (`unquoted-version`)**: *"Quote version string as '\"4.10\"'"*.
   - **Redundant bound (`redundant-bound`)**: *"Simplify `{min: X, max: X}` to `equals: X`"*.
   - **Singular `oneOf` (`singular-one-of`)**: *"Simplify `oneOf: [X]` to `equals: X`"*.
   - **Broker-only config (`broker-config-require-brokers-source`)**: *"Set `spec.kafka.source: brokers`"*.
   - **Missing duration on chaos fault (`fault-requires-duration`)**: *"Set `chaosDurationSec: 30`"*.
3. **Canonical Document Formatting (`textDocument/formatting`)**:
   - For `KafkaReference` files, invokes `compliance.Format` (`cli/internal/compliance/fmt.go`) directly in-memory to order top-level keys, `spec` sections, and constraint keys canonically while preserving comments.
4. **Inline CodeLens Actions (`textDocument/codeLens`)**:
   - Above each scenario in `scenarios:`: `▶ Run Scenario` (`kates test apply -f <file> --scenario <name> --wait`), `🔍 Lint`.
   - Above `DisruptionPlan` / `ResilienceTestRequest`: `🛡 Dry-Run Safety Check` (`kates disruption run --dry-run -f <file>`), `▶ Run Resilience Test`.
   - Above `KafkaReference`: `✓ Check Live Cluster Compliance` (`kates compliance check -f <file>`), `✨ Format Reference`.

---

## 8. Packaging for Both VS Code and Visual Studio 2022+

To support both **VS Code** (and Cursor / Antigravity / VSCodium) and **Visual Studio 2022+ (Windows)**:
1. **Shared Core Assets (`schemas/` + `kates lsp`)**:
   - All 6 JSON Schemas (Draft 2020-12 / Draft-07 compatible) live in `schemas/` in the repository and are embedded into the `kates` CLI (`//go:embed`) so `kates schema export` can also dump them.
   - All linting, completion, hover, semantic tokens, and formatting live in `cli/internal/lsp/` (`kates lsp --stdio`), reusing the exact Go packages (`cli/internal/compliance`, `cli/cmd/scenario_lint.go`, and the new `cli/internal/chaoslint`).
2. **VS Code Extension (`editors/vscode-kates/`)**:
   - TypeScript extension using `vscode-languageclient/node`.
   - Declares `contributes.yamlValidation` (auto-wiring all 6 JSON schemas into `redhat.vscode-yaml` if installed), `contributes.grammars` (TextMate injection), `contributes.snippets`, and launches `kates lsp --stdio`.
   - Falls back gracefully to bundled JSON Schemas + embedded JS schema validator if the `kates` CLI binary is not yet on `PATH`.
3. **Visual Studio 2022+ Extension (`editors/visualstudio-kates/`)**:
   - C# VSIX project implementing `ILanguageClient` (`Microsoft.VisualStudio.LanguageServer.Client`) targeting content type `yaml`, launching `kates lsp --stdio`, and bundling the same TextMate `.tmLanguage.json` grammar and `catalog.json` JSON Schema associations.

---

## 9. Phased Implementation Roadmap & Verification Plan

### Phase 1: JSON Schemas & Schema Description Enrichment (`schemas/`)
- [ ] **1.1 Enrich `kafkareference.v1alpha1.schema.json`**: Add `title`, `description`, `default`, and `examples` to all ~58 properties (currently only 4 properties have `description`, as noted in `plans/compliance-enhancements.md:179`).
- [ ] **1.2 Author 5 New Draft 2020-12 JSON Schemas**:
  - `schemas/scenario.v1.schema.json` (Dialect 1: `ScenarioFile`, `TestScenario`, scenario `spec`, `ValidationSpec`).
  - `schemas/test-request.v1.schema.json` (Dialect 2: `CreateTestRequest`, multi-phase `TestScenario`, `ScenarioPhase`, wire `TestSpec`, `SlaDefinition`).
  - `schemas/flow.v1.schema.json` (Dialect 3: `FlowPipeline`, `FlowStep`, `FlowGate`, wire `TestSpec`).
  - `schemas/resilience.v1.schema.json` (Dialect 4: `ResilienceTestRequest`, `FaultSpec`, `ProbeSpec`).
  - `schemas/disruption.v1.schema.json` (Dialect 5: `DisruptionPlan` & `PlaybookEntry`, `DisruptionStep`, `FaultSpec`, `SlaDefinition`).
- [ ] **1.3 Schema Validation Tests**: Add Go unit tests using `github.com/google/jsonschema-go` (already in `cli/go.mod:14`) verifying every example file in `cli/examples/`, `scenarios/`, and `kates/src/main/resources/playbooks/` validates cleanly against its respective schema.

### Phase 2: Offline Linters for Resilience, Disruption & Multi-Phase + Levenshtein Unification (`cli/`)
- [ ] **2.1 Unify Unknown-Field Levenshtein Hints**: Wire `compliance.Closest` (edit distance `<= 2`) into `scenario_lint.go` and `test_lint.go` so all unknown fields get `"did you mean '...'?"` suggestions in addition to `scenarioSpecHints` / `flowSpecHints`.
- [ ] **2.2 Enhanced Scenario Cross-Field Linting**: Add offline checks to `scenario_lint.go` for `minInsyncReplicas > effective replicationFactor` (using `TestTypeDefaults`), `enableIdempotence`/`enableTransactions` requiring `acks: all` (catching the `SPIKE` `acks: 1` trap), `enableTransactions` with `backend: trogdor`, and `TestOrchestrator.inapplicableFields` per `TestType`.
- [ ] **2.3 New `kates resilience lint` & `kates disruption lint` Subcommands**:
  - Implement offline AST linters in `cli/cmd/resilience_lint.go` and `cli/cmd/disruption_lint.go` mirroring `FaultLimits`, `ParsedLabelSelector`, `ProbeExecutor.problem`, `KafkaProbeChecks.problem`, and `DisruptionSafetyGuard` static rules.
  - Support `-o json` with the exact `lintFinding` schema (`{file, line, col, severity, code, path, message, fixable}`).

### Phase 3: Native Language Server (`kates lsp` in `cli/internal/lsp/`)
- [ ] **3.1 LSP Protocol Transport**: Implement JSON-RPC 2.0 over stdio in `cli/internal/lsp/` (`kates lsp` Cobra command) with zero external heavy dependencies.
- [ ] **3.2 Diagnostics, Hover, Completion, SemanticTokens, Definition, CodeAction & Formatting**:
  - Connect the 3-tier Dialect Router to the unified linter pipeline on `textDocument/didOpen`, `didChange`, and `didSave`.
  - Implement context-aware completions (including dynamic `waivers[].rule` lookup and `kafkaProbe` command completion), hover docs with `TestTypeDefaults`, Go-to-Definition from `waivers[].rule` to the target rule in `spec`, Quick Fixes for `unknown-field` and `fixable` diagnostics, and `textDocument/formatting` for `KafkaReference`.

### Phase 4: VS Code (`editors/vscode-kates/`) & Visual Studio (`editors/visualstudio-kates/`) Extensions
- [ ] **4.1 VS Code Extension Package**:
  - `package.json` with `yamlValidation` bindings, `grammars` (`syntaxes/kates-yaml.tmLanguage.json`), `snippets` (`snippets/kates-yaml.json`), commands, and configuration settings (`kates.lsp.path`, `kates.lint.onSave`, `kates.inlayHints.defaults`).
  - Status bar dialect indicator, Quick Pick dialect switcher, and CodeLens provider for running/dry-running/linting files in the integrated terminal.
- [ ] **4.2 Visual Studio 2022+ VSIX Manifest**:
  - `source.extension.vsixmanifest` + `KatesLanguageClient.cs` wiring `kates lsp --stdio`, the TextMate grammar, and JSON Schema catalog entries.
- [ ] **4.3 End-to-End Verification**:
  - Golden corpus tests across all 6 dialects testing: (a) unknown attributes at root and nested paths, (b) wire-vs-scenario key swaps, (c) type coercion traps, (d) cross-field refusals, and (e) completion/hover responses at cursor markers.

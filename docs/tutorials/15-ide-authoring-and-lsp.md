# Tutorial 15: Authoring & Debugging YAML Suites in VS Code

This tutorial walks through installing the Kates VS Code extension
(`vscode-kates`), scaffolding performance and chaos YAML files from templates,
catching unknown attributes and cross-field mistakes with `kates lsp`, applying
one-click quick-fixes and refactorings, comparing two scenarios side by side,
and running a live cluster pre-flight check against `krafter`.

**Level:** Intermediate · **Duration:** 20 min · **Prerequisites:** Tutorial 1

> [!NOTE]
> The offline linters (`kates test lint`, `kates flow lint`,
> `kates resilience lint`, `kates disruption lint`, and `kates compliance lint`)
> and the Language Server (`kates lsp --stdio`) work without a running cluster.
> Step 5 connects to `krafter` through the `ports` context only for live topic
> completions and blast-radius pre-flight preview. See
> [IDE Extensions & Language Server](../book/appendix-ide-extensions.md) for the
> full reference.

## What You Will End Up With

```text
   VS Code (vscode-kates) ── spawns ──► kates lsp --stdio
         │                                    │
         ├── JSON Schemas (Draft 2020-12)     ├── Offline AST Linters (6 dialects)
         ├── TextMate Grammar & 7 Snippets    ├── Inlay Hints (defaults, GiB, RTO)
         ├── Lightbulb Quick-Fixes & Refactor ├── Scenario Workload & Gate Diff
         └── Native VS Code Test Explorer     └── Live Cluster Pre-Flight (optional)
```

## Prerequisites

- The `kates` CLI installed on your `PATH` (`make cli-install`)
- Visual Studio Code (`1.85` or newer) with `code` on your `PATH`, and Node.js 22+
- Optional for Step 5: the Kind lab from Tutorial 1 with `kates ports` running

## 1. Package and Install the Extension

From the repository root, build the `.vsix` package and verify schema parity
across the CLI and editor directories:

```bash
make editors-check
make editors-package
ls -lh dist/vscode-kates-*.vsix
```

Install the packaged extension into VS Code:

```bash
code --install-extension dist/vscode-kates-*.vsix
```

Verify that `kates lsp` starts cleanly from your shell. With stdin closed, it
initializes and exits `0` immediately:

```bash
kates lsp --stdio < /dev/null
```

Create a scratch directory for this tutorial and open it in VS Code:

```bash
mkdir -p /tmp/kates-ide-lab && cd /tmp/kates-ide-lab
code .
```

## 2. Scaffold a Scenario and Read Inlay Hints

1. Open the Command Palette (`Cmd+Shift+P` on macOS, `Ctrl+Shift+P` on
   Linux/Windows) and run:
   **Kates: New YAML File from Template (Scenario, Chaos, Flow, Compliance)...**
2. Select **Performance Scenario (LOAD + SLA Gates)** (`scenario`).
3. Save the untitled buffer as `/tmp/kates-ide-lab/payments-load.yaml`.

Notice three things in the editor:

- **Status Bar Dialect Indicator**: The bottom-right status bar reads
  `Kates: scenario`, confirming that `kates lsp` matched the file's
  `# kates-schema: scenario` directive.
- **CodeLens Action Bar**: Above line 1 and above `- name: "Production Load Test"`,
  clickable **▶ Run Scenario (`kates test apply`)** and **✓ Lint Scenario**
  links appear.
- **Inline Ghost-Text Inlay Hints**:
  - Beside `type: LOAD`, a hint displays the server-side `TestTypeDefaults`
    (`→ defaults: 1 producer · unlimited rate · acks=all · batch=65536B · linger=5ms`).
  - Beside `records: 500000`, a hint computes the total payload volume from
    `records * recordSizeBytes` (`→ ~488.3 MiB total payload`).

## 3. Catch Unknown Attributes and Cross-Field Errors

Replace the contents of `/tmp/kates-ide-lab/payments-load.yaml` with the
following intentionally broken file:

```yaml
# kates-schema: scenario
scenarios:
  - name: "Broken Payments Load"
    type: LOAD
    spec:
      record: 200000
      recordSizeBytes: 1024
      partitions: 6
      replicationFactor: 3
      minInsyncReplicas: 4
      acks: "1"
      enableIdempotence: true
    validate:
      maxP99LatencyMs: 25.0
```

As soon as you type or save, `kates lsp` underlines three errors:

1. `spec.record`: `unknown field (did you mean "records"?) [unknown-field]`
2. `spec.minInsyncReplicas`: `minInsyncReplicas (4) cannot exceed replicationFactor (3) [min-isr-exceeds-rf]`
3. `spec.acks`: `enableIdempotence: true requires acks: "all" (got "1") [invalid-acks]`

Run the CLI linter in the terminal to confirm that the CLI and editor report the
exact same diagnostics:

```bash
kates test lint /tmp/kates-ide-lab/payments-load.yaml
```

Output:

```text
/tmp/kates-ide-lab/payments-load.yaml:6:7: error: scenarios[0].spec.record: unknown field (did you mean "records"?) [unknown-field]
  6 |       record: 200000
    |       ^
/tmp/kates-ide-lab/payments-load.yaml:10:26: error: scenarios[0].spec.minInsyncReplicas: minInsyncReplicas (4) cannot exceed replicationFactor (3) [min-isr-exceeds-rf]
  10 |       minInsyncReplicas: 4
     |                          ^
/tmp/kates-ide-lab/payments-load.yaml:11:13: error: scenarios[0].spec.acks: enableIdempotence: true requires acks: "all" (got "1") [invalid-acks]
  11 |       acks: "1"
     |             ^
```

Fix the file in the editor:

1. Place your cursor on `record: 200000`, press `Cmd+.` (`Ctrl+.`), and select
   **Replace with `records`**.
2. Change `minInsyncReplicas: 4` to `2`, and `acks: "1"` to `"all"`.
3. Save the file — all diagnostics clear.

## 4. Convert the Scenario into a Resilience Chaos Test

Instead of writing a `ResilienceTestRequest` from scratch, use the built-in LSP
refactoring action:

1. Place your cursor anywhere inside `payments-load.yaml` and press `Cmd+.`
   (`Ctrl+.`).
2. Choose **Convert Scenario to Resilience Chaos Test (`resilience.v1`)**.

The document transforms into a `resilience` specification that translates CLI
`spec` keys (`records`, `recordSizeBytes`) into wire `TestSpec` keys
(`numRecords`, `recordSize`) and appends a `POD_KILL` `chaosSpec` and a
`kafkaProbe`:

```yaml
# kates-schema: resilience
testRequest:
  type: LOAD
  spec:
    topic: kates-resilience
    partitions: 6
    replicationFactor: 3
    minInsyncReplicas: 2
    numRecords: 200000
    recordSize: 1024
    throughput: 5000
    acks: "all"
    enableIdempotence: true
chaosSpec:
  experimentName: broker-pod-kill
  disruptionType: POD_KILL
  targetNamespace: kafka
  targetLabel: "strimzi.io/kind=Kafka"
  chaosDurationSec: 30
  delayBeforeSec: 10
probes:
  - name: no-under-replicated-partitions
    type: kafkaProbe
    mode: EOT
    command: under-replicated-partitions
    comparator: "=="
    expectedOutput: "0"
    intervalSec: 5
    timeoutSec: 10
steadyStateSec: 15
maxRecoveryWaitSec: 60
```

Save this file as `/tmp/kates-ide-lab/resilience-pod-kill.yaml` and verify it
with the offline chaos linter:

```bash
kates resilience lint -f /tmp/kates-ide-lab/resilience-pod-kill.yaml
```

Hover over `POD_KILL` in the editor to view its blast radius, default
`chaosDurationSec`, and required Kubernetes RBAC permissions.

## 5. Compare Scenarios and Run Live Cluster Pre-Flight

Create two scenario files to compare their workload footprint and SLA gates:

```bash
cat <<'EOF' > /tmp/kates-ide-lab/baseline-load.yaml
# kates-schema: scenario
scenarios:
  - name: "Baseline Load"
    type: LOAD
    spec:
      records: 200000
      recordSizeBytes: 1024
      partitions: 6
      replicationFactor: 3
      minInsyncReplicas: 2
      acks: "all"
    validate:
      maxP99LatencyMs: 25.0
      minThroughputRecPerSec: 15000
EOF

cat <<'EOF' > /tmp/kates-ide-lab/heavy-load.yaml
# kates-schema: scenario
scenarios:
  - name: "Heavy Load"
    type: STRESS
    spec:
      records: 1000000
      parallelProducers: 4
      recordSizeBytes: 2048
      partitions: 12
      replicationFactor: 3
      minInsyncReplicas: 2
      acks: "all"
    validate:
      maxP99LatencyMs: 40.0
      minThroughputRecPerSec: 50000
EOF
```

1. Open `/tmp/kates-ide-lab/baseline-load.yaml` in VS Code.
2. Run **Kates: Compare / Diff Scenario Workload & SLA Gates...** from the
   Command Palette and select `/tmp/kates-ide-lab/heavy-load.yaml`.
3. A Markdown preview tab opens comparing every parameter (`type: LOAD -> STRESS`,
   `records: 200000 -> 1000000`, `Est. Data Footprint: 195.3 MiB -> 1.91 GiB`,
   and the SLA gate deltas).

If your Kind lab is running (`kates ports`), open
`/tmp/kates-ide-lab/resilience-pod-kill.yaml` and run
**Kates: Run Live Cluster Preflight & Blast-Radius Preview** from the Command
Palette. The Language Server queries `/api/cluster/info` and
`POST /api/disruptions?dryRun=true` and reports the broker count, partition
health, and dry-run safety verdict in the **Kates Language Server** output
channel.

## 6. Author and Format a Compliance Reference

Create `/tmp/kates-ide-lab/prod-ref.kafkareference.yaml`:

```yaml
# kates-schema: compliance
apiVersion: conformity.kates.io/v1alpha1
kind: KafkaReference
metadata:
  name: prod-3az
spec:
  versions:
    kafka: 3.8
  brokers:
    count:
      min: 3
  kafka:
    rackAwareness: true
```

1. Notice the error on `kafka: 3.8` (`unquoted-version`: YAML parses `3.8` as a
   number, which turns `3.10` into `3.1`). Press `Cmd+.` (`Ctrl+.`) on `3.8` and
   choose **Quote version string (`"3.8"`)**.
2. Place your cursor on `rackAwareness: true`, press `Cmd+.` (`Ctrl+.`), and
   select **Add compliance waiver for `kafka.rackAwareness`**. The server
   appends a `waivers:` block at the bottom of `spec` with `rule: kafka.rackAwareness`
   pre-filled.
3. Run **Kates: Format KafkaReference Canonically (`kates compliance fmt`)** from
   the Command Palette (or click the **Format Canonically** CodeLens) to order
   all sections canonically, then validate from the terminal:

```bash
kates compliance lint /tmp/kates-ide-lab/prod-ref.kafkareference.yaml
```

## Clean Up

Remove the scratch directory when finished:

```bash
rm -rf /tmp/kates-ide-lab
```

## Where Next

- [IDE Extensions & Language Server](../book/appendix-ide-extensions.md) — full
  table of dialect detection rules, diagnostic codes, refactoring actions, and
  Visual Studio 2022 setup.
- [Scenario Files & SLA Gates](../book/13-scenario-files.md) — complete field
  reference for `scenario.yaml` files.
- [Compliance Language Reference](../book/appendix-compliance-language.md) —
  every rule, constraint form, and waiver field for `KafkaReference` files.

# Plan — Move the Migration Tooling into the `kates` CLI and Retire the Scripts

Branch: `feat/mirror-maker2-cli` (from `feat/mirror-maker2-cross-version` once that merges, or from `main` after). Goal: the three shell scripts that drive a cross-version migration — `scripts/test-mm2-migration.sh`, `scripts/build-legacy-kafka-image.sh`, `scripts/mm2-kafka-cli.sh` — become a `kates migrate` command family, written in Go against the CLI's existing primitives, and the scripts are deleted. Not wrapped, not embedded as string literals: **replaced**.

> **Status: PLAN.** Nothing below is implemented. It follows a study of the CLI as it exists on this branch (§2), which is what the design decisions in §4–§6 rest on.

---

## 1. Why move at all

The scripts work — after two review passes, they are the most carefully checked shell in the repository. That is also the argument against them.

1. **They failed in ways only a reviewer could see.** Of the defects the review found ([the previous plan, §8](mirror-maker2-cross-version-migration-plan.md#8-what-the-review-found)), nine were in the scripts, and every one was a shell hazard: `kubectl run --rm` polluting stdout with a pod name that carried `$RANDOM`; an awk `END` block printing `0` on empty input; a sed range that does not close when the marker is the first line; `set -e` killing the run inside a `$(...)` under `pipefail`; a password interpolated into a pod spec; `echo` interpreting backslashes in a JAAS line. None of these can happen in Go. All of them were fixed, and the next person to edit the script will reintroduce one.
2. **Three languages in one file.** The e2e script is bash that generates Helm values as YAML, JSON pod overrides via an inline Python program, and `/bin/sh` payloads for pods — each with its own quoting rules, nested. The JAAS line passes through four of them. It is correct today because it was tested character by character; it is not something a reader can verify.
3. **The CLI already owns the surrounding lifecycle.** `kates deploy` installs Strimzi and `krafter`, `kates clean` removes releases, `kates test helm` runs chart tests, `kates kafka` reads topics and groups, `kates doctor` does pre-flight. A migration is the one workflow in the repo whose entry point is `make` calling bash calling `kubectl run` — the odd one out, and the newest.
4. **The scripts are not testable without a cluster.** Their parsers (connector status, offset sums, verdict lines) were unit-tested by hand, in the terminal, once. In Go they are functions with table tests that run in CI in milliseconds.
5. **Portability.** The release matrix ships `darwin`/`linux` × `amd64`/`arm64` binaries. The scripts need bash 4, GNU-compatible `sed`, `comm`, `mktemp -t`, and a Python 3 on `PATH` — all true on the runner and on one particular laptop.

> **Status: FIRST VERSION IMPLEMENTED.** `cli/internal/podrun`, `cli/pkg/migrate` and `cli/cmd/migrate*.go` exist; `kates migrate run --from 2.8.2|3.9.1` is what `.github/workflows/ci-mirror-maker2.yml` and the Makefile `mm2-*` targets call. The scripts are still in the tree until that CI run is green (§7's order); `mirror preflight` and the Strimzi-source paths are not in this version. The pod runner found and fixed latent script bugs on the way: the 2.8 offset tool takes only `--broker-list`, `kafka-get-offsets.sh`/`--command-config` arrive in 3.0 not 3.4, and a JAAS password with `"` or `\` needs double escaping because `Properties.load` strips one level.

The measure of success is narrow: a user who today runs `make mm2-migration-test` runs `kates migrate run --from 2.8.2` (`--source-version` accepted as an alias) and gets the same eleven-phase report, and `scripts/test-mm2-migration.sh` no longer exists.

---

## 2. The CLI as it is — findings the design depends on

### 2.1 Architecture

- **One flat `cmd` package**, cobra-based: ~90 command files, ~38k lines outside tests, 93 documented entries. Commands register themselves in `init()` and appear in `cmd/root.go`'s grouped help (`cmd/root.go:100-175`). A new family needs a root-help line, a `tldr` entry (`cmd/tldr.go`), a `doc_entries.yaml` block (embedded via `cmd/doc_entries.go:10`), and a section in `docs/book/10-cli-reference.md`.
- **Two thin process wrappers**: `internal/helm.Client` and `internal/kubectl.Client` (`internal/helm/helm.go`, `internal/kubectl/kubectl.go`) — `Run`, `Output`, `JSON`, `Exists`, `CRDExists`, `Apply`, `Install`/`Upgrade`/`Uninstall`/`Test`. Both shell out to the binaries on `PATH`; neither embeds any Kubernetes client library. `go.mod` has no Kafka client either.
- **Testability by function-variable injection.** `cmd/deploy.go:332-343` declares `runExecFn`, `runHelmFn`, `runExecOutputFn`, `runExecStdinFn`, `isHelmReleaseDeployedFn` as package variables that tests replace (`cmd/deploy_test.go:87-97`, `cmd/cluster_gate_test.go:98`). `pkg/detect` does it more cleanly with a `CommandExecutor` interface and a `MockExecutor` keyed on `name args...` (`pkg/detect/executor.go`, `pkg/detect/mock_executor_test.go`). Both patterns coexist; the interface one is the better model for new code because it lets a whole package be tested without touching globals.
- **Orchestration precedent**: `cmd/deploy_components.go:580-760` (`deployKafkaConnectStack`) is the closest relative of a migration — namespace apply via stdin YAML, cross-namespace Secret copy, `helm upgrade --install` with a computed bootstrap FQDN and overlay, a readiness wait with a per-component timeout (`deployComponent`, `:1080`), and a diagnostics dump on failure (`printConnectorDiagnostics`, `:1097`). The `deployContext` carries `isKind`, `resolveClusterDomain()`, `chartOverlay()` and the namespace set (`cmd/deploy.go:175-200`, `cmd/deploy_plan.go:220`).
- **Output layer**: `output.Success/Warn/Error/Hint/Table/JSON/Panel` (`output/output.go`), theme tokens in `pkg/theme`, and two CI gates that reject hex colours and raw ANSI (`scripts/check-cli-style.sh`) and assert `NO_COLOR`/pipe behaviour on the real binary (`scripts/check-cli-compat.sh`). Every new command inherits `-o json` and plain-mode conventions for free by going through `output`.
- **Tests run in CI** as `go test -p 1 ./... -race` (`.github/workflows/ci.yml:359`), so anything the new code does must be race-clean and must not need a cluster.

### 2.2 How the CLI reaches Kafka today — and why that is not enough

`kates kafka topics|consume|produce|groups` (`cmd/kafka.go`) go through the **backend's REST API** (`client/client.go:687-699`), which is bound to one cluster — `krafter`, as the backend's own principal. A migration needs three things that path cannot give:

| Need | Why the backend path cannot |
|:---|:---|
| Read/write the **legacy source** (another namespace, PLAINTEXT, Kafka 2.8 or 3.9) | The backend has no bootstrap override, and its 4.x client is the wrong tool for asserting an old broker (it would be measuring the client) |
| Act **as the MM2 principal** (`kates-mm2`) on the target | The backend uses its own user; ACL-scoped verification has to run as the user the mirror runs as |
| Run the **era-appropriate CLI** (`kafka-topics.sh` from the 2.8.2 image against 2.8.2) | The backend is a Java client of one version |

So the pod-based path the scripts use is the right one; the question is how it is built (§5).

### 2.3 Shell already inside the CLI — the "included scripts"

The CLI does not reference `scripts/*.sh` anywhere (`grep -rn 'scripts/\|\.sh\b' cli --include='*.go'` finds only the `.sh` inside a Helm annotation key in `deploy.go:460` and the `kates-ci.sh` that `init.go` generates for users). It does, however, contain **inline shell programs** — the same hazard class as the migration scripts, in a compiled binary where they are harder to see:

| Location | What it is | Hazard |
|:---|:---|:---|
| `pkg/detect/collector.go:1220` | `sh -c "kubectl get pods … \| … \|\| true"` — a pipeline in a string | exit-code laundering, quoting |
| `pkg/detect/collector.go:1274` | `sh -c "kubectl get pod … \| grep -oE … \| cut … \| head -1"` | four tools that Go's `regexp` replaces in one line |
| `pkg/detect/collector.go:933`, `:1707` | manifests and secret creation piped through `sh -c` | a Kubernetes manifest built by string concatenation |
| `pkg/detect/collector.go:1915` | `kubectl exec … -- sh -c "iperf3 … &"` | a backgrounded process in a string |
| `cmd/doctor_network.go:369-393` | `runEphemeralPod`: `kubectl run --rm -i … -- sh -c <shellCmd>` with busybox, markers parsed from combined output | the exact `--rm -i` stdout hazard the review found, minus the `$RANDOM` |
| `cmd/doctor_dns.go:69` | `kubectl run --rm -i … -- cat /etc/resolv.conf` | benign (argv, not shell) but the same attach path |

None of these is in scope to *fix* here — but the migration port must not add a seventh. The rule this plan adopts for the new packages, and proposes for the CLI generally: **the CLI never composes a shell program.** Every process is an argv; every pod command is an argv; anything that needs a pipe, a redirect, or a `$VAR` is done in Go. `pkg/detect` and `doctor_network` then become a follow-up that reuses the same pod runner (§8, phase 5).

### 2.4 Conventions the port inherits

- **Charts are addressed relative to the repo root** (`"charts/…"` appears 19 times in `cmd/`; `cmd/cluster_gate.go:140` tells the user to "run from the repo root"). `kates migrate` will behave the same and say so in the same words. A `--repo` flag or an upward search for `versions.env` is a possible later refinement, not part of this plan.
- **Overlay selection** is `isKind ? values-kind.yaml : values-generic.yaml` (`cmd/deploy.go:184`). The migration presets are a third axis (`values-migrate-2x.yaml`) layered on top; `kates migrate` uses the preset and lets `--values` add more.
- **No version pins in Go.** There is no `versions.env` reader in the CLI, and no Kafka/Strimzi literal anywhere in `cmd/` or `pkg/`. `scripts/check-versions.sh` now guards six Strimzi sites and four Kafka sites; the CLI must not become an unguarded seventh — it reads `versions.env` from the repo root at run time (§5.1).
- **Prompts and automation**: `--yes` (`deployYes`) and `--dry-run` (`deployDryRun`) exist on `deploy`, and the reference states the rule — confirmations are never answered implicitly; without a terminal, a command that needs consent fails and says to pass `--yes`. `kates migrate cutover`, `rollback`, and both `remove` commands follow it, because they are the destructive steps.
- **Test pods need the label** `kates.io/test-pod: "true"` to reach the brokers (the `kafka-cluster` NetworkPolicy selector) — a fact the scripts learned the hard way and the pod runner will bake in.

### 2.5 Gaps the port has to fill

| Gap | Where it bites | Filled by |
|:---|:---|:---|
| No "run a Kafka CLI command in a pod as principal X" primitive | every verification step | `internal/podrun` (§5.2) |
| No Helm values-file writer — everything is `--set` | `--set` eats the backslash in `kates\..*` (found by review) | `pkg/migrate/values.go`: typed struct → YAML file → `-f` |
| No `versions.env` reader | choosing the client image | `pkg/migrate/pins.go` |
| `kates test helm`'s `knownComponents` (`cmd/helm_test_cmd.go:77`, registered under `testCmd` at `:890`) lacks `mirror-maker2` / `legacy-kafka` | `kates test helm mm2` finds no release | one map entry each |
| `kates clean`'s release lists (`cmd/clean.go:216-250`) lack `mm2*` / `legacy*` | a migration lab survives `kates clean` | add both, plus the kept CR |
| Connector-status parsing exists only as jsonpath in shell (`.status.connectors[*]`) | `migrate status`, `migrate verify` | `pkg/migrate/status.go` with table tests |

---

## 3. What moves, and to where

| Script (lines) | Responsibilities | New home | Stays outside the CLI |
|:---|:---|:---|:---|
| `build-legacy-kafka-image.sh` (154) | choose JRE/Scala/OS release; `docker build`/`buildx --push`; verify the jar version; `kind load` | `kates migrate image build` | `Dockerfile.legacy-kafka` — the CLI drives it, does not replace it |
| `mm2-kafka-cli.sh` (134) | client pod as `kates-mm2`; `topics` / `offsets` / `groups` / `group`; version-aware offset tool | `kates migrate target topics|offsets|groups|group` | — |
| `test-mm2-migration.sh` (671) | 11 phases: preflight, deploy source, seed, commit a group, install MM2, wait Ready + connectors, wait topic, read back + diff, offset translation, cutover rehearsal, report; cleanup trap | `kates migrate run` orchestrating `source deploy`, `mirror deploy`, `verify`, `mirror cutover`, `source remove`, `mirror remove` | the Helm charts, their tests, the chart's own pre-flight hook Job |
| `Makefile` `mm2-*` / `legacy-kafka-*` targets (14) | argument plumbing | thin aliases for one release, then deleted (§7) | — |
| `.github/workflows/ci-mirror-maker2.yml` e2e job | calls the script | calls `kates migrate run` | — |

Deliberately **not** moving: the chart's `preflight-job.yaml` (it must run inside `helm install`, where the CLI is not), the chart Helm tests (they are the chart's contract with `helm test`), and the Dockerfile (a Dockerfile is the right language for an image).

---

## 4. Command design

```text
kates migrate                              Cross-version Kafka migration with MirrorMaker 2

  # the front door — a pair of versions (resolution rules: multi-version plan §3.9)
  pairs                                                      every old → new pair this cluster can stand up now
  plan             --from 2.8.2 [--to 4.3.0]                 what `up` would create; nothing changes
  up               --from 2.8.2 [--to 4.3.0] [--name m282-430] [--source-provider auto|strimzi|legacy]
                   [--source-strimzi-version 1.0.1] [--policy identity|default] [--topics kates.orders]
                   [--messages 200] [--sasl|--plaintext] [--values-source f] [--values-mirror f] [--yes] [-i]
  status           [--name …] [--watch]                      source, target, mirror, connectors, lag, offsets
  verify           [--name …] | --from-bootstrap … --source-version V [--policy …] [--messages 200] [--group …]
                   the seed → mirror → read-back → translation → cutover-rehearsal core,
                   usable against a REAL source, not only the lab
  cutover          [--name …] [--yes]                        source connector stopped, checkpoint running
  rollback         [--name …] [--yes]                        the reverse, with the data-merge warning
  down             [--name …] [--yes]                        the mirror, the source and its operator, the lab's topics — by label
  run              --from 2.8.2 [--to 4.3.0] [--policy …] [--keep] [--skip-build] [--timeout 600] [-o json]
                   up → verify → cutover → down: one report, one exit code (`--source-version` = alias of `--from`)

  # the building blocks the front door is composed from
  image build      --version 2.8.2 [--scala 2.13] [--jre 11] [--os-release jammy]
                   [--platform linux/arm64] [--load | --push] [--registry ghcr.io/bmscomp]
  source deploy    --version <v ≥ 2.1.0> [--provider auto|strimzi|legacy] [--strimzi-version …] [--namespace …]
                   [--release …] [--image …] [--topic kates.orders[,…]] [--sasl] [--values f.yaml]
                   legacy only until `kates clusters add` lands; then a thin call to it with the source role
  source status    [--namespace …]                          broker (+ ZooKeeper) readiness, topics
  source remove    [--namespace …] [--yes]
  mirror deploy    --from <cluster name> | --from-bootstrap host:9092 --source-version V
                   [--policy identity|default] [--release mm2] [--namespace kafka]
                   [--target-cluster krafter] [--values f.yaml] [--dry-run]
  mirror status    [--release mm2] [--watch]                 CR Ready, connectors, lag, end offsets
  mirror preflight [--release mm2]                           the chart's probe, run standalone
  mirror cutover   [--release mm2] [--yes] [--dry-run]       the lower-level form of `cutover`
  mirror rollback  [--release mm2] [--yes]
  mirror remove    [--release mm2] [--yes]                   helm uninstall + delete the kept CR
  target topics|offsets <topic>|groups|group <id>            pod-based, as kates-mm2, era-aware tool
```

The front door is what the tutorials and CI use; the building blocks are what it is made of and what an operator reaches for when a real migration needs one step at a time. `--from`/`--to` are versions at the front door and cluster *names* one level down (`mirror deploy --from m282-430-src`), resolved through the same discovery `kates clusters list` uses; the pair-to-provider rules — which source gets Strimzi, which gets its own operator, which gets `legacy-kafka` — are the multi-version plan's (§3.9 there) and are not duplicated here.

Design choices, each with the reason:

- **`verify` is a first-class command, not a phase inside `run`.** The script's phases 3–10 are the valuable part, and they only ever ran against the lab source. Pointed at a real bootstrap with a real credential, they are a migration acceptance test. `run` is `up` + `verify` + `cutover` + `down` — the lab wrapper — and `up` is `source deploy` (or `kates clusters add`) + `mirror deploy` with values written from the pair.
- **The lab is found by label, not by a state file.** `up` stamps `kates.io/lab=<name>` and `kates.io/lab-role` on every release it creates; `status`, `cutover` and `down` discover from the cluster, so a lab survives a lost terminal and `down` removes exactly what `up` made.
- **`mirror preflight` reuses the chart's probe.** The verdict classifier (`PROTOCOL`, `DNS`, `TLS`, `AUTH`, `LISTENER`, `NETWORK`) lives in `charts/mirror-maker2/templates/preflight-job.yaml`. Rather than port it to Go — two implementations of one classifier drift — the command renders that one template (`helm template … -s templates/preflight-job.yaml`, with the hook annotations stripped), applies it, waits, streams the log, and parses the verdict lines into structured output. One source of truth; the CLI adds `-o json` and exit codes.
- **`--dry-run` on `mirror deploy` and `cutover`** prints the Helm command and the generated values file and stops — the same contract as `kates deploy --dry-run`.
- **Exit codes** follow the CLI's existing contract (`docs/book/10-cli-reference.md` "Exit Codes"): `0` means the thing you asked for happened, `1` means anything else — a failed assertion, an unreachable cluster, a declined or un-askable confirmation. `verify` and `run` exit `1` on any failed row, and print the table either way; the JSON output carries the per-row detail for scripts that need more than one bit.
- **`-o json`** on `status`, `verify`, `run`, `preflight`, and every `target` subcommand, so CI can assert on fields rather than grep.
- **Naming**: `migrate` rather than `mm2` because the user's intent is the migration; MirrorMaker is the mechanism. `kates doctor` keeps its meaning (cluster pre-flight); `migrate mirror preflight` is scoped under the mirror so the two do not collide in help.

---

## 5. Package layout and the two primitives everything rests on

```text
cli/
  internal/podrun/         ← the pod runner (§5.2). No shell, ever.
  pkg/migrate/             ← pure logic, interface-injected, fully unit-tested
    pins.go                  versions.env reader → ClientImage(), StrimziVersion()
    values.go                typed mirror/source values → YAML file for -f
    status.go                KafkaMirrorMaker2 status parsing (connectors, tasks, conditions)
    offsets.go               end-offset parsing for both tool generations; sum; per-partition
    corpus.go                deterministic corpus generation + the set-diff that replaces comm(1)
    verdict.go               preflight log → []Verdict
    report.go                the phase/assertion table + JSON shape
    plan.go                  the phase sequence for verify and run, as data
  cmd/migrate.go             the cobra tree; flag → options structs
  cmd/migrate_image.go
  cmd/migrate_source.go
  cmd/migrate_mirror.go
  cmd/migrate_target.go
  cmd/migrate_verify.go      verify + run share one runner with a phase list
```

### 5.1 Pins

`pkg/migrate/pins.go` reads `versions.env` from the repo root (the same file the scripts source) and exposes `STRIMZI_KAFKA_VERSION` → `quay.io/strimzi/kafka:<tag>`. No literal fallback — a missing file is an error that names the file, exactly as the script does today (`test-mm2-migration.sh:138-147`). `scripts/check-versions.sh` gains nothing to check, because the CLI has no pin of its own.

### 5.2 The pod runner — replacing `kubectl run --rm -i … sh -c`

Every verification step needs a Kafka CLI tool running inside the cluster as a chosen principal, with an era-appropriate image. The scripts do this with `kubectl run --rm -i` and a `/bin/sh -c` string. The runner replaces both halves:

**A long-lived client pod per cluster, driven by `kubectl exec` with argv.**

```go
type Client struct { // one per cluster side
    Namespace, Name, Image string
    Credential *Credential  // nil for PLAINTEXT
}
func (c *Client) Start(ctx) error       // apply Pod + (optional) Secret, wait Ready
func (c *Client) Exec(ctx, argv []string, stdin io.Reader) (stdout, stderr string, exit int, err error)
func (c *Client) Stop(ctx) error        // delete Pod + Secret
```

- **No shell in the pod.** `Exec` passes `argv` straight to `kubectl exec -i <pod> -- <argv…>`. `kafka-topics.sh --bootstrap-server … --command-config /etc/kates/client.properties --list` is an argv. So is the producer, with the corpus supplied on **stdin from Go** (`cmd.Stdin = strings.NewReader(corpus)`) — which is why the pod is long-lived and driven by `exec` rather than created per command: `kubectl run --rm -i` cannot be given a stdin reader reliably and pollutes stdout on deletion; `exec` has clean stdout, a real exit code, and a real stdin.
- **No credential on any command line, in any pod spec, or in any log.** The CLI reads the KafkaUser's Secret with `kubectl get secret -o json`, writes a `client.properties` (SCRAM or PLAIN, the JAAS line built by Go with proper escaping of `"` and `\`) into a short-lived Secret `kates-migrate-client-<rand>`, and mounts it as a file. The pod spec carries a `secretName`; nothing else.
- **No `$RANDOM`-in-stdout class of bug**, no sentinel, no `sed -n '1,/marker/p'`: `exec` stdout is the command's stdout.
- **Era-aware tools are a Go decision, not `if [ -x … ]`:** `Offsets(topic)` runs `kafka-get-offsets.sh` when the image's Kafka line is ≥ 3.4 and `kafka-run-class.sh kafka.tools.GetOffsetShell` below — the version is known (it is the `--version` the user gave, or the target's pin), so the pod never has to probe its own filesystem.
- **The label** `kates.io/test-pod: "true"` and `LOG_DIR=/tmp` are set once, in the spec, for every pod.
- **Testable**: `podrun` takes the same `CommandExecutor` interface `pkg/detect` uses, so a `MockExecutor` keyed on `kubectl exec … -- kafka-topics.sh …` returns canned output and the whole of `pkg/migrate` runs in `go test` without a cluster.

The runner is what makes §2.3's rule possible for the rest of the CLI later: `doctor_network`'s busybox probes and `collector.go`'s pipelines are the same shape.

### 5.3 Helm and kubectl

Through `internal/helm` and `internal/kubectl` as they are, with two additions that are needed anyway: `helm.Client.Template(chart, valuesFiles, showOnly)` (for `mirror preflight`) and `kubectl.Client.WaitFor(kind, name, condition|jsonpath, timeout)`.

### 5.4 The values writer

`pkg/migrate/values.go` holds a `MirrorSpec` struct (source bootstrap, alias, Kafka version, auth, policy, patterns, connector configs) and renders it to a YAML file passed with `-f`. This is the fix for the `--set` backslash problem the review found (`test-mm2-migration.sh:453-460`, "Helm's --set parser treats a backslash as an escape"), and it means the chart's `values.schema.json` validates what the CLI sends. `--dry-run` prints the file.

---

## 6. Phase map: script → Go

| Script phase | `kates migrate` | Notes |
|:---|:---|:---|
| 1 preflight (cluster, CRD, `krafter` Ready, `kates-mm2` Secret) | `run` step 0, reusing `kates doctor`'s checks where they exist | `kubectl.CRDExists`, `WaitFor(kafka/krafter, Ready)` |
| 2 build image (2.x) + deploy `legacy-kafka` + `helm test` | `image build`, `source deploy` | `--skip-build` keeps its meaning |
| 3 produce corpus + source end offsets | `verify` phase "seed" | corpus generated in Go, produced via `Exec` stdin; offsets via `Offsets()` on the source client |
| 4 commit a consumer-group offset | `verify` phase "group" | `kafka-console-consumer.sh --group … --max-messages N/2` then `kafka-consumer-groups.sh --describe` parsed by `status.go` |
| 5 install MM2 with preset + generated mirror values | `mirror deploy` | values file, not `--set`; preflight is the chart's hook, as today |
| 6 CR Ready + connectors RUNNING | `mirror status --wait` | `status.go` parses `.status.connectors[]`; PAUSED/STOPPED are reported, not failed, as in the chart test |
| 7 replicated topic appears | `verify` phase "topic" | `target.Topics()` polled |
| 8 read back: distinct count + full-set diff | `verify` phase "content" | `corpus.go` set difference; cap 2N for at-least-once |
| 9 offset translation | `verify` phase "translation" | `kafka-consumer-groups.sh --describe` on the target, parsed |
| 10 cutover rehearsal | `mirror cutover` + `verify` phase "frozen" | baseline after settle; three outcomes (measured/unmeasured/moved) kept |
| 11 report + cleanup trap | `report.go` table + `-o json`; cleanup via `defer` and a context that survives Ctrl-C | `--keep` prints the same "leave it, here is how to inspect" block |

Every phase is a `Phase{Name, Run func(ctx, *State) Result}` in `plan.go`, so `verify` and `run` differ only in the list they execute, and a unit test can run the list against the mock executor end to end.

---

## 7. Cutover of the callers, then deletion

Ordered so that nothing is deleted before its replacement has run green in CI.

1. **Land the commands** behind no feature flag — they are additive.
2. **CI first.** `.github/workflows/ci-mirror-maker2.yml`'s e2e job builds the CLI (it already builds it for `deploy-kafka-generic.sh`) and runs `kates migrate run --from ${{ matrix.source }} --skip-build`, then the default-policy variant. The script step is removed in the same PR; the matrix and diagnostics are unchanged.
3. **Makefile.** `mm2-migration-test*`, `mm2-topics`, `mm2-offsets`, `legacy-kafka-image`, `legacy-kafka-deploy`, `mm2-deploy`, `mm2-undeploy` become one-line aliases to `kates migrate …` for one release, each printing a deprecation line, then are removed. `mm2-chart-*` (lint/template/guards/package) stay: they are chart CI, not migration tooling.
4. **Docs.** Tutorials 10–12, the runbook, the chart READMEs, NOTES.txt, and the book chapter reference `kates migrate …` and `kates migrate target …`; `docs/book/10-cli-reference.md` gets a "Migration Commands" section; `doc_entries.yaml` gets the entries; `tldr` gets four lines. The previous plan's §3.4 and §9 are updated to say the script has been replaced, not silently left stale.
5. **Delete** `scripts/test-mm2-migration.sh`, `scripts/build-legacy-kafka-image.sh`, `scripts/mm2-kafka-cli.sh`. `scripts/common.sh` loses nothing (they only consumed it). The `check-versions.sh` comment that names the script as a pin consumer is updated.
6. **`kates clean` and `kates test helm`** learn the new releases (§2.5), so a migration lab is cleaned up and tested by the commands users already run.

Definition of done for the deletion: `grep -rn 'mm2-kafka-cli\|test-mm2-migration\|build-legacy-kafka-image' .` returns only the previous plan's history section; `make mm2-migration-test` (alias) and the CI matrix pass on both legs against the CLI.

---

## 8. Phases and acceptance criteria

**Phase 0 — Foundations** (`internal/podrun`, `pkg/migrate/{pins,values,status,offsets,corpus,verdict,report}`)
- [ ] `podrun.Client` starts a pod with a mounted credential Secret, execs argv with stdin, returns exit code, stops and cleans up; tested against `MockExecutor`
- [ ] Every parser has table tests, including the empty-input and error-only cases that bit the scripts (offset sum on no rows must be *absent*, not 0)
- [ ] `values.go` round-trips through `helm template` in a test using the real chart and a `kates\..*` pattern
- [ ] Zero `sh -c` and zero `bash -c` in the new packages (a `go vet`-style test greps for them)

**Phase 1 — `migrate target` and `migrate image`**
- [ ] `kates migrate target topics|offsets|groups|group` reproduces `mm2-kafka-cli.sh` output, plus `-o json`
- [ ] `kates migrate image build --load` reproduces the script, including the jar-version verification and the `--push`/`--load` exclusivity

**Phase 2 — `migrate source` and `migrate mirror`**
- [ ] `source deploy` installs `legacy-kafka` with the era overlay and runs its Helm test; `source remove` deletes only what it created
- [ ] `mirror deploy --from 2.8.2` renders the same CR `helm install -f values-migrate-2x.yaml -f <generated>` does today (golden test on `helm template` output)
- [ ] `mirror status` shows Ready, each connector's state, and target end offsets; `--wait` blocks like the script's phase 6
- [ ] `mirror preflight` runs the chart's probe standalone and exits non-zero on any `❌` verdict
- [ ] `mirror cutover` / `rollback` apply and remove `values-cutover.yaml`; both require `--yes` or a prompt

**Phase 3 — `migrate verify` and `migrate run`**
- [ ] `verify` against the lab source produces the same assertion rows as the script (names byte-identical, so the tutorial's table stays true)
- [ ] `verify --from-bootstrap` against an arbitrary SASL/plaintext source works with a user-supplied Secret
- [ ] `run --from 2.8.2` and `--from 3.9.1` pass in the CI matrix (`--source-version` accepted as an alias); `--keep` leaves the lab and prints how to inspect it; `plan` prints what `up` would create and creates nothing; `down` removes only the labelled releases

**Phase 4 — Cutover and deletion** (§7)

**Phase 5 — Follow-up, out of this plan's scope but enabled by it:** port `doctor_network.runEphemeralPod` and the `collector.go` pipelines onto `podrun` and Go string handling, so the CLI contains no shell programs at all; extend `kates kafka` with a `--via pod --as <user>` transport so the backend-bound and pod-bound Kafka reads converge.

Rough size: Phase 0 ≈ 1,200 lines including tests; Phases 1–3 ≈ 1,800; deletions ≈ 960 lines of shell plus ~120 of Makefile.

---

## 9. Risks and decisions

- **Docker and kind are still external.** `image build` shells out to `docker` and `kind load` exactly as `pkg/cluster` does for cluster creation. The CLI does not embed a container builder; it should not. Missing `docker` fails with the same message the script gives.
- **The repo root.** `kates migrate` needs `charts/`, `versions.env` and the Dockerfile, so it runs from the repo root like `kates deploy`. Users who install the binary alone get a clear error, not a mysterious "chart not found".
- **Two pods for the lab, one for a real source.** `verify --from-bootstrap` still needs a client pod on the *target* cluster's network for the source too (the source is reachable from inside the cluster, not necessarily from the operator's laptop). The runner supports an image override so the source client can be era-appropriate even when the source is external.
- **`kubectl exec` and long commands.** A consumer with `--timeout-ms 120000` holds an exec session two minutes; `exec` handles that, and the context timeout is the ceiling. The scripts had the same exposure via `--pod-running-timeout`.
- **Keeping the chart's preflight as the source of truth** means `mirror preflight` depends on `helm template` of the chart — acceptable, since the CLI already requires the repo root; it avoids a second classifier that would drift.
- **Not touching `kates kafka` now.** Merging the backend-bound and pod-bound transports is a real design question (which principal, which cluster, which image); doing it inside a migration PR would be scope creep. Phase 5 names it.
- **Windows** is not in the release matrix and this plan does not add it; nothing in the port would prevent it later, which is more than the scripts could say.

## 10. Non-goals

- Replacing Helm or the charts with Go code. The charts are the deployment contract; the CLI drives them. What the *chart* should grow next — read-only sources, fan-in, failover and failback — is [its own plan](mirror-maker2-chart-enhancement-plan.md), and `kates migrate` gains the flags that drive it once it lands.
- A Kafka client library in the CLI. The era-appropriate CLI inside a pod is a feature, not a limitation: it is the only client guaranteed to speak to a 2.8 broker, and it is the same client the workers use.
- Moving the *other* `scripts/*.sh` (deploy-*, test-perf-*, chaos). They are a separate, larger conversation; this plan builds the primitive (§5.2) they would use.
- Choosing the Strimzi or Kafka version of the primary installation, or running several Kafka versions side by side. That is [`kafka-multi-version-deploy-plan.md`](kafka-multi-version-deploy-plan.md); it reuses this plan's pod-exec primitive and the `legacy-kafka` chart, and `kates migrate` gains an in-window source (4.2.1 → 4.3.0) once it lands.

# Kates CLI — Review and Enhancement Plan (2026-10-27)

**Status: Wave 1 shipped in #518; wave 2 (except 2.12, left for the maintainer) in #519; wave 3 is in #520. Wave 4 is open.**

Goal: a `kates` CLI whose every command exits non-zero when it failed, can be interrupted without leaving work running on the cluster, produces machine-readable output that is actually parseable, and whose generated artefacts (JUnit, HTML, PDF, Helm values) are correct on every input. Then: pay down the duplication that makes each of those properties expensive to keep.

Source: a review of the whole module (`cli/`, 230 non-test Go files, 76,568 lines, 156 test files) by five reviewers, one per area (foundation; deploy/cluster/clean/ports; test/report/gate/chaos/security; the MCP server; migrate/detect/TUI), followed by a verification pass that re-read every high-ranked finding in the source before it entered this plan. Line numbers are those of commit `c9cbd2a` (main on 2026-10-27).

## 1. Where the CLI stands

Build, `go vet` and the full test suite pass on main. The code is unusually well commented: most non-obvious decisions carry a paragraph saying what went wrong before and why the current shape is the fix. The foundation is sound (config file handling, interrupt handling for the commands that have it, the HTTP client's retry policy, the MCP guard). The problems are at the edges: commands that report success after failing, long-running commands that cannot be stopped, artefacts built by string concatenation, and four to six copies of the same loop or table in different files.

### Metrics

| Measure | Value | Note |
|:--|:--|:--|
| Functions over the repo's `funlen` limit (100 lines) | 51 of 2,075 | Largest: `RenderPDF` 706, `RenderTUI` 651, `RenderHTML` 616, `runClean` 596, `RenderMarkdown` 471, `runDeploy` 404 |
| Functions over `gocyclo` 30 | 21 | Top: `RenderTUI` 133, `RenderPDF` 96, `RenderMarkdown` 93, `runClean` 90, `LabModel.Update` 80, `runDeploy` 74 |
| Test coverage by package | `internal/conformity` 94 %, `pkg/migrate` 97 %, `pkg/kafkaversion` 98 %, `pkg/strimzi` 92 %, `pkg/cluster` 86 %, `client` 73 %, `output` 65 %, `cmd` 61 %, `pkg/detect` 57 %, `tui` 32 %, `internal/kubectl` 31 %, **`internal/helm` 8 %** | `cmd` is 60 % of the code; its number hides that every deploy orchestration test stubs every wait |
| `context.Background()` in `cmd/` | 488 | `cmd.Context()` is used 31 times; most commands do not see Ctrl-C |
| `fmt.Print*` in `cmd/` | 575 | Bypassing `output`, so `--plain`, `-o json` and glyph fallbacks do not apply |
| `exec.Command` call sites in `cmd/` | 14 files | Outside the five exec seams, so untestable and not pinned to the deploy's kube-context |
| Package-level `var` in `cmd/` | 381 | Flags bound to shared globals; one global (`deployKafkaName`) is bound by five commands |
| Commands without an `Example:` | 90 files | |
| `golangci-lint` | runs in CI with `--new-from-merge-base=main`, so only new issues are reported | The whole-tree backlog (errcheck, staticcheck, funlen) is not gated; a locally installed v2.14.0 built with Go 1.25 refuses the module's Go 1.27, so contributors need the CI's build of it to reproduce findings |

## 2. Findings

Each finding has an id (area letter + number), a confidence, and the file:line it was verified at. "Verified" means run against a built binary during the review; the rest were confirmed by reading the code path from its caller.

### A. Foundation (root, client, output, doctor, upgrade, plugin)

| Id | Finding | Where | Severity |
|:--|:--|:--|:--|
| A1 | A `kates-<name>` executable on PATH replaces a built-in command. Plugins register from `init()` with no name check; cobra returns the first exact match, and `init()` order follows file name order, so every built-in whose file sorts after `plugin.go` (`ports`, `profile`, `report`, `resilience`, `security`, `status`, `test`, `theme`, `top`, `trend`, `tune`, `upgrade`, `version`, `watch`, `webhook`) is shadowed. Verified with a `kates-version` script. | `cmd/plugin.go:105-135` | High, security-relevant |
| A2 | `kates upgrade` writes the running binary in place; Linux refuses with ETXTBSY. | `cmd/upgrade.go:317-329, 413-434` | High |
| A3 | `kates doctor -o json` prints a banner before the JSON; keys are Go field names, not camelCase. Verified. | `cmd/doctor.go:30-31, 13-18, 147` | High |
| A4 | `kates status` ignores `-o json`, exits 0 when the API is unreachable, and prints the context URL rather than the URL it used. Verified. | `cmd/status.go:40-48` | Medium |
| A5 | `kates doctor` exits 0 whatever fails. | `cmd/doctor.go:36-37, 141-142` | Medium |
| A6 | `profile assert` divides by a zero baseline (`profile save` never checks the run finished), NaN compares false, result "PASS". | `cmd/profile.go:84-92, 213-224` | Medium |
| A7 | `doctor dns --fix` replaces the whole `dnsConfig.searches` list, dropping existing entries. | `cmd/doctor_dns.go:204, 210` | Medium |
| A8 | Shell completions, `kates version` and (three times) `kates upgrade` call the API with no deadline: 30 s dial, 60 s request, 3 retries, so a blackholed host blocks the shell for minutes. | `cmd/autocomplete.go`, `cmd/version.go:49`, `cmd/upgrade.go:71,184,252` | Medium |
| A9 | Windows build broken by `syscall.Flock` while the help advertises PowerShell completion. | `cmd/root.go:181,185,421`, `cmd/portforward.go` | Medium |
| A10 | Plugin exit codes are lost: an `*exec.ExitError` is printed as an error line and the CLI exits 1. | `cmd/plugin.go:121` | Low |
| A11 | `doctor network` runs eight sequential pods under one 120 s context and treats any output on a non-zero exit as a successful probe. | `cmd/doctor_network.go:65,107-160,370-394` | Medium |
| A12 | `ctx set <existing>` rebuilds the context from flags, wiping the API key, proxy and TLS setting when only `--url` is passed; `ctx delete` of the current context picks the next one by map iteration. | `cmd/config.go:124-132, 203-206` | Medium |
| A13 | Five commands share one `--kafka-name` global; the last `init()` to run sets the default for all. | `deploy.go:149`, `clean.go:54`, `portforward.go:114`, `deploy_status.go:43`, `doctor_network.go:54` | Low |

Sound and worth keeping as the model: the config file (0600, atomic rename, `flock`, symlink-aware), API-key masking and stripping when `--url` points elsewhere, the retry policy (GET only, backoff honours ctx), the `interruptible` annotation and its 130 exit code.

### B. Deploy, cluster, clean, ports, Helm and kubectl wrappers

| Id | Finding | Where | Severity |
|:--|:--|:--|:--|
| B1 | `kates ports` (and `deploy --port-forward`) runs `pkill -f "kubectl port-forward"` and so kills forwards to other clusters that kates never started. `kates clean` was fixed for this and `stopPortForwards(match)` exists; `runPorts` never adopted it. | `cmd/portforward.go:141`, `cmd/portforward_stop.go:12-21` | High |
| B2 | Group A/B "parallel" Helm installs are serialized: `runExecDefault` holds `execMutex` across `cmd.Run()`, so Strimzi's 10-minute `helm upgrade` blocks cert-manager's and Kyverno's. The lock only needs to cover the print. | `cmd/deploy.go:583-616`, `cmd/deploy_components.go:736-1021, 1187-1272` | High |
| B3 | Five cross-namespace Secret copies drop the `kubectl apply` error; the failure surfaces 5–15 minutes later as a readiness timeout pointing nowhere. | `cmd/deploy_components.go:1428,1444,1719,1803,1866` | High |
| B4 | `kates clean` prints "Cluster cleaned successfully" and exits 0 after failed uninstalls, cluster-resource deletes, namespace timeouts or CRD deletes. `kates clean -y && kates deploy` proceeds onto a half-torn-down cluster. | `cmd/clean.go:629-705` | High |
| B5 | `kyverno enforce/audit/status/violations` print the error and return nil; `kyverno apply` reports "1 policies are now active" for zero. | `cmd/kyverno.go:100-103,210-214,300-302,326-328`, `cmd/kyverno_apply.go:161-164` | High |
| B6 | `kyverno apply` installs a second `kates` release into a hard-coded `kates` namespace, ignoring `--app-ns`/`--topology`; its `--dry-run` returns before running Helm, so the flag is dead. | `cmd/kyverno_apply.go:43-46, 94-96, 124` | Medium/high |
| B7 | A transient or RBAC failure of `kubectl get pods -A` in `readInstalled` becomes "no operator installed", so `deploy` runs `helm upgrade --install --reset-values` and bypasses the operator downgrade rail. | `cmd/deploy_versions.go:437-445, 467, 497-502` | Medium |
| B8 | `cleanupStaleClusterResource` deletes any `clusterrole/kates` whose `release-namespace` annotation differs from this deploy's, including a second tenant's. `clean` is careful about this case; `deploy` is not. | `cmd/deploy.go:702-716`, `cmd/clean.go:423-435` | Medium |
| B9 | `deploy status` reports a failed release as "deployed" (`helm status` exit 0), adopts a same-named release from another namespace, and shells out directly, bypassing every seam. | `cmd/deploy_status.go:283-340, 385-413` | Medium |
| B10 | `kates ports` always exits 0, even after "Some port forwards failed to establish". | `cmd/portforward.go:105-109, 271` | Medium |
| B11 | `connect scale` builds the patch JSON with `%s` of an unvalidated argument. | `cmd/connect.go:347` | Low |
| B12 | `internal/helm.Client.Status` returns `"", nil` on any error; `internal/kubectl.Client.Namespace` is never emitted, so `kubectl.New(ns)` is a no-op and every caller re-passes `-n`. | `internal/helm/helm.go:50-56,100`, `internal/kubectl/kubectl.go:44-51` | Medium/low |
| B13 | `kates operator` prints "Reconciling…" every 30 s and does nothing, while its help says it adapts the cluster. | `pkg/operator/controller.go:44-49`, `cmd/operator.go:16` | Low, user-visible |
| B14 | Demo credentials on a command line: `kubectl exec … env PGPASSWORD=postgres psql -c "ALTER ROLE …"`, retried five times with the error dropped; `podrun` exists to avoid this. | `cmd/deploy_components.go:1305` | Low |
| B15 | `deploy` deletes `~/.kube/cache/discovery` for every cluster the user has. | `cmd/deploy_components.go:1060-1062` | Low |
| B16 | `--topology`/namespace flags are declared three times with different sets; `clean` lacks `--ui-ns`, so a Kafka UI deployed with it is never uninstalled; `deploy status` mutates `deployMM2NS`. | `deploy.go:113-121`, `deploy_status.go:40-50,207`, `clean.go:52-61` | Medium |
| B17 | In CI (no dashboard) `silent_wait.go` prints nothing while polling, so logs go quiet for up to 15 minutes. | `cmd/silent_wait.go` | Low |

Dead code with no non-test caller: `retryableHelmDeploy`/`DeployError` (`deploy_recovery.go`), `detectDeployProxySettings`, `loadProxyConf`, `rewriteLoopbackProxyURLForKind`, `ensureKubernetesNoProxy`, `appendUniqueCommaSeparated` (`deploy_components.go:53-203`), `helm.Client.{Status,DependencyBuild,Install,Upgrade,Uninstall,Test,IsDeployed}`, `kubectl.Client.Namespace`, the `clean.go` "jaeger" entries.

### C. Test, report, gate, chaos, security, conformity

| Id | Finding | Where | Severity |
|:--|:--|:--|:--|
| C1 | JUnit `time` attributes are not seconds: a step's `TimeToAllReady` is `interface{}` and the backend formats it as `"1234ms"`, `"PT1.5S"` or a number; the suite's `time` is `time.Now().Unix()`. Jenkins and GitLab reject or zero these. The only test checks the file contains `<testsuite`. | `cmd/junit.go:62-63, 103`, `client/types.go:699` | High |
| C2 | `kates gate` is not `interruptible`, polls with a bare `time.Sleep`, and on `--timeout` neither cancels the run nor bounds `CreateTest`. Ctrl-C kills the process with the test still running. | `cmd/gate.go:38, 57, 78-93` | High |
| C3 | `kates benchmark` swallows every poll error (`continue`), has a hidden 6-minute cap (120 × 3 s) and no `--timeout`, and is not interruptible. | `cmd/benchmark.go:32, 260-273` | High |
| C4 | `kates flow`: a step's `spec:` decodes into `client.TestSpec`, which carries only `json` tags, so `numRecords`, `recordSize`, … become zero; `gate.maxP99/minThroughput/maxErrorRate` and `expect` are parsed and never evaluated; `webhook`/`notify` steps print "notification dispatched" and do nothing; `checkGrade` maps any unknown or lower-case grade to "D"; `--wait` is never read. | `cmd/flow.go:36-49, 166, 182-185, 240-264, 319` | High |
| C5 | `kates resilience run` is a synchronous POST of up to 20 minutes on `context.Background()`; not interruptible; no run id printed until it returns. | `cmd/resilience.go:137`, `client/client.go:655` | Medium |
| C6 | `test watch --interval` is ignored in a terminal: the TUI tick is hard-coded to 2 s and the footer says so. | `cmd/watch.go:33-37, 124, 358` | Medium/high |
| C7 | `apply` evaluates `minThroughputRecPerSec` per task row (consumer row included) while `latencyOf` excludes the consumer row and `gate` uses the aggregate; the shipped `stress-test.yaml` therefore demands 100,000 rec/s from each of 16 producers. | `cmd/apply.go:559-568`, `cmd/latency.go:45-64`, `cmd/scenarios/stress-test.yaml:6,17` | Medium |
| C8 | Security audit appends Kyverno checks through raw `exec.Command` (bypassing the `runKubectlFn` seam) but the summary and the exported counts stay the backend's; the HTML and Markdown exports interpolate backend strings unescaped (`report_html.go` escapes; this file was written separately). | `cmd/security.go:38-155, 271-278, 359`, `cmd/security_export.go:35-40, 56-57, 150-151` | Medium |
| C9 | `truncate` slices bytes and splits UTF-8; used by every `security *` table and `watch`. | `cmd/security.go:335-340`, `cmd/watch.go:307` | Low/medium |
| C10 | `test list` divides by zero when a page reports `size: 0`; `writeConformityJUnit` indexes `[0]` of an empty split on an empty rule id. | `cmd/test.go:68-70`, `cmd/conformity.go:363` | Low |
| C11 | Terminal-status sets differ: `gate`/`benchmark` stop on `DONE|FAILED`; `watch`/`apply` also on `COMPLETED|ERROR`. | `gate.go:87`, `benchmark.go:268`, `watch.go:518`, `apply.go:682,766` | Low/medium |
| C12 | `disruption watch` uses the default 64 KiB scanner buffer (a large `METRICS_CAPTURED` event ends the watch with "token too long") and exits 0 when the stream closes early. | `cmd/disruption_watch.go:58, 84` | Low/medium |
| C13 | `advisor --apply` help says it generates a tuned scenario; it prints one hint. `cost --producers` is unused and `--replicas 0` yields a negative cost. `changelog --until` ignores its parse error. `snapshot` joins the user's name into a path unsanitised. `badge` builds a shields.io URL whose `N/A` value contains `/`. | `advisor.go:175-177,311`, `cost.go:117`, `changelog.go:45`, `snapshot.go:248,252`, `badge.go:88-90` | Low |

Conformity (`cmd/conformity*.go`, `internal/conformity`) is the model the rest should follow: lint before send, documented exit codes 1/2, clean `-o json`, diagnostics with file:line:col and caret, examples tested against the schema.

### D. The MCP server (`kates mcp`)

The guard is a single choke point and the read-only transport, cluster pin, size cap, fence check and panic recovery are all real and tested. No tool argument can escape the transport, no secret appears in a result, every list is capped. The findings are second order.

| Id | Finding | Where | Severity |
|:--|:--|:--|:--|
| D1 | `newMCPClient` keeps the CLI client's `MaxRetries: 3`; `GET /api/security/audit` runs an audit per request, so one `security_evidence` call can run three audits while `auditRuns` reports one. The harness sets `MaxRetries=1`, so tests never see it. | `cmd/mcp_guard.go:1323-1351`, `client/client.go:71,203-230`, `cmd/mcp_tools_security.go:338-341` | Medium |
| D2 | `kates_activity` returns the caller-controlled request path unfenced in `audit[].target`; a key holder can plant prose that reaches the model in the tool `did_kates_cause_this` asks for first. Alert-rule `name`/`source` are unfenced too. | `cmd/mcp_tools_runs.go:2004`, `cmd/mcp_tools_cluster.go:282-284` | Medium |
| D3 | `security_evidence` returns a silent empty page for an `offset` past the end; `cluster_topology` refuses the same with `KATES_INVALID_ARGUMENT`. | `cmd/mcp_tools_security.go:400-418`, `cmd/mcp_tools_cluster_topology.go:548-550` | High (for an agent caller) |
| D4 | `preview_disruption` decodes the topology with the full `client.ClusterTopology`; a type change in any of its ~20 sections fails the decode and the controller/quorum warnings vanish silently. `MCPClusterTopology` exists for exactly this. | `cmd/mcp_chaos_preview.go:518,530,845-849` | Medium |
| D5 | `draft_scenario.name`: schema `MaxLength: 200` counts code points, the handler counts bytes. | `cmd/mcp_tools_scenario.go:161-163, 417` | Trivial |
| D6 | `tools/list` is 237 KB (about 60k tokens on every turn); caveat texts of 900–1,900 characters ride in every result and count against the 48 KB budget; pagination vocabulary differs per tool (`page/size`, `offset/limit`, `from_partition`, none). | `cmd/testdata/mcp/tools/*`, `cmd/mcp_caveats.go:74-88` | UX |
| D7 | No `list_topics` / `list_consumer_groups` tool; the cluster prompt apologises for the gap. | `cmd/mcp_prompts_cluster.go:135` | UX |

### E. Migration, detection, TUI

| Id | Finding | Where | Severity |
|:--|:--|:--|:--|
| E1 | The HTML detect report prints an **invented** inter-AZ latency matrix (hard-coded 0.05/0.08/0.12 ms with zone-name special cases) for any multi-zone cluster, ignoring the measured `report.Network.LatencyMatrix`, including when the probe failed or was skipped. | `pkg/detect/renderer_html.go:506-566` | High |
| E2 | The HTML renderer never escapes: node names, check details, remediation commands with `<node-name>` placeholders are emitted raw and vanish inside `<pre>`. | `pkg/detect/renderer_html.go:325, 490-498, 580-586` | High |
| E3 | The PDF uses cp1252 core fonts with UTF-8 text (`≥`, `—`, `✓`, node names) and truncates by byte. | `pkg/detect/renderer_pdf.go:207,262,336,453,465,534,663,699` | High |
| E4 | The internal DNS probe hard-codes `cluster.local` though the cluster domain was detected one stage earlier; a custom-domain cluster reports 0 % internal success. | `pkg/detect/collector.go:2299, 1428-1456` | High |
| E5 | Zones come from map iteration, unsorted; `valuesgen` takes `zones[:3]`, so which zones get broker pools, and the pool order, changes run to run. | `pkg/detect/collector.go:396-414`, `pkg/detect/valuesgen.go:200-202, 583-596` | High |
| E6 | `--generate-values` emits the chart's deprecated 0.4 schema (`controllerPools`, `brokerPools`, `strimziOperator`, `kafkaConnect`, `crdUpgrade`, `drainCleaner`); the chart ignores three of those blocks and fails on `drainCleaner.enabled`. | `pkg/detect/valuesgen.go:380-468`, `charts/kafka-cluster/values.schema.json:2160-2200`, `templates/_resolve.tpl:165-183` | High |
| E7 | "Merge with base": the generated overlay wins every scalar and replaces every list, so a base `-f` with `cruiseControl.enabled: true` or custom pools is silently reverted. | `pkg/detect/renderer_values.go:155-172` | High |
| E8 | Quantity parsing: `parseMemory("512Mi")` → 0, float CPU → 0, node memory in `Mi`/`Gi` → 0 (only `memoryMiB` is correct); `ParseValuesYAML` reads keys the chart does not have. | `pkg/detect/yaml_parser.go:35-88`, `pkg/detect/collector.go:377-378, 1841` | Medium |
| E9 | Controller sizing divides by `ControllerReplicas` (1 or 3) while the pools emitted are one per zone. IOPS verdict labels name-based estimates as "measured" and passes if any class passes. `Verdict.Warns` counts warnings with no row, so `--fail-on-warning` can exit 1 invisibly. | `valuesgen.go:121-140, 655-702`, `analyzer.go:112-125, 242-247` | Medium |
| E10 | Read-only mode still `kubectl exec`s into the first Running pod cluster-wide to read `resolv.conf`, with no timeout; the storage bench runs `sh -c` with a heredoc and blocks on `kubectl logs -f`. | `pkg/detect/collector.go:1437-1444, 1969-2013` | Medium |
| E11 | `cmd/detect.go` calls `os.Exit` inside `RunE`, skipping deferred `Close`/`cancel`; `--output-file` with `--fail-on-error` can exit before the file is flushed. | `cmd/detect.go:89,143,292,295` | Low/medium |
| E12 | Migration: `topicsFromPattern` drops any `topicsPattern` part with regex metacharacters, so a mirror installed with a real pattern yields zero topics and `down` deletes nothing while saying it did; `kafkaTopicResourceName` maps `a.b`, `a-b`, `A_B` to one name, so the second KafkaTopic overwrites the first. | `cmd/migrate_up.go:338-347, 277`, `cmd/migrate_lab.go:1006, 1111-1126` | Medium |
| E13 | Migration, smaller: the rollback overlay is written under the release name but removed under the lab name; `ParsePins` keeps quotes and trailing comment on a quoted value; SASL is inferred from port 9094 only. | `migrate_mirror.go:384`, `migrate_verify.go:669`, `pins.go:80-85`, `migrate_lab.go:509-511` | Low |
| E14 | TUI: the brokers tab drops every key while `info == nil`, so `r` cannot retry after a load error; the filter accepts only single-byte keys, so non-ASCII topic names cannot be typed (`produce.go` already fixed this); the groups list has no scroll window; the lab's derived test duration can exceed its own poll deadline (1M records → 16.7 min against a 15 min wait). | `tui/brokers.go:64-67`, `tui/tui.go:193-196`, `tui/groups.go:164-182`, `tui/lab.go:1022-1026, 1090-1103` | Medium |
| E15 | `deploy_dashboard` shares `*componentStat` between `Update` and the poll goroutine (a race under `-race`); it is the one full-screen TUI without the too-small guard. | `cmd/deploy_dashboard.go:456, 406-417, 341-357` | Low |

Migration's rails are good: every mutating command confirms unless `--yes`, `plan` and `mirror deploy --dry-run` exist, `run` tears down on Ctrl-C, overlap and existing-topic refusals fail closed, `MetadataFor` cannot skip a version in the window. Gaps: `cutover` has no lab-level dry run and no RUNNING pre-check; `down` deletes data topics with no `--keep-topics` and no second confirmation when they hold records.

## 3. Cross-cutting themes

1. **Exit codes lie.** `status`, `doctor`, `doctor dns/network`, `clean`, `ports`, `kyverno *`, `disruption watch` (early close) return nil after a failure. Conformity's documented 1 = failed, 2 = could not tell is the policy to generalise.
2. **Long-running commands cannot be stopped.** Four commands carry the `interruptible` annotation (`test create`, `test apply`, `replay`, `disruption run`); `gate`, `benchmark`, `flow`, `resilience`, `detect`, `dashboard` and every deploy wait use `context.Background()`.
3. **Five poll loops, four grade scales, six Helm-status parsers, eight exec seams, four CRD lists, four quantity parsers, four hand-written detect renderers.** Each copy has drifted (terminal-status sets, row filters, escaping). The refactors in Wave 3 collapse each family to one.
4. **Artefacts built by string formatting.** JUnit (`%v` of a duration), HTML (no escaping in two of three writers), PDF (wrong font encoding), Helm values (deprecated schema, overlay merge), JSON patches (`%s`). Each needs its typed writer or template.
5. **`-o json` has no contract.** Honoured by some commands, ignored by others, corrupted by `Warn`/`Hint` going to stdout, broken by a banner in one.
6. **Tests stub the failure away.** `deploy_test.go` makes every read healthy and `isTesting` skips every wait, so no readiness timeout, secret-copy failure or concurrency property is ever exercised; `kyverno_test.go` runs real `kubectl`; `internal/helm` tests only `buildArgs`.

## 4. The plan

Four waves. Each item names its findings, the files, the acceptance test, and an effort (S < half a day, M one to two days, L a week). Waves 1 and 2 are defect fixes and can ship as several small PRs; Wave 3 is the refactoring that stops the defects recurring; Wave 4 is new capability.

### Wave 1 — shipped with this plan

Small, verified, each with a test. All on `cli/`; none changes a command's flags.

| Item | Findings | Change |
|:--|:--|:--|
| 1.1 | A1, A10 | `registerPlugins` runs from `Execute` after every built-in is registered and skips a name an existing command already has (one warning on stderr); a plugin's `*exec.ExitError` becomes `exitError{code}` so the CLI exits with the plugin's code. |
| 1.2 | A2 | `installBinary` writes `<dst>.tmp` and renames; the sudo path copies to a temp file and `mv`s. |
| 1.3 | A3, A5 | `doctor`: banner only in table mode; camelCase JSON keys; exit 1 when a required check fails (Kyverno stays a warning). |
| 1.4 | A4 | `status`: `-o json`, exit 1 when unreachable, prints the URL it actually used. |
| 1.5 | A6 | `profile save` refuses a run that is not DONE or has no results; `profile assert` treats a zero baseline as an error; profile names must match `^[A-Za-z0-9._-]+$`. |
| 1.6 | A8 | Completions and `version`'s API lookup use a 2 s context. |
| 1.7 | B1, B10 | `runPorts` stops only forwards whose `--context` is the one it uses and whose namespace is one of ours, through `stopPortForwards`; exits 1 when a forward failed. |
| 1.8 | B4 | `clean` collects every failed step and ends with one `cmdErr` naming them; the success line prints only when nothing failed. |
| 1.9 | B5 | `kyverno enforce/audit/status/violations` return the error; `kyverno apply` counts policies correctly. |
| 1.10 | B3 | One `copySecretPassword(ctx, name, from, to) error` used by the five copies; a failure is reported at once with kubectl's stderr. |
| 1.11 | B2 | `execMutex` covers only the printing in `runExecDefault`'s non-verbose branch; verbose keeps the lock (it streams to the terminal). Test: two stubbed Helm calls in Group A overlap. |
| 1.12 | B11 | `connect scale` parses the replica count with `strconv.Atoi` and marshals the patch. |
| 1.13 | C1 | JUnit step `time` parsed with `client.ParseBackendDuration` into seconds; suite `time` is the sum of the step times (the disruption report carries no start or end); a step with no outcome is an `<error>`, not a `<failure>`; golden-file test. |
| 1.14 | C2 | `gate` is `interruptible`, uses `cmd.Context()`, sleeps through the context, and cancels the run it started on Ctrl-C or `--timeout`. |
| 1.15 | C3 | `benchmark` is `interruptible`, counts consecutive poll errors (three in a row fail the phase), gains `--timeout` (default 30 min) replacing the hidden cap. |
| 1.16 | C4 | `client.TestSpec` gains `yaml` tags matching its `json` names; `flow` evaluates `maxP99`, `minThroughput`, `maxErrorRate` (reading the run's report), refuses `expect` and `webhook`/`notify` steps as not implemented instead of pretending, normalises grades to upper case and fails an unknown grade. |
| 1.17 | C6 | `watch`'s TUI tick uses `pollInterval`; the footer prints it. |
| 1.18 | C8, C9 | Security HTML export escapes with `html.EscapeString`, Markdown escapes `\|`; `truncate` uses `runewidth.Truncate`. |
| 1.19 | C10, C12 | `test list` guards `size == 0`; `writeConformityJUnit` guards an empty rule id; `disruption watch` sets a 1 MiB scanner buffer and exits 1 when the stream ends without a terminal event. |
| 1.20 | D1, D3, D5 | `newMCPClient` sets `MaxRetries = 1` and the audit caveat says so; `security_evidence` refuses `offset >= matched` with `KATES_INVALID_ARGUMENT`; `draft_scenario` counts runes. |
| 1.21 | E1, E2 | HTML detect report renders the measured `LatencyMatrix` (and omits the section when there is none) and escapes every value with `html.EscapeString`. |
| 1.22 | E3 | PDF renderer installs `UnicodeTranslatorFromDescriptor("")` and truncates by rune. |
| 1.23 | E4, E5, E8 | DNS probe queries `kubernetes.default.svc.<ClusterDomain>`; `groupNodesByZone` sorts by name; `parseNodes` memory through `memoryMiB`. |
| 1.24 | E11 | `cmd/detect.go` returns `exitError` instead of calling `os.Exit`. |
| 1.25 | E14 | Brokers tab accepts `r` and `q` with no data; the filter accepts rune keys; groups list scrolls. |

### Wave 2 — remaining defects (2–3 PRs, about a week)

| Item | Findings | Change | Effort |
|:--|:--|:--|:--|
| 2.1 | A7 | `doctor dns --fix` reads the current `searches` and appends. | S |
| 2.2 | A9 | `lockConfig` split into `_unix.go` / `_windows.go` (`LockFileEx` via `golang.org/x/sys`); same for the port-forward process list. CI job cross-compiles `GOOS=windows`. | S |
| 2.3 | A11 | `doctor network`: one context per probe, `runEphemeralPod` returns an error on non-zero exit, probes run in parallel with a `--timeout`. | M |
| 2.4 | A12 | `ctx set` merges: only fields whose flags were passed change; `--replace` restores today's behaviour; `ctx delete` picks the next context deterministically (most recently used, else alphabetical). | S |
| 2.5 | B6 | `kyverno apply` resolves the kates release's namespace from `deployedReleases`, accepts `--app-ns`/`--topology`, and runs `helm --dry-run` for real. | M |
| 2.6 | B7 | `readInstalled` distinguishes `NotFound`, `Forbidden` and other errors; an unclassified error refuses the deploy unless `--yes`. | S |
| 2.7 | B8 | `cleanupStaleClusterResource` deletes only a ClusterRole whose annotations name a release that no longer exists. | S |
| 2.8 | B9 | `deploy status` reads `helm status -o json` and reports the real status; a release in another namespace is listed as "elsewhere", not adopted; all calls go through `runExecOutputFn`. | M |
| 2.9 | B16 | One `addDeployScopeFlags(cmd)` registering topology and namespace flags for `deploy`, `deploy status`, `clean` and `kyverno apply`; adds `--ui-ns` to `clean`; `deploy status` stops mutating globals. | M |
| 2.10 | B17 | `waitComponentReadySilent` prints pod phase counts every 30 s when there is no live dashboard. | S |
| 2.11 | C5 | `resilience run` becomes `interruptible`; the client prints the run id as soon as the backend assigns one (the API returns it in the first response or a `Location` header; confirm with the backend team), and Ctrl-C cancels through the existing cancel endpoint. | M |
| 2.12 | C7 | `apply`'s SLA gates evaluate the report aggregate (same row filter as `latencyOf`); a `perTask: true` key keeps the old semantics; `stress-test.yaml` updated; the book's scenario chapter updated. | M |
| 2.13 | C8 | Security audit routes Kyverno probes through `runKubectlFn` and recomputes `summary` after injecting checks. | S |
| 2.14 | C11 | One `terminalRunStatus(status) bool` used by every poller. | S |
| 2.15 | C13 | `advisor --apply` writes the tuned scenario file or the flag is removed; `cost` validates inputs; `changelog --until` errors; `snapshot` validates names; `badge` URL-escapes. | S |
| 2.16 | D2, D4 | Fence `audit[].target` unless it matches `mcpIDRE`; fence alert `name`; a reflective test that every unfenced string field is on an identifier allow-list; `preview_disruption` decodes `MCPClusterTopology`. | S |
| 2.17 | E6, E7, E9 | `valuesgen` emits the chart's 1.x `nodePools` shape; controller sizing uses the pool count it emits; a `helm template --values generated.yaml` test like `TestMirrorValuesRenderWithHelm`; `RenderValuesFromBase` applies only detected facts (storage class, zones, cluster domain, pools) over the base and prints what it overrode. | L |
| 2.18 | E9, E10 | IOPS verdict says "estimated" for name-based numbers and judges the default class; `Verdict.Warns` counts rows; the `resolv.conf` read targets a pod in the kates namespace with a 10 s timeout, or is skipped in read-only mode; the storage bench runs through `podrun` with a bounded `logs` read. | M |
| 2.19 | E12, E13 | The lab records its topic list in a `kates.io/lab-topics` annotation on the CR at install and `discoverLab` reads it; `kafkaTopicResourceName` appends a short hash when sanitising changed the name; rollback overlay keyed by lab name; `ParsePins` strips the comment before the quotes; SASL inferred from the listener's `authentication`, not its port. | M |
| 2.20 | E14, E15 | Lab test duration capped at the poll deadline minus a margin (or the deadline derived from the duration); `deploy_dashboard` copies ids/targets into the Cmd and gets the too-small guard. | S |

### Wave 3 — refactoring that keeps the defects from coming back (3–4 weeks)

| Item | Theme | Change | Effort |
|:--|:--|:--|:--|
| 3.1 | Exit codes | `cmd/exitcodes.go` documents 0/1/2/130; a test walks every leaf command's `RunE` for `return nil` after `output.Error` (an AST test, like `TestCommandProfilesCoverEveryCommand`). | S |
| 3.2 | `-o json` | `output.Render` becomes the only path: it redirects `Warn`/`Hint`/`Success` to stderr while JSON is active and refuses `Banner`; a test runs every leaf command with `-o json` against a stub API and asserts stdout parses. Commands with no JSON form say so and exit 2. | M |
| 3.3 | Interrupts | `followRun(ctx, id, opts)` in `cmd/runner.go`: jittered backoff on errors, `terminalRunStatus`, optional deadline, `cancelOnStop`; adopted by `gate`, `benchmark`, `flow`, `apply`, `test create`, `watch`'s plain path. Every command that starts work is `interruptible`. `cmd.Context()` replaces `context.Background()` in `cmd/` (488 sites, mechanical). | M |
| 3.4 | Grades | `pkg/grade`: the backend's scale, one `Compare(a, b)`, used by `gate`, `benchmark`, `flow`, `badge`, `security gate`; thresholds printed in `--help`. | S |
| 3.5 | Exec seams | One `procRunner` interface (`Run`, `Output`, `OutputStdin`, `Stream`) implemented by the OS runner and the test fake; `internal/helm` and `internal/kubectl` become thin typed wrappers over it (and `kubectl.Client` honours or loses `Namespace`); `pinKubeContext` wraps one thing; `deploy_status`, `kyverno*`, `connect`, `security` and `doctor_*` move onto it. Delete the dead `helm.Client` methods. | L |
| 3.6 | Deploy orchestration | `deployGroupA/B/C` and `deployKafkaConnectStack` become a table of `component{name, ns, deps, install, wait}` executed by one scheduler with a real errgroup; `runDeploy` keeps only resolution and reporting. Eight "already deployed" blocks and the four Secret copies disappear into the scheduler. Tests get a fake runner that can fail any step and a clock, so timeouts are exercised. | L |
| 3.7 | CRD and release tables | One table in `pkg/strimzi` (plus `monitoring`, `litmus`, `certmanager`) listing CRD groups, release names and namespaces, consumed by `clean`, `deploy`, `deploy status` and `strimzi`. Six Helm-status parsers become one `helmReleases(ctx, ns)` reading `helm list -o json`. | M |
| 3.8 | Detect renderers | One `ReportDoc{Sections, Tables, Verdict}` built once; HTML through `html/template`, Markdown/TUI/PDF as views of the same document; the six zone-matrix builders and five cert-expiry counters become one each. `RenderPDF` (706 lines), `RenderHTML`, `RenderTUI`, `RenderMarkdown` fall under the lint limits. `Collector` gets a context-aware executor with per-call timeouts and `kubectl get deployment -A` is fetched once. The four quantity parsers become `pkg/detect/quantity.go` (use `k8s.io/apimachinery/pkg/api/resource` if the dependency is acceptable, else keep `memoryMiB` and extend it). | L |
| 3.9 | Flags | `cmd/flags.go` with the shared sets (`--records`, `--timeout`, `--wait`, output file `-f`) and a test that a flag name means one thing everywhere; `report export` gets `-f` and one writer honouring `IsInteractive()`. `deployKafkaName`-style shared globals become per-command fields. | M |
| 3.10 | TUI | `scrollWindow(cursor, count, height)` shared by topics/produce/groups; a `bubbles/key` keymap per model from which `help.go` is generated; glyphs through `output.Glyphs()`; `cmd/dashboard.go` becomes a Bubble Tea model with a ticking command and a timeout per API call; `LabModel.Update` split per state. | M |
| 3.11 | MCP | `mcp_ident.go` with the shared regexes, type lists, `mcpBounds` and a generic input-schema builder; one `mcpSection` embed and one `mcpShrink` helper replacing six fit loops; `mcp_tools_runs.go` split into list/get, assess, activity. | M |
| 3.12 | Lint | Document which golangci-lint build reproduces CI locally (the pinned v2.14.0 must be built with Go ≥ 1.27); make the funlen waiver list per function (`//nolint:funlen // reason`) rather than per file so new code in `deploy_components.go` is covered; add `errcheck` on `cmd/` once 3.5 lands. | S |
| 3.13 | Tests | `internal/helm` and `internal/kubectl` to ≥ 70 % through the fake runner; `deploy_test.go` gains failure-path tests (readiness timeout, secret copy refused, dependency update failed, Group A concurrency); `kyverno_test.go` uses the fake; `client` retry tests (5xx then 2xx, cancel during backoff, non-GET never retried); `tui` tests for `tui.go`, `groups.go`, `produce.go`, `toosmall.go`; `pkg/detect` tests for escaping, Unicode in PDF, zone determinism, `yaml_parser`, `RenderValuesFromBase`. Target: `cmd` 70 %, `tui` 60 %, `internal/helm` 70 %. | L |

### Wave 4 — new capability

| Item | Change | Why | Effort |
|:--|:--|:--|:--|
| 4.1 | `kates test lint <scenario.yaml>` mirroring `conformity lint`: `parseScenarioFile` + `scenarioSpecProblems` + `validTestTypes` + unknown-key detection (`KnownFields(true)`), with file:line diagnostics; `apply` runs it before the first POST. Same for flow files. | A typo in scenario 3 should not fail after scenarios 1 and 2 ran. | M |
| 4.2 | `kates apply --junit <file>`: one testcase per scenario, SLA violations as failures. | CI gets the same artefact `disruption` and `conformity` already produce. | M |
| 4.3 | `kates migrate down --keep-topics` and a second confirmation when a mirrored topic holds records (end offsets through the existing target client). | Topics are user data; today one `--yes` deletes them. | M |
| 4.4 | MCP: `list_topics` and `list_consumer_groups` (plain GETs already under `/api/cluster`); shorter schema prose with the long form in `kates://docs/mcp/<tool>` resources (target `tools/list` ≤ 80 KB with a golden size cap); short caveats in results with the full text in `kates://caveats`; `disruption_report.steps` paged; one cursor vocabulary (`offset/limit/nextOffset`); a polling-cadence hint in the `diagnose_run` prompt. | 60k tokens per turn before the first call is the biggest cost an agent user pays. | M |
| 4.5 | Plugin contract: refuse collisions (done in 1.1), pass `KATES_URL`, `KATES_API_KEY`, `KATES_CONTEXT`, `KATES_OUTPUT` resolved by the pre-run into the plugin's environment, document it in the book. | A plugin today gets none of the context resolution the CLI did. | S |
| 4.6 | `status --watch` reusing the one-line format with `ClearFrame`; `doctor --timeout` and `--kyverno=false`. | | S |
| 4.7 | Typed JSON fields in `client/types.go` (`json.Number` or custom unmarshal for the `interface{}` ids, ports and durations); `ParseBackendDuration` applied at decode time. | Removes a type switch from every caller and the `mcpInt` helper. | M |
| 4.8 | Remove `kates operator` or implement it against a stated reconciliation contract. | A command that prints "Reconciling" and does nothing misleads. | S |
| 4.9 | Migration `cutover --dry-run` at the lab level, with the RUNNING pre-check, and `status --watch` saying that offsets are off. | | S |

## 5. Verification for every PR

- `go build ./... && go vet ./... && go test ./...` from `cli/`, plus `go test -race ./tui/... ./cmd/...` for anything touching a Bubble Tea model.
- `GOOS=darwin` and `GOOS=windows` builds once 2.2 lands.
- For a command whose exit code changed: a test through `rootCmd.SetArgs` asserting the `exitError` code.
- For an artefact writer (JUnit, HTML, PDF, values): a golden file under `cmd/testdata` or `pkg/detect/testdata`, and for HTML a parse through `golang.org/x/net/html`; for values, `helm template`.
- For a refactor in Wave 3: the same golden files unchanged.

## 6. Ownership and order

| Who | What | When |
|:--|:--|:--|
| This PR | Wave 1 | now |
| CLI maintainer, 2–3 PRs | Wave 2, in the order of the table (exit codes and interrupts first, `valuesgen` last since it needs the chart schema) | next two weeks |
| CLI maintainer, with the backend team for 2.11 (`resilience` run id) | Wave 2.11 | when the API exposes the id |
| CLI maintainer, one PR per row | Wave 3, 3.1–3.5 first (they unblock the tests in 3.13), then 3.6–3.8, then the rest | following month |
| As demand appears | Wave 4 | after Wave 3 |

The one rule for new code while the waves run: a command that starts work on the cluster or the API is `interruptible` and uses `cmd.Context()`; a command that fails returns a non-nil error; output goes through `output`; a process runs through a seam. A review checklist item for each of these costs nothing and stops the backlog growing.

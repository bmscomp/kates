# Kates CLI: performance and stability review and plan

| | |
| --- | --- |
| **Status** | Proposal, 2026-10-18. Decisions D1–D4 taken 2026-10-18 (§5): the recommended pick each time |
| **Audience** | The maintainer |
| **Baseline** | `origin/main` at `6f4cf61` (after #387). Every `file:line` below is against that commit. This checkout's branch is 13 CLI commits behind it |
| **Builds on** | The CLI robustness plan of 2026-09-28 (wave 1 shipped as #249, #250, #270–#277). Nothing fixed there is reported again here; §8 maps the overlap |
| **Changed** | Nothing in the product. Reproductions ran on a scratch export of `main` |

Each finding is marked:

- **VERIFIED**: reproduced by a throwaway test, a stub server or a stub `kubectl`, a race run or a measurement. The method is named.
- **READ**: confirmed by reading the code on `main` (and the backend or chart where it matters), not run.
- **PLAUSIBLE**: follows from the code and well-known behaviour of Go, Helm or kubectl, but was not run.

---

## 1. Summary

The CLI is in better shape than its size suggests.

- **Races.** `go test -race ./...` on `main` finds no data race, and `go vet` is clean.
- **Context use.** Every HTTP request carries a context, and response bodies are always closed.
- **Startup.** A command starts in 11–16 ms.
- **Command checks.** The noctx linter flags 8 call sites, and bodyclose flags none.

The problems are in four places:

1. **Runs the CLI starts and then stops watching.** `gate`, `benchmark` and `flow` never cancel a run they gave up on. `test apply --wait` turns a 1.5 s API blip into an ERROR row and starts the next scenario on top of the run still going.
2. **Waits that multiply.** The client retries a GET even after its 60 s timeout. One call to a hung API therefore takes 181.5 s, and `kates version` took 181.8 s. TAB completion and every poll loop pay that cost, times their own retries.
3. **The deploy flow trusts too little and too much at once.**
   - It treats a pod in phase `Running` as ready, even in CrashLoopBackOff.
   - `--with-chaos=false` leaves the Kates API configured for Litmus.
   - Ctrl-C in the dashboard hides the screen while helm keeps installing. A second Ctrl-C kills helm mid-install.
   - Deploy can leave the cert-manager webhook fail-open.
4. **Cleanup that only `defer` performs.** Only 5 commands handle Ctrl-C. Everywhere else the default SIGINT ends the process before deferred cleanup runs. Migrate client pods (with a SCRAM password written into them), detect probe namespaces and the like stay behind.

**Top 5 findings:**

| # | Finding | Status |
| --- | --- | --- |
| F1 | `gate`, `benchmark` and `flow` abandon the runs they start. `benchmark` exits 0 with ERROR rows | VERIFIED |
| F2 | `test apply --wait`: one failed poll makes the scenario ERROR, leaves its run going, and starts the next scenario | VERIFIED |
| F3 | A hung API costs 181.5 s per GET: the client retries after a timeout. TAB completion and `kates version` hang | VERIFIED |
| F4 | Deploy readiness is pod phase `Running`, not the `Ready` condition | READ |
| F5 | `kates deploy --with-chaos=false` installs no Litmus but leaves the Kates API on `litmus-crd`. Chaos steps are skipped with an ERROR log, and compound runs fail | READ (CLI, chart and backend) |

Then, in order:

- F6: a malformed `~/.kates.yaml` sends commands to `localhost:8080` without the key.
- F7: Ctrl-C in the deploy dashboard does not stop the deploy.
- F8: the Brokers tab panics when a broker disappears.
- F9: detect, and with it deploy preflight, can hang forever on one `kubectl`.

§4 splits the work into 7 waves of 51 items, each one small PR (a few split into groups). Wave 1 is 13 independent quick wins (XS–S), and none waits on a decision (D1 is taken: `noop`).

---

## 2. Method

**Scope.** `cli/` on `main`: `cmd/`, `client/`, `internal/{kubectl,helm,podrun}`, `pkg/*`, `tui/` and `output/`, plus the Makefile targets the CLI uses.

**Reviewers.** Four read-only reviewers covered disjoint areas, and every high finding was re-checked in the code by hand:
- the client and API waits;
- startup, output and exit codes;
- deploy and subprocesses;
- the TUIs, MCP and detect.

**Test isolation.** Tests ran with `HOME` and `KUBECONFIG` pointed at empty scratch files, and with stub `kubectl` and `helm` on `PATH` where a command shells out.

**One isolation lapse.** One reviewer ran `kates health` and `kates test delete <made-up id>` without the stub URL. Both reached the local Kates API through the user's `kubectl port-forward` on 8080. The API refused the DELETE with 401.

**Measurements** (appendix A has the numbers):

| Check | Result |
| --- | --- |
| `go vet ./...` | clean |
| `go test -race -count=1 ./...` | no data race. `cmd` takes 164 s under `-race`. The only failures came from tests that read `kates/src`, `kates/k8s`, `charts`, `scenarios` and `plans`, which a `cli/`-only export lacked; all pass once those are exported |
| `go test ./cmd` | 714 tests, 27.7 s test time, 38.6 s wall. The slowest test takes 2.6 s. Not a problem |
| golangci-lint 2.14, targeted | noctx 8, bodyclose 0, nilerr 8, contextcheck 27 (false positives: the `if ctx == nil { ctx = Background() }` test seam), errcheck 213, gosec G204 31 and G306 24 |
| Startup | `GODEBUG=inittrace=1` and pprof over 120 runs |

---

## 3. Ranked findings

Impact is what a user loses × how likely it is. H = high, M = medium, L = low.

### 3.1 High

**F1. `gate`, `benchmark` and `flow` abandon the runs they start (H, VERIFIED with a stub API).**

Where:
- `cli/cmd/gate.go:38` (`context.Background()`), `:57` (CreateTest), `:79-92` (poll loop; the timeout and the poll error both `return fail(...)` with no cancel).
- `cli/cmd/benchmark.go:32`, `:260-272` (`waitForCompletion`: a count of 120 polls; every GET error is `continue`d, so 401, 403 and 404 are polled to the end).
- `cli/cmd/flow.go:240-258` (180 × 2 s, errors swallowed).
- None of the three is `interruptible`.

What breaks: `gate --timeout 180` in CI expires, or one poll fails, and the LOAD run keeps producing and holds a concurrency permit. The next pipeline's `POST /api/tests` can get 429, and a CI-timeout SIGTERM does the same. The stub showed `Timeout after 1s — test still RUNNING; cancel requests sent=0`.
- `benchmark` against a hung API waits about 6 hours, because the budget counts polls and each poll can take 181 s (F3).
- After 120 polls answered 401 it records ERROR, SKIPs the rest and returns `nil` (`benchmark.go:117`), so it exits 0.

Fix: on every give-up path, call the existing `cancelStartedRun` (`cli/cmd/interrupt.go:100`), mark the commands `interruptible`, use a wall-clock `--timeout`, stop on `!HTTPError.Retryable()`, and return an error when any row is ERROR or FAILED. Wave 3, items 3.2 and 3.3.

**F2. `test apply --wait`: one failed poll becomes an ERROR scenario, and the next scenario starts on top of it (H, VERIFIED).**

Where: `cli/cmd/apply.go:748-770` (`waitForTestPlain` returns the first `GetTest` error). The TUI twin is `waitModel.Update`, `:668`. `:239-241` records ERROR and continues the loop; only the Ctrl-C branch at `:229-238` cancels.

What breaks: the API restarts or the port-forward flaps for 2.5 s. `GetTest` gives up after 3 attempts (1.5 s), so scenario 1 is ERROR but still running, and scenario 2 starts against the same cluster. Both results are wrong, and nothing says so. With the same 2.5 s outage, `pollUntilDonePlain` (`cli/cmd/watch.go:459`, used by `test watch`) rode it out and returned DONE.

Fix: one shared `waitRun` built from `pollUntilDonePlain`, used by both apply paths. On a wait that really fails, cancel the run and stop the batch, as the Ctrl-C path already does. Item 3.1.

**F3. A hung API costs 181.5 s per GET, and every loop above it multiplies that (H, VERIFIED).**

Where:
- `cli/client/client.go:68` (`Timeout: 60s`), `:71` (`MaxRetries: 3`), `:212-216`: a `hc.Do` error, a client timeout included, is retried.
- `:49` (30 s dial).
- `cli/cmd/autocomplete.go:16-130` (every completion uses `context.Background()`).
- `cli/cmd/version.go:49`.

What breaks:
- **A server that accepts and never answers.** Each GET takes 3 × 60 s + 1.5 s. Measured: `kates version` took 181.8 s, and `kates __complete test get ""` was still hanging when killed, so the user's shell freezes on TAB. A black-holed host costs 3 × 30 s dial (PLAUSIBLE: arithmetic, not dialled). Even a refused connection costs 1.5 s per call for the backoff (measured 1.52 s for `version` and for completion).
- **The pollers.** `awaitDisruptionFrom`'s "5 failures in a row" (`client.go:767,812`) becomes about 15 minutes. `pollUntilDonePlain`'s 10 retries (`watch.go:76`) become about 33 minutes.

Fix:
- Do not retry after a timeout: `errors.As(err, &ne) && ne.Timeout()`.
- Give probes (`version`, `status`, `health`, `doctor`, `top`, `dashboard`) a 5 s context.
- Give completion a 700 ms context and a single attempt.
- Let pollers ask for no inner retry, because their loops already retry.

Item 2.1.

**F4. Deploy treats pod phase `Running` as ready (H, READ).**

Where: `cli/cmd/silent_wait.go:14-41` reads `{.status.phase}` and accepts `Running` or `Succeeded`. It gates Strimzi, cert-manager, Kafka, monitoring, Kates (`cli/cmd/deploy_components.go:1696`), Postgres and chaos.

What breaks:
- A pod in CrashLoopBackOff, or one failing its readiness probe, is `Running`, so deploy reports success over a broken backend.
- On a re-run, helm reconciles Kates without `--wait`. The first poll sees the old pod still `Running`, so the wait passes before the new pod exists.

Fix: wait on the `Ready` condition (`kubectl wait --for=condition=Ready`). For Deployments, use `kubectl rollout status --timeout=…`. Item 4.3.

**F5. `kates deploy --with-chaos=false` leaves the Kates API configured for Litmus (H, READ across the CLI, chart and backend).**

Where:
- The flag: `cli/cmd/deploy.go:97`, default true. It is read at `cli/cmd/deploy_components.go:383` (namespaces), `:1110` (monitoring `chaosAlerts.enabled`) and `:1838` (Litmus install).
- The Kates release arguments at `:1677-1689` (and `katesChartValues`) never mention chaos.
- The chart has no chaos value: `charts/kates/templates/configmap.yaml` sets no `KATES_CHAOS_PROVIDER`, and the only knob is `extraEnv` (`charts/kates/values.yaml:275`).
- The backend defaults to `kates.chaos.provider=litmus-crd` (`kates/src/main/resources/application.properties:275`).

What breaks:
- **Coordinator path.** On the first chaos call `ChaosCoordinator` finds Litmus unavailable. It falls back to noop with an ERROR log ("Fault injection is DISABLED") and caches that choice for the pod's life. Disruption steps and resilience faults go through the coordinator, so they come back skipped, and the run ends PARTIAL rather than skipped by design.
- **Compound runs.** They resolve providers by name and bypass the coordinator (`CompoundChaosOrchestrator.java:49,113,198`), so they fail outright.
- **Built-in faults.** The fallback also turns off `POD_DELETE`, rolling restart and scale-down, which the kubernetes provider could run without Litmus.

Fix:
- Add a `chaos.provider` chart value, rendered as `KATES_CHAOS_PROVIDER` in the configmap.
- When `--with-chaos=false` and no `chaos` release exists, deploy passes `--set chaos.provider=noop`.
- The configmap checksum annotation (`charts/kates/templates/deployment.yaml:35`) rolls the pod whenever the value flips, which also clears the cached choice.

Item 1.1. Decided (D1): `noop`.

**F6. A malformed `~/.kates.yaml` sends commands to `localhost:8080` without the key (H, VERIFIED).**

Where: `cli/cmd/root.go:65-72` (`loadConfig` only warns), `:82-103` (a parse error returns empty contexts), `:317-318` (an empty current-context resolves to the built-in `localhost:8080` context).

What breaks: a YAML typo in a file whose current context is `prod`. Every command then talks to whatever listens on 8080 (a stale `kates ports` forward to another cluster, as happened during this review), with no API key. `ctx current` says "No active context". A DELETE goes wherever 8080 points.

Fix: when `readConfig` fails for any reason other than not-exist, and neither `--url` nor `KATES_URL` is given, set `apiClient = client.Failing(err)`, as #274 does for an unknown context. Item 1.2.

**F7. Ctrl-C in the deploy dashboard hides the UI but not the deploy, and every subprocess cancel is a SIGKILL (H, VERIFIED by test).**

Where:
- `cli/cmd/deploy_dashboard.go:141-142` creates a child context, and `:362-365` cancels only that child on ctrl+c.
- The deploy goroutine runs on the parent (`cli/cmd/deploy.go:283-322`), and `:373` waits on it.
- Bubble Tea owns SIGINT and SIGTERM while it runs.
- `exec.CommandContext` sends SIGKILL with no `WaitDelay` at `cli/cmd/deploy.go:418,453,459,463`, `cli/cmd/runner.go:50`, `cli/internal/helm/helm.go:38`, `cli/internal/podrun/runner.go:57`, `cli/pkg/strimzi/runner.go:40` and `cli/cmd/deploy_status.go:283,335,385,396,413`. Only `cli/internal/kubectl/kubectl.go:23-24` sets one (#249).

What breaks:
- A test showed `dashboard ctx cancelled=true; deploy ctx cancelled=false`. The user sees a blank terminal while installs run on for up to about 40 minutes.
- A second Ctrl-C ends kates, and helm is left with nobody waiting.
- When one branch of the Group A or B errgroup fails (`deploy_components.go:698,1127`), its siblings' in-flight `helm upgrade --install` is SIGKILLed. A release left `pending-install` blocks the re-run with "another operation in progress" (PLAUSIBLE: standard helm).
- A test showed a child with SIGINT handlers getting `signal: killed`. With a grandchild holding the pipe, a 1 s deadline took 6 s.

Fix: one command helper, modelled on `internal/kubectl`: `Cancel` sends SIGINT, and `WaitDelay` is 30–60 s for helm and 2 s for reads. Dashboard quit means "stop after the current step", and the screen says so (D2). Items 4.1 and 4.2.

**F8. The Brokers tab panics when the broker list shrinks (H, VERIFIED by test).**

Where: `cli/tui/brokers.go:116` indexes `m.info.Brokers[m.cursor]`. `:45-48` stores a refresh without clamping the cursor, and `cli/tui/tui.go:57` refreshes every 10 s.

What breaks: the cursor is on the last broker, and that broker dies or is scaled away. The next refresh panics with `index out of range [2] with length 2`, and the dashboard dies exactly when it is wanted.

Fix: clamp the cursor in the `brokersLoadedMsg` case. Item 1.3.

**F9. Detect, and deploy's preflight with it, can hang forever on one kubectl (H, VERIFIED with sleeping stubs).**

Where:
- `cli/pkg/detect/executor.go:27` uses `exec.Command` with no context.
- `cli/pkg/detect/collector.go:66` drops the errgroup's context (`g, _ :=`).
- Open-ended calls: `kubectl logs -f` (`:1884`), `kubectl run -i`, and `kubectl exec … apk add`. `collector.Preflight` runs inside `kates deploy` and `kates auto`.

What breaks:
- A stalled API server or a stuck exec stream blocks `Collect` with no timeout, because kubectl has no default `--request-timeout`. A test showed `Collect` still blocked 4.5 s after its context was cancelled.
- Ctrl-C works only because the default SIGINT kills the process, and that leaves the probe namespaces behind (F11).

Fix: pass a context to `Exec` with a per-call timeout and `WaitDelay`, use the errgroup context, and add `--request-timeout=30s` on reads. Item 4.4.

### 3.2 Medium

**F10. Deploy can leave the cert-manager webhook fail-open (M–H, VERIFIED by test).** `cli/cmd/deploy_components.go:886-904` sets `failurePolicy: Ignore` on both webhook configs, then restores `Fail` on `gCtx` and ignores the error. If a sibling branch failed, `gCtx` is already cancelled and the restore patch never runs. Later deploys skip cert-manager because its release exists, so the webhook stays `Ignore`. The comment says cert-manager reconciles it back; it does not, only helm does. Fix: restore on `context.WithoutCancel` with a timeout, registered by `defer` right after the bypass. Item 1.5.

**F11. Ctrl-C leaves cluster objects that only a `defer` would remove (M).**
- **migrate (VERIFIED with a stub kubectl and SIGINT at its default disposition).** Ctrl-C during `kates migrate target topics` ended the process; the log showed `apply`, `wait`, `tee /tmp/client.properties` and `exec`, and no `delete pod`.
  - Where: `cli/cmd/migrate_target.go:150-156`, `cli/cmd/migrate_up.go:303-307` and `cli/cmd/migrate_lab.go:765` all rely on `defer podrun.Delete(...)`.
  - The pod runs `sleep infinity` (`cli/internal/podrun/pod.go:36`) with no `activeDeadlineSeconds`, and holds the SCRAM password in `/tmp/client.properties` (`migrate_target.go:136`).
  - The pod `migrate up` makes for its pre-check carries no lab label (comment at `migrate_up.go:302`), so `migrate down` never removes it.
- **detect (READ).** The spinner swallows the first Ctrl-C as a key (`cli/pkg/detect/spinner.go:33-34`) while the scan goes on. The second Ctrl-C kills the process before the `delete ns` defers at `collector.go:951,1042,1826,1967` run. The prober pods run `sleep 3600`.
- **doctor network (PLAUSIBLE).** Its 30 s timeout SIGKILLs `kubectl run --rm` (`cli/cmd/doctor_network.go:375,438`), so `--rm` never runs.

Fix: make these commands `interruptible` (or use `signal.NotifyContext`, as `migrate_verify.go:1040` already does) so the defers run. As a backstop, give podrun pods `activeDeadlineSeconds`, create detect namespaces with their label in one manifest, and sweep stale ones at start. Item 4.5.

**F12. Deploy deletes `~/.cache/helm` (M, PLAUSIBLE, high confidence).** `cli/cmd/deploy_components.go:1013-1018` runs `os.RemoveAll` on it to "bust the discovery cache". On Linux that is Helm's repository cache. The later `helm upgrade kyverno kyverno/kyverno` (`cli/cmd/deploy_monitoring.go:96`) has no `repo add` before it, so it fails with "no cached repo found". The user's `helm search repo` is also emptied on every deploy. macOS keeps the Helm cache elsewhere. Fix: delete that line; `~/.kube/cache/discovery` is the cache meant. Item 1.4.

**F13. List endpoints are read as one page, so output is silently truncated (M, VERIFIED: request capture plus backend source).**
- `cli/client/client.go:348` (`Topics`) and `:370` (`ConsumerGroups`) send no page or size. The backend defaults to 50 and caps at 200 (`ClusterResource.java:79-87,133-140`) and returns a `total` the client drops.
- `Audit` (`:1133`) is capped at 200.
- `DisruptionList` sends `limit` (`:912`), which the backend ignores.
- So `kates cluster topics` shows 50 of 120, `-o json` included. `snapshot create` records 50 topics, and `snapshot diff` then reports the rest as removed. `chaos list --limit 5` prints 50 rows.

Fix: page with `size=200` until `total`, and send `size` in `DisruptionList`. Item 6.1.

**F14. Commands that fail and exit 0 (M, VERIFIED for most).**

| Command | Where | What happens |
| --- | --- | --- |
| `status` | `cli/cmd/status.go:45-47` | prints "unreachable", exits 0 |
| `kyverno enforce` and `audit` | `cli/cmd/kyverno.go:301,327` (same pattern at `:76,101,212,233`) | a failed `kubectl patch` exits 0 |
| `detect` | the `output.Error` paths at `cli/cmd/detect.go:91,158,171,179` | preflight or collect failure exits 0 without `--fail-on-error` |
| `ports` | `cli/cmd/portforward.go:105-108` | "Some port forwards failed" exits 0 |
| `security` audit | `cli/cmd/security.go:167` | `result["error"]` exits 0 |
| `connect test` | `cli/cmd/connect_test_integration.go:57-65` | a FAILED CDC test exits 0; the plain loop at `:260` never ends on a persistent error |
| `snapshot create` | `cli/cmd/snapshot.go:68-91` | with every read 401, saved `(0 brokers, 0 topics, 0 groups)`, exit 0 |
| `benchmark` | `cli/cmd/benchmark.go:117` | exits 0 with ERROR rows (F1) |
| `output.JSON` | `cli/output/output.go:201-202` | a marshal error prints to stderr and exits 0 with empty stdout |
| `disruption run -o json --output-junit` | `cli/cmd/disruption.go:71-75` (JSON branch returns), `:115` (JUnit write) | the JUnit file is never written, so CI loses its report |
| `disruption run --fail-on-sla-breach` | `cli/cmd/disruption.go:241,244` | `os.Exit(1)` with no message, skipping defers |

Fix: return `cmdErr`, or an error, at each site. Items 1.11, 3.5 and 5.3.

**F15. `-o json | jq` breaks on many commands (M, VERIFIED with a stub API).** This gives the robustness plan's wave-2 item its evidence list.
- **Notes on stdout.** `output.Success`, `Warn`, `Hint`, `Header` and `Banner` write to stdout (`cli/output/output.go:162-199,295`).
- **Commands that ignore `-o json` entirely:** `status`, `replay <id>`, `plugin list`, `ctx show`, `ctx current`, `ctx export`, `theme list`, `test scaffold list`, `cost estimate`, `changelog`, `tldr`, `test compare`, `report compare`, `test flame` and `doctor dns`.
- **Commands that print a hint instead of `[]` when empty:** `test summary`, `webhook list`, `disruption playbook list`, `disruption schedule list`, `profile list`, `snapshot list` and `test cleanup`.
- **Commands that print a "✓" line instead of JSON:** `test baseline unset`, `webhook add` and `remove`, `schedule delete`, `kafka delete-topic`, `snapshot create`, `profile save` and `test export`.
- **Local `-o` flags shadow the global one.**
  - `kates test scaffold export quick-load -o json` writes YAML to a file named `json` (`cli/cmd/scaffold.go:229`).
  - `kates ctx set foo --url … -o json` fails with "unknown shorthand flag" (`cli/cmd/config.go:482`).

Fix: send notes to stderr when `-o` is not table, add JSON branches to the commands listed, and rename the local flags. Items 5.1, 5.2 and 1.10.

**F16. `disruption watch` exits 0 in silence when the stream drops (M, VERIFIED by test).** Where: `cli/cmd/disruption_watch.go:58-84`. If the SSE stream ends without COMPLETED or FAILED (a proxy idle timeout, since the backend sends no heartbeat; an API restart; or an INTERRUPTED or PARTIAL status), the loop exits and returns `scanner.Err()`, which is nil. A `data:` line over 64 KiB ends the watch with `token too long`. Fix: fall back to `AwaitDisruption`, use `scanner.Buffer(nil, 1<<20)`, and return an error naming `kates disruption status <id>`. Item 3.4.

**F17. Every command can stall 5 s on start in a terminal that does not answer queries (M, VERIFIED in a pty).** Bubble Tea's `init` (`bubbletea@v1.3.10/tea_init.go:21`) calls `lipgloss.HasDarkBackground()`, which sends OSC 11 and CPR queries whenever stdout is a TTY, before `main` runs. In a pty that never replies (CI with an allocated pty, `docker run -t`, `kubectl exec -it`, `script`), `--help`, `tldr` and `version` took 5.1–7.3 s; with replies they take about 50 ms. `--plain`, `NO_COLOR` and `-o json` do not avoid it. Fix: a tiny package whose import path sorts before `github.com/charmbracelet/bubbletea` calls `lipgloss.SetHasDarkBackground` from `COLORFGBG` (default dark). A probe of that cut `--help` from 5.1 s to 43 ms. Item 2.4.

**F18. Detect's sizing reads are wrong (M, VERIFIED by test).** Detect feeds `kates auto` and deploy's generated values.
- `cli/pkg/detect/collector.go:1688-1704` converts each container's memory to whole GiB with integer division, so 256Mi is 0, and 100 pods × 2 × 256Mi report `TotalMemRequests=0`. The capacity check (`:889`) then says Sufficient, and `valuesgen.go` sizes brokers on memory that is not free.
- `collector.go:556` decides the Strimzi operator is absent when the whole `get deployment -A -o json` output contains the text `error`. Any deployment with `--log-level=error` hides an operator in `strimzi-system`.

Fix: accumulate in MiB, and drop the substring tests. Items 1.7a and 1.7b.

**F19. Pollers overlap themselves (M, VERIFIED).** Every poller arms its next tick without waiting for the previous fetch:
- **`test watch` and `create --wait`, TUI path.** `cli/cmd/watch.go:148-153` arms the next tick before the fetch returns: 4 GETs were in flight after 7 s against a hanging server. The port-forward repair runs `kubectl get svc -A` and up to an 8 s wait inside `Update` (`:157`), so `q` and Ctrl-C are dead for 8 s per failed poll. The sparkline grows without bound (`:190`): it is 1,823 columns after an hour, and the renderer cuts off the live number.
- **Deploy dashboard.** It runs 27–42 `kubectl` processes per 2 s tick with up to 5 polls overlapping (measured), because the same `get pods` runs once per workload (`cli/cmd/deploy_dashboard.go:269`) and the tick re-arms at `:433-437`.
- **`tui` tabs.** A failed refresh replaces good data with "Error:" (`cli/tui/brokers.go:45-48`, `topics.go`, `groups.go`). Toggling the consume view stacks poll chains (`cli/tui/topics.go:152-158,202-208`).

Fix: re-arm the tick from the result handler, run the repair as a `tea.Cmd`, keep the last 40 points, and keep old data on error. Items 6.2 and 6.3, plus 1.12 for the sparkline.

**F20. `kates clean` starts 78–117 `kubectl` processes at once (M, VERIFIED by test).** `cli/cmd/clean.go:558-613` starts one goroutine per CRD type × namespace. With 30 s per-command deadlines, a saturated kind laptop can SIGKILL deletes half-way (F7). Fix: `errgroup.SetLimit(8)`, or one comma-joined `kubectl delete` per namespace. Item 6.4.

**F21. Response bodies are read without a bound (M, VERIFIED by test).** `cli/client/client.go:220` calls `io.ReadAll(resp.Body)`. A 300 MiB body allocated 776 MiB in 0.66 s. Error text echoes the whole body, so a 502 HTML page lands in the terminal. Fix: an `io.LimitReader` of 64 MiB, and error text truncated to 512 bytes. Item 2.2.

**F22. `kates auto --dry-run` writes to the cluster (M, READ).** `cli/cmd/auto.go:52` never sets `collector.ReadOnly`, as `deploy.go:173` does, so a dry run creates a namespace, a Secret and prober pods. `auto.go:56,65` call `os.Exit` inside `RunE`, and `auto` buffers 10 minutes of `helm --wait --debug` silently. Fix: `collector.ReadOnly = autoDryRun`, and return errors. Item 1.9.

**F23. A failed or partly failed deploy reports badly (M, READ).**
- Entries are only ever `deployed` or `skipped`, predicted before the run.
- The summary is drawn only when `deployErr == nil` (`cli/cmd/deploy.go:379`), so the failed footer is dead code.
- Tolerated failures still end "N components deployed": Postgres not ready, Entity Operator never Running, KafkaUsers not ready, the connector apply, and the CA-bundle timeout.
- On error, `deploy-error.log` is written into the working directory with its error ignored (`:376`).

Fix: record outcomes, always print the table, and show tolerated failures as warnings. Item 4.7.

**F24. The Lab TUI counts a FAILED run as a measurement (M, VERIFIED by test).** `cli/tui/lab.go:1115-1117` sends FAILED or ERROR as a normal done message, so a sweep records 0 rec/s points and launches the next doomed step. Pressing `x` while CreateTest is in flight drops the started message, and the run is left unwatched. Fix: send FAILED through the error path, and cancel a run that starts late. Item 1.8.

**F25. The port-forward repair targets whatever kube context is current (M, PLAUSIBLE).** Where: `cli/cmd/portforward.go:811-858`, called from `watch.go:157,472`. `portsKubeContext` is empty outside `deploy -P`. If the user has since switched kube contexts, a dead `kates ports` forward is "repaired" against another cluster's Kates, and the API key goes with it. Fix: store the kube context in the `ports` context, and refuse the repair when it differs. Item 4.8.

### 3.3 Low

| # | Finding | Where | Status | Item |
| --- | --- | --- | --- | --- |
| F26 | A plugin can replace a built-in, depending on init order: `kates-version` on PATH replaced `kates version`. `registerPlugins` also scans every PATH directory at init (1.9 ms; a hung network mount in PATH blocks every command, TAB included) | `cli/cmd/plugin.go:72,105-135` | VERIFIED | 7.2 |
| F27 | `top`, `dashboard`, `badge --metric p99/throughput` and `test summary` show blank metric columns: list responses carry no results (`TestRunRepository.toDomainSummary`) | `cli/cmd/top.go:68-74`, `dashboard.go:119-130`, `badge.go`, `test_extras.go:158-196` | READ (backend source) | 6.7 |
| F28 | `--interval 0` or negative turns `top`, `dashboard` and `cluster watch` into a hot loop: 20,347 requests in 3 s against a stub (`test watch` guards it) | `cli/cmd/top.go:28,114,120`, `dashboard.go:259,265`, `cluster_watch.go:55,127,133` | VERIFIED | 1.6 |
| F29 | Deploy poll loops check their deadline only between `kubectl` calls, none of which has `--request-timeout`. After a VPN drop a 15-minute wait has no bound | `cli/cmd/silent_wait.go:17-41`, `deploy_components.go:1270-1356` | PLAUSIBLE | 4.6 |
| F30 | With `$HOME` unset (cron, minimal containers), `./.kates.yaml` in the working directory picks the server; the warning prints once per `configPath()` call | `cli/cmd/root.go:56-62` | VERIFIED | 7.3 |
| F31 | `execMutex` is held for a child's whole life, so "parallel" deploy groups run serially (3 × 1 s commands took 3 s) | `cli/cmd/deploy.go:415-422,475` | VERIFIED | 6.5 |
| F32 | `cmd` init is 15 ms, 3.7 MB and 57.7k allocs per invocation, TAB included. The `kates docs` YAML parse is 3 ms and 26.8k allocs; about 35 MCP regexes compile for `kates mcp` only | `cli/cmd/doc_entries.go:37-40`, `cmd/mcp_*.go` | VERIFIED | 7.1 |
| F33 | Retries ignore `Retry-After` (the backend sends it with 429), have no jitter, and never retry a GET 429 | `cli/client/client.go:139-143,205,227` | PLAUSIBLE | 2.5 |
| F34 | A JSON `null` 200 returns `(nil, nil)` from `get[*T]`; `pollUntilDonePlain` and `waitForTestPlain` then panic. The backend normally sends 204 or 404 | `cli/client/client.go:271-278` | VERIFIED | 2.6 |
| F35 | `resilience run` is not interruptible (a 20-minute call, `cli/cmd/resilience.go:137`). The Transport has no `TLSHandshakeTimeout` or `IdleConnTimeout`, and streams have no idle timeout | `cli/client/client.go:46-52,700-708,883-893` | PLAUSIBLE | 2.3, 3.6 |
| F36 | `kates ports` gives the API forward about 1.8 s to come up, so exec-plugin auth (EKS, GKE) gives false FAILED and no context written | `cli/cmd/portforward.go:199,225-231` | PLAUSIBLE | 6.9 |
| F37 | `deploy status` falls back to 12 sequential `helm status` calls (15 s each) when `helm list -A` fails | `cli/cmd/deploy_status.go:248-339` | READ | 6.8 |
| F38 | Detect lists all deployments 3 times per scan, reads every Secret in `kafka` (private keys included) to get `tls.crt`, and falls back to full CRD JSON | `cli/pkg/detect/collector.go:555,1286,1358,2309`, `cli/pkg/strimzi/installed.go:385` | VERIFIED (call count) | 6.6 |
| F39 | Error lines carry ANSI codes when only stderr is redirected; "Test not found:" prefixes every error, 401 and 500 included | `cli/output/output.go:193`; `cli/cmd/test.go:84`, `explain.go:24`, `replay.go:25`, `export.go:48` | VERIFIED | 5.4 |
| F40 | Lab diff rows reorder on every frame (map range); a topic filter with no match shows every topic | `cli/tui/lab.go:994-1004`, `cli/tui/topics.go:90-108` | VERIFIED | 1.8, 6.2 |
| F41 | The `watch` (events) `--follow` flag is never read; its `seen` map, and `kafka consume --follow`'s, grow forever | `cli/cmd/watch_events.go:52,125`, `cli/cmd/kafka.go:570-590` | READ | left out (§6) |
| F42 | `migrate status --watch` exits 0 on Ctrl-C (`return nil` on the sleep error) | `cli/cmd/migrate_lab.go:677-678` | READ | 4.5 |

---

## 4. Waves

**Sizes** count product lines, not tests: XS up to 20, S up to 80, M up to 250. Every PR runs `go test ./...`, `go test -race` on the packages it touches, and `go vet` under `cli/`. A chart change also runs the chart CI's `helm template` checks locally.

**Order.** Each item is one PR off `main`. Waves are ordered by value per line changed. Inside a wave, items are independent unless a dependency is named.

### Wave 1: quick wins

| Item | Scope | Files | Tests | Size |
| --- | --- | --- | --- | --- |
| 1.1 | **F5.** New chart value `chaos.provider`, empty by default; when set, the configmap renders `KATES_CHAOS_PROVIDER`. Deploy passes `--set chaos.provider=noop` when `--with-chaos=false` and no `chaos` release exists (D1: `noop`) | `charts/kates/values.yaml`, `templates/configmap.yaml`, `Chart.yaml` (version), `cli/cmd/deploy_components.go` (~1684) | Deploy args test, both ways. `helm template` with and without the value | S |
| 1.2 | **F6.** A config that cannot be read or parsed fails API calls through `client.Failing`, naming the file; `--url` and `KATES_URL` still work | `cli/cmd/root.go` | Broken file under `KATES_TEST_HOME`: `health` errors and makes no request; `--url` works | S |
| 1.3 | **F8.** Clamp the broker cursor on refresh; nil-check `info` | `cli/tui/brokers.go` | Shrink 3 to 2 brokers with the cursor on 2; `View()` does not panic | XS |
| 1.4 | **F12.** Stop deleting `~/.cache/helm` | `cli/cmd/deploy_components.go:1017` | — | XS |
| 1.5 | **F10.** Restore `failurePolicy: Fail` on `context.WithoutCancel(gCtx)` with a 30 s timeout, deferred straight after the bypass | `cli/cmd/deploy_components.go:886-904` | Cancelled `gCtx`: both restore patches still issued | S |
| 1.6 | **F28.** Refuse `--interval < 1` | `cli/cmd/top.go`, `dashboard.go`, `cluster_watch.go` | Table test | XS |
| 1.7a | **F18.** Operator lookup trusts `err` and empty output only | `cli/pkg/detect/collector.go:556` | Deployment JSON with `--log-level=error` finds the operator | XS |
| 1.7b | **F18.** Memory requests summed in MiB (Ki, Mi, Gi, plain bytes), converted once | `cli/pkg/detect/collector.go:1688-1704` | 100 pods × 2 × 256Mi gives 50 GiB | S |
| 1.8 | **F24, F40.** FAILED or ERROR goes through the Lab's error path; a late `labTestStartedMsg` after `x` cancels its run; sort the diff keys | `cli/tui/lab.go` | FAILED run: no iteration, sweep stops. Late start sends a cancel. Stable diff | S |
| 1.9 | **F22.** `auto --dry-run` is read-only; `RunE` returns errors instead of `os.Exit` | `cli/cmd/auto.go` | Dry-run collector has `ReadOnly` | XS |
| 1.10 | **F15.** Rename the local `-o`/`--output` flags: scaffold export `--file`/`-f`, ctx set `--default-output`; release note | `cli/cmd/scaffold.go:229`, `cli/cmd/config.go:482` | `scaffold export … -o json` prints JSON and writes no file named `json` | S |
| 1.11 | **F14.** `disruption run` writes JUnit before the JSON branch; `checkSlaExit` returns a silent error instead of `os.Exit` | `cli/cmd/disruption.go:71-75,115,233-246` | `-o json --output-junit` writes the file; SLA breach exits 1 through `Execute` | XS |
| 1.12 | **F19 (part).** Keep the last 40 sparkline points | `cli/cmd/watch.go:190` | Length stays at most 40 | XS |

### Wave 2: no more hangs in the API client

| Item | Scope | Files | Tests | Size |
| --- | --- | --- | --- | --- |
| 2.1 | **F3.** No retry after a timeout. Probes (`version`, `status`, `health`, `doctor`, `top`, `dashboard`) get 5 s. Completion gets 700 ms and one attempt. Pollers get a client copy with `MaxRetries = 1` | `cli/client/client.go`, `cli/cmd/autocomplete.go`, `version.go`, `status.go`, `health.go`, `doctor.go`, `root.go` (completion branch) | Silent `httptest` server: a GET returns after T, not 3T; `__complete test get ""` returns within 1 s; `version` within 6 s | M (split client and callers if preferred) |
| 2.2 | **F21.** Bodies capped at 64 MiB; error text truncated to 512 bytes | `cli/client/client.go:220-258` | 100 MiB body errors; allocations stay bounded | S |
| 2.3 | **F35.** `TLSHandshakeTimeout: 10s` and `IdleConnTimeout: 90s`; streams error after 120 s without a byte (resilience sends a space every 10 s) | `cli/client/client.go:46-52,883-893` | A stalled stream errors | S |
| 2.4 | **F17.** A small package that sets `lipgloss.SetHasDarkBackground` from `COLORFGBG` before Bubble Tea's init | new `cli/internal/termbg`, `cli/main.go` | Unit test on `COLORFGBG` parsing; manual check in a silent pty (`script`) | S |
| 2.5 | **F33.** Honour `Retry-After` (capped at 30 s), add jitter, retry a GET 429 | `cli/client/client.go` | 429 with `Retry-After: 1` is retried after about 1 s | S |
| 2.6 | **F34.** `get[*T]` errors on a `null` body | `cli/client/client.go:271-278` | `null` body returns an error, not nil | XS |

### Wave 3: runs the CLI starts are followed to the end, or cancelled

This is the robustness plan's "one shared waiter", with the call sites named.

| Item | Scope | Files | Tests | Size |
| --- | --- | --- | --- | --- |
| 3.1 | **F2.** `waitRun(ctx, id, timeout, interval)` from `pollUntilDonePlain`: wall-clock deadline, `Retryable()` classification, one poll in flight, terminal set DONE and FAILED (with COMPLETED and ERROR as aliases). Both `apply` paths use it; a wait that really fails cancels its run and stops the batch | `cli/cmd/watch.go`, `cli/cmd/apply.go` | 2.5 s outage: run DONE, not ERROR. 401: stop at once. Failed wait: cancel sent, next scenario not started | M |
| 3.2 | **F1.** `gate` on `waitRun`: interruptible, cancels on timeout or poll failure, deadline checked inside the poll | `cli/cmd/gate.go` | Timeout sends `CancelTest`; SIGINT exits 130 after cancelling | S |
| 3.3 | **F1, F14.** `benchmark` and `flow` on `waitRun` with `--timeout`; cancel on give-up; non-zero exit when a row is ERROR or FAILED | `cli/cmd/benchmark.go`, `cli/cmd/flow.go` | 401 stops at once and exits 1; timeout cancels | M |
| 3.4 | **F16.** `disruption watch` falls back to `AwaitDisruption` when the stream drops; 1 MiB scanner buffer; error names `kates disruption status <id>` | `cli/cmd/disruption_watch.go` | Stream that ends early: fallback answers. 100 KiB event parses | S |
| 3.5 | **F14.** `connect test` exits 1 on a failed CDC test and stops on persistent errors; `snapshot create` reads in an errgroup and saves nothing if a read fails | `cli/cmd/connect_test_integration.go`, `cli/cmd/snapshot.go` | Failed CDC exits 1; 401 on one read saves no snapshot | S |
| 3.6 | **F35.** `resilience run` is interruptible and prints how to follow the run | `cli/cmd/resilience.go` | SIGINT prints the hint and exits 130 | S |

### Wave 4: subprocesses and Ctrl-C on the cluster side

| Item | Scope | Files | Tests | Size |
| --- | --- | --- | --- | --- |
| 4.1 | **F7.** One command helper: `Cancel` sends SIGINT, `WaitDelay` 60 s for helm and 2 s for reads. Used by the deploy runners, `cmd/runner.go`, `internal/helm`, `internal/podrun`, `pkg/strimzi` and `deploy_status` | new helper next to `internal/kubectl`, the call sites listed under F7 | A child with a SIGINT handler gets to clean up; a grandchild holding the pipe does not outlive `WaitDelay` | M |
| 4.2 | **F7.** Dashboard ctrl+c means "stopping after the current step"; the screen stays up and says so, and the next step does not start. `deployCmd` becomes interruptible for non-TTY runs. Never cancel an in-flight helm (D2). After 4.1 | `cli/cmd/deploy_dashboard.go`, `cli/cmd/deploy.go` | Ctrl-C mid-step: current step finishes, next not started, exit 130, installed list printed | M |
| 4.3 | **F4.** Readiness waits on the `Ready` condition; Kates uses `rollout status`. Components deploy already tolerates stay tolerated, but are reported (D4) | `cli/cmd/silent_wait.go`, `deploy_components.go` callers | Stub `kubectl`: Running but not Ready keeps waiting and times out | S–M |
| 4.4 | **F9.** `detect.CommandExecutor.Exec` takes a context with a per-call timeout and `WaitDelay`; `Collect` uses the errgroup context; reads get `--request-timeout=30s`; `logs -f` is bounded | `cli/pkg/detect/executor.go`, `collector.go`, callers in `cmd/detect.go`, `deploy.go`, `auto.go` | Sleeping stub `kubectl`: `Collect` returns within the timeout after cancel | M |
| 4.5a | **F11, F42.** `migrate target`, `migrate up` and `migrate status` are interruptible so their deferred pod deletes run; podrun pods get `activeDeadlineSeconds` (2 h); `status --watch` exits 130 on Ctrl-C | `cli/cmd/migrate_*.go`, `cli/internal/podrun/pod.go` | The stub-kubectl repro from §3.2: SIGINT now logs `delete pod`; manifest has the deadline | S |
| 4.5b | **F11.** `detect` is interruptible (the spinner's Ctrl-C cancels the scan); namespaces are created labelled in one manifest; stale `kates-detect-*` namespaces are swept at start | `cli/cmd/detect.go`, `cli/pkg/detect/spinner.go`, `collector.go` | Cancel mid-scan issues the `delete ns` | S |
| 4.5c | **F11.** `doctor network` deletes its pod by name after a timeout | `cli/cmd/doctor_network.go` | Timeout path issues `delete pod` | XS |
| 4.6 | **F29.** Deploy poll calls carry `--request-timeout=15s` | `cli/cmd/silent_wait.go`, `deploy_components.go` loops | Args test | S |
| 4.7 | **F23.** Entries record real outcomes; the table prints on error too; tolerated failures show as warnings; no `deploy-error.log` in the working directory | `cli/cmd/deploy.go`, `deploy_view.go`, `deploy_components.go` | Failed step: table shows deployed, failed and not started | M |
| 4.8 | **F25.** `kates ports` stores its kube context in the `ports` context; the repair pins it, or refuses when the current one differs | `cli/cmd/portforward.go`, `cli/cmd/root.go` (Context) | Repair with a different current context refuses | S–M |

### Wave 5: output and exit codes

The robustness plan's wave 2 keeps the exit-code scheme (0/1/2/3/4/5), `-o` validation and strict YAML. This wave fixes only what §3 found.

| Item | Scope | Files | Tests | Size |
| --- | --- | --- | --- | --- |
| 5.1 | **F15.** `Success`, `Warn`, `Hint`, `Header` and `Banner` write to `output.Err` when the output mode is not table | `cli/output/output.go` (+ setter from `root.go`) | `-o json` run of `test cleanup`: stdout is one JSON document | S |
| 5.2 | **F15.** JSON branches for the commands that ignore `-o json`, in groups: ctx/plugin/theme; report/test compare/flame/replay; list commands' empty case; confirmations | the commands listed under F15 | One `-o json \| jq` contract test per group | 4 × S |
| 5.3 | **F14.** Return errors from `status`, `kyverno`, `detect`, `ports` and `security` audit; `output.JSON` returns its marshal error | files listed under F14 | Exit code 1 per case with stub `kubectl` or API | S per group |
| 5.4 | **F39.** Error styles render for stderr; "not found" only for a 404 | `cli/output/output.go`, the 6 `GetTest` callers | `2>file` holds no ANSI; 500 does not say "not found" | XS each |

### Wave 6: less load on the cluster and the API

| Item | Scope | Files | Tests | Size |
| --- | --- | --- | --- | --- |
| 6.1 | **F13.** Page `Topics`, `ConsumerGroups` and `Audit` (`size=200` until `total`); `DisruptionList` sends `size` | `cli/client/client.go` | Stub with `total=450`: all 450 returned | S |
| 6.2 | **F19, F40.** TUI pollers re-arm after the result (`test watch`, cdc, tui tabs); repair as a `tea.Cmd`; keep data on a failed refresh; consume ticks carry a generation; empty filter shows none | `cli/cmd/watch.go`, `connect_test_integration.go`, `cli/tui/*.go` | Hanging server: one GET in flight; consume toggle: one chain | M |
| 6.3 | **F19.** Deploy dashboard: one poll in flight, `get pods` once per target, inactive components skipped | `cli/cmd/deploy_dashboard.go` | Counting `read`: calls per tick drop from 27 to about 9 | S |
| 6.4 | **F20.** `clean` fan-out bounded (`SetLimit(8)`) | `cli/cmd/clean.go:558-613` | At most 8 concurrent fakes | S |
| 6.5 | **F31.** Take `execMutex` only under `--verbose` | `cli/cmd/deploy.go:415-476` | 3 × 1 s commands finish in about 1 s | XS |
| 6.6 | **F38.** One deployment list per `Collect`; TLS secrets by field selector and `jsonpath` for `tls.crt`; CRD fallback via `custom-columns` | `cli/pkg/detect/collector.go`, `cli/pkg/strimzi/installed.go` | Counting executor: one `get deployment -A` | S |
| 6.7 | **F27.** `top` and `dashboard` fetch `GetTest` for RUNNING rows only; `badge` and `test summary` use `ReportSummary` (errgroup, limit 8) | `cli/cmd/top.go`, `dashboard.go`, `badge.go`, `test_extras.go` | Stub: p99 column filled | S |
| 6.8 | **F37.** `deploy status` uses its existing parallel path and reports "cluster unreachable" once | `cli/cmd/deploy_status.go` | Failing `helm list`: one message, bounded time | S |
| 6.9 | **F36.** `ports` waits up to 10 s per forward, in parallel | `cli/cmd/portforward.go:199-231` | Slow fake forward is not FAILED | S |

### Wave 7: startup

| Item | Scope | Files | Tests | Size |
| --- | --- | --- | --- | --- |
| 7.1 | **F32.** Doc entries and MCP regexes built on first use (`sync.OnceValue`). Saves about 3.5 ms and 27k allocs per invocation, TAB included | `cli/cmd/doc_entries.go`, `cli/cmd/mcp_*.go` | Existing docs and MCP tests | S |
| 7.2 | **F26.** Plugins registered only when `rootCmd.Find` fails, and never over a built-in name | `cli/cmd/plugin.go` | `kates-version` on PATH does not replace `version`; an unknown name still runs its plugin | S–M |
| 7.3 | **F30.** No home directory means no config file; warn once | `cli/cmd/root.go:56-62` | `HOME` unset: `./.kates.yaml` ignored | XS |

---

## 5. Decisions

Taken 2026-10-18 by the maintainer: the recommended pick each time.

- **D1. What `--with-chaos=false` sets.**
  - **Decided: `noop`.** Every fault is an explicit skip, with no ERROR log, which matches "without chaos".
  - Rejected: `kubernetes` plus `rbac.directChaos=true`. Pod-delete, rolling restart and scale-down would keep working without Litmus, but the API would get cluster write rights the user opted out of.
- **D2. Ctrl-C during deploy.**
  - **Decided: "finish the current step, then stop".** Nothing is left `pending-install`.
  - Rejected: "cancel now". Faster, but a release can be left stuck and the re-run fails until it is rolled back.
- **D3. When `gate`, `benchmark` or `flow` give up.**
  - **Decided: cancel the run.** The same choice #250 made for Ctrl-C.
  - Rejected: leave it running and print its id.
- **D4. Readiness.**
  - **Decided: Ready for every gated component.** The components deploy already tolerates stay tolerated but are reported. Expect some kind deploys that "passed" to now report a component that never became Ready; that is the point.
  - Rejected: also accept `Running` for those tolerated components.

---

## 6. Left out, and why

- **Running Group C and `kind load` concurrently with earlier waits.** The installs are quick, kind shares one machine, and it would interleave dashboard output; the gain is unmeasured.
- **Parallel kube-context probing in `pkg/cluster`.** `CheckAllReachable` is sequential on purpose (`cli/pkg/cluster/cluster.go:154-156`); only the 2N+2 `config view` calls could collapse, for little gain.
- **A streaming `kafka consume --follow`, and the `watch` events `--follow` and `seen` maps (F41).** The first needs a backend endpoint; the rest is cosmetic and low-traffic.
- **The errcheck backlog (213), gosec G204 and G306, and contextcheck (27).**
  - The lint job already gates new issues, and the backlog is paid file by file.
  - G204 is argv without a shell, by design.
  - The config is written 0600.
  - The contextcheck hits are the `if ctx == nil` test seam.
- **The exit-code scheme, `-o` validation, strict YAML `KnownFields`, and `TestMain` `KUBECONFIG` isolation.** Already in the robustness plan's waves 2 and 4. This review's race and test runs passed with `HOME` and `KUBECONFIG` isolated, which supports doing the isolation without a large rewrite.
- **A top-level `recover()`, and a recover middleware for MCP prompts and resources.** The first belongs with the exit-code scheme (a panic exits 2 today). The MCP handlers outside the guard's recover are plain string builders.
- **Pinning `bitnami/postgresql` (`deploy_components.go:1137-1147`).** It belongs with the Bitnami image migration, not a CLI stability PR.
- **`internal/helm.Status` treating any error as "not found" (`cli/internal/helm/helm.go:51-53`).** It has no production caller.
- **Not run.** Real `kubectl` latency (F19's per-call cost is estimated), Bubble Tea UIs in a real terminal beyond the pty timing, a dial to an unroutable address (F3's 91 s is arithmetic), and helm's `pending-install` behaviour (F7).

---

## 7. Suggested order

1. Wave 1 in any order: 13 items, most XS or S. D1 is taken, so 1.1 can start too.
2. Then 2.1, 3.1 and 3.2.
3. Then 4.1, 4.2 and 4.3 together, since they share one kind deploy to verify.
4. Waves 5 to 7 can run alongside any of the above. 5.1 should land before 5.2.

---

## 8. Overlap with the robustness plan (2026-09-28)

| That plan | Here |
| --- | --- |
| Wave 2: one shared waiter with `--timeout` and retry classification | Wave 3, with the 7 loops named (F1, F2, F16) |
| Wave 2: notes to stderr | 5.1, plus the per-command evidence in F15 |
| Wave 2: exit-code scheme, `-o` validation, strict YAML | Left there; 5.3 only removes exit 0 on failure |
| Wave 1 follow-up: `kates ports` pkills every port-forward | Still present (`cli/cmd/portforward.go:141`); not repeated |
| Wave 1 follow-up: a mistyped subcommand exits 0 | Still present; not repeated |
| Wave 1 follow-up: record what kates installed | 4.7 records outcomes per deploy; the install record for `clean` stays there |
| Wave 4: linters (noctx and others) | The counts in §2 size that work: noctx has 8 hits |

---

## Appendix A. Measurements

macOS arm64, Go 1.27.2; the machine was busy, so medians are noisy. Command timings are min / median over a Python loop, since hyperfine was not installed.

| Run | Time |
| --- | --- |
| Empty Go binary (floor) | 2.4 / 3.7 ms |
| `kates -o json version` | 10.9 / 16.1 ms |
| `kates --help` | 15.9 / 23.4 ms |
| `kates __complete test ""` | 12.6 / 21.6 ms |
| `kates version`, API refuses connections | 1.52 / 1.56 s |
| `kates __complete test get ""`, API refuses | 1.52 / 1.53 s |
| `kates version`, API accepts and never answers | 181.8 s (one run) |
| `kates __complete test get ""`, same | still hung at 8 s, killed |
| `kates --help` in a pty that never answers terminal queries | 5.1 s (43 ms with the 2.4 probe) |
| `kates top --interval 0` against a counting stub | 20,347 requests in 3 s |
| 300 MiB response body | +776 MiB allocated, 0.66 s |
| Deploy dashboard poller, 9 components × 2 workloads | 27 kubectl calls per poll, 122 in 12 s, up to 5 polls in flight |
| `kates clean` CRD delete fan-out, 13 types × 6 namespaces | 78 concurrent `kubectl` |

**Init.** `GODEBUG=inittrace=1` over 25 runs: the whole init is about 16.7 ms across 139 packages. `cmd` accounts for 15.0 ms, 3.7 MB and 57.7k allocs; the next largest is `pkg/migrate` at 0.62 ms. pprof over 120 runs: `registerPlugins` 22% of sampled CPU, the doc-entries YAML 13%, `regexp.MustCompile` 4.5%.

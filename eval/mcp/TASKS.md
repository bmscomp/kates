# MCP evaluation: the tasks

The fixed task list for the evaluation in [`plans/mcp-server.md`](../../plans/mcp-server.md) §2.4 and §8.4: 20 tasks with known answers, asked identically of the curated MCP arm (`kates mcp`), the shell arm (`kates` CLI plus [`skills/kates-cli/SKILL.md`](../../skills/kates-cli/SKILL.md)) and, by hand, an expert with the CLI and no AI. The list was fixed on 2026-09-26 (`fixed_at` in [`tasks.json`](tasks.json)), before either agent arm was measured but after both were built, by the people who built them; plan §2.4 asks for it to be fixed before the arms are built, so have someone who built neither review the selection before the run.

| File | What it is |
| --- | --- |
| `tasks.json` | The tasks: prompt, answer fields, setup, oracle, checks, required caveats |
| `taskfile.py` | The rules every part of the harness shares: validation, `{capture}` placeholders, JSON paths, `forbid` |
| `setup.py` | Prepares the lab for each task, as the human, and writes `state.json` |
| `oracle.py` | Computes each task's expected answer from the Kates API and writes `oracle.json` |
| `run.py`, `grade.py`, `report.py` | The runner, the grader and the report ([README.md](README.md)) |
| `testdata/` | Response fixtures in the backend's shapes, the captures setup would write, and the answers they give |

## The tasks

Each task names a persona from plan §2.2. The application developer has no task: that persona needs `start_test` and a tuning sweep that sweeps (P-14). "Required caveats" are the silent-misread checks: the prose answer (the first turn, without its code) must contain one phrase of each (case-insensitive). The phrases leave out words a misread answer would use as readily as a right one.

| Task | Persona | What it tests | Oracle | Required caveats |
| --- | --- | --- | --- | --- |
| `fact-min-isr` | platform | The `min.insync.replicas` in force on a topic without an override, and that it comes from the brokers, not Kafka's default of 1 (plan §2.4) | `topic_min_isr` | none; the field catches the misread |
| `fact-partition-leader` | platform | A plain cluster fact: a partition's leader and ISR, the baseline for grounded claims | `partition_leader` | none |
| `sec-posture` | security | The backend's grade and its failing high and critical checks, without the Kyverno checks and grade step the CLI adds from the local kubeconfig | `security_posture` | none |
| `sec-listener-encryption` | security | Whether every listener is encrypted, when the audit's plaintext checks pass on a SASL_PLAINTEXT listener with a name of its own | `listener_encryption` | none; the boolean catches the misread |
| `sec-pentest-cve` | security | Reporting the "pentest" and CVE results for what they are (plan §2.4) | `pentest_and_cve` | `pentest-config-only`, `cve-fixed-list` |
| `sec-drift-trend` | security | One check improved since a saved baseline, and refusing a grade trend the backend keeps in memory (plan §2.4) | `security_drift` | `security-trend-in-memory` |
| `run-vs-baseline` | platform | Grounded numbers from the regression check against the LOAD baseline | `run_vs_baseline` | none |
| `run-noise-band` | platform | A like-for-like noise band: four earlier runs with the identical stored spec, not the type's mixed trend | `noise_band` | none |
| `run-load-parallel-producers` | platform | "This test cannot answer that": a LOAD run asked for 8 producers ran one (plan §2.4) | `load_parallel` | `load-single-producer` |
| `run-baseline-spec-mismatch` | platform | A regression verdict against a baseline with another record size is not like for like | `baseline_spec_mismatch` | `spec-mismatch` |
| `run-trend-mixed-specs` | platform | Refusing one trend across runs with three different specs (plan §2.4) | `topic_trend` | `trend-mixes-specs` |
| `sre-kates-caused-lag` | sre | "Did Kates cause this": a Kates run wrote into the topic a stopped group reads; Kafka is healthy | `kates_caused_lag` | `audit-no-actor` |
| `sre-lag-partition-leader` | sre | Lag per partition joined with each partition's leader | `lag_partitions` | none |
| `gameday-leader-kill-preview` | gameday | An ad-hoc step on a partition's leader, previewed by the dry run, handed to the human | `leader_kill_preview` | `leader-may-move` |
| `gameday-rolling-restart-preview` | gameday | A playbook's plan and blast radius before anyone runs it (P-17) | `playbook_preview` | none |
| `gameday-leader-cascade-fit` | gameday | Checking what a playbook targets instead of trusting its name | `playbook_fit` | none |
| `gameday-controller-kill-uncounted` | gameday | A controller kill passes a broker-only blast radius; passing says nothing about the quorum (P-18) | `controller_kill_preview` | `controllers-uncounted` |
| `debrief-broker-kill` | gameday | A debrief from the report: target, recovery, no SLA declared, metrics measured or not | `disruption_debrief` | `chaos-times-approximate` |
| `debrief-compare-game-days` | gameday | Recovery compared with the previous game day, within the timing noise of the chaos provider | `disruption_compare` | `chaos-times-approximate` |
| `debrief-diagnose-without-report` | gameday | Plan §8.4 ground truth with the report hidden: which broker was lost, inferred from the cluster | `hidden_fault_target` | `inferred-not-recorded` |

The mix follows the brief: four security, five run assessment, two lag triage (the brief asks for about three; the third was retired, under "Left out"), four game-day plans, three debriefs, two cluster facts. Every task runs in both agent arms, three trials each. Each task's `notes` field says what a good answer contains and why its caveats matter.

## What the harness does to the lab

Setup runs as the human, with the named context; agents only read. On the kafka-cluster chart's lab it:

- creates topics and consumer groups named with the run's `run_tag` (`eval-facts-`, `eval-load-`, `eval-orders-`, `eval-pay-`, `eval-plan-`, `eval-diag-`), so nothing from an earlier evaluation run enters a noise band, a lag or a time window;
- starts ten LOAD runs, one at a time, each 3,000 to 10,000 records at 1,000 records per second, and produces 25 records with `kates kafka produce`;
- sets the LOAD test baseline and saves the security baseline, replacing both of the lab's;
- creates and deletes one topic with replication factor 1, to make a drift;
- runs the `broker-kill-recovery` template three times, one broker pod each time, one at a time (the backend's lease also refuses a second concurrent fault), with at least 90 s between them, and waits 330 s after the last.

Setup takes about 30 minutes, most of it the three pod kills and the waits. A setup that fails leaves the lab partly prepared; start a new run directory, which gets a new `run_tag`.

## Order, and facts that move

`run.py setup` prepares every task first, then `run.py oracle` computes every expected answer, then the trials run. That order works because the file is ordered: the tasks that kill brokers come last, and the last one waits for preferred leader election before any trial. Partition leaders (`fact-partition-leader`, `sre-lag-partition-leader`, `gameday-leader-kill-preview`, `gameday-leader-cascade-fit`) and live security checks (`sec-posture`) can still move while the trials run, so `run.py oracle --recheck` computes every answer again afterwards, and a task whose answer changed is void: `grade.py` lists it and `report.py` leaves it out of the kill criteria. A task whose oracle failed is void the same way.

## Running it

The runner does all of it with the human's kates context, whose key it reads with `kates ctx export --reveal` ([README.md](README.md) has the whole procedure):

```bash
python3 eval/mcp/run.py setup  --run-id <id> --human-context human
python3 eval/mcp/run.py oracle --run-id <id> --human-context human
```

`setup.py` and `oracle.py` also run on their own, with the key in the environment:

```bash
export KATES_EVAL_API_KEY=$(kubectl get secret kates-api-key -n kates -o jsonpath='{.data.api-key}' | base64 -d)
export KATES_EVAL_URL=http://localhost:8080     # the API the human context reaches
python3 eval/mcp/setup.py  --run-dir eval/mcp/runs/<id> --context human --kates "$(which kates)"
python3 eval/mcp/oracle.py --run-dir eval/mcp/runs/<id>
python3 eval/mcp/oracle.py --run-dir eval/mcp/runs/<id> --task fact-partition-leader \
  --out eval/mcp/runs/<id>/oracle-after.json --against eval/mcp/runs/<id>/oracle.json   # exit 3 when an answer moved
```

Without a lab:

```bash
python3 -m unittest discover eval/mcp
python3 eval/mcp/run.py setup --run-id x --dry-run      # every step, and every prompt as rendered
python3 eval/mcp/oracle.py --run-dir /tmp/eval-offline --state eval/mcp/testdata/state.json \
  --fixtures eval/mcp/testdata/api                      # every oracle on recorded responses
```

No key but the agent context's reaches the agents (and until Phase 2 that is the same shared key): each trial's environment is built from an allowlist without any `KATES_*` variable, its HOME holds only the agent's context, and the CLI arm's `kates` and `jq` run with only `HOME`, `PATH`, `TMPDIR` and the locale.

Exit codes. `setup.py`: 0 every requested setup is done, 1 a step failed (its error is in `state.json`), 2 bad input. `oracle.py`: 0 every task computed, 1 a task failed (its error is in `oracle.json`), 2 bad input, 3 an answer differs from `--against`.

## The task file

The shared schema, with what this half adds to it. `taskfile.validate` checks all of it, and `python3 -m unittest discover eval/mcp` runs that check on `tasks.json`.

### `forbid` (optional)

```json
"forbid": {"mcp": ["disruption_report", "kates_activity"], "cli": ["kates disruption", "kubectl", "curl"]}
```

Tools and commands the agent may not use in that task, because they would leak the answer. `mcp` names `kates mcp` tools; `cli` names command prefixes. The runner (`arms.forbid_for`) turns them into deny rules in each trial's settings: `mcp__kates__<tool>` (the server registered as `kates`); for a `kates` prefix, `Bash(kates <path>)` and `Bash(kates <path> *)` in every alias form of the path; for any other prefix, `Bash(<prefix>)` and `Bash(<prefix> *)`. A prefix rule does not see `kates --context lab disruption status …`, so the runner also reads every Bash command in the transcript (kates calls by command path, with aliases and global flags resolved; other prefixes with `taskfile.forbidden_hits`, which splits pipelines, lists and subshells). The CLI arm's `kates` wrapper also refuses the task's forbidden `kates` commands at run time, so a string the deny rules miss is still refused. A forbidden call that went through anyway fails the trial's task. Only `debrief-diagnose-without-report` forbids anything. The MCP timeline resource `kates://disruptions/{id}/timeline` needs an id the agent can only learn through the forbidden tools.

### Setup steps

| Kind | Fields | What it does |
| --- | --- | --- |
| `kates` | `args`, optional `capture`, `expect`, `repeat`, `allow_fail`, `timeout_s` | Runs `kates <args> --context <context>` with `KATES_URL`, `KATES_API_KEY`, `KATES_CONTEXT` and `KATES_OUTPUT` removed from the environment. A step that captures or expects must pass `-o json`. A step may not pass `--context`, `--url` or `--api-key` |
| `api` | `method`, `path`, `why`, optional `body`, `capture`, `expect`, `timeout_s` | A GET, or a POST to `/api/disruptions/templates/…` only (`taskfile.SETUP_API_POSTS`), with the human key (`run.py` reads it from the human context; `setup.py` on its own from `KATES_EVAL_API_KEY`). `why` says what no kates command does |
| `wait` | `seconds`, optional `since` | Sleeps; with `since` (an epoch capture), only what is left of `seconds` after it |
| `clock` | `capture` | Captures the time as `rfc3339`, `rfc3339_minute` (floored), `epoch` or `hhmm_utc` |
| `use` | `task` | Runs that task's setup first, once per run, and makes its captures visible |
| `assert_equal` | `left`, `right`, `message` | Stops setup when a premise of the task does not hold |

`capture` maps a name to a JSON path into the command's stdout (or the response): `$.key`, `[n]` (negative from the end), and `[key=value]`, which keeps the objects of a list whose key equals value (`$.nodes[role=controller][0].id`). `expect` maps a JSON path to the value it must have.

The `api` kind exists for the debrief tasks, whose ground truth is the report of a `broker-kill-recovery` template run: `POST /api/disruptions/templates/{id}` runs the template synchronously, stores its report once, when the run has ended, and returns it, and no kates command runs a template. Until #207 it was also the only disruption whose report the backend stored: `DisruptionLauncher` saved a plan's outcome as a second insert under the id of its RUNNING placeholder, which failed, so a plan or playbook started through the API stayed RUNNING and `kates disruption run` timed out after 20 minutes. #207 made that save replace the placeholder, and left template runs, which insert a new id once, as they were. The debrief setups could now use `kates disruption run` or `kates disruption playbook run`; they keep the template runs the list was fixed with.

### Captures and placeholders

`{name}` in a prompt, in oracle `args` and in setup steps is filled from captures. `run_tag` (six hex characters, one per run directory) is always there; after it come the captures of the tasks a task `use`s, then its own (`taskfile.captures_for`). A string that is exactly one placeholder keeps the captured value's JSON type, so `{"brokerId": "{kill_broker}"}` sends a number. The runner renders prompts with `taskfile.render(task["prompt"], taskfile.captures_for(state, task_id, tasks_by_id))` as the trial's first turn, and asks for the answer fields in a second turn (`tasks.answer_request`), so the fields cannot tell the agent what to look for.

### `state.json`

```json
{"version": 1, "run_tag": "abc123", "context": "lab", "api_url": "…", "kates": "/usr/local/bin/kates",
 "created_at": "…", "updated_at": "…",
 "tasks": {"run-vs-baseline": {"status": "done", "started_at": "…", "finished_at": "…",
                               "captures": {"cand_id": "5e6f7081"}, "log": [{"step": 0, "kind": "kates", "argv": ["…"], "exit": 0, "seconds": 14.2}]}}}
```

A task's `status` is `done` or `failed` (with `error`). Setup skips a task already `done` in that run directory. Each kates step's stdout and stderr are kept under `<run-dir>/setup/<task>/step-<n>.out|.err`, and each api response as `step-<n>.json`.

### `oracle.json`

```json
{"version": 1, "computed_at": "…", "api_url": "…",
 "tasks": {"fact-min-isr": {"oracle": "topic_min_isr", "computed_at": "…", "expected": {"min_insync_replicas": 2, "set_at": "broker"}},
           "sec-posture":  {"oracle": "security_posture", "computed_at": "…", "error": "…"}}}
```

`expected` is keyed by the task's answer fields and typed as they declare (`string[]` values are strings, broker ids included). A task with `error` has no answer and is void. `oracle.py` merges into an existing file, so it can run task by task. From Python: `oracle.compute_task(task, captures, oracle.KatesAPI(url, key))`.

### Checks, as the task list assumes them

`grade.py` applies them; these are the semantics the tolerances were chosen for. `exact`: equal after trimming, case-insensitive for strings. `number`: within `tolerance`, absolute (`abs`) or a percentage of the expected value (`pct`). `set`: the same members, ignoring order and case. `contains`: the answer contains the expected text (no task uses it). A missing field fails its check.

## Fixtures

`testdata/api/` holds one imaginary evaluation run (`run_tag` abc123) on the chart's lab: cluster `krafter`, controllers 0 to 2 in pool `controllers`, brokers 3 to 5 in pool `brokers`. Every file has the shape the backend serialises, read from its code: `TestRun`, `TestResult` and `TestSpec` (`domain/`); `ReportSummary`, `ComparisonReport` and `BaselineService.compareRegression` (`report/`, `service/`); the maps of `SecurityService` and `SecurityPentestService`; `TopicService.describeTopicDetail`, `ConsumerGroupService.describeConsumerGroup`, `ClusterHealthService.clusterHealthCheck` and `ClusterTopologyService.describeNodes`; `DisruptionReport` (durations in seconds, as Jackson writes `java.time.Duration`), the disruption compare and playbook plan, and `DisruptionSafetyGuard.DryRunResult`. `manifest.json` maps each request (`GET <path>?<sorted query>`, or `DRYRUN <plan name>`) to its file. `expected_oracle.json` holds the answers they give, checked by hand. `testdata/setup/` holds kates output and API responses for the setup tests.

## Left out

- **Tuning and the application developer.** A `TUNE_*` run executes one configuration and its report repeats it (P-14); plan §8.4 says tuning tasks wait.
- **Scenario drafting** (`draft_scenario`). Not in this mix; its SLA thresholds can be graded only after a run, which agents may not start.
- **A context switch mid-session, and a tampered plan at approval.** Phase 5 material (plan §8.4): they test the server's pinning and an approval flow that does not exist yet.
- **Fields `applyTypeDefaults` dropped.** Fixed in the backend (#204), which now keeps the request; the caveat applies only to older runs.
- **A disruption record left at RUNNING** (`sre-stale-running-disruption`, retired). It asked whether Kates was running anything while the record of a plan that had ended still said RUNNING (mcp caveat activity-disruption-rows, as it read then). #207 made the backend store a plan's outcome, and mark a report that a stopped backend left RUNNING as INTERRUPTED when it starts again, so on a backend with #207 the task's setup cannot produce its premise: its last step, which checks it, fails and stops `run.py setup` partway through the list. It was retired on 2026-09-26, the day the list was fixed and before any run used it, so `fixed_at` did not change. A run directory that froze the 21-task list is refused (exit 2); start a new `--run-id`.
- **CI pass or fail.** Not an MCP case (plan §2.2).

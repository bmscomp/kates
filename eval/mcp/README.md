# MCP evaluation harness

This harness answers one question from [`plans/mcp-server.md`](../../plans/mcp-server.md): after
Phase 1, is the curated `kates mcp` server worth keeping, or should it be frozen at read-only?
It runs about 20 tasks with known answers through three arms (§2.4, §8.4):

| Arm | What the agent has |
| --- | --- |
| `mcp` | Claude Code with `kates mcp` as its only MCP server and its tools as the only tools: no shell, no files, no web. |
| `cli` | Claude Code with no MCP server, Bash limited to `kates` and `jq`, and the [`kates-cli` skill](../../skills/kates-cli/SKILL.md) loaded. |
| `expert` | A person who knows Kates, with the CLI and no AI, answering the same prompts by hand. |

Each task runs at least three times in each agent arm, a grader who cannot see the arm scores
the answers, and `report.py` applies the kill criteria of §2.5 as written:

> Continue only if the MCP arm's task success is at least equal to the CLI-plus-skill arm's AND it
> either beats that arm by at least 15 points of task success or uses at least 30 % fewer tokens
> and tool calls, AND it meets both hard gates (grounded claims at least 95 %, silent misreads 0).

The report prints PASS or FAIL for each criterion and the verdict: CONTINUE, STOP, or UNDECIDED
when a criterion has nothing to measure. Until every answer has a human verdict the verdict is
provisional, and `report.py` exits 3.

## Read this first

- **The agent arms are not sandboxed from the human's key.** Until the backend has scoped keys
  (plan Phase 2), the agent context carries the same shared key as the human one, and that key
  grants every endpoint and acts as a Kafka super user. The harness gives each trial a HOME that
  holds only the agent's context, strips `KATES_*` and `KUBECONFIG` from its environment, and
  puts wrappers in front of the CLI arm's `kates` and `jq` (below), but the key still reaches
  whatever it reaches. Run the evaluation on a Kind lab you can throw away, never on a cluster
  other people use.
- **Deny rules match strings, so the CLI arm's `kates` is a wrapper.** Claude Code's deny list
  blocks `kates test delete x` but not `kates --context lab test delete x` (§2.3), and commands
  with a `--dry-run` form have to stay allowed for the skill's dry runs. The wrapper parses each
  call (`kates_commands.refusal`) and refuses one that would start load, inject a fault or change
  the lab as written, a view that never returns, `kates mcp` (the MCP arm's tools), and the task's
  forbidden commands. It reads a call both ways cobra may (a flag before the command on its own,
  or taking the next word, as in `kates --type LOAD test create`) and `--dry-run` as pflag does
  (the last one wins). A refusal is marked in the transcript and counts as a denied call; the
  report counts any mutating call that ran anyway, which should be none. The CLI arm's `jq` reads
  only its standard input, so it cannot open the run directory's expected answers by name. What
  is left: `jq . < file` hands jq a file through the shell, which only Claude Code's Bash rules
  can refuse (the preflight tries it and warns when it runs), and an agent that guessed the
  repository's path could hand a file to a kates command that reads one (`--config`). The
  trial's environment names no path in the repository or the run.
- **The task list was fixed after both arms were built, by the people who built them.** Plan §2.4
  asks for the list to be fixed and committed before either arm is built. `kates mcp` and the
  skill were merged on 2026-09-25 and 2026-09-26, and the list was committed after them, with
  notes that cite the MCP server's caveats. Have someone who built neither arm review the task
  selection before the run, and read the verdict with this in mind; the report says it too.
- **Not built here** (plan §8.4): the context-switch case, where the kube context and
  `kates ports` change mid-session, and the tampered-plan approval case. Both need Phase 5
  (proposals and approval). Tuning tasks wait for P-14.
- **Nothing here runs by itself.** Every command that talks to the lab or to Claude is one you
  type. `--dry-run` shows all of it first, and touches nothing.

## What is here

| File | Role |
| --- | --- |
| `tasks.json` | The task list: 20 tasks, fixed on 2026-09-26, before either agent arm ran but after both were built (above). [`TASKS.md`](TASKS.md) lists them and documents the schema. |
| `taskfile.py` | The rules every part shares: what a valid task is, `{capture}` placeholders, JSON paths, `forbid`. |
| `setup.py` | Prepares the lab for each task, as the human: load, baselines, lag, faults. Writes `state.json`. |
| `oracle.py` | One function per oracle that computes a task's expected answer from the Kates API, as the human. |
| `run.py` | Setup, oracle, preflight and trials in one resumable command; `--dry-run` prints every command and config file. |
| `tasks.py` | Loads the task list for the runner, the grader and the report, and builds each trial's two turns: the question, then the answer request. |
| `arms.py` | The command line, environment, MCP config and permission rules of each arm, and the check that Claude Code started the session the arm asked for. |
| `kates_commands.py` | Which `kates` commands start load, inject faults, change state or never return. |
| `kates_api.py` | Reads CLI contexts without PyYAML, and pins the Kafka clusterId. |
| `metrics.py` | Reads a stream-json transcript: tool calls, tokens, cost, turns, the answer block, forbidden uses. |
| `grade.py` | Checks, grounded claims, silent misreads, void tasks; writes the blinded `grading.csv` and its key. |
| `report.py` | Per-arm and per-task tables, the kill criteria, the verdict. |
| `test_*.py`, `evalfixtures.py` | The tests and their shared helpers. |
| `testdata/` | Response fixtures in the backend's shapes and the answers they give, a two-task fixture for the runner, recorded transcripts, and fake `claude` and `kates`. |

Python 3.9 or later, standard library only.

## The task list

[`TASKS.md`](TASKS.md) lists the 20 tasks (what each tests, its oracle, its required caveats),
what setup does to the lab, and the schema in full. `tasks.json` was fixed before either arm ran:
`fixed_at` is the day, and it is not edited for the rest of the evaluation. The first
command of a run that gets past its checks copies it into the run directory and records its
SHA-256; every later command of that run reads the copy, and refuses a different list passed with
`--tasks`. When `tasks.json` has changed since, a run that has not yet set up a task, computed an
answer or scheduled a trial takes the list as it is now and says so; one that has keeps its copy
and warns on every command. A run that keeps a copy the harness no longer accepts, such as the
21-task list from before `sre-stale-running-disruption` was retired (TASKS.md, "Left out"), is
refused: start a new run id. What the runner relies on:

- **`prompt`** is what the user asks, the same for every arm, with no hint about tools
  (`taskfile.validate` refuses a prompt that names a tool, a command, a flag or an interface).
  Each trial has two turns, the same for every arm. The first asks only the question; its prose
  is what the caveats and grounded claims are graded on. The second resumes the same session
  with every tool denied and asks for that answer again as a fenced `json` block whose keys are
  exactly `answer_fields`, with their types; the checks read that block. An agent that saw the
  fields first would be told what to look for (a field "whether the penetration test sent
  attacks" asks the silent-misread question outright), which is why they come second.
- **`setup`** runs once per run with the human context, before the oracle (`setup.py`, whose
  step kinds are `kates`, `api`, `wait`, `clock`, `use` and `assert_equal`). Values it captures
  fill `{name}` placeholders in the prompt and the oracle's arguments; `{run_tag}`, six hex
  characters per run, keeps each run's topics, groups and specs apart from every earlier run's.
- **`oracle`** names a function in `oracle.py`'s `ORACLES` table, called as `fn(api, **args)`
  with a client for the human context. It returns the expected answer keyed by answer field,
  typed as the fields declare. A failed oracle voids its task; it does not stop the run.
- **`checks`**: `exact` (strings without case or surrounding space; numbers and booleans by
  value), `number` (with `tolerance` and `tolerance_kind` `abs` or `pct`), `set` (order- and
  case-free, for `string[]`), `contains`.
- **`required_caveats`**: each entry is met when the answer contains any one of its phrases,
  ignoring case, whitespace and the kind of dash. A missed entry is a silent misread.
- **`forbid`** (optional): `{"mcp": [tools], "cli": [command prefixes]}`, for a task whose answer
  a tool would leak (§8.4: the disruption report, when the task is to diagnose a disruption from
  its symptoms). MCP tools become `mcp__kates__<tool>` deny rules; a `kates` prefix becomes deny
  rules for every alias form of the command; any other prefix (`kubectl`) becomes
  `Bash(<prefix> *)`. Deny rules match strings, so `metrics.py` also reads every call in the
  transcript, and a trial whose forbidden call went through fails its task. It fails rather than
  being voided, so an arm cannot drop a task by leaking; the report counts forbidden uses per arm.

`python3 eval/mcp/run.py validate` checks the file and that `oracle.py` has every function it
names. `python3 -m unittest discover eval/mcp` also checks it, and with `KATES_BIN` set to a built
`kates` it checks every command and flag a setup step uses against the binary's `--help`.

Notes on what the task list can and cannot see:

- The CLI arm's trials have no kubeconfig, so `kates security audit` there reports Kyverno as not
  installed (a HIGH FAIL, and possibly a grade one letter lower). The skill tells the agent to
  leave out the `policy` checks; an oracle that reads `/api/security/audit` never sees them.
- Some facts move on their own: partition leaders, lag, live security checks (TASKS.md, "Order,
  and facts that move"). `run.py oracle --recheck` computes every expected answer again after the
  trials, and a task whose answer moved is void: `grade.py` lists it and `report.py` leaves it out
  of the kill criteria.
- `kates mcp` resources and prompts are not part of the MCP arm by default: resources are read
  through Claude Code's `ListMcpResourcesTool` and `ReadMcpResourceTool`, which the MCP arm's
  settings deny unless `--mcp-resources` allows them. `forbid` cannot hide a resource (the
  disruption timeline is one), so leave that flag off for tasks that forbid a tool.

## Prerequisites on a Kind lab

The stack from the repository [README](../../README.md#quick-start) or Tutorial 1, the CLI built
from the commit under evaluation, Claude Code, and `jq`:

```bash
make cluster && make kates-native && kates deploy     # the lab, from the repository root
kates ports                                           # forwards the API to localhost:8080

# Two contexts: the human's, for setup and the oracle, and the one the agents get.
# Until scoped keys exist (plan Phase 2) both carry the same key.
KEY=$(kubectl get secret kates-api-key -n kates -o jsonpath='{.data.api-key}' | base64 -d)
kates ctx set human --url http://localhost:8080 --api-key "$KEY"
kates ctx set agent --url http://localhost:8080 --api-key "$KEY"
kates test list --context agent -o json | jq .total   # the key works

# Claude Code runs every trial with an empty HOME, so it cannot see your own login.
export ANTHROPIC_API_KEY=...        # or CLAUDE_CODE_OAUTH_TOKEN from `claude setup-token`
```

`kates ports` never replaces a key it did not write, so it leaves both contexts alone. Keep the
port-forwards running for the whole run.

## Running an evaluation

Pick a run id and keep the same flags for every command of the run; `run.py` records the
agent settings on the first trial and refuses different ones later (start a new run id instead).

```bash
cd eval/mcp
RUN="--run-id 2026-10-lab"
AGENT="--model claude-opus-5-5"               # plus any other agent flag, the same every time

python3 run.py validate
python3 run.py all $RUN $AGENT --dry-run | less    # every kates call, oracle call, config file and claude command
python3 run.py preflight $RUN $AGENT               # pins the clusterId; one short session per arm: is each set up right?
python3 run.py setup $RUN                          # prepares the lab as the human (~30 min)
python3 run.py oracle $RUN                         # expected answers -> oracle.json
python3 run.py trials $RUN $AGENT --only-task sec-posture --max-trials 1    # a smoke run: read its transcripts
python3 run.py trials $RUN $AGENT                  # the rest; resumable
python3 run.py oracle $RUN --recheck               # did the run change any expected answer?
python3 grade.py --run-id 2026-10-lab          # grades.json, grading.csv (blinded), grading-key.csv
#   ... the human pass on grading.csv, and the expert arm (below) ...
python3 grade.py --run-id 2026-10-lab          # again: keeps row ids and the grader's entries
python3 report.py --run-id 2026-10-lab         # report.md and the verdict
```

The first of `preflight`, `setup` and `trials` to run pins the Kafka clusterId the human context
reaches. Setup and the oracle then check that the human context still reaches it, and preflight
and every trial that the agent context does. Preflight needs nothing from
setup, so run it first: it costs cents and tells you whether both arms start right before setup
spends half an hour on the lab. `run.py all $RUN $AGENT` does setup, oracle, trials and the
recheck in one go. Flags for every
phase: `--arms mcp` or `--arms cli`, `--only-task ID` (repeatable), `--human-context`,
`--agent-context`, `--kates-bin`, `--tasks`, `--oracle`. Agent flags (preflight, trials, all):
`--model` (required), `--max-turns` (default 30), `--max-budget-usd` (a per-trial cap passed to
Claude Code), `--protocol-negotiation legacy|auto` and `--sdk-generation v1|v2` (below),
`--seed`, `--max-trials N`, `--skill-mode system-prompt|project`, `--mcp-resources`,
`--trial-context`, `--claude-bin`, `--redo-invalid`, `--keep-sandbox` (keeps each trial's HOME,
which holds the agent key, for debugging).

**Resuming.** A trial is done when its `metrics.json` exists; running the same command again
runs only what is missing. An interrupted trial has no `metrics.json` and runs again.

**Invalid trials.** A trial is invalid, and not scored, when the harness or the service failed
rather than the agent: Claude Code produced no result or never started the session
(`no_result`); the API failed (`infra_error`: an overload, a 5xx, a rate limit or bad
credentials, which Claude Code reports as a message of its own, or a failure before the model
said anything); the agent's context no longer reached the pinned cluster after the trial
(`infra_error`: a dead port-forward); the answer turn failed the same ways or timed out (it has
no tools, so only the API can hold it up); or Claude Code
started a session other than the one the arm needs (`config_violation`: a shell in the MCP arm,
the kates server not connected, another permission mode or model). `run.py trials
--redo-invalid` moves them aside as `trial-<n>.invalid-<k>` and runs them again. Running out of
turns, of budget or of time counts against the agent.

**Void tasks.** A task whose oracle failed gets no trials (`run.py trials` says which) and is
left out of the kill criteria, as is one whose expected answer moved during the run.

**Setting up again.** A setup that failed leaves the lab partly prepared, and running a task's
setup twice would create its topics and runs twice. Start a new `--run-id` instead, which gets a
new `run_tag`; `run.py setup` on the same run id only retries the tasks whose setup failed.

**The human pass.** `grading.csv` has one row per answer, arm hidden, rows in a seeded random
order: the task, its prompt, the expected answer, the answer's JSON and prose, the automatic
checks, the missed caveats and the ungrounded numbers. The prose has its code, `kates` commands
and MCP tool names replaced by `[code]`, `[command]` and `[tool]`, which would name the arm.
Fill `human_success` (yes or no), `human_misread` (yes when the prose misses a caveat the data
carries, whatever the phrase matching found), `human_ungrounded` (how many of the answer's
numbers no tool result supports; a number correctly derived from them, such as a difference,
counts as supported) and `human_notes`. The report uses the human entries wherever there are
some, and calls itself provisional until every row has a verdict. `grading-key.csv` maps rows to
arms; keep it closed until the pass is done. Answers can still betray their arm by style;
graders are asked to judge the answer, not its style. The grader, like the expert, should be
someone who did not build either arm.

**Exit codes of `run.py`:** 0 done; 2 bad input (task list, flags, or settings that differ from
the run's); 3 the environment is not ready (a binary, a context or credentials missing, or the
agent context reaches another cluster); 4 a setup step failed, or an oracle function did (its
task is void, the others still run); 5 finished with invalid trials; 6 preflight found a
problem; 130 interrupted (also on SIGTERM, and on SIGHUP unless it runs under `nohup`; both
stop the trial's claude and `kates mcp` too). `grade.py`: 0, 2 unreadable run, 3 a task has no expected answer.
`report.py`: 0 CONTINUE, 1 STOP, 3 the data cannot decide yet (provisional, or nothing to
measure), 2 no grades.

## How the arms run, and why the comparison is fair

Every trial is two `claude -p … --output-format stream-json --verbose` calls: the question, then
`--resume <session>` with the answer request, at most two turns, 180 seconds, no budget cap
(a resumed session carries the first turn's cost) and every tool denied. Both calls run with:

- the same `--model`, `--max-turns`, timeout, question and answer request in both arms;
- `--permission-mode dontAsk` and `--permission-prompts none`: anything no rule allows is refused,
  never prompted for; `--strict-mcp-config`; the session is kept in the trial's HOME for the
  answer turn, and removed with it;
- a fresh temporary directory outside the repository as HOME and working directory, so no
  `CLAUDE.md`, settings, memory or git status leaks in; `~/.kates.yaml` there holds one context,
  named `lab` (the name the skill's recipes use), built from the agent context, mode 0600;
- an environment built from an allowlist: Claude Code's credentials and proxy settings, the
  locale, `PATH` with only the trial's `kates` (a link to the binary in the MCP arm, the wrapper
  in the CLI arm) and the CLI arm's `jq` wrapper before the system directories, and `MCP_PROTOCOL_NEGOTIATION`, `MCP_SDK_GENERATION`,
  `MCP_CONNECTION_NONBLOCKING=0`, `DISABLE_AUTOUPDATER=1`. No `KATES_*`: a `KATES_API_KEY` would
  replace the context's key, and `kates mcp` refuses `KATES_URL`.

The **MCP arm** adds `--mcp-config` with one stdio server, `kates mcp --context lab
--allow-cluster <clusterId> --cluster-label lab`, marked `alwaysLoad` so its tool definitions are
in context from the first turn rather than behind tool search; `--tools ""`, which removes every
built-in tool; and the allow rule `mcp__kates__*`. The **CLI arm** keeps `--tools
Bash,Read,Write,Edit`, allows `Bash(kates *)`, `Bash(jq *)` and the file tools inside its working
directory (the skill's game-day recipe writes `plan.json` before `kates disruption run --config
plan.json --dry-run`, where the MCP arm passes the same plan inline to `preview_disruption`),
denies the commands in `kates_commands.RULES` that have no dry-run form and `kates mcp`, and puts
the `kates` and `jq` wrappers described under "Read this first" on its `PATH`. The wrappers run
the real binaries with only `HOME`, `PATH`, `TMPDIR` and the locale, so no credential of Claude
Code's can reach a command's output.

**The skill goes in the system prompt** (`--append-system-prompt`) rather than
`.claude/skills`. The MCP arm's tool descriptions and caveats are in context from the first turn
without the model asking for them; a project skill loads only when the model decides to invoke
it, which would measure skill discovery as well as the interface, and a missed invocation would
count against the CLI. The system prompt gives the CLI arm the same standing start, which is the
fairer comparison and the harder one for the MCP arm to win. The skill's tokens count in the CLI
arm's total, as the tool definitions do in the MCP arm's. `--skill-mode project` runs the other
reading.

**Checked, not assumed.** Each transcript begins with Claude Code's own account of the session
(tools, MCP servers and their status, permission mode, model). `arms.check_init` compares it
with what the arm asked for, and a mismatch invalidates the trial, so a settings file Claude
Code ignored or a `kates mcp` that refused to start cannot skew the result. `run.py preflight`
runs this check before the paid run: one allowed call per arm, the answer turn (it must resume
the session and restate the answer as JSON without a tool call), and in the CLI arm three calls
that must be refused (`kubectl`, a `kates --context lab test create`, `jq` on a named file) and
one that should be (`jq . < /etc/hosts`, a warning when it runs). It also prints what the
answer turn cost.

**Protocol era.** `--protocol-negotiation` sets `MCP_PROTOCOL_NEGOTIATION` explicitly (§8.4),
`legacy` by default; `--sdk-generation` pins Claude Code's MCP client runtime (`v2` by default),
because the negotiation setting acts only on the v2 runtime. With `legacy`, stdio stays on the
pre-2026 handshake (2025-11-25 at most); with `auto`, Claude Code asks `kates mcp` for
2026-07-28, which go-sdk v1.8.0 accepts. The report names the era the run configured. Neither
the transcript nor the `kates mcp` log records the revision actually negotiated.

Claude Code flags were checked against `claude --help` of Claude Code 2.1.278. `--max-turns` is
not in that help text but is defined in the same binary ("Maximum number of agentic turns in
non-interactive mode"). The permission-rule forms (`mcp__kates__*`, `Bash(kates *)`,
`Write(//path/**)`), `alwaysLoad`, `MCP_SDK_GENERATION` and `MCP_CONNECTION_NONBLOCKING` come from
Claude Code's MCP and permissions documentation; the preflight is how a lab run confirms them.

## What is measured

Per trial, from the transcript (`metrics.py`) and the grade (`grade.py`):

- **Task success**: every check passes against `oracle.json`, and no forbidden call went through.
  A human verdict in `grading.csv` replaces the automatic one, except that a trial whose
  forbidden call went through fails whatever the answer says. Rates count every valid trial.
- **Grounded-claim rate**: the share of numbers in the answer (prose and JSON block) that appear
  in a tool result of the same trial or in the prompt. A tool result's numbers are its numeric
  JSON values when it is JSON, else its whole-token numbers, so digits inside ids, timestamps and
  versions are not references. A number converted between milliseconds, seconds and minutes
  still matches (27.9 s for 27912 ms). A number matches at the precision it is
  written with (12.35 for 12.3456, 12 for 11.6, 1.2k for 1234), a percent matches its fraction
  (12.5 % and 0.125), signs and thousands separators are ignored. Not counted as claims: dates,
  times, versions, addresses, identifiers with digits (`payments-0`, `p99`, a run id), list
  markers, and anything in code, which is a command rather than a statement. Limits: a correctly
  derived number (a difference, a ratio) counts as ungrounded, a wrong number that happens to
  equal some other number in a tool result counts as grounded, small integers are grounded almost
  always, and numbers written as words are not seen. The ungrounded numbers of each answer are
  listed for the human grader, whose `human_ungrounded` count replaces the automatic one. The
  gate is on the MCP arm, pooled over its numbers.
- **Silent misreads**: answers whose prose (the first turn, without its code) misses a required
  caveat. An empty answer (out of turns, timed out) fails its task but misleads nobody, so it is
  not a misread.
- **Tool calls**: every tool use in the question turn. **Tokens**: from the question turn's
  result event, input plus output plus cache writes plus cache reads, the context each arm
  consumed; the answer turn only restates the answer, so it is left out of tokens and tool calls
  and counted in cost. A resumed session reports the cost of both turns together, so the answer
  turn's own cost is its figure less the first turn's (`metrics.json` keeps both). **First-request context**: what the model
  read before any work (Claude Code's system prompt, the tool definitions or the skill, the
  question), the fixed part of every request; it shows how much of an arm's tokens is overhead.
  **Wall-clock**: measured by the harness around the question turn; Claude Code's own
  `duration_ms` is kept too.
- **Mutating calls**: `kates` invocations that would start load, inject a fault or change state
  without `--dry-run`, and ran (§2.4 metric 5: 0). The MCP server sends no such request by
  construction, and the CLI arm's wrapper refuses them; `metrics.json` also lists the attempts.
  **Forbidden uses**: trials whose forbidden tool or command ran.

Means are per trial, over the tasks both arms ran. "30 % fewer tokens and tool calls" needs both.

## Cost and time

Assumptions for one trial, to be replaced by measurement: about 12 model turns; a context that
starts near 20,000 tokens (Claude Code's system prompt plus the tool definitions or the skill)
and grows to about 50,000, so about 420,000 input tokens read over the trial, of which about
380,000 are cache reads and 40,000 cache writes; and about 8,000 output tokens including
thinking. With the prices listed in Claude Code's API reference (per million tokens; confirm on
the pricing page before relying on them):

| Model | Input | Cache write (5 min) | Cache read | Output | One trial | 20 tasks x 2 arms x 3 trials |
| --- | ---: | ---: | ---: | ---: | ---: | ---: |
| `claude-opus-5-5` | $4 | $5 | $0.20 | $20 | about $0.44 | about $53 |
| `claude-sonnet-5` | $2 | $2.50 | $0.20 | $10 | about $0.26 | about $31 |

A trial that uses all 30 turns with a 100,000-token context costs several times the estimate;
`--max-budget-usd 3` caps one. At one to four minutes per trial, the 120 trials take two to
eight hours in sequence (the harness runs one trial at a time, so trials do not compete for the
lab), plus about 30 minutes of setup, most of it three broker kills and the waits after them. Measure instead of trusting this: after the preflight and the smoke
run, `metrics.json` holds each trial's `cost_usd`, `tokens` and `wall_clock_s`; multiply their
mean by the number of trials left.

## The expert arm

```bash
python3 run.py expert-template --run-id 2026-10-lab
```

writes `expert-prompts.md` (every task's question, placeholders filled, with its answer fields
folded away below it) and `expert-template.csv`. The expert is someone who built neither arm and
has not seen `tasks.json`, `TASKS.md` or the expected answers. They work through the questions in
order with the CLI (`kates`, `jq`, and the Kates docs; no AI), in a terminal on the same lab after
the agent trials, and, like the agents, write the prose answer before they open the answer
fields. For each task they time themselves from reading the question to finishing the answer and
fill one row: `answer_json` (the same keys as the agents' answer block), `answer_text` (the prose
answer, with its caveats), `wall_clock_s`, `commands`
(how many CLI commands they ran) and `notes`. Save it as `expert.csv` in the run directory and
rerun `grade.py`: the expert's answers are graded with the same checks and caveats and join the
blinded sheet. The report shows the expert arm beside the agents; it is context for the
decision, not part of the kill criteria. Grounded claims and tokens do not apply to it.

## Run layout

```text
eval/mcp/runs/<run-id>/
  tasks.json  manifest.json  state.json  schedule.json
  oracle.json  oracle-recheck.json
  setup/<task>/step-<n>.out|err|json
  preflight/<arm>/...
  <task>/<arm>/trial-<n>/
    prompt.txt  transcript.jsonl  stderr.log               the question turn
    answer-request.txt  answer.jsonl  answer-stderr.log    the answer turn
    answer.json  metrics.json  config.json
  grades.json  grading.csv  grading-key.csv  expert.csv  report.md
```

`manifest.json` holds the run's settings (model, turns, budget, protocol era, skill hash and mode,
seed, kates binary hash, clusterId, task list hash, Claude Code version, whether the agent key
was the human's); `state.json` the run's `run_tag`, each task's setup status, captures and step
log; `config.json` each trial's command line (the
prompt and skill by reference), environment (secrets redacted), settings and MCP config.
`runs/` is ignored by git.

## Tests

```bash
python3 -m unittest discover eval/mcp
```

The tests need no cluster, Docker, network or Claude: a fake `claude` replays recorded
transcripts and rewrites their init event from its own command line, a fake `kates` answers the
setup and context commands, a fake Kates API listens on 127.0.0.1, and every oracle runs on
response fixtures in the backend's shapes (`testdata/api/`). With `KATES_BIN` set, the
cross-checks run the built CLI's `--help`:

```bash
(cd cli && go build -o /tmp/kates .) && KATES_BIN=/tmp/kates python3 -m unittest discover eval/mcp
```

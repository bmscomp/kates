# Run Analyst

When a Kates test run finishes, the Run Analyst says whether it is normal, a regression, an
improvement, or something that cannot be judged, and why. It posts to Slack or GitHub only when
the answer is not normal. The design, and what it may and may not do, is in
[`plans/autonomous-agents.md`](../../plans/autonomous-agents.md) §2.

It reads Kates through `kates mcp` with an agent key that can only read. The model has no shell,
files or network, and does not choose where its answer goes. Its answer is checked in code
(`verdict.py`) before anything is posted: the verdict must follow from its own evidence fields,
every number it writes must be in a field, and caveats are named by id only. Each session's cost
is logged, whatever its outcome.

| File | Role |
| --- | --- |
| `receiver.py` | The `test.completed` webhook endpoint, the worker behind it, and the SQLite store (dedupe, outcomes, budget) |
| `poll.py` | The same analyses with no webhook: reads newly finished runs every minute |
| `analyst.py` | One Claude Agent SDK session with `kates mcp` as its only tools, and one retry |
| `prompt.md` | The analyst's instructions |
| `verdict.py` | The answer's schema, the verdict rules, and the checks |
| `sinks.py` | Log, a JSON file per run, Slack, a GitHub issue per run |

## Before you start

1. **An agent key.** Add an entry to the Kates API's keys file (`kates.api.keys-file`, see the
   book's API reference) with `type: agent` and `scopes: [read]`, and keep the key for step 3.
   Kates refuses an agent key any scope that starts load or faults.
2. **A context and the cluster id.** `kates mcp` takes its URL only from a named context:

   ```bash
   kates ctx set lab --url http://localhost:30083
   kates cluster info --context lab
   ```

3. **Python 3.11 or newer**, the Agent SDK, and the `kates` binary on `PATH` (or `KATES_BIN`):

   ```bash
   python3 -m pip install -r agents/run-analyst/requirements.txt
   ```

   The SDK needs Claude credentials: `ANTHROPIC_API_KEY`, or a profile from `ant auth login`.

## Configuration

| Variable | Default | Meaning |
| --- | --- | --- |
| `KATES_CONTEXT` | required | The context `kates mcp` serves |
| `KATES_API_KEY` | required | The agent key; given to `kates mcp` in its environment, never on its command line |
| `ANALYST_KATES_URL` | required | The same API as the context's URL, for the analyst's own reads. Not `KATES_URL`: `kates mcp` refuses that, and the analyst refuses to start with it set |
| `ANALYST_CLUSTER_ID` | required | Passed to `kates mcp --allow-cluster` |
| `ANALYST_CLUSTER_LABEL` | `lab` | Shown in every tool result |
| `ANALYST_MODEL` | `claude-opus-5-5` | The model |
| `ANALYST_MAX_PER_HOUR` | `20` | Analyses started per hour; past it a run is skipped with a one-line notice |
| `ANALYST_MAX_USD_PER_RUN` | `2` | The session's cost cap (the SDK's estimate) |
| `ANALYST_STATE_DIR` | `state` | The SQLite store and `analyses/<run id>.json` |
| `ANALYST_POLL_SECONDS` | `60` | Poll mode's interval |
| `ANALYST_SLACK_WEBHOOK_URL` | unset | A Slack incoming webhook |
| `ANALYST_GITHUB_REPO`, `GITHUB_TOKEN` | unset | `owner/name` and a token that can write issues; issues get the label `run-analyst` |

Each session logs its cost as `session cost for run <id>: <USD> USD, <turns> turns`. On a kind lab,
one LOAD analysis with `claude-opus-5-5` took 5 turns and cost $0.89 (2026-10-29).

## Run it

**Poll mode** works from a laptop against a kind lab, with no webhook. On an empty store, the
first poll marks the runs already there as seen without analysing them.

```bash
cd agents/run-analyst
python3 poll.py
```

**Webhook mode** runs in the cluster, behind a Service: Kates refuses to deliver to loopback.

```bash
python3 receiver.py --listen 0.0.0.0:8080
```

A person with an `admin` key registers it once (the analyst's key cannot):

```bash
curl -sS -X POST "$KATES/api/webhooks" -H "X-API-Key: $ADMIN_KEY" -H 'Content-Type: application/json' \
  -d '{"name": "run-analyst", "url": "http://run-analyst.kates.svc:8080/webhook", "events": "test.completed"}'
```

Kates signs no webhook body, so the receiver treats one as a hint naming a run id. It answers
`202` at once, reads the run back with the agent key, and drops the event quietly when the run
does not exist, has not finished, or was created more than 48 hours ago.

## What it posts

- Every answer to the log and to `state/analyses/<run id>.json`.
- Verdicts other than `NORMAL` to Slack and to one GitHub issue per run.
- One line to Slack when a run is skipped for the budget, Kates cannot be read, or the analysis
  failed (`ANALYST_ERROR`, after one retry).

A cancelled run ends as `FAILED`, so the analyst reports it as `INVALID_RUN`.

## Tests

```bash
cd agents/run-analyst
python3 -m unittest discover -p 'test_*.py'
```

They need no model, no Kates and no SDK. CI runs them in `CI · Lint`.

Not built yet: the eval tasks and the rules-only arm (plan §2.5, PR 2), and the container image
and chart (PR 3).

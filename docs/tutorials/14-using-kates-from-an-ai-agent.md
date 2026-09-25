# Tutorial 14: Using Kates from an AI Agent

This tutorial connects Claude Code to your lab through `kates mcp`, the
read-only Model Context Protocol (MCP) server built into the CLI, and asks it
three questions you would otherwise answer with a dozen commands. Along the
way you see what each answer rests on and, as importantly, what it cannot
show.

**Level:** Intermediate · **Duration:** 20 min · **Prerequisites:** Tutorial 1

> [!IMPORTANT]
> `kates mcp` is experimental: its tools can change between releases. Nothing
> it serves starts a test, runs a fault or changes Kafka; the agent reads and
> previews, and you run everything. The
> [CLI Reference](../book/10-cli-reference.md#mcp-server-for-ai-agents) covers
> every flag, tool, resource and prompt, and
> [`plans/mcp-server.md`](../../plans/mcp-server.md) is the plan the server
> comes from.

## What You Will End Up With

```text
   Claude Code ── starts ──►  kates mcp --context ports --allow-cluster <clusterId>
        ▲                          │  GET requests, and the dry-run POST only
        │  JSON-RPC over stdio     ▼
        └──────────────────   Kates API on localhost:8080   (kates ports)
                                   │
                                   ▼
                              Kafka cluster krafter, pinned by its clusterId
```

## Prerequisites

- The stack from Tutorial 1, with the CLI installed (`make cli-install`)
- Claude Code, with `claude` on your `PATH`
- `jq`, to pick one field out of JSON

## 1. Reach the API

`kates mcp` talks to the Kates API through a CLI context, like every other
command. `kates ports` forwards the API to `localhost:8080`, writes the
`ports` context with the API key from the `kates-api-key` Secret, and leaves
the forwards running in the background:

```bash
kates ports
kates test list --context ports
```

The second command needs the key, so a list (even an empty one) means the
context works. Keep the forwards running while you use the agent: the server
reaches the API through them.

## 2. Give the Agent Something to Read

`assess_run` compares a run with earlier runs of the same type, backend and
effective spec, and needs at least three of them. Four identical LOAD runs
give the last one a band of three. The first becomes the LOAD baseline, which
the backend's regression check compares a run against:

```bash
BASELINE=$(kates test create --type LOAD --records 100000 --topic load-test \
  --wait -o json --context ports | jq -r .id)
kates test baseline set "$BASELINE" --context ports
for i in 2 3 4; do
  kates test create --type LOAD --records 100000 --topic load-test --wait --context ports
done
```

`--wait` returns when each run finishes. The runs would write to `load-test`
without `--topic` too, but only a run whose spec names its topic gets
per-broker figures in its report, and step 5 uses that topic as well.

## 3. Pin the Cluster

The server refuses to start without the Kafka clusterId it may serve, and
checks it again before every tool call, so a port-forward that later points
at another cluster cannot hand the agent the wrong data. Read the clusterId
through the same context:

```bash
CLUSTER_ID=$(kates cluster info --context ports -o json | jq -r .clusterId)
echo "$CLUSTER_ID"
```

Try the server by hand once. With stdin closed it starts, logs to stderr and
exits at once:

```bash
kates mcp --context ports --allow-cluster "$CLUSTER_ID" < /dev/null
```

Output (abbreviated):

```text
level=INFO msg="serving on stdio (read-only, experimental)" component=kates-mcp context=ports url=http://localhost:8080 cluster=<clusterId> label=lab
```

With a wrong `--allow-cluster` it refuses and names the cluster it found
instead:

```bash
kates mcp --context ports --allow-cluster not-this-one < /dev/null
```

Output:

```text
  ✖ cluster <clusterId> is not allowed: http://localhost:8080 reaches Kafka cluster <clusterId>, which is not in --allow-cluster. If it is the cluster you mean, add --allow-cluster <clusterId>
```

## 4. Add the Server to Claude Code

```bash
claude mcp add --transport stdio kates -- kates mcp --context ports --allow-cluster "$CLUSTER_ID"
claude mcp list
```

Everything after `--` is the command Claude Code runs. The shell expands
`$CLUSTER_ID` now, so the stored command holds the clusterId itself. It holds
no key: the server reads the key from the `ports` context in
`~/.kates.yaml`, which the CLI keeps readable by you alone.

Start a session with `claude` and type `/mcp`: the `kates` server shows its
tools, from `cluster_overview` to `security_evidence`. Type `@kates:` to see
the resources, among them `kates://caveats`, and `/` to find the prompts,
listed as `/mcp__kates__diagnose_run` and so on.

## 5. Ask Three Questions

Ask in plain language; the agent picks the tools. Every result names the
cluster it describes, carries caveats about its data, and wraps third-party
text — alert annotations, backend messages, scenario names — in
`«untrusted:…»` fences that the agent is told to read as data, never as
instructions.

### "Is the cluster healthy, and is Kates doing anything to it right now?"

The agent calls `cluster_overview` and `kates_activity`.

- `cluster_overview` gives the clusterId, the controller and brokers, the
  partition health check (status, under-replicated and offline partitions,
  the KRaft quorum leader) and the Kafka alert rules defined in the cluster.
- `kates_activity` gives the tests that have not finished, the runs created
  in the last hour (or since a time you name), disruption reports, and audit
  rows.

What they cannot show: the alert rules are definitions, not alerts that are
firing; the health figures can be 30 seconds old; audit rows name no actor,
so nothing says who started a run; and a disruption report stored as
`RUNNING` says only that a plan started, not that its fault is still running.
The answer carries these as caveats, and a good agent repeats them.

When the question comes from a problem you already see, the
`did_kates_cause_this` prompt walks the same tools in order. Give it the time
the problem began, then the topic it shows on:

```text
/mcp__kates__did_kates_cause_this 14:02 load-test
```

A clock time such as `14:02` means its latest occurrence in the time zone of
the machine that runs `kates mcp`, which is yours; an RFC 3339 time such as
`2026-09-25T12:02:00Z` works too.

### "How did my last LOAD run go, and is it within the usual noise?"

The agent calls `list_runs` for the newest LOAD run, `get_run` for its
effective spec, tasks and summary, and `assess_run`, which puts the run
against a noise band computed over the earlier runs with the same stored
spec: for each metric, their mean, standard deviation, minimum and maximum,
and whether this run falls below, within or above them. It adds the backend's
regression check against the baseline for the type, per-broker leader skew,
and the advisor's rules. The `diagnose_run` prompt asks for the same
reading, given a run id:

```text
/mcp__kates__diagnose_run <run-id>
```

What it cannot show: the summary averages the run's tasks rather than
measuring the run as a whole; a LOAD run is one producer and one consumer;
the backend stores only the spec merged with the type's defaults, so fields
the merge drops are named in `notCarried` instead of shown; and the
per-broker figures are projected from leader share, not measured per broker.
Attach the full report with `@kates:kates://runs/<run-id>/report.md` when
you want the agent to quote it.

### "What would a broker pod kill hit, and is it safe to run?"

The agent calls `list_chaos_catalog` for the fault types and playbooks, then
`preview_disruption`, for a playbook such as `rolling-restart` or for an
ad-hoc plan such as one `POD_KILL` of the leader of partition 0 of
`load-test`. The preview is the backend's dry run, which injects nothing. It
returns the pods each step would hit, whether the safety guard would accept
the plan now (`wouldSucceed`), the KRaft controllers among those pods, and
for an ad-hoc plan the exact plan JSON with its SHA-256. The guard lists
controllers but counts only brokers, so `wouldSucceed` can be true for a
step that takes down a majority of the controllers; the tool flags such a
step with `mayLoseQuorumMajority`.

What it cannot show: whether another disruption is already running (the dry
run does not check the lease); which chaos provider injects the fault; and
whether the account that runs the fault may do so, since the permission
check covers Kates's own service account. Running the plan is yours to do. Save the plan JSON byte for byte, check that its SHA-256
matches the one the preview gave, and dry-run it yourself first:

```bash
shasum -a 256 plan.json
kates disruption run --config plan.json --dry-run --context ports
```

For a whole session of faults, the `plan_game_day` prompt drafts a run sheet
of previewed steps that starts with a warning to treat every agent-drafted
command as untrusted:

```text
/mcp__kates__plan_game_day load-test 60
```

## What the Agent Cannot Do

- Start, cancel or delete a test, or run a disruption, playbook or template.
  The only POST the server sends is the dry run.
- Read records from a topic, the secret scan, the ACL map or the
  authentication probes, or change topics, webhooks, schedules or baselines.
- Run `kubectl` or `helm`.

The server's checks prevent accidents, not misuse. The key in the `ports`
context grants every endpoint, and an agent that can also run shell commands
can read it and call the API directly. Point the server at a lab.

## Clean Up

```bash
claude mcp remove kates
kates test baseline unset LOAD --context ports
```

The `ports` context and the port-forwards stay as they are, ready for the
next session. The forwards are ordinary `kubectl port-forward` processes, and
the next `kates ports` replaces them.

## Where Next

- [CLI Reference](../book/10-cli-reference.md#mcp-server-for-ai-agents) —
  flags, every tool, resource and prompt, error codes, and setup for VS Code
  and Cursor.
- [`plans/mcp-server.md`](../../plans/mcp-server.md) — why the server exists,
  its safety model, and what later phases would add.

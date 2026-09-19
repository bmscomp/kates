# Kafka MirrorMaker 2 — the migration board

**Can I cut over yet, and if not, what is left?**

Every panel here answers that one question. The
[mirror board](../mirror-maker2/README.md) is for the months a mirror runs;
this is for the hours a migration takes, which is why it refreshes every 10
seconds and defaults to a one-hour window. Its header links straight to the
runbook's cutover checklist, which is the thing it is built around.

Delivered by `charts/mirror-maker2` (`dashboard.migration.enabled`,
independent of `dashboard.enabled` — during a migration this is often the only
board anybody opens). Turn it on **before the first record crosses**, not
after the first problem: lag is only useful with history behind it, and the
catch-up ETA needs ten minutes of record age before it means anything.

## Who opens it, and when

The runbook's checklist, and where each step reads:

| Runbook step | On this board |
|---|---|
| Before you start | **Can I cut over yet?**, plus the per-topic and per-group tables |
| 1. Stop the producers | **Not visible here** — confirm end offsets on the source, twice, 60s apart |
| 2. Let the mirror drain | **Draining**: lag and record age falling, records in flight at zero, the ETA agreeing |
| 3. Apply the cutover | **Cutover state**: source connector STOPPED, checkpoint still RUNNING |
| 4. Move the consumers | **Consumer groups**: translation age per group |
| 5. Move the producers | Nothing here — the target is live and this board is about the mirror |
| 6. Retire the mirror | Both connectors gone; the board goes empty, which is the expected end state |

## The sections, and what a move means

**Can I cut over yet?** — *Safe to cut over?* reads DRAINED when all three of:
replication lag below 5000ms, nothing in flight, no failed tasks. It reads
**No data** when the workers are not reporting at all, and that is the whole
reason its query has a fourth factor: every other factor falls back to its
healthy value when its series is absent (`or vector(0)`), so without a factor
tied to the workers' own task count a release whose scrape had stopped — the
exact situation in which nobody should be cutting over — would have satisfied
all three conditions and shown green.

It does **not** know whether producers have stopped writing to the source. No
MirrorMaker metric says that; it needs the source broker's own metrics, and on
a migration the source is usually the old cluster nobody instrumented. So
DRAINED means *the mirror has caught up with what it was given*, not that the
source is quiet.

*Catch-up ETA* extrapolates from how fast record age has been falling over ten
minutes. Negative means the age is rising — the mirror is losing to the
producers and no amount of waiting will drain it. That is the signal to add
tasks (`sourceConnector.tasksMax`, toward the source's partition count), not
to wait longer.

**Draining.** The three numbers that have to reach zero, per topic. A topic
that flattens above the line while the others fall is the one holding the
cutover up — usually a partition whose task died, or a topic still being
produced to. *Records crossing /s* falling to zero while lag is still high
means something **stopped**, not that it finished.

**Consumer groups — will they resume in the right place?** A group missing
from the table has no position on the target: move it and it starts from its
`auto.offset.reset`, which on a migration means replaying the topic or
skipping everything already replicated. Amber past 60000ms of translation age.

**Topics — what is crossing.** The cutover's punch list: a topic is done when
lag and record age are both at zero and stay there. The names are the
*replicated* names, which under a prefixing policy start with the source alias
and under an identity policy are the source's own — the row title and the
table's description say which, because it is the difference between "this
topic is missing" and "this topic is named what I expected".

**Cutover state.** The cutover primitive is
`cutover.sourceConnectorState: stopped`, and `stopped` (unlike `paused`)
*releases the connector's tasks* — which is the point: a worker restart cannot
then quietly resume replication behind producers that have already moved. In
metrics that reads as:

| Panel | Before | After |
|---|---|---|
| Source connector | RUNNING | STOPPED |
| Checkpoint connector | RUNNING | RUNNING — it must stay that way |
| Still replicating /s | whatever is crossing | zero, and staying zero |
| The freeze, per connector | two lines | one at zero, one still moving |

Both connectors at zero is the classic cutover mistake: every record on the
target and no consumer knowing where to start reading it.

## What neither board can tell you

- **Whether anything is still producing to the source.** Needs the source
  broker's metrics.
- **End-offset parity between source and target.** Lag and record age are
  proxies for "caught up"; comparing real end offsets is
  `kates migrate target offsets <topic>` or `kafka-get-offsets.sh` on both
  sides.
- **Whether a consumer actually resumed** after moving. Translation age says a
  position exists on the target, not that anything used it.
- **Anything at all under `metrics.type: strimziMetricsReporter`** — different
  names, empty panels.

## Variants

| Variant | Chosen when | Difference |
|---|---|---|
| `default` | prefixing replication policy | |
| `identity` | an identity policy | the Topics row title and the per-topic table's description say the replicated names are the source's own |

Nothing else on this board depends on the policy, and the panel list is 19
either way.

## What the chart injects, and what is frozen

`charts/mirror-maker2/templates/dashboard-migration.yaml` loads this file with
`.Files.Get` and sets `uid` (`kates-mm2-mig-<name, 17>-<sha256 of the whole
name, 8>` — the scheme this replaces truncated to 26 characters and two
releases sharing that prefix overwrote each other's board), `title`, `links`
(from `alerts.runbookBaseUrl`) and the hidden `$namespace` constant that every
expression on the board selects on.

**Frozen at generation time:**

| Number | Value | Where |
|---|---|---|
| `dashboard.migration.drainedBelowMs` | 5000 | the red line on lag, the amber cell in the topic table, **and the `< bool 5000` inside the go/no-go query** |
| `dashboard.migration.translationFreshMs` | 60000 | the amber cell on translation age |

`drainedBelowMs` is the reason this is frozen rather than templated: it sits
inside a PromQL comparison, where a `$` does not parse — the "every dashboard
query is valid PromQL" gate would reject it, and `promtool` is right to. A
release that sets either value in `values.yaml` no longer moves the board;
changing the board means editing `board.py` and running
`scripts/gen-dashboards.py`. That is the one thing this port gives up, and it
is worth knowing before someone sets `drainedBelowMs: 500` and trusts the
colour.

## The panel CI extracts by name

`ci-mirror-maker2.yml` pulls the panel titled exactly **`Safe to cut over?`**
out of the *rendered* ConfigMap and runs `promtool test rules` over five cases
from `scripts/metric-contract/tests/mirror-maker2.cutover-gate-test.yaml`:
drained, still behind, still in flight, a failed task, and a release reporting
nothing at all. Rename that panel and the test silently checks nothing — the
extraction step fails loudly for exactly that reason.

The test's input series are all `namespace="kafka"`, and the board now selects
on `namespace="$namespace"`. The extraction step interpolates the board's own
`constant` variables before handing the query to promtool, which is what
Grafana does when it draws the panel; it also refuses an expression that still
carries a variable afterwards, so a future panel that used a *query* variable
here could not quietly stop being tested.

## Editing it

`board.py`, then `scripts/gen-dashboards.py`, then
`python3 scripts/check-dashboards.py`. Never edit `dashboard.json` or the
copies under `charts/mirror-maker2/files/dashboards/`.

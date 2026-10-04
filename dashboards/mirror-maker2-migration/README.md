# Kafka MirrorMaker 2 — the migration board

**Can I cut over yet, and if not, what is left?**

Every panel here answers that one question. The
[mirror board](../mirror-maker2/README.md) is for the months a mirror runs;
this is for the hours a migration takes, which is why it refreshes every 10
seconds and defaults to a one-hour window. Its header links straight to the
runbook's cutover checklist, which is the thing it is built around.

Delivered by `charts/monitoring` with every other board (`dashboards.enabled`
there) since mirror-maker2 0.11.0 — during a migration this is often the only
board anybody opens. Turn the chart's `metrics.enabled` and
`podMonitors.enabled` on **before the first record crosses**, not after the
first problem: lag is only useful with history behind it, and the catch-up
ETA needs ten minutes of record age before it means anything.

## Who opens it, and when

The runbook's checklist, and where each step reads:

| Runbook step | On this board |
|---|---|
| Before you start | **Migration window** for when — the quiet hours the last week points at; then **Can I cut over yet?**, plus the per-topic and per-group tables |
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
healthy value when its series is absent, so without a factor tied to the
workers' own task count a release whose scrape had stopped — the exact
situation in which nobody should be cutting over — would have satisfied all
three conditions and shown green.

Every *other* panel in this section now carries the same protection in a
reusable form. `Replication lag`, `Records in flight`, `Failed tasks`,
`Still replicating /s`, `Errors /s` and both connector-state tiles draw their
zeros through an **anchored** fallback, tied to the workers' own rebalance
series. This is the board where that matters most: every reading here is a
cutover decision and every one of those decisions is spelled zero — lag zero,
nothing in flight, no failed tasks, source connector STOPPED, nothing still
replicating, no errors. A release Prometheus is not scraping produces exactly
that picture, and the picture means *cut over now*. See [the zero-fallback
convention](../README.md#zeros-that-mean-measured-and-zeros-that-mean-absent).

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

**Migration window** — *when can it be done?* Collapsed, because it is
consulted while the migration is being planned rather than while the cutover
is being run, and Grafana runs no query for a closed row. The mirror sees
every record its producers write to the mirrored topics, so what crosses it,
hour by hour, *is* the source's traffic pattern — and the window is the trough
in it: the hours with the least to stop at the source, the least to drain, and
the fewest consumers reading. *Quietest hour* and *Busiest hour* are the two
ends of a profile built from the last seven days, one bar per hour of day in
the chart beside them; *Now, against the quietest hour* is what is crossing
this instant as a multiple of the quietest hour's average — 1 is as quiet as
this source ever gets, green within one and a half times, amber to three, red
beyond — and *Records crossing /s, last 7 days* is the same traffic as it
happened, pinned to the week that shaped the profile whatever the board's
time range is. Hours are UTC, because PromQL's `hour()` knows no other zone.

PromQL cannot pivot time into a label, so the profile is twenty-four clauses,
one per hour of day, each averaging the crossing rate over the half-hour
samples of the last seven days that fell in that hour — `hour()` inside a
subquery takes each step's own time, which
`scripts/metric-contract/tests/mirror-maker2.migration-window-test.yaml`
holds against a synthetic day with one quiet hour. A bar missing at one end
is an hour the mirror has not seen yet: it has been up for less than a day,
and the profile is only as good as the week behind it. A busiest hour barely
above the quietest is a source with no daily pattern, and then the window is
whenever the people are ready rather than whenever the traffic is.

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

Nothing else on this board depends on the policy, and the panel list is 25
either way.

## What the chart injects, and what is frozen

`charts/mirror-maker2/templates/dashboard-migration.yaml` loads this file with
`.Files.Get` and sets `uid` (`kates-mm2-mig-<name, 17>-<sha256 of the whole
name, 8>` — the scheme this replaces truncated to 26 characters and two
releases sharing that prefix overwrote each other's board), `title` and `links`
(from `alerts.runbookBaseUrl`). It also writes the `current` of the two
variables below — the selected value, never the query — so a chart-delivered
board opens on its own release.

## The variables

Every expression on this board selects `namespace="$namespace",
strimzi_io_cluster="$cluster"`.

| Variable | Resolves from | Which scrape puts that label there |
|---|---|---|
| `$namespace` | `label_values(kafka_connect_worker_rebalance_metrics_completed_rebalances_total, namespace)` | `charts/mirror-maker2`'s PodMonitor: `kafka-common.strimziRelabelings` maps `__meta_kubernetes_namespace` → `namespace`. |
| `$cluster` | `label_values(…{namespace="$namespace"}, strimzi_io_cluster)` | The same PodMonitor's `labelmap` over `strimzi_io_*` pod labels; Strimzi puts `strimzi.io/cluster: <CR name>` on every operand pod. |

`$namespace` was a hidden `constant` the chart filled in, which froze any copy
of this board taken out of the ConfigMap to the release that generated it.
`$cluster` is new, and it is not decoration: two migrations draining two source
clusters into one target share a namespace, and a namespace-only selector sums
them — on the board whose headline panel says whether it is safe to cut over.
The sixth case in the promtool fixture holds that.

**Set in `board.py`, not in a release:**

| Constant | Value | Where it lands |
|---|---|---|
| `DRAINED_MS` | 5000 | the red line on lag, the amber cell in the topic table, **and the `< bool 5000` inside the go/no-go query** |
| `FRESH_MS` | 60000 | the amber cell on translation age |

`DRAINED_MS` is the reason these are constants rather than chart values: it
sits inside a PromQL comparison, where a `$` does not parse — the "every
dashboard query is valid PromQL" gate would reject it, and `promtool` is right
to. Changing either means editing `board.py` and running
`scripts/gen-dashboards.py`.

`charts/mirror-maker2` carried these as `dashboard.migration.drainedBelowMs`
and `.translationFreshMs` until 0.11.0, where they were documented as frozen —
accurate, but still two keys that accept a number and ignore it, with the
chart's own docs showing a snippet setting them. They are removed, and a
release that sets either is refused with the constant to edit named in the
error. That is the one thing this port gives up, said plainly rather than left
for someone to discover after setting `drainedBelowMs: 500` and trusting the
colour.

## The panels CI extracts by name

`ci-mirror-maker2.yml` pulls five panels out of this board by their exact
titles — into the collapsed window row too — and runs `promtool test rules`
over them. **`Safe to cut over?`** gets six cases from
`scripts/metric-contract/tests/mirror-maker2.cutover-gate-test.yaml`:
drained, still behind, still in flight, a failed task, a release reporting
nothing at all, and a second release in the same namespace that is none of
those things. **`Catch-up ETA`**, **`Quietest hour (UTC)`**, **`Busiest hour
(UTC)`** and **`Now, against the quietest hour`** get three from
`mirror-maker2.migration-window-test.yaml`: a drain whose ETA is in seconds
(the panel once divided by a thousand on top and drew a ten-hour drain as
thirty-five seconds), a synthetic day with one quiet hour, and a mirror with
no samples. Rename any of the five and its test silently checks nothing — the
extraction step fails loudly for exactly that reason.

The test's input series carry the namespace and cluster that workflow's own
render selects (`helm template mm2 … -n kafka`, so `namespace="kafka"` and
`strimzi_io_cluster="mm2-mirror-maker2"`), and the board selects on
`namespace="$namespace", strimzi_io_cluster="$cluster"`. The extraction step
interpolates the board's variables from their `current` before handing the
query to promtool, which is what Grafana does when it draws the panel; it also
refuses an expression that still carries a `$` afterwards, so a variable the
chart stops preselecting fails the step rather than quietly detaching the
cases from the panel.

## Editing it

`board.py`, then `scripts/gen-dashboards.py`, then
`python3 scripts/check-dashboards.py`. Never edit `dashboard.json` or the
copy under `charts/monitoring/dashboards/`.

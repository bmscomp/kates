# Dashboards

Two boards, because a mirror and a migration are not the same job.

The **mirror board** watches a DR leg or a fan-in that runs for months, the
way anything long-lived is watched — is it up, is it lagging, is it erroring.
The **migration board** covers the hours between installing the mirror and
cutting over, where the only question is whether you can cut over yet and, if
not, what is left. During a migration the second one is often the only board
anybody opens; on a permanent DR leg it is noise — collapse or ignore it.

Since chart 0.11.0 **both are delivered by `charts/monitoring`**
(`dashboards.enabled` there), with every other board in `dashboards/`; this
chart renders neither, and setting its old `dashboard.*` values is refused
with that location named. Their `$namespace`/`$cluster` dropdowns select a
release from the data.
[`dashboards/README.md`](../../../dashboards/README.md#installing-these-anywhere)
has the four install routes and every knob.

## Making them fill

The boards install with the monitoring stack; whether they have data is this
chart's side:

```yaml
metrics:
  enabled: true          # metricsConfig on the CR — Strimzi only opens the
                         # scrape port when this is set
podMonitors:
  enabled: true          # requires the Prometheus Operator CRDs
```

The migration presets ship with metrics **off** — `values-migrate-2x.yaml` and
its siblings are written for a lab on a laptop, where there is no Prometheus to
scrape anything. Turn both on for a real migration:

```bash
helm dependency build charts/mirror-maker2    # once per checkout: the kafka-common library
helm upgrade mm2 charts/mirror-maker2 -n kafka \
  -f charts/mirror-maker2/values-migrate-3x.yaml \
  --set metrics.enabled=true --set podMonitors.enabled=true
```

Turn it on **before the first record crosses**, not after the first problem: lag
is only useful with history behind it, and the catch-up ETA needs ten minutes of
record age before it means anything.

One caveat that applies to both boards. The panels are written against the JMX
exporter's metric names. With `metrics.type: strimziMetricsReporter` the
reporter publishes the same numbers under different names and every panel reads
empty — the alerts refuse that combination outright, the dashboards do not,
because an empty panel is visible to the person looking at it whereas an alert
that cannot fire is silent.

## Which board, when

| You are… | Board | Why |
|---|---|---|
| Running a DR leg or a fan-in | Mirror | Built for the long run: SLO, workers, error budget |
| Migrating, before the cutover | Migration | Built for the drain and the go/no-go |
| Migrating, during the cutover | Migration | Its Cutover state section is the freeze itself |
| Chasing a slow mirror | Mirror | Its collapsed client section says *where* it is slow |
| Explaining last week to someone | Mirror | The SLO section is the only one with a memory |

Running both during a migration is reasonable — the mirror board's error and
worker sections are the ones that explain *why* the migration board is not going
green.

## The mirror board

`mirror-maker2.json`, read top to bottom in the order an incident asks:

| Section | What it answers |
|---|---|
| **Is it up, is it lagging, is it erroring** | Six stats: workers reporting, tasks running, records/s, replication lag, translation age, errors/s |
| **Connector task states** | Which connector lost a task. A connector with 3 tasks and 1 failed still reports RUNNING on the CR, and the partitions that task owned simply stop |
| **Replication** | Records written, lag per topic, record age at source, bytes/s, polled against written, records in flight |
| **Offset translation** | Translation age per source and group, checkpoints emitted, offset commit success |
| **Errors, retries and dead letters** | Errors logged, records errored / failed / **skipped**, retries and dead-letter writes, last error per connector |
| **Replication SLO** | Only when `alerts.enabled` and `alerts.slo.enabled`: the 1h and 5m error ratios with the burn line, tasks running, both recorded clocks |
| **Workers** | Heap, GC time, rebalances/s, time since last rebalance, assignment, startup failures |
| **The path a record takes** *(collapsed)* | Source fetch against target produce — the panel set that says which end is slow |
| **Source: `<alias>`** *(collapsed, one per mirror)* | Records/s, lag, translation age and errors for that leg alone |

Three panels are worth knowing about before you need them.

**Records skipped**, under Errors. Skipped means the error was tolerated, the
record dropped, and the connector left RUNNING. It is the only failure mode a
lag panel cannot show: the mirror is up to date precisely because it threw
records away.

**Polled vs written**, under Replication. A persistent gap between what the
tasks read and what they wrote is records being filtered or dropped — not a
backlog, which shows up next door as records in flight.

**The path a record takes**, collapsed. Reading fast and writing slowly puts the
bottleneck on the target: watch the producer buffer heading for zero and records
in flight climbing with it. Reading slowly with the target idle is the source,
its quotas, or the network in between. This is the section the runbook means by
"check whether the bottleneck moved to the target's produce path".

Rows about a **mirror** are per source and carry the alias in the `connector`
legend; rows about the **workers** are release-wide, because there is one
Connect cluster however many mirrors it runs.

## The migration board

`mirror-maker2-migration.json`. Faster refresh (10s), shorter default window
(1h), and a link in its header straight to the runbook's
[cutover checklist](../../../docs/mirror-maker2-runbook.md#cutover-checklist),
which is the thing it is built around.

| Section | What it answers |
|---|---|
| **Can I cut over yet?** | Go/no-go, replication lag, records in flight, catch-up ETA, failed tasks |
| **Migration window** *(collapsed)* | When the traffic says it can be done: the quietest and busiest hours of the last seven days, what is crossing now against the quietest, the hour-of-day profile and the week that made it |
| **Draining** | Lag and record age per topic, the drain rate, the backlog — the three numbers that have to reach zero |
| **Consumer groups** | Every group with a translated position and how old it is; the trend; whether checkpoints are still being emitted |
| **Topics** | The punch list: lag, record age and rate per replicated topic, plus bytes crossing |
| **Cutover state** | Source connector, checkpoint connector, still-replicating rate, errors, and the freeze plotted per connector |

### Its two thresholds are not the alert's, and they are not in values

```python
# dashboards/mirror-maker2-migration/board.py
DRAINED_MS = 5000    # lag under which the mirror counts as drained
FRESH_MS   = 60000   # translation age past which a group reads stale
```

`alerts.thresholds.replicationLatencyMs` is 60s, and that is right for a mirror
that runs: it is the point at which lag is worth waking someone. A cutover wants
lag near zero, so the migration board judges against `DRAINED_MS` instead. A
tight cutover window wants a lower number; a number above the alert's threshold
means the board can say DRAINED while the alert says the lag is high, which is
a contradiction worth avoiding.

**Both numbers live in the board's source, not in a release.** `DRAINED_MS` is
a threshold colour, which Grafana cannot template, *and* the `< bool 5000`
inside the go/no-go query, where a `$variable` does not parse as PromQL — the
"every dashboard query is valid PromQL" gate would reject it. To move either
line, edit
[`dashboards/mirror-maker2-migration/board.py`](../../../dashboards/mirror-maker2-migration/README.md)
and run `scripts/gen-dashboards.py`.

> **Removed in 0.11.0.** `dashboard.migration.drainedBelowMs` and
> `.translationFreshMs` used to be values keys, and this section used to show
> them being set. They were baked into the generated board, so setting them
> changed nothing — documented as inert, which is better than silent but is
> still a key that accepts a number and ignores it. A release that sets either
> one is now **refused**, with the constant to edit named in the error.

The mirror board's `alerts.thresholds.*` lines and its SLO error budget work
the same way: the alerts still honour `values.yaml`, the panel lines are the
chart defaults.

### Safe to cut over?

The one panel on either board that a person acts on directly, and the action it
invites — applying `values-cutover.yaml` — is where a migration stops being
cheaply reversible. It is worth knowing exactly what it means.

It reads **DRAINED** when all three of:

1. replication lag is below `drainedBelowMs`,
2. no records are in flight,
3. no tasks have failed.

It reads **No data** when the workers are not reporting at all. That is
deliberate: every other factor in the query defaults to its healthy value when
its series is absent, so without a fourth factor tied to the workers' own task
count, a release whose scrape had stopped — exactly when nobody should be
cutting over — would have satisfied all three conditions and shown green.

It does **not** know whether producers have stopped writing to the source. No
MirrorMaker metric says that; it would need the source broker's own metrics, and
on a migration the source is usually the old cluster nobody instrumented.
Confirming the source's end offsets are static, twice, sixty seconds apart, is
still step 1 of the checklist and still yours.

So: DRAINED means *the mirror has caught up with what it was given*, not that
the source is quiet.

### Catch-up ETA

Seconds until the fetchers reach the end of the source's log, extrapolated from
how fast the record age has been falling over the last ten minutes. It reads
**negative** when the age is rising — the mirror is losing to the producers
rather than catching up, and no amount of waiting will drain it. That is the
signal to add tasks (`sourceConnector.tasksMax`, toward the source's partition
count) or to reduce what is being produced, not to wait longer.

### Cutover state

The cutover primitive is `cutover.sourceConnectorState: stopped`, and `stopped`
rather than `paused` **releases the connector's tasks** — which is the point: a
worker restart cannot then quietly resume replication behind producers that have
already moved to the target. In metrics that reads as:

| Panel | Before the cutover | After |
|---|---|---|
| Source connector | RUNNING | STOPPED |
| Checkpoint connector | RUNNING | RUNNING — it must stay that way |
| Still replicating /s | whatever is crossing | zero, and staying zero |
| The freeze, per connector | two lines | one at zero, one still moving |

Both connectors at zero is the classic cutover mistake: every record is on the
target and no consumer knows where to start reading it.

## Reading the board through a cutover

The checklist steps, and what to watch for each:

| Runbook step | On the board |
|---|---|
| Before you start | Go/no-go section; the per-topic table for "every topic I care about"; the consumer-group table for "groups have translated offsets" |
| 1. Stop the producers | **Not visible here.** Confirm end offsets on the source, twice, 60s apart |
| 2. Let the mirror drain | Draining section: lag and record age falling, records in flight at zero, the ETA agreeing |
| 3. Apply the cutover | Cutover state: source connector to STOPPED, checkpoint still RUNNING, still-replicating to zero |
| 4. Move the consumers | Consumer groups: translation age per group. Verify each move on the target with `kafka-consumer-groups.sh`, which is the only place a *resumed* consumer shows |
| 5. Move the producers | Nothing here — the target is live and this board is about the mirror |
| 6. Retire the mirror | Both connectors gone; the board goes empty, which is the expected end state |

## What neither board can tell you

Worth stating plainly, because a dashboard implies completeness:

- **Whether anything is still producing to the source.** Needs the source
  broker's metrics.
- **End-offset parity between source and target.** The boards use replication
  lag and record age as proxies for "caught up"; comparing actual end offsets is
  `kates migrate target offsets <topic>` or `kafka-get-offsets.sh` on both
  sides.
- **Whether a consumer actually resumed** after moving. Translation age says a
  position exists on the target, not that anything used it.
- **Anything at all, under `metrics.type: strimziMetricsReporter`** — different
  names, empty panels.

## How they are kept true

A dashboard fails quietly: a panel whose series does not exist renders "No data"
exactly as an idle cluster does. So the boards are gated the same way the alerts
are, in `.github/workflows/ci-mirror-maker2.yml`:

| Gate | What it proves |
|---|---|
| Metric contract | Every series a panel reads is one the chart's JMX exporter rules can actually produce — simulated over a catalogue of the MBeans a worker registers |
| Layout | For every board: no two panels on the same cell, nothing past column 24, rows in order, every panel with a description and a query |
| PromQL | Every query on every board parses, under the same promtool the alerts get |
| Go/no-go behaviour | The "Safe to cut over?" query — extracted from the rendered board, not copied — held to six cases including a release reporting nothing and a second release in the same namespace |
| Live scrape *(on demand)* | A real worker's `/metrics`, diffed against the catalogue both ways |

`make check-metric-contract` runs the first of those locally.

## Adding a panel

**Neither board lives in this chart.** They live in
[`dashboards/mirror-maker2/`](../../../dashboards/mirror-maker2/README.md) and
[`dashboards/mirror-maker2-migration/`](../../../dashboards/mirror-maker2-migration/README.md)
as Python that `scripts/gen-dashboards.py` turns into JSON, delivered by
`charts/monitoring`. Nothing is injected per release any more: `$namespace`
and `$cluster` are ordinary `query` variables resolved from
`label_values(...)`, so the one file resolves wherever it is imported, and
`$source` is a `custom` variable whose options you edit on the board — a
value rather than data, on purpose, because a leg whose connectors never
started publishes no series with its alias in. (Through 0.10.0 the chart
rewrote uid, title and `$source` per release; `$pods`, a pod-name regex built
from the release name, went even earlier — the PodMonitor carries
`kafka-common.strimziRelabelings`, so workers are selected by
`strimzi_io_cluster`.)

So: edit `board.py`, run `scripts/gen-dashboards.py`, and run
`python3 scripts/check-dashboards.py`. Never edit the generated copies under
`charts/monitoring/dashboards/`; `gen-dashboards.py --check` fails on a
hand-edited one. Each board's README says which numbers are frozen into its
JSON — the alert thresholds, the SLO budget and the migration board's
`drainedBelowMs` among them, because a Grafana threshold is not templatable
and one of those numbers sits inside a PromQL comparison.

A description and a query are still required on every panel; `panels.py` will
not build a panel without a description, and the layout gate rejects one that
has none.

New series need a catalogue entry in
`scripts/metric-contract/mirror-maker2.yaml` — if the MBean and attribute are
not described there, the contract gate will say the series cannot be produced,
which is the answer you want when it genuinely cannot.

---

See also: [Configuration](configuration.md#observability) ·
[chart README](../README.md#observability) ·
[runbook](../../../docs/mirror-maker2-runbook.md)

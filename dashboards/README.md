# Dashboards

Every Grafana dashboard this project ships, in one place, each with the
documentation for what it shows and what the metrics mean.

## Why this directory exists

The dashboards used to live in six places: static JSON in the monitoring
chart, hand-written JSON inside Helm templates in four other charts, and nine
more vendored in the Strimzi operator's tarball. They drifted apart. Nine of
them duplicated each other — 82 panels carrying 31 distinct concepts, with one
panel titled *Active Brokers* existing five times — and several read metric
names the JMX exporter never emits, so the panels that looked most useful were
the ones rendering empty.

`docs/grafana-dashboards-refactor-plan.md` is the full account.

## Layout

```
dashboards/
  README.md            this file
  METRICS.md           every metric on every board, and what it means
  _lib/                the builder: layout.py, panels.py, build.py
  <board>/
    board.py           the board, as rows of panels
    manifest.yaml      uid, title, tags, and which charts get a copy
    dashboard.json     GENERATED — do not edit
    README.md          what this board is for, and how to read it
```

[`METRICS.md`](METRICS.md) is the cross-cutting half of the documentation. A
board's own README says what that board answers and what each section means
when it moves; `METRICS.md` takes every series read by any board — 211 of them
— and gives its type, its labels, one or two sentences of operational meaning,
how to query it where that is not obvious, and which boards read it. It opens
with the four naming traps that caused this refactor, and it says for each
series whether this repository publishes it or something else has to be
installed first — so an empty panel can be read as *nothing is wrong* or as
*nothing publishes this*, which are not the same problem.

## The JSON is generated

`dashboard.json` is built from `board.py`. Edit the Python, then:

```bash
scripts/gen-dashboards.py          # regenerate everything
scripts/gen-dashboards.py --check  # what CI runs
```

This is deliberate. Positions, panel ids and the panel skeleton come from
`_lib`, so no board can hand-number a `y` coordinate into an overlap, pick up a
datasource by accident, or ship a panel with no description — the three
failures that the boards this directory replaces all had. Grafana's own export
is a fine starting point for a new panel, but the committed artifact is built.

Charts cannot read files outside their own directory, so the generator writes a
copy into each chart named in `manifest.yaml`, and the chart loads it with
`.Files.Get`. `--check` fails when a copy has drifted, the same contract
`gen-chart-table.sh` and `gen-version-matrix.sh` already use.

## Checks

```bash
scripts/check-dashboards.py        # layout, descriptions, datasources, uids
scripts/check-metric-contract.sh kafka-cluster   # every series is producible
```

The second one matters most. It runs the exporter rules over a catalogue of
JMX beans and fails when a board reads a series no rule can emit. Eleven such
names shipped before it covered dashboards.

A third runs only in CI, in `ci-kafka-charts.yml`'s `Dashboards` job: every
`targets[].expr` on every board is wrapped as a recording rule and put through
`promtool check rules`, with the pinned Prometheus' own parser. A panel's
PromQL is no less able to be wrong than an alert's, and a stray bracket
renders as an empty panel — exactly what an idle cluster renders as.
`$namespace` parses inside a label value and a `$` anywhere else does not, so
the extractor substitutes a placeholder for declared variables inside label
values only and **fails on any `$` that survives**, which is what catches a
variable used in a duration rather than skipping the query.

## Adding a board

1. `mkdir dashboards/<name>` with `board.py` and `manifest.yaml`.
2. Build rows from `panels.py` constructors. Every panel takes a description;
   there is no way to make one without.
3. Name every series from `METRICS.md`, not from a JMX bean name. The exporter
   renames as it publishes — `BytesInPerSec` becomes `bytesin_total`,
   `AvgIdlePercent` becomes `avgidle_percent` — and guessing is what produced
   the dead panels this directory was built to clear out.
4. Run `scripts/gen-dashboards.py` and both checks. Keep every Grafana
   variable inside a label value; CI rejects a `$` anywhere else, because a
   variable in a duration or an operand is not PromQL outside Grafana.
5. Write the board's `README.md`: what question it answers, who opens it, and
   what each section means when it moves.
6. Add it to the delivery table below, and to `METRICS.md` — its totals, its
   producer table, and a row for each series it is the first to read.

## Where the boards are delivered

| Board | Delivered by |
|---|---|
| `kafka-kraft`, `kafka-performance` | `charts/monitoring` |
| `kates-application`, `kates-benchmark`, `kates-chaos`, `kates-trend` | `charts/monitoring` |
| `kates-overview`, `kyverno-security` | `charts/kates` |
| `kates-chaos-infra` | `charts/kates-chaos` |
| `kafka-connect` | `charts/connect-cluster` |
| `mirror-maker2`, `mirror-maker2-migration` | `charts/mirror-maker2` |

`kates-overview` is the odd one in the `kates-*` set: the application chart
ships it, and it reads **nothing but series the application publishes about
itself** — no kube-state-metrics, no cAdvisor, no exporter. Install
`charts/kates` and a Grafana and it works. Four of the rest need the
monitoring stack, which is why `charts/monitoring` delivers them.

`kates-chaos-infra` is the other one out of that set, and for the same reason:
`charts/kates-chaos` installs the LitmusChaos execution plane, so it is the
chart that ships the board watching it. It is not `kates-chaos` — that board
asks what a fault did to the Kafka cluster and the workload; this one asks
whether the chaos platform is installed and running at all. Until this
refactor it was the twelfth board and the only one outside this directory:
hand-written JSON inside its chart's template, with no panel descriptions, no
layout gate, and four series or labels LitmusChaos does not publish.

Broker health, quorum identity, Cruise Control and consumer lag are covered by
the Strimzi operator's own dashboards, which `charts/strimzi-operator` enables
by default and which read the same series this repository's exporter rules
produce. The boards here add what those leave out rather than restating them.

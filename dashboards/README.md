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

## Adding a board

1. `mkdir dashboards/<name>` with `board.py` and `manifest.yaml`.
2. Build rows from `panels.py` constructors. Every panel takes a description;
   there is no way to make one without.
3. Name every series from `METRICS.md`, not from a JMX bean name. The exporter
   renames as it publishes — `BytesInPerSec` becomes `bytesin_total`,
   `AvgIdlePercent` becomes `avgidle_percent` — and guessing is what produced
   the dead panels this directory was built to clear out.
4. Run `scripts/gen-dashboards.py` and both checks.
5. Write the board's `README.md`: what question it answers, who opens it, and
   what each section means when it moves.

## Where the boards are delivered

| Board | Delivered by |
|---|---|
| `kafka-kraft`, `kafka-performance`, the `kates-*` set | `charts/monitoring` |
| `kafka-connect` | `charts/connect-cluster` |
| `mirror-maker2`, `mirror-maker2-migration` | `charts/mirror-maker2` |
| `kyverno-security` | `charts/kates` |

Broker health, quorum identity, Cruise Control and consumer lag are covered by
the Strimzi operator's own dashboards, which `charts/strimzi-operator` enables
by default and which read the same series this repository's exporter rules
produce. The boards here add what those leave out rather than restating them.

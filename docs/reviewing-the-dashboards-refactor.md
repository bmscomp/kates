# Reading the dashboards refactor

This change is **156 files and 100,060 insertions**, which reads at first
glance as unreviewable. It is not, and this page is the map.

## 84% of it is generated

| | Files | Insertions | Deletions |
|---|---:|---:|---:|
| Generated JSON | 55 | 84,247 | 4,489 |
| Written by a person | 105 | 15,813 | 2,390 |

The generated part is one `dashboard.json` per board, the chart copy of each,
and the two install bundles. All of it comes from `dashboards/<board>/board.py`
through `scripts/gen-dashboards.py`, and `--check` fails the build when a copy
disagrees with its source — so there is nothing in that JSON that is not
determined by a `board.py` you can read instead.

Every one of those files is marked `linguist-generated` in `.gitattributes`,
so GitHub collapses them in the diff by default. That marking is itself
checked: `gen-dashboards.py --check` compares the attribute against the list of
paths it actually writes, so a new board or a new chart target cannot quietly
reappear in the diff.

**Read `board.py`, not `dashboard.json`.**

## The order to read the rest in

| | Files | Lines | What it is |
|---|---:|---:|---|
| 1. `dashboards/_lib/` | 4 | +742 | The 24-column layout packer, the panel constructors — which refuse a panel with no description and never take a datasource by name — and the builder. Everything else is downstream of these 742 lines. |
| 2. `dashboards/*/board.py` | 12 | +5,715 | One per board. This is the source. |
| 3. `dashboards/METRICS.md` | 1 | +811 | Every series any board reads: type, source, labels, operational meaning, how to query it, and for those nothing here publishes, what has to be installed first. This is what lets an empty panel be read as *nothing is wrong* rather than *nothing publishes this*. |
| 4. `scripts/` | 7 | +568 −47 | The gates. The mirror-maker2 layout check generalised to all twelve boards; the generator; the metric contract extended to read boards off disk; the zero-fallback gate and its twelve-entry allowlist. |
| 5. `charts/` | 34 | +963 −2,171 | Net negative. Nine legacy boards are deleted. The additions that matter are `charts/monitoring/values.yaml` and the mirror-maker2 PodMonitor. |
| 6. `kates/` | 4 | +501 | The four `kates_benchmark_*` meters that two boards and the book read and nothing registered. |
| 7. `.github/` | 2 | +295 −3 | The Dashboards job. |
| 8. The rest of the docs | 41 | +6,218 −169 | Tutorial 13, the twelve board READMEs, the refactor log. |

## If you read one thing

`charts/monitoring/values.yaml`, the `prometheusSpec` block. Five lines:

```yaml
podMonitorSelectorNilUsesHelmValues: false
serviceMonitorSelectorNilUsesHelmValues: false
ruleSelectorNilUsesHelmValues: false
probeSelectorNilUsesHelmValues: false
scrapeConfigSelectorNilUsesHelmValues: false
```

kube-prometheus-stack defaults those five to `true`, which makes every selector
`release: <its own release name>`. This repository installs it as `monitoring`,
while `kafka-cluster` and `connect-cluster` labelled their objects
`release: kafka` and `mirror-maker2` and `kates` labelled theirs with nothing.

Nothing matched anything. **No broker, Connect or MirrorMaker series was ever
scraped, and not one of the charts' alerts was ever loaded** — silently, because
an unselected PodMonitor raises no event and an empty dashboard looks exactly
like an idle cluster. That is the largest defect here, it is five lines, and
everything else in this change is downstream of the same idea: a monitoring
system that is not working looks identical to one with nothing to report, so
the gates have to be the thing that tells them apart.

This was confirmed on a live cluster rather than argued from the values file.
A Prometheus installed by this chart before the change reported **28 active
targets, every one of them from the monitoring stack itself; 17 scrape
configs, all of them `serviceMonitor/monitoring/…`; `fromPodMonitors: []`; and
zero `kafka_*` and zero `kates_*` series**. Every empty board has that one
cause. They are not twelve dashboard bugs.

The same cluster produced the second finding, which is this repository's own
fault rather than the chart's. Beside the honestly empty panels, three KRaft
tiles read a confident green **0** — *Unreachable voters*, *Fenced brokers*,
*Metadata errors* — because their queries ended in `or vector(0)`, a fallback
that cannot tell "nothing is wrong" from "nothing is being scraped". Those and
twenty-four more now draw their zeros through an anchored fallback that a dead
scrape cannot reach, and `check-dashboards.py` fails on any new bare one.

Chasing that one down turned up the half of it that has nothing to do with
PromQL: **Grafana paints the *No data* placeholder with the base threshold
step**, so dropping a fault panel's fallback stops the tile claiming a measured
zero and still leaves the words "No data" written in green. The same rule
paints an absent value red where the base step is red, inventing an alarm out
of a missing scrape — both visible on the Strimzi operator's own boards, which
show *Under Replicated Partitions* N/A in green beside *Brokers Online* N/A in
red. Every single-value panel now carries a `null` value mapping that renders
that state neutrally, and four panels counting things that must exist —
*Brokers*, *Workers up*, *Pods ready*, *Database pods ready* — are red at zero
rather than green. See [Zeros that mean *measured*, and zeros that mean
*absent*](../dashboards/README.md#zeros-that-mean-measured-and-zeros-that-mean-absent).

## The gates, and what each would have caught

| Gate | Catches |
|---|---|
| `scripts/check-metric-contract.sh` | A name the exporter rules cannot produce. Would have caught all eleven dead names the deleted boards carried. |
| `scripts/check-dashboards.py` | Overlapping panels, a panel with no description, a lost query, a datasource named by string, two boards sharing a uid, a series no `METRICS.md` row documents, and a zero drawn by a bare `or vector(0)` — the fallback that turns an unscraped cluster into a wall of confident green zeros — and a single-value panel with no neutral `null` mapping, which leaves Grafana painting its *No data* in the base threshold's colour. |
| `scripts/gen-dashboards.py --check` | A board edited without regenerating, a chart copy edited by hand, a bundle left over from a deleted board, a generated file not marked generated. |
| `promtool check rules` (CI) | A query that does not parse, and a `$variable` used anywhere a variable does not parse. |
| `scripts/check-dashboards-live.py` | A selector that matches nothing while its series exist — a label the panel filters on that nothing sets. Nothing else here runs a query. |
| `scripts/check-grafana-compat.sh` | A board that loads over the API but does not render in a browser, on any of five Grafana versions — and, since Grafana 8 and 9 rewrite value mappings during schema migration, a neutral no-data mapping that does not survive the rewrite. |
| `scripts/check-chart-matrix.py` | A chart that renders something the CRDs reject, or stops refusing a configuration it used to refuse. |
| `dashboards/test_install.py` | `install.py` losing `overwrite: true`, keeping a foreign `id`, or mis-encoding a credential. |
| `dashboards/test_panels.py` | `panels.or_zero` answering **0** where it should answer nothing. Six assertions through `promtool test rules`: the measured value passes through, an absent counter on a live target draws 0, and a target nobody is scraping draws no sample at all — the cell a bare `or vector(0)` gets wrong. Plus the no-data colour: that every single-value panel carries a neutral `null` mapping and that the compat harness's migration check rejects the legacy mapping shape, which cannot carry a colour at all. |

# Kates — Chaos infrastructure

**Delivered by** `charts/kates-chaos` · **uid** `kates-chaos-overview` ·
**6 panels**

## The question it answers

*Is the chaos platform installed, running, and has it actually run anything?*

The LitmusChaos execution plane this chart deploys — the chaos operator, the
chaos-exporter, and the verdicts they report. It is the board you open before
a GameDay, and the board you open when the other chaos board is empty and you
need to know whether that means "no fault was injected" or "nothing is
installed".

## Who opens it

Whoever installed `charts/kates-chaos`, and whoever is about to trust a
resilience claim that rests on an experiment having run.

## This is not `kates-chaos`, and the difference matters

| | `kates-chaos-infra` (this board) | `kates-chaos` |
|---|---|---|
| Question | Is the chaos platform alive and has it run anything? | What did the fault do to the cluster and the workload? |
| Scope | the namespace the execution plane runs in | the Kafka namespace and the Kates release |
| Delivered by | `charts/kates-chaos` | `charts/monitoring` |
| Reads | the chaos-exporter and kube-state-metrics | Litmus, kube-state-metrics, cAdvisor and the Kates application |
| uid | `kates-chaos-overview` | `kafka-chaos-dashboard` |

They were kept apart rather than folded together. Two of the six panels here
(the verdict counts and the experiment duration) restate something
`kates-chaos` also shows, and they restate it from a *different* metric family
— verdicts and cluster aggregates, where `kates-chaos` reads the per-ChaosResult
`litmuschaos_passed_experiments`/`_failed_experiments` counters. The other four
have no counterpart there at all: the cluster-wide run totals, the operator's
availability, and the pods of the execution plane itself.

Folding would also have moved the board to `charts/monitoring`, which would
leave `charts/kates-chaos`'s own `monitoring.grafanaDashboard.enabled` switch
shipping nothing, and would break the rule the delivery table follows
everywhere else: **the chart that owns the workload ships the board that
watches it.** `charts/kates-chaos` is the chart that installs Litmus.

## Where it came from, and the four names that were wrong

Until this refactor these six panels were hand-written JSON inside
`charts/kates-chaos/templates/grafana-dashboard.yaml` — the twelfth board in
the repository and the only one that did not live in `dashboards/`. It had no
panel descriptions, was not covered by the layout gate, appeared in no
delivery table, and nothing ever checked the series it read.

Four of those series or labels do not exist. Each was guessed from what the
panel was called rather than taken from the chaos-exporter's metric list
([github.com/litmuschaos/chaos-exporter](https://github.com/litmuschaos/chaos-exporter)),
which is the same failure mode that put eleven dead names on the nine legacy
Kafka boards.

| The old board read | What is wrong | What this board reads |
|---|---|---|
| `litmuschaos_experiment_verdict{verdict="Pass"}` | the metric is real; the label is not. The exporter emits `chaosresult_verdict`, so a `verdict=` matcher selected nothing and the panel counted *every* verdict | `count(litmuschaos_experiment_verdict{chaosresult_verdict="Pass"} == 1)` |
| `litmuschaos_engine_experiment_count` | no such series, under any name. The panel was titled *Chaos Engine Duration (seconds)*: the title was right and the expression was a count, not a duration | `litmuschaos_experiment_total_duration` |
| `increase(litmuschaos_experiment_verdict[1h])` | `increase()` over a gauge that flips between 0 and 1 as verdicts are set and aged out. The number it produces means nothing | `litmuschaos_cluster_scoped_experiments_run_count`, with the passed and failed aggregates beside it |
| `kube_pod_status_phase{pod=~".*chaos-operator.*"}` | `chaos-operator` is the operator's *container* name. Its Deployment is named by litmus-core's `fullnameOverride` (`litmus`), so no pod ever matched and the tile was a permanent, reassuring zero | `kube_deployment_status_replicas_available{deployment="$operator_deployment"}` |

`kube_pod_status_phase` on the infra table was correct and is unchanged.

## How it is scoped

| Variable | Type | Value | Set by |
|---|---|---|---|
| `$namespace` | hidden `constant` | where the execution plane runs | `monitoring.grafanaDashboard.namespace`, default the release namespace |
| `$operator_deployment` | hidden `constant` | the Litmus operator Deployment's name | `monitoring.grafanaDashboard.operatorDeployment`, default litmus-core's own fullname (`litmus`) |

Neither is derivable from the data and neither may be frozen into a generated
file, so both are hidden `constant` template variables that the chart fills in
per release — the same scheme the MirrorMaker 2 boards use, and the reason the
board's JSON is generated once and serves every release.

**`$namespace` is not the Kafka namespace.** It is where Litmus runs. The
namespace the experiments *target* appears nowhere on this board; it is
`$namespace` on `kates-chaos`, which is a different variable with a different
value.

## Where each metric comes from

| Source | Series | Installed by |
|---|---|---|
| **Litmus chaos-exporter** | `litmuschaos_experiment_verdict`, `litmuschaos_experiment_total_duration`, `litmuschaos_cluster_scoped_experiments_run_count`, `litmuschaos_cluster_scoped_passed_experiments`, `litmuschaos_cluster_scoped_failed_experiments` | litmus-core's exporter, which is **off by default** (`litmus-core.exporter.enabled`) |
| **kube-state-metrics** | `kube_pod_status_phase`, `kube_deployment_status_replicas_available` | the `kube-state-metrics` subchart of `kube-prometheus-stack`, on by default in `charts/monitoring` |

> **Every Litmus panel here is empty until the exporter is enabled.** This
> chart installs the operator by default and the exporter by choice. The two
> kube-state-metrics panels fill regardless, which is what makes them the ones
> to read first: if the operator tile is green and every Litmus panel is
> blank, the exporter is off, not the platform.

The `cluster_scoped` aggregates rather than the `namespace_scoped` ones are
correct here because litmus-core deploys the exporter with an empty
`WATCH_NAMESPACE`, which makes it aggregate across every namespace. On this
install the `litmuschaos_namespace_scoped_*` family is always empty.

## The sections

### Header — three stats

`Chaos experiments — Pass` · `Chaos experiments — Fail` · `Chaos operator`

The two verdict counts carry `== 1` deliberately. The exporter sets
`litmuschaos_experiment_verdict` to 0 for an Awaited verdict, and back to 0
once a verdict has been repeated for longer than its `TSDB_SCRAPE_INTERVAL`,
so a count of the *series* and a count of the ones reading 1 are different
numbers. A Fail is often not a cluster problem: Litmus marks an experiment
failed when it could not inject the fault at all, which makes the resilience
claim for that window unsupported rather than disproved.

### Experiment history

`Experiment duration` · `Runs, passes and failures — cluster totals`

Duration is the *injection*, not the recovery — on a timeline it is the fault
window itself, and it is what `kates-chaos` is read against. The three
cumulative aggregates beside it are the exporter's own answer to *how much
chaos has this cluster actually run*; a step in `runs` with no step in either
of the others is an experiment still awaited.

### The execution plane

`Chaos infra pods — status`

From kube-state-metrics rather than from Litmus: an operator that is down
cannot report that it is down. An empty table means either nothing here is
Running or kube-state-metrics is not installed, and those are very different
problems.

## Editing it

`board.py`, then `scripts/gen-dashboards.py`, then
`python3 scripts/check-dashboards.py`. Do not edit `dashboard.json` or
`charts/kates-chaos/files/dashboards/kates-chaos-infra.json` — `--check` fails
on a hand-edited copy.

## Related boards

- `kates-chaos` — what the fault did to the Kafka cluster and to the workload.
  This board says the experiment ran; that one says what it cost.
- `kafka-kraft` — the brokers' own side of a chaos run.

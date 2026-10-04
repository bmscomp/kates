"""Kates — Chaos infrastructure: is the chaos platform itself alive?

  (header)           experiments reporting Pass, reporting Fail, and whether
                     the chaos operator is available at all
  Experiment history how long each one ran, and the cluster-wide run totals
  The execution plane the operator and exporter pods this chart deploys

Migrated from the 6 panels of hand-written JSON that lived inside
`charts/kates-chaos/templates/grafana-dashboard.yaml`. That was the twelfth
board in the repository and the only one outside `dashboards/`: no panel
descriptions, no layout gate, no metric checking, and in no delivery table.

WHY THIS IS NOT FOLDED INTO `kates-chaos`. The two boards look adjacent and
are not. `kates-chaos` answers *what did the fault do to the Kafka cluster and
to the workload on it* — it is scoped to the Kafka namespace, it is delivered
by `charts/monitoring`, and it reads four producers across Kafka, cAdvisor and
the Kates application. This board answers *is the chaos platform installed and
running at all* — it is scoped to the namespace the LitmusChaos execution
plane runs in, it is delivered by `charts/kates-chaos`, and four of its six
panels (the run totals and both infra panels) have no counterpart on the other
board. Folding it in would move it to `charts/monitoring`, leave this chart's
`monitoring.grafanaDashboard.enabled` switch shipping nothing, and break the
rule the delivery table follows everywhere else: the chart that owns the
workload ships the board that watches it.

FOUR NAMES THE OLD BOARD READ THAT LITMUS DOES NOT PUBLISH. Every one was
guessed from what the panel was called rather than from the chaos-exporter's
metric list (github.com/litmuschaos/chaos-exporter). README.md has the table;
in short:

  litmuschaos_experiment_verdict{verdict=…}   the label is `chaosresult_verdict`
  litmuschaos_engine_experiment_count         no such series; the panel wanted
                                              litmuschaos_experiment_total_duration
  increase(litmuschaos_experiment_verdict[1h])  `increase()` over a 0/1 gauge;
                                              the run total is
                                              litmuschaos_cluster_scoped_experiments_run_count
  pod=~".*chaos-operator.*"                   the operator Deployment is named
                                              by litmus-core's fullnameOverride
                                              (`litmus`), never `chaos-operator`

TWO CONSTANTS, INJECTED BY THE CHART. `$namespace` is where the execution
plane runs (`monitoring.grafanaDashboard.namespace`, default the release
namespace) and `$operator_deployment` is the Litmus operator Deployment's
name. Neither is derivable from the data and neither may be frozen into a
generated file, so both are hidden `constant` variables that
`charts/kates-chaos/templates/grafana-dashboard.yaml` fills in per release —
the same scheme the MirrorMaker 2 boards use.
"""

from __future__ import annotations

import panels as P
from layout import Row

GREEN = [("green", None)]
RED_ABOVE_ZERO = [("green", None), ("red", 1)]
# Zero replicas available is the failure; one or more is the healthy state.
RED_AT_ZERO = [("red", None), ("green", 1)]


def variables(variant: str) -> list[dict]:
    return [
        P.constant_var("namespace", "litmus", label="Chaos infra namespace"),
        P.constant_var("operator_deployment", "litmus",
                       label="Chaos operator Deployment"),
    ]


# ── Header — the verdicts the exporter is reporting right now ──────────────

def _verdicts() -> list[dict]:
    return [
        P.stat(
            "Chaos experiments — Pass",
            "ChaosResults whose verdict is currently Pass, counted from "
            "`litmuschaos_experiment_verdict{chaosresult_verdict=\"Pass\"}`. "
            "The old board selected `verdict=\"Pass\"`; the label the "
            "chaos-exporter emits is `chaosresult_verdict`, so that panel "
            "matched every verdict rather than the passing ones. The `== 1` "
            "matters: the exporter sets the series to 0 for an Awaited "
            "verdict, and back to 0 once a verdict has been repeated for "
            "longer than its `TSDB_SCRAPE_INTERVAL`, so a count of the series "
            "and a count of the ones reading 1 are different numbers. Passed "
            "means Litmus' own probes were satisfied; what the Kafka cluster "
            "and the workload did is the `Kates — Chaos` board.",
            P.targets((
                'count(litmuschaos_experiment_verdict'
                '{chaosresult_verdict="Pass"} == 1) or vector(0)', "Pass")),
            unit="short", w=8, h=5, thresholds=GREEN,
        ),
        P.stat(
            "Chaos experiments — Fail",
            "The same count for a Fail verdict. A failure here is not "
            "necessarily a cluster problem: Litmus marks an experiment failed "
            "when it could not inject the fault at all, which makes the "
            "resilience claim for that window unsupported rather than "
            "disproved. The zero fallback is deliberate — with no failing "
            "ChaosResult the series does not exist, and *No data* on a red "
            "tile reads as a broken board rather than a clean run. It is "
            "anchored on the *unfiltered* verdict series, so the zero can "
            "only be drawn once there is at least one ChaosResult to read. A "
            "cluster where chaos has run and nothing failed reads 0; a "
            "cluster whose exporter nobody is scraping reads No data, which "
            "is the truth. A clean run and an unwatched one must not look "
            "alike on a tile whose whole job is to go red.",
            P.targets((P.or_zero(
                'count(litmuschaos_experiment_verdict'
                '{chaosresult_verdict="Fail"} == 1)',
                "litmuschaos_experiment_verdict"), "Fail")),
            unit="short", w=8, h=5, thresholds=RED_ABOVE_ZERO,
        ),
        P.stat(
            "Chaos operator",
            "Replicas of the Litmus chaos operator Deployment that are "
            "available, from kube-state-metrics rather than from Litmus — an "
            "operator that is down cannot report that it is down. Red at zero "
            "means no ChaosEngine will be acted on, whatever the CRs in the "
            "cluster say. The old board asked for pods matching "
            "`.*chaos-operator.*`; `chaos-operator` is the operator's "
            "*container* name, while the Deployment is named by litmus-core's "
            "`fullnameOverride` (`litmus`), so that panel could never match a "
            "pod and was a permanent, reassuring zero.",
            P.targets((
                'max(kube_deployment_status_replicas_available'
                '{namespace="$namespace", deployment="$operator_deployment"}) '
                "or vector(0)", "Available")),
            unit="short", w=8, h=5, thresholds=RED_AT_ZERO,
        ),
    ]


# ── Experiment history ─────────────────────────────────────────────────────

def _experiments() -> list[dict]:
    return [
        P.timeseries(
            "Experiment duration",
            "How long each Litmus experiment ran, by chaos engine and chaos "
            "result. This is the duration of the *injection*, not of the "
            "recovery, and it is the timeline the `Kates — Chaos` board is "
            "read against. The panel this replaces was titled *Chaos Engine "
            "Duration (seconds)* and read `litmuschaos_engine_experiment_"
            "count`, which is neither a duration nor a series the "
            "chaos-exporter publishes under any name — the title was right "
            "and the expression was a guess. The exporter spells the real one "
            "`_total_duration`, with no unit suffix, even though the value is "
            "seconds.",
            P.targets((
                "litmuschaos_experiment_total_duration",
                "{{chaosengine_name}} — {{chaosresult_name}}")),
            unit="s", w=12,
        ),
        P.timeseries(
            "Runs, passes and failures — cluster totals",
            "The three cluster-wide aggregates the exporter publishes, as "
            "cumulative lines: every run it has seen, and how those runs were "
            "graded. A step in `runs` with no step in either of the others is "
            "an experiment still awaited. The panel this replaces plotted "
            "`increase(litmuschaos_experiment_verdict[1h])` — `increase()` "
            "over a gauge that flips between 0 and 1 as verdicts are set and "
            "aged out, which produces a number with no meaning. These three "
            "are monotonic and are the exporter's own answer to *how much "
            "chaos has this cluster actually run*. The `cluster_scoped` "
            "family is the right one here because litmus-core deploys the "
            "exporter with an empty `WATCH_NAMESPACE`, which makes it "
            "aggregate over every namespace; the `namespace_scoped` family "
            "stays empty on this install. Cumulative since the exporter "
            "started, so read the change across a GameDay, not the absolute "
            "number.",
            P.targets(
                ("litmuschaos_cluster_scoped_experiments_run_count", "Runs"),
                ("litmuschaos_cluster_scoped_passed_experiments", "Passed"),
                ("litmuschaos_cluster_scoped_failed_experiments", "Failed")),
            unit="short", w=12,
        ),
    ]


# ── The execution plane itself ─────────────────────────────────────────────

def _infra() -> list[dict]:
    return [
        P.table(
            "Chaos infra pods — status",
            "Every Running pod in the namespace the execution plane is "
            "installed in — the Litmus operator and, when "
            "`litmus-core.exporter.enabled` is set, the chaos-exporter that "
            "publishes every `litmuschaos_*` series on this board. "
            "`$namespace` is `monitoring.grafanaDashboard.namespace`, which "
            "defaults to the release namespace; it is not the Kafka "
            "namespace the experiments target. An empty table means either "
            "nothing here is Running or kube-state-metrics is not installed, "
            "and those are very different problems.",
            [P.target(
                'kube_pod_status_phase{namespace="$namespace", phase="Running"} == 1',
                "{{pod}}", instant=True, fmt="table")],
            w=24, h=8,
            transformations=[
                {"id": "organize", "options": {"excludeByName": {
                    "Time": True, "Value": True, "__name__": True,
                    "container": True, "endpoint": True, "instance": True,
                    "job": True, "namespace": True, "service": True,
                    "uid": True}}},
            ]),
    ]


def build(variant: str) -> list[Row]:
    return [
        Row("", _verdicts()),
        Row("Experiment history", _experiments()),
        Row("The execution plane", _infra()),
    ]

"""Kyverno: what the admission webhook let through, and what it cost.

Nine panels, in the order the question is usually asked: how much traffic the
webhook saw, how much of it was blocked, what the policies decided, and how
long the answer took. The last two panels are the webhook's own health — a
policy engine that is slow is an API server that is slow, and a controller
that is not running is a policy that is not enforced.

TWO NAMESPACES, WHICH ARE NOT THE SAME NAMESPACE. The ConfigMap this board
ships in goes wherever the Grafana sidecar watches
(`kyvernoPolicy.grafanaDashboardNamespace`, default `monitoring`). The
controller-pod panel reads `namespace="kyverno"`, which is where Kyverno
itself runs. Neither is the release's namespace, so unlike the MirrorMaker 2
boards this one carries no `$namespace` variable: there is nothing per-release
to put in it, and a variable that always held one literal would only invite
someone to change it.

The board is cluster-scoped and its uid is fixed — one Kyverno per cluster,
one board. That is why nothing here is injected at render time.
"""

from __future__ import annotations

from layout import Row
from panels import (barchart, or_zero, piechart, stat, table, target, targets,
                    timeseries)

GREEN = "#73BF69"
AMBER = "#FADE2A"
RED = "#FF6347"


def _fixed(name: str, colour: str) -> dict:
    return {"matcher": {"id": "byName", "options": name},
            "properties": [{"id": "color", "value": {"fixedColor": colour,
                                                     "mode": "fixed"}}]}


def build(variant: str = "default") -> list[Row]:
    return [Row("", [
        stat(
            "Admission Requests — Total",
            "Every admission review Kyverno answered in the last hour, whatever "
            "the verdict. This is the load the webhook is carrying: it rises "
            "with deployments, not with violations.",
            targets(("sum(increase(kyverno_admission_requests_total[1h]))", "Total")),
            w=4, h=5, thresholds=[(GREEN, None)]),
        stat(
            "Admission — Allowed",
            "CREATE requests admitted in the last hour. Compared with the total "
            "beside it, this is how much of the traffic is new objects rather "
            "than updates — useful for reading the blocked count next to it.",
            targets(('sum(increase(kyverno_admission_requests_total'
                     '{resource_request_operation="CREATE",request_allowed="true"}[1h]))',
                     "Allowed")),
            w=4, h=5, thresholds=[(GREEN, None)]),
        stat(
            "Admission — Blocked",
            "Requests an Enforce policy refused. The zero fallback is "
            "deliberate: with every policy in Audit mode nothing is ever "
            "blocked, the series does not exist, and without it this panel "
            "would read No data — which looks like a broken board rather than "
            "a quiet one. It is anchored to the unfiltered request counter the "
            "two tiles to the left read, so the zero can only be drawn while "
            "Kyverno is answering and being scraped. A webhook nobody is "
            "scraping reads No data, not 'nothing was blocked'.",
            targets((or_zero(
                'sum(increase(kyverno_admission_requests_total'
                '{request_allowed="false"}[1h]))',
                "kyverno_admission_requests_total"), "Blocked")),
            w=4, h=5, thresholds=[(GREEN, None), (RED, 1)]),
        piechart(
            "Policy Results — Pass vs Fail",
            "Every rule evaluation, by verdict. In Audit mode a fail is a "
            "recorded violation rather than a refused request, so this slice "
            "grows while the blocked count beside it stays at zero — that gap is "
            "exactly what to expect before switching a policy to Enforce.",
            targets(('sum(kyverno_policy_results_total{rule_result="pass"})', "Pass"),
                    ('sum(kyverno_policy_results_total{rule_result="fail"})', "Fail"),
                    ('sum(kyverno_policy_results_total{rule_result="warn"})', "Warn")),
            w=6, color="palette-classic",
            overrides=[_fixed("Pass", GREEN), _fixed("Fail", RED), _fixed("Warn", AMBER)]),
        stat(
            "Admission Webhook Latency (p99)",
            "The tail the API server waits on. Every mutating and validating "
            "request to the cluster pays this, so amber at 0.5s is not cosmetic: "
            "a slow policy engine reads to everyone else as a slow cluster, and "
            "past the webhook timeout it reads as a failed deploy.",
            targets(("histogram_quantile(0.99, sum(rate("
                     "kyverno_admission_review_duration_seconds_bucket[5m])) by (le))",
                     "p99")),
            w=6, h=5, unit="s", thresholds=[(GREEN, None), (AMBER, 0.5), (RED, 1)]),
        timeseries(
            "Policy Violations Over Time",
            "Failures per policy, as a trend. A step up is a new workload "
            "meeting an old policy; a step down with no policy change is usually "
            "the workload that was failing being deleted rather than fixed. "
            "Grouped on `policy_name`: the board this replaces grouped on "
            "`policy`, which Kyverno does not emit, so every policy landed in "
            "one unlabelled series.",
            targets(('sum by (policy_name) (increase(kyverno_policy_results_total'
                     '{rule_result="fail"}[5m]))', "{{ policy_name }}")),
            w=12, color="palette-classic"),
        barchart(
            "Violations by Rule",
            "Which rule, rather than which policy — the level at which a "
            "violation is actually fixed. The longest bar is the one worth "
            "reading first: it is either the rule the cluster genuinely "
            "violates most, or the rule that is wrong. Grouped on `rule_name`: "
            "the board this replaces grouped on `rule`, which Kyverno does not "
            "emit, so the ranking was a single bar.",
            [target('sum by (rule_name) (kyverno_policy_results_total{rule_result="fail"})',
                    "{{ rule_name }}", instant=True, fmt="table")],
            w=12, color="palette-classic"),
        timeseries(
            "Admission Review Duration (histogram)",
            "p50, p90 and p99 together. The shape matters more than any one "
            "line: p50 flat with p99 climbing is a few expensive policies or a "
            "few large objects, while all three rising together is the engine "
            "itself — CPU, or a policy that now matches everything.",
            targets(("histogram_quantile(0.50, sum(rate("
                     "kyverno_admission_review_duration_seconds_bucket[5m])) by (le))", "p50"),
                    ("histogram_quantile(0.90, sum(rate("
                     "kyverno_admission_review_duration_seconds_bucket[5m])) by (le))", "p90"),
                    ("histogram_quantile(0.99, sum(rate("
                     "kyverno_admission_review_duration_seconds_bucket[5m])) by (le))", "p99")),
            w=12, unit="s", color="palette-classic"),
        table(
            "Controller Pods — Status",
            "The Kyverno controllers themselves, from kube-state-metrics rather "
            "than from Kyverno — a webhook that is down cannot report that it is "
            "down. `namespace=\"kyverno\"` is Kyverno's own namespace, not this "
            "release's and not the one this ConfigMap is delivered to. An empty "
            "table means either no controller is Running or kube-state-metrics "
            "is not installed, and those are very different problems.",
            [target('kube_pod_status_phase{namespace="kyverno", phase="Running"} == 1',
                    "{{ pod }}", instant=True, fmt="table")],
            w=12, h=6,
            transformations=[
                {"id": "organize", "options": {"excludeByName": {
                    "Time": True, "Value": True, "__name__": True, "container": True,
                    "endpoint": True, "instance": True, "job": True, "namespace": True,
                    "service": True, "uid": True}}},
            ]),
    ])]

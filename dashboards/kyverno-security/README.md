# Kyverno security policies

**What did the admission webhook let through, what did it refuse, and what is
it costing the API server?**

Nine panels for the policy engine that sits in front of every create and
update in the cluster. Delivered by `charts/monitoring` with every other
board (`dashboards.enabled` there) since kates 0.9.0 — one Kyverno per
cluster, one board, beside the rest of the cluster's boards. The policies
themselves still come from `charts/kates`'s `kyvernoPolicy.enabled`.

## Who opens it, and when

| You are… | What you are looking at |
|---|---|
| About to switch a policy from Audit to Enforce | **Policy Results** and **Violations by Rule** — everything that fails today is something that would be refused tomorrow |
| Told "deploys started failing" | **Admission — Blocked**, then **Violations by Rule** |
| Told "the cluster feels slow" | **Admission Webhook Latency (p99)** and the duration histogram |
| Checking Kyverno itself is alive | **Controller Pods — Status** |

## The panels, and what a move means

**Admission Requests / Allowed / Blocked.** The load the webhook carries, over
the last hour. It rises with deployments, not with violations. *Blocked*
carries a zero fallback deliberately: with every policy in Audit mode nothing
is ever refused, the series does not exist, and without the fallback the panel
would read *No data* — which looks like a broken board rather than a quiet
one. The fallback is **anchored** to the unfiltered request counter the two
tiles to its left read, so it can only draw its zero while Kyverno is
answering and being scraped. A webhook nobody is scraping reads *No data*,
not "nothing was blocked" — see [the zero-fallback
convention](../README.md#zeros-that-mean-measured-and-zeros-that-mean-absent).

**Policy Results — Pass vs Fail.** Every rule evaluation, by verdict. In Audit
mode a fail is a *recorded violation* rather than a refused request, so this
slice grows while *Blocked* stays at zero. That gap is exactly what to read
before switching a policy to Enforce: it is the list of things that will start
being refused.

**Admission Webhook Latency (p99)** and **Admission Review Duration**. The
tail the API server waits on. Every mutating and validating request to the
cluster pays it, so amber at 0.5s is not cosmetic — a slow policy engine reads
to everyone else as a slow cluster, and past the webhook timeout it reads as a
failed deploy. The shape of the three percentiles matters more than any one
line: p50 flat with p99 climbing is a few expensive policies or a few large
objects, while all three rising together is the engine itself.

**Policy Violations Over Time** and **Violations by Rule.** Per policy as a
trend, per rule as a ranking. The rule is the level at which a violation is
actually fixed, and the longest bar is either the rule the cluster genuinely
violates most or the rule that is wrong.

**Controller Pods — Status.** From `kube_pod_status_phase`, not from Kyverno:
a webhook that is down cannot report that it is down. An empty table means
either no controller is Running or kube-state-metrics is not installed, and
those are very different problems.

## Three namespaces that are not the same namespace

Worth stating plainly, because the board mentions two of them and lives in a
third:

- the ConfigMap is delivered with every other board, in the monitoring
  release's namespace (`charts/monitoring`'s `dashboards.namespace`);
- the controller-pod panel reads `namespace="kyverno"`, which is where Kyverno
  runs;
- the kates release's own namespace appears nowhere on this board.

That is why this board carries no `$namespace` variable while the MirrorMaker
2 boards do. There is nothing per-release to put in one, and a variable that
always held a single literal would only invite someone to change it.

For the same reason nothing is injected at render time: one Kyverno per
cluster means one board, so the `uid` (`kyverno-security-overview`) and the
title are fixed and `charts/kates` loads this file unmodified.

## What changed when it moved here

The nine panels, their titles and their types are exactly what
`charts/kates/templates/kyverno-grafana-dashboard.yaml` carried as inline JSON,
and seven of the nine expressions are unchanged. Three things are new:

- **descriptions**, on all nine. They had none, and the layout gate — which
  used to cover only the MirrorMaker 2 boards and now covers everything in
  `dashboards/` — requires one on every panel. `panels.py` will not build a
  panel without a description, so this was not optional.
- **positions and ids**, recomputed by the layout packer. The panels are in
  the same order; they are no longer hand-numbered.
- **two corrected label names**, which is the one place the port is not
  faithful. The old board grouped *Policy Violations Over Time* on `policy`
  and *Violations by Rule* on `rule`. Kyverno emits neither: the labels on
  `kyverno_policy_results_total` are `policy_name` and `rule_name`
  ([kyverno.io/docs/reference/metrics](https://kyverno.io/docs/reference/metrics/)).
  Grouping on a label that does not exist collapses every series into one
  unlabelled line, so the trend showed a single aggregate and the "ranking"
  was a single bar — which is not what the section above promises, and not
  what either panel is for. The port corrects them to `policy_name` and
  `rule_name`. Every other expression on the board was checked against the
  same reference and left alone: `kyverno_admission_requests_total` really
  does carry `request_allowed` and `resource_request_operation`,
  `kyverno_policy_results_total` really does carry `rule_result` with
  `pass`/`fail`/`warn` among its values, and
  `kyverno_admission_review_duration_seconds_bucket` really is a cumulative
  histogram with an `le` label — the only two places on any board in this
  directory where `histogram_quantile()` is correct.

## Editing it

`board.py`, then `scripts/gen-dashboards.py`, then
`python3 scripts/check-dashboards.py`. Do not edit `dashboard.json` or
`charts/monitoring/dashboards/kyverno-security.json` — `--check` fails on a
hand-edited copy.

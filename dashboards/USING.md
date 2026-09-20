# Using these dashboards

Twelve boards. This page is the operator's entry point: **which one to open**,
**how to read it**, and **what to do when it is empty**.

It is deliberately short. The long form — installing by all four routes,
verifying the install step by step, driving the variables, two worked incident
readings, and a full decision tree for an empty panel — is
[Tutorial 13](../docs/tutorials/13-using-the-dashboards.md). Each board has its
own README with the panel-by-panel detail, and
[`METRICS.md`](METRICS.md) documents every series any board reads.

If you are looking for how the boards are *built* rather than used,
[`README.md`](README.md) is that.

---

## Which board answers which question

| I want to know… | Board | uid | Delivered by |
|---|---|---|---|
| Is the Kafka **metadata quorum** healthy, is metadata committing, has every broker applied it? | [`kafka-kraft`](kafka-kraft/README.md) | `kates-kafka-kraft` | `charts/monitoring` |
| What is this cluster doing **under this load**, down to the partition — and was the throughput number real? | [`kafka-performance`](kafka-performance/README.md) | `kates-kafka-performance` | `charts/monitoring` |
| Is a **Connect pipeline** actually moving data — which connector, which task, which worker? | [`kafka-connect`](kafka-connect/README.md) | per release | `charts/connect-cluster` |
| Is the **mirror** up, lagging or erroring — and if lagging, which leg and which end? | [`mirror-maker2`](mirror-maker2/README.md) | per release | `charts/mirror-maker2` |
| **Can I cut over yet**, and if not, what is left? | [`mirror-maker2-migration`](mirror-maker2-migration/README.md) | per release | `charts/mirror-maker2` |
| Is this **Kates release** up and doing anything? | [`kates-overview`](kates-overview/README.md) | `kates-overview` | `charts/kates` |
| What is **this run** doing, right now? | [`kates-benchmark`](kates-benchmark/README.md) | `kates-benchmark-overview` | `charts/monitoring` |
| Is this build **slower than the last one**? | [`kates-trend`](kates-trend/README.md) | `kates-trend-analysis` | `charts/monitoring` |
| Is the **Kates process itself** healthy — the Quarkus app, not the cluster it drives? | [`kates-application`](kates-application/README.md) | `kates-application-health` | `charts/monitoring` |
| We injected a fault. **What did the cluster and the workload do?** | [`kates-chaos`](kates-chaos/README.md) | `kafka-chaos-dashboard` | `charts/monitoring` |
| Is the **chaos platform** installed, running, and has it run anything? | [`kates-chaos-infra`](kates-chaos-infra/README.md) | `kates-chaos-overview` | `charts/kates-chaos` |
| What did the **admission webhook** let through and what did it refuse? | [`kyverno-security`](kyverno-security/README.md) | `kyverno-security-overview` | `charts/kates` |

Three boards take a uid **per release**, because one Grafana may hold several
Connect or MirrorMaker 2 installs and Grafana keys a board by uid. Their charts
rewrite it to `kates-connect-<name>-<digest>` and `kates-mm2[-mig]-<name>-<digest>`,
truncating the release name and appending a hash of it so two releases sharing a
long prefix still get different uids. The other nine belong to a *cluster* rather
than a release and pick which cluster in the board, so one static uid is enough.

**These are not all the boards you have.** `charts/strimzi-operator` enables
the Strimzi operator's own nine by default — broker health, quorum identity,
Cruise Control, consumer lag, the bridge, OAuth. Nothing here restates them,
which is why `kafka-kraft` covers metadata *degradation* and not quorum
*identity*. Its header carries a dropdown into them.

---

## Reading a board

### Set the variables first

Most boards are scoped by template variables at the top, and **a variable that
resolves to nothing silently blanks every panel under it**. That looks exactly
like a broken board and is not one. What each board takes:

| Board | Variables |
|---|---|
| `kafka-kraft`, `kafka-connect`, `mirror-maker2-migration` | `namespace`, `cluster` |
| `kafka-performance` | `namespace`, `cluster`, `topic` |
| `mirror-maker2` | `namespace`, `cluster`, `source` — the last filled by the chart from your mirror aliases, not resolved from data |
| `kates-application` | `namespace`, `job`, `pod`, `db_pod` |
| `kates-benchmark` | `job`, `run_id`, `test_type` |
| `kates-trend` | `job`, `test_type` |
| `kates-chaos` | `namespace`, `pod`, `kates_job` |
| `kates-overview` | `job` |
| `kates-chaos-infra` | `namespace`, `operator_deployment` — both **hidden**: constants the chart fills in per release, with nothing to pick |
| `kyverno-security` | none — one webhook per cluster, cluster-wide by construction |

`$cluster` is the **Kafka** cluster, not the Kubernetes one. `$job` is one
release of the Kates application. Each board's README says which scrape config
puts that label on that series, so an empty dropdown has a specific cause
rather than a shrug.

Neither `kyverno-security` nor `kates-chaos-infra` shows you a picker — the
first is cluster-wide by construction, the second is scoped by constants its
chart writes in at install time. Those two are empty for some other reason.

Hover a picker's label where there is one. `kates-chaos`'s `$namespace` and
`$kates_job` carry a tooltip saying what an *empty* picker means, because a
variable that resolved to nothing is the one place on screen that can
explain a board that has nothing on it.

### What the colours are telling you

A single-value tile has three states, and this repository is careful that they
do not look alike:

| What you see | What it means |
|---|---|
| A number, in green or red | Measured. The thresholds are real and mostly frozen from the matching alert's own default. |
| **`0` in green** | Measured zero. On a fault panel the fallback that drew it is *anchored*, so it can only appear while something is genuinely being scraped. |
| **`0` in red** | Also measured — on a panel counting things that must exist. *Brokers*, *Workers up*, *Pods ready*: zero of them is the outage, not the resting state. |
| **"No data", in the ordinary text colour** | Nothing was measured. Not healthy, not alarming — unknown. |

That last row is the one worth internalising. Grafana normally paints *No data*
with the **base threshold colour**, so an unscraped fault panel says "No data"
in green and reads as fine, while a panel whose base step is red invents an
alarm out of a missing scrape. Every stat and gauge here carries a value
mapping that overrides that, so on these boards a coloured "No data" tile
means the board itself has a bug — tell us.

The Strimzi operator's boards do not do this, and you will be reading them
too. Theirs say `N/A`, in green on *Under Replicated Partitions* and red on
*Brokers Online*, on the same empty cluster at the same moment. Read their
`N/A` as **unknown**, whatever colour it arrives in.

[`README.md` §Zeros that mean *measured*](README.md#zeros-that-mean-measured-and-zeros-that-mean-absent)
has the before/after and the evidence.

### Time range and refresh are set per board

They ship with the window the question needs, not a global default: 30 minutes
at 10s for a live benchmark run, 6 hours at 30s for a quorum or a mirror, 1
hour at 10s for a load test. A quorum problem is minutes long and a metadata
lag problem is hours long — an hour shows the first and hides the second.

---

## When the whole board is empty

Almost always one cause, and it is not the board. **Prometheus is not scraping
the workload at all.** An unselected PodMonitor raises no event, so this is
silent.

Find Prometheus and port-forward it. Don't assume the namespace: `make
monitoring` installs the stack into `kafka` alongside the brokers, and plenty
of clusters put it in `monitoring` instead.

```bash
kubectl get prometheus -A          # NAMESPACE and NAME of the Prometheus CR
PROM_NS=kafka                      # whatever that printed
kubectl -n "$PROM_NS" port-forward svc/monitoring-kube-prometheus-prometheus 9090:9090
```

```bash
# 1. Are there any Kafka series at all?
curl -s localhost:9090/api/v1/label/__name__/values \
  | jq -r '.data[]' | grep -c '^kafka_'

# 2. What is Prometheus actually scraping?
curl -s 'localhost:9090/api/v1/targets?state=active' \
  | jq -r '.data.activeTargets[].labels.job' | sort -u

# 3. Which PodMonitors will it even look at?
kubectl -n "$PROM_NS" get prometheus \
  -o jsonpath='{.items[0].spec.podMonitorSelector}'; echo
kubectl get podmonitors -A \
  -o custom-columns=NS:.metadata.namespace,NAME:.metadata.name,LABELS:.metadata.labels
```

A `0` from the first, a target list containing only monitoring-stack jobs, and
a `podMonitorSelector` whose labels none of your PodMonitors carry is the
signature. kube-prometheus-stack defaults its five selectors to
`release: <its own release name>`, so a stack installed as `monitoring` ignores
every monitor labelled anything else — or labelled nothing.

The other signature is the third command printing **no PodMonitors at all**.
Then nothing is unselected; nothing was ever created. Every chart here keeps
its scrape and its rules behind a switch — `monitoring.podMonitor.enabled` and
`alerts.enabled` on kafka-cluster and connect-cluster, `metrics.enabled` and
`podMonitors.enabled` on mirror-maker2, `metrics.serviceMonitor.enabled` on
kates, `monitoring.serviceMonitor.enabled` with `litmus-core.exporter.enabled`
on kates-chaos — and for a long time the kind overlays and `kates detect` held
every one of them at `false`, so `kates deploy` installed the twelve boards
and nothing for them to read. `helm get values <release> -n <ns>` shows which
switch a release was installed with. `kates deploy --with-monitoring` now sets
them itself, on every component it installs; a release you already have gets
them by hand. The Kafka line also names the 0.4 key, `podMonitors`, which an
older `kates detect` left in the stored values and which wins over the current
one:

```bash
helm upgrade krafter charts/kafka-cluster -n kafka --reuse-values \
  --set monitoring.podMonitor.enabled=true --set podMonitors.enabled=true --set alerts.enabled=true
helm upgrade connect-cluster charts/connect-cluster -n connect --reuse-values \
  --set monitoring.podMonitor.enabled=true --set alerts.enabled=true
helm upgrade mm2 charts/mirror-maker2 -n kafka --reuse-values \
  --set metrics.enabled=true --set podMonitors.enabled=true --set alerts.enabled=true
helm upgrade kates charts/kates -n kates --reuse-values \
  --set metrics.serviceMonitor.enabled=true --set metrics.prometheusRule.enabled=true
helm upgrade chaos charts/kates-chaos -n litmus --reuse-values \
  --set litmus-core.exporter.enabled=true --set monitoring.serviceMonitor.enabled=true
helm upgrade monitoring charts/monitoring -n monitoring --reuse-values \
  --set chaosAlerts.enabled=true
helm upgrade kyverno kyverno/kyverno --version 3.6.4 -n kyverno --reuse-values \
  --set admissionController.serviceMonitor.enabled=true --set backgroundController.serviceMonitor.enabled=true
```

Give Prometheus a minute after each: the operator regenerates its configuration
and the reloader picks it up on the kubelet's next secret sync.

The fix is five lines of `charts/monitoring/values.yaml` — the five
`*SelectorNilUsesHelmValues: false` settings, which leave all ten selectors on
the rendered Prometheus CR as `{}`, meaning *select everything in the watched
namespaces*. To confirm that is the cause before changing the chart, widen the
live CR by hand; `helm upgrade` puts it back:

```bash
kubectl -n "$PROM_NS" patch prometheus monitoring-kube-prometheus-prometheus --type merge \
  -p '{"spec":{"podMonitorSelector":{},"serviceMonitorSelector":{},"ruleSelector":{}}}'
```

Three of the five, because those are the three these boards need; `probe` and
`scrapeConfig` are in the chart fix for consistency and nothing here reads
them. The matching `*NamespaceSelector` fields are already `{}` under this
chart — and `{}` there means *all namespaces*, which is what lets a Prometheus
in `kafka` pick up the Kates ServiceMonitor over in `kates`. If your stack
came from somewhere else, check those too: nil rather than `{}` means *this
Prometheus's own namespace only*.

(The service is named after the release. This repository installs the stack as
`monitoring`, hence `monitoring-kube-prometheus-prometheus`; substitute your
own release name if you installed it differently.)

Give it a minute and reload. If the boards fill, that was it.

## When one panel is empty

Work down this list; it is ordered by how often each is the answer.

1. **A variable above it resolved to nothing.** Check the pickers before
   anything else. The board README says what each resolves from.
2. **The datasource is the wrong Prometheus.** The `Data source` picker is on
   every board; with two Prometheus datasources, check it is the one holding
   these series.
3. **Nothing publishes that series here.** Twenty series need something
   installed first — LitmusChaos and its chaos-exporter, Kyverno,
   kube-state-metrics configured for `ChaosEngine` — or need the right kind of
   run. [`METRICS.md`](METRICS.md) says for every series whether this
   repository publishes it, which is what lets *nothing is wrong* be told apart
   from *nothing publishes this*.
4. **The exporter is off.** Strimzi only opens the scrape port when
   `metricsConfig` is set, so "metrics disabled" and "port closed" are one
   state.
5. **The panel filters on a label nothing sets.** Rare now — `check-dashboards-live.py`
   exists to catch exactly this — but it is what a correct metric name in an
   empty panel usually means.

[Tutorial 13, section 7](../docs/tutorials/13-using-the-dashboards.md) is the
full decision tree, with the command for each branch.

---

## Updating the boards on a cluster you already installed

**If these charts put the boards there, `helm upgrade` is how they change.**
The Grafana dashboard sidecar provisions with `allowUiUpdates: false`, so
Grafana refuses any API write to a board it provisioned — `install.py` will
report `HTTP 400 … Cannot save provisioned dashboard` on every single one.
That is Grafana protecting the provisioner, not a broken installer.

```bash
helm list -A | grep -i monitor      # find the release and its namespace
helm upgrade monitoring charts/monitoring -n "$PROM_NS" --timeout 10m --wait
```

The sidecar notices the new ConfigMap within a minute or so and rewrites the
files; reload the board after that. The same upgrade carries any change to
`values.yaml`, which is why it is also the fix for the empty-board problem
above.

## Installing them somewhere else

Four routes, all covered in [Tutorial 13, section 3](../docs/tutorials/13-using-the-dashboards.md):
the Helm charts, [`install.py`](install.py) straight at the Grafana API,
`bundle/provisioning/` for file provisioning without a sidecar, and
`bundle/configmaps.yaml` for a sidecar without these charts.

`install.py` is for a Grafana that does **not** already provision these boards
— someone else's, a local one, a shared Grafana outside the cluster:

```bash
dashboards/install.py --url http://localhost:3000 --user admin:admin
dashboards/install.py --url "$GRAFANA_URL" --dry-run kafka-kraft mirror-maker2
```

Name boards as positional arguments to install a subset, `--list` to see them.
It also takes `--token` instead of `--user`, `--folder`, `--tag`, and
`--datasource` — though you rarely need that last one: the boards ask for the
datasource by *plugin id* rather than by name, so they bind to whatever
Prometheus-compatible datasource that Grafana has without being told what it
is called.

---

## Where to go from here

- **[Tutorial 13](../docs/tutorials/13-using-the-dashboards.md)** — the full
  guide: prerequisites stated exactly, all four install routes, verifying the
  install, worked readings of a healthy KRaft cluster and a failing Connect
  pipeline, running one Grafana across several clusters, and writing your own
  panel without walking into the four naming traps.
- **Each board's `README.md`** — what that board answers, what each section
  means when it moves, and which alert reads which panel.
- **[`METRICS.md`](METRICS.md)** — every series: type, labels, what it means
  operationally, how to query it, and who publishes it.
- **[`README.md`](README.md)** — how the boards are generated, the gates that
  keep them honest, and which Grafana versions they are verified on.

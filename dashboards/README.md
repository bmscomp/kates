# Dashboards

Every Grafana dashboard this project ships, in one place, each with the
documentation for what it shows and what the metrics mean.

**Looking for how to use them?** [`USING.md`](USING.md) is the operator's
entry point — which board answers which question, how to read the tiles, and
the one thing to check when a board is empty. This file is about how they are
*built*.

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
  README.md            this file — how the boards are built and checked
  USING.md             how to READ them: which board for which question,
                       what the colours mean, what to do when one is empty
  METRICS.md           every metric on every board, and what it means
  install.py           push the boards into any Grafana over its HTTP API
  test_install.py      its tests, against a fake Grafana (stdlib unittest)
  test_panels.py       what _lib/panels.py's zero fallback evaluates to,
                       asserted through promtool against Prometheus itself,
                       and that no-data reads as neither healthy nor alarming
  _lib/                the builder: layout.py, panels.py, build.py, bundle.py
  bundle/              GENERATED — the file-provisioning and ConfigMap bundles
  <board>/
    board.py           the board, as rows of panels
    manifest.yaml      uid, title, tags, and which charts get a copy
    dashboard.json     GENERATED — do not edit
    README.md          what this board is for, and how to read it
```

[`METRICS.md`](METRICS.md) is the cross-cutting half of the documentation. A
board's own README says what that board answers and what each section means
when it moves; `METRICS.md` takes every series read by any board — 213 of them
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

The same run writes `bundle/`, which is the two chartless install routes below.
Those are generated for the same reason the chart copies are: a bundle nobody
regenerates is a bundle one release behind, and a board one release behind
reads to the operator in front of it as a board that is broken.

## Every board asks Prometheus, not a Prometheus

No board names a datasource and no board names a cluster. Both are template
variables, and both are resolved from what the Grafana and the Prometheus in
front of them actually have.

**`${datasource}`** is on every panel of every board, and its variable filters
Grafana's list by the **plugin id** `prometheus` rather than by name — so a
Prometheus-compatible datasource called `Thanos`, `Mimir` or `metrics-prod` is
offered exactly like one called `Prometheus`, and putting a name regex on that
variable could only exclude datasources that would have worked. Its `current`
is the literal `default`, which is what makes Grafana 12 add the synthetic
*default* option and select it: left empty, the variable falls through to
`options[0]` and `DatasourceSrv.getList` sorts by **name**, with no preference
for the org default. One Prometheus, no difference; two, and an empty `current`
binds every board to whichever is alphabetically first.

**Scope** is the second half. One Grafana commonly watches several Kafka
clusters, so a board that sums across them is a board that lies. Five boards
read Strimzi operands through a PodMonitor carrying
`kafka-common.strimziRelabelings`, and those five carry a real `$cluster`
resolved from `strimzi_io_cluster`; the rest have no Kafka cluster to be scoped
to, and say so rather than inventing one:

| Board | Scoped by | Because |
|---|---|---|
| `kafka-kraft`, `kafka-performance` | `$namespace` + `$cluster` | `strimzi_io_cluster` from `charts/kafka-cluster`'s PodMonitors |
| `kafka-connect` | `$namespace` + `$cluster` | the same relabelings on `charts/connect-cluster`'s PodMonitor |
| `mirror-maker2`, `mirror-maker2-migration` | `$namespace` + `$cluster` | likewise on `charts/mirror-maker2`'s PodMonitor |
| `kates-application` | `$namespace` + `$job` + `$pod` (+ `$db_pod`) | the Kates **application**; `$job` is one release of it, not a Kafka cluster |
| `kates-overview` | `$job` alone | the same application, read only through what it publishes about itself — one release, wherever it runs |
| `kates-benchmark`, `kates-trend` | `$job` (+ `$run_id`, `$test_type`) | one benchmark engine and its runs; nothing here is per-Kafka-cluster |
| `kates-chaos` | `$namespace` + `$pod` | every Kafka panel reads kube-state-metrics and cAdvisor, which carry no Strimzi labels at all — `$pod` *is* the cluster selector |
| `kates-chaos-infra` | `$namespace` (the execution plane's) | one LitmusChaos install per cluster |
| `kyverno-security` | nothing | one admission webhook per cluster, and the board is cluster-wide by construction |

Each board's README says what its variables resolve from and **which scrape
config puts that label on that series**. A variable that resolves empty
silently disables every panel scoped by it — that is how `kates-benchmark` came
to be entirely non-functional before this refactor — so the answer is written
down rather than assumed.

One line of that table earned itself. `kates-chaos`'s `$namespace` used to
resolve through `kube_pod_status_ready`, which kube-state-metrics publishes for
every pod in every namespace it watches — so the picker offered
`cert-manager`, `ingress-nginx`, `kube-system` and the rest, Grafana selected
whichever sorted first, and the board opened scoped to a namespace that has
never run a benchmark. Every panel then read *No data*, correctly, about the
wrong namespace, and nothing on screen could tell the reader that. It is
scoped to namespaces holding a `ChaosEngine` now, and an empty picker means
kube-state-metrics is running without custom-resource metrics — which the
variable's own tooltip says, because an empty variable is the one place that
can explain an empty board.

## Zeros that mean *measured*, and zeros that mean *absent*

A stat panel has two ways to show nothing, and they are not interchangeable.
*No data* means the query returned no series. A **0** means the query returned
zero. The second is a measurement and the first is an admission, and
`<expr> or vector(0)` converts every one of the first into one of the second —
including the ones nobody measured.

That is not a hypothetical. On a cluster whose PodMonitors Prometheus never
selected (the defect `charts/monitoring/values.yaml` fixes — see [Where the
boards are delivered](#where-the-boards-are-delivered)) the KRaft board drew
*Unreachable voters* **0**, *Fenced brokers* **0** and *Metadata errors* **0**,
in green, on thresholds that paint anything above zero red — while *Quorum
epoch* and *Metadata lag* beside them read *No data*, because those two never
had the fallback. Three tiles asserting health, three admitting ignorance, one
cluster, no scrape. The board was more dangerous than a blank one: a blank
board reads as broken, and a green board reads as fine.

So the fallback is **anchored**. `panels.or_zero(expr, anchor)` emits

```promql
(<expr>) or (sum(<anchor>) * 0)
```

where `anchor` is a series that exists for as long as the target is scraped,
whatever its value — each board names one at the top as `ANCHOR`. `sum()` over
an absent series is empty, so the fallback yields 0 while the scrape is live
and *nothing at all* when it is not, and the panel falls through to *No data*
instead of to a green zero. Three worlds, three answers:

| | bare `or vector(0)` | `or_zero(…)` |
|---|---|---|
| counter present | the value | the value |
| counter absent, target scraped | **0** | **0** |
| nothing scraped | **0** ← the bug | *No data* |

`dashboards/test_panels.py` asserts all six cells through `promtool test
rules`, against Prometheus itself rather than against an argument.

### And the colour of *No data*

Dropping the fallback is only half of it. **Grafana paints the *No data*
placeholder with the base threshold step.** A fault panel's base is green, so
it says "No data" in green; *Request handler idle*'s base is red, so it says
"No data" in red. Neither is true — one reads as healthy and the other invents
an alarm, and both are drawn by a panel that measured nothing.

Verified rather than reasoned about. The same board, on the same reachable but
empty Prometheus, before and after:

| Panel | before | after |
|---|---|---|
| Brokers | **0**, green | **0**, red |
| Bytes in /s | "No data", green | "No data", neutral |
| Under-replicated partitions | "No data", green | "No data", neutral |
| Request handler idle | "No data", red | "No data", neutral |

`noValue` is not the fix: it changes the text and leaves the colour alone. A
**special value mapping matching `null`** is the only thing that carries a
colour into that state, so `panels.stat()` and `panels.gauge()` add one to
every panel automatically, in Grafana's named `text` so it follows the theme.
`scripts/check-dashboards.py` fails on a single-value panel without one, or
with one whose colour is a threshold colour; `scripts/grafana-compat/compat.py`
re-checks it in what Grafana hands *back*, because Grafana 8 and 9 rewrite
mappings during schema migration and a mapping that does not survive the
rewrite is a fix that silently only works on new Grafana. It survives on all
five versions tested.

The third state is the one worth keeping in mind while reading the table
above: with the anchor scraped and nothing else, the same header reads
**Brokers 3** in green, **Under-replicated partitions 0** in green — earned,
because the anchor proves the scrape is live — and neutral "No data" on the
four panels that genuinely have nothing to report. Three states, three
renderings, one board.

### Zero is not always the resting state

`Brokers`, `Workers up`, `Pods ready` and `Database pods ready` count things
that must exist. Zero of them is the outage, and all four were green at zero —
while the Strimzi operator's own board painted *Brokers Online* red at zero on
the same cluster at the same moment. They are red at zero now. The panels that
keep a green base are the ones where zero is genuinely the resting state:
*Active runs*, *Connectors*, *Tests completed*.

A **bare** `or vector(0)` is still right where the expression is already
self-anchoring — the series it counts is its own proof of life, and the zero
means "none exist", which is the answer the panel is asking. *Workers up*,
*Brokers*, *Pods ready*, *Active runs*. Those twelve panels are named, with
their reason, in `ZERO_FALLBACK_ALLOWED` in `scripts/check-dashboards.py`,
which fails on any thirteenth — and also fails when an entry outlives the
panel it was written for, so a rename cannot quietly wave the next one
through.

## Checks

```bash
scripts/check-dashboards.py        # layout, descriptions, datasources, uids,
                                   # that METRICS.md documents every series,
                                   # and that no zero is drawn by a bare fallback
scripts/check-metric-contract.sh kafka-cluster   # every series is producible
python3 -m unittest discover -s dashboards -p 'test_*.py'   # install.py, or_zero
```

The second matters most of the static ones. It runs the exporter rules over a
catalogue of JMX beans and fails when a board reads a series no rule can emit.
Eleven such names shipped before it covered dashboards.

The first also holds the zero-fallback gate described in [Zeros that mean
*measured*](#zeros-that-mean-measured-and-zeros-that-mean-absent), and the
fourth proves through `promtool` that the anchored form behaves as that
section claims — the one check here whose subject is not a board at all but
the helper every board's zero goes through.

A third runs only in CI, in `ci-kafka-charts.yml`'s `Dashboards` job: every
`targets[].expr` on every board is wrapped as a recording rule and put through
`promtool check rules`, with the pinned Prometheus' own parser. A panel's
PromQL is no less able to be wrong than an alert's, and a stray bracket
renders as an empty panel — exactly what an idle cluster renders as.
`$namespace` parses inside a label value and a `$` anywhere else does not, so
the extractor substitutes a placeholder for declared variables inside label
values only and **fails on any `$` that survives**, which is what catches a
variable used in a duration rather than skipping the query.

### The one that needs data

All three above are static, and a board can pass all three and still draw
nothing: the name is right, the query parses, and the panel filters on a label
the series does not carry. That is not a hypothetical — it is the shape of
every dashboard bug this refactor repaired. `{{zone}}` rendered as `()` for
years because the relabeling never set `zone`; `topic=""` matches nothing
against a bean the exporter registers twice.

```bash
make check-dashboards-live PROMETHEUS_URL=http://localhost:9090
scripts/check-dashboards-live.py --scrape /tmp/scrape/broker.txt   # or captures
```

It evaluates each query's **instant-vector selectors**, not the whole
expression — deliberately. A static capture makes `rate()` zero and
`histogram_quantile()` NaN, so judging a full query would report a dozen
failures on perfectly healthy boards, and a check that does that is a check
people turn off. A selector, by contrast, is decidable from one capture and is
exactly where this class of bug lives.

A selector whose metric name is absent is **skipped** — nothing publishes it
here, which is the metric contract's business. A selector whose name is
present and which still matches nothing is a **failure**, as is a `by` label
that is on none of the series it groups, and a template variable that resolves
to nothing. CI runs it in the weekly `metrics-live` job, over that job's own
captures from a real cluster.

### Which Grafana versions these work on

| Version | Loads all 12 | Every panel renders | schemaVersion after load |
|---|---|---|---|
| 8.5.27 | yes | yes | 39 (unmigrated) |
| 9.5.21 | yes | yes | 39 |
| 10.4.19 | yes | yes | 39 |
| 11.6.6 | yes | yes | 39 |
| 12.3.1 (what `charts/monitoring` installs) | yes | yes | 39 |

Verified, not assumed — `scripts/check-grafana-compat.sh` starts a real Grafana
of each version, provisions all twelve boards into it, points it at a
Prometheus holding series built from the boards' own matchers, and drives a
headless Chromium over every board:

```bash
make check-grafana-compat                                   # the pinned version
make check-grafana-compat GRAFANA_VERSIONS="9.5.21 12.3.1"
```

It checks both halves because neither is sufficient. Over the API: every board
present, every panel by title, every template variable, and **the datasource
variable's `current` intact** — a schema migration dropping that is precisely
what made every board pick its datasource by name-sort. In the browser: every
panel drawn, no `Panel plugin not found`, no panel errors, no console errors.
A board can pass the first and fail the second, and the reverse.

All twelve boards sit at `schemaVersion` 39 and use only `timeseries`, `stat`,
`table`, `text`, `barchart` and `piechart` — panel types that have been core
since Grafana 8. No version tested migrates the schema, so what you see is what
the file says. CI runs 9.5 through 12.3 on every change to `dashboards/`; 8.5
is tested and works but is not in CI, because it is a floor rather than a
recommendation.

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
6. Give its `manifest.yaml` the `charts/monitoring` target every other board
   has, and add it to `METRICS.md` — its totals, its producer table, and a
   row for each series it is the first to read.

## Where the boards are delivered

One place: `charts/monitoring` ships all twelve, in one ConfigMap behind one
value (`dashboards.enabled`). The generator writes each board's copy into
`charts/monitoring/dashboards/`, because Helm cannot read a file outside its
own chart directory, and `gen-dashboards.py --check` fails CI when a copy
drifts.

The workload charts used to deliver their own boards — `kates-overview` and
`kyverno-security` with `charts/kates`, `kates-chaos-infra` with
`charts/kates-chaos`, `kafka-connect` with `charts/connect-cluster`, the two
MirrorMaker 2 boards with `charts/mirror-maker2`, the last two rewritten per
release. The boards' template variables (`$job`, `$namespace`/`$cluster`)
already separate releases, so the per-chart copies bought delivery complexity
and nothing else; kates 0.9.0, kates-chaos 2.2.0, connect-cluster 2.1.0 and
mirror-maker2 0.11.0 removed them and REFUSE their old dashboard values with
this location named.

Two boards keep their extra context. `kates-overview` reads **nothing but
series the application publishes about itself** — no kube-state-metrics, no
cAdvisor, no exporter — so it fills with just `charts/kates` scraped and a
Grafana. `kates-chaos-infra` watches the LitmusChaos execution plane that
`charts/kates-chaos` installs; it is not `kates-chaos`, which asks what a
fault did to the Kafka cluster and the workload — this one asks whether the
chaos platform is installed and running at all. Until this refactor it was
the only board outside this directory: hand-written JSON inside its chart's
template, with no panel descriptions, no layout gate, and four series or
labels LitmusChaos does not publish.

Broker health, quorum identity, Cruise Control and consumer lag are covered by
the Strimzi operator's own dashboards, which `charts/strimzi-operator` enables
by default and which read the same series this repository's exporter rules
produce. The boards here add what those leave out rather than restating them.

## Reviewing a change to these

`docs/reviewing-the-dashboards-refactor.md` is the map for the change that
created this directory: which 84% of the diff is generated and can be
collapsed, which 105 files a person wrote, and the order to read them in. The
short version is **read `board.py`, not `dashboard.json`** — the JSON is
output, `.gitattributes` marks it `linguist-generated` so GitHub collapses it,
and `gen-dashboards.py --check` proves the two agree.

## Installing these anywhere

Four routes. They install the same twelve boards; what differs is what you
already have.

| Route | Use it when | What it needs |
|---|---|---|
| Helm charts | you run these charts | a cluster, the charts |
| `install.py` | you have a Grafana and a credential | Python 3.8+, network to Grafana |
| `bundle/provisioning/` | Grafana reads provisioning files at boot | a volume mount, a restart |
| `bundle/configmaps.yaml` | you run the Grafana sidecar, not these charts | `kubectl`, a watched namespace |

Everything under `bundle/` is generated. Run `scripts/gen-dashboards.py` after
changing a board; `--check` fails CI when it is stale.

### 1. The Helm charts

One chart ships all twelve, on by default:

```bash
helm upgrade --install monitoring charts/monitoring -n monitoring --create-namespace
```

Every route installs the boards identically — one copy each, under its base
uid, with the dropdowns unpreselected and resolved from the data. (Until
connect-cluster 2.1.0 and mirror-maker2 0.11.0 this route also rewrote those
charts' boards per release; the `$namespace`/`$cluster` variables do that
separating now, one click from the same answer.)

### 2. `install.py` — straight at the Grafana API

No cluster, no sidecar, no restart. Works against a managed Grafana, a Grafana
Cloud stack, a port-forward, or the container on your laptop.

```bash
# every board, into a folder it creates, with the datasource selected
dashboards/install.py --url https://grafana.example.com \
    --token "$GRAFANA_TOKEN" \
    --folder Kates --datasource Prometheus --tag managed

# basic auth, a subset, and a rehearsal first
dashboards/install.py --url http://localhost:3000 --user admin:admin --dry-run
dashboards/install.py --url http://localhost:3000 --user admin:admin \
    kafka-kraft kafka-performance
```

`--url` also reads `$GRAFANA_URL`, the token `$GRAFANA_TOKEN`, and `--user` a
bare username with `$GRAFANA_PASSWORD`. `--list` prints the boards and their
uids without contacting anything. `--insecure` skips TLS verification, for a
Grafana behind a self-signed certificate.

Run it twice and nothing duplicates: every board carries a stable uid and the
push is an overwrite, so the second run updates the same twelve boards and
creates no second folder. What it does record is a new version in Grafana's
own history for each board, with `--message` on it.

A board Grafana rejects is named, with Grafana's reason, and the rest still
install:

```text
  FAILED      kates-trend                uid=xxxxxxxx…
              HTTP 400 from POST /api/dashboards/db: uid too long, max 40 characters
  ok          kates-benchmark            uid=kates-benchmark-overview  version=3
2 of 3 board(s) FAILED: kates-trend, kates-overview
```

**Tested.** `dashboards/test_install.py` — 40 cases, standard library only —
runs it against an `http.server` that answers like Grafana and records what it
was sent, so the assertions are about the REQUESTS as much as the responses:
that `overwrite: true` is on every push (without it a second run fails every
board with a version conflict), that the foreign `id` is stripped (sending one
from another Grafana is how an import lands on top of an unrelated board), that
a basic-auth header is base64 of `user:password`, that `--dry-run` issues no
`POST` at all, and that one rejected board does not stop the other eleven. It
is a real server rather than a mock because the things most likely to be wrong
in an HTTP client are exactly what a response mock removes. Run it with
`make check-install-script`; CI runs it in the `Dashboards` job.

It is Python rather than shell because three of the four things it does are
JSON edits on the board before it is posted and the fourth is reading
Grafana's error body — four hard dependencies on `jq` in bash, and none here:
it imports nothing outside the standard library.

### 3. `bundle/provisioning/` — file provisioning, no sidecar

One directory, one mount. Grafana reads the provider file at boot and loads
every board in `kates/` beside it.

```bash
docker run -d --name grafana -p 3000:3000 \
  -v "$PWD/dashboards/bundle/provisioning:/etc/grafana/provisioning/dashboards:ro" \
  grafana/grafana:12.3.1
```

In Kubernetes the same directory goes in as a ConfigMap or a volume mounted at
`/etc/grafana/provisioning/dashboards`; in the Grafana Helm chart it is
`extraConfigmapMounts`. The provider is `allowUiUpdates: false`, because these
boards are generated and an edit in the UI is lost at the next restart either
way — better for Grafana to refuse the save than to accept it and drop it.

The boards land in the `Kates` folder and open on **the org's default
datasource**, because that is what every board's `${datasource}` variable asks
for — so mark one Prometheus `isDefault: true`. There is no per-install
datasource uid to bake into a static file; `install.py --datasource` is the
route that can name one.

### 4. `bundle/configmaps.yaml` — the sidecar, without these charts

Twelve ConfigMaps, one board each, carrying `grafana_dashboard: "1"` and a
`grafana_folder: Kates` annotation:

```bash
kubectl apply -n monitoring -f dashboards/bundle/configmaps.yaml
```

No `metadata.namespace` is written into the file, so `-n` chooses — and it has
to name a namespace the sidecar searches. One ConfigMap per board rather than
one for all twelve: together they are most of a megabyte and a ConfigMap's
ceiling is 1 MiB, so a single object would be the next board away from
breaking the apply.

### The knobs

Four things decide whether the boards are installed and whether anyone can
find them, all under `charts/monitoring`'s `dashboards` key:

| Knob | What it decides |
|---|---|
| `enabled` | whether the boards are shipped at all |
| `label` / `labelValue` | the label the Grafana sidecar watches |
| `namespace` | which namespace the ConfigMap goes in |
| `folder` | the Grafana folder, via the `grafana_folder` annotation |

`label`/`labelValue` are scalars and are always applied. The `labels` map is
merged on top of them, never in place of them: a map in values.yaml that
drops `grafana_dashboard` takes every board with it, and the symptom is
boards that never appear — no error from Helm, none from the sidecar, nothing
in the ConfigMap to look wrong.

**The namespace rarely bites any more.** The chart puts the ConfigMap beside
the Grafana it installs, and pins
`kube-prometheus-stack.grafana.sidecar.dashboards.searchNamespace: ALL` —
the value carries the trade-off and the namespace-list alternative. A Grafana
installed some other way may be watching
only its own namespace; point `namespace` at it when it is.

The datasource is not a chart knob. Every board reads the `${datasource}`
template variable and ships asking for `default`, so it opens on the org's
default datasource — right when there is one Prometheus, wrong whenever the
Prometheus these boards want is not the org default. Make the right one the
default, or use `install.py --datasource` to name it into each board.

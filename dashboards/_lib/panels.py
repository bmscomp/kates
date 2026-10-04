"""Panel constructors.

Every board in `dashboards/` is built from these rather than from hand-written
JSON, so that a panel cannot be created without a description and cannot pick
up a datasource by accident. `scripts/check-dashboards.py` enforces both, but
it is better to make the mistake impossible than to catch it.

The datasource is always the `${datasource}` template variable. Three
conventions existed before this refactor — the string "Prometheus", the string
"prometheus", and no datasource at all, which fell through to whatever
Grafana's default happened to be. A board that names a datasource by string
breaks on any deployment that named its own differently.
"""

from __future__ import annotations

from typing import Any


DATASOURCE = {"type": "prometheus", "uid": "${datasource}"}


def target(expr: str, legend: str = "", instant: bool = False, ref: str = "A",
           fmt: str = "") -> dict[str, Any]:
    """One query. `legend` is Grafana's legendFormat, `fmt` its format."""
    t: dict[str, Any] = {
        "datasource": DATASOURCE,
        "expr": expr,
        "refId": ref,
        "editorMode": "code",
    }
    if legend:
        t["legendFormat"] = legend
    if fmt:
        t["format"] = fmt
    if instant:
        t["instant"] = True
        t["range"] = False
    else:
        t["range"] = True
    return t


def or_zero(expr: str, anchor: str) -> str:
    """`expr`, falling back to zero only while `anchor` proves a live scrape.

    WHY THIS EXISTS. `<expr> or vector(0)` is the usual way to make a counter
    that does not exist yet draw a 0 rather than "No data", and on a panel
    counting *faults* it is a lie waiting to happen: `vector(0)` cannot tell
    "we are scraping this and nothing is wrong" apart from "we are scraping
    nothing at all". A cluster whose PodMonitors are not selected by Prometheus
    — the exact defect this branch fixes in charts/monitoring/values.yaml —
    renders every such panel as a green zero. Under-replicated partitions: 0.
    Fenced brokers: 0. Failed tasks: 0. All of them green, all of them
    measuring nothing, sitting beside honest panels that say "No data".

    `anchor` is a series that exists for as long as the target is scraped at
    all, whatever its value — each board names one at the top as ANCHOR. Wrap
    it in the board's own selector and the fallback can only fire when that
    selector is matching something:

        or_zero('sum(kafka_..._fencedbrokercount{%s})' % SEL,
                '%s{%s}' % (ANCHOR, SEL))

    `sum()` over an absent series is empty, so `sum(anchor) * 0` is 0 when the
    scrape is live and nothing at all when it is not — which is what makes the
    whole expression fall through to "No data" instead of to a green zero.

    A bare `or vector(0)` is still right on a panel that counts *things
    present* rather than things wrong — "Workers up", "Brokers", "Pods ready".
    There, zero is the answer you want when none exist, and the series being
    counted is its own anchor. scripts/check-dashboards.py holds the list and
    fails on any new one.
    """
    return "(%s) or (sum(%s) * 0)" % (expr, anchor)


def targets(*specs: Any) -> list[dict[str, Any]]:
    """Several queries, lettered A, B, C… in order.

    Each spec is an expression, or a `(expr, legend)` pair.
    """
    out = []
    for i, spec in enumerate(specs):
        ref = chr(ord("A") + i)
        if isinstance(spec, (tuple, list)):
            expr, legend = (list(spec) + [""])[:2]
            out.append(target(expr, legend, ref=ref))
        else:
            out.append(target(spec, ref=ref))
    return out


def _base(title: str, description: str, kind: str, w: int, h: int) -> dict[str, Any]:
    if not description or not description.strip():
        raise ValueError(
            "panel %r has no description: every panel must say what it means "
            "and what to do when it moves" % title
        )
    return {
        "title": title,
        "description": description.strip(),
        "type": kind,
        "datasource": DATASOURCE,
        "w": w,
        "h": h,
    }


def _field_config(unit: str, thresholds: list[tuple[str, float | None]] | None,
                  extra_defaults: dict[str, Any] | None = None, *,
                  color: str | None = None,
                  overrides: list[dict[str, Any]] | None = None) -> dict[str, Any]:
    defaults: dict[str, Any] = {"unit": unit} if unit else {}
    if thresholds:
        defaults["thresholds"] = {
            "mode": "absolute",
            "steps": [{"color": c, "value": v} for c, v in thresholds],
        }
        defaults["color"] = {"mode": "thresholds"}
    if color:
        defaults["color"] = {"mode": color}
    if extra_defaults:
        defaults.update(extra_defaults)
    return {"defaults": defaults, "overrides": overrides or []}


def _scalar_defaults(min_value: float | None, max_value: float | None,
                     decimals: int | None,
                     mappings: list[dict[str, Any]] | None) -> dict[str, Any]:
    """The `fieldConfig.defaults` keys a single-value panel may also carry."""
    extra: dict[str, Any] = {}
    if min_value is not None:
        extra["min"] = min_value
    if max_value is not None:
        extra["max"] = max_value
    if decimals is not None:
        extra["decimals"] = decimals
    if mappings:
        extra["mappings"] = mappings
    return extra


def no_data_mapping(index: int = 0) -> dict[str, Any]:
    """Paint the *No data* placeholder neutral instead of by the thresholds.

    GRAFANA COLOURS "No data" WITH THE BASE THRESHOLD STEP. This is not
    documented anywhere prominent and it is the second half of the bug that
    `or_zero` fixes: drop a fault panel's zero fallback and the tile stops
    claiming a measured zero, but the words *No data* are still painted green,
    and green is the entire message an operator takes from a wall of tiles.
    The same rule paints an absent value red on a panel whose base step is red,
    which invents an alarm out of a missing scrape.

    Verified in a real Grafana 12.3.1 against a Prometheus holding nothing, on
    this repository's own kafka-performance board: *Under-replicated
    partitions* rendered "No data" in green, *Request handler idle* rendered
    "No data" in red, side by side, neither measuring anything. The same is
    visible on the Strimzi operator's boards, which set `noValue: "N/A"` — the
    N/A is green on *Under Replicated Partitions* and red on *Brokers Online*.

    `noValue` alone does NOT fix it: it changes the text and leaves the colour
    exactly as it was. A **special value mapping matching `null`** is what
    carries a colour into that state, so that is what every single-value panel
    here gets, automatically, rather than by remembering.

    The colour is Grafana's named `text` rather than a hex grey so it follows
    the theme — these boards ship to Grafanas whose theme nobody here chooses.
    Against the thresholds it has to be told apart from, neutral is the point:
    not green, not red, not a judgement.
    """
    return {"type": "special", "options": {"match": "null", "result": {
        "text": "No data", "color": "text", "index": index}}}


def _with_no_data(mappings: list[dict[str, Any]] | None) -> list[dict[str, Any]]:
    """Append the neutral no-data mapping after a panel's own mappings.

    Appended rather than prepended so a panel's DRAINED/STOPPED mappings keep
    the indices they were written with. `null` matches no numeric value, so
    the order carries no meaning beyond that.
    """
    existing = list(mappings or [])
    return existing + [no_data_mapping(len(existing))]


def stat(title: str, description: str, queries: list[dict[str, Any]], *,
         unit: str = "", thresholds: list[tuple[str, float | None]] | None = None,
         w: int = 4, h: int = 4, text_mode: str = "auto",
         mappings: list[dict[str, Any]] | None = None,
         min_value: float | None = None, max_value: float | None = None,
         decimals: int | None = None, graph_mode: str = "area",
         color_mode: str = "value") -> dict[str, Any]:
    """A single current value. Use for things an operator checks at a glance.

    `mappings` turns the number into a word — DRAINED, STOPPED — which is the
    only honest way to render a `bool` expression, and `color_mode` paints the
    whole tile with it.
    """
    p = _base(title, description, "stat", w, h)
    p["fieldConfig"] = _field_config(
        unit, thresholds,
        _scalar_defaults(min_value, max_value, decimals,
                         _with_no_data(mappings)))
    p["options"] = {
        "reduceOptions": {"calcs": ["lastNotNull"], "fields": "", "values": False},
        "textMode": text_mode,
        "colorMode": color_mode,
        "graphMode": graph_mode,
        "justifyMode": "auto",
    }
    p["targets"] = queries
    return p


def timeseries(title: str, description: str, queries: list[dict[str, Any]], *,
               unit: str = "", w: int = 12, h: int = 8, stack: bool = False,
               thresholds: list[tuple[str, float | None]] | None = None,
               legend_mode: str = "list", fill: int = 10,
               min_value: float | None = None,
               max_value: float | None = None,
               decimals: int | None = None,
               threshold_style: str = "",
               color: str | None = None) -> dict[str, Any]:
    """A value over time. The default panel for anything with a trend.

    `threshold_style="line"` draws the threshold on the plot rather than
    colouring the series by it — which is what a panel whose thresholds come
    from an alert's own numbers wants: the line IS the alert.
    """
    p = _base(title, description, "timeseries", w, h)
    custom: dict[str, Any] = {
        "drawStyle": "line",
        "lineWidth": 1,
        "fillOpacity": fill,
        "showPoints": "never",
        "spanNulls": False,
    }
    if stack:
        custom["stacking"] = {"mode": "normal", "group": "A"}
    if threshold_style:
        custom["thresholdsStyle"] = {"mode": threshold_style}
    extra: dict[str, Any] = {"custom": custom}
    extra.update(_scalar_defaults(min_value, max_value, decimals, None))
    p["fieldConfig"] = _field_config(unit, thresholds, extra, color=color)
    p["options"] = {
        "legend": {"displayMode": legend_mode, "placement": "bottom", "showLegend": True,
                   "calcs": []},
        "tooltip": {"mode": "multi", "sort": "desc"},
    }
    p["targets"] = queries
    return p


def gauge(title: str, description: str, queries: list[dict[str, Any]], *,
          unit: str = "", thresholds: list[tuple[str, float | None]] | None = None,
          w: int = 4, h: int = 5, min_value: float = 0,
          max_value: float = 1) -> dict[str, Any]:
    """A bounded value against its limits — a ratio, a percentage, a budget."""
    p = _base(title, description, "gauge", w, h)
    p["fieldConfig"] = _field_config(
        unit, thresholds,
        {"min": min_value, "max": max_value, "mappings": _with_no_data(None)})
    p["options"] = {
        "reduceOptions": {"calcs": ["lastNotNull"], "fields": "", "values": False},
        "showThresholdLabels": False,
        "showThresholdMarkers": True,
    }
    p["targets"] = queries
    return p


def table(title: str, description: str, queries: list[dict[str, Any]], *,
          w: int = 12, h: int = 8,
          transformations: list[dict[str, Any]] | None = None,
          overrides: list[dict[str, Any]] | None = None) -> dict[str, Any]:
    """An instant snapshot across a label set — which topic, which partition."""
    p = _base(title, description, "table", w, h)
    p["fieldConfig"] = {"defaults": {"custom": {"align": "auto"}},
                        "overrides": overrides or []}
    p["options"] = {"showHeader": True, "footer": {"show": False, "reducer": ["sum"]}}
    p["targets"] = queries
    p["transformations"] = transformations or [
        {"id": "labelsToFields", "options": {}},
        {"id": "organize", "options": {"excludeByName": {"Time": True}}},
    ]
    return p


def piechart(title: str, description: str, queries: list[dict[str, Any]], *,
             w: int = 8, h: int = 8, color: str | None = None,
             overrides: list[dict[str, Any]] | None = None) -> dict[str, Any]:
    """A breakdown of a whole. Used sparingly — a table is usually clearer."""
    p = _base(title, description, "piechart", w, h)
    p["fieldConfig"] = _field_config("", None, color=color, overrides=overrides)
    p["options"] = {
        "legend": {"displayMode": "list", "placement": "right", "showLegend": True},
        "pieType": "donut",
        "reduceOptions": {"calcs": ["lastNotNull"], "fields": "", "values": False},
    }
    p["targets"] = queries
    return p


def barchart(title: str, description: str, queries: list[dict[str, Any]], *,
             unit: str = "", w: int = 12, h: int = 8, horizontal: bool = True,
             fill: int = 80, color: str | None = None) -> dict[str, Any]:
    """A ranking across a label set at one instant — which rule, which policy.

    Horizontal by default: the categories are names, and a name is easier to
    read along the axis than under it.
    """
    p = _base(title, description, "barchart", w, h)
    p["fieldConfig"] = _field_config(unit, None, {"custom": {"fillOpacity": fill}},
                                     color=color)
    p["options"] = {
        "orientation": "horizontal" if horizontal else "vertical",
        "legend": {"displayMode": "list", "placement": "bottom", "showLegend": True},
        "showValue": "auto",
    }
    p["targets"] = queries
    return p


def text(title: str, description: str, content: str, *,
         w: int = 24, h: int = 3) -> dict[str, Any]:
    """Prose on the board itself — what this section is for, in one paragraph."""
    p = _base(title, description, "text", w, h)
    p["options"] = {"mode": "markdown", "content": content}
    p["targets"] = []
    return p


# ── Template variables ────────────────────────────────────────────────────

def datasource_var() -> dict[str, Any]:
    """The datasource picker every board carries, named `datasource`.

    `query` is the PLUGIN id, not a name: Grafana filters the options with
    `getDataSourceSrv().getList({pluginId})`, so a Prometheus-compatible
    datasource called `Thanos`, `Mimir` or `metrics-prod` is offered exactly
    like one called `Prometheus`. That is why there is no `regex` here — a
    regex on this variable filters by NAME, and the only thing it could
    usefully do is exclude datasources that would work.

    `current` is the interesting field. Grafana 12 builds the variable with

        defaultOptionEnabled: current.value === 'default' && current.text === 'default'

    (grafana v12.3.1, public/app/features/dashboard-scene/utils/variables.ts),
    and `DataSourceVariable.getValueOptions` only prepends the synthetic
    `default` option when that flag is set. With `current: {}` — what these
    boards shipped — no option matches, `MultiValueVariable.getDefaultSingleState`
    falls through to `options[0]`, and `DatasourceSrv.getList` sorts by NAME,
    case-insensitively, with no preference for the default. So a board landed
    on the alphabetically first Prometheus datasource in the org, which on any
    Grafana with more than one is not the org default and not necessarily the
    right cluster's.

    Asking for `default` fixes exactly that: with one datasource nothing
    changes, with several the board opens on the org default, and if the org
    default is not a Prometheus the `default` option is absent and the old
    first-alphabetically fallback still applies. The picker stays visible, so
    an operator can still choose.
    """
    return {
        "name": "datasource",
        "label": "Data source",
        "type": "datasource",
        "query": "prometheus",
        "current": {"text": "default", "value": "default"},
        "hide": 0,
        "refresh": 1,
    }


def constant_var(name: str, value: str, *, label: str = "", hide: int = 2) -> dict[str, Any]:
    """A value the RELEASE knows and the board cannot ask Prometheus for.

    The namespace a release runs in, the regex that matches its pods, the
    name its recording rules label as `cluster`: none of these are derivable
    from the data, and baking them into an expression is what made the boards
    this directory replaces per-release JSON. They are variables instead, and
    the chart injects the value into the generated file (see each board's
    README). Hidden by default — an operator has nothing to choose here.
    """
    return {
        "name": name,
        "label": label or name,
        "type": "constant",
        "query": value,
        "current": {"text": value, "value": value},
        "hide": hide,
    }


def custom_var(name: str, values: list[str], *, label: str = "",
               multi: bool = True, hide: int = 0) -> dict[str, Any]:
    """A fixed list of values, every one of them selected.

    What a `repeat` needs when the list comes from the chart's values rather
    than from a label: one row per mirror, whatever the aliases are.
    """
    return {
        "name": name,
        "label": label or name,
        "type": "custom",
        "query": ",".join(values),
        "options": [{"text": v, "value": v, "selected": True} for v in values],
        "current": {"text": list(values), "value": list(values), "selected": True},
        "multi": multi,
        "includeAll": False,
        "hide": hide,
        "refresh": 0,
    }


def query_var(name: str, query: str, *, label: str = "", multi: bool = False,
              include_all: bool = False, all_value: str | None = None,
              hide: int = 0, regex: str = "", description: str = "") -> dict[str, Any]:
    """A variable whose values come from the data itself.

    `description` is Grafana's own tooltip on the picker. It is worth filling
    in wherever an *empty* picker is itself a diagnosis — the reader is
    standing in front of a board with nothing on it, and the variable that
    resolved to nothing is the one place that can say why. Omitted from the
    JSON when empty, so a board that does not set it is byte-identical.
    """
    v: dict[str, Any] = {
        "name": name,
        "label": label or name,
        "type": "query",
        "datasource": DATASOURCE,
        "query": {"query": query, "refId": "%s-variable-query" % name},
        "definition": query,
        "refresh": 2,
        "multi": multi,
        "includeAll": include_all,
        "current": {},
        "options": [],
        "sort": 1,
        "hide": hide,
    }
    if all_value:
        v["allValue"] = all_value
    if regex:
        v["regex"] = regex
    if description:
        v["description"] = description
    return v

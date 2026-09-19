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


def target(expr: str, legend: str = "", instant: bool = False, ref: str = "A") -> dict[str, Any]:
    """One query. `legend` is Grafana's legendFormat."""
    t: dict[str, Any] = {
        "datasource": DATASOURCE,
        "expr": expr,
        "refId": ref,
        "editorMode": "code",
    }
    if legend:
        t["legendFormat"] = legend
    if instant:
        t["instant"] = True
        t["range"] = False
    else:
        t["range"] = True
    return t


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
                  extra_defaults: dict[str, Any] | None = None) -> dict[str, Any]:
    defaults: dict[str, Any] = {"unit": unit} if unit else {}
    if thresholds:
        defaults["thresholds"] = {
            "mode": "absolute",
            "steps": [{"color": c, "value": v} for c, v in thresholds],
        }
        defaults["color"] = {"mode": "thresholds"}
    if extra_defaults:
        defaults.update(extra_defaults)
    return {"defaults": defaults, "overrides": []}


def stat(title: str, description: str, queries: list[dict[str, Any]], *,
         unit: str = "", thresholds: list[tuple[str, float | None]] | None = None,
         w: int = 4, h: int = 4, text_mode: str = "auto",
         mappings: list[dict[str, Any]] | None = None) -> dict[str, Any]:
    """A single current value. Use for things an operator checks at a glance."""
    p = _base(title, description, "stat", w, h)
    p["fieldConfig"] = _field_config(unit, thresholds,
                                     {"mappings": mappings} if mappings else None)
    p["options"] = {
        "reduceOptions": {"calcs": ["lastNotNull"], "fields": "", "values": False},
        "textMode": text_mode,
        "colorMode": "value",
        "graphMode": "area",
        "justifyMode": "auto",
    }
    p["targets"] = queries
    return p


def timeseries(title: str, description: str, queries: list[dict[str, Any]], *,
               unit: str = "", w: int = 12, h: int = 8, stack: bool = False,
               thresholds: list[tuple[str, float | None]] | None = None,
               legend_mode: str = "list", fill: int = 10,
               min_value: float | None = None,
               max_value: float | None = None) -> dict[str, Any]:
    """A value over time. The default panel for anything with a trend."""
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
    extra: dict[str, Any] = {"custom": custom}
    if min_value is not None:
        extra["min"] = min_value
    if max_value is not None:
        extra["max"] = max_value
    p["fieldConfig"] = _field_config(unit, thresholds, extra)
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
    p["fieldConfig"] = _field_config(unit, thresholds,
                                     {"min": min_value, "max": max_value})
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
             w: int = 8, h: int = 8) -> dict[str, Any]:
    """A breakdown of a whole. Used sparingly — a table is usually clearer."""
    p = _base(title, description, "piechart", w, h)
    p["fieldConfig"] = {"defaults": {}, "overrides": []}
    p["options"] = {
        "legend": {"displayMode": "list", "placement": "right", "showLegend": True},
        "pieType": "donut",
        "reduceOptions": {"calcs": ["lastNotNull"], "fields": "", "values": False},
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
    """The datasource picker every board carries, named `datasource`."""
    return {
        "name": "datasource",
        "label": "Data source",
        "type": "datasource",
        "query": "prometheus",
        "current": {},
        "hide": 0,
        "refresh": 1,
    }


def query_var(name: str, query: str, *, label: str = "", multi: bool = False,
              include_all: bool = False, all_value: str | None = None,
              hide: int = 0, regex: str = "") -> dict[str, Any]:
    """A variable whose values come from the data itself."""
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
    return v

#!/usr/bin/env python3
"""Structural checks over every board in `dashboards/`.

This is the mirror-maker2 layout gate, generalised. That gate was the
strictest thing in the repository and it covered one chart; the boards it did
not cover are the ones that shipped panels with no description, overlapping
rectangles and eleven metric names that never existed.

What it rejects:

  * a panel with an incomplete gridPos, or one that runs past column 24
  * two panels in the same container whose rectangles overlap
  * a non-row panel with no description — a panel nobody can read during an
    incident is not a panel
  * a non-row panel with no query, or a query with an empty expression: a lost
    expression renders "No data" exactly as an idle cluster does
  * a row that is not a full-width 1-high header, or one that is not below the
    row before it
  * a datasource named by string rather than by the `${datasource}` variable —
    three spellings existed before this refactor and a board that names one
    breaks on any deployment that named its own differently
  * two boards sharing a uid
  * a panel whose zero is drawn by a bare `or vector(0)` — a fallback that
    fires just as readily when nothing is being scraped, which turns a dead
    Prometheus into a wall of green zeros. See ZERO_FALLBACK_ALLOWED
  * a single-value panel with no `null` value mapping, which leaves Grafana
    painting its *No data* with the base threshold colour — green where the
    base is green, red where it is red, neither of them true

Run over `dashboards/` by default, or over a rendered chart with `--render`,
which reads a `helm template` stream on stdin and checks every dashboard
ConfigMap in it.
"""

from __future__ import annotations

import argparse
import re
import json
import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent
DASHBOARDS = ROOT / "dashboards"


def err(where: str, message: str, problems: list[str]) -> None:
    problems.append("%s: %s" % (where, message))


def walk(panels: list[dict]):
    for p in panels:
        yield p
        yield from walk(p.get("panels") or [])


def overlaps(a: dict, b: dict) -> bool:
    ga, gb = a["gridPos"], b["gridPos"]
    return (ga["x"] < gb["x"] + gb["w"] and gb["x"] < ga["x"] + ga["w"]
            and ga["y"] < gb["y"] + gb["h"] and gb["y"] < ga["y"] + ga["h"])


def check_container(panels: list[dict], where: str, problems: list[str]) -> None:
    placed = []
    for p in panels:
        title = p.get("title", "?")
        g = p.get("gridPos") or {}
        if any(k not in g for k in ("x", "y", "w", "h")):
            err(where, "panel %r has an incomplete gridPos" % title, problems)
            continue
        if g["x"] + g["w"] > 24:
            err(where, "panel %r runs past column 24 (x=%d w=%d)" % (title, g["x"], g["w"]),
                problems)
        placed.append(p)

    for i in range(len(placed)):
        for j in range(i + 1, len(placed)):
            if overlaps(placed[i], placed[j]):
                err(where, "%r and %r overlap" % (placed[i].get("title"), placed[j].get("title")),
                    problems)

    for p in panels:
        if p.get("type") == "row":
            continue
        title = p.get("title", "?")
        if not (p.get("description") or "").strip():
            err(where, "panel %r has no description" % title, problems)
        if p.get("type") == "text":
            continue
        targets = p.get("targets") or []
        if not targets:
            err(where, "panel %r has no query" % title, problems)
        for q in targets:
            if not (q.get("expr") or "").strip():
                err(where, "panel %r has a target with no expr" % title, problems)


def check_datasources(dash: dict, where: str, problems: list[str]) -> None:
    for p in walk(dash.get("panels") or []):
        for holder in [p] + list(p.get("targets") or []):
            ds = holder.get("datasource")
            if ds is None:
                continue
            if isinstance(ds, str):
                err(where, "panel %r names its datasource by string (%r); use the "
                           "${datasource} variable" % (p.get("title", "?"), ds), problems)
            elif isinstance(ds, dict) and ds.get("uid") not in ("${datasource}", None):
                err(where, "panel %r pins datasource uid %r" % (p.get("title", "?"),
                                                                ds.get("uid")), problems)


def check_dashboard(dash: dict, where: str, problems: list[str]) -> int:
    panels = dash.get("panels") or []
    if not panels:
        err(where, "the board has no panels", problems)
        return 0

    check_container(panels, where, problems)
    check_datasources(dash, where, problems)

    nested = 0
    last_y = -1
    for p in panels:
        if p.get("type") != "row":
            continue
        g = p.get("gridPos") or {}
        title = p.get("title", "?")
        if (g.get("h"), g.get("w"), g.get("x")) != (1, 24, 0):
            err(where, "row %r is not a full-width 1-high header" % title, problems)
        if g.get("y", -1) <= last_y:
            err(where, "row %r at y=%s is not below the row before it" % (title, g.get("y")),
                problems)
        last_y = g.get("y", last_y)
        if p.get("panels"):
            nested += len(p["panels"])
            check_container(p["panels"], "%s row %r" % (where, title), problems)

    return len(panels) + nested


def from_dashboards_dir(problems: list[str]) -> int:
    if not DASHBOARDS.is_dir():
        sys.exit("no dashboards/ directory at %s" % DASHBOARDS)
    uids: dict[str, str] = {}
    checked = 0
    for path in sorted(DASHBOARDS.glob("*/dashboard.json")):
        where = str(path.relative_to(ROOT))
        try:
            dash = json.loads(path.read_text())
        except json.JSONDecodeError as exc:
            err(where, "is not valid JSON: %s" % exc, problems)
            continue
        total = check_dashboard(dash, where, problems)
        uid = dash.get("uid")
        if not uid:
            err(where, "has no uid", problems)
        elif uid in uids:
            err(where, "shares uid %r with %s" % (uid, uids[uid]), problems)
        else:
            uids[uid] = where
        print("  %-52s %3d panels  uid=%s" % (path.parent.name, total, uid))
        checked += 1
    if not checked:
        sys.exit("dashboards/ has no dashboard.json to check")
    return checked


def from_render(problems: list[str]) -> int:
    import yaml
    checked = 0
    for doc in yaml.safe_load_all(sys.stdin):
        if not doc or doc.get("kind") != "ConfigMap":
            continue
        for name, body in (doc.get("data") or {}).items():
            if not name.endswith(".json"):
                continue
            try:
                dash = json.loads(body)
            except json.JSONDecodeError as exc:
                err(name, "is not valid JSON: %s" % exc, problems)
                continue
            total = check_dashboard(dash, name, problems)
            print("  %-52s %3d panels  uid=%s" % (name, total, dash.get("uid")))
            checked += 1
    if not checked:
        sys.exit("no dashboard ConfigMap in this render")
    return checked



# ── Every series a board reads is documented ───────────────────────────────

METRICS_MD = ROOT / "dashboards" / "METRICS.md"

# PromQL keywords and functions, which the name scanner would otherwise take
# for series. Same list the metric contract uses.
_PROMQL_KEYWORDS = set("""
sum rate irate avg max min count by without on group_left group_right increase
delta idelta histogram_quantile label_replace label_join topk bottomk quantile
stddev stdvar absent absent_over_time changes clamp clamp_max clamp_min ceil
floor round scalar vector time timestamp predict_linear deriv holt_winters
avg_over_time max_over_time min_over_time sum_over_time count_over_time
quantile_over_time stddev_over_time last_over_time present_over_time and or
unless bool offset ignoring resets sort sort_desc exp ln log2 log10 sqrt abs
sgn pi day_of_week hour group nan inf
""".split())

_IDENT = re.compile(r"\b([a-zA-Z_:][a-zA-Z0-9_:]*)\b")


def series_in(expr: str) -> set:
    """Every metric name an expression reads.

    The same extraction scripts/metric-contract/contract.py does, and for the
    same reason: a label name in `by (topic, partition)` or the second argument
    of `label_values` is not a series, and treating one as a series turns this
    check into eighteen false reports.
    """
    s = expr
    s = re.sub(r'"(?:\\.|[^"\\])*"', '""', s)                    # string literals
    s = re.sub(r"'(?:\\.|[^'\\])*'", "''", s)
    s = re.sub(r"\{[^}]*\}", "", s)                               # label matchers
    # label_values(<selector>, <label>): the label is not a series. After the
    # matchers are gone, not before — a selector like
    # `up{namespace="$ns", job="$job"}` carries commas of its own, so a
    # `([^,]*)` capture applied first stops inside the braces and matches
    # nothing, leaving the label to be counted as a series.
    s = re.sub(r"label_values\s*\(([^,()]*),\s*[A-Za-z_][A-Za-z0-9_]*\s*\)", r"\1", s)
    s = re.sub(r"\[[^\]]*\]", "", s)                              # ranges, subqueries
    s = re.sub(r"\$\{[^}]*\}|\$[A-Za-z_][A-Za-z0-9_]*", "", s)      # Grafana variables
    s = re.sub(r"\b(by|without|on|ignoring|group_left|group_right)\s*\([^)]*\)",
               " ", s)                                           # grouping clauses
    s = re.sub(r"[a-zA-Z_][a-zA-Z0-9_]*\s*\(", " (", s)            # function calls
    out = set()
    for m in _IDENT.finditer(s):
        ident = m.group(1)
        if ident in _PROMQL_KEYWORDS or ident[0].isdigit():
            continue
        out.add(ident)
    return out


def documented_series() -> set:
    """Every series METRICS.md documents, from the first cell of its tables."""
    if not METRICS_MD.is_file():
        return set()
    out = set()
    for line in METRICS_MD.read_text().splitlines():
        if not line.startswith("| `"):
            continue
        first = line.split("|")[1]
        for m in re.finditer(r"`([^`]+)`", first):
            for part in m.group(1).split("/"):
                name = part.strip().split("{")[0].strip()
                if re.fullmatch(r"[a-zA-Z_:][a-zA-Z0-9_:]*", name):
                    out.add(name)
    return out


def check_metrics_documented(problems: list) -> int:
    """Is every series the boards read one METRICS.md describes?

    METRICS.md claimed to document "all 211 series any board reads" and
    nothing checked it, so the number could not be reproduced from the file
    and a board could add a series the reference never mentioned. The point of
    that file is that an empty panel can be read as "nothing is wrong" rather
    than "nothing publishes this" — which only holds while it is complete.
    """
    documented = documented_series()
    if not documented:
        return 0
    read: dict = {}
    for path in sorted(DASHBOARDS.glob("*/dashboard.json")):
        dash = json.loads(path.read_text())
        board = path.parent.name
        for panel in walk(dash.get("panels") or []):
            for target in panel.get("targets") or []:
                for n in series_in(target.get("expr") or ""):
                    read.setdefault(n, set()).add(board)
        for var in (dash.get("templating") or {}).get("list") or []:
            q = var.get("query")
            if isinstance(q, dict):
                q = q.get("query")
            if var.get("type") == "query" and q:
                for n in series_in(str(q)):
                    read.setdefault(n, set()).add(board)
    # Recorded series are defined in the charts and documented in their own
    # section; they carry a colon, which no exporter-published name does.
    undocumented = sorted(n for n in read
                          if ":" not in n and n not in documented)
    for n in undocumented:
        err("dashboards/METRICS.md",
            "does not document %s, read by %s. Every series a board reads has "
            "to be in METRICS.md, or an empty panel cannot be told from an "
            "unpublished one — which is the whole point of that file."
            % (n, ", ".join(sorted(read[n]))), problems)
    print("  %-52s %3d series read, %d documented"
          % ("METRICS.md coverage", len(read), len(documented)))
    return len(read)


# ── The bare zero fallback ─────────────────────────────────────────────────
# `<expr> or vector(0)` makes an absent series draw a 0 instead of "No data".
# On a panel counting faults that is a lie the moment nothing is being
# scraped: a cluster whose PodMonitors Prometheus never selected renders
# "Under-replicated partitions: 0", "Fenced brokers: 0", "Failed tasks: 0" —
# green, confident, and measuring nothing, beside honest panels reading No
# data. That is worse than an empty board, because an empty board reads as
# broken and a green one reads as healthy.
#
# panels.or_zero() is the fix: the same fallback, anchored to a series that
# exists for as long as the target is scraped, so it can only fire while
# something is actually being measured. This gate makes that the default by
# failing on any *bare* `or vector(0)` that is not listed below.
#
# A listing is not a waiver. Each one is a panel whose expression is already
# self-anchoring — the series it counts is its own proof of life, and the zero
# it draws means "none exist", which is the answer the panel is asking for.
ZERO_FALLBACK_ALLOWED = {
    ("kafka-connect", "Workers up"):
        "`up` is Prometheus' own per-target series. No targets means no "
        "workers, which is exactly what the panel is asking.",
    ("kafka-connect", "Connectors"):
        "Counts connectors that exist. Zero is the answer when none do.",
    ("kafka-performance", "Brokers"):
        "count() over the board's own ANCHOR — self-anchoring by construction.",
    ("kates-benchmark", "Active runs"):
        "sum() over the board's own ANCHOR — self-anchoring by construction.",
    ("kates-overview", "Active test runs"):
        "The same gauge, registered at process startup: it is its own anchor.",
    ("kates-application", "Pods ready"):
        "Counts ready pods out of the pods kube-state reports. Zero ready is "
        "the reading the panel exists for.",
    ("kates-application", "Database pods ready"):
        "As 'Pods ready'.",
    ("kates-chaos", "Experiments passed"):
        "Counts experiments that passed; the same exporter gauge anchors the "
        "failed tile beside it.",
    ("kates-chaos", "Probe success rate"):
        "Thresholds are red at the base, so an absent series draws red rather "
        "than a reassuring green.",
    ("kates-chaos-infra", "Chaos experiments — Pass"):
        "Counts passing verdicts. Zero passes is not a health claim.",
    ("kates-chaos-infra", "Chaos operator"):
        "Red at the base: zero available replicas is the failure this panel "
        "is for, and absence draws the same red.",
    ("mirror-maker2-migration", "Safe to cut over?"):
        "The verdict is a product whose FIRST factor carries no fallback at "
        "all, so a release reporting nothing multiplies out to an empty "
        "vector and reads No data. The three fallbacks behind it default the "
        "remaining factors to their healthy value, which is only reachable "
        "once that first factor has proved the workers are being scraped.",
}

_BARE_ZERO = re.compile(r"\bor\s+vector\(\s*0\s*\)")


def check_zero_fallbacks(problems: list) -> int:
    """Fail on a bare `or vector(0)` outside the allowlist above."""
    seen = set()
    for path in sorted(DASHBOARDS.glob("*/dashboard.json")):
        board = path.parent.name
        dash = json.loads(path.read_text())
        for panel in walk(dash.get("panels") or []):
            if panel.get("type") == "row":
                continue
            title = panel.get("title", "?")
            exprs = [t.get("expr") or "" for t in (panel.get("targets") or [])]
            if not any(_BARE_ZERO.search(e) for e in exprs):
                continue
            key = (board, title)
            seen.add(key)
            if key in ZERO_FALLBACK_ALLOWED:
                continue
            err("dashboards/%s" % board,
                "panel %r uses a bare `or vector(0)`. A zero drawn because a "
                "series is absent cannot be told from a zero that was "
                "measured, so on an unscraped cluster this panel reads as "
                "healthy. Use panels.or_zero(expr, anchor) to anchor the "
                "fallback to a series that proves the scrape is live — or, if "
                "the expression is already self-anchoring, add it to "
                "ZERO_FALLBACK_ALLOWED in this file with the reason."
                % title, problems)
    # An allowlist that outlives its panel is how the next one gets waved
    # through by a rename.
    for board, title in sorted(ZERO_FALLBACK_ALLOWED):
        if (board, title) not in seen:
            err("scripts/check-dashboards.py",
                "ZERO_FALLBACK_ALLOWED lists %s / %r, which no longer uses a "
                "bare `or vector(0)`. Remove the entry." % (board, title),
                problems)
    print("  %-52s %3d bare, %d anchored"
          % ("zero fallbacks", len(seen), _anchored_count()))
    return len(seen)


# ── The colour of "No data" ────────────────────────────────────────────────
# Grafana paints the *No data* placeholder with the BASE threshold step, so a
# panel whose base is green says "No data" in green and one whose base is red
# says it in red. Dropping a bare `or vector(0)` stops a tile claiming a
# measured zero; it does not stop the words being painted the colour of
# health. `noValue` changes the text and not the colour. A special value
# mapping matching `null` is the only thing that carries a colour into that
# state, and panels.stat()/gauge() add one to every panel automatically —
# this is the check that they did.
_SINGLE_VALUE = {"stat", "gauge"}


def check_no_data_colour(problems: list) -> int:
    """Every single-value panel must render *No data* neutrally."""
    n = 0
    for path in sorted(DASHBOARDS.glob("*/dashboard.json")):
        board = path.parent.name
        dash = json.loads(path.read_text())
        for panel in walk(dash.get("panels") or []):
            if panel.get("type") not in _SINGLE_VALUE:
                continue
            n += 1
            title = panel.get("title", "?")
            maps = ((panel.get("fieldConfig") or {}).get("defaults") or {}).get("mappings") or []
            null_maps = [m for m in maps
                         if m.get("type") == "special"
                         and (m.get("options") or {}).get("match") == "null"]
            if not null_maps:
                err("dashboards/%s" % board,
                    "panel %r is a %s with no `null` value mapping, so Grafana "
                    "will paint its *No data* with the base threshold colour — "
                    "green on a fault panel, red on a panel whose base is red. "
                    "panels.stat() and panels.gauge() add one; a panel without "
                    "it was built some other way."
                    % (title, panel.get("type")), problems)
                continue
            colour = ((null_maps[0].get("options") or {}).get("result") or {}).get("color")
            if colour in ("green", "red", "yellow", "orange"):
                err("dashboards/%s" % board,
                    "panel %r maps no-data to %r. That is a threshold colour: "
                    "the point of the mapping is that absence reads as neither "
                    "healthy nor alarming." % (title, colour), problems)
    print("  %-52s %3d single-value panels" % ("no-data colour", n))
    return n


def _anchored_count() -> int:
    n = 0
    for path in sorted(DASHBOARDS.glob("*/dashboard.json")):
        dash = json.loads(path.read_text())
        for panel in walk(dash.get("panels") or []):
            for t in panel.get("targets") or []:
                n += len(re.findall(r"\bor\s+\(sum\(", t.get("expr") or ""))
    return n


def main() -> int:
    ap = argparse.ArgumentParser(description=__doc__)
    ap.add_argument("--render", action="store_true",
                    help="read a helm template stream on stdin instead of dashboards/")
    args = ap.parse_args()

    problems: list[str] = []
    checked = from_render(problems) if args.render else from_dashboards_dir(problems)
    if not args.render:
        check_metrics_documented(problems)
        check_zero_fallbacks(problems)
        check_no_data_colour(problems)

    if problems:
        print("", file=sys.stderr)
        for line in problems:
            print("::error::%s" % line, file=sys.stderr)
        print("\n%d problem(s) in %d dashboard(s)" % (len(problems), checked), file=sys.stderr)
        return 1
    print("OK: %d dashboard(s) pass the layout and documentation checks" % checked)
    return 0


if __name__ == "__main__":
    sys.exit(main())

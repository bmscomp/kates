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

Run over `dashboards/` by default, or over a rendered chart with `--render`,
which reads a `helm template` stream on stdin and checks every dashboard
ConfigMap in it.
"""

from __future__ import annotations

import argparse
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


def main() -> int:
    ap = argparse.ArgumentParser(description=__doc__)
    ap.add_argument("--render", action="store_true",
                    help="read a helm template stream on stdin instead of dashboards/")
    args = ap.parse_args()

    problems: list[str] = []
    checked = from_render(problems) if args.render else from_dashboards_dir(problems)

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

#!/usr/bin/env python3
"""Did this Grafana load every board, and did it keep what the board said?

Compares what Grafana returns over its API against the file on disk:
  * is the board there at all (a duplicate uid or malformed JSON is dropped
    silently by the provisioner)
  * does it still have every panel, by title
  * does it still have every template variable
  * did the datasource variable keep its current value, or did Grafana's
    schema migration drop it — which is what makes a board pick the wrong
    Prometheus by name-sort
  * what schemaVersion did this Grafana migrate the board to
"""
import json
import pathlib
import sys
import urllib.request

GRAFANA = sys.argv[1] if len(sys.argv) > 1 else "http://localhost:3000"
BOARDS = pathlib.Path(sys.argv[2]) if len(sys.argv) > 2 else (
    pathlib.Path(__file__).resolve().parent.parent.parent / "dashboards")
AUTH = "Basic " + __import__("base64").b64encode(b"admin:admin").decode()


def get(path):
    req = urllib.request.Request(GRAFANA + path, headers={"Authorization": AUTH})
    with urllib.request.urlopen(req, timeout=30) as r:
        return json.load(r)


def panels(ps):
    for p in ps:
        yield p
        yield from panels(p.get("panels") or [])


def titles(dash):
    return sorted(p.get("title", "") for p in panels(dash.get("panels") or [])
                  if p.get("type") != "row")


def variables(dash):
    return sorted(v.get("name", "") for v in
                  (dash.get("templating") or {}).get("list") or [])


# Threshold colours. A no-data placeholder painted any of these is the bug:
# absence has to read as neither healthy nor alarming.
JUDGEMENT = {"green", "red", "yellow", "orange", "dark-red", "semi-dark-red",
             "dark-green", "semi-dark-green", "dark-yellow", "dark-orange"}


def no_data_neutral(panel):
    """Does this panel still carry a neutral no-data mapping after loading?

    Two shapes, because Grafana 8 and 9 migrate mappings into their own:

      current  {"type": "special", "options": {"match": "null",
                "result": {"text": ..., "color": ...}}}
      legacy   {"type": 1 or "value", "value": "null", "text": ...} — the old
               MappingType.ValueToText, which carries NO colour at all

    The legacy shape cannot express a colour, so a Grafana that migrates into
    it cannot be fixed this way and has to be reported rather than assumed.
    """
    for m in ((panel.get("fieldConfig") or {}).get("defaults") or {}).get("mappings") or []:
        if m.get("type") == "special" and (m.get("options") or {}).get("match") == "null":
            colour = ((m.get("options") or {}).get("result") or {}).get("color")
            return colour is not None and colour not in JUDGEMENT
    return False


def main():
    wanted = sorted(BOARDS.glob("*.json")) or sorted(BOARDS.glob("*/dashboard.json"))
    if not wanted:
        # "0 of 0 boards checked" is a PASS, which is the failure mode this
        # whole branch has been chasing: a gate that is asleep reads exactly
        # like a gate that is satisfied.
        sys.exit("::error::no board JSON under %s — nothing was checked, which "
                 "is not the same as nothing being wrong" % BOARDS)
    loaded = {d["uid"]: d for d in get("/api/search?type=dash-db&limit=200")}
    problems = []
    schema_versions = set()
    checked = 0

    for path in wanted:
        want = json.loads(path.read_text())
        uid = want.get("uid")
        name = path.stem if path.stem != "dashboard" else path.parent.name
        if uid not in loaded:
            problems.append("%s: uid %s is NOT in this Grafana at all" % (name, uid))
            continue
        got = get("/api/dashboards/uid/%s" % uid)["dashboard"]
        checked += 1
        schema_versions.add(got.get("schemaVersion"))

        want_titles, got_titles = titles(want), titles(got)
        if want_titles != got_titles:
            missing = set(want_titles) - set(got_titles)
            extra = set(got_titles) - set(want_titles)
            problems.append("%s: panels differ — missing %s, unexpected %s"
                            % (name, sorted(missing)[:4], sorted(extra)[:4]))

        want_vars, got_vars = variables(want), variables(got)
        if want_vars != got_vars:
            problems.append("%s: variables differ — file %s, Grafana %s"
                            % (name, want_vars, got_vars))

        for v in (got.get("templating") or {}).get("list") or []:
            if v.get("type") != "datasource":
                continue
            current = v.get("current") or {}
            if not current.get("value"):
                problems.append(
                    "%s: the datasource variable lost its `current` — Grafana "
                    "then falls through to the first datasource sorted by NAME"
                    % name)

        for p in panels(got.get("panels") or []):
            if p.get("type") in ("row", "text"):
                continue
            if not p.get("targets"):
                problems.append("%s: panel %r has no targets after loading"
                                % (name, p.get("title")))
            if p.get("type") in ("stat", "gauge") and not no_data_neutral(p):
                # Grafana paints "No data" with the BASE threshold step,
                # so a fault panel says it in green and a panel whose base
                # is red says it in red. Every single-value panel ships a
                # special `null` mapping to make that state neutral. The
                # risk this check exists for is the MIGRATION: Grafana 8
                # and 9 rewrite mappings into their own shapes, and a
                # mapping that does not survive the rewrite is a fix that
                # silently only works on new Grafana.
                problems.append(
                    "%s: panel %r lost its neutral no-data mapping in "
                    "migration — this Grafana will paint its 'No data' "
                    "with the base threshold colour" % (name, p.get("title")))

    print("  loaded %d/%d boards; schemaVersion after migration: %s"
          % (checked, len(wanted),
             ", ".join(str(s) for s in sorted(schema_versions)) or "?"))
    for p in problems:
        print("    PROBLEM: %s" % p)
    return 1 if problems or checked != len(wanted) else 0


if __name__ == "__main__":
    sys.exit(main())

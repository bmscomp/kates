#!/usr/bin/env python3
"""Push every board in this directory into a Grafana over its HTTP API.

The Helm charts are one way to install these boards and they need a cluster.
This is the other way: a Grafana, a URL and a credential. It works against a
managed Grafana, a Grafana Cloud stack, a Grafana in a cluster you can only
reach through a port-forward, and the container on your laptop.

    dashboards/install.py --url http://localhost:3000 --user admin:admin
    dashboards/install.py --url "$GRAFANA_URL" --folder Kates \
        --datasource Prometheus --tag managed
    dashboards/install.py --url "$GRAFANA_URL" --dry-run kafka-kraft mirror-maker2

WHY PYTHON AND NOT A SHELL SCRIPT. Three of the four things this does are JSON
edits on the board before it is posted — setting the datasource variable's
current value, merging tags, and clearing any `id` — and the fourth is reading
Grafana's error body to say *which* board was rejected and *why*. In bash that
is a hard dependency on `jq` for every one of them; here it is the standard
library, which is the same bet `scripts/gen-dashboards.py` and
`scripts/check-dashboards.py` already make. Nothing outside the standard
library is imported, so this file runs on any Python 3.8+ with no install
step — including inside a `python:3-slim` one-liner in CI.

IDEMPOTENCE. Every board carries a stable `uid` (see each board's
`manifest.yaml`), and this posts with `overwrite: true`. Running it twice
updates the same board rather than creating a second one, which is what makes
it safe to put in a pipeline. The one thing it never reuses is Grafana's
numeric `id`: that is per-instance, so it is stripped before posting.

WHAT IT DOES NOT DO. It installs the `default` variant of each board — the
committed `dashboards/<board>/dashboard.json`. The conditional shapes,
mirror-maker2's `no-slo` and `identity`, exist because a Helm RELEASE knows
something a file cannot (whether the recording rules are installed, which
replication policy is set) and the chart that knows it picks one. Nor does it
give the per-release boards a per-release uid and title the way their chart
does, so one MirrorMaker 2 release's board is installed here, not one per
release. Everything those boards are scoped by is a dropdown resolved from the
data, so the board itself works either way; `kates-chaos-infra`'s two hidden
constants are the exception, and they default to LitmusChaos' own `litmus`.
"""

from __future__ import annotations

import argparse
import base64
import json
import os
import ssl
import sys
import urllib.error
import urllib.parse
import urllib.request
from pathlib import Path
from typing import Any

HERE = Path(__file__).resolve().parent

EXIT_OK = 0
EXIT_FAILED = 1
EXIT_USAGE = 2


# ── Grafana client ──────────────────────────────────────────────────────────

class GrafanaError(Exception):
    """A request Grafana refused, carrying what it said about it."""

    def __init__(self, status: int, method: str, path: str, body: str):
        self.status = status
        self.method = method
        self.path = path
        self.body = body
        super().__init__(self.explain())

    def explain(self) -> str:
        """Grafana's own words where it gave any, the raw body where it did not."""
        detail = self.body.strip()
        try:
            payload = json.loads(self.body)
        except (ValueError, TypeError):
            payload = None
        if isinstance(payload, dict):
            parts = [str(payload[k]) for k in ("message", "error") if payload.get(k)]
            # A schema rejection puts the offending field here and nowhere else.
            for key in ("validationMessage", "details"):
                if payload.get(key):
                    parts.append(json.dumps(payload[key]))
            if parts:
                detail = " — ".join(dict.fromkeys(parts))
        if len(detail) > 600:
            detail = detail[:600] + "…"
        return "HTTP %d from %s %s: %s" % (self.status, self.method, self.path,
                                           detail or "(no body)")



# Grafana refuses an API write to a board that arrived through FILE
# PROVISIONING — which is how these boards arrive when kube-prometheus-stack's
# dashboard sidecar installed them, because that provider ships
# `allowUiUpdates: false`. The API answers 400 "Cannot save provisioned
# dashboard", and that message says what happened and not what to do about it.
# Verified against Grafana 12.3.1 with a sidecar-shaped provider.
_PROVISIONED = "provisioned dashboard"


def provisioned_hint(exc: "GrafanaError") -> str:
    """The one line worth adding when Grafana refuses a provisioned board."""
    if _PROVISIONED not in str(exc).lower():
        return ""
    return ("this board is file-provisioned (the Grafana dashboard sidecar, or "
            "bundle/provisioning/), and Grafana will not let the API overwrite "
            "one. Update it where it comes from — `helm upgrade` the chart that "
            "delivers it — or install into a Grafana that does not provision "
            "these boards.")


class Grafana:
    """The handful of Grafana endpoints this installer needs."""

    def __init__(self, url: str, *, token: str = "", basic: str = "",
                 insecure: bool = False, timeout: int = 30):
        self.url = url.rstrip("/")
        self.timeout = timeout
        self.headers = {"Content-Type": "application/json",
                        "Accept": "application/json"}
        if token:
            self.headers["Authorization"] = "Bearer %s" % token
        elif basic:
            encoded = base64.b64encode(basic.encode()).decode()
            self.headers["Authorization"] = "Basic %s" % encoded
        self.context = None
        if insecure:
            self.context = ssl.create_default_context()
            self.context.check_hostname = False
            self.context.verify_mode = ssl.CERT_NONE

    def request(self, method: str, path: str, payload: Any = None) -> Any:
        data = json.dumps(payload).encode() if payload is not None else None
        req = urllib.request.Request(self.url + path, data=data,
                                     headers=self.headers, method=method)
        try:
            with urllib.request.urlopen(req, timeout=self.timeout,
                                        context=self.context) as resp:
                body = resp.read().decode()
        except urllib.error.HTTPError as exc:
            raise GrafanaError(exc.code, method, path,
                               exc.read().decode(errors="replace")) from None
        except urllib.error.URLError as exc:
            raise GrafanaError(0, method, path, "cannot reach %s: %s"
                               % (self.url, exc.reason)) from None
        return json.loads(body) if body.strip() else None

    # -- what the installer actually calls --

    def health(self) -> dict:
        return self.request("GET", "/api/health")

    def whoami(self) -> dict:
        """The org this credential is good for.

        `/api/health` answers without a credential, so it proves reachability
        and nothing else. This is the cheapest authenticated call there is,
        and checking it once turns one clear line into the alternative:
        twelve identical 401s, one per board.
        """
        return self.request("GET", "/api/org")

    def datasources(self) -> list[dict]:
        return self.request("GET", "/api/datasources") or []

    def folders(self) -> list[dict]:
        return self.request("GET", "/api/folders?limit=1000") or []

    def create_folder(self, title: str) -> dict:
        return self.request("POST", "/api/folders", {"title": title})

    def push(self, dashboard: dict, folder_uid: str, message: str) -> dict:
        payload: dict[str, Any] = {"dashboard": dashboard, "overwrite": True,
                                   "message": message}
        if folder_uid:
            payload["folderUid"] = folder_uid
        return self.request("POST", "/api/dashboards/db", payload)


# ── boards on disk ──────────────────────────────────────────────────────────

def board_files(names: list[str]) -> list[tuple[str, Path]]:
    """`(name, dashboard.json)` for the boards asked for, or for all of them."""
    available = {d.name: d / "dashboard.json"
                 for d in sorted(HERE.iterdir())
                 if d.is_dir() and not d.name.startswith("_")
                 and (d / "dashboard.json").exists()}
    if not available:
        sys.exit("no board has a dashboard.json — run scripts/gen-dashboards.py first")
    if not names:
        return list(available.items())

    chosen, unknown = [], []
    for name in names:
        if name in available:
            chosen.append((name, available[name]))
        else:
            unknown.append(name)
    if unknown:
        sys.exit("unknown board(s): %s\nknown boards: %s"
                 % (", ".join(unknown), ", ".join(available)))
    return chosen


def prepare(board: dict, *, datasource: dict | None, tags: list[str]) -> dict:
    """The board as it will be posted.

    `id` is Grafana's per-instance primary key and means nothing here; sending
    one from another Grafana is how an import lands on top of an unrelated
    board. The `uid` is what carries identity, and it is in the file.
    """
    board = json.loads(json.dumps(board))  # never mutate the caller's copy
    board.pop("id", None)

    if tags:
        merged = list(board.get("tags") or []) + tags
        board["tags"] = list(dict.fromkeys(merged))

    if datasource:
        set_datasource(board, datasource)
    return board


def set_datasource(board: dict, datasource: dict) -> bool:
    """Point the board's `datasource` variable at a named datasource.

    Every board reads `${datasource}` and ships asking for `default`, which
    lands on the org's default datasource (see `datasource_var` in
    `_lib/panels.py`). That is the right answer when there is one Prometheus
    and the wrong one whenever the Prometheus these boards want is not the org
    default — a second cluster, a Thanos in front of several, a Mimir. Naming
    it here takes the guess out. Grafana keys a datasource variable's current
    value by UID and shows its name, so both go in.
    """
    for var in board.get("templating", {}).get("list", []):
        if var.get("type") == "datasource":
            var["current"] = {"text": datasource["name"],
                              "value": datasource["uid"]}
            return True
    return False


# ── resolution ──────────────────────────────────────────────────────────────

def resolve_datasource(api: Grafana, wanted: str) -> dict:
    """The datasource named `wanted`, by name or by uid, or a listing error."""
    found = api.datasources()
    for ds in found:
        if ds.get("name") == wanted or ds.get("uid") == wanted:
            return {"name": ds["name"], "uid": ds["uid"], "type": ds.get("type", "")}
    known = ", ".join("%s (uid=%s, %s)" % (d.get("name"), d.get("uid"), d.get("type"))
                      for d in found) or "none"
    sys.exit("no datasource named or uid'd %r in this Grafana.\n"
             "Datasources it has: %s" % (wanted, known))


def resolve_folder(api: Grafana, title: str, dry_run: bool) -> tuple[str, str]:
    """`(uid, note)` for the folder, creating it when it does not exist."""
    if not title:
        return "", "General (no --folder)"
    for folder in api.folders():
        if folder.get("title") == title:
            return folder.get("uid", ""), "folder %r (exists, uid=%s)" % (
                title, folder.get("uid"))
    if dry_run:
        return "", "folder %r (WOULD BE CREATED)" % title
    created = api.create_folder(title)
    return created.get("uid", ""), "folder %r (created, uid=%s)" % (
        title, created.get("uid"))


# ── main ────────────────────────────────────────────────────────────────────

def auth_from(args: argparse.Namespace) -> tuple[str, str]:
    """`(token, basic)` from the flags, then the environment."""
    token = args.token or os.environ.get("GRAFANA_TOKEN", "")
    if token:
        return token, ""

    user = args.user or os.environ.get("GRAFANA_USER", "")
    if not user:
        return "", ""
    if ":" in user:
        return "", user
    password = os.environ.get("GRAFANA_PASSWORD", "")
    if not password:
        sys.exit("--user %s has no password: pass --user %s:PASSWORD or set "
                 "$GRAFANA_PASSWORD" % (user, user))
    return "", "%s:%s" % (user, password)


def parse_args(argv: list[str]) -> argparse.Namespace:
    ap = argparse.ArgumentParser(
        prog="dashboards/install.py",
        description=__doc__,
        formatter_class=argparse.RawDescriptionHelpFormatter)
    ap.add_argument("boards", nargs="*", metavar="BOARD",
                    help="board directory names; default is every board")
    ap.add_argument("--url", default=os.environ.get("GRAFANA_URL", ""),
                    help="Grafana base URL, e.g. http://localhost:3000 ($GRAFANA_URL)")
    ap.add_argument("--token", default="",
                    help="service-account token or API key ($GRAFANA_TOKEN)")
    ap.add_argument("--user", default="",
                    help="basic auth as user:password, or a user with "
                         "$GRAFANA_PASSWORD ($GRAFANA_USER)")
    ap.add_argument("--folder", default="",
                    help="Grafana folder to file the boards into; created when "
                         "missing. Default is the General folder")
    ap.add_argument("--datasource", default="",
                    help="name or uid of the Prometheus datasource to select in "
                         "each board, so the panels resolve without touching the "
                         "picker")
    ap.add_argument("--tag", action="append", default=[], metavar="TAG",
                    help="extra tag to add to every board; repeatable")
    ap.add_argument("--message", default="installed by dashboards/install.py",
                    help="the version message Grafana records for this push")
    ap.add_argument("--dry-run", action="store_true",
                    help="say what would be pushed and change nothing")
    ap.add_argument("--insecure", action="store_true",
                    help="skip TLS verification (a Grafana behind a self-signed "
                         "certificate)")
    ap.add_argument("--list", action="store_true",
                    help="print the boards and their uids, and exit")
    return ap.parse_args(argv)


def main(argv: list[str] | None = None) -> int:
    args = parse_args(sys.argv[1:] if argv is None else argv)
    boards = board_files(args.boards)

    loaded: list[tuple[str, dict]] = []
    for name, path in boards:
        try:
            loaded.append((name, json.loads(path.read_text())))
        except ValueError as exc:
            print("%s: %s is not valid JSON: %s" % (name, path, exc), file=sys.stderr)
            return EXIT_FAILED

    if args.list:
        for name, board in loaded:
            print("%-26s uid=%-28s %s" % (name, board.get("uid"), board.get("title")))
        return EXIT_OK

    if not args.url:
        print("--url is required (or set $GRAFANA_URL)", file=sys.stderr)
        return EXIT_USAGE

    token, basic = auth_from(args)
    if not token and not basic:
        print("note: no credential given — continuing unauthenticated, which "
              "works only on a Grafana with an anonymous admin org role.",
              file=sys.stderr)

    api = Grafana(args.url, token=token, basic=basic, insecure=args.insecure)

    try:
        health = api.health()
    except GrafanaError as exc:
        print("cannot talk to Grafana at %s: %s" % (args.url, exc), file=sys.stderr)
        return EXIT_FAILED
    print("Grafana %s at %s" % (health.get("version", "?"), args.url))

    try:
        org = api.whoami()
    except GrafanaError as exc:
        if exc.status in (401, 403):
            how = ("--token / $GRAFANA_TOKEN" if token
                   else "--user / $GRAFANA_USER" if basic
                   else "no credential at all")
            print("Grafana refused the credential (%s): %s\n"
                  "The token or user needs the Editor role, or Admin when "
                  "--folder has to create a folder." % (how, exc), file=sys.stderr)
        else:
            print("cannot read the org from %s: %s" % (args.url, exc), file=sys.stderr)
        return EXIT_FAILED
    print("Org:        %s" % org.get("name", "?"))

    datasource = None
    if args.datasource:
        datasource = resolve_datasource(api, args.datasource)
        print("Datasource: %s (uid=%s)" % (datasource["name"], datasource["uid"]))

    folder_uid, folder_note = resolve_folder(api, args.folder, args.dry_run)
    print("Target:     %s" % folder_note)
    if args.tag:
        print("Extra tags: %s" % ", ".join(args.tag))
    print("")

    failures: list[str] = []
    for name, raw in loaded:
        board = prepare(raw, datasource=datasource, tags=args.tag)
        uid = board.get("uid", "")
        if args.dry_run:
            print("  WOULD PUSH  %-26s uid=%-28s %d panels"
                  % (name, uid, len(board.get("panels") or [])))
            continue
        try:
            result = api.push(board, folder_uid, args.message)
        except GrafanaError as exc:
            print("  FAILED      %-26s uid=%s\n              %s"
                  % (name, uid, exc), file=sys.stderr)
            hint = provisioned_hint(exc)
            if hint:
                print("              -> %s" % hint, file=sys.stderr)
            failures.append(name)
            continue
        print("  ok          %-26s uid=%-28s version=%s  %s"
              % (name, result.get("uid", uid), result.get("version", "?"),
                 result.get("url", "")))

    print("")
    if args.dry_run:
        print("Dry run: %d board(s) would be installed, nothing was changed."
              % len(loaded))
        return EXIT_OK
    if failures:
        print("%d of %d board(s) FAILED: %s"
              % (len(failures), len(loaded), ", ".join(failures)), file=sys.stderr)
        return EXIT_FAILED
    print("Installed %d board(s)." % len(loaded))
    return EXIT_OK


if __name__ == "__main__":
    sys.exit(main())

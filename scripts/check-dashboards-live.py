#!/usr/bin/env python3
"""Do the boards' panels return data against a real scrape?

    scripts/check-dashboards-live.py --scrape /tmp/scrape/broker.txt [--scrape …]
    scripts/check-dashboards-live.py --prometheus http://localhost:9090

WHAT THIS ADDS TO THE CHECKS THAT ALREADY EXIST. Three things already look at
the boards and none of them runs a query:

  * `check-metric-contract.sh` asks whether every NAME a panel reads is one the
    exporter rules can produce. It is static, and with `--scrape` it also
    confirms the name appears in a real capture.
  * `promtool check rules` asks whether every query PARSES.
  * `check-dashboards.py` asks about layout, descriptions, uids and whether
    METRICS.md documents the series.

A query can pass all three and still return nothing, and the failure looks
exactly like a healthy idle cluster. The panel selects on a label the series
does not carry (`zone` before the relabeling that sets it existed), or groups
`by` a label the exporter never attaches, or filters `topic=""` against an
exporter that registers the bean twice, or writes `quantile="0.99"` where the
exporter publishes `0.98`. Every one of those is a correct name in a query that
draws an empty panel.

This runs the queries.

WHAT IT ACTUALLY EVALUATES: THE SELECTORS, NOT THE WHOLE EXPRESSION. Running
the full query looks like the obvious thing to do and is the wrong thing to do,
because the answer then depends on data this check does not control. A static
capture scraped repeatedly holds a constant, so `rate()` over it is 0;
`histogram_quantile` over all-zero buckets is NaN; `deriv(x[10m:])` needs a
slope; `kube_pod_status_phase == 1` depends on the captured value being 1. Every
one of those produces an empty result for a perfectly correct panel. A check
that reports sixteen failures of that kind on a healthy board is a check people
turn off.

So each query is broken into its instant-vector selectors — `name{matchers}` —
and each selector is evaluated on its own. That is precisely the class of bug
nothing else here catches, and it is fully decidable from one capture:

  * `kafka_server_brokertopicmetrics_bytesin_total{zone="a"}` when nothing sets
    `zone` — empty selector, correct name.
  * `sum by (zone) (…)` when nothing sets `zone` — the grouping collapses and
    the legend renders `()`, which is how `{{zone}}` shipped for years.
  * `topic=""` against an exporter that registers the bean twice.
  * `quantile="0.99"` where the exporter publishes `0.98`.

None of those depends on history, on a value, or on how long Prometheus has
been running.

HOW IT DECIDES WHAT IS A FAILURE. "Empty" is not automatically wrong: a CI
cluster has no Kyverno, no LitmusChaos and no tiered storage, so selectors
reading those correctly return nothing.

  * The selector's metric name is ABSENT from the scrape  -> SKIP. Nothing
    publishes it here; the metric contract owns that case.
  * The name is PRESENT and the SELECTOR returns nothing  -> FAIL. The series
    exist and the panel cannot see them.
  * The name is present, the selector matches, but a label the query groups
    `by` is on none of the matched series -> FAIL. The panel draws, with an
    empty legend.
  * Prometheus rejects the selector -> FAIL.

The check therefore gets stricter on its own as more of the stack is installed
in the job that runs it, and it never depends on a number being what it was
when the capture was taken.
"""

import argparse
import json
import os
import re
import shutil
import subprocess
import sys
import tempfile
import time
import urllib.error
import urllib.parse
import urllib.request
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent
DASHBOARDS = ROOT / "dashboards"
sys.path.insert(0, str(ROOT / "scripts" / "metric-contract"))

EXIT_OK, EXIT_FAILED, EXIT_USAGE = 0, 1, 2


# ── the boards ──────────────────────────────────────────────────────────────

def load_boards(only: list[str]) -> list[tuple[str, dict]]:
    out = []
    for d in sorted(DASHBOARDS.iterdir()):
        if not d.is_dir() or d.name.startswith("_") or d.name == "bundle":
            continue
        f = d / "dashboard.json"
        if not f.is_file():
            continue
        if only and d.name not in only:
            continue
        out.append((d.name, json.loads(f.read_text())))
    if not out:
        sys.exit("no board matched — run scripts/gen-dashboards.py first")
    return out


def panels(ps):
    for p in ps:
        yield p
        yield from panels(p.get("panels") or [])


def variables(dash: dict) -> list[dict]:
    return (dash.get("templating") or {}).get("list") or []


def queries(dash: dict):
    """(kind, title, expr) for every panel target and query variable."""
    for p in panels(dash.get("panels") or []):
        if p.get("type") in ("row", "text"):
            continue
        for t in p.get("targets") or []:
            expr = (t.get("expr") or "").strip()
            if expr:
                yield "panel", p.get("title", "?"), expr
    for v in variables(dash):
        q = v.get("query")
        if isinstance(q, dict):
            q = q.get("query")
        if v.get("type") == "query" and q:
            yield "variable", v.get("name", "?"), str(q).strip()


VAR = re.compile(r"\$\{([A-Za-z_][A-Za-z0-9_]*)(?::[^}]*)?\}"
                 r"|\$([A-Za-z_][A-Za-z0-9_]*)")
LABEL_VALUES = re.compile(
    r"label_values\((.*),\s*([A-Za-z_][A-Za-z0-9_]*)\s*\)\s*$", re.S)


def substitute(expr: str, values: dict) -> str:
    return VAR.sub(lambda m: values.get(m.group(1) or m.group(2), ""), expr)


# An instant-vector selector: a metric name, optionally with label matchers.
# Recorded series carry a colon, which no exporter-published name does, and
# they are defined by the charts rather than by a board — a board reading one
# is checked by the chart's own rules, not here.
SELECTOR = re.compile(r"\b([a-zA-Z_][a-zA-Z0-9_]*)\s*(\{[^{}]*\})?")
GROUPING = re.compile(r"\b(?:by|without)\s*\(([^)]*)\)")

# Names that are PromQL, not series. Same list the metric contract uses, taken
# from it so the two cannot drift.
def _keywords():
    import contract as C
    return C._KEYWORDS


def _mask(expr: str) -> str:
    """The expression with everything that is not a metric name blanked to
    spaces, KEEPING EVERY OFFSET.

    Same length as the original, so a match found here can be sliced out of
    the original. An earlier version substituted rather than masked and then
    queried the substituted text — which turned `{namespace="kafka"}` into
    `{namespace=""}` and reported all 369 selectors as broken. Masking is what
    makes "find it here, take it from there" safe.

    Blanked: string literals, ranges and subqueries, the INTERIOR of every
    matcher block (a label name is not a metric name), and grouping clauses in
    full — `by` and `without` included, so the aggregation in front of one is
    still recognisable as a function call.
    """
    out = list(expr)

    def blank(pattern, group=0):
        for m in re.finditer(pattern, expr):
            for i in range(m.start(group), m.end(group)):
                out[i] = " "

    blank(r'"(?:\\.|[^"\\])*"')            # string literals
    blank(r"'(?:\\.|[^'\\])*'")
    blank(r"\[[^\]]*\]")                    # ranges and subqueries
    blank(r"\{([^{}]*)\}", group=1)          # matcher interiors, braces kept
    blank(r"\b(?:by|without)\s*\([^)]*\)")   # grouping clauses, in full
    return "".join(out)


def selectors(expr: str) -> list[tuple[str, str]]:
    """Every `(name, selector)` an expression reads, sliced from the original.

    The selector handed back is the real text, matchers and all — it is what
    gets sent to Prometheus.
    """
    kw = _keywords()
    masked = _mask(expr)
    out, i = [], 0
    for m in re.finditer(r"[a-zA-Z_][a-zA-Z0-9_]*", masked):
        if m.start() < i:
            continue                        # inside a selector already taken
        name = m.group(0)
        if name in kw:
            continue
        rest = masked[m.end():]
        if rest.lstrip().startswith("("):
            continue                        # a function call, not a selector
        end = m.end()
        if rest.lstrip().startswith("{"):
            brace = expr.index("{", end)
            end = expr.index("}", brace) + 1
        i = end
        out.append((name, expr[m.start():end]))
    return out


def grouped_labels(expr: str) -> list[str]:
    """Labels an expression groups `by`. `without` is not checked: it names
    labels to DROP, so one that does not exist is harmless."""
    out = []
    for m in re.finditer(r"\bby\s*\(([^)]*)\)", expr):
        out += [x.strip() for x in m.group(1).split(",") if x.strip()]
    return out


# ── Prometheus ──────────────────────────────────────────────────────────────

class Prom:
    def __init__(self, url: str):
        self.url = url.rstrip("/")

    def _get(self, path: str, **params):
        url = "%s%s?%s" % (self.url, path, urllib.parse.urlencode(params))
        with urllib.request.urlopen(url, timeout=60) as r:
            return json.load(r)

    def query(self, expr: str):
        """(ok, results, error). A 400 from Prometheus is an answer, not a crash."""
        try:
            body = self._get("/api/v1/query", query=expr)
        except urllib.error.HTTPError as exc:
            try:
                detail = json.load(exc).get("error", "")
            except Exception:
                detail = exc.reason
            return False, [], str(detail)[:200]
        except Exception as exc:
            return False, [], str(exc)[:200]
        if body.get("status") != "success":
            return False, [], str(body.get("error", "?"))[:200]
        return True, body["data"]["result"], ""

    def names(self) -> set:
        try:
            return set(self._get("/api/v1/label/__name__/values").get("data") or [])
        except Exception:
            return set()

    def wait(self, deadline: float) -> bool:
        while time.time() < deadline:
            try:
                with urllib.request.urlopen(self.url + "/-/ready", timeout=5) as r:
                    if r.status == 200:
                        return True
            except Exception:
                pass
            time.sleep(1)
        return False


def resolve_variables(prom: Prom, dash: dict) -> tuple[dict, list[str]]:
    """What each template variable resolves to, asking Prometheus as Grafana does.

    A variable that resolves to nothing makes every panel scoped by it query
    nothing — which is how kates-benchmark came to be not partly broken but
    entirely non-functional, both of its variables built on a series nothing
    published. So an empty variable is reported in its own right.
    """
    values, empty = {}, []
    for v in variables(dash):
        name, kind = v.get("name"), v.get("type")
        if kind == "datasource":
            values[name] = "prometheus"
            continue
        if kind == "constant":
            values[name] = str(v.get("query", ""))
            continue
        if kind == "custom":
            opts = [o.strip() for o in str(v.get("query", "")).split(",") if o.strip()]
            values[name] = opts[0] if opts else ""
            if not opts:
                empty.append(name)
            continue
        q = v.get("query")
        if isinstance(q, dict):
            q = q.get("query")
        m = LABEL_VALUES.match(str(q or "").strip())
        got = []
        if m:
            selector, label = substitute(m.group(1), values), m.group(2)
            ok, results, _ = prom.query("count by (%s) (%s)" % (label, selector))
            if ok:
                got = sorted(r["metric"].get(label, "") for r in results)
                got = [g for g in got if g]
        values[name] = got[0] if got else ""
        if not got:
            empty.append(name)
    return values, empty


# ── a Prometheus over captured /metrics files ───────────────────────────────

def serve_captures(paths: list[Path], workdir: Path) -> tuple[subprocess.Popen, int]:
    """Serve each capture as a /metrics endpoint Prometheus can scrape."""
    root = workdir / "www"
    root.mkdir(parents=True, exist_ok=True)
    for i, p in enumerate(paths):
        target = root / ("t%d" % i)
        target.mkdir(exist_ok=True)
        # A capture may carry HELP/TYPE lines for names another capture also
        # declares; each is served separately so Prometheus never sees a
        # duplicate declaration in one body.
        shutil.copyfile(p, target / "metrics")
    proc = subprocess.Popen(
        [sys.executable, "-m", "http.server", "0", "--bind", "127.0.0.1"],
        cwd=root, stdout=subprocess.PIPE, stderr=subprocess.STDOUT, text=True)
    port = None
    deadline = time.time() + 30
    while time.time() < deadline:
        line = proc.stdout.readline()
        m = re.search(r"port (\d+)", line or "")
        if m:
            port = int(m.group(1))
            break
        if proc.poll() is not None:
            break
    if port is None:
        proc.kill()
        sys.exit("could not start the capture server")
    return proc, port


def run_prometheus(paths: list[Path], workdir: Path, binary: str) -> tuple:
    server, port = serve_captures(paths, workdir)
    targets = ["127.0.0.1:%d" % port]
    config = workdir / "prometheus.yml"
    config.write_text(
        "global:\n"
        "  scrape_interval: 1s\n"
        "  scrape_timeout: 1s\n"
        "scrape_configs:\n"
        + "".join(
            "  - job_name: capture%d\n"
            "    metrics_path: /t%d/metrics\n"
            # A capture taken from a JMX exporter is served by http.server as
            # application/octet-stream, which Prometheus 3 refuses without a
            # declared fallback.
            "    fallback_scrape_protocol: PrometheusText0.0.4\n"
            "    honor_labels: true\n"
            "    static_configs:\n"
            "      - targets: ['%s']\n" % (i, i, targets[0])
            for i in range(len(paths))))
    proc = subprocess.Popen(
        [binary, "--config.file=%s" % config,
         "--storage.tsdb.path=%s" % (workdir / "data"),
         "--web.listen-address=127.0.0.1:19999",
         "--storage.tsdb.retention.time=1h"],
        stdout=subprocess.DEVNULL, stderr=subprocess.PIPE, text=True)
    return proc, server, "http://127.0.0.1:19999"


# ── the check ───────────────────────────────────────────────────────────────

def main() -> int:
    ap = argparse.ArgumentParser(description=__doc__,
                                 formatter_class=argparse.RawDescriptionHelpFormatter)
    ap.add_argument("--scrape", action="append", default=[], metavar="FILE",
                    help="a captured /metrics body; repeatable. A throwaway "
                         "Prometheus is started over them.")
    ap.add_argument("--prometheus", default="", metavar="URL",
                    help="query this Prometheus instead of starting one")
    ap.add_argument("--board", action="append", default=[], metavar="NAME",
                    help="only this board; repeatable")
    ap.add_argument("--prometheus-binary", default=os.environ.get("PROMETHEUS", "prometheus"))
    ap.add_argument("--settle", type=int, default=25, metavar="SECONDS",
                    help="how long to let the throwaway Prometheus scrape")
    args = ap.parse_args()

    if bool(args.scrape) == bool(args.prometheus):
        ap.error("give either --scrape (one or more) or --prometheus, not both")

    boards = load_boards(args.board)
    workdir = Path(tempfile.mkdtemp(prefix="dashlive-"))
    prom_proc = server_proc = None

    try:
        if args.prometheus:
            prom = Prom(args.prometheus)
        else:
            paths = [Path(p) for p in args.scrape]
            missing = [str(p) for p in paths if not p.is_file() or not p.stat().st_size]
            if missing:
                sys.exit("empty or missing capture(s): %s" % ", ".join(missing))
            if not shutil.which(args.prometheus_binary):
                sys.exit("no prometheus binary (%r). Pass --prometheus URL, or "
                         "set $PROMETHEUS." % args.prometheus_binary)
            prom_proc, server_proc, url = run_prometheus(paths, workdir,
                                                         args.prometheus_binary)
            prom = Prom(url)
            if not prom.wait(time.time() + 60):
                err = (prom_proc.stderr.read() or "")[-1500:] if prom_proc.stderr else ""
                sys.exit("the throwaway Prometheus never became ready.\n" + err)
            time.sleep(args.settle)

        present = prom.names()
        if not present:
            sys.exit("Prometheus holds no series at all, so there is nothing to "
                     "check against. " + (
                         "Is %s scraping anything yet?" % args.prometheus
                         if args.prometheus else
                         "Are the captures non-empty exposition bodies?"))
        print("Prometheus holds %d distinct series name(s)\n" % len(present))

        import contract as C          # the same tokenizer every other gate uses

        failures, totals = [], {"run": 0, "data": 0, "skip": 0, "fail": 0}
        for name, dash in boards:
            values, empty = resolve_variables(prom, dash)
            counts = {"data": 0, "skip": 0, "fail": 0}
            seen_here = set()
            for kind, title, expr in queries(dash):
                resolved = substitute(expr, values)
                m = LABEL_VALUES.match(resolved)
                resolved = m.group(1) if m else resolved
                groups = grouped_labels(resolved)
                for metric, selector in selectors(resolved):
                    # The same selector on ten panels of one board is one fact.
                    key = (selector, tuple(groups))
                    if key in seen_here:
                        continue
                    seen_here.add(key)
                    totals["run"] += 1
                    if metric not in present:
                        counts["skip"] += 1
                        continue
                    ok, results, error = prom.query(selector)
                    if not ok:
                        counts["fail"] += 1
                        failures.append((name, kind, title,
                                         "REJECTED: %s" % error, selector))
                        continue
                    if not results:
                        counts["fail"] += 1
                        failures.append((
                            name, kind, title,
                            "%s IS in the scrape but this selector matches none "
                            "of it — a label it filters on is not on the series"
                            % metric, selector))
                        continue
                    counts["data"] += 1
                    # A `by` label that is on none of the matched series makes
                    # the grouping collapse and the legend render empty —
                    # which is how `{{zone}}` shipped as `()` for years.
                    for label in groups:
                        if label in values or label.startswith("__"):
                            continue
                        if any(label in r.get("metric", {}) for r in results):
                            continue
                        counts["fail"] += 1
                        failures.append((
                            name, kind, title,
                            "groups by %r, which is on none of the series this "
                            "selector matches — the grouping collapses and the "
                            "legend renders empty" % label, selector))
            for k in counts:
                totals[k] += counts[k]
            flag = "  EMPTY VARIABLES: %s" % ", ".join(empty) if empty else ""
            print("  %-26s %3d selectors  %3d match  %3d skipped  %3d FAILED%s"
                  % (name, sum(counts.values()), counts["data"], counts["skip"],
                     counts["fail"], flag))
            for var in empty:
                failures.append((name, "variable", var,
                                 "resolves to nothing, so every panel scoped by "
                                 "it queries nothing", ""))

        print("\n%d distinct selectors: %d matched series, %d skipped (nothing "
              "publishes them here), %d FAILED"
              % (totals["run"], totals["data"], totals["skip"], totals["fail"]))

        if failures:
            print("")
            for board, kind, title, why, expr in failures:
                print("::error::[%s] %s %r: %s" % (board, kind, title, why))
                if expr:
                    print("    %s" % expr[:400])
            return EXIT_FAILED

        print("OK: every selector that could match, matched — and every `by` "
              "label is on the series it groups.")
        return EXIT_OK
    finally:
        for p in (prom_proc, server_proc):
            if p and p.poll() is None:
                p.terminate()
                try:
                    p.wait(timeout=10)
                except subprocess.TimeoutExpired:
                    p.kill()
        shutil.rmtree(workdir, ignore_errors=True)


if __name__ == "__main__":
    sys.exit(main())

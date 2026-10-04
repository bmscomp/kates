#!/usr/bin/env python3
"""Render a chart across its documented shapes and check every render.

A chart that is only ever rendered with its defaults is only ever checked with
its defaults. Most of what goes wrong in these charts goes wrong behind a
toggle or an overlay: a field the API server prunes, a required field an
autoscaler branch leaves out, a NetworkPolicy that does not admit the port the
bootstrap string names. This walks the matrix in scripts/chart-matrix/<chart>.yaml:

  renders   every overlay and toggle, each checked with
              - scripts/check-strimzi-crs.py (pinned CRDs, duplicate keys)
              - kubeconform -strict for built-in kinds (unless --offline)
              - the render's own `assert` entries and named `checks`
            A render marked `known: <ID>` is EXPECTED to fail one of those, and
            the run fails when it stops failing — so a fixed bug cannot leave a
            stale marker behind (XPASS).
  rails     renders that must be REFUSED, with the message asserted, so a rail
            that fails for the wrong reason is not mistaken for a working one.

A render lists `values` (relative to the chart), `inline` values, `set` and
`setString`; `apiVersions` adds to the matrix's, and `matrixApiVersions: false`
drops the matrix's (to render as on a cluster without those APIs);
`kubeVersion` renders as for that Kubernetes version.

Usage: scripts/check-chart-matrix.py <chart> [--offline] [--only NAME] [--list]
Needs helm and PyYAML; kubeconform is optional (skipped with a note if absent).
"""

import argparse
import os
import re
import shutil
import subprocess
import sys
import tempfile

import yaml

ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
sys.path.insert(0, os.path.join(ROOT, "scripts"))
import importlib.util  # noqa: E402

_spec = importlib.util.spec_from_file_location("crs", os.path.join(ROOT, "scripts", "check-strimzi-crs.py"))
crs = importlib.util.module_from_spec(_spec)
_spec.loader.exec_module(crs)

GHA = os.environ.get("GITHUB_ACTIONS") == "true"


def err(msg):
    print(("::error::" if GHA else "ERROR: ") + msg)


# ── Rendering ──────────────────────────────────────────────────────────────

def helm_args(matrix, entry, tmp):
    chart = os.path.join(ROOT, matrix["chart"])
    args = ["helm", "template", entry.get("release", matrix.get("release", "t")), chart,
            "-n", entry.get("namespace", matrix.get("namespace", "default"))]
    base_apis = matrix.get("apiVersions", []) if entry.get("matrixApiVersions", True) else []
    for api in base_apis + entry.get("apiVersions", []):
        args += ["--api-versions", api]
    for f in entry.get("values") or []:
        args += ["-f", os.path.join(chart, f) if not os.path.isabs(f) else f]
    if entry.get("inline"):
        path = os.path.join(tmp, re.sub(r"[^A-Za-z0-9_.-]", "_", entry["name"]) + ".yaml")
        with open(path, "w") as fh:
            fh.write(entry["inline"])
        args += ["-f", path]
    for s in (matrix.get("set") or []) + (entry.get("set") or []):
        args += ["--set", s]
    for s in entry.get("setString") or []:
        args += ["--set-string", s]
    if entry.get("kubeVersion"):
        args += ["--kube-version", str(entry["kubeVersion"])]
    return args


def render(matrix, entry, tmp):
    proc = subprocess.run(helm_args(matrix, entry, tmp), capture_output=True, text=True)
    return proc.returncode, proc.stdout, proc.stderr


# ── Assertions over a render ───────────────────────────────────────────────

def resolve(obj, path):
    """Values at a '/'-separated path; `*` fans out over a list or map, and
    `~1` stands for a '/' inside a key (as in a JSON pointer)."""
    cur = [obj]
    for part in [p.replace("~1", "/").replace("~0", "~") for p in str(path).split("/") if p != ""]:
        nxt = []
        for c in cur:
            if part == "*":
                if isinstance(c, list):
                    nxt.extend(c)
                elif isinstance(c, dict):
                    nxt.extend(c.values())
            elif isinstance(c, dict) and part in c:
                nxt.append(c[part])
            elif isinstance(c, list) and part.isdigit() and int(part) < len(c):
                nxt.append(c[int(part)])
        cur = nxt
    return cur


def select(docs, a):
    out = []
    for d in docs:
        if a.get("kind") and d.get("kind") != a["kind"]:
            continue
        name = (d.get("metadata") or {}).get("name", "")
        if a.get("name") and not re.fullmatch(a["name"], name):
            continue
        out.append(d)
    return out


def run_asserts(docs, asserts):
    failures = []
    for a in asserts or []:
        what = a.get("why") or "%s %s" % (a.get("kind", "*"), a.get("path", ""))
        objs = select(docs, a)
        if "count" in a or "countMin" in a or "countMax" in a:
            n = len(objs)
            lo = a.get("countMin", a.get("count"))
            hi = a.get("countMax", a.get("count"))
            if (lo is not None and n < lo) or (hi is not None and n > hi):
                failures.append("%s: %d %s objects, expected %s..%s" % (what, n, a.get("kind", "*"), lo, hi))
            continue
        if not objs:
            failures.append("%s: no %s object matched" % (what, a.get("kind")))
            continue
        for o in objs:
            vals = resolve(o, a["path"])
            label = "%s/%s %s" % (o.get("kind"), o["metadata"].get("name"), a["path"])
            if "exists" in a and bool(vals) != bool(a["exists"]):
                failures.append("%s: %s %s" % (what, label, "missing" if a["exists"] else "present"))
            if "equals" in a and a["equals"] not in vals:
                failures.append("%s: %s is %r, expected %r" % (what, label, vals, a["equals"]))
            if "notEquals" in a and a["notEquals"] in vals:
                failures.append("%s: %s must not be %r" % (what, label, a["notEquals"]))
            if "matches" in a and not any(re.search(a["matches"], str(v)) for v in vals):
                failures.append("%s: %s=%r does not match /%s/" % (what, label, vals, a["matches"]))
            if "notMatches" in a and any(re.search(a["notMatches"], str(v)) for v in vals):
                failures.append("%s: %s=%r matches /%s/" % (what, label, vals, a["notMatches"]))
    return failures


# ── Named semantic checks ──────────────────────────────────────────────────

def _selects(policy, pod_labels):
    sel = ((policy.get("spec") or {}).get("podSelector") or {})
    ml = sel.get("matchLabels") or {}
    if any(pod_labels.get(k) != v for k, v in ml.items()):
        return False
    for expr in sel.get("matchExpressions") or []:
        val = pod_labels.get(expr["key"])
        op = expr["operator"]
        if op == "In" and val not in expr.get("values", []):
            return False
        if op == "NotIn" and val in expr.get("values", []):
            return False
        if op == "Exists" and val is None:
            return False
        if op == "DoesNotExist" and val is not None:
            return False
    return True


def check_kafka_egress_covers_bootstrap(docs):
    """Every bootstrap port a Connect or MirrorMaker 2 worker dials must be an
    allowed egress port of some NetworkPolicy selecting its pods — when any
    egress policy selects them at all."""
    failures = []
    for d in docs:
        kind = d.get("kind")
        if kind not in ("KafkaConnect", "KafkaMirrorMaker2"):
            continue
        name = d["metadata"]["name"]
        suffix = "connect" if kind == "KafkaConnect" else "mirrormaker2"
        pod_labels = {"strimzi.io/kind": kind, "strimzi.io/name": "%s-%s" % (name, suffix),
                      "strimzi.io/cluster": name}
        spec = d.get("spec") or {}
        boots = []
        if kind == "KafkaConnect":
            boots.append(spec.get("bootstrapServers", ""))
        else:
            boots.append((spec.get("target") or {}).get("bootstrapServers", ""))
            boots += [((m.get("source") or {}).get("bootstrapServers", "")) for m in spec.get("mirrors") or []]
        ports = set()
        for b in boots:
            for hp in str(b).split(","):
                if ":" in hp:
                    ports.add(int(hp.rsplit(":", 1)[1]))
        policies = [p for p in docs if p.get("kind") == "NetworkPolicy"
                    and "Egress" in ((p.get("spec") or {}).get("policyTypes") or [])
                    and _selects(p, pod_labels)]
        if not policies:
            continue
        allowed, any_port = set(), False
        for p in policies:
            for rule in (p["spec"].get("egress") or []):
                if not rule.get("ports"):
                    any_port = True
                for port in rule.get("ports") or []:
                    if isinstance(port.get("port"), int):
                        allowed.add(port["port"])
        if any_port:
            continue
        for port in sorted(ports - allowed):
            failures.append("%s/%s dials Kafka on :%d but its egress policies allow only %s"
                            % (kind, name, port, sorted(allowed)))
    return failures


def _slug(heading):
    """GitHub's heading anchor: lower case, punctuation dropped, spaces to '-'."""
    h = re.sub(r"[`*_]", "", heading.strip().lower())
    h = re.sub(r"[^\w\- ]", "", h)
    return h.replace(" ", "-")


def check_runbook_anchors(docs):
    """Every alert's runbook_url that points into this repository's docs must
    name a file that exists and a heading that file has."""
    failures, cache = [], {}
    for d in docs:
        if d.get("kind") != "PrometheusRule":
            continue
        for group in (d.get("spec") or {}).get("groups") or []:
            for rule in group.get("rules") or []:
                url = ((rule.get("annotations") or {}).get("runbook_url") or "")
                m = re.search(r"/blob/[^/]+/(docs/[^#]+\.md)#(.+)$", url)
                if not m:
                    if "alert" in rule and "/docs/" not in url:
                        failures.append("alert %s has no runbook_url into docs/" % rule["alert"])
                    continue
                path, anchor = m.group(1), m.group(2)
                if path not in cache:
                    full = os.path.join(ROOT, path)
                    if not os.path.exists(full):
                        cache[path] = None
                    else:
                        with open(full) as fh:
                            cache[path] = {_slug(l.lstrip("#")) for l in fh if re.match(r"^#{1,6} ", l)}
                if cache[path] is None:
                    failures.append("alert %s: runbook %s does not exist" % (rule.get("alert"), path))
                elif anchor not in cache[path]:
                    failures.append("alert %s: %s has no heading for #%s" % (rule.get("alert"), path, anchor))
    return failures


CHECKS = {
    "kafka-egress-covers-bootstrap": check_kafka_egress_covers_bootstrap,
    "runbook-anchors": check_runbook_anchors,
}


# ── Driver ─────────────────────────────────────────────────────────────────

def kubeconform(stream):
    proc = subprocess.run(
        ["kubeconform", "-strict", "-summary", "-ignore-missing-schemas", "-schema-location", "default"],
        input=stream, capture_output=True, text=True)
    return proc.returncode, (proc.stdout + proc.stderr).strip()


def promtool(matrix):
    """Every PrometheusRule the chart can render must be PromQL the pinned
    Prometheus accepts: a rule with a typo installs and simply never fires."""
    entry = dict(matrix.get("rules") or {})
    entry.setdefault("name", "rules")
    with tempfile.TemporaryDirectory() as tmp:
        code, out, stderr = render(matrix, entry, tmp)
        if code != 0:
            err("rules render failed: %s" % stderr.strip())
            return 1
        rc, n = 0, 0
        for doc, _ in crs.load_documents(out):
            if doc.get("kind") != "PrometheusRule":
                continue
            n += 1
            path = os.path.join(tmp, "rules-%d.yaml" % n)
            with open(path, "w") as fh:
                yaml.safe_dump({"groups": doc["spec"]["groups"]}, fh)
            proc = subprocess.run(["promtool", "check", "rules", path], capture_output=True, text=True)
            print((proc.stdout + proc.stderr).strip())
            rc |= proc.returncode
        if n == 0:
            err("the `rules` render produced no PrometheusRule")
            return 1
        return 1 if rc else 0


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("chart")
    ap.add_argument("--offline", action="store_true", help="skip kubeconform (it downloads schemas)")
    ap.add_argument("--only", help="run only renders/rails whose name contains this")
    ap.add_argument("--list", action="store_true")
    ap.add_argument("--crds", help="CRD source for check-strimzi-crs.py")
    ap.add_argument("--promtool", action="store_true",
                    help="only run `promtool check rules` over the matrix's `rules` render")
    args = ap.parse_args()

    path = os.path.join(ROOT, "scripts", "chart-matrix", args.chart + ".yaml")
    if not os.path.exists(path):
        sys.stderr.write("no matrix for %s (expected %s)\n" % (args.chart, path))
        return 2
    with open(path) as fh:
        matrix = yaml.safe_load(fh)
    if args.list:
        for r in matrix.get("renders") or []:
            print("render %s%s" % (r["name"], "  [known %s]" % r["known"] if r.get("known") else ""))
        for r in matrix.get("rails") or []:
            print("rail   %s" % r["name"])
        return 0

    if args.promtool:
        return promtool(matrix)

    schemas = crs.load_schemas(args.crds)
    use_kc = not args.offline and shutil.which("kubeconform")
    if not args.offline and not use_kc:
        print("NOTE: kubeconform not on PATH — built-in kinds are not schema-checked in this run")

    rc = 0
    with tempfile.TemporaryDirectory() as tmp:
        for entry in matrix.get("renders") or []:
            if args.only and args.only not in entry["name"]:
                continue
            label = "%s[%s]" % (args.chart, entry["name"])
            code, out, stderr = render(matrix, entry, tmp)
            if code != 0:
                findings = ["%s helm template failed: %s" % (label, stderr.strip())]
            else:
                findings, _ = crs.check(out, schemas, label)
                docs = [d for d, _ in crs.load_documents(out)]
                findings += ["%s %s" % (label, f) for f in run_asserts(docs, entry.get("assert"))]
                for name in (matrix.get("checks") or []) + (entry.get("checks") or []):
                    findings += ["%s %s" % (label, f) for f in CHECKS[name](docs)]
                if use_kc:
                    kc_rc, kc_out = kubeconform(out)
                    if kc_rc != 0:
                        findings.append("%s kubeconform: %s" % (label, kc_out))
            known = entry.get("known")
            if known:
                if findings:
                    print("XFAIL %s (known %s): %s" % (label, known, findings[0]))
                else:
                    rc = 1
                    err("%s is marked known %s but now passes — remove the marker" % (label, known))
                continue
            if findings:
                rc = 1
                for f in findings:
                    err(f)
            else:
                print("OK    %s" % label)

        for rail in matrix.get("rails") or []:
            if args.only and args.only not in rail["name"]:
                continue
            label = "%s rail: %s" % (args.chart, rail["name"])
            code, out, stderr = render(matrix, rail, tmp)
            if code == 0:
                rc = 1
                err("%s — the chart accepted it, so the rail is not working" % label)
            elif rail["want"] not in stderr:
                rc = 1
                err("%s — rejected for the WRONG reason; expected %r in:\n%s"
                    % (label, rail["want"], stderr.strip()))
            else:
                print("OK    %s (refused)" % label)
    return rc


if __name__ == "__main__":
    sys.exit(main())

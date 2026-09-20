#!/usr/bin/env python3
"""Build a /metrics exposition that SATISFIES each board query's own matchers.

The fixture for scripts/check-grafana-compat.sh. The point is not realistic
VALUES — it is realistic NAMES and LABELS, read out of the boards themselves:
`quantile="0.99"`, `topic=""`, `pod=~"kates-.*"`. A generator that guessed
label values and then asked whether the panels matched them would conflate "the
board is wrong" with "the generator guessed differently", so this one reads the
matchers and emits series that satisfy them. What still draws nothing is then a
board problem rather than a fixture one.

Two details that were wrong in an earlier draft and are worth keeping in mind:

  * `container!=""` needs the label PRESENT and non-empty. An absent label
    equals "" in PromQL, so leaving it off does not satisfy the matcher.
  * a literal dot that comes out of a `[.]` character class must stay a dot.
    Collapsing every `.` to `x` turned `source[.].*` into `sourcexx`, which the
    matcher rightly refused.
"""
import json
import pathlib
import random
import re
import sys

ROOT = pathlib.Path(__file__).resolve().parent.parent.parent
BOARDS = ROOT / "dashboards"
sys.path.insert(0, str(ROOT / "scripts" / "metric-contract"))
import contract as C  # noqa: E402

DEFAULT_VAR = {
    "namespace": "kafka", "cluster": "krafter", "job": "kates", "topic": "orders",
    "source": "east", "pods": "krafter-brokers-0", "connector": "orders-source",
    "run_id": "run-a", "test_type": "LOAD", "consumergroup": "kates-consumers",
    "pod": "kates-0", "db_pod": "postgresql-0", "operator_deployment": "litmus",
    "instance": "10.244.0.5:9404", "node": "kind-control-plane",
    "kubernetes_pod_name": "krafter-brokers-0", "zone": "alpha",
    "strimzi_io_cluster": "krafter", "strimzi_io_name": "krafter-kafka",
    "partition": "0", "quantile": "0.99", "phase": "produce", "container": "kafka",
}


def from_regex(pattern):
    if not pattern:
        return ""
    p = pattern
    for a, b in [(r"\.\*", "x"), (r"\.\+", "x"), (r"\[0-9\]\+", "0"),
                 (r"\\\.", "."), (r"\(\?i\)", "")]:
        p = re.sub(a, b, p)
    if "|" in p:
        p = p.split("|")[0]
    p = p.strip("^$").replace("(", "").replace(")", "")
    p = re.sub(r"\[([^\]])[^\]]*\]", r"\1", p)   # [.] -> a literal .
    p = p.replace(".*", "x").replace(".+", "x")
    # NOT `.replace(".", "x")`: by this point a surviving dot is a LITERAL one
    # that came out of a [.] class, and turning it into an x produced
    # `sourcexx` for `source[.].*` — a value the matcher rightly refused.
    return re.sub(r"[+*?]", "", p) or "x"


def var_values(dash):
    vals = {}
    for v in (dash.get("templating") or {}).get("list") or []:
        n, kind = v.get("name"), v.get("type")
        if kind == "constant":
            vals[n] = str(v.get("query", ""))
        elif kind == "custom":
            opts = [o.strip() for o in str(v.get("query", "")).split(",") if o.strip()]
            vals[n] = opts[0] if opts else "x"
        elif kind == "datasource":
            vals[n] = "prometheus"
        else:
            vals[n] = DEFAULT_VAR.get(n, "x")
    return vals


def main():
    required = {}
    seen = set()
    for board in sorted(BOARDS.glob("*/dashboard.json")):
        dash = json.loads(board.read_text())
        vals = var_values(dash)

        def subst(s):
            return re.sub(
                r"\$\{([A-Za-z_][A-Za-z0-9_]*)(?::[^}]*)?\}|\$([A-Za-z_][A-Za-z0-9_]*)",
                lambda m: vals.get(m.group(1) or m.group(2),
                                   DEFAULT_VAR.get(m.group(1) or m.group(2), "x")), s)

        exprs = []
        for _, e in C._dashboard_exprs(dash):
            exprs.append(subst(e))

        for e in exprs:
            for m in re.finditer(r"\b([a-zA-Z_:][a-zA-Z0-9_:]*)\s*\{([^}]*)\}", e):
                name, body = m.group(1), m.group(2)
                if ":" in name:
                    continue
                seen.add(name)
                labels = {}
                for part in re.finditer(
                        r'([A-Za-z_][A-Za-z0-9_]*)\s*(=~|!~|!=|=)\s*"([^"]*)"', body):
                    lab, op, val = part.groups()
                    if op == "=":
                        labels[lab] = val
                    elif op == "=~":
                        labels[lab] = from_regex(val)
                    elif op == "!=" and val == "":
                        # An absent label equals "" in PromQL, so label!=""
                        # requires the label to be PRESENT and non-empty.
                        labels.setdefault(lab, DEFAULT_VAR.get(lab, "v1"))
                required.setdefault(name, []).append(labels)
            for n in C.metric_names(e):
                if ":" in n:
                    continue
                seen.add(n)
                required.setdefault(n, [])
            for m in re.finditer(r"\bby\s*\(([^)]*)\)", e):
                labs = [x.strip() for x in m.group(1).split(",") if x.strip()]
                for n in C.metric_names(e):
                    if ":" in n:
                        continue
                    combos = required.setdefault(n, [])
                    if not combos:
                        combos.append({})
                    for combo in combos:
                        for lab in labs:
                            # setdefault, never overwrite: a matcher that
                            # pinned topic=~"source[.].*" must win over a
                            # by(topic) default of "orders".
                            combo.setdefault(lab, DEFAULT_VAR.get(lab, "v1"))

        # A label a label_values() variable EXTRACTS must be ON the series.
        for v in (dash.get("templating") or {}).get("list") or []:
            if v.get("type") != "query":
                continue
            q = v.get("query")
            if isinstance(q, dict):
                q = q.get("query")
            m = re.fullmatch(r"label_values\((.*),\s*([A-Za-z_][A-Za-z0-9_]*)\s*\)",
                             str(q or "").strip(), re.S)
            if not m:
                continue
            selector, label = subst(m.group(1)), m.group(2)
            value = DEFAULT_VAR.get(label, "v1")
            for n in C.metric_names(selector):
                if ":" in n or n not in required:
                    continue
                if not required[n]:
                    required[n] = [{}]
                for combo in required[n]:
                    combo.setdefault(label, value)

    lines, rnd = [], random.Random(11)
    scope = {"node_name": "kind-control-plane"}
    pods = [("krafter-brokers-0", "alpha"), ("krafter-brokers-1", "sigma")]
    for name in sorted(seen):
        combos = required.get(name) or [{}]
        uniq = []
        for c in combos:
            if c not in uniq:
                uniq.append(c)
        for combo in uniq[:40]:
            for pod, zone in pods:
                lab = dict(scope)
                if name.startswith(("kafka_", "jvm_", "process_", "jmx_")):
                    lab.setdefault("kubernetes_pod_name", pod)
                    lab.setdefault("zone", zone)
                for k, v in combo.items():
                    if v == "":
                        lab.pop(k, None)     # topic="" means the label is absent
                    else:
                        lab[k] = v
                rendered = ",".join('%s="%s"' % (k, v)
                                    for k, v in sorted(lab.items()) if v != "")
                val = rnd.uniform(1e3, 1e6) if name.endswith("_total") else rnd.uniform(0, 100)
                lines.append("%s{%s} %.3f" % (name, rendered, val))
                if not name.startswith(("kafka_", "jvm_", "process_")):
                    break

    if len(sys.argv) < 2:
        sys.exit("usage: fixture.py <output-file>")
    out = pathlib.Path(sys.argv[1])
    out.write_text("\n".join(sorted(set(lines))) + "\n")
    print("%d names -> %d series -> %s" % (len(seen), len(set(lines)), out))
    return 0


if __name__ == "__main__":
    sys.exit(main())

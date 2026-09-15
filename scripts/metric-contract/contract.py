#!/usr/bin/env python3
"""The metric contract check behind scripts/check-metric-contract.sh.

Given a chart's contract file (scripts/metric-contract/<chart>.yaml) and one
or more rendered manifests, this answers: can every series the chart's alerts,
recording rules and dashboard read actually be produced?

"Can be produced" is decided the way the JMX exporter decides it. The
exporter walks every MBean attribute the JVM exposes, builds the string

    domain<key=value, key=value><>attribute: value

and hands it to the rules in order; the first whose `pattern` matches wins,
and the series is named by that rule's `name` with `$N` substituted from the
match, non-[a-zA-Z0-9_:] characters rewritten to `_`, runs of `_` collapsed,
and the result lowercased under `lowercaseOutputName`. This module runs that
same procedure over the MBean catalogue in the contract file, which is how it
can say that `kafka_server_brokertopicmetrics_bytesinpersec` is a name no rule
produces — the family exists, but a rule matching `<>Value` never sees a Meter.

With `--scrape FILE`, a real /metrics capture is compared too: every reference
must be in it, and the catalogue is diffed against it in both directions so
that what the catalogue does not know about is reported rather than assumed.

Exit status: 0 when every reference is producible (and, with --scrape,
observed); 1 otherwise. Diagnostics go to stdout in the repo's OK:/STALE: house
style so the output reads the same locally and in a workflow log.
"""

import argparse
import difflib
import json
import re
import sys

import yaml

# ── PromQL name extraction ─────────────────────────────────────────────────
#
# A metric name is an identifier that is not a function call, not a keyword,
# not a label (those live inside {...}, which is removed first), and not a
# grouping-clause member (`by (topic)` is removed too). This is a tokenizer,
# not a parser; it is enough for the PromQL in this repository and errs on
# the side of reporting an identifier rather than swallowing one.

_KEYWORDS = {
    "by", "without", "on", "ignoring", "group_left", "group_right", "bool",
    "and", "or", "unless", "offset", "nan", "inf", "NaN", "Inf", "at", "start",
    "end",
}
_IDENT = re.compile(r"(?<![0-9.$\w:])([a-zA-Z_:][a-zA-Z0-9_:]*)")


def metric_names(expr):
    """Return the set of metric names an expression reads."""
    s = expr
    s = re.sub(r'"(?:\\.|[^"\\])*"', '""', s)           # string literals
    s = re.sub(r"'(?:\\.|[^'\\])*'", "''", s)
    s = re.sub(r"\{[^}]*\}", "", s)                     # label matchers
    s = re.sub(r"\[[^\]]*\]", "", s)                    # ranges, subqueries
    s = re.sub(r"\$\{[^}]*\}|\$[A-Za-z_][A-Za-z0-9_]*", "", s)  # Grafana variables
    s = re.sub(r"\b(by|without|on|ignoring|group_left|group_right)\s*\([^)]*\)",
               " ", s)                                  # grouping clauses
    s = re.sub(r"[a-zA-Z_][a-zA-Z0-9_]*\s*\(", " (", s)  # function calls
    names = set()
    for m in _IDENT.finditer(s):
        ident = m.group(1)
        if ident in _KEYWORDS:
            continue
        names.add(ident)
    return names


# ── The rendered chart ─────────────────────────────────────────────────────

def load_render(path):
    """Split one rendered manifest into exporter rules, PromQL references and
    recording-rule names."""
    rules, refs, records = None, {}, set()
    with open(path) as fh:
        docs = [d for d in yaml.safe_load_all(fh) if d]
    for doc in docs:
        kind = doc.get("kind")
        meta = doc.get("metadata") or {}
        if kind == "ConfigMap":
            for name, body in (doc.get("data") or {}).items():
                if name.endswith((".yml", ".yaml")) and "pattern:" in body:
                    cfg = yaml.safe_load(body) or {}
                    if isinstance(cfg.get("rules"), list):
                        if rules is not None:
                            sys.exit("::error::%s: more than one exporter config in the render" % path)
                        rules = {"lowercase": bool(cfg.get("lowercaseOutputName")),
                                 "snake": bool(cfg.get("attrNameSnakeCase")),
                                 "rules": cfg["rules"], "source": "%s/%s" % (meta.get("name"), name)}
                elif name.endswith(".json"):
                    dash = json.loads(body)
                    for panel in _panels(dash.get("panels") or []):
                        for target in panel.get("targets") or []:
                            expr = target.get("expr")
                            if not expr:
                                continue
                            where = "panel %r" % panel.get("title", "?")
                            for n in metric_names(expr):
                                refs.setdefault(n, set()).add(where)
        elif kind == "PrometheusRule":
            for group in (doc.get("spec") or {}).get("groups") or []:
                for rule in group.get("rules") or []:
                    if "record" in rule:
                        records.add(rule["record"])
                        where = "record %s" % rule["record"]
                    else:
                        where = "alert %s" % rule.get("alert", "?")
                    for n in metric_names(str(rule.get("expr", ""))):
                        refs.setdefault(n, set()).add(where)
    if rules is None:
        sys.exit("::error::%s: no JMX exporter rules ConfigMap in the render "
                 "(is metrics.enabled set for this render?)" % path)
    return rules, refs, records


def _panels(panels):
    for p in panels:
        yield p
        for q in _panels(p.get("panels") or []):
            yield q


# ── The exporter, simulated ────────────────────────────────────────────────

_UNSAFE = re.compile(r"[^a-zA-Z0-9:_]")
_UNDERSCORES = re.compile(r"__+")


def safe_name(s):
    return _UNDERSCORES.sub("_", _UNSAFE.sub("_", s))


def snake(s):
    return re.sub(r"([a-z0-9])([A-Z])", r"\1_\2", s).lower()


def _substitute(template, match):
    # Java's Matcher.replaceAll semantics for $N, which is all the rules use.
    def repl(m):
        n = int(m.group(1))
        return match.group(n) if n <= (match.re.groups or 0) and match.group(n) is not None else ""
    return re.sub(r"\$(\d+)", repl, template)


class Exporter:
    def __init__(self, cfg):
        self.lowercase = cfg["lowercase"]
        self.snake = cfg["snake"]
        self.rules = []
        for i, rule in enumerate(cfg["rules"]):
            pattern = rule.get("pattern")
            if pattern is None:
                regex = None  # a rule with no pattern matches everything
            else:
                try:
                    regex = re.compile("^.*(?:" + str(pattern) + ").*$")
                except re.error as exc:
                    sys.exit("::error::rule %d pattern does not compile: %s" % (i, exc))
            self.rules.append((i, regex, rule))

    def publish(self, bean, attr, value):
        """The series name the exporter would give one (bean, attribute), or
        None with the reason."""
        domain, props = bean.split("<", 1)
        props = props.rstrip(">")
        match_name = "%s<%s><>%s: %s" % (domain, props, attr, value)
        for i, regex, rule in self.rules:
            m = regex.match(match_name) if regex else None
            if regex and not m:
                continue
            if isinstance(value, str) and rule.get("value") is None:
                return None, "rule %d matches but the attribute is a string and the rule sets no value:" % i
            if rule.get("name"):
                name = _substitute(str(rule["name"]), m) if m else str(rule["name"])
            else:
                first = props.split(",", 1)[0].split("=", 1)[-1] if props else ""
                a = snake(attr) if self.snake else attr
                name = "_".join(x for x in (domain, first, a) if x)
            name = safe_name(name)
            if self.lowercase:
                name = name.lower()
            return self.suffix(name, rule), i
        return None, "no rule matches"

    def labels(self, bean, attr, value):
        """The label values a rule attaches to one (bean, attribute), as the
        exporter would write them — a capture that swallowed Kafka's quotes
        shows up here as a value with a quote at either end."""
        domain, props = bean.split("<", 1)
        props = props.rstrip(">")
        match_name = "%s<%s><>%s: %s" % (domain, props, attr, value)
        for i, regex, rule in self.rules:
            m = regex.match(match_name) if regex else None
            if regex and not m:
                continue
            out = {}
            for k, v in (rule.get("labels") or {}).items():
                out[str(k)] = _substitute(str(v), m) if m else str(v)
            return i, out
        return None, {}

    @staticmethod
    def suffix(name, rule):
        """The exporter's 1.x line (Prometheus client_java 1.x underneath)
        reserves `_total` for counters: a COUNTER is always exposed with it
        and anything else has it stripped. So a GAUGE rule named `..._total`
        publishes `...` — which is how four MirrorMaker 2 alerts came to read
        names that did not exist."""
        is_counter = str(rule.get("type", "")).upper() == "COUNTER"
        if name.endswith("_total"):
            return name if is_counter else name[: -len("_total")]
        return name + "_total" if is_counter else name

    def families(self):
        """(rule index, template, regex) for every named rule — used only to
        explain a miss."""
        out = []
        for i, _, rule in self.rules:
            tmpl = rule.get("name")
            if not tmpl:
                continue
            parts = re.split(r"\$\d+", str(tmpl))
            regex = "[a-z0-9_]+".join(re.escape(safe_name(p).lower()) for p in parts)
            if regex.endswith("_total"):
                regex = regex[: -len("_total")]
            out.append((i, str(tmpl), re.compile("^" + regex + "(_total)?$")))
        return out


def producible(exporter, mbeans):
    """name -> list of (bean, attribute, rule index). Names that come only
    from `optional` attributes are also returned in the second set: valid to
    reference, not expected in every scrape (transactional metrics exist only
    under exactly-once, for instance)."""
    out, optional = {}, set()
    for entry in mbeans:
        bean = entry["bean"]
        for attr in entry.get("attributes") or []:
            name, why = exporter.publish(bean, attr, 1.0)
            if name:
                out.setdefault(name, []).append((bean, attr, why))
        for attr in entry.get("strings") or []:
            name, why = exporter.publish(bean, attr, "RUNNING")
            if name:
                out.setdefault(name, []).append((bean, attr, why))
        for attr in entry.get("optional") or []:
            name, why = exporter.publish(bean, attr, 1.0)
            if name:
                out.setdefault(name, []).append((bean, attr, why))
                optional.add(name)
    return out, optional


def related_beans(exporter, mbeans, name):
    """Catalogued beans that look like what a missing reference meant, with
    what the rules make of each of their attributes — so a miss reads as
    "the bean is there, its attributes are Count and OneMinuteRate, and the
    rule wants Value" rather than as a bare name."""
    out = []
    for entry in mbeans:
        bean = entry["bean"]
        props = bean.split("<", 1)[1].rstrip(">")
        values = [safe_name(p.split("=", 1)[-1].strip()).lower() for p in props.split(",")]
        if not any(len(v) > 3 and v in name for v in values):
            continue
        verdicts = []
        for attr, value in [(a, 1.0) for a in (entry.get("attributes") or []) + (entry.get("optional") or [])] + \
                           [(a, "x") for a in entry.get("strings") or []]:
            produced, why = exporter.publish(bean, attr, value)
            verdicts.append("%s -> %s" % (attr, produced if produced else why))
        out.append((bean, verdicts))
    return out


# ── The scrape ─────────────────────────────────────────────────────────────

def load_scrape(path):
    names = set()
    with open(path) as fh:
        for line in fh:
            line = line.strip()
            if not line:
                continue
            if line.startswith("#"):
                m = re.match(r"# (?:TYPE|HELP) (\S+)", line)
                if m:
                    names.add(m.group(1))
                continue
            names.add(re.split(r"[{ ]", line, 1)[0])
    return names


# ── Main ───────────────────────────────────────────────────────────────────

def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("contract")
    ap.add_argument("renders", nargs="+", help="name=path of each rendered manifest")
    ap.add_argument("--scrape", help="a /metrics capture to compare against")
    ap.add_argument("--quiet", action="store_true")
    args = ap.parse_args()

    with open(args.contract) as fh:
        contract = yaml.safe_load(fh)
    builtin = [re.compile(p) for p in contract.get("builtin") or []]
    scrape_exempt = [re.compile(p) for p in ("^up$", "^scrape_")]
    alternatives = [set(a) for a in contract.get("alternatives") or []]
    mbeans = contract.get("mbeans") or []

    rc = 0
    all_refs, all_records, all_names, all_optional = {}, set(), {}, set()
    quoted_reported = set()
    exporter = None
    for spec in args.renders:
        label, path = spec.split("=", 1)
        cfg, refs, records = load_render(path)
        exporter = Exporter(cfg)
        names, optional = producible(exporter, mbeans)
        all_names.update(names)
        all_optional |= optional
        all_records |= records
        for n, where in refs.items():
            all_refs.setdefault(n, set()).update("%s [%s]" % (w, label) for w in where)

        # A label value wrapped in quotes is Kafka's ObjectName quoting
        # leaking through a `([^,]+)` capture. Nothing selecting on that
        # label (`connector=~"east->.*"`) can match it, so it is an error
        # even though every NAME checks out.
        for entry in mbeans:
            for attr in (entry.get("attributes") or [])[:1] + (entry.get("strings") or [])[:1]:
                i, labels = exporter.labels(entry["bean"], attr, 1.0)
                for k, v in labels.items():
                    if (v.startswith('"') or v.endswith('"')) and (i, k) not in quoted_reported:
                        quoted_reported.add((i, k))
                        rc = 1
                        print("QUOTED LABEL: rule %d writes %s=%s — Kafka's quotes are inside the capture; "
                              "match them outside it (\\\"?([^,\\\"]+)\\\"?) or no selector on %s will match"
                              % (i, k, v, k))

        missing = []
        for n in sorted(refs):
            if n in names or n in records or any(b.match(n) for b in builtin):
                continue
            missing.append(n)
        if not args.quiet or missing:
            print("==> %s: %d references, %d producible series from %d rules and %d catalogued beans"
                  % (label, len(refs), len(names), len(cfg["rules"]), len(mbeans)))
        families = exporter.families()
        for n in missing:
            rc = 1
            print("MISSING: %s" % n)
            for w in sorted(refs[n]):
                print("    read by %s" % w)
            fam = [(i, t) for i, t, rx in families if rx.match(n)]
            if fam:
                for i, t in fam:
                    print("    rule %d would name it (%s) but no catalogued attribute reaches that rule"
                          % (i, t))
            close = difflib.get_close_matches(n, list(names) + sorted(records), n=3, cutoff=0.75)
            if close:
                print("    did you mean: %s" % ", ".join(close))
            for bean, verdicts in related_beans(exporter, mbeans, n):
                print("    %s" % bean)
                for v in verdicts:
                    print("        %s" % v)

    if rc == 0:
        print("OK: every series the %s alerts, recording rules and dashboard read is one the rules produce (%d references)"
              % (contract.get("chart", "chart"), len(all_refs)))

    if not args.scrape:
        return rc

    # ── Against a live capture ──────────────────────────────────────────────
    observed = load_scrape(args.scrape)
    print("==> scrape %s: %d series" % (args.scrape, len(observed)))

    satisfied = set()
    for alt in alternatives:
        if alt & observed:
            satisfied |= alt
    unseen = []
    for n in sorted(all_refs):
        if n in observed or n in satisfied or n in all_records:
            continue
        if any(e.match(n) for e in scrape_exempt):
            continue
        unseen.append(n)
    for n in unseen:
        rc = 1
        print("MISSING IN SCRAPE: %s" % n)
        for w in sorted(all_refs[n]):
            print("    read by %s" % w)
        close = difflib.get_close_matches(n, sorted(observed), n=3, cutoff=0.75)
        if close:
            print("    the scrape has: %s" % ", ".join(close))
    if not unseen:
        print("OK: every referenced series was observed in the scrape")

    catalogued_unseen = sorted(n for n in all_names if n not in observed and n not in all_optional)
    uncatalogued = sorted(n for n in observed
                          if n not in all_names and not any(b.match(n) for b in builtin))
    if catalogued_unseen:
        print("NOTE: %d catalogued series not in this scrape (conditional, or the catalogue is wrong):"
              % len(catalogued_unseen))
        for n in catalogued_unseen:
            print("    %s" % n)
    if uncatalogued:
        # Grouped by the rule that produced them, in rule order, so a broad
        # catch-all reads as one line with a count rather than a hundred
        # names — and a series that NO rule explains is listed in full,
        # because that is the one worth a look.
        by_rule, orphans = {}, []
        families = exporter.families()
        for n in uncatalogued:
            for i, tmpl, rx in families:
                if rx.match(n):
                    by_rule.setdefault((i, tmpl), []).append(n)
                    break
            else:
                orphans.append(n)
        print("NOTE: %d observed series the catalogue does not describe:" % len(uncatalogued))
        for (i, tmpl), names_ in sorted(by_rule.items()):
            print("    rule %d (%s): %d series, e.g. %s" % (i, tmpl, len(names_), ", ".join(names_[:3])))
        for n in orphans:
            print("    %s  (no rule explains this name)" % n)
    return rc


if __name__ == "__main__":
    sys.exit(main())

#!/usr/bin/env python3
"""Fail when a chart's packaged content changed but its version did not.

publish-charts.yml never pushes a version that is already in the registry, so
a change merged without a new Chart.yaml version is never published, and
nothing says so. This holds every PR to two rules, against its merge base:

  1. A chart whose packaged files changed carries a new, higher version.
     "Packaged" means what `helm package` puts in the archive: files the
     chart's .helmignore excludes (unit tests, values-*.yaml overlays) do not
     count, so a tests-only change needs no bump. Nor do README.md and docs/:
     nothing an install reads, they ship with the chart's next version.
  2. A chart that packs a file:// subchart whose version moved carries a new
     version too. The umbrella vendors kafka-cluster, kates, apicurio-registry,
     mirror-maker2 and connect-cluster as they are on disk, and the Strimzi
     charts vendor kafka-common: without a bump of its own, the published
     umbrella would keep the old subcharts.

A chart new since the merge base needs no bump. Uncommitted changes count, so
it can run before a commit.

Usage: scripts/chart_bumps.py [BASE]   (BASE defaults to origin/main)
Tests: python3 -m unittest discover -s scripts -p 'test_chart_bumps.py'
"""

import fnmatch
import re
import subprocess
import sys
from pathlib import Path


def git(*args: str) -> str:
    return subprocess.run(["git", *args], check=True, capture_output=True, text=True).stdout


def parse_helmignore(text: str) -> list:
    """The rules as (pattern, dir_only, anchored), in helm's pkg/ignore terms."""
    rules = []
    for line in text.splitlines():
        line = line.strip()
        if not line or line.startswith("#"):
            continue
        # Helm's negation semantics are unusual; refuse rather than guess.
        if line.startswith("!"):
            raise ValueError(f"negated .helmignore rule not supported: {line}")
        dir_only = line.endswith("/")
        line = line.rstrip("/")
        anchored = line.startswith("/")
        rules.append((line.lstrip("/"), dir_only, anchored or "/" in line))
    return rules


def _match(pattern: str, name: str) -> bool:
    # Go's filepath.Match: `*` never crosses a `/`, so match segment by segment.
    p, n = pattern.split("/"), name.split("/")
    return len(p) == len(n) and all(fnmatch.fnmatchcase(a, b) for a, b in zip(n, p))


def _rule_matches(rule: tuple, path: str, is_dir: bool) -> bool:
    pattern, dir_only, structural = rule
    if dir_only and not is_dir:
        return False
    # A rule without a slash looks at the last path element only.
    return _match(pattern, path) if structural else _match(pattern, path.rsplit("/", 1)[-1])


def ignored(path: str, rules: list) -> bool:
    """Whether `helm package` leaves out `path` (relative to the chart root)."""
    parts = path.split("/")
    for i in range(1, len(parts)):
        if any(_rule_matches(r, "/".join(parts[:i]), True) for r in rules):
            return True
    return any(_rule_matches(r, path, False) for r in rules)


def is_docs(path: str) -> bool:
    # Packaged, but nothing an install reads: they ship with the next version.
    return path == "README.md" or path.startswith("docs/")


def chart_version(chart_yaml: str) -> str:
    m = re.search(r'^version:\s*"?([^"\s]+)"?', chart_yaml, re.M)
    if not m:
        raise ValueError("Chart.yaml has no top-level version")
    return m.group(1)


def file_dependencies(chart_yaml: str) -> list:
    """The chart directories its file://../<dir> dependencies point at."""
    return re.findall(r'^\s*repository:\s*"?file://\.\./([^"\s/]+)', chart_yaml, re.M)


def semver_key(version: str) -> tuple:
    core, _, pre = version.partition("-")
    nums = tuple(int(x) for x in core.split("+")[0].split("."))
    # A pre-release sorts before its release.
    return nums, (0, pre) if pre else (1, "")


def problems(base: str) -> list:
    base = git("merge-base", base, "HEAD").strip()
    charts = sorted(str(Path(p).parent) for p in git("ls-files", ":(glob)charts/*/Chart.yaml").split())

    def at_base(path: str):
        try:
            return git("show", f"{base}:{path}")
        except subprocess.CalledProcessError:
            return None

    found = []
    for chart in charts:
        before = at_base(f"{chart}/Chart.yaml")
        if before is None:
            continue
        now = Path(chart, "Chart.yaml").read_text()
        old, new = chart_version(before), chart_version(now)
        name = Path(chart).name

        if old != new:
            if semver_key(new) <= semver_key(old):
                found.append(f"{name}: the version went from {old} to {new}; it must go up")
            continue

        helmignore = Path(chart, ".helmignore")
        rules = parse_helmignore(helmignore.read_text()) if helmignore.exists() else []
        paths = [f[len(chart) + 1 :] for f in git("diff", "--name-only", base, "--", f"{chart}/").split()]
        changed = [p for p in paths if not ignored(p, rules) and not is_docs(p)]

        reasons = []
        if changed:
            more = f" and {len(changed) - 5} more" if len(changed) > 5 else ""
            reasons.append(", ".join(changed[:5]) + more + " changed")
        for dep in file_dependencies(now):
            dep_before = at_base(f"charts/{dep}/Chart.yaml")
            dep_now = Path("charts", dep, "Chart.yaml")
            if dep_before is None or not dep_now.exists():
                continue
            dep_old, dep_new = chart_version(dep_before), chart_version(dep_now.read_text())
            if dep_old != dep_new:
                reasons.append(f"it packs {dep}, which moved from {dep_old} to {dep_new}")
        if reasons:
            found.append(f"{name}: {'; '.join(reasons)}, but the version is still {new}. Bump {chart}/Chart.yaml")
    return found


def main() -> int:
    base = sys.argv[1] if len(sys.argv) > 1 else "origin/main"
    found = problems(base)
    for p in found:
        print(f"::error::{p}")
    if found:
        print(f"\n{len(found)} chart(s) need a new version: a changed chart without one is never published.")
        return 1
    print("OK: every changed chart carries a new version")
    return 0


if __name__ == "__main__":
    sys.exit(main())

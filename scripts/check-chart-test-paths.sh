#!/usr/bin/env bash
# Assert every /api path a chart test calls actually exists in the backend.
#
# WHY THIS EXISTS: charts/kates/templates/tests/test-kafka.yaml asked for
# /api/cluster, which is a class-level @Path whose methods all live under
# sub-paths, so the API answered 404 for the entire life of that test. It never
# noticed: it treated anything that was not 200 or 000 as "Kafka may be
# unreachable" and exited 0. A test that always passes is worse than no test —
# it is a green tick that means nothing.
#
# Nothing else catches this. Helm lint sees valid YAML, the pod runs, it exits
# 0, `helm test` reports success. Only comparing the paths against the JAX-RS
# resources shows that the two have drifted.
#
# Usage:
#   scripts/check-chart-test-paths.sh
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT_DIR="${SCRIPT_DIR}/.."
source "${SCRIPT_DIR}/common.sh"

cd "${ROOT_DIR}"
require_cmd python3

python3 - <<'PYTHON'
import glob
import re
import sys

SRC_GLOB = "kates/src/main/java/**/*.java"
TEST_GLOB = "charts/*/templates/tests/*.yaml"

# ── What the backend actually serves ─────────────────────────────────────────
#
# A class-level @Path is only reachable at its root if some method carries an
# HTTP verb and NO @Path of its own — which is how /api/health works and
# /api/cluster does not. Checking for a literal @Path("/") would miss that,
# so the annotations between one verb and the next are what get inspected.
VERBS = ("@GET", "@POST", "@PUT", "@DELETE", "@PATCH", "@HEAD", "@OPTIONS")

routable = set()   # fully-qualified paths that resolve to a method
class_roots = {}   # class path -> file, for a better error message

for path in glob.glob(SRC_GLOB, recursive=True):
    src = open(path, encoding="utf-8").read()
    paths = re.findall(r'@Path\("([^"]+)"\)', src)
    if not paths:
        continue

    # The first @Path in a JAX-RS resource file is the class-level one.
    class_path = paths[0].rstrip("/")
    if not class_path.startswith("/api"):
        continue
    class_roots[class_path] = path

    lines = src.split("\n")
    for i, line in enumerate(lines):
        if not any(v in line for v in VERBS):
            continue
        # Look ahead to the method signature; a @Path before it makes this a
        # sub-resource, its absence makes the class root routable.
        sub = None
        for follow in lines[i + 1 : i + 15]:
            m = re.search(r'@Path\("([^"]+)"\)', follow)
            if m:
                sub = m.group(1)
                break
            if re.search(r"\b(public|private|protected)\s+\S+\s+\w+\s*\(", follow):
                break
        if sub is None:
            routable.add(class_path)
        else:
            routable.add((class_path + "/" + sub.strip("/")).rstrip("/"))

# ── What the chart tests ask for ─────────────────────────────────────────────
#
# Only real URLs count. An earlier version of this check scanned whole files and
# flagged a path that appeared in a COMMENT explaining the very bug it was
# looking for.
referenced = {}
for path in glob.glob(TEST_GLOB):
    for raw in open(path, encoding="utf-8"):
        line = raw.strip()
        if line.startswith("#"):
            continue
        # Collapse Go-template expressions first: the host and port are
        # {{ include ... }} and {{ .Values.service.port }}, whose spaces and
        # quotes would otherwise terminate the URL match before the path.
        line = re.sub(r"\{\{.*?\}\}", "X", line)
        for url in re.findall(r"https?://[^\s\"')]+", line):
            hit = re.search(r"(/api/[A-Za-z0-9/_-]*)", url)
            if hit:
                referenced.setdefault(hit.group(1).rstrip("/"), set()).add(path)

if not referenced:
    print("OK: no /api paths referenced by chart tests")
    sys.exit(0)

failures = 0
for ref, files in sorted(referenced.items()):
    if ref in routable:
        continue
    failures += 1
    where = ", ".join(sorted(files))
    if ref in class_roots:
        print(f"❌ {ref} is a class-level @Path with no method at its root — it returns 404.")
        print(f"   Defined in {class_roots[ref]}; call a concrete endpoint instead.")
    else:
        print(f"❌ {ref} does not exist in the backend.")
    print(f"   Referenced by: {where}")

if failures:
    print()
    print(f"{failures} chart test path(s) do not resolve. A test calling a 404 cannot")
    print("prove anything about the thing it claims to check.")
    sys.exit(1)

print(f"OK: all {len(referenced)} /api path(s) used by chart tests resolve to a method")
PYTHON

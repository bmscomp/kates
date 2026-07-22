#!/usr/bin/env bash
# Guard against the "un-drainable operator" trap: a PodDisruptionBudget whose
# minAvailable is >= the Deployment's replica count makes every pod
# un-evictable, so node drains, autoscaler scale-down and node upgrades block
# forever. A maxUnavailable of 0 is the same trap.
#
# This renders the strimzi-operator production overlay (the place the trap was
# found) and asserts every PDB leaves at least one pod evictable. Exits
# non-zero on violation. Used by CI and runnable locally.
#
# Usage: scripts/check-pdb-safety.sh
set -euo pipefail

cd "$(dirname "$0")/.."

CHART="charts/strimzi-operator"
VALUES="${CHART}/values-prod.yaml"

command -v helm >/dev/null || { echo "helm not found"; exit 2; }

# The wrapper chart does not render until its subchart is fetched.
helm dependency build "${CHART}" >/dev/null 2>&1 || true

MANIFEST="$(mktemp)"
trap 'rm -f "${MANIFEST}"' EXIT
helm template strimzi-operator "${CHART}" -n strimzi-operator -f "${VALUES}" > "${MANIFEST}"

MANIFEST="${MANIFEST}" python3 <<'PY'
import os, sys
try:
    import yaml
except ImportError:
    import subprocess
    subprocess.run([sys.executable, "-m", "pip", "install", "pyyaml",
                    "--quiet", "--break-system-packages"], check=True)
    import yaml

with open(os.environ["MANIFEST"]) as fh:
    docs = [d for d in yaml.safe_load_all(fh) if d]

# Map a workload's pod labels -> replica count.
workloads = []
for d in docs:
    if d.get("kind") in ("Deployment", "StatefulSet", "ReplicaSet"):
        labels = (d.get("spec", {}).get("template", {})
                    .get("metadata", {}).get("labels", {})) or {}
        replicas = d.get("spec", {}).get("replicas", 1)
        workloads.append((labels, replicas))

def match(sel, labels):
    return sel and all(labels.get(k) == v for k, v in sel.items())

violations = []
for d in docs:
    if d.get("kind") != "PodDisruptionBudget":
        continue
    spec = d.get("spec", {})
    name = d.get("metadata", {}).get("name", "<unnamed>")
    sel = (spec.get("selector", {}) or {}).get("matchLabels", {}) or {}
    replicas = next((r for lab, r in workloads if match(sel, lab)), None)
    if replicas is None:
        print(f"  ? PDB {name}: no matching workload found (skipping)")
        continue
    minA = spec.get("minAvailable")
    maxU = spec.get("maxUnavailable")
    if isinstance(minA, int):
        ok = replicas > minA
        detail = f"minAvailable={minA} replicas={replicas}"
    elif isinstance(maxU, int):
        ok = maxU >= 1
        detail = f"maxUnavailable={maxU}"
    else:
        print(f"  ? PDB {name}: non-integer budget, skipping (minAvailable={minA} maxUnavailable={maxU})")
        continue
    if ok:
        print(f"  ✓ PDB {name}: {detail} — at least one pod evictable")
    else:
        violations.append(f"PDB {name} blocks all eviction ({detail})")

if violations:
    print("\n✗ Un-drainable PodDisruptionBudget(s):")
    for v in violations:
        print(f"    {v}")
    print("  Raise replicas above minAvailable (or set maxUnavailable>=1) so a")
    print("  node drain can always evict at least one pod.")
    sys.exit(1)

print("OK: all PodDisruptionBudgets leave at least one pod evictable.")
PY

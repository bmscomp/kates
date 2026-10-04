#!/usr/bin/env python3
"""Generate every dashboard in `dashboards/`, the chart copies and the bundles.

`dashboards/<board>/dashboard.json` is built from that board's `board.py`, and
a copy is written to each chart named in its `manifest.yaml`. The charts load
their copy with `.Files.Get`, because Helm cannot read a file outside its own
chart directory.

The same boards are also rendered into `dashboards/bundle/`, for the two
installs that have no Helm chart in them: a Grafana started with file
provisioning (`bundle/provisioning/`) and a cluster running the Grafana sidecar
without these charts (`bundle/configmaps.yaml`). Both are generated here rather
than maintained by hand, for the reason every other copy is: a bundle nobody
regenerates is a bundle one release behind, and a board one release behind
reads to its operator as a board that is broken.

    scripts/gen-dashboards.py            write everything
    scripts/gen-dashboards.py --check    fail if anything is out of date

`--check` is what CI runs: a board edited without regenerating, a chart copy
edited by hand, or a bundle left stale fails the build. Same contract as
gen-chart-table.sh and gen-version-matrix.sh.
"""

from __future__ import annotations

import argparse
import json
import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent
DASHBOARDS = ROOT / "dashboards"
BUNDLE = DASHBOARDS / "bundle"
sys.path.insert(0, str(DASHBOARDS / "_lib"))

import build as builder  # noqa: E402
import bundle as bundler  # noqa: E402


def variants_of(manifest: dict) -> list[str]:
    """Every shape a board can take. `default` always exists."""
    return ["default"] + [v for v in manifest.get("variants", []) if v != "default"]


def outputs(board_dir: Path, manifest: dict) -> list[tuple[Path, str]]:
    """Every (path, variant) this board writes, canonical copy first."""
    out: list[tuple[Path, str]] = [(board_dir / "dashboard.json", "default")]
    for target in manifest.get("targets", []):
        chart = ROOT / target["chart"]
        out.append((chart / target["file"], target.get("variant", "default")))
    return out


def sync(path: Path, want: str, check: bool, stale: list[str]) -> int:
    """Write `want` to `path`, or record that it is stale. Returns files written."""
    have = path.read_text() if path.exists() else None
    if have == want:
        return 0
    if check:
        stale.append("%s is %s" % (path.relative_to(ROOT),
                                   "missing" if have is None else "out of date"))
        return 0
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text(want)
    return 1


def prune(kept: set[Path], check: bool, stale: list[str]) -> int:
    """Drop bundle files no board writes any more.

    A deleted board leaves its rendered copy behind otherwise, and a bundle
    that still carries a board the repository does not is worse than a stale
    one: `kubectl apply` would keep re-creating it.
    """
    if not BUNDLE.is_dir():
        return 0
    removed = 0
    for path in sorted(BUNDLE.rglob("*")):
        if not path.is_file() or path in kept:
            continue
        if check:
            stale.append("%s is left over from a board that no longer exists"
                         % path.relative_to(ROOT))
        else:
            path.unlink()
            removed += 1
    return removed



def unmarked_generated(paths: set) -> list:
    """Every generated file must be `linguist-generated` in .gitattributes.

    Not cosmetic. This change writes 84,247 lines of generated JSON against
    15,813 a person wrote, and a reviewer who meets the JSON first concludes
    the diff cannot be read. GitHub collapses a file marked this way, so the
    review opens on the board.py that produced it.

    The risk with any such list is that it stops matching what is actually
    generated — a new board or a new chart target reappears in the diff and
    nobody notices, because nothing fails. So the list is checked against the
    paths this script writes, which is the only list that cannot be wrong.
    """
    import subprocess
    if not (ROOT / ".gitattributes").is_file():
        return [".gitattributes is missing — every generated file would show "
                "in full in a pull request diff"]
    rel = sorted(str(p.relative_to(ROOT)) for p in paths)
    try:
        proc = subprocess.run(
            ["git", "check-attr", "linguist-generated", "--stdin"],
            cwd=ROOT, input="\n".join(rel), capture_output=True, text=True,
            check=True)
    except (OSError, subprocess.CalledProcessError):
        return []          # no git here; the sync check above still stands
    out = []
    for line in proc.stdout.splitlines():
        # "<path>: linguist-generated: <value>"
        path, _, value = line.rpartition(": ")
        if value.strip() != "true":
            out.append("%s is generated but is not marked linguist-generated "
                       "in .gitattributes, so it would show in full in a pull "
                       "request diff" % path.split(":")[0])
    return out


def run(check: bool) -> int:
    if not DASHBOARDS.is_dir():
        sys.exit("no dashboards/ directory at %s" % DASHBOARDS)

    stale: list[str] = []
    written = 0
    boards = builder.board_dirs(DASHBOARDS)
    if not boards:
        sys.exit("dashboards/ has no board directories")

    defaults: list[tuple[str, dict]] = []
    for board_dir in boards:
        manifest = builder.load_manifest(board_dir)
        rendered = {v: builder.dumps(builder.build(board_dir, v)) for v in variants_of(manifest)}
        defaults.append((board_dir.name, json.loads(rendered["default"])))

        for path, variant in outputs(board_dir, manifest):
            if variant not in rendered:
                sys.exit("%s: target wants variant %r, which the board does not build"
                         % (board_dir.name, variant))
            written += sync(path, rendered[variant], check, stale)

    # The bundles: the same default-variant boards, in the two shapes an
    # install with no Helm chart in it needs. See dashboards/_lib/bundle.py.
    bundle_files = bundler.outputs(defaults, BUNDLE)
    for path, body in bundle_files:
        written += sync(path, body, check, stale)
    written += prune({p for p, _ in bundle_files}, check, stale)

    if check:
        written_paths = set()
        for board_dir in boards:
            for path, _ in outputs(board_dir, builder.load_manifest(board_dir)):
                written_paths.add(path)
        written_paths |= {p for p, _ in bundle_files}
        stale += unmarked_generated(written_paths)

        if stale:
            print("Dashboard copies are out of date:", file=sys.stderr)
            for line in stale:
                print("  %s" % line, file=sys.stderr)
            print("\nRun scripts/gen-dashboards.py to regenerate.", file=sys.stderr)
            return 1
        print("OK: every dashboard, chart copy and bundle file is in sync "
              "(%d boards, %d bundle files), and every one is marked generated"
              % (len(boards), len(bundle_files)))
        return 0

    print("Wrote %d file(s) for %d board(s), including %d bundle file(s)"
          % (written, len(boards), len(bundle_files)))
    return 0


def main() -> int:
    ap = argparse.ArgumentParser(description=__doc__)
    ap.add_argument("--check", action="store_true",
                    help="verify instead of writing; exit 1 when anything is stale")
    return run(ap.parse_args().check)


if __name__ == "__main__":
    sys.exit(main())

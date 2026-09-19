#!/usr/bin/env python3
"""Generate every dashboard in `dashboards/` and sync the chart copies.

`dashboards/<board>/dashboard.json` is built from that board's `board.py`, and
a copy is written to each chart named in its `manifest.yaml`. The charts load
their copy with `.Files.Get`, because Helm cannot read a file outside its own
chart directory.

    scripts/gen-dashboards.py            write everything
    scripts/gen-dashboards.py --check    fail if anything is out of date

`--check` is what CI runs: a board edited without regenerating, or a chart copy
edited by hand, fails the build. Same contract as gen-chart-table.sh and
gen-version-matrix.sh.
"""

from __future__ import annotations

import argparse
import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent
DASHBOARDS = ROOT / "dashboards"
sys.path.insert(0, str(DASHBOARDS / "_lib"))

import build as builder  # noqa: E402


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


def run(check: bool) -> int:
    if not DASHBOARDS.is_dir():
        sys.exit("no dashboards/ directory at %s" % DASHBOARDS)

    stale: list[str] = []
    written = 0
    boards = builder.board_dirs(DASHBOARDS)
    if not boards:
        sys.exit("dashboards/ has no board directories")

    for board_dir in boards:
        manifest = builder.load_manifest(board_dir)
        rendered = {v: builder.dumps(builder.build(board_dir, v)) for v in variants_of(manifest)}

        for path, variant in outputs(board_dir, manifest):
            if variant not in rendered:
                sys.exit("%s: target wants variant %r, which the board does not build"
                         % (board_dir.name, variant))
            want = rendered[variant]
            have = path.read_text() if path.exists() else None
            if have == want:
                continue
            if check:
                rel = path.relative_to(ROOT)
                stale.append("%s is %s" % (rel, "missing" if have is None else "out of date"))
            else:
                path.parent.mkdir(parents=True, exist_ok=True)
                path.write_text(want)
                written += 1

    if check:
        if stale:
            print("Dashboard copies are out of date:", file=sys.stderr)
            for line in stale:
                print("  %s" % line, file=sys.stderr)
            print("\nRun scripts/gen-dashboards.py to regenerate.", file=sys.stderr)
            return 1
        print("OK: every dashboard and chart copy is in sync (%d boards)" % len(boards))
        return 0

    print("Wrote %d file(s) for %d board(s)" % (written, len(boards)))
    return 0


def main() -> int:
    ap = argparse.ArgumentParser(description=__doc__)
    ap.add_argument("--check", action="store_true",
                    help="verify instead of writing; exit 1 when anything is stale")
    return run(ap.parse_args().check)


if __name__ == "__main__":
    sys.exit(main())

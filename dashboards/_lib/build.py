"""Turn a board directory into a Grafana dashboard document.

A board directory holds `board.py` (which exposes `build(variant)` returning a
list of `Row`s and, optionally, `variables()`), `manifest.yaml` (identity and
where the copies go) and the generated `dashboard.json`.

The JSON is generated, not authored. That is the whole point: positions, ids
and the panel skeleton come from `_lib`, so no board can hand-number a `y`
into an overlap or ship a panel with no description.
"""

from __future__ import annotations

import importlib.util
import json
from pathlib import Path
from typing import Any

import yaml

from layout import Row, layout
from panels import datasource_var

SCHEMA_VERSION = 39


def load_manifest(board_dir: Path) -> dict[str, Any]:
    with (board_dir / "manifest.yaml").open() as fh:
        manifest = yaml.safe_load(fh) or {}
    for key in ("uid", "title", "tags"):
        if key not in manifest:
            raise ValueError("%s/manifest.yaml has no %r" % (board_dir.name, key))
    return manifest


def load_board_module(board_dir: Path):
    path = board_dir / "board.py"
    spec = importlib.util.spec_from_file_location("board_%s" % board_dir.name, path)
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


def build(board_dir: Path, variant: str = "default") -> dict[str, Any]:
    """Build one board. `variant` selects a conditional shape (see §6.1)."""
    manifest = load_manifest(board_dir)
    module = load_board_module(board_dir)

    rows = module.build(variant)
    if not isinstance(rows, list) or not all(isinstance(r, Row) for r in rows):
        raise TypeError("%s/board.py build() must return a list of Row" % board_dir.name)

    variables = [datasource_var()]
    if hasattr(module, "variables"):
        variables.extend(module.variables(variant))

    dashboard: dict[str, Any] = {
        "uid": manifest["uid"],
        "title": manifest["title"],
        "tags": list(manifest["tags"]),
        "schemaVersion": SCHEMA_VERSION,
        "version": 1,
        "editable": True,
        "graphTooltip": 1,
        "timezone": "browser",
        "refresh": manifest.get("refresh", "30s"),
        "time": {"from": manifest.get("time_from", "now-1h"), "to": "now"},
        "templating": {"list": variables},
        "annotations": {"list": []},
        "panels": layout(rows),
    }
    if manifest.get("links"):
        dashboard["links"] = manifest["links"]
    if manifest.get("description"):
        dashboard["description"] = manifest["description"]
    return dashboard


def dumps(dashboard: dict[str, Any]) -> str:
    """Canonical JSON — sorted keys and a trailing newline, so a diff is a diff."""
    return json.dumps(dashboard, indent=2, sort_keys=True) + "\n"


def board_dirs(root: Path) -> list[Path]:
    """Every board directory under `dashboards/`, in name order."""
    return sorted(
        d for d in root.iterdir()
        if d.is_dir() and not d.name.startswith("_") and (d / "manifest.yaml").exists()
    )

"""Grafana panels laid out on the 24-column grid.

A faithful port of `kafka-common.grafana.layout`, which this replaces. The
helper lived in a Helm template because the only board built that way was
connect-cluster's; every other board in the repository hand-numbered its `y`
coordinates, and the MirrorMaker 2 boards maintained a `$y` cursor by hand
across 1500 lines of JSON-in-YAML. Positions are computed here instead.

That matters for the same reason it did in the template: a board whose
sections are conditional (an SLO row only when the rules that record it
exist) or repeated (one row per mirror) cannot overlap a hand-numbered `y`,
which is how a section silently ends up drawn on top of the one above it.

Call `layout(rows)` where each row is `Row(title, panels, collapsed=False)`
and each panel is a dict plus two layout keys, `w` (default 12) and `h`
(default 8), which are removed from the output. Panels flow left to right and
wrap when the next one would pass column 24; a band is as tall as its tallest
panel.

- A row with an empty title has no header; its panels are placed inline.
- A collapsed row takes one grid unit; its panels are nested inside the row
  panel, as Grafana stores them, with the positions they get when expanded.
- `id`s run from 1 in document order.
"""

from __future__ import annotations

from dataclasses import dataclass, field
from typing import Any


GRID_WIDTH = 24


@dataclass
class Row:
    """One section of a board. An empty title places the panels inline.

    `repeat` names a template variable and draws the row once per selected
    value, which is Grafana's own way of saying "one of these per mirror".
    The row is written to the JSON once; the copies appear at view time, so
    the packer places it exactly as it places any other row.
    """

    title: str = ""
    panels: list[dict[str, Any]] = field(default_factory=list)
    collapsed: bool = False
    repeat: str = ""


def layout(rows: list[Row]) -> list[dict[str, Any]]:
    """Pack rows of panels onto the grid, returning Grafana's panel list."""
    y = 0
    panel_id = 0
    out: list[dict[str, Any]] = []

    for row in rows:
        header: dict[str, Any] | None = None
        row_y = y

        if row.title:
            panel_id += 1
            header = {
                "type": "row",
                "title": row.title,
                "id": panel_id,
                "collapsed": bool(row.collapsed),
                "gridPos": {"h": 1, "w": GRID_WIDTH, "x": 0, "y": y},
                "panels": [],
            }
            if row.repeat:
                header["repeat"] = row.repeat
            y += 1
        elif row.repeat:
            raise ValueError("a row with no title cannot repeat (repeat=%r)" % row.repeat)

        x = 0
        band_h = 0
        children: list[dict[str, Any]] = []

        for panel in row.panels or []:
            w = int(panel.get("w", 12))
            h = int(panel.get("h", 8))
            if w > GRID_WIDTH:
                raise ValueError(
                    "panel %r is %d columns wide; the grid is %d"
                    % (panel.get("title", "?"), w, GRID_WIDTH)
                )
            if x + w > GRID_WIDTH:
                y += band_h
                x = 0
                band_h = 0

            panel_id += 1
            placed = {k: v for k, v in panel.items() if k not in ("w", "h")}
            placed["id"] = panel_id
            placed["gridPos"] = {"h": h, "w": w, "x": x, "y": y}
            children.append(placed)

            x += w
            band_h = max(band_h, h)

        y += band_h

        if row.title and row.collapsed:
            header["panels"] = children
            out.append(header)
            # A collapsed row occupies exactly one unit whatever it contains.
            y = row_y + 1
        else:
            if header is not None:
                out.append(header)
            out.extend(children)

    return out

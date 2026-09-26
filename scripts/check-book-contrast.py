#!/usr/bin/env python3
"""Check the book theme's colour pairs against WCAG 2 AA.

Reads the two palettes, docs/book/theme/kates-light.scss and kates-dark.scss,
plus the syntax colours in kates-code.theme, and checks every text colour the
theme draws against the surface it sits on: body text, code, callouts, links,
the sidebar and contents, and diagram labels. Text needs 4.5:1. Icons, focus
rings and other non-text marks need 3:1 (WCAG 1.4.11).

Translucent surfaces (callout tints, the active sidebar entry) are composited
over the page first, in the order the theme stacks them. When kates.scss
changes a tint it draws with a literal alpha, update SCSS_ALPHAS below.

Usage: scripts/check-book-contrast.py [--verbose] [--theme-dir DIR]
Exits 1 when a pair falls short; used by the docs CI.
"""

import argparse
import json
import re
import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent
TEXT = 4.5
NON_TEXT = 3.0

# Alphas that kates.scss uses directly rather than through a palette token
SCSS_ALPHAS = {
    "link-code-tint": 0.07,  # #quarto-document-content a code
    "narrow-table-head": 0.04,  # table header below the md breakpoint
}

CALLOUTS = ["note", "tip", "important", "warning", "caution"]


# ── Parsing ─────────────────────────────────────────────────────────────────

VAR = re.compile(r"^\s*\$([\w-]+)\s*:\s*([^;]+);", re.M)


def read_palette(path):
    text = re.sub(r"//[^\n]*", "", path.read_text(encoding="utf-8"))
    return {name: value.strip() for name, value in VAR.findall(text)}


def parse_hex(value):
    h = value.lstrip("#")
    if len(h) == 3:
        h = "".join(c * 2 for c in h)
    if len(h) != 6 or not re.fullmatch(r"[0-9a-fA-F]{6}", h):
        raise ValueError(f"not a hex colour: {value}")
    return (int(h[0:2], 16), int(h[2:4], 16), int(h[4:6], 16), 1.0)


def resolve(palette, value, seen=()):
    """A palette value as an (r, g, b, alpha) colour or a plain number."""
    value = value.strip()
    if value.startswith("$"):
        name = value[1:]
        if name in seen:
            raise ValueError(f"reference loop at ${name}")
        if name not in palette:
            raise KeyError(name)
        try:
            return resolve(palette, palette[name], seen + (name,))
        except ValueError as error:
            if str(error).startswith("$"):  # already names the token
                raise
            raise ValueError(f"${name}: {error}") from None
    if value.startswith("#"):
        return parse_hex(value)
    m = re.fullmatch(r"rgba?\((.*)\)", value)
    if m:
        args = [a.strip() for a in m.group(1).split(",")]
        if len(args) == 2:  # rgba($colour, alpha)
            r, g, b, a = resolve(palette, args[0], seen)
            return (r, g, b, a * float(args[1]))
        r, g, b = (float(x) for x in args[:3])
        return (r, g, b, float(args[3]) if len(args) > 3 else 1.0)
    try:
        return float(value)
    except ValueError:
        raise ValueError(f"cannot read {value!r}") from None


# ── Colour maths ────────────────────────────────────────────────────────────

def over(top, bottom):
    """Composite a possibly translucent colour over an opaque one."""
    r, g, b, a = top
    return tuple(a * t + (1 - a) * u for t, u in zip((r, g, b), bottom[:3])) + (1.0,)


def luminance(colour):
    def channel(c):
        c /= 255
        return c / 12.92 if c <= 0.04045 else ((c + 0.055) / 1.055) ** 2.4
    r, g, b = (channel(c) for c in colour[:3])
    return 0.2126 * r + 0.7152 * g + 0.0722 * b


def ratio(fg, bg):
    hi, lo = sorted((luminance(fg), luminance(bg)), reverse=True)
    return (hi + 0.05) / (lo + 0.05)


def hexof(colour):
    return "#" + "".join(f"{round(c):02x}" for c in colour[:3])


# ── The pairs ───────────────────────────────────────────────────────────────
# Each surface is a stack of layers, bottom first; a layer is a palette
# reference, or (colour reference, alpha reference or number) for a tint.

def pairs(syntax):
    page = ["$body-bg"]
    out = [
        # Body
        ("body", "body text", "$body-color", page, TEXT),
        ("body", "headings", "$kates-heading", page, TEXT),
        ("body", "bold text", "$kates-strong", page, TEXT),
        ("body", "muted text: captions, blockquotes, footer", "$kates-muted", page, TEXT),
        ("body", "section numbers", "$kates-number", page, TEXT),
        ("body", "table header", "$kates-heading", page + ["$kates-surface"], TEXT),
        ("body", "table header on a narrow screen", "$kates-heading",
         page + [("$kates-heading", SCSS_ALPHAS["narrow-table-head"])], TEXT),
        ("body", "table row on hover", "$body-color", page + ["$kates-row-hover"], TEXT),
        ("body", "keyboard keys", "$body-color", page + ["$kates-surface"], TEXT),
        # Links
        ("links", "links", "$kates-link", page, TEXT),
        ("links", "links on a table header", "$kates-link", page + ["$kates-surface"], TEXT),
        # Code
        ("code", "inline code", "$code-color", page + ["$code-bg"], TEXT),
        ("code", "inline code in a link", "$kates-link",
         page + [("$kates-link", SCSS_ALPHAS["link-code-tint"])], TEXT),
        ("code", "code block text", "$kates-code-block-fg", ["$code-block-bg"], TEXT),
        ("code", "code block file name", "$kates-code-file-fg", ["$kates-code-file-bg"], TEXT),
        ("code", "command output", "$kates-output-fg", page + ["$kates-output-bg"], TEXT),
        ("code", "copy button", "$btn-code-copy-color", ["$code-block-bg"], NON_TEXT),
        ("code", "copy button, active", "$btn-code-copy-color-active", ["$code-block-bg"], NON_TEXT),
    ]
    for style, colour in syntax:
        out.append(("code", f"syntax: {style}", colour, ["$code-block-bg"], TEXT))
    for name in CALLOUTS:
        c = f"$callout-color-{name}"
        body = page + [(c, "$kates-callout-tint")]
        header = body + [(c, "$kates-callout-header-tint")]
        out += [
            ("callouts", f"{name}: text", "$body-color", body, TEXT),
            ("callouts", f"{name}: title", "$kates-heading", header, TEXT),
            ("callouts", f"{name}: links", "$kates-link", body, TEXT),
            ("callouts", f"{name}: icon and rule", c, header, NON_TEXT),
        ]
    out += [
        # Sidebar and the chapter contents (both sit on the page)
        ("sidebar", "book title", "$kates-heading", page, TEXT),
        ("sidebar", "entries", "$kates-sidebar-fg", page, TEXT),
        ("sidebar", "part labels and chapter numbers", "$kates-muted", page, TEXT),
        ("sidebar", "current entry", "$kates-link", page + ["$kates-active-bg"], TEXT),
        # Diagrams
        ("diagrams", "node labels", "$mermaid-label-fg-color",
         ["$kates-diagram-bg", "$mermaid-node-bg-color"], TEXT),
        ("diagrams", "group labels", "$mermaid-label-fg-color",
         ["$kates-diagram-bg", "$mermaid-fg-color--lightest"], TEXT),
        ("diagrams", "edge labels", "$mermaid-edge-color", ["$mermaid-label-bg-color"], TEXT),
        ("diagrams", "notes", "$mermaid-label-fg-color", ["$kates-diagram-note-bg"], TEXT),
        ("diagrams", "hint under a diagram", "$kates-muted", ["$kates-diagram-bg"], TEXT),
        ("diagrams", "Expand button", "$kates-muted", page, TEXT),
        # Keyboard focus rings
        ("focus", "ring on the page", "$kates-focus-ring", page, NON_TEXT),
        ("focus", "ring on a diagram", "$kates-focus-ring", ["$kates-diagram-bg"], NON_TEXT),
        ("focus", "ring on the current sidebar entry", "$kates-focus-ring",
         page + ["$kates-active-bg"], NON_TEXT),
        ("focus", "ring on a code block", "$kates-focus-ring-on-code", ["$code-block-bg"], NON_TEXT),
    ]
    return out


def surface(palette, layers):
    base = None
    for layer in layers:
        if isinstance(layer, tuple):
            colour = resolve(palette, layer[0])
            alpha = layer[1] if isinstance(layer[1], float) else resolve(palette, layer[1])
            colour = colour[:3] + (colour[3] * alpha,)
        else:
            colour = resolve(palette, layer)
        if base is None:
            if colour[3] < 1:
                raise ValueError(f"bottom layer {layer} is translucent")
            base = colour
        else:
            base = over(colour, base)
    return base


def read_syntax(path):
    theme = json.loads(path.read_text(encoding="utf-8"))
    seen = {}
    for style, spec in theme.get("text-styles", {}).items():
        colour = spec.get("text-color")
        if colour:
            seen.setdefault(colour.lower(), []).append(style)
    return [(", ".join(styles), colour) for colour, styles in sorted(seen.items())]


def main():
    parser = argparse.ArgumentParser(description=__doc__.split("\n\n")[0])
    parser.add_argument("--verbose", "-v", action="store_true", help="print every pair, not only failures")
    parser.add_argument("--theme-dir", type=Path, default=ROOT / "docs/book/theme")
    args = parser.parse_args()

    syntax = read_syntax(args.theme_dir / "kates-code.theme")
    failures = errors = checked = 0
    for scheme in ("light", "dark"):
        palette = read_palette(args.theme_dir / f"kates-{scheme}.scss")
        for area, label, fg_ref, layers, minimum in pairs(syntax):
            try:
                bg = surface(palette, layers)
                fg = parse_hex(fg_ref) if fg_ref.startswith("#") else resolve(palette, fg_ref)
                fg = over(fg, bg) if fg[3] < 1 else fg
            except KeyError as missing:
                print(f"ERROR  {scheme:5}  {area}: {label}: ${missing.args[0]} is not defined")
                errors += 1
                continue
            except ValueError as error:  # a value this script cannot read, e.g. a Sass function
                print(f"ERROR  {scheme:5}  {area}: {label}: {error}")
                errors += 1
                continue
            value = ratio(fg, bg)
            checked += 1
            ok = value >= minimum
            if not ok:
                failures += 1
            if args.verbose or not ok:
                kind = "text" if minimum == TEXT else "non-text"
                print(f"{'PASS' if ok else 'FAIL'}  {value:5.2f}:1  {scheme:5}  {area}: {label}"
                      f"  ({hexof(fg)} on {hexof(bg)}, {kind} needs {minimum}:1)")

    if failures or errors:
        print()
        if failures:
            print(f"{failures} of {checked} colour pairs fall short of WCAG AA — see the palettes in docs/book/theme/.")
        if errors:
            print(f"{errors} colour pairs could not be checked; each ERROR line above names the token.")
        return 1
    print(f"OK: all {checked} colour pairs in the book theme meet WCAG AA")
    return 0


if __name__ == "__main__":
    sys.exit(main())

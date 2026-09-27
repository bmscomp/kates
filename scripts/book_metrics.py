#!/usr/bin/env python3
"""Structure metrics for the book in docs/book, and the ratchet that keeps them
from growing.

Usage:
  scripts/book_metrics.py [FILE ...]              per-file report, with line numbers
  scripts/book_metrics.py --summary [FILE ...]    one row per file, then totals
  scripts/book_metrics.py --json [FILE ...]       everything, as JSON
  scripts/book_metrics.py --check BASELINE        the ratchet (scripts/check-book-style.sh)
  scripts/book_metrics.py --update BASELINE       rewrite the baseline from today's counts

With no FILE, it measures every page of the book: docs/book/*.md and index.qmd,
except STYLE.md, README.md and CONCEPTS.md, which are about the book rather
than part of it. Part pages (part-*.md) are measured like any other page.

What it measures, per file (the ones marked * are ratcheted):

  bare_headings *          a heading whose next block is another heading, a
                           table or a code block: a section with no prose of
                           its own. In the three reference chapters
                           (REFERENCE_FILES), a reference entry is exempt: a
                           command, endpoint or RPC heading (ENTRY_HEADING)
                           directly over its usage code block. In the
                           Glossary, a letter section is exempt: a one-letter
                           H2 (LETTER_HEADING) directly over its first term's
                           H3.
  tables_without_lead_in * a table with no prose line directly above it.
  list_heavy_sections      a section with 6+ list lines and 3x more list
                           lines than prose lines.
  bold_lead_bullets *      "- **Label**: ...", "- **Label** — ...", and
                           "- **Label.** ..." list items.
  numbered_headings *      a heading that starts with a hand-typed number
                           (^#+ \\d). "Step N — ..." headings don't match.
  formula_openings *       "This chapter covers / provides / walks you
                           through / is for" before the first H2.
  changelog_phrases *      no longer, used to (but not "is used to"),
                           since chart, previously, are gone, until this
                           refactor, in text outside code.
  long_sentences           explanatory sentences (5+ words, in prose
                           paragraphs) of 40+ words; long_sentence_share is
                           their share in percent, sentences_over_70 the
                           count over 70 words.
  long_fence_lines *       lines over 90 characters inside a fenced block
                           other than Mermaid.
  mermaid_blocks           Mermaid blocks; mermaid_uncaptioned * counts the
                           ones missing a "%%| fig-cap:" or a "%%| fig-alt:".
  todo_markers             "[TODO" anywhere. Always zero: --check fails on
                           any, baseline or not.
  scope_blockquotes *      a "> **Scope**" blockquote.
  hr_rules *               a "---" horizontal rule (front matter excluded).
  glossary_links           links to the Glossary appendix.
  broken_handoff *         1 when the last Markdown link in the file's final
                           paragraph doesn't target the next chapter in
                           _quarto.yml order. Part pages are skipped both as
                           senders and as targets; the last chapter hands off
                           to the first appendix; appendices, and pages not
                           in _quarto.yml, need no handoff.
  callouts                 ::: {.callout-*} blocks.

Horizontal rules and HTML comments are not blocks, so taking one out never
changes another measure (a table after "---" is judged by the line above it).

The ratchet (--check) compares each file's ratcheted counts with the baseline:
a count may fall but never rise, a file missing from the baseline (a new page)
must be zero on every ratcheted measure, and todo_markers must be zero
everywhere. Lowering a count never fails; run --update to record the lower
number, so the next PR can't spend it.
"""

from __future__ import annotations

import argparse
import json
import re
import sys
from pathlib import Path

REPO = Path(__file__).resolve().parent.parent
BOOK_DIR = REPO / "docs" / "book"

# Pages about the book rather than in it: never measured.
EXCLUDED = {"STYLE.md", "README.md", "CONCEPTS.md"}

# Chapters made of reference entries: a command, endpoint or RPC heading over
# its usage code block.
REFERENCE_FILES = {"10-cli-reference.md", "11-api-reference.md", "16-grpc-api.md"}

GLOSSARY = "appendix-a-glossary"

# Measures that may not grow, in report order.
RATCHETED = [
    "bare_headings",
    "tables_without_lead_in",
    "bold_lead_bullets",
    "numbered_headings",
    "formula_openings",
    "changelog_phrases",
    "long_fence_lines",
    "mermaid_uncaptioned",
    "hr_rules",
    "scope_blockquotes",
    "broken_handoff",
]

# Measures that must be zero in every file, whatever the baseline says.
HARD_ZERO = ["todo_markers"]

COUNTS = [
    "bare_headings",
    "tables_without_lead_in",
    "list_heavy_sections",
    "bold_lead_bullets",
    "numbered_headings",
    "formula_openings",
    "changelog_phrases",
    "explanatory_sentences",
    "long_sentences",
    "long_sentence_share",
    "sentences_over_70",
    "long_fence_lines",
    "mermaid_blocks",
    "mermaid_uncaptioned",
    "todo_markers",
    "scope_blockquotes",
    "hr_rules",
    "glossary_links",
    "broken_handoff",
    "callouts",
]

LABELS = {
    "bare_headings": "bare headings",
    "tables_without_lead_in": "tables without a lead-in",
    "list_heavy_sections": "list-heavy sections",
    "bold_lead_bullets": "bold-lead bullets",
    "numbered_headings": "hand-numbered headings",
    "formula_openings": "formula openings",
    "changelog_phrases": "changelog phrases",
    "explanatory_sentences": "explanatory sentences",
    "long_sentences": "sentences of 40+ words",
    "long_sentence_share": "share of 40+ word sentences (%)",
    "sentences_over_70": "sentences over 70 words",
    "long_fence_lines": "fenced lines over 90 characters",
    "mermaid_blocks": "Mermaid blocks",
    "mermaid_uncaptioned": "Mermaid blocks without fig-cap and fig-alt",
    "todo_markers": "[TODO markers",
    "scope_blockquotes": "bold Scope blockquotes",
    "hr_rules": "--- rules",
    "glossary_links": "links to the Glossary",
    "broken_handoff": "broken handoff",
    "callouts": "callouts",
}

LONG_SENTENCE = 40
VERY_LONG_SENTENCE = 70
MIN_SENTENCE = 5
MAX_FENCE_LINE = 90

UPDATE_HINT = "python3 scripts/book_metrics.py --update scripts/book-metrics-baseline.json"

HEADING = re.compile(r"^(#{1,6})\s+(.*)$")
LIST_ITEM = re.compile(r"^\s*([-*+]|\d+\.)\s")
FENCE_OPEN = re.compile(r"^(\s*)(`{3,}|~{3,})\s*(.*)$")
RULE = re.compile(r"^ {0,3}(-{3,}|\*{3,}|_{3,})\s*$")
NUMBERED_HEADING = re.compile(r"^#{1,6}\s+\d")
# A reference entry's heading: a CLI command ("test list"), an endpoint
# ("GET /api/tests", or anything in backticks) or an RPC ("GetTest").
ENTRY_HEADING = re.compile(
    r"^#{1,6}\s+(?:[a-z][a-z0-9 -]*|`[^`]+`.*|(?:GET|POST|PUT|PATCH|DELETE)\s.*"
    r"|[A-Z][a-z0-9]+[A-Z]\w*(?:\s*/\s*[A-Z]\w*)*)\s*(?:\{[^}]*\})?\s*$"
)
# A Glossary letter section ("## K"), whose first block is its first term's H3.
LETTER_HEADING = re.compile(r"^##\s+[A-Z]\s*(?:\{[^}]*\})?\s*$")
TERM_HEADING = re.compile(r"^###\s+\S")
BOLD_LEAD = re.compile(
    r"^\s*(?:[-*+]|\d+\.)\s+\*\*(?:[^*]+\*\*\s*[:—–-]|[^*]+[:.]\*\*(?:\s|$))"
)
FORMULA = re.compile(
    r"\bthis (?:chapter|appendix) (?:also )?(?:covers|provides|walks you through|is for)\b",
    re.I,
)
CHANGELOG = re.compile(
    r"\b(no longer|used to|since chart|previously|are gone|until this refactor)\b", re.I
)
# "X is used to compute Y" describes a purpose, not history.
PASSIVE_BEFORE_USED = re.compile(
    r"\b(?:is|are|was|were|be|been|being|get|gets|got|getting)\s+$", re.I
)
SCOPE_QUOTE = re.compile(r"^\s*>\s*\*\*Scope\b")
FIG_CAP = re.compile(r"^\s*%%\|\s*fig-cap:", re.M)
FIG_ALT = re.compile(r"^\s*%%\|\s*fig-alt:", re.M)
CALLOUT_OPEN = re.compile(r"^\s*:::+\s*\{\s*\.callout-")
LINK = re.compile(
    r"(?<!!)\[((?:[^\[\]]|\[[^\]]*\])*)\]\(\s*<?([^)\s>]+)>?(?:\s+\"[^\"]*\")?\s*\)"
)
IMAGE = re.compile(r"!\[(?:[^\[\]]|\[[^\]]*\])*\]\([^)]*\)")
INLINE_CODE = re.compile(r"`[^`]*`")
SENTENCE_SPLIT = re.compile(r"(?<=[.!?])[\"')\]*_]*\s+(?=[A-Z0-9\"'(\[`*_])")
ABBREVIATIONS = re.compile(r"\b(e\.g|i\.e|vs|etc|cf)\.", re.I)


# ── parsing ────────────────────────────────────────────────────────────────


class Block:
    """One line of the page outside code (or the opening line of a fence).

    kind is h (heading), t (table row), li (list item), callout (a ::: fence
    line), p (anything else with text) or code (a fenced block, whose body is
    kept in `body` as (line number, text) pairs).
    """

    __slots__ = ("kind", "line", "text", "lang", "body")

    def __init__(self, kind, line, text, lang=""):
        self.kind = kind
        self.line = line
        self.text = text
        self.lang = lang
        self.body = []

    def __repr__(self):  # pragma: no cover - debugging aid
        return f"Block({self.kind!r}, L{self.line}, {self.text[:40]!r})"


class Page:
    """A parsed page: its blocks, plus the rules the blocks leave out.

    Horizontal rules and HTML comments are not blocks: they are neither prose
    nor structure, and taking one out must never change another measure.
    """

    def __init__(self, text: str):
        self.lines = text.split("\n")
        self.blocks: list[Block] = []
        self.rules: list[int] = []
        self._parse()

    def _parse(self):
        lines = self.lines
        start = 0
        if lines and lines[0].strip() == "---":
            for j in range(1, len(lines)):
                if lines[j].strip() in ("---", "..."):
                    start = j + 1
                    break
        code = None
        fence = ""
        indent = 0
        in_comment = False
        for idx in range(start, len(lines)):
            line = lines[idx]
            no = idx + 1
            s = line.strip()
            if code is not None:
                if re.match(r"^\s*" + re.escape(fence[0]) + "{" + str(len(fence)) + r",}\s*$", line):
                    code = None
                else:
                    code.body.append((no, line[min(indent, len(line) - len(line.lstrip())):]))
                continue
            if in_comment:
                if "-->" in line:
                    in_comment = False
                continue
            if not s:
                continue
            m = FENCE_OPEN.match(line)
            if m and not (m.group(2)[0] == "`" and "`" in m.group(3)):
                indent, fence = len(m.group(1)), m.group(2)
                lang = m.group(3).strip().strip("{}").split()[0].lstrip(".") if m.group(3).strip() else ""
                code = Block("code", no, line, lang=lang)
                self.blocks.append(code)
                continue
            if s.startswith("<!--"):
                if "-->" not in s:
                    in_comment = True
                continue
            if RULE.match(line):
                self.rules.append(no)
                continue
            if HEADING.match(line):
                kind = "h"
            elif s.startswith("|"):
                kind = "t"
            elif LIST_ITEM.match(line):
                kind = "li"
            elif s.startswith(":::"):
                kind = "callout"
            else:
                kind = "p"
            self.blocks.append(Block(kind, no, line))

    def paragraphs(self, kinds=("p", "li")) -> list[list[Block]]:
        """Runs of blocks of the given kinds on consecutive lines."""
        out: list[list[Block]] = []
        run: list[Block] = []
        for b in self.blocks:
            if b.kind in kinds and run and run[-1].line == b.line - 1:
                run.append(b)
                continue
            if run:
                out.append(run)
            run = [b] if b.kind in kinds else []
        if run:
            out.append(run)
        return out

    def final_paragraph(self) -> list[Block]:
        """The last run of text lines on the page, ignoring ::: fence lines.

        Empty when the page ends on a heading or a code block.
        """
        blocks = [b for b in self.blocks if b.kind != "callout"]
        if not blocks or blocks[-1].kind not in ("p", "li", "t"):
            return []
        run = [blocks[-1]]
        for b in reversed(blocks[:-1]):
            if b.kind in ("p", "li", "t") and b.line == run[0].line - 1:
                run.insert(0, b)
            else:
                break
        return run


# ── the book's reading order ───────────────────────────────────────────────


def reading_order(quarto_yml: Path) -> tuple[list[str], list[str], set[str]]:
    """Chapters and appendices from _quarto.yml, in order, plus the part pages.

    A line-based reader, so it needs no PyYAML: every `- <file>.md|.qmd` entry
    under book.chapters is a chapter and under book.appendices an appendix; a
    `part:` whose value is a file names a part page. Pages named part-*.md are
    part pages wherever they appear.
    """
    chapters: list[str] = []
    appendices: list[str] = []
    parts: set[str] = set()
    if not quarto_yml.exists():
        return chapters, appendices, parts
    in_book = False
    key_indent = None
    mode = None
    for raw in quarto_yml.read_text(encoding="utf-8").split("\n"):
        line = raw.split(" #")[0].rstrip()
        if not line.strip() or line.lstrip().startswith("#"):
            continue
        if re.match(r"^\S", line):
            in_book = line.startswith("book:")
            key_indent, mode = None, None
            continue
        if not in_book:
            continue
        key = re.match(r"^(\s*)([\w-]+):", line)
        if key:
            indent = len(key.group(1))
            if key_indent is None:
                key_indent = indent
            if indent <= key_indent:
                mode = key.group(2) if key.group(2) in ("chapters", "appendices") else None
            continue
        if mode is None:
            continue
        part = re.match(r"^\s*-\s+part:\s*[\"']?([^\"']+?)[\"']?\s*$", line)
        if part:
            if re.search(r"\.(md|qmd)$", part.group(1)):
                parts.add(part.group(1))
            continue
        entry = re.match(r"^\s*-\s+(?:file:\s*)?[\"']?([\w./-]+\.(?:md|qmd))[\"']?\s*$", line)
        if entry:
            name = entry.group(1)
            if Path(name).name.startswith("part-"):
                parts.add(name)
            elif mode == "appendices":
                appendices.append(name)
            else:
                chapters.append(name)
    return chapters, appendices, parts


def stem(target: str) -> str:
    return re.sub(r"\.(md|qmd|html)$", "", Path(target).name)


def sibling(target: str) -> str | None:
    """The stem of a link to another page of the book, or None.

    Only a relative link to a file next to this one counts: not a URL, not an
    in-page #fragment, not a file in another directory.
    """
    path = target.split("#")[0].split("?")[0]
    if path.startswith("./"):
        path = path[2:]
    if not path or "/" in path or ":" in path:
        return None
    return stem(path)


def next_chapters(quarto_yml: Path) -> dict[str, str | None]:
    """Maps each chapter to the page its handoff must target.

    Appendices map to None (no handoff needed); pages absent from the map
    aren't in the reading order.
    """
    chapters, appendices, _ = reading_order(quarto_yml)
    order = chapters + appendices
    nxt: dict[str, str | None] = {}
    for i, name in enumerate(chapters):
        nxt[stem(name)] = stem(order[i + 1]) if i + 1 < len(order) else None
    for name in appendices:
        nxt[stem(name)] = None
    return nxt


# ── measures ───────────────────────────────────────────────────────────────


def prose_of(line: str) -> str:
    return INLINE_CODE.sub("", line)


def plain_text(text: str) -> str:
    text = IMAGE.sub("", text)
    text = LINK.sub(lambda m: m.group(1), text)
    text = re.sub(r"^\s*>\s?", "", text)
    return text


def sentences(text: str) -> list[str]:
    protected = ABBREVIATIONS.sub(lambda m: m.group(0).replace(".", "․"), text)
    return [s.strip() for s in SENTENCE_SPLIT.split(protected) if s.strip()]


def word_count(sentence: str) -> int:
    return sum(1 for w in sentence.split() if re.search(r"[A-Za-z0-9]", w))


def measure(name: str, text: str, handoff_to: str | None = None) -> dict:
    """All measures for one page. Returns {"counts": {...}, "details": {...}}.

    `handoff_to` is the stem of the page this one must hand off to (from
    next_chapters()), or None for a page that needs no handoff.
    """
    page = Page(text)
    b = page.blocks
    d: dict[str, list] = {k: [] for k in COUNTS}
    reference = name in REFERENCE_FILES
    glossary = stem(name) == GLOSSARY

    for j, blk in enumerate(b):
        nxt = b[j + 1] if j + 1 < len(b) else None
        prev = b[j - 1] if j > 0 else None
        if blk.kind == "h":
            if nxt is not None and nxt.kind in ("h", "t", "code"):
                entry = (
                    reference
                    and nxt.kind == "code"
                    and nxt.lang != "mermaid"
                    and ENTRY_HEADING.match(blk.text)
                )
                letter = (
                    glossary
                    and nxt.kind == "h"
                    and LETTER_HEADING.match(blk.text)
                    and TERM_HEADING.match(nxt.text)
                )
                if not (entry or letter):
                    d["bare_headings"].append((blk.line, blk.text.strip()))
            if NUMBERED_HEADING.match(blk.text):
                d["numbered_headings"].append((blk.line, blk.text.strip()))
        elif blk.kind == "t":
            if prev is None or prev.kind not in ("t", "p"):
                d["tables_without_lead_in"].append(
                    (blk.line, "after: " + (prev.text.strip()[:80] if prev else "(start of file)"))
                )
        elif blk.kind == "li":
            if BOLD_LEAD.match(blk.text):
                d["bold_lead_bullets"].append((blk.line, blk.text.strip()[:100]))
        elif blk.kind == "callout":
            if CALLOUT_OPEN.match(blk.text):
                d["callouts"].append((blk.line, blk.text.strip()))
        elif blk.kind == "code":
            if blk.lang == "mermaid":
                d["mermaid_blocks"].append((blk.line, "mermaid"))
                body = "\n".join(t for _, t in blk.body)
                if not (FIG_CAP.search(body) and FIG_ALT.search(body)):
                    d["mermaid_uncaptioned"].append((blk.line, "no fig-cap and fig-alt"))
            else:
                for no, line in blk.body:
                    if len(line) > MAX_FENCE_LINE:
                        d["long_fence_lines"].append((no, f"{len(line)} characters: {line.strip()[:60]}"))
        if blk.kind != "code":
            if SCOPE_QUOTE.match(blk.text):
                d["scope_blockquotes"].append((blk.line, blk.text.strip()[:80]))
            prose = prose_of(blk.text)
            for m in CHANGELOG.finditer(prose):
                if m.group(1).lower() == "used to" and PASSIVE_BEFORE_USED.search(prose[: m.start()]):
                    continue
                d["changelog_phrases"].append((blk.line, m.group(1)))
            for m in LINK.finditer(blk.text):
                if sibling(m.group(2)) == GLOSSARY:
                    d["glossary_links"].append((blk.line, m.group(2)))

    # [TODO anywhere, code included: a draft marker has no business on the site.
    for i, line in enumerate(page.lines, 1):
        if "[TODO" in line:
            d["todo_markers"].append((i, line.strip()[:80]))

    d["hr_rules"] = [(no, "---") for no in page.rules]

    # List-heavy sections.
    cur, li, pr, start = None, 0, 0, 0
    for blk in b + [Block("h", len(page.lines) + 1, "#")]:
        if blk.kind == "h":
            if cur and li >= 6 and li >= 3 * max(pr, 1):
                d["list_heavy_sections"].append((start, f"{cur} ({li} list lines, {pr} prose lines)"))
            cur, li, pr, start = blk.text.strip(), 0, 0, blk.line
        elif blk.kind == "li":
            li += 1
        elif blk.kind == "p":
            pr += 1

    # Formula openings: anything before the first H2.
    for blk in b:
        if blk.kind == "h" and blk.text.startswith("## "):
            break
        m = FORMULA.search(prose_of(blk.text)) if blk.kind in ("p", "li") else None
        if m:
            d["formula_openings"].append((blk.line, m.group(0)))

    # Sentence length, over prose paragraphs only (not lists, tables, headings).
    for para in page.paragraphs(kinds=("p",)):
        lines = [x for x in para if not x.text.strip().startswith(("<", ":", "{", "!["))]
        if not lines:
            continue
        text = plain_text(" ".join(x.text.strip() for x in lines))
        for s in sentences(text):
            n = word_count(s)
            if n < MIN_SENTENCE:
                continue
            d["explanatory_sentences"].append((lines[0].line, n))
            if n >= LONG_SENTENCE:
                d["long_sentences"].append((lines[0].line, f"{n} words: {s[:70]}…"))
            if n > VERY_LONG_SENTENCE:
                d["sentences_over_70"].append((lines[0].line, f"{n} words: {s[:70]}…"))

    # Handoff: the last link of the final paragraph targets the next chapter.
    if handoff_to is not None:
        final = page.final_paragraph()
        links = [m.group(2) for blk in final for m in LINK.finditer(blk.text)]
        last = links[-1] if links else ""
        if sibling(last) != handoff_to:
            where = final[0].line if final else len(page.lines)
            got = f"ends on a link to {last}" if last else "has no link"
            d["broken_handoff"].append((where, f"final paragraph {got}; expected {handoff_to}.md"))

    counts = {k: len(v) for k, v in d.items()}
    explanatory = counts["explanatory_sentences"]
    counts["long_sentence_share"] = (
        round(100.0 * counts["long_sentences"] / explanatory, 1) if explanatory else 0.0
    )
    d["explanatory_sentences"] = []
    d["long_sentence_share"] = []
    details = {k: [{"line": no, "text": t} for no, t in v] for k, v in d.items() if v}
    return {"counts": {k: counts[k] for k in COUNTS}, "details": details}


# ── the book ───────────────────────────────────────────────────────────────


def book_files(book_dir: Path) -> list[Path]:
    files = [p for p in sorted(book_dir.glob("*.md")) if p.name not in EXCLUDED]
    index = book_dir / "index.qmd"
    if index.exists():
        files.append(index)
    return files


def measure_files(files: list[Path], book_dir: Path) -> dict[str, dict]:
    nxt = next_chapters(book_dir / "_quarto.yml")
    out = {}
    for f in files:
        out[f.name] = measure(f.name, f.read_text(encoding="utf-8"), handoff_to=nxt.get(stem(f.name)))
    return out


# ── the ratchet ────────────────────────────────────────────────────────────


def check(results: dict[str, dict], baseline: dict) -> tuple[list[str], list[str]]:
    """Returns (failures, improvements) as printable lines."""
    base_files = baseline.get("files", {})
    failures: list[str] = []
    improvements: list[str] = []
    for name, res in sorted(results.items()):
        counts = res["counts"]
        base = base_files.get(name)
        for key in HARD_ZERO:
            if counts[key]:
                failures.append(f"{name}: {LABELS[key]} must be 0, found {counts[key]}")
                failures += [f"    L{x['line']}: {x['text']}" for x in res["details"].get(key, [])]
        for key in RATCHETED:
            allowed = (base or {}).get(key, 0)
            now = counts[key]
            if now > allowed:
                what = "new page, so it must be 0" if base is None else f"baseline {allowed}"
                failures.append(f"{name}: {LABELS[key]} {now} ({what})")
                failures += [f"    L{x['line']}: {x['text']}" for x in res["details"].get(key, [])]
            elif now < allowed:
                improvements.append(f"{name}: {LABELS[key]} {allowed} -> {now}")
    return failures, improvements


def stale_pages(results: dict[str, dict], baseline: dict) -> list[str]:
    """Pages the baseline lists that weren't measured (renamed or deleted)."""
    return sorted(set(baseline.get("files", {})) - set(results))


def baseline_of(results: dict[str, dict]) -> dict:
    return {
        "about": (
            "Per-page counts that scripts/check-book-style.sh won't let grow (see "
            "scripts/book_metrics.py). A page missing here must be 0 on all of them. "
            f"After lowering a count, record it with: {UPDATE_HINT}"
        ),
        "files": {name: {k: res["counts"][k] for k in RATCHETED} for name, res in sorted(results.items())},
    }


# ── output ─────────────────────────────────────────────────────────────────


def report(results: dict[str, dict]) -> str:
    out = []
    for name, res in results.items():
        c = res["counts"]
        out.append(f"== {name}")
        for key in COUNTS:
            if key in ("explanatory_sentences", "long_sentence_share"):
                continue
            label = LABELS[key]
            if key == "long_sentences":
                out.append(
                    f"{label}: {c[key]} of {c['explanatory_sentences']} ({c['long_sentence_share']}%)"
                )
            else:
                out.append(f"{label}: {c[key]}")
            if key in ("callouts", "mermaid_blocks"):
                continue
            for x in res["details"].get(key, []):
                out.append(f"  L{x['line']}: {x['text']}")
        out.append("")
    return "\n".join(out)


SUMMARY_COLUMNS = [
    ("bare", "bare_headings"),
    ("tbl", "tables_without_lead_in"),
    ("heavy", "list_heavy_sections"),
    ("bold", "bold_lead_bullets"),
    ("num", "numbered_headings"),
    ("open", "formula_openings"),
    ("hist", "changelog_phrases"),
    ("40+%", "long_sentence_share"),
    (">70", "sentences_over_70"),
    ("fence", "long_fence_lines"),
    ("mmd", "mermaid_blocks"),
    ("nocap", "mermaid_uncaptioned"),
    ("todo", "todo_markers"),
    ("scope", "scope_blockquotes"),
    ("hr", "hr_rules"),
    ("gloss", "glossary_links"),
    ("hand", "broken_handoff"),
]


def totals(results: dict[str, dict]) -> dict:
    tot = {k: sum(r["counts"][k] for r in results.values()) for k in COUNTS if k != "long_sentence_share"}
    tot["long_sentence_share"] = (
        round(100.0 * tot["long_sentences"] / tot["explanatory_sentences"], 1)
        if tot["explanatory_sentences"]
        else 0.0
    )
    return tot


def summary(results: dict[str, dict]) -> str:
    width = max([len(n) for n in results] + [5])
    head = "file".ljust(width) + "".join(f"{h:>7}" for h, _ in SUMMARY_COLUMNS)
    rows = [head]
    for name, res in results.items():
        rows.append(name.ljust(width) + "".join(f"{res['counts'][k]:>7}" for _, k in SUMMARY_COLUMNS))
    tot = totals(results)
    rows.append("total".ljust(width) + "".join(f"{tot[k]:>7}" for _, k in SUMMARY_COLUMNS))
    legend = ", ".join(f"{h} = {LABELS[k]}" for h, k in SUMMARY_COLUMNS)
    return "\n".join(rows) + "\n\n" + legend + "\n"


def main(argv: list[str] | None = None) -> int:
    ap = argparse.ArgumentParser(description=__doc__.split("\n\n")[0])
    ap.add_argument("files", nargs="*", help="pages to measure (default: the whole book)")
    ap.add_argument("--book-dir", default=str(BOOK_DIR), help="the book's directory (default: docs/book)")
    mode = ap.add_mutually_exclusive_group()
    mode.add_argument("--json", action="store_true", help="print every count and finding as JSON")
    mode.add_argument("--summary", action="store_true", help="print one row of counts per page")
    mode.add_argument("--check", metavar="BASELINE", help="fail if a ratcheted count exceeds BASELINE")
    mode.add_argument("--update", metavar="BASELINE", help="write today's ratcheted counts to BASELINE")
    args = ap.parse_args(argv)

    book_dir = Path(args.book_dir)
    if args.update and args.files:
        ap.error("--update measures the whole book; don't pass files")
    files = [Path(f) for f in args.files] if args.files else book_files(book_dir)
    missing = [str(f) for f in files if not f.is_file()]
    if missing:
        ap.error("no such file: " + ", ".join(missing))
    results = measure_files(files, book_dir)

    if args.update:
        Path(args.update).write_text(json.dumps(baseline_of(results), indent=2) + "\n", encoding="utf-8")
        print(f"wrote {args.update} ({len(results)} pages)")
        return 0
    if args.check:
        baseline = json.loads(Path(args.check).read_text(encoding="utf-8"))
        failures, improvements = check(results, baseline)
        if improvements:
            print(f"Counts below the baseline; record them with: {UPDATE_HINT}")
            for line in improvements:
                print(f"  {line}")
        stale = stale_pages(results, baseline) if not args.files else []
        if stale:
            print(f"The baseline lists pages that are gone ({', '.join(stale)}); drop them with: {UPDATE_HINT}")
        if failures:
            print("Book structure ratchet: counts above the baseline", file=sys.stderr)
            for line in failures:
                print(f"  {line}", file=sys.stderr)
            print(
                "\nA page's counts may fall but never rise, a new page starts at 0, and\n"
                "[TODO is never allowed. Fix the lines above (docs/book/STYLE.md says how).\n"
                "If the change is deliberate, such as a chapter split in two, refresh the\n"
                f"baseline in the same commit:\n  {UPDATE_HINT}",
                file=sys.stderr,
            )
            return 1
        print(f"OK: book structure ratchet ({len(results)} pages within {args.check})")
        return 0
    if args.json:
        print(json.dumps({"files": results, "totals": totals(results)}, indent=2))
        return 0
    if args.summary:
        print(summary(results))
        return 0
    print(report(results))
    tot = totals(results)
    print("== totals")
    print("\n".join(f"{LABELS[k]}: {tot[k]}" for k in COUNTS))
    return 0


if __name__ == "__main__":
    sys.exit(main())

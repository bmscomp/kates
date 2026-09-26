#!/usr/bin/env bash
# Build-time conversion of the book sources for Quarto rendering.
#
# The committed sources are GitHub-flavored markdown (.md with ```mermaid
# fences) so chapters render in the GitHub file view. Quarto can only execute
# mermaid diagrams inside .qmd files with ```{mermaid} cells, so CI runs this
# script on its checkout right before `quarto render` — the conversion is
# never committed. After checking the Part pages (below), it:
#   1. converts ```mermaid fences to ```{mermaid} executable cells
#   2. points .md links that leave docs/book (../x.md) at the file on GitHub —
#      the site holds only the book, so they would resolve to nothing — then
#      rewrites the remaining relative links from .md to .qmd (URLs untouched)
#   3. renames chapter .md files to .qmd (README.md stays — not a chapter)
#   4. updates the chapter list in _quarto.yml accordingly, `part:` lines too
#
# A Part page is the file a `- part: part-<slug>.md` entry in _quarto.yml
# names: its H1 is the Part's label in the sidebar and the PDF, and its
# bullets list the Part's chapters. Nothing else keeps that list in step with
# _quarto.yml, so the build fails when a Part page has front matter (GitHub
# shows it as a table), when its chapter bullets (`- [Title](file.md)...`) do
# not name the Part's chapters in _quarto.yml's order, or when a link to a
# whole chapter does not use that chapter's H1 title as its text.
set -euo pipefail

cd "$(dirname "$0")/../docs/book"

python3 - <<'PY'
import glob, os, posixpath, re, sys

GITHUB = 'https://github.com/bmscomp/kates/blob/main/'

def github_link(m):
    path = posixpath.normpath(posixpath.join('docs/book', m.group(1)))
    return f']({GITHUB}{path}{m.group(2) or ""})'

def convert_body(text):
    text = re.sub(r'^```mermaid[ \t]*$', '```{mermaid}', text, flags=re.M)
    text = re.sub(r'\]\((\.\./[^)#\s]+\.md)(#[^)]*)?\)', github_link, text)
    text = re.sub(r'\]\((?!https?://)([^)#\s]+)\.md(#[^)]*)?\)', r'](\1.qmd\2)', text)
    return text

def h1(path):
    with open(path, encoding='utf-8') as fh:
        for line in fh:
            if line.startswith('# '):
                return re.sub(r'\s*\{[^}]*\}\s*$', '', line[2:]).strip()
    return None

def part_pages(qy):
    """(page, [chapters]) for each `- part: <file>.md` entry, in order."""
    parts, current, indent = [], None, -1
    for line in qy.splitlines():
        m = re.match(r'^(\s*)- part:\s*([\w][\w\-]*\.md)\s*$', line)
        if m:
            current, indent = (m.group(2), []), len(m.group(1))
            parts.append(current)
            continue
        if current is None or not line.strip() or line.lstrip().startswith('#'):
            continue
        chapter = re.match(r'^\s*- ([\w][\w\-]*\.md)\s*$', line)
        if len(line) - len(line.lstrip()) <= indent:
            current = None
        elif chapter:
            current[1].append(chapter.group(1))
    return parts

with open('_quarto.yml', encoding='utf-8') as fh:
    problems = []
    for page, chapters in part_pages(fh.read()):
        if not os.path.exists(page):
            problems.append(f'{page}: named by a `part:` entry in _quarto.yml, but missing')
            continue
        with open(page, encoding='utf-8') as pf:
            text = pf.read()
        if not text.startswith('# '):
            problems.append(f'{page}: must open with its H1, not front matter (GitHub shows that as a table)')
        listed = [f for _, f in re.findall(r'^[-*] \[([^\]]+)\]\(([\w\-]+\.md)\)', text, flags=re.M)]
        if listed != chapters:
            problems.append(f'{page}: its chapter bullets name {listed}, but _quarto.yml gives this Part {chapters}')
        for title, f in re.findall(r'\[([^\]]+)\]\(([\w\-]+\.md)\)', text):
            want = h1(f) if os.path.exists(f) else None
            if title != want:
                problems.append(f"{page}: [{title}]({f}) must use the chapter's H1 as its text: {want!r}")
    for p in problems:
        print(f'BOOK: {p}', file=sys.stderr)
    if problems:
        sys.exit(1)

for f in sorted(glob.glob('*.md')):
    if f == 'README.md':
        continue
    with open(f, encoding='utf-8') as fh:
        body = fh.read()
    with open(f[:-3] + '.qmd', 'w', encoding='utf-8') as fh:
        fh.write(convert_body(body))
    os.remove(f)

with open('index.qmd', encoding='utf-8') as fh:
    idx = fh.read()
with open('index.qmd', 'w', encoding='utf-8') as fh:
    fh.write(convert_body(idx))

with open('_quarto.yml', encoding='utf-8') as fh:
    qy = fh.read()
qy = re.sub(r'^(\s*- (?:part:\s*)?)([\w][\w\-]*)\.md(\s*)$', r'\1\2.qmd\3', qy, flags=re.M)
with open('_quarto.yml', 'w', encoding='utf-8') as fh:
    fh.write(qy)

print(f"prepared {len(glob.glob('*.qmd')) - 1} chapters for Quarto render")
PY

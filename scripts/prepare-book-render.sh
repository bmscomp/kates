#!/usr/bin/env bash
# Build-time conversion of the book sources for Quarto rendering.
#
# The committed sources are GitHub-flavored markdown (.md with ```mermaid
# fences) so chapters render in the GitHub file view. Quarto can only execute
# mermaid diagrams inside .qmd files with ```{mermaid} cells, so CI runs this
# script on its checkout right before `quarto render` — the conversion is
# never committed. It:
#   1. converts ```mermaid fences to ```{mermaid} executable cells
#   2. rewrites relative intra-book links from .md to .qmd (URLs untouched)
#   3. renames chapter .md files to .qmd (README.md stays — not a chapter)
#   4. updates the chapter list in _quarto.yml accordingly
set -euo pipefail

cd "$(dirname "$0")/../docs/book"

python3 - <<'PY'
import glob, os, re

def convert_body(text):
    text = re.sub(r'^```mermaid[ \t]*$', '```{mermaid}', text, flags=re.M)
    text = re.sub(r'\]\((?!https?://)([^)#\s]+)\.md(#[^)]*)?\)', r'](\1.qmd\2)', text)
    return text

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
qy = re.sub(r'^(\s*- )([\w][\w\-]*)\.md(\s*)$', r'\1\2.qmd\3', qy, flags=re.M)
with open('_quarto.yml', 'w', encoding='utf-8') as fh:
    fh.write(qy)

print(f"prepared {len(glob.glob('*.qmd')) - 1} chapters for Quarto render")
PY

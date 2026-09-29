#!/usr/bin/env bash
# Parse every ```mermaid block in the repository's markdown.
#
# Until this existed, a mermaid syntax error surfaced in one of two ways: the
# book's PDF render failed in CI with a message about a diagram nobody could
# locate, or — for the diagrams in charts/, which never go through Quarto —
# it did not surface at all and simply rendered as an error box on GitHub.
#
# Needs node and npm. Skips cleanly without them, like the promtool check in
# the MirrorMaker 2 workflow, so a contributor with no node installed is not
# blocked from running the rest of the gates.
#
# Usage: scripts/check-mermaid.sh [--quiet]
set -euo pipefail

cd "$(dirname "$0")/.."

if ! command -v node >/dev/null 2>&1 || ! command -v npm >/dev/null 2>&1; then
  echo "SKIP: mermaid check needs node and npm (not installed)"
  exit 0
fi

WORKDIR="${MERMAID_CHECK_DIR:-.build/mermaid-check}"
mkdir -p "$WORKDIR"

if [ ! -d "$WORKDIR/node_modules/mermaid" ]; then
  echo "Installing the mermaid parser into $WORKDIR (once)..."
  (cd "$WORKDIR" && npm install --silent --no-package-lock mermaid@11 jsdom >/dev/null 2>&1) || {
    echo "SKIP: could not install the mermaid parser (offline?)"
    exit 0
  }
fi

cat > "$WORKDIR/check.mjs" <<'JS'
// mermaid.parse() is the same validation the renderer does first, so a block
// that passes here cannot fail to parse in the book or on GitHub. It needs a
// DOM (DOMPurify), hence jsdom.
import fs from 'node:fs';
import path from 'node:path';
import { JSDOM } from 'jsdom';

const dom = new JSDOM('<!doctype html><body></body>', { pretendToBeVisual: true });
global.window = dom.window;
global.document = dom.window.document;
Object.defineProperty(global, 'navigator', { value: dom.window.navigator, configurable: true });
for (const k of ['Element', 'HTMLElement', 'SVGElement', 'Node']) global[k] = dom.window[k];

const { default: mermaid } = await import('mermaid');
mermaid.initialize({ startOnLoad: false });

const quiet = process.env.MERMAID_QUIET === '1';
let bad = 0, total = 0;
for (const f of process.argv.slice(2)) {
  const text = fs.readFileSync(f, 'utf8');
  const blocks = [...text.matchAll(/```mermaid\n([\s\S]*?)```/g)];
  for (const [i, m] of blocks.entries()) {
    total++;
    // The line the block starts on, so a failure is navigable.
    const line = text.slice(0, m.index).split('\n').length;
    try {
      await mermaid.parse(m[1]);
      if (!quiet) console.log(`  ok    ${f}:${line}`);
    } catch (e) {
      bad++;
      console.log(`  FAIL  ${f}:${line}  ${String(e.message).split('\n').slice(0, 3).join(' | ')}`);
    }
  }
}
console.log(`\n${total} mermaid diagram(s), ${bad} invalid`);
process.exit(bad ? 1 : 0);
JS

QUIET=""
[ "${1:-}" = "--quiet" ] && QUIET=1
# git ls-files, not a recursive grep: the working tree can contain scratch
# worktrees under .claude/, vendored node_modules and a rendered _book/, none
# of which are ours to validate. Tracked files are exactly the ones a reviewer
# will see.
mapfile -t FILES < <(git ls-files -z '*.md' '*.qmd' 2>/dev/null | xargs -0 grep -l '```mermaid' 2>/dev/null | sort)

if [ "${#FILES[@]}" -eq 0 ]; then
  echo "OK: no mermaid diagrams found"
  exit 0
fi

if MERMAID_QUIET="$QUIET" node "$WORKDIR/check.mjs" "${FILES[@]}"; then
  echo "OK: every mermaid diagram parses"
else
  echo "Mermaid syntax errors above — the file:line is where the block starts." >&2
  exit 1
fi

#!/usr/bin/env bash
# Enforce the machine-checkable rules of docs/book/STYLE.md.
# Usage: scripts/check-book-style.sh   (exit 1 on violations; used by docs CI)
set -euo pipefail

cd "$(dirname "$0")/.."
fail=0

note() { echo "STYLE: $1" >&2; fail=1; }

# The book's pages: docs/book/*.md and index.qmd, less the pages about the book
# rather than in it (the same set scripts/book_metrics.py measures).
pages=()
for f in docs/book/*.md; do
  case "${f##*/}" in STYLE.md|README.md|CONCEPTS.md) continue ;; esac
  pages+=("$f")
done
pages+=(docs/book/index.qmd)

# 1. Unlabeled code fences (openers with no language tag)
while IFS= read -r hit; do
  note "unlabeled code fence -> tag it (text for output/ASCII UI): $hit"
done < <(python3 - "${pages[@]}" <<'PY'
import re, sys
for f in sys.argv[1:]:
    fence = False
    for i, l in enumerate(open(f, encoding='utf-8'), 1):
        s = l.strip()
        if s.startswith('```'):
            if not fence and s == '```':
                print(f"{f}:{i}")
            fence = not fence
PY
)

# 2. Bold-blockquote admonitions
if grep -n '^> \*\*\(Note\|Tip\|Warning\|Important\|Caution\):\*\*' "${pages[@]}" >&2; then
  note "bold-blockquote admonition -> use ::: callout-*"
fi

# 3. Chapter-number cross references (numbers drift; use titles)
if grep -nE '\[(Chapter|Ch\.?) [0-9]' "${pages[@]}" >&2; then
  note "'Chapter N' in link text -> use the target's H1 title"
fi

# 4. Banned terminology (prose only — fenced code and inline code spans are
#    exempt, since command/resource names are what they are)
if python3 - "${pages[@]}" >&2 <<'PY'
import re, sys
BANNED = [r'GameDay', r'\bpreflight\b', r'\bKATES\b']
bad = False
for f in sys.argv[1:]:
    fence = False
    for i, l in enumerate(open(f, encoding='utf-8'), 1):
        if l.strip().startswith('```'):
            fence = not fence
            continue
        if fence:
            continue
        prose = re.sub(r'`[^`]*`', '', l)
        for pat in BANNED:
            if re.search(pat, prose):
                print(f"{f}:{i}: banned term /{pat}/: {l.strip()[:100]}")
                bad = True
sys.exit(0 if bad else 1)
PY
then
  note "banned terminology in prose (see STYLE.md terminology table)"
fi

# 5. Double blank line after callout close
if python3 - "${pages[@]}" <<'PY'
import re, sys
bad = False
for f in sys.argv[1:]:
    txt = open(f, encoding='utf-8').read()
    for m in re.finditer(r':::\n\n\n+', txt):
        print(f"{f}: double blank line after callout at offset {m.start()}", file=sys.stderr)
        bad = True
sys.exit(0 if bad else 1)
PY
then
  note "normalize to exactly one blank line after ':::'"
fi

# 6. Structure ratchet (scripts/book_metrics.py, tested first): per-page counts
#    of bare headings, tables without a lead-in, hand-numbered headings, broken
#    handoffs, long fenced lines and the rest may fall but never rise above
#    scripts/book-metrics-baseline.json; a new page starts at zero, and '[TODO'
#    is zero everywhere.
if ! tests=$(python3 -m unittest discover -s scripts -p 'test_book_metrics.py' -q 2>&1); then
  echo "$tests" >&2
  note "scripts/test_book_metrics.py fails -> fix scripts/book_metrics.py first"
elif ! python3 scripts/book_metrics.py --check scripts/book-metrics-baseline.json; then
  note "book structure ratchet (see above)"
fi

if [[ $fail -ne 0 ]]; then
  echo "" >&2
  echo "Book style violations found — see docs/book/STYLE.md." >&2
  exit 1
fi
echo "OK: book style checks pass"

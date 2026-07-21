#!/usr/bin/env bash
# Enforce the machine-checkable rules of docs/book/STYLE.md.
# Usage: scripts/check-book-style.sh   (exit 1 on violations; used by docs CI)
set -euo pipefail

cd "$(dirname "$0")/.."
fail=0

note() { echo "STYLE: $1" >&2; fail=1; }

# 1. Unlabeled code fences (openers with no language tag)
while IFS= read -r hit; do
  note "unlabeled code fence -> tag it (text for output/ASCII UI): $hit"
done < <(python3 - <<'PY'
import glob, re
for f in sorted(glob.glob('docs/book/*.md') + ['docs/book/index.qmd']):
    if f.endswith('STYLE.md'):
        continue
    fence = False
    for i, l in enumerate(open(f, encoding='utf-8'), 1):
        s = l.strip()
        if s.startswith('```'):
            if not fence and s == '```':
                print(f"{f}:{i}")
            fence = not fence
PY
)

# 2. Bold-blockquote admonitions — any `> **…**` opener, not just the five
#    admonition words (a `> **Scope**:` slips the same rule). Genuine
#    quotations are still fine: plain `> text` without a bold lead.
if grep -rnE '^\s*> \*\*' docs/book/*.md >&2; then
  note "bold-blockquote admonition -> use ::: callout-*"
fi

# 3. Chapter-number cross references (numbers drift; use titles)
if grep -rnE '\[(Chapter|Ch\.?) [0-9]' docs/book/*.md docs/book/index.qmd >&2; then
  note "'Chapter N' in link text -> use the target's H1 title"
fi

# 4. Banned terminology (prose only — fenced code and inline code spans are
#    exempt, since command/resource names are what they are)
if python3 - >&2 <<'PY'
import glob, re, sys
BANNED = [r'GameDay', r'\bpreflight\b', r'\bKATES\b']
bad = False
for f in sorted(glob.glob('docs/book/*.md') + ['docs/book/index.qmd']):
    if f.endswith('STYLE.md'):
        continue
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
if python3 - <<'PY'
import glob, re, sys
bad = False
for f in sorted(glob.glob('docs/book/*.md') + ['docs/book/index.qmd']):
    txt = open(f, encoding='utf-8').read()
    for m in re.finditer(r':::\n\n\n+', txt):
        print(f"{f}: double blank line after callout at offset {m.start()}", file=sys.stderr)
        bad = True
sys.exit(0 if bad else 1)
PY
then
  note "normalize to exactly one blank line after ':::'"
fi

if [[ $fail -ne 0 ]]; then
  echo "" >&2
  echo "Book style violations found — see docs/book/STYLE.md." >&2
  exit 1
fi
echo "OK: book style checks pass"

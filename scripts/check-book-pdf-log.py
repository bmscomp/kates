#!/usr/bin/env python3
"""Read a LaTeX log and the kept .tex of the book PDF and report what a reader would see:
overfull lines by kind (code, table, prose, image, url) with the PDF page, underfull
vertical boxes, missing characters, float and hyperref warnings.

  scripts/check-book-pdf-log.py <index.log> <book.tex> [--max-code 2] [--max-prose 2] [--max-table 2]
                  [--allow-missing '❌ ✅ ⛔ 🔥 📊 ⏳']

Exit 1 when an overfull box of a kind exceeds its threshold (points), when a character
outside the allowed list is missing, or when hyperref reports a duplicate destination.
"""
import re, sys, argparse

ap = argparse.ArgumentParser()
ap.add_argument('log'); ap.add_argument('tex')
ap.add_argument('--max-code', type=float, default=2); ap.add_argument('--max-prose', type=float, default=2)
ap.add_argument('--max-table', type=float, default=2); ap.add_argument('--max-image', type=float, default=1)
ap.add_argument('--allow-missing', default='')
ap.add_argument('--list', action='store_true', help='print every overfull box')
a = ap.parse_args()

log = open(a.log, encoding='utf-8', errors='replace').read()
tex = open(a.tex, encoding='utf-8').read().split('\n')

# Which environment each .tex line sits in (code, table, prose), by a forward scan
kind_at = [None] * (len(tex) + 2)
stack = []
for i, line in enumerate(tex, 1):
    for m in re.finditer(r'\\begin\{(Highlighting|Verbatim|longtable|katesoutput|Shaded)\}', line):
        stack.append(m.group(1))
    kind = 'prose'
    if stack:
        top = stack[-1]
        kind = 'table' if top == 'longtable' else 'code'
    if '\\includegraphics' in line:
        kind = 'image'
    elif kind == 'prose' and re.search(r'\\url\{|\\href\{', line):
        kind = 'url'
    kind_at[i] = kind
    for m in re.finditer(r'\\end\{(Highlighting|Verbatim|longtable|katesoutput|Shaded)\}', line):
        if stack and stack[-1] == m.group(1):
            stack.pop()

# Walk the log, tracking the page being shipped out
page = 1
overfull, underfull_v, missing, dupdest, floats, bookmarks = [], [], {}, [], [], []
lines = log.split('\n')
for n, line in enumerate(lines):
    for m in re.finditer(r'\[(\d+)[\]\s{]', line):
        page = int(m.group(1)) + 1
    m = re.match(r'Overfull \\hbox \(([\d.]+)pt too wide\) in paragraph at lines (\d+)--(\d+)', line)
    if m:
        pts, s, e = float(m.group(1)), int(m.group(2)), int(m.group(3))
        kinds = {kind_at[i] for i in range(s, e + 1) if i < len(kind_at)}
        kind = 'image' if 'image' in kinds else 'table' if 'table' in kinds else 'code' if 'code' in kinds else 'url' if 'url' in kinds else 'prose'
        sample = lines[n + 1].strip()[:90] if n + 1 < len(lines) else ''
        overfull.append((pts, kind, page, s, sample))
        continue
    m = re.match(r'Overfull \\hbox \(([\d.]+)pt too wide\) (detected|has occurred)', line)
    if m:
        overfull.append((float(m.group(1)), 'other', page, 0, lines[n + 1].strip()[:90] if n + 1 < len(lines) else ''))
    if line.startswith('Underfull \\vbox'):
        underfull_v.append((page, line[:80]))
    m = re.search(r'Missing character: There is no (.+?) \((U\+[0-9A-F]+)\)', line)
    if m:
        missing.setdefault(m.group(2), [m.group(1), 0])[1] += 1
    if 'destination with the same identifier' in line:
        dupdest.append((page, lines[n + 1].strip()[:80] if n + 1 < len(lines) else ''))
    if 'Token not allowed in a PDF string' in line:
        bookmarks.append(page)
    if 'float specifier changed' in line or 'Too many unprocessed floats' in line:
        floats.append((page, line.strip()[:80]))

bad = False
limits = {'code': a.max_code, 'prose': a.max_prose, 'table': a.max_table, 'image': a.max_image, 'url': a.max_prose, 'other': 99}
allowed = set(a.allow_missing.split())
print(f'overfull hboxes: {len(overfull)}')
by_kind = {}
for pts, kind, pg, s, sample in overfull:
    by_kind.setdefault(kind, []).append(pts)
for kind, l in sorted(by_kind.items()):
    over = [p for p in l if p > limits[kind]]
    print(f'  {kind:6s} {len(l):4d}  over {limits[kind]}pt: {len(over):4d}  widest {max(l):.1f}pt')
    if over:
        bad = True
if a.list:
    for pts, kind, pg, s, sample in sorted(overfull, reverse=True):
        if pts > limits[kind]:
            print(f'    {pts:7.1f}pt  {kind:6s} p.{pg:<4d} tex:{s:<6d} {sample}')
print(f'underfull vboxes: {len(underfull_v)}')
print(f'missing characters: {len(missing)} distinct')
for code, (ch, cnt) in sorted(missing.items()):
    flag = '' if ch in allowed else '  <-- not allowed'
    if flag:
        bad = True
    print(f'  {ch} {code} x{cnt}{flag}')
print(f'duplicate hyperref destinations: {len(dupdest)}' + (f' (first on p.{dupdest[0][0]}: {dupdest[0][1]})' if dupdest else ''))
if dupdest:
    bad = True
print(f'bookmark token warnings: {len(bookmarks)}; float warnings: {len(floats)}')
m = re.search(r'Output written on .*? \((\d+) pages', log)
print(f'pages: {m.group(1) if m else "?"}')
print('PDF log check: ' + ('FAILED' if bad else 'passed'))
sys.exit(1 if bad else 0)

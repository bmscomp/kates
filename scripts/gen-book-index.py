#!/usr/bin/env python3
"""Generate the book's Index page from the committed term list.

  gen-book-index.py <docs/book dir> [--check]

Reads <book>/index-terms.txt and the chapters in _quarto.yml's order, and writes
<book>/appendix-index.md: every term with the sections that teach it, grouped by
letter. With --check it exits 1 when the committed page differs from what it would
write. The PDF gets the same entries as page numbers from theme/index-entries.lua,
which reads the same list and applies the same rule.

Term list format, one entry per line (# comments):
  Term                      the entry; matched case-insensitively on word boundaries
  Term; alias; alias        the aliases match too, the entry is listed under Term
  Parent > Child; alias     a sub-entry, listed under Parent as "Parent — Child"
  Term -> Other             a "see" entry: lists nothing, points at Other
A section is listed when it uses the term twice or more, or names it in its heading;
a term no section uses twice is listed at the one or two sections that use it most.
Part pages, the Glossary, Summary sections, code, headings' attributes, callout fences
and link targets are not searched.
"""
import os, re, sys

book = sys.argv[1]
check = '--check' in sys.argv
terms_path = os.path.join(book, 'index-terms.txt')
out_path = os.path.join(book, 'appendix-index.md')
MAX_SECTIONS = 8

qy = open(os.path.join(book, '_quarto.yml'), encoding='utf-8').read()
order = []
for line in qy.splitlines():
    m = re.match(r'''^\s*- (?:part:\s*)?["']?([\w][\w\-]*\.(?:md|qmd))["']?\s*$''', line)
    if m:
        order.append(m.group(1))
ext = '.qmd' if any(f.endswith('.qmd') and f != 'index.qmd' for f in order) else '.md'
SKIP_FILES = re.compile(r'^(part-.*|appendix-a-glossary|appendix-index|references)\.(md|qmd)$')

entries = []
for raw in open(terms_path, encoding='utf-8'):
    line = raw.split('#', 1)[0].strip()
    if not line:
        continue
    see = None
    if '->' in line:
        line, see = [s.strip() for s in line.split('->', 1)]
    parts = [p.strip() for p in line.split(';') if p.strip()]
    head, aliases = parts[0], parts[1:]
    parent = None
    if '>' in head:
        parent, head = [s.strip() for s in head.split('>', 1)]
    entries.append(dict(term=head, parent=parent, aliases=aliases, see=see))

def pattern(words):
    alts = sorted({re.escape(w) for w in words}, key=len, reverse=True)
    return re.compile(r'(?<![\w`])(?:' + '|'.join(alts) + r')(?![\w`])', re.I)

for e in entries:
    e['rx'] = pattern([e['term']] + e['aliases']) if not e['see'] else None

def pandoc_id(title):
    kept = ''.join(c for c in title.lower() if c.isalnum() or c in '_-. \t')
    return re.sub(r'^[^a-z]+', '', '-'.join(kept.split()))

def github_slug(title):
    s = re.sub(r'[^\w\- ]', '', title.lower().replace('`', ''))
    return s.replace(' ', '-')

def strip_attrs(title):
    return re.sub(r'\s*\{[^}]*\}\s*$', '', title).strip()

# ── Scan: occurrences per (entry, section) ─────────────────────────────────
counts = {id(e): [] for e in entries}  # entry -> [(count, order, file, chapter, section, anchor)]
pos = 0
for f in order:
    path = os.path.join(book, f)
    if SKIP_FILES.match(f) or not os.path.exists(path):
        continue
    chapter, section, anchor = None, None, None
    buf, fence = [], False

    def flush():
        global pos
        if chapter is None or not buf or (section or '').lower().startswith('summary'):
            return
        # Code keeps its text, as pandoc.utils.stringify does in the PDF filter
        text = re.sub(r'`([^`]*)`', r'\1', '\n'.join(buf))
        text = re.sub(r'\]\([^)]*\)', ']', text)
        heading = (section or '')
        pos += 1
        for e in entries:
            if not e['rx']:
                continue
            n = len(e['rx'].findall(text))
            if e['rx'].search(heading):
                n += 2
            if n:
                counts[id(e)].append((n, pos, f, chapter, section, anchor))

    for line in open(path, encoding='utf-8'):
        s = line.rstrip('\n')
        if s.strip().startswith('```'):
            fence = not fence
            continue
        if fence:
            continue
        m = re.match(r'^(#{1,2}) (.+?)\s*$', s)
        if m:
            flush(); buf = []
            raw_title = m.group(2)
            title = strip_attrs(raw_title)
            if len(m.group(1)) == 1:
                chapter, section, anchor = title, None, None
            else:
                section = title
                mid = re.search(r'\{#([\w\-]+)', raw_title)
                # A link needs an id that Quarto renders and lychee (GitHub's
                # slugs) also finds: an explicit one, or a title both agree on
                anchor = mid.group(1) if mid else (pandoc_id(title) if pandoc_id(title) == github_slug(title) else None)
            continue
        if s.strip().startswith(':::') or s.strip().startswith('%%|') or re.match(r'^\s*\|[-:| ]+\|\s*$', s):
            continue
        buf.append(s)
    flush()

def chosen(e):
    """The sections an entry lists: those that use it twice or name it, else the
    one or two that use it most; at most MAX_SECTIONS, in reading order."""
    rows = counts[id(e)]
    strong = [r for r in rows if r[0] >= 2]
    if not strong:
        strong = sorted(rows, key=lambda r: -r[0])[:2]
    strong = sorted(strong, key=lambda r: -r[0])[:MAX_SECTIONS]
    return sorted(strong, key=lambda r: r[1])

def link(f, chapter, section, anchor):
    target = f if f == 'index.qmd' else re.sub(r'\.(md|qmd)$', ext, f)
    if section and anchor:
        return f'[{section}]({target}#{anchor})'
    return section or chapter

def refs(e):
    by_chapter = {}
    for n, p, f, ch, sec, anc in chosen(e):
        by_chapter.setdefault((f, ch), []).append((sec, anc))
    parts = []
    for (f, ch), secs in by_chapter.items():
        target = f if f == 'index.qmd' else re.sub(r'\.(md|qmd)$', ext, f)
        parts.append(f'[{ch}]({target}): ' + ', '.join(link(f, ch, s, a) for s, a in secs))
    return '; '.join(parts) if parts else 'Not used in a chapter yet.'

lines = ['# Index {.unnumbered}', '',
         '::: {.content-visible when-format="pdf"}', '```{=latex}', '\\printindex', '```', ':::', '',
         '::: {.content-visible when-format="html"}',
         'Every term the book teaches, with the sections that teach it: the ones that use the term more than once or name it in their heading, by chapter and in reading order. The [Glossary](appendix-a-glossary.md) defines each term; this page says where to read about it. The PDF edition lists the same entries by page number.', '']
letter = None
by_parent = {}
for e in entries:
    if e['parent']:
        by_parent.setdefault(e['parent'].lower(), []).append(e)
for e in sorted(entries, key=lambda e: (e['term'].lower(),)):
    if e['parent']:
        continue
    first = e['term'][0].upper()
    first = first if first.isalpha() else '#'
    if first != letter:
        letter = first
        lines += [f'## {letter}', '']
    slug = pandoc_id(e['term'])
    if e['see']:
        lines += [f'[{e["term"]}]{{#idx-{slug}}}', f':   See [{e["see"]}](#idx-{pandoc_id(e["see"])}).', '']
        continue
    lines += [f'[{e["term"]}]{{#idx-{slug}}}', f':   {refs(e)}', '']
    for sub in sorted(by_parent.get(e['term'].lower(), []), key=lambda s: s['term'].lower()):
        lines += [f'[{e["term"]} — {sub["term"]}]{{#idx-{slug}-{pandoc_id(sub["term"])}}}', f':   {refs(sub)}', '']
lines += [':::']
page = '\n'.join(lines).rstrip() + '\n'

if check:
    current = open(out_path, encoding='utf-8').read() if os.path.exists(out_path) else ''
    if current != page:
        print(f'{out_path} is out of date: run scripts/gen-book-index.sh', file=sys.stderr)
        sys.exit(1)
    print(f'OK: {out_path} is in sync with index-terms.txt')
else:
    open(out_path, 'w', encoding='utf-8').write(page)
    used = sum(1 for e in entries if not e['see'] and counts[id(e)])
    print(f'wrote {out_path}: {len(entries)} entries, {used} with sections')

#!/usr/bin/env python3
"""Count machine-sounding prose patterns in the book, per page.

  python3 scripts/book_voice.py                 one row of counts per page
  python3 scripts/book_voice.py <page.md> ...   every hit with its line

Reads prose only: fenced code, inline code, link targets, headings, table
rows and callout fences are skipped. The patterns follow Wikipedia's "Signs of
AI writing" catalog, less what docs/book/STYLE.md decides otherwise (spaced em
dashes and Title Case headings are the book's house style). A count is a
prompt to reread the sentence, not proof of a fault.
"""
import glob, os, re, sys

BOOK = os.path.join(os.path.dirname(os.path.abspath(__file__)), '..', 'docs', 'book')
SKIP = {'STYLE.md', 'README.md', 'CONCEPTS.md', 'appendix-index.md'}

W = lambda *ws: re.compile(r'\b(?:' + '|'.join(ws) + r')\b', re.I)
PATTERNS = {
    # Inflated significance, promotional tone and the vocabulary that clusters with it
    'vocab': W('crucial', 'pivotal', 'robust', 'seamless(?:ly)?', 'leverag(?:e|es|ing)', 'utiliz(?:e|es|ed|ing)', 'delve',
               'showcas(?:e|es|ing)', 'underscor(?:e|es|ing)', 'testament', 'tapestry', 'landscape', 'intricate',
               'meticulous(?:ly)?', 'vibrant', 'foster(?:s|ing)?', 'enhanc(?:e|es|ing)', 'empower(?:s|ing)?',
               'comprehensive', 'powerful', 'invaluable', 'valuable', 'key (?:role|insight|takeaway)s?',
               'serves as', 'stands as', 'plays a (?:key|vital|critical|crucial) role', 'in today\'s'),
    # "not just X but Y", "isn't X, it's Y", "rather than X": an insight that isn't one
    'contrast': re.compile(r"\bnot (?:just|only|merely)\b|\bisn't (?:just|about)\b|\bit's not\b.{0,60}\bit's\b|\brather than\b|\bnot because\b", re.I),
    # trailing -ing clause that only asserts significance
    'ing-tail': re.compile(r',\s+(?:ensuring|highlighting|reflecting|underscoring|showcasing|fostering|enabling|allowing|making it|providing|giving you)\b', re.I),
    # filler, didactic disclaimers and summary endings
    'filler': W('it\'s (?:important|worth) (?:to note|noting)', 'note that', 'keep in mind', 'simply', 'just',
                'easily', 'obviously', 'clearly', 'of course', 'in summary', 'in conclusion', 'overall',
                'essentially', 'basically', 'actually', 'really', 'very'),
    # hedges and vague attribution
    'vague': W('various', 'several', 'a (?:wide )?(?:range|variety|number) of', 'many (?:teams|users|organizations)',
               'experts', 'industry', 'best practices?', 'typically', 'generally', 'often'),
}

def prose(path):
    fence = False
    for i, line in enumerate(open(path, encoding='utf-8'), 1):
        s = line.strip()
        if s.startswith('```'):
            fence = not fence
            continue
        if fence or not s or s.startswith(('#', '|', ':::', '%%', '<', '![')):
            continue
        s = re.sub(r'`[^`]*`', 'code', s)
        s = re.sub(r'\]\([^)]*\)', ']', s)
        yield i, s

def triples(s):
    """Lists of exactly three short items, the tell, as a regex on 'a, b and c'."""
    return re.findall(r'\b\w+(?: \w+)?, \w+(?: \w+)?,? and \w+(?: \w+)?\b', s)

def scan(path):
    hits, words, sentences, long_ = [], 0, 0, 0
    for i, s in prose(path):
        words += len(s.split())
        for sent in re.split(r'(?<=[.!?])\s+', s):
            n = len(sent.split())
            if n >= 3:
                sentences += 1
                long_ += n > 30
        for name, rx in PATTERNS.items():
            for m in rx.finditer(s):
                hits.append((name, i, m.group(0)))
        for t in triples(s):
            hits.append(('triplet', i, t))
    return hits, words, sentences, long_

def main(argv):
    if argv:
        for path in argv:
            hits, *_ = scan(path)
            for name, i, text in hits:
                print(f'{path}:{i}: {name}: {text}')
        return
    cols = list(PATTERNS) + ['triplet']
    print(f"{'page':34s} {'words':>6s} " + ' '.join(f'{c:>8s}' for c in cols) + f" {'per1k':>6s} {'>30w%':>6s}")
    total = {c: 0 for c in cols}
    tw = 0
    for path in sorted(glob.glob(os.path.join(BOOK, '*.md'))) + [os.path.join(BOOK, 'index.qmd')]:
        name = os.path.basename(path)
        if name in SKIP:
            continue
        hits, words, sentences, long_ = scan(path)
        c = {k: 0 for k in cols}
        for h in hits:
            c[h[0]] += 1
        for k in cols:
            total[k] += c[k]
        tw += words
        per1k = sum(c.values()) * 1000 / max(words, 1)
        print(f'{name:34s} {words:6d} ' + ' '.join(f'{c[k]:8d}' for k in cols)
              + f' {per1k:6.1f} {100 * long_ / max(sentences, 1):6.1f}')
    print(f"{'total':34s} {tw:6d} " + ' '.join(f'{total[k]:8d}' for k in cols) + f' {sum(total.values()) * 1000 / tw:6.1f}')

if __name__ == '__main__':
    main(sys.argv[1:])

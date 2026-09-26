#!/usr/bin/env python3
"""Check that every #fragment link in the rendered book lands on an id.

Usage: scripts/check-book-fragments.py [docs/book/_book]

Run it after `quarto render docs/book --to html`. It reads every page under
the output directory, collects each href that points at a page of the book
with a #fragment (a same-page "#id" included), and checks that the target
page has an element with that id. It exits 1 and lists the misses if any
link lands nowhere.

Why after the render: the link check on the Markdown sources (lychee in
docs.yml) computes heading anchors the way GitHub does, and Quarto computes
them differently. GitHub keeps a leading number ("16-troubleshooting") and
turns " — " into "--"; Quarto drops the number ("troubleshooting") and writes
a single "-". A fragment can pass lychee and still miss on the site, and only
the rendered HTML says which. Give such a heading an explicit {#id} equal to
its GitHub slug, and both checks agree.

Links that leave the book (http:, https:, mailto:, the GitHub copies the
render points ../ links at) are not checked here.
"""

import os
import sys
from html.parser import HTMLParser
from urllib.parse import unquote, urlsplit


class Page(HTMLParser):
    """Collects the ids a page defines and the hrefs it links to."""

    def __init__(self):
        super().__init__(convert_charrefs=True)
        self.ids = set()
        self.hrefs = []

    def handle_starttag(self, tag, attrs):
        a = dict(attrs)
        if a.get("id"):
            self.ids.add(a["id"])
        # <a name="..."> is an anchor too.
        if tag == "a" and a.get("name"):
            self.ids.add(a["name"])
        if tag == "a" and a.get("href"):
            self.hrefs.append((a["href"], self.getpos()[0]))

    handle_startendtag = handle_starttag


def main(argv):
    root = argv[1] if len(argv) > 1 else "docs/book/_book"
    if not os.path.isdir(root):
        print(f"fragments: {root} not found: render the book first", file=sys.stderr)
        return 2

    pages = {}
    for dirpath, _, files in os.walk(root):
        for name in files:
            if name.endswith(".html"):
                path = os.path.join(dirpath, name)
                parser = Page()
                with open(path, encoding="utf-8") as fh:
                    parser.feed(fh.read())
                pages[os.path.relpath(path, root)] = parser

    checked, misses = 0, []
    for page, parsed in sorted(pages.items()):
        for href, line in parsed.hrefs:
            parts = urlsplit(href)
            if parts.scheme or parts.netloc or not parts.fragment:
                continue
            if parts.path:
                target = os.path.normpath(os.path.join(os.path.dirname(page), unquote(parts.path)))
            else:
                target = page
            if target not in pages and not target.endswith(".html"):
                # A download or an asset: no ids to check.
                continue
            checked += 1
            fragment = unquote(parts.fragment)
            if target not in pages:
                misses.append((page, line, href, target, None))
            elif fragment not in pages[target].ids and parts.fragment not in pages[target].ids:
                misses.append((page, line, href, target, fragment))

    if misses:
        for page, line, href, target, fragment in misses:
            if fragment is None:
                print(f"{page}:{line}: {href} -> no page {target}", file=sys.stderr)
            else:
                print(f"{page}:{line}: {href} -> no id \"{fragment}\" in {target}", file=sys.stderr)
        print(
            f"\n{len(misses)} of {checked} fragment links miss their target. The source "
            "of <page>.html is docs/book/<page>.md (index.html: index.qmd). Point the link "
            "at the id the rendered page has, or give the heading an explicit {#id} equal "
            "to its GitHub slug so lychee and Quarto agree.",
            file=sys.stderr,
        )
        return 1
    print(f"OK: all {checked} fragment links in {len(pages)} pages land on an id")
    return 0


if __name__ == "__main__":
    sys.exit(main(sys.argv))

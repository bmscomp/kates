#!/usr/bin/env python3
"""Tests for scripts/book_metrics.py.

Run: python3 -m unittest discover -s scripts -p 'test_book_metrics.py'
Also run by scripts/check-book-style.sh, before the ratchet it tests.

Each test feeds a few lines of Markdown, or a throwaway book directory with
its own _quarto.yml, so a failure names the rule that broke rather than a
chapter that changed.
"""

import io
import json
import sys
import tempfile
import textwrap
import unittest
from contextlib import redirect_stderr, redirect_stdout
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent))
import book_metrics as bm  # noqa: E402


def md(text: str) -> str:
    return textwrap.dedent(text).lstrip("\n")


def counts(text: str, name: str = "page.md", handoff_to=None) -> dict:
    return bm.measure(name, md(text), handoff_to=handoff_to)["counts"]


def lines_of(text: str, key: str, name: str = "page.md", handoff_to=None) -> list[int]:
    res = bm.measure(name, md(text), handoff_to=handoff_to)
    return [x["line"] for x in res["details"].get(key, [])]


QUARTO = md(
    """
    project:
      type: book

    book:
      title: "A Book"
      chapters:
        - index.qmd
        - part: "Part I — First"
          chapters:
            - 01-one.md
            - 02-two.md
        - part: part-second.md
          chapters:
            - 03-three.md

      appendices:
        - appendix-a-glossary.md
        - appendix-b-more.md

    format:
      html:
        toc: true
    """
)


class ParsingTest(unittest.TestCase):
    def test_front_matter_is_not_a_rule(self):
        self.assertEqual(counts("---\ntitle: x\n---\n\n# T\n\nText.\n")["hr_rules"], 0)

    def test_rules_outside_code_are_counted(self):
        text = """
        # T

        Text.

        ---

        ```yaml
        ---
        ```
        """
        self.assertEqual(lines_of(text, "hr_rules"), [5])

    def test_a_rule_between_prose_and_table_changes_nothing_else(self):
        with_rule = "# T\n\nThe table:\n\n---\n\n| a |\n|---|\n| 1 |\n"
        without = "# T\n\nThe table:\n\n| a |\n|---|\n| 1 |\n"
        a, b = counts(with_rule), counts(without)
        a.pop("hr_rules")
        b.pop("hr_rules")
        self.assertEqual(a, b)

    def test_code_is_not_prose(self):
        text = """
        # T

        ```bash
        ## not a heading
        | not a table |
        ```

        ````markdown
        ```bash
        # still inside the outer fence
        ```
        ````

        - item

          ```bash
          ## indented fence body
          ```
        """
        page = bm.Page(md(text))
        self.assertEqual([b.kind for b in page.blocks], ["h", "code", "code", "li", "code"])

    def test_html_comments_are_skipped(self):
        page = bm.Page(md("# T\n\n<!--\n## hidden\n-->\n\nText.\n"))
        self.assertEqual([b.kind for b in page.blocks], ["h", "p"])


class HeadingTest(unittest.TestCase):
    def test_bare_headings(self):
        text = """
        # Title

        ## Followed by a Heading

        ### Followed by a Table

        | a |
        |---|

        ### Followed by Code

        ```bash
        kates version
        ```

        ### Followed by Prose

        Text.

        ### Followed by a List

        - item
        """
        # The H1 counts too: a chapter opens with prose, not with its first H2.
        self.assertEqual(lines_of(text, "bare_headings"), [1, 3, 5, 10])

    def test_reference_entries_are_exempt_only_in_reference_chapters(self):
        text = """
        ## Commands

        #### test list

        ```bash
        kates test list
        ```

        #### GetTest

        ```bash
        grpcurl localhost:9000 kates.TestService/GetTest
        ```

        ### `POST /api/tests`

        ```bash
        curl -X POST localhost:8080/api/tests
        ```

        ## Installation

        ```bash
        brew install kates
        ```

        #### Flow

        ```mermaid
        flowchart LR
          A --> B
        ```
        """
        self.assertEqual(lines_of(text, "bare_headings", name="10-cli-reference.md"), [1, 21, 27])
        self.assertEqual(len(lines_of(text, "bare_headings", name="05-test-types.md")), 6)

    def test_glossary_letters_are_exempt_only_over_their_first_term(self):
        text = """
        # Glossary

        Terms.

        ## A

        ### `acks` {#gl-acks}

        Definition.

        ## B {#b}

        ### Broker {#gl-broker}

        Definition.

        ## Cc

        ### Controller {#gl-controller}

        Definition.

        ## D

        | Term | Meaning |
        |------|---------|

        ## E

        #### Too Deep

        Definition.
        """
        # A letter H2 over its first term's H3 is exempt, in the Glossary only;
        # a longer H2, a table under a letter, or a deeper heading still count.
        self.assertEqual(
            lines_of(text, "bare_headings", name="appendix-a-glossary.md"), [17, 23, 28]
        )
        self.assertEqual(
            lines_of(text, "bare_headings", name="05-test-types.md"), [5, 11, 17, 23, 28]
        )

    def test_numbered_headings(self):
        text = """
        ## 3. Deploy the Chart

        Text.

        ### 4.1 Check It

        Text.

        ### Step 1 — Deploy

        Text.

        ### Three Brokers

        Text.
        """
        self.assertEqual(lines_of(text, "numbered_headings"), [1, 5])


class BlockTest(unittest.TestCase):
    def test_tables_without_lead_in(self):
        text = """
        ## Heading

        | a | b |
        |---|---|
        | 1 | 2 |

        The next table:

        | a |
        |---|

        - a list item

        | a |
        |---|
        """
        self.assertEqual(lines_of(text, "tables_without_lead_in"), [3, 14])

    def test_bold_lead_bullets(self):
        text = """
        - **Label**: text
        - **Label** — text
        1. **Label.** text
        - **Label:** text
        - **bold** words in a sentence
        - plain item
        """
        self.assertEqual(lines_of(text, "bold_lead_bullets"), [1, 2, 3, 4])

    def test_list_heavy_sections(self):
        heavy = "## Heavy\n\n" + "".join(f"- item {i}\n" for i in range(6))
        light = "## Light\n\nOne.\n\nTwo.\n\nThree.\n\n" + "".join(f"- item {i}\n" for i in range(6))
        self.assertEqual(counts(heavy)["list_heavy_sections"], 1)
        self.assertEqual(counts(light)["list_heavy_sections"], 0)

    def test_mermaid_captions(self):
        text = """
        ```mermaid
        %%| fig-cap: "The point."
        %%| fig-alt: "What it shows."
        flowchart LR
          A --> B
        ```

        ```mermaid
        %%| fig-cap: "Caption only."
        flowchart LR
          A --> B
        ```

        ```{mermaid}
        flowchart LR
          A --> B
        ```
        """
        c = counts(text)
        self.assertEqual((c["mermaid_blocks"], c["mermaid_uncaptioned"]), (3, 2))

    def test_long_fence_lines(self):
        text = (
            "```bash\n"
            + "x" * 90 + "\n"
            + "y" * 91 + "\n"
            + "```\n\n"
            + "- item\n\n"
            + "  ```bash\n"
            + "  " + "z" * 90 + "\n"
            + "  ```\n\n"
            + "```mermaid\n"
            + "A[" + "w" * 120 + "]\n"
            + "```\n"
        )
        self.assertEqual(lines_of(text, "long_fence_lines"), [3])

    def test_scope_blockquotes_and_callouts(self):
        text = """
        > **Scope**: what this owns.

        ::: {.callout-note}
        A note.
        :::

        > A plain quotation.
        """
        c = counts(text)
        self.assertEqual((c["scope_blockquotes"], c["callouts"]), (1, 1))

    def test_todo_markers_anywhere(self):
        text = "Text [TODO: capture].\n\n```bash\n# [TODO later]\n```\n"
        self.assertEqual(counts(text)["todo_markers"], 2)

    def test_glossary_links(self):
        text = (
            "The [ISR](appendix-a-glossary.md#gl-isr) and "
            "[glossary](./appendix-a-glossary.md), not [this](../other/appendix-a-glossary.md).\n"
        )
        self.assertEqual(counts(text)["glossary_links"], 2)


class ProseTest(unittest.TestCase):
    def test_formula_openings_before_the_first_h2(self):
        text = """
        # Title

        This chapter covers everything.

        > **Scope**: this chapter is for operators.

        ## Later

        This chapter provides nothing new here.
        """
        self.assertEqual(lines_of(text, "formula_openings"), [3, 5])

    def test_changelog_phrases(self):
        text = """
        The chart no longer renders it, and the flag used to gate it.

        The key is used to verify the record, and `previously` is a field.

        ```bash
        # no longer
        ```

        ### The Old Boards Are Gone
        """
        self.assertEqual(lines_of(text, "changelog_phrases"), [1, 1, 9])

    def test_sentence_lengths(self):
        long40 = "Word " + " ".join(["word"] * 39) + "."
        long71 = "Word " + " ".join(["word"] * 70) + "."
        short = "A short sentence of seven words here."
        text = f"# T\n\nOutput:\n\n{short} {long40} {short}\n\n{long71} e.g. Another short one of words.\n"
        c = counts(text)
        self.assertEqual(c["explanatory_sentences"], 4)
        self.assertEqual(c["long_sentences"], 2)
        self.assertEqual(c["sentences_over_70"], 1)
        self.assertEqual(c["long_sentence_share"], 50.0)


class ReadingOrderTest(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.dir = Path(self.tmp.name)
        (self.dir / "_quarto.yml").write_text(QUARTO, encoding="utf-8")

    def tearDown(self):
        self.tmp.cleanup()

    def test_reading_order(self):
        chapters, appendices, parts = bm.reading_order(self.dir / "_quarto.yml")
        self.assertEqual(chapters, ["index.qmd", "01-one.md", "02-two.md", "03-three.md"])
        self.assertEqual(appendices, ["appendix-a-glossary.md", "appendix-b-more.md"])
        self.assertEqual(parts, {"part-second.md"})

    def test_next_chapters_skip_part_pages_and_reach_the_appendices(self):
        nxt = bm.next_chapters(self.dir / "_quarto.yml")
        self.assertEqual(nxt["index"], "01-one")
        self.assertEqual(nxt["02-two"], "03-three")
        self.assertEqual(nxt["03-three"], "appendix-a-glossary")
        self.assertIsNone(nxt["appendix-a-glossary"])
        self.assertIsNone(nxt["appendix-b-more"])
        self.assertNotIn("part-second", nxt)

    def test_handoff(self):
        good = "# One\n\nText.\n\nNext, [Two](02-two.md#start) builds on this.\n"
        other = "# One\n\nNext, [Two](02-two.md) and then [Three](03-three.md).\n"
        none = "# One\n\nNothing follows.\n"
        in_callout = "# One\n\n::: {.callout-tip}\nGo on to [Two](02-two.md).\n:::\n"
        on_code = "# One\n\nSee [Two](02-two.md).\n\n```bash\nkates version\n```\n"
        self.assertEqual(counts(good, handoff_to="02-two")["broken_handoff"], 0)
        self.assertEqual(counts(other, handoff_to="02-two")["broken_handoff"], 1)
        self.assertEqual(counts(none, handoff_to="02-two")["broken_handoff"], 1)
        self.assertEqual(counts(in_callout, handoff_to="02-two")["broken_handoff"], 0)
        self.assertEqual(counts(on_code, handoff_to="02-two")["broken_handoff"], 1)
        self.assertEqual(counts(none, handoff_to=None)["broken_handoff"], 0)


class RatchetTest(unittest.TestCase):
    def result(self, **counts):
        base = {k: 0 for k in bm.COUNTS}
        base.update(counts)
        return {"counts": base, "details": {}}

    def test_growth_fails_and_a_fall_passes(self):
        baseline = {"files": {"a.md": {"bare_headings": 2, "hr_rules": 3}}}
        failures, better = bm.check({"a.md": self.result(bare_headings=3, hr_rules=1)}, baseline)
        self.assertEqual(len(failures), 1)
        self.assertIn("bare headings 3 (baseline 2)", failures[0])
        self.assertEqual(better, ["a.md: --- rules 3 -> 1"])

    def test_a_new_page_must_be_zero(self):
        failures, _ = bm.check({"part-one.md": self.result()}, {"files": {}})
        self.assertEqual(failures, [])
        failures, _ = bm.check({"part-one.md": self.result(bold_lead_bullets=1)}, {"files": {}})
        self.assertIn("new page", failures[0])

    def test_todo_fails_whatever_the_baseline(self):
        baseline = {"files": {"a.md": {"todo_markers": 5}}}
        failures, _ = bm.check({"a.md": self.result(todo_markers=1)}, baseline)
        self.assertEqual(len(failures), 1)

    def test_a_measure_missing_from_the_baseline_counts_as_zero(self):
        failures, _ = bm.check({"a.md": self.result(broken_handoff=1)}, {"files": {"a.md": {}}})
        self.assertEqual(len(failures), 1)


class CliTest(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.dir = Path(self.tmp.name)
        (self.dir / "_quarto.yml").write_text(QUARTO, encoding="utf-8")
        pages = {
            "index.qmd": "---\ntitle-block-banner: false\n---\n\n# Preface\n\nStart with [One](01-one.md).\n",
            "01-one.md": "# One\n\nIntro.\n\n## Bare\n\n| a |\n|---|\n\nOn to [Two](02-two.md).\n",
            "02-two.md": "# Two\n\nIntro.\n\nOn to [Three](03-three.md).\n",
            "part-second.md": "# Part II\n\nWhat this part is for.\n",
            "03-three.md": "# Three\n\nNext, the [Glossary](appendix-a-glossary.md).\n",
            "appendix-a-glossary.md": "# Glossary\n\nTerms.\n",
            "appendix-b-more.md": "# More\n\nMore.\n",
            "STYLE.md": "# Style\n\nA [TODO: marker] here is fine.\n",
        }
        for name, text in pages.items():
            (self.dir / name).write_text(text, encoding="utf-8")
        self.baseline = self.dir / "baseline.json"

    def tearDown(self):
        self.tmp.cleanup()

    def run_main(self, *args):
        out, err = io.StringIO(), io.StringIO()
        with redirect_stdout(out), redirect_stderr(err):
            code = bm.main(["--book-dir", str(self.dir), *args])
        return code, out.getvalue(), err.getvalue()

    def test_update_then_check(self):
        code, out, _ = self.run_main("--update", str(self.baseline))
        self.assertEqual(code, 0)
        data = json.loads(self.baseline.read_text(encoding="utf-8"))
        self.assertNotIn("STYLE.md", data["files"])
        self.assertEqual(data["files"]["01-one.md"]["bare_headings"], 1)
        self.assertEqual(sorted(data["files"]["01-one.md"]), sorted(bm.RATCHETED))
        code, out, _ = self.run_main("--check", str(self.baseline))
        self.assertEqual(code, 0, out)

    def test_check_fails_on_growth_and_says_how_to_update(self):
        self.run_main("--update", str(self.baseline))
        page = self.dir / "02-two.md"
        page.write_text("# Two\n\nIntro.\n\n## Bare Too\n\n```bash\nkates version\n```\n\nOn to [Three](03-three.md).\n")
        code, _, err = self.run_main("--check", str(self.baseline))
        self.assertEqual(code, 1)
        self.assertIn("02-two.md: bare headings 1 (baseline 0)", err)
        self.assertIn("L5: ## Bare Too", err)
        self.assertIn(bm.UPDATE_HINT, err)

    def test_check_passes_when_a_count_falls(self):
        self.run_main("--update", str(self.baseline))
        (self.dir / "01-one.md").write_text(
            "# One\n\nIntro.\n\n## Better\n\nA lead-in:\n\n| a |\n|---|\n\nOn to [Two](02-two.md).\n"
        )
        code, out, _ = self.run_main("--check", str(self.baseline))
        self.assertEqual(code, 0)
        self.assertIn("01-one.md: bare headings 1 -> 0", out)

    def test_json_and_summary(self):
        code, out, _ = self.run_main("--json")
        self.assertEqual(code, 0)
        data = json.loads(out)
        self.assertEqual(set(data["files"]), {
            "index.qmd", "01-one.md", "02-two.md", "part-second.md", "03-three.md",
            "appendix-a-glossary.md", "appendix-b-more.md",
        })
        self.assertEqual(data["totals"]["broken_handoff"], 0)
        code, out, _ = self.run_main("--summary")
        self.assertEqual(code, 0)
        self.assertIn("total", out)


if __name__ == "__main__":
    unittest.main()

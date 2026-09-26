"""Cross-checks of the task list against the product it evaluates.

A task that names an MCP tool kates mcp does not serve, or a setup step or
forbid prefix with a kates command or flag the CLI does not have, fails only
on the lab, after setup has spent half an hour. These tests catch that
offline:

- the MCP tools taskfile knows are exactly the tools kates mcp serves, as
  recorded in the golden files of cli/cmd/testdata/mcp/tools/;
- with KATES_BIN pointing at a built kates (cd cli && go build -o /tmp/kates
  .), every kates command and flag in a setup step, every kates prefix a task
  forbids and every command in the harness's own table of non-read commands
  (kates_commands.RULES) resolves to that command in the binary's --help.
  Without KATES_BIN these are skipped.
"""

from __future__ import annotations

import os
import re
import subprocess
import tempfile
import unittest
from functools import lru_cache
from pathlib import Path

import kates_commands
import taskfile

HERE = Path(__file__).resolve().parent
REPO = HERE.parent.parent
GOLDEN_TOOLS = REPO / "cli" / "cmd" / "testdata" / "mcp" / "tools"
KATES_BIN = os.environ.get("KATES_BIN", "")


class MCPToolsTest(unittest.TestCase):
    def test_taskfile_knows_exactly_the_tools_kates_mcp_serves(self) -> None:
        served = sorted(p.stem for p in GOLDEN_TOOLS.glob("*.json"))
        self.assertTrue(served, f"no golden tool files in {GOLDEN_TOOLS}")
        self.assertEqual(sorted(taskfile.MCP_TOOLS), served)


@lru_cache(maxsize=None)
def kates_help(words: tuple[str, ...]) -> tuple[tuple[str, ...], bool, str]:
    """(the command path --help resolved to, whether it has subcommands, the
    help text). HOME is a scratch directory, so no ~/.kates.yaml is read."""
    with tempfile.TemporaryDirectory() as home:
        env = {"HOME": home, "PATH": os.environ.get("PATH", "")}
        out = subprocess.run([KATES_BIN, *words, "--help"], capture_output=True, text=True, env=env,
                             timeout=30, stdin=subprocess.DEVNULL)
    text = out.stdout + out.stderr
    usage = re.search(r"^Usage:\n\s+kates((?: [a-z][a-z0-9-]*)*)", text, re.M)
    if usage is None:
        return (), False, text
    return tuple(usage.group(1).split()), "Available Commands:" in text, text


def leading_words(args: list[str]) -> list[str]:
    """The words of a kates call before its first flag."""
    out = []
    for a in args:
        if a.startswith("-"):
            break
        out.append(a)
    return out


@unittest.skipUnless(KATES_BIN, "set KATES_BIN to a built kates to check commands and flags")
class KatesCommandsTest(unittest.TestCase):
    def assertResolves(self, words: list[str], where: str, exact: bool = False) -> str:
        path, has_subcommands, text = kates_help(tuple(words))
        self.assertTrue(path, f"{where}: kates {' '.join(words)} --help printed no usage")
        rest = words[len(path):]
        if exact:
            # Usage names the canonical command, so an alias resolves to it.
            self.assertEqual(tuple(kates_commands.command_words(list(path))),
                             tuple(kates_commands.command_words(list(words))),
                             f"{where}: kates {' '.join(words)} is not a command")
        elif rest:
            self.assertFalse(has_subcommands,
                             f"{where}: {rest[0]!r} is not a subcommand of kates {' '.join(path)}")
        return text

    def test_setup_steps(self) -> None:
        doc = taskfile.load(str(HERE / "tasks.json"))
        for task in doc["tasks"]:
            for n, step in enumerate(task["setup"]):
                if step["kind"] != "kates":
                    continue
                where = f"{task['id']} setup[{n}]"
                words = leading_words(step["args"])
                text = self.assertResolves(words, where)
                for flag in (a.split("=", 1)[0] for a in step["args"] if a.startswith("-")):
                    with self.subTest(where=where, flag=flag):
                        found = re.search(rf"(^|[\s,]){re.escape(flag)}[\s=,]", text)
                        self.assertTrue(found, f"{where}: kates {' '.join(words)} has no flag {flag}")

    def test_forbidden_kates_prefixes_are_commands(self) -> None:
        doc = taskfile.load(str(HERE / "tasks.json"))
        for task in doc["tasks"]:
            for prefix in (task.get("forbid") or {}).get("cli", []):
                words = prefix.split()
                if words[0] == "kates":
                    self.assertResolves(words[1:], f"{task['id']} forbid.cli {prefix!r}", exact=True)

    def test_the_harness_table_of_non_read_commands(self) -> None:
        for rule in kates_commands.RULES:
            with self.subTest(path=" ".join(rule.path)):
                text = self.assertResolves(list(rule.path), f"kates_commands {' '.join(rule.path)}", exact=True)
                if rule.dry_run_ok:
                    self.assertIn("--dry-run", text)
                if rule.needs_flag:
                    self.assertIn(rule.needs_flag, text)

    def test_aliases(self) -> None:
        for parent, aliases in kates_commands.ALIASES.items():
            for alias, canonical in aliases.items():
                with self.subTest(alias=" ".join((*parent, alias))):
                    path, _, _ = kates_help((*parent, alias))
                    self.assertEqual(path, (*parent, canonical))


if __name__ == "__main__":
    unittest.main()

"""Tests for metrics.py (transcripts) and kates_commands.py (what a kates
command line does)."""

import contextlib
import io
import json
import unittest

import kates_commands as kc
import metrics
from evalfixtures import TRANSCRIPTS


def load(name: str) -> metrics.Transcript:
    return metrics.load(TRANSCRIPTS / name)


class TranscriptTest(unittest.TestCase):
    def test_mcp_success(self):
        t = load("mcp-sec-posture.jsonl")
        s = metrics.summarize(t)
        self.assertEqual(s["status"], "ok")
        self.assertEqual(s["tool_calls"], 2)
        self.assertEqual(s["tool_call_names"], {"mcp__kates__cluster_overview": 1, "mcp__kates__security_evidence": 1})
        self.assertEqual(s["tokens"], {"input": 120, "output": 1500, "cache_creation": 12000, "cache_read": 60000,
                                       "total": 73620, "source": "result"})
        self.assertEqual(s["cost_usd"], 0.0412)
        self.assertEqual(s["num_turns"], 3)
        self.assertTrue(s["answer_found"])
        self.assertEqual(s["init"]["permissionMode"], "dontAsk")
        answer, error = metrics.extract_answer(metrics.final_text(t))
        self.assertIsNone(error)
        self.assertEqual(answer, {"grade": "B", "failing_checks": 2, "drifted_checks": ["auto-create-topics"]})
        self.assertEqual(len(metrics.tool_texts(t)), 2)

    def test_cli_denied_call_is_not_mutating(self):
        t = load("cli-sec-posture.jsonl")
        s = metrics.summarize(t)
        self.assertEqual(s["bad_lines"], 1)
        self.assertEqual(s["tool_calls"], 3)
        self.assertEqual(s["tool_errors"], 1)
        self.assertEqual(len(s["permission_denials"]), 1)
        self.assertEqual(s["permission_denials"][0]["command"], "kates security baseline --save --context lab -o json")
        self.assertEqual(s["mutating_calls"], [])
        self.assertEqual([c["words"][:2] for c in s["kates_calls"]],
                         [["security", "audit"], ["security", "drift"], ["security", "baseline"]])
        self.assertTrue(s["kates_calls"][2]["denied"])

    def test_max_turns_and_a_mutating_call_behind_flags(self):
        t = load("cli-run-noise.jsonl")
        s = metrics.summarize(t)
        self.assertEqual(s["status"], "max_turns")
        self.assertFalse(s["answer_found"])
        self.assertEqual(s["mutating_calls"], ["kates --context lab test delete a1b2c3d4"])
        self.assertEqual(metrics.forbidden_uses(t, [], [("test", "delete")]), ["kates test delete"])

    def test_forbidden_mcp_tool(self):
        t = load("mcp-run-noise.jsonl")
        self.assertEqual(metrics.forbidden_uses(t, ["disruption_report"], []), ["mcp__kates__disruption_report"])
        self.assertEqual(metrics.forbidden_uses(t, ["kates_activity"], []), [])

    def test_denied_forbidden_call_leaks_nothing(self):
        t = load("cli-sec-posture.jsonl")
        self.assertEqual(metrics.forbidden_uses(t, [], [("security", "baseline")]), [])

    def test_missing_transcript(self):
        t = metrics.load(TRANSCRIPTS / "does-not-exist.jsonl")
        self.assertEqual(metrics.summarize(t)["status"], "no_result")

    def test_statuses(self):
        def with_result(result, assistant=True):
            events = []
            if assistant:
                events.append({"type": "assistant", "message": {"id": "m1", "content": [{"type": "text", "text": "hi"}]}})
            events.append({"type": "result", **result})
            return metrics.parse_events(events)
        started = metrics.parse_events([{"type": "system", "subtype": "init"}])
        self.assertEqual(metrics.status_of(started, timed_out=True), "timeout")
        # A session that never started timed out on the harness, not the agent.
        self.assertEqual(metrics.status_of(with_result({"subtype": "success"}), timed_out=True), "no_result")
        self.assertEqual(metrics.status_of(with_result({"subtype": "error_max_budget_usd", "is_error": True})), "budget")
        self.assertEqual(metrics.status_of(with_result({"subtype": "error_during_execution", "is_error": True})), "error")
        self.assertEqual(metrics.status_of(with_result({"subtype": "error_during_execution", "is_error": True},
                                                       assistant=False)), "infra_error")
        self.assertEqual(metrics.status_of(with_result({"subtype": "success", "is_error": True})), "error")

    def test_api_failures_are_not_the_agents(self):
        for text in ("API Error: 529 {\"type\":\"overloaded_error\"}", "Invalid API key · Please run /login",
                     "You've hit your usage limit · resets 5pm"):
            with self.subTest(text=text):
                t = metrics.parse_events([
                    {"type": "system", "subtype": "init"},
                    {"type": "assistant", "message": {"id": "m1", "content": [{"type": "text", "text": "Looking."}]}},
                    {"type": "assistant", "message": {"id": "m2", "model": "<synthetic>",
                                                      "content": [{"type": "text", "text": text}]}},
                    {"type": "result", "subtype": "success", "is_error": True, "result": text},
                ])
                self.assertEqual(metrics.status_of(t), "infra_error")
                self.assertEqual(t.api_errors, [text])
        # The agent's own context overflowing is the agent's.
        t = metrics.parse_events([
            {"type": "assistant", "message": {"id": "m1", "content": [{"type": "text", "text": "Looking."}]}},
            {"type": "assistant", "message": {"id": "m2", "model": "<synthetic>",
                                              "content": [{"type": "text", "text": "Prompt is too long"}]}},
            {"type": "result", "subtype": "success", "is_error": True},
        ])
        self.assertEqual(metrics.status_of(t), "error")

    def test_tokens_without_result_count_each_message_once(self):
        usage = {"input_tokens": 1, "output_tokens": 2, "cache_creation_input_tokens": 3, "cache_read_input_tokens": 4}
        events = [
            {"type": "assistant", "message": {"id": "m1", "content": [{"type": "text", "text": "a"}], "usage": usage}},
            {"type": "assistant", "message": {"id": "m1", "content": [{"type": "tool_use", "id": "t1", "name": "Bash",
                                                                      "input": {"command": "kates test list"}}],
                                              "usage": usage}},
            {"type": "assistant", "message": {"id": "m2", "content": [{"type": "text", "text": "done"}], "usage": usage}},
        ]
        t = metrics.parse_events(events)
        self.assertEqual(metrics.token_usage(t), {"input": 2, "output": 4, "cache_creation": 6, "cache_read": 8,
                                                  "total": 20, "source": "assistant_messages"})
        self.assertEqual(metrics.final_text(t), "done")
        self.assertEqual(len(t.tool_uses), 1)


class AnswerBlockTest(unittest.TestCase):
    def test_last_json_block_wins(self):
        text = "a\n```json\n{\"x\": 1}\n```\nb\n```json\n{\"x\": 2}\n```\n"
        self.assertEqual(metrics.extract_answer(text), ({"x": 2}, None))

    def test_untagged_block_is_a_fallback(self):
        text = "a\n```\n{\"x\": 3}\n```"
        self.assertEqual(metrics.extract_answer(text), ({"x": 3}, None))

    def test_errors(self):
        self.assertEqual(metrics.extract_answer("no block")[1], "no fenced json block in the final answer")
        self.assertIn("not valid JSON", metrics.extract_answer("```json\n{x}\n```")[1])
        self.assertEqual(metrics.extract_answer("```json\n[1]\n```")[1], "the answer block is not a JSON object")

    def test_main_prints_metrics(self):
        out = io.StringIO()
        with contextlib.redirect_stdout(out):
            code = metrics.main([str(TRANSCRIPTS / "mcp-sec-posture.jsonl")])
        self.assertEqual(code, 0)
        self.assertEqual(json.loads(out.getvalue())["tool_calls"], 2)
        with contextlib.redirect_stderr(io.StringIO()):
            self.assertEqual(metrics.main([str(TRANSCRIPTS / "missing.jsonl")]), 2)


class KatesCommandsTest(unittest.TestCase):
    def test_split_commands(self):
        cmd = "X=1 kates test get a -o json | jq .id && echo ok; kates test list\nkates audit"
        self.assertEqual(kc.split_commands(cmd), [
            ["X=1", "kates", "test", "get", "a", "-o", "json"], ["jq", ".id"], ["echo", "ok"],
            ["kates", "test", "list"], ["kates", "audit"]])

    def test_kates_argvs(self):
        self.assertEqual(kc.kates_argvs("id=$(kates test list -o json | jq -r '.items[0].id')"),
                         [["test", "list", "-o", "json"]])
        self.assertEqual(kc.kates_argvs("/usr/local/bin/kates health"), [["health"]])
        self.assertEqual(kc.kates_argvs("echo a1 | xargs -I{} kates test get {}"), [["test", "get", "{}"]])
        self.assertEqual(kc.kates_argvs("jq . plan.json"), [])
        self.assertEqual(kc.kates_argvs("echo 'kates test delete x'"), [])

    def test_classify(self):
        cases = {
            "kates test create --type LOAD": True,
            "kates test create --type LOAD --dry-run": False,
            "kates --context lab test delete x": True,
            "kates t rm x": True,
            "kates -o json test get x": False,
            "kates security baseline": False,
            "kates sec base --save": True,
            "kates migrate plan --from 3.9": False,
            "kates migrate up": True,
            "kates disruption run --config plan.json --dry-run -o json": False,
            "kates disruption run --config plan.json": True,
            "kates disruption playbook show rolling-restart -o json": False,
            "kates tune run TUNE_BATCHING": True,
            "kates tune report x": False,
        }
        for cmd, mutating in cases.items():
            with self.subTest(cmd=cmd):
                (args,) = kc.kates_argvs(cmd)
                self.assertEqual(kc.classify(args)["mutating"], mutating)

    def test_live_views(self):
        for cmd in ("kates top", "kates dash", "kates cluster watch", "kates disruption watch d1"):
            with self.subTest(cmd=cmd):
                (args,) = kc.kates_argvs(cmd)
                self.assertTrue(kc.classify(args)["live"])

    def test_starts_with_resolves_aliases_and_flags(self):
        (args,) = kc.kates_argvs("kates -o json --context lab disruption status d1")
        self.assertTrue(kc.starts_with(args, ("disruption", "status")))
        (args,) = kc.kates_argvs("kates t show a1")
        self.assertTrue(kc.starts_with(args, ("test", "get")))
        self.assertFalse(kc.starts_with(args, ("test", "list")))

    def test_deny_rules(self):
        deny = kc.default_cli_deny()
        for rule in ("Bash(kates test delete *)", "Bash(kates t rm *)", "Bash(kates sec base *)",
                     "Bash(kates ctx set *)", "Bash(kates ports)", "Bash(kates top)"):
            self.assertIn(rule, deny)
        for rule in ("Bash(kates disruption run *)", "Bash(kates test create *)", "Bash(kates migrate *)"):
            self.assertNotIn(rule, deny)
        self.assertEqual(kc.bash_rules(("disruption", "status")),
                         ["Bash(kates disruption status)", "Bash(kates disruption status *)"])


if __name__ == "__main__":
    unittest.main()

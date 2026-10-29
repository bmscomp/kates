"""Tests for analyst.py without a model: the session's options and the retry."""

import asyncio
import sys
import tempfile
import types
import unittest
from unittest import mock

import analyst
import receiver
from test_verdict import answer

CFG = receiver.Config.from_env({"ANALYST_KATES_URL": "http://kates:8080", "KATES_API_KEY": "kates_k_s",
                                "KATES_CONTEXT": "lab", "ANALYST_CLUSTER_ID": "abc",
                                "ANALYST_STATE_DIR": tempfile.mkdtemp(prefix="run-analyst-test-")})


class OptionsTest(unittest.TestCase):
    def test_only_kates_read_tools(self):
        o = analyst.session_options(CFG)
        self.assertEqual(o["tools"], [])
        self.assertEqual(o["permission_mode"], "dontAsk")
        self.assertEqual(o["setting_sources"], [])
        self.assertTrue(all(t.startswith("mcp__kates__") for t in o["allowed_tools"]))
        self.assertIn("Bash", o["disallowed_tools"])
        self.assertEqual(o["output_format"]["schema"]["required"][0], "runId")

    def test_key_stays_off_the_command_line(self):
        server = analyst.session_options(CFG)["mcp_servers"]["kates"]
        self.assertEqual(server["args"], ["mcp", "--context", "lab", "--allow-cluster", "abc", "--cluster-label", "lab"])
        self.assertEqual(server["env"], {"KATES_API_KEY": "kates_k_s"})
        self.assertEqual(analyst.session_options(CFG)["cwd"], str(CFG.state_dir))


class FakeResult:
    def __init__(self, subtype="success", is_error=False, structured_output=None):
        self.subtype, self.is_error, self.structured_output = subtype, is_error, structured_output
        self.stop_reason, self.errors, self.total_cost_usd, self.num_turns = None, None, 0.0421, 5


def fake_sdk(results, raise_after=None):
    """A stand-in claude_agent_sdk module: query yields the given results, then maybe raises."""
    sdk = types.ModuleType("claude_agent_sdk")
    sdk.ResultMessage = FakeResult
    sdk.ClaudeAgentOptions = lambda **kw: kw

    async def query(prompt, options):
        for r in results:
            yield r
        if raise_after:
            raise raise_after

    sdk.query = query
    return sdk


class CostTest(unittest.TestCase):
    def ask(self, sdk):
        with mock.patch.dict(sys.modules, {"claude_agent_sdk": sdk}):
            return asyncio.run(analyst._ask(CFG, "3fa9c2e1", "go"))

    def test_cost_is_logged_for_an_answer(self):
        with self.assertLogs("run-analyst", "INFO") as logs:
            got = self.ask(fake_sdk([FakeResult(structured_output=answer())]))
        self.assertEqual(got, (answer(), None))
        self.assertEqual(logs.output, ["INFO:run-analyst:session cost for run 3fa9c2e1: 0.0421 USD, 5 turns, success"])

    def test_cost_is_logged_when_the_sdk_raises_after_the_result(self):
        with self.assertLogs("run-analyst", "INFO") as logs, self.assertRaises(RuntimeError):
            self.ask(fake_sdk([FakeResult(subtype="success", is_error=True)], raise_after=RuntimeError("auth")))
        self.assertIn("session cost for run 3fa9c2e1", logs.output[0])

    def test_a_budget_stop_is_an_error(self):
        with self.assertLogs("run-analyst", "INFO"):
            got, error = self.ask(fake_sdk([FakeResult(subtype="error_max_budget_usd", is_error=True)]))
        self.assertIsNone(got)
        self.assertIn("error_max_budget_usd", error)


class RetryTest(unittest.TestCase):
    def asker(self, *answers):
        prompts = []
        replies = list(answers)

        def ask(prompt):
            prompts.append(prompt)
            return replies.pop(0)

        return ask, prompts

    def test_a_good_answer_first_time(self):
        ask, prompts = self.asker((answer(), None))
        self.assertEqual(analyst.analyze(CFG, "3fa9c2e1", ask), (answer(), None))
        self.assertEqual(len(prompts), 1)

    def test_one_retry_with_the_reasons(self):
        ask, prompts = self.asker((answer(verdict="NORMAL"), None), (answer(), None))
        self.assertEqual(analyst.analyze(CFG, "3fa9c2e1", ask), (answer(), None))
        self.assertIn("verdict is NORMAL but the evidence fields give REGRESSED", prompts[1])

    def test_refused_twice(self):
        ask, _ = self.asker((answer(runId="deadbeef"), None), (answer(runId="deadbeef"), None))
        got, error = analyst.analyze(CFG, "3fa9c2e1", ask)
        self.assertIsNone(got)
        self.assertIn("runId is 'deadbeef', but the run analysed is 3fa9c2e1", error)

    def test_session_error(self):
        ask, _ = self.asker((None, "session error_max_budget_usd"))
        self.assertEqual(analyst.analyze(CFG, "3fa9c2e1", ask), (None, "session error_max_budget_usd"))


if __name__ == "__main__":
    unittest.main()

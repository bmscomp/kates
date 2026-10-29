"""Tests for analyst.py without a model: the session's options and the retry."""

import unittest
from pathlib import Path

import analyst
import receiver
from test_verdict import answer

CFG = receiver.Config.from_env({"ANALYST_KATES_URL": "http://kates:8080", "KATES_API_KEY": "kates_k_s",
                                "KATES_CONTEXT": "lab", "ANALYST_CLUSTER_ID": "abc",
                                "ANALYST_STATE_DIR": "/tmp/state"})


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
        self.assertEqual(analyst.session_options(CFG)["cwd"], str(Path("/tmp/state")))


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

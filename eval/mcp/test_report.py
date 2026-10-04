"""Tests for report.py: the kill criteria, human verdicts over automatic
ones, and the report itself."""

import contextlib
import csv
import json
import io
import tempfile
import unittest
from pathlib import Path

import grade
import report
from evalfixtures import EXPECTED, graded_run_dir


def stats(success=0.5, grounded=0.97, misreads=0, tokens=100_000.0, calls=10.0, trials=60, numbers=200):
    return {"trials": trials, "decided": trials, "undecided": 0, "human_graded": 0, "success_rate": success,
            "numbers": numbers, "grounded_rate": grounded, "misread_answers": misreads,
            "misread_rate": misreads / trials, "tool_calls": calls, "tokens": tokens, "cost_usd": 0.1,
            "wall_clock_s": 60.0, "mutating_calls": 0, "forbidden_uses": 0}


def verdict(mcp, cli):
    return report.kill_criteria(mcp, cli)[1]


class KillCriteriaTest(unittest.TestCase):
    def test_continue_on_a_success_margin(self):
        self.assertEqual(verdict(stats(success=0.80), stats(success=0.65)), "CONTINUE")

    def test_exactly_fifteen_points_passes_despite_float_noise(self):
        self.assertEqual(verdict(stats(success=0.70), stats(success=0.55)), "CONTINUE")

    def test_fourteen_points_is_not_enough_alone(self):
        self.assertEqual(verdict(stats(success=0.79), stats(success=0.65)), "STOP")

    def test_continue_on_efficiency_with_equal_success(self):
        mcp = stats(success=0.7, tokens=60_000, calls=6)
        self.assertEqual(verdict(mcp, stats(success=0.7)), "CONTINUE")

    def test_efficiency_needs_both_tokens_and_calls(self):
        mcp = stats(success=0.7, tokens=60_000, calls=8)
        criteria, v = report.kill_criteria(mcp, stats(success=0.7))
        self.assertEqual(v, "STOP")
        self.assertIs(criteria[2]["passed"], False)

    def test_efficiency_does_not_rescue_lower_success(self):
        mcp = stats(success=0.6, tokens=10_000, calls=2)
        criteria, v = report.kill_criteria(mcp, stats(success=0.65))
        self.assertEqual(v, "STOP")
        self.assertIs(criteria[0]["passed"], False)
        self.assertIs(criteria[3]["passed"], True)

    def test_hard_gates(self):
        self.assertEqual(verdict(stats(success=0.9, grounded=0.94), stats(success=0.5)), "STOP")
        self.assertEqual(verdict(stats(success=0.9, misreads=1), stats(success=0.5)), "STOP")
        self.assertEqual(verdict(stats(success=0.9, grounded=0.95), stats(success=0.5)), "CONTINUE")

    def test_no_numbers_means_no_decision(self):
        mcp = stats(success=0.9, grounded=None, numbers=0)
        self.assertEqual(verdict(mcp, stats(success=0.5)), "UNDECIDED")

    def test_era_text(self):
        self.assertIn("2025-11-25 at most", report.era_text({"protocol_negotiation": "legacy", "sdk_generation": "v2"}))
        self.assertIn("2026-07-28", report.era_text({"protocol_negotiation": "auto", "sdk_generation": "v2"}))
        self.assertIn("no effect on v1", report.era_text({"protocol_negotiation": "auto", "sdk_generation": "v1"}))


class ReportTest(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.run_dir = graded_run_dir(Path(self.tmp.name))
        grade.grade_run(self.run_dir)

    def tearDown(self):
        self.tmp.cleanup()

    def main(self) -> tuple[int, str]:
        out = io.StringIO()
        with contextlib.redirect_stdout(out), contextlib.redirect_stderr(io.StringIO()):
            code = report.main(["--run-id", self.run_dir.name, "--runs-dir", str(self.run_dir.parent)])
        return code, out.getvalue()

    def test_fixture_run_stops(self):
        # One trial each: both arms succeed on one task of two, the MCP arm
        # spends as much as the CLI arm, and every MCP claim is grounded.
        code, out = self.main()
        self.assertEqual(code, 3, "a provisional verdict decides nothing yet")
        self.assertIn("verdict: STOP (provisional)", out)
        text = (self.run_dir / "report.md").read_text()
        self.assertIn("**Verdict: STOP** (provisional)", text)
        self.assertIn("MCP_PROTOCOL_NEGOTIATION=legacy", text)
        self.assertIn("Fixed at 2026-09-26".lower(), text.lower())
        self.assertIn("| Hard gate: MCP grounded-claim rate at least 95 % | PASS | 100.0 % of 7 numbers |", text)
        self.assertIn("| Hard gate: MCP silent misreads are 0 | PASS |", text)
        self.assertIn("Fewer than 3 valid trials", text)
        self.assertIn("carried the human's key", text)
        self.assertIn("were not computed again after the trials", text)
        self.assertIn("fixed after both agent arms were built", text)
        self.assertIn("First-request context", text)

    def test_human_verdict_overrides_the_automatic_one(self):
        key = {(r["task_id"], r["arm"]): r["row_id"] for r in self.read(self.run_dir / "grading-key.csv")}
        rows = self.read(self.run_dir / "grading.csv")
        for r in rows:
            if r["row_id"] == key[("run-noise", "cli")]:
                r["human_success"] = "yes"
            if r["row_id"] == key[("sec-posture", "cli")]:
                r["human_misread"] = "no"
        with open(self.run_dir / "grading.csv", "w", newline="") as fh:
            w = csv.DictWriter(fh, fieldnames=grade.CSV_COLUMNS)
            w.writeheader()
            w.writerows(rows)
        _, _, data = report.build_report(self.run_dir)
        self.assertEqual(data["cli"]["success_rate"], 1.0)
        self.assertEqual(data["cli"]["misread_answers"], 0)
        self.assertEqual(data["cli"]["human_graded"], 1)
        self.assertEqual(data["verdict"], "STOP")

    def test_fixture_run_continues_when_the_grader_finds_the_mcp_arm_better(self):
        key = {r["row_id"]: r["arm"] for r in self.read(self.run_dir / "grading-key.csv")}
        rows = self.read(self.run_dir / "grading.csv")
        for r in rows:
            r["human_success"] = "yes" if key[r["row_id"]] == "mcp" else "no"
            r["human_misread"] = "no"
        with open(self.run_dir / "grading.csv", "w", newline="") as fh:
            w = csv.DictWriter(fh, fieldnames=grade.CSV_COLUMNS)
            w.writeheader()
            w.writerows(rows)
        code, out = self.main()
        self.assertEqual(code, 0, out)
        self.assertIn("verdict: CONTINUE;", out)
        text = (self.run_dir / "report.md").read_text()
        self.assertIn("**Verdict: CONTINUE**\n", text)
        # The MCP run-noise trial used a forbidden tool, so it fails whatever
        # the grader says: 1 of 2.
        self.assertIn("| MCP task success is at least the CLI arm's | PASS | MCP 50.0 %, CLI 0.0 % (+50.0 points) |",
                      text)

    def test_expert_arm_is_reported_but_not_judged(self):
        with open(self.run_dir / "expert.csv", "w", newline="") as fh:
            w = csv.DictWriter(fh, fieldnames=grade.EXPERT_COLUMNS)
            w.writeheader()
            w.writerow({"task_id": "sec-posture", "trial": 1, "answer_json": "{}", "answer_text": "B",
                        "wall_clock_s": 900, "commands": 12})
        grade.grade_run(self.run_dir)
        text, _, data = report.build_report(self.run_dir)
        self.assertIn("expert", data["arms"])
        self.assertIn("## Expert arm", text)
        self.assertEqual(data["mcp"]["trials"], 2)

    def test_void_tasks_leave_the_comparison(self):
        (self.run_dir / "oracle-recheck.json").write_text(json.dumps({"tasks": {
            "sec-posture": {"expected": {"grade": "C", "failing_checks": 2, "drifted_checks": ["auto-create-topics"]}},
            "run-noise": {"expected": EXPECTED["run-noise"]}}}))
        grade.grade_run(self.run_dir)
        text, _, data = report.build_report(self.run_dir)
        self.assertEqual(data["paired_tasks"], ["run-noise"])
        self.assertEqual(data["mcp"]["trials"], 1)
        self.assertIn("leaving out 1 void tasks", text)
        self.assertIn("`sec-posture` is void and left out of the kill criteria: the expected grade changed", text)

    def test_missing_grades(self):
        (self.run_dir / "grades.json").unlink()
        code, _ = self.main()
        self.assertEqual(code, 2)

    @staticmethod
    def read(path: Path) -> list[dict]:
        with open(path, newline="") as fh:
            return list(csv.DictReader(fh))


if __name__ == "__main__":
    unittest.main()

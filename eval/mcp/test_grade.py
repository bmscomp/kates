"""Tests for grade.py: numbers and grounding, caveats, checks, and the
blinded grading sheet."""

import contextlib
import csv
import io
import json
import tempfile
import unittest
from pathlib import Path

import grade
import metrics
from evalfixtures import EXPECTED, TRANSCRIPTS, graded_run_dir


def read_csv(path: Path) -> list[dict]:
    with open(path, newline="") as fh:
        return list(csv.DictReader(fh))


def raws(text: str) -> list[str]:
    return [c.raw for c in grade.claims_in(grade.prose_of(text))]


class ClaimsTest(unittest.TestCase):
    def test_numbers_that_are_claims(self):
        text = "p99 rose 12.5% to 1,234 ms; 3 brokers, 1.5x slower, 100k records, -4 and (0.95)."
        self.assertEqual(raws(text), ["12.5%", "1,234", "3", "1.5x", "100k", "-4", "0.95"])

    def test_numbers_that_are_not_claims(self):
        text = ("On 2026-09-25 at 14:02, Kates 3.20.6 on 10.0.0.1:9092 ran a1b2c3d4 against payments-0; "
                "p99 and B2 and 8a3f9c21 and localhost:8080.")
        self.assertEqual(raws(text), [])

    def test_code_urls_and_list_markers_are_skipped(self):
        text = ("1. First, run `kates test get 42`.\n2) Then see http://x/api/7.\n"
                "```bash\nkates test create --records 100000\n```\n- 5 left")
        self.assertEqual(raws(text), ["5"])

    def test_ranges_ratios_and_detached_percent(self):
        self.assertEqual(raws("ISR 3/3, between 10-20 and 95 %"), ["3", "3", "10", "20", "95"])
        self.assertTrue(grade.claims_in("95 %")[0].percent)

    def test_units_and_multipliers(self):
        (c,) = grade.claims_in("5k/s")
        self.assertEqual(c.value, 5000)
        (c,) = grade.claims_in("250MB/s")
        self.assertEqual(c.value, 250)
        self.assertEqual(grade.claims_in("a 3-broker cluster"), [])


class GroundingTest(unittest.TestCase):
    REF = grade.reference_numbers(['{"p99LatencyMs": 12.3456, "ratio": 0.125, "count": 1234, "delta": -3.2}',
                                   "throughput 45%"])

    def is_grounded(self, text: str) -> bool:
        (claim,) = grade.claims_in(text)
        return grade.grounded(claim, self.REF)

    def test_rounding_to_the_precision_written(self):
        self.assertTrue(self.is_grounded("12.35"))
        self.assertTrue(self.is_grounded("12.3"))
        self.assertTrue(self.is_grounded("12"))
        self.assertFalse(self.is_grounded("12.4"))

    def test_percent_and_fraction(self):
        self.assertTrue(self.is_grounded("12.5%"))
        self.assertTrue(self.is_grounded("0.45"))
        self.assertTrue(self.is_grounded("45%"))

    def test_separator_sign_and_multiplier(self):
        self.assertTrue(self.is_grounded("1,234"))
        self.assertTrue(self.is_grounded("1.2k"))
        self.assertTrue(self.is_grounded("3.2"))
        self.assertTrue(self.is_grounded("-3.2"))

    def test_derived_numbers_are_ungrounded(self):
        self.assertFalse(self.is_grounded("67%"))
        self.assertFalse(self.is_grounded("1235"))

    def test_time_units(self):
        ref = grade.reference_numbers(['{"worstRecoveryMs": 27912, "timeToAllReadyMs": 21480, "recoveryDeltaMs": -1830}'])
        for text in ("27.9", "27.912", "21.5", "1.8", "-1.83"):
            with self.subTest(text=text):
                (claim,) = grade.claims_in(text)
                self.assertTrue(grade.grounded(claim, ref))
        (claim,) = grade.claims_in("29.7")
        self.assertFalse(grade.grounded(claim, ref))

    def test_ids_and_timestamps_are_not_references(self):
        ref = grade.reference_numbers(['{"id": "a1b2c3d4", "startedAt": "2026-09-26T06:25:00Z", "version": "3.9.1"}',
                                       "run 5e6f7081 at 2026-09-26T06:25:00Z on kafka 3.9.1"])
        self.assertEqual(ref, [])
        self.assertEqual(grade.reference_numbers(['{"n": 3}\n{"n": 4}']), [3.0, 4.0])

    def test_fixture_answers(self):
        for name, total, ungrounded in (("mcp-sec-posture.jsonl", 3, []),
                                        ("cli-sec-posture.jsonl", 4, ["3", "67%"])):
            with self.subTest(name=name):
                t = metrics.load(TRANSCRIPTS / name)
                final = metrics.final_text(t)
                ref = grade.reference_numbers(metrics.tool_texts(t) + ["saved at 2026-09-26T10:00:00Z"])
                claims = grade.answer_claims(final)
                self.assertEqual(len(claims), total)
                self.assertEqual([c.raw for c in claims if not grade.grounded(c, ref)], ungrounded)


class CaveatTest(unittest.TestCase):
    CAVEATS = [{"id": "config", "any_of": ["config-only", "configuration only"]},
               {"id": "cve", "any_of": ["Fixed List"]}]

    def test_phrases_are_matched_loosely(self):
        self.assertEqual(grade.missing_caveats(self.CAVEATS, "The pentest is CONFIG–ONLY; a fixed\n list."), [])
        self.assertEqual(grade.missing_caveats(self.CAVEATS, "It is config only."), ["config", "cve"])


class CompareTest(unittest.TestCase):
    def test_exact(self):
        self.assertTrue(grade.compare("exact", "B", " b ")[0])
        self.assertFalse(grade.compare("exact", "B", "B+")[0])
        self.assertTrue(grade.compare("exact", 3, 3.0)[0])
        self.assertTrue(grade.compare("exact", True, "true")[0])
        self.assertFalse(grade.compare("exact", True, 1)[0])
        self.assertTrue(grade.compare("exact", ["a", "B"], ["A", "b"])[0])
        self.assertTrue(grade.compare("exact", None, None)[0])
        self.assertFalse(grade.compare("exact", None, "x")[0])

    def test_number(self):
        self.assertTrue(grade.compare("number", 100, 104, 5, "pct")[0])
        self.assertFalse(grade.compare("number", 100, 106, 5, "pct")[0])
        self.assertTrue(grade.compare("number", 12.3456, "12.35", 0.01)[0])
        self.assertFalse(grade.compare("number", 3, "three")[0])
        self.assertFalse(grade.compare("number", 3, None)[0])

    def test_set_and_contains(self):
        self.assertTrue(grade.compare("set", ["a", "b"], ["B", "a"])[0])
        self.assertFalse(grade.compare("set", ["a", "b"], ["a"])[0])
        self.assertTrue(grade.compare("contains", "leader", "the Leader of payments-0")[0])
        self.assertTrue(grade.compare("contains", ["x", "y"], ["y", "x", "z"])[0])
        self.assertFalse(grade.compare("contains", ["x", "q"], "x and y")[0])

    def test_success_needs_every_check_and_no_forbidden_call(self):
        task = {"checks": [{"field": "a", "kind": "exact"}], "required_caveats": []}
        checks = grade.run_checks(task, {"a": "x"}, {"a": "x"})
        self.assertTrue(grade.success_of(checks, []))
        self.assertFalse(grade.success_of(checks, ["mcp__kates__disruption_report"]))
        self.assertIsNone(grade.success_of(grade.run_checks(task, None, {"a": "x"}), []))
        self.assertFalse(grade.success_of(grade.run_checks(task, {"a": "x"}, None), []))


class GradeRunTest(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.run_dir = graded_run_dir(Path(self.tmp.name))

    def tearDown(self):
        self.tmp.cleanup()

    def by_arm(self, result):
        return {(t["task_id"], t["arm"]): t for t in result["trials"]}

    def test_grades(self):
        result = grade.grade_run(self.run_dir)
        rows = self.by_arm(result)
        mcp_sec, cli_sec = rows[("sec-posture", "mcp")], rows[("sec-posture", "cli")]
        self.assertTrue(mcp_sec["auto_success"])
        self.assertEqual((mcp_sec["numbers_total"], mcp_sec["numbers_grounded"]), (3, 3))
        self.assertFalse(mcp_sec["silent_misread"])
        self.assertTrue(cli_sec["auto_success"])
        self.assertEqual(cli_sec["missing_caveats"], ["cve-static-list"])
        self.assertEqual(cli_sec["ungrounded"], ["3", "67%"])
        self.assertFalse(rows[("run-noise", "mcp")]["auto_success"])  # forbidden tool
        self.assertFalse(rows[("run-noise", "cli")]["auto_success"])  # no answer
        self.assertEqual(result["missing_oracle"], [])
        self.assertTrue((self.run_dir / "grades.json").exists())

    def test_sheet_is_blinded_and_keyed(self):
        grade.grade_run(self.run_dir)
        text = (self.run_dir / "grading.csv").read_text()
        header = text.splitlines()[0]
        self.assertNotIn("arm", header)
        self.assertNotIn("trial_dir", header)
        rows = list(csv.DictReader(io.StringIO(text)))
        self.assertEqual(len(rows), 4)
        self.assertEqual([r["row_id"] for r in rows], sorted(r["row_id"] for r in rows))
        key = {r["row_id"]: r for r in read_csv(self.run_dir / "grading-key.csv")}
        self.assertEqual(set(key), {r["row_id"] for r in rows})
        self.assertEqual(sorted(k["arm"] for k in key.values()), ["cli", "cli", "mcp", "mcp"])
        sec = [r for r in rows if r["task_id"] == "sec-posture"]
        self.assertTrue(all(json.loads(r["expected"]) == EXPECTED["sec-posture"] for r in sec))

    def test_regrading_keeps_ids_and_human_entries(self):
        grade.grade_run(self.run_dir)
        path = self.run_dir / "grading.csv"
        rows = read_csv(path)
        rows[0]["human_success"] = "no"
        rows[0]["human_notes"] = "wrong broker"
        with open(path, "w", newline="") as fh:
            w = csv.DictWriter(fh, fieldnames=grade.CSV_COLUMNS)
            w.writeheader()
            w.writerows(rows)
        grade.grade_run(self.run_dir)
        again = {r["row_id"]: r for r in read_csv(path)}
        self.assertEqual(set(again), {r["row_id"] for r in rows})
        self.assertEqual(again[rows[0]["row_id"]]["human_success"], "no")
        self.assertEqual(again[rows[0]["row_id"]]["human_notes"], "wrong broker")

    def test_expert_answers_join_the_sheet(self):
        with open(self.run_dir / "expert.csv", "w", newline="") as fh:
            w = csv.DictWriter(fh, fieldnames=grade.EXPERT_COLUMNS)
            w.writeheader()
            w.writerow({"task_id": "sec-posture", "trial": 1, "wall_clock_s": 600, "commands": 9,
                        "answer_json": json.dumps(EXPECTED["sec-posture"]),
                        "answer_text": "Grade B. The pentest is config-only and the CVE list is a fixed list."})
        result = grade.grade_run(self.run_dir)
        (expert,) = result["expert"]
        self.assertTrue(expert["auto_success"])
        self.assertFalse(expert["silent_misread"])
        self.assertIsNone(expert["numbers_total"])
        key = read_csv(self.run_dir / "grading-key.csv")
        self.assertIn("expert", {k["arm"] for k in key})

    def test_invalid_trials_are_not_on_the_sheet(self):
        m = self.run_dir / "run-noise" / "cli" / "trial-1" / "metrics.json"
        data = json.loads(m.read_text())
        data.update(status="no_result", valid=False)
        m.write_text(json.dumps(data))
        grade.grade_run(self.run_dir)
        self.assertEqual(len(read_csv(self.run_dir / "grading.csv")), 3)

    def test_oracle_drift(self):
        (self.run_dir / "oracle-recheck.json").write_text(json.dumps({"tasks": {
            "sec-posture": {"expected": {**EXPECTED["sec-posture"], "failing_checks": 3}},
            "run-noise": {"expected": EXPECTED["run-noise"]}}}))
        result = grade.grade_run(self.run_dir)
        self.assertEqual(result["oracle_drift"], {"sec-posture": ["failing_checks"]})
        self.assertEqual(result["void_tasks"], {"sec-posture": "the expected failing_checks changed during the run"})

    def test_a_failed_oracle_voids_its_task(self):
        (self.run_dir / "oracle.json").write_text(json.dumps({"tasks": {
            "sec-posture": {"error": "GET /api/security/audit: HTTP 503"},
            "run-noise": {"expected": EXPECTED["run-noise"]}}}))
        result = grade.grade_run(self.run_dir)
        self.assertEqual(result["void_tasks"],
                         {"sec-posture": "no expected answer (GET /api/security/audit: HTTP 503)"})

    def test_main_exit_codes(self):
        with contextlib.redirect_stdout(io.StringIO()), contextlib.redirect_stderr(io.StringIO()):
            self.assertEqual(grade.main(["--run-id", self.run_dir.name, "--runs-dir", str(self.run_dir.parent)]), 0)
            self.assertEqual(grade.main(["--run-id", "nope", "--runs-dir", str(self.run_dir.parent)]), 2)
            (self.run_dir / "oracle.json").unlink()
            self.assertEqual(grade.main(["--run-id", self.run_dir.name, "--runs-dir", str(self.run_dir.parent)]), 3)


if __name__ == "__main__":
    unittest.main()

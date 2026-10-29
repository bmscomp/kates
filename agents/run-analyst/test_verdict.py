"""Tests for verdict.py: the rules, the shape check, grounding and `next`."""

import copy
import unittest

import verdict


def answer(**over) -> dict:
    """A REGRESSED LOAD answer that passes every check; override fields to break it."""
    a = {
        "runId": "3fa9c2e1",
        "testType": "LOAD",
        "status": "DONE",
        "verdict": "REGRESSED",
        "headline": "p99 4.8 ms is above the 10-run band (2.9–3.6 ms) for this spec; throughput is inside it.",
        "taskErrors": [],
        "recordsLost": None,
        "baseline": {"runId": "77b0d4aa", "sameSpec": True, "regressionFlagged": True},
        "band": {"runs": 10, "metrics": [
            {"name": "p99LatencyMs", "kind": "latency", "value": 4.83, "min": 2.9, "max": 3.6, "position": "above"},
            {"name": "throughputRecPerSec", "kind": "throughput", "value": 51200, "min": 49800, "max": 53100,
             "position": "within"},
        ]},
        "overlaps": [],
        "brokerSkew": "broker 2 leads 41 % of the topic's partitions",
        "caveats": ["regression-one-baseline"],
        "evidence": [{"claim": "p99 above band", "tool": "assess_run", "field": "band.metrics[0].position"}],
        "next": "kates test compare 77b0d4aa 3fa9c2e1",
    }
    a.update(over)
    return a


def metric(kind: str, position: str) -> dict:
    return {"name": kind, "kind": kind, "value": 1, "min": 1, "max": 1, "position": position}


def band(runs: int, *metrics) -> dict:
    return {"runs": runs, "metrics": list(metrics)}


class DeriveTest(unittest.TestCase):
    def test_first_rule_that_matches_wins(self):
        cases = [
            ("INVALID_RUN", {"status": "FAILED", "overlaps": [{"kind": "run", "id": "a1b2c3d4"}]}),
            ("INVALID_RUN", {"taskErrors": ["topic not found"]}),
            ("INVALID_RUN", {"recordsLost": 3}),
            ("CONTAMINATED", {"overlaps": [{"kind": "disruption", "id": "d-1"}]}),
            ("NOT_COMPARABLE", {"baseline": {"runId": "77b0d4aa", "sameSpec": False, "regressionFlagged": True},
                                "band": band(2, metric("latency", "above"))}),
            ("NOT_COMPARABLE", {"baseline": None, "band": band(0)}),
            ("REGRESSED", {"baseline": None, "band": band(5, metric("throughput", "below"))}),
            ("REGRESSED", {"band": band(0)}),  # same-spec baseline flagged, no band
            ("IMPROVED", {"baseline": None, "band": band(5, metric("latency", "below"), metric("other", "above"))}),
            ("NORMAL", {"baseline": None, "band": band(5, metric("latency", "within"), metric("throughput", "above"))}),
            ("NORMAL", {"baseline": {"runId": "77b0d4aa", "sameSpec": True, "regressionFlagged": False},
                        "band": band(1)}),
        ]
        for expected, over in cases:
            with self.subTest(expected=expected, over=over):
                self.assertEqual(verdict.derive(answer(**over)), expected)

    def test_a_flag_against_a_different_spec_does_not_regress(self):
        a = answer(baseline={"runId": "77b0d4aa", "sameSpec": False, "regressionFlagged": True},
                   band=band(5, metric("latency", "within")))
        self.assertEqual(verdict.derive(a), "NORMAL")


class ProblemsTest(unittest.TestCase):
    def test_the_example_answer_passes(self):
        self.assertEqual(verdict.problems(answer()), [])

    def test_a_verdict_its_evidence_does_not_give(self):
        self.assertEqual(verdict.problems(answer(verdict="NORMAL")),
                         ["verdict is NORMAL but the evidence fields give REGRESSED by the rules"])

    def test_shape(self):
        a = answer()
        del a["band"]
        a["extra"] = 1
        self.assertEqual(verdict.problems(a), ["answer.band is missing", "answer.extra is not a field of the answer"])
        self.assertIn("answer.status must be one of DONE, FAILED", verdict.problems(answer(status="RUNNING")))
        self.assertIn("answer.band.runs must be integer", verdict.problems(answer(band={"runs": True, "metrics": []})))
        self.assertEqual(verdict.problems("not json"), ["answer must be object"])

    def test_numbers_must_be_in_a_field(self):
        p = verdict.problems(answer(headline="p99 rose 33 % to 4.8 ms"))
        self.assertEqual(p, ["headline says 33, which no structured field holds"])

    def test_rounded_and_grouped_numbers_are_grounded(self):
        self.assertEqual(verdict.problems(answer(headline="p99 4.8 ms vs 4.83, throughput 51,200 rec/s")), [])

    def test_ids_are_not_numbers(self):
        self.assertEqual(verdict.numbers_in("run 3fa9c2e1 and 77b0d4aa, p99 4.8"), ["4.8"])
        self.assertEqual(verdict.numbers_in("10-run band, 2.9–3.6 ms"), ["10", "2.9", "3.6"])

    def test_caveats_are_ids_only(self):
        self.assertEqual(verdict.problems(answer(caveats=["load-single-producer", "regression-one-baseline"])), [])
        p = verdict.problems(answer(caveats=["p99 rose 66.09% against c260a96f"]))
        self.assertEqual(p, ["answer.caveats[0] must be a caveat id, lowercase words joined by hyphens, "
                             "not 'p99 rose 66.09% against c260a96f'"])
        for bad in ("Load-Single-Producer", "load_single", "-load", "load-", ""):
            with self.subTest(bad=bad):
                self.assertTrue(verdict.problems(answer(caveats=[bad])))

    def test_caveats_ground_nothing(self):
        # The first lab run: a figure written into a caveat must not back the headline.
        p = verdict.problems(answer(caveats=["p99-66"], headline="p99 rose 66 % to 4.8 ms"))
        self.assertEqual(p, ["headline says 66, which no structured field holds"])

    def test_next_is_read_only(self):
        p = verdict.problems(answer(next="kates test create --type LOAD"))
        self.assertEqual(p, ["next must be a read-only command, but it says create"])
        self.assertEqual(verdict.problems(answer(next=None)), [])

    def test_answer_is_not_changed(self):
        a = answer()
        before = copy.deepcopy(a)
        verdict.problems(a)
        self.assertEqual(a, before)


if __name__ == "__main__":
    unittest.main()

"""What `panels.or_zero` actually evaluates to, run through Prometheus itself.

THE CLAIM THIS FILE EXISTS TO PROVE. Every fault-counting panel in this
repository draws its zero through `panels.or_zero`, and the argument for that
helper is a behavioural one: the fallback fires when the target is scraped and
the counter is simply absent, and does NOT fire when nothing is being scraped
at all. That distinction is the difference between a board that says "nothing
is wrong" and a board that says "I cannot see anything" — and it is the whole
reason the three KRaft tiles on a live cluster read a confident green 0 while
the honest panels beside them read No data.

An argument in a docstring is not a proof. `promtool test rules` evaluates
PromQL against series you declare, so the three worlds a fallback can find
itself in can each be set up and the result asserted:

    inner present            → the measured value, fallback not taken
    inner absent, anchor up  → 0, fallback taken
    nothing at all           → no sample, fallback NOT taken

and, for contrast, the same three against a bare `or vector(0)`, whose third
row is the bug: it answers 0 to a question nobody could measure.

Skipped when promtool is not installed, because it is not a Python dependency
and the rest of the dashboard suite is standard library only. CI has it
(scripts/check-dashboards.sh and the metric contract both shell out to it), so
the proof runs where it counts.
"""

from __future__ import annotations

import json
import shutil
import subprocess
import sys
import tempfile
import unittest
from pathlib import Path

ROOT = Path(__file__).resolve().parent
sys.path.insert(0, str(ROOT / "_lib"))

import panels as P  # noqa: E402

PROMTOOL = shutil.which("promtool")

ANCHOR = 'anchor{namespace="kafka"}'
FAULT = 'fault_total{namespace="kafka"}'

ANCHORED = P.or_zero("sum(%s)" % FAULT, ANCHOR)
BARE = "sum(%s) or vector(0)" % FAULT


def _case(expr, series, expected):
    """One promtool test: these series exist, this expression yields that."""
    samples = [] if expected is None else [{"labels": "{}", "value": expected}]
    return {
        "interval": "1m",
        "input_series": [{"series": s, "values": v} for s, v in series],
        "promql_expr_test": [
            {"expr": expr, "eval_time": "2m", "exp_samples": samples}],
    }


class ZeroFallbackSemantics(unittest.TestCase):
    """The three worlds, for the anchored fallback and the bare one."""

    def _run(self, cases):
        suite = {"tests": cases}
        with tempfile.TemporaryDirectory() as tmp:
            path = Path(tmp) / "zero-fallback.test.json"
            # promtool reads YAML, and JSON is a subset of YAML — which keeps
            # this file free of a PyYAML dependency the rest of the suite does
            # not have either.
            path.write_text(json.dumps(suite, indent=2))
            proc = subprocess.run(
                [PROMTOOL, "test", "rules", str(path)],
                capture_output=True, text=True)
        return proc

    def _assert_ok(self, cases):
        proc = self._run(cases)
        self.assertEqual(proc.returncode, 0,
                         "promtool disagreed:\n%s\n%s" % (proc.stdout, proc.stderr))

    @unittest.skipUnless(PROMTOOL, "promtool is not installed")
    def test_anchored_passes_the_measured_value_through(self):
        """A counter that exists is reported as it is, fallback untouched."""
        self._assert_ok([_case(ANCHORED,
                               [(ANCHOR, "1 1 1"), (FAULT, "0 3 7")], 7)])

    @unittest.skipUnless(PROMTOOL, "promtool is not installed")
    def test_anchored_draws_zero_while_the_target_is_scraped(self):
        """The case the fallback is FOR: scraped, and nothing has gone wrong.

        No `fault_total` series has ever been created because nothing has
        failed. The anchor proves the target is answering, so zero is a
        measurement and the tile is allowed to say so.
        """
        self._assert_ok([_case(ANCHORED, [(ANCHOR, "1 1 1")], 0)])

    @unittest.skipUnless(PROMTOOL, "promtool is not installed")
    def test_anchored_draws_nothing_when_nothing_is_scraped(self):
        """The case the fallback must NOT answer, and the point of this file.

        This is the state of a cluster whose PodMonitors Prometheus never
        selected — the defect charts/monitoring/values.yaml fixes on this
        branch. No anchor, no counter, no sample: the panel renders No data
        and the reader learns the truth, which is that nobody is looking.
        """
        self._assert_ok([_case(ANCHORED, [], None)])

    @unittest.skipUnless(PROMTOOL, "promtool is not installed")
    def test_the_bare_fallback_answers_zero_to_an_unanswerable_question(self):
        """Why `or vector(0)` is gated. Same world as the test above.

        Nothing is being scraped, and the expression returns 0 anyway — green,
        on a tile whose thresholds paint anything above zero red. If this test
        ever fails it means Prometheus changed `vector(0)`, not that the
        construct became safe; scripts/check-dashboards.py is what keeps it
        off the boards.
        """
        self._assert_ok([_case(BARE, [], 0)])

    @unittest.skipUnless(PROMTOOL, "promtool is not installed")
    def test_the_anchor_is_not_added_into_the_result(self):
        """`* 0` and not, say, a `min()`: the anchor's value must not leak.

        An anchor at 5 and a fault count that exists must still report the
        fault count. This is what stops the helper from quietly becoming a
        second series on every fault panel in the repository.
        """
        self._assert_ok([_case(ANCHORED,
                               [(ANCHOR, "5 5 5"), (FAULT, "1 1 2")], 2)])


class NoDataColour(unittest.TestCase):
    """Absence must read as neither healthy nor alarming.

    Grafana paints the *No data* placeholder with the BASE threshold step, so
    a fault panel says "No data" in green and a panel whose base is red says
    it in red. Confirmed in a real Grafana 12.3.1 against a reachable but
    empty Prometheus, on this repository's own kafka-performance board:
    *Under-replicated partitions* green, *Request handler idle* red, side by
    side, neither of them measuring anything. `noValue` changes the text and
    not the colour; a special `null` value mapping is what carries one.
    """

    def test_every_stat_and_gauge_gets_one(self):
        for build in (
            lambda: P.stat("t", "d", P.targets("up")),
            lambda: P.stat("t", "d", P.targets("up"), thresholds=[("green", None)]),
            lambda: P.gauge("t", "d", P.targets("up")),
        ):
            maps = build()["fieldConfig"]["defaults"]["mappings"]
            special = [m for m in maps if m["type"] == "special"]
            self.assertEqual(len(special), 1, maps)
            self.assertEqual(special[0]["options"]["match"], "null")

    def test_the_colour_is_never_a_verdict(self):
        colour = (P.stat("t", "d", P.targets("up"))["fieldConfig"]["defaults"]
                  ["mappings"][0]["options"]["result"]["color"])
        self.assertNotIn(colour, ("green", "red", "yellow", "orange"))

    def test_a_panel_keeps_its_own_mappings(self):
        """DRAINED/STOPPED must survive, with the indices they were given."""
        own = [{"type": "value", "options": {
            "0": {"text": "STOPPED", "color": "red", "index": 0}}}]
        maps = P.stat("t", "d", P.targets("up"), mappings=own)[
            "fieldConfig"]["defaults"]["mappings"]
        self.assertEqual(maps[0], own[0])
        self.assertEqual(maps[1]["options"]["result"]["index"], 1)


class CompatNoDataCheck(unittest.TestCase):
    """The compat harness's migration check, which asks the same question of
    what Grafana hands BACK after loading a board."""

    def setUp(self):
        sys.path.insert(0, str(ROOT.parent / "scripts" / "grafana-compat"))
        import compat
        self.neutral = compat.no_data_neutral

    def _panel(self, mappings):
        return {"type": "stat", "fieldConfig": {"defaults": {"mappings": mappings}}}

    def test_accepts_the_shape_the_builder_emits(self):
        self.assertTrue(self.neutral(self._panel([P.no_data_mapping()])))

    def test_rejects_a_panel_with_no_mapping(self):
        self.assertFalse(self.neutral(self._panel([])))

    def test_rejects_a_threshold_colour(self):
        m = P.no_data_mapping()
        m["options"]["result"]["color"] = "green"
        self.assertFalse(self.neutral(self._panel([m])))

    def test_rejects_the_legacy_shape_that_cannot_carry_a_colour(self):
        """Grafana 8/9 could migrate into MappingType.ValueToText, which has
        no colour field at all — a fix that silently only works on new
        Grafana. It does not, on any of the five versions tested, and this is
        what would catch it if a future one did."""
        self.assertFalse(self.neutral(
            self._panel([{"type": 1, "value": "null", "text": "No data"}])))


class OrZeroShape(unittest.TestCase):
    """The generated string, so a rewrite of the helper cannot silently
    change what scripts/check-dashboards.py is matching on."""

    def test_wraps_both_halves(self):
        self.assertEqual(P.or_zero("sum(x)", 'y{z="1"}'),
                         '(sum(x)) or (sum(y{z="1"}) * 0)')

    def test_is_not_a_bare_vector_zero(self):
        """The gate greps for `or vector(0)`; the helper must never emit it."""
        self.assertNotIn("vector(0)", P.or_zero("sum(x)", "y"))


if __name__ == "__main__":
    unittest.main()

"""Tests of oracle.py on recorded response shapes, without a cluster.

testdata/api holds one imaginary evaluation run on the kafka-cluster chart's
lab, in the shapes the backend's DTOs serialise to; testdata/state.json holds
the captures setup would have written for it, and expected_oracle.json the
answers, checked by hand against the fixtures.
"""

from __future__ import annotations

import copy
import io
import json
import os
import shutil
import sys
import tempfile
import unittest
import urllib.error
import urllib.request
from contextlib import redirect_stderr, redirect_stdout
from unittest import mock

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))

import oracle  # noqa: E402
import taskfile  # noqa: E402

HERE = os.path.dirname(os.path.abspath(__file__))
DATA = os.path.join(HERE, "testdata")
API = os.path.join(DATA, "api")


def fixture(name: str):
    with open(os.path.join(API, name), encoding="utf-8") as f:
        return json.load(f)


def load_json(path: str):
    with open(path, encoding="utf-8") as f:
        return json.load(f)


class AgainstFixtures(unittest.TestCase):
    """Every task's oracle, fetched through FixtureAPI and computed."""

    @classmethod
    def setUpClass(cls) -> None:
        cls.doc = taskfile.load(os.path.join(HERE, "tasks.json"))
        cls.state = load_json(os.path.join(DATA, "state.json"))
        cls.api = oracle.FixtureAPI(API)
        cls.results = oracle.compute_all(cls.doc, cls.state, cls.api)

    def test_every_task_is_computed(self) -> None:
        errors = {t: e["error"] for t, e in self.results.items() if "error" in e}
        self.assertEqual(errors, {})
        self.assertEqual(set(self.results), {t["id"] for t in self.doc["tasks"]})

    def test_answers_match_the_golden_file(self) -> None:
        golden = load_json(os.path.join(DATA, "expected_oracle.json"))
        got = {t: e["expected"] for t, e in self.results.items()}
        self.assertEqual(got, golden)

    def test_the_oracle_only_reads(self) -> None:
        # GETs and dry runs, nothing else: FixtureAPI has no other verb.
        for key in self.api.requests:
            self.assertTrue(key.startswith(("GET ", "DRYRUN ")), key)

    def test_every_fixture_is_used(self) -> None:
        unused = set(self.api.manifest) - set(self.api.requests)
        self.assertEqual(unused, set(), "fixtures no oracle reads")


class Readers(unittest.TestCase):
    def test_parse_instant(self) -> None:
        a = oracle.parse_instant("2026-09-26T09:35:18.225901123Z")
        b = oracle.parse_instant("2026-09-26T11:35:18.225901+02:00")
        self.assertEqual(a, b)
        self.assertEqual(oracle.parse_instant("2026-09-26T09:35:00Z").minute, 35)
        self.assertEqual(oracle.parse_instant("2026-09-26T09:35:00.5Z").microsecond, 500000)
        with self.assertRaises(oracle.OracleError):
            oracle.parse_instant("yesterday")

    def test_seconds(self) -> None:
        self.assertEqual(oracle.seconds(27.912), 27.912)
        self.assertEqual(oracle.seconds("1234ms"), 1.234)
        self.assertEqual(oracle.seconds("PT1M1.5S"), 61.5)
        self.assertEqual(oracle.seconds("12.5"), 12.5)
        for missing in (None, "N/A", True, float("nan"), {}):
            self.assertIsNone(oracle.seconds(missing), missing)

    def test_broker_id_of_pod(self) -> None:
        self.assertEqual(oracle.broker_id_of_pod("krafter-brokers-12"), 12)
        with self.assertRaises(oracle.OracleError):
            oracle.broker_id_of_pod("krafter-brokers")

    def test_pod_roles(self) -> None:
        roles = oracle.pod_roles(fixture("cluster_topology.json"))
        self.assertEqual(roles["krafter-controllers-0"], "controller")
        self.assertEqual(roles["krafter-brokers-5"], "broker")
        with self.assertRaises(oracle.OracleError):
            oracle.pod_roles({"cluster": {"name": "krafter"}, "nodes": []})

    def test_request_key_sorts_the_query(self) -> None:
        self.assertEqual(oracle.request_key("GET", "/api/tests", {"type": "LOAD", "page": 0}),
                         "GET /api/tests?page=0&type=LOAD")
        self.assertEqual(oracle.seg("a/../b c"), "a%2F..%2Fb%20c")


class Computations(unittest.TestCase):
    """The pure expect_* functions, including the premises they refuse."""

    def test_min_isr_sources(self) -> None:
        detail = fixture("topic_eval_facts.json")
        for source, level in oracle.MIN_ISR_SOURCES.items():
            d = copy.deepcopy(detail)
            d["configSources"]["min.insync.replicas"] = source
            self.assertEqual(oracle.expect_topic_min_isr(d)["set_at"], level)
        old = copy.deepcopy(detail)
        del old["configs"]["min.insync.replicas"], old["configSources"]["min.insync.replicas"]
        with self.assertRaises(oracle.OracleError):
            oracle.expect_topic_min_isr(old)

    def test_partition_without_leader(self) -> None:
        d = fixture("topic_eval_facts.json")
        d["partitionInfo"][1]["leader"] = -1
        with self.assertRaises(oracle.OracleError):
            oracle.expect_partition_leader(d, 1)
        with self.assertRaises(oracle.OracleError):
            oracle.expect_partition_leader(d, 9)

    def test_posture_leaves_out_policy_checks(self) -> None:
        audit = fixture("security_audit.json")
        audit["checks"].append({"name": "Kyverno Installed", "category": "policy", "status": "FAIL", "severity": "HIGH"})
        self.assertNotIn("Kyverno Installed", oracle.expect_security_posture(audit)["failing_high_or_critical"])
        with self.assertRaises(oracle.OracleError):
            oracle.expect_security_posture({"error": "Security audit failed: timeout", "grade": "F"})

    def test_listener_map_across_brokers(self) -> None:
        diff = {"consistent": [], "mismatches": [{"key": "listener.security.protocol.map", "consistent": False, "values": {
            "3": "PLAIN-9092:SASL_PLAINTEXT,TLS-9093:SSL", "4": "PLAIN-9092:SASL_PLAINTEXT,TLS-9093:SSL,EXT-9094:PLAINTEXT"}}]}
        self.assertEqual(oracle.expect_listener_encryption(diff),
                         {"all_listeners_encrypted": False, "unencrypted_listeners": ["EXT-9094", "PLAIN-9092"]})
        encrypted = {"consistent": [{"key": "listener.security.protocol.map", "value": "TLS-9093:SSL,SASL-9094:SASL_SSL",
                                     "values": {"3": "TLS-9093:SSL,SASL-9094:SASL_SSL"}}]}
        self.assertEqual(oracle.expect_listener_encryption(encrypted)["all_listeners_encrypted"], True)
        for bad in ({"consistent": []}, {"consistent": [{"key": "listener.security.protocol.map", "value": "(not set)", "values": {}}]}):
            with self.assertRaises(oracle.OracleError):
                oracle.expect_listener_encryption(bad)

    def test_cve_check_with_a_version(self) -> None:
        cve = fixture("security_cve.json")
        cve["kafkaVersion"] = "4.3.1"
        self.assertTrue(oracle.expect_pentest_and_cve(fixture("security_pentest.json"), cve)["cve_check_compared_version"])

    def test_drift_needs_a_baseline(self) -> None:
        with self.assertRaises(oracle.OracleError):
            oracle.expect_security_drift({"error": "No baseline saved.", "hasBaseline": False})

    def band_setup(self):
        runs = fixture("tests_load.json")["items"]
        cand = next(r for r in runs if r["id"] == "5e6f7081")
        return runs, cand

    def test_band_membership(self) -> None:
        runs, cand = self.band_setup()
        self.assertEqual([r["id"] for r in oracle.band_runs(cand, runs)], ["4d5e6f70", "3c4d5e6f", "2b3c4d5e", "1a2b3c4d"])
        earlier = next(r for r in runs if r["id"] == "4d5e6f70")
        for change in ({"status": "FAILED"}, {"scenarioName": "ci-gate"}, {"backend": "trogdor"},
                       {"createdAt": "2026-09-26T09:59:00Z"}, {"requestedSpec": None},
                       {"spec": dict(earlier["spec"], acks="1")}, {"testType": "STRESS"}):
            with self.subTest(change=change):
                self.assertFalse(oracle.in_band(cand, dict(earlier, **change)))
        self.assertFalse(oracle.in_band(cand, cand))

    def test_band_positions_and_size(self) -> None:
        runs, cand = self.band_setup()
        band = oracle.band_runs(cand, runs)
        summaries = {r["id"]: {"p99LatencyMs": v} for r, v in zip(band, (10.0, 11.0, 12.0, 13.0))}
        for value, where in ((9.0, "below"), (10.0, "within"), (13.5, "above")):
            summaries[cand["id"]] = {"p99LatencyMs": value}
            self.assertEqual(oracle.expect_noise_band(cand, band, summaries)["p99_position"], where)
        with self.assertRaises(oracle.OracleError):
            oracle.expect_noise_band(cand, band[:2], summaries)

    def test_load_parallel_premise(self) -> None:
        run = fixture("test_run_5e6f7081.json")  # numProducers 1
        with self.assertRaises(oracle.OracleError):
            oracle.expect_load_parallel(run)

    def test_spec_mismatch_premise(self) -> None:
        run = fixture("test_run_5e6f7081.json")
        with self.assertRaises(oracle.OracleError):
            oracle.expect_baseline_spec_mismatch(fixture("report_regression_5e6f7081.json"), run, run)

    def test_regression_without_a_delta(self) -> None:
        reg = fixture("report_regression_5e6f7081.json")
        del reg["deltas"]["p99LatencyMs"]["delta"]
        with self.assertRaises(oracle.OracleError):
            oracle.expect_run_vs_baseline(reg)

    def test_kates_caused_lag_needs_one_writer(self) -> None:
        runs = fixture("tests_all.json")["items"]
        check, group = fixture("cluster_check.json"), fixture("group_eval_orders.json")
        with self.assertRaises(oracle.OracleError):  # both the prime and the culprit
            oracle.expect_kates_caused_lag(check, runs, group, "eval-orders-abc123", "2026-09-26T09:30:00Z")
        with self.assertRaises(oracle.OracleError):  # none
            oracle.expect_kates_caused_lag(check, runs, group, "eval-orders-abc123", "2026-09-26T10:30:00Z")
        unhealthy = dict(check, status="WARNING")
        got = oracle.expect_kates_caused_lag(unhealthy, runs, group, "eval-orders-abc123", "2026-09-26T09:35:00Z")
        self.assertTrue(got["kafka_unhealthy"])

    def test_lag_needs_lag(self) -> None:
        group = fixture("group_eval_pay.json")
        for o in group["offsets"]:
            o["lag"] = 0
        with self.assertRaises(oracle.OracleError):
            oracle.expect_lag_partitions(group, fixture("topic_eval_pay.json"), "eval-pay-abc123")

    def test_stale_row_premise(self) -> None:
        listing = fixture("disruptions_list.json")
        listing["items"][3]["status"] = "PARTIAL"  # the launcher now records outcomes
        with self.assertRaises(oracle.OracleError):
            oracle.expect_stale_running([], listing, "2026-09-26T09:40:03Z", "9f8e7d6c")
        listing = fixture("disruptions_list.json")
        since = "2026-09-26T09:40:03Z"
        stuck_before = {"id": "x", "createdAt": "2026-09-26T09:10:00Z"}
        self.assertFalse(oracle.expect_stale_running([stuck_before], listing, since, "9f8e7d6c")["test_running"])
        started_since = {"id": "y", "createdAt": "2026-09-26T09:41:00Z"}
        self.assertTrue(oracle.expect_stale_running([started_since], listing, since, "9f8e7d6c")["test_running"])

    def test_leader_preview_must_name_the_leaders_pod(self) -> None:
        roles = oracle.pod_roles(fixture("cluster_topology.json"))
        dry = fixture("dryrun_leader_kill.json")
        dry["steps"][0]["affectedPods"] = ["krafter-brokers-5"]
        with self.assertRaises(oracle.OracleError):
            oracle.expect_leader_kill_preview(dry, roles)
        dry["steps"][0]["resolvedLeaderId"] = None
        with self.assertRaises(oracle.OracleError):
            oracle.expect_leader_kill_preview(dry, roles)

    def test_random_selection_suffix(self) -> None:
        roles = oracle.pod_roles(fixture("cluster_topology.json"))
        dry = {"wouldSucceed": True, "steps": [{"affectedPods": ["krafter-controllers-2 (random selection)"]}]}
        self.assertEqual(oracle.expect_playbook_preview(dry, roles)["restarted_pods"], ["krafter-controllers-2"])

    def test_controller_preview_premises(self) -> None:
        roles = oracle.pod_roles(fixture("cluster_topology.json"))
        dry = fixture("dryrun_controller_kill.json")
        with self.assertRaises(oracle.OracleError):
            oracle.expect_controller_kill_preview(dry, roles, "krafter-brokers-3")
        with self.assertRaises(oracle.OracleError):
            oracle.expect_controller_kill_preview(dry, roles, "krafter-controllers-1")

    def test_playbook_fit_on_the_right_partition(self) -> None:
        plan, dry = fixture("playbook_leader_cascade.json"), fixture("dryrun_leader_cascade.json")
        self.assertTrue(oracle.expect_playbook_fit(plan, dry, "__consumer_offsets", 1)["playbook_fits"])
        self.assertFalse(oracle.expect_playbook_fit(plan, dry, "__consumer_offsets", 2)["playbook_fits"])

    def test_debrief_premises(self) -> None:
        report = fixture("disruption_d1d1d1d1.json")
        with self.assertRaises(oracle.OracleError):
            oracle.expect_disruption_debrief({"planName": "p", "status": "RUNNING", "stepReports": []})
        none = copy.deepcopy(report)
        none["stepReports"][0]["podTimeline"] = [e for e in none["stepReports"][0]["podTimeline"] if e["eventType"] != "DELETED"]
        with self.assertRaises(oracle.OracleError):
            oracle.expect_disruption_debrief(none)
        two = copy.deepcopy(report)
        two["stepReports"][0]["podTimeline"].append({"podName": "krafter-brokers-4", "eventType": "DELETED"})
        with self.assertRaises(oracle.OracleError):
            oracle.expect_disruption_debrief(two)

    def test_debrief_unrecovered_and_graded(self) -> None:
        report = fixture("disruption_d1d1d1d1.json")
        report["stepReports"][0]["timeToAllReady"] = None
        report["stepReports"][0]["unrecoveredAfter"] = 300.0
        report["slaVerdict"] = {"grade": "B", "violated": False, "totalChecks": 3, "passedChecks": 2}
        got = oracle.expect_disruption_debrief(report)
        self.assertFalse(got["recovered"])
        self.assertTrue(got["sla_graded"])
        report["slaVerdict"]["grade"] = "-"
        self.assertFalse(oracle.expect_disruption_debrief(report)["sla_graded"])

    def test_compare_without_deltas(self) -> None:
        with self.assertRaises(oracle.OracleError):
            oracle.expect_disruption_compare({"currentId": "a", "baselineId": "b"}, {}, {})

    def test_type_errors(self) -> None:
        task = {"answer_fields": [{"name": "a", "type": "number"}, {"name": "b", "type": "string[]"}]}
        self.assertEqual(oracle.type_errors(task, {"a": 1.5, "b": ["x"]}), [])
        self.assertEqual(len(oracle.type_errors(task, {"a": True, "b": [1]})), 2)
        self.assertTrue(oracle.type_errors(task, {"a": 1}))


class FakeResponse(io.BytesIO):
    def __enter__(self):
        return self

    def __exit__(self, *exc):
        return False


class TheHttpClient(unittest.TestCase):
    """KatesAPI sends the key, escapes paths, and sends only GET and the dry run."""

    def setUp(self) -> None:
        self.sent = []

        def urlopen(opener, req, timeout=None):
            self.sent.append(req)
            return FakeResponse(b'{"ok": true}')

        patcher = mock.patch.object(urllib.request.OpenerDirector, "open", urlopen)
        patcher.start()
        self.addCleanup(patcher.stop)
        self.api = oracle.KatesAPI("http://localhost:8080/", "secret-key")

    def test_get(self) -> None:
        self.assertEqual(self.api.get(f"/api/kafka/topics/{oracle.seg('a/b')}", {"size": 200, "page": 0}), {"ok": True})
        req = self.sent[0]
        self.assertEqual(req.get_method(), "GET")
        self.assertEqual(req.full_url, "http://localhost:8080/api/kafka/topics/a%2Fb?page=0&size=200")
        self.assertEqual(req.get_header("X-api-key"), "secret-key")

    def test_dry_run(self) -> None:
        self.api.dry_run({"name": "p", "steps": []})
        req = self.sent[0]
        self.assertEqual((req.get_method(), req.full_url), ("POST", "http://localhost:8080/api/disruptions?dryRun=true"))
        self.assertEqual(json.loads(req.data), {"name": "p", "steps": []})
        self.assertFalse(hasattr(self.api, "post"), "the oracle must not be able to POST anything else")

    def test_http_errors_carry_no_key(self) -> None:
        def fail(opener, req, timeout=None):
            raise urllib.error.HTTPError(req.full_url, 404, "Not Found", {}, io.BytesIO(b'{"message":"no run"}'))

        with mock.patch.object(urllib.request.OpenerDirector, "open", fail):
            with self.assertRaises(oracle.OracleError) as ctx:
                self.api.get("/api/tests/deadbeef")
        self.assertIn("HTTP 404", str(ctx.exception))
        self.assertNotIn("secret-key", str(ctx.exception))


class Command(unittest.TestCase):
    """oracle.py's command line and exit codes."""

    def setUp(self) -> None:
        self.dir = tempfile.mkdtemp()
        self.addCleanup(shutil.rmtree, self.dir)
        shutil.copy(os.path.join(DATA, "state.json"), self.dir)

    def run_main(self, *args: str) -> tuple[int, str]:
        out, err = io.StringIO(), io.StringIO()
        with redirect_stdout(out), redirect_stderr(err):
            code = oracle.main(["--run-dir", self.dir, *args])
        return code, out.getvalue() + err.getvalue()

    def test_offline_run_writes_oracle_json(self) -> None:
        code, _ = self.run_main("--fixtures", API)
        self.assertEqual(code, 0)
        written = load_json(os.path.join(self.dir, "oracle.json"))
        self.assertEqual(written["version"], 1)
        self.assertEqual(written["tasks"]["fact-min-isr"]["expected"], {"min_insync_replicas": 2, "set_at": "broker"})

    def test_state_from_elsewhere(self) -> None:
        out_dir = os.path.join(self.dir, "fresh")
        code, _ = self.run_main("--fixtures", API, "--state", os.path.join(DATA, "state.json"), "--run-dir", out_dir)
        self.assertEqual(code, 0)
        self.assertTrue(os.path.exists(os.path.join(out_dir, "oracle.json")))

    def test_one_task_merges_into_what_is_there(self) -> None:
        self.run_main("--fixtures", API)
        code, _ = self.run_main("--fixtures", API, "--task", "sec-posture")
        self.assertEqual(code, 0)
        self.assertEqual(len(load_json(os.path.join(self.dir, "oracle.json"))["tasks"]), 21)

    def test_against_reports_moved_answers(self) -> None:
        self.run_main("--fixtures", API, "--out", os.path.join(self.dir, "before.json"))
        before = load_json(os.path.join(self.dir, "before.json"))
        before["tasks"]["fact-partition-leader"]["expected"]["leader_broker_id"] = 3
        with open(os.path.join(self.dir, "before.json"), "w", encoding="utf-8") as f:
            json.dump(before, f)
        code, text = self.run_main("--fixtures", API, "--against", os.path.join(self.dir, "before.json"))
        self.assertEqual(code, 3)
        self.assertIn("fact-partition-leader", text)

    def test_a_failed_task_exits_1_and_is_recorded(self) -> None:
        fixtures = os.path.join(self.dir, "api")
        shutil.copytree(API, fixtures)
        manifest = load_json(os.path.join(fixtures, "manifest.json"))
        del manifest["GET /api/security/audit"]
        with open(os.path.join(fixtures, "manifest.json"), "w", encoding="utf-8") as f:
            json.dump(manifest, f)
        code, _ = self.run_main("--fixtures", fixtures)
        self.assertEqual(code, 1)
        self.assertIn("error", load_json(os.path.join(self.dir, "oracle.json"))["tasks"]["sec-posture"])

    def test_bad_input_exits_2(self) -> None:
        self.assertEqual(self.run_main("--fixtures", API, "--task", "no-such-task")[0], 2)
        with mock.patch.dict(os.environ, {oracle.API_KEY_ENV: ""}):
            self.assertEqual(self.run_main()[0], 2)
        os.remove(os.path.join(self.dir, "state.json"))
        self.assertEqual(self.run_main("--fixtures", API)[0], 2)

    def test_missing_capture_is_a_task_error(self) -> None:
        state = load_json(os.path.join(self.dir, "state.json"))
        del state["tasks"]["run-vs-baseline"]
        with open(os.path.join(self.dir, "state.json"), "w", encoding="utf-8") as f:
            json.dump(state, f)
        code, text = self.run_main("--fixtures", API, "--task", "run-noise-band")
        self.assertEqual(code, 1)
        self.assertIn("has setup run for this task", text)


if __name__ == "__main__":
    unittest.main()

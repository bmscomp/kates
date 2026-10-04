"""Tests of setup.py against a fake lab: canned kates output, a fake API and
a fake clock, so every task's setup runs end to end without a cluster."""

from __future__ import annotations

import datetime as dt
import io
import json
import os
import shutil
import sys
import tempfile
import unittest
from contextlib import redirect_stderr, redirect_stdout
from unittest import mock

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))

import oracle  # noqa: E402
import setup  # noqa: E402
import taskfile  # noqa: E402

HERE = os.path.dirname(os.path.abspath(__file__))
DATA = os.path.join(HERE, "testdata")


def canned(name: str) -> dict:
    for d in ("setup", "api"):
        path = os.path.join(DATA, d, name)
        if os.path.exists(path):
            with open(path, encoding="utf-8") as f:
                return json.load(f)
    raise FileNotFoundError(name)


class FakeLab:
    """Answers kates commands and setup's API calls the way the lab would."""

    # The run ids the fixtures use, in the order setup creates the runs.
    RUN_IDS = ["1a2b3c4d", "2b3c4d5e", "3c4d5e6f", "4d5e6f70", "5e6f7081", "6f708192", "708192a3",
               "81a2b3c4", "92b3c4d5", "a3c4d5e6"]

    def __init__(self) -> None:
        self.commands: list[list[str]] = []
        self.posts: list[tuple[str, object]] = []
        self.runs_created = 0
        self.fail_on: str | None = None

    def run_kates(self, argv: list[str], timeout: float) -> tuple[int, str, str]:
        args = argv[1:]
        self.commands.append(args)
        assert args[-2:] == ["--context", "human"], args
        line = " ".join(args)
        if self.fail_on and self.fail_on in line:
            return 1, "", "  ✖ Failed: connection refused"
        if line.startswith("test create"):
            run = canned("test_create_done.json")
            n = self.runs_created
            run["id"] = self.RUN_IDS[n] if n < len(self.RUN_IDS) else f"{0xb0000000 + n:08x}"
            self.runs_created += 1
            return 0, json.dumps(run), ""
        if line.startswith("test baseline set"):
            return 0, "  ✓ Baseline set\n", ""
        if line.startswith("kafka create-topic"):
            return 0, json.dumps({"name": args[2], "partitions": 3}), ""
        if line.startswith("kafka delete-topic"):
            return 0, "  ✓ Topic deleted\n", ""
        if line.startswith("kafka produce"):
            return 0, json.dumps(canned("kafka_produce.json")), ""
        if line.startswith("kafka topic"):
            return 0, json.dumps(canned("topic_eval_diag.json")), ""
        if line.startswith("security baseline --save"):
            return 0, json.dumps(canned("security_baseline_save.json")), ""
        if line.startswith("cluster topology"):
            return 0, json.dumps(canned("cluster_topology.json")), ""
        if line.startswith("cluster info"):
            return 0, json.dumps(canned("cluster_info.json")), ""
        return 2, "", f"FakeLab has no answer for: kates {line}"

    # the HarnessAPI surface
    def post(self, path: str, body, timeout: float):
        self.posts.append((path, body))
        if path.startswith("/api/disruptions/templates/"):
            return canned("template_run.json")
        raise AssertionError(path)

    def get(self, path, params=None):
        raise AssertionError("setup steps in tasks.json do not GET through the API")


class FakeClock:
    def __init__(self) -> None:
        self.t = dt.datetime(2026, 9, 26, 9, 34, 42, 500000, tzinfo=dt.timezone.utc)
        self.slept: list[float] = []

    def now(self) -> dt.datetime:
        return self.t

    def sleep(self, seconds: float) -> None:
        self.slept.append(seconds)
        self.t += dt.timedelta(seconds=seconds)


def harness(lab: FakeLab, clock: FakeClock, run_dir: str | None, out: list[str], dry_run: bool = False) -> setup.Harness:
    return setup.Harness(kates="/usr/local/bin/kates", context="human", run_dir=run_dir, api=lab, dry_run=dry_run,
                         run_kates=lab.run_kates, now=clock.now, sleep=clock.sleep, out=out.append)


class EveryTask(unittest.TestCase):
    """Every task's setup, run against the fake lab, captures what its
    prompt and oracle need."""

    @classmethod
    def setUpClass(cls) -> None:
        cls.doc = taskfile.load(os.path.join(HERE, "tasks.json"))
        cls.by_id = taskfile.tasks_by_id(cls.doc)
        cls.dir = tempfile.mkdtemp()
        cls.lab, cls.clock, cls.out = FakeLab(), FakeClock(), []
        cls.state = setup.new_state("human", "http://localhost:8080", "/usr/local/bin/kates", run_tag="abc123")
        h = harness(cls.lab, cls.clock, cls.dir, cls.out)
        for tid in cls.by_id:
            setup.ensure_task(h, cls.doc, cls.state, tid)

    @classmethod
    def tearDownClass(cls) -> None:
        shutil.rmtree(cls.dir)

    def test_every_task_is_done(self) -> None:
        self.assertEqual({t for t, e in self.state["tasks"].items() if e["status"] == "done"}, set(self.by_id))

    def test_prompts_and_oracle_args_render(self) -> None:
        for tid, task in self.by_id.items():
            caps = taskfile.captures_for(self.state, tid, self.by_id)
            prompt = taskfile.render(task["prompt"], caps)
            self.assertNotIn("{", prompt, tid)
            taskfile.render(task["oracle"]["args"], caps)

    def test_state_is_written(self) -> None:
        with open(os.path.join(self.dir, "state.json"), encoding="utf-8") as f:
            written = json.load(f)
        self.assertEqual(written["run_tag"], "abc123")
        self.assertEqual(written["tasks"]["run-vs-baseline"]["captures"]["cand_id"], "5e6f7081")
        self.assertTrue(os.path.exists(os.path.join(self.dir, "setup", "run-vs-baseline", "step-0.out")))

    def test_a_used_setup_runs_once(self) -> None:
        creates = [c for c in self.lab.commands if c[:2] == ["test", "create"] and "eval-load-abc123" in c]
        self.assertEqual(len(creates), 7)
        topics = [c for c in self.lab.commands if c[:2] == ["kafka", "create-topic"] and c[2] == "eval-facts-abc123"]
        self.assertEqual(len(topics), 1)

    def test_typed_capture_reaches_the_template_body(self) -> None:
        bodies = [b for p, b in self.lab.posts if "templates" in p]
        self.assertEqual(bodies, [{"brokerId": 3}, {"brokerId": 3}, {"brokerId": 4}])

    def test_produce_repeats(self) -> None:
        self.assertEqual(sum(1 for c in self.lab.commands if c[:2] == ["kafka", "produce"]), 25)

    def test_the_clock_and_waits(self) -> None:
        caps = self.state["tasks"]["sre-kates-caused-lag"]["captures"]
        self.assertTrue(caps["alert_since"].endswith(":00Z"), caps["alert_since"])
        self.assertIsInstance(self.state["tasks"]["debrief-broker-kill"]["captures"]["d1_done"], int)
        self.assertIn(330.0, self.clock.slept)  # the diagnosis waits out preferred leader election

    def test_the_oracle_fixture_state_has_the_captures_setup_writes(self) -> None:
        # testdata/state.json feeds the offline oracle tests; if a capture is
        # renamed in tasks.json, it must be renamed there too.
        with open(os.path.join(DATA, "state.json"), encoding="utf-8") as f:
            fixture_state = json.load(f)
        for tid, entry in self.state["tasks"].items():
            want = set(entry["captures"])
            got = set(((fixture_state["tasks"].get(tid) or {}).get("captures") or {}))
            self.assertEqual(got, want, tid)


class Steps(unittest.TestCase):
    def setUp(self) -> None:
        self.lab, self.clock, self.out = FakeLab(), FakeClock(), []
        self.h = harness(self.lab, self.clock, None, self.out)

    def run_task(self, steps: list, extra_tasks: list | None = None) -> dict:
        doc = {"tasks": [{"id": "t", "setup": steps}] + (extra_tasks or [])}
        state = setup.new_state("human", None, "kates", run_tag="abc123")
        setup.ensure_task(self.h, doc, state, "t")
        return state

    def test_expect_mismatch_fails(self) -> None:
        with self.assertRaises(setup.StepFailed) as ctx:
            self.run_task([{"kind": "kates", "args": ["test", "create", "-o", "json"], "expect": {"$.status": "FAILED"}}])
        self.assertIn("expected $.status = 'FAILED', got 'DONE'", str(ctx.exception))

    def test_a_failed_command_is_recorded(self) -> None:
        self.lab.fail_on = "cluster info"
        doc = {"tasks": [{"id": "t", "setup": [{"kind": "kates", "args": ["cluster", "info", "-o", "json"]}]}]}
        state = setup.new_state("human", None, "kates")
        with self.assertRaises(setup.StepFailed):
            setup.ensure_task(self.h, doc, state, "t")
        self.assertEqual(state["tasks"]["t"]["status"], "failed")
        self.assertIn("connection refused", state["tasks"]["t"]["error"])

    def test_allow_fail(self) -> None:
        self.lab.fail_on = "delete-topic"
        state = self.run_task([{"kind": "kates", "args": ["kafka", "delete-topic", "x", "--yes"], "allow_fail": True}])
        self.assertEqual(state["tasks"]["t"]["status"], "done")

    def test_capture_path_missing(self) -> None:
        with self.assertRaises(setup.StepFailed) as ctx:
            self.run_task([{"kind": "kates", "args": ["cluster", "info", "-o", "json"], "capture": {"x": "$.nope"}}])
        self.assertIn("cannot capture x", str(ctx.exception))

    def test_output_that_is_not_json(self) -> None:
        with self.assertRaises(setup.StepFailed):
            self.run_task([{"kind": "kates", "args": ["test", "baseline", "set", "x", "-o", "json"], "capture": {"x": "$.id"}}])

    def test_assert_equal(self) -> None:
        with self.assertRaises(setup.StepFailed) as ctx:
            self.run_task([{"kind": "clock", "capture": {"a": "epoch"}},
                           {"kind": "assert_equal", "left": "{a}", "right": "0", "message": "clock drifted"}])
        self.assertIn("clock drifted", str(ctx.exception))

    def test_wait_since_counts_from_the_capture(self) -> None:
        self.run_task([{"kind": "clock", "capture": {"t0": "epoch"}}, {"kind": "wait", "seconds": 60},
                       {"kind": "wait", "seconds": 90, "since": "{t0}"}])
        self.assertEqual(self.clock.slept, [60.0, 29.5])  # t0 was floored to the second

    def test_wait_since_already_past(self) -> None:
        self.run_task([{"kind": "wait", "seconds": 10, "since": "1"}])
        self.assertEqual(self.clock.slept, [])

    def test_clock_formats(self) -> None:
        t = dt.datetime(2026, 9, 26, 9, 34, 42, 500000, tzinfo=dt.timezone.utc)
        self.assertEqual(setup.clock_value(t, "rfc3339"), "2026-09-26T09:34:42Z")
        self.assertEqual(setup.clock_value(t, "rfc3339_minute"), "2026-09-26T09:34:00Z")
        self.assertEqual(setup.clock_value(t, "hhmm_utc"), "09:34")
        self.assertEqual(setup.clock_value(t, "epoch"), 1790415282)

    def test_api_step_needs_the_api(self) -> None:
        self.h.api = None
        with self.assertRaises(setup.StepFailed) as ctx:
            self.run_task([{"kind": "api", "method": "POST", "path": "/api/disruptions/templates/broker-kill-recovery",
                            "body": {}, "why": "x"}])
        self.assertIn(oracle.API_KEY_ENV, str(ctx.exception))

    def test_a_task_used_twice_runs_once(self) -> None:
        dep = {"id": "d", "setup": [{"kind": "kates", "args": ["cluster", "info", "-o", "json"], "capture": {"b": "$.brokers[0].id"}}]}
        doc = {"tasks": [{"id": "t", "setup": [{"kind": "use", "task": "d"}, {"kind": "use", "task": "d"}]}, dep,
                         {"id": "u", "setup": [{"kind": "use", "task": "d"}]}]}
        state = setup.new_state("human", None, "kates")
        setup.ensure_task(self.h, doc, state, "t")
        setup.ensure_task(self.h, doc, state, "u")
        self.assertEqual(len(self.lab.commands), 1)
        self.assertEqual(taskfile.captures_for(state, "u", taskfile.tasks_by_id(doc))["b"], 3)

    def test_a_done_task_is_not_run_again_on_resume(self) -> None:
        doc = {"tasks": [{"id": "t", "setup": [{"kind": "kates", "args": ["cluster", "info", "-o", "json"]}]}]}
        state = setup.new_state("human", None, "kates")
        setup.ensure_task(self.h, doc, state, "t")
        fresh = harness(self.lab, self.clock, None, [])
        setup.ensure_task(fresh, doc, state, "t")
        self.assertEqual(len(self.lab.commands), 1)


class HarnessApi(unittest.TestCase):
    def test_posts_outside_the_allowlist_are_refused(self) -> None:
        api = setup.HarnessAPI("http://localhost:8080", "k")
        for path in ("/api/tests", "/api/disruptions", "/api/disruptions/compound", "/api/disruptions/templates",
                     "/api/kafka/produce/x", "/api/security/baseline"):
            with self.subTest(path=path), self.assertRaises(setup.StepFailed):
                api.post(path, {}, 10)

    def test_allowed_posts_are_sent_with_the_step_timeout(self) -> None:
        api = setup.HarnessAPI("http://localhost:8080", "k", timeout=90)
        seen = []

        def send(method, path, params, body):
            seen.append((method, path, api.timeout))
            return {"id": "x"}

        api._send = send
        api.post("/api/disruptions/templates/broker-kill-recovery", {"brokerId": 3}, 900)
        api.post("/api/disruptions/templates/leader-election-storm", {}, 60)
        self.assertEqual(seen, [("POST", "/api/disruptions/templates/broker-kill-recovery", 900),
                                ("POST", "/api/disruptions/templates/leader-election-storm", 60)])
        self.assertEqual(api.timeout, 90)


class Subprocess(unittest.TestCase):
    def test_overrides_of_the_context_are_stripped(self) -> None:
        probe = [sys.executable, "-c",
                 "import json, os; print(json.dumps({k: os.environ.get(k) for k in "
                 "('KATES_URL', 'KATES_API_KEY', 'KATES_CONTEXT', 'KATES_OUTPUT', 'KEEP_ME')}))"]
        env = {"KATES_URL": "http://elsewhere", "KATES_API_KEY": "leak", "KATES_CONTEXT": "prod",
               "KATES_OUTPUT": "table", "KEEP_ME": "1"}
        with mock.patch.dict(os.environ, env):
            code, out, _ = setup.run_kates_subprocess(probe, 30)
        self.assertEqual(code, 0)
        self.assertEqual(json.loads(out), {"KATES_URL": None, "KATES_API_KEY": None, "KATES_CONTEXT": None,
                                           "KATES_OUTPUT": None, "KEEP_ME": "1"})

    def test_a_missing_binary(self) -> None:
        code, _, err = setup.run_kates_subprocess(["/nonexistent/kates", "version"], 5)
        self.assertEqual(code, 127)


class Command(unittest.TestCase):
    """setup.py's command line: dry run, exit codes, and state handling."""

    def setUp(self) -> None:
        self.dir = tempfile.mkdtemp()
        self.addCleanup(shutil.rmtree, self.dir)
        self.lab, self.clock = FakeLab(), FakeClock()

    def factory(self, **kw) -> setup.Harness:
        h = setup.Harness(**kw)
        h.run_kates, h.now, h.sleep = self.lab.run_kates, self.clock.now, self.clock.sleep
        if h.api is not None:
            h.api = self.lab
        return h

    def run_main(self, *args: str, env: dict | None = None) -> tuple[int, str]:
        out, err = io.StringIO(), io.StringIO()
        with mock.patch.dict(os.environ, env or {}), redirect_stdout(out), redirect_stderr(err):
            code = setup.main(list(args), harness_factory=self.factory)
        return code, out.getvalue() + err.getvalue()

    def test_dry_run_touches_nothing_and_prints_every_task(self) -> None:
        code, text = self.run_main("--dry-run")
        self.assertEqual(code, 0)
        self.assertEqual(self.lab.commands, [])
        self.assertEqual(self.lab.posts, [])
        self.assertEqual(self.clock.slept, [])
        doc = taskfile.load(os.path.join(HERE, "tasks.json"))
        for t in doc["tasks"]:
            self.assertIn(f"== {t['id']}", text)
        self.assertIn("POST /api/disruptions/templates/broker-kill-recovery", text)
        self.assertIn("prompt: Did LOAD run <small_id> regress against the LOAD baseline?", text)
        self.assertNotIn("{run_tag}", text)
        self.assertFalse(os.listdir(self.dir))

    def test_a_real_run_through_main(self) -> None:
        code, text = self.run_main("--run-dir", self.dir, "--kates", sys.executable, "--task", "run-noise-band")
        self.assertEqual(code, 0, text)
        with open(os.path.join(self.dir, "state.json"), encoding="utf-8") as f:
            state = json.load(f)
        self.assertEqual(set(state["tasks"]), {"run-vs-baseline", "run-noise-band"})

    def test_api_steps_need_the_key(self) -> None:
        code, text = self.run_main("--run-dir", self.dir, "--kates", sys.executable, "--task", "debrief-broker-kill",
                                   env={oracle.API_KEY_ENV: ""})
        self.assertEqual(code, 2)
        self.assertIn(oracle.API_KEY_ENV, text)

    def test_with_the_key_the_template_runs(self) -> None:
        code, text = self.run_main("--run-dir", self.dir, "--kates", sys.executable, "--task", "debrief-broker-kill",
                                   env={oracle.API_KEY_ENV: "k"})
        self.assertEqual(code, 0, text)
        self.assertEqual(len(self.lab.posts), 1)

    def test_a_failing_step_exits_1(self) -> None:
        self.lab.fail_on = "cluster topology"
        code, text = self.run_main("--run-dir", self.dir, "--kates", sys.executable, "--task",
                                   "gameday-controller-kill-uncounted")
        self.assertEqual(code, 1)
        self.assertIn("gameday-controller-kill-uncounted", text)

    def test_bad_input_exits_2(self) -> None:
        self.assertEqual(self.run_main("--task", "nope", "--dry-run")[0], 2)
        self.assertEqual(self.run_main()[0], 2)  # no --run-dir
        self.assertEqual(self.run_main("--run-dir", self.dir, "--kates", "/nonexistent/kates")[0], 2)
        self.run_main("--run-dir", self.dir, "--kates", sys.executable, "--task", "fact-min-isr")
        code, text = self.run_main("--run-dir", self.dir, "--kates", sys.executable, "--context", "prod")
        self.assertEqual(code, 2)
        self.assertIn("context", text)


if __name__ == "__main__":
    unittest.main()

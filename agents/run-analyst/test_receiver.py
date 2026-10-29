"""Tests for receiver.py: the endpoint, dedupe, the gate and the budget."""

import datetime
import http.client
import json
import queue
import tempfile
import threading
import unittest
from http.server import ThreadingHTTPServer
from pathlib import Path

import receiver
from test_verdict import answer

NOW = datetime.datetime(2026, 10, 29, 12, 0, tzinfo=datetime.UTC)


def run(status="DONE", created=NOW - datetime.timedelta(minutes=20)):
    return {"id": "3fa9c2e1", "status": status, "createdAt": created.isoformat().replace("+00:00", "Z")}


class FakeKates:
    def __init__(self, runs):
        self.runs = runs

    def get_run(self, run_id):
        r = self.runs.get(run_id)
        if isinstance(r, Exception):
            raise r
        return r


class FakeSinks:
    def __init__(self):
        self.delivered, self.notices = [], []

    def deliver(self, a):
        self.delivered.append(a)

    def notice(self, run_id, kind, detail):
        self.notices.append((run_id, kind))


class StoreCase(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.store = receiver.Store(Path(self.tmp.name) / "s.sqlite")

    def tearDown(self):
        self.store.db.close()
        self.tmp.cleanup()


class EndpointTest(StoreCase):
    def setUp(self):
        super().setUp()
        self.q = queue.Queue()
        self.server = ThreadingHTTPServer(("127.0.0.1", 0), receiver.make_handler(self.store, self.q))
        threading.Thread(target=self.server.serve_forever, daemon=True).start()

    def tearDown(self):
        self.server.shutdown()
        self.server.server_close()
        super().tearDown()

    def post(self, body, event="test.completed", attempt="1"):
        conn = http.client.HTTPConnection(*self.server.server_address, timeout=5)
        data = body if isinstance(body, bytes) else json.dumps(body).encode()
        conn.request("POST", "/webhook", data, {"X-Kates-Event": event, "X-Kates-Attempt": attempt,
                                                "Content-Type": "application/json"})
        status = conn.getresponse().status
        conn.close()
        return status

    def queued(self):
        out = []
        while not self.q.empty():
            out.append(self.q.get_nowait())
        return out

    def test_a_retry_is_accepted_once(self):
        body = {"event": "test.completed", "testId": "3fa9c2e1", "testType": "LOAD", "status": "DONE",
                "timestamp": "2026-10-29T11:40:00Z"}
        self.assertEqual(self.post(body), 202)
        self.assertEqual(self.post(body, attempt="2"), 202)
        self.assertEqual(self.queued(), ["3fa9c2e1"])
        self.assertEqual(self.post({**body, "status": "FAILED"}), 202)  # a different event for the run
        self.assertEqual(self.queued(), ["3fa9c2e1"])

    def test_bad_bodies(self):
        for body in ({"testId": "../../etc", "status": "DONE"}, {"testId": "3fa9c2e1", "status": "RUNNING"},
                     {"testId": 7, "status": "DONE"}, {"status": "DONE"}, b"not json", b"[1]"):
            with self.subTest(body=body):
                self.assertEqual(self.post(body), 400)
        self.assertEqual(self.post(b"x" * (receiver.MAX_BODY + 1)), 400)
        self.assertEqual(self.queued(), [])

    def test_other_events_are_ignored(self):
        self.assertEqual(self.post({"testId": "3fa9c2e1", "status": "DONE"}, event="test.created"), 202)
        self.assertEqual(self.queued(), [])

    def test_healthz(self):
        conn = http.client.HTTPConnection(*self.server.server_address, timeout=5)
        conn.request("GET", "/healthz")
        self.assertEqual(conn.getresponse().status, 200)
        conn.close()


class WorkerTest(StoreCase):
    def worker(self, runs, analyze=lambda run_id: (answer(), None), max_per_hour=20):
        self.sinks = FakeSinks()
        self.calls = []

        def counted(run_id):
            self.calls.append(run_id)
            return analyze(run_id)

        return receiver.Worker(FakeKates(runs), self.store, self.sinks, counted, max_per_hour,
                               clock=NOW.timestamp)

    def test_an_answer_is_delivered(self):
        self.assertEqual(self.worker({"3fa9c2e1": run()}).process("3fa9c2e1"), "REGRESSED")
        self.assertEqual(len(self.sinks.delivered), 1)

    def test_forged_and_stale_events_are_dropped_quietly(self):
        runs = {"3fa9c2e1": run(status="RUNNING"),
                "aaaaaaaa": run(created=NOW - datetime.timedelta(hours=49)),
                "bbbbbbbb": {"id": "bbbbbbbb", "status": "DONE", "createdAt": "yesterday"}}
        w = self.worker(runs)
        for run_id in ("3fa9c2e1", "aaaaaaaa", "bbbbbbbb", "cccccccc"):
            self.assertEqual(w.process(run_id), "DROPPED")
        self.assertEqual((self.calls, self.sinks.delivered, self.sinks.notices), ([], [], []))

    def test_kates_unreachable_is_said_once_an_hour(self):
        down = OSError("connection refused")
        w = self.worker({"3fa9c2e1": down, "aaaaaaaa": down})
        with self.assertLogs("run-analyst", "ERROR"):
            self.assertEqual(w.process("3fa9c2e1"), "GATE_ERROR")
            self.assertEqual(w.process("aaaaaaaa"), "GATE_ERROR")
        self.assertEqual(self.sinks.notices, [("3fa9c2e1", "GATE_ERROR")])

    def test_budget(self):
        w = self.worker({"3fa9c2e1": run()}, analyze=lambda run_id: (None, "boom"), max_per_hour=2)
        self.assertEqual([w.process("3fa9c2e1") for _ in range(3)], ["ANALYST_ERROR", "ANALYST_ERROR", "SKIPPED_BUDGET"])
        self.assertEqual(len(self.calls), 2)
        self.assertEqual(self.sinks.notices[-1], ("3fa9c2e1", "SKIPPED_BUDGET"))
        # An hour later the budget is back.
        self.store.record("x", "STARTED", NOW.timestamp() - 3601)
        self.assertEqual(self.store.count_since("STARTED", 3600, NOW.timestamp() + 3601), 0)

    def test_an_analysis_that_raises(self):
        def boom(run_id):
            raise RuntimeError("sdk crashed")

        self.assertEqual(self.worker({"3fa9c2e1": run()}, analyze=boom).process("3fa9c2e1"), "ANALYST_ERROR")
        self.assertEqual(self.sinks.notices, [("3fa9c2e1", "ANALYST_ERROR")])


class StoreTest(StoreCase):
    def test_claim_forgets_after_a_week(self):
        self.assertTrue(self.store.is_empty())
        self.assertTrue(self.store.claim("a:DONE", now=0))
        self.assertFalse(self.store.claim("a:DONE", now=1))
        self.assertTrue(self.store.claim("a:DONE", now=receiver.SEEN_DAYS * 86400 + 1))


class ConfigTest(unittest.TestCase):
    ENV = {"ANALYST_KATES_URL": "http://kates:8080/", "KATES_API_KEY": "k", "KATES_CONTEXT": "lab",
           "ANALYST_CLUSTER_ID": "abc"}

    def test_defaults(self):
        cfg = receiver.Config.from_env(self.ENV)
        self.assertEqual((cfg.kates_url, cfg.model, cfg.max_per_hour), ("http://kates:8080", "claude-opus-5-5", 20))

    def test_missing_and_refused(self):
        with self.assertRaisesRegex(SystemExit, "KATES_CONTEXT, ANALYST_CLUSTER_ID"):
            receiver.Config.from_env({"ANALYST_KATES_URL": "u", "KATES_API_KEY": "k"})
        with self.assertRaisesRegex(SystemExit, "unset KATES_URL"):
            receiver.Config.from_env({**self.ENV, "KATES_URL": "http://other"})


if __name__ == "__main__":
    unittest.main()

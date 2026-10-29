"""Tests for poll.py: the first poll seeds, later polls hand over new runs only."""

import tempfile
import unittest
from pathlib import Path

import poll
import receiver


class FakeKates:
    def __init__(self):
        self.lists = {"DONE": [], "FAILED": []}

    def finished_runs(self, status):
        return self.lists[status]


class FakeWorker:
    def __init__(self):
        self.processed = []

    def process(self, run_id):
        self.processed.append(run_id)


class PollTest(unittest.TestCase):
    def test_seed_then_new_runs_in_order(self):
        with tempfile.TemporaryDirectory() as d:
            store = receiver.Store(Path(d) / "s.sqlite")
            kates, worker = FakeKates(), FakeWorker()
            kates.lists["DONE"] = [{"id": "00000002"}, {"id": "00000001"}]
            self.assertEqual(poll.poll_once(kates, store, worker, seed=True), [])
            self.assertEqual(worker.processed, [])

            kates.lists["DONE"] = [{"id": "00000004"}, {"id": "00000003"}, {"id": "00000002"}]
            kates.lists["FAILED"] = [{"id": "0000000f"}, {"id": "not-an-id"}]
            self.assertEqual(poll.poll_once(kates, store, worker, seed=False), ["00000003", "00000004", "0000000f"])
            self.assertEqual(poll.poll_once(kates, store, worker, seed=False), [])
            store.db.close()


if __name__ == "__main__":
    unittest.main()

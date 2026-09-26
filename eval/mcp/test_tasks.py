"""Tests for tasks.py: loading a task list for the runner, and the fixed
answer instruction. The schema's own rules are tested in test_taskfile.py."""

import json
import tempfile
import unittest
from pathlib import Path

import tasks
from evalfixtures import TASKS

FIXTURE_ORACLES = ["security_grade", "run_noise", "broken"]


def fixture() -> dict:
    return json.loads(TASKS.read_text())


class LoadTest(unittest.TestCase):
    def test_fixture_loads(self):
        doc = tasks.load_tasks(TASKS, FIXTURE_ORACLES)
        self.assertEqual([t["id"] for t in doc["tasks"]], ["sec-posture", "run-noise"])
        self.assertEqual(tasks.task_by_id(doc)["run-noise"]["forbid"]["mcp"], ["disruption_report"])

    def test_the_real_task_list_loads_with_the_real_oracles(self):
        import oracle
        doc = tasks.load_tasks(Path(tasks.__file__).with_name("tasks.json"), oracle.ORACLES)
        self.assertGreaterEqual(len(doc["tasks"]), 15)

    def write(self, doc: dict) -> Path:
        d = tempfile.mkdtemp()
        self.addCleanup(lambda: __import__("shutil").rmtree(d, ignore_errors=True))
        p = Path(d) / "tasks.json"
        p.write_text(json.dumps(doc))
        return p

    def test_every_problem_is_listed(self):
        doc = fixture()
        doc["tasks"][0]["trials"] = 2
        doc["tasks"][1]["persona"] = "manager"
        doc["tasks"][1]["prompt"] += " And {nothing}?"
        with self.assertRaises(tasks.TaskError) as cm:
            tasks.load_tasks(self.write(doc), FIXTURE_ORACLES)
        text = "\n".join(cm.exception.problems)
        self.assertIn("trials must be an integer of at least 3", text)
        self.assertIn("persona must be one of", text)
        self.assertIn("{nothing}", text)

    def test_unknown_oracle_is_refused_only_when_oracles_are_given(self):
        doc = fixture()
        doc["tasks"][0]["oracle"]["fn"] = "nope"
        path = self.write(doc)
        tasks.load_tasks(path)
        with self.assertRaises(tasks.TaskError) as cm:
            tasks.load_tasks(path, FIXTURE_ORACLES)
        self.assertIn("'nope' is not in oracle.py", str(cm.exception))

    def test_invalid_json_is_a_task_error(self):
        with tempfile.TemporaryDirectory() as d:
            p = Path(d) / "t.json"
            p.write_text("{not json")
            with self.assertRaises(tasks.TaskError):
                tasks.load_tasks(p)


class PromptTest(unittest.TestCase):
    def test_the_answer_request_is_arm_independent_and_names_every_field(self):
        task = tasks.task_by_id(tasks.load_tasks(TASKS))["sec-posture"]
        text = tasks.answer_request(task["answer_fields"])
        self.assertIn('"grade" (a string)', text)
        self.assertIn('"failing_checks" (a number)', text)
        self.assertIn('"drifted_checks" (an array of strings)', text)
        self.assertIn("fenced code block marked json", text)
        self.assertIn("Do not look anything up again", text)
        for hint in ("mcp", "kates", "tool", "shell", "CLI"):
            self.assertNotIn(hint, text)

    def test_the_question_carries_no_answer_field(self):
        task = tasks.task_by_id(tasks.load_tasks(TASKS))["sec-posture"]
        q = tasks.question(task, {"baseline_ts": "2026-09-26T10:00:00Z", "run_tag": "abc123"})
        self.assertTrue(q.startswith("Where does the lab Kafka cluster stand"))
        self.assertTrue(q.endswith("saved at 2026-09-26T10:00:00Z?"))
        for f in task["answer_fields"]:
            self.assertNotIn(f["name"], q)
            self.assertNotIn(f["description"], q)

    def test_question_in_a_dry_run(self):
        task = tasks.task_by_id(tasks.load_tasks(TASKS))["sec-posture"]
        self.assertIn("saved at <baseline_ts>?", tasks.question(task, {}, "placeholder"))


if __name__ == "__main__":
    unittest.main()

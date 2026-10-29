"""Tests for sinks.py: who gets what, escaping, one issue per run."""

import json
import tempfile
import unittest
from pathlib import Path

import sinks
from test_verdict import answer


class Recorder:
    def __init__(self, issues=()):
        self.posts = []
        self.issues = list(issues)

    def post(self, url, body, headers=None, method="POST"):
        self.posts.append((method, url, body))

    def get(self, url, headers=None):
        return self.issues


class SlackTest(unittest.TestCase):
    def test_only_verdicts_other_than_normal(self):
        r = Recorder()
        s = sinks.SlackSink("https://hooks.example/x", post=r.post)
        s.deliver(answer(verdict="NORMAL"))
        self.assertEqual(r.posts, [])
        s.deliver(answer())
        self.assertEqual(len(r.posts), 1)
        self.assertTrue(r.posts[0][2]["text"].startswith("Run 3fa9c2e1 (LOAD): REGRESSED"))

    def test_kates_text_cannot_mention_or_link(self):
        r = Recorder()
        sinks.SlackSink("https://hooks.example/x", post=r.post).deliver(
            answer(headline="<!channel> see <https://evil.example|here> & more"))
        text = r.posts[0][2]["text"]
        self.assertNotIn("<", text)
        self.assertIn("&lt;!channel&gt;", text)
        self.assertIn("&amp; more", text)

    def test_notice_is_one_line(self):
        r = Recorder()
        sinks.SlackSink("https://hooks.example/x", post=r.post).notice("3fa9c2e1", "SKIPPED_BUDGET", "20 an hour")
        self.assertEqual(r.posts[0][2], {"text": "Run analyst, run 3fa9c2e1: SKIPPED_BUDGET: 20 an hour"})


class GitHubTest(unittest.TestCase):
    def test_creates_one_issue_then_updates_it(self):
        r = Recorder()
        gh = sinks.GitHubSink("o/r", "t", post=r.post, get=r.get, api="https://gh")
        gh.deliver(answer())
        method, url, body = r.posts[0]
        self.assertEqual((method, url), ("POST", "https://gh/repos/o/r/issues"))
        self.assertEqual(body["title"], "[run-analyst] 3fa9c2e1: REGRESSED")
        self.assertEqual(body["labels"], ["run-analyst"])

        r = Recorder(issues=[{"number": 7, "title": "[run-analyst] 3fa9c2e1: REGRESSED"},
                             {"number": 8, "title": "[run-analyst] 3fa9c2e10: REGRESSED"}])
        sinks.GitHubSink("o/r", "t", post=r.post, get=r.get, api="https://gh").deliver(answer(verdict="NORMAL"))
        self.assertEqual(r.posts, [])
        sinks.GitHubSink("o/r", "t", post=r.post, get=r.get, api="https://gh").deliver(answer())
        self.assertEqual(r.posts[0][:2], ("PATCH", "https://gh/repos/o/r/issues/7"))

    def test_body_cannot_ping_or_break_the_fence(self):
        r = Recorder()
        sinks.GitHubSink("o/r", "t", post=r.post, get=r.get).deliver(answer(headline="@admins ```done```"))
        body = r.posts[0][2]["body"]
        self.assertNotIn("@admins", body)
        self.assertEqual(body.count("```"), 2)


class FanOutTest(unittest.TestCase):
    def test_file_sink_and_a_failing_sink(self):
        class Broken:
            def deliver(self, a):
                raise OSError("down")

        with tempfile.TemporaryDirectory() as d:
            fan = sinks.Sinks([Broken(), sinks.FileSink(Path(d))])
            with self.assertLogs("run-analyst", "ERROR"):
                fan.deliver(answer())
            self.assertEqual(json.loads((Path(d) / "3fa9c2e1.json").read_text())["verdict"], "REGRESSED")


if __name__ == "__main__":
    unittest.main()

"""Tests of tasks.json and of the rules every half of the harness shares."""

from __future__ import annotations

import copy
import os
import sys
import unittest
from collections import Counter

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))

import oracle  # noqa: E402
import taskfile  # noqa: E402

HERE = os.path.dirname(os.path.abspath(__file__))


def load_tasks() -> dict:
    return taskfile.load(os.path.join(HERE, "tasks.json"))


class TheTaskFile(unittest.TestCase):
    """The committed task list: valid, balanced, and frozen as the plan asks."""

    @classmethod
    def setUpClass(cls) -> None:
        cls.doc = load_tasks()
        cls.tasks = cls.doc["tasks"]

    def test_is_valid(self) -> None:
        self.assertEqual(taskfile.validate(self.doc, oracle.ORACLES), [])

    def test_about_twenty_tasks_in_the_brief_proportions(self) -> None:
        # plan §2.4: about 20 tasks; the brief: ~4 security, ~5 run
        # assessment, ~3 lag triage, ~4 game-day plans, ~3 debriefs, 1-2 facts.
        prefixes = Counter(t["id"].split("-")[0] for t in self.tasks)
        self.assertEqual(prefixes, Counter(sec=4, run=5, sre=3, gameday=4, debrief=3, fact=2))
        self.assertTrue(18 <= len(self.tasks) <= 22)

    def test_personas_are_the_v1_ones(self) -> None:
        personas = {t["persona"] for t in self.tasks}
        # The application developer is blocked on P-14 (plan §2.2).
        self.assertEqual(personas, {"security", "platform", "sre", "gameday"})

    def test_every_task_runs_in_both_agent_arms_at_least_three_times(self) -> None:
        for t in self.tasks:
            self.assertEqual(sorted(t["arms"]), ["cli", "mcp"], t["id"])
            self.assertGreaterEqual(t["trials"], 3, t["id"])

    def test_fixed_at_is_the_freeze_date(self) -> None:
        self.assertEqual(self.doc["fixed_at"], "2026-09-26")

    def test_one_run_assessment_task_says_the_test_cannot_answer(self) -> None:
        t = next(t for t in self.tasks if t["id"] == "run-load-parallel-producers")
        self.assertIn("load-single-producer", [c["id"] for c in t["required_caveats"]])
        self.assertIn("run_supports_sizing", [f["name"] for f in t["answer_fields"]])

    def test_only_the_hidden_report_task_forbids_tools(self) -> None:
        forbidding = [t["id"] for t in self.tasks if "forbid" in t]
        self.assertEqual(forbidding, ["debrief-diagnose-without-report"])
        t = self.tasks[-1]
        self.assertEqual(t["id"], "debrief-diagnose-without-report", "keep the task that waits out its fault last")
        self.assertEqual(set(t["forbid"]["mcp"]), {"disruption_report", "kates_activity"})
        self.assertIn("kates disruption", t["forbid"]["cli"])

    def test_tasks_whose_answer_is_a_disruption_read_its_report(self) -> None:
        for t in self.tasks:
            if t["oracle"]["fn"] in ("disruption_debrief", "disruption_compare", "hidden_fault_target"):
                self.assertTrue(any(s["kind"] == "api" and "templates" in s.get("path", "") for s in
                                    t["setup"] + [s for u in taskfile.uses(t) for s in
                                                  taskfile.tasks_by_id(self.doc)[u]["setup"]]), t["id"])

    def test_answer_field_names_are_unique_within_a_task(self) -> None:
        for t in self.tasks:
            names = [f["name"] for f in t["answer_fields"]]
            self.assertEqual(len(names), len(set(names)), t["id"])

    def test_every_oracle_function_is_used(self) -> None:
        used = {t["oracle"]["fn"] for t in self.tasks}
        self.assertEqual(used, set(oracle.ORACLES))

    def test_setup_never_passes_a_context_or_writes_outside_the_allowlist(self) -> None:
        for t in self.tasks:
            for s in t["setup"]:
                if s["kind"] == "kates":
                    self.assertNotIn("--context", s["args"])
                if s["kind"] == "api" and s["method"] == "POST":
                    self.assertTrue(s["path"].startswith("/api/disruptions"), t["id"])

    def test_topics_and_groups_carry_the_run_tag(self) -> None:
        # Names from an earlier evaluation run would leak into this one's
        # noise bands, lags and "since" windows.
        naming = {"--topic", "--consumer-group", "create-topic", "delete-topic", "produce", "topic"}
        for t in self.tasks:
            for s in t["setup"]:
                args = s.get("args", [])
                for before, a in zip(args, args[1:]):
                    if before in naming:
                        self.assertIn("{run_tag}", a, t["id"])


class Validation(unittest.TestCase):
    """validate() catches what a typo in tasks.json would silently break."""

    def setUp(self) -> None:
        self.doc = copy.deepcopy(load_tasks())
        self.by_id = taskfile.tasks_by_id(self.doc)

    def errors(self) -> list[str]:
        return taskfile.validate(self.doc, oracle.ORACLES)

    def assertError(self, fragment: str) -> None:
        errs = self.errors()
        self.assertTrue(any(fragment in e for e in errs), f"no error containing {fragment!r} in {errs}")

    def test_duplicate_ids(self) -> None:
        self.doc["tasks"][1]["id"] = self.doc["tasks"][0]["id"]
        self.assertError("is used twice")

    def test_unknown_persona(self) -> None:
        self.doc["tasks"][0]["persona"] = "dba"
        self.assertError("persona must be one of")

    def test_prompt_naming_a_tool(self) -> None:
        self.by_id["sec-posture"]["prompt"] = "Call security_evidence and tell me the grade."
        self.assertError("names the tool 'security_evidence'")

    def test_prompt_with_a_command(self) -> None:
        self.by_id["sec-posture"]["prompt"] = "Run kates security audit and tell me the grade."
        self.assertError("a kates command")

    def test_prompt_with_a_flag(self) -> None:
        self.by_id["sec-posture"]["prompt"] = "What grade does the audit give, with --plain?"
        self.assertError("a command flag")

    def test_prompt_mentioning_the_interface(self) -> None:
        self.by_id["sec-posture"]["prompt"] = "Using the MCP server, what is the grade?"
        self.assertError("(mcp)")

    def test_a_prompt_about_clients_is_not_a_cli_hint(self) -> None:
        self.by_id["sec-posture"]["prompt"] = "Can clients reach the cluster securely, and what is its grade?"
        self.assertEqual(self.errors(), [])

    def test_check_on_a_field_that_does_not_exist(self) -> None:
        self.by_id["sec-posture"]["checks"].append({"field": "score", "kind": "exact"})
        self.assertError("not an answer field")

    def test_field_without_a_check(self) -> None:
        self.by_id["sec-posture"]["checks"].pop()
        self.assertError("has no check")

    def test_number_check_needs_a_tolerance(self) -> None:
        self.by_id["run-vs-baseline"]["checks"][1] = {"field": "p99_change_percent", "kind": "number"}
        self.assertError("needs a tolerance")

    def test_set_check_needs_a_list_field(self) -> None:
        self.by_id["sec-posture"]["checks"][0] = {"field": "grade", "kind": "set"}
        self.assertError("kind set needs a string[] field")

    def test_placeholder_without_a_producer(self) -> None:
        self.by_id["sec-posture"]["prompt"] = "What changed since {baseline_saved_at}?"
        self.assertError("which no setup step captures")

    def test_setup_step_using_a_capture_before_it_exists(self) -> None:
        steps = self.by_id["run-vs-baseline"]["setup"]
        steps.insert(0, steps.pop(1))  # baseline set before the run it names
        self.assertError("before any step captures it")

    def test_use_of_an_unknown_task(self) -> None:
        self.by_id["run-noise-band"]["setup"] = [{"kind": "use", "task": "run-world"}]
        self.assertError("uses unknown task")

    def test_use_cycle(self) -> None:
        self.by_id["fact-min-isr"]["setup"].append({"kind": "use", "task": "fact-partition-leader"})
        self.assertError("uses itself")

    def test_setup_post_outside_the_allowlist(self) -> None:
        self.by_id["debrief-broker-kill"]["setup"][1]["path"] = "/api/tests"
        self.assertError("setup may POST only to")

    def test_setup_step_with_its_own_context(self) -> None:
        self.by_id["fact-min-isr"]["setup"][0]["args"] += ["--context", "prod"]
        self.assertError("do not pass --context")

    def test_capturing_step_without_json(self) -> None:
        self.by_id["run-vs-baseline"]["setup"][0]["args"] = ["test", "create", "--type", "LOAD"]
        self.assertError("must ask for -o json")

    def test_forbid_naming_an_unknown_tool(self) -> None:
        self.by_id["debrief-diagnose-without-report"]["forbid"]["mcp"].append("disruption_timeline")
        self.assertError("is not a kates mcp tool")

    def test_too_few_trials(self) -> None:
        self.by_id["sec-posture"]["trials"] = 2
        self.assertError("at least 3")

    def test_caveat_phrases_must_be_lower_case(self) -> None:
        self.by_id["sec-pentest-cve"]["required_caveats"][0]["any_of"].append("Attacks Nothing")
        self.assertError("lower case")

    def test_unknown_task_key(self) -> None:
        self.by_id["sec-posture"]["hint"] = "use the audit"
        self.assertError("unknown key 'hint'")

    def test_unknown_oracle_function(self) -> None:
        self.by_id["sec-posture"]["oracle"]["fn"] = "grade_it"
        self.assertError("is not in oracle.py")


class Placeholders(unittest.TestCase):
    def test_a_whole_placeholder_keeps_its_type(self) -> None:
        self.assertEqual(taskfile.render({"brokerId": "{b}"}, {"b": 3}), {"brokerId": 3})

    def test_text_substitution(self) -> None:
        self.assertEqual(taskfile.render("eval-{run_tag}-app", {"run_tag": "abc123"}), "eval-abc123-app")
        self.assertEqual(taskfile.render("{a}-{b}", {"a": "x", "b": 0}), "x-0")

    def test_lists_and_nesting(self) -> None:
        got = taskfile.render(["a", {"k": ["{x}"]}], {"x": True})
        self.assertEqual(got, ["a", {"k": [True]}])

    def test_braces_that_are_not_placeholders_stay(self) -> None:
        self.assertEqual(taskfile.render('{"a": {B}}', {}), '{"a": {B}}')

    def test_missing_capture(self) -> None:
        with self.assertRaises(taskfile.MissingCapture):
            taskfile.render("{nope}", {})
        self.assertEqual(taskfile.render("id {nope}", {}, missing="placeholder"), "id <nope>")

    def test_captures_for_merges_builtins_uses_and_own(self) -> None:
        by_id = {"a": {"id": "a", "setup": []}, "b": {"id": "b", "setup": [{"kind": "use", "task": "a"}]}}
        state = {"run_tag": "t1", "tasks": {"a": {"captures": {"x": 1, "y": 1}}, "b": {"captures": {"y": 2}}}}
        self.assertEqual(taskfile.captures_for(state, "b", by_id), {"run_tag": "t1", "x": 1, "y": 2})
        self.assertEqual(taskfile.captures_for(state, "a", by_id), {"run_tag": "t1", "x": 1, "y": 1})


class JsonPaths(unittest.TestCase):
    doc = {"id": "r1", "items": [{"id": 3, "role": "broker"}, {"id": 0, "role": "controller"},
                                 {"id": 1, "role": "controller"}], "ok": True}

    def test_keys_and_indexes(self) -> None:
        self.assertEqual(taskfile.json_path(self.doc, "$.id"), "r1")
        self.assertEqual(taskfile.json_path(self.doc, "$.items[0].id"), 3)
        self.assertEqual(taskfile.json_path(self.doc, "$.items[-1].id"), 1)
        self.assertEqual(taskfile.json_path(self.doc, "$"), self.doc)

    def test_filter(self) -> None:
        self.assertEqual(taskfile.json_path(self.doc, "$.items[role=controller][0].id"), 0)
        self.assertEqual(len(taskfile.json_path(self.doc, "$.items[role=controller]")), 2)
        self.assertEqual(taskfile.json_path({"a": [{"f": True}]}, "$.a[f=true][0].f"), True)

    def test_errors(self) -> None:
        for bad in ("id", "$.missing", "$.items[7]", "$.id[0]", "$.items[role=nobody][0]", "$..x"):
            with self.subTest(path=bad), self.assertRaises(taskfile.PathError):
                taskfile.json_path(self.doc, bad)


class Forbid(unittest.TestCase):
    prefixes = ["kates disruption", "kates audit", "kubectl", "curl"]

    def hits(self, command: str) -> list[str]:
        return taskfile.forbidden_hits(command, self.prefixes)

    def test_plain_commands(self) -> None:
        self.assertEqual(self.hits("kates disruption status d1d1d1d1 --context lab -o json"), ["kates disruption"])
        self.assertEqual(self.hits("kates kafka topic eval-diag -o json --context lab"), [])

    def test_global_flags_before_the_subcommand(self) -> None:
        self.assertEqual(self.hits("kates --context lab -o json disruption list"), ["kates disruption"])
        self.assertEqual(self.hits("kates --context=lab --plain audit"), ["kates audit"])

    def test_pipelines_lists_and_subshells(self) -> None:
        cmd = "cd /tmp && KATES_CONTEXT=lab kates kafka topic x -o json | jq . ; $(kates disruption list -o json)"
        self.assertEqual(self.hits(cmd), ["kates disruption"])
        self.assertEqual(self.hits("echo ok || curl -s http://localhost:8080/api/audit"), ["curl"])

    def test_paths_and_wrappers(self) -> None:
        self.assertEqual(self.hits("/usr/local/bin/kates disruption list"), ["kates disruption"])
        self.assertEqual(self.hits("env FOO=1 kubectl get pods"), ["kubectl"])

    def test_a_mention_is_not_a_use(self) -> None:
        self.assertEqual(self.hits("echo 'kates disruption list is forbidden'"), [])
        self.assertEqual(self.hits("grep kubectl notes.txt"), [])


if __name__ == "__main__":
    unittest.main()

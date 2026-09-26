"""Tests for arms.py and run.py: each arm's command line and config, the
schedule, the dry run, and a whole run against fake kates and claude
binaries and a fake Kates API on 127.0.0.1."""

import contextlib
import copy
import io
import json
import os
import shutil
import subprocess
import tempfile
import unittest
from pathlib import Path
from unittest import mock

import arms
import grade
import kates_commands
import report
import run
import tasks
from evalfixtures import CLUSTER_ID, HUMAN_KEY, ORACLE, TASKS, TESTDATA, FakeKatesAPI, make_wrapper

SKILL = "---\nname: kates-cli\n---\n\n# Kates from the shell\nUse --context lab.\n"


def settings(**kw) -> arms.ArmSettings:
    base = dict(claude_bin="/opt/claude", kates_bin="/opt/kates", model="claude-opus-5-5", max_turns=30,
                protocol_negotiation="legacy", sdk_generation="v2", trial_context="lab", cluster_id=CLUSTER_ID,
                skill_text=SKILL, jq_bin="/opt/jq")
    base.update(kw)
    return arms.ArmSettings(**base)


def fixture_task(tid: str) -> dict:
    return copy.deepcopy(tasks.task_by_id(tasks.load_tasks(TASKS))[tid])


PARENT_ENV = {"ANTHROPIC_API_KEY": "sk-secret", "KATES_API_KEY": "human-key", "KATES_URL": "http://x",
              "KUBECONFIG": "/home/me/.kube/config", "AWS_SECRET_ACCESS_KEY": "aws", "LANG": "en_US.UTF-8",
              "PATH": "/usr/local/bin:/usr/bin"}


class ArmPlanTest(unittest.TestCase):
    sandbox = arms.Sandbox(Path("/SANDBOX"))

    def plan(self, arm: str, tid: str = "run-noise", **kw) -> arms.TrialPlan:
        return arms.plan_trial(arm, fixture_task(tid), settings(**kw), self.sandbox, "What now?", PARENT_ENV)

    def test_mcp_arm(self):
        p = self.plan("mcp")
        argv = p.argv
        self.assertEqual(argv[:3], ["/opt/claude", "-p", "What now?"])
        for flag in ("--output-format", "--verbose", "--strict-mcp-config"):
            self.assertIn(flag, argv)
        # The session is kept in the trial's HOME for the answer turn.
        self.assertNotIn("--no-session-persistence", argv)
        self.assertNotIn("--resume", argv)
        self.assertEqual(argv[argv.index("--output-format") + 1], "stream-json")
        self.assertEqual(argv[argv.index("--tools") + 1], "")
        self.assertEqual(argv[argv.index("--permission-mode") + 1], "dontAsk")
        self.assertEqual(argv[argv.index("--permission-prompts") + 1], "none")
        self.assertEqual(argv[argv.index("--max-turns") + 1], "30")
        self.assertEqual(argv[argv.index("--mcp-config") + 1], "/SANDBOX/mcp-config.json")
        self.assertNotIn("--append-system-prompt", argv)
        server = p.mcp_config["mcpServers"]["kates"]
        self.assertEqual(server["command"], "/opt/kates")
        self.assertEqual(server["args"][:5], ["mcp", "--context", "lab", "--allow-cluster", CLUSTER_ID])
        self.assertTrue(server["alwaysLoad"])
        self.assertNotIn("env", server)
        self.assertEqual(p.settings["permissions"]["allow"], ["mcp__kates__*"])
        self.assertEqual(p.settings["permissions"]["deny"],
                         ["mcp__kates__disruption_report", "ListMcpResourcesTool", "ReadMcpResourceTool"])
        self.assertEqual(p.forbid_mcp_tools, ["disruption_report"])
        self.assertEqual(p.forbid_kates_paths, [])

    def test_cli_arm(self):
        p = self.plan("cli")
        argv = p.argv
        self.assertIn("--strict-mcp-config", argv)
        self.assertNotIn("--mcp-config", argv)
        self.assertIsNone(p.mcp_config)
        self.assertEqual(argv[argv.index("--tools") + 1], "Bash,Read,Write,Edit")
        skill = argv[argv.index("--append-system-prompt") + 1]
        self.assertTrue(skill.startswith(arms.SKILL_HEADER))
        self.assertTrue(skill.endswith(SKILL))
        allow = p.settings["permissions"]["allow"]
        self.assertEqual(allow[:2], ["Bash(kates *)", "Bash(jq *)"])
        self.assertIn("Write(//SANDBOX/work/**)", allow)
        deny = p.settings["permissions"]["deny"]
        self.assertIn("Bash(kates disruption status *)", deny)
        self.assertIn("Bash(kates test delete *)", deny)
        self.assertNotIn("mcp__kates__disruption_report", deny)
        self.assertEqual(p.forbid_kates_paths, [("disruption", "status")])
        self.assertIn("Bash(kates mcp *)", deny)
        self.assertEqual(p.links, {})
        wrapper = p.files[Path("/SANDBOX/bin/kates")]
        self.assertIn("os.execve('/opt/kates'", wrapper)
        self.assertIn("[['disruption', 'status']]", wrapper)
        self.assertIn("/SANDBOX/lib/kates_commands.py", [str(f) for f in p.files])


    def test_same_prompt_model_and_limits_in_both_arms(self):
        a, b = self.plan("mcp", max_budget_usd=2.5), self.plan("cli", max_budget_usd=2.5)
        for flag in ("-p", "--model", "--max-turns", "--permission-mode", "--max-budget-usd"):
            self.assertEqual(a.argv[a.argv.index(flag) + 1], b.argv[b.argv.index(flag) + 1], flag)
        self.assertEqual({k: v for k, v in a.env.items()}, {k: v for k, v in b.env.items()})

    def test_project_skill_mode(self):
        p = self.plan("cli", skill_mode="project")
        self.assertNotIn("--append-system-prompt", p.argv)
        self.assertEqual(p.argv[p.argv.index("--tools") + 1], "Bash,Read,Write,Edit,Skill")
        self.assertEqual(p.files[Path("/SANDBOX/work/.claude/skills/kates-cli/SKILL.md")], SKILL)

    def test_mcp_resources_option(self):
        p = self.plan("mcp", mcp_resources=True)
        self.assertEqual(p.argv[p.argv.index("--tools") + 1], "ListMcpResourcesTool,ReadMcpResourceTool")
        self.assertIn("ReadMcpResourceTool", p.settings["permissions"]["allow"])
        self.assertNotIn("ReadMcpResourceTool", p.settings["permissions"]["deny"])

    def test_environment_is_an_allowlist(self):
        env = self.plan("cli").env
        self.assertEqual(env["HOME"], "/SANDBOX/home")
        self.assertEqual(env["PATH"], "/SANDBOX/bin:" + arms.SYSTEM_PATH)
        self.assertEqual(env["MCP_PROTOCOL_NEGOTIATION"], "legacy")
        self.assertEqual(env["MCP_SDK_GENERATION"], "v2")
        self.assertEqual(env["ANTHROPIC_API_KEY"], "sk-secret")
        for leaked in ("KATES_API_KEY", "KATES_URL", "KUBECONFIG", "AWS_SECRET_ACCESS_KEY"):
            self.assertNotIn(leaked, env)
        self.assertEqual(arms.redact_env(env)["ANTHROPIC_API_KEY"], "<redacted>")

    def test_render_and_record_hide_secrets_and_the_skill(self):
        p = self.plan("cli")
        line = arms.render_command(p, "What now?")
        self.assertIn("env -i", line)
        self.assertIn('"$SKILL_PROMPT"', line)
        self.assertNotIn("sk-secret", line)
        recorded = arms.recorded_argv(p.argv, "What now?", "abc")
        self.assertIn("<prompt.txt>", recorded)
        self.assertIn("<SKILL.md sha256:abc>", recorded)

    def test_forbid_per_arm(self):
        task = fixture_task("run-noise")
        task["forbid"] = {"mcp": ["kates_activity"], "cli": ["kates audit", "kubectl"]}
        self.assertEqual(arms.forbid_for(task, "mcp"), (["mcp__kates__kates_activity"], ["kates_activity"], [], []))
        rules, tools, paths, prefixes = arms.forbid_for(task, "cli")
        self.assertEqual(rules, ["Bash(kates audit)", "Bash(kates audit *)", "Bash(kubectl)", "Bash(kubectl *)"])
        self.assertEqual((tools, paths, prefixes), ([], [("audit",)], ["kubectl"]))

    def test_forbid_rules_reach_the_settings(self):
        p = self.plan("mcp")
        self.assertIn("mcp__kates__disruption_report", p.settings["permissions"]["deny"])
        p = self.plan("cli")
        deny = p.settings["permissions"]["deny"]
        self.assertIn("Bash(kates disruption status *)", deny)
        self.assertIn("Bash(kubectl *)", deny)
        self.assertEqual(p.forbid_prefixes, ["kubectl"])


class WrapperTest(unittest.TestCase):
    """The CLI arm's kates and jq wrappers, run for real against stand-ins."""

    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        root = Path(self.tmp.name)
        self.real = make_wrapper(root / "real", "kates", TESTDATA / "echo_args.py")
        (TESTDATA / "echo_args.py").exists() or self.skipTest("no echo_args.py")
        s = settings(kates_bin=str(self.real), jq_bin=shutil.which("jq"))
        sandbox = arms.Sandbox(root / "sb")
        files = arms.cli_wrappers(s, sandbox, [("disruption",)])
        for path, content in files.items():
            path.parent.mkdir(parents=True, exist_ok=True)
            path.write_text(content)
            if content.startswith("#!"):
                path.chmod(0o755)
        self.bin = sandbox.bin
        self.env = {"PATH": f"{sandbox.bin}:/usr/bin:/bin", "HOME": str(root), "ANTHROPIC_API_KEY": "sk-secret"}

    def tearDown(self):
        self.tmp.cleanup()

    def run_tool(self, argv, stdin=""):
        p = subprocess.run(argv, input=stdin, capture_output=True, text=True, env=self.env,
                           cwd=self.tmp.name, timeout=30)
        return p.returncode, p.stdout, p.stderr

    def test_kates_runs_reads_and_refuses_changes(self):
        code, out, _ = self.run_tool([self.bin / "kates", "--context", "lab", "test", "list", "-o", "json"])
        self.assertEqual(code, 0)
        seen = json.loads(out)
        self.assertEqual(seen["args"], ["--context", "lab", "test", "list", "-o", "json"])
        self.assertNotIn("ANTHROPIC_API_KEY", seen["env_keys"])
        for argv, fragment in ((["--context", "lab", "test", "create", "--type", "LOAD"], "change the lab"),
                               (["disruption", "playbook", "run", "x"], "not available"),
                               (["cx", "list"], None),
                               (["mcp", "--context", "lab"], "not available"),
                               (["top"], "no terminal")):
            with self.subTest(argv=argv):
                code, out, err = self.run_tool([self.bin / "kates", *argv])
                if fragment is None:
                    self.assertEqual(code, 0, err)
                    continue
                self.assertEqual(code, 126)
                self.assertIn(kates_commands.REFUSED_MARKER, err)
                self.assertIn(fragment, err)
                self.assertEqual(out, "")
        code, _, _ = self.run_tool([self.bin / "kates", "test", "create", "--type", "LOAD", "--dry-run"])
        self.assertEqual(code, 0)

    @unittest.skipUnless(shutil.which("jq"), "needs jq")
    def test_jq_reads_only_standard_input(self):
        code, out, _ = self.run_tool([self.bin / "jq", "-r", ".a"], stdin='{"a": "x"}')
        self.assertEqual((code, out), (0, "x\n"))
        code, out, _ = self.run_tool([self.bin / "jq", "-n", "--arg", "v", "1", "$v"])
        self.assertEqual((code, out), (0, '"1"\n'))
        code, out, _ = self.run_tool([self.bin / "jq", "-n", "env | has(\"ANTHROPIC_API_KEY\")"])
        self.assertEqual((code, out), (0, "false\n"))
        for argv in ([".", "/etc/hosts"], ["--rawfile", "x", "/etc/hosts", "-n", "$x"], ["-rf", "prog.jq"],
                     ["-n", 'import "x" as $x; $x'], ["-L", "/tmp", "."]):
            with self.subTest(argv=argv):
                code, _, err = self.run_tool([self.bin / "jq", *argv], stdin="{}")
                self.assertEqual(code, 126)
                self.assertIn(kates_commands.REFUSED_MARKER, err)


class CheckInitTest(unittest.TestCase):
    MCP_INIT = {"tools": ["mcp__kates__cluster_overview", "mcp__kates__get_run"],
                "mcp_servers": [{"name": "kates", "status": "connected"}],
                "permissionMode": "dontAsk", "model": "claude-opus-5-5"}
    CLI_INIT = {"tools": ["Bash", "Read", "Write", "Edit"], "mcp_servers": [], "permissionMode": "dontAsk",
                "model": "claude-opus-5-5"}

    def check(self, init, arm, **kw):
        return arms.check_init(init, arm, settings(**kw))

    def test_good_sessions(self):
        self.assertEqual(self.check(self.MCP_INIT, "mcp"), ([], []))
        self.assertEqual(self.check(self.CLI_INIT, "cli"), ([], []))

    def test_problems(self):
        cases = [
            ("mcp", {**self.MCP_INIT, "tools": self.MCP_INIT["tools"] + ["Bash"]}, "shell, file or web"),
            ("mcp", {**self.MCP_INIT, "mcp_servers": [{"name": "kates", "status": "failed"}], "tools": []},
             "'failed'"),
            ("mcp", {**self.MCP_INIT, "tools": self.MCP_INIT["tools"] + ["mcp__github__x"]}, "other MCP servers"),
            ("cli", {**self.CLI_INIT, "mcp_servers": [{"name": "kates", "status": "connected"}]}, "MCP servers"),
            ("cli", {**self.CLI_INIT, "tools": ["Bash", "WebFetch"]}, "WebFetch"),
            ("cli", {**self.CLI_INIT, "permissionMode": "default"}, "permission mode"),
            ("cli", {**self.CLI_INIT, "model": "claude-sonnet-5"}, "not the requested"),
        ]
        for arm, init, fragment in cases:
            with self.subTest(fragment=fragment):
                problems, _ = self.check(init, arm)
                self.assertTrue(any(fragment in p for p in problems), problems)
        self.assertTrue(self.check(None, "cli")[0])

    def test_harmless_extra_tools_are_notes(self):
        problems, notes = self.check({**self.CLI_INIT, "tools": self.CLI_INIT["tools"] + ["TodoWrite"]}, "cli")
        self.assertEqual(problems, [])
        self.assertIn("TodoWrite", notes[0])

    def test_model_alias_is_not_compared(self):
        self.assertEqual(self.check({**self.CLI_INIT, "model": "claude-x-9"}, "cli", model="opus")[0], [])


class ScheduleTest(unittest.TestCase):
    doc = tasks.load_tasks(TASKS)

    def test_each_round_holds_every_pair_once(self):
        schedule = run.build_schedule(self.doc, ["mcp", "cli"], 7)
        self.assertEqual(len(schedule), 12)
        for r in (1, 2, 3):
            pairs = sorted((t, a) for rr, t, a in schedule if rr == r)
            self.assertEqual(pairs, [("run-noise", "cli"), ("run-noise", "mcp"),
                                     ("sec-posture", "cli"), ("sec-posture", "mcp")])

    def test_seeded(self):
        a = run.build_schedule(self.doc, ["mcp", "cli"], 7)
        self.assertEqual(a, run.build_schedule(self.doc, ["mcp", "cli"], 7))
        orders = {tuple(run.build_schedule(self.doc, ["mcp", "cli"], s)) for s in range(10)}
        self.assertGreater(len(orders), 1)

    def test_limits(self):
        self.assertEqual(len(run.build_schedule(self.doc, ["mcp"], 1, max_trials=1)), 2)
        self.assertEqual(len(run.build_schedule(self.doc, ["mcp", "cli"], 1, only=["run-noise"])), 6)


def call(argv: list[str]) -> tuple[int, str, str]:
    out, err = io.StringIO(), io.StringIO()
    with contextlib.redirect_stdout(out), contextlib.redirect_stderr(err):
        code = run.main(argv)
    return code, out.getvalue(), err.getvalue()


class DryRunTest(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.runs = Path(self.tmp.name) / "runs"
        self.common = ["--run-id", "dry", "--runs-dir", str(self.runs), "--tasks", str(TASKS),
                       "--oracle", str(ORACLE), "--kates-bin", "kates-not-installed",
                       "--claude-bin", "claude-not-installed"]

    def tearDown(self):
        self.tmp.cleanup()

    def test_validate(self):
        code, out, _ = call(["validate", "--tasks", str(TASKS), "--oracle", str(ORACLE)])
        self.assertEqual(code, 0)
        self.assertIn("2 tasks, fixed at 2026-09-26, 12 agent trials", out)
        bad = Path(self.tmp.name) / "bad.json"
        bad.write_text(json.dumps({"version": 1, "fixed_at": "2026-09-26", "tasks": [{"id": "x"}]}))
        code, _, err = call(["validate", "--tasks", str(bad)])
        self.assertEqual(code, 2)
        self.assertIn("persona", err)

    def test_all_prints_everything_and_writes_nothing(self):
        code, out, err = call(["all", "--dry-run", *self.common, "--model", "claude-opus-5-5", "--seed", "3"])
        self.assertEqual(code, 0, err)
        self.assertFalse(self.runs.exists())
        self.assertIn("kates security baseline --save -o json --context human", out)
        self.assertIn("security_grade(baseline_ts='<baseline_ts>')", out)
        self.assertIn("# trials: 12 in 3 rounds, seed 3", out)
        self.assertEqual(out.count("=== round"), 12)
        self.assertIn("MCP_PROTOCOL_NEGOTIATION=legacy", out)
        self.assertIn("--allow-cluster", out)
        self.assertIn("'Bash(kates *)'".strip("'"), out)
        self.assertIn('"$SKILL_PROMPT"', out)
        self.assertIn("--tools ''", out)
        self.assertNotIn("KATES_API_KEY", out)
        self.assertIn("oracle-recheck.json", out)

    def test_trials_need_a_model_unless_dry(self):
        code, _, err = call(["trials", *self.common])
        self.assertEqual(code, 2)
        self.assertIn("--model is required", err)


class AnswerCostTest(unittest.TestCase):
    def test_a_resumed_session_reports_both_turns_together(self):
        self.assertAlmostEqual(run.answer_cost(0.40, 0.43), 0.03)
        self.assertEqual(run.answer_cost(0.40, 0.002), 0.002)
        self.assertIsNone(run.answer_cost(0.40, None))
        self.assertEqual(run.answer_cost(None, 0.01), 0.01)


class KatesReadingTest(unittest.TestCase):
    def test_flags_before_the_command_cannot_hide_it(self):
        for args in (["--type", "LOAD", "test", "create", "--records", "1"],
                     ["test", "--type", "LOAD", "create"],
                     ["--context", "lab", "t", "run", "--type", "LOAD"]):
            with self.subTest(args=args):
                self.assertIsNotNone(kates_commands.refusal(args))
                self.assertTrue(kates_commands.classify(args)["mutating"])
        self.assertIsNotNone(kates_commands.refusal(["--config", "p.json", "disruption", "run"], [("disruption",)]))
        self.assertTrue(kates_commands.starts_with(["--foo", "x", "disruption", "list"], ("disruption",)))

    def test_the_last_dry_run_wins(self):
        self.assertIsNotNone(kates_commands.refusal(["test", "create", "--dry-run", "--dry-run=false"]))
        self.assertIsNone(kates_commands.refusal(["test", "create", "--dry-run=false", "--dry-run"]))
        self.assertIsNone(kates_commands.refusal(["test", "create", "--dry-run=true"]))

    def test_reads_stay_allowed(self):
        for args in (["test", "list", "--type", "LOAD", "--status", "DONE", "-o", "json"],
                     ["--plain", "test", "create", "--dry-run"], ["security", "audit", "--context", "lab"],
                     ["security", "baseline"], ["migrate", "plan"]):
            with self.subTest(args=args):
                self.assertIsNone(kates_commands.refusal(args))


class RedactionTest(unittest.TestCase):
    def test_proxy_credentials_are_masked(self):
        env = arms.redact_env({"HTTPS_PROXY": "http://u:p@ss@proxy:3128", "HTTP_PROXY": "http://u:p/w@proxy",
                               "NO_PROXY": "localhost", "ANTHROPIC_AUTH_TOKEN": "tok"})
        self.assertEqual(env["HTTPS_PROXY"], "http://<redacted>@proxy:3128")
        self.assertEqual(env["HTTP_PROXY"], "http://<redacted>@proxy")
        self.assertEqual(env["ANTHROPIC_AUTH_TOKEN"], "<redacted>")
        self.assertIn("ANTHROPIC_AUTH_TOKEN", arms.child_env(settings(), arms.Sandbox(Path("/S")),
                                                               {"ANTHROPIC_AUTH_TOKEN": "tok"}))


class AgentReachTest(unittest.TestCase):
    def test_after_a_trial_the_agent_context_must_still_reach_the_cluster(self):
        import kates_api
        with FakeKatesAPI() as api:
            ctx = kates_api.KatesContext("agent", api.url, HUMAN_KEY)
            self.assertIsNone(run.agent_api_problem(ctx, CLUSTER_ID))
            self.assertIn("reached Kafka cluster fixture-cluster-1, not other",
                          run.agent_api_problem(ctx, "other"))
            url = api.url
        self.assertIn("did not answer", run.agent_api_problem(kates_api.KatesContext("agent", url, HUMAN_KEY),
                                                               CLUSTER_ID))

    def test_run_ids_stay_inside_the_runs_directory(self):
        code, _, err = call(["setup", "--run-id", "../escape", "--dry-run"])
        self.assertEqual(code, 2)
        self.assertIn("letters, digits", err)


class EndToEndTest(unittest.TestCase):
    """A whole run with fake binaries: setup, oracle, trials, recheck, grade,
    report. The fake claude replays recorded transcripts and reports on
    stderr what it was given."""

    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        root = Path(self.tmp.name)
        self.runs = root / "runs"
        self.kates = make_wrapper(root / "fakebin", "kates", TESTDATA / "fake_kates.py")
        self.claude = make_wrapper(root / "fakebin", "claude", TESTDATA / "fake_claude.py")
        self.api = FakeKatesAPI().__enter__()
        self.env = mock.patch.dict(os.environ, {"ANTHROPIC_API_KEY": "sk-test", "FAKE_KATES_URL": self.api.url,
                                                "KATES_API_KEY": "should-not-leak"})
        self.env.start()
        self.common = ["--run-id", "e2e", "--runs-dir", str(self.runs), "--tasks", str(TASKS),
                       "--oracle", str(ORACLE), "--kates-bin", str(self.kates)]
        self.agent = ["--claude-bin", str(self.claude), "--model", "claude-opus-5-5", "--seed", "7"]

    def tearDown(self):
        self.env.stop()
        self.api.__exit__()
        self.tmp.cleanup()

    def run_all(self, *extra: str) -> tuple[int, str, str]:
        return call(["all", *self.common, *self.agent, "--max-trials", "1", *extra])

    def test_full_run(self):
        code, out, err = self.run_all()
        self.assertEqual(code, 0, err)
        rd = self.runs / "e2e"
        state = json.loads((rd / "state.json").read_text())
        self.assertEqual(state["cluster_id"], CLUSTER_ID)
        self.assertEqual(state["tasks"]["sec-posture"]["captures"], {"baseline_ts": "2026-09-26T10:00:00Z"})
        self.assertEqual(state["tasks"]["run-noise"]["captures"], {"run_id": "a1b2c3d4"})
        setup_log = (rd / "setup" / "sec-posture" / "step-0.err").read_text()
        self.assertIn('"--context", "human"', setup_log)

        oracle = json.loads((rd / "oracle.json").read_text())["tasks"]
        self.assertEqual(oracle["sec-posture"]["expected"]["failing_checks"], 2)
        self.assertEqual(oracle["run-noise"]["expected"]["verdict"], "within")
        self.assertTrue((rd / "oracle-recheck.json").exists())
        # Only the human key ever reaches the API from the harness: as a
        # Bearer token (the clusterId pin) or an X-API-Key (the oracle).
        self.assertTrue(all(auth in (f"Bearer {HUMAN_KEY}", HUMAN_KEY) for _, auth in self.api.requests))

        manifest = json.loads((rd / "manifest.json").read_text())
        self.assertTrue(manifest["agent_key_is_human_key"])
        self.assertEqual(manifest["claude_version"], "2.1.278")
        self.assertEqual(manifest["protocol_negotiation"], "legacy")
        self.assertEqual(manifest["seed"], 7)
        self.assertIn("agent context holds the human's key", err)

        for tid in ("sec-posture", "run-noise"):
            for arm in ("mcp", "cli"):
                tdir = rd / tid / arm / "trial-1"
                m = json.loads((tdir / "metrics.json").read_text())
                self.assertTrue(m["valid"], (tid, arm, m["config_problems"]))
                self.assertEqual(m["config_problems"], [])
                self.assertEqual(m["env"]["MCP_PROTOCOL_NEGOTIATION"], "legacy")
                prompt = (tdir / "prompt.txt").read_text()
                self.assertNotIn("json", prompt)
                if m["status"] == "ok":
                    request = (tdir / "answer-request.txt").read_text()
                    self.assertTrue(request.endswith("write nothing after that block."))
                    first = json.loads((tdir / "transcript.jsonl").read_text().splitlines()[-1])
                    second = [json.loads(line) for line in (tdir / "answer.jsonl").read_text().splitlines()]
                    self.assertEqual(second[0]["session_id"], first["session_id"])
                    self.assertTrue(second[0]["denied_everything"])
                    self.assertEqual(m["answer_turn"]["status"], "ok")
                    self.assertTrue(m["answer_found"])
                config = json.loads((tdir / "config.json").read_text())
                self.assertEqual(config["env"]["ANTHROPIC_API_KEY"], "<redacted>")
                self.assertIn("<prompt.txt>", config["argv"])
                seen = json.loads((tdir / "stderr.log").read_text().splitlines()[0])
                self.assertFalse(any(k.startswith("KATES_") for k in seen["env_keys"]))
                self.assertFalse(Path(seen["home"]).exists(), "the sandbox must be removed")
                kates_cfg = json.loads(seen["kates_config"])
                self.assertEqual(list(kates_cfg["contexts"]), ["lab"])
                self.assertEqual(kates_cfg["contexts"]["lab"]["api-key"], HUMAN_KEY)
                self.assertEqual(kates_cfg["contexts"]["lab"]["url"], self.api.url)
                self.assertEqual(seen["kates_config_mode"], "0o600")
                self.assertTrue(seen["kates_on_path"].endswith("/bin/kates"))
                self.assertIn("baseline saved at 2026-09-26T10:00:00Z", prompt) if tid == "sec-posture" else \
                    self.assertIn("run a1b2c3d4 faster", prompt)
        noise_mcp = json.loads((rd / "run-noise" / "mcp" / "trial-1" / "metrics.json").read_text())
        self.assertEqual(noise_mcp["forbidden_used"], ["mcp__kates__disruption_report"])
        noise_cli = json.loads((rd / "run-noise" / "cli" / "trial-1" / "metrics.json").read_text())
        self.assertEqual(noise_cli["status"], "max_turns")
        self.assertEqual(noise_cli["mutating_calls"], ["kates --context lab test delete a1b2c3d4"])

        with contextlib.redirect_stdout(io.StringIO()), contextlib.redirect_stderr(io.StringIO()):
            self.assertEqual(grade.main(["--run-id", "e2e", "--runs-dir", str(self.runs)]), 0)
            self.assertEqual(report.main(["--run-id", "e2e", "--runs-dir", str(self.runs)]), 3)
        self.assertIn("STOP", (rd / "report.md").read_text())

        # Resuming runs nothing new; other settings are refused.
        code, out, _ = call(["trials", *self.common, *self.agent, "--max-trials", "1"])
        self.assertEqual(code, 0)
        self.assertNotIn("tool calls", out)
        code, _, err = call(["trials", *self.common, "--claude-bin", str(self.claude), "--model", "claude-other",
                             "--max-trials", "1"])
        self.assertEqual(code, 2)
        self.assertIn("model: run has 'claude-opus-5-5', now 'claude-other'", err)

    def test_a_changed_task_list_is_refused(self):
        self.assertEqual(self.run_all()[0], 0)
        other = Path(self.tmp.name) / "tasks2.json"
        doc = json.loads(TASKS.read_text())
        doc["tasks"][0]["notes"] = "edited"
        other.write_text(json.dumps(doc))
        code, _, err = call(["trials", "--run-id", "e2e", "--runs-dir", str(self.runs), "--tasks", str(other),
                             "--kates-bin", str(self.kates), *self.agent])
        self.assertEqual(code, 2)
        self.assertIn("froze a different task list", err)

    def test_preflight_before_setup_pins_the_cluster_itself(self):
        code, out, err = call(["preflight", *self.common, *self.agent])
        self.assertEqual(code, 0, out + err)
        self.assertNotIn("FAIL", out)
        state = json.loads((self.runs / "e2e" / "state.json").read_text())
        self.assertEqual(state["cluster_id"], CLUSTER_ID)
        self.assertEqual(state["tasks"], {})
        # Setup afterwards keeps the pin and the run_tag.
        code, _, err = call(["setup", *self.common])
        self.assertEqual(code, 0, err)
        after = json.loads((self.runs / "e2e" / "state.json").read_text())
        self.assertEqual((after["cluster_id"], after["run_tag"]), (CLUSTER_ID, state["run_tag"]))

    def test_preflight_refuses_a_cluster_other_than_allow_cluster(self):
        code, _, err = call(["preflight", *self.common, *self.agent, "--allow-cluster", "another-cluster"])
        self.assertEqual(code, 3)
        self.assertIn("--allow-cluster another-cluster but", err)
        # Once pinned, too.
        self.assertEqual(call(["preflight", *self.common, *self.agent])[0], 0)
        code, _, err = call(["preflight", *self.common, *self.agent, "--allow-cluster", "another-cluster"])
        self.assertEqual(code, 3)
        self.assertIn("but this run pinned Kafka cluster fixture-cluster-1", err)

    def test_an_environment_that_cannot_run_a_trial_writes_no_state(self):
        with mock.patch.dict(os.environ, {"ANTHROPIC_API_KEY": ""}):
            code, _, err = call(["preflight", *self.common, *self.agent])
        self.assertEqual(code, 3, err)
        self.assertFalse((self.runs / "e2e" / "state.json").exists())
        self.assertEqual(self.api.requests, [])

    def test_trials_before_setup_refuse_before_recording_settings(self):
        code, _, err = call(["trials", *self.common, *self.agent, "--max-trials", "1"])
        self.assertEqual(code, 2)
        self.assertIn("not set up yet: sec-posture, run-noise", err)
        # Nothing was recorded: a later run with other settings is not refused.
        self.assertFalse((self.runs / "e2e" / "manifest.json").exists())
        self.assertEqual(self.api.requests, [])

    def test_the_oracle_refuses_a_cluster_other_than_the_pinned_one(self):
        self.assertEqual(call(["setup", *self.common])[0], 0)
        state_path = self.runs / "e2e" / "state.json"
        state = json.loads(state_path.read_text())
        state["cluster_id"] = "the-cluster-setup-saw"
        state_path.write_text(json.dumps(state))
        code, _, err = call(["oracle", *self.common])
        self.assertEqual(code, 3)
        self.assertIn("this run pinned Kafka cluster the-cluster-setup-saw", err)
        self.assertFalse((self.runs / "e2e" / "oracle.json").exists())

    def test_a_dry_run_of_an_unpinned_preflight_names_the_pin(self):
        code, out, _ = call(["preflight", *self.common, *self.agent, "--dry-run"])
        self.assertEqual(code, 0)
        self.assertIn("# pin: read contexts 'human' (human) and 'agent' (agent)", out)
        self.assertFalse((self.runs / "e2e").exists())

    def test_preflight(self):
        code, out, err = call(["setup", *self.common])
        self.assertEqual(code, 0, err)
        code, out, err = call(["preflight", *self.common, *self.agent])
        self.assertEqual(code, 0, out + err)
        self.assertEqual(out.count("PASS"), 14)
        self.assertNotIn("FAIL", out)
        self.assertIn("PASS  cli: kubectl was refused", out)
        self.assertIn("PASS  cli: a kates call that would start load was refused", out)
        self.assertIn("PASS  cli: jq could not read a file named as an argument", out)
        self.assertIn("PASS  cli: jq could not read a file through a shell redirect", out)
        for arm in ("mcp", "cli"):
            self.assertIn(f"PASS  {arm}: the answer turn resumed the session and restated the answer as JSON", out)
        # The answer turn's own cost: the fake reports it alone, below the first turn's.
        self.assertIn("answer turn: 0.002 USD (reported 0.002)", out)

    def test_invalid_trials_timeouts_and_missing_credentials(self):
        doc = json.loads(TASKS.read_text())
        doc["tasks"][0]["prompt"] += " FAKE:crash"
        doc["tasks"][1]["prompt"] += " FAKE:sleep=30"
        # Long enough for the fake to start and report its session even on a
        # loaded machine (a timeout before the init event is the harness's,
        # not the agent's, and would make the trial invalid).
        doc["tasks"][1]["timeout_s"] = 4
        path = Path(self.tmp.name) / "tasks-bad.json"
        path.write_text(json.dumps(doc))
        common = [*self.common]
        common[common.index(str(TASKS))] = str(path)
        code, out, err = call(["all", *common, *self.agent, "--max-trials", "1", "--arms", "cli"])
        self.assertEqual(code, 5, out + err)
        rd = self.runs / "e2e"
        crashed = json.loads((rd / "sec-posture" / "cli" / "trial-1" / "metrics.json").read_text())
        self.assertEqual((crashed["status"], crashed["valid"], crashed["exit_code"]), ("no_result", False, 1))
        slow = json.loads((rd / "run-noise" / "cli" / "trial-1" / "metrics.json").read_text())
        self.assertEqual((slow["status"], slow["valid"]), ("timeout", True))
        self.assertLess(slow["wall_clock_s"], 20)

        # --redo-invalid keeps the invalid trial aside and runs it again;
        # the valid (timed-out) one stays as it is.
        code, out, _ = call(["trials", *common, *self.agent, "--max-trials", "1", "--arms", "cli", "--redo-invalid"])
        self.assertEqual(code, 5)
        self.assertTrue((rd / "sec-posture" / "cli" / "trial-1.invalid-1" / "metrics.json").exists())
        self.assertTrue((rd / "sec-posture" / "cli" / "trial-1" / "metrics.json").exists())
        self.assertFalse((rd / "run-noise" / "cli" / "trial-1.invalid-1").exists())
        self.assertEqual(out.count("tool calls"), 1)
        self.assertEqual(len(grade.trial_dirs(rd)), 2)

        with mock.patch.dict(os.environ, {"ANTHROPIC_API_KEY": ""}):
            code, _, err = call(["trials", *common, *self.agent])
        self.assertEqual(code, 3)
        self.assertIn("ANTHROPIC_API_KEY", err)

    def test_setup_and_oracle_failures(self):
        doc = json.loads(TASKS.read_text())
        doc["tasks"][0]["setup"][0]["args"] = ["security", "nonsense", "-o", "json"]
        path = Path(self.tmp.name) / "tasks-setup.json"
        path.write_text(json.dumps(doc))
        code, _, err = call(["setup", "--run-id", "s", "--runs-dir", str(self.runs), "--tasks", str(path),
                             "--oracle", str(ORACLE), "--kates-bin", str(self.kates)])
        self.assertEqual(code, 4)
        self.assertIn("kates security nonsense -o json exited 1", err)
        state = json.loads((self.runs / "s" / "state.json").read_text())
        self.assertEqual(state["tasks"]["sec-posture"]["status"], "failed")

        doc = json.loads(TASKS.read_text())
        doc["tasks"][0]["oracle"] = {"fn": "broken"}
        path.write_text(json.dumps(doc))
        base = ["--run-id", "o", "--runs-dir", str(self.runs), "--tasks", str(path), "--oracle", str(ORACLE),
                "--kates-bin", str(self.kates)]
        self.assertEqual(call(["setup", *base])[0], 0)
        code, _, err = call(["oracle", *base])
        self.assertEqual(code, 4)
        self.assertIn("grade: 7 is not a string", err)
        entry = json.loads((self.runs / "o" / "oracle.json").read_text())["tasks"]["sec-posture"]
        self.assertNotIn("expected", entry)
        # Its task is void: no trials are spent on it.
        code, _, err = call(["trials", *base, *self.agent, "--max-trials", "1"])
        self.assertEqual(code, 0, err)
        self.assertIn("no trials for void tasks", err)
        self.assertFalse((self.runs / "o" / "sec-posture").exists())
        self.assertTrue((self.runs / "o" / "run-noise" / "mcp" / "trial-1" / "metrics.json").exists())

    def test_missing_agent_context(self):
        code, _, err = call(["setup", *self.common, "--agent-context", "nobody"])
        self.assertEqual(code, 3)
        self.assertIn("context 'nobody' is not in the kates config", err)

    def test_expert_template(self):
        self.assertEqual(call(["setup", *self.common])[0], 0)
        code, out, _ = call(["expert-template", *self.common])
        self.assertEqual(code, 0)
        rd = self.runs / "e2e"
        rows = (rd / "expert-template.csv").read_text().splitlines()
        self.assertEqual(rows[0], ",".join(grade.EXPERT_COLUMNS))
        self.assertEqual(len(rows), 3)
        self.assertIn("baseline saved at 2026-09-26T10:00:00Z", (rd / "expert-prompts.md").read_text())


if __name__ == "__main__":
    unittest.main()

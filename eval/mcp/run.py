"""Run the MCP evaluation on a Kind lab: setup, oracle, and the two agent arms.

This is the part of the harness that touches the lab and spends money, so it
is built to be predictable and resumable (plans/mcp-server.md §2.4, §8.4):

  validate          check the task list; touches nothing
  setup             read the human and agent contexts, pin the Kafka
                    clusterId, and prepare the lab for each task as the human
                    (setup.py); the run_tag and captures go to state.json
  oracle            compute each task's expected answer from the Kates API as
                    the human (oracle.py) into oracle.json; with --recheck,
                    compute them again after the trials into
                    oracle-recheck.json: a task whose answer moved while its
                    trials ran is void, and the report leaves it out
  preflight         one short real session per arm, checking that Claude Code
                    started it the way the arm needs (tools, MCP server,
                    permission mode, the answer turn) before paying for the
                    full run; it needs nothing from setup, and pins the
                    clusterId itself when setup has not run yet
  trials            every task x arm x trial as `claude -p`, in a random
                    order per round (seeded), skipping trials whose
                    metrics.json exists (--redo-invalid moves invalid ones
                    aside and runs them again)
  all               setup, oracle, trials, oracle --recheck
  expert-template   the CSV and the prompts for the expert arm

Everything a run produces goes under eval/mcp/runs/<run-id>/, which git
ignores. The first phase freezes the task list into the run (tasks.json) and
every later phase reads that copy, so an edit to eval/mcp/tasks.json cannot
change a run halfway. The run's settings (model, turns, protocol era, skill,
seed, kates binary) are recorded in manifest.json on the first trial, and a
later invocation with different settings is refused: start a new run id.

--dry-run touches nothing: no kates, no claude, no network, no files. It
checks the task list and the oracle functions, then prints every command and
config file a real run would use, with the trial's temporary directory shown
as /SANDBOX and the skill as "$SKILL_PROMPT".

Exit codes:
  0  done (or the dry run printed everything)
  2  bad input: the task list, the flags, or settings that differ from the run's
  3  the environment is not ready: a binary, a context or credentials missing
  4  a setup step failed, or an oracle function did (its task is void; the
     other tasks still run)
  5  finished, but some trials are invalid (the harness or the service
     failed, not the agent); run the same command with --redo-invalid
  6  preflight found a problem
  130 interrupted; run the same command again to resume
"""

from __future__ import annotations

import argparse
import csv
import dataclasses
import datetime as dt
import importlib.util
import json
import os
import random
import re
import shutil
import signal
import subprocess
import sys
import tempfile
import time
import types
from pathlib import Path
from typing import Any

import arms as armlib
import kates_api
import metrics
import oracle as oraclelib
import setup as setuplib
import taskfile
import tasks as tasklib

HERE = Path(__file__).resolve().parent
REPO = HERE.parent.parent
DEFAULT_TASKS = HERE / "tasks.json"
DEFAULT_ORACLE = HERE / "oracle.py"
DEFAULT_RUNS = HERE / "runs"
DEFAULT_SKILL = REPO / "skills" / "kates-cli" / "SKILL.md"
DRY_SANDBOX = Path("/SANDBOX")
RUN_ID_RE = re.compile(r"[A-Za-z0-9][A-Za-z0-9._-]{0,99}")

# Settings every trial of a run must share. A later invocation that differs
# in any of them would mix two experiments in one report.
MANIFEST_KEYS = (
    "model", "max_turns", "max_budget_usd", "protocol_negotiation", "sdk_generation",
    "skill_mode", "skill_sha256", "trial_context", "mcp_resources", "seed", "kates_sha256", "cluster_id",
)

PREFLIGHT_PROMPTS = {
    "mcp": "Call the kates tool cluster_overview once. Then reply with only the Kafka clusterId it reports.",
    "cli": ("Run `kates cluster info -o json` once and read the clusterId from it. Then try each of these once: "
            "`kubectl version --client`, `kates --context lab test create --type LOAD --records 1`, "
            "`jq . /etc/hosts` and `jq . < /etc/hosts`. Reply with the clusterId, and say for each of the four "
            "whether it ran."),
}


class Fail(Exception):
    """Stop the phase with an exit code and a message."""

    def __init__(self, code: int, message: str):
        self.code = code
        super().__init__(message)


def now() -> str:
    return dt.datetime.now(dt.timezone.utc).isoformat(timespec="seconds")


def read_json(path: Path, default: Any = None) -> Any:
    return json.loads(path.read_text()) if path.exists() else default


def write_json(path: Path, data: Any) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    tmp = path.with_suffix(path.suffix + ".tmp")
    tmp.write_text(json.dumps(data, indent=2, sort_keys=False, default=str) + "\n")
    tmp.replace(path)


def say(msg: str = "") -> None:
    print(msg, flush=True)


def warn(msg: str) -> None:
    print(f"warning: {msg}", file=sys.stderr, flush=True)


# ---------------------------------------------------------------------------
# The run directory


def run_started(run_dir: Path) -> bool:
    """Whether a run has used its task list: a task set up, an expected
    answer computed, or a trial scheduled. A preflight or a pin alone uses
    no task."""
    state = read_json(run_dir / "state.json", {}) or {}
    return bool(state.get("tasks")) or (run_dir / "oracle.json").exists() or \
        (run_dir / "schedule.json").exists() or any(run_dir.glob("*/*/trial-*/metrics.json"))


class Run:
    def __init__(self, args: argparse.Namespace):
        self.args = args
        self.dir: Path = args.runs_dir / args.run_id
        frozen = self.dir / "tasks.json"
        source = args.tasks or DEFAULT_TASKS
        self.oracle = load_oracle(args.oracle)
        self.refreeze = False
        try:
            if frozen.exists() and not args.tasks and Path(source).exists() and \
                    tasklib.file_sha256(source) != tasklib.file_sha256(frozen):
                # tasks.json changed since this run froze it. A run that has
                # used its list keeps it (its answers belong to it); one that
                # has only frozen it takes the current list.
                if run_started(self.dir):
                    warn(f"{self.dir} keeps the task list it froze, not {source} as it is now; "
                         "start a new --run-id to use the current list")
                else:
                    say(f"the task list changed since {self.dir} froze it, and nothing in the run has used it: "
                        f"using {source} as it is now")
                    self.refreeze = True
            if frozen.exists() and not self.refreeze:
                if args.tasks and tasklib.file_sha256(args.tasks) != tasklib.file_sha256(frozen):
                    raise Fail(2, f"{self.dir} froze a different task list than {args.tasks}; "
                                  "start a new --run-id to use the new list")
                self.doc = tasklib.load_tasks(frozen, self.oracle.ORACLES)
                self.tasks_path = frozen
            else:
                self.doc = tasklib.load_tasks(source, self.oracle.ORACLES)
                self.tasks_path = Path(source)
        except tasklib.TaskError as e:
            raise Fail(2, "the task list is invalid:\n  " + "\n  ".join(e.problems)) from e
        self.by_id = tasklib.task_by_id(self.doc)
        self.manifest: dict[str, Any] = read_json(self.dir / "manifest.json", {})
        # setup.py's state: the run's run_tag, the human context, and each
        # task's captures. A new run gets a new run_tag, so its topics and
        # specs never mix with an earlier run's.
        self.state: dict[str, Any] = read_json(self.dir / "state.json", None) or setuplib.new_state(
            args.human_context, None, args.kates_bin)
        if self.state.get("context") != args.human_context:
            raise Fail(2, f"{self.dir} was started with the human context {self.state.get('context')!r}, "
                          f"not {args.human_context!r}")
        unknown = [t for t in args.only_task or [] if t not in self.by_id]
        if unknown:
            raise Fail(2, f"--only-task names tasks the list does not have: {', '.join(unknown)}")

    @property
    def dry(self) -> bool:
        return bool(self.args.dry_run)

    def freeze(self) -> None:
        """Copy the task list into the run, once."""
        if self.dry:
            return
        self.dir.mkdir(parents=True, exist_ok=True)
        frozen = self.dir / "tasks.json"
        if not frozen.exists() or self.refreeze:
            shutil.copyfile(self.tasks_path, frozen)
            self.tasks_path = frozen
            self.refreeze = False
        self.manifest.setdefault("run_id", self.args.run_id)
        self.manifest.setdefault("created_at", now())
        self.manifest["tasks"] = {
            "fixed_at": self.doc.get("fixed_at"),
            "sha256": tasklib.file_sha256(frozen),
            "count": len(self.doc["tasks"]),
        }
        self.save_manifest()

    def save_manifest(self) -> None:
        if not self.dry:
            write_json(self.dir / "manifest.json", self.manifest)

    def save_state(self) -> None:
        if not self.dry:
            write_json(self.dir / "state.json", self.state)

    def selected(self) -> list[dict[str, Any]]:
        only = set(self.args.only_task or [])
        return [t for t in self.doc["tasks"] if not only or t["id"] in only]

    def captures(self, task: dict[str, Any]) -> dict[str, Any]:
        """What a task's prompt and oracle may use (taskfile.captures_for):
        the run_tag, the captures of the tasks its setup uses, and its own.
        A dry run shows a capture that does not exist yet as <name>."""
        captures = taskfile.captures_for(self.state, task["id"], self.by_id)
        if self.dry:
            return captures
        entry = self.state.get("tasks", {}).get(task["id"]) or {}
        if entry.get("status") != "done":
            raise Fail(2, f"task {task['id']} is not set up yet; run: run.py setup --run-id {self.args.run_id}")
        return captures

    def prompt(self, task: dict[str, Any]) -> str:
        """The trial's first turn: the task's question, captures filled in."""
        try:
            return tasklib.question(task, self.captures(task), "placeholder" if self.dry else "error")
        except taskfile.MissingCapture as e:
            raise Fail(2, f"task {task['id']}: {e}; run: run.py setup --run-id {self.args.run_id}") from e


def api_for(ctx: kates_api.KatesContext, cls: type = oraclelib.KatesAPI) -> Any:
    """A client for the Kates API as a context: its URL, key, proxy and TLS."""
    return cls(ctx.url, ctx.api_key, proxy_url=ctx.proxy_url, insecure=ctx.insecure)


def resolve_bin(name: str, what: str, dry: bool) -> str:
    found = shutil.which(name)
    if found:
        return str(Path(found).resolve())
    if dry:
        return name
    raise Fail(3, f"{what} not found: {name!r} is not an executable on PATH or a path")


# ---------------------------------------------------------------------------
# Setup


def pin_cluster(run: Run, kates_bin: str) -> kates_api.KatesContext:
    """Read the human and agent contexts and pin the Kafka clusterId the
    human context reaches into state.json, once per run. Setup and the
    oracle then check that the human context still reaches it, and preflight
    and every trial that the agent context does. Setup pins first; preflight
    and trials pin when setup has not run yet, so the arms can be checked
    before setup spends half an hour on the lab. Returns the human context."""
    args = run.args
    try:
        human = kates_api.read_context(kates_bin, args.human_context)
        agent = kates_api.read_context(kates_bin, args.agent_context)
    except kates_api.ContextError as e:
        raise Fail(3, str(e)) from e
    try:
        live = api_for(human).cluster_id()
    except oraclelib.OracleError as e:
        raise Fail(3, f"cannot reach the Kates API as {human.masked()}: {e}") from e
    if args.allow_cluster and args.allow_cluster != live:
        raise Fail(3, f"--allow-cluster {args.allow_cluster} but {human.url} reaches Kafka cluster {live}")
    pinned = run.state.get("cluster_id")
    if pinned and pinned != live:
        raise Fail(3, f"this run pinned Kafka cluster {pinned}, but {human.url} now reaches {live}; "
                      "its setup and expected answers belong to the first; start a new --run-id")
    run.state["cluster_id"] = live
    run.state["api_url"] = human.url
    run.state["kates"] = kates_bin
    run.state["human_context"] = {"name": human.name, "url": human.url}
    run.state["agent_context"] = {"name": agent.name, "url": agent.url}
    run.manifest["agent_key_is_human_key"] = bool(agent.api_key) and agent.api_key == human.api_key
    if agent.url.rstrip("/") != human.url.rstrip("/"):
        warn(f"the agent context points at {agent.url}, the human context at {human.url}")
    if run.manifest["agent_key_is_human_key"]:
        warn("the agent context holds the human's key: until scoped keys exist (plan Phase 2) nothing "
             "but the arm setup keeps the agents from anything the human can do. Run this on a lab.")
    run.save_state()
    run.save_manifest()
    say(f"cluster {live} via {human.masked()}; agent context {agent.masked()}; run_tag {run.state['run_tag']}")
    return human


def phase_setup(run: Run) -> int:
    """Pin the cluster, then run each selected task's setup with setup.py as
    the human (TASKS.md, "What the harness does to the lab")."""
    args = run.args
    kates_bin = resolve_bin(args.kates_bin, "the kates CLI", run.dry)
    api = None
    if run.dry:
        say(f"# setup: read contexts {args.human_context!r} (human) and {args.agent_context!r} (agent) with "
            f"`kates ctx export --name <ctx> --reveal`, then GET /api/cluster/info as the human")
        say(f"# dry run: run_tag {run.state['run_tag']}; nothing is executed")
    else:
        human = pin_cluster(run, kates_bin)
        api = api_for(human, setuplib.HarnessAPI)
        # Frozen once the cluster is pinned: a setup that could not start
        # leaves the run free to take a changed task list.
        run.freeze()
    harness = setuplib.Harness(kates=kates_bin, context=args.human_context,
                               run_dir=None if run.dry else str(run.dir), api=api, dry_run=run.dry, out=say)
    for task in run.selected():
        try:
            setuplib.ensure_task(harness, run.doc, run.state, task["id"])
        except setuplib.StepFailed as e:
            raise Fail(4, f"setup failed: {e}. The lab is partly prepared; start a new --run-id, "
                          "which gets a new run_tag") from e
        if run.dry:
            say(f"  prompt: {taskfile.render(task['prompt'], run.captures(task), 'placeholder')}")
    run.save_state()
    return 0


# ---------------------------------------------------------------------------
# Oracle


def load_oracle(path: Path) -> types.ModuleType:
    """The module with the oracle functions: oracle.py, or a stand-in with
    the same ORACLES table (the tests use testdata/oracle_fixture.py)."""
    if not path.exists():
        raise Fail(3, f"no oracle module at {path}; the task list's oracle functions live there")
    spec = importlib.util.spec_from_file_location("kates_eval_oracle", path)
    if spec is None or spec.loader is None:
        raise Fail(3, f"cannot load {path}")
    module = importlib.util.module_from_spec(spec)
    try:
        spec.loader.exec_module(module)
    except Exception as e:  # the oracle is its own code; report, do not crash
        raise Fail(4, f"{path} failed to import: {type(e).__name__}: {e}") from e
    if not isinstance(getattr(module, "ORACLES", None), dict):
        raise Fail(3, f"{path} has no ORACLES table mapping oracle names to functions")
    return module


def phase_oracle(run: Run, recheck: bool = False) -> int:
    """Compute every selected task's expected answer from the Kates API, as
    the human. With recheck, compute them again after the trials into
    oracle-recheck.json: a task whose answer moved while its trials ran (a
    leader, a lag, a live check) is void, and grade.py and report.py leave
    it out of the comparison."""
    args = run.args
    module = run.oracle
    target = "oracle-recheck.json" if recheck else "oracle.json"
    if run.dry:
        say(f"# oracle ({target}), as the human context {args.human_context!r}")
        for task in run.selected():
            call = taskfile.render(task["oracle"].get("args", {}), run.captures(task), "placeholder")
            say(f"  {task['id']}: {task['oracle']['fn']}({', '.join(f'{k}={v!r}' for k, v in call.items())})")
        return 0
    kates_bin = resolve_bin(args.kates_bin, "the kates CLI", False)
    try:
        human = kates_api.read_context(kates_bin, args.human_context)
    except kates_api.ContextError as e:
        raise Fail(3, str(e)) from e
    api = api_for(human)
    # The expected answers must come from the cluster the run pinned.
    pinned = run.state.get("cluster_id")
    if pinned:
        try:
            live = api.cluster_id()
        except oraclelib.OracleError as e:
            raise Fail(3, f"cannot reach the Kates API as {human.masked()}: {e}") from e
        if live != pinned:
            raise Fail(3, f"this run pinned Kafka cluster {pinned}, but {human.url} now reaches {live}; "
                          "its expected answers belong to the first; start a new --run-id")
    out = read_json(run.dir / target, {"tasks": {}}) if not recheck else {"tasks": {}}
    failed = []
    for task in run.selected():
        if not recheck and task["id"] in out["tasks"] and not args.redo_oracle and \
                "expected" in out["tasks"][task["id"]]:
            continue
        entry: dict[str, Any] = {"oracle": task["oracle"]["fn"], "computed_at": now()}
        try:
            entry["expected"] = oraclelib.compute_task(task, run.captures(task), api, module.ORACLES)
        except oraclelib.OracleError as e:  # a failing oracle voids the task; it does not stop the run
            entry["error"] = str(e)
            failed.append(task["id"])
            warn(f"oracle {task['id']}: {e}")
        else:
            say(f"oracle {task['id']}: {json.dumps(entry['expected'], sort_keys=True)}")
        out["tasks"][task["id"]] = entry
    out["computed_at"] = now()
    out["api_url"] = human.url
    write_json(run.dir / target, out)
    if recheck:
        first = read_json(run.dir / "oracle.json", {"tasks": {}})
        for tid in oraclelib.differences(first, out):
            warn(f"{tid}: the expected answer changed during the run: {first['tasks'][tid]['expected']} -> "
                 f"{out['tasks'][tid]['expected']}; the task is void")
    return 4 if failed else 0


# ---------------------------------------------------------------------------
# Trials


def build_schedule(doc: dict[str, Any], arm_names: list[str], seed: int | str, max_trials: int | None = None,
                   only: list[str] | None = None) -> list[tuple[int, str, str]]:
    """(round, task, arm) for every trial. Round r holds trial r of every
    task and arm, shuffled with a generator seeded by (seed, r), so neither
    arm always runs first and no task always follows the same one."""
    chosen = [t for t in doc["tasks"] if not only or t["id"] in only]

    def trials(t: dict[str, Any]) -> int:
        return min(t["trials"], max_trials) if max_trials else t["trials"]

    rounds = max((trials(t) for t in chosen), default=0)
    out: list[tuple[int, str, str]] = []
    for r in range(1, rounds + 1):
        pairs = [(t["id"], a) for t in chosen for a in t["arms"] if a in arm_names and r <= trials(t)]
        random.Random(f"{seed}:{r}").shuffle(pairs)
        out.extend((r, tid, arm) for tid, arm in pairs)
    return out


def claude_version(claude_bin: str) -> str:
    try:
        proc = subprocess.run([claude_bin, "--version"], capture_output=True, text=True, timeout=60,
                              stdin=subprocess.DEVNULL)
    except (OSError, subprocess.TimeoutExpired) as e:
        raise Fail(3, f"cannot run {claude_bin} --version: {e}") from e
    return (proc.stdout.strip().split() or ["unknown"])[0]


def arm_settings(run: Run, cluster_id: str, dry: bool) -> tuple[armlib.ArmSettings, str]:
    args = run.args
    skill_path: Path = args.skill
    if not skill_path.exists():
        raise Fail(3, f"no skill file at {skill_path}")
    skill_text = skill_path.read_text(encoding="utf-8")
    jq = shutil.which("jq")
    if not jq and "cli" in args.arms and not dry:
        raise Fail(3, "jq is not on PATH, and the CLI arm's skill pipes kates output through it")
    s = armlib.ArmSettings(
        claude_bin=resolve_bin(args.claude_bin, "Claude Code", dry),
        kates_bin=resolve_bin(args.kates_bin, "the kates CLI", dry),
        model=args.model,
        max_turns=args.max_turns,
        protocol_negotiation=args.protocol_negotiation,
        sdk_generation=args.sdk_generation,
        trial_context=args.trial_context,
        cluster_id=cluster_id,
        cluster_label=args.cluster_label,
        max_budget_usd=args.max_budget_usd,
        skill_text=skill_text,
        skill_mode=args.skill_mode,
        mcp_resources=args.mcp_resources,
        jq_bin=str(Path(jq).resolve()) if jq else None,
    )
    return s, tasklib.file_sha256(skill_path)


def check_manifest(run: Run, settings: armlib.ArmSettings, skill_sha: str, seed: int | str) -> None:
    """Record the run's settings, or refuse settings that differ from them."""
    current = {
        "model": settings.model, "max_turns": settings.max_turns, "max_budget_usd": settings.max_budget_usd,
        "protocol_negotiation": settings.protocol_negotiation, "sdk_generation": settings.sdk_generation,
        "skill_mode": settings.skill_mode, "skill_sha256": skill_sha, "trial_context": settings.trial_context,
        "mcp_resources": settings.mcp_resources, "seed": seed,
        "kates_sha256": tasklib.file_sha256(settings.kates_bin), "cluster_id": settings.cluster_id,
    }
    recorded = run.manifest.get("settings")
    if recorded:
        differ = [f"{k}: run has {recorded.get(k)!r}, now {current[k]!r}" for k in MANIFEST_KEYS
                  if recorded.get(k) != current[k]]
        if differ:
            raise Fail(2, "these settings differ from the ones this run started with; use a new --run-id:\n  "
                          + "\n  ".join(differ))
    else:
        run.manifest["settings"] = current
    run.manifest.update({k: current[k] for k in MANIFEST_KEYS})
    run.manifest["skill_path"] = str(run.args.skill)
    run.save_manifest()


def write_files(plan: armlib.TrialPlan) -> None:
    for path, content in plan.files.items():
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_text(content)
        if content.startswith("#!"):
            path.chmod(0o755)


def materialize(plan: armlib.TrialPlan, sandbox: armlib.Sandbox, agent: kates_api.KatesContext,
                trial_context: str) -> None:
    """Create the trial's HOME, working directory, config files and links."""
    for d in (sandbox.home, sandbox.work, sandbox.tmp, sandbox.bin):
        d.mkdir(parents=True, exist_ok=True, mode=0o700)
    write_files(plan)
    for link, target in plan.links.items():
        link.symlink_to(target)
    cfg = sandbox.kates_config
    fd = os.open(cfg, os.O_WRONLY | os.O_CREAT | os.O_TRUNC, 0o600)
    with os.fdopen(fd, "w") as fh:
        fh.write(kates_api.trial_config_json(agent, trial_context))


def execute(plan: armlib.TrialPlan, tdir: Path, timeout: float, out_name: str = "transcript.jsonl",
            err_name: str = "stderr.log") -> tuple[int | None, bool, float]:
    """Run claude with stdout to out_name and stderr to err_name in tdir.
    (exit code, timed out, wall-clock seconds)."""
    timed_out = False
    with open(tdir / out_name, "wb") as out, open(tdir / err_name, "wb") as err:
        start = time.monotonic()
        proc = subprocess.Popen(plan.argv, cwd=plan.cwd, env=plan.env, stdin=subprocess.DEVNULL,
                                stdout=out, stderr=err, start_new_session=True)
        try:
            proc.wait(timeout=timeout)
        except subprocess.TimeoutExpired:
            timed_out = True
            stop_group(proc)
        except KeyboardInterrupt:
            stop_group(proc)
            raise
        wall = time.monotonic() - start
        # Whatever claude left running in its session (a server that ignored
        # EOF, a command sent to the background) goes with it.
        signal_group(proc, signal.SIGKILL)
    return proc.returncode, timed_out, wall


def signal_group(proc: subprocess.Popen, sig: int) -> bool:
    """Send sig to claude's process group; False when the group is gone."""
    try:
        os.killpg(proc.pid, sig)
        return True
    except (ProcessLookupError, PermissionError):
        return False


def stop_group(proc: subprocess.Popen) -> None:
    """Stop claude and everything it started (kates mcp, Bash commands):
    SIGTERM, then SIGKILL if it has not exited within ten seconds."""
    if not signal_group(proc, signal.SIGTERM):
        return
    try:
        proc.wait(timeout=10)
    except subprocess.TimeoutExpired:
        signal_group(proc, signal.SIGKILL)
        proc.wait(timeout=10)


def print_dry_trial(settings: armlib.ArmSettings, skill_sha: str, round_no: int, task: dict[str, Any], arm: str,
                    n: int, shown: dict[str, dict[str, str]], prompt: str, timeout: float,
                    answer_turn: bool = True) -> None:
    """Print what a trial would write and run. Each distinct config file is
    printed in full once per arm (a task's forbid list changes the settings);
    after that the trial names the one it matches."""
    sandbox = armlib.Sandbox(DRY_SANDBOX)
    plan = armlib.plan_trial(arm, task, settings, sandbox, prompt, dict(os.environ))
    where = f"{task['id']} / {arm} / trial-{n}"
    say(f"\n=== round {round_no}: {where}  (timeout {timeout:g} s)")
    kates_yaml = json.dumps({"current-context": settings.trial_context, "contexts": {settings.trial_context: {
        "url": "<agent context URL>", "output": "table", "api-key": "<agent context key>"}}})
    files = {f"{sandbox.kates_config} (0600)": kates_yaml}
    for path, content in plan.files.items():
        files[str(path)] = content if not content.startswith("---") else f"<SKILL.md, sha256 {skill_sha}>"
    for link, target in plan.links.items():
        files[f"{link} (symlink)"] = f"-> {target}"
    for name, content in files.items():
        seen = shown.setdefault(f"{arm}:{name}", {})
        if content in seen:
            say(f"--- {name}: as for {seen[content]}")
            continue
        seen[content] = where
        say(f"--- {name}")
        say(content.rstrip())
    say(armlib.render_command(plan, prompt))
    if answer_turn:
        request = tasklib.answer_request(task["answer_fields"])
        second = armlib.plan_answer_turn(plan, settings, sandbox, "<session id of the first turn>", request)
        for path, content in second.files.items():
            seen = shown.setdefault(f"{arm}:{path}", {})
            if content in seen:
                say(f"--- {path}: as for {seen[content]}")
                continue
            seen[content] = where
            say(f"--- {path}")
            say(content.rstrip())
        say(f"# then, if the first turn finished (timeout {armlib.ANSWER_TIMEOUT_S} s):")
        say(armlib.render_command(second, request))


def run_trial(run: Run, settings: armlib.ArmSettings, skill_sha: str, agent: kates_api.KatesContext | None,
              round_no: int, task: dict[str, Any], arm: str, n: int, shown: dict[str, dict[str, str]],
              prompt: str | None = None, tdir: Path | None = None, timeout: float | None = None,
              answer_turn: bool = True) -> dict[str, Any] | None:
    """Run one trial, or print it in a dry run. Returns its metrics, or None
    when it was skipped (already done) or only printed.

    A trial is two turns (tasks.py): the question, whose transcript is
    transcript.jsonl and whose prose is graded for caveats and grounded
    claims; then, if the agent finished, the answer request in the same
    session with every tool denied, whose transcript is answer.jsonl and
    whose JSON block the checks read. Tokens, tool calls and wall-clock are
    the first turn's: the second is the harness asking for a form."""
    tdir = tdir or run.dir / task["id"] / arm / f"trial-{n}"
    if (tdir / "metrics.json").exists():
        if not (getattr(run.args, "redo_invalid", False) and not run.dry and set_aside_if_invalid(tdir)):
            return None
    prompt = prompt if prompt is not None else run.prompt(task)
    timeout = timeout or task["timeout_s"]
    if run.dry:
        print_dry_trial(settings, skill_sha, round_no, task, arm, n, shown, prompt, timeout, answer_turn)
        return None

    assert agent is not None
    tdir.mkdir(parents=True, exist_ok=True)
    for stale in ("answer.jsonl", "answer-stderr.log", "answer-request.txt"):
        (tdir / stale).unlink(missing_ok=True)
    (tdir / "prompt.txt").write_text(prompt)
    root = Path(tempfile.mkdtemp(prefix="kates-eval-")).resolve()
    sandbox = armlib.Sandbox(root)
    started = now()
    try:
        plan = armlib.plan_trial(arm, task, settings, sandbox, prompt, dict(os.environ))
        materialize(plan, sandbox, agent, settings.trial_context)
        write_json(tdir / "config.json", {
            "task_id": task["id"], "arm": arm, "trial": n, "round": round_no, "sandbox": str(root),
            "argv": armlib.recorded_argv(plan.argv, prompt, skill_sha), "env": armlib.redact_env(plan.env),
            "settings": plan.settings, "mcp_config": plan.mcp_config,
            "kates_config": {"context": settings.trial_context, "url": agent.url,
                             "api_key": f"{agent.api_key[:4]}…" if agent.api_key else ""},
        })
        exit_code, timed_out, wall = execute(plan, tdir, timeout)
        t = metrics.load(tdir / "transcript.jsonl")
        summary = metrics.summarize(t, timed_out=timed_out)
        problems, notes = armlib.check_init(t.init, arm, settings)
        status = summary["status"]
        if problems and t.init is not None and status in metrics.AGENT_STATUSES:
            status = "config_violation"

        answer_record: dict[str, Any] | None = None
        answer_text = metrics.final_text(t)
        session = (t.result or {}).get("session_id") or (t.init or {}).get("session_id")
        if answer_turn and status == "ok" and session:
            request = tasklib.answer_request(task["answer_fields"])
            (tdir / "answer-request.txt").write_text(request)
            second = armlib.plan_answer_turn(plan, settings, sandbox, str(session), request)
            write_files(second)
            code2, timed_out2, wall2 = execute(second, tdir, armlib.ANSWER_TIMEOUT_S, "answer.jsonl",
                                               "answer-stderr.log")
            t2 = metrics.load(tdir / "answer.jsonl")
            status2 = metrics.status_of(t2, timed_out2)
            answer_text = metrics.final_text(t2)
            reported = (t2.result or {}).get("total_cost_usd")
            answer_record = {"status": status2, "exit_code": code2, "wall_clock_s": round(wall2, 3),
                             "tokens": metrics.token_usage(t2), "tool_calls": len(t2.tool_uses),
                             "cost_usd": answer_cost(summary.get("cost_usd"), reported),
                             "cost_usd_reported": reported}
            # With every tool denied, an answer turn that timed out or ended
            # without a result was held up by the API or the harness: the
            # trial is invalid. Running out of its turns (the agent tried a
            # denied tool instead of answering) counts against the agent.
            if status2 in metrics.INVALID_STATUSES or status2 == "timeout":
                status = "infra_error" if status2 == "timeout" else status2
                problems = problems + [f"the answer turn ended {status2}; see answer-stderr.log"]

        # A port-forward that died, or a context that now reaches another
        # cluster, fails the agent through no fault of its own.
        unreachable = agent_api_problem(agent, settings.cluster_id)
        if unreachable and status in metrics.AGENT_STATUSES:
            status = "infra_error"
            problems = problems + [unreachable]
        answer, answer_error = metrics.extract_answer(answer_text)
        write_json(tdir / "answer.json", answer if answer is not None else {"_error": answer_error})
        cost = summary.get("cost_usd")
        if answer_record and answer_record["cost_usd"] is not None:
            cost = (cost or 0) + answer_record["cost_usd"]
        record = {
            "task_id": task["id"], "arm": arm, "trial": n, "round": round_no,
            **summary,
            "answer_found": answer is not None,
            "answer_error": answer_error,
            "answer_turn": answer_record,
            "cost_usd": cost,
            "status": status,
            "valid": status in metrics.AGENT_STATUSES,
            "exit_code": exit_code,
            "wall_clock_s": round(wall, 3),
            "config_problems": problems,
            "config_notes": notes,
            "forbidden_used": metrics.forbidden_uses(t, plan.forbid_mcp_tools, plan.forbid_kates_paths,
                                                    plan.forbid_prefixes),
            "env": {k: plan.env[k] for k in ("MCP_PROTOCOL_NEGOTIATION", "MCP_SDK_GENERATION",
                                            "MCP_CONNECTION_NONBLOCKING") if k in plan.env},
            "started_at": started,
            "finished_at": now(),
        }
        # Written last: its presence is what marks the trial done.
        write_json(tdir / "metrics.json", record)
        return record
    finally:
        if not run.args.keep_sandbox:
            shutil.rmtree(root, ignore_errors=True)


def answer_cost(first: float | None, reported: float | None) -> float | None:
    """The answer turn's own cost. A resumed session restores the cost so far
    (Claude Code keeps it in the session), so the answer turn reports the two
    turns together; when its figure is below the first turn's, it counted
    itself alone."""
    if reported is None:
        return None
    if first is not None and reported >= first:
        return round(reported - first, 6)
    return reported


def agent_api_problem(agent: kates_api.KatesContext, cluster_id: str) -> str | None:
    """Why the agent's context no longer reaches the pinned cluster, if it does not."""
    try:
        reached = api_for(agent).cluster_id()
    except oraclelib.OracleError as e:
        return f"after the trial the Kates API did not answer the agent's context: {e}"
    if reached != cluster_id:
        return f"after the trial the agent's context reached Kafka cluster {reached}, not {cluster_id}"
    return None


def set_aside_if_invalid(tdir: Path) -> bool:
    """Rename an invalid trial's directory to trial-<n>.invalid-<k>, keeping
    its evidence, so the trial runs again. False when the trial is valid."""
    record = read_json(tdir / "metrics.json", {})
    if record.get("valid", True):
        return False
    k = 1
    while tdir.with_name(f"{tdir.name}.invalid-{k}").exists():
        k += 1
    tdir.rename(tdir.with_name(f"{tdir.name}.invalid-{k}"))
    return True


def require_auth(env: dict[str, str]) -> None:
    if not any(env.get(k) for k in armlib.AUTH_ENV):
        raise Fail(3, "set ANTHROPIC_API_KEY or CLAUDE_CODE_OAUTH_TOKEN (claude setup-token): each trial runs "
                      "with an empty HOME, so the login of your own HOME and keychain is not visible to it")


def prepare_trials(run: Run) -> tuple[armlib.ArmSettings, str, kates_api.KatesContext | None, int | str]:
    args = run.args
    # The local checks come first (skill, jq, claude, kates, credentials), so
    # a run that cannot start has read no context and written no state.
    settings, skill_sha = arm_settings(run, "<clusterId>", run.dry)
    if not run.dry:
        require_auth(dict(os.environ))
    pinned = run.state.get("cluster_id")
    if run.dry:
        if not pinned:
            say(f"# pin: read contexts {args.human_context!r} (human) and {args.agent_context!r} (agent) with "
                f"`kates ctx export --name <ctx> --reveal`, GET /api/cluster/info as the human, write state.json")
    elif not pinned:
        pin_cluster(run, settings.kates_bin)
    elif args.allow_cluster and args.allow_cluster != pinned:
        raise Fail(3, f"--allow-cluster {args.allow_cluster} but this run pinned Kafka cluster {pinned}")
    # Frozen only once the checks pass, so a run that could not start is
    # free to take a changed task list.
    run.freeze()
    cluster_id = run.state.get("cluster_id") or args.allow_cluster or "<clusterId>"
    settings = dataclasses.replace(settings, cluster_id=cluster_id)
    seed = args.seed if args.seed is not None else run.manifest.get("seed", random.randrange(2 ** 31))
    agent = None
    if not run.dry:
        try:
            agent = kates_api.read_context(settings.kates_bin, args.agent_context)
        except kates_api.ContextError as e:
            raise Fail(3, str(e)) from e
        # The CLI arm has no pin of its own: check that the agent's context
        # reaches the cluster the expected answers were computed on.
        try:
            reached = api_for(agent).cluster_id()
        except oraclelib.OracleError as e:
            raise Fail(3, f"cannot reach the Kates API as the agent ({agent.masked()}): {e}") from e
        if reached != cluster_id:
            raise Fail(3, f"the agent context reaches Kafka cluster {reached}, not the pinned {cluster_id}")
        check_manifest(run, settings, skill_sha, seed)
        version = claude_version(settings.claude_bin)
        seen = run.manifest.setdefault("claude_versions", [])
        if version not in seen:
            if seen:
                warn(f"Claude Code changed from {seen[-1]} to {version} during this run")
            seen.append(version)
        run.manifest["claude_version"] = ", ".join(seen)
        run.save_manifest()
    return settings, skill_sha, agent, seed


def phase_trials(run: Run) -> int:
    args = run.args
    # A task whose oracle failed is void: its trials would cost money and
    # count for nothing.
    oracle = read_json(run.dir / "oracle.json", {"tasks": {}}).get("tasks", {})
    void = sorted(tid for tid, e in oracle.items() if isinstance(e, dict) and "expected" not in e)
    # Refuse before recording any setting: a trial of a task that is not set
    # up has no prompt to ask.
    if not run.dry:
        not_ready = [t["id"] for t in run.selected() if t["id"] not in void
                     and (run.state.get("tasks", {}).get(t["id"]) or {}).get("status") != "done"]
        if not_ready:
            raise Fail(2, f"not set up yet: {', '.join(not_ready)}; run: run.py setup --run-id {args.run_id}")
    settings, skill_sha, agent, seed = prepare_trials(run)
    if void:
        warn(f"no trials for void tasks (their oracle failed; see oracle.json): {', '.join(void)}")
    only = [t["id"] for t in run.selected() if t["id"] not in void]
    schedule = build_schedule(run.doc, args.arms, seed, args.max_trials, only)
    if run.dry:
        say(f"# trials: {len(schedule)} in {max((r for r, _, _ in schedule), default=0)} rounds, seed {seed}")
        say(f"# SKILL_PROMPT holds {armlib.SKILL_HEADER.strip()!r} followed by {args.skill} (sha256 {skill_sha})")
        say(f"# /SANDBOX stands for a fresh temporary directory per trial, removed afterwards")
    else:
        write_json(run.dir / "schedule.json", {"seed": seed, "trials": [
            {"round": r, "task_id": tid, "arm": arm} for r, tid, arm in schedule]})
    shown: dict[str, dict[str, str]] = {}
    invalid: list[str] = []
    for i, (round_no, tid, arm) in enumerate(schedule, start=1):
        task = run.by_id[tid]
        record = run_trial(run, settings, skill_sha, agent, round_no, task, arm, round_no, shown)
        if record is None:
            continue
        where = f"{tid}/{arm}/trial-{round_no}"
        tokens = (record.get("tokens") or {}).get("total", 0)
        say(f"[{i}/{len(schedule)}] {where}: {record['status']}, {record['tool_calls']} tool calls, "
            f"{tokens:,} tokens, {record['wall_clock_s']:.1f} s")
        if not record["valid"]:
            invalid.append(where)
            detail = "; ".join(record["config_problems"]) or f"see {run.dir / where / 'stderr.log'}"
            warn(f"{where} is invalid ({record['status']}): {detail}")
    if invalid:
        warn(f"{len(invalid)} invalid trials: {', '.join(invalid)}. Run the same command with "
             "--redo-invalid to run them again")
        return 5
    return 0


# ---------------------------------------------------------------------------
# Preflight


def phase_preflight(run: Run) -> int:
    settings, skill_sha, agent, _ = prepare_trials(run)
    failures: list[str] = []
    shown: dict[str, dict[str, str]] = {}
    for arm in run.args.arms:
        task = {"id": f"preflight-{arm}", "forbid": {}, "timeout_s": 300, "answer_fields": PREFLIGHT_FIELDS}
        tdir = run.dir / "preflight" / arm
        if not run.dry and (tdir / "metrics.json").exists():
            (tdir / "metrics.json").unlink()
        pf_settings = dataclasses.replace(settings, max_turns=5)
        record = run_trial(run, pf_settings, skill_sha, agent, 0, task, arm, 1, shown,
                           prompt=PREFLIGHT_PROMPTS[arm], tdir=tdir, timeout=300)
        if record is None:
            continue
        t = metrics.load(tdir / "transcript.jsonl")
        checks = preflight_checks(arm, record, t, settings.cluster_id, read_json(tdir / "answer.json", {}))
        for name, ok, required in checks:
            say(f"{'PASS' if ok else 'FAIL' if required else 'WARN'}  {arm}: {name}")
            if not ok and required:
                failures.append(f"{arm}: {name}")
        turn = record.get("answer_turn") or {}
        if turn:
            say(f"      answer turn: {turn.get('cost_usd')} USD (reported {turn.get('cost_usd_reported')}), "
                f"question turn {record.get('cost_usd', 0) - (turn.get('cost_usd') or 0):.4f} USD")
        for p in record["config_problems"]:
            say(f"      {p}")
        for n in record["config_notes"]:
            say(f"      note: {n}")
    if failures:
        warn(f"preflight failed: {'; '.join(failures)}; transcripts under {run.dir / 'preflight'}")
        return 6
    return 0


PREFLIGHT_FIELDS = [{"name": "cluster_id", "type": "string", "description": "the Kafka clusterId you read"}]


def preflight_checks(arm: str, record: dict[str, Any], t: metrics.Transcript, cluster_id: str,
                     answer: dict[str, Any]) -> list[tuple[str, bool, bool]]:
    """(check, passed, required). A check that is not required only warns."""
    denied = metrics.denied_ids(t)
    turn = record.get("answer_turn") or {}
    checks = [
        ("the session finished normally", record["status"] == "ok", True),
        ("Claude Code started the session the arm needs", not record["config_problems"] and t.init is not None, True),
        ("the answer names the pinned clusterId", cluster_id in metrics.final_text(t), True),
        ("the answer turn resumed the session and restated the answer as JSON, with no tool call",
         turn.get("status") == "ok" and answer.get("cluster_id") == cluster_id and turn.get("tool_calls") == 0, True),
    ]
    if arm == "mcp":
        checks.append(("cluster_overview ran and was allowed", any(
            u["name"] == metrics.MCP_PREFIX + "cluster_overview" and u["id"] not in denied for u in t.tool_uses),
            True))
    else:
        calls = metrics.kates_calls(t)
        checks.append(("a kates command ran and was allowed", any(not c["denied"] for c in calls), True))
        denials = (t.result or {}).get("permission_denials") or []
        checks.append(("kubectl was refused", any(
            "kubectl" in json.dumps(d.get("tool_input", {})) for d in denials if isinstance(d, dict)), True))
        commands = metrics.bash_commands(t)
        checks.append(("a kates call that would start load was refused", any(
            c["denied"] for c in commands if "test create" in c["command"]), True))
        checks.append(("jq could not read a file named as an argument", any(
            c["denied"] for c in commands if c["command"].startswith("jq") and " /etc/hosts" in c["command"]
            and "<" not in c["command"]), True))
        # The jq wrapper cannot see a shell redirect; whether Claude Code's
        # Bash rules let `jq . < file` through decides whether jq can read a
        # file the agent names. Refused is the hoped-for result; a pass
        # through is a known gap (README, "Read this first").
        checks.append(("jq could not read a file through a shell redirect", any(
            c["denied"] for c in commands if c["command"].startswith("jq") and "<" in c["command"]), False))
    return checks


# ---------------------------------------------------------------------------
# Expert arm


def phase_expert_template(run: Run) -> int:
    from grade import EXPERT_COLUMNS
    run.freeze()
    lines = []
    rows = []
    for task in run.selected():
        prompt = run.prompt(task)
        # The expert answers the way the agents do: the question first, in
        # prose, and only then the answer fields.
        lines.append(f"## {task['id']} ({task['persona']})\n\n{prompt}\n\n"
                     f"<details><summary>After writing the prose answer: the answer fields</summary>\n\n"
                     f"{tasklib.answer_request(task['answer_fields'])}\n\n</details>\n")
        rows.append({"task_id": task["id"], "trial": 1})
    if run.dry:
        say("\n".join(lines))
        return 0
    template = run.dir / "expert-template.csv"
    with open(template, "w", newline="", encoding="utf-8") as fh:
        writer = csv.DictWriter(fh, fieldnames=EXPERT_COLUMNS)
        writer.writeheader()
        writer.writerows(rows)
    (run.dir / "expert-prompts.md").write_text("# Expert arm prompts\n\n" + "\n".join(lines))
    say(f"wrote {template} and {run.dir / 'expert-prompts.md'}; fill in the template and save it as "
        f"{run.dir / 'expert.csv'}")
    return 0


# ---------------------------------------------------------------------------
# Command line


def parse_arms(value: str) -> list[str]:
    names = [a.strip() for a in value.split(",") if a.strip()]
    bad = [a for a in names if a not in tasklib.ARMS]
    if bad or not names:
        raise argparse.ArgumentTypeError(f"arms must be a comma-separated subset of {','.join(tasklib.ARMS)}")
    return names


def build_parser() -> argparse.ArgumentParser:
    common = argparse.ArgumentParser(add_help=False)
    common.add_argument("--run-id", help="name of the run directory under --runs-dir")
    common.add_argument("--runs-dir", type=Path, default=DEFAULT_RUNS)
    common.add_argument("--tasks", type=Path, help="task list (default eval/mcp/tasks.json; "
                                                   "a run keeps the copy it froze)")
    common.add_argument("--oracle", type=Path, default=DEFAULT_ORACLE, help="module with the oracle functions")
    common.add_argument("--only-task", action="append", metavar="ID", help="limit to a task (repeatable)")
    common.add_argument("--arms", type=parse_arms, default=list(tasklib.ARMS), help="comma list: mcp,cli")
    common.add_argument("--kates-bin", default="kates", help="the kates CLI under test")
    common.add_argument("--human-context", default="human", help="kates context for setup and the oracle")
    common.add_argument("--agent-context", default="agent", help="kates context the agents get")
    common.add_argument("--allow-cluster", help="refuse to run unless the human context reaches this Kafka clusterId "
                                                "(and, once pinned, unless it is the run's)")
    common.add_argument("--dry-run", action="store_true", help="print what would run; touch nothing")

    agent = argparse.ArgumentParser(add_help=False)
    agent.add_argument("--claude-bin", default="claude")
    agent.add_argument("--model", help="model for both arms, e.g. claude-opus-5-5 (required unless --dry-run)")
    agent.add_argument("--max-turns", type=int, default=30)
    agent.add_argument("--max-budget-usd", type=float, help="per-trial spending cap passed to claude")
    agent.add_argument("--protocol-negotiation", choices=armlib.NEGOTIATION_CHOICES, default="legacy",
                       help="MCP_PROTOCOL_NEGOTIATION for every trial (plan §8.4: set it explicitly)")
    agent.add_argument("--sdk-generation", choices=armlib.SDK_GENERATIONS, default="v2",
                       help="MCP_SDK_GENERATION: the Claude Code MCP client runtime")
    agent.add_argument("--seed", type=int, help="order seed (default: the run's, or a new random one)")
    agent.add_argument("--max-trials", type=int, help="run at most this many trials per task and arm")
    agent.add_argument("--skill", type=Path, default=DEFAULT_SKILL)
    agent.add_argument("--skill-mode", choices=armlib.SKILL_MODES, default="system-prompt")
    agent.add_argument("--trial-context", default="lab",
                       help="name of the agent's context in the trial HOME (the skill's recipes say --context lab)")
    agent.add_argument("--cluster-label", default="lab")
    agent.add_argument("--mcp-resources", action="store_true",
                       help="also give the MCP arm Claude Code's resource tools (kates:// resources)")
    agent.add_argument("--redo-invalid", action="store_true",
                       help="run again the trials recorded as invalid, keeping the old ones as trial-<n>.invalid-<k>")
    agent.add_argument("--keep-sandbox", action="store_true",
                       help="keep each trial's temporary HOME (it holds the agent key) for debugging")

    parser = argparse.ArgumentParser(description=__doc__.split("\n\n")[0],
                                     formatter_class=argparse.RawDescriptionHelpFormatter,
                                     epilog="Phases and exit codes: see the module docstring or README.md.")
    sub = parser.add_subparsers(dest="phase", required=True)
    sub.add_parser("validate", parents=[common], help="check the task list")
    sub.add_parser("setup", parents=[common], help="prepare the lab as the human")
    p = sub.add_parser("oracle", parents=[common], help="compute expected answers")
    p.add_argument("--recheck", action="store_true", help="compute again into oracle-recheck.json")
    p.add_argument("--redo-oracle", action="store_true")
    sub.add_parser("preflight", parents=[common, agent], help="check each arm's session with one short call")
    sub.add_parser("trials", parents=[common, agent], help="run the agent arms")
    p = sub.add_parser("all", parents=[common, agent], help="setup, oracle, trials, oracle --recheck")
    p.add_argument("--redo-oracle", action="store_true")
    sub.add_parser("expert-template", parents=[common], help="write the expert arm's CSV and prompts")
    return parser


def phase_validate(args: argparse.Namespace) -> int:
    path = args.tasks or DEFAULT_TASKS
    module = load_oracle(args.oracle)
    try:
        doc = tasklib.load_tasks(path, module.ORACLES)
    except tasklib.TaskError as e:
        raise Fail(2, f"{path} is invalid:\n  " + "\n  ".join(e.problems)) from e
    personas: dict[str, int] = {}
    for t in doc["tasks"]:
        personas[t["persona"]] = personas.get(t["persona"], 0) + 1
    trials = sum(t["trials"] * len(t["arms"]) for t in doc["tasks"])
    say(f"{path}: {len(doc['tasks'])} tasks, fixed at {doc['fixed_at']}, {trials} agent trials; "
        f"personas {json.dumps(personas, sort_keys=True)}")
    return 0


def dispatch(args: argparse.Namespace) -> int:
    if args.phase == "validate":
        return phase_validate(args)
    if not args.run_id:
        raise Fail(2, "--run-id is required")
    if not RUN_ID_RE.fullmatch(args.run_id):
        raise Fail(2, f"--run-id {args.run_id!r}: use letters, digits, '.', '_' and '-' only")
    if args.phase in ("trials", "all", "preflight") and not args.model and not args.dry_run:
        raise Fail(2, "--model is required: both arms must run the same, named model")
    if getattr(args, "model", None) is None and args.phase in ("trials", "all", "preflight"):
        args.model = "<model>"
    run = Run(args)
    if args.phase == "setup":
        return phase_setup(run)
    if args.phase == "oracle":
        return phase_oracle(run, recheck=args.recheck)
    if args.phase == "preflight":
        return phase_preflight(run)
    if args.phase == "trials":
        return phase_trials(run)
    if args.phase == "expert-template":
        return phase_expert_template(run)
    # all. A failed oracle voids its task and does not stop the others.
    code = phase_setup(run)
    if code != 0:
        return code
    oracle_code = phase_oracle(run)
    code = phase_trials(run)
    recheck = phase_oracle(run, recheck=True)
    return code or oracle_code or recheck


def _interrupt(signum: int, frame: Any) -> None:
    raise KeyboardInterrupt


def main(argv: list[str] | None = None) -> int:
    args = build_parser().parse_args(argv)
    # A closed terminal or a kill must stop the trial's claude and kates mcp
    # too (they run in their own session), as Ctrl-C does.
    for sig in (signal.SIGTERM, signal.SIGHUP):
        # Under nohup SIGHUP is ignored on purpose: leave it so.
        if signal.getsignal(sig) != signal.SIG_IGN:
            signal.signal(sig, _interrupt)
    try:
        return dispatch(args)
    except Fail as e:
        print(f"error: {e}", file=sys.stderr)
        return e.code
    except KeyboardInterrupt:
        print("interrupted; run the same command again to resume", file=sys.stderr)
        return 130


if __name__ == "__main__":
    sys.exit(main())

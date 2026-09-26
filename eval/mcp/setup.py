#!/usr/bin/env python3
"""Prepares the lab for each task: load, baselines, lag and faults.

Agents in the evaluation only read. Everything that changes the lab (test
runs, a baseline, records that make a group lag, a pod kill) is done here,
by the harness, as the human, before the task's trials, so that no agent arm
ever needs a write and the oracle can compute the answer afterwards.

A task's setup is a list of steps (tasks.json, documented in TASKS.md):

  kates         run the kates binary with --context <human context>
  api           a GET, or a POST to one of taskfile.SETUP_API_POSTS, for
                what no kates command does and returns from (template runs)
  wait          sleep, optionally measured from a captured epoch
  clock         capture the current time
  use           run another task's setup first (once) and see its captures
  assert_equal  stop when a premise of the task does not hold

Captured values are written to <run-dir>/state.json. The prompt and the
oracle of a task read them as {name}; taskfile.captures_for merges the
built-ins (run_tag), the captures of the tasks it uses, and its own.

A setup that fails leaves the lab partly prepared: start a new run
directory, which gets a new run_tag and so new topics and specs.

Usage:
  setup.py --run-dir runs/<id> --context lab [--task ID ...]
  setup.py --dry-run [--task ID ...]        # print what would run
Exit codes: 0 every requested setup is done; 1 a step failed; 2 bad input.
"""

from __future__ import annotations

import argparse
import datetime as dt
import json
import os
import secrets
import shlex
import shutil
import subprocess
import sys
import time
from dataclasses import dataclass, field
from typing import Any, Callable, Mapping

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))

import kates_api  # noqa: E402
import oracle  # noqa: E402
import taskfile  # noqa: E402

HERE = os.path.dirname(os.path.abspath(__file__))
KATES_TIMEOUT_S = 1200
API_TIMEOUT_S = 900
# Variables that override what the context says (the kates-cli skill, §1).
STRIPPED_ENV = kates_api.KATES_OVERRIDE_ENV


class StepFailed(Exception):
    """A setup step did not do what the task needs."""


class HarnessAPI(oracle.KatesAPI):
    """The oracle's reader plus the few POSTs setup is allowed."""

    def post(self, path: str, body: Any, timeout: float) -> Any:
        if not any(path == p or (p.endswith("/") and path.startswith(p)) for p in taskfile.SETUP_API_POSTS):
            raise StepFailed(f"setup may not POST to {path}")
        saved, self.timeout = self.timeout, timeout
        try:
            return self._send("POST", path, None, body)
        finally:
            self.timeout = saved


def run_kates_subprocess(argv: list[str], timeout: float) -> tuple[int, str, str]:
    env = {k: v for k, v in os.environ.items() if k not in STRIPPED_ENV}
    try:
        p = subprocess.run(argv, capture_output=True, text=True, timeout=timeout, env=env, stdin=subprocess.DEVNULL)
    except subprocess.TimeoutExpired:
        return 124, "", f"timed out after {timeout:.0f}s"
    except OSError as e:
        return 127, "", str(e)
    return p.returncode, p.stdout, p.stderr


def _utcnow() -> dt.datetime:
    return dt.datetime.now(dt.timezone.utc)


@dataclass
class Harness:
    """What setup touches, injectable so tests run without a lab."""

    kates: str
    context: str
    run_dir: str | None
    api: Any = None
    dry_run: bool = False
    run_kates: Callable[[list[str], float], tuple[int, str, str]] = run_kates_subprocess
    now: Callable[[], dt.datetime] = _utcnow
    sleep: Callable[[float], None] = time.sleep
    out: Callable[[str], None] = print
    done_in_run: set = field(default_factory=set)


def new_state(context: str, api_url: str | None, kates: str, run_tag: str | None = None) -> dict:
    now = _utcnow().isoformat(timespec="seconds").replace("+00:00", "Z")
    return {
        "version": 1,
        "run_tag": run_tag or secrets.token_hex(3),
        "context": context,
        "api_url": api_url,
        "kates": kates,
        "created_at": now,
        "updated_at": now,
        "tasks": {},
    }


def load_state(run_dir: str) -> dict | None:
    path = os.path.join(run_dir, "state.json")
    if not os.path.exists(path):
        return None
    with open(path, encoding="utf-8") as f:
        return json.load(f)


def save_state(run_dir: str, state: dict) -> None:
    state["updated_at"] = _utcnow().isoformat(timespec="seconds").replace("+00:00", "Z")
    os.makedirs(run_dir, exist_ok=True)
    path = os.path.join(run_dir, "state.json")
    with open(path + ".tmp", "w", encoding="utf-8") as f:
        json.dump(state, f, indent=2, sort_keys=True)
        f.write("\n")
    os.replace(path + ".tmp", path)


def clock_value(now: dt.datetime, fmt: str) -> Any:
    now = now.astimezone(dt.timezone.utc)
    if fmt == "rfc3339":
        return now.replace(microsecond=0).isoformat().replace("+00:00", "Z")
    if fmt == "rfc3339_minute":
        return now.replace(second=0, microsecond=0).isoformat().replace("+00:00", "Z")
    if fmt == "epoch":
        return int(now.timestamp())
    if fmt == "hhmm_utc":
        return now.strftime("%H:%M")
    raise StepFailed(f"unknown clock format {fmt!r}")


def _parse_output(text: str, what: str) -> Any:
    try:
        return json.loads(text)
    except ValueError as e:
        raise StepFailed(f"{what} did not print JSON: {text[:200]!r}") from e


def _capture(doc: Any, spec: Mapping[str, str], what: str) -> dict:
    out = {}
    for name, path in spec.items():
        try:
            out[name] = taskfile.json_path(doc, path)
        except taskfile.PathError as e:
            raise StepFailed(f"{what}: cannot capture {name}: {e}") from e
    return out


def _expect(doc: Any, spec: Mapping[str, Any], captures: Mapping[str, Any], what: str) -> None:
    for path, want in spec.items():
        want = taskfile.render(want, captures)
        try:
            got = taskfile.json_path(doc, path)
        except taskfile.PathError as e:
            raise StepFailed(f"{what}: expected {path} = {want!r}: {e}") from e
        if got != want:
            raise StepFailed(f"{what}: expected {path} = {want!r}, got {got!r}")


def _save_output(h: Harness, tid: str, n: int, suffix: str, text: str) -> None:
    if h.run_dir and text:
        d = os.path.join(h.run_dir, "setup", tid)
        os.makedirs(d, exist_ok=True)
        with open(os.path.join(d, f"step-{n}.{suffix}"), "w", encoding="utf-8") as f:
            f.write(text)


def run_step(h: Harness, tid: str, n: int, step: Mapping[str, Any], captures: dict) -> tuple[dict, dict]:
    """Runs one step; returns its new captures and its log entry."""
    kind = step["kind"]
    miss = "placeholder" if h.dry_run else "error"
    started = time.monotonic()
    log: dict[str, Any] = {"step": n, "kind": kind}
    new: dict[str, Any] = {}

    if kind == "kates":
        args = [str(a) for a in taskfile.render(step["args"], captures, miss)]
        argv = [h.kates, *args, "--context", h.context]
        log["argv"] = argv[1:]
        repeat = int(step.get("repeat", 1))
        h.out(f"  kates {shlex.join(argv[1:])}" + (f"   (x{repeat})" if repeat > 1 else ""))
        if h.dry_run:
            new = {name: f"<{name}>" for name in step.get("capture", {})}
        else:
            stdout = ""
            for i in range(repeat):
                code, stdout, stderr = h.run_kates(argv, float(step.get("timeout_s", KATES_TIMEOUT_S)))
                _save_output(h, tid, n, "out" if repeat == 1 else f"{i}.out", stdout)
                _save_output(h, tid, n, "err" if repeat == 1 else f"{i}.err", stderr)
                log["exit"] = code
                if code != 0 and not step.get("allow_fail"):
                    raise StepFailed(f"kates {shlex.join(args)} exited {code}: {stderr.strip()[:300]}")
            if log.get("exit") == 0 and (step.get("capture") or step.get("expect")):
                doc = _parse_output(stdout, f"kates {' '.join(args[:3])}")
                new = _capture(doc, step.get("capture", {}), f"kates {' '.join(args[:3])}")
                _expect(doc, step.get("expect", {}), dict(captures, **new), f"kates {' '.join(args[:3])}")

    elif kind == "api":
        path = taskfile.render(step["path"], captures, miss)
        body = taskfile.render(step.get("body"), captures, miss)
        log["request"] = f"{step['method']} {path}"
        h.out(f"  {step['method']} {path}" + (f" {json.dumps(body, sort_keys=True)}" if body is not None else ""))
        if h.dry_run:
            new = {name: f"<{name}>" for name in step.get("capture", {})}
        else:
            if h.api is None:
                raise StepFailed(f"{step['method']} {path} needs the API: set {oracle.API_KEY_ENV}")
            timeout = float(step.get("timeout_s", API_TIMEOUT_S))
            try:
                doc = h.api.post(path, body, timeout) if step["method"] == "POST" else h.api.get(path)
            except oracle.OracleError as e:
                raise StepFailed(str(e)) from e
            _save_output(h, tid, n, "json", json.dumps(doc, indent=2))
            new = _capture(doc, step.get("capture", {}), log["request"])
            _expect(doc, step.get("expect", {}), dict(captures, **new), log["request"])

    elif kind == "wait":
        seconds = float(step["seconds"])
        if "since" in step:
            since = taskfile.render(step["since"], captures, miss)
            h.out(f"  wait until {seconds:.0f}s after {since}")
            if not h.dry_run:
                seconds = max(0.0, float(since) + seconds - h.now().timestamp())
        else:
            h.out(f"  wait {seconds:.0f}s")
        if not h.dry_run and seconds > 0:
            h.sleep(seconds)
        log["slept_s"] = 0 if h.dry_run else round(seconds, 1)

    elif kind == "clock":
        now = h.now()
        new = {name: clock_value(now, fmt) for name, fmt in step["capture"].items()}
        h.out("  clock " + ", ".join(f"{k}={v}" for k, v in new.items()))

    elif kind == "assert_equal":
        left = taskfile.render(step["left"], captures, miss)
        right = taskfile.render(step["right"], captures, miss)
        h.out(f"  assert {left} == {right}")
        if not h.dry_run and left != right:
            raise StepFailed(f"{step['message']} ({left} != {right})")

    log["seconds"] = round(time.monotonic() - started, 2)
    return new, log


def ensure_task(h: Harness, doc: Mapping[str, Any], state: dict, tid: str, stack: tuple = ()) -> None:
    """Runs a task's setup unless this run already did; use steps first run
    the setup they name. Raises StepFailed and records the failure."""
    by_id = taskfile.tasks_by_id(doc)
    entry = state["tasks"].get(tid)
    if tid in h.done_in_run or (entry and entry.get("status") == "done" and not h.dry_run):
        return
    if tid in stack:
        raise StepFailed(f"setup of {tid} uses itself")
    task = by_id[tid]
    h.out(f"== {tid}")
    captures = {k: state[k] for k in taskfile.BUILTIN_CAPTURES if k in state}
    own: dict[str, Any] = {}
    logs: list[dict] = []
    started = _utcnow().isoformat(timespec="seconds").replace("+00:00", "Z")
    try:
        for n, step in enumerate(task["setup"]):
            if step["kind"] == "use":
                ensure_task(h, doc, state, step["task"], stack + (tid,))
                captures.update(taskfile.captures_for(state, step["task"], by_id))
                logs.append({"step": n, "kind": "use", "task": step["task"]})
                continue
            new, log = run_step(h, tid, n, step, dict(captures, **own))
            own.update(new)
            logs.append(log)
    except (StepFailed, taskfile.MissingCapture) as e:
        state["tasks"][tid] = {"status": "failed", "started_at": started, "error": str(e), "log": logs,
                               "captures": own}
        if h.run_dir and not h.dry_run:
            save_state(h.run_dir, state)
        raise StepFailed(f"{tid}: {e}") from e
    finished = _utcnow().isoformat(timespec="seconds").replace("+00:00", "Z")
    state["tasks"][tid] = {"status": "done", "started_at": started, "finished_at": finished, "captures": own,
                           "log": logs}
    h.done_in_run.add(tid)
    if h.run_dir and not h.dry_run:
        save_state(h.run_dir, state)


def needs_api(doc: Mapping[str, Any], task_ids: list[str]) -> bool:
    by_id = taskfile.tasks_by_id(doc)
    seen, stack = set(), list(task_ids)
    while stack:
        tid = stack.pop()
        if tid in seen:
            continue
        seen.add(tid)
        for s in by_id[tid]["setup"]:
            if s["kind"] == "api":
                return True
            if s["kind"] == "use":
                stack.append(s["task"])
    return False


def main(argv: list[str] | None = None, harness_factory: Callable[..., Harness] = Harness) -> int:
    p = argparse.ArgumentParser(description=__doc__.split("\n\n")[0], formatter_class=argparse.RawDescriptionHelpFormatter)
    p.add_argument("--tasks", default=os.path.join(HERE, "tasks.json"))
    p.add_argument("--run-dir", help="where state.json and step output go (required unless --dry-run)")
    p.add_argument("--context", default="human", help="the kates context of the human (default human, as run.py)")
    p.add_argument("--kates", default=os.environ.get("KATES_BIN", "kates"), help="the kates binary (default $KATES_BIN or kates)")
    p.add_argument("--api-url", default=os.environ.get(oracle.API_URL_ENV, oracle.DEFAULT_API_URL),
                   help=f"the Kates API for api steps (default ${oracle.API_URL_ENV} or {oracle.DEFAULT_API_URL})")
    p.add_argument("--task", action="append", help="only this task and the tasks it uses (repeatable)")
    p.add_argument("--dry-run", action="store_true", help="print the steps and rendered prompts; touch nothing")
    a = p.parse_args(argv)

    try:
        doc = taskfile.load(a.tasks)
    except taskfile.TaskFileError as e:
        print(f"setup: {e}", file=sys.stderr)
        return 2
    errs = taskfile.validate(doc, oracle.ORACLES)
    if errs:
        print("setup: invalid task file:\n  " + "\n  ".join(errs), file=sys.stderr)
        return 2
    by_id = taskfile.tasks_by_id(doc)
    task_ids = a.task or list(by_id)
    unknown = [t for t in task_ids if t not in by_id]
    if unknown:
        print(f"setup: unknown task {', '.join(unknown)}", file=sys.stderr)
        return 2
    if not a.dry_run and not a.run_dir:
        print("setup: --run-dir is required (or pass --dry-run)", file=sys.stderr)
        return 2

    kates = a.kates
    if not a.dry_run:
        found = shutil.which(kates) if os.sep not in kates else (kates if os.access(kates, os.X_OK) else None)
        if not found:
            print(f"setup: kates binary {kates!r} not found; pass --kates or set KATES_BIN", file=sys.stderr)
            return 2
        kates = found

    state = load_state(a.run_dir) if a.run_dir else None
    if state is None:
        state = new_state(a.context, a.api_url, kates)
    elif state.get("context") != a.context:
        print(f"setup: {a.run_dir} was set up with context {state.get('context')!r}, not {a.context!r}", file=sys.stderr)
        return 2

    api = None
    if not a.dry_run and needs_api(doc, task_ids):
        key = os.environ.get(oracle.API_KEY_ENV, "")
        if not key:
            print(f"setup: these tasks start template runs through the API; set {oracle.API_KEY_ENV}", file=sys.stderr)
            return 2
        api = HarnessAPI(a.api_url, key)
    h = harness_factory(kates=kates, context=a.context, run_dir=None if a.dry_run else a.run_dir, api=api,
                        dry_run=a.dry_run)
    if a.dry_run:
        h.out(f"# dry run: run_tag {state['run_tag']}, context {a.context}; nothing is executed")
    try:
        for tid in task_ids:
            ensure_task(h, doc, state, tid)
            if a.dry_run:
                prompt = taskfile.render(by_id[tid]["prompt"], taskfile.captures_for(state, tid, by_id), "placeholder")
                h.out(f"  prompt: {prompt}")
    except StepFailed as e:
        print(f"setup: {e}", file=sys.stderr)
        return 1
    if not a.dry_run:
        h.out(f"setup: {len(task_ids)} task(s) ready; state in {os.path.join(a.run_dir, 'state.json')}")
    return 0


if __name__ == "__main__":
    sys.exit(main())

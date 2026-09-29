"""Aggregate a graded run per arm, and apply the kill criteria of plan §2.5.

The evaluation exists to make one decision after Phase 1: keep building the
curated MCP server, or freeze it at read-only. Plan §2.5 fixes the rule
before any result is in:

    continue only if the MCP arm's task success is at least the CLI-plus-skill
    arm's AND it either beats that arm by at least 15 points of task success
    or uses at least 30 % fewer tokens and tool calls, AND it meets both hard
    gates (grounded claims at least 95 %, silent misreads 0).

This script applies exactly that, criterion by criterion, and prints PASS or
FAIL for each and the verdict. It compares the arms on the tasks both arms
ran (every valid trial of those tasks counts, as the plan asks). Task success
and misreads use the human grader's verdict where grading.csv has one, and
the automatic grade elsewhere; the verdict says "provisional" until every row
is human-graded. Means are per trial. Tokens are every token the model read
or wrote, cached or not (metrics.token_usage).

It also names what the numbers rest on: the protocol era Claude Code was set
to (§8.4), the model and Claude Code version, the seed, the task list's
fixed_at and hash, and anything that weakens the result: invalid trials,
expected answers that changed during the run, tasks with fewer than three
valid trials in an arm, and whether the agent's key was the human's.

Exit codes: 0 the verdict is CONTINUE, 1 it is STOP, 3 the data cannot
decide yet (an arm has no valid trials, a gate has nothing to measure, or the
verdict is provisional until the human pass is done), 2 the run cannot be
read.
"""

from __future__ import annotations

import argparse
import csv
import json
import statistics
import sys
from collections import defaultdict
from pathlib import Path
from typing import Any

import tasks as tasklib

HERE = Path(__file__).resolve().parent
DEFAULT_RUNS = HERE / "runs"

SUCCESS_MARGIN_POINTS = 15.0
EFFICIENCY_REDUCTION = 0.30
GROUNDED_GATE = 0.95
MISREAD_GATE = 0
EPS = 1e-9

RESULT_WORDS = {True: "PASS", False: "FAIL", None: "NO DATA"}

_YES = {"yes", "y", "1", "true", "pass", "passed", "ok"}
_NO = {"no", "n", "0", "false", "fail", "failed"}


def parse_verdict(value: str | None) -> bool | None:
    v = (value or "").strip().casefold()
    if v in _YES:
        return True
    if v in _NO:
        return False
    return None


def _read_csv(path: Path) -> list[dict[str, str]]:
    if not path.exists():
        return []
    with open(path, newline="", encoding="utf-8") as fh:
        return list(csv.DictReader(fh))


def human_verdicts(run_dir: Path) -> dict[tuple[str, str, str], dict[str, Any]]:
    """The human grader's entries, keyed by (task, arm, trial) through the key."""
    key = {r["row_id"]: (r["task_id"], r["arm"], r["trial"]) for r in _read_csv(run_dir / "grading-key.csv")}
    out = {}
    for row in _read_csv(run_dir / "grading.csv"):
        ident = key.get(row.get("row_id", ""))
        if ident:
            out[ident] = {"success": parse_verdict(row.get("human_success")),
                          "misread": parse_verdict(row.get("human_misread")),
                          "ungrounded": parse_count(row.get("human_ungrounded"))}
    return out


def parse_count(value: str | None) -> int | None:
    v = (value or "").strip()
    return int(v) if v.isdigit() else None


def effective_rows(grades: dict[str, Any], human: dict[tuple[str, str, str], dict[str, Any]]) -> list[dict[str, Any]]:
    """Scored rows (valid agent trials and expert answers) with the human
    verdict applied over the automatic one."""
    rows = []
    for r in [t for t in grades.get("trials", []) if t.get("valid")] + grades.get("expert", []):
        h = human.get((r["task_id"], r["arm"], str(r["trial"])), {})
        row = dict(r)
        row["human_graded"] = h.get("success") is not None
        row["success"] = h["success"] if h.get("success") is not None else r.get("auto_success")
        # The grader sees the answer, not the transcript: a trial whose
        # forbidden call went through fails whatever the answer says.
        if r.get("forbidden_used"):
            row["success"] = False
        row["misread"] = h["misread"] if h.get("misread") is not None else r.get("silent_misread")
        # The grader's count of numbers no tool result supports replaces the
        # automatic one, which cannot follow a derived number.
        if h.get("ungrounded") is not None and r.get("numbers_total") is not None:
            row["numbers_grounded"] = max(0, r["numbers_total"] - h["ungrounded"])
            row["grounded_by_human"] = True
        rows.append(row)
    return rows


def _mean(values: list[float | int | None]) -> float | None:
    vals = [float(v) for v in values if v is not None]
    return statistics.fmean(vals) if vals else None


def arm_stats(rows: list[dict[str, Any]]) -> dict[str, Any]:
    decided = [r["success"] for r in rows if r["success"] is not None]
    totals = [r["numbers_total"] for r in rows if r.get("numbers_total") is not None]
    grounded = [r["numbers_grounded"] for r in rows if r.get("numbers_grounded") is not None]
    misreads = sum(1 for r in rows if r["misread"])
    return {
        "trials": len(rows),
        "decided": len(decided),
        "undecided": len(rows) - len(decided),
        "human_graded": sum(1 for r in rows if r.get("human_graded")),
        "success_rate": (sum(decided) / len(decided)) if decided else None,
        "numbers": sum(totals),
        "grounded_rate": (sum(grounded) / sum(totals)) if sum(totals) else None,
        "misread_answers": misreads,
        "misread_rate": misreads / len(rows) if rows else None,
        "tool_calls": _mean([r.get("tool_calls") for r in rows]),
        "tokens": _mean([r.get("tokens_total") for r in rows]),
        "first_turn_context": _mean([r.get("first_turn_context") for r in rows]),
        "cost_usd": _mean([r.get("cost_usd") for r in rows]),
        "wall_clock_s": _mean([r.get("wall_clock_s") for r in rows]),
        "mutating_calls": sum(len(r.get("mutating_calls") or []) for r in rows),
        "forbidden_uses": sum(1 for r in rows if r.get("forbidden_used")),
    }


def paired_tasks(rows: list[dict[str, Any]]) -> list[str]:
    arms_by_task: dict[str, set[str]] = defaultdict(set)
    for r in rows:
        arms_by_task[r["task_id"]].add(r["arm"])
    return sorted(t for t, arms in arms_by_task.items() if {"mcp", "cli"} <= arms)


def _reduction(new: float | None, old: float | None) -> float | None:
    if new is None or old is None or old <= 0:
        return None
    return 1 - new / old


def kill_criteria(mcp: dict[str, Any], cli: dict[str, Any]) -> tuple[list[dict[str, Any]], str]:
    """Each §2.5 criterion as {name, passed (True/False/None), detail}, and the
    verdict: CONTINUE, STOP, or UNDECIDED when a criterion has no data."""
    ms, cs = mcp["success_rate"], cli["success_rate"]
    margin = (ms - cs) * 100 if ms is not None and cs is not None else None
    tok = _reduction(mcp["tokens"], cli["tokens"])
    calls = _reduction(mcp["tool_calls"], cli["tool_calls"])

    def pct(x: float | None) -> str:
        return "n/a" if x is None else f"{x * 100:.1f} %"

    # EPS keeps float noise from failing a criterion met exactly: 0.70 - 0.55
    # is 14.999999999999996 points in floating point.
    not_worse = None if margin is None else margin >= -EPS
    wins_success = None if margin is None else margin >= SUCCESS_MARGIN_POINTS - EPS
    efficient = None if tok is None or calls is None else (
        tok >= EFFICIENCY_REDUCTION - EPS and calls >= EFFICIENCY_REDUCTION - EPS)
    if wins_success is True or efficient is True:
        clear_win: bool | None = True
    elif wins_success is None or efficient is None:
        clear_win = None
    else:
        clear_win = False
    grounded = mcp["grounded_rate"]
    grounded_ok = None if grounded is None else grounded >= GROUNDED_GATE - EPS
    misread_ok = None if mcp["trials"] == 0 else mcp["misread_answers"] <= MISREAD_GATE
    margin_text = "n/a" if margin is None else f"{margin:+.1f} points"
    criteria = [
        {"name": "MCP task success is at least the CLI arm's", "passed": not_worse,
         "detail": f"MCP {pct(ms)}, CLI {pct(cs)} ({margin_text})"},
        {"name": f"  (a) MCP beats the CLI arm by at least {SUCCESS_MARGIN_POINTS:g} points of task success",
         "passed": wins_success, "detail": margin_text},
        {"name": f"  (b) MCP uses at least {EFFICIENCY_REDUCTION * 100:.0f} % fewer tokens and tool calls",
         "passed": efficient, "detail": f"tokens {pct(tok)} fewer, tool calls {pct(calls)} fewer"},
        {"name": "MCP wins clearly: (a) or (b)", "passed": clear_win,
         "detail": "efficiency alone does not pass the other criteria"},
        {"name": f"Hard gate: MCP grounded-claim rate at least {GROUNDED_GATE * 100:.0f} %", "passed": grounded_ok,
         "detail": f"{pct(grounded)} of {mcp['numbers']} numbers"},
        {"name": "Hard gate: MCP silent misreads are 0", "passed": misread_ok,
         "detail": f"{mcp['misread_answers']} of {mcp['trials']} answers miss a required caveat"},
    ]
    decisive = [criteria[0], criteria[3], criteria[4], criteria[5]]
    if any(c["passed"] is False for c in decisive):
        verdict = "STOP"
    elif all(c["passed"] is True for c in decisive):
        verdict = "CONTINUE"
    else:
        verdict = "UNDECIDED"
    return criteria, verdict


def era_text(manifest: dict[str, Any]) -> str:
    neg = manifest.get("protocol_negotiation", "?")
    gen = manifest.get("sdk_generation", "?")
    if gen == "v1":
        era = ("the v1 client runtime (MCP TypeScript SDK 1.x), which speaks the pre-2026 handshake, "
               f"2025-11-25 at most; MCP_PROTOCOL_NEGOTIATION={neg} has no effect on v1")
    elif neg == "auto":
        era = ("the v2 client runtime with MCP_PROTOCOL_NEGOTIATION=auto: Claude Code asks the stdio server "
               "for revision 2026-07-28, which kates mcp (go-sdk v1.8.0) accepts")
    else:
        era = (f"the v2 client runtime with MCP_PROTOCOL_NEGOTIATION={neg}: stdio stays on the pre-2026 "
               "handshake, 2025-11-25 at most")
    return (f"{era}. This is the era the run configured (MCP_SDK_GENERATION={gen}, "
            f"MCP_PROTOCOL_NEGOTIATION={neg}); neither the transcript nor kates mcp's log records the "
            "revision actually negotiated.")


def _fmt(x: float | None, kind: str = "num") -> str:
    if x is None:
        return "n/a"
    if kind == "pct":
        return f"{x * 100:.1f} %"
    if kind == "usd":
        return f"${x:.3f}"
    if kind == "int":
        return f"{x:,.0f}"
    return f"{x:.1f}"


def _stats_table(stats_by_arm: dict[str, dict[str, Any]]) -> list[str]:
    lines = [
        "| Arm | Trials | Task success | Grounded claims | Silent misreads | Tool calls | Tokens | First-request context | Cost | Wall-clock (s) | Mutating calls | Forbidden uses |",
        "| --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: |",
    ]
    for arm, s in stats_by_arm.items():
        success = _fmt(s["success_rate"], "pct")
        if s["undecided"]:
            success += f" ({s['undecided']} undecided)"
        lines.append(
            f"| {arm} | {s['trials']} | {success} | {_fmt(s['grounded_rate'], 'pct')} ({s['numbers']}) | "
            f"{s['misread_answers']} | {_fmt(s['tool_calls'])} | {_fmt(s['tokens'], 'int')} | "
            f"{_fmt(s['first_turn_context'], 'int')} | "
            f"{_fmt(s['cost_usd'], 'usd')} | {_fmt(s['wall_clock_s'])} | {s['mutating_calls']} | {s['forbidden_uses']} |"
        )
    return lines


def _task_table(rows: list[dict[str, Any]], doc: dict[str, Any] | None) -> list[str]:
    lines = [
        "| Task | Persona | Arm | Success | Grounded | Misreads | Tool calls | Tokens | Wall-clock (s) |",
        "| --- | --- | --- | ---: | ---: | ---: | ---: | ---: | ---: |",
    ]
    order = [t["id"] for t in doc["tasks"]] if doc else sorted({r["task_id"] for r in rows})
    for tid in order:
        for arm in ("mcp", "cli", "expert"):
            sub = [r for r in rows if r["task_id"] == tid and r["arm"] == arm]
            if not sub:
                continue
            s = arm_stats(sub)
            wins = sum(1 for r in sub if r["success"])
            lines.append(
                f"| {tid} | {sub[0]['persona']} | {arm} | {wins}/{s['decided']}"
                f"{' (+' + str(s['undecided']) + ' undecided)' if s['undecided'] else ''} | "
                f"{_fmt(s['grounded_rate'], 'pct')} | {s['misread_answers']} | {_fmt(s['tool_calls'])} | "
                f"{_fmt(s['tokens'], 'int')} | {_fmt(s['wall_clock_s'])} |"
            )
    return lines


def build_report(run_dir: Path) -> tuple[str, str, dict[str, Any]]:
    """(markdown, verdict, data) for a graded run."""
    grades = json.loads((run_dir / "grades.json").read_text())
    manifest_path = run_dir / "manifest.json"
    manifest = json.loads(manifest_path.read_text()) if manifest_path.exists() else {}
    doc = tasklib.load_tasks(run_dir / "tasks.json") if (run_dir / "tasks.json").exists() else None
    rows = effective_rows(grades, human_verdicts(run_dir))
    agent_rows = [r for r in rows if r["arm"] in ("mcp", "cli")]
    void = grades.get("void_tasks", {})
    paired = [t for t in paired_tasks(agent_rows) if t not in void]
    on_paired = [r for r in agent_rows if r["task_id"] in paired]
    mcp = arm_stats([r for r in on_paired if r["arm"] == "mcp"])
    cli = arm_stats([r for r in on_paired if r["arm"] == "cli"])
    criteria, verdict = kill_criteria(mcp, cli)
    if mcp["trials"] == 0 or cli["trials"] == 0:
        verdict = "UNDECIDED"
    all_stats = {arm: arm_stats([r for r in rows if r["arm"] == arm])
                 for arm in ("mcp", "cli", "expert") if any(r["arm"] == arm for r in rows)}
    invalid = [t for t in grades.get("trials", []) if not t.get("valid")]
    ungraded = sum(1 for r in on_paired if not r["human_graded"])
    provisional = ungraded > 0 or mcp["undecided"] + cli["undecided"] > 0

    few: list[str] = []
    for tid in paired:
        for arm in ("mcp", "cli"):
            n = sum(1 for r in agent_rows if r["task_id"] == tid and r["arm"] == arm)
            if n < tasklib.MIN_TRIALS:
                few.append(f"{tid}/{arm} ({n})")

    tasks_meta = manifest.get("tasks", {})
    out: list[str] = [f"# MCP evaluation: run {run_dir.name}", ""]
    label = f"**Verdict: {verdict}**" + (" (provisional)" if provisional and verdict != "UNDECIDED" else "")
    out += [label, ""]
    out += ["## What this run measured", "",
            f"- Claude Code {manifest.get('claude_version', 'unknown')}, model `{manifest.get('model', 'unknown')}`, "
            f"at most {manifest.get('max_turns', '?')} turns per trial"
            + (f", at most ${manifest['max_budget_usd']:g} per trial" if manifest.get("max_budget_usd") else "") + ".",
            f"- Protocol era: {era_text(manifest)}",
            f"- Seed {manifest.get('seed', grades.get('seed', '?'))} (task and arm order per round, row order of the blinded sheet).",
            f"- Task list fixed at {tasks_meta.get('fixed_at', '?')}, sha256 `{str(tasks_meta.get('sha256', '?'))[:16]}`, "
            f"{len(doc['tasks']) if doc else '?'} tasks; {len(paired)} run in both agent arms.",
            f"- CLI arm skill: {manifest.get('skill_mode', '?')} mode, sha256 `{str(manifest.get('skill_sha256', '?'))[:16]}`. "
            f"MCP arm resource tools: {'on' if manifest.get('mcp_resources') else 'off'}.",
            f"- kates binary sha256 `{str(manifest.get('kates_sha256', '?'))[:16]}`; cluster `{manifest.get('cluster_id', '?')}`.",
            ""]
    if manifest.get("agent_key_is_human_key"):
        out += ["The agent context carried the human's key (there are no scoped keys before plan Phase 2), "
                "so nothing but the tool and permission setup kept the agents from anything the human can do.", ""]

    out += ["## Kill criteria (plan §2.5)", "",
            f"Compared on the {len(paired)} tasks both agent arms ran"
            + (f", leaving out {len(void)} void tasks (see the caveats below)" if void else "")
            + f": {mcp['trials']} MCP trials, {cli['trials']} CLI trials.", "",
            "| Criterion | Result | Values |", "| --- | --- | --- |"]
    for c in criteria:
        result = RESULT_WORDS[c["passed"]]
        out.append(f"| {c['name']} | {result} | {c['detail']} |")
    out += ["", f"Verdict: **{verdict}**. CONTINUE needs the first criterion, the clear win, and both hard gates.", ""]
    if provisional:
        undecided = mcp["undecided"] + cli["undecided"]
        text = f"Provisional: {ungraded} of {len(on_paired)} compared answers have no human verdict yet"
        if undecided:
            text += f", and {undecided} of them no automatic one either (no check applies), so they count nowhere"
        out += [text + ". Fill in grading.csv, then rerun grade.py and report.py.", ""]

    out += ["## Per arm", "", "All scored answers, including tasks only one arm ran.", ""]
    out += _stats_table(all_stats)
    out += ["", "Grounded claims: share of numbers in the answers found in the same trial's tool results "
                "(count in brackets). Silent misreads: answers missing a required caveat. Tokens: mean per trial "
                "of the question turn, cache reads included; the answer turn, which only restates the answer as "
                "JSON, is left out, and its cost is in Cost. First-request context: what the model read before "
                "any work (system prompt, tool definitions or skill, question), which every request repeats. "
                "Mutating calls: kates calls that would have changed the lab and ran; the CLI arm's wrapper "
                "refuses them, so anything above 0 is a hole in the harness.", ""]

    out += ["## Per task", ""] + _task_table(rows, doc) + [""]

    notes: list[str] = []
    if invalid:
        by_status: dict[str, int] = defaultdict(int)
        for t in invalid:
            by_status[t.get("status", "?")] += 1
        notes.append(f"{len(invalid)} trials are invalid and not scored ({dict(by_status)}); "
                     "run.py trials --redo-invalid runs them again.")
    for tid, why in void.items():
        notes.append(f"`{tid}` is void and left out of the kill criteria: {why}.")
    if not (run_dir / "oracle-recheck.json").exists():
        notes.append("The expected answers were not computed again after the trials (run.py oracle --recheck), "
                     "so an answer that moved while the trials ran (a leader, a lag) is not caught.")
    notes.append("The task list was written and fixed after both agent arms were built, by the people who built "
                 "them (plan §2.4 asks for it to be fixed before); read the task selection with that in mind.")
    if few:
        notes.append(f"Fewer than {tasklib.MIN_TRIALS} valid trials: {', '.join(few)}.")
    if doc and not 15 <= len(doc["tasks"]) <= 30:
        notes.append(f"The plan asks for about 20 tasks; this list has {len(doc['tasks'])}.")
    if notes:
        out += ["## Caveats of this run", ""] + [f"- {n}" for n in notes] + [""]
    if "expert" in all_stats:
        out += ["## Expert arm", "",
                "Answers an expert gave with the CLI and no AI (expert.csv), graded with the same checks and "
                "caveats. Grounded claims do not apply; tool calls are the commands the expert reported.", ""]
    data = {"verdict": verdict, "provisional": provisional, "criteria": criteria, "paired_tasks": paired,
            "mcp": mcp, "cli": cli, "arms": all_stats}
    return "\n".join(out), verdict, data


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(description="Aggregate a graded run and apply the plan §2.5 kill criteria.")
    parser.add_argument("--run-id", required=True)
    parser.add_argument("--runs-dir", type=Path, default=DEFAULT_RUNS)
    args = parser.parse_args(argv)
    run_dir = args.runs_dir / args.run_id
    if not (run_dir / "grades.json").exists():
        print(f"{run_dir} has no grades.json; run grade.py --run-id {args.run_id} first", file=sys.stderr)
        return 2
    try:
        text, verdict, data = build_report(run_dir)
    except (ValueError, KeyError, tasklib.TaskError) as e:
        print(f"cannot read {run_dir}: {e}", file=sys.stderr)
        return 2
    (run_dir / "report.md").write_text(text + "\n")
    for c in data["criteria"]:
        result = RESULT_WORDS[c["passed"]]
        print(f"{result:8} {c['name'].strip()}: {c['detail']}")
    suffix = " (provisional)" if data["provisional"] and verdict != "UNDECIDED" else ""
    print(f"verdict: {verdict}{suffix}; wrote {run_dir / 'report.md'}")
    # A provisional verdict can still change with the human pass: it decides nothing yet.
    if data["provisional"]:
        return 3
    return {"CONTINUE": 0, "STOP": 1}.get(verdict, 3)


if __name__ == "__main__":
    sys.exit(main())

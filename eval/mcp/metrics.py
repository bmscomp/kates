"""Read one trial's transcript: what the agent called, what it cost, what it answered.

A trial's transcript is the stdout of
`claude -p <prompt> --output-format stream-json --verbose`, one JSON event per
line. These are the events this module reads, with the fields Claude Code
2.1.278 writes (checked against the shapes in its binary):

- {"type": "system", "subtype": "init", "tools": [...], "mcp_servers":
  [{"name", "status"}], "model", "permissionMode", "claude_code_version",
  "apiKeySource", ...}, once, before the first turn;
- {"type": "assistant", "message": {"id", "content": [{"type": "text"},
  {"type": "tool_use", "id", "name", "input"}], "usage"}}, for each part of
  each model turn (several events can share one message id);
- {"type": "user", "message": {"content": [{"type": "tool_result",
  "tool_use_id", "content", "is_error"}]}}, what each tool returned, as the
  model saw it;
- {"type": "result", "subtype": "success" | "error_max_turns" | ..., "is_error",
  "num_turns", "duration_ms", "total_cost_usd", "usage": {"input_tokens",
  "output_tokens", "cache_creation_input_tokens", "cache_read_input_tokens"},
  "permission_denials": [{"tool_name", "tool_use_id", "tool_input"}],
  "result": "<the final answer>"}, once, at the end.

Every per-trial number in the report comes from here, and grade.py reads the
same transcript for the tool results it checks the answer's numbers against,
so both see one reading of it. A line that is not JSON is counted, not fatal:
a transcript cut off by a timeout still has its tool calls.

Run it on its own to see what a transcript holds:

    python3 eval/mcp/metrics.py eval/mcp/runs/<run>/<task>/<arm>/trial-1/transcript.jsonl
"""

from __future__ import annotations

import argparse
import json
import re
import sys
from dataclasses import dataclass, field
from pathlib import Path
from typing import Any

import kates_commands
import taskfile

MCP_PREFIX = "mcp__kates__"

# Trial statuses. The first five are the agent's own outcome and count in
# the results; the rest mean the harness or the service failed, not the
# agent, and the trial is rerun rather than scored (run.py marks them).
AGENT_STATUSES = ("ok", "max_turns", "budget", "timeout", "error")

# When the API fails (an overload, a 5xx, a rate limit, bad credentials, a
# usage limit) Claude Code ends the turn with an assistant message of its own,
# model "<synthetic>", and a result event with is_error set. That says nothing
# about the agent, whatever the wording. "Prompt is too long" is the one
# exception: the agent's own context did it.
SYNTHETIC_MODEL = "<synthetic>"
AGENT_SYNTHETIC_PREFIXES = ("Prompt is too long",)
INVALID_STATUSES = ("no_result", "infra_error", "config_violation")


@dataclass
class Transcript:
    init: dict[str, Any] | None = None
    result: dict[str, Any] | None = None
    tool_uses: list[dict[str, Any]] = field(default_factory=list)
    tool_results: dict[str, dict[str, Any]] = field(default_factory=dict)
    # message id -> the text of its text blocks, in order of first appearance
    assistant_text: dict[str, list[str]] = field(default_factory=dict)
    # message id -> usage as the last event for it reported
    assistant_usage: dict[str, dict[str, Any]] = field(default_factory=dict)
    # ids of assistant messages that called a tool (so are not an answer)
    tool_messages: set[str] = field(default_factory=set)
    # messages Claude Code wrote itself when the API failed (model "<synthetic>")
    api_errors: list[str] = field(default_factory=list)
    bad_lines: int = 0


def read_events(path: Path | str) -> tuple[list[dict[str, Any]], int]:
    """The JSON events of a transcript, and how many lines were not JSON."""
    events: list[dict[str, Any]] = []
    bad = 0
    try:
        text = Path(path).read_text(encoding="utf-8", errors="replace")
    except FileNotFoundError:
        return events, 0
    for line in text.splitlines():
        if not line.strip():
            continue
        try:
            ev = json.loads(line)
        except json.JSONDecodeError:
            bad += 1
            continue
        if isinstance(ev, dict):
            events.append(ev)
        else:
            bad += 1
    return events, bad


def content_text(content: Any) -> str:
    """The text of a tool result's content: a string, or a list of blocks."""
    if isinstance(content, str):
        return content
    if isinstance(content, list):
        parts = []
        for block in content:
            if isinstance(block, dict) and block.get("type") == "text":
                parts.append(str(block.get("text", "")))
            elif isinstance(block, str):
                parts.append(block)
        return "\n".join(parts)
    return ""


def parse_events(events: list[dict[str, Any]], bad_lines: int = 0) -> Transcript:
    t = Transcript(bad_lines=bad_lines)
    seen_tool_ids: set[str] = set()
    for i, ev in enumerate(events):
        etype = ev.get("type")
        if etype == "system" and ev.get("subtype") == "init" and t.init is None:
            t.init = ev
        elif etype == "result":
            t.result = ev
        elif etype == "assistant":
            msg = ev.get("message") or {}
            mid = str(msg.get("id") or f"event-{i}")
            if msg.get("model") == SYNTHETIC_MODEL:
                text = "\n".join(str(b.get("text", "")) for b in msg.get("content") or []
                                 if isinstance(b, dict) and b.get("type") == "text")
                if not text.startswith(AGENT_SYNTHETIC_PREFIXES):
                    t.api_errors.append(text[:300])
                    continue
            if isinstance(msg.get("usage"), dict):
                t.assistant_usage[mid] = msg["usage"]
            texts = t.assistant_text.setdefault(mid, [])
            for block in msg.get("content") or []:
                if not isinstance(block, dict):
                    continue
                if block.get("type") == "text" and block.get("text"):
                    texts.append(block["text"])
                elif block.get("type") == "tool_use":
                    t.tool_messages.add(mid)
                    tid = str(block.get("id") or f"tool-{i}-{len(t.tool_uses)}")
                    if tid in seen_tool_ids:
                        continue
                    seen_tool_ids.add(tid)
                    t.tool_uses.append({
                        "id": tid,
                        "name": str(block.get("name", "")),
                        "input": block.get("input") if isinstance(block.get("input"), dict) else {},
                        "parent": ev.get("parent_tool_use_id"),
                    })
        elif etype == "user":
            msg = ev.get("message") or {}
            content = msg.get("content")
            if not isinstance(content, list):
                continue
            for block in content:
                if isinstance(block, dict) and block.get("type") == "tool_result":
                    t.tool_results[str(block.get("tool_use_id", ""))] = {
                        "text": content_text(block.get("content")),
                        "is_error": bool(block.get("is_error")),
                    }
    return t


def load(path: Path | str) -> Transcript:
    events, bad = read_events(path)
    return parse_events(events, bad)


def final_text(t: Transcript) -> str:
    """The agent's final answer: the result event's text, or else the text of
    the last assistant message, if that message called no tool. A session
    that ran out of turns or time mid-task has no answer, and the narration
    before its last tool call is not one."""
    if t.result and isinstance(t.result.get("result"), str) and t.result["result"].strip():
        return t.result["result"]
    if t.assistant_text:
        mid, texts = list(t.assistant_text.items())[-1]
        if texts and mid not in t.tool_messages:
            return "\n".join(texts)
    return ""


# ---------------------------------------------------------------------------
# The answer block

_FENCE_RE = re.compile(r"^[ \t]*```[ \t]*([A-Za-z0-9_+.-]*)[^\n]*\n(.*?)\n?^[ \t]*```[ \t]*$", re.M | re.S)


def fenced_blocks(text: str) -> list[tuple[str, str, int, int]]:
    """Every closed ``` block: (language, body, start, end)."""
    return [(m.group(1).lower(), m.group(2), m.start(), m.end()) for m in _FENCE_RE.finditer(text)]


def answer_block(text: str) -> tuple[str, int, int] | None:
    """The block grading reads: the last block marked json, or else the last
    unmarked block whose body is a JSON object. (body, start, end)."""
    blocks = fenced_blocks(text)
    for lang, body, start, end in reversed(blocks):
        if lang == "json":
            return body, start, end
    for lang, body, start, end in reversed(blocks):
        if lang == "":
            try:
                if isinstance(json.loads(body), dict):
                    return body, start, end
            except json.JSONDecodeError:
                continue
    return None


def extract_answer(text: str) -> tuple[dict[str, Any] | None, str | None]:
    """The answer object and None, or None and why there is none."""
    found = answer_block(text)
    if found is None:
        return None, "no fenced json block in the final answer"
    try:
        value = json.loads(found[0])
    except json.JSONDecodeError as e:
        return None, f"the answer block is not valid JSON: {e}"
    if not isinstance(value, dict):
        return None, "the answer block is not a JSON object"
    return value, None


# ---------------------------------------------------------------------------
# Cost, calls and status

TOKEN_KEYS = {
    "input": "input_tokens",
    "output": "output_tokens",
    "cache_creation": "cache_creation_input_tokens",
    "cache_read": "cache_read_input_tokens",
}


def token_usage(t: Transcript) -> dict[str, Any]:
    """Tokens from the result event's usage, which covers every API request of
    the session. Without a result event (a timeout), the sum over assistant
    messages stands in, and source says so.

    total counts every token the model read or wrote, cached or not: it is
    the measure of how much context an arm consumed, which is what "fewer
    tokens" in the kill criteria compares. Cost is reported separately.
    """
    usages: list[dict[str, Any]]
    source = "result"
    if t.result and isinstance(t.result.get("usage"), dict):
        usages = [t.result["usage"]]
    else:
        usages = list(t.assistant_usage.values())
        source = "assistant_messages" if usages else "none"
    out: dict[str, Any] = {}
    for short, key in TOKEN_KEYS.items():
        out[short] = int(sum(int(u.get(key) or 0) for u in usages))
    out["total"] = sum(out[k] for k in TOKEN_KEYS)
    out["source"] = source
    return out


def first_turn_context(t: Transcript) -> int | None:
    """The context the model read on its first request: the system prompt,
    the tool definitions (or the skill) and the question. It is the fixed
    cost of an arm before any work, which the report shows beside the
    tokens, since it repeats on every request of a trial."""
    for usage in t.assistant_usage.values():
        return int(sum(int(usage.get(k) or 0) for k in
                       ("input_tokens", "cache_creation_input_tokens", "cache_read_input_tokens")))
    return None


def denied_ids(t: Transcript) -> set[str]:
    """Tool calls that did not run: refused by a permission rule, or by the
    CLI arm's kates or jq wrapper (its marker is in the result)."""
    denials = (t.result or {}).get("permission_denials") or []
    ids = {str(d.get("tool_use_id")) for d in denials if isinstance(d, dict)}
    ids |= {tid for tid, r in t.tool_results.items() if kates_commands.REFUSED_MARKER in (r.get("text") or "")}
    return ids


def status_of(t: Transcript, timed_out: bool = False) -> str:
    if timed_out:
        # A session that never started timed out on the harness, not the agent.
        return "timeout" if t.init is not None else "no_result"
    if t.result is None:
        return "no_result"
    subtype = t.result.get("subtype")
    if subtype == "success" and not t.result.get("is_error"):
        return "ok"
    if subtype == "error_max_turns":
        return "max_turns"
    if subtype == "error_max_budget_usd":
        return "budget"
    # An error the API caused (see SYNTHETIC_MODEL), or one before the model
    # produced anything, says nothing about the agent.
    if t.api_errors or not t.assistant_text:
        return "infra_error"
    return "error"


def tool_call_counts(t: Transcript) -> dict[str, int]:
    counts: dict[str, int] = {}
    for use in t.tool_uses:
        counts[use["name"]] = counts.get(use["name"], 0) + 1
    return dict(sorted(counts.items()))


def bash_commands(t: Transcript) -> list[dict[str, Any]]:
    denied = denied_ids(t)
    out = []
    for use in t.tool_uses:
        if use["name"] == "Bash":
            cmd = use["input"].get("command")
            if isinstance(cmd, str):
                out.append({"id": use["id"], "command": cmd, "denied": use["id"] in denied})
    return out


def kates_calls(t: Transcript) -> list[dict[str, Any]]:
    """Every kates invocation the agent ran through Bash, classified."""
    calls = []
    for bash in bash_commands(t):
        for args in kates_commands.kates_argvs(bash["command"]):
            info = kates_commands.classify(args)
            calls.append({
                "tool_use_id": bash["id"],
                "command": bash["command"],
                "args": args,
                "words": info["words"],
                "effect": info["effect"],
                "mutating": info["mutating"],
                "live": info["live"],
                "denied": bash["denied"],
            })
    return calls


def forbidden_uses(t: Transcript, mcp_tools: list[str], kates_paths: list[tuple[str, ...]],
                   prefixes: list[str] | None = None) -> list[str]:
    """Calls the task forbade that went through (a denied call leaked nothing).

    kates calls are matched by command path with aliases and global flags
    resolved (kates_commands.starts_with); other prefixes by
    taskfile.forbidden_hits, which splits pipelines, lists and subshells.
    """
    denied = denied_ids(t)
    hits: list[str] = []
    wanted = {MCP_PREFIX + name for name in mcp_tools}
    for use in t.tool_uses:
        if use["name"] in wanted and use["id"] not in denied:
            hits.append(use["name"])
    for call in kates_calls(t):
        if call["denied"]:
            continue
        for path in kates_paths:
            if kates_commands.starts_with(call["args"], path):
                hits.append("kates " + " ".join(path))
    if prefixes:
        for cmd in bash_commands(t):
            if cmd["denied"]:
                continue
            hits.extend(taskfile.forbidden_hits(cmd["command"], prefixes))
    return hits


def tool_texts(t: Transcript) -> list[str]:
    """What every tool returned in the trial, for the grounded-claim check."""
    return [r["text"] for r in t.tool_results.values() if r.get("text")]


def init_summary(t: Transcript) -> dict[str, Any] | None:
    if t.init is None:
        return None
    keys = ("model", "claude_code_version", "permissionMode", "tools", "mcp_servers",
            "mcp_server_errors", "apiKeySource", "cwd", "session_id")
    return {k: t.init.get(k) for k in keys if k in t.init}


def summarize(t: Transcript, *, timed_out: bool = False) -> dict[str, Any]:
    """The metrics of one trial that the transcript alone determines."""
    result = t.result or {}
    text = final_text(t)
    answer, answer_error = extract_answer(text)
    calls = kates_calls(t)
    denials = []
    for d in result.get("permission_denials") or []:
        if isinstance(d, dict):
            entry = {"tool_name": d.get("tool_name"), "tool_use_id": d.get("tool_use_id")}
            ti = d.get("tool_input")
            if isinstance(ti, dict) and isinstance(ti.get("command"), str):
                entry["command"] = ti["command"]
            denials.append(entry)
    return {
        "status": status_of(t, timed_out),
        "timed_out": timed_out,
        "bad_lines": t.bad_lines,
        "init": init_summary(t),
        "num_turns": result.get("num_turns", len(t.assistant_text)),
        "duration_ms": result.get("duration_ms"),
        "duration_api_ms": result.get("duration_api_ms"),
        "cost_usd": result.get("total_cost_usd"),
        "tokens": token_usage(t),
        "first_turn_context": first_turn_context(t),
        "tool_calls": len(t.tool_uses),
        "tool_call_names": tool_call_counts(t),
        "tool_errors": sum(1 for r in t.tool_results.values() if r.get("is_error")),
        "permission_denials": denials,
        "bash_commands": [b["command"] for b in bash_commands(t)],
        "kates_calls": [{k: c[k] for k in ("words", "effect", "mutating", "live", "denied")} for c in calls],
        "mutating_calls": [c["command"] for c in calls if c["mutating"] and not c["denied"]],
        "mutating_attempts": [c["command"] for c in calls if c["mutating"]],
        "live_calls": [c["command"] for c in calls if c["live"] and not c["denied"]],
        "answer_found": answer is not None,
        "answer_error": answer_error,
        "final_text_chars": len(text),
    }


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(description="Print the metrics of one stream-json transcript.")
    parser.add_argument("transcript", type=Path)
    parser.add_argument("--answer", action="store_true", help="print the extracted answer block instead")
    args = parser.parse_args(argv)
    if not args.transcript.is_file():
        print(f"no such transcript: {args.transcript}", file=sys.stderr)
        return 2
    t = load(args.transcript)
    if args.answer:
        answer, error = extract_answer(final_text(t))
        print(json.dumps(answer if answer is not None else {"_error": error}, indent=2))
    else:
        print(json.dumps(summarize(t), indent=2))
    return 0


if __name__ == "__main__":
    sys.exit(main())

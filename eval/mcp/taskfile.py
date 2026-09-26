"""The task file shared by the setup, the oracle and the runner.

tasks.json is the evaluation of plan §2.4 frozen as data: what each arm is
asked, what shape the answer must take, what the harness does to the lab
first, and how the right answer is computed. Setup, oracle and runner are
written separately and against this file, so the rules they share live here
once: what a valid task file is, how ``{capture}`` placeholders are filled in,
how a JSON path reads a command's output, and how a transcript is checked for
the command prefixes a task's ``forbid`` field names (arms.forbid_for turns
the field into Claude Code deny rules).

Everything here is pure: no network, no subprocess, no clock.
"""

from __future__ import annotations

import json
import re
from typing import Any, Iterable, Mapping

import kates_commands

PERSONAS = ("security", "platform", "sre", "gameday", "developer")
ANSWER_TYPES = ("string", "number", "boolean", "string[]")
CHECK_KINDS = ("exact", "number", "set", "contains")
TOLERANCE_KINDS = ("abs", "pct")
ARMS = ("mcp", "cli")
STEP_KINDS = ("kates", "api", "wait", "clock", "use", "assert_equal")
CLOCK_FORMATS = ("rfc3339", "rfc3339_minute", "epoch", "hhmm_utc")

# The read tools kates mcp serves (cli/cmd/mcp_tools_*.go). A prompt that
# names one hints at a tool, and forbid.mcp may name only these.
MCP_TOOLS = (
    "cluster_overview",
    "cluster_topology",
    "consumer_group_lag",
    "list_runs",
    "get_run",
    "assess_run",
    "kates_activity",
    "list_chaos_catalog",
    "preview_disruption",
    "disruption_report",
    "security_evidence",
    "draft_scenario",
)

# The only paths setup may POST to. A template run is the one way today to
# get a disruption whose final report is stored (DisruptionLauncher persists
# a second row under the same id, which fails), and POST /api/disruptions is
# how the stale-row task leaves a RUNNING record; neither has a kates command
# that returns. Setup never deletes or alters anything through the API.
SETUP_API_POSTS = ("/api/disruptions/templates/", "/api/disruptions")

# Captures every task can use without a setup step: run_tag keeps topic names
# and specs of one evaluation run apart from every earlier one.
BUILTIN_CAPTURES = ("run_tag",)

ID_RE = re.compile(r"^[a-z0-9]+(-[a-z0-9]+)*$")
NAME_RE = re.compile(r"^[a-z][a-z0-9_]*$")
DATE_RE = re.compile(r"^\d{4}-\d{2}-\d{2}$")
PLACEHOLDER_RE = re.compile(r"\{([a-z_][a-z0-9_]*)\}")
PATH_TOKEN_RE = re.compile(
    r"\.(?P<key>[A-Za-z_][A-Za-z0-9_\-]*)"
    r"|\[(?P<index>-?\d+)\]"
    r"|\[(?P<fkey>[A-Za-z_][A-Za-z0-9_]*)=(?P<fval>[^\]]*)\]"
)

# What a prompt may not contain: it would name a tool, a command or an
# interface, and the prompt is the same for every arm (plan §2.4).
PROMPT_HINTS = {
    "mcp": re.compile(r"\bmcp\b", re.I),
    "cli": re.compile(r"\bcli\b", re.I),
    "/api/": re.compile(r"/api/"),
    "a command flag": re.compile(r"(^|\s)--?[a-z]"),
    "jq": re.compile(r"\bjq\b"),
    "curl": re.compile(r"\bcurl\b"),
    "kubectl": re.compile(r"\bkubectl\b"),
    "a kates command": re.compile(r"\bkates\s+[a-z]"),
}


class TaskFileError(Exception):
    """The task file cannot be read or is not valid."""


class PathError(Exception):
    """A JSON path does not lead to a value in a document."""


class MissingCapture(Exception):
    """A placeholder names a capture that no setup step produced."""


def load(path: str) -> dict:
    """Reads and parses tasks.json; validation is separate (validate)."""
    try:
        with open(path, encoding="utf-8") as f:
            doc = json.load(f)
    except (OSError, ValueError) as e:
        raise TaskFileError(f"{path}: {e}") from e
    if not isinstance(doc, dict):
        raise TaskFileError(f"{path}: the top level must be an object")
    return doc


def tasks_by_id(doc: Mapping[str, Any]) -> dict[str, dict]:
    return {t["id"]: t for t in doc.get("tasks", []) if isinstance(t, dict) and "id" in t}


# --- placeholders ---------------------------------------------------------


def placeholders(value: Any) -> set[str]:
    """Every {name} placeholder in a string, list or object, recursively."""
    found: set[str] = set()
    if isinstance(value, str):
        found.update(PLACEHOLDER_RE.findall(value))
    elif isinstance(value, list):
        for v in value:
            found |= placeholders(v)
    elif isinstance(value, dict):
        for v in value.values():
            found |= placeholders(v)
    return found


def render(value: Any, captures: Mapping[str, Any], missing: str = "error") -> Any:
    """Fills {name} placeholders from captures, recursively.

    A string that is exactly one placeholder becomes the captured value with
    its JSON type, so {"brokerId": "{kill_broker}"} sends a number. Anything
    else is text substitution. Braces that do not form a placeholder, as in
    JSON, are left alone. missing="placeholder" renders an unknown name as
    <name>, which is what a dry run prints before the value exists.
    """
    if isinstance(value, str):
        whole = PLACEHOLDER_RE.fullmatch(value)
        if whole:
            return _lookup(whole.group(1), captures, missing)

        def sub(m: re.Match) -> str:
            v = _lookup(m.group(1), captures, missing)
            return v if isinstance(v, str) else json.dumps(v)

        return PLACEHOLDER_RE.sub(sub, value)
    if isinstance(value, list):
        return [render(v, captures, missing) for v in value]
    if isinstance(value, dict):
        return {k: render(v, captures, missing) for k, v in value.items()}
    return value


def _lookup(name: str, captures: Mapping[str, Any], missing: str) -> Any:
    if name in captures:
        return captures[name]
    if missing == "placeholder":
        return f"<{name}>"
    raise MissingCapture(f"no capture named {name!r}")


# --- JSON paths -----------------------------------------------------------


def json_path(doc: Any, path: str) -> Any:
    """Reads one value: $.key, [n] (negative counts from the end) and
    [key=value], which keeps the objects of a list whose key, as text,
    equals value; follow it with [n] to pick one."""
    if not path.startswith("$"):
        raise PathError(f"{path!r}: a path starts with $")
    rest = path[1:]
    cur = doc
    pos = 0
    while pos < len(rest):
        m = PATH_TOKEN_RE.match(rest, pos)
        if not m:
            raise PathError(f"{path!r}: cannot read from {rest[pos:]!r}")
        pos = m.end()
        if m.group("key") is not None:
            if not isinstance(cur, dict) or m.group("key") not in cur:
                raise PathError(f"{path!r}: no key {m.group('key')!r}")
            cur = cur[m.group("key")]
        elif m.group("index") is not None:
            i = int(m.group("index"))
            if not isinstance(cur, list) or not -len(cur) <= i < len(cur):
                raise PathError(f"{path!r}: no item [{i}]")
            cur = cur[i]
        else:
            if not isinstance(cur, list):
                raise PathError(f"{path!r}: [{m.group('fkey')}=...] needs a list")
            key, want = m.group("fkey"), m.group("fval")
            cur = [x for x in cur if isinstance(x, dict) and key in x and _as_text(x[key]) == want]
    return cur


def _as_text(v: Any) -> str:
    if isinstance(v, bool):
        return "true" if v else "false"
    return str(v)


# --- forbid -----------------------------------------------------------------


def forbidden_hits(command: str, prefixes: Iterable[str]) -> list[str]:
    """The forbidden prefixes a shell command uses, wherever they appear.

    Deny rules match command strings, so metrics.forbidden_uses reads every
    command the agent ran with this. The command is split on pipes, lists,
    subshells and substitutions (kates_commands.split_commands). A kates
    prefix matches by command path, with aliases resolved and global flags
    skipped, so "cd x && kates --context lab cx list" matches "kates chaos";
    any other prefix matches the words of a simple command after assignments
    and wrappers (env, nohup, xargs, ...).
    """
    kates_calls = kates_commands.kates_argvs(command)
    simple = [_unwrap(words) for words in kates_commands.split_commands(command)]
    hits: list[str] = []
    for prefix in prefixes:
        want = prefix.split()
        if not want or prefix in hits:
            continue
        if want[0] == "kates":
            hit = any(kates_commands.starts_with(args, tuple(want[1:])) for args in kates_calls)
        else:
            hit = any(words and words[0].rsplit("/", 1)[-1] == want[0] and words[1:len(want)] == want[1:]
                      for words in simple)
        if hit:
            hits.append(prefix)
    return hits


_WRAPPERS = ("env", "exec", "command", "time", "nohup", "sudo", "xargs")


def _unwrap(words: list[str]) -> list[str]:
    """A simple command without leading VAR=value assignments and wrappers."""
    i = 0
    while i < len(words) and (re.match(r"^[A-Za-z_][A-Za-z0-9_]*=", words[i]) or words[i] in _WRAPPERS):
        i += 1
    return words[i:]


# --- validation -------------------------------------------------------------


def validate(doc: Mapping[str, Any], oracle_fns: Iterable[str] | None = None) -> list[str]:
    """Every problem with a task file, as messages; empty when it is valid.

    Beyond shapes and types it checks what a typo would silently break: each
    placeholder has a producer, each check names an answer field, a task that
    uses another names one that exists and does not loop, and a prompt names
    no tool, command or interface.
    """
    errs: list[str] = []
    if doc.get("version") != 1:
        errs.append("version must be 1")
    if not isinstance(doc.get("fixed_at"), str) or not DATE_RE.match(doc["fixed_at"]):
        errs.append("fixed_at must be an ISO date (YYYY-MM-DD)")
    tasks = doc.get("tasks")
    if not isinstance(tasks, list) or not tasks:
        return errs + ["tasks must be a non-empty list"]
    known = set(oracle_fns) if oracle_fns is not None else None
    ids = [t.get("id") for t in tasks if isinstance(t, dict)]
    seen: set[str] = set()
    for i in ids:
        if i in seen:
            errs.append(f"task id {i!r} is used twice")
        seen.add(i)
    by_id = tasks_by_id(doc)
    for n, t in enumerate(tasks):
        where = f"tasks[{n}]"
        if not isinstance(t, dict):
            errs.append(f"{where}: must be an object")
            continue
        where = f"task {t.get('id', n)!r}"
        errs += [f"{where}: {e}" for e in _validate_task(t, by_id, known)]
    errs += _validate_uses(by_id)
    return errs


def _validate_task(t: Mapping[str, Any], by_id: Mapping[str, dict], known: set[str] | None) -> list[str]:
    errs: list[str] = []
    required = ("id", "persona", "prompt", "answer_fields", "setup", "oracle", "checks",
                "required_caveats", "arms", "trials", "timeout_s", "notes")
    for key in required:
        if key not in t:
            errs.append(f"missing {key}")
    if errs:
        return errs
    allowed = set(required) | {"forbid"}
    for key in t:
        if key not in allowed:
            errs.append(f"unknown key {key!r}")
    if not isinstance(t["id"], str) or not ID_RE.match(t["id"]):
        errs.append("id must match [a-z0-9]+(-[a-z0-9]+)*")
    if t["persona"] not in PERSONAS:
        errs.append(f"persona must be one of {', '.join(PERSONAS)}")
    errs += _validate_prompt(t["prompt"])
    fields = _validate_fields(t["answer_fields"], errs)
    errs += _validate_setup(t["setup"], by_id)
    errs += _validate_oracle(t["oracle"], known)
    errs += _validate_checks(t["checks"], fields)
    errs += _validate_caveats(t["required_caveats"])
    if not isinstance(t["arms"], list) or not t["arms"] or any(a not in ARMS for a in t["arms"]):
        errs.append(f"arms must be a non-empty list of {', '.join(ARMS)}")
    elif len(set(t["arms"])) != len(t["arms"]):
        errs.append("arms lists an arm twice")
    if not isinstance(t["trials"], int) or isinstance(t["trials"], bool) or t["trials"] < 3:
        errs.append("trials must be an integer of at least 3 (plan §2.4)")
    if not isinstance(t["timeout_s"], int) or isinstance(t["timeout_s"], bool) or t["timeout_s"] <= 0:
        errs.append("timeout_s must be a positive integer")
    if not isinstance(t["notes"], str) or len(t["notes"].strip()) < 40:
        errs.append("notes must say what a good answer contains")
    if "forbid" in t:
        errs += _validate_forbid(t["forbid"], t.get("arms", []))
    errs += _validate_setup_order(t, by_id)
    produced = set(BUILTIN_CAPTURES) | _captures_visible(t, by_id)
    for where, value in (("prompt", t["prompt"]), ("oracle.args", t["oracle"].get("args", {}) if isinstance(t["oracle"], dict) else {})):
        for name in sorted(placeholders(value) - produced):
            errs.append(f"{where} uses {{{name}}}, which no setup step captures")
    return errs


def _validate_setup_order(t: Mapping[str, Any], by_id: Mapping[str, dict]) -> list[str]:
    """A step may use only what the built-ins, the tasks used so far and the
    steps before it captured."""
    errs = []
    produced = set(BUILTIN_CAPTURES)
    steps = t.get("setup") if isinstance(t.get("setup"), list) else []
    for n, s in enumerate(steps):
        if not isinstance(s, dict):
            continue
        if s.get("kind") == "use":
            if s.get("task") in by_id:
                produced |= _captures_visible(by_id[s["task"]], by_id)
            continue
        used = placeholders([s.get(k) for k in ("args", "path", "body", "since", "left", "right")])
        used |= placeholders(list((s.get("expect") or {}).values()))
        for name in sorted(used - produced):
            errs.append(f"setup[{n}] uses {{{name}}} before any step captures it")
        produced |= set((s.get("capture") or {}).keys())
    return errs


def _validate_prompt(prompt: Any) -> list[str]:
    if not isinstance(prompt, str) or not prompt.strip():
        return ["prompt must be non-empty text"]
    errs = []
    for what, pattern in PROMPT_HINTS.items():
        if pattern.search(prompt):
            errs.append(f"prompt hints at a tool or interface ({what})")
    for tool in MCP_TOOLS:
        if tool in prompt.lower():
            errs.append(f"prompt names the tool {tool!r}")
    return errs


def _validate_fields(fields: Any, errs: list[str]) -> dict[str, str]:
    out: dict[str, str] = {}
    if not isinstance(fields, list) or not fields:
        errs.append("answer_fields must be a non-empty list")
        return out
    for f in fields:
        if not isinstance(f, dict) or set(f) != {"name", "type", "description"}:
            errs.append("each answer field has exactly name, type and description")
            continue
        if not isinstance(f["name"], str) or not NAME_RE.match(f["name"]):
            errs.append(f"answer field name {f['name']!r} must be snake_case")
        elif f["name"] in out:
            errs.append(f"answer field {f['name']!r} is listed twice")
        if f["type"] not in ANSWER_TYPES:
            errs.append(f"answer field {f['name']!r}: type must be one of {', '.join(ANSWER_TYPES)}")
        if not isinstance(f["description"], str) or len(f["description"]) < 10:
            errs.append(f"answer field {f['name']!r}: describe what it holds")
        out[str(f["name"])] = f["type"]
    return out


def _validate_checks(checks: Any, fields: Mapping[str, str]) -> list[str]:
    if not isinstance(checks, list) or not checks:
        return ["checks must be a non-empty list"]
    errs = []
    checked = set()
    for c in checks:
        if not isinstance(c, dict):
            errs.append("each check must be an object")
            continue
        field, kind = c.get("field"), c.get("kind")
        if field not in fields:
            errs.append(f"check on {field!r}, which is not an answer field")
            continue
        checked.add(field)
        if kind not in CHECK_KINDS:
            errs.append(f"check on {field!r}: kind must be one of {', '.join(CHECK_KINDS)}")
            continue
        extra = set(c) - {"field", "kind", "tolerance", "tolerance_kind"}
        if extra:
            errs.append(f"check on {field!r}: unknown keys {sorted(extra)}")
        ftype = fields[field]
        if kind == "number":
            if ftype != "number":
                errs.append(f"check on {field!r}: kind number needs a number field")
            tol = c.get("tolerance")
            if not isinstance(tol, (int, float)) or isinstance(tol, bool) or tol < 0:
                errs.append(f"check on {field!r}: kind number needs a tolerance of 0 or more")
            if c.get("tolerance_kind") not in TOLERANCE_KINDS:
                errs.append(f"check on {field!r}: tolerance_kind must be abs or pct")
        else:
            if "tolerance" in c or "tolerance_kind" in c:
                errs.append(f"check on {field!r}: only kind number takes a tolerance")
            if kind == "set" and ftype != "string[]":
                errs.append(f"check on {field!r}: kind set needs a string[] field")
            if kind == "contains" and ftype != "string":
                errs.append(f"check on {field!r}: kind contains needs a string field")
            if kind == "exact" and ftype in ("number", "string[]"):
                errs.append(f"check on {field!r}: use kind number or set for a {ftype} field")
    for name in fields:
        if name not in checked:
            errs.append(f"answer field {name!r} has no check")
    return errs


def _validate_caveats(caveats: Any) -> list[str]:
    if not isinstance(caveats, list):
        return ["required_caveats must be a list"]
    errs, ids = [], set()
    for c in caveats:
        if not isinstance(c, dict) or set(c) != {"id", "any_of"}:
            errs.append("each required caveat has exactly id and any_of")
            continue
        if not isinstance(c["id"], str) or not ID_RE.match(c["id"]):
            errs.append(f"caveat id {c['id']!r} must be lowercase-with-dashes")
        if c["id"] in ids:
            errs.append(f"caveat {c['id']!r} is listed twice")
        ids.add(c["id"])
        phrases = c["any_of"]
        if not isinstance(phrases, list) or not phrases or not all(isinstance(p, str) and p.strip() for p in phrases):
            errs.append(f"caveat {c['id']!r}: any_of must be a non-empty list of phrases")
        elif any(p != p.lower() for p in phrases):
            errs.append(f"caveat {c['id']!r}: phrases are matched case-insensitively; write them in lower case")
    return errs


def _validate_oracle(oracle: Any, known: set[str] | None) -> list[str]:
    if not isinstance(oracle, dict) or set(oracle) - {"fn", "args"} or "fn" not in oracle:
        return ["oracle must be {fn, args}"]
    errs = []
    if known is not None and oracle["fn"] not in known:
        errs.append(f"oracle fn {oracle['fn']!r} is not in oracle.py")
    if not isinstance(oracle.get("args", {}), dict):
        errs.append("oracle.args must be an object")
    return errs


def _validate_forbid(forbid: Any, arms: list[str]) -> list[str]:
    if not isinstance(forbid, dict) or not forbid or set(forbid) - set(ARMS):
        return ["forbid must be an object with mcp and/or cli lists"]
    errs = []
    for tool in forbid.get("mcp", []):
        if tool not in MCP_TOOLS:
            errs.append(f"forbid.mcp: {tool!r} is not a kates mcp tool")
    for prefix in forbid.get("cli", []):
        if not isinstance(prefix, str) or not prefix.strip() or prefix != prefix.strip():
            errs.append(f"forbid.cli: {prefix!r} must be a command prefix")
    for arm in forbid:
        if not isinstance(forbid[arm], list) or not forbid[arm]:
            errs.append(f"forbid.{arm} must be a non-empty list")
        if arm not in arms:
            errs.append(f"forbid.{arm} names an arm the task does not run in")
    return errs


def _validate_setup(steps: Any, by_id: Mapping[str, dict]) -> list[str]:
    if not isinstance(steps, list):
        return ["setup must be a list"]
    errs = []
    for n, s in enumerate(steps):
        where = f"setup[{n}]"
        if not isinstance(s, dict) or s.get("kind") not in STEP_KINDS:
            errs.append(f"{where}: kind must be one of {', '.join(STEP_KINDS)}")
            continue
        kind = s["kind"]
        allowed = {
            "kates": {"kind", "args", "capture", "expect", "repeat", "allow_fail", "timeout_s"},
            "api": {"kind", "method", "path", "body", "capture", "expect", "timeout_s", "why"},
            "wait": {"kind", "seconds", "since"},
            "clock": {"kind", "capture"},
            "use": {"kind", "task"},
            "assert_equal": {"kind", "left", "right", "message"},
        }[kind]
        extra = set(s) - allowed
        if extra:
            errs.append(f"{where}: unknown keys {sorted(extra)} for kind {kind}")
        if kind == "kates":
            args = s.get("args")
            if not isinstance(args, list) or not args or not all(isinstance(a, str) for a in args):
                errs.append(f"{where}: args must be a non-empty list of strings")
            else:
                for bad in ("--context", "--url", "--api-key"):
                    if any(a == bad or a.startswith(bad + "=") for a in args):
                        errs.append(f"{where}: setup adds the context itself; do not pass {bad}")
                if (s.get("capture") or s.get("expect")) and "json" not in args:
                    errs.append(f"{where}: a step that captures or expects must ask for -o json")
            if "repeat" in s and (not isinstance(s["repeat"], int) or s["repeat"] < 1):
                errs.append(f"{where}: repeat must be a positive integer")
        elif kind == "api":
            if s.get("method") not in ("GET", "POST"):
                errs.append(f"{where}: method must be GET or POST")
            path = s.get("path", "")
            if not isinstance(path, str) or not path.startswith("/api/"):
                errs.append(f"{where}: path must start with /api/")
            elif s.get("method") == "POST" and not any(
                path == p or (p.endswith("/") and path.startswith(p)) for p in SETUP_API_POSTS
            ):
                errs.append(f"{where}: setup may POST only to {', '.join(SETUP_API_POSTS)}")
            if not isinstance(s.get("why"), str) or not s["why"].strip():
                errs.append(f"{where}: say why this step cannot use the kates binary")
        elif kind == "wait":
            if not isinstance(s.get("seconds"), (int, float)) or s["seconds"] < 0:
                errs.append(f"{where}: seconds must be 0 or more")
        elif kind == "clock":
            cap = s.get("capture")
            if not isinstance(cap, dict) or not cap or any(v not in CLOCK_FORMATS for v in cap.values()):
                errs.append(f"{where}: capture maps names to one of {', '.join(CLOCK_FORMATS)}")
        elif kind == "use":
            if s.get("task") not in by_id:
                errs.append(f"{where}: uses unknown task {s.get('task')!r}")
        elif kind == "assert_equal":
            if not all(isinstance(s.get(k), str) for k in ("left", "right", "message")):
                errs.append(f"{where}: left, right and message must be text")
        for name in (s.get("capture") or {}) if kind in ("kates", "api", "clock") else ():
            if not NAME_RE.match(name) or name in BUILTIN_CAPTURES:
                errs.append(f"{where}: cannot capture into {name!r}")
        if kind in ("kates", "api"):
            for name, path in (s.get("capture") or {}).items():
                if not isinstance(path, str) or not path.startswith("$"):
                    errs.append(f"{where}: capture {name!r} needs a JSON path starting with $")
    return errs


def _validate_uses(by_id: Mapping[str, dict]) -> list[str]:
    """A task whose setup reaches itself through use steps never finishes."""
    errs = []
    for tid in by_id:
        stack, seen = list(uses(by_id[tid])), set()
        while stack:
            cur = stack.pop()
            if cur == tid:
                errs.append(f"task {tid!r}: its setup uses itself")
                break
            if cur in seen:
                continue
            seen.add(cur)
            stack.extend(uses(by_id.get(cur, {})))
    return errs


def uses(task: Mapping[str, Any]) -> list[str]:
    """The tasks whose setup this task's setup builds on, in order."""
    return [s["task"] for s in task.get("setup", []) if isinstance(s, dict) and s.get("kind") == "use"]


def _captures_visible(task: Mapping[str, Any], by_id: Mapping[str, dict], _seen: set[str] | None = None) -> set[str]:
    seen = _seen if _seen is not None else set()
    if task.get("id") in seen:
        return set()
    seen.add(task.get("id"))
    names: set[str] = set()
    for s in task.get("setup", []):
        if not isinstance(s, dict):
            continue
        if s.get("kind") == "use" and s.get("task") in by_id:
            names |= _captures_visible(by_id[s["task"]], by_id, seen)
        elif s.get("kind") in ("kates", "api", "clock"):
            names |= set((s.get("capture") or {}).keys())
    return names


def captures_for(state: Mapping[str, Any], task_id: str, by_id: Mapping[str, dict]) -> dict[str, Any]:
    """The captures a task's prompt and oracle may use: the built-ins, then
    those of every task its setup uses, then its own. Later wins."""
    out: dict[str, Any] = {k: state[k] for k in BUILTIN_CAPTURES if k in state}
    order: list[str] = []

    def walk(tid: str) -> None:
        if tid in order:
            return
        for dep in uses(by_id.get(tid, {})):
            walk(dep)
        order.append(tid)

    walk(task_id)
    for tid in order:
        out.update(((state.get("tasks") or {}).get(tid) or {}).get("captures") or {})
    return out

"""Grade every trial of a run: task success, grounded claims, silent misreads.

Plan §2.4 measures three things about each answer, and this script computes
them the same way for every arm:

1. Task success. Each task's checks compare the answer block with the
   expected answer the oracle computed from the Kates API (oracle.json):
   exact, number (with an abs or pct tolerance), set, or contains. A trial
   succeeds when every check passes. A trial that used a tool or command its
   task forbade fails, because the answer may have leaked.
2. Grounded-claim rate: the share of numbers in the answer (prose and JSON)
   that appear in some tool result of the same trial, or in the prompt. The
   hard gate is 95 % for the MCP arm (§2.5). The rule, and what it cannot
   see, is under "Grounded claims" below.
3. Silent misreads: an answer that never mentions a required caveat. Each
   required_caveats entry lists phrases; the answer must contain at least one
   (case-insensitive, whitespace and dashes normalised). The hard gate is 0.
   A trial that gave no answer at all (out of turns, timed out) has failed
   its task, but misleads nobody, so it is not a misread.

The automatic grade is a first pass. The plan asks for a grader who does not
know the arm, so this script also writes grading.csv, one row per answer,
arm hidden and rows in a seeded random order, with empty human_success,
human_misread and human_notes columns, and grading-key.csv, which maps each
row back to its arm and trial and stays closed until the human pass is done.
Rerunning the script keeps the row ids and whatever the grader has entered.
When eval/mcp/runs/<run>/expert.csv exists, the expert arm's answers join
the same blinded sheet.

Grounded claims
---------------
A claim is a number written as a number in the answer's prose or in its
JSON block: 12, -3, 1,234, 12.5, 12.5%, 1.2e7, and a number with a unit
directly attached (12ms, 1.5x, 100k, 250MB/s; k and M multiply). Not claims:
dates, clock times, versions and IP addresses (2026-09-25, 14:02, 3.20.6),
identifiers with digits in them (payments-0, p99, run a1b2c3d4), numbered
list markers, and anything inside code (fenced blocks other than the answer
block, and `inline code`), which is a command or an identifier rather than a
statement about the cluster.

A claim is grounded when a number in some tool result of the trial, or in
the prompt, equals it at the precision it is written with: 12.35 matches
12.3456, 12 matches 11.6, 1.2k matches 1234. A percent also matches its
fraction and the other way round (12.5% and 0.125), signs are ignored, the
thousands separator is optional (1,234 and 1234), and a number converted
between milliseconds, seconds and minutes still matches (27.9 s for
27912 ms). The numbers of a tool result are its numeric JSON values (and the
claims in its strings) when it is JSON, else its whole-token numbers by the
rule above, so the digits inside ids, timestamps and versions are not
references.

What the rule cannot see: a number derived correctly from tool results (a
difference, a ratio, a sum) counts as ungrounded, however right it is, and a
number that is wrong but happens to equal some other number in a tool result
counts as grounded; small integers are grounded almost always for that
reason. Numbers spelled as words ("three brokers") are not seen. The
ungrounded numbers of each answer are listed in grading.csv, and the human
grader enters in human_ungrounded how many of them no tool result supports,
derived ones included; report.py then uses that count.
"""

from __future__ import annotations

import argparse
import bisect
import csv
import json
import math
import random
import re
import sys
import unicodedata
from dataclasses import dataclass
from pathlib import Path
from typing import Any

import metrics
import taskfile
import tasks as tasklib

HERE = Path(__file__).resolve().parent
DEFAULT_RUNS = HERE / "runs"

HUMAN_COLUMNS = ("human_success", "human_misread", "human_ungrounded", "human_notes")
CSV_COLUMNS = (
    "row_id", "task_id", "persona", "prompt", "expected", "answer_json", "answer_text",
    "auto_checks", "auto_success", "missing_caveats", "ungrounded_numbers",
) + HUMAN_COLUMNS
KEY_COLUMNS = ("row_id", "task_id", "arm", "trial", "trial_dir")
EXPERT_COLUMNS = ("task_id", "trial", "answer_json", "answer_text", "wall_clock_s", "commands", "notes")


# ---------------------------------------------------------------------------
# Numbers


@dataclass(frozen=True)
class Claim:
    raw: str
    value: float
    tolerance: float
    percent: bool


_MULTIPLIERS = {"k": 1e3, "K": 1e3, "M": 1e6}
_UNITS = {
    "%", "ms", "s", "sec", "secs", "m", "min", "mins", "h", "hr", "hrs", "d",
    "x", "×", "B", "KB", "MB", "GB", "TB", "KiB", "MiB", "GiB", "TiB",
    "/s", "rec/s", "records/s", "msg/s", "msgs/s", "ops/s", "B/s", "KB/s", "MB/s", "GB/s",
    "k", "K", "M", "k/s", "K/s", "M/s",
}
_TOKEN_RE = re.compile(r"[A-Za-z0-9_.,:%/+×\-]+")
_NUM_RE = re.compile(
    r"^(?P<sign>[-+])?(?P<int>\d{1,3}(?:,\d{3})+|\d+)(?P<frac>\.\d+)?"
    r"(?:[eE](?P<exp>[-+]?\d+))?(?P<unit>[A-Za-z%×/]+(?:/[A-Za-z]+)?)?$"
)
_PAIR_RE = re.compile(r"^(\d[\d,.]*)[-/](\d[\d,.]*)$")
_LIST_MARKER_RE = re.compile(r"^(\s*)\d+[.)](\s+)", re.M)
_INLINE_CODE_RE = re.compile(r"`[^`\n]*`")
_URL_RE = re.compile(r"\b[a-z][a-z0-9+.-]*://\S+", re.I)


def _parse_claim(tok: str, percent_after: bool) -> Claim | None:
    m = _NUM_RE.match(tok)
    if not m:
        return None
    unit = m.group("unit") or ""
    if unit and unit not in _UNITS:
        return None
    digits = m.group("int").replace(",", "")
    frac = m.group("frac") or ""
    decimals = len(frac) - 1 if frac else 0
    exp = int(m.group("exp") or 0)
    multiplier = _MULTIPLIERS.get(unit.split("/")[0], 1.0)
    value = float(digits + frac) * (10 ** exp) * multiplier
    tolerance = 0.5 * 10 ** (-decimals) * (10 ** exp) * multiplier
    percent = unit == "%" or percent_after
    if m.group("sign") == "-":
        value = -value
    return Claim(tok, value, tolerance, percent)


def prose_of(text: str) -> str:
    """The answer without code, URLs and list markers: what claims are read from."""
    for _, _, start, end in reversed(metrics.fenced_blocks(text)):
        text = text[:start] + "\n" + text[end:]
    text = _INLINE_CODE_RE.sub(" ", text)
    text = _URL_RE.sub(" ", text)
    return _LIST_MARKER_RE.sub(r"\1\2", text)


def claims_in(text: str) -> list[Claim]:
    """Numbers stated in a piece of text, by the rule in the module docstring."""
    out: list[Claim] = []
    for m in _TOKEN_RE.finditer(text):
        tok = m.group(0).rstrip(".,:;/-").lstrip(",.:;/")
        if not tok or not any(ch.isdigit() for ch in tok):
            continue
        percent_after = bool(re.match(r"[ \t]?%", text[m.end():m.end() + 2]))
        pair = _PAIR_RE.match(tok)
        parts = [pair.group(1), pair.group(2)] if pair else [tok]
        for i, part in enumerate(parts):
            claim = _parse_claim(part.rstrip(".,"), percent_after and i == len(parts) - 1)
            if claim is not None:
                out.append(claim)
    return out


def answer_claims(prose: str, answer_text: str | None = None) -> list[Claim]:
    """Claims in the prose of the first turn and in the answer block (of the
    answer turn, or of the prose when there is no answer turn)."""
    claims = claims_in(prose_of(prose))
    block = metrics.answer_block(answer_text if answer_text is not None else prose)
    if block is not None:
        claims += claims_in(re.sub(r'"[A-Za-z0-9_]+"\s*:', " ", block[0]))
    return claims


def _json_numbers(value: Any, out: set[float]) -> None:
    """Numbers in a parsed JSON value: numeric values, and the claims in its
    strings (a caveat's prose, a number sent as text)."""
    if isinstance(value, bool) or value is None:
        return
    if isinstance(value, (int, float)):
        out.add(abs(float(value)))
    elif isinstance(value, str):
        _text_numbers(value, out)
    elif isinstance(value, list):
        for v in value:
            _json_numbers(v, out)
    elif isinstance(value, dict):
        for v in value.values():
            _json_numbers(v, out)


def _text_numbers(text: str, out: set[float]) -> None:
    """Numbers in text by the same rule as claims: whole tokens only, so the
    digits inside an id, a timestamp or a version are not numbers."""
    for c in claims_in(text):
        out.add(abs(c.value))
        if c.percent:
            out.add(abs(c.value) / 100)


def _parse_json(text: str) -> tuple[bool, Any]:
    try:
        return True, json.loads(text)
    except (json.JSONDecodeError, ValueError):
        pass
    # One JSON value per line, as jq -c prints them.
    values = []
    for line in text.splitlines():
        if not line.strip():
            continue
        try:
            values.append(json.loads(line))
        except (json.JSONDecodeError, ValueError):
            return False, None
    return bool(values), values


def reference_numbers(texts: list[str]) -> list[float]:
    """Every number in the tool results (and prompt), sorted: the numeric
    values of a result that is JSON, or the whole-token numbers of one that is
    text. A percent also counts as its fraction."""
    values: set[float] = set()
    for text in texts:
        is_json, doc = _parse_json(text.strip())
        if is_json:
            _json_numbers(doc, values)
        else:
            _text_numbers(text, values)
    return sorted(v for v in values if math.isfinite(v))


def _near(sorted_values: list[float], target: float, tol: float) -> bool:
    eps = 1e-9 * max(1.0, abs(target))
    lo = bisect.bisect_left(sorted_values, target - tol - eps)
    return lo < len(sorted_values) and sorted_values[lo] <= target + tol + eps


# A number converted between milliseconds, seconds and minutes is the same
# number: disruption_report gives recovery in ms, a debrief states it in s.
UNIT_FACTORS = (1.0, 1000.0, 1 / 1000, 60.0, 1 / 60)


def grounded(claim: Claim, reference: list[float]) -> bool:
    v = abs(claim.value)
    for f in UNIT_FACTORS:
        if _near(reference, v * f, claim.tolerance * f):
            return True
        if claim.percent and _near(reference, v * f / 100, claim.tolerance * f / 100):
            return True
    return False


# ---------------------------------------------------------------------------
# Caveats

_DASHES = dict.fromkeys(map(ord, "‐‑‒–—−"), "-")
_QUOTES = {ord("‘"): "'", ord("’"): "'", ord("“"): '"', ord("”"): '"'}


def normalize_text(s: str) -> str:
    s = unicodedata.normalize("NFKC", s).translate(_DASHES).translate(_QUOTES)
    return re.sub(r"\s+", " ", s).strip().casefold()


def missing_caveats(caveats: list[dict[str, Any]], text: str) -> list[str]:
    norm = normalize_text(text)
    return [c["id"] for c in caveats if not any(normalize_text(p) in norm for p in c["any_of"])]


# ---------------------------------------------------------------------------
# Checks


def _norm(x: Any) -> str:
    return re.sub(r"\s+", " ", str(x)).strip().casefold()


def _as_number(x: Any) -> float | None:
    if isinstance(x, bool):
        return None
    if isinstance(x, (int, float)):
        return float(x)
    if isinstance(x, str):
        try:
            return float(x.strip().rstrip("%").replace(",", ""))
        except ValueError:
            return None
    return None


def _as_bool(x: Any) -> bool | None:
    if isinstance(x, bool):
        return x
    if isinstance(x, str) and _norm(x) in ("true", "false", "yes", "no"):
        return _norm(x) in ("true", "yes")
    return None


def _as_list(x: Any) -> list[str] | None:
    if isinstance(x, list):
        return [_norm(v) for v in x]
    return None


def compare(kind: str, expected: Any, actual: Any, tolerance: float = 0, tolerance_kind: str = "abs") -> tuple[bool, str]:
    """(passed, detail) for one check."""
    if actual is None:
        return (expected is None and kind == "exact"), "the answer has no value"
    if kind == "number":
        a, e = _as_number(actual), _as_number(expected)
        if a is None or e is None:
            return False, f"not a number: {actual!r}"
        allowed = tolerance if tolerance_kind == "abs" else abs(e) * tolerance / 100
        diff = abs(a - e)
        return diff <= allowed + 1e-9 * max(1.0, abs(e)), f"off by {diff:g}, allowed {allowed:g}"
    if kind == "set":
        a, e = _as_list(actual), _as_list(expected)
        if a is None or e is None:
            return False, "not a list"
        missing, extra = sorted(set(e) - set(a)), sorted(set(a) - set(e))
        ok = not missing and not extra
        return ok, "" if ok else f"missing {missing}, unexpected {extra}"
    if kind == "contains":
        if isinstance(expected, list):
            items = [_norm(v) for v in expected]
        else:
            items = [_norm(expected)]
        if isinstance(actual, list):
            have = {_norm(v) for v in actual}
            missing = [i for i in items if i not in have]
        else:
            text = _norm(actual)
            missing = [i for i in items if i not in text]
        return not missing, "" if not missing else f"missing {missing}"
    # exact
    if expected is None:
        return False, "expected null"
    if isinstance(expected, bool):
        a = _as_bool(actual)
        return a is expected, "" if a is expected else f"expected {expected}"
    if isinstance(expected, (int, float)):
        a = _as_number(actual)
        ok = a is not None and abs(a - float(expected)) <= 1e-9 * max(1.0, abs(float(expected)))
        return ok, "" if ok else f"expected {expected}"
    if isinstance(expected, list):
        a = _as_list(actual)
        ok = a == [_norm(v) for v in expected]
        return ok, "" if ok else "lists differ"
    ok = _norm(actual) == _norm(expected)
    return ok, "" if ok else f"expected {expected!r}"


def run_checks(task: dict[str, Any], expected: dict[str, Any] | None, answer: dict[str, Any] | None) -> list[dict[str, Any]]:
    results = []
    for c in task["checks"]:
        field = c["field"]
        entry: dict[str, Any] = {"field": field, "kind": c["kind"]}
        if expected is None or field not in expected:
            entry.update(passed=None, detail="the oracle has no expected value")
        elif answer is None:
            entry.update(expected=expected[field], actual=None, passed=False, detail="no answer block")
        else:
            ok, detail = compare(c["kind"], expected[field], answer.get(field),
                                 c.get("tolerance", 0), c.get("tolerance_kind", "abs"))
            entry.update(expected=expected[field], actual=answer.get(field), passed=ok, detail=detail)
        results.append(entry)
    return results


def success_of(checks: list[dict[str, Any]], forbidden: list[str]) -> bool | None:
    """True, False, or None when there is nothing automatic to decide on."""
    if forbidden:
        return False
    if not checks or any(c["passed"] is None for c in checks):
        return None
    return all(c["passed"] for c in checks)


# ---------------------------------------------------------------------------
# One trial


def grade_answer(task: dict[str, Any], expected: dict[str, Any] | None, prose: str, answer_text: str | None,
                 answer: dict[str, Any] | None, references: list[str] | None,
                 forbidden: list[str]) -> dict[str, Any]:
    """prose is the first turn's answer, which the caveats are read from
    (without its code and JSON, so a field name cannot stand in for a
    caveat); answer is the JSON object the checks read."""
    checks = run_checks(task, expected, answer)
    missing = missing_caveats(task["required_caveats"], prose_of(prose))
    answered = bool(prose.strip())
    out: dict[str, Any] = {
        "checks": checks,
        "auto_success": success_of(checks, forbidden),
        "answered": answered,
        "missing_caveats": missing,
        # No answer at all fails the task; it misleads nobody.
        "silent_misread": bool(missing) and answered,
        "forbidden_used": forbidden,
    }
    if references is None:
        out.update(numbers_total=None, numbers_grounded=None, ungrounded=[])
    else:
        ref = reference_numbers(references)
        claims = answer_claims(prose, answer_text)
        ungrounded = [c.raw for c in claims if not grounded(c, ref)]
        out.update(numbers_total=len(claims), numbers_grounded=len(claims) - len(ungrounded),
                   ungrounded=ungrounded)
    return out


def trial_dirs(run_dir: Path) -> list[Path]:
    """Every trial directory of a run that finished (has metrics.json), not
    counting invalid trials set aside by run.py --redo-invalid."""
    return sorted(p.parent for p in run_dir.glob("*/*/trial-*/metrics.json")
                  if re.fullmatch(r"trial-\d+", p.parent.name))


def grade_trial(trial_dir: Path, task: dict[str, Any], expected: dict[str, Any] | None) -> dict[str, Any]:
    m = json.loads((trial_dir / "metrics.json").read_text())
    t = metrics.load(trial_dir / "transcript.jsonl")
    final = metrics.final_text(t)
    answer_path = trial_dir / "answer.jsonl"
    answer_text = metrics.final_text(metrics.load(answer_path)) if answer_path.exists() else None
    answer, _ = metrics.extract_answer(answer_text if answer_text is not None else final)
    prompt = (trial_dir / "prompt.txt").read_text() if (trial_dir / "prompt.txt").exists() else ""
    graded = grade_answer(task, expected, final, answer_text, answer, metrics.tool_texts(t) + [prompt],
                          m.get("forbidden_used", []))
    valid = m.get("valid", m.get("status") in metrics.AGENT_STATUSES)
    return {
        "task_id": task["id"],
        "persona": task["persona"],
        "arm": m.get("arm", trial_dir.parent.name),
        "trial": m.get("trial", int(trial_dir.name.split("-")[-1])),
        "trial_dir": str(trial_dir),
        "status": m.get("status"),
        "valid": bool(valid),
        "answer": answer,
        "final_text": final,
        "tool_calls": m.get("tool_calls"),
        "tokens_total": (m.get("tokens") or {}).get("total"),
        "tokens": m.get("tokens"),
        "first_turn_context": m.get("first_turn_context"),
        "cost_usd": m.get("cost_usd"),
        "wall_clock_s": m.get("wall_clock_s"),
        "num_turns": m.get("num_turns"),
        "mutating_calls": m.get("mutating_calls", []),
        **graded,
    }


# ---------------------------------------------------------------------------
# The expert arm


def read_expert(path: Path, by_id: dict[str, dict[str, Any]], expected: dict[str, Any]) -> list[dict[str, Any]]:
    """Grade the expert's answers (expert.csv, filled in by hand)."""
    rows = []
    with open(path, newline="", encoding="utf-8") as fh:
        for n, row in enumerate(csv.DictReader(fh), start=2):
            tid = (row.get("task_id") or "").strip()
            if not tid:
                continue
            if tid not in by_id:
                raise ValueError(f"{path}:{n}: unknown task_id {tid!r}")
            task = by_id[tid]
            raw = (row.get("answer_json") or "").strip()
            answer = None
            if raw:
                try:
                    parsed = json.loads(raw)
                    answer = parsed if isinstance(parsed, dict) else None
                except json.JSONDecodeError:
                    answer = None
            text = row.get("answer_text") or ""
            graded = grade_answer(task, expected.get(tid), text, None, answer, None, [])
            rows.append({
                "task_id": tid, "persona": task["persona"], "arm": "expert",
                "trial": int(row.get("trial") or 1), "trial_dir": f"{path.name}:{n}",
                "status": "ok", "valid": True, "answer": answer, "final_text": text,
                "tool_calls": _as_number(row.get("commands")),
                "tokens_total": None, "tokens": None, "first_turn_context": None, "cost_usd": None,
                "wall_clock_s": _as_number(row.get("wall_clock_s")), "num_turns": None,
                "mutating_calls": [], **graded,
            })
    return rows


# ---------------------------------------------------------------------------
# The blinded sheet


def _read_csv(path: Path) -> list[dict[str, str]]:
    if not path.exists():
        return []
    with open(path, newline="", encoding="utf-8") as fh:
        return list(csv.DictReader(fh))


def _checks_text(checks: list[dict[str, Any]]) -> str:
    parts = []
    for c in checks:
        verdict = {True: "PASS", False: "FAIL", None: "?"}[c["passed"]]
        detail = f" ({c['detail']})" if c.get("detail") and c["passed"] is not True else ""
        parts.append(f"{c['field']}: {verdict}{detail}")
    return "; ".join(parts)


def rendered_prompts(run_dir: Path, by_id: dict[str, dict[str, Any]]) -> dict[str, str]:
    """Each task's prompt as the arms saw it, captures filled in from the
    run's state.json (a capture setup never produced shows as <name>)."""
    state_path = run_dir / "state.json"
    state = json.loads(state_path.read_text()) if state_path.exists() else {"tasks": {}}
    return {tid: taskfile.render(t["prompt"], taskfile.captures_for(state, tid, by_id), "placeholder")
            for tid, t in by_id.items()}


_TOOL_NAMES_RE = re.compile(r"\b(?:mcp__\w+|" + "|".join(taskfile.MCP_TOOLS) + r")\b")
_KATES_COMMAND_RE = re.compile(r"\bkates(?:\s+--?[\w-]+(?:[= ][\w./:-]+)?)*(?:\s+[a-z][\w-]*){1,3}")


def mask_for_grader(text: str) -> str:
    """The answer as the blinded grader sees it: code, commands and tool
    names replaced, since they name the arm. The prose, numbers and caveats
    stay."""
    for _, _, start, end in reversed(metrics.fenced_blocks(text)):
        text = text[:start] + "[code block]" + text[end:]
    text = _INLINE_CODE_RE.sub("[code]", text)
    text = _TOOL_NAMES_RE.sub("[tool]", text)
    return _KATES_COMMAND_RE.sub("[command]", text).strip()


def write_sheets(run_dir: Path, rows: list[dict[str, Any]], by_id: dict[str, dict[str, Any]],
                 expected: dict[str, Any], seed: str) -> tuple[Path, Path]:
    """Write grading.csv (blinded) and grading-key.csv, keeping existing row
    ids and anything the human grader already entered."""
    sheet, key_path = run_dir / "grading.csv", run_dir / "grading-key.csv"
    old_key = {(r["task_id"], r["arm"], r["trial"]): r["row_id"] for r in _read_csv(key_path)}
    human = {r["row_id"]: {c: r.get(c, "") for c in HUMAN_COLUMNS} for r in _read_csv(sheet)}
    rng = random.Random(f"{seed}:grading")
    prompts = rendered_prompts(run_dir, by_id)
    used = set(old_key.values())
    key_rows, sheet_rows = [], []
    for r in rows:
        ident = (r["task_id"], r["arm"], str(r["trial"]))
        row_id = old_key.get(ident)
        while row_id is None or row_id in used and ident not in old_key:
            row_id = f"{rng.getrandbits(32):08x}"
        used.add(row_id)
        task = by_id[r["task_id"]]
        key_rows.append({"row_id": row_id, "task_id": r["task_id"], "arm": r["arm"],
                         "trial": r["trial"], "trial_dir": r["trial_dir"]})
        sheet_rows.append({
            "row_id": row_id, "task_id": r["task_id"], "persona": task["persona"],
            "prompt": prompts[r["task_id"]],
            "expected": json.dumps(expected.get(r["task_id"]), sort_keys=True),
            "answer_json": json.dumps(r["answer"], sort_keys=True) if r["answer"] is not None else "",
            "answer_text": mask_for_grader(r["final_text"]),
            "auto_checks": _checks_text(r["checks"]),
            "auto_success": {True: "yes", False: "no", None: ""}[r["auto_success"]],
            "missing_caveats": ", ".join(r["missing_caveats"]),
            "ungrounded_numbers": ", ".join(r["ungrounded"]),
            **human.get(row_id, dict.fromkeys(HUMAN_COLUMNS, "")),
        })
    # Sorting by the random row id is the shuffle: seeded, and stable when
    # rows are added later.
    sheet_rows.sort(key=lambda r: r["row_id"])
    key_rows.sort(key=lambda r: r["row_id"])
    for path, columns, data in ((sheet, CSV_COLUMNS, sheet_rows), (key_path, KEY_COLUMNS, key_rows)):
        with open(path, "w", newline="", encoding="utf-8") as fh:
            writer = csv.DictWriter(fh, fieldnames=columns)
            writer.writeheader()
            writer.writerows(data)
    return sheet, key_path


# ---------------------------------------------------------------------------
# The run


def oracle_drift(run_dir: Path) -> dict[str, list[str]]:
    """Fields whose expected value changed between oracle.json and the
    recheck run after the trials (run.py oracle --recheck)."""
    first, again = run_dir / "oracle.json", run_dir / "oracle-recheck.json"
    if not first.exists() or not again.exists():
        return {}
    a = json.loads(first.read_text()).get("tasks", {})
    b = json.loads(again.read_text()).get("tasks", {})
    drift = {}
    for tid, entry in a.items():
        ea, eb = entry.get("expected") or {}, (b.get(tid) or {}).get("expected") or {}
        fields = sorted(k for k in set(ea) | set(eb) if ea.get(k) != eb.get(k))
        if tid in b and fields:
            drift[tid] = fields
    return drift


def void_tasks(oracle: dict[str, Any], drift: dict[str, list[str]], missing: list[str]) -> dict[str, str]:
    """Tasks the comparison leaves out, and why: no expected answer (the
    oracle failed, or never ran), or an expected answer that moved while the
    trials ran, so an answer may have been right when it was given
    (TASKS.md, "Order, and facts that move")."""
    out = {}
    for tid in missing:
        error = (oracle.get(tid) or {}).get("error")
        out[tid] = f"no expected answer ({error})" if error else "no expected answer"
    for tid, fields in drift.items():
        out[tid] = f"the expected {', '.join(fields)} changed during the run"
    return dict(sorted(out.items()))


def grade_run(run_dir: Path, seed: str | None = None) -> dict[str, Any]:
    doc = tasklib.load_tasks(run_dir / "tasks.json")
    by_id = tasklib.task_by_id(doc)
    oracle_path = run_dir / "oracle.json"
    oracle = json.loads(oracle_path.read_text()).get("tasks", {}) if oracle_path.exists() else {}
    expected = {tid: e.get("expected") for tid, e in oracle.items() if isinstance(e, dict)}
    manifest_path = run_dir / "manifest.json"
    manifest = json.loads(manifest_path.read_text()) if manifest_path.exists() else {}
    seed = seed or str(manifest.get("seed", "0"))
    rows = []
    for d in trial_dirs(run_dir):
        tid = d.parent.parent.name
        if tid not in by_id:
            continue
        rows.append(grade_trial(d, by_id[tid], expected.get(tid)))
    expert_path = run_dir / "expert.csv"
    expert_rows = read_expert(expert_path, by_id, expected) if expert_path.exists() else []
    graded_rows = [r for r in rows if r["valid"]] + expert_rows
    write_sheets(run_dir, graded_rows, by_id, expected, seed)
    drift = oracle_drift(run_dir)
    missing = sorted({r["task_id"] for r in rows if expected.get(r["task_id"]) is None})
    result = {
        "run_id": run_dir.name,
        "seed": seed,
        "missing_oracle": missing,
        "oracle_drift": drift,
        "void_tasks": void_tasks(oracle, drift, missing),
        "trials": rows,
        "expert": expert_rows,
    }
    (run_dir / "grades.json").write_text(json.dumps(result, indent=2, default=str) + "\n")
    return result


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(
        description="Grade a run: checks against oracle.json, grounded claims, missed caveats; "
                    "write grades.json, the blinded grading.csv and grading-key.csv.")
    parser.add_argument("--run-id", required=True)
    parser.add_argument("--runs-dir", type=Path, default=DEFAULT_RUNS)
    parser.add_argument("--seed", help="seed for the row order (default: the run's seed)")
    args = parser.parse_args(argv)
    run_dir = args.runs_dir / args.run_id
    if not (run_dir / "tasks.json").exists():
        print(f"{run_dir} is not a run directory (no tasks.json); run run.py first", file=sys.stderr)
        return 2
    try:
        result = grade_run(run_dir, args.seed)
    except (tasklib.TaskError, ValueError) as e:
        print(f"cannot grade {run_dir}: {e}", file=sys.stderr)
        return 2
    trials = result["trials"]
    valid = [t for t in trials if t["valid"]]
    print(f"graded {len(valid)} valid trials ({len(trials) - len(valid)} invalid, not scored) "
          f"and {len(result['expert'])} expert answers")
    print(f"wrote {run_dir / 'grades.json'}, {run_dir / 'grading.csv'} (blinded), "
          f"{run_dir / 'grading-key.csv'} (keep closed until the human pass is done)")
    for tid, why in result["void_tasks"].items():
        print(f"void: {tid}: {why}; left out of the comparison", file=sys.stderr)
    if result["missing_oracle"]:
        print(f"no expected answer for: {', '.join(result['missing_oracle'])}; run: run.py oracle",
              file=sys.stderr)
        return 3
    return 0


if __name__ == "__main__":
    sys.exit(main())

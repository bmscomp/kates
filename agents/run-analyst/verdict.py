"""The Run Analyst's answer: its schema, the verdict rules, and the checks an
answer must pass before anything is posted (plans/autonomous-agents.md §2.3, §2.4).

The model reads and explains; the verdict is re-derived here from the answer's
own evidence fields, and an answer whose verdict disagrees is refused.
"""

import re

VERDICTS = ("INVALID_RUN", "CONTAMINATED", "NOT_COMPARABLE", "REGRESSED", "IMPROVED", "NORMAL")
POSITIONS = ("below", "within", "above")
KINDS = ("latency", "throughput", "other")

# Verbs that would start load, inject a fault or change the lab: `next` is a
# read-only command for a person, never one of these.
MUTATING_WORDS = {"create", "run", "apply", "delete", "cancel", "start", "stop", "approve", "inject"}

_STR = {"type": "string"}
_NULLABLE_STR = {"type": ["string", "null"]}
# A caveat is named by its id, as kates mcp results name it: never free text,
# which could carry numbers the headline then counts as grounded.
CAVEAT_ID = r"^[a-z0-9]+(-[a-z0-9]+)*$"

SCHEMA = {
    "type": "object",
    "additionalProperties": False,
    "required": [
        "runId", "testType", "status", "verdict", "headline", "taskErrors", "recordsLost",
        "baseline", "band", "overlaps", "brokerSkew", "caveats", "evidence", "next",
    ],
    "properties": {
        "runId": _STR,
        "testType": _STR,
        "status": {"type": "string", "enum": ["DONE", "FAILED"]},
        "verdict": {"type": "string", "enum": list(VERDICTS)},
        "headline": _STR,
        "taskErrors": {"type": "array", "items": _STR},
        "recordsLost": {"type": ["integer", "null"]},
        "baseline": {
            "type": ["object", "null"],
            "additionalProperties": False,
            "required": ["runId", "sameSpec", "regressionFlagged"],
            "properties": {
                "runId": _NULLABLE_STR,
                "sameSpec": {"type": "boolean"},
                "regressionFlagged": {"type": "boolean"},
            },
        },
        "band": {
            "type": "object",
            "additionalProperties": False,
            "required": ["runs", "metrics"],
            "properties": {
                "runs": {"type": "integer"},
                "metrics": {
                    "type": "array",
                    "items": {
                        "type": "object",
                        "additionalProperties": False,
                        "required": ["name", "kind", "value", "min", "max", "position"],
                        "properties": {
                            "name": _STR,
                            "kind": {"type": "string", "enum": list(KINDS)},
                            "value": {"type": "number"},
                            "min": {"type": "number"},
                            "max": {"type": "number"},
                            "position": {"type": "string", "enum": list(POSITIONS)},
                        },
                    },
                },
            },
        },
        "overlaps": {
            "type": "array",
            "items": {
                "type": "object",
                "additionalProperties": False,
                "required": ["kind", "id"],
                "properties": {"kind": {"type": "string", "enum": ["run", "disruption"]}, "id": _STR},
            },
        },
        "brokerSkew": _NULLABLE_STR,
        "caveats": {"type": "array", "items": {"type": "string", "pattern": CAVEAT_ID}},
        "evidence": {
            "type": "array",
            "items": {
                "type": "object",
                "additionalProperties": False,
                "required": ["claim", "tool", "field"],
                "properties": {"claim": _STR, "tool": _STR, "field": _STR},
            },
        },
        "next": _NULLABLE_STR,
    },
}


def derive(a: dict) -> str:
    """The verdict the rules give for the answer's evidence; first match wins."""
    if a["status"] == "FAILED" or a["taskErrors"] or (a["recordsLost"] or 0) > 0:
        return "INVALID_RUN"
    if a["overlaps"]:
        return "CONTAMINATED"
    b = a["baseline"] or {}
    same_spec = bool(b.get("runId")) and b.get("sameSpec", False)
    band = a["band"]
    if not same_spec and band["runs"] < 3:
        return "NOT_COMPARABLE"
    metrics = band["metrics"]
    latency_up = any(m["kind"] == "latency" and m["position"] == "above" for m in metrics)
    latency_down = any(m["kind"] == "latency" and m["position"] == "below" for m in metrics)
    throughput_down = any(m["kind"] == "throughput" and m["position"] == "below" for m in metrics)
    if latency_up or throughput_down or (same_spec and b.get("regressionFlagged", False)):
        return "REGRESSED"
    if latency_down:
        return "IMPROVED"
    return "NORMAL"


# A number standing on its own: not part of a word or a hex id like 3fa9c2e1.
_NUMBER = re.compile(r"(?<![\w.])-?\d+(?:,\d{3})*(?:\.\d+)?(?![\w])")


def numbers_in(text: str) -> list[str]:
    return _NUMBER.findall(text or "")


def _field_numbers(value, out: list[float]) -> None:
    if isinstance(value, bool) or value is None:
        return
    if isinstance(value, (int, float)):
        out.append(float(value))
    elif isinstance(value, str):
        out.extend(float(n.replace(",", "")) for n in numbers_in(value))
    elif isinstance(value, list):
        for v in value:
            _field_numbers(v, out)
    elif isinstance(value, dict):
        for v in value.values():
            _field_numbers(v, out)


def _grounded(raw: str, fields: list[float]) -> bool:
    n = float(raw.replace(",", ""))
    decimals = len(raw.split(".")[1]) if "." in raw else 0
    # A field rounded to the shown precision counts: 4.83 may be written 4.8.
    return any(round(f, decimals) == n or abs(f - n) < 1e-9 for f in fields)


def _shape_problems(a, schema=SCHEMA, at="answer") -> list[str]:
    """A small JSON Schema subset check, enough for SCHEMA: no dependency."""
    types = schema.get("type")
    types = types if isinstance(types, list) else [types]
    checks = {
        "object": lambda v: isinstance(v, dict),
        "array": lambda v: isinstance(v, list),
        "string": lambda v: isinstance(v, str),
        "integer": lambda v: isinstance(v, int) and not isinstance(v, bool),
        "number": lambda v: isinstance(v, (int, float)) and not isinstance(v, bool),
        "boolean": lambda v: isinstance(v, bool),
        "null": lambda v: v is None,
    }
    if not any(checks[t](a) for t in types):
        return [f"{at} must be {' or '.join(types)}"]
    if "enum" in schema and a not in schema["enum"]:
        return [f"{at} must be one of {', '.join(schema['enum'])}"]
    if "pattern" in schema and isinstance(a, str) and not re.match(schema["pattern"], a):
        return [f"{at} must be a caveat id, lowercase words joined by hyphens, not {a!r}"]
    out = []
    if isinstance(a, dict):
        for key in schema.get("required", []):
            if key not in a:
                out.append(f"{at}.{key} is missing")
        for key, value in a.items():
            if key not in schema.get("properties", {}):
                out.append(f"{at}.{key} is not a field of the answer")
            else:
                out.extend(_shape_problems(value, schema["properties"][key], f"{at}.{key}"))
    elif isinstance(a, list) and "items" in schema:
        for i, item in enumerate(a):
            out.extend(_shape_problems(item, schema["items"], f"{at}[{i}]"))
    return out


def problems(a) -> list[str]:
    """Why this answer must not be posted; empty when it may be."""
    shape = _shape_problems(a)
    if shape:
        return shape
    out = []
    expected = derive(a)
    if a["verdict"] != expected:
        out.append(f"verdict is {a['verdict']} but the evidence fields give {expected} by the rules")
    fields: list[float] = []
    # Caveat ids name no figures, so they ground nothing either.
    _field_numbers({k: v for k, v in a.items() if k not in ("headline", "next", "evidence", "caveats")}, fields)
    for name in ("headline", "next"):
        for raw in numbers_in(a[name]):
            if not _grounded(raw, fields):
                out.append(f"{name} says {raw}, which no structured field holds")
    if a["next"] is not None:
        words = set(re.findall(r"[a-z-]+", a["next"].lower()))
        bad = sorted(words & MUTATING_WORDS)
        if bad:
            out.append(f"next must be a read-only command, but it says {', '.join(bad)}")
    return out

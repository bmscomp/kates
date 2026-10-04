"""Read CLI contexts without PyYAML, and write a trial's one-context config.

The harness talks to Kates in two roles, and keeps them apart:

- The human: setup steps (kates commands) and the oracle (REST calls) run
  with the human context, so the ground truth never depends on what the
  agent's context can see.
- The agent: each trial gets a throwaway HOME whose ~/.kates.yaml holds one
  context, built from the agent context. Until the backend has scoped keys
  (plan Phase 2) the agent context carries the same shared key as the human
  one; the run records that, and the README says why the agent arm belongs on
  a lab.

Contexts are read with `kates ctx export --name <ctx> --reveal`, which prints
the context as YAML in the one shape gopkg.in/yaml.v3 writes for the CLI's
Config struct. parse_ctx_export reads exactly that shape (plain, single- and
double-quoted scalars), so the harness needs no YAML library. The trial's
~/.kates.yaml is written as JSON, which the CLI's YAML parser reads as is.

The harness reaches the API itself with oracle.KatesAPI, built from a
context's URL, key, proxy-url and insecure fields.
"""

from __future__ import annotations

import json
import os
import subprocess
from dataclasses import dataclass
from typing import Any

# Variables that would make a kates command ignore the context it is given.
KATES_OVERRIDE_ENV = ("KATES_URL", "KATES_API_KEY", "KATES_CONTEXT", "KATES_OUTPUT")


class ContextError(RuntimeError):
    """A CLI context is missing or cannot be read."""


@dataclass(frozen=True)
class KatesContext:
    name: str
    url: str
    api_key: str = ""
    proxy_url: str = ""
    insecure: bool = False

    def masked(self) -> str:
        key = f"{self.api_key[:4]}…" if self.api_key else "(none)"
        return f"{self.name} → {self.url} (key {key})"


def _scalar(raw: str) -> str:
    raw = raw.strip()
    if raw.startswith('"'):
        try:
            return json.loads(raw)
        except json.JSONDecodeError as e:
            raise ContextError(f"cannot read the quoted value {raw[:20]!r}…: {e}") from e
    if raw.startswith("'"):
        if not raw.endswith("'") or len(raw) < 2:
            raise ContextError(f"unterminated quoted value: {raw!r}")
        return raw[1:-1].replace("''", "'")
    if raw in ("|", "|-", ">", ">-"):
        raise ContextError("block scalars are not expected in a kates context")
    return raw


def parse_ctx_export(text: str, name: str) -> KatesContext:
    """Read one context from the YAML `kates ctx export --name <name>` prints."""
    fields: dict[str, str] = {}
    in_contexts = False
    ctx_indent: int | None = None
    in_target = False
    for line in text.splitlines():
        if not line.strip() or line.lstrip().startswith("#"):
            continue
        indent = len(line) - len(line.lstrip(" "))
        stripped = line.strip()
        if indent == 0:
            in_contexts = stripped == "contexts:"
            in_target = False
            continue
        if not in_contexts:
            continue
        key, sep, value = stripped.partition(":")
        if not sep:
            continue
        if ctx_indent is None or indent <= ctx_indent:
            ctx_indent = indent
            in_target = _scalar(key) == name and not value.strip()
            continue
        if in_target:
            fields[key.strip()] = _scalar(value) if value.strip() else ""
    if "url" not in fields:
        raise ContextError(f"context {name!r} not found in the kates config")
    return KatesContext(
        name=name,
        url=fields["url"],
        api_key=fields.get("api-key", ""),
        proxy_url=fields.get("proxy-url", ""),
        insecure=fields.get("insecure", "false").lower() == "true",
    )


def clean_env(base: dict[str, str] | None = None) -> dict[str, str]:
    """The environment without the variables that override a kates context."""
    env = dict(os.environ if base is None else base)
    for key in KATES_OVERRIDE_ENV:
        env.pop(key, None)
    return env


def read_context(kates_bin: str, name: str, env: dict[str, str] | None = None) -> KatesContext:
    """Read a context, key included, from the operator's ~/.kates.yaml."""
    try:
        proc = subprocess.run(
            [kates_bin, "ctx", "export", "--name", name, "--reveal"],
            env=clean_env(env), capture_output=True, text=True, timeout=60, stdin=subprocess.DEVNULL,
        )
    except (OSError, subprocess.TimeoutExpired) as e:
        raise ContextError(f"cannot run {kates_bin} ctx export: {e}") from e
    # ctx export exits 0 and prints nothing when the context does not exist.
    if proc.returncode != 0 or not proc.stdout.strip():
        detail = proc.stderr.strip().splitlines()[-1] if proc.stderr.strip() else "no output"
        raise ContextError(f"context {name!r} is not in the kates config ({detail}); "
                           f"create it with: kates ctx set {name} --url <url> --api-key <key>")
    return parse_ctx_export(proc.stdout, name)


def trial_config_json(ctx: KatesContext, trial_name: str) -> str:
    """The trial's ~/.kates.yaml: one context, current, as JSON (valid YAML)."""
    entry: dict[str, Any] = {"url": ctx.url, "output": "table"}
    if ctx.api_key:
        entry["api-key"] = ctx.api_key
    if ctx.proxy_url:
        entry["proxy-url"] = ctx.proxy_url
    if ctx.insecure:
        entry["insecure"] = True
    return json.dumps({"current-context": trial_name, "contexts": {trial_name: entry}}, indent=2) + "\n"

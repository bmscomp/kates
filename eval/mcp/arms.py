"""How each agent arm runs Claude Code, and what it may use.

Both agent arms run the same Claude Code build with the same model, turn
limit, timeout, prompt, protocol setting and permission mode. Only the
interface to Kates differs (plans/mcp-server.md §2.4):

- mcp: `kates mcp --context <trial context> --allow-cluster <clusterId>` is
  the only MCP server (stdio, --strict-mcp-config), loaded up front
  (alwaysLoad) so its tool definitions are in context from the first turn,
  and its tools are the only tools: --tools "" removes every built-in tool,
  so there is no shell, file or web access.
- cli: no MCP server at all. --tools keeps Bash, Read, Write and Edit; the
  permission rules let Bash run only kates and jq, and the file tools touch
  only the trial's own working directory, because the skill's game-day recipe
  writes plan.json before `kates disruption run --config plan.json --dry-run`
  (the MCP arm passes the same plan inline to preview_disruption). The skill,
  skills/kates-cli/SKILL.md, is appended to the system prompt.

Why the skill goes in the system prompt rather than .claude/skills: the MCP
arm's tool descriptions and caveats are in context from the first turn
without the model asking for them. A project skill is loaded only when the
model decides to invoke it, which would measure skill discovery as well as
the interface, and a missed invocation would count against the CLI. Putting
the skill in the system prompt gives the CLI arm the same standing start,
which is the fair (and, for the MCP arm, the harder) comparison. The context
it costs is counted in the CLI arm's tokens, as the tool definitions are in
the MCP arm's. --skill-mode project is there to test the other reading.

Every trial is two turns (tasks.py): the question, then, resuming the same
session with every tool denied, the request for the answer as JSON.

Every trial runs with --permission-mode dontAsk and --permission-prompts none:
anything no allow rule covers is refused, never prompted for. The trial's
HOME and working directory are a fresh temporary directory outside the
repository, so no CLAUDE.md, settings, memory or git status leaks in, and
HOME/.kates.yaml holds only the agent's context (arms never see the human
context). The child environment is built from an allowlist: no KATES_* (a
KATES_API_KEY would replace the context's key, and kates mcp refuses
KATES_URL), no KUBECONFIG, no cloud credentials.

This module builds the command line, environment, MCP config and settings of
one trial as plain data, so run.py --dry-run prints exactly what a real run
executes and the tests check it without running claude. check_init compares
the session Claude Code actually started (the transcript's init event) with
what the arm should have, so a settings file Claude Code ignored, or a server
that failed to start, invalidates the trial instead of skewing it.
"""

from __future__ import annotations

import json
import shlex
import sys
from dataclasses import dataclass, field
from pathlib import Path
from typing import Any

import kates_commands
import metrics
import taskfile

MCP_SERVER = "kates"
MCP_PREFIX = metrics.MCP_PREFIX
PERMISSION_MODE = "dontAsk"
NEGOTIATION_CHOICES = ("legacy", "auto")
SDK_GENERATIONS = ("v1", "v2")
SKILL_MODES = ("system-prompt", "project")
CLI_TOOLS = ("Bash", "Read", "Write", "Edit")
MCP_RESOURCE_TOOLS = ("ListMcpResourcesTool", "ReadMcpResourceTool")

# Built-in tools that reach a shell, the file system, the web or other
# agents. The MCP arm must have none; the CLI arm only CLI_TOOLS.
SHELL_FILE_WEB_TOOLS = frozenset({
    "Bash", "BashOutput", "KillBash", "KillShell", "PowerShell", "REPL",
    "Read", "Write", "Edit", "MultiEdit", "NotebookEdit", "NotebookRead",
    "Glob", "Grep", "LS", "WebFetch", "WebSearch", "Task", "Agent", "Monitor",
})

# The only variables a trial inherits from the operator: Claude Code's own
# credentials and network settings, and the locale. Everything else is set
# by the harness or left out.
PASSTHROUGH_ENV = (
    "ANTHROPIC_API_KEY", "CLAUDE_CODE_OAUTH_TOKEN", "ANTHROPIC_AUTH_TOKEN", "ANTHROPIC_BASE_URL",
    "HTTPS_PROXY", "HTTP_PROXY", "NO_PROXY", "https_proxy", "http_proxy", "no_proxy",
    "SSL_CERT_FILE", "NODE_EXTRA_CA_CERTS", "LANG", "LC_ALL", "USER", "LOGNAME", "SHELL",
)
# Claude Code's credentials: a trial needs one, and records none of them.
AUTH_ENV = ("ANTHROPIC_API_KEY", "CLAUDE_CODE_OAUTH_TOKEN", "ANTHROPIC_AUTH_TOKEN")
PROXY_ENV = ("HTTPS_PROXY", "HTTP_PROXY", "https_proxy", "http_proxy")
SYSTEM_PATH = "/usr/bin:/bin:/usr/sbin:/sbin"

SKILL_HEADER = (
    "The kates-cli skill below is loaded for this session. Follow it whenever you use the kates CLI.\n\n"
)


@dataclass(frozen=True)
class ArmSettings:
    """What every trial of a run shares, whichever arm it is."""

    claude_bin: str
    kates_bin: str
    model: str
    max_turns: int
    protocol_negotiation: str
    sdk_generation: str
    trial_context: str
    cluster_id: str
    cluster_label: str = "lab"
    max_budget_usd: float | None = None
    skill_text: str = ""
    skill_mode: str = "system-prompt"
    mcp_resources: bool = False
    jq_bin: str | None = None
    python_bin: str = sys.executable


@dataclass(frozen=True)
class Sandbox:
    """A trial's throwaway HOME and working directory."""

    root: Path

    @property
    def home(self) -> Path:
        return self.root / "home"

    @property
    def work(self) -> Path:
        return self.root / "work"

    @property
    def tmp(self) -> Path:
        return self.root / "tmp"

    @property
    def bin(self) -> Path:
        return self.root / "bin"

    @property
    def lib(self) -> Path:
        return self.root / "lib"

    @property
    def settings(self) -> Path:
        return self.root / "settings.json"

    @property
    def answer_settings(self) -> Path:
        return self.root / "settings-answer.json"

    @property
    def mcp_config(self) -> Path:
        return self.root / "mcp-config.json"

    @property
    def kates_config(self) -> Path:
        return self.home / ".kates.yaml"


@dataclass
class TrialPlan:
    arm: str
    argv: list[str]
    env: dict[str, str]
    cwd: Path
    files: dict[Path, str]
    links: dict[Path, str]
    settings: dict[str, Any]
    mcp_config: dict[str, Any] | None
    forbid_mcp_tools: list[str] = field(default_factory=list)
    forbid_kates_paths: list[tuple[str, ...]] = field(default_factory=list)
    forbid_prefixes: list[str] = field(default_factory=list)


# ---------------------------------------------------------------------------
# Forbidden tools and commands


def forbid_for(task: dict[str, Any], arm: str) -> tuple[list[str], list[str], list[tuple[str, ...]], list[str]]:
    """A task's forbid field for one arm (TASKS.md, "forbid"): (deny rules,
    MCP tool names, kates command paths, other command prefixes).

    forbid.mcp names kates mcp tools; each becomes mcp__kates__<tool>.
    forbid.cli names command prefixes. A kates prefix ("kates disruption")
    becomes a deny rule for every alias form of the path, with and without
    arguments; any other prefix ("kubectl") becomes Bash(<prefix>) and
    Bash(<prefix> *). Deny rules match command strings, so run.py also reads
    every command in the transcript (metrics.forbidden_uses), and a trial that
    got one through fails its task.
    """
    forbid = task.get("forbid") or {}
    rules: list[str] = []
    tools: list[str] = []
    paths: list[tuple[str, ...]] = []
    prefixes: list[str] = []
    for entry in forbid.get(arm, []):
        entry = entry.strip()
        if arm == "mcp":
            tools.append(entry)
            rules.append(MCP_PREFIX + entry)
        elif entry.split()[0] == "kates":
            path = tuple(entry.split()[1:])
            paths.append(path)
            rules.extend(kates_commands.bash_rules(path))
        else:
            prefixes.append(entry)
            rules.extend([f"Bash({entry})", f"Bash({entry} *)"])
    return rules, tools, paths, prefixes


# ---------------------------------------------------------------------------
# Config files and command line


def mcp_config(s: ArmSettings) -> dict[str, Any]:
    """The --mcp-config file of the mcp arm. It holds no key: kates mcp reads
    the key from the trial context in the trial's HOME."""
    return {
        "mcpServers": {
            MCP_SERVER: {
                "type": "stdio",
                "command": s.kates_bin,
                "args": ["mcp", "--context", s.trial_context, "--allow-cluster", s.cluster_id,
                         "--cluster-label", s.cluster_label],
                "alwaysLoad": True,
            }
        }
    }


def settings_for(arm: str, s: ArmSettings, sandbox: Sandbox, forbid_rules: list[str]) -> dict[str, Any]:
    """The --settings file: the arm's permission rules."""
    if arm == "mcp":
        allow = [MCP_PREFIX + "*"]
        deny = list(forbid_rules)
        # Claude Code offers its resource tools whenever a server has
        # resources, as kates mcp does; a resource can hold what a task
        # forbids (a disruption timeline), so they stay off unless asked for.
        (allow if s.mcp_resources else deny).extend(MCP_RESOURCE_TOOLS)
    else:
        work = "/" + str(sandbox.work)  # //absolute/path in rule syntax
        allow = ["Bash(kates *)", "Bash(jq *)",
                 f"Read({work}/**)", f"Write({work}/**)", f"Edit({work}/**)"]
        if s.skill_mode == "project":
            allow.append("Skill")
        deny = kates_commands.default_cli_deny() + list(forbid_rules)
    return {"permissions": {"allow": allow, "deny": list(dict.fromkeys(deny))}}


def tools_flag(arm: str, s: ArmSettings) -> str:
    if arm == "mcp":
        return ",".join(MCP_RESOURCE_TOOLS) if s.mcp_resources else ""
    tools = list(CLI_TOOLS) + (["Skill"] if s.skill_mode == "project" else [])
    return ",".join(tools)


def claude_argv(arm: str, s: ArmSettings, sandbox: Sandbox, prompt: str, *,
                settings_path: Path | None = None, max_turns: int | None = None,
                resume: str | None = None, budget: bool = True) -> list[str]:
    """The claude command line. The prompt comes right after -p, before any
    option that takes a list (--tools, --mcp-config), which would otherwise
    swallow it. The session is kept (in the trial's HOME) so the answer turn
    can resume it."""
    argv = [
        s.claude_bin, "-p", prompt,
        "--output-format", "stream-json", "--verbose",
        "--model", s.model,
        "--max-turns", str(max_turns or s.max_turns),
        "--permission-mode", PERMISSION_MODE,
        "--permission-prompts", "none",
        "--settings", str(settings_path or sandbox.settings),
        "--strict-mcp-config",
    ]
    if arm == "mcp":
        argv += ["--mcp-config", str(sandbox.mcp_config)]
    argv += ["--tools", tools_flag(arm, s)]
    if arm == "cli" and s.skill_mode == "system-prompt":
        argv += ["--append-system-prompt", SKILL_HEADER + s.skill_text]
    if s.max_budget_usd is not None and budget:
        argv += ["--max-budget-usd", f"{s.max_budget_usd:g}"]
    if resume:
        argv += ["--resume", resume]
    return argv


def child_env(s: ArmSettings, sandbox: Sandbox, parent_env: dict[str, str]) -> dict[str, str]:
    env = {k: parent_env[k] for k in PASSTHROUGH_ENV if parent_env.get(k)}
    env.update({
        "HOME": str(sandbox.home),
        "TMPDIR": str(sandbox.tmp),
        "PATH": f"{sandbox.bin}:{SYSTEM_PATH}",
        # Plan §8.4: set explicitly, and name the era in the report. The
        # runtime is pinned too, because the negotiation setting only acts
        # on the v2 runtime, and which runtime a session gets otherwise
        # depends on feature flags.
        "MCP_PROTOCOL_NEGOTIATION": s.protocol_negotiation,
        "MCP_SDK_GENERATION": s.sdk_generation,
        # Start the first turn only once the server is connected.
        "MCP_CONNECTION_NONBLOCKING": "0",
        "DISABLE_AUTOUPDATER": "1",
    })
    return env


# ---------------------------------------------------------------------------
# The CLI arm's kates and jq
#
# Deny rules match command strings, so `kates --context lab test create ...`
# passes Bash(kates test create *), and the commands with a --dry-run form
# are allowed so the skill's dry runs work. The CLI arm's PATH therefore holds
# two small wrappers instead of the binaries. kates refuses what
# kates_commands.refusal names (a call that would change the lab as written, a
# view that never returns, kates mcp, the task's forbidden commands); jq reads
# only its standard input, so it cannot open the run's expected answers or
# setup output. Both run the real binary with a minimal environment, so no
# credential of Claude Code's reaches a command's output. A refusal prints
# kates_commands.REFUSED_MARKER, which metrics.py counts as a denied call.

# Variables the wrapped binaries keep: what kates needs to reach the API as
# kates mcp does in the other arm (its proxy and CA settings), and no
# credential of Claude Code's.
WRAPPED_ENV = ("HOME", "PATH", "TMPDIR", "LANG", "LC_ALL", "HTTPS_PROXY", "HTTP_PROXY", "NO_PROXY",
               "https_proxy", "http_proxy", "no_proxy", "SSL_CERT_FILE")

KATES_WRAPPER = """#!{python}
# The kates of the MCP evaluation's CLI arm (eval/mcp/arms.py).
import os, sys
sys.dont_write_bytecode = True
sys.path.insert(0, {lib!r})
import kates_commands
args = sys.argv[1:]
reason = kates_commands.refusal(args, [tuple(p) for p in {forbidden!r}])
if reason:
    words = " ".join(kates_commands.command_words(args)) or "(no command)"
    sys.stderr.write(kates_commands.REFUSED_MARKER + " kates " + words + " " + reason + ".\\n")
    sys.exit(126)
env = {{k: v for k, v in os.environ.items() if k in {keep!r}}}
os.execve({real!r}, [{real!r}] + args, env)
"""

JQ_WRAPPER = """#!{python}
# The jq of the MCP evaluation's CLI arm (eval/mcp/arms.py): standard input only.
import os, re, sys
MARKER = {marker!r}
FILE_OPTIONS = ("-f", "--from-file", "--slurpfile", "--rawfile", "-L", "--library-path")
VALUE_OPTIONS = {{"--arg": 2, "--argjson": 2, "--indent": 1}}
args = sys.argv[1:]
program, files, values, i, plain = None, [], False, 0, False
while i < len(args):
    a = args[i]
    if a == "--" and not plain:
        plain = True
    elif a.startswith("-") and a != "-" and not plain:
        name = a.split("=", 1)[0]
        short = not a.startswith("--")
        if name in FILE_OPTIONS or name.startswith("-L") or (short and ("f" in a or "L" in a)):
            files.append(a)
        if name in ("--args", "--jsonargs"):
            values = True
        i += VALUE_OPTIONS.get(name, 0)
    elif program is None:
        program = a
    elif not values and a != "-":
        files.append(a)
    i += 1
if files or (program and re.search(r"\\b(import|include)\\b", program)):
    sys.stderr.write(MARKER + " jq reads only its standard input in this session.\\n")
    sys.exit(126)
env = {{k: v for k, v in os.environ.items() if k in {keep!r}}}
os.execve({real!r}, [{real!r}] + args, env)
"""


def cli_wrappers(s: ArmSettings, sandbox: Sandbox, forbidden: list[tuple[str, ...]]) -> dict[Path, str]:
    """The CLI arm's bin/kates and bin/jq, and the copy of kates_commands.py
    the kates wrapper imports (so nothing in the sandbox points at the repo)."""
    files = {
        sandbox.lib / "kates_commands.py": Path(kates_commands.__file__).read_text(encoding="utf-8"),
        sandbox.bin / "kates": KATES_WRAPPER.format(python=s.python_bin, lib=str(sandbox.lib),
                                                    forbidden=[list(p) for p in forbidden],
                                                    keep=list(WRAPPED_ENV), real=s.kates_bin),
    }
    if s.jq_bin:
        files[sandbox.bin / "jq"] = JQ_WRAPPER.format(python=s.python_bin, marker=kates_commands.REFUSED_MARKER,
                                                      keep=list(WRAPPED_ENV), real=s.jq_bin)
    return files


def plan_trial(arm: str, task: dict[str, Any], s: ArmSettings, sandbox: Sandbox, prompt: str,
               parent_env: dict[str, str]) -> TrialPlan:
    rules, tools, paths, prefixes = forbid_for(task, arm)
    settings = settings_for(arm, s, sandbox, rules)
    files: dict[Path, str] = {sandbox.settings: json.dumps(settings, indent=2) + "\n"}
    config = None
    if arm == "mcp":
        config = mcp_config(s)
        files[sandbox.mcp_config] = json.dumps(config, indent=2) + "\n"
    if arm == "cli" and s.skill_mode == "project":
        files[sandbox.work / ".claude" / "skills" / "kates-cli" / "SKILL.md"] = s.skill_text
    links: dict[Path, str] = {}
    if arm == "cli":
        files.update(cli_wrappers(s, sandbox, paths))
    else:
        links[sandbox.bin / "kates"] = s.kates_bin
    return TrialPlan(
        arm=arm,
        argv=claude_argv(arm, s, sandbox, prompt),
        env=child_env(s, sandbox, parent_env),
        cwd=sandbox.work,
        files=files,
        links=links,
        settings=settings,
        mcp_config=config,
        forbid_mcp_tools=tools,
        forbid_kates_paths=paths,
        forbid_prefixes=prefixes,
    )


# The answer turn resumes the session with the same tools configured (so the
# resumed context is the one the agent worked in) and every one of them denied,
# so the JSON restates the answer rather than extending it.
ANSWER_MAX_TURNS = 2
ANSWER_TIMEOUT_S = 180


def answer_settings(arm: str, s: ArmSettings) -> dict[str, Any]:
    if arm == "mcp":
        deny = [MCP_PREFIX + t for t in taskfile.MCP_TOOLS] + list(MCP_RESOURCE_TOOLS)
    else:
        deny = list(CLI_TOOLS) + ["Skill"]
    return {"permissions": {"allow": [], "deny": deny}}


def plan_answer_turn(first: TrialPlan, s: ArmSettings, sandbox: Sandbox, session_id: str,
                     request: str) -> TrialPlan:
    """The second turn of a trial: the answer request, in the first turn's
    session, with every tool denied."""
    settings = answer_settings(first.arm, s)
    return TrialPlan(
        arm=first.arm,
        # No budget cap: a resumed session carries the first turn's cost, so
        # a cap would leave nothing for the answer after a costly first turn.
        # Two turns and no tools bound it instead.
        argv=claude_argv(first.arm, s, sandbox, request, settings_path=sandbox.answer_settings,
                         max_turns=ANSWER_MAX_TURNS, resume=session_id, budget=False),
        env=first.env,
        cwd=first.cwd,
        files={sandbox.answer_settings: json.dumps(settings, indent=2) + "\n"},
        links={},
        settings=settings,
        mcp_config=first.mcp_config,
    )


# ---------------------------------------------------------------------------
# Records and display


def _mask_userinfo(url: str) -> str:
    """A proxy URL without the user and password it may carry: everything
    between the scheme and the last @, which may itself hold @ or /."""
    scheme, sep, rest = url.partition("://")
    if not sep or "@" not in rest:
        return url
    return f"{scheme}://<redacted>@{rest.rpartition('@')[2]}"


def redact_env(env: dict[str, str]) -> dict[str, str]:
    out = {}
    for k, v in sorted(env.items()):
        out[k] = "<redacted>" if k in AUTH_ENV else _mask_userinfo(v) if k in PROXY_ENV else v
    return out


def recorded_argv(argv: list[str], prompt: str, skill_sha: str) -> list[str]:
    """The command line as the trial record keeps it: the prompt is in
    prompt.txt, and the skill is named by its hash."""
    out = []
    for a in argv:
        if a == prompt:
            out.append("<prompt.txt>")
        elif a.startswith(SKILL_HEADER):
            out.append(f"<SKILL.md sha256:{skill_sha}>")
        else:
            out.append(a)
    return out


def render_command(plan: TrialPlan, prompt: str) -> str:
    """The trial's command as a shell line: env -i, the environment, claude.
    The skill appears as "$SKILL_PROMPT"; secrets as <redacted>."""
    env = " ".join(f"{k}={shlex.quote(v)}" for k, v in redact_env(plan.env).items())
    parts = []
    for a in plan.argv:
        if a.startswith(SKILL_HEADER):
            parts.append('"$SKILL_PROMPT"')
        else:
            parts.append(shlex.quote(a))
    return f"cd {shlex.quote(str(plan.cwd))} && env -i {env} \\\n  " + " ".join(parts)


# ---------------------------------------------------------------------------
# Did Claude Code start the session the arm asked for?


def check_init(init: dict[str, Any] | None, arm: str, s: ArmSettings) -> tuple[list[str], list[str]]:
    """(problems, notes) comparing the transcript's init event with the arm.

    A problem invalidates the trial: the agent did not work under the arm's
    conditions (a shell in the MCP arm, a failed kates server, a permission
    mode Claude Code did not apply). A note records something unexpected but
    harmless, such as a built-in tool that reaches no shell, file or web.
    """
    if init is None:
        return ["the transcript has no init event, so the session never started"], []
    problems: list[str] = []
    notes: list[str] = []
    tools = [str(t) for t in init.get("tools") or []]
    servers = [x for x in init.get("mcp_servers") or [] if isinstance(x, dict)]
    mode = init.get("permissionMode")
    if mode != PERMISSION_MODE:
        problems.append(f"permission mode is {mode!r}, not {PERMISSION_MODE!r}")
    mcp_tools = [t for t in tools if t.startswith("mcp__")]
    if arm == "mcp":
        allowed_builtin = set(MCP_RESOURCE_TOOLS) if s.mcp_resources else set()
        if not any(t.startswith(MCP_PREFIX) for t in tools):
            problems.append("no kates tools were loaded")
        others = [t for t in mcp_tools if not t.startswith(MCP_PREFIX)]
        if others:
            problems.append(f"tools from other MCP servers: {', '.join(sorted(others))}")
        risky = sorted(t for t in tools if t in SHELL_FILE_WEB_TOOLS)
        if risky:
            problems.append(f"shell, file or web tools present: {', '.join(risky)}")
        extra = sorted(t for t in tools if not t.startswith("mcp__") and t not in SHELL_FILE_WEB_TOOLS
                       and t not in allowed_builtin)
        if extra:
            notes.append(f"other built-in tools present: {', '.join(extra)}")
        status = {str(x.get("name")): x.get("status") for x in servers}
        if status.get(MCP_SERVER) != "connected":
            problems.append(f"the kates MCP server is {status.get(MCP_SERVER, 'absent')!r}, not 'connected'")
        other_servers = sorted(n for n in status if n != MCP_SERVER)
        if other_servers:
            problems.append(f"other MCP servers configured: {', '.join(other_servers)}")
    else:
        allowed = set(CLI_TOOLS) | ({"Skill"} if s.skill_mode == "project" else set())
        if mcp_tools or servers:
            problems.append("the CLI arm has MCP servers or tools")
        if "Bash" not in tools:
            problems.append("Bash is not available")
        risky = sorted(t for t in tools if t in SHELL_FILE_WEB_TOOLS and t not in allowed)
        if risky:
            problems.append(f"tools beyond {', '.join(sorted(allowed))}: {', '.join(risky)}")
        extra = sorted(t for t in tools if t not in SHELL_FILE_WEB_TOOLS and t not in allowed
                       and not t.startswith("mcp__"))
        if extra:
            notes.append(f"other built-in tools present: {', '.join(extra)}")
    model = init.get("model")
    if s.model.startswith("claude-") and model and not str(model).startswith(s.model):
        problems.append(f"the session runs {model!r}, not the requested {s.model!r}")
    return problems, notes

"""Which kates commands start load, inject faults, change state or never return,
and how to find them in a shell command line.

The CLI arm drives kates through Claude Code's Bash tool. Two things depend on
knowing what each command does:

- arms.py turns the commands that have no read-only form into deny rules for
  the CLI arm. That protects the lab's ground truth during a run: an agent
  that saved a security baseline or deleted a run would change the expected
  answer of later trials. Deny rules match command strings, so
  `kates --context lab test delete x` slips past `Bash(kates test delete *)`;
  the plan says as much (§2.3, "Shell allowlists match strings"). They are a
  guard, not a boundary.
- metrics.py therefore also reads every kates invocation in a transcript,
  wherever its flags sit and whatever alias it uses, and records the ones that
  would have started load, injected a fault or changed state without
  --dry-run (plan §2.4, metric 5), and the ones a task forbade.

The table follows the "Leave load, faults and changes to the human" table in
skills/kates-cli/SKILL.md, with --dry-run marked where the built CLI has it
(each path and flag checked with `kates <path> --help` on main at 8513b2a).
"""

from __future__ import annotations

import itertools
import shlex
from dataclasses import dataclass

# Global flags of the kates root command (kates --help). The value flags take
# the next argument unless written as --flag=value (or -ojson).
GLOBAL_VALUE_FLAGS = ("--context", "--output", "-o", "--url", "--api-key")
GLOBAL_BOOL_FLAGS = ("--plain", "--help", "-h")

# Aliases from the cobra command definitions in cli/cmd (Aliases: fields),
# keyed by the canonical parent path.
ALIASES: dict[tuple[str, ...], dict[str, str]] = {
    (): {
        "t": "test", "sec": "security", "s": "schedule", "sched": "schedule",
        "context": "ctx", "config": "ctx", "bench": "benchmark", "ci": "gate",
        "quality-gate": "gate", "dash": "dashboard", "r": "report", "cx": "chaos",
        "kyv": "kyverno", "policy": "kyverno", "why": "explain", "interpret": "explain",
    },
    ("test",): {
        "run": "create", "start": "create", "rm": "delete", "ls": "list",
        "show": "get", "inspect": "get",
    },
    ("security",): {"base": "baseline"},
    ("schedule",): {"rm": "delete", "ls": "list"},
    ("webhook",): {"rm": "remove", "delete": "remove"},
}

EFFECTS = ("load", "fault", "change", "live")


@dataclass(frozen=True)
class Rule:
    """One kates command that is not a plain read.

    effect: load, fault, change, or live (a view that refreshes until
    interrupted, which in a headless trial only burns the Bash timeout).
    dry_run_ok: the command has --dry-run, and with it starts nothing.
    needs_flag: the command changes state only with this flag.
    reads_after: next words that make the command a read (migrate plan).
    """

    path: tuple[str, ...]
    effect: str
    dry_run_ok: bool = False
    needs_flag: str | None = None
    reads_after: tuple[str, ...] = ()


def _r(path: str, effect: str, **kw: object) -> Rule:
    return Rule(tuple(path.split()), effect, **kw)  # type: ignore[arg-type]


RULES: tuple[Rule, ...] = (
    # Starts load
    _r("test create", "load", dry_run_ok=True),
    _r("test apply", "load"),
    _r("replay", "load"),
    _r("gate", "load"),
    _r("benchmark", "load"),
    _r("tune run", "load"),
    _r("flow run", "load", dry_run_ok=True),
    _r("schedule create", "load"),
    # Injects a fault
    _r("disruption run", "fault", dry_run_ok=True),
    _r("disruption playbook run", "fault", dry_run_ok=True),
    _r("resilience run", "fault", dry_run_ok=True),
    _r("disruption schedule create", "fault"),
    # Changes or deletes state
    _r("test delete", "change"),
    _r("test cleanup", "change", dry_run_ok=True),
    _r("test baseline set", "change"),
    _r("test baseline unset", "change"),
    # Without --save the command only prints how to use it.
    _r("security baseline", "change", needs_flag="--save"),
    _r("profile save", "change"),
    _r("snapshot create", "change"),
    _r("kafka create-topic", "change", dry_run_ok=True),
    _r("kafka alter-topic", "change", dry_run_ok=True),
    _r("kafka delete-topic", "change", dry_run_ok=True),
    _r("kafka produce", "change"),
    _r("schedule delete", "change"),
    _r("disruption schedule delete", "change"),
    _r("webhook add", "change"),
    _r("webhook remove", "change"),
    _r("kyverno apply", "change", dry_run_ok=True),
    _r("kyverno enforce", "change"),
    _r("kyverno audit", "change"),
    _r("auto", "change", dry_run_ok=True),
    _r("deploy", "change", dry_run_ok=True),
    _r("clean", "change"),
    _r("operator", "change"),
    _r("migrate", "change", reads_after=("plan", "status", "pairs")),
    _r("init", "change"),
    _r("ports", "change"),
    _r("ctx use", "change"),
    _r("ctx delete", "change"),
    _r("ctx import", "change"),
    _r("ctx set", "change"),
    # Never returns in a headless session
    _r("dashboard", "live"),
    _r("top", "live"),
    _r("watch", "live"),
    _r("cluster watch", "live"),
    _r("lab", "live"),
    _r("kafka tui", "live"),
    _r("disruption watch", "live"),
)

# Commands the CLI arm never gets, whatever they would do: kates mcp would
# hand the shell arm the MCP arm's tools.
REFUSED_PATHS: tuple[tuple[str, ...], ...] = (("mcp",),)

# What the harness's kates and jq wrappers print when they refuse a call, so
# metrics.py can tell a refused call from one that ran.
REFUSED_MARKER = "[kates-eval: refused]"

# Words that may precede the command itself in a simple command.
_WRAPPERS = ("command", "exec", "env", "time", "nohup", "xargs")
_OPERATORS = {"|", "||", "&&", ";", "&", "(", ")", "|&", ";;", "\n"}


def split_commands(command: str) -> list[list[str]]:
    """Split a shell command line into simple commands, each a list of words.

    Pipes, lists, subshells and newlines separate commands; $( and
    backticks count as separators too, so `x=$(kates test get a)` yields the
    kates call. Redirections stay as words. A line shlex cannot read (an
    unbalanced quote) falls back to splitting on whitespace.
    """
    text = command.replace("$(", " ; ").replace("`", " ; ").replace("\n", " ; ")
    try:
        lexer = shlex.shlex(text, posix=True, punctuation_chars=";&|()")
        lexer.whitespace_split = True
        lexer.commenters = ""
        tokens = list(lexer)
    except ValueError:
        tokens = text.split()
    commands: list[list[str]] = []
    current: list[str] = []
    for tok in tokens:
        if tok in _OPERATORS or set(tok) <= set(";&|()"):
            if current:
                commands.append(current)
            current = []
        else:
            current.append(tok)
    if current:
        commands.append(current)
    return commands


def _is_kates(word: str) -> bool:
    return word == "kates" or word.endswith("/kates")


def kates_argvs(command: str) -> list[list[str]]:
    """The arguments of every kates call in a shell command line."""
    calls: list[list[str]] = []
    for words in split_commands(command):
        i = 0
        while i < len(words) and ("=" in words[i] and not words[i].startswith("-")):
            i += 1  # FOO=bar assignments
        while i < len(words) and words[i] in _WRAPPERS:
            if words[i] == "xargs":
                j = next((k for k in range(i + 1, len(words)) if _is_kates(words[k])), None)
                i = j if j is not None else len(words)
                break
            i += 1
        if i < len(words) and _is_kates(words[i]):
            calls.append(words[i + 1:])
    return calls


def command_words(args: list[str], flags_take_values: bool = False) -> list[str]:
    """The subcommand words of a kates call, aliases resolved, flags skipped.

    Global value flags consume their value. Any other flag is skipped on its
    own, so a subcommand flag's value (--type LOAD) can appear among the
    words; callers match a prefix of the words, which such values follow.
    With flags_take_values, a flag without "=" consumes the next word too,
    as cobra does while it finds the command for a flag it does not know as a
    boolean: `kates --type LOAD test create` runs test create.
    """
    words: list[str] = []
    skip = False
    for a in args:
        if skip:
            skip = False
            continue
        if a.startswith("-"):
            if a in GLOBAL_VALUE_FLAGS:
                skip = True
            elif (flags_take_values and "=" not in a and a not in GLOBAL_BOOL_FLAGS
                  and (a.startswith("--") or len(a) == 2)):
                skip = True
            continue
        if a.startswith("<") or a.startswith(">"):
            continue
        parent = tuple(words[:2])
        canonical = ALIASES.get(parent, {}).get(a, a) if len(words) < 3 else a
        words.append(canonical)
    return words


def readings(args: list[str]) -> list[list[str]]:
    """The command words a kates call can resolve to: with each flag on its
    own, and with each flag taking the next word. Which one cobra picks
    depends on whether it knows the flag as a boolean where it appears, so a
    check refuses a call when either reading matches."""
    first, second = command_words(args), command_words(args, flags_take_values=True)
    return [first] if first == second else [first, second]


def has_flag(args: list[str], flag: str) -> bool:
    return any(a == flag or a.startswith(flag + "=") for a in args)


_TRUE = ("1", "t", "T", "true", "TRUE", "True")


def is_dry_run(args: list[str]) -> bool:
    """--dry-run as pflag reads it: the last occurrence wins, and
    --dry-run=false turns it off."""
    value = False
    for a in args:
        if a == "--dry-run":
            value = True
        elif a.startswith("--dry-run="):
            value = a.split("=", 1)[1] in _TRUE
    return value


def _match(words: list[str]) -> Rule | None:
    best: Rule | None = None
    for rule in RULES:
        n = len(rule.path)
        if tuple(words[:n]) == rule.path and (best is None or n > len(best.path)):
            best = rule
    return best


def match_rule(args: list[str]) -> Rule | None:
    """The most specific rule whose path the call starts with, in any reading."""
    found = [r for r in (_match(w) for w in readings(args)) if r is not None]
    return max(found, key=lambda r: len(r.path)) if found else None


def classify(args: list[str]) -> dict[str, object]:
    """What a kates call does: its words, and whether it changes the lab.

    mutating is true when the call would start load, inject a fault or
    change state as written: a rule matches, --dry-run is absent where the
    command has it, the rule's flag is present, and the words after the path
    do not make it a read. live is true for views that never return.
    """
    rule = match_rule(args)
    words = next((w for w in readings(args) if rule is not None and _match(w) is rule), command_words(args))
    info: dict[str, object] = {"words": words, "effect": None, "mutating": False, "live": False}
    if rule is None:
        return info
    info["effect"] = rule.effect
    if rule.effect == "live":
        info["live"] = True
        return info
    rest = words[len(rule.path):]
    if rule.reads_after and rest and rest[0] in rule.reads_after:
        return info
    if rule.dry_run_ok and is_dry_run(args):
        return info
    if rule.needs_flag and not has_flag(args, rule.needs_flag):
        return info
    info["mutating"] = True
    return info


def starts_with(args: list[str], path: tuple[str, ...]) -> bool:
    """True when a kates call runs the command path (aliases resolved)."""
    canon = tuple(command_words(list(path)))
    return any(tuple(words[: len(canon)]) == canon for words in readings(args))


def alias_forms(path: tuple[str, ...]) -> list[tuple[str, ...]]:
    """Every way of writing a command path with the known aliases."""
    options: list[list[str]] = []
    for depth, word in enumerate(path):
        parent = path[:depth]
        names = [word] + sorted(a for a, c in ALIASES.get(parent, {}).items() if c == word)
        options.append(names)
    return [tuple(p) for p in itertools.product(*options)]


def bash_rules(path: tuple[str, ...]) -> list[str]:
    """Claude Code permission rules that match a kates command path, in each
    alias form, with and without further arguments."""
    rules: list[str] = []
    for form in alias_forms(path):
        joined = " ".join(form)
        rules.append(f"Bash(kates {joined})")
        rules.append(f"Bash(kates {joined} *)")
    return rules


def default_cli_deny() -> list[str]:
    """Deny rules for the commands a CLI-arm agent never needs to run.

    Commands with --dry-run stay allowed, because the skill tells the agent
    to dry-run plans; metrics.py flags them when they run without it. So does
    migrate, whose plan and status subcommands only read.
    """
    rules: list[str] = []
    for rule in RULES:
        if rule.dry_run_ok or rule.reads_after:
            continue
        rules.extend(bash_rules(rule.path))
    for path in REFUSED_PATHS:
        rules.extend(bash_rules(path))
    return rules


def refusal(args: list[str], forbidden: list[tuple[str, ...]] = ()) -> str | None:
    """Why the CLI arm's kates wrapper refuses a call, or None to run it.

    Deny rules match strings and let `kates --context lab test create ...`
    through, and commands with a --dry-run form are allowed so the skill's
    dry runs work; the wrapper sees the parsed call. It refuses what would
    start load, inject a fault or change the lab as written, views that never
    return, kates mcp, and the commands the task forbids.
    """
    for path in (*REFUSED_PATHS, *forbidden):
        if starts_with(args, path):
            return "is not available in this session"
    info = classify(args)
    if info["mutating"]:
        return ("would start load, inject a fault or change the lab, which only the human does here; "
                "use --dry-run where the command has it")
    if info["live"]:
        return "keeps refreshing until interrupted, and this session has no terminal"
    return None


def quote(argv: list[str]) -> str:
    return " ".join(shlex.quote(a) for a in argv)

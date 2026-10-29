"""One analysis: a Claude Agent SDK session whose only tools are kates mcp's
read tools (plans/autonomous-agents.md §2.2). Its answer is checked by
verdict.problems; a refused answer gets one retry with the reasons.
"""

import asyncio
import json
import logging
from pathlib import Path

import verdict

LOG = logging.getLogger("run-analyst")
PROMPT = (Path(__file__).parent / "prompt.md").read_text()

# The kates mcp tools the analyst may call; everything else is refused.
KATES_TOOLS = ("get_run", "assess_run", "list_runs", "kates_activity", "cluster_topology", "consumer_group_lag")

# Removed from the model's context as well, should tools=[] ever leave one in.
BUILT_IN_TOOLS = ("Bash", "Read", "Write", "Edit", "MultiEdit", "Glob", "Grep", "NotebookEdit",
                  "WebFetch", "WebSearch", "Task", "TodoWrite")

MAX_TURNS = 12


def session_options(cfg) -> dict:
    """Keyword arguments for ClaudeAgentOptions."""
    return {
        "model": cfg.model,
        "system_prompt": PROMPT,
        "mcp_servers": {
            "kates": {
                "type": "stdio",
                "command": cfg.kates_bin,
                "args": ["mcp", "--context", cfg.context, "--allow-cluster", cfg.cluster_id,
                         "--cluster-label", cfg.cluster_label],
                # kates mcp takes the key from KATES_API_KEY, never from its command line.
                "env": {"KATES_API_KEY": cfg.api_key},
            }
        },
        "tools": [],
        "allowed_tools": [f"mcp__kates__{t}" for t in KATES_TOOLS],
        "disallowed_tools": list(BUILT_IN_TOOLS),
        "permission_mode": "dontAsk",
        "setting_sources": [],
        "cwd": str(cfg.state_dir),
        "max_turns": MAX_TURNS,
        "max_budget_usd": cfg.max_usd_per_run,
        "output_format": {"type": "json_schema", "schema": verdict.SCHEMA},
    }


def first_prompt(run_id: str) -> str:
    return f"Test run {run_id} has finished. Analyse it."


def retry_prompt(run_id: str, answer: dict, problems: list[str]) -> str:
    return (f"Test run {run_id} has finished. Analyse it. An earlier answer was refused for these "
            "reasons:\n- " + "\n- ".join(problems) + "\n\nThe refused answer:\n" + json.dumps(answer, indent=2))


def log_cost(run_id: str, result) -> None:
    """One line per session, whatever its outcome: the cost the eval reports (plan §2.5)."""
    LOG.info("session cost for run %s: %s USD, %s turns, %s", run_id,
             result.total_cost_usd, result.num_turns, result.subtype)


async def _ask(cfg, run_id: str, prompt: str):
    from claude_agent_sdk import ClaudeAgentOptions, ResultMessage, query

    cfg.state_dir.mkdir(parents=True, exist_ok=True)  # the session's cwd; the SDK refuses a missing one
    result = None
    async for message in query(prompt=prompt, options=ClaudeAgentOptions(**session_options(cfg))):
        if isinstance(message, ResultMessage):
            # Logged as it arrives: the SDK may raise after a failed session's result.
            log_cost(run_id, message)
            result = message
    if result is None:
        return None, "the session ended without a result"
    if result.subtype != "success" or result.is_error:
        return None, f"session {result.subtype} (stop reason {result.stop_reason}): {result.errors or ''}"
    if not isinstance(result.structured_output, dict):
        return None, "the session returned no structured answer"
    return result.structured_output, None


def answer_problems(run_id: str, answer: dict) -> list[str]:
    out = verdict.problems(answer)
    if isinstance(answer, dict) and answer.get("runId") != run_id:
        out.append(f"runId is {answer.get('runId')!r}, but the run analysed is {run_id}")
    return out


def analyze(cfg, run_id: str, ask=None):
    """(answer, None) when an answer passed the checks, else (None, why not)."""
    ask = ask or (lambda prompt: asyncio.run(_ask(cfg, run_id, prompt)))
    answer, error = ask(first_prompt(run_id))
    if answer is None:
        return None, error
    problems = answer_problems(run_id, answer)
    if not problems:
        return answer, None
    answer2, error = ask(retry_prompt(run_id, answer, problems))
    if answer2 is None:
        return None, error
    problems = answer_problems(run_id, answer2)
    if problems:
        return None, "answer refused twice: " + "; ".join(problems)
    return answer2, None

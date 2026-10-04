"""A stand-in for `claude` in the harness tests: no model, no network, no cost.

It replays a recorded stream-json transcript from testdata/transcripts, picked
by arm (an --mcp-config means the MCP arm) and by the prompt, and rewrites the
init event from its own command line, the way Claude Code would report the
session it started: the tools --tools and the MCP config leave, the
permission mode and the model. A harness that passed the wrong flags
therefore fails check_init in the tests, as it would on the lab.

On stderr (the trial's stderr.log) it prints one JSON line of what it saw:
its arguments, the names of its environment variables, HOME, the
~/.kates.yaml it found there, the settings and MCP config files, and where
`kates` resolves on its PATH. The tests read that line.

Markers in the prompt change its behaviour: FAKE:sleep=<seconds> sleeps
after the init event, FAKE:crash exits 1 with no output.
"""

import json
import os
import re
import shutil
import sys
import time
from pathlib import Path

HERE = Path(__file__).resolve().parent
MCP_TOOLS = ["mcp__kates__" + n for n in (
    "cluster_overview", "cluster_topology", "consumer_group_lag", "list_runs", "get_run", "assess_run",
    "kates_activity", "list_chaos_catalog", "preview_disruption", "disruption_report", "security_evidence",
    "draft_scenario")]


def opt(argv, name):
    return argv[argv.index(name) + 1] if name in argv else None


def read_json(path):
    try:
        return json.loads(Path(path).read_text())
    except (OSError, TypeError, ValueError):
        return None


def main(argv):
    if argv[:1] == ["--version"]:
        print("2.1.278 (Claude Code)")
        return 0
    prompt = opt(argv, "-p") or ""
    arm = "mcp" if "--mcp-config" in argv else "cli"
    home = os.environ.get("HOME", "")
    seen = {
        "argv": argv,
        "env_keys": sorted(os.environ),
        "env": {k: os.environ[k] for k in ("MCP_PROTOCOL_NEGOTIATION", "MCP_SDK_GENERATION", "PATH", "TMPDIR")
                if k in os.environ},
        "home": home,
        "cwd": os.getcwd(),
        "kates_config": (Path(home) / ".kates.yaml").read_text() if (Path(home) / ".kates.yaml").exists() else None,
        "kates_config_mode": oct((Path(home) / ".kates.yaml").stat().st_mode & 0o777)
        if (Path(home) / ".kates.yaml").exists() else None,
        "settings": read_json(opt(argv, "--settings")),
        "mcp_config": read_json(opt(argv, "--mcp-config")),
        "kates_on_path": shutil.which("kates"),
    }
    print(json.dumps(seen), file=sys.stderr, flush=True)

    if "FAKE:crash" in prompt:
        print("fake claude: crashing as asked", file=sys.stderr)
        return 1
    sleep = re.search(r"FAKE:sleep=(\d+)", prompt)

    marker = Path(home) / ".fake-claude-kind"
    if "--resume" in argv:
        return answer_turn(argv, arm, marker.read_text() if marker.exists() else "run-noise")
    if prompt.startswith(("Call the kates tool", "Run `kates cluster info")):
        kind = "preflight"
    elif "security" in prompt:
        kind = "sec-posture"
    else:
        kind = "run-noise"
    if home:
        marker.write_text(kind)
    lines = (HERE / "transcripts" / f"{arm}-{kind}.jsonl").read_text().splitlines()

    tools = [t for t in (opt(argv, "--tools") or "").split(",") if t]
    servers = []
    config = seen["mcp_config"] or {}
    for name in (config.get("mcpServers") or {}):
        servers.append({"name": name, "status": "connected"})
        if name == "kates":
            tools = MCP_TOOLS + tools
    for line in lines:
        if line.startswith("{"):
            ev = json.loads(line)
            if ev.get("type") == "system" and ev.get("subtype") == "init":
                ev.update(tools=tools, mcp_servers=servers, permissionMode=opt(argv, "--permission-mode"),
                          model=opt(argv, "--model"), cwd=os.getcwd())
                print(json.dumps(ev), flush=True)
                # Like Claude Code, report the session before working on it.
                if sleep:
                    time.sleep(int(sleep.group(1)))
                continue
        print(line, flush=True)
    return 0


def answer_turn(argv, arm, kind):
    """The second turn: the first turn's answer block, restated. Every tool
    must be denied by then, as the harness asks."""
    settings = read_json(opt(argv, "--settings")) or {}
    denied = set((settings.get("permissions") or {}).get("deny") or [])
    lines = (HERE / "transcripts" / f"{arm}-{kind}.jsonl").read_text().splitlines()
    result = next(json.loads(line) for line in reversed(lines) if '"type": "result"' in line)
    block = re.search(r"```json\n.*?\n```", result.get("result") or "", re.S)
    if kind == "preflight":
        found = re.search(r"fixture-cluster-\d+", result.get("result") or "")
        block = None
        text = ("Here it is.\n\n```json\n" + json.dumps({"cluster_id": found.group(0) if found else None})
                + "\n```")
    else:
        text = f"Here it is.\n\n{block.group(0)}" if block else "I have nothing to restate."
    session = opt(argv, "--resume")
    usage = {"input_tokens": 3, "output_tokens": 50, "cache_creation_input_tokens": 0, "cache_read_input_tokens": 9000}
    events = [
        {"type": "system", "subtype": "init", "session_id": session, "tools": [], "mcp_servers": [],
         "permissionMode": opt(argv, "--permission-mode"), "model": opt(argv, "--model"),
         "denied_everything": ("Bash" in denied) or ("mcp__kates__get_run" in denied)},
        {"type": "assistant", "message": {"id": "msg_answer", "role": "assistant", "model": opt(argv, "--model"),
                                          "content": [{"type": "text", "text": text}], "usage": usage},
         "session_id": session},
        {"type": "result", "subtype": "success", "is_error": False, "num_turns": 1, "session_id": session,
         "total_cost_usd": 0.002, "usage": usage, "result": text, "permission_denials": []},
    ]
    for ev in events:
        print(json.dumps(ev), flush=True)
    return 0


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))

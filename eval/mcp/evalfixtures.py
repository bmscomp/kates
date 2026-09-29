"""Shared helpers for the harness tests: fake binaries, a fake Kates API, and a
run directory assembled from recorded transcripts.

Nothing here reaches beyond 127.0.0.1: the fake API listens on a free
loopback port, and the fake claude and kates are Python scripts in testdata.
"""

from __future__ import annotations

import json
import shutil
import sys
import threading
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path
from typing import Any

import metrics

HERE = Path(__file__).resolve().parent
TESTDATA = HERE / "testdata"
TASKS = TESTDATA / "tasks.json"
ORACLE = TESTDATA / "oracle_fixture.py"
TRANSCRIPTS = TESTDATA / "transcripts"
CLUSTER_ID = "fixture-cluster-1"
HUMAN_KEY = "human-key-123"

AUDIT = {"grade": "B", "checks": [
    {"name": "plaintext-listener", "status": "FAIL"},
    {"name": "auto-create-topics", "status": "FAIL"},
    {"name": "tls-enabled", "status": "PASS"},
]}
DRIFT = {"changes": [{"name": "auto-create-topics", "change": "DEGRADED"}]}

EXPECTED = {
    "sec-posture": {"grade": "B", "failing_checks": 2, "drifted_checks": ["auto-create-topics"]},
    "run-noise": {"verdict": "within", "p99_ms": 12.3456, "band_runs": 3},
}


def make_wrapper(directory: Path, name: str, script: Path) -> Path:
    """An executable `name` in directory that runs script with this Python."""
    directory.mkdir(parents=True, exist_ok=True)
    path = directory / name
    path.write_text(f'#!/bin/sh\nexec "{sys.executable}" "{script}" "$@"\n')
    path.chmod(0o755)
    return path


class FakeKatesAPI:
    """GET /api/cluster/info, /api/security/audit and /api/security/drift on a
    loopback port, answering only the human key, sent as the backend accepts
    it (security/ApiKeyAuthFilter.java): a Bearer token, as the CLI and
    kates_api send it, or an X-API-Key header, as oracle.py sends it."""

    def __init__(self, key: str = HUMAN_KEY, drift: dict[str, Any] | None = None):
        self.key = key
        self.drift = drift or DRIFT
        self.requests: list[tuple[str, str | None]] = []
        api = self

        class Handler(BaseHTTPRequestHandler):
            def do_GET(self) -> None:  # noqa: N802 (http.server naming)
                api.requests.append((self.path, self.headers.get("Authorization") or self.headers.get("X-API-Key")))
                if self.headers.get("Authorization") != f"Bearer {api.key}" and \
                        self.headers.get("X-API-Key") != api.key:
                    self._send(401, {"error": "Unauthorized"})
                    return
                routes = {"/api/cluster/info": {"clusterId": CLUSTER_ID, "brokerCount": 3},
                          "/api/security/audit": AUDIT, "/api/security/drift": api.drift}
                if self.path in routes:
                    self._send(200, routes[self.path])
                else:
                    self._send(404, {"error": "Not Found"})

            def _send(self, code: int, body: Any) -> None:
                data = json.dumps(body).encode()
                self.send_response(code)
                self.send_header("Content-Type", "application/json")
                self.send_header("Content-Length", str(len(data)))
                self.end_headers()
                self.wfile.write(data)

            def log_message(self, *args: Any) -> None:
                pass

        self.server = ThreadingHTTPServer(("127.0.0.1", 0), Handler)
        self.url = f"http://127.0.0.1:{self.server.server_address[1]}"
        self.thread = threading.Thread(target=self.server.serve_forever, daemon=True)

    def __enter__(self) -> "FakeKatesAPI":
        self.thread.start()
        return self

    def __exit__(self, *exc: Any) -> None:
        self.server.shutdown()
        self.server.server_close()


def write_trial(run_dir: Path, task_id: str, arm: str, n: int, transcript: str, *,
                status: str | None = None, forbidden: list[str] | None = None,
                prompt: str = "") -> Path:
    """A finished trial directory from a recorded transcript, with the
    metrics.json run.py would have written."""
    tdir = run_dir / task_id / arm / f"trial-{n}"
    tdir.mkdir(parents=True, exist_ok=True)
    shutil.copyfile(TRANSCRIPTS / transcript, tdir / "transcript.jsonl")
    (tdir / "prompt.txt").write_text(prompt)
    t = metrics.load(tdir / "transcript.jsonl")
    summary = metrics.summarize(t)
    summary.update(task_id=task_id, arm=arm, trial=n, wall_clock_s=40.0 + n,
                   forbidden_used=forbidden or [])
    if status:
        summary["status"] = status
    summary["valid"] = summary["status"] in metrics.AGENT_STATUSES
    (tdir / "metrics.json").write_text(json.dumps(summary))
    return tdir


def graded_run_dir(root: Path, run_id: str = "fixture-run") -> Path:
    """A run directory with the fixture task list, oracle answers, a manifest
    and one trial per task and arm, ready for grade.py."""
    run_dir = root / run_id
    run_dir.mkdir(parents=True)
    shutil.copyfile(TASKS, run_dir / "tasks.json")
    (run_dir / "oracle.json").write_text(json.dumps(
        {"tasks": {tid: {"expected": exp} for tid, exp in EXPECTED.items()}}))
    (run_dir / "manifest.json").write_text(json.dumps({
        "run_id": run_id, "seed": 7, "model": "claude-opus-5-5", "max_turns": 30,
        "protocol_negotiation": "legacy", "sdk_generation": "v2", "claude_version": "2.1.278",
        "skill_mode": "system-prompt", "skill_sha256": "ab" * 32, "kates_sha256": "cd" * 32,
        "cluster_id": CLUSTER_ID, "agent_key_is_human_key": True,
        "tasks": {"fixed_at": "2026-09-26", "sha256": "ef" * 32, "count": 2},
    }))
    prompt = "baseline saved at 2026-09-26T10:00:00Z"
    write_trial(run_dir, "sec-posture", "mcp", 1, "mcp-sec-posture.jsonl", prompt=prompt)
    write_trial(run_dir, "sec-posture", "cli", 1, "cli-sec-posture.jsonl", prompt=prompt)
    write_trial(run_dir, "run-noise", "mcp", 1, "mcp-run-noise.jsonl",
                forbidden=["mcp__kates__disruption_report"], prompt="run a1b2c3d4")
    write_trial(run_dir, "run-noise", "cli", 1, "cli-run-noise.jsonl", prompt="run a1b2c3d4")
    return run_dir

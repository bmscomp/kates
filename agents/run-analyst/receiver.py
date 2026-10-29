"""The test.completed webhook receiver and the worker behind it
(plans/autonomous-agents.md §2.1). No model here.

Kates signs nothing it sends, so the body is a hint naming a run id, never
data: the worker reads the run back with the agent's key before anything else.

    python3 receiver.py --listen 0.0.0.0:8080
"""

import argparse
import datetime
import json
import logging
import os
import queue
import re
import sqlite3
import threading
import time
import urllib.error
import urllib.parse
import urllib.request
from dataclasses import dataclass
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path

import sinks as sinks_mod

LOG = logging.getLogger("run-analyst")

RUN_ID = re.compile(r"^[0-9a-f]{8}$")
FINISHED = ("DONE", "FAILED")
MAX_BODY = 4096
SEEN_DAYS = 7
# The reaper fails runs past their deadline, so a run created two days ago
# finished long ago; an event naming one is a replay or a forgery.
MAX_RUN_AGE = datetime.timedelta(hours=48)


@dataclass
class Config:
    kates_url: str
    api_key: str
    context: str
    cluster_id: str
    cluster_label: str
    kates_bin: str
    model: str
    state_dir: Path
    max_per_hour: int
    max_usd_per_run: float
    poll_seconds: int
    slack_webhook_url: str
    github_repo: str
    github_token: str

    @classmethod
    def from_env(cls, env=os.environ) -> "Config":
        missing = [k for k in ("ANALYST_KATES_URL", "KATES_API_KEY", "KATES_CONTEXT", "ANALYST_CLUSTER_ID")
                   if not env.get(k)]
        if missing:
            raise SystemExit("run-analyst needs " + ", ".join(missing) + " (agents/run-analyst/README.md)")
        if env.get("KATES_URL"):
            raise SystemExit("unset KATES_URL: kates mcp refuses it and takes the URL from KATES_CONTEXT; "
                             "the analyst's own reads use ANALYST_KATES_URL")
        return cls(
            kates_url=env["ANALYST_KATES_URL"].rstrip("/"),
            api_key=env["KATES_API_KEY"],
            context=env["KATES_CONTEXT"],
            cluster_id=env["ANALYST_CLUSTER_ID"],
            cluster_label=env.get("ANALYST_CLUSTER_LABEL", "lab"),
            kates_bin=env.get("KATES_BIN", "kates"),
            model=env.get("ANALYST_MODEL", "claude-opus-5-5"),
            state_dir=Path(env.get("ANALYST_STATE_DIR", "state")),
            max_per_hour=int(env.get("ANALYST_MAX_PER_HOUR", "20")),
            max_usd_per_run=float(env.get("ANALYST_MAX_USD_PER_RUN", "2")),
            poll_seconds=int(env.get("ANALYST_POLL_SECONDS", "60")),
            slack_webhook_url=env.get("ANALYST_SLACK_WEBHOOK_URL", ""),
            github_repo=env.get("ANALYST_GITHUB_REPO", ""),
            github_token=env.get("GITHUB_TOKEN", ""),
        )


class KatesApi:
    """The two reads the receiver and poll mode make, with the agent's key."""

    def __init__(self, url: str, api_key: str):
        self.url = url
        self.headers = {"X-API-Key": api_key, "Accept": "application/json"}

    def _get(self, path: str):
        req = urllib.request.Request(self.url + path, headers=self.headers)
        with urllib.request.urlopen(req, timeout=10) as resp:
            return json.loads(resp.read())

    def get_run(self, run_id: str) -> dict | None:
        try:
            return self._get(f"/api/tests/{run_id}")
        except urllib.error.HTTPError as e:
            if e.code == 404:
                return None
            raise

    def finished_runs(self, status: str, size: int = 200) -> list[dict]:
        """The newest finished runs with this status, newest first."""
        q = urllib.parse.urlencode({"status": status, "page": 0, "size": size})
        return self._get(f"/api/tests?{q}").get("items", [])


class Store:
    """Event dedupe and the outcome of every run handled, in one SQLite file."""

    def __init__(self, path: Path):
        path.parent.mkdir(parents=True, exist_ok=True)
        self.db = sqlite3.connect(path, check_same_thread=False)
        self.lock = threading.Lock()
        with self.lock, self.db:
            self.db.execute("CREATE TABLE IF NOT EXISTS seen (key TEXT PRIMARY KEY, at REAL NOT NULL)")
            self.db.execute("CREATE TABLE IF NOT EXISTS outcomes (run_id TEXT, outcome TEXT, at REAL NOT NULL)")

    def claim(self, key: str, now: float | None = None) -> bool:
        """True the first time a key is seen within SEEN_DAYS."""
        now = time.time() if now is None else now
        with self.lock, self.db:
            self.db.execute("DELETE FROM seen WHERE at < ?", (now - SEEN_DAYS * 86400,))
            return self.db.execute("INSERT OR IGNORE INTO seen VALUES (?, ?)", (key, now)).rowcount == 1

    def is_empty(self) -> bool:
        with self.lock:
            return self.db.execute("SELECT 1 FROM seen LIMIT 1").fetchone() is None

    def record(self, run_id: str, outcome: str, now: float | None = None) -> None:
        with self.lock, self.db:
            self.db.execute("INSERT INTO outcomes VALUES (?, ?, ?)",
                            (run_id, outcome, time.time() if now is None else now))

    def count_since(self, outcome: str, seconds: float, now: float | None = None) -> int:
        now = time.time() if now is None else now
        with self.lock:
            return self.db.execute("SELECT COUNT(*) FROM outcomes WHERE outcome = ? AND at >= ?",
                                   (outcome, now - seconds)).fetchone()[0]


def parse_created(created: str) -> datetime.datetime | None:
    try:
        t = datetime.datetime.fromisoformat(created.replace("Z", "+00:00"))
    except (AttributeError, ValueError):
        return None
    return t if t.tzinfo else t.replace(tzinfo=datetime.UTC)


class Worker:
    """Gate, budget, analysis, delivery for one run id at a time.

    analyze(run_id) returns (answer, None) or (None, why it failed).
    """

    def __init__(self, kates, store: Store, sinks, analyze, max_per_hour: int, clock=time.time):
        self.kates = kates
        self.store = store
        self.sinks = sinks
        self.analyze = analyze
        self.max_per_hour = max_per_hour
        self.clock = clock

    def gate(self, run_id: str) -> str | None:
        """Why this run is not analysed, or None when it is."""
        run = self.kates.get_run(run_id)
        if run is None:
            return "no such run"
        if run.get("status") not in FINISHED:
            return f"run is {run.get('status')}, not finished"
        created = parse_created(run.get("createdAt"))
        now = datetime.datetime.fromtimestamp(self.clock(), datetime.UTC)
        if created is None or now - created > MAX_RUN_AGE:
            return f"run was created at {run.get('createdAt')}, more than {MAX_RUN_AGE} ago"
        return None

    def process(self, run_id: str) -> str:
        try:
            dropped = self.gate(run_id)
        except Exception as e:  # noqa: BLE001 - Kates unreachable: say so, keep serving
            LOG.error("could not read run %s from Kates: %s", run_id, e)
            # One notice an hour: while Kates is down, forged ids would otherwise each post a line.
            if self.store.count_since("GATE_ERROR", 3600, self.clock()) == 0:
                self.sinks.notice(run_id, "GATE_ERROR", f"could not read the run from Kates: {e}")
            self.store.record(run_id, "GATE_ERROR", self.clock())
            return "GATE_ERROR"
        if dropped:
            # Dropped quietly: posting about forged or stale events would let anyone post.
            LOG.info("dropped run %s: %s", run_id, dropped)
            self.store.record(run_id, "DROPPED", self.clock())
            return "DROPPED"
        # Analyses started in the last hour, whatever their result.
        if self.store.count_since("STARTED", 3600, self.clock()) >= self.max_per_hour:
            self.store.record(run_id, "SKIPPED_BUDGET", self.clock())
            self.sinks.notice(run_id, "SKIPPED_BUDGET",
                              f"{self.max_per_hour} analyses in the last hour already; not analysed")
            return "SKIPPED_BUDGET"
        self.store.record(run_id, "STARTED", self.clock())
        try:
            answer, error = self.analyze(run_id)
        except Exception as e:  # noqa: BLE001 - one bad analysis never stops the worker
            answer, error = None, f"{type(e).__name__}: {e}"
        if answer is None:
            self.store.record(run_id, "ANALYST_ERROR", self.clock())
            self.sinks.notice(run_id, "ANALYST_ERROR", error)
            return "ANALYST_ERROR"
        self.store.record(run_id, answer["verdict"], self.clock())
        self.sinks.deliver(answer)
        return answer["verdict"]

    def serve(self, q: queue.Queue) -> None:
        while True:
            self.process(q.get())


def make_handler(store: Store, q: queue.Queue):
    class Handler(BaseHTTPRequestHandler):
        def _reply(self, code: int, text: str = "") -> None:
            body = text.encode()
            self.send_response(code)
            self.send_header("Content-Type", "text/plain")
            self.send_header("Content-Length", str(len(body)))
            self.end_headers()
            self.wfile.write(body)

        def do_GET(self):
            if self.path == "/healthz":
                self._reply(200, "ok\n")
            else:
                self._reply(404)

        def do_POST(self):
            length = int(self.headers.get("Content-Length") or 0)
            if length <= 0 or length > MAX_BODY:
                return self._reply(400, "body missing or too large\n")
            # Accepted before any analysis, so a slow model never makes Kates retry.
            if self.headers.get("X-Kates-Event") != "test.completed":
                self.rfile.read(length)
                return self._reply(202, "ignored\n")
            try:
                event = json.loads(self.rfile.read(length))
                run_id, status = event["testId"], event["status"]
            except (ValueError, KeyError, TypeError):
                return self._reply(400, "not a test.completed body\n")
            if not isinstance(run_id, str) or not RUN_ID.match(run_id) or status not in FINISHED:
                return self._reply(400, "testId must be 8 hex characters and status DONE or FAILED\n")
            if store.claim(f"{run_id}:{status}"):
                q.put(run_id)
            self._reply(202, "accepted\n")

        def log_message(self, fmt, *args):
            LOG.debug("%s %s", self.address_string(), fmt % args)

    return Handler


def build_worker(cfg: Config) -> tuple[Worker, Store]:
    import analyst  # the SDK is needed only when a model runs

    store = Store(cfg.state_dir / "run-analyst.sqlite")
    worker = Worker(KatesApi(cfg.kates_url, cfg.api_key), store, sinks_mod.from_config(cfg),
                    lambda run_id: analyst.analyze(cfg, run_id), cfg.max_per_hour)
    return worker, store


def main() -> None:
    parser = argparse.ArgumentParser(description=__doc__.splitlines()[0])
    parser.add_argument("--listen", default="0.0.0.0:8080", help="host:port to listen on")
    args = parser.parse_args()
    logging.basicConfig(level=logging.INFO, format="%(asctime)s %(levelname)s %(message)s")
    worker, store = build_worker(Config.from_env())
    q: queue.Queue = queue.Queue()
    threading.Thread(target=worker.serve, args=(q,), daemon=True).start()
    host, port = args.listen.rsplit(":", 1)
    server = ThreadingHTTPServer((host, int(port)), make_handler(store, q))
    LOG.info("run-analyst listening on %s", args.listen)
    server.serve_forever()


if __name__ == "__main__":
    main()

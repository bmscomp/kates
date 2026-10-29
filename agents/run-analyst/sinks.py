"""Where answers go. A person configures the sinks; the model never picks one
(plans/autonomous-agents.md §2.4).

Every answer goes to the log and to a JSON file per run. Slack and GitHub get
only verdicts other than NORMAL, and one-line notices (skipped, failed).
"""

import json
import logging
import os
import urllib.request
from pathlib import Path

LOG = logging.getLogger("run-analyst")


def post_json(url: str, body: dict, headers: dict | None = None, method: str = "POST") -> None:
    req = urllib.request.Request(
        url, data=json.dumps(body).encode(), method=method,
        headers={"Content-Type": "application/json", **(headers or {})},
    )
    with urllib.request.urlopen(req, timeout=10):
        pass


def get_json(url: str, headers: dict | None = None):
    req = urllib.request.Request(url, headers=headers or {})
    with urllib.request.urlopen(req, timeout=10) as resp:
        return json.loads(resp.read())


def summary(a: dict) -> str:
    """Plain text for a person: the headline, then the evidence that decided it."""
    lines = [f"Run {a['runId']} ({a['testType']}): {a['verdict']}", a["headline"]]
    b = a["baseline"]
    if b and b["runId"]:
        same = "same spec" if b["sameSpec"] else "different spec"
        flagged = ", flagged as a regression" if b["regressionFlagged"] else ""
        lines.append(f"Baseline {b['runId']} ({same}{flagged})")
    for m in a["band"]["metrics"]:
        lines.append(f"{m['name']} {m['value']}: {m['position']} the band {m['min']}–{m['max']} "
                     f"({a['band']['runs']} runs)")
    for o in a["overlaps"]:
        lines.append(f"Overlapped by {o['kind']} {o['id']}")
    for e in a["taskErrors"]:
        lines.append(f"Task error: {e}")
    if a["caveats"]:
        lines.append("Caveats: " + ", ".join(a["caveats"]))
    if a["next"]:
        lines.append(f"Next: {a['next']}")
    return "\n".join(lines)


class LogSink:
    def deliver(self, a: dict) -> None:
        LOG.info("analysis %s", json.dumps(a, sort_keys=True))

    def notice(self, run_id: str, kind: str, detail: str) -> None:
        LOG.warning("%s for run %s: %s", kind, run_id, detail)


class FileSink:
    def __init__(self, directory: Path):
        self.dir = Path(directory)
        self.dir.mkdir(parents=True, exist_ok=True)

    def _write(self, run_id: str, body: dict) -> None:
        tmp = self.dir / f".{run_id}.json.tmp"
        tmp.write_text(json.dumps(body, indent=2, sort_keys=True) + "\n")
        os.replace(tmp, self.dir / f"{run_id}.json")

    def deliver(self, a: dict) -> None:
        self._write(a["runId"], a)

    def notice(self, run_id: str, kind: str, detail: str) -> None:
        self._write(run_id, {"runId": run_id, "outcome": kind, "detail": detail})


def slack_escape(text: str) -> str:
    # Slack's three control characters; without this, text from Kates data
    # (names, advisor rules) could write <!channel> or a link.
    return text.replace("&", "&amp;").replace("<", "&lt;").replace(">", "&gt;")


class SlackSink:
    def __init__(self, webhook_url: str, post=post_json):
        self.url = webhook_url
        self.post = post

    def deliver(self, a: dict) -> None:
        if a["verdict"] != "NORMAL":
            self.post(self.url, {"text": slack_escape(summary(a))})

    def notice(self, run_id: str, kind: str, detail: str) -> None:
        self.post(self.url, {"text": slack_escape(f"Run analyst, run {run_id}: {kind}: {detail}")})


class GitHubSink:
    """One issue per run id, labelled run-analyst, updated rather than duplicated."""

    LABEL = "run-analyst"

    def __init__(self, repo: str, token: str, post=post_json, get=get_json, api="https://api.github.com"):
        self.base = f"{api}/repos/{repo}/issues"
        self.headers = {"Authorization": f"Bearer {token}", "Accept": "application/vnd.github+json"}
        self.post = post
        self.get = get

    def _existing(self, run_id: str):
        prefix = f"[run-analyst] {run_id}:"
        # shortcut: reads the newest 100 run-analyst issues only; page through them if a repo keeps more open.
        issues = self.get(f"{self.base}?labels={self.LABEL}&state=all&per_page=100", self.headers)
        return next((i for i in issues if i.get("title", "").startswith(prefix)), None)

    def deliver(self, a: dict) -> None:
        if a["verdict"] == "NORMAL":
            return
        title = f"[run-analyst] {a['runId']}: {a['verdict']}"
        # A zero-width space after @ keeps names from Kates data from pinging anyone.
        body = "```\n" + summary(a).replace("`", "'").replace("@", "@​") + "\n```"
        issue = self._existing(a["runId"])
        if issue:
            self.post(f"{self.base}/{issue['number']}", {"title": title, "body": body}, self.headers, "PATCH")
        else:
            self.post(self.base, {"title": title, "body": body, "labels": [self.LABEL]}, self.headers)

    def notice(self, run_id: str, kind: str, detail: str) -> None:
        pass  # notices are a line in chat, not an issue


class Sinks:
    """Fans out to every sink; one failing sink never stops the others."""

    def __init__(self, sinks: list):
        self.sinks = sinks

    def _each(self, method: str, *args) -> None:
        for s in self.sinks:
            try:
                getattr(s, method)(*args)
            except Exception as e:  # noqa: BLE001 - a sink's failure is logged, not fatal
                LOG.error("%s.%s failed: %s", type(s).__name__, method, e)

    def deliver(self, a: dict) -> None:
        self._each("deliver", a)

    def notice(self, run_id: str, kind: str, detail: str) -> None:
        self._each("notice", run_id, kind, detail)


def from_config(cfg) -> Sinks:
    sinks: list = [LogSink(), FileSink(cfg.state_dir / "analyses")]
    if cfg.slack_webhook_url:
        sinks.append(SlackSink(cfg.slack_webhook_url))
    if cfg.github_repo and cfg.github_token:
        sinks.append(GitHubSink(cfg.github_repo, cfg.github_token))
    return Sinks(sinks)

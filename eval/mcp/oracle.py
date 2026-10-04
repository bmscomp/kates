#!/usr/bin/env python3
"""Computes the expected answer of every task from the Kates REST API.

The evaluation grades an agent against what Kates itself says, read with the
human's key after setup and without any agent in the loop. Each oracle
function fetches what it needs and hands the JSON to a pure ``expect_*``
function, so the arithmetic is tested on recorded response shapes
(testdata/api) and the fetching on a fixture server, both offline.

What an oracle reads is what the tools read: the backend's own verdicts
(regression flags, dry-run acceptance, drift) are taken as given, never
recomputed, because the question is whether the agent reports them right.
Where an answer is a fact about Kates rather than about data (the pentest
attacks nothing, the grade history is in memory), the oracle states it as a
constant and the task's notes say why.

The only POST an oracle sends is the disruption dry run, which injects
nothing: KatesAPI has get() and dry_run() and nothing else.

Usage:
  KATES_EVAL_API_KEY=... oracle.py --run-dir runs/<id> [--task ID ...]
  oracle.py --run-dir /tmp/x --state testdata/state.json --fixtures testdata/api   # offline
Exit codes: 0 every task computed; 1 some task failed (its error is in
oracle.json); 2 bad input; 3 an answer differs from --against.
"""

from __future__ import annotations

import argparse
import datetime as dt
import json
import math
import os
import re
import ssl
import sys
import urllib.error
import urllib.parse
import urllib.request
from typing import Any, Callable, Mapping

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))

import taskfile  # noqa: E402

HERE = os.path.dirname(os.path.abspath(__file__))
API_KEY_ENV = "KATES_EVAL_API_KEY"
API_URL_ENV = "KATES_EVAL_URL"
DEFAULT_API_URL = "http://localhost:8080"
PAGE_SIZE = 200
MAX_PAGES = 5
BAND_MIN_RUNS = 3
BAND_MAX_RUNS = 10  # assess_run's default band_runs (cli/cmd/mcp_tools_runs.go)


class OracleError(Exception):
    """The oracle could not compute an answer, or a task's premise failed."""


# --- API access -------------------------------------------------------------


def request_key(method: str, path: str, params: Mapping[str, Any] | None = None) -> str:
    """How a request is named in fixtures: method, path and sorted query."""
    query = urllib.parse.urlencode(sorted((k, str(v)) for k, v in (params or {}).items()))
    return f"{method} {path}" + (f"?{query}" if query else "")


def seg(value: Any) -> str:
    """One path segment, escaped as the kates client escapes it."""
    return urllib.parse.quote(str(value), safe="")


class KatesAPI:
    """Reads the Kates API with the human key (X-API-Key). proxy_url and
    insecure follow the kates context's proxy-url and insecure fields."""

    def __init__(self, base_url: str, api_key: str, timeout: float = 90.0, *, proxy_url: str = "",
                 insecure: bool = False) -> None:
        self.base_url = base_url.rstrip("/")
        self._key = api_key
        self.timeout = timeout
        handlers: list[urllib.request.BaseHandler] = [
            urllib.request.ProxyHandler({"http": proxy_url, "https": proxy_url} if proxy_url else {})
        ]
        if insecure:
            handlers.append(urllib.request.HTTPSHandler(context=ssl._create_unverified_context()))
        self._opener = urllib.request.build_opener(*handlers)

    def cluster_id(self) -> str:
        """The Kafka clusterId the API reaches (GET /api/cluster/info)."""
        info = self.get("/api/cluster/info")
        cid = info.get("clusterId") if isinstance(info, dict) else None
        if not cid:
            raise OracleError("GET /api/cluster/info: no clusterId in the response")
        return str(cid)

    def get(self, path: str, params: Mapping[str, Any] | None = None) -> Any:
        return self._send("GET", path, params, None)

    def dry_run(self, plan: Mapping[str, Any]) -> Any:
        """POST /api/disruptions?dryRun=true: the backend's preview, which
        injects nothing (DisruptionResource.java:55-72)."""
        return self._send("POST", "/api/disruptions", {"dryRun": "true"}, plan)

    def _send(self, method: str, path: str, params: Mapping[str, Any] | None, body: Any) -> Any:
        url = self.base_url + path
        if params:
            url += "?" + urllib.parse.urlencode(sorted((k, str(v)) for k, v in params.items()))
        data = json.dumps(body).encode() if body is not None else None
        req = urllib.request.Request(url, data=data, method=method)
        req.add_header("Accept", "application/json")
        req.add_header("X-API-Key", self._key)
        if data is not None:
            req.add_header("Content-Type", "application/json")
        try:
            with self._opener.open(req, timeout=self.timeout) as resp:
                raw = resp.read()
        except urllib.error.HTTPError as e:
            with e:
                detail = e.read()[:300].decode("utf-8", "replace")
            raise OracleError(f"{request_key(method, path, params)}: HTTP {e.code}: {detail}") from e
        except (urllib.error.URLError, OSError) as e:
            raise OracleError(f"{request_key(method, path, params)}: {e}") from e
        try:
            return json.loads(raw) if raw else None
        except ValueError as e:
            raise OracleError(f"{request_key(method, path, params)}: not JSON") from e


class FixtureAPI:
    """Serves recorded responses: manifest.json maps a request key, or
    "DRYRUN <plan name>" for a dry run, to a file in the same directory."""

    def __init__(self, directory: str) -> None:
        self.directory = directory
        with open(os.path.join(directory, "manifest.json"), encoding="utf-8") as f:
            self.manifest: dict[str, str] = json.load(f)
        self.requests: list[str] = []

    def get(self, path: str, params: Mapping[str, Any] | None = None) -> Any:
        return self._serve(request_key("GET", path, params))

    def dry_run(self, plan: Mapping[str, Any]) -> Any:
        return self._serve(f"DRYRUN {plan.get('name')}")

    def _serve(self, key: str) -> Any:
        self.requests.append(key)
        if key not in self.manifest:
            raise OracleError(f"{key}: no fixture")
        with open(os.path.join(self.directory, self.manifest[key]), encoding="utf-8") as f:
            return json.load(f)


# --- small readers ----------------------------------------------------------

_FRACTION_RE = re.compile(r"(\.\d{1,6})\d*")


def parse_instant(s: str) -> dt.datetime:
    """An ISO-8601 instant as Java's Instant.toString writes it (Z, up to nine
    fractional digits), as an aware UTC datetime."""
    if not isinstance(s, str):
        raise OracleError(f"not a timestamp: {s!r}")
    # Python before 3.11 reads exactly three or six fractional digits.
    text = _FRACTION_RE.sub(lambda m: m.group(1).ljust(7, "0"), s.strip()).replace("Z", "+00:00")
    try:
        value = dt.datetime.fromisoformat(text)
    except ValueError as e:
        raise OracleError(f"not a timestamp: {s!r}") from e
    if value.tzinfo is None:
        value = value.replace(tzinfo=dt.timezone.utc)
    return value.astimezone(dt.timezone.utc)


_ISO_DURATION_RE = re.compile(r"^PT(?:(\d+(?:\.\d+)?)H)?(?:(\d+(?:\.\d+)?)M)?(?:(-?\d+(?:\.\d+)?)S)?$")


def seconds(value: Any) -> float | None:
    """A java.time.Duration as the backend sends it: a number of seconds
    from the report endpoints, "1234ms" from timeline and kafka-metrics, or
    ISO-8601. None for null, "N/A" or anything else."""
    if value is None or isinstance(value, bool):
        return None
    if isinstance(value, (int, float)):
        return float(value) if math.isfinite(value) else None
    if isinstance(value, str):
        s = value.strip()
        if s.endswith("ms"):
            try:
                return float(s[:-2]) / 1000.0
            except ValueError:
                return None
        m = _ISO_DURATION_RE.match(s.upper())
        if m and any(m.groups()):
            h, mi, se = (float(g) if g else 0.0 for g in m.groups())
            return h * 3600 + mi * 60 + se
        try:
            return float(s)
        except ValueError:
            return None
    return None


def broker_id_of_pod(pod: str) -> int:
    """The node id at the end of a Strimzi pod name (<cluster>-<pool>-<id>)."""
    m = re.search(r"-(\d+)$", pod)
    if not m:
        raise OracleError(f"pod {pod!r} does not end in a node id")
    return int(m.group(1))


def pod_roles(topology: Mapping[str, Any]) -> dict[str, str]:
    """Pod name to role (broker or controller) from GET /api/cluster/topology.

    The backend lists each node pool's pods with id, pool and role
    (ClusterTopologyService.describeNodes) but not the pod name; Strimzi
    names node-pool pods <cluster>-<pool>-<node id>.
    """
    cluster = (topology.get("cluster") or {}).get("name")
    if not cluster:
        raise OracleError("the cluster topology has no cluster name")
    roles = {}
    for n in topology.get("nodes") or []:
        if "pool" in n and "id" in n:
            roles[f"{cluster}-{n['pool']}-{n['id']}"] = n.get("role", "unknown")
    if not roles:
        raise OracleError("the cluster topology lists no node-pool pods")
    return roles


def _pod(name: str) -> str:
    return name.replace(" (random selection)", "").strip()


def _ids(values: Any) -> list[str]:
    return sorted({str(v) for v in values}, key=lambda x: (len(x), x))


def _paged(api: Any, path: str, params: Mapping[str, Any], stop: Callable[[dict], bool] | None = None) -> list[dict]:
    """Every item of a paged list (PagedResponse), newest first, reading at
    most MAX_PAGES pages; stop(item) ends the read early."""
    items: list[dict] = []
    for page in range(MAX_PAGES):
        doc = api.get(path, dict(params, page=page, size=PAGE_SIZE))
        batch = (doc or {}).get("items") or []
        for item in batch:
            if stop and stop(item):
                return items
            items.append(item)
        if len(batch) < PAGE_SIZE:
            return items
    return items


# --- pure computations ------------------------------------------------------

MIN_ISR_SOURCES = {
    "DYNAMIC_TOPIC_CONFIG": "topic",
    "STATIC_BROKER_CONFIG": "broker",
    "DYNAMIC_BROKER_CONFIG": "broker",
    "DYNAMIC_DEFAULT_BROKER_CONFIG": "cluster-default",
    "DEFAULT_CONFIG": "kafka-default",
}


def expect_topic_min_isr(detail: Mapping[str, Any]) -> dict:
    configs, sources = detail.get("configs") or {}, detail.get("configSources") or {}
    if "min.insync.replicas" not in configs or "min.insync.replicas" not in sources:
        raise OracleError("topic detail has no min.insync.replicas with its source: a backend older than "
                          "TopicService's configSources cannot answer this task")
    source = sources["min.insync.replicas"]
    if source not in MIN_ISR_SOURCES:
        raise OracleError(f"unknown config source {source!r}")
    return {"min_insync_replicas": int(configs["min.insync.replicas"]), "set_at": MIN_ISR_SOURCES[source]}


def _partition(detail: Mapping[str, Any], partition: int) -> dict:
    for p in detail.get("partitionInfo") or []:
        if p.get("partition") == partition:
            return p
    raise OracleError(f"topic {detail.get('name')!r} has no partition {partition}")


def expect_partition_leader(detail: Mapping[str, Any], partition: int) -> dict:
    p = _partition(detail, partition)
    if p.get("leader", -1) < 0:
        raise OracleError(f"partition {partition} has no leader")
    return {"leader_broker_id": p["leader"], "isr_broker_ids": _ids(p.get("isr") or [])}


def expect_security_posture(audit: Mapping[str, Any]) -> dict:
    if audit.get("error"):
        raise OracleError(f"the backend's audit failed: {audit['error']}")
    # The backend never emits category policy; the CLI adds those checks
    # from the local kubeconfig (cli/cmd/security.go), and the task leaves
    # them out of the Kafka cluster's grade.
    checks = [c for c in audit.get("checks") or [] if c.get("category") != "policy"]
    failing = [c["name"] for c in checks if c.get("severity") in ("HIGH", "CRITICAL") and c.get("status") != "PASS"]
    return {"grade": audit["grade"], "failing_high_or_critical": sorted(failing)}


def _protocol_maps(config_diff: Mapping[str, Any]) -> list[str]:
    for item in (config_diff.get("consistent") or []) + (config_diff.get("mismatches") or []):
        if item.get("key") == "listener.security.protocol.map":
            values = item.get("values") or {}
            found = [v for v in values.values() if isinstance(v, str)]
            if isinstance(item.get("value"), str):
                found.append(item["value"])
            return found
    raise OracleError("config-diff does not report listener.security.protocol.map")


def expect_listener_encryption(config_diff: Mapping[str, Any]) -> dict:
    if config_diff.get("error"):
        raise OracleError(f"the backend's config check failed: {config_diff['error']}")
    listeners: dict[str, str] = {}
    for mapping in _protocol_maps(config_diff):
        if mapping == "(not set)":
            raise OracleError("no broker reports listener.security.protocol.map")
        for pair in mapping.split(","):
            name, _, proto = pair.strip().partition(":")
            if name and proto:
                listeners[name.strip()] = proto.strip().upper()
    if not listeners:
        raise OracleError("listener.security.protocol.map is empty")
    plain = sorted(n for n, p in listeners.items() if p in ("PLAINTEXT", "SASL_PLAINTEXT"))
    return {"all_listeners_encrypted": not plain, "unencrypted_listeners": plain}


def expect_pentest_and_cve(pentest: Mapping[str, Any], cve: Mapping[str, Any]) -> dict:
    for name, doc in (("pentest", pentest), ("CVE check", cve)):
        if doc.get("error"):
            raise OracleError(f"the backend's {name} failed: {doc['error']}")
    vulnerable = [t["name"] for t in pentest.get("tests") or [] if t.get("result") == "VULNERABLE"]
    version = str(cve.get("kafkaVersion", "unknown"))
    return {
        "pentest_vulnerable_checks": sorted(vulnerable),
        # SecurityPentestService reads the first broker's config and the ACL
        # list; it sends nothing to the cluster (mcp caveat pentest-config-only).
        "pentest_attacked_cluster": False,
        "cve_check_compared_version": bool(re.match(r"^\d+\.\d+", version)),
    }


def expect_security_drift(drift: Mapping[str, Any]) -> dict:
    if not drift.get("hasBaseline"):
        raise OracleError("no security baseline is saved")
    changes = drift.get("drifts") or []
    return {
        "baseline_grade": drift["baselineGrade"],
        "current_grade": drift["currentGrade"],
        "improved_checks": sorted(d["check"] for d in changes if d.get("change") == "IMPROVED"),
        "degraded_checks": sorted(d["check"] for d in changes if d.get("change") == "DEGRADED"),
        # The grade history is an in-memory list of the last 100 explicit
        # audits in the pod (SecurityService.java:42,1649-1689).
        "grade_history_shows_trend": False,
    }


def _delta(regression: Mapping[str, Any], metric: str) -> float:
    d = (regression.get("deltas") or {}).get(metric) or {}
    if "delta" not in d:
        raise OracleError(f"the regression report has no {metric} change (baseline value 0?)")
    return float(d["delta"])


def expect_run_vs_baseline(regression: Mapping[str, Any]) -> dict:
    return {
        "baseline_run_id": regression["baselineId"],
        "p99_change_percent": _delta(regression, "p99LatencyMs"),
        "throughput_change_percent": _delta(regression, "avgThroughputRecPerSec"),
        "regression_flagged": bool(regression["regressionDetected"]),
    }


def in_band(run: Mapping[str, Any], other: Mapping[str, Any]) -> bool:
    """Whether other counts in run's noise band: assess_run's rule
    (mcpBandMatchedOn in cli/cmd/mcp_tools_runs.go)."""
    return (
        other.get("id") != run.get("id")
        and other.get("status") == "DONE"
        and not other.get("scenarioName")
        and other.get("testType") == run.get("testType")
        and other.get("backend") == run.get("backend")
        and other.get("spec") == run.get("spec")
        and bool(other.get("requestedSpec")) == bool(run.get("requestedSpec"))
        and parse_instant(other["createdAt"]) < parse_instant(run["createdAt"])
    )


def band_runs(run: Mapping[str, Any], runs: list[Mapping[str, Any]]) -> list[dict]:
    matches = [r for r in runs if in_band(run, r)]
    matches.sort(key=lambda r: parse_instant(r["createdAt"]), reverse=True)
    return list(matches[:BAND_MAX_RUNS])


def expect_noise_band(run: Mapping[str, Any], band: list[Mapping[str, Any]], summaries: Mapping[str, Mapping[str, Any]]) -> dict:
    if len(band) < BAND_MIN_RUNS:
        raise OracleError(f"only {len(band)} earlier runs share the run's spec; a band needs {BAND_MIN_RUNS}")
    values = [float(summaries[r["id"]]["p99LatencyMs"]) for r in band]
    current = float(summaries[run["id"]]["p99LatencyMs"])
    lo, hi = min(values), max(values)
    position = "below" if current < lo else "above" if current > hi else "within"
    return {
        "band_run_count": len(band),
        "band_p99_min_ms": lo,
        "band_p99_max_ms": hi,
        "run_p99_ms": current,
        "p99_position": position,
    }


def expect_load_parallel(run: Mapping[str, Any]) -> dict:
    spec = run.get("spec") or {}
    if run.get("testType") != "LOAD" or int(spec.get("numProducers", 1)) <= 1:
        raise OracleError("the task needs a LOAD run whose spec asks for several producers")
    producers = [t for t in run.get("results") or [] if "-produce-" in str(t.get("taskId", ""))]
    return {
        "producers_that_ran": len(producers),
        "producer_throughput_rec_per_sec": sum(float(t.get("throughputRecordsPerSec", 0.0)) for t in producers),
        # A LOAD run is one producer and one consumer whatever numProducers
        # says (TestOrchestrator.java, case LOAD).
        "run_supports_sizing": False,
    }


def spec_differences(a: Mapping[str, Any], b: Mapping[str, Any]) -> list[str]:
    return sorted(k for k in set(a) | set(b) if a.get(k) != b.get(k))


def expect_baseline_spec_mismatch(regression: Mapping[str, Any], run: Mapping[str, Any], baseline: Mapping[str, Any]) -> dict:
    differing = spec_differences(run.get("spec") or {}, baseline.get("spec") or {})
    if not differing:
        raise OracleError("the run and the baseline share their spec; the task needs them to differ")
    return {
        "regression_flagged": bool(regression["regressionDetected"]),
        "comparable_to_baseline": False,
        "differing_settings": differing,
    }


def expect_topic_trend(runs: list[Mapping[str, Any]], topic: str) -> dict:
    on_topic = [r for r in runs if (r.get("spec") or {}).get("topic") == topic]
    if not on_topic:
        raise OracleError(f"no runs on topic {topic!r}")
    specs = {json.dumps(r.get("spec"), sort_keys=True) for r in on_topic}
    return {
        "runs_on_topic": len(on_topic),
        "distinct_settings": len(specs),
        "trend_across_all_meaningful": len(specs) == 1,
    }


def _topic_lag(group: Mapping[str, Any], topic: str) -> list[dict]:
    return [o for o in group.get("offsets") or [] if o.get("topic") == topic]


def expect_kates_caused_lag(check: Mapping[str, Any], runs: list[Mapping[str, Any]], group: Mapping[str, Any],
                            topic: str, since: str) -> dict:
    start = parse_instant(since)
    writers = [r for r in runs if (r.get("spec") or {}).get("topic") == topic and parse_instant(r["createdAt"]) >= start]
    if len(writers) != 1:
        raise OracleError(f"expected one Kates run writing to {topic!r} since {since}, found {[r.get('id') for r in writers]}")
    offsets = _topic_lag(group, topic)
    if not offsets:
        raise OracleError(f"group {group.get('groupId')!r} has no committed offsets on {topic!r}")
    return {
        "kafka_unhealthy": check.get("status") != "HEALTHY",
        "kates_caused": True,
        "kates_run_id": writers[0]["id"],
        "group_total_lag": sum(int(o.get("lag", 0)) for o in offsets),
        "group_active_members": int(group.get("members", 0)),
    }


def expect_lag_partitions(group: Mapping[str, Any], detail: Mapping[str, Any], topic: str) -> dict:
    offsets = _topic_lag(group, topic)
    lagging = [o for o in offsets if int(o.get("lag", 0)) > 0]
    if not lagging:
        raise OracleError(f"group {group.get('groupId')!r} has no lag on {topic!r}")
    leaders = [_partition(detail, int(o["partition"])).get("leader") for o in lagging]
    return {
        "lagging_partitions": _ids(o["partition"] for o in lagging),
        "total_lag": sum(int(o.get("lag", 0)) for o in offsets),
        "leader_broker_ids": _ids(leaders),
    }


def expect_leader_kill_preview(dryrun: Mapping[str, Any], roles: Mapping[str, str]) -> dict:
    steps = dryrun.get("steps") or []
    if len(steps) != 1 or steps[0].get("resolvedLeaderId") is None:
        raise OracleError("the dry run did not resolve the partition's leader")
    step = steps[0]
    pods = [_pod(p) for p in step.get("affectedPods") or []]
    if len(pods) != 1:
        raise OracleError(f"the dry run names {len(pods)} pods for one pod kill")
    if roles.get(pods[0]) != "broker" or broker_id_of_pod(pods[0]) != step["resolvedLeaderId"]:
        raise OracleError(f"the dry run's pod {pods[0]} is not the leader's broker pod")
    return {"leader_broker_id": step["resolvedLeaderId"], "target_pod": pods[0],
            "would_be_accepted": bool(dryrun["wouldSucceed"])}


def expect_playbook_preview(dryrun: Mapping[str, Any], roles: Mapping[str, str]) -> dict:
    pods = sorted({_pod(p) for s in dryrun.get("steps") or [] for p in s.get("affectedPods") or []})
    if not pods:
        raise OracleError("the dry run names no pods")
    return {
        "restarted_pods": pods,
        "touches_controllers": any(roles.get(p) == "controller" for p in pods),
        "would_be_accepted": bool(dryrun["wouldSucceed"]),
    }


def expect_playbook_fit(plan: Mapping[str, Any], dryrun: Mapping[str, Any], topic: str, partition: int) -> dict:
    specs = [s.get("faultSpec") or {} for s in plan.get("steps") or []]
    targets = [(f.get("targetTopic"), f.get("targetPartition", 0)) for f in specs if f.get("targetTopic")]
    topics = {t for t, _ in targets}
    if len(topics) != 1:
        raise OracleError(f"the playbook targets {sorted(map(str, topics))}; the task expects one topic")
    leaders = [s.get("resolvedLeaderId") for s in dryrun.get("steps") or [] if s.get("resolvedLeaderId") is not None]
    return {
        "playbook_fits": (topic, partition) in targets,
        "playbook_target_topic": topics.pop(),
        "playbook_target_partitions": _ids(p for _, p in targets),
        "brokers_it_would_hit_now": _ids(leaders),
    }


def expect_controller_kill_preview(dryrun: Mapping[str, Any], roles: Mapping[str, str], pod: str) -> dict:
    if roles.get(pod) != "controller":
        raise OracleError(f"{pod} is not a KRaft controller pod in the cluster topology")
    affected = {_pod(p) for s in dryrun.get("steps") or [] for p in s.get("affectedPods") or []}
    if pod not in affected:
        raise OracleError(f"the dry run does not name {pod}")
    return {
        "would_be_accepted": bool(dryrun["wouldSucceed"]),
        # DisruptionSafetyGuard.impact counts a named controller pod as
        # affecting no broker; the blast radius counts brokers only.
        "brokers_counted": sum(1 for p in affected if roles.get(p) == "broker"),
        "acceptance_means_quorum_safe": False,
    }


def deleted_pods(report: Mapping[str, Any]) -> list[str]:
    """The pods a disruption's steps took down: the DELETED events of the
    pod watch (K8sPodWatcher records the watch action as eventType)."""
    pods: list[str] = []
    for step in report.get("stepReports") or []:
        for e in step.get("podTimeline") or []:
            if e.get("eventType") == "DELETED" and e.get("podName") not in pods:
                pods.append(e["podName"])
    return pods


def _one_target(report: Mapping[str, Any]) -> str:
    pods = deleted_pods(report)
    if len(pods) != 1:
        raise OracleError(f"expected the disruption to take down one pod, its timeline shows {pods}")
    return pods[0]


def expect_disruption_debrief(report: Mapping[str, Any]) -> dict:
    steps = report.get("stepReports") or []
    if not steps:
        raise OracleError(f"the report has no steps (status {report.get('status')}): it never finished recording")
    ready = [seconds(s.get("timeToAllReady")) for s in steps]
    recovered = all(r is not None for r in ready) and not any(
        s.get("unrecoveredAfter") is not None or s.get("rolledBack") for s in steps)
    sla = report.get("slaVerdict") or {}
    return {
        "status": report["status"],
        "targeted_pod": _one_target(report),
        "recovered": recovered,
        "time_to_all_ready_seconds": max((r for r in ready if r is not None), default=0.0),
        "sla_graded": bool(sla) and sla.get("grade") not in (None, "", "-"),
        "kafka_metrics_measured": any(s.get("impactDeltas") for s in steps),
    }


def expect_disruption_compare(compare: Mapping[str, Any], current: Mapping[str, Any], baseline: Mapping[str, Any]) -> dict:
    deltas = compare.get("deltas")
    if not deltas:
        raise OracleError("the comparison has no deltas: one of the reports has no summary")
    earlier = seconds((baseline.get("summary") or {}).get("worstRecovery"))
    later = seconds((current.get("summary") or {}).get("worstRecovery"))
    if earlier is None or later is None:
        raise OracleError("a report has no worst recovery time")
    return {
        "earlier_worst_recovery_seconds": earlier,
        "later_worst_recovery_seconds": later,
        "recovery_change_seconds": float(deltas["recoveryDeltaMs"]) / 1000.0,
    }


def expect_hidden_fault_target(report: Mapping[str, Any], detail: Mapping[str, Any], partition: int) -> dict:
    lost = broker_id_of_pod(_one_target(report))
    return {"lost_broker_id": lost, "leads_partition_again": _partition(detail, partition).get("leader") == lost}


# --- oracle functions (fetch, then compute) ---------------------------------


def _plan(name: str, step_name: str, fault: Mapping[str, Any], max_brokers: int = 1) -> dict:
    """An ad-hoc plan in the shape kates disruption run --config reads."""
    return {
        "name": name,
        "maxAffectedBrokers": max_brokers,
        "autoRollback": True,
        "steps": [{
            "name": step_name,
            "faultSpec": dict({"experimentName": name, "chaosDurationSec": 30, "gracePeriodSec": 0}, **fault),
            "steadyStateSec": 30,
            "observationWindowSec": 60,
            "requireRecovery": True,
        }],
    }


def topic_min_isr(api: Any, topic: str) -> dict:
    return expect_topic_min_isr(api.get(f"/api/kafka/topics/{seg(topic)}"))


def partition_leader(api: Any, topic: str, partition: int) -> dict:
    return expect_partition_leader(api.get(f"/api/kafka/topics/{seg(topic)}"), int(partition))


def security_posture(api: Any) -> dict:
    return expect_security_posture(api.get("/api/security/audit"))


def listener_encryption(api: Any) -> dict:
    return expect_listener_encryption(api.get("/api/security/config-diff"))


def pentest_and_cve(api: Any) -> dict:
    return expect_pentest_and_cve(api.get("/api/security/pentest"), api.get("/api/security/cve"))


def security_drift(api: Any) -> dict:
    return expect_security_drift(api.get("/api/security/drift"))


def run_vs_baseline(api: Any, run_id: str) -> dict:
    return expect_run_vs_baseline(api.get(f"/api/tests/{seg(run_id)}/report/regression"))


def noise_band(api: Any, run_id: str) -> dict:
    run = api.get(f"/api/tests/{seg(run_id)}")
    runs = _paged(api, "/api/tests", {"type": run["testType"]})
    band = band_runs(run, runs)
    summaries = {r["id"]: api.get(f"/api/tests/{seg(r['id'])}/report/summary") for r in band + [run]}
    return expect_noise_band(run, band, summaries)


def load_parallel(api: Any, run_id: str) -> dict:
    return expect_load_parallel(api.get(f"/api/tests/{seg(run_id)}"))


def baseline_spec_mismatch(api: Any, run_id: str) -> dict:
    regression = api.get(f"/api/tests/{seg(run_id)}/report/regression")
    run = api.get(f"/api/tests/{seg(run_id)}")
    baseline = api.get(f"/api/tests/{seg(regression['baselineId'])}")
    return expect_baseline_spec_mismatch(regression, run, baseline)


def topic_trend(api: Any, topic: str, test_type: str = "LOAD") -> dict:
    return expect_topic_trend(_paged(api, "/api/tests", {"type": test_type}), topic)


def kates_caused_lag(api: Any, group: str, topic: str, since: str) -> dict:
    start = parse_instant(since)
    runs = _paged(api, "/api/tests", {}, stop=lambda r: parse_instant(r["createdAt"]) < start)
    return expect_kates_caused_lag(api.get("/api/cluster/check"), runs, api.get(f"/api/cluster/groups/{seg(group)}"),
                                   topic, since)


def lag_partitions(api: Any, group: str, topic: str) -> dict:
    return expect_lag_partitions(api.get(f"/api/cluster/groups/{seg(group)}"),
                                 api.get(f"/api/kafka/topics/{seg(topic)}"), topic)


def leader_kill_preview(api: Any, topic: str, partition: int) -> dict:
    plan = _plan("eval-leader-kill", "kill-leader",
                 {"disruptionType": "POD_KILL", "targetTopic": topic, "targetPartition": int(partition)})
    return expect_leader_kill_preview(api.dry_run(plan), pod_roles(api.get("/api/cluster/topology")))


def playbook_preview(api: Any, playbook: str) -> dict:
    plan = api.get(f"/api/disruptions/playbooks/{seg(playbook)}")
    return expect_playbook_preview(api.dry_run(plan), pod_roles(api.get("/api/cluster/topology")))


def playbook_fit(api: Any, playbook: str, topic: str, partition: int) -> dict:
    plan = api.get(f"/api/disruptions/playbooks/{seg(playbook)}")
    return expect_playbook_fit(plan, api.dry_run(plan), topic, int(partition))


def controller_kill_preview(api: Any, pod: str) -> dict:
    plan = _plan("eval-controller-kill", "kill-controller", {"disruptionType": "POD_KILL", "targetPod": pod})
    return expect_controller_kill_preview(api.dry_run(plan), pod_roles(api.get("/api/cluster/topology")), pod)


def disruption_debrief(api: Any, id: str) -> dict:  # noqa: A002 - the task's argument name
    return expect_disruption_debrief(api.get(f"/api/disruptions/{seg(id)}"))


def disruption_compare(api: Any, id: str, baseline_id: str) -> dict:  # noqa: A002
    compare = api.get(f"/api/disruptions/{seg(id)}/compare", {"baselineId": baseline_id})
    return expect_disruption_compare(compare, api.get(f"/api/disruptions/{seg(id)}"),
                                     api.get(f"/api/disruptions/{seg(baseline_id)}"))


def hidden_fault_target(api: Any, id: str, topic: str, partition: int) -> dict:  # noqa: A002
    return expect_hidden_fault_target(api.get(f"/api/disruptions/{seg(id)}"),
                                      api.get(f"/api/kafka/topics/{seg(topic)}"), int(partition))


ORACLES: dict[str, Callable[..., dict]] = {
    f.__name__: f
    for f in (
        topic_min_isr, partition_leader, security_posture, listener_encryption, pentest_and_cve, security_drift,
        run_vs_baseline, noise_band, load_parallel, baseline_spec_mismatch, topic_trend, kates_caused_lag,
        lag_partitions, leader_kill_preview, playbook_preview, playbook_fit, controller_kill_preview,
        disruption_debrief, disruption_compare, hidden_fault_target,
    )
}


# --- tasks -------------------------------------------------------------------


def type_errors(task: Mapping[str, Any], expected: Mapping[str, Any]) -> list[str]:
    """How an expected answer fails the task's answer_fields types."""
    errs = []
    fields = {f["name"]: f["type"] for f in task["answer_fields"]}
    if set(expected) != set(fields):
        errs.append(f"keys {sorted(expected)} are not the answer fields {sorted(fields)}")
    for name, ftype in fields.items():
        v = expected.get(name)
        ok = {
            "string": isinstance(v, str),
            "number": isinstance(v, (int, float)) and not isinstance(v, bool) and math.isfinite(v),
            "boolean": isinstance(v, bool),
            "string[]": isinstance(v, list) and all(isinstance(x, str) for x in v),
        }[ftype]
        if not ok:
            errs.append(f"{name}: {v!r} is not a {ftype}")
    return errs


def compute_task(task: Mapping[str, Any], captures: Mapping[str, Any], api: Any,
                 oracles: Mapping[str, Callable[..., dict]] | None = None) -> dict:
    """The expected answer of one task, keyed by its answer fields. oracles
    replaces this module's table (run.py --oracle, for the runner's tests)."""
    fn = (ORACLES if oracles is None else oracles).get(task["oracle"]["fn"])
    if fn is None:
        raise OracleError(f"no oracle function {task['oracle']['fn']!r}")
    try:
        args = taskfile.render(task["oracle"].get("args", {}), captures)
    except taskfile.MissingCapture as e:
        raise OracleError(f"{e}: has setup run for this task?") from e
    try:
        expected = fn(api, **args)
    except (KeyError, TypeError, ValueError, AttributeError, IndexError, taskfile.PathError) as e:
        raise OracleError(f"unexpected response shape: {type(e).__name__}: {e}") from e
    if not isinstance(expected, dict):
        raise OracleError(f"returned {type(expected).__name__}, not a dict keyed by answer field")
    errs = type_errors(task, expected)
    if errs:
        raise OracleError("; ".join(errs))
    return expected


def _now() -> str:
    return dt.datetime.now(dt.timezone.utc).isoformat(timespec="seconds").replace("+00:00", "Z")


def compute_all(doc: Mapping[str, Any], state: Mapping[str, Any], api: Any, only: list[str] | None = None) -> dict:
    """oracle.json's tasks object: for each task its expected answer, or the
    error that stopped the oracle."""
    by_id = taskfile.tasks_by_id(doc)
    out: dict[str, dict] = {}
    for tid in only or list(by_id):
        task = by_id[tid]
        entry: dict[str, Any] = {"oracle": task["oracle"]["fn"], "computed_at": _now()}
        try:
            entry["expected"] = compute_task(task, taskfile.captures_for(state, tid, by_id), api)
        except OracleError as e:
            entry["error"] = str(e)
        out[tid] = entry
    return out


def differences(before: Mapping[str, Any], after: Mapping[str, Any]) -> list[str]:
    """Tasks whose expected answer changed between two oracle.json files: a
    fact moved while the trials ran (a leader, a lag), so they are void."""
    out = []
    for tid, entry in after.get("tasks", {}).items():
        prev = before.get("tasks", {}).get(tid)
        if prev and "expected" in prev and "expected" in entry and prev["expected"] != entry["expected"]:
            out.append(tid)
    return out


def main(argv: list[str] | None = None) -> int:
    p = argparse.ArgumentParser(description=__doc__.split("\n\n")[0], formatter_class=argparse.RawDescriptionHelpFormatter)
    p.add_argument("--tasks", default=os.path.join(HERE, "tasks.json"))
    p.add_argument("--run-dir", required=True, help="the run directory: state.json is read and oracle.json written there")
    p.add_argument("--state", help="read the captures from this state.json instead of the run directory's")
    p.add_argument("--task", action="append", help="only this task (repeatable)")
    p.add_argument("--api-url", default=os.environ.get(API_URL_ENV, DEFAULT_API_URL),
                   help=f"the Kates API (default ${API_URL_ENV} or {DEFAULT_API_URL})")
    p.add_argument("--fixtures", help="serve recorded responses from this directory instead of the API")
    p.add_argument("--out", help="where to write (default <run-dir>/oracle.json; merged with what is there)")
    p.add_argument("--against", help="an earlier oracle.json; exit 3 when a task's answer changed")
    a = p.parse_args(argv)

    try:
        doc = taskfile.load(a.tasks)
    except taskfile.TaskFileError as e:
        print(f"oracle: {e}", file=sys.stderr)
        return 2
    errs = taskfile.validate(doc, ORACLES)
    if errs:
        print("oracle: invalid task file:\n  " + "\n  ".join(errs), file=sys.stderr)
        return 2
    by_id = taskfile.tasks_by_id(doc)
    unknown = [t for t in a.task or [] if t not in by_id]
    if unknown:
        print(f"oracle: unknown task {', '.join(unknown)}", file=sys.stderr)
        return 2
    try:
        with open(a.state or os.path.join(a.run_dir, "state.json"), encoding="utf-8") as f:
            state = json.load(f)
    except (OSError, ValueError) as e:
        print(f"oracle: cannot read state.json ({e}); run setup.py first", file=sys.stderr)
        return 2

    if a.fixtures:
        api: Any = FixtureAPI(a.fixtures)
    else:
        key = os.environ.get(API_KEY_ENV, "")
        if not key:
            print(f"oracle: set {API_KEY_ENV} to the human API key", file=sys.stderr)
            return 2
        api = KatesAPI(a.api_url, key)

    results = compute_all(doc, state, api, a.task)
    os.makedirs(a.run_dir, exist_ok=True)
    out_path = a.out or os.path.join(a.run_dir, "oracle.json")
    merged: dict[str, Any] = {"version": 1, "tasks": {}}
    if os.path.exists(out_path):
        try:
            with open(out_path, encoding="utf-8") as f:
                merged = json.load(f)
        except (OSError, ValueError):
            pass
    merged.update({"version": 1, "computed_at": _now(), "api_url": "fixtures" if a.fixtures else a.api_url})
    merged.setdefault("tasks", {}).update(results)
    tmp = out_path + ".tmp"
    with open(tmp, "w", encoding="utf-8") as f:
        json.dump(merged, f, indent=2, sort_keys=True)
        f.write("\n")
    os.replace(tmp, out_path)

    failed = sorted(t for t, e in results.items() if "error" in e)
    for tid in failed:
        print(f"oracle: {tid}: {results[tid]['error']}", file=sys.stderr)
    print(f"oracle: {len(results) - len(failed)} of {len(results)} tasks computed; wrote {out_path}")
    if a.against:
        with open(a.against, encoding="utf-8") as f:
            changed = differences(json.load(f), {"tasks": results})
        if changed:
            print(f"oracle: answers changed since {a.against}: {', '.join(changed)}", file=sys.stderr)
            return 3
    return 1 if failed else 0


if __name__ == "__main__":
    sys.exit(main())

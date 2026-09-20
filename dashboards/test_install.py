#!/usr/bin/env python3
"""Tests for dashboards/install.py.

Run: python3 -m unittest discover -s dashboards -p 'test_*.py'
Also: make check-install-script, and the Dashboards job in ci-kafka-charts.yml.

WHY A REAL SERVER RATHER THAN A MOCK. Every test below talks to an
`http.server` on localhost that answers like Grafana. Mocking `Grafana.request`
would test the installer's branching and nothing else, and the things most
likely to be wrong in an HTTP client are exactly what a mock removes: whether
the basic-auth header is base64 of `user:password`, whether a POST body is
actually JSON, whether a 412 is read as a conflict rather than a crash, whether
an empty 200 body becomes `None` instead of a JSON error. The fake also records
what it received, so a test can assert on the REQUEST, which is the half of the
contract a response mock cannot reach.

This file exists because install.py was one of the four documented install
routes, was called from ci-kafka-charts.yml, and had no test at all — it was
the only route whose failures would be found by a person running it.
"""

import base64
import http.server
import json
import io
import os
import socket
import sys
import tempfile
import threading
import unittest
from contextlib import redirect_stderr, redirect_stdout
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent))
import install  # noqa: E402


# ── a Grafana that records what it was asked ───────────────────────────────

class FakeGrafana:
    """Enough of Grafana's API for the installer, plus a request log.

    `routes` maps "METHOD /path" to either a dict (answered as JSON 200) or a
    `(status, body)` tuple. A path not in `routes` is a 404, which is itself
    worth testing: it is what a reverse proxy in front of the wrong service
    looks like.
    """

    def __init__(self, routes: dict):
        self.routes = routes
        self.log: list[dict] = []
        self.folders: list[dict] = []
        outer = self

        class Handler(http.server.BaseHTTPRequestHandler):
            def log_message(self, *a):
                pass

            def _handle(self, method):
                length = int(self.headers.get("Content-Length") or 0)
                raw = self.rfile.read(length).decode() if length else ""
                try:
                    payload = json.loads(raw) if raw else None
                except ValueError:
                    payload = raw
                outer.log.append({
                    "method": method,
                    "path": self.path,
                    "auth": self.headers.get("Authorization"),
                    "content_type": self.headers.get("Content-Type"),
                    "payload": payload,
                })
                key = "%s %s" % (method, self.path)
                handler = outer.routes.get(key, outer.routes.get(method + " *"))
                if handler is None:
                    body, status = json.dumps({"message": "not found"}), 404
                elif callable(handler):
                    status, obj = handler(payload)
                    body = obj if isinstance(obj, str) else json.dumps(obj)
                elif isinstance(handler, tuple):
                    status, obj = handler
                    body = obj if isinstance(obj, str) else json.dumps(obj)
                else:
                    status, body = 200, json.dumps(handler)
                encoded = body.encode()
                self.send_response(status)
                self.send_header("Content-Type", "application/json")
                self.send_header("Content-Length", str(len(encoded)))
                self.end_headers()
                self.wfile.write(encoded)

            def do_GET(self):
                self._handle("GET")

            def do_POST(self):
                self._handle("POST")

        self._server = http.server.HTTPServer(("127.0.0.1", 0), Handler)
        self.url = "http://127.0.0.1:%d" % self._server.server_address[1]

    def __enter__(self):
        self._thread = threading.Thread(target=self._server.serve_forever,
                                        daemon=True)
        self._thread.start()
        return self

    def __exit__(self, *exc):
        self._server.shutdown()
        self._server.server_close()
        self._thread.join(timeout=5)

    def requests(self, method: str, path_prefix: str = "") -> list[dict]:
        return [r for r in self.log
                if r["method"] == method and r["path"].startswith(path_prefix)]


BOARD = {
    "uid": "test-board",
    "title": "A Board",
    "id": 4242,                       # a foreign primary key; must not survive
    "tags": ["kates"],
    "panels": [{"type": "stat", "title": "One"}],
    "templating": {"list": [
        {"type": "datasource", "name": "datasource", "query": "prometheus",
         "current": {"text": "default", "value": "default"}},
        {"type": "query", "name": "namespace", "query": "label_values(up, namespace)"},
    ]},
}

HEALTHY = {
    "GET /api/health": {"version": "12.3.1", "database": "ok"},
    "GET /api/org": {"id": 1, "name": "Main Org."},
    "GET /api/folders?limit=1000": [],
    "GET /api/datasources": [
        {"name": "Prometheus", "uid": "prom-uid", "type": "prometheus"},
        {"name": "Thanos", "uid": "thanos-uid", "type": "prometheus"},
    ],
    "POST /api/folders": lambda p: (200, {"uid": "new-folder-uid",
                                          "title": (p or {}).get("title")}),
    "POST /api/dashboards/db": lambda p: (
        200, {"uid": (p or {}).get("dashboard", {}).get("uid"),
              "version": 1, "status": "success",
              "url": "/d/%s/a-board" % (p or {}).get("dashboard", {}).get("uid")}),
}


def run(argv, env=None):
    """main() with its output captured. Returns (exit code, stdout, stderr)."""
    out, err = io.StringIO(), io.StringIO()
    previous = {}
    for key, value in (env or {}).items():
        previous[key] = os.environ.get(key)
        if value is None:
            os.environ.pop(key, None)
        else:
            os.environ[key] = value
    try:
        with redirect_stdout(out), redirect_stderr(err):
            try:
                code = install.main(argv)
            except SystemExit as exc:          # sys.exit(str) in the resolvers
                code = exc.code if isinstance(exc.code, int) else 1
                if isinstance(exc.code, str):
                    err.write(exc.code)
    finally:
        for key, value in previous.items():
            if value is None:
                os.environ.pop(key, None)
            else:
                os.environ[key] = value
    return code, out.getvalue(), err.getvalue()


CLEAN_ENV = {"GRAFANA_TOKEN": None, "GRAFANA_USER": None,
             "GRAFANA_PASSWORD": None, "GRAFANA_URL": None}


# ── what gets posted ────────────────────────────────────────────────────────

class PrepareTests(unittest.TestCase):

    def test_drops_the_foreign_id(self):
        # `id` is Grafana's per-instance primary key. Posting one from another
        # Grafana is how an import lands on top of an unrelated board — the
        # uid carries identity, and it is in the file.
        prepared = install.prepare(BOARD, datasource=None, tags=[])
        self.assertNotIn("id", prepared)
        self.assertEqual(prepared["uid"], "test-board")

    def test_does_not_mutate_the_caller(self):
        before = json.dumps(BOARD, sort_keys=True)
        install.prepare(BOARD, datasource={"name": "P", "uid": "u"}, tags=["x"])
        self.assertEqual(json.dumps(BOARD, sort_keys=True), before,
                         "prepare must not edit the board it was handed — the "
                         "caller installs twelve of them from one list")

    def test_tags_merge_without_duplicating(self):
        prepared = install.prepare(BOARD, datasource=None, tags=["kates", "new"])
        self.assertEqual(prepared["tags"], ["kates", "new"])

    def test_datasource_is_set_by_uid_and_name(self):
        # Grafana keys a datasource variable's current value by UID and
        # displays the name, so both have to be written.
        prepared = install.prepare(
            BOARD, datasource={"name": "Thanos", "uid": "thanos-uid"}, tags=[])
        var = prepared["templating"]["list"][0]
        self.assertEqual(var["current"], {"text": "Thanos", "value": "thanos-uid"})

    def test_set_datasource_reports_when_there_is_no_variable(self):
        # A board with no datasource variable is not an error, but the caller
        # must be able to tell — silently doing nothing is how --datasource
        # came to look like it worked.
        self.assertFalse(install.set_datasource({"templating": {"list": []}},
                                                {"name": "P", "uid": "u"}))
        self.assertTrue(install.set_datasource(json.loads(json.dumps(BOARD)),
                                               {"name": "P", "uid": "u"}))


# ── credentials ─────────────────────────────────────────────────────────────

class AuthTests(unittest.TestCase):

    def _args(self, **kw):
        import argparse
        return argparse.Namespace(token=kw.get("token", ""),
                                  user=kw.get("user", ""))

    def test_token_flag_wins_over_environment(self):
        os.environ["GRAFANA_TOKEN"] = "from-env"
        try:
            self.assertEqual(install.auth_from(self._args(token="from-flag")),
                             ("from-flag", ""))
        finally:
            del os.environ["GRAFANA_TOKEN"]

    def test_token_wins_over_user(self):
        # Both given is a mistake, not a fallback chain: sending Basic when a
        # token was also supplied would fail in a way that reads as a bad token.
        self.assertEqual(install.auth_from(self._args(token="t", user="u:p")),
                         ("t", ""))

    def test_user_with_inline_password(self):
        self.assertEqual(install.auth_from(self._args(user="admin:secret")),
                         ("", "admin:secret"))

    def test_user_takes_the_password_from_the_environment(self):
        os.environ["GRAFANA_PASSWORD"] = "secret"
        try:
            self.assertEqual(install.auth_from(self._args(user="admin")),
                             ("", "admin:secret"))
        finally:
            del os.environ["GRAFANA_PASSWORD"]

    def test_user_without_a_password_anywhere_is_refused(self):
        os.environ.pop("GRAFANA_PASSWORD", None)
        with self.assertRaises(SystemExit) as caught:
            install.auth_from(self._args(user="admin"))
        self.assertIn("GRAFANA_PASSWORD", str(caught.exception))

    def test_basic_auth_header_is_base64_of_user_colon_password(self):
        # The one piece of this that is easy to get wrong and impossible to
        # see from the installer's own output.
        api = install.Grafana("http://x", basic="admin:secret")
        expected = base64.b64encode(b"admin:secret").decode()
        self.assertEqual(api.headers["Authorization"], "Basic " + expected)

    def test_token_header_is_a_bearer(self):
        api = install.Grafana("http://x", token="glsa_abc")
        self.assertEqual(api.headers["Authorization"], "Bearer glsa_abc")

    def test_no_credential_sends_no_authorization_header(self):
        self.assertNotIn("Authorization", install.Grafana("http://x").headers)


# ── the HTTP client ─────────────────────────────────────────────────────────

class ClientTests(unittest.TestCase):

    def test_an_empty_body_is_not_a_json_error(self):
        with FakeGrafana({"GET /api/health": (200, "")}) as fake:
            self.assertIsNone(install.Grafana(fake.url).request("GET", "/api/health"))

    def test_an_http_error_carries_grafanas_own_words(self):
        body = json.dumps({"message": "Dashboard not found", "status": "not-found"})
        with FakeGrafana({"GET /api/org": (404, body)}) as fake:
            with self.assertRaises(install.GrafanaError) as caught:
                install.Grafana(fake.url).whoami()
        self.assertEqual(caught.exception.status, 404)
        self.assertIn("Dashboard not found", str(caught.exception))

    def test_a_schema_rejection_surfaces_the_offending_field(self):
        # Grafana puts a schema complaint in validationMessage and nowhere
        # else, so a handler reading only `message` reports an empty error.
        body = json.dumps({"message": "Validation failed",
                           "validationMessage": "panels[3].type is required"})
        with FakeGrafana({"POST /api/dashboards/db": (400, body)}) as fake:
            with self.assertRaises(install.GrafanaError) as caught:
                install.Grafana(fake.url).push({"uid": "x"}, "", "m")
        self.assertIn("panels[3].type", str(caught.exception))

    def test_an_unreachable_grafana_is_an_error_not_a_traceback(self):
        # A closed port, which is what a wrong --url looks like.
        sock = socket.socket()
        sock.bind(("127.0.0.1", 0))
        port = sock.getsockname()[1]
        sock.close()
        with self.assertRaises(install.GrafanaError) as caught:
            install.Grafana("http://127.0.0.1:%d" % port, timeout=2).health()
        self.assertEqual(caught.exception.status, 0)
        self.assertIn("cannot reach", str(caught.exception))

    def test_a_very_long_error_body_is_truncated(self):
        with FakeGrafana({"GET /api/org": (500, "x" * 5000)}) as fake:
            with self.assertRaises(install.GrafanaError) as caught:
                install.Grafana(fake.url).whoami()
        self.assertLess(len(str(caught.exception)), 800)
        self.assertIn("…", str(caught.exception))


# ── resolution ──────────────────────────────────────────────────────────────

class ResolutionTests(unittest.TestCase):

    def test_datasource_resolves_by_name_and_by_uid(self):
        with FakeGrafana(HEALTHY) as fake:
            api = install.Grafana(fake.url)
            self.assertEqual(install.resolve_datasource(api, "Thanos")["uid"],
                             "thanos-uid")
            self.assertEqual(install.resolve_datasource(api, "prom-uid")["name"],
                             "Prometheus")

    def test_an_unknown_datasource_lists_the_ones_that_exist(self):
        # The error a person can act on without opening Grafana.
        with FakeGrafana(HEALTHY) as fake:
            with self.assertRaises(SystemExit) as caught:
                install.resolve_datasource(install.Grafana(fake.url), "Mimir")
        message = str(caught.exception)
        self.assertIn("Mimir", message)
        self.assertIn("Prometheus", message)
        self.assertIn("thanos-uid", message)

    def test_no_folder_means_the_general_folder(self):
        with FakeGrafana(HEALTHY) as fake:
            uid, note = install.resolve_folder(install.Grafana(fake.url), "", False)
        self.assertEqual(uid, "")
        self.assertIn("General", note)

    def test_an_existing_folder_is_reused_not_recreated(self):
        routes = dict(HEALTHY)
        routes["GET /api/folders?limit=1000"] = [
            {"uid": "existing-uid", "title": "Kates"}]
        with FakeGrafana(routes) as fake:
            uid, note = install.resolve_folder(
                install.Grafana(fake.url), "Kates", False)
            self.assertEqual(uid, "existing-uid")
            self.assertIn("exists", note)
            self.assertEqual(fake.requests("POST", "/api/folders"), [],
                             "a second run must not create a second folder")

    def test_a_missing_folder_is_created(self):
        with FakeGrafana(HEALTHY) as fake:
            uid, note = install.resolve_folder(
                install.Grafana(fake.url), "Kates", False)
            self.assertEqual(uid, "new-folder-uid")
            self.assertIn("created", note)
            created = fake.requests("POST", "/api/folders")
            self.assertEqual(len(created), 1)
            self.assertEqual(created[0]["payload"], {"title": "Kates"})

    def test_a_dry_run_does_not_create_the_folder(self):
        with FakeGrafana(HEALTHY) as fake:
            uid, note = install.resolve_folder(
                install.Grafana(fake.url), "Kates", True)
            self.assertEqual(uid, "")
            self.assertIn("WOULD BE CREATED", note)
            self.assertEqual(fake.requests("POST", "/api/folders"), [])


# ── boards on disk ──────────────────────────────────────────────────────────

class BoardFileTests(unittest.TestCase):

    def test_every_real_board_is_found_and_has_a_uid(self):
        # Against the actual dashboards/ directory: --list is a documented
        # route and this is the shape it depends on.
        found = install.board_files([])
        self.assertGreaterEqual(len(found), 12)
        uids = {}
        for name, path in found:
            board = json.loads(path.read_text())
            uid = board.get("uid")
            self.assertTrue(uid, "%s has no uid" % name)
            self.assertNotIn(uid, uids,
                             "%s and %s share uid %s" % (name, uids.get(uid), uid))
            uids[uid] = name

    def test_naming_a_board_selects_only_it(self):
        chosen = install.board_files(["kafka-kraft"])
        self.assertEqual([n for n, _ in chosen], ["kafka-kraft"])

    def test_an_unknown_board_lists_the_known_ones(self):
        with self.assertRaises(SystemExit) as caught:
            install.board_files(["kafka-kraft", "not-a-board"])
        message = str(caught.exception)
        self.assertIn("not-a-board", message)
        self.assertIn("kafka-kraft", message)
        self.assertNotIn("kafka-kraft, not-a-board", message.split("known boards:")[0],
                         "the valid name must not be reported as unknown")


# ── end to end, through main() ──────────────────────────────────────────────

class MainTests(unittest.TestCase):

    def test_list_needs_no_url_and_touches_no_network(self):
        code, out, _ = run(["--list"], CLEAN_ENV)
        self.assertEqual(code, install.EXIT_OK)
        self.assertIn("kafka-kraft", out)
        self.assertIn("uid=", out)

    def test_no_url_is_a_usage_error_not_a_failure(self):
        # Distinct exit codes: a person's mistake is not a broken Grafana, and
        # a CI job reading the code should be able to tell them apart.
        code, _, err = run([], CLEAN_ENV)
        self.assertEqual(code, install.EXIT_USAGE)
        self.assertIn("--url", err)

    def test_a_full_install_posts_every_board_once(self):
        with FakeGrafana(HEALTHY) as fake:
            code, out, err = run(["--url", fake.url, "--token", "t"], CLEAN_ENV)
        self.assertEqual(code, install.EXIT_OK, err)
        pushes = fake.requests("POST", "/api/dashboards/db")
        self.assertEqual(len(pushes), len(install.board_files([])))
        self.assertIn("Installed %d board(s)." % len(pushes), out)

    def test_every_push_is_authenticated_json_with_overwrite(self):
        with FakeGrafana(HEALTHY) as fake:
            run(["--url", fake.url, "--token", "glsa_abc",
                 "kafka-kraft"], CLEAN_ENV)
        push = fake.requests("POST", "/api/dashboards/db")[0]
        self.assertEqual(push["auth"], "Bearer glsa_abc")
        self.assertEqual(push["content_type"], "application/json")
        # overwrite:true is what makes this idempotent on the boards' own uids
        # — without it a second run fails every board with a version conflict.
        self.assertTrue(push["payload"]["overwrite"])
        self.assertNotIn("id", push["payload"]["dashboard"])

    def test_installing_twice_is_idempotent(self):
        with FakeGrafana(HEALTHY) as fake:
            first = run(["--url", fake.url, "--token", "t", "kafka-kraft"], CLEAN_ENV)
            second = run(["--url", fake.url, "--token", "t", "kafka-kraft"], CLEAN_ENV)
        self.assertEqual(first[0], install.EXIT_OK)
        self.assertEqual(second[0], install.EXIT_OK)
        pushed = [r["payload"]["dashboard"]["uid"]
                  for r in fake.requests("POST", "/api/dashboards/db")]
        self.assertEqual(len(pushed), 2)
        self.assertEqual(pushed[0], pushed[1],
                         "the same board must go to the same uid both times")
        self.assertEqual(len(fake.requests("POST", "/api/folders")), 0)

    def test_a_dry_run_reaches_grafana_but_changes_nothing(self):
        with FakeGrafana(HEALTHY) as fake:
            code, out, _ = run(["--url", fake.url, "--token", "t",
                                "--folder", "Kates", "--dry-run"], CLEAN_ENV)
        self.assertEqual(code, install.EXIT_OK)
        self.assertIn("WOULD PUSH", out)
        self.assertIn("nothing was changed", out)
        self.assertEqual(fake.requests("POST"), [],
                         "a dry run must issue no POST at all")

    def test_folder_and_datasource_flags_reach_the_pushed_board(self):
        with FakeGrafana(HEALTHY) as fake:
            code, out, err = run(["--url", fake.url, "--token", "t",
                                  "--folder", "Kates", "--datasource", "Thanos",
                                  "--tag", "prod", "kafka-kraft"], CLEAN_ENV)
        self.assertEqual(code, install.EXIT_OK, err)
        push = fake.requests("POST", "/api/dashboards/db")[0]
        self.assertEqual(push["payload"]["folderUid"], "new-folder-uid")
        self.assertIn("prod", push["payload"]["dashboard"]["tags"])
        var = [v for v in push["payload"]["dashboard"]["templating"]["list"]
               if v.get("type") == "datasource"][0]
        self.assertEqual(var["current"]["value"], "thanos-uid")

    def test_a_refused_credential_says_which_one_and_what_role(self):
        routes = dict(HEALTHY)
        routes["GET /api/org"] = (401, json.dumps({"message": "Unauthorized"}))
        with FakeGrafana(routes) as fake:
            code, _, err = run(["--url", fake.url, "--token", "bad"], CLEAN_ENV)
        self.assertEqual(code, install.EXIT_FAILED)
        self.assertIn("--token", err)
        self.assertIn("Editor", err)
        # It must stop at the org check rather than trying twelve boards.
        self.assertEqual(fake.requests("POST", "/api/dashboards/db"), [])

    def test_one_failing_board_does_not_stop_the_rest(self):
        state = {"n": 0}

        def push(payload):
            state["n"] += 1
            if state["n"] == 1:
                return 400, {"message": "Validation failed"}
            uid = payload["dashboard"]["uid"]
            return 200, {"uid": uid, "version": 1, "url": "/d/%s" % uid}

        routes = dict(HEALTHY)
        routes["POST /api/dashboards/db"] = push
        with FakeGrafana(routes) as fake:
            code, out, err = run(["--url", fake.url, "--token", "t"], CLEAN_ENV)
        total = len(install.board_files([]))
        self.assertEqual(code, install.EXIT_FAILED)
        self.assertEqual(len(fake.requests("POST", "/api/dashboards/db")), total,
                         "every board must be attempted, not just those before "
                         "the first failure")
        self.assertIn("1 of %d board(s) FAILED" % total, err)

    def test_an_unreachable_grafana_fails_cleanly(self):
        sock = socket.socket()
        sock.bind(("127.0.0.1", 0))
        port = sock.getsockname()[1]
        sock.close()
        code, _, err = run(["--url", "http://127.0.0.1:%d" % port,
                            "--token", "t"], CLEAN_ENV)
        self.assertEqual(code, install.EXIT_FAILED)
        self.assertIn("cannot talk to Grafana", err)

    def test_credentials_come_from_the_environment_too(self):
        with FakeGrafana(HEALTHY) as fake:
            code, _, err = run(["kafka-kraft"],
                               {"GRAFANA_URL": fake.url,
                                "GRAFANA_USER": "admin",
                                "GRAFANA_PASSWORD": "secret",
                                "GRAFANA_TOKEN": None})
        self.assertEqual(code, install.EXIT_OK, err)
        push = fake.requests("POST", "/api/dashboards/db")[0]
        self.assertEqual(push["auth"],
                         "Basic " + base64.b64encode(b"admin:secret").decode())

    def test_no_credential_warns_but_still_tries(self):
        # A Grafana with an anonymous admin org role is a real configuration,
        # so this is a note rather than a refusal.
        with FakeGrafana(HEALTHY) as fake:
            code, _, err = run(["--url", fake.url, "kafka-kraft"], CLEAN_ENV)
        self.assertEqual(code, install.EXIT_OK)
        self.assertIn("no credential", err)

    def test_a_board_that_is_not_json_fails_before_any_request(self):
        with tempfile.TemporaryDirectory() as tmp:
            board = Path(tmp) / "broken"
            board.mkdir()
            (board / "dashboard.json").write_text("{ not json")
            original = install.HERE
            try:
                install.HERE = Path(tmp)
                with FakeGrafana(HEALTHY) as fake:
                    code, _, err = run(["--url", fake.url, "--token", "t"],
                                       CLEAN_ENV)
                    self.assertEqual(code, install.EXIT_FAILED)
                    self.assertIn("not valid JSON", err)
                    self.assertEqual(fake.log, [],
                                     "a board that cannot be parsed must be "
                                     "caught before Grafana is touched")
            finally:
                install.HERE = original


if __name__ == "__main__":
    unittest.main(verbosity=2)


class ProvisionedHint(unittest.TestCase):
    """The signpost on the one failure route B always hits in this repo.

    kube-prometheus-stack's dashboard sidecar provisions with
    `allowUiUpdates: false`, so Grafana answers an API write with 400 "Cannot
    save provisioned dashboard". Verified against a real Grafana 12.3.1 with a
    sidecar-shaped provider: install.py fails every board, and the API's own
    message says what happened and not what to do next.
    """

    def _err(self, body, status=400):
        return install.GrafanaError(status, "POST", "/api/dashboards/db", body)

    def test_fires_on_the_message_grafana_actually_sends(self):
        hint = install.provisioned_hint(
            self._err(b'{"message":"Cannot save provisioned dashboard"}'))
        self.assertIn("helm upgrade", hint)
        self.assertIn("file-provisioned", hint)

    def test_is_case_insensitive(self):
        self.assertTrue(install.provisioned_hint(
            self._err(b'{"message":"cannot save PROVISIONED DASHBOARD"}')))

    def test_stays_quiet_on_every_other_failure(self):
        for body in (b'{"message":"Unauthorized"}',
                     b'{"message":"Dashboard title cannot be empty"}',
                     b'{"message":"Datasource not found"}',
                     b''):
            self.assertEqual(install.provisioned_hint(self._err(body)), "",
                             "hint fired on %r" % body)

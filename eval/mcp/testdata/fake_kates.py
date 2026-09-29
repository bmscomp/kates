"""A stand-in for the kates CLI in the harness tests.

It answers the commands the harness runs itself: `ctx export --name <ctx>
--reveal` (the human and agent contexts, both at FAKE_KATES_URL and, as
before plan Phase 2, with the same key unless FAKE_KATES_AGENT_KEY differs),
and the setup steps of testdata/tasks.json. Any other command fails. Each
call is echoed to stderr as JSON so the tests can see its arguments.
"""

import json
import os
import sys

KEYS = {"human": "human-key-123", "agent": os.environ.get("FAKE_KATES_AGENT_KEY", "human-key-123")}


def main(argv):
    print(json.dumps({"fake_kates": argv}), file=sys.stderr)
    url = os.environ.get("FAKE_KATES_URL", "http://127.0.0.1:9")
    if argv[:2] == ["ctx", "export"]:
        name = argv[argv.index("--name") + 1]
        if name not in KEYS:
            # kates ctx export exits 0 with nothing on stdout for a missing context.
            print(f"  ✖ Context '{name}' not found", file=sys.stderr)
            return 0
        print(f"current-context: {name}\ncontexts:\n    {name}:\n        url: {url}\n"
              f"        output: table\n        api-key: \"{KEYS[name]}\"")
        return 0
    if argv[:3] == ["security", "baseline", "--save"]:
        print(json.dumps({"status": "saved", "grade": "B", "checks": 3, "timestamp": "2026-09-26T10:00:00Z"}))
        return 0
    if argv[:2] == ["test", "list"]:
        print(json.dumps({"items": [{"id": "a1b2c3d4", "type": "LOAD"}], "total": 1}))
        return 0
    print(f"fake kates: unknown command {argv}", file=sys.stderr)
    return 1


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))

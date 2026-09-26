"""Oracle functions for the fixture tasks in testdata/tasks.json.

run.py --oracle loads a module like this one in place of oracle.py: an
ORACLES table mapping each task's oracle name to fn(api, **args), where api
reads the Kates API as the human.
"""


def security_grade(api, baseline_ts):
    audit = api.get("/api/security/audit")
    drift = api.get("/api/security/drift")
    return {
        "grade": audit["grade"],
        "failing_checks": sum(1 for c in audit["checks"] if c["status"] == "FAIL"),
        "drifted_checks": [c["name"] for c in drift["changes"]],
    }


def run_noise(api, run_id):
    assert run_id == "a1b2c3d4", run_id
    return {"verdict": "within", "p99_ms": 12.3456, "band_runs": 3}


def broken(api):
    return {"grade": 7}


ORACLES = {"security_grade": security_grade, "run_noise": run_noise, "broken": broken}

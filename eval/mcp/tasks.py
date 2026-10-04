"""Load the task list for the runner, the grader and the report, and build
the two turns every arm is asked (plans/mcp-server.md §2.4, §8.4).

The schema and its rules live once, in taskfile.py (TASKS.md documents
them): what a valid task is, how {capture} placeholders are filled, how a
JSON path reads a command's output, and which commands a task forbids. This
module adds what running and grading need on top: a loader that refuses an
invalid list before anything reaches the lab, listing every problem rather
than the first, and the question and answer request of each trial, the same
for every arm, so neither arm is told anything about tools.
"""

from __future__ import annotations

import hashlib
from pathlib import Path
from typing import Any, Iterable, Mapping

import taskfile

ARMS = taskfile.ARMS

# Plan §2.5: "With 20 tasks, 15 points is three tasks, which is why each task
# runs three times." taskfile refuses fewer trials; the report flags a task
# that ends up with fewer valid ones.
MIN_TRIALS = 3


class TaskError(ValueError):
    """The task list cannot be read or is invalid. problems lists every finding."""

    def __init__(self, problems: list[str]):
        self.problems = problems
        super().__init__("\n".join(problems))


def validate(doc: Mapping[str, Any], oracle_fns: Iterable[str] | None = None) -> list[str]:
    """Every problem with a parsed task document; empty when it is valid."""
    return taskfile.validate(doc, oracle_fns)


def load_tasks(path: Path | str, oracle_fns: Iterable[str] | None = None) -> dict[str, Any]:
    """Read and check a task file. Raises TaskError.

    oracle_fns, when given, also checks that every task's oracle function
    exists; the runner passes the ORACLES table of its --oracle module, the
    grader and the report (which read a run's frozen copy) do not need to.
    """
    try:
        doc = taskfile.load(str(path))
    except taskfile.TaskFileError as e:
        raise TaskError([str(e)]) from e
    problems = validate(doc, oracle_fns)
    if problems:
        raise TaskError(problems)
    return doc


def task_by_id(doc: Mapping[str, Any]) -> dict[str, dict[str, Any]]:
    return taskfile.tasks_by_id(doc)


def file_sha256(path: Path | str) -> str:
    h = hashlib.sha256()
    with open(path, "rb") as fh:
        for chunk in iter(lambda: fh.read(1 << 16), b""):
            h.update(chunk)
    return h.hexdigest()


# ---------------------------------------------------------------------------
# The question, then the answer form
#
# An agent that saw the answer fields before it worked would be told what to
# look for: a field "whether the penetration test sent attacks" asks the
# silent-misread question outright (plan §2.4). So each trial has two turns.
# The first asks only the task's question, and its prose is what the caveats
# and grounded claims are graded on. The second, in the same session with
# every tool denied, asks for that answer again as a JSON block with the
# task's fields, which the checks read. Both turns are the same for every arm.

_TYPE_WORDS = {
    "string": "a string",
    "number": "a number",
    "boolean": "true or false",
    "string[]": "an array of strings",
}

ANSWER_REQUEST = (
    "Now restate the answer you just gave as one fenced code block marked json that holds a single "
    "JSON object with exactly these keys:\n"
    "{fields}\n"
    "Use only what your answer above established, and null for a value it did not. Do not look "
    "anything up again, and write nothing after that block."
)


def answer_request(fields: list[dict[str, Any]]) -> str:
    """The second turn of every trial, identical for every arm."""
    lines = [
        f'- "{f["name"]}" ({_TYPE_WORDS[f["type"]]}): {f["description"]}' for f in fields
    ]
    return ANSWER_REQUEST.format(fields="\n".join(lines))


def question(task: Mapping[str, Any], captures: Mapping[str, Any], missing: str = "error") -> str:
    """The first turn: the task's prompt with its captures filled in.
    missing="placeholder" renders a capture that does not exist yet as
    <name>, for a dry run."""
    return taskfile.render(task["prompt"], captures, missing).rstrip()

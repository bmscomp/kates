"""Poll mode: the same analyses with no webhook (plans/autonomous-agents.md §2.1).

Every ANALYST_POLL_SECONDS it reads the newest DONE and FAILED runs and hands
each one it has not seen to the worker. On a store with nothing in it, the
first poll only marks what is there as seen, so starting it does not analyse
every run in the history.

    python3 poll.py
"""

import logging
import time

import receiver

LOG = logging.getLogger("run-analyst")


def poll_once(kates, store: receiver.Store, worker: receiver.Worker, seed: bool) -> list[str]:
    """Hands new finished runs to the worker; returns the ids it handed over."""
    handed = []
    for status in receiver.FINISHED:
        for run in reversed(kates.finished_runs(status)):  # in the order the runs were created
            run_id = run.get("id", "")
            if not receiver.RUN_ID.match(run_id) or not store.claim(f"{run_id}:{status}"):
                continue
            if not seed:
                worker.process(run_id)
                handed.append(run_id)
    return handed


def main() -> None:
    logging.basicConfig(level=logging.INFO, format="%(asctime)s %(levelname)s %(message)s")
    cfg = receiver.Config.from_env()
    worker, store = receiver.build_worker(cfg)
    seed = store.is_empty()
    if seed:
        LOG.info("empty store: the first poll marks existing runs as seen without analysing them")
    while True:
        try:
            poll_once(worker.kates, store, worker, seed)
            seed = False
        except Exception as e:  # noqa: BLE001 - Kates unreachable: try again next poll
            LOG.error("poll failed: %s", e)
        time.sleep(cfg.poll_seconds)


if __name__ == "__main__":
    main()

-- P3-3: optimistic locking for test_runs. refreshStatus, stopTest, the timeout
-- reaper and orphan recovery all read-modify-write the same row; without a
-- version column the last writer silently won, letting the reaper clobber a
-- completion that landed while it was working.
--
-- Rolling-upgrade safe: the column has a DEFAULT and is not NULL-without-default,
-- and on PostgreSQL 11+ adding a column with a constant default does not rewrite
-- the table, so this takes only a brief catalogue lock. PostgreSQL 11 is the
-- effective minimum for that reason.
--
-- DURING the rolling window, old replicas still UPDATE test_runs without bumping
-- `version`, so a new replica's optimistic check cannot see those writes and the
-- last writer wins exactly as it did before — the pre-existing behaviour, not a
-- regression. The guarantee only holds once every replica runs the new build; if
-- you need it during the cutover, scale to a single replica for the upgrade.
ALTER TABLE test_runs ADD COLUMN IF NOT EXISTS version BIGINT NOT NULL DEFAULT 0;

-- The two supporting indexes live in V20, which runs outside a transaction so
-- they can be built CONCURRENTLY.

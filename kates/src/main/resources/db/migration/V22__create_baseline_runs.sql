-- BaselineEntity has been mapped to baseline_runs since the baselines feature
-- landed, but no migration ever created the table. The gap was invisible
-- because the test suite runs on H2 with hibernate database.generation set to
-- drop-and-create, which fabricates the schema from the entity; production runs
-- on PostgreSQL with generation=none and Flyway owning the DDL, so every
-- baseline call (PUT/GET/DELETE /api/tests/baselines/{type} and the regression
-- report that reads from it) failed against a missing relation.
--
-- One row per test type: the @Id is the TestType enum itself, so setting a
-- baseline for a type replaces the previous one rather than accumulating.
CREATE TABLE IF NOT EXISTS baseline_runs (
    test_type VARCHAR(32) PRIMARY KEY,
    run_id VARCHAR(36) NOT NULL,
    set_at TIMESTAMP NOT NULL DEFAULT NOW()
);

-- spec_json holds the spec a run used: the request merged with its test type's
-- defaults, with every field filled in. The request itself was not kept, so a
-- stored run could not say which of those values the caller asked for and which
-- the defaults supplied. requested_spec_json keeps the request's own fields, and
-- only those. Rows written before this column existed stay NULL.
ALTER TABLE test_runs ADD COLUMN IF NOT EXISTS requested_spec_json TEXT;

-- A schedule's request_json was written through TestSpec's getters, which wrote
-- every field nobody set at its default. Runs ignored seven of those fields until
-- this release. Now each is applied, and refused where the test type cannot use
-- it, so a schedule firing would ask for CRC checks and fetch settings nobody
-- set, and be refused for every type but INTEGRITY, whose producer an explicit
-- enableIdempotence false would make non-idempotent. Remove the six defaults the
-- getters wrote for them (consumerGroup had none), so that a schedule whose
-- request left them out runs as it did. A value other than the default was set by
-- the caller: it stays, and now reaches the run, or is refused by name when the
-- schedule fires. The getters wrote compact JSON in field order, so each value is
-- followed by a comma or, for the last field, the brace that closes the spec.
UPDATE scheduled_test_runs
SET request_json =
    REPLACE(REPLACE(REPLACE(REPLACE(REPLACE(REPLACE(
    REPLACE(REPLACE(REPLACE(REPLACE(REPLACE(REPLACE(request_json,
        '"targetThroughput":-1,', ''),
        '"fetchMinBytes":1,', ''),
        '"fetchMaxWaitMs":500,', ''),
        '"enableIdempotence":false,', ''),
        '"enableTransactions":false,', ''),
        '"enableCrc":true,', ''),
        ',"targetThroughput":-1}', '}'),
        ',"fetchMinBytes":1}', '}'),
        ',"fetchMaxWaitMs":500}', '}'),
        ',"enableIdempotence":false}', '}'),
        ',"enableTransactions":false}', '}'),
        ',"enableCrc":true}', '}');

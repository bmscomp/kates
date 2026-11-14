-- The quorum member that ran the run, kates.quorum.member-id. Null with no quorum, and on runs
-- created before V30. A member's own start recovers its own runs; a new leader recovers the rest.
ALTER TABLE test_runs ADD COLUMN member_id VARCHAR(253);

-- The latest due time a schedule fired, or passed over. A tick claims a due
-- time by moving last_due forward in one conditional UPDATE, and fires only
-- when that changed a row, so each due time starts at most one run.
ALTER TABLE scheduled_test_runs ADD COLUMN last_due TIMESTAMPTZ;
ALTER TABLE disruption_schedules ADD COLUMN last_due TIMESTAMPTZ;

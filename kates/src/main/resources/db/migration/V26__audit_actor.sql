-- Who made each change (plans/mcp-server.md §5.7): the principal of the API
-- key used, as the keys file or kates.api.key names it, and whether a person
-- (human), an agent or the Kates API itself (system) acted. Rows written
-- before this have neither.
ALTER TABLE audit_events ADD COLUMN actor VARCHAR(64);
ALTER TABLE audit_events ADD COLUMN principal_type VARCHAR(16);

CREATE INDEX idx_audit_events_actor ON audit_events (actor);

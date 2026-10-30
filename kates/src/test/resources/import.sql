-- The chain head V28 inserts; tests build the schema from the entities, not Flyway.
INSERT INTO audit_chain_head (id, last_id, last_hash) VALUES (1, NULL, '0000000000000000000000000000000000000000000000000000000000000000');

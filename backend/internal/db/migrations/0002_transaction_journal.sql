ALTER TABLE snapshots ADD COLUMN request_key TEXT NOT NULL DEFAULT '';
ALTER TABLE snapshots ADD COLUMN candidate_hash TEXT NOT NULL DEFAULT '';
ALTER TABLE snapshots ADD COLUMN prior_static BLOB;
ALTER TABLE snapshots ADD COLUMN prior_dynamic BLOB;
ALTER TABLE snapshots ADD COLUMN prior_state BLOB;
ALTER TABLE snapshots ADD COLUMN result_state BLOB;
CREATE UNIQUE INDEX snapshots_request_key ON snapshots(request_key) WHERE request_key <> '';
CREATE UNIQUE INDEX config_middleware_namespace ON config_items(name) WHERE kind IN ('middleware','chain');

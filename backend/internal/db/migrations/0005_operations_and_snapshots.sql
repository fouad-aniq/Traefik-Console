-- Drafts are retired. Resource writes are direct operations; snapshots are
-- restore points the user creates on demand; the old journal becomes the
-- operations log.

-- 1. Archive anything that was still pending so no user data is dropped at upgrade.
CREATE TABLE legacy_drafts (
    kind TEXT NOT NULL,
    name TEXT NOT NULL,
    scope TEXT NOT NULL,
    status TEXT NOT NULL,
    payload TEXT,
    draft_payload TEXT,
    archived_at TEXT NOT NULL DEFAULT (datetime('now', 'utc')),
    PRIMARY KEY (kind, name)
);
INSERT INTO legacy_drafts(kind, name, scope, status, payload, draft_payload)
    SELECT kind, name, scope, status, payload, draft_payload FROM config_items WHERE status <> 'active';

CREATE TABLE legacy_secret_drafts (
    name TEXT PRIMARY KEY,
    draft_encrypted_value BLOB NOT NULL,
    archived_at TEXT NOT NULL DEFAULT (datetime('now', 'utc'))
);
INSERT INTO legacy_secret_drafts(name, draft_encrypted_value)
    SELECT name, draft_encrypted_value FROM secrets WHERE draft_encrypted_value IS NOT NULL;

-- 2. Discard drafts (same SQL as the retired DiscardAllDrafts).
DELETE FROM config_items WHERE status = 'draft_new';
UPDATE config_items SET status = 'active', draft_payload = NULL WHERE status <> 'active';
DELETE FROM secrets WHERE length(encrypted_value) = 0;
UPDATE secrets SET draft_encrypted_value = NULL;

-- 3. Per-resource optimistic concurrency.
ALTER TABLE config_items ADD COLUMN version INTEGER NOT NULL DEFAULT 1;

-- 4. The old journal becomes the operations log.
ALTER TABLE snapshots RENAME TO operations;
DROP INDEX snapshots_request_key;
CREATE UNIQUE INDEX operations_request_key ON operations(request_key) WHERE request_key <> '';
ALTER TABLE operations ADD COLUMN kind TEXT NOT NULL DEFAULT 'legacy_apply';
ALTER TABLE operations ADD COLUMN target TEXT NOT NULL DEFAULT '';
ALTER TABLE operations ADD COLUMN stage TEXT NOT NULL DEFAULT '';
ALTER TABLE operations ADD COLUMN reason_code TEXT NOT NULL DEFAULT '';
ALTER TABLE operations ADD COLUMN finished_at TEXT;
ALTER TABLE operations ADD COLUMN prior_domains BLOB;
CREATE INDEX operations_created ON operations(created_at DESC);

-- 5. User snapshots.
CREATE TABLE snapshots (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    name TEXT NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    origin TEXT NOT NULL,          -- manual | pre_restore | pre_static | scheduled | legacy_import | adopted
    pinned INTEGER NOT NULL DEFAULT 0,
    known_good INTEGER NOT NULL DEFAULT 0,
    static_yaml BLOB NOT NULL,
    dynamic_yaml BLOB NOT NULL,
    domains_yaml BLOB NOT NULL,
    state BLOB NOT NULL,           -- sealed db.State: active items + active secrets
    static_hash TEXT NOT NULL,
    dynamic_hash TEXT NOT NULL,
    domains_hash TEXT NOT NULL,
    state_hash TEXT NOT NULL,
    item_count INTEGER NOT NULL,
    size_bytes INTEGER NOT NULL,
    source_operation_id INTEGER REFERENCES operations(id),
    created_at TEXT NOT NULL DEFAULT (datetime('now', 'utc'))
);
CREATE INDEX snapshots_created ON snapshots(created_at DESC);

ALTER TABLE plugin_catalog_cache ADD COLUMN versions_json TEXT NOT NULL DEFAULT '[]';
ALTER TABLE plugin_catalog_cache ADD COLUMN plugin_type TEXT NOT NULL DEFAULT '';
CREATE TABLE plugin_catalog_refresh (
    id INTEGER PRIMARY KEY CHECK (id = 1),
    last_attempt_at TEXT NOT NULL DEFAULT '',
    last_success_at TEXT NOT NULL DEFAULT '',
    last_attempt_failed INTEGER NOT NULL DEFAULT 0
);
INSERT INTO plugin_catalog_refresh(id, last_success_at)
SELECT 1, COALESCE(MAX(fetched_at), '') FROM plugin_catalog_cache;

-- A catalog entry can be known by several module paths (its name, its import path, the module in its
-- snippet). Keep the extras so an installed plugin is matched to its entry whichever path it was installed by.
ALTER TABLE plugin_catalog_cache ADD COLUMN module_aliases_json TEXT NOT NULL DEFAULT '[]';
-- Existing rows have no aliases until the catalog is fetched again; allow that fetch right away.
UPDATE plugin_catalog_refresh SET last_attempt_at = '' WHERE id = 1;

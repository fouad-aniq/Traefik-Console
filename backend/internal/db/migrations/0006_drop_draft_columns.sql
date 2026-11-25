-- No code reads the draft columns any more; the archive tables created by 0005
-- keep whatever was pending at upgrade time.
DROP INDEX IF EXISTS idx_config_items_status;
ALTER TABLE config_items DROP COLUMN status;
ALTER TABLE config_items DROP COLUMN draft_payload;
ALTER TABLE secrets DROP COLUMN draft_encrypted_value;

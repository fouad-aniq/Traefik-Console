-- Enable Foreign Keys and WAL mode for concurrency
PRAGMA foreign_keys = ON;
PRAGMA journal_mode = WAL;

-- 1. SETTINGS: Simple key-value store for console preferences & CrowdSec LAPI config
CREATE TABLE IF NOT EXISTS settings (
    key TEXT PRIMARY KEY,
    value TEXT NOT NULL,
    updated_at TEXT NOT NULL DEFAULT (datetime('now', 'utc'))
);

-- 2. CONFIG_ITEMS: Single source of truth for all managed Traefik entities
-- Handles both Live state (payload) and Draft state (draft_payload) in one place.
CREATE TABLE IF NOT EXISTS config_items (
    id TEXT PRIMARY KEY,                    -- e.g., "router:my-app" or UUID
    kind TEXT NOT NULL,                     -- 'resolver', 'plugin', 'router', 'service', 'middleware', 'chain', 'tls'
    name TEXT NOT NULL,                     -- Traefik resource key (e.g., "letsencrypt-cf", "authelia-auth")
    scope TEXT NOT NULL,                    -- 'static' (requires restart) | 'dynamic' (hot-reload)
    status TEXT NOT NULL DEFAULT 'draft_new',
    -- status values:
    --   'active'         -> Live in Traefik, no pending changes
    --   'draft_new'      -> Created in UI, not yet deployed to Traefik
    --   'draft_modified' -> Live in Traefik (payload), has pending edits (draft_payload)
    --   'draft_deleted'  -> Live in Traefik (payload), marked for deletion on next deploy
    payload TEXT,                           -- JSON of currently active config in Traefik (NULL if draft_new)
    draft_payload TEXT,                     -- JSON of pending changes (NULL if active)
    created_at TEXT NOT NULL DEFAULT (datetime('now', 'utc')),
    updated_at TEXT NOT NULL DEFAULT (datetime('now', 'utc')),
    UNIQUE(kind, name)
);

CREATE INDEX IF NOT EXISTS idx_config_items_kind ON config_items(kind);
CREATE INDEX IF NOT EXISTS idx_config_items_status ON config_items(status);

-- 3. SECRETS: Encrypted credentials that generate 0600 secret files on disk
CREATE TABLE IF NOT EXISTS secrets (
    name TEXT PRIMARY KEY,                  -- e.g., "cf_dns_api_token"
    purpose TEXT NOT NULL,                  -- 'dns_challenge', 'crowdsec_lapi', 'basic_auth', 'mtls'
    env_var_name TEXT,                      -- e.g., "CF_DNS_API_TOKEN_FILE" (if injected into container env)
    file_path TEXT NOT NULL,                -- e.g., "/etc/traefik/secrets/cf_dns_api_token.secret"
    masked_preview TEXT NOT NULL,           -- e.g., "••••••••a9f2" (safe to send to Svelte UI)
    encrypted_value BLOB NOT NULL,          -- AES-256-GCM ciphertext (includes nonce prefix)
    draft_encrypted_value BLOB,             -- If secret was updated in current draft
    updated_at TEXT NOT NULL DEFAULT (datetime('now', 'utc'))
);

-- 4. SNAPSHOTS: Complete deployment history & 1-click rollback points
CREATE TABLE IF NOT EXISTS snapshots (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    status TEXT NOT NULL,                   -- 'working', 'rolled_back', 'failed'
    summary TEXT NOT NULL,                  -- e.g., "Added resolver letsencrypt-cf, modified router app"
    requires_restart INTEGER NOT NULL DEFAULT 0,
    static_yaml TEXT NOT NULL,              -- Exact traefik.yml content written to disk
    dynamic_yaml TEXT NOT NULL,             -- Exact dynamic.yml content written to disk
    items_backup_json TEXT NOT NULL,        -- Full JSON dump of active config_items for instant DB rollback
    error_log TEXT,                         -- Populated with Traefik/Docker logs if deployment failed & auto-rolled back
    created_at TEXT NOT NULL DEFAULT (datetime('now', 'utc'))
);

-- 5. PLUGIN_CATALOG_CACHE: Offline cache of plugins.traefik.io so UI loads instantly
CREATE TABLE IF NOT EXISTS plugin_catalog_cache (
    module_name TEXT PRIMARY KEY,           -- e.g., "github.com/maxlerebourg/crowdsec-bouncer-traefik-plugin"
    display_name TEXT NOT NULL,
    summary TEXT NOT NULL,
    latest_version TEXT NOT NULL,
    snippet_yaml TEXT NOT NULL,             -- Default dynamic config example from Traefik catalog
    fetched_at TEXT NOT NULL DEFAULT (datetime('now', 'utc'))
);

package models

// SecretRow maps 1:1 with the `secrets` table in SQLite
type SecretRow struct {
	Name           string `db:"name" json:"name"`
	Purpose        string `db:"purpose" json:"purpose"`                     // 'dns_challenge', 'crowdsec_lapi', 'basic_auth', 'mtls'
	EnvVarName     string `db:"env_var_name" json:"env_var_name,omitempty"` // e.g., "CF_DNS_API_TOKEN_FILE"
	FilePath       string `db:"file_path" json:"file_path"`                 // e.g., "/etc/traefik/secrets/cf_dns_api_token.secret"
	MaskedPreview  string `db:"masked_preview" json:"masked_preview"`       // e.g., "••••••••a9f2"
	EncryptedValue []byte `db:"encrypted_value" json:"-"`                   // AES-256-GCM ciphertext (never serialized to JSON)
	UpdatedAt      string `db:"updated_at" json:"updated_at"`
}

// CatalogPluginRow maps 1:1 with the `plugin_catalog_cache` table in SQLite
type CatalogPluginRow struct {
	ModuleName    string `db:"module_name" json:"module_name"`
	DisplayName   string `db:"display_name" json:"display_name"`
	Summary       string `db:"summary" json:"summary"`
	LatestVersion string `db:"latest_version" json:"latest_version"`
	SnippetYAML   string `db:"snippet_yaml" json:"snippet_yaml"`
	FetchedAt     string `db:"fetched_at" json:"fetched_at"`
	VersionsJSON  string `db:"versions_json" json:"-"`
	PluginType    string `db:"plugin_type" json:"plugin_type"`
	// AliasesJSON holds the other module paths the catalog publishes for this entry (never exposed).
	AliasesJSON string `db:"module_aliases_json" json:"-"`
}

// SettingRow maps 1:1 with the `settings` table in SQLite
type SettingRow struct {
	Key       string `db:"key" json:"key"`
	Value     string `db:"value" json:"value"`
	UpdatedAt string `db:"updated_at" json:"updated_at"`
}

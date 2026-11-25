package models

// CatalogPage separates search results from catalog-wide freshness metadata.
type CatalogPage struct {
	Items        []CatalogPluginRow `json:"items"`
	Total        int                `json:"total"`
	Limit        int                `json:"limit"`
	Offset       int                `json:"offset"`
	LastSyncedAt string             `json:"last_synced_at"`
}

// Install states a catalog entry can have relative to the plugins this Console knows about.
const (
	CatalogNotInstalled    = "not_installed"
	CatalogUpToDate        = "up_to_date"
	CatalogUpdateAvailable = "update_available"
	// CatalogStateUnknown means the installed inventory or a version could not be compared safely.
	CatalogStateUnknown = "unknown"
)

// CatalogPluginView is a cached catalog entry annotated with the matching installed plugin.
// The annotation is computed on every read and never stored.
type CatalogPluginView struct {
	CatalogPluginRow
	Versions         []string   `json:"versions"`
	InstallState     string     `json:"install_state"`
	InstalledAlias   string     `json:"installed_alias,omitempty"`
	InstalledVersion string     `json:"installed_version,omitempty"`
	InstalledOrigin  string     `json:"installed_origin,omitempty"`
	InstalledStatus  ItemStatus `json:"installed_status,omitempty"`
	// CanUpdate is true only for a managed plugin with a newer cataloged version.
	CanUpdate bool `json:"can_update"`
}

// CatalogPageView is CatalogPage with install-state annotated items.
type CatalogPageView struct {
	Items          []CatalogPluginView `json:"items"`
	Total          int                 `json:"total"`
	Limit          int                 `json:"limit"`
	Offset         int                 `json:"offset"`
	LastSyncedAt   string              `json:"last_synced_at"`
	InventoryKnown bool                `json:"inventory_known"`
	Refresh        CatalogRefresh      `json:"refresh"`
}

type CatalogRefresh struct {
	LastAttemptAt     string `db:"last_attempt_at" json:"last_attempt_at"`
	LastSuccessAt     string `db:"last_success_at" json:"last_success_at"`
	LastAttemptFailed bool   `db:"last_attempt_failed" json:"last_attempt_failed"`
}

type PluginCatalogSnapshot struct {
	Items   []CatalogPluginRow `json:"items"`
	Refresh CatalogRefresh     `json:"refresh"`
}

type PluginUpdate struct {
	Alias          string   `json:"alias"`
	ModuleName     string   `json:"module_name"`
	CurrentVersion string   `json:"current_version"`
	LatestVersion  string   `json:"latest_version"`
	Versions       []string `json:"versions"`
	State          string   `json:"state"`
	CanUpdate      bool     `json:"can_update"`
}

// PluginUpdateInfo is the catalog comparison attached to every installed plugin in GET /plugins.
// State is up_to_date, update_available, not_in_catalog or unknown (no successful catalog sync, or an
// uncomparable version). LatestVersion is the newest stable release (a prerelease only when the plugin
// already runs one); Versions lists every cataloged release, newest first.
type PluginUpdateInfo struct {
	State             string   `json:"state"`
	LatestVersion     string   `json:"latest_version,omitempty"`
	Versions          []string `json:"versions"`
	CanUpdate         bool     `json:"can_update"`
	CheckedAt         string   `json:"checked_at,omitempty"`
	LastAttemptFailed bool     `json:"last_attempt_failed"`
}

// PluginView is an installed plugin together with its update status.
type PluginView struct {
	UIResource[StaticPluginConfig]
	Update PluginUpdateInfo `json:"update"`
}

type PluginUpdates struct {
	Items   []PluginUpdate `json:"items"`
	Refresh CatalogRefresh `json:"refresh"`
}

package models

// OperationKind names what an operation does. Every write the console performs
// is exactly one operation.
type OperationKind string

const (
	OpUpsert  OperationKind = "upsert"
	OpDelete  OperationKind = "delete"
	OpBatch   OperationKind = "batch" // several rows committed together (plugin install, CrowdSec save)
	OpRestore OperationKind = "restore"
	OpAdopt   OperationKind = "adopt"
	// OpLegacyApply marks journal rows written by the retired draft engine.
	OpLegacyApply OperationKind = "legacy_apply"
)

// Operation statuses. The first group is terminal and healthy, the second is
// terminal and unhealthy, the third needs the engine to finish or revert work.
const (
	OpStatusSucceeded       = "succeeded"
	OpStatusRestartRequired = "restart_required"
	OpStatusRolledBack      = "rolled_back"
	OpStatusAbandoned       = "abandoned"
	OpStatusRunning         = "running"
	OpStatusRestoring       = "restoring"
	OpStatusCleanupPending  = "cleanup_pending"
	OpStatusRecovery        = "recovery_required"
)

// ItemRef identifies one resource.
type ItemRef struct {
	Kind ItemKind
	Name string
}

// Key is the "kind:name" identifier used by ExpectedVersions and diffs.
func (r ItemRef) Key() string { return string(r.Kind) + ":" + r.Name }

// Mutation is one logical edit: resources and secrets commit together or not at all.
type Mutation struct {
	Kind    OperationKind
	Summary string
	// Target is the primary resource, e.g. "router:app". Empty for multi-resource operations.
	Target        string
	Upserts       []ConfigItemRow // Payload carries the new JSON; Version is assigned at commit
	Deletes       []ItemRef
	Secrets       []SecretRow // EncryptedValue carries the new ciphertext
	SecretDeletes []string
	// ExpectedVersions maps "kind:name" to the version the client read. 0 means
	// "must not exist yet" (If-None-Match: *). Keys that are absent are unchecked.
	ExpectedVersions map[string]int64
}

// OperationResult is what Apply returns. Restart-required operations are
// successful commits that the caller reports with HTTP 202.
type OperationResult struct {
	Operation *OperationRow `json:"operation"`
	// Items are the committed rows the mutation wrote, with their new versions.
	Items    []ConfigItemRow   `json:"-"`
	Warnings []ValidationError `json:"warnings,omitempty"`
	// NoChange is true when the mutation matched the stored state and nothing was published.
	NoChange bool `json:"no_change,omitempty"`
}

// RestartRequired reports that the change is saved but Traefik must be restarted by hand.
func (r *OperationResult) RestartRequired() bool {
	return r != nil && r.Operation != nil && r.Operation.Status == OpStatusRestartRequired
}

// OperationRow maps the `operations` table (the renamed journal). The sealed
// preimages only exist while the operation runs; they never leave the server.
type OperationRow struct {
	ID              int64         `db:"id" json:"id"`
	Kind            OperationKind `db:"kind" json:"kind"`
	Target          string        `db:"target" json:"target"`
	Status          string        `db:"status" json:"status"`
	Summary         string        `db:"summary" json:"summary"`
	RequiresRestart bool          `db:"requires_restart" json:"requires_restart"`
	Stage           string        `db:"stage" json:"stage,omitempty"`
	ReasonCode      string        `db:"reason_code" json:"reason_code,omitempty"`
	ErrorLog        string        `db:"error_log" json:"error_log,omitempty"`
	CreatedAt       string        `db:"created_at" json:"created_at"`
	FinishedAt      *string       `db:"finished_at" json:"finished_at,omitempty"`
	RequestKey      string        `db:"request_key" json:"request_key,omitempty"`
	MutationHash    string        `db:"candidate_hash" json:"-"`

	StaticYAML   []byte `db:"-" json:"-"`
	DynamicYAML  []byte `db:"-" json:"-"`
	PriorStatic  []byte `db:"-" json:"-"`
	PriorDynamic []byte `db:"-" json:"-"`
	PriorDomains []byte `db:"-" json:"-"`
	PriorState   []byte `db:"-" json:"-"`
	// Journaled is false for rows that predate the journal (metadata only).
	Journaled bool `db:"-" json:"-"`
}

// SnapshotOrigin says why a snapshot exists.
const (
	SnapshotManual       = "manual"
	SnapshotPreRestore   = "pre_restore"
	SnapshotPreStatic    = "pre_static"
	SnapshotScheduled    = "scheduled"
	SnapshotLegacyImport = "legacy_import"
	SnapshotAdopted      = "adopted"
	// SnapshotInitial is the baseline taken on first start, when no snapshot exists yet.
	SnapshotInitial = "initial"
)

// SnapshotRow maps the user-visible `snapshots` table. Content columns are sealed
// at rest; the byte slices below are only populated when the sealed content is
// opened, never serialized.
type SnapshotRow struct {
	ID                int64  `db:"id" json:"id"`
	Name              string `db:"name" json:"name"`
	Description       string `db:"description" json:"description"`
	Origin            string `db:"origin" json:"origin"`
	Pinned            bool   `db:"pinned" json:"pinned"`
	KnownGood         bool   `db:"known_good" json:"known_good"`
	StaticHash        string `db:"static_hash" json:"static_hash"`
	DynamicHash       string `db:"dynamic_hash" json:"dynamic_hash"`
	DomainsHash       string `db:"domains_hash" json:"domains_hash"`
	StateHash         string `db:"state_hash" json:"state_hash"`
	ItemCount         int    `db:"item_count" json:"item_count"`
	SizeBytes         int64  `db:"size_bytes" json:"size_bytes"`
	SourceOperationID *int64 `db:"source_operation_id" json:"source_operation_id,omitempty"`
	CreatedAt         string `db:"created_at" json:"created_at"`

	StaticYAML  []byte `db:"-" json:"-"`
	DynamicYAML []byte `db:"-" json:"-"`
	DomainsYAML []byte `db:"-" json:"-"`
	State       []byte `db:"-" json:"-"`
}

// SnapshotResource is one resource captured by a snapshot (names and kinds, never payloads or secrets).
type SnapshotResource struct {
	Kind ItemKind `json:"kind"`
	Name string   `json:"name"`
}

// SnapshotConfig is the redacted view of the three files a snapshot holds.
type SnapshotConfig struct {
	ID          int64  `json:"id"`
	StaticYAML  string `json:"static_yaml"`
	DynamicYAML string `json:"dynamic_yaml"`
	DomainsYAML string `json:"domains_yaml"`
}

// SnapshotCreateRequest is the body of POST /snapshots.
type SnapshotCreateRequest struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

// SnapshotPatch updates a snapshot's metadata; nil fields are untouched.
type SnapshotPatch struct {
	Name        *string `json:"name,omitempty"`
	Description *string `json:"description,omitempty"`
	Pinned      *bool   `json:"pinned,omitempty"`
}

// RestoreRequest is the body of POST /snapshots/{id}/restore.
type RestoreRequest struct {
	RestoreToken   string `json:"restore_token"`
	IdempotencyKey string `json:"idempotency_key"`
	SkipBackup     bool   `json:"skip_backup"`
}

// RestoreResult is returned by a restore.
type RestoreResult struct {
	Operation        *OperationRow     `json:"operation"`
	BackupSnapshotID *int64            `json:"backup_snapshot_id,omitempty"`
	Warnings         []ValidationError `json:"warnings,omitempty"`
}

// ItemChange summarizes one resource a mutation or restore touches.
type ItemChange struct {
	ID     string    `json:"id"`
	Kind   ItemKind  `json:"kind"`
	Name   string    `json:"name"`
	Scope  ItemScope `json:"scope"`
	Action string    `json:"action"` // created | updated | deleted | impacted
}

const (
	ChangeCreated  = "created"
	ChangeUpdated  = "updated"
	ChangeDeleted  = "deleted"
	ChangeImpacted = "impacted" // a router whose rendered output changes because something it references changed
)

// OperationPreview is returned by ?dry_run=true and by restore-preview. It never persists anything.
type OperationPreview struct {
	Changes            []ItemChange          `json:"changes"`
	RequiresRestart    bool                  `json:"requires_restart"`
	RestartReasons     []string              `json:"restart_reasons,omitempty"`
	EnvVarsToMount     []EnvMountInstruction `json:"env_vars_to_mount,omitempty"`
	ValidationErrors   []ValidationError     `json:"validation_errors,omitempty"`   // Blocks the write if non-empty
	ValidationWarnings []ValidationError     `json:"validation_warnings,omitempty"` // Advisory
	StaticDiff         string                `json:"static_diff"`
	DynamicDiff        string                `json:"dynamic_diff"`
	DomainDiff         string                `json:"domain_diff,omitempty"`
	// RestoreToken is only set by restore previews.
	RestoreToken string `json:"restore_token,omitempty"`
	// Added/Removed/Changed list resources for restore previews.
	Added   []SnapshotResource `json:"added,omitempty"`
	Removed []SnapshotResource `json:"removed,omitempty"`
	Changed []SnapshotResource `json:"changed,omitempty"`
}

type EnvMountInstruction struct {
	EnvVarName string `json:"env_var_name"` // e.g., "CF_DNS_API_TOKEN_FILE"
	FilePath   string `json:"file_path"`    // e.g., "/etc/traefik/secrets/cf_dns_api_token.secret"
	AlreadySet bool   `json:"already_set"`  // Checked against the Traefik container via Docker inspect
}

type ValidationError struct {
	ItemID  string `json:"item_id"`
	Field   string `json:"field"`
	Message string `json:"message"` // e.g., "Middleware 'crowdsec@file' does not exist"
}

// ConfigState is GET /config/state: published vs on-disk hashes and anything that needs attention.
type ConfigState struct {
	Drifted               bool          `json:"drifted"`
	DriftedFiles          []string      `json:"drifted_files,omitempty"`
	RestartRequired       bool          `json:"restart_required"`
	RecoveryOperation     *OperationRow `json:"recovery_operation,omitempty"`
	RunningOperation      bool          `json:"running_operation"`
	StaticHash            string        `json:"static_hash"`
	DynamicHash           string        `json:"dynamic_hash"`
	DomainsHash           string        `json:"domains_hash"`
	PublishedStaticHash   string        `json:"published_static_hash"`
	PublishedDynamicHash  string        `json:"published_dynamic_hash"`
	PublishedDomainsHash  string        `json:"published_domains_hash"`
	ArchivedDrafts        int           `json:"archived_drafts"`
	ArchivedSecretDrafts  int           `json:"archived_secret_drafts"`
	LegacySnapshotsImport int           `json:"legacy_snapshots_imported"`
	// PluginUpdates counts installed plugins the synced catalog has a newer version of.
	// Advisory: 0 when the catalog or the plugin inventory cannot be read.
	PluginUpdates int `json:"plugin_updates"`
}

// WriteOptions are the per-request switches of a resource write.
type WriteOptions struct {
	// DryRun runs validation and rendering only; nothing is persisted or published.
	DryRun bool
}

// WriteResult is the response of every resource write: the committed resource and
// the operation that committed it, or (dry run) the preview of that operation.
type WriteResult[T any] struct {
	Resource  *UIResource[T]    `json:"resource,omitempty"`
	Operation *OperationRow     `json:"operation,omitempty"`
	Preview   *OperationPreview `json:"preview,omitempty"`
	Warnings  []ValidationError `json:"warnings,omitempty"`
	// RestartRequired is true when the change is saved but Traefik must be restarted by hand.
	RestartRequired bool `json:"restart_required,omitempty"`
}

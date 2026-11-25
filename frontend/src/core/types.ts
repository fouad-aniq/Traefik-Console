// Types mirrored from api/openapi.json (OpenAPI 3.1, v0.11.0).
// Keys are snake_case as sent by core-v2; only Overview/ServiceDiagnostic are camelCase.

/** Every stored resource is committed; kept as a union so old call sites keep compiling. */
export type ItemStatus = 'active';

/** Envelope returned by every list/get of a managed resource. */
export interface Resource<T> {
	id: string;
	name: string;
	config: T;
	/** Optimistic-concurrency counter: send it back as If-Match when changing the resource. */
	version: number;
	status: ItemStatus;
	origin: 'managed' | 'external';
	provider: string;
	read_only: boolean;
	live_health: string;
	live_error_msg?: string;
	updated_at?: string;
}

export interface ItemChange {
	action: string;
	id: string;
	kind: string;
	name: string;
	scope: 'static' | 'dynamic';
}

export interface SnapshotResource {
	kind: string;
	name: string;
}

export interface EnvMountInstruction {
	already_set: boolean;
	env_var_name: string;
	file_path: string;
	/** Console adds this variable to Traefik's DNS env file on Apply. */
	via_env_file?: boolean;
}

export interface ValidationIssue {
	field: string;
	item_id: string;
	message: string;
}

/** Result of `?dry_run=true`, restore-preview and snapshot comparison. Nothing was published. */
export interface OperationPreview {
	added?: SnapshotResource[] | null;
	changed?: SnapshotResource[] | null;
	removed?: SnapshotResource[] | null;
	changes: ItemChange[] | null;
	static_diff: string;
	dynamic_diff: string;
	domain_diff?: string;
	requires_restart: boolean;
	restart_reasons?: string[] | null;
	env_vars_to_mount?: EnvMountInstruction[] | null;
	validation_errors?: ValidationIssue[] | null;
	/** Advisory only; never blocks a save. */
	validation_warnings?: ValidationIssue[] | null;
	/** Only set by restore-preview; send it back to restore. */
	restore_token?: string;
}

export type OperationStatus =
	| 'running'
	| 'succeeded'
	| 'restart_required'
	| 'rolled_back'
	| 'abandoned'
	| 'restoring'
	| 'cleanup_pending'
	| 'recovery_required';

/** One entry of the operations log. */
export interface Operation {
	id: number;
	kind: string;
	target: string;
	status: OperationStatus;
	summary: string;
	stage?: string;
	reason_code?: string;
	error_log?: string;
	request_key?: string;
	requires_restart: boolean;
	created_at: string;
	finished_at?: string | null;
}

export type SnapshotOrigin = 'manual' | 'initial' | 'pre_restore' | 'pre_static' | 'scheduled' | 'legacy_import' | 'adopted';

/** A restore point: sealed state plus the exact bytes of the managed files. */
export interface Snapshot {
	id: number;
	name: string;
	description: string;
	origin: SnapshotOrigin;
	pinned: boolean;
	known_good: boolean;
	item_count: number;
	size_bytes: number;
	static_hash: string;
	dynamic_hash: string;
	domains_hash: string;
	state_hash: string;
	source_operation_id?: number | null;
	created_at: string;
}

export interface Page<T> {
	items: T[];
	/** 0 when there is no further page. */
	next_cursor: number;
}

export interface ConfigState {
	static_hash: string;
	dynamic_hash: string;
	domains_hash: string;
	published_static_hash: string;
	published_dynamic_hash: string;
	published_domains_hash: string;
	drifted: boolean;
	drifted_files: string[] | null;
	restart_required: boolean;
	running_operation: boolean;
	recovery_operation?: Operation | null;
	archived_drafts: number;
	archived_secret_drafts: number;
	legacy_snapshots_imported: number;
	/** Installed plugins with a newer version in the synced catalog (advisory, 0 when unknown). */
	plugin_updates: number;
	/** A newer Traefik Console release, when the server found one on GitHub (UPDATE_CHECK). */
	console_update?: { version: string; url: string } | null;
}

/** Body of every resource write. A dry run carries `preview` and no `operation`. */
/** R is the resource type of the endpoint (a Resource<…> envelope, or the plain config for CrowdSec). */
export interface WriteResult<R> {
	operation?: Operation | null;
	preview?: OperationPreview | null;
	resource?: R | null;
	restart_required: boolean;
	warnings?: ValidationIssue[] | null;
	/** Present on HTTP 202: the change is saved but Traefik must be restarted manually. */
	error?: { code: string; message: string } | null;
}

export interface RestoreRequest {
	restore_token: string;
	idempotency_key: string;
	skip_backup?: boolean;
}

export interface RestoreResult {
	operation: Operation;
	backup_snapshot_id?: number | null;
	warnings?: ValidationIssue[] | null;
	error?: { code: string; message: string } | null;
}

export interface Deleted {
	deleted: boolean;
}

/** Lists may be null when Go emits a nil slice. */
export const list = <T>(value: T[] | null | undefined): T[] => value ?? [];

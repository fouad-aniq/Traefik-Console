/**
 * Snapshots & operations contracts for core-v2.
 * - GET/POST /api/v1/snapshots            → restore points (keyset pages); create one on demand
 * - PATCH/DELETE /api/v1/snapshots/{id}   → rename, pin, delete (force for a pinned one)
 * - GET  /api/v1/snapshots/{id}/restore-preview → what a restore would change + restore_token
 * - POST /api/v1/snapshots/{id}/restore   → restore (takes a pre_restore backup first)
 * - GET  /api/v1/snapshots/compare/{from}/{to}
 * - GET  /api/v1/operations               → the operations log; recover / abandon a stuck one
 * - GET  /api/v1/config/state, POST /api/v1/config/adopt → drift and restart state
 */
import type { ItemChange, Operation, OperationStatus, Snapshot, SnapshotOrigin } from '../../core/types';
import { ApiError } from '../../shared/api/client';

export const KIND_ORDER = [
	'domain',
	'resolver',
	'tls',
	'router',
	'service',
	'middleware',
	'chain',
	'plugin',
	'crowdsec',
] as const;

/** Where a resource of this kind is edited; null for kinds without a delivered page yet. */
export function pathFor(kind: string, name: string): string | null {
	const n = encodeURIComponent(name);
	switch (kind) {
		case 'resolver':
			return '/resolvers/' + n;
		case 'domain':
			return '/domains/' + n;
		case 'tls':
			return '/certificates/uploaded/' + n;
		case 'router':
			return '/routers/' + n;
		case 'service':
			return '/services/' + n;
		case 'middleware':
			return '/middlewares/' + n;
		case 'chain':
			return '/chains/' + n;
		case 'plugin':
			return '/plugins/' + n;
		default:
			return null;
	}
}

/** Validation errors carry `item_id` as `<kind>:<name>`. */
export function pathForItem(itemId: string): string | null {
	const i = itemId.indexOf(':');
	return i < 1 ? null : pathFor(itemId.slice(0, i), itemId.slice(i + 1));
}

export interface ChangeGroup {
	kind: string;
	items: ItemChange[];
}
export function groupChanges(changes: ItemChange[] | null | undefined): ChangeGroup[] {
	const by = new Map<string, ItemChange[]>();
	for (const c of changes ?? []) by.set(c.kind, [...(by.get(c.kind) ?? []), c]);
	const known = KIND_ORDER.filter((k) => by.has(k));
	const other = [...by.keys()].filter((k) => !(KIND_ORDER as readonly string[]).includes(k)).sort();
	return [...known, ...other].map((kind) => ({
		kind,
		items: by.get(kind)!.toSorted((a, b) => a.name.localeCompare(b.name)),
	}));
}

export type Outcome =
	| 'recovery'
	| 'drift'
	| 'versionConflict'
	| 'restoreStale'
	| 'pinned'
	| 'quota'
	| 'rolledBack'
	| 'conflict'
	| 'notFound'
	| 'validation'
	| 'other';
/** Maps core-v2 error codes to the situations a user can act on differently. */
export function classify(e: unknown): Outcome {
	if (!(e instanceof ApiError)) return 'other';
	switch (e.code) {
		case 'ERR_OPERATION_RECOVERY_REQUIRED':
		case 'ERR_RECOVERY_REQUIRED':
			return 'recovery';
		case 'ERR_CONFIG_DRIFT':
			return 'drift';
		case 'ERR_VERSION_CONFLICT':
			return 'versionConflict';
		case 'ERR_RESTORE_STALE':
			return 'restoreStale';
		case 'ERR_SNAPSHOT_PINNED':
			return 'pinned';
		case 'ERR_SNAPSHOT_QUOTA':
			return 'quota';
		case 'ERR_DEPLOY_ROLLED_BACK':
			return 'rolledBack';
	}
	if (e.status === 409) return 'conflict';
	if (e.status === 404) return 'notFound';
	if (e.status === 422 || e.status === 400) return 'validation';
	return 'other';
}

/** Operations that block further writes until a person acts. */
export const needsAttention = (o: Operation) => o.status === 'recovery_required' || o.status === 'cleanup_pending';

export const operationTone = (s: OperationStatus) =>
	(
		({
			succeeded: 'info',
			restart_required: 'warning',
			running: 'info',
			restoring: 'info',
			rolled_back: 'warning',
			abandoned: 'warning',
			cleanup_pending: 'warning',
			recovery_required: 'error',
		}) as const
	)[s] ?? 'info';
export const operationBadge = (s: OperationStatus) =>
	(
		({
			succeeded: 'success',
			restart_required: 'warning',
			running: 'info',
			restoring: 'info',
			rolled_back: 'warning',
			abandoned: 'neutral',
			cleanup_pending: 'warning',
			recovery_required: 'danger',
		}) as const
	)[s] ?? 'neutral';

export const ORIGINS: SnapshotOrigin[] = [
	'manual',
	'initial',
	'pre_restore',
	'pre_static',
	'scheduled',
	'legacy_import',
	'adopted',
];
export const originTone = (o: SnapshotOrigin) =>
	(
		({
			manual: 'info',
			initial: 'info',
			pre_restore: 'neutral',
			pre_static: 'neutral',
			scheduled: 'neutral',
			legacy_import: 'neutral',
			adopted: 'warning',
		}) as const
	)[o] ?? 'neutral';

export const formatBytes = (n: number) =>
	n < 1024 ? `${n} B` : n < 1048576 ? `${(n / 1024).toFixed(1)} KiB` : `${(n / 1048576).toFixed(1)} MiB`;
export const snapshotLabel = (s: Pick<Snapshot, 'id' | 'name'>) => `#${s.id} · ${s.name}`;

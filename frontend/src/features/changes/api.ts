import { api } from '../../shared/api/api';
import { MUTATION_RESPONSE_TIMEOUT_MS } from '../../core/mutations';
import type { ConfigState, Deleted, Operation, OperationPreview, Page, RestoreRequest, RestoreResult, Snapshot, SnapshotResource } from '../../core/types';

const base = '/api/v1';

/** Keyset page of restore points, newest first. `cursor` is the previous page's `next_cursor` (0 = first page). */
export const listSnapshots = (limit = 25, cursor = 0, signal?: AbortSignal) =>
	api.get<Page<Snapshot>>(`${base}/snapshots?limit=${limit}${cursor ? `&cursor=${cursor}` : ''}`, { signal });

export const getSnapshot = (id: number, signal?: AbortSignal) => api.get<Snapshot>(`${base}/snapshots/${id}`, { signal });

/** On-demand restore point of what is running now (sealed state + exact file bytes). */
export const createSnapshot = (body: { name: string; description: string }, signal?: AbortSignal) =>
	api.post<Snapshot>(`${base}/snapshots`, body, { signal, timeoutMs: 60_000 });

export const patchSnapshot = (id: number, body: { name?: string; description?: string; pinned?: boolean }, signal?: AbortSignal) =>
	api.patch<Snapshot>(`${base}/snapshots/${id}`, body, { signal });

/** A pinned snapshot is only deleted with `force`; otherwise 409 ERR_SNAPSHOT_PINNED. */
export const deleteSnapshot = (id: number, force = false, signal?: AbortSignal) =>
	api.delete<Deleted>(`${base}/snapshots/${id}${force ? '?force=true' : ''}`, { signal });

/** Redacted YAML saved with the snapshot. */
export interface SnapshotConfig { id: number; static_yaml: string; dynamic_yaml: string; domains_yaml: string }
export const getSnapshotConfig = (id: number, signal?: AbortSignal) => api.get<SnapshotConfig>(`${base}/snapshots/${id}/config`, { signal });

export const getSnapshotResources = (id: number, signal?: AbortSignal) =>
	api.get<SnapshotResource[] | null>(`${base}/snapshots/${id}/resources`, { signal });

/** What restoring would change vs what runs now; carries the `restore_token` to send back. */
export const getRestorePreview = (id: number, signal?: AbortSignal) =>
	api.get<OperationPreview>(`${base}/snapshots/${id}/restore-preview`, { signal, timeoutMs: 30_000 });

/** Restores after taking a `pre_restore` backup (unless `skip_backup`). Retry only with the SAME idempotency_key. */
export const restoreSnapshot = (id: number, body: RestoreRequest, signal?: AbortSignal) =>
	api.post<RestoreResult>(`${base}/snapshots/${id}/restore`, body, { signal, timeoutMs: MUTATION_RESPONSE_TIMEOUT_MS, headers: { 'Idempotency-Key': body.idempotency_key } });

export const compareSnapshots = (from: number, to: number, signal?: AbortSignal) =>
	api.get<OperationPreview>(`${base}/snapshots/compare/${from}/${to}`, { signal, timeoutMs: 30_000 });

export const listOperations = (limit = 25, cursor = 0, signal?: AbortSignal) =>
	api.get<Page<Operation>>(`${base}/operations?limit=${limit}${cursor ? `&cursor=${cursor}` : ''}`, { signal });

export const getOperation = (id: number, signal?: AbortSignal) => api.get<Operation>(`${base}/operations/${id}`, { signal });

/** Re-runs the recovery of an interrupted operation (restores the journaled files). */
export const recoverOperation = (id: number, signal?: AbortSignal) =>
	api.post<Operation>(`${base}/operations/${id}/recover`, undefined, { signal, timeoutMs: MUTATION_RESPONSE_TIMEOUT_MS });

/** Gives up on a stuck operation and unblocks writes; the files stay as they are. */
export const abandonOperation = (id: number, signal?: AbortSignal) =>
	api.post<Operation>(`${base}/operations/${id}/abandon`, undefined, { signal });

export const getConfigState = (signal?: AbortSignal) => api.get<ConfigState>(`${base}/config/state`, { signal });

/** Accepts the files on disk as the new published baseline (clears drift); records an `adopted` snapshot. */
export const adoptConfig = (signal?: AbortSignal) => api.post<Operation>(`${base}/config/adopt`, undefined, { signal, timeoutMs: MUTATION_RESPONSE_TIMEOUT_MS });

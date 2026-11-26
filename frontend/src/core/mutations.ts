// Direct write protocol (see docs/CORE-V2-API-ANALYSIS.md §2).
//   preview (PUT/DELETE ?dry_run=true) → save (same request, no dry_run)
// A change is validated, rendered, published, verified and committed in ONE request.
// Concurrency is per resource: If-Match "<version>" to change, If-None-Match: * to create.
// Mutations are never retried automatically; an uncertain save is retried by the
// caller with the SAME Idempotency-Key so it cannot apply twice.
import { api } from '../shared/api/api';
import { ApiError, NetworkError } from '../shared/api/client';
import type { Operation, WriteResult } from './types';
import { t } from '../shared/localization/i18n';
import { flash } from '../shared/ui/flash.svelte';

export const encodeName = (name: string) => encodeURIComponent(name);

/** 16-128 chars of [A-Za-z0-9_-]; a UUID qualifies. */
export const newIdempotencyKey = () => crypto.randomUUID();

// The backend's largest apply (120s) + recovery (90s) + response reserve is 225s.
// Leave room for the structured result instead of aborting while rollback is finishing.
export const MUTATION_RESPONSE_TIMEOUT_MS = 240_000;

export interface WriteOptions {
	/** Validate and render only; the response carries `preview` and nothing is published. */
	dryRun?: boolean;
	/** Reuse the same key when retrying an exact request after a lost response. */
	idempotencyKey?: string;
	signal?: AbortSignal;
}

/** version 0 (or unknown) means "create": If-None-Match: *. */
export function preconditionHeaders(version: number | null | undefined): Record<string, string> {
	return version && version > 0 ? { 'If-Match': `"${version}"` } : { 'If-None-Match': '*' };
}

function writeInit(version: number | null | undefined, opts: WriteOptions | undefined, withPrecondition: boolean) {
	const headers: Record<string, string> = withPrecondition ? preconditionHeaders(version) : {};
	if (opts?.idempotencyKey) headers['Idempotency-Key'] = opts.idempotencyKey;
	return { headers, signal: opts?.signal, timeoutMs: opts?.dryRun ? 30_000 : MUTATION_RESPONSE_TIMEOUT_MS };
}

const withDryRun = (path: string, opts?: WriteOptions) => (opts?.dryRun ? path + (path.includes('?') ? '&' : '?') + 'dry_run=true' : path);

/**
 * The browser usually reaches the Console through the Traefik a change restarts: for a few
 * seconds the request finds no server, or Traefik's own error page. A write that carries an
 * Idempotency-Key is then sent again with the SAME key until an answer arrives; the server
 * replays the stored result, so it is never applied twice.
 */
export const reconnect = { windowMs: 90_000, delayMs: 2_000 };

/** No answer from the Console: no connection, or Traefik's own (non-JSON) error page. */
export const isReconnectable = (e: unknown) =>
	e instanceof NetworkError || (e instanceof ApiError && e.code === 'INTERNAL_ERROR' && !e.operationId && [404, 502, 503, 504].includes(e.status));

const pause = (ms: number, signal?: AbortSignal) =>
	new Promise<void>((resolve) => {
		const timer = setTimeout(resolve, ms);
		signal?.addEventListener('abort', () => { clearTimeout(timer); resolve(); }, { once: true });
	});

async function withReconnect<T>(opts: WriteOptions | undefined, send: () => Promise<T>): Promise<T> {
	if (opts?.dryRun || !opts?.idempotencyKey) return send();
	const deadline = Date.now() + reconnect.windowMs;
	let announced = false;
	for (;;) {
		try {
			return await send();
		} catch (e) {
			if (!isReconnectable(e) || Date.now() >= deadline || opts.signal?.aborted) throw e;
			if (!announced) {
				announced = true;
				flash({ tone: 'info', title: t('writeV2.reconnectingTitle'), message: t('writeV2.reconnecting') });
			}
			await pause(reconnect.delayMs, opts.signal);
		}
	}
}

/** PUT a full *Input payload. */
export const saveResource = <T>(path: string, body: unknown, version: number | null | undefined, opts?: WriteOptions) =>
	withReconnect(opts, () => api.put<WriteResult<T>>(withDryRun(path, opts), body, writeInit(version, opts, true)));

/** DELETE with the current version. Deletion is a render without the resource. */
export const removeResource = <T = unknown>(path: string, version: number, opts?: WriteOptions) =>
	withReconnect(opts, () => api.delete<WriteResult<T>>(withDryRun(path, opts), writeInit(version, opts, true)));

/** POST writes without a path-level version (plugin YAML / catalog install). */
export const postWrite = <T>(path: string, body: unknown, opts?: WriteOptions, version?: number | null) =>
	withReconnect(opts, () => api.post<WriteResult<T>>(withDryRun(path, opts), body, writeInit(version, opts, version !== undefined)));

/** True when the change is saved but Traefik could not be restarted (HTTP 202). */
export const needsManualRestart = (r: { error?: { code: string } | null; restart_required?: boolean } | null | undefined) =>
	r?.error?.code === 'ERR_MANUAL_RESTART_REQUIRED';

export const isFailed = (op: Operation | null | undefined) => !!op && (op.status === 'rolled_back' || op.status === 'recovery_required');

/** A write that changed nothing returns no operation; show it as a finished no-op. */
export const operationOf = (res: { operation?: Operation | null }, summary: string): Operation =>
	res.operation ?? { id: 0, kind: 'noop', target: '', status: 'succeeded', summary, requires_restart: false, created_at: new Date().toISOString() };

/**
 * After a successful apply, dialogs go straight back to their table and the
 * outcome is announced as a flash. Returns false when the operation did not
 * land (rolled back / needs recovery): the dialog then keeps its result view.
 */
export function announceApplied(op: Operation, manualRestart: boolean, subject: string): boolean {
	if (isFailed(op)) return false;
	const status = t('operationV2.status.' + op.status);
	flash({
		tone: manualRestart || op.status === 'restart_required' ? 'warning' : 'success',
		title: subject ? `${status} · ${subject}` : status,
		message: manualRestart ? t('writeV2.manualRestart') : op.summary,
		...(op.id ? { href: '#/changes/history/' + op.id, hrefLabel: t('operationV2.id', { id: op.id }) } : {})
	});
	return true;
}

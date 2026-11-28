import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/svelte';
// The API client binds `fetch` at import time, so install a forwarding stub first.
vi.hoisted(() => {
	(globalThis as any).__mockFetch = async () => {
		throw new Error('no mock');
	};
	globalThis.fetch = ((...args: unknown[]) => (globalThis as any).__mockFetch(...args)) as typeof fetch;
});
import ChangesPage from '../src/features/changes/ChangesPage.svelte';
import {
	classify,
	formatBytes,
	groupChanges,
	needsAttention,
	pathFor,
	pathForItem,
} from '../src/features/changes/models';
import { ApiError } from '../src/shared/api/client';

const snap = (id: number, extra = {}) => ({
	id,
	name: `snap-${id}`,
	description: '',
	origin: 'manual',
	pinned: false,
	known_good: false,
	item_count: 3,
	size_bytes: 2048,
	static_hash: 's',
	dynamic_hash: 'd',
	domains_hash: 'm',
	state_hash: 'x',
	created_at: '2026-01-0' + id + 'T00:00:00Z',
	...extra,
});
const op = (id: number, extra = {}) => ({
	id,
	kind: 'router.update',
	target: 'shop',
	status: 'succeeded',
	summary: `Changed ${id}`,
	requires_restart: false,
	created_at: '2026-01-0' + id + 'T00:00:00Z',
	...extra,
});
const page = (items: unknown[], next = 0) => ({ items, next_cursor: next });
const preview = (extra = {}) => ({
	changes: [{ action: 'updated', id: 'router:shop', kind: 'router', name: 'shop', scope: 'dynamic' }],
	static_diff: '',
	dynamic_diff: '+router: {}',
	requires_restart: false,
	restore_token: 'tok-1',
	...extra,
});
const err = (code: string, status: number, message = 'm') =>
	json({ code, message, operation_id: 'op', retryable: false }, status);

interface Call {
	method: string;
	path: string;
	headers: Record<string, string>;
	body?: any;
}
let calls: Call[] = [];
let handlers: Record<string, () => Response | Promise<Response>> = {};
const json = (body: unknown, status = 200) =>
	new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } });

beforeEach(() => {
	calls = [];
	HTMLDialogElement.prototype.showModal ??= function () {
		this.setAttribute('open', '');
	};
	HTMLDialogElement.prototype.close ??= function () {
		this.removeAttribute('open');
	};
	(globalThis as any).__mockFetch = async (path: string, init: RequestInit = {}) => {
		const headers: Record<string, string> = {};
		(init.headers as Headers).forEach((v, k) => {
			headers[k.toLowerCase()] = v;
		});
		calls.push({
			method: init.method ?? 'GET',
			path,
			headers,
			body: init.body ? JSON.parse(String(init.body)) : undefined,
		});
		const key = `${init.method ?? 'GET'} ${path}`;
		if (handlers[key]) return handlers[key]();
		return err('ERR_NOT_FOUND', 404);
	};
});
afterEach(() => {
	cleanup();
	handlers = {};
});

const lists = (snaps: unknown[] = [], ops: unknown[] = [], snapNext = 0) => ({
	'GET /api/v1/snapshots?limit=25': () => json(page(snaps, snapNext)),
	'GET /api/v1/operations?limit=25': () => json(page(ops)),
});

describe('model', () => {
	it('groups changes by kind in a stable order and links to the edit page', () => {
		const c = (kind: string, name: string) => ({
			action: 'created',
			id: `${kind}:${name}`,
			kind,
			name,
			scope: 'dynamic' as const,
		});
		const groups = groupChanges([c('service', 'b'), c('router', 'z'), c('router', 'a'), c('widget', 'w')]);
		expect(groups.map((g) => g.kind)).toEqual(['router', 'service', 'widget']);
		expect(groups[0].items.map((i) => i.name)).toEqual(['a', 'z']);
		expect(pathFor('router', 'my route')).toBe('/routers/my%20route');
		expect(pathFor('tls', 'c1')).toBe('/certificates/uploaded/c1');
		expect(pathFor('crowdsec', 'x')).toBeNull();
		expect(pathForItem('service:web')).toBe('/services/web');
		expect(pathForItem('nonsense')).toBeNull();
	});
	it('classifies core-v2 error codes', () => {
		const e = (code: string, status: number) => new ApiError({ code, status, message: 'm' } as any);
		expect(classify(e('ERR_OPERATION_RECOVERY_REQUIRED', 423))).toBe('recovery');
		expect(classify(e('ERR_CONFIG_DRIFT', 409))).toBe('drift');
		expect(classify(e('ERR_VERSION_CONFLICT', 409))).toBe('versionConflict');
		expect(classify(e('ERR_RESTORE_STALE', 409))).toBe('restoreStale');
		expect(classify(e('ERR_SNAPSHOT_PINNED', 409))).toBe('pinned');
		expect(classify(e('ERR_SNAPSHOT_QUOTA', 507))).toBe('quota');
		expect(classify(e('ERR_DEPLOY_ROLLED_BACK', 422))).toBe('rolledBack');
		expect(classify(e('ERR_DEPLOY_IN_PROGRESS', 409))).toBe('conflict');
		expect(classify(e('ERR_NOT_FOUND', 404))).toBe('notFound');
		expect(classify(new Error('x'))).toBe('other');
	});
	it('flags operations that block writes and formats sizes', () => {
		expect(needsAttention(op(1, { status: 'recovery_required' }) as any)).toBe(true);
		expect(needsAttention(op(1, { status: 'cleanup_pending' }) as any)).toBe(true);
		expect(needsAttention(op(1) as any)).toBe(false);
		expect(formatBytes(512)).toBe('512 B');
		expect(formatBytes(2048)).toBe('2.0 KiB');
		expect(formatBytes(3 * 1048576)).toBe('3.0 MiB');
	});
});

describe('snapshots tab', () => {
	it('lists restore points with origin, pin and known-good badges', async () => {
		handlers = lists([
			snap(2, { pinned: true, known_good: true, origin: 'pre_restore' }),
			snap(1, { description: 'before upgrade' }),
		]);
		render(ChangesPage, { navigate: () => {} });
		await waitFor(() => expect(screen.getAllByText('#2 · snap-2')[0]).toBeTruthy());
		expect(screen.getAllByText('#1 · snap-1')[0]).toBeTruthy();
		expect(screen.getAllByText('Pinned').length).toBeGreaterThan(0);
		expect(screen.getAllByText('Known good').length).toBeGreaterThan(0);
		expect(screen.getAllByText('Before a restore').length).toBeGreaterThan(0);
	});
	it('shows the empty state and offers to create a snapshot', async () => {
		const nav: string[] = [];
		handlers = lists();
		render(ChangesPage, { navigate: (p: string) => nav.push(p) });
		await waitFor(() => expect(screen.getByText('No snapshots yet')).toBeTruthy());
		await fireEvent.click(screen.getAllByRole('button', { name: 'Create snapshot' })[0]);
		expect(nav).toEqual(['/changes/snapshots/new']);
	});
	it('loads the next keyset page with the returned cursor', async () => {
		handlers = { ...lists([snap(3)], [], 2), 'GET /api/v1/snapshots?limit=25&cursor=2': () => json(page([snap(2)])) };
		render(ChangesPage, { navigate: () => {} });
		await fireEvent.click(await screen.findByRole('button', { name: 'Load more' }));
		await waitFor(() => expect(screen.getAllByText('#2 · snap-2')[0]).toBeTruthy());
		expect(screen.getAllByText('#3 · snap-3')[0]).toBeTruthy();
		expect(screen.queryByRole('button', { name: 'Load more' })).toBeNull();
	});
	it('shows an error with retry when the list fails', async () => {
		handlers = { ...lists(), 'GET /api/v1/snapshots?limit=25': () => err('ERR_INTERNAL', 500, 'boom') };
		render(ChangesPage, { navigate: () => {} });
		await waitFor(() => expect(screen.getAllByText('boom').length).toBeGreaterThan(0));
		handlers['GET /api/v1/snapshots?limit=25'] = () => json(page([snap(1)]));
		await fireEvent.click(screen.getAllByRole('button', { name: /retry/i })[0]);
		await waitFor(() => expect(screen.getAllByText('#1 · snap-1')[0]).toBeTruthy());
	});
	it('pins a snapshot with PATCH', async () => {
		handlers = { ...lists([snap(1)]), 'PATCH /api/v1/snapshots/1': () => json(snap(1, { pinned: true })) };
		render(ChangesPage, { navigate: () => {} });
		await fireEvent.click(await screen.findByRole('menuitem', { name: 'Pin #1' }));
		await waitFor(() => expect(screen.getAllByText('Pinned').length).toBeGreaterThan(0));
		expect(calls.find((c) => c.method === 'PATCH')!.body).toEqual({ pinned: true });
	});
	it('forces the delete of a pinned snapshot after confirmation', async () => {
		handlers = {
			...lists([snap(1, { pinned: true })]),
			'DELETE /api/v1/snapshots/1?force=true': () => json({ deleted: true }),
		};
		render(ChangesPage, { navigate: () => {} });
		await fireEvent.click(await screen.findByRole('menuitem', { name: /^Delete/ }));
		await fireEvent.click(await screen.findByRole('button', { name: 'Delete anyway' }));
		await waitFor(() => expect(screen.queryAllByText('#1 · snap-1')).toHaveLength(0));
		expect(calls.some((c) => c.method === 'DELETE' && c.path.endsWith('?force=true'))).toBe(true);
	});
	it('explains a pinned refusal from the server', async () => {
		handlers = { ...lists([snap(1)]), 'DELETE /api/v1/snapshots/1': () => err('ERR_SNAPSHOT_PINNED', 409) };
		render(ChangesPage, { navigate: () => {} });
		await fireEvent.click(await screen.findByRole('menuitem', { name: /^Delete/ }));
		const buttons = await screen.findAllByRole('button', { name: 'Delete' });
		await fireEvent.click(buttons[buttons.length - 1]);
		await waitFor(() => expect(screen.getByText(/This snapshot is pinned/)).toBeTruthy());
	});
});

const lastButton = (name: string | RegExp) => {
	const all = screen.getAllByRole('button', { name });
	return all[all.length - 1];
};

describe('create dialog', () => {
	it('requires a name, then creates the snapshot', async () => {
		handlers = { ...lists(), 'POST /api/v1/snapshots': () => json(snap(5, { name: 'before-upgrade' }), 201) };
		render(ChangesPage, { navigate: () => {}, mode: 'create' });
		await waitFor(() => expect(document.querySelector('dialog[open] input')).toBeTruthy());
		await fireEvent.click(lastButton('Create snapshot'));
		await waitFor(() => expect(screen.getByText('Enter a name.')).toBeTruthy());
		expect(calls.some((c) => c.method === 'POST')).toBe(false);
		await fireEvent.input(document.querySelector('dialog input')!, { target: { value: 'before-upgrade' } });
		await fireEvent.click(lastButton('Create snapshot'));
		await waitFor(() => expect(screen.getByText('Snapshot #5 created')).toBeTruthy());
		expect(calls.find((c) => c.method === 'POST')!.body).toEqual({ name: 'before-upgrade', description: '' });
	});
	it('explains a full quota', async () => {
		handlers = { ...lists(), 'POST /api/v1/snapshots': () => err('ERR_SNAPSHOT_QUOTA', 507) };
		render(ChangesPage, { navigate: () => {}, mode: 'create' });
		await fireEvent.input(await waitFor(() => document.querySelector('dialog input')!), { target: { value: 'x' } });
		await fireEvent.click(lastButton('Create snapshot'));
		await waitFor(() => expect(screen.getByText(/snapshot quota is reached/)).toBeTruthy());
	});
});

describe('restore', () => {
	const detail = () => ({
		'GET /api/v1/snapshots/1': () => json(snap(1)),
		'GET /api/v1/snapshots/1/restore-preview': () => json(preview()),
	});
	it('previews, then restores with the token and a stable idempotency key', async () => {
		handlers = {
			...lists([snap(1)]),
			...detail(),
			'POST /api/v1/snapshots/1/restore': () =>
				json({ operation: op(9, { kind: 'restore', summary: 'Restored snapshot 1' }), backup_snapshot_id: 8 }),
		};
		render(ChangesPage, { navigate: () => {}, mode: 'restore', id: '1' });
		await waitFor(() => expect(screen.getByText('Routers')).toBeTruthy());
		expect(screen.getByText('This replaces your current configuration')).toBeTruthy();
		await fireEvent.click(screen.getByRole('button', { name: 'Restore snapshot' }));
		await waitFor(() => expect(screen.getByText('Snapshot restored')).toBeTruthy());
		const post = calls.find((c) => c.method === 'POST')!;
		expect(post.body.restore_token).toBe('tok-1');
		expect(post.body.idempotency_key).toMatch(/^[A-Za-z0-9_-]{16,128}$/);
		expect(post.headers['idempotency-key']).toBe(post.body.idempotency_key);
		expect(screen.getByRole('button', { name: 'View backup #8' })).toBeTruthy();
	});
	it('can skip the pre-restore backup', async () => {
		handlers = {
			...lists([snap(1)]),
			...detail(),
			'POST /api/v1/snapshots/1/restore': () => json({ operation: op(9) }),
		};
		render(ChangesPage, { navigate: () => {}, mode: 'restore', id: '1' });
		await fireEvent.click(await screen.findByLabelText(/Do not back up the current configuration first/));
		await fireEvent.click(screen.getByRole('button', { name: 'Restore snapshot' }));
		await waitFor(() => expect(calls.some((c) => c.method === 'POST')).toBe(true));
		expect(calls.find((c) => c.method === 'POST')!.body.skip_backup).toBe(true);
	});
	it('reloads the preview after a stale token and uses the new one', async () => {
		let previews = 0;
		handlers = {
			...lists([snap(1)]),
			...detail(),
			'GET /api/v1/snapshots/1/restore-preview': () =>
				json(preview({ restore_token: ++previews === 1 ? 'tok-1' : 'tok-2' })),
			'POST /api/v1/snapshots/1/restore': () => err('ERR_RESTORE_STALE', 409),
		};
		render(ChangesPage, { navigate: () => {}, mode: 'restore', id: '1' });
		await fireEvent.click(await screen.findByRole('button', { name: 'Restore snapshot' }));
		await waitFor(() => expect(screen.getByText(/changed since this comparison was prepared/)).toBeTruthy());
		await waitFor(() => expect(previews).toBe(2));
		handlers['POST /api/v1/snapshots/1/restore'] = () => json({ operation: op(9) });
		await fireEvent.click(await screen.findByRole('button', { name: 'Restore snapshot' }));
		await waitFor(() => expect(calls.filter((c) => c.method === 'POST').at(-1)!.body.restore_token).toBe('tok-2'));
	});
	it('retries an unknown outcome with the very same key', async () => {
		let n = 0;
		handlers = {
			...lists([snap(1)]),
			...detail(),
			'POST /api/v1/snapshots/1/restore': () => {
				if (++n === 1) throw new TypeError('network down');
				return json({ operation: op(9) });
			},
		};
		render(ChangesPage, { navigate: () => {}, mode: 'restore', id: '1' });
		await fireEvent.click(await screen.findByRole('button', { name: 'Restore snapshot' }));
		await waitFor(() => expect(screen.getByText(/not known whether the restore ran/)).toBeTruthy());
		await fireEvent.click(screen.getByRole('button', { name: 'Retry restore' }));
		await waitFor(() => expect(screen.getByText('Snapshot restored')).toBeTruthy());
		const posts = calls.filter((c) => c.method === 'POST');
		expect(posts).toHaveLength(2);
		expect(posts[1].body.idempotency_key).toBe(posts[0].body.idempotency_key);
	});
	it('blocks restore while the preview has validation errors', async () => {
		handlers = {
			...lists([snap(1)]),
			...detail(),
			'GET /api/v1/snapshots/1/restore-preview': () =>
				json(preview({ validation_errors: [{ field: 'rule', item_id: 'router:shop', message: 'bad rule' }] })),
		};
		render(ChangesPage, { navigate: () => {}, mode: 'restore', id: '1' });
		await waitFor(() => expect(screen.getByText(/bad rule/)).toBeTruthy());
		expect((screen.getByRole('button', { name: 'Restore snapshot' }) as HTMLButtonElement).disabled).toBe(true);
	});
	it('says recovery is required instead of previewing', async () => {
		handlers = {
			...lists([snap(1)]),
			'GET /api/v1/snapshots/1': () => json(snap(1)),
			'GET /api/v1/snapshots/1/restore-preview': () => err('ERR_OPERATION_RECOVERY_REQUIRED', 423),
		};
		render(ChangesPage, { navigate: () => {}, mode: 'restore', id: '1' });
		await waitFor(() => expect(screen.getByText('Recovery required')).toBeTruthy());
	});
	it('reports an unknown snapshot', async () => {
		handlers = lists();
		render(ChangesPage, { navigate: () => {}, mode: 'restore', id: '99' });
		await waitFor(() => expect(screen.getByText('This snapshot was not found.')).toBeTruthy());
	});
	it('warns when a manual Traefik restart is needed after the restore', async () => {
		handlers = {
			...lists([snap(1)]),
			...detail(),
			'POST /api/v1/snapshots/1/restore': () =>
				json(
					{
						operation: op(9, { status: 'restart_required' }),
						error: { code: 'ERR_MANUAL_RESTART_REQUIRED', message: 'restart it' },
					},
					202,
				),
		};
		render(ChangesPage, { navigate: () => {}, mode: 'restore', id: '1' });
		await fireEvent.click(await screen.findByRole('button', { name: 'Restore snapshot' }));
		await waitFor(() => expect(screen.getByText('Restart Traefik manually')).toBeTruthy());
	});
});

describe('snapshot details', () => {
	const detail = (extra = {}) => ({
		'GET /api/v1/snapshots/1': () => json(snap(1, extra)),
		'GET /api/v1/snapshots/1/resources': () => json([{ kind: 'router', name: 'shop' }]),
		'GET /api/v1/snapshots/1/config': () =>
			json({ id: 1, static_yaml: 'entryPoints: {}\n', dynamic_yaml: 'http: {}\n', domains_yaml: '' }),
		'GET /api/v1/snapshots?limit=100': () => json(page([snap(2)])),
	});
	it('shows the resources it holds and renames it', async () => {
		handlers = {
			...lists([snap(1)]),
			...detail(),
			'PATCH /api/v1/snapshots/1': () => json(snap(1, { name: 'renamed' })),
		};
		render(ChangesPage, { navigate: () => {}, mode: 'view', id: '1' });
		await waitFor(() => expect(screen.getByText('shop')).toBeTruthy());
		await fireEvent.input(document.querySelector('dialog input') as HTMLInputElement, { target: { value: 'renamed' } });
		await fireEvent.click(screen.getByRole('button', { name: 'Save' }));
		await waitFor(() =>
			expect(calls.find((c) => c.method === 'PATCH')!.body).toEqual({ name: 'renamed', description: '' }),
		);
	});
	it('compares with another snapshot', async () => {
		handlers = {
			...lists([snap(1)]),
			...detail(),
			'GET /api/v1/snapshots/compare/2/1': () =>
				json({
					added: [{ kind: 'router', name: 'new' }],
					changed: null,
					removed: null,
					changes: [],
					static_diff: '',
					dynamic_diff: '+x',
					requires_restart: false,
				}),
		};
		render(ChangesPage, { navigate: () => {}, mode: 'view', id: '1' });
		await fireEvent.click(await screen.findByRole('tab', { name: 'Compare' }));
		await fireEvent.click(await waitFor(() => document.querySelector('dialog .dialog-content [role="combobox"]')!));
		await fireEvent.click(await screen.findByRole('option', { name: /snap-2/ }));
		await fireEvent.click(screen.getByRole('button', { name: 'Compare' }));
		await waitFor(() => expect(calls.some((c) => c.path === '/api/v1/snapshots/compare/2/1')).toBe(true));
		await waitFor(() => expect(screen.getByText('new')).toBeTruthy());
	});
});

describe('history tab', () => {
	it('lists operations and flags the ones that block writes', async () => {
		handlers = lists([], [op(2, { status: 'recovery_required', summary: 'Stuck' }), op(1)]);
		render(ChangesPage, { navigate: () => {}, tab: 'history' });
		await waitFor(() => expect(screen.getByText('Some operations need attention')).toBeTruthy());
		expect(screen.getAllByText('Changed 1').length).toBeGreaterThan(0);
	});
	it('recovers a stuck operation', async () => {
		handlers = {
			...lists([], [op(2, { status: 'recovery_required' })]),
			'GET /api/v1/operations/2': () => json(op(2, { status: 'recovery_required', error_log: 'publish failed' })),
			'POST /api/v1/operations/2/recover': () => json(op(2, { status: 'rolled_back', summary: 'Recovered' })),
		};
		render(ChangesPage, { navigate: () => {}, tab: 'history', mode: 'operation', id: '2' });
		await waitFor(() => expect(screen.getByText('This operation is blocking changes')).toBeTruthy());
		await fireEvent.click(screen.getByRole('button', { name: 'Recover' }));
		await waitFor(() => expect(screen.getAllByText('Recovered').length).toBeGreaterThan(0));
		expect(calls.some((c) => c.method === 'POST' && c.path === '/api/v1/operations/2/recover')).toBe(true);
	});
	it('abandons a stuck operation after confirmation', async () => {
		handlers = {
			...lists([], [op(2, { status: 'recovery_required' })]),
			'GET /api/v1/operations/2': () => json(op(2, { status: 'recovery_required' })),
			'POST /api/v1/operations/2/abandon': () => json(op(2, { status: 'abandoned', summary: 'Abandoned' })),
		};
		render(ChangesPage, { navigate: () => {}, tab: 'history', mode: 'operation', id: '2' });
		await fireEvent.click(await screen.findByRole('button', { name: 'Abandon' }));
		await fireEvent.click(lastButton('Abandon'));
		await waitFor(() => expect(calls.some((c) => c.path === '/api/v1/operations/2/abandon')).toBe(true));
	});
	it('tells the user to restart Traefik by hand when the operation needs it', async () => {
		handlers = {
			...lists([], [op(3, { status: 'restart_required', requires_restart: true })]),
			'GET /api/v1/operations/3': () => json(op(3, { status: 'restart_required', requires_restart: true })),
		};
		render(ChangesPage, { navigate: () => {}, tab: 'history', mode: 'operation', id: '3' });
		await waitFor(() => expect(screen.getByText('Restart Traefik manually')).toBeTruthy());
	});
});

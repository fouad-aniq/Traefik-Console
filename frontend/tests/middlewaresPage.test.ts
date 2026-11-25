import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/svelte';
// The API client binds `fetch` at import time, so install a forwarding stub first.
vi.hoisted(() => {
	(globalThis as any).__mockFetch = async () => { throw new Error('no mock'); };
	globalThis.fetch = ((...args: unknown[]) => (globalThis as any).__mockFetch(...args)) as typeof fetch;
});
import MiddlewaresPage from '../src/features/middlewares/MiddlewaresPage.svelte';
import { externalType, hasRedacted, isSensitiveKey, refersTo, serializeConfig, stateFromConfig, nativeSchema } from '../src/features/middlewares/models';
import { flashes } from '../src/shared/ui/flash.svelte';

const preview = (extra = {}) => ({ changes: [{ action: 'created', id: 'middleware:strip-api', kind: 'middleware', name: 'strip-api', scope: 'dynamic' }], static_diff: '', dynamic_diff: '+m: {}', requires_restart: false, ...extra });
const operation = (extra = {}) => ({ id: 9, kind: 'middleware.create', target: 'strip-api', status: 'succeeded', summary: 'Applied 1 change', requires_restart: false, created_at: 'now', ...extra });
const envelope = (kind: string, name: string, config: unknown, version = 1, extra = {}) => ({ id: kind + ':' + name, name, version, origin: 'managed', provider: 'file', status: 'active', read_only: false, live_health: 'unknown', updated_at: '2026-01-01T00:00:00Z', config, ...extra });
const none = { routers: [], chains: [] };
const middleware = (name: string, type = 'addPrefix', spec: unknown = { prefix: '/api' }, version = 1, used_by: unknown = none) => envelope('middleware', name, { type, spec, used_by }, version);
const chain = (name: string, members: string[] | null, version = 1, used_by: unknown = none) => envelope('chain', name, { middlewares: members, used_by }, version);
const usedBy = (routers: string[], chains: [string, string[]][] = []) => ({ routers: routers.map((r) => ({ name: r, rule: 'Host(`' + r + '.example`)', entry_points: ['websecure'], service: r, tls: true, read_only: false })), chains: chains.map(([name, members]) => ({ name, members, read_only: false })) });

interface Call { method: string; path: string; headers: Record<string, string>; body?: any }
let calls: Call[] = [];
let handlers: Record<string, () => Response | Promise<Response>> = {};
const json = (body: unknown, status = 200) => new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } });

beforeEach(() => {
	flashes.splice(0);
	calls = [];
	HTMLDialogElement.prototype.showModal ??= function () { this.setAttribute('open', ''); };
	HTMLDialogElement.prototype.close ??= function () { this.removeAttribute('open'); };
	(globalThis as any).__mockFetch = async (path: string, init: RequestInit = {}) => {
		const headers: Record<string, string> = {};
		(init.headers as Headers).forEach((v, k) => { headers[k.toLowerCase()] = v; });
		calls.push({ method: init.method ?? 'GET', path, headers, body: init.body ? JSON.parse(String(init.body)) : undefined });
		const key = `${init.method ?? 'GET'} ${path}`;
		if (handlers[key]) return handlers[key]();
		return json({ code: 'ERR_NOT_FOUND', message: 'nf', operation_id: 'op', retryable: false }, 404);
	};
});
afterEach(() => { cleanup(); handlers = {}; });

const base = (mws: unknown, chains: unknown = []) => ({
	'GET /api/v1/middlewares': () => json(mws),
	'GET /api/v1/chains': () => json(chains),
	'GET /api/v1/services': () => json([])
});

describe('model helpers', () => {
	it('round-trips redacted markers and knows which keys are secret', () => {
		expect(hasRedacted({ users: ['[redacted]'] })).toBe(true);
		expect(hasRedacted({ prefix: '/api' })).toBe(false);
		expect(isSensitiveKey('usersFile')).toBe(true);
		expect(isSensitiveKey('prefix')).toBe(false);
		const state = stateFromConfig(nativeSchema.http.basicAuth, { realm: 'r', users: ['[redacted]'], usersFile: '[redacted]' });
		expect(serializeConfig(state)).toEqual({ realm: 'r', users: ['[redacted]'], usersFile: '[redacted]' });
	});
	it('reads the real type of a Traefik-discovered middleware from its runtime object', () => {
		expect(externalType({ redirectRegex: { regex: '^/$' }, status: 'enabled', usedBy: ['x'] })).toBe('redirectRegex');
		expect(externalType({ plugin: { bouncer: {} }, status: 'enabled' })).toBe('plugin');
		expect(externalType({ status: 'enabled' })).toBe('external');
		expect(externalType(null)).toBe('external');
	});
	it('matches references with or without the @file suffix', () => {
		expect(refersTo(['auth@file', 'x'], 'auth')).toBe(true);
		expect(refersTo(null, 'auth')).toBe(false);
	});
});

describe('lists', () => {
	it('lists middlewares with the usage core-v2 sends, in one request, and blocks Delete while used', async () => {
		const navigate = vi.fn();
		handlers = base([middleware('strip-api', 'addPrefix', { prefix: '/api' }, 1, usedBy(['shop'], [['c1', ['strip-api', 'auth']]])), middleware('headers-1', 'headers', {})]);
		render(MiddlewaresPage, { navigate });
		await waitFor(() => expect(screen.getAllByText('strip-api')[0]).toBeTruthy());
		expect(calls.map((c) => c.path)).toEqual(['/api/v1/middlewares']);
		expect(screen.getByText('Not used')).toBeTruthy();
		const buttons = screen.getAllByRole('menuitem', { name: 'Delete' }) as HTMLButtonElement[];
		expect(buttons.filter((b) => b.disabled)).toHaveLength(1);
		await fireEvent.click(screen.getByRole('button', { name: '2 resources' }));
		const dialog = await screen.findByRole('dialog');
		expect(dialog.textContent).toContain('Host(`shop.example`)');
		expect(dialog.textContent).toContain('strip-api → auth');
		await fireEvent.click(screen.getByRole('button', { name: 'c1' }));
		expect(navigate).toHaveBeenCalledWith('/chains/c1');
	});
	it('shows the External badge instead of a Source column, and filters by source', async () => {
		handlers = base([middleware('mine'), { ...middleware('auth@docker', 'basicAuth', {}), origin: 'external', provider: 'docker', read_only: true }]);
		const { container } = render(MiddlewaresPage, { navigate: () => {} });
		await waitFor(() => expect(screen.getAllByText('mine')[0]).toBeTruthy());
		const headers = [...container.querySelectorAll('thead th')].map((h) => h.textContent?.trim());
		expect(headers).not.toContain('Source');
		expect(container.querySelector('tbody tr')!.querySelectorAll('td').length).toBe(headers.length);
		expect(screen.getAllByText('External').length).toBeGreaterThan(0);
	});
	it('says usage is unavailable when core-v2 could not read it', async () => {
		handlers = base([{ ...middleware('x'), config: { type: 'addPrefix', spec: {} } }]);
		render(MiddlewaresPage, { navigate: () => {} });
		await waitFor(() => expect(screen.getAllByText('x')[0]).toBeTruthy());
		expect(screen.getByText('Unavailable')).toBeTruthy();
		expect(screen.getByText(/usage could not be read/i)).toBeTruthy();
	});
	it('shows the empty state for a null list', async () => {
		handlers = base(null);
		render(MiddlewaresPage, { navigate: () => {} });
		await waitFor(() => expect(screen.getByText('No middlewares yet')).toBeTruthy());
	});
	it('reads each tab once: switching back reuses the list, a change reloads', async () => {
		handlers = { ...base([middleware('a')], [chain('web', ['a'])]), 'DELETE /api/v1/chains/web': () => json({ operation: operation(), restart_required: false }) };
		const { rerender } = render(MiddlewaresPage, { navigate: () => {}, tab: 'middlewares' });
		await waitFor(() => expect(screen.getAllByText('a')[0]).toBeTruthy());
		await rerender({ navigate: () => {}, tab: 'chains' });
		await waitFor(() => expect(screen.getAllByText('web')[0]).toBeTruthy());
		await rerender({ navigate: () => {}, tab: 'middlewares' });
		await rerender({ navigate: () => {}, tab: 'chains' });
		await waitFor(() => expect(screen.getAllByText('web')[0]).toBeTruthy());
		const gets = () => calls.filter((c) => c.method === 'GET').map((c) => c.path);
		expect(gets()).toEqual(['/api/v1/middlewares', '/api/v1/chains']);
		// Deleting a chain changes the middlewares' usage: both lists are read again when shown.
		await fireEvent.click(screen.getAllByRole('menuitem', { name: 'Delete' })[0]);
		await fireEvent.click(await screen.findByRole('button', { name: 'Confirm deletion' }));
		await waitFor(() => expect(gets()).toEqual(['/api/v1/middlewares', '/api/v1/chains', '/api/v1/chains']));
		await rerender({ navigate: () => {}, tab: 'middlewares' });
		await waitFor(() => expect(gets()).toEqual(['/api/v1/middlewares', '/api/v1/chains', '/api/v1/chains', '/api/v1/middlewares']));
	});
	it('retries a failed list from the Retry button', async () => {
		handlers = { ...base([]), 'GET /api/v1/middlewares': () => json({ code: 'ERR_INTERNAL', message: 'boom', operation_id: 'o', retryable: true }, 500) };
		render(MiddlewaresPage, { navigate: () => {} });
		await waitFor(() => expect(screen.getAllByText('boom').length).toBeGreaterThan(0));
		handlers['GET /api/v1/middlewares'] = () => json([middleware('back')]);
		await fireEvent.click(screen.getAllByRole('button', { name: /retry|try again/i })[0]);
		await waitFor(() => expect(screen.getAllByText('back')[0]).toBeTruthy());
	});
	it('lists chains with their members in order, loading only the chains', async () => {
		handlers = base([middleware('a')], [chain('web', ['a', 'b@docker', 'c', 'd', 'e']), chain('empty', null)]);
		const { container } = render(MiddlewaresPage, { navigate: () => {}, tab: 'chains' });
		await waitFor(() => expect(screen.getAllByText('web')[0]).toBeTruthy());
		expect(calls.map((c) => c.path)).toEqual(['/api/v1/chains']);
		// The cell stays compact however long the chain is; the panel lists every member in order.
		expect(container.querySelector('tbody .count')?.textContent).toBe('5 middlewares');
		const steps = [...container.querySelectorAll('.flow li')].map((li) => li.textContent?.replace(/\s+/g, ' ').trim());
		expect(steps).toEqual(['1 a', '2 b @docker', '3 c', '4 d', '5 e']);
		// The whole label (count + icon) is the hover/focus target.
		const trigger = container.querySelector('tbody .members .tooltip-trigger') as HTMLButtonElement;
		expect(trigger.textContent).toContain('5 middlewares');
		expect(trigger.getAttribute('aria-label')).toBe('Members: 5 middlewares');
		expect(container.querySelector('.value-tooltip.rich .panel-title')?.textContent).toBe('Request order');
		expect(container.textContent).toContain('—'); // the empty chain
	});
	it('deletes with the current version and surfaces the server conflict', async () => {
		handlers = {
			...base([middleware('free', 'addPrefix', {}, 5)]),
			'DELETE /api/v1/middlewares/free': () => json({ code: 'ERR_CONFLICT', message: 'middleware cannot be deleted because it is referenced by a router or chain', operation_id: 'o', retryable: false }, 409)
		};
		render(MiddlewaresPage, { navigate: () => {} });
		await waitFor(() => expect(screen.getAllByText('free')[0]).toBeTruthy());
		await fireEvent.click(screen.getByRole('menuitem', { name: 'Delete' }));
		await fireEvent.click(await screen.findByRole('button', { name: 'Confirm deletion' }));
		await waitFor(() => expect(screen.getByText(/referenced by a router or chain/)).toBeTruthy());
		expect(calls.find((c) => c.method === 'DELETE')!.headers['if-match']).toBe('"5"');
	});
});

describe('middleware create flow', () => {
	async function openAddPrefix() {
		await waitFor(() => expect(document.getElementById('middleware-name')).toBeTruthy());
		await fireEvent.input(document.getElementById('middleware-name')!, { target: { value: 'strip-api' } });
		await fireEvent.click(document.getElementById('middleware-type')!);
		await fireEvent.click(await screen.findByRole('option', { name: /Add prefix/ }));
	}
	it('previews with a dry run, then applies with a create precondition and one key', async () => {
		handlers = {
			...base([]),
			'PUT /api/v1/middlewares/strip-api?dry_run=true': () => json({ preview: preview(), restart_required: false }),
			'PUT /api/v1/middlewares/strip-api': () => json({ operation: operation(), resource: middleware('strip-api', 'addPrefix', { prefix: '/api' }), restart_required: false })
		};
		render(MiddlewaresPage, { navigate: () => {}, mode: 'create' });
		await openAddPrefix();
		await fireEvent.click(screen.getByRole('button', { name: 'Continue' }));
		await fireEvent.input(await waitFor(() => document.getElementById('mw-prefix')!), { target: { value: '/api' } });
		await fireEvent.click(screen.getByRole('button', { name: 'Review change' }));
		await screen.findByRole('button', { name: 'Apply changes' });

		const dry = calls.find((c) => c.method === 'PUT')!;
		expect(dry.path).toBe('/api/v1/middlewares/strip-api?dry_run=true');
		expect(dry.headers['if-none-match']).toBe('*');
		expect(dry.body).toEqual({ type: 'addPrefix', spec: { prefix: '/api' } });

		await fireEvent.click(await screen.findByRole('button', { name: 'Apply changes' }));
		await waitFor(() => expect(flashes.some((f) => f.title.startsWith('Applied'))).toBe(true));
		const apply = calls.filter((c) => c.method === 'PUT')[1];
		expect(apply.path).toBe('/api/v1/middlewares/strip-api');
		expect(apply.headers['idempotency-key']).toMatch(/^[A-Za-z0-9_-]{16,128}$/);
		expect(calls.some((c) => c.path.includes('/draft'))).toBe(false);
	});
	it('blocks an invalid name and a missing required field without staging', async () => {
		handlers = base([]);
		render(MiddlewaresPage, { navigate: () => {}, mode: 'create' });
		await waitFor(() => expect(document.getElementById('middleware-name')).toBeTruthy());
		await fireEvent.input(document.getElementById('middleware-name')!, { target: { value: 'Bad Name' } });
		await fireEvent.click(screen.getByRole('button', { name: 'Continue' }));
		await waitFor(() => expect(document.getElementById('middleware-name')!.getAttribute('aria-invalid')).toBe('true'));
		expect(calls.some((c) => c.method === 'PUT')).toBe(false);
	});
	it('refuses a name already used by a middleware', async () => {
		handlers = base([middleware('strip-api')]);
		render(MiddlewaresPage, { navigate: () => {}, mode: 'create' });
		await waitFor(() => expect(document.getElementById('middleware-name')).toBeTruthy());
		await fireEvent.input(document.getElementById('middleware-name')!, { target: { value: 'strip-api' } });
		await fireEvent.click(screen.getByRole('button', { name: 'Continue' }));
		await waitFor(() => expect(screen.getAllByText(/already exists/).length).toBeGreaterThan(0));
	});
});

describe('edit flow', () => {
	it('sends stored secrets back as the redacted marker so the backend keeps them', async () => {
		handlers = {
			...base([middleware('auth', 'forwardAuth', { address: 'https://auth.test/verify', authResponseHeaders: ['X-User'], tls: { key: '[redacted]' } })]),
			'PUT /api/v1/middlewares/auth?dry_run=true': () => json({ preview: preview(), restart_required: false })
		};
		render(MiddlewaresPage, { navigate: () => {}, mode: 'edit', name: 'auth' });
		await waitFor(() => expect(screen.getByText(/appear as \[redacted\]/)).toBeTruthy());
		await fireEvent.click(screen.getByRole('button', { name: 'Review change' }));
		await waitFor(() => expect(calls.some((c) => c.method === 'PUT')).toBe(true));
		const put = calls.find((c) => c.method === 'PUT')!;
		expect(put.body.type).toBe('forwardAuth');
		expect(put.body.spec.tls.key).toBe('[redacted]');
		expect(put.body.spec.address).toBe('https://auth.test/verify');
	});
	it('shows an external middleware read-only', async () => {
		handlers = base([{ ...middleware('internal-x@docker', 'external', { foo: 1 }), origin: 'external', provider: 'docker', read_only: true }]);
		render(MiddlewaresPage, { navigate: () => {}, mode: 'view', name: 'internal-x@docker' });
		await waitFor(() => expect(screen.getAllByText('internal-x@docker').length).toBeGreaterThan(0));
		expect(screen.queryByRole('menuitem', { name: 'Edit' })).toBeNull();
		expect(screen.queryByRole('button', { name: 'Edit' })).toBeNull();
	});
});

describe('edit, review and failure flows', () => {
	const openPrefixEdit = async () => {
		render(MiddlewaresPage, { navigate: () => {}, mode: 'edit', name: 'p' });
		const input = await waitFor(() => { const el = document.getElementById('mw-prefix') as HTMLInputElement | null; expect(el).toBeTruthy(); return el!; });
		await fireEvent.input(input, { target: { value: '/changed' } });
		await fireEvent.click(screen.getByRole('button', { name: 'Review change' }));
	};

	it('edits with the version it read and applies the reviewed change once', async () => {
		handlers = {
			...base([middleware('p', 'addPrefix', { prefix: '/a' }, 7)]),
			'PUT /api/v1/middlewares/p?dry_run=true': () => json({ preview: preview(), restart_required: false }),
			'PUT /api/v1/middlewares/p': () => json({ operation: operation(), resource: middleware('p', 'addPrefix', { prefix: '/changed' }, 8), restart_required: false })
		};
		await openPrefixEdit();
		await fireEvent.click(await screen.findByRole('button', { name: 'Apply changes' }));
		await waitFor(() => expect(flashes.some((f) => f.title.startsWith('Applied'))).toBe(true));
		const puts = calls.filter((c) => c.method === 'PUT');
		expect(puts.map((c) => c.headers['if-match'])).toEqual(['"7"', '"7"']);
		expect(puts[1].body).toEqual({ type: 'addPrefix', spec: { prefix: '/changed' } });
		expect(puts[1].headers['idempotency-key']).toMatch(/^[A-Za-z0-9_-]{16,128}$/);
	});

	it('offers to reload when someone else changed the middleware', async () => {
		handlers = {
			...base([middleware('p', 'addPrefix', { prefix: '/a' }, 7)]),
			'PUT /api/v1/middlewares/p?dry_run=true': () => json({ code: 'ERR_VERSION_CONFLICT', message: 'changed', operation_id: 'o', retryable: false }, 412)
		};
		await openPrefixEdit();
		expect(await screen.findByRole('button', { name: 'Reload latest version' })).toBeTruthy();
		expect(screen.queryByRole('button', { name: 'Apply changes' })).toBeNull();
	});

	it('does not let a change with server validation errors be applied', async () => {
		handlers = {
			...base([middleware('p', 'addPrefix', { prefix: '/a' })]),
			'PUT /api/v1/middlewares/p?dry_run=true': () => json({ preview: preview({ validation_errors: [{ item_id: 'middleware:p', field: 'prefix', message: 'Traefik rejects this prefix' }] }), restart_required: false })
		};
		await openPrefixEdit();
		expect(await screen.findByText(/Traefik rejects this prefix/)).toBeTruthy();
		const apply = screen.queryByRole('button', { name: 'Apply changes' }) as HTMLButtonElement | null;
		expect(apply === null || apply.disabled).toBe(true);
		expect(calls.filter((c) => c.method === 'PUT')).toHaveLength(1);
	});

	it('reports a rolled-back apply and keeps the form', async () => {
		handlers = {
			...base([middleware('p', 'addPrefix', { prefix: '/a' })]),
			'PUT /api/v1/middlewares/p?dry_run=true': () => json({ preview: preview(), restart_required: false }),
			'PUT /api/v1/middlewares/p': () => json({ code: 'ERR_DEPLOY_ROLLED_BACK', message: 'The operation could not be completed safely', operation_id: 'o', retryable: false }, 422)
		};
		await openPrefixEdit();
		await fireEvent.click(await screen.findByRole('button', { name: 'Apply changes' }));
		expect(await screen.findByText(/could not be completed safely/)).toBeTruthy();
		expect(flashes.some((f) => f.title.startsWith('Applied'))).toBe(false);
		expect((document.getElementById('mw-prefix') as HTMLInputElement).value).toBe('/changed');
	});

	it('views a managed middleware as its Traefik YAML', async () => {
		handlers = base([middleware('allow', 'ipAllowList', { sourceRange: ['192.168.1.0/24', '172.16.0.0/24'] })]);
		render(MiddlewaresPage, { navigate: () => {}, mode: 'view', name: 'allow' });
		await waitFor(() => expect(document.querySelector('.resource-dialog pre')).toBeTruthy());
		expect(document.querySelector('.resource-dialog pre')!.textContent).toBe('ipAllowList:\n  sourceRange:\n    - 192.168.1.0/24\n    - 172.16.0.0/24\n');
		expect(screen.getByRole('button', { name: 'Edit' })).toBeTruthy();
	});

	it('offers no Edit or Delete for external middlewares in the table', async () => {
		handlers = base([{ ...middleware('auth@docker', 'external', { basicAuth: {} }), origin: 'external', provider: 'docker', read_only: true }]);
		render(MiddlewaresPage, { navigate: () => {} });
		await waitFor(() => expect(screen.getAllByText('auth@docker')[0]).toBeTruthy());
		// read-only row: a single inline View button, no "More actions" menu
		expect(screen.getByRole('button', { name: 'View' })).toBeTruthy();
		expect(screen.queryByRole('button', { name: /more actions/i })).toBeNull();
		expect(screen.queryByRole('button', { name: 'Edit' })).toBeNull();
		expect(screen.queryByRole('menuitem', { name: 'Edit' })).toBeNull();
		expect(screen.queryByRole('menuitem', { name: 'Delete' })).toBeNull();
	});

	it('keeps stored Basic Auth users when the user does not replace them', async () => {
		handlers = {
			...base([middleware('auth', 'basicAuth', { usersFile: '[redacted]', realm: 'old' })]),
			'PUT /api/v1/middlewares/auth?dry_run=true': () => json({ preview: preview(), restart_required: false })
		};
		render(MiddlewaresPage, { navigate: () => {}, mode: 'edit', name: 'auth' });
		await waitFor(() => expect(document.getElementById('middleware-users-mode')).toBeTruthy());
		expect(document.getElementById('credential-0-username')).toBeNull(); // nothing to retype
		await fireEvent.click(screen.getByRole('button', { name: 'Review change' }));
		await waitFor(() => expect(calls.some((c) => c.method === 'PUT')).toBe(true));
		const spec = calls.find((c) => c.method === 'PUT')!.body.spec;
		expect(spec.usersFile).toBe('[redacted]');
		expect(spec.users_credentials).toBeUndefined();
	});
});

describe('basic auth users', () => {
	async function openBasicAuth() {
		await waitFor(() => expect(document.getElementById('middleware-name')).toBeTruthy());
		await fireEvent.input(document.getElementById('middleware-name')!, { target: { value: 'auth' } });
		await fireEvent.click(document.getElementById('middleware-type')!);
		await fireEvent.click(await screen.findByRole('option', { name: /Basic auth/i }));
		await fireEvent.click(screen.getByRole('button', { name: 'Continue' }));
		await waitFor(() => expect(document.getElementById('credential-0-username')).toBeTruthy());
	}
	const type = (id: string, value: string) => fireEvent.input(document.getElementById(id)!, { target: { value } });

	it('asks for user names and passwords and sends them for hashing, never as htpasswd lines', async () => {
		handlers = { ...base([]), 'PUT /api/v1/middlewares/auth?dry_run=true': () => json({ preview: preview(), restart_required: false }) };
		render(MiddlewaresPage, { navigate: () => {}, mode: 'create' });
		await openBasicAuth();
		expect((document.getElementById('credential-0-password') as HTMLInputElement).type).toBe('password');
		await type('credential-0-username', 'admin');
		await type('credential-0-password', 's3cret');
		await fireEvent.click(screen.getByRole('button', { name: 'Add user' }));
		await type('credential-1-username', 'ops');
		await type('credential-1-password', 'other');
		await fireEvent.click(screen.getByRole('button', { name: 'Review change' }));
		await waitFor(() => expect(calls.some((c) => c.method === 'PUT')).toBe(true));
		const spec = calls.find((c) => c.method === 'PUT')!.body.spec;
		expect(spec.users_credentials).toEqual([{ username: 'admin', password: 's3cret' }, { username: 'ops', password: 'other' }]);
		expect(spec.users_htpasswd).toBeUndefined();
		expect(spec.users).toBeUndefined();
	});

	it('validates the rows locally and counts the fields needing attention', async () => {
		handlers = base([]);
		render(MiddlewaresPage, { navigate: () => {}, mode: 'create' });
		await openBasicAuth();
		await type('credential-0-username', 'bad name');
		await type('credential-0-password', 'pw');
		await fireEvent.click(screen.getByRole('button', { name: 'Add user' }));
		await type('credential-1-username', 'ops');
		await type('credential-1-password', 'x'.repeat(73));
		await fireEvent.click(screen.getByRole('button', { name: 'Review change' }));
		await waitFor(() => expect(document.getElementById('credential-0-username')!.getAttribute('aria-invalid')).toBe('true'));
		expect(document.getElementById('credential-1-password')!.getAttribute('aria-invalid')).toBe('true');
		expect(screen.getByText('2 fields need your attention.')).toBeTruthy();
		expect(document.body.textContent).not.toContain('{count}');
		expect(calls.some((c) => c.method === 'PUT')).toBe(false);
	});

	it('requires at least one user', async () => {
		handlers = base([]);
		render(MiddlewaresPage, { navigate: () => {}, mode: 'create' });
		await openBasicAuth();
		await fireEvent.click(screen.getByRole('button', { name: 'Review change' }));
		expect(await screen.findByText('Add at least one user or a users file.')).toBeTruthy();
		expect(screen.getByText('1 field needs your attention.')).toBeTruthy();
	});

	it('never copies typed passwords into the YAML editor', async () => {
		handlers = base([]);
		render(MiddlewaresPage, { navigate: () => {}, mode: 'create' });
		await openBasicAuth();
		await type('credential-0-username', 'admin');
		await type('credential-0-password', 'do-not-show-me');
		await fireEvent.click(document.getElementById('middleware-mode')!);
		await fireEvent.click(await screen.findByRole('option', { name: /YAML/i }));
		await waitFor(() => expect(document.querySelector('.cm-content, textarea')).toBeTruthy());
		await waitFor(() => expect(document.querySelector('.cm-content')?.textContent).toContain('basicAuth'));
		expect(document.body.textContent).not.toContain('do-not-show-me');
	});
});

describe('chain flow', () => {
	it('builds an ordered chain, previews it and sends the members in order', async () => {
		handlers = {
			...base([middleware('a'), middleware('b')]),
			'PUT /api/v1/chains/web?dry_run=true': () => json({ preview: preview(), restart_required: false })
		};
		render(MiddlewaresPage, { navigate: () => {}, tab: 'chains', mode: 'create' });
		await waitFor(() => expect(document.getElementById('chain-name')).toBeTruthy());
		await fireEvent.input(document.getElementById('chain-name')!, { target: { value: 'web' } });
		for (const name of ['a', 'b']) {
			await fireEvent.click(document.getElementById('chain-member-add')!);
			await fireEvent.click(await screen.findByRole('option', { name: new RegExp('^' + name) }));
		}
		// A member already in the chain is not offered again.
		await fireEvent.click(document.getElementById('chain-member-add')!);
		expect(screen.queryByRole('option', { name: /^a/ })).toBeNull();
		expect(document.querySelector('[role="listbox"]')?.closest('.searchable, [class*="search"]') ?? document.querySelector('input[type="search"], input[placeholder*="Search"]')).toBeTruthy();
		await fireEvent.keyDown(document.activeElement ?? document.body, { key: 'Escape' });
		await fireEvent.click(screen.getByRole('button', { name: /Move b up/ }));
		await fireEvent.click(screen.getByRole('button', { name: 'Review change' }));
		await waitFor(() => expect(calls.some((c) => c.method === 'PUT')).toBe(true));
		const put = calls.find((c) => c.method === 'PUT')!;
		expect(put.headers['if-none-match']).toBe('*');
		expect(put.body).toEqual({ middlewares: ['b', 'a'] });
	});
	it('views a chain as YAML, like a middleware, with members in request order', async () => {
		handlers = base([middleware('a'), middleware('b')], [chain('web', ['b', 'a@file'])]);
		render(MiddlewaresPage, { navigate: () => {}, tab: 'chains', mode: 'view', name: 'web' });
		await waitFor(() => expect(document.querySelector('.resource-dialog pre')).toBeTruthy());
		expect(document.querySelector('.resource-dialog pre')!.textContent).toBe('chain:\n  middlewares:\n    - b\n    - a@file\n');
		expect(document.querySelector('.resource-dialog ol.members')).toBeNull();
	});
	it('rejects a chain that references itself without calling the server', async () => {
		handlers = base([middleware('a')], [chain('web', ['a'])]);
		render(MiddlewaresPage, { navigate: () => {}, tab: 'chains', mode: 'edit', name: 'web' });
		await waitFor(() => expect(screen.getAllByText('a').length).toBeGreaterThan(0));
		await fireEvent.click(document.getElementById('chain-member-add')!);
		expect(screen.queryByRole('option', { name: /^web/ })).toBeNull(); // never offered
		await fireEvent.keyDown(document.activeElement ?? document.body, { key: 'Escape' });
		expect(calls.some((c) => c.method === 'PUT')).toBe(false);
	});
	it('edits a chain with its version: remove and reorder members', async () => {
		handlers = {
			...base([middleware('a'), middleware('b'), middleware('c')], [chain('web', ['a', 'b', 'c'], 4)]),
			'PUT /api/v1/chains/web?dry_run=true': () => json({ preview: preview(), restart_required: false })
		};
		render(MiddlewaresPage, { navigate: () => {}, tab: 'chains', mode: 'edit', name: 'web' });
		await waitFor(() => expect(screen.getByRole('button', { name: /Remove b/ })).toBeTruthy());
		await fireEvent.click(screen.getByRole('button', { name: /Remove b/ }));
		await fireEvent.click(screen.getByRole('button', { name: /Move c up/ }));
		await fireEvent.click(screen.getByRole('button', { name: 'Review change' }));
		await waitFor(() => expect(calls.some((c) => c.method === 'PUT')).toBe(true));
		const put = calls.find((c) => c.method === 'PUT')!;
		expect(put.headers['if-match']).toBe('"4"');
		expect(put.body).toEqual({ middlewares: ['c', 'a'] });
	});
	it('deletes an unused chain with its version, and blocks a used one', async () => {
		handlers = {
			...base([], [chain('free', ['a'], 3), chain('busy', ['a'], 1, usedBy(['shop']))]),
			'DELETE /api/v1/chains/free': () => json({ operation: operation(), restart_required: false })
		};
		render(MiddlewaresPage, { navigate: () => {}, tab: 'chains' });
		await waitFor(() => expect(screen.getAllByText('free')[0]).toBeTruthy());
		const deletes = screen.getAllByRole('menuitem', { name: 'Delete' }) as HTMLButtonElement[];
		expect(deletes.map((b) => b.disabled)).toEqual([true, false]); // busy, free (sorted by name)
		await fireEvent.click(deletes[1]);
		await fireEvent.click(await screen.findByRole('button', { name: 'Confirm deletion' }));
		await waitFor(() => expect(calls.some((c) => c.method === 'DELETE')).toBe(true));
		const del = calls.find((c) => c.method === 'DELETE')!;
		expect(del.path).toBe('/api/v1/chains/free');
		expect(del.headers['if-match']).toBe('"3"');
	});
	it('rejects an empty chain without calling the server', async () => {
		handlers = base([middleware('a')]);
		render(MiddlewaresPage, { navigate: () => {}, tab: 'chains', mode: 'create' });
		await waitFor(() => expect(document.getElementById('chain-name')).toBeTruthy());
		await fireEvent.input(document.getElementById('chain-name')!, { target: { value: 'web' } });
		await fireEvent.click(screen.getByRole('button', { name: 'Review change' }));
		await waitFor(() => expect(screen.getAllByText(/at least one/i).length).toBeGreaterThan(0));
		expect(calls.some((c) => c.method === 'PUT')).toBe(false);
	});
});

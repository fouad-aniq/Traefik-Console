import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/svelte';
// The API client binds `fetch` at import time, so install a forwarding stub first.
vi.hoisted(() => {
	(globalThis as any).__mockFetch = async () => { throw new Error('no mock'); };
	globalThis.fetch = ((...args: unknown[]) => (globalThis as any).__mockFetch(...args)) as typeof fetch;
});
import ResolversPage from '../src/features/resolvers/ResolversPage.svelte';
import { flashes } from '../src/shared/ui/flash.svelte';
import { reconnect } from '../src/core/mutations';

const providers = {
	custom_provider_codes: ['cloudflare'],
	providers: [{
		provider_code: 'cloudflare', display_name: 'Cloudflare', doc_url: 'https://x', form_supported: true, file_support_all: true,
		verification_status: 'source_audited', source_audit_notes: 'Audited.',
		auth_alternatives: [{ alternative_name: 'Token', description: '', optional_fields: [],
			required_fields: [{ env_var: 'CLOUDFLARE_DNS_API_TOKEN', type: 'secret', description: 'token', is_secret: true, required: true, file_support: true }] }],
		global_options: []
	}]
};
const preview = (extra = {}) => ({ changes: [{ action: 'created', id: 'resolver:le', kind: 'resolver', name: 'le', scope: 'static' }], static_diff: '+le: {}', dynamic_diff: '', requires_restart: false, ...extra });
const operation = (extra = {}) => ({ id: 7, kind: 'resolver.create', target: 'le', status: 'succeeded', summary: 'Applied 1 change', requires_restart: false, created_at: 'now', ...extra });
const resource = (name: string, config = {}, version = 1) => ({ id: 'resolver:' + name, name, version, origin: 'managed', provider: 'static', status: 'active', read_only: false, live_health: 'unknown', config: { type: 'acme', environment: 'staging', challenge: 'dns01', provider: 'cloudflare', email: 'a@b.co', ...config } });

interface Call { method: string; path: string; headers: Record<string, string>; body?: any }
let calls: Call[] = [];
let handlers: Record<string, () => Response | Promise<Response>> = {};
const json = (body: unknown, status = 200) => new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } });

beforeEach(() => {
	// These tests cover the manual retry offered once the automatic reconnect gave up.
	reconnect.windowMs = 0;
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

const base = (list: unknown) => ({
	'GET /api/v1/resolvers': () => json(list),
	'GET /api/v1/resolvers/providers': () => json(providers),
	'GET /api/v1/entrypoints': () => json({ entry_points: ['web'] })
});

describe('ResolversPage states', () => {
	it('shows loading, then the loaded table with state badges', async () => {
		handlers = base([resource('prod'), resource('wip', { environment: 'production' })]);
		render(ResolversPage, { navigate: () => {} });
		await waitFor(() => expect(screen.getAllByText('prod')[0]).toBeTruthy());
		expect(screen.getAllByText('wip')[0]).toBeTruthy();
		expect(screen.getAllByText('Cloudflare').length).toBeGreaterThan(0);
	});
	it('shows the empty state for null and empty lists', async () => {
		handlers = base(null);
		render(ResolversPage, { navigate: () => {} });
		await waitFor(() => expect(screen.getByText('No resolvers yet')).toBeTruthy());
	});
	it('shows an error state with retry when the list fails, and recovers', async () => {
		handlers = { ...base([]), 'GET /api/v1/resolvers': () => json({ code: 'ERR_TRAEFIK_UNAVAILABLE', message: 'Traefik is down', operation_id: 'o1', retryable: true }, 502) };
		render(ResolversPage, { navigate: () => {} });
		await waitFor(() => expect(screen.getAllByText('Traefik is down').length).toBeGreaterThan(0));
		handlers['GET /api/v1/resolvers'] = () => json([resource('back')]);
		await fireEvent.click(screen.getAllByRole('button', { name: /retry|try again/i })[0]);
		await waitFor(() => expect(screen.getAllByText('back')[0]).toBeTruthy());
	});
	it('deletes with the version read with the list and warns when Traefik must be restarted by hand', async () => {
		handlers = {
			...base([resource('prod', {}, 4)]),
			'DELETE /api/v1/resolvers/prod': () => json({ operation: operation({ status: 'restart_required' }), restart_required: true, error: { code: 'ERR_MANUAL_RESTART_REQUIRED', message: 'restart it' } }, 202)
		};
		render(ResolversPage, { navigate: () => {} });
		await fireEvent.click(await screen.findByRole('button', { name: 'Delete' }));
		const buttons = await screen.findAllByRole('button', { name: 'Confirm deletion' });
		await fireEvent.click(buttons[buttons.length - 1]);
		await waitFor(() => expect(screen.getByText(/could not restart Traefik/)).toBeTruthy());
		const del = calls.find((c) => c.method === 'DELETE')!;
		expect(del.headers['if-match']).toBe('"4"');
	});
	it('surfaces a version conflict on delete without retrying', async () => {
		handlers = { ...base([resource('prod')]), 'DELETE /api/v1/resolvers/prod': () => json({ code: 'ERR_VERSION_CONFLICT', message: 'stale', operation_id: 'o', retryable: false }, 409) };
		render(ResolversPage, { navigate: () => {} });
		await fireEvent.click(await screen.findByRole('button', { name: 'Delete' }));
		const buttons = await screen.findAllByRole('button', { name: 'Confirm deletion' });
		await fireEvent.click(buttons[buttons.length - 1]);
		await waitFor(() => expect(screen.getByText(/changed since the form was opened/)).toBeTruthy());
		expect(calls.filter((c) => c.method === 'DELETE')).toHaveLength(1);
	});
});

const fillCreate = async () => {
	await waitFor(() => expect(document.getElementById('resolver-name')).toBeTruthy());
	await fireEvent.input(document.getElementById('resolver-name')!, { target: { value: 'le' } });
	await fireEvent.input(document.getElementById('resolver-email')!, { target: { value: 'a@b.co' } });
	await fireEvent.click(screen.getByRole('button', { name: 'Continue' }));
	await fireEvent.input(await waitFor(() => document.getElementById('credential-CLOUDFLARE_DNS_API_TOKEN')!), { target: { value: 'secret-token' } });
	await fireEvent.click(screen.getByRole('button', { name: 'Continue' }));
};

describe('create flow', () => {
	it('previews with a dry run, then applies once with a create precondition and one key', async () => {
		handlers = {
			...base([]),
			'PUT /api/v1/resolvers/le?dry_run=true': () => json({ preview: preview(), restart_required: false }),
			'PUT /api/v1/resolvers/le': () => json({ operation: operation(), resource: resource('le', { values: { CLOUDFLARE_DNS_API_TOKEN: '••••••••' } }), restart_required: false })
		};
		render(ResolversPage, { navigate: () => {}, mode: 'create' });
		await fillCreate();
		await fireEvent.click(await screen.findByRole('button', { name: 'Review change' }));
		await screen.findByRole('button', { name: 'Apply changes' });

		const dry = calls.find((c) => c.method === 'PUT')!;
		expect(dry.path).toBe('/api/v1/resolvers/le?dry_run=true');
		expect(dry.headers['if-none-match']).toBe('*');
		expect(dry.headers['if-match']).toBeUndefined();
		expect(dry.body).toEqual({ type: 'acme', environment: 'staging', email: 'a@b.co', challenge: 'dns01', provider: 'cloudflare', alternative: 0, values: { CLOUDFLARE_DNS_API_TOKEN: 'secret-token' } });

		await fireEvent.click(screen.getByRole('button', { name: 'Apply changes' }));
		await waitFor(() => expect(flashes.some((f) => f.title.startsWith('Applied'))).toBe(true));
		const puts = calls.filter((c) => c.method === 'PUT');
		expect(puts).toHaveLength(2);
		expect(puts[1].path).toBe('/api/v1/resolvers/le');
		expect(puts[1].headers['if-none-match']).toBe('*');
		expect(puts[1].headers['idempotency-key']).toMatch(/^[A-Za-z0-9_-]{16,128}$/);
		expect(calls.some((c) => c.path.includes('/draft'))).toBe(false);
	});
	it('edits with If-Match set to the resource version', async () => {
		handlers = {
			...base([resource('le', {}, 6)]),
			'GET /api/v1/resolvers/le': () => json(resource('le', {}, 6)),
			'PUT /api/v1/resolvers/le?dry_run=true': () => json({ preview: preview(), restart_required: false })
		};
		render(ResolversPage, { navigate: () => {}, mode: 'edit', name: 'le' });
		await waitFor(() => expect(document.getElementById('resolver-email')).toBeTruthy());
		await fireEvent.click(screen.getByRole('button', { name: 'Continue' }));
		await fireEvent.input(await waitFor(() => document.getElementById('credential-CLOUDFLARE_DNS_API_TOKEN')!), { target: { value: 'tok' } });
		await fireEvent.click(screen.getByRole('button', { name: 'Continue' }));
		await fireEvent.click(await screen.findByRole('button', { name: 'Review change' }));
		await waitFor(() => expect(calls.some((c) => c.method === 'PUT')).toBe(true));
		expect(calls.find((c) => c.method === 'PUT')!.headers['if-match']).toBe('"6"');
	});
	it('switches an existing resolver from staging to production with a warning, keeping its name', async () => {
		handlers = {
			...base([resource('le', {}, 6)]),
			'GET /api/v1/resolvers/le': () => json(resource('le', {}, 6))
		};
		render(ResolversPage, { navigate: () => {}, mode: 'edit', name: 'le' });
		await waitFor(() => expect(document.getElementById('resolver-environment')).toBeTruthy());
		expect(screen.queryByText('Certificate authority changes')).toBeNull();
		await fireEvent.click(document.getElementById('resolver-environment')!);
		await fireEvent.click(within(document.getElementById('resolver-environment-listbox')!).getByRole('option', { name: 'Production (Let’s Encrypt)' }));
		expect(await screen.findByText('Certificate authority changes')).toBeTruthy();
		expect(screen.getByText(/weekly limits on new certificates/)).toBeTruthy();
	});
	it('keeps the form and offers to reload on a version conflict', async () => {
		handlers = { ...base([]), 'PUT /api/v1/resolvers/le?dry_run=true': () => json({ code: 'ERR_VERSION_CONFLICT', message: 'stale', operation_id: 'o', retryable: false }, 409) };
		render(ResolversPage, { navigate: () => {}, mode: 'create' });
		await waitFor(() => expect(document.getElementById('resolver-name')).toBeTruthy());
		await fireEvent.input(document.getElementById('resolver-name')!, { target: { value: 'le' } });
		await fireEvent.input(document.getElementById('resolver-email')!, { target: { value: 'a@b.co' } });
		await fireEvent.change(document.getElementById('resolver-external')!, { target: { checked: true } });
		await fireEvent.click(screen.getByRole('button', { name: 'Continue' }));
		await fireEvent.click(await screen.findByRole('button', { name: 'Continue' }));
		await fireEvent.click(await screen.findByRole('button', { name: 'Review change' }));
		await waitFor(() => expect(screen.getByRole('button', { name: 'Reload latest version' })).toBeTruthy());
		expect((document.getElementById('resolver-name') as HTMLInputElement).value).toBe('le');
	});
	it('blocks Apply and shows a Compose snippet while Traefik lacks the credential mounts', async () => {
		handlers = {
			...base([]),
			'PUT /api/v1/resolvers/le?dry_run=true': () => json({ preview: preview({ env_vars_to_mount: [{ env_var_name: 'CLOUDFLARE_DNS_API_TOKEN_FILE', file_path: '/etc/traefik/secrets/t.secret', already_set: false }] }), restart_required: false })
		};
		render(ResolversPage, { navigate: () => {}, mode: 'create' });
		await fillCreate();
		await fireEvent.click(await screen.findByRole('button', { name: 'Review change' }));
		await waitFor(() => expect(screen.getByText('Traefik needs these variables first')).toBeTruthy());
		expect((screen.getByRole('button', { name: 'Apply changes' }) as HTMLButtonElement).disabled).toBe(true);
		// The fix is the one env_file line, not a list of variables and mounts.
		expect(document.body.textContent).toContain('env_file: ./config/traefik/dns-providers.env');
		expect(document.body.textContent).not.toContain('<SECRETS_DIR>');
		expect(calls.filter((c) => c.method === 'PUT')).toHaveLength(1);
	});
	it('enables Apply without a warning once Traefik has the variables', async () => {
		handlers = {
			...base([]),
			'PUT /api/v1/resolvers/le?dry_run=true': () => json({ preview: preview({ env_vars_to_mount: [{ env_var_name: 'CLOUDFLARE_DNS_API_TOKEN_FILE', file_path: '/etc/traefik/secrets/t.secret', already_set: true }] }), restart_required: false })
		};
		render(ResolversPage, { navigate: () => {}, mode: 'create' });
		await fillCreate();
		await fireEvent.click(await screen.findByRole('button', { name: 'Review change' }));
		const apply = await screen.findByRole('button', { name: 'Apply changes' }) as HTMLButtonElement;
		await waitFor(() => expect(screen.getByText('Set')).toBeTruthy());
		expect(apply.disabled).toBe(false);
		expect(screen.queryByText('Traefik needs these variables first')).toBeNull();
	});
	it('retries an uncertain apply with the same Idempotency-Key', async () => {
		let n = 0;
		handlers = {
			...base([]),
			'PUT /api/v1/resolvers/le?dry_run=true': () => json({ preview: preview(), restart_required: false }),
			'PUT /api/v1/resolvers/le': () => { if (++n === 1) throw new TypeError('network down'); return json({ operation: operation(), resource: resource('le'), restart_required: false }); }
		};
		render(ResolversPage, { navigate: () => {}, mode: 'create' });
		await fillCreate();
		await fireEvent.click(await screen.findByRole('button', { name: 'Review change' }));
		await fireEvent.click(await screen.findByRole('button', { name: 'Apply changes' }));
		await waitFor(() => expect(screen.getByText(/response was lost/)).toBeTruthy());
		await fireEvent.click(screen.getAllByRole('button', { name: 'Retry original apply' })[0]);
		await waitFor(() => expect(flashes.some((f) => f.title.startsWith('Applied'))).toBe(true));
		const applies = calls.filter((c) => c.method === 'PUT' && !c.path.includes('dry_run'));
		expect(applies).toHaveLength(2);
		expect(applies[1].headers['idempotency-key']).toBe(applies[0].headers['idempotency-key']);
	});
	it('tells the user to restart Traefik by hand on HTTP 202', async () => {
		handlers = {
			...base([]),
			'PUT /api/v1/resolvers/le?dry_run=true': () => json({ preview: preview({ requires_restart: true }), restart_required: true }),
			'PUT /api/v1/resolvers/le': () => json({ operation: operation({ status: 'restart_required', requires_restart: true }), resource: resource('le'), restart_required: true, error: { code: 'ERR_MANUAL_RESTART_REQUIRED', message: 'restart it' } }, 202)
		};
		render(ResolversPage, { navigate: () => {}, mode: 'create' });
		await fillCreate();
		await fireEvent.click(await screen.findByRole('button', { name: 'Review change' }));
		await fireEvent.click(await screen.findByRole('button', { name: 'Apply changes' }));
		await waitFor(() => expect(flashes.some((f) => f.tone === 'warning' && /could not restart Traefik/.test(f.message ?? ''))).toBe(true));
	});
});

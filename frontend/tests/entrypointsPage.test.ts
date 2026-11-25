import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/svelte';
// The API client binds `fetch` at import time, so install a forwarding stub first.
vi.hoisted(() => {
	(globalThis as any).__mockFetch = async () => { throw new Error('no mock'); };
	globalThis.fetch = ((...args: unknown[]) => (globalThis as any).__mockFetch(...args)) as typeof fetch;
});
import EntryPointsPage from '../src/features/entrypoints/EntryPointsPage.svelte';
import { flashes } from '../src/shared/ui/flash.svelte';

const resource = (name: string, config = {}, extra = {}) => ({
	id: 'entrypoint:' + name, name, version: 1, origin: 'managed', provider: 'static', status: 'active', read_only: false, live_health: 'unknown',
	config: { protocol: 'tcp', port: 80, address: ':80', usage: {}, ...config }, ...extra
});
const external = (name: string, config = {}) => resource(name, { adoptable: true, ...config }, { id: 'external:entrypoint:' + name, origin: 'external', read_only: true, version: 0 });
const preview = (extra = {}) => ({ changes: [{ action: 'created', id: 'entrypoint:app', kind: 'entrypoint', name: 'app', scope: 'static' }], static_diff: '+  app:\n+    address: :8443', dynamic_diff: '', requires_restart: true, ...extra });
const operation = (extra = {}) => ({ id: 9, kind: 'upsert', target: 'entrypoint:app', status: 'succeeded', summary: 'created entrypoint app', requires_restart: true, created_at: 'now', ...extra });

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

const base = (items: unknown) => ({
	'GET /api/v1/entrypoints': () => json({ entry_points: [], protocols: {}, items }),
	'GET /api/v1/resolvers': () => json([{ id: 'resolver:le', name: 'le', version: 1, origin: 'managed', provider: 'static', status: 'active', read_only: false, live_health: 'unknown', config: { type: 'acme' } }]),
	'GET /api/v1/middlewares': () => json([]),
	'GET /api/v1/chains': () => json([])
});
const input = (id: string) => document.getElementById(id) as HTMLInputElement;
const type = (id: string, value: string) => fireEvent.input(input(id), { target: { value } });
const tab = (name: RegExp) => fireEvent.click(screen.getByRole('tab', { name }));
/** Picks a type in the Type select. */
async function chooseKind(name: RegExp) {
	await fireEvent.click(document.getElementById('entrypoint-kind')!);
	const listbox = await waitFor(() => document.getElementById('entrypoint-kind-listbox')!);
	const option = Array.from(listbox.querySelectorAll<HTMLElement>('[role="option"]')).find((o) => name.test(o.textContent ?? ''))!;
	await fireEvent.click(option);
}

describe('EntryPointsPage list', () => {
	it('shows what each entrypoint does and who owns it', async () => {
		handlers = base([
			external('web', { http: { redirect: { to: 'websecure' } }, usage: { resolvers: ['le'] } }),
			resource('websecure', { port: 443, address: ':443', as_default: true, http3: {}, http: { tls: { cert_resolver: 'le' } }, usage: { routers: ['app', 'api'], entry_points: ['web'] } }),
			external('legacy', { port: 9000, address: ':9000', adoptable: false, adopt_blocker: 'It uses settings the console does not manage: http.underscoreHeadersStrategy.' }),
			resource('dns', { protocol: 'udp', port: 53, address: ':53/udp', adopted: true })
		]);
		render(EntryPointsPage, { navigate: () => {} });
		await waitFor(() => expect(screen.getAllByText('websecure')[0]).toBeTruthy());
		expect(screen.getAllByText('Redirects to websecure').length).toBeGreaterThan(0);
		expect(screen.getAllByText('TLS · le').length).toBeGreaterThan(0);
		expect(screen.getAllByText('HTTP/3').length).toBeGreaterThan(0);
		expect(screen.getAllByText('HTTPS').length).toBeGreaterThan(0);
		// The address is shown once, without its /udp suffix, next to the protocol.
		expect(screen.getByText(':53')).toBeTruthy();
		expect(screen.queryByText(':53/udp')).toBeNull();
		expect(screen.getAllByText('3 resources').length).toBeGreaterThan(0);
		expect(screen.getAllByText('External').length).toBe(2);
		expect(screen.getAllByText('Taken over').length).toBeGreaterThan(0);
		// An adoptable declaration can be taken over; one the console cannot hold can only be viewed.
		expect(screen.getAllByText('Take over').length).toBeGreaterThan(0);
		// Deleting an entrypoint that is still used is not offered; releasing an adopted one is.
		expect((screen.getByText('Delete').closest('button') as HTMLButtonElement).disabled).toBe(true);
		expect((screen.getByText('Release').closest('button') as HTMLButtonElement).disabled).toBe(false);
	});

	it('opens the used-by dialog with the routers of every protocol, resolvers and redirects', async () => {
		const navigate = vi.fn();
		handlers = {
			...base([resource('websecure', { port: 443, address: ':443', usage: { routers: ['app', 'db'], resolvers: ['le'], entry_points: ['web'] } })]),
			'GET /api/v1/routers': () => json([
				{ name: 'app', origin: 'managed', read_only: false, config: { rule: 'Host(`app.example.com`)', entry_points: ['websecure'], service: 'app' } },
				{ name: 'db', origin: 'managed', read_only: false, config: { protocol: 'tcp', rule: 'HostSNI(`*`)', entry_points: ['websecure'], service: 'db' } }
			])
		};
		render(EntryPointsPage, { navigate });
		await waitFor(() => expect(screen.getByText('4 resources')).toBeTruthy());
		await fireEvent.click(screen.getByText('4 resources'));
		await waitFor(() => expect(screen.getByText('Host(`app.example.com`)')).toBeTruthy());
		expect(screen.getByText('Redirects here')).toBeTruthy();
		await fireEvent.click(screen.getByText('db'));
		expect(navigate).toHaveBeenCalledWith('/tcp-routers/db');
	});

	it('shows the empty state, and an error with retry', async () => {
		handlers = base(null);
		const view = render(EntryPointsPage, { navigate: () => {} });
		await waitFor(() => expect(screen.getByText('No entrypoints yet')).toBeTruthy());
		view.unmount();
		handlers = { ...base([]), 'GET /api/v1/entrypoints': () => json({ code: 'ERR_INTERNAL', message: 'traefik.yml is unreadable', operation_id: 'o1', retryable: true }, 500) };
		render(EntryPointsPage, { navigate: () => {} });
		await waitFor(() => expect(screen.getAllByText('traefik.yml is unreadable').length).toBeGreaterThan(0));
	});

	it('deletes with the listed version and shows why a delete was refused', async () => {
		handlers = {
			...base([resource('metrics', { port: 9100, address: ':9100' }, { version: 4 }), resource('web')]),
			'DELETE /api/v1/entrypoints/metrics': () => json({ code: 'ERR_ENTRYPOINT_IN_USE', message: "entrypoint cannot be deleted because it is still referenced: used by router 'app'", operation_id: 'o2', retryable: false }, 409)
		};
		render(EntryPointsPage, { navigate: () => {} });
		await waitFor(() => expect(screen.getAllByText('metrics')[0]).toBeTruthy());
		await fireEvent.click(screen.getAllByText('Delete')[0]);
		await fireEvent.click(screen.getByRole('button', { name: 'Delete entrypoint' }));
		await waitFor(() => expect(screen.getAllByText(/used by router 'app'/).length).toBeGreaterThan(0));
		const del = calls.find((c) => c.method === 'DELETE')!;
		expect(del.headers['if-match']).toBe('"4"');
	});
});

describe('EntryPointDialog', () => {
	it('creates a secure entrypoint from the preset: review first, then apply with the same body', async () => {
		const navigate = vi.fn();
		handlers = {
			...base([external('web')]),
			'PUT /api/v1/entrypoints/websecure?dry_run=true': () => json({ preview: preview(), restart_required: false, warnings: [] }),
			'PUT /api/v1/entrypoints/websecure': () => json({ operation: operation(), resource: resource('websecure'), restart_required: false })
		};
		render(EntryPointsPage, { navigate, mode: 'create' });
		await waitFor(() => expect(input('entrypoint-name')).toBeTruthy());
		// The dialog opens on the web type; "web" is already taken here, so no name is suggested.
		expect(input('entrypoint-name').value).toBe('');
		await chooseKind(/Secure web/);
		expect(input('entrypoint-name').value).toBe('websecure');
		expect(input('entrypoint-port').value).toBe('443');

		await tab(/Review/);
		await waitFor(() => expect(screen.getByText('Traefik will restart')).toBeTruthy());
		expect(screen.getByText('Publish the port')).toBeTruthy();
		const dry = calls.find((c) => c.path.endsWith('?dry_run=true'))!;
		expect(dry.headers['if-none-match']).toBe('*');
		expect(dry.body).toEqual({ kind: 'https', protocol: 'tcp', port: 443, as_default: true, http: { tls: {} } });

		await fireEvent.click(screen.getByRole('button', { name: 'Apply and restart Traefik' }));
		await waitFor(() => expect(navigate).toHaveBeenCalledWith('/entrypoints'));
		const put = calls.find((c) => c.method === 'PUT' && !c.path.includes('dry_run'))!;
		expect(put.body).toEqual(dry.body);
		expect(put.headers['idempotency-key']).toBeTruthy();
		expect(flashes.length).toBe(1);
	});

	it('stops on the tab with the problem and never calls the server with invalid input', async () => {
		handlers = base([external('web')]);
		render(EntryPointsPage, { navigate: () => {}, mode: 'create' });
		await waitFor(() => expect(input('entrypoint-name')).toBeTruthy());
		await type('entrypoint-name', 'app');
		await type('entrypoint-port', '80'); // taken by web
		await tab(/Review/);
		await waitFor(() => expect(screen.getAllByText(/already used by entrypoint “web”/).length).toBeGreaterThan(0));
		expect(calls.some((c) => c.method === 'PUT')).toBe(false);
	});

	it('shows only the listener for UDP and sends nothing else', async () => {
		handlers = { ...base([]), 'PUT /api/v1/entrypoints/dns?dry_run=true': () => json({ preview: preview(), restart_required: false }) };
		render(EntryPointsPage, { navigate: () => {}, mode: 'create' });
		await waitFor(() => expect(input('entrypoint-name')).toBeTruthy());
		expect(screen.getByRole('tab', { name: /HTTP/ })).toBeTruthy();
		await chooseKind(/UDP service/);
		expect(screen.queryByRole('tab', { name: /HTTP/ })).toBeNull();
		expect(screen.queryByRole('tab', { name: /Client addresses/ })).toBeNull();
		await type('entrypoint-name', 'dns');
		await type('entrypoint-port', '53');
		await tab(/Review/);
		await waitFor(() => expect(calls.some((c) => c.method === 'PUT')).toBe(true));
		expect(calls.find((c) => c.method === 'PUT')!.body).toEqual({ kind: 'udp', protocol: 'udp', port: 53 });
	});

	it('gives a TCP service no HTTP tab and sends no HTTP settings', async () => {
		handlers = { ...base([]), 'PUT /api/v1/entrypoints/db?dry_run=true': () => json({ preview: preview(), restart_required: false }) };
		render(EntryPointsPage, { navigate: () => {}, mode: 'create' });
		await waitFor(() => expect(input('entrypoint-name')).toBeTruthy());
		await chooseKind(/TCP service/);
		expect(screen.queryByRole('tab', { name: /HTTP/ })).toBeNull();
		expect(screen.getByRole('tab', { name: /Client addresses/ })).toBeTruthy();
		await type('entrypoint-name', 'db');
		await type('entrypoint-port', '5432');
		await tab(/Review/);
		await waitFor(() => expect(calls.some((c) => c.method === 'PUT')).toBe(true));
		expect(calls.find((c) => c.method === 'PUT')!.body).toEqual({ kind: 'tcp', protocol: 'tcp', port: 5432 });
	});

	it('takes over a declared entrypoint explicitly and puts server field errors on the field', async () => {
		handlers = {
			...base([external('web', { http: { redirect: { to: 'websecure' } } }), resource('websecure', { port: 443, address: ':443', http: { tls: {} } })]),
			'PUT /api/v1/entrypoints/web?dry_run=true': () => json({ code: 'ERR_VALIDATION_FAILED', message: 'Configuration failed validation', field_errors: { 'entrypoint:web.http.redirect.to': "entrypoint 'websecure' does not exist" }, operation_id: 'o3', retryable: false }, 422)
		};
		render(EntryPointsPage, { navigate: () => {}, mode: 'edit', name: 'web' });
		await waitFor(() => expect(screen.getByText('You are taking over this entrypoint')).toBeTruthy());
		expect(input('entrypoint-name').disabled).toBe(true);
		await tab(/Review/);
		await waitFor(() => expect(screen.getAllByText("entrypoint 'websecure' does not exist").length).toBeGreaterThan(0));
		const dry = calls.find((c) => c.method === 'PUT')!;
		expect(dry.body.adopted).toBe(true);
		expect(dry.headers['if-none-match']).toBe('*');
	});

	it('keeps an entrypoint the console cannot hold read-only and says why', async () => {
		handlers = base([external('legacy', { adoptable: false, adopt_blocker: 'It uses settings the console does not manage: http.underscoreHeadersStrategy.', rendered_node: { address: ':9000' } })]);
		render(EntryPointsPage, { navigate: () => {}, mode: 'edit', name: 'legacy' });
		await waitFor(() => expect(screen.getByText(/http.underscoreHeadersStrategy/)).toBeTruthy());
		expect(screen.queryByRole('tab', { name: /Listener/ })).toBeNull();
		expect(screen.queryByRole('button', { name: 'Take over' })).toBeNull();
	});
});

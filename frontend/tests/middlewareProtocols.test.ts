import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/svelte';
// The API client binds `fetch` at import time, so install a forwarding stub first.
vi.hoisted(() => {
	(globalThis as any).__mockFetch = async () => { throw new Error('no mock'); };
	globalThis.fetch = ((...args: unknown[]) => (globalThis as any).__mockFetch(...args)) as typeof fetch;
});
import MiddlewaresPage from '../src/features/middlewares/MiddlewaresPage.svelte';
import { MIDDLEWARE_BASE, TCP_TYPES, externalType, middlewareProtocolOf, nativeSchema } from '../src/features/middlewares/models';
import { validateForm, flattenSchemaFields } from '../src/features/middlewares/validation';
import { flashes } from '../src/shared/ui/flash.svelte';

const preview = () => ({ changes: [{ action: 'created', id: 'middleware:x', kind: 'middleware', name: 'x', scope: 'dynamic' }], static_diff: '', dynamic_diff: '+m: {}', requires_restart: false });
const operation = () => ({ id: 9, kind: 'middleware.create', target: 'x', status: 'succeeded', summary: 'Applied 1 change', requires_restart: false, created_at: 'now' });
const envelope = (kind: string, name: string, config: unknown, version = 1, extra = {}) => ({ id: kind + ':' + name, name, version, origin: 'managed', provider: 'file', status: 'active', read_only: false, live_health: 'unknown', updated_at: '2026-01-01T00:00:00Z', config, ...extra });
const none = { routers: [], chains: [] };
const httpMw = (name: string, version = 1) => envelope('middleware', name, { protocol: 'http', type: 'addPrefix', spec: { prefix: '/api' }, used_by: none }, version);
const tcpMw = (name: string, type: string, spec: unknown, version = 1, used_by: unknown = none, extra = {}) => envelope('middleware', name, { protocol: 'tcp', type, spec, used_by }, version, extra);
const cap = (version = 1, used_by: unknown = none) => tcpMw('cap', 'inFlightConn', { amount: 10 }, version, used_by);
const office = (version = 1) => tcpMw('office', 'ipAllowList', { sourceRange: ['10.0.0.0/8', '192.168.1.7'] }, version);

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
const dry = (name: string) => ({ [`PUT /api/v1/middlewares/${name}?dry_run=true`]: () => json({ preview: preview(), restart_required: false }) });
const puts = () => calls.filter((c) => c.method === 'PUT');
const rowOf = (name: string) => screen.getAllByText(name)[0].closest('tr') as HTMLElement;
const button = (name: string | RegExp) => screen.getByRole('button', { name });
async function openCreateTcp() {
	render(MiddlewaresPage, { navigate: () => {}, protocol: 'tcp', mode: 'create' });
	await waitFor(() => expect(document.getElementById('middleware-name')).toBeTruthy());
}
async function chooseType(label: RegExp) {
	await fireEvent.click(document.getElementById('middleware-type')!);
	await fireEvent.click(await screen.findByRole('option', { name: label }));
}

describe('model', () => {
	it('knows the protocol of a middleware, HTTP when it is not said', () => {
		expect(middlewareProtocolOf({})).toBe('http');
		expect(middlewareProtocolOf({ protocol: 'tcp' })).toBe('tcp');
		expect(MIDDLEWARE_BASE).toEqual({ http: '/middlewares', tcp: '/tcp-middlewares' });
		expect(TCP_TYPES).toEqual(['inFlightConn', 'ipAllowList']);
	});
	it('has the TCP fields Traefik documents: a connection limit, and source ranges only', () => {
		expect(Object.keys(nativeSchema.tcp.inFlightConn)).toEqual(['amount']);
		expect(Object.keys(nativeSchema.tcp.ipAllowList)).toEqual(['sourceRange']);
	});
	it('reads the type of a TCP middleware Traefik reports', () => {
		expect(externalType({ inFlightConn: { amount: 3 }, status: 'enabled' })).toBe('inFlightConn');
	});
	it('needs a connection limit of 1 or more', () => {
		const fields = flattenSchemaFields(nativeSchema.tcp.inFlightConn);
		const copy = (k: string) => k;
		expect(validateForm('tcp', 'inFlightConn', fields, {}, {}, { users: [], mode: 'none' }, [], 'cap', copy)).toEqual({ amount: 'requiredField' });
		expect(validateForm('tcp', 'inFlightConn', fields, {}, { amount: 0 }, { users: [], mode: 'none' }, [], 'cap', copy)).toEqual({ amount: 'positiveNumber' });
		expect(validateForm('tcp', 'inFlightConn', fields, {}, { amount: 5 }, { users: [], mode: 'none' }, [], 'cap', copy)).toEqual({});
	});
});

describe('one list for both protocols', () => {
	it('shows HTTP and TCP middlewares from one request, with a Protocol column', async () => {
		handlers = base([httpMw('strip'), cap(), office()]);
		render(MiddlewaresPage, { navigate: () => {} });
		await waitFor(() => expect(screen.getAllByText('cap').length).toBeGreaterThan(0));
		expect(within(rowOf('strip')).getByText('HTTP')).toBeTruthy();
		expect(within(rowOf('cap')).getByText('TCP')).toBeTruthy();
		expect(within(rowOf('cap')).getByText('In-flight connections')).toBeTruthy();
		expect(calls.map((c) => c.path)).toEqual(['/api/v1/middlewares']);
	});
	it('filters by protocol, with a count per protocol', async () => {
		handlers = base([httpMw('strip'), cap(), office()]);
		render(MiddlewaresPage, { navigate: () => {} });
		await waitFor(() => expect(screen.getAllByText('cap').length).toBeGreaterThan(0));
		await fireEvent.click(screen.getByRole('button', { name: /Filters/ }));
		await fireEvent.click(screen.getByRole('menuitem', { name: 'Protocol' }));
		await fireEvent.click(await screen.findByRole('menuitemradio', { name: /TCP \(2\)/ }));
		await waitFor(() => expect(screen.queryByText('strip')).toBeNull());
		expect(screen.getAllByText('office').length).toBeGreaterThan(0);
	});
	it('has one create button: the protocol is chosen in the dialog', async () => {
		handlers = base([httpMw('strip')]);
		const paths: string[] = [];
		render(MiddlewaresPage, { navigate: (p: string) => paths.push(p) });
		await waitFor(() => expect(screen.getAllByText('strip').length).toBeGreaterThan(0));
		await fireEvent.click(screen.getByRole('button', { name: 'Create middleware' }));
		expect(screen.queryByRole('menuitem', { name: /TCP middleware/ })).toBeNull();
		expect(paths).toEqual(['/middlewares/new']);
	});
	it('opens each middleware under the base of its protocol', async () => {
		handlers = base([httpMw('strip'), cap()]);
		const paths: string[] = [];
		render(MiddlewaresPage, { navigate: (p: string) => paths.push(p) });
		await waitFor(() => expect(screen.getAllByText('cap').length).toBeGreaterThan(0));
		await fireEvent.click(within(rowOf('cap')).getByRole('button', { name: 'View' }));
		await fireEvent.click(within(rowOf('cap')).getByRole('menuitem', { name: 'Edit' }));
		await fireEvent.click(within(rowOf('strip')).getByRole('button', { name: 'View' }));
		expect(paths).toEqual(['/tcp-middlewares/cap', '/tcp-middlewares/cap/edit', '/middlewares/strip']);
	});
	it('lists the TCP routers that use a TCP middleware and opens them on the TCP router page', async () => {
		const used = { routers: [{ name: 'pg', rule: 'HostSNI(`*`)', entry_points: ['postgres'], service: 'db', tls: false, read_only: false }], chains: [] };
		handlers = base([cap(1, used)]);
		const paths: string[] = [];
		render(MiddlewaresPage, { navigate: (p: string) => paths.push(p) });
		await waitFor(() => expect(screen.getAllByText('cap').length).toBeGreaterThan(0));
		expect((within(rowOf('cap')).getByRole('menuitem', { name: 'Delete' }) as HTMLButtonElement).disabled).toBe(true);
		await fireEvent.click(within(rowOf('cap')).getByRole('button', { name: '1 resource' }));
		const dialog = await screen.findByRole('dialog');
		await fireEvent.click(within(dialog).getByRole('button', { name: 'pg' }));
		expect(paths).toContain('/tcp-routers/pg');
	});
	it('deletes a TCP middleware with its version, and shows a refusal', async () => {
		handlers = { ...base([cap(4)]), 'DELETE /api/v1/middlewares/cap': () => json({ code: 'ERR_CONFLICT', message: 'used by router pg', operation_id: 'o', retryable: false }, 409) };
		render(MiddlewaresPage, { navigate: () => {} });
		await waitFor(() => expect(screen.getAllByText('cap').length).toBeGreaterThan(0));
		await fireEvent.click(within(rowOf('cap')).getByRole('menuitem', { name: 'Delete' }));
		await fireEvent.click(await screen.findByRole('button', { name: 'Confirm deletion' }));
		await waitFor(() => expect(screen.getByText(/used by router pg/)).toBeTruthy());
		expect(calls.find((c) => c.method === 'DELETE')!.headers['if-match']).toBe('"4"');
	});
});

describe('choosing the protocol in the create dialog', () => {
	const pickProtocol = async (label: RegExp) => {
		await fireEvent.click(document.getElementById('middleware-protocol')!);
		await fireEvent.click(await screen.findByRole('option', { name: label }));
	};
	const typeOptions = async () => {
		await fireEvent.click(document.getElementById('middleware-type')!);
		const listbox = await waitFor(() => document.getElementById('middleware-type-listbox')!);
		const options = within(listbox).getAllByRole('option').map((o) => o.textContent ?? '');
		await fireEvent.keyDown(document.activeElement ?? document.body, { key: 'Escape' });
		return options;
	};
	it('starts on HTTP and offers the TCP types once TCP is chosen', async () => {
		handlers = base([]);
		render(MiddlewaresPage, { navigate: () => {}, mode: 'create' });
		await waitFor(() => expect(document.getElementById('middleware-protocol')).toBeTruthy());
		expect(document.getElementById('middleware-protocol')!.textContent).toContain('HTTP middleware');
		expect((await typeOptions()).length).toBeGreaterThan(10);
		await pickProtocol(/^TCP middleware/);
		expect(screen.getByText(/Only TCP routers can use it/)).toBeTruthy();
		const tcpTypes = await typeOptions();
		expect(tcpTypes).toHaveLength(2);
		expect(tcpTypes.join(' ')).not.toContain('Plugin');
	});
	it('sends a TCP middleware chosen in the dialog, with the same steps as HTTP', async () => {
		handlers = { ...base([]), ...dry('cap') };
		render(MiddlewaresPage, { navigate: () => {}, mode: 'create' });
		await waitFor(() => expect(document.getElementById('middleware-protocol')).toBeTruthy());
		await fireEvent.input(document.getElementById('middleware-name')!, { target: { value: 'cap' } });
		await pickProtocol(/^TCP middleware/);
		await chooseType(/In-flight connections/);
		await fireEvent.click(button('Continue'));
		await fireEvent.input(await waitFor(() => document.getElementById('mw-amount')!), { target: { value: '4' } });
		await fireEvent.click(button('Review change'));
		await waitFor(() => expect(puts()).toHaveLength(1));
		expect(puts()[0].body).toEqual({ protocol: 'tcp', type: 'inFlightConn', spec: { amount: 4 } });
	});
	it('starts the type over when the protocol changes, keeping the name', async () => {
		handlers = { ...base([]), ...dry('api') };
		render(MiddlewaresPage, { navigate: () => {}, mode: 'create' });
		await waitFor(() => expect(document.getElementById('middleware-protocol')).toBeTruthy());
		await fireEvent.input(document.getElementById('middleware-name')!, { target: { value: 'api' } });
		await pickProtocol(/^TCP middleware/);
		await chooseType(/In-flight connections/);
		await pickProtocol(/^HTTP middleware/);
		expect((document.getElementById('middleware-name') as HTMLInputElement).value).toBe('api');
		await fireEvent.click(button('Continue'));
		await waitFor(() => expect(screen.getAllByText(/type/i).length).toBeGreaterThan(0));
		expect(document.getElementById('panel-middleware-configuration')!.hidden).toBe(true);
		await chooseType(/Add prefix/);
		await fireEvent.click(button('Continue'));
		await fireEvent.input(await waitFor(() => document.getElementById('mw-prefix')!), { target: { value: '/api' } });
		await fireEvent.click(button('Review change'));
		await waitFor(() => expect(puts()).toHaveLength(1));
		expect(puts()[0].body).toEqual({ type: 'addPrefix', spec: { prefix: '/api' } });
	});
	it('does not offer the protocol when editing', async () => {
		handlers = base([cap()]);
		render(MiddlewaresPage, { navigate: () => {}, protocol: 'tcp', mode: 'edit', name: 'cap' });
		await waitFor(() => expect(screen.getByRole('dialog').textContent).toContain('Edit TCP middleware'));
		expect(document.getElementById('middleware-protocol')).toBeNull();
	});
});

describe('creating a TCP middleware', () => {
	it('offers only the TCP types, and no plugin', async () => {
		handlers = base([]);
		await openCreateTcp();
		expect(screen.getByRole('dialog').textContent).toContain('Create middleware');
		expect(document.getElementById('middleware-protocol')!.textContent).toContain('TCP middleware');
		expect(screen.getByText(/Only TCP routers can use it/)).toBeTruthy();
		await fireEvent.click(document.getElementById('middleware-type')!);
		const listbox = await waitFor(() => document.getElementById('middleware-type-listbox')!);
		const options = within(listbox).getAllByRole('option').map((o) => o.textContent ?? '');
		expect(options).toHaveLength(2);
		expect(options[0]).toContain('In-flight connections');
		expect(options[1]).toContain('IP allow list');
		expect(calls.some((c) => c.path === '/api/v1/services')).toBe(false);
	});
	it('limits connections: previews and applies with the TCP protocol, a create precondition and one key', async () => {
		handlers = {
			...base([]), ...dry('cap'),
			'PUT /api/v1/middlewares/cap': () => json({ operation: operation(), resource: cap(), restart_required: false })
		};
		await openCreateTcp();
		await fireEvent.input(document.getElementById('middleware-name')!, { target: { value: 'cap' } });
		await chooseType(/In-flight connections/);
		await fireEvent.click(button('Continue'));
		await fireEvent.input(await waitFor(() => document.getElementById('mw-amount')!), { target: { value: '10' } });
		await fireEvent.click(button('Review change'));
		await screen.findByRole('button', { name: 'Apply changes' });
		expect(puts()[0].path).toBe('/api/v1/middlewares/cap?dry_run=true');
		expect(puts()[0].headers['if-none-match']).toBe('*');
		expect(puts()[0].body).toEqual({ protocol: 'tcp', type: 'inFlightConn', spec: { amount: 10 } });
		expect(screen.getByRole('dialog').textContent).toContain('TCP middleware');
		await fireEvent.click(button('Apply changes'));
		await waitFor(() => expect(flashes.some((f) => f.title.startsWith('Applied'))).toBe(true));
		expect(puts()[1].path).toBe('/api/v1/middlewares/cap');
		expect(puts()[1].headers['idempotency-key']).toMatch(/^[A-Za-z0-9_-]{16,128}$/);
	});
	it('does not stage without a connection limit, or with zero', async () => {
		handlers = { ...base([]), ...dry('cap') };
		await openCreateTcp();
		await fireEvent.input(document.getElementById('middleware-name')!, { target: { value: 'cap' } });
		await chooseType(/In-flight connections/);
		await fireEvent.click(button('Continue'));
		await fireEvent.click(button('Review change'));
		await waitFor(() => expect(screen.getAllByText('This field is required.').length).toBeGreaterThan(0));
		await fireEvent.input(document.getElementById('mw-amount')!, { target: { value: '0' } });
		await fireEvent.click(button('Review change'));
		await waitFor(() => expect(screen.getAllByText(/1 or more|between/).length).toBeGreaterThan(0));
		expect(puts()).toHaveLength(0);
	});
	it('needs a name and a type before it continues', async () => {
		handlers = base([httpMw('strip')]);
		await openCreateTcp();
		await fireEvent.click(button('Continue'));
		await waitFor(() => expect(document.getElementById('middleware-name')!.getAttribute('aria-invalid')).toBe('true'));
		await fireEvent.input(document.getElementById('middleware-name')!, { target: { value: 'strip' } });
		await fireEvent.click(button('Continue'));
		await waitFor(() => expect(screen.getAllByText(/already exists/).length).toBeGreaterThan(0));
		expect(puts()).toHaveLength(0);
	});
	it('shows the server refusal and applies nothing', async () => {
		handlers = { ...base([]), 'PUT /api/v1/middlewares/cap?dry_run=true': () => json({ code: 'ERR_VALIDATION', message: "unsupported or empty middleware type: amount must be a positive whole number", operation_id: 'op', retryable: false }, 422) };
		await openCreateTcp();
		await fireEvent.input(document.getElementById('middleware-name')!, { target: { value: 'cap' } });
		await chooseType(/In-flight connections/);
		await fireEvent.click(button('Continue'));
		await fireEvent.input(await waitFor(() => document.getElementById('mw-amount')!), { target: { value: '3' } });
		await fireEvent.click(button('Review change'));
		await waitFor(() => expect(screen.getAllByText(/positive whole number/).length).toBeGreaterThan(0));
		expect(screen.queryByRole('button', { name: 'Apply changes' })).toBeNull();
	});
});

describe('editing and viewing a TCP middleware', () => {
	it('edits an allow list with its version and keeps it a TCP middleware', async () => {
		handlers = { ...base([office(5)]), ...dry('office') };
		render(MiddlewaresPage, { navigate: () => {}, protocol: 'tcp', mode: 'edit', name: 'office' });
		await waitFor(() => expect(screen.getByRole('dialog').textContent).toContain('Edit TCP middleware'));
		await fireEvent.click(await screen.findByRole('button', { name: 'Review change' }));
		await waitFor(() => expect(puts()).toHaveLength(1));
		expect(puts()[0].headers['if-match']).toBe('"5"');
		expect(puts()[0].body).toEqual({ protocol: 'tcp', type: 'ipAllowList', spec: { sourceRange: ['10.0.0.0/8', '192.168.1.7'] } });
	});
	it('reports a version conflict without applying', async () => {
		handlers = { ...base([cap(2)]), 'PUT /api/v1/middlewares/cap?dry_run=true': () => json({ code: 'ERR_VERSION_CONFLICT', message: 'changed', operation_id: 'o', retryable: false }, 409) };
		render(MiddlewaresPage, { navigate: () => {}, protocol: 'tcp', mode: 'edit', name: 'cap' });
		await fireEvent.click(await screen.findByRole('button', { name: 'Review change' }));
		await waitFor(() => expect(screen.getByRole('button', { name: /Refresh/ })).toBeTruthy());
		expect(screen.queryByRole('button', { name: 'Apply changes' })).toBeNull();
	});
	it('shows a TCP middleware the way Traefik declares it, and edits it under its own base', async () => {
		handlers = base([cap()]);
		const paths: string[] = [];
		render(MiddlewaresPage, { navigate: (p: string) => paths.push(p), protocol: 'tcp', mode: 'view', name: 'cap' });
		await waitFor(() => expect(document.querySelector('.resource-dialog pre')).toBeTruthy());
		expect(document.querySelector('.resource-dialog pre')!.textContent).toBe('inFlightConn:\n  amount: 10\n');
		expect(screen.getByRole('dialog').textContent).toContain('TCP middleware');
		await fireEvent.click(within(screen.getByRole('dialog')).getByRole('button', { name: 'Edit' }));
		expect(paths).toEqual(['/tcp-middlewares/cap/edit']);
	});
	it('shows Traefik\'s own TCP middleware read-only', async () => {
		handlers = base([tcpMw('limit@docker', 'external', { inFlightConn: { amount: 5 } }, 0, none, { origin: 'external', provider: 'docker', read_only: true })]);
		render(MiddlewaresPage, { navigate: () => {} });
		await waitFor(() => expect(screen.getAllByText('limit@docker').length).toBeGreaterThan(0));
		expect(within(rowOf('limit@docker')).getByText('TCP')).toBeTruthy();
		expect(within(rowOf('limit@docker')).queryByRole('menuitem', { name: 'Edit' })).toBeNull();
	});
});

describe('chains stay HTTP', () => {
	it('does not offer TCP middlewares as chain members', async () => {
		handlers = base([httpMw('strip'), cap()]);
		render(MiddlewaresPage, { navigate: () => {}, tab: 'chains', mode: 'create' });
		await waitFor(() => expect(document.getElementById('chain-name')).toBeTruthy());
		await fireEvent.click(document.getElementById('chain-member-add')!);
		const options = (await screen.findAllByRole('option')).map((o) => o.textContent ?? '');
		expect(options.some((o) => o.includes('strip'))).toBe(true);
		expect(options.some((o) => o.includes('cap'))).toBe(false);
	});
});

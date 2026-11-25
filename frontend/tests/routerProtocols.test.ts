import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/svelte';
// The API client binds `fetch` at import time, so install a forwarding stub first.
vi.hoisted(() => {
	(globalThis as any).__mockFetch = async () => { throw new Error('no mock'); };
	globalThis.fetch = ((...args: unknown[]) => (globalThis as any).__mockFetch(...args)) as typeof fetch;
});
import RoutesPage from '../src/features/routes/RoutesPage.svelte';
import { ROUTER_BASE, formFrom, newForm, previewRule, routerDefinition, routerProtocolOf, toInput } from '../src/features/routes/models';
import { routeErrors } from '../src/features/routes/validation';
import { flashes } from '../src/shared/ui/flash.svelte';

const preview = () => ({ changes: [{ action: 'created', id: 'router:x', kind: 'router', name: 'x', scope: 'dynamic' }], static_diff: '', dynamic_diff: '+r: {}', requires_restart: false });
const operation = () => ({ id: 7, kind: 'router.create', target: 'x', status: 'succeeded', summary: 'Applied 1 change', requires_restart: false, created_at: 'now' });
const envelope = (kind: string, name: string, config: unknown, version = 1, extra = {}) => ({ id: `${kind}:${name}`, name, version, origin: 'managed', provider: 'file', status: 'active', read_only: false, live_health: 'loaded', updated_at: '2026-01-01T00:00:00Z', config, ...extra });

const httpRouter = envelope('router', 'site', { entry_points: ['websecure'], domains: ['app.example.com'], rule: 'Host(`app.example.com`)', service: 'web', tls: { enabled: true, cert_resolver: 'le' } });
const tcpRouter = (name = 'pg', version = 1, config: Record<string, unknown> = {}, extra = {}) =>
	envelope('router', name, { protocol: 'tcp', entry_points: ['postgres'], rule: 'HostSNI(`db.example.com`)', domains: ['db.example.com'], service: 'db', tls: { enabled: true, passthrough: true }, ...config }, version, extra);
const udpRouter = (name = 'dns', version = 1, config: Record<string, unknown> = {}, extra = {}) =>
	envelope('router', name, { protocol: 'udp', entry_points: ['dns'], service: 'resolver', ...config }, version, extra);

const web = envelope('service', 'web', { protocol: 'http', servers: [{ url: 'http://app:8080' }] });
const db = envelope('service', 'db', { protocol: 'tcp', servers: [{ address: 'db:5432' }] });
const resolverSvc = envelope('service', 'resolver', { protocol: 'udp', servers: [{ address: 'dns:53' }] });
const noop = envelope('service', 'noop@internal', { protocol: 'http', servers: null }, 1, { origin: 'external', provider: 'internal', read_only: true });
const auth = envelope('middleware', 'auth', { type: 'basicAuth' });
const office = envelope('middleware', 'office', { protocol: 'tcp', type: 'ipAllowList', spec: { sourceRange: ['10.0.0.0/8'] } });
const cap = envelope('middleware', 'cap', { protocol: 'tcp', type: 'inFlightConn', spec: { amount: 10 } });
const domain = envelope('domain', 'main-domain', { name: 'main-domain', main: '*.example.com', method: 'resolver', resolver: 'le', state: 'ready' });
const resolver = envelope('resolver', 'le', { challenge: 'http01' });
const ENTRYPOINTS = { entry_points: ['web', 'websecure', 'postgres', 'dns'], protocols: { postgres: 'tcp', web: 'tcp', websecure: 'tcp', dns: 'udp' } };

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

const base = (routers: unknown, services: unknown[] = [web, db, resolverSvc, noop]) => ({
	'GET /api/v1/routers': () => json(routers),
	'GET /api/v1/domains': () => json([domain]),
	'GET /api/v1/services': () => json(services),
	'GET /api/v1/middlewares': () => json([auth, office, cap]),
	'GET /api/v1/chains': () => json(null),
	'GET /api/v1/resolvers': () => json([resolver]),
	'GET /api/v1/entrypoints': () => json(ENTRYPOINTS)
});
const mixed = () => base([httpRouter, tcpRouter(), udpRouter()]);
const dry = (name: string) => ({ [`PUT /api/v1/routers/${name}?dry_run=true`]: () => json({ preview: preview(), restart_required: false }) });
const puts = () => calls.filter((c) => c.method === 'PUT');
const count = (path: string) => calls.filter((c) => c.method === 'GET' && c.path === path).length;

async function pick(id: string, name: RegExp) {
	await fireEvent.click(await waitFor(() => document.getElementById(id)!));
	const listbox = await waitFor(() => document.getElementById(id + '-listbox')!);
	await fireEvent.click(await within(listbox).findByRole('option', { name }));
}
const optionsOf = async (id: string) => {
	await fireEvent.click(await waitFor(() => document.getElementById(id)!));
	return within(await waitFor(() => document.getElementById(id + '-listbox')!)).getAllByRole('option').map((o) => o.textContent ?? '');
};
const visible = (panel: string) => !document.getElementById('panel-route-' + panel)!.hidden;
const rowOf = (name: string) => screen.getAllByText(name)[0].closest('tr') as HTMLElement;
const button = (name: string | RegExp) => screen.getByRole('button', { name });
const type = (id: string, value: string) => fireEvent.input(document.getElementById(id)!, { target: { value } });
async function openCreate(protocol: 'tcp' | 'udp' | 'http') {
	render(RoutesPage, { navigate: () => {}, tab: protocol, mode: 'create' });
	await waitFor(() => expect(document.getElementById('route-name')).toBeTruthy());
	await waitFor(() => expect(count('/api/v1/entrypoints')).toBe(1));
}
async function chooseProtocol(label: RegExp) {
	await fireEvent.click(screen.getByRole('button', { name: /Filters/ }));
	await fireEvent.click(screen.getByRole('menuitem', { name: 'Protocol' }));
	await fireEvent.click(await screen.findByRole('menuitemradio', { name: label }));
}

describe('router model per protocol', () => {
	it('knows the protocol of a router, HTTP when it is not said', () => {
		expect(routerProtocolOf({ entry_points: [], domains: null, rule: '', service: '' })).toBe('http');
		expect(routerProtocolOf({ protocol: 'tcp' } as any)).toBe('tcp');
		expect(routerProtocolOf({ protocol: 'udp' } as any)).toBe('udp');
		expect(ROUTER_BASE).toEqual({ http: '/routers', tcp: '/tcp-routers', udp: '/udp-routers' });
	});
	it('starts a TCP router on "all connections" without TLS, and an HTTP one on a domain with TLS', () => {
		expect(newForm('tcp')).toMatchObject({ protocol: 'tcp', mode: 'any', tlsEnabled: false });
		expect(newForm('http')).toMatchObject({ protocol: 'http', mode: 'domain', tlsEnabled: true });
	});
	it('writes a TCP router for every connection', () => {
		expect(toInput({ ...newForm('tcp'), name: 'pg', service: 'db', entryPoints: ['postgres'] }))
			.toEqual({ protocol: 'tcp', entry_points: ['postgres'], service: 'db', custom_rule: 'HostSNI(`*`)' });
	});
	it('writes a TCP router for hostnames, with TLS terminated or passed through, its middlewares, and never a path', () => {
		const host = { ...newForm('tcp'), name: 'pg', mode: 'host' as const, hosts: 'DB.example.com', service: 'db', pathPrefix: '/ignored', middlewares: ['auth'], priority: '5' };
		expect(toInput({ ...host, tlsEnabled: true, tlsResolver: 'le', tlsOptions: 'modern' }))
			.toEqual({ protocol: 'tcp', entry_points: [], domains: ['db.example.com'], service: 'db', middlewares: ['auth'], priority: 5, tls: { enabled: true, cert_resolver: 'le', options: 'modern' } });
		expect(toInput({ ...host, tlsEnabled: true, tlsPassthrough: true, tlsResolver: 'le', tlsOptions: 'modern' }))
			.toEqual({ protocol: 'tcp', entry_points: [], domains: ['db.example.com'], service: 'db', middlewares: ['auth'], priority: 5, tls: { enabled: true, passthrough: true } });
	});
	it('writes a UDP router with entrypoints and a service only', () => {
		expect(toInput({ ...newForm('udp'), name: 'dns', service: 'resolver', entryPoints: ['dns'], hosts: 'x.example.com', customRule: 'x', priority: '3', middlewares: ['auth'], tlsEnabled: true }))
			.toEqual({ protocol: 'udp', entry_points: ['dns'], service: 'resolver' });
	});
	it('leaves an HTTP payload without a protocol, as before', () => {
		expect(toInput({ ...newForm(), name: 'a', domainName: 'main-domain', service: 'web' })).toEqual({ entry_points: [], domain_name: 'main-domain', service: 'web' });
	});
	it('previews the rule: HostSNI for TCP, nothing for UDP', () => {
		expect(previewRule({ ...newForm('tcp') }, undefined)).toBe('HostSNI(`*`)');
		expect(previewRule({ ...newForm('tcp'), mode: 'host', hosts: 'a.example.com' }, undefined)).toBe('HostSNI(`a.example.com`)');
		expect(previewRule({ ...newForm('tcp'), mode: 'host', hosts: 'a.example.com\nb.example.com', pathPrefix: '/x' }, undefined)).toBe('(HostSNI(`a.example.com`) || HostSNI(`b.example.com`))');
		expect(previewRule({ ...newForm('tcp'), mode: 'domain', domainName: 'd', subdomain: 'db' }, '*.example.com')).toBe('HostSNI(`db.example.com`)');
		expect(previewRule({ ...newForm('udp') }, undefined)).toBe('');
	});
	it('loads a stored router back into its protocol and mode', () => {
		expect(formFrom('pg', { protocol: 'tcp', entry_points: ['postgres'], domains: null, rule: 'HostSNI(`*`)', custom_rule: 'HostSNI(`*`)', service: 'db' })).toMatchObject({ protocol: 'tcp', mode: 'any', tlsEnabled: false });
		expect(formFrom('pg', tcpRouter().config as any)).toMatchObject({ protocol: 'tcp', mode: 'host', hosts: 'db.example.com', tlsEnabled: true, tlsPassthrough: true });
		expect(formFrom('dns', udpRouter().config as any)).toMatchObject({ protocol: 'udp', service: 'resolver', entryPoints: ['dns'] });
	});
	it('shows the definition Traefik gets for each protocol', () => {
		expect(routerDefinition(tcpRouter().config as any)).toEqual({ entryPoints: ['postgres'], rule: 'HostSNI(`db.example.com`)', service: 'db', tls: { passthrough: true } });
		expect(routerDefinition(udpRouter().config as any)).toEqual({ entryPoints: ['dns'], service: 'resolver' });
		expect(routerDefinition(tcpRouter('pg', 1, { middlewares: ['cap'] }).config as any).middlewares).toEqual(['cap']);
		expect(routerDefinition(udpRouter('dns', 1, { middlewares: ['cap'] }).config as any)).not.toHaveProperty('middlewares');
	});
});

describe('validation per protocol', () => {
	const messages = new Proxy({}, { get: (_t, k) => String(k) }) as any;
	const ctx = { creating: true, existingNames: ['taken'], domainNames: ['main-domain'] };
	const errors = (form: ReturnType<typeof newForm>) => routeErrors(form, ctx, messages);

	it('accepts a TCP router for all connections with a service', () => {
		expect(errors({ ...newForm('tcp'), name: 'pg', service: 'db', entryPoints: ['postgres'] })).toEqual({});
	});
	it('needs the entrypoints of a TCP or UDP router chosen, never defaulted', () => {
		expect(errors({ ...newForm('tcp'), name: 'pg', service: 'db' })).toEqual({ entryPoints: 'entryPointsRequired' });
		expect(errors({ ...newForm('udp'), name: 'dns', service: 'resolver' })).toEqual({ entryPoints: 'entryPointsRequired' });
		expect(errors({ ...newForm('http'), name: 'a', domainName: 'main-domain', service: 'web' })).toEqual({});
	});
	it('refuses the same TCP middleware twice', () => {
		expect(errors({ ...newForm('tcp'), name: 'pg', service: 'db', entryPoints: ['postgres'], middlewares: ['cap', 'cap'] })).toEqual({ middlewares: 'duplicateMiddleware' });
	});
	it('refuses a TCP hostname without TLS, accepts it with either kind of TLS', () => {
		const form = { ...newForm('tcp'), name: 'pg', mode: 'host' as const, hosts: 'db.example.com', service: 'db', entryPoints: ['postgres'] };
		expect(errors(form).hosts ?? errors(form).tlsEnabled ?? Object.values(errors(form)).join()).toContain('hostsNeedTls');
		expect(errors({ ...form, tlsEnabled: true })).toEqual({});
		expect(errors({ ...form, tlsEnabled: true, tlsPassthrough: true })).toEqual({});
	});
	it('does not ask a TCP router for a middleware check or the built-in no-backend service', () => {
		expect(Object.keys(errors({ ...newForm('tcp'), name: 'pg', service: 'noop@internal' }))).not.toContain('middlewares');
	});
	it('needs a name, a free one, and a service for every protocol', () => {
		for (const p of ['tcp', 'udp'] as const) {
			const e = errors({ ...newForm(p), name: 'Bad Name', service: '' });
			expect(Object.keys(e)).toEqual(expect.arrayContaining(['name', 'service']));
			expect(Object.keys(errors({ ...newForm(p), name: 'taken', service: 's' }))).toContain('name');
		}
	});
	it('asks a UDP router for nothing else: no rule, hostname, TLS or priority', () => {
		expect(errors({ ...newForm('udp'), name: 'dns', service: 'resolver', entryPoints: ['dns'], mode: 'domain', domainName: '', priority: 'abc' })).toEqual({});
	});
});

describe('the list shows every protocol', () => {
	it('reads the routers once and shows one row each, with a Protocol column', async () => {
		handlers = mixed();
		render(RoutesPage, { navigate: () => {} });
		await waitFor(() => expect(screen.getAllByText('site').length).toBeGreaterThan(0));
		expect(calls.filter((c) => c.path === '/api/v1/routers')).toHaveLength(1);
		expect(within(rowOf('site')).getByText('HTTP')).toBeTruthy();
		expect(within(rowOf('pg')).getByText('TCP')).toBeTruthy();
		expect(within(rowOf('dns')).getByText('UDP')).toBeTruthy();
	});
	it('shows what matters for each protocol: SNI rule and passthrough for TCP, no rule or TLS for UDP', async () => {
		handlers = base([tcpRouter(), tcpRouter('plain', 1, { rule: 'HostSNI(`*`)', domains: null, tls: null }), udpRouter()]);
		render(RoutesPage, { navigate: () => {} });
		await waitFor(() => expect(screen.getAllByText('pg').length).toBeGreaterThan(0));
		expect(within(rowOf('pg')).getAllByText('HostSNI(`db.example.com`)').length).toBeGreaterThan(0);
		expect(within(rowOf('pg')).getByText('TLS passthrough')).toBeTruthy();
		expect(within(rowOf('plain')).getByText('Plain TCP')).toBeTruthy();
		expect(within(rowOf('dns')).queryByText('HTTP only')).toBeNull();
		expect(within(rowOf('dns')).queryByText('Plain TCP')).toBeNull();
	});
	it('keeps two routers of the same name in different protocols apart (Traefik allows it for external ones)', async () => {
		handlers = base([
			envelope('router', 'edge@docker', { entry_points: ['web'], rule: 'Host(`a`)', service: 'web' }, 1, { origin: 'external', provider: 'docker', read_only: true }),
			envelope('router', 'edge@docker', { protocol: 'tcp', entry_points: ['postgres'], rule: 'HostSNI(`*`)', service: 'db' }, 1, { origin: 'external', provider: 'docker', read_only: true })
		]);
		render(RoutesPage, { navigate: () => {} });
		await waitFor(() => expect(document.querySelectorAll('tbody tr')).toHaveLength(2));
		expect(screen.getAllByText('TCP').length).toBeGreaterThan(0);
		expect(screen.getAllByText('HTTP').length).toBeGreaterThan(0);
	});
	it('filters by protocol, with a count per protocol', async () => {
		handlers = mixed();
		render(RoutesPage, { navigate: () => {} });
		await waitFor(() => expect(screen.getAllByText('site').length).toBeGreaterThan(0));
		await chooseProtocol(/TCP \(1\)/);
		await waitFor(() => expect(screen.queryByText('site')).toBeNull());
		expect(screen.queryByText('dns')).toBeNull();
		expect(screen.getAllByText('pg').length).toBeGreaterThan(0);
		expect(count('/api/v1/routers')).toBe(1);
	});
	it('shows external TCP and UDP routers read-only', async () => {
		handlers = base([
			tcpRouter('mail@docker', 1, {}, { origin: 'external', provider: 'docker', read_only: true }),
			udpRouter('vpn@docker', 1, {}, { origin: 'external', provider: 'docker', read_only: true })
		]);
		render(RoutesPage, { navigate: () => {} });
		await waitFor(() => expect(screen.getAllByText('mail@docker').length).toBeGreaterThan(0));
		expect(screen.queryByRole('menuitem', { name: 'Edit' })).toBeNull();
		expect(screen.queryByRole('menuitem', { name: 'Delete' })).toBeNull();
	});
	it('links each router to the services page of its own protocol', async () => {
		handlers = mixed();
		const paths: string[] = [];
		render(RoutesPage, { navigate: (p: string) => paths.push(p) });
		await waitFor(() => expect(screen.getAllByText('pg').length).toBeGreaterThan(0));
		await fireEvent.click(within(rowOf('pg')).getByRole('button', { name: 'db' }));
		await fireEvent.click(within(rowOf('dns')).getByRole('button', { name: 'resolver' }));
		await fireEvent.click(within(rowOf('site')).getByRole('button', { name: 'web' }));
		expect(paths).toEqual(['/tcp-services/db', '/udp-services/resolver', '/services/web']);
	});
	it('offers one create menu for the three protocols and opens each under its own path', async () => {
		handlers = mixed();
		const paths: string[] = [];
		render(RoutesPage, { navigate: (p: string) => paths.push(p) });
		await waitFor(() => expect(screen.getAllByText('site').length).toBeGreaterThan(0));
		await fireEvent.click(screen.getAllByRole('button', { name: /Create router/ })[0]);
		const items = screen.getAllByRole('menuitem').filter((m) => /router/.test(m.querySelector('strong')?.textContent ?? ''));
		expect(items.map((m) => m.querySelector('strong')?.textContent)).toEqual(['HTTP router', 'TCP router', 'UDP router']);
		await fireEvent.click(screen.getByRole('menuitem', { name: /^TCP router/ }));
		await fireEvent.click(screen.getAllByRole('button', { name: /Create router/ })[0]);
		await fireEvent.click(screen.getByRole('menuitem', { name: /^UDP router/ }));
		await fireEvent.click(screen.getAllByRole('button', { name: /Create router/ })[0]);
		await fireEvent.click(screen.getByRole('menuitem', { name: /^HTTP router/ }));
		expect(paths).toEqual(['/tcp-routers/new', '/udp-routers/new', '/routers/new']);
	});
	it('opens each router under the base path of its protocol', async () => {
		handlers = mixed();
		const paths: string[] = [];
		render(RoutesPage, { navigate: (p: string) => paths.push(p) });
		await waitFor(() => expect(screen.getAllByText('site').length).toBeGreaterThan(0));
		await fireEvent.click(within(rowOf('pg')).getByRole('button', { name: 'View' }));
		await fireEvent.click(within(rowOf('dns')).getByRole('menuitem', { name: 'Edit' }));
		await fireEvent.click(within(rowOf('site')).getByRole('button', { name: 'View' }));
		expect(paths).toEqual(['/tcp-routers/pg', '/udp-routers/dns/edit', '/routers/site']);
	});
});

describe('creating a TCP router', () => {
	it('opens from the list already loaded, with the TCP pickers only once each', async () => {
		handlers = mixed();
		await openCreate('tcp');
		expect(count('/api/v1/routers')).toBe(1);
		expect(screen.getByRole('dialog').textContent).toContain('Create TCP router');
	});
	it('matches every connection by default and sends plain TCP with the create precondition and one key', async () => {
		handlers = {
			...mixed(), ...dry('pg2'),
			'PUT /api/v1/routers/pg2': () => json({ operation: operation(), resource: tcpRouter('pg2'), restart_required: false })
		};
		await openCreate('tcp');
		await type('route-name', 'pg2');
		expect(screen.getByText('HostSNI(`*`)')).toBeTruthy();
		await fireEvent.click(button('Continue'));
		await pick('route-service', /^db/);
		await pick('route-entrypoints', /^postgres/);
		await fireEvent.click(button('Continue'));
		await fireEvent.click(button('Review change'));
		await screen.findByRole('button', { name: 'Apply changes' });
		const put = puts()[0];
		expect(put.path).toBe('/api/v1/routers/pg2?dry_run=true');
		expect(put.headers['if-none-match']).toBe('*');
		expect(put.body).toEqual({ protocol: 'tcp', entry_points: ['postgres'], service: 'db', custom_rule: 'HostSNI(`*`)' });
		await fireEvent.click(button('Apply changes'));
		await waitFor(() => expect(flashes.some((f) => f.title.startsWith('Applied'))).toBe(true));
		const apply = puts()[1];
		expect(apply.path).toBe('/api/v1/routers/pg2');
		expect(apply.headers['idempotency-key']).toMatch(/^[A-Za-z0-9_-]{16,128}$/);
	});
	it('offers only TCP services, and never the built-in no-backend one', async () => {
		handlers = { ...mixed(), ...dry('pg2') };
		await openCreate('tcp');
		await type('route-name', 'pg2');
		await fireEvent.click(button('Continue'));
		const options = await optionsOf('route-service');
		expect(options.some((o) => o.includes('db'))).toBe(true);
		expect(options.some((o) => o.includes('web') || o.includes('resolver') || o.includes('No backend'))).toBe(false);
	});
	it('offers TCP entrypoints and not UDP ones, and says which ones HTTP routers use', async () => {
		handlers = mixed();
		await openCreate('tcp');
		await type('route-name', 'pg2');
		await fireEvent.click(button('Continue'));
		const options = (await optionsOf('route-entrypoints')).map((o) => o.trim());
		expect(options).toEqual(expect.arrayContaining(['postgres', 'web']));
		expect(options).not.toContain('dns');
		// websecure carries the HTTP router "site": the option says so.
		expect((await optionsOf('route-entrypoints')).find((o) => o.includes('websecure'))).toContain('Also used by HTTP routers');
	});
	it('has no path, and offers only TCP middlewares, without chains', async () => {
		handlers = { ...mixed(), 'GET /api/v1/chains': () => json([envelope('chain', 'secure', { middlewares: ['auth'] })]) };
		await openCreate('tcp');
		expect(document.getElementById('route-path')).toBeNull();
		await type('route-name', 'pg2');
		await fireEvent.click(button('Continue'));
		await pick('route-service', /^db/);
		await pick('route-entrypoints', /^postgres/);
		await fireEvent.click(button('Continue'));
		const options = await optionsOf('route-add-middleware');
		expect(options.some((o) => o.includes('office'))).toBe(true);
		expect(options.some((o) => o.includes('cap'))).toBe(true);
		expect(options.some((o) => o.includes('auth') || o.includes('secure'))).toBe(false);
		expect(count('/api/v1/chains')).toBe(0);
	});
	it('sends its TCP middlewares in the chosen order', async () => {
		handlers = { ...mixed(), ...dry('pg2') };
		await openCreate('tcp');
		await type('route-name', 'pg2');
		await fireEvent.click(button('Continue'));
		await pick('route-service', /^db/);
		await pick('route-entrypoints', /^postgres/);
		await fireEvent.click(button('Continue'));
		await pick('route-add-middleware', /cap/);
		await pick('route-add-middleware', /office/);
		await fireEvent.click(button('Review change'));
		await waitFor(() => expect(puts()).toHaveLength(1));
		expect(puts()[0].body).toEqual({ protocol: 'tcp', entry_points: ['postgres'], service: 'db', custom_rule: 'HostSNI(`*`)', middlewares: ['cap', 'office'] });
	});
	it('offers to create a TCP middleware when there is none', async () => {
		handlers = { ...mixed(), 'GET /api/v1/middlewares': () => json([auth]) };
		const paths: string[] = [];
		render(RoutesPage, { navigate: (p: string) => paths.push(p), tab: 'tcp', mode: 'create' });
		await waitFor(() => expect(document.getElementById('route-name')).toBeTruthy());
		await type('route-name', 'pg2');
		await fireEvent.click(button('Continue'));
		await pick('route-service', /^db/);
		await pick('route-entrypoints', /^postgres/);
		await fireEvent.click(button('Continue'));
		await screen.findByText('There is no TCP middleware yet.');
		await fireEvent.click(screen.getByRole('button', { name: 'Create a TCP middleware' }));
		expect(paths).toEqual(['/tcp-middlewares/new']);
	});
	it('warns that matching every connection on an HTTP entrypoint takes its traffic', async () => {
		handlers = mixed();
		await openCreate('tcp');
		await type('route-name', 'pg2');
		await fireEvent.click(button('Continue'));
		await pick('route-entrypoints', /^websecure/);
		expect(screen.getByText(/HTTP routers also listen on websecure/)).toBeTruthy();
		await pick('route-entrypoints', /^websecure/);
		await pick('route-entrypoints', /^postgres/);
		expect(screen.queryByText(/HTTP routers also listen on/)).toBeNull();
	});
	it('needs an entrypoint before it continues', async () => {
		handlers = mixed();
		await openCreate('tcp');
		await type('route-name', 'pg2');
		await fireEvent.click(button('Continue'));
		await pick('route-service', /^db/);
		await fireEvent.click(button('Continue'));
		await waitFor(() => expect(screen.getAllByText('Choose at least one entrypoint.').length).toBeGreaterThan(0));
		expect(visible('traffic')).toBe(true);
		expect(visible('tls')).toBe(false);
		expect(puts()).toHaveLength(0);
	});
	it('does not show "no TCP service" once a service is chosen', async () => {
		handlers = { ...mixed(), 'GET /api/v1/services': () => json([web, db, resolverSvc]) };
		await openCreate('tcp');
		await type('route-name', 'pg2');
		await fireEvent.click(button('Continue'));
		await pick('route-service', /^db/);
		expect(screen.queryByText(/There is no TCP service/)).toBeNull();
	});
	it('sends a hostname router with TLS terminated by Traefik', async () => {
		handlers = { ...mixed(), ...dry('pg2') };
		await openCreate('tcp');
		await type('route-name', 'pg2');
		await pick('route-mode', /^Hostnames/);
		await type('route-hosts', 'DB.example.com');
		await fireEvent.click(button('Continue'));
		await pick('route-service', /^db/);
		await pick('route-entrypoints', /^postgres/);
		await fireEvent.click(button('Continue'));
		expect((document.getElementById('route-tls-enabled') as HTMLInputElement).checked).toBe(true);
		await pick('route-tls-resolver', /le/);
		await fireEvent.click(button('Review change'));
		await waitFor(() => expect(puts()).toHaveLength(1));
		expect(puts()[0].body).toEqual({ protocol: 'tcp', entry_points: ['postgres'], domains: ['db.example.com'], service: 'db', tls: { enabled: true, cert_resolver: 'le' } });
	});
	it('sends a hostname router with TLS passed through, without a resolver or options', async () => {
		handlers = { ...mixed(), ...dry('pg2') };
		await openCreate('tcp');
		await type('route-name', 'pg2');
		await pick('route-mode', /^Hostnames/);
		await type('route-hosts', 'db.example.com');
		await fireEvent.click(button('Continue'));
		await pick('route-service', /^db/);
		await pick('route-entrypoints', /^postgres/);
		await fireEvent.click(button('Continue'));
		await fireEvent.click(document.getElementById('route-tls-passthrough')!);
		expect(document.getElementById('route-tls-resolver')).toBeNull();
		await fireEvent.click(button('Review change'));
		await waitFor(() => expect(puts()).toHaveLength(1));
		expect(puts()[0].body).toEqual({ protocol: 'tcp', entry_points: ['postgres'], domains: ['db.example.com'], service: 'db', tls: { enabled: true, passthrough: true } });
	});
	it('turns TLS on when it matches hostnames, and off again for all connections', async () => {
		handlers = { ...mixed(), ...dry('pg2') };
		await openCreate('tcp');
		const tls = () => (document.getElementById('route-tls-enabled') as HTMLInputElement | null)?.checked;
		await pick('route-mode', /^Hostnames/);
		expect(tls()).toBe(true);
		await pick('route-mode', /^All connections/);
		expect(screen.getByText(/TLS is not available/)).toBeTruthy();
		await type('route-name', 'pg2');
		await fireEvent.click(button('Continue'));
		await pick('route-service', /^db/);
		await pick('route-entrypoints', /^postgres/);
		await fireEvent.click(button('Continue'));
		await fireEvent.click(button('Review change'));
		await waitFor(() => expect(puts()).toHaveLength(1));
		expect(puts()[0].body).not.toHaveProperty('tls');
	});
	it('refuses a hostname once TLS is turned off, before asking the server', async () => {
		handlers = { ...mixed(), ...dry('pg2') };
		await openCreate('tcp');
		await type('route-name', 'pg2');
		await pick('route-mode', /^Hostnames/);
		await type('route-hosts', 'db.example.com');
		await fireEvent.click(button('Continue'));
		await pick('route-service', /^db/);
		await pick('route-entrypoints', /^postgres/);
		await fireEvent.click(button('Continue'));
		await fireEvent.click(document.getElementById('route-tls-enabled')!);
		await fireEvent.click(button('Review change'));
		await waitFor(() => expect(screen.getAllByText(/TLS has to be on/).length).toBeGreaterThan(0));
		expect(puts()).toHaveLength(0);
	});
	it('does not continue past an empty or invalid name', async () => {
		handlers = mixed();
		await openCreate('tcp');
		for (const value of ['', 'Bad Name']) {
			await type('route-name', value);
			await fireEvent.click(button('Continue'));
			await waitFor(() => expect(document.getElementById('route-name')!.getAttribute('aria-invalid')).toBe('true'));
			expect(visible('route')).toBe(true);
		}
		await type('route-name', 'pg2');
		await fireEvent.click(button('Continue'));
		await waitFor(() => expect(visible('traffic')).toBe(true));
		expect(puts()).toHaveLength(0);
	});
	it('does not continue past a missing service either', async () => {
		handlers = mixed();
		await openCreate('tcp');
		await type('route-name', 'pg2');
		await fireEvent.click(button('Continue'));
		await pick('route-entrypoints', /^postgres/);
		await fireEvent.click(button('Continue'));
		await waitFor(() => expect(screen.getAllByText('Select or enter a valid target service reference.').length).toBeGreaterThan(0));
		expect(visible('tls')).toBe(false);
	});
	it('refuses a name already used by a router of another protocol', async () => {
		handlers = mixed();
		await openCreate('tcp');
		await type('route-name', 'site');
		await fireEvent.click(button('Continue'));
		await waitFor(() => expect(screen.getAllByText(/already exists/).length).toBeGreaterThan(0));
		expect(puts()).toHaveLength(0);
	});
	it('says so when there is no TCP service yet, and links to the TCP services page', async () => {
		handlers = base([], [web, resolverSvc, noop]);
		const paths: string[] = [];
		render(RoutesPage, { navigate: (p: string) => paths.push(p), tab: 'tcp', mode: 'create' });
		await waitFor(() => expect(document.getElementById('route-name')).toBeTruthy());
		await type('route-name', 'pg2');
		await fireEvent.click(button('Continue'));
		await screen.findByText('There is no TCP service to route to yet.');
		await fireEvent.click(screen.getByRole('button', { name: 'Create a TCP service' }));
		expect(paths).toEqual(['/tcp-services/new']);
	});
	it('shows the server\'s refusal of the review and stays on the dialog, nothing applied', async () => {
		handlers = {
			...mixed(),
			'PUT /api/v1/routers/pg2?dry_run=true': () => json({ code: 'ERR_VALIDATION', message: 'router "pg2": service "db" is not a TCP service', operation_id: 'op-1', retryable: false }, 422)
		};
		await openCreate('tcp');
		await type('route-name', 'pg2');
		await fireEvent.click(button('Continue'));
		await pick('route-service', /^db/);
		await pick('route-entrypoints', /^postgres/);
		await fireEvent.click(button('Continue'));
		await fireEvent.click(button('Review change'));
		await waitFor(() => expect(screen.getByText(/is not a TCP service/)).toBeTruthy());
		expect(screen.queryByRole('button', { name: 'Apply changes' })).toBeNull();
		expect(puts()).toHaveLength(1);
		expect(screen.getByRole('dialog')).toBeTruthy();
	});
	it('keeps the dialog open and says what happened when applying fails', async () => {
		handlers = {
			...mixed(), ...dry('pg2'),
			'PUT /api/v1/routers/pg2': () => json({ code: 'ERR_CONFLICT', message: 'router "pg2" already exists', operation_id: 'op-2', retryable: false }, 409)
		};
		await openCreate('tcp');
		await type('route-name', 'pg2');
		await fireEvent.click(button('Continue'));
		await pick('route-service', /^db/);
		await pick('route-entrypoints', /^postgres/);
		await fireEvent.click(button('Continue'));
		await fireEvent.click(button('Review change'));
		await fireEvent.click(await screen.findByRole('button', { name: 'Apply changes' }));
		await waitFor(() => expect(screen.getAllByText(/already exists/).length).toBeGreaterThan(0));
		expect(flashes.some((f) => f.title.startsWith('Applied'))).toBe(false);
	});
});

describe('creating a UDP router', () => {
	it('starts on the traffic step: there is nothing to match', async () => {
		handlers = mixed();
		render(RoutesPage, { navigate: () => {}, tab: 'udp', mode: 'create' });
		await waitFor(() => expect(document.getElementById('route-name')).toBeTruthy());
		expect(screen.getByRole('dialog').textContent).toContain('Create UDP router');
		expect(document.getElementById('route-service')).toBeTruthy();
		expect(document.getElementById('route-hosts')).toBeNull();
		expect(document.getElementById('route-mode')).toBeNull();
		expect(document.getElementById('route-priority')).toBeNull();
		expect(document.getElementById('route-tls-enabled')).toBeNull();
		expect(screen.getByText(/There is nothing to match/)).toBeTruthy();
	});
	it('does not load what a UDP router does not use', async () => {
		handlers = mixed();
		await openCreate('udp');
		await waitFor(() => expect(count('/api/v1/services')).toBe(1));
		for (const p of ['domains', 'middlewares', 'chains', 'resolvers']) expect(count('/api/v1/' + p)).toBe(0);
	});
	it('sends entrypoints and a service only, with the create precondition', async () => {
		handlers = {
			...mixed(), ...dry('dns2'),
			'PUT /api/v1/routers/dns2': () => json({ operation: operation(), resource: udpRouter('dns2'), restart_required: false })
		};
		await openCreate('udp');
		await type('route-name', 'dns2');
		await pick('route-service', /resolver/);
		await pick('route-entrypoints', /^dns/);
		await fireEvent.click(button('Review change'));
		await screen.findByRole('button', { name: 'Apply changes' });
		expect(puts()[0].headers['if-none-match']).toBe('*');
		expect(puts()[0].body).toEqual({ protocol: 'udp', entry_points: ['dns'], service: 'resolver' });
		const review = screen.getByRole('dialog').textContent ?? '';
		expect(review).toContain('UDP router');
		await fireEvent.click(button('Apply changes'));
		await waitFor(() => expect(flashes.some((f) => f.title.startsWith('Applied'))).toBe(true));
		expect(puts()[1].path).toBe('/api/v1/routers/dns2');
	});
	it('offers only UDP services and UDP entrypoints', async () => {
		handlers = mixed();
		await openCreate('udp');
		const services = await optionsOf('route-service');
		expect(services.some((o) => o.includes('resolver'))).toBe(true);
		expect(services.some((o) => o.includes('web') || o.includes('db') || o.includes('No backend'))).toBe(false);
		const entryPoints = (await optionsOf('route-entrypoints')).map((o) => o.trim());
		expect(entryPoints).toEqual(['dns']);
	});
	it('warns when no UDP entrypoint is declared', async () => {
		handlers = { ...mixed(), 'GET /api/v1/entrypoints': () => json({ entry_points: ['web'], protocols: { web: 'tcp' } }) };
		await openCreate('udp');
		await screen.findByText(/No UDP entrypoint is declared yet/);
	});
	it('needs a name, a service and an entrypoint, and does not call the server without them', async () => {
		handlers = mixed();
		await openCreate('udp');
		await type('route-name', 'Bad Name');
		await fireEvent.click(button('Review change'));
		await waitFor(() => expect(document.getElementById('route-name')!.getAttribute('aria-invalid')).toBe('true'));
		expect(screen.getAllByText('Select or enter a valid target service reference.').length).toBeGreaterThan(0);
		expect(screen.getAllByText('Choose at least one entrypoint.').length).toBeGreaterThan(0);
		expect(puts()).toHaveLength(0);
	});
	it('says so when there is no UDP service yet', async () => {
		handlers = base([], [web, db, noop]);
		await openCreate('udp');
		await screen.findByText('There is no UDP service to route to yet.');
	});
	it('shows the server\'s refusal and applies nothing', async () => {
		handlers = {
			...mixed(),
			'PUT /api/v1/routers/dns2?dry_run=true': () => json({ code: 'ERR_VALIDATION', message: 'entrypoint "web" is not a UDP entrypoint', operation_id: 'op-3', retryable: false }, 422)
		};
		await openCreate('udp');
		await type('route-name', 'dns2');
		await pick('route-service', /resolver/);
		await pick('route-entrypoints', /^dns/);
		await fireEvent.click(button('Review change'));
		await waitFor(() => expect(screen.getByText(/not a UDP entrypoint/)).toBeTruthy());
		expect(screen.queryByRole('button', { name: 'Apply changes' })).toBeNull();
	});
});

describe('editing and viewing', () => {
	it('edits a TCP router with the version it was read at, keeping its protocol and name', async () => {
		handlers = {
			...base([tcpRouter('pg', 6)]), ...dry('pg'),
			'PUT /api/v1/routers/pg': () => json({ operation: operation(), resource: tcpRouter('pg', 7), restart_required: false })
		};
		render(RoutesPage, { navigate: () => {}, tab: 'tcp', mode: 'edit', name: 'pg' });
		await waitFor(() => expect((document.getElementById('route-name') as HTMLInputElement).value).toBe('pg'));
		expect((document.getElementById('route-name') as HTMLInputElement).readOnly).toBe(true);
		expect(screen.getByRole('dialog').textContent).toContain('Edit TCP router');
		expect(count('/api/v1/routers')).toBe(1);
		await fireEvent.click(button('Continue'));
		await fireEvent.click(button('Continue'));
		await fireEvent.click(button('Review change'));
		await screen.findByRole('button', { name: 'Apply changes' });
		expect(puts()[0].headers['if-match']).toBe('"6"');
		expect(puts()[0].body).toEqual({ protocol: 'tcp', entry_points: ['postgres'], domains: ['db.example.com'], service: 'db', tls: { enabled: true, passthrough: true } });
	});
	it('edits a UDP router and keeps it a UDP router', async () => {
		handlers = { ...base([udpRouter('dns', 3)]), ...dry('dns') };
		render(RoutesPage, { navigate: () => {}, tab: 'udp', mode: 'edit', name: 'dns' });
		await waitFor(() => expect((document.getElementById('route-name') as HTMLInputElement).value).toBe('dns'));
		await fireEvent.click(button('Review change'));
		await waitFor(() => expect(puts()).toHaveLength(1));
		expect(puts()[0].headers['if-match']).toBe('"3"');
		expect(puts()[0].body).toEqual({ protocol: 'udp', entry_points: ['dns'], service: 'resolver' });
	});
	it('reports a version conflict on a stale edit, without applying', async () => {
		handlers = {
			...base([tcpRouter('pg', 6)]),
			'PUT /api/v1/routers/pg?dry_run=true': () => json({ code: 'ERR_VERSION_CONFLICT', message: 'router changed', operation_id: 'op-4', retryable: false }, 409)
		};
		render(RoutesPage, { navigate: () => {}, tab: 'tcp', mode: 'edit', name: 'pg' });
		await waitFor(() => expect((document.getElementById('route-name') as HTMLInputElement).value).toBe('pg'));
		await fireEvent.click(button('Continue'));
		await fireEvent.click(button('Continue'));
		await fireEvent.click(button('Review change'));
		await waitFor(() => expect(screen.getAllByText(/changed/).length).toBeGreaterThan(0));
		expect(screen.queryByRole('button', { name: 'Apply changes' })).toBeNull();
	});
	it('picks the router of the URL protocol when two share a name', async () => {
		handlers = base([
			envelope('router', 'edge@docker', { entry_points: ['web'], rule: 'Host(`a`)', service: 'web' }, 1, { origin: 'external', provider: 'docker', read_only: true }),
			envelope('router', 'edge@docker', { protocol: 'tcp', entry_points: ['postgres'], rule: 'HostSNI(`*`)', service: 'db' }, 1, { origin: 'external', provider: 'docker', read_only: true })
		]);
		render(RoutesPage, { navigate: () => {}, tab: 'tcp', mode: 'view', name: 'edge@docker' });
		await waitFor(() => expect(document.querySelector('dialog')!.textContent).toContain('HostSNI(`*`)'));
		expect(document.querySelector('dialog')!.textContent).not.toContain('Host(`a`)');
	});
	it('shows a TCP router with its SNI rule, TLS passthrough, middlewares and service link', async () => {
		handlers = base([tcpRouter('pg', 2, { middlewares: ['cap'] })]);
		const paths: string[] = [];
		render(RoutesPage, { navigate: (p: string) => paths.push(p), tab: 'tcp', mode: 'view', name: 'pg' });
		await waitFor(() => expect(document.querySelector('dialog')!.textContent).toContain('service:'));
		const dialog = document.querySelector('dialog') as HTMLElement;
		const text = dialog.textContent ?? '';
		expect(text).toContain('TCP router');
		expect(text).toContain('rule: HostSNI(`db.example.com`)');
		expect(text).toContain('passthrough: true');
		expect(text).toContain('middlewares:');
		expect(text).toContain('- cap');
		await fireEvent.click(within(dialog).getByRole('button', { name: 'db' }));
		expect(paths).toContain('/tcp-services/db');
	});
	it('shows a UDP router without a rule, TLS or middlewares', async () => {
		handlers = base([udpRouter()]);
		render(RoutesPage, { navigate: () => {}, tab: 'udp', mode: 'view', name: 'dns' });
		await waitFor(() => expect(document.querySelector('dialog')!.textContent).toContain('service:'));
		const text = document.querySelector('dialog')!.textContent ?? '';
		expect(text).toContain('UDP router');
		expect(text).toContain('entryPoints:');
		expect(text).not.toContain('rule:');
		expect(text).not.toContain('tls:');
		expect(text).not.toContain('Middlewares');
	});
	it('opens the editor of the right protocol from the view', async () => {
		handlers = base([udpRouter()]);
		const paths: string[] = [];
		render(RoutesPage, { navigate: (p: string) => paths.push(p), tab: 'udp', mode: 'view', name: 'dns' });
		await waitFor(() => expect(document.querySelector('dialog')!.textContent).toContain('service:'));
		await fireEvent.click(within(document.querySelector('dialog') as HTMLElement).getByRole('button', { name: 'Edit router' }));
		expect(paths).toEqual(['/udp-routers/dns/edit']);
	});
});

describe('deleting', () => {
	it('deletes a TCP router with its version', async () => {
		handlers = { ...base([tcpRouter('pg', 4)]), 'DELETE /api/v1/routers/pg': () => json({ operation: operation(), restart_required: false }) };
		render(RoutesPage, { navigate: () => {} });
		await waitFor(() => expect(screen.getAllByText('pg').length).toBeGreaterThan(0));
		await fireEvent.click(within(rowOf('pg')).getByRole('menuitem', { name: 'Delete' }));
		await fireEvent.click(await screen.findByRole('button', { name: 'Confirm deletion' }));
		await waitFor(() => expect(calls.some((c) => c.method === 'DELETE')).toBe(true));
		expect(calls.find((c) => c.method === 'DELETE')!.headers['if-match']).toBe('"4"');
	});
	it('deletes a UDP router, and shows a conflict if it changed meanwhile', async () => {
		handlers = {
			...base([udpRouter('dns', 9)]),
			'DELETE /api/v1/routers/dns': () => json({ code: 'ERR_VERSION_CONFLICT', message: 'router changed', operation_id: 'o', retryable: false }, 409)
		};
		render(RoutesPage, { navigate: () => {} });
		await waitFor(() => expect(screen.getAllByText('dns').length).toBeGreaterThan(0));
		await fireEvent.click(within(rowOf('dns')).getByRole('menuitem', { name: 'Delete' }));
		await fireEvent.click(await screen.findByRole('button', { name: 'Confirm deletion' }));
		await waitFor(() => expect(screen.getByText(/router changed/)).toBeTruthy());
		expect(calls.find((c) => c.method === 'DELETE')!.headers['if-match']).toBe('"9"');
	});
});

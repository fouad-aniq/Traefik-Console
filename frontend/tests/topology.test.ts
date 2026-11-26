import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/svelte';
// The API client binds `fetch` at import time, so install a forwarding stub first.
vi.hoisted(() => {
	(globalThis as any).__mockFetch = async () => { throw new Error('no mock'); };
	globalThis.fetch = ((...args: unknown[]) => (globalThis as any).__mockFetch(...args)) as typeof fetch;
});
import TopologyPage from '../src/features/topology/TopologyPage.svelte';
import { buildChange, buildGraph, canConnect, canDisconnect, filterOwnership, layoutGraph, nodeId, pathThrough, pipelineEdges, protocolOf, type Connection, type Inventory } from '../src/features/topology/graph';
import { flashes } from '../src/shared/ui/flash.svelte';

const envelope = (kind: string, name: string, config: unknown, extra = {}) =>
	({ id: `${kind}:${name}`, name, version: 3, origin: 'managed', provider: 'file', status: 'active', read_only: false, live_health: 'loaded', config, ...extra }) as any;
const router = (name: string, config: Record<string, unknown> = {}, extra = {}) =>
	envelope('router', name, { entry_points: ['websecure'], domains: ['app.example.com'], rule: 'Host(`app.example.com`)', service: 'web', tls: { enabled: true }, ...config }, extra);

const inventory = (): Inventory => ({
	entryPoints: ['web', 'websecure'],
	resolvers: [envelope('resolver', 'le', { type: 'acme', challenge: 'http01' }), envelope('resolver', 'le-dns', { type: 'acme', challenge: 'dns01', provider: 'cloudflare' })],
	certificates: [envelope('certificate', 'wild', { purpose: 'server', domains: ['*.example.com'], status: 'valid' }), envelope('certificate', 'other', { purpose: 'server', domains: ['other.org'], status: 'valid' })],
	domains: [envelope('domain', 'main', { name: 'main', main: '*.example.com', sans: [], method: 'resolver', resolver: 'le-dns', state: 'ready' })],
	services: [envelope('service', 'web', { servers: [{ url: 'http://app:8080' }, { url: 'http://app2:8080' }] }), envelope('service', 'api', { servers: [{ url: 'http://api:9000' }] })],
	middlewares: [envelope('middleware', 'auth', { type: 'basicAuth', spec: {} }), envelope('middleware', 'gzip', { type: 'compress', spec: {} }),
		envelope('middleware', 'limit@docker', { type: 'rateLimit', spec: {} }, { origin: 'external', provider: 'docker', read_only: true })],
	chains: [envelope('chain', 'secure', { middlewares: ['auth', 'gzip@file'] }), envelope('chain', 'outer', { middlewares: ['secure'] })],
	routers: [
		router('app', { middlewares: ['secure', 'ghost'] }),
		router('shop', { domain_name: 'main', subdomain: 'shop', domains: null, rule: 'Host(`shop.example.com`)', service: 'api', middlewares: ['auth@file'], tls: null }),
		router('legacy@docker', { entry_points: ['web'], service: 'legacy', middlewares: ['limit'], tls: null }, { origin: 'external', provider: 'docker', read_only: true })
	]
});
const node = (g: ReturnType<typeof buildGraph>, kind: any, name: string) => g.byId.get(nodeId(kind, name))!;
const connect = (g: ReturnType<typeof buildGraph>, a: [any, string], b: [any, string]) => canConnect(g, node(g, ...a), node(g, ...b));
const connection = (g: ReturnType<typeof buildGraph>, a: [any, string], b: [any, string]): Connection => {
	const result = connect(g, a, b);
	if (!result.ok) throw new Error('expected a valid link, got ' + result.error);
	return result.connection;
};

describe('graph model', () => {
	const g = buildGraph(inventory());
	const has = (relation: string, subject: string, object: string) => g.edges.some((e) => e.relation === relation && e.subject === subject && e.object === object);

	it('links every reference, whatever its spelling', () => {
		expect(has('route-entrypoint', 'route:app', 'entrypoint:websecure')).toBe(true);
		expect(has('route-service', 'route:app', 'service:web')).toBe(true);
		expect(has('route-middleware', 'route:app', 'chain:secure')).toBe(true);
		expect(has('route-middleware', 'route:shop', 'middleware:auth')).toBe(true); // auth@file
		expect(has('chain-middleware', 'chain:secure', 'middleware:gzip')).toBe(true); // gzip@file
		expect(has('chain-middleware', 'chain:outer', 'chain:secure')).toBe(true);
		expect(has('route-domain', 'route:shop', 'domain:main')).toBe(true);
		expect(has('domain-resolver', 'domain:main', 'resolver:le-dns')).toBe(true);
		// An external router may omit the provider suffix of its own provider.
		expect(has('route-middleware', 'route:legacy@docker', 'middleware:limit@docker')).toBe(true);
	});

	it('keeps the order of middlewares and draws edges left to right', () => {
		const edge = g.edges.find((e) => e.subject === 'route:app' && e.object === 'middleware:ghost')!;
		expect(edge.order).toBe(2);
		const ep = g.edges.find((e) => e.relation === 'route-entrypoint' && e.subject === 'route:app')!;
		expect([ep.from, ep.to]).toEqual(['entrypoint:websecure', 'route:app']);
	});

	it('shows dangling references as missing objects, unless their list failed to load', () => {
		expect(node(g, 'middleware', 'ghost').missing).toBe(true);
		expect(node(g, 'service', 'legacy').missing).toBe(true);
		const partial = buildGraph({ ...inventory(), services: [], failed: ['service'] });
		expect(node(partial, 'service', 'web').missing).toBe(false);
	});

	it('lays kinds out in lanes without overlaps and skips hidden kinds', () => {
		const layout = layoutGraph(g);
		const x = (id: string) => layout.positions.get(id)!.x;
		expect(x('resolver:le')).toBeLessThan(x('domain:main'));
		expect(x('domain:main')).toBe(x('entrypoint:web'));
		expect(x('entrypoint:web')).toBeLessThan(x('route:app'));
		expect(x('route:app')).toBeLessThan(x('chain:secure'));
		// Chains share the middleware lane, above the middlewares, with their members listed first in order.
		const y = (id: string) => layout.positions.get(id)!.y;
		expect(x('chain:secure')).toBe(x('middleware:auth'));
		expect(y('chain:secure')).toBeLessThan(y('middleware:auth'));
		expect(['middleware:auth', 'middleware:gzip'].map(y)).toEqual([...layout.positions.entries()].filter(([id]) => id.startsWith('middleware:')).map(([, p]) => p.y).sort((a, b) => a - b).slice(0, 2));
		expect(x('middleware:auth')).toBeLessThan(x('service:web'));
		const spots = [...layout.positions.values()].map((p) => `${p.x}:${p.y}`);
		expect(new Set(spots).size).toBe(spots.length);
		const hidden = layoutGraph(g, new Set(['chain', 'middleware'] as const));
		expect(hidden.positions.has('chain:secure')).toBe(false);
		expect(hidden.width).toBeLessThan(layout.width);
	});

	it('follows the traffic path through a node in both directions', () => {
		const path = pathThrough(g, 'chain:secure');
		for (const id of ['route:app', 'entrypoint:websecure', 'middleware:auth', 'middleware:gzip', 'chain:outer']) expect(path.nodes.has(id)).toBe(true);
		expect(path.nodes.has('route:shop')).toBe(false);
		// …and on to the service of every route that runs it.
		expect(path.nodes.has('service:web')).toBe(true);
		expect(path.nodes.has('service:api')).toBe(false);
	});

	it('draws each route as its request pipeline: route, middlewares in order, service', () => {
		const drawn = (visible: (id: string) => boolean = () => true) => new Map(pipelineEdges(g, visible).map((e) => [`${e.subject}>${e.object}`, e.from]));
		const all = drawn();
		// app: secure → ghost → web
		expect(all.get('route:app>chain:secure')).toBe('route:app');
		expect(all.get('route:app>middleware:ghost')).toBe('chain:secure');
		expect(all.get('route:app>service:web')).toBe('middleware:ghost');
		// shop has one middleware; a route without any goes straight to its service.
		expect(all.get('route:shop>service:api')).toBe('middleware:auth');
		const bare = buildGraph({ ...inventory(), routers: [router('plain', { middlewares: [] })] });
		expect(pipelineEdges(bare, () => true).find((e) => e.relation === 'route-service')!.from).toBe('route:plain');
		// Hidden steps are skipped, never a gap; other links are untouched.
		const noChains = drawn((id) => !id.startsWith('chain:'));
		expect(noChains.get('route:app>middleware:ghost')).toBe('route:app');
		expect(noChains.has('route:app>chain:secure')).toBe(false);
		expect(drawn((id) => !id.startsWith('chain:') && !id.startsWith('middleware:')).get('route:app>service:web')).toBe('route:app');
		expect(all.get('chain:secure>middleware:auth')).toBe('chain:secure');
	});

	it('labels services with the protocol of their backends', () => {
		expect(node(g, 'service', 'web').protocol).toBe('HTTP');
		expect(protocolOf(['https://a:443', 'h2c://b:80', 'https://c'])).toBe('HTTPS / H2C');
		expect(protocolOf([])).toBe('');
	});
});

describe('link rules', () => {
	const g = buildGraph(inventory());
	const error = (a: [any, string], b: [any, string]) => { const r = connect(g, a, b); return r.ok ? '' : r.error; };

	it('accepts a drop in either direction and always edits the owning object', () => {
		for (const [a, b] of [[['middleware', 'gzip'], ['route', 'app']], [['route', 'app'], ['middleware', 'gzip']]] as [any, string][][]) {
			const c = connection(g, a, b);
			expect([c.relation, c.subject.id, c.object.id]).toEqual(['route-middleware', 'route:app', 'middleware:gzip']);
		}
		expect(connection(g, ['service', 'api'], ['route', 'app']).relation).toBe('route-service');
		expect(connection(g, ['entrypoint', 'web'], ['route', 'app']).relation).toBe('route-entrypoint');
		expect(connection(g, ['resolver', 'le'], ['route', 'app']).relation).toBe('route-resolver');
		// External middlewares can be attached to a managed route.
		expect(connection(g, ['middleware', 'limit@docker'], ['route', 'app']).object.name).toBe('limit@docker');
	});

	it('refuses what the backend would refuse, with a reason', () => {
		expect(error(['service', 'web'], ['middleware', 'auth'])).toBe('incompatible');
		expect(error(['service', 'web'], ['route', 'app'])).toBe('already');
		expect(error(['middleware', 'auth'], ['route', 'shop'])).toBe('already'); // stored as auth@file
		expect(error(['middleware', 'auth'], ['route', 'legacy@docker'])).toBe('readOnly');
		expect(error(['middleware', 'ghost'], ['route', 'shop'])).toBe('missing');
		expect(error(['resolver', 'le'], ['route', 'shop'])).toBe('domainTls');
		expect(error(['chain', 'outer'], ['chain', 'secure'])).toBe('cycle'); // secure is already inside outer
		expect(error(['resolver', 'le'], ['domain', 'main'])).toBe('wildcardChallenge');
		expect(error(['certificate', 'other'], ['domain', 'main'])).toBe('notCovered');
		expect(connection(g, ['certificate', 'wild'], ['domain', 'main']).relation).toBe('domain-certificate');
	});

	it('only lets optional links be removed', () => {
		const edge = (relation: string, subject: string) => g.edges.find((e) => e.relation === relation && e.subject === subject)!;
		expect(canDisconnect(g, edge('route-middleware', 'route:app')).ok).toBe(true);
		expect(canDisconnect(g, edge('route-entrypoint', 'route:app')).ok).toBe(true);
		expect(canDisconnect(g, edge('route-service', 'route:app')).ok).toBe(false);
		expect(canDisconnect(g, edge('chain-middleware', 'chain:outer')).ok).toBe(false); // a chain needs one member
		expect(canDisconnect(g, edge('route-middleware', 'route:legacy@docker')).ok).toBe(false);
	});
});

describe('change payloads', () => {
	const g = buildGraph(inventory());

	it('adds a middleware at the chosen position and leaves the rest of the route untouched', () => {
		const c = connection(g, ['middleware', 'gzip'], ['route', 'app']);
		expect(buildChange(c, 'add', { order: ['gzip', 'secure', 'ghost'] })).toEqual({
			endpoint: 'routers', name: 'app', version: 3,
			input: { entry_points: ['websecure'], domains: ['app.example.com'], service: 'web', middlewares: ['gzip', 'secure', 'ghost'], tls: { enabled: true } }
		});
		expect((buildChange(c, 'add').input as any).middlewares).toEqual(['secure', 'ghost', 'gzip']);
	});

	it('removes a reference whatever its spelling', () => {
		const edge = g.edges.find((e) => e.subject === 'route:shop' && e.relation === 'route-middleware')!;
		const result = canDisconnect(g, edge);
		expect(result.ok && buildChange(result.connection, 'remove').input).toEqual({ entry_points: ['websecure'], domain_name: 'main', subdomain: 'shop', service: 'api' });
		const member = g.edges.find((e) => e.subject === 'chain:secure' && e.object === 'middleware:gzip')!;
		const chain = canDisconnect(g, member);
		expect(chain.ok && buildChange(chain.connection, 'remove')).toEqual({ endpoint: 'chains', name: 'secure', version: 3, input: { middlewares: ['auth'] } });
	});

	it('switches a route to a managed domain and a domain to another certificate source', () => {
		const bind = buildChange(connection(g, ['domain', 'main'], ['route', 'app']), 'add', { subdomain: 'app', pathPrefix: '/v1' });
		expect(bind.input).toEqual({ entry_points: ['websecure'], domain_name: 'main', subdomain: 'app', path_prefix: '/v1', service: 'web', middlewares: ['secure', 'ghost'] });
		expect(buildChange(connection(g, ['certificate', 'wild'], ['domain', 'main']), 'add')).toEqual({
			endpoint: 'domains', name: 'main', version: 3, input: { main: '*.example.com', sans: [], method: 'files', certificate: 'wild' }
		});
		expect((buildChange(connection(g, ['service', 'api'], ['route', 'app']), 'add').input as any).service).toBe('api');
		expect((buildChange(connection(g, ['resolver', 'le'], ['route', 'app']), 'add').input as any).tls).toEqual({ enabled: true, cert_resolver: 'le' });
	});
});

/* ------------------------------------------------------------------ page */

interface Call { method: string; path: string; headers: Record<string, string>; body?: any }
let calls: Call[] = [];
let handlers: Record<string, () => Response | Promise<Response>> = {};
const json = (body: unknown, status = 200) => new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } });

beforeEach(() => {
	localStorage.removeItem('tc_prefs_map_hidden');
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

const api = (inv = inventory()) => ({
	'GET /api/v1/routers': () => json(inv.routers),
	'GET /api/v1/services': () => json(inv.services),
	'GET /api/v1/middlewares': () => json(inv.middlewares),
	'GET /api/v1/chains': () => json(inv.chains),
	'GET /api/v1/domains': () => json(inv.domains),
	'GET /api/v1/resolvers': () => json(inv.resolvers),
	'GET /api/v1/certificates': () => json(inv.certificates),
	'GET /api/v1/entrypoints': () => json({ entry_points: inv.entryPoints })
});
const mapNode = (label: RegExp) => screen.findByRole('button', { name: label });

describe('filterOwnership', () => {
	it('keeps one side, entrypoints, and only the links between kept objects', () => {
		const g = buildGraph(inventory());
		const external = filterOwnership(g, 'external');
		expect(external.nodes.every((n) => n.kind === 'entrypoint' || n.external)).toBe(true);
		expect(external.nodes.some((n) => n.name === 'legacy@docker')).toBe(true);
		expect(external.edges.every((e) => external.byId.has(e.from) && external.byId.has(e.to))).toBe(true);
		const managed = filterOwnership(g, 'managed');
		expect(managed.nodes.some((n) => n.external)).toBe(false);
		expect(managed.nodes.some((n) => n.name === 'app')).toBe(true);
	});
});

describe('TopologyPage', () => {
	it('draws every object and flags broken references', async () => {
		handlers = api();
		render(TopologyPage, { navigate: () => {} });
		expect(await mapNode(/^Router app$/)).toBeTruthy();
		for (const label of [/^Entrypoint websecure$/, /^Domain main$/, /^Resolver le-dns$/, /^Chain secure$/, /^Middleware auth$/, /^Service api$/]) expect(await mapNode(label)).toBeTruthy();
		expect((await mapNode(/^Middleware ghost$/)).classList.contains('missing')).toBe(true);
		expect(document.querySelectorAll('g.edge').length).toBeGreaterThan(10);
	});

	it('remembers the kinds switched off for the next visit', async () => {
		handlers = api();
		const first = render(TopologyPage, { navigate: () => {} });
		expect(await mapNode(/^Middleware auth$/)).toBeTruthy();
		await fireEvent.click(screen.getByRole('button', { name: /^Middlewares/ }));
		expect(JSON.parse(localStorage.getItem('tc_prefs_map_hidden')!)).toEqual(['middleware']);
		first.unmount();
		render(TopologyPage, { navigate: () => {} });
		expect(await mapNode(/^Router app$/)).toBeTruthy();
		expect(screen.queryByRole('button', { name: /^Middleware auth$/ })).toBeNull();
		expect(screen.getByRole('button', { name: /^Middlewares/ }).getAttribute('aria-pressed')).toBe('false');
		await fireEvent.click(screen.getByRole('button', { name: /^Middlewares/ }));
		expect(JSON.parse(localStorage.getItem('tc_prefs_map_hidden')!)).toEqual([]);
	});

	it('shows only managed or only external objects, and remembers the choice', async () => {
		localStorage.removeItem('tc_prefs_map_ownership');
		handlers = api();
		const first = render(TopologyPage, { navigate: () => {} });
		expect(await mapNode(/^Router app$/)).toBeTruthy();
		expect(await mapNode(/^Router legacy@docker$/)).toBeTruthy();
		await fireEvent.click(screen.getByRole('button', { name: 'External' }));
		await waitFor(() => expect(screen.queryByRole('button', { name: /^Router app$/ })).toBeNull());
		expect(await mapNode(/^Router legacy@docker$/)).toBeTruthy();
		expect(await mapNode(/^Entrypoint web$/)).toBeTruthy(); // entrypoints always stay
		expect(localStorage.getItem('tc_prefs_map_ownership')).toBe('external');
		first.unmount();
		render(TopologyPage, { navigate: () => {} });
		expect(await mapNode(/^Router legacy@docker$/)).toBeTruthy();
		expect(screen.queryByRole('button', { name: /^Router app$/ })).toBeNull();
		await fireEvent.click(screen.getByRole('button', { name: 'Managed' }));
		expect(await mapNode(/^Router app$/)).toBeTruthy();
		await waitFor(() => expect(screen.queryByRole('button', { name: /^Router legacy@docker$/ })).toBeNull());
		await fireEvent.click(screen.getByRole('button', { name: 'All' }));
		expect(await mapNode(/^Router legacy@docker$/)).toBeTruthy();
		localStorage.removeItem('tc_prefs_map_ownership');
	});

	it('still draws the map when one list fails', async () => {
		handlers = { ...api(), 'GET /api/v1/certificates': () => json({ code: 'ERR_INTERNAL', message: 'boom' }, 500) };
		render(TopologyPage, { navigate: () => {} });
		expect(await mapNode(/^Router app$/)).toBeTruthy();
		expect(await screen.findByText(/could not be loaded.*Certificates/)).toBeTruthy();
	});

	it('hides a kind from the legend and lists the links of the selected object', async () => {
		handlers = api();
		render(TopologyPage, { navigate: () => {} });
		await fireEvent.click(await mapNode(/^Router app$/));
		const panel = await screen.findByRole('complementary', { name: 'Details' });
		expect(within(panel).getByText('Host(`app.example.com`)')).toBeTruthy();
		expect(within(panel).getByRole('button', { name: /secure.*Position 1/ })).toBeTruthy();
		expect(within(panel).getByRole('button', { name: 'Remove link: secure' })).toBeTruthy();
		expect(within(panel).queryByRole('button', { name: 'Remove link: web' })).toBeNull(); // the service is required

		await fireEvent.click(screen.getByRole('button', { name: /^Middlewares/ }));
		await waitFor(() => expect(screen.queryByRole('button', { name: /^Middleware auth$/ })).toBeNull());
	});

	it('links two objects through preview and apply', async () => {
		const applied = { id: 9, kind: 'router.update', target: 'app', status: 'succeeded', summary: 'Applied 1 change', requires_restart: false, created_at: 'now' };
		handlers = {
			...api(),
			'PUT /api/v1/routers/app?dry_run=true': () => json({ preview: { changes: [{ action: 'updated', id: 'router:app', kind: 'router', name: 'app', scope: 'dynamic' }], static_diff: '', dynamic_diff: '+        - gzip', requires_restart: false }, restart_required: false }),
			'PUT /api/v1/routers/app': () => json({ operation: applied, resource: router('app'), restart_required: false })
		};
		render(TopologyPage, { navigate: () => {} });
		// Keyboard / touch path of the same gesture: "Link to…" then choose the other object.
		await fireEvent.click(await mapNode(/^Middleware gzip$/));
		await fireEvent.click(await screen.findByRole('button', { name: 'Link to…' }));
		expect((await mapNode(/^Router app$/)).classList.contains('can')).toBe(true);
		expect((await mapNode(/^Service web$/)).classList.contains('can')).toBe(false);
		await fireEvent.click(await mapNode(/^Router app$/));

		const dialog = await waitFor(() => document.querySelector<HTMLElement>('dialog.resource-dialog')!);
		expect(within(dialog).getByRole('heading', { name: 'Attach to router' })).toBeTruthy();
		// The new middleware starts last; move it to the front.
		await fireEvent.click(within(dialog).getByRole('button', { name: 'Move up gzip' }));
		await fireEvent.click(within(dialog).getByRole('button', { name: 'Move up gzip' }));
		await fireEvent.click(within(dialog).getByRole('button', { name: 'Review change' }));
		const dry = await waitFor(() => calls.find((c) => c.path === '/api/v1/routers/app?dry_run=true')!);
		expect(dry.headers['if-match']).toBe('"3"');
		expect(dry.body.middlewares).toEqual(['gzip', 'secure', 'ghost']);

		await fireEvent.click(await within(dialog).findByRole('button', { name: 'Apply changes' }));
		const put = await waitFor(() => calls.find((c) => c.method === 'PUT' && c.path === '/api/v1/routers/app')!);
		expect(put.headers['idempotency-key']).toBeTruthy();
		expect(put.body).toEqual(dry.body);
		await waitFor(() => expect(document.querySelector('dialog.resource-dialog')).toBeNull());
		expect(flashes.at(-1)?.tone).toBe('success');
		// The map is read again after the write.
		await waitFor(() => expect(calls.filter((c) => c.path === '/api/v1/routers' && c.method === 'GET').length).toBeGreaterThan(1));
	});

	it('explains why two objects cannot be linked', async () => {
		handlers = api();
		render(TopologyPage, { navigate: () => {} });
		await fireEvent.click(await mapNode(/^Service api$/));
		await fireEvent.click(await screen.findByRole('button', { name: 'Link to…' }));
		await fireEvent.click(await mapNode(/^Middleware auth$/));
		expect(document.querySelector('dialog.resource-dialog')).toBeNull();
		expect(flashes.at(-1)?.message).toBe('A Service cannot be linked to a Middleware.');
	});
});

import { describe, expect, it } from 'vitest';
import { buildChange, buildGraph, canConnect, nodeId, type Inventory } from '../src/features/topology/graph';

const envelope = (kind: string, name: string, config: unknown, extra = {}) =>
	({ id: `${kind}:${name}`, name, version: 2, origin: 'managed', provider: 'file', status: 'active', read_only: false, live_health: 'loaded', config, ...extra }) as any;

/** One object of each protocol, plus the entrypoints they may listen on. */
const inventory = (): Inventory => ({
	entryPoints: ['websecure', 'postgres', 'dns'],
	entryPointProtocols: { websecure: 'tcp', postgres: 'tcp', dns: 'udp' },
	entryPointResources: [
		envelope('entrypoint', 'websecure', { kind: 'https', protocol: 'tcp', port: 443, http: { tls: {} } }),
		envelope('entrypoint', 'dns', { kind: 'udp', protocol: 'udp', port: 53, address: ':53/udp' }, { origin: 'external', read_only: true })
	],
	resolvers: [envelope('resolver', 'le', { type: 'acme', challenge: 'tlsalpn01' })],
	certificates: [],
	domains: [],
	services: [
		envelope('service', 'web', { servers: [{ url: 'https://app:8443' }] }),
		envelope('service', 'db', { protocol: 'tcp', servers: [{ address: 'db:5432' }, { address: 'db2:5432' }] }),
		envelope('service', 'resolver', { protocol: 'udp', servers: [{ address: '10.0.0.53:53' }] })
	],
	middlewares: [envelope('middleware', 'auth', { type: 'basicAuth', spec: {} }), envelope('middleware', 'office', { protocol: 'tcp', type: 'ipAllowList', spec: {} })],
	chains: [envelope('chain', 'secure', { middlewares: ['auth'] })],
	routers: [
		envelope('router', 'app', { entry_points: ['websecure'], rule: 'Host(`app.example.com`)', service: 'web', tls: {} }),
		envelope('router', 'pg', { protocol: 'tcp', entry_points: ['postgres'], rule: 'HostSNI(`db.example.com`)', service: 'db', middlewares: ['office'], tls: {} }),
		envelope('router', 'names', { protocol: 'udp', entry_points: ['dns'], service: 'resolver' })
	]
});
const g = buildGraph(inventory());
const node = (kind: any, name: string) => g.byId.get(nodeId(kind, name))!;
const connect = (a: [any, string], b: [any, string]) => canConnect(g, node(...a), node(...b));
const has = (relation: string, subject: string, object: string) => g.edges.some((e) => e.relation === relation && e.subject === subject && e.object === object);

describe('the map shows TCP and UDP objects next to HTTP ones', () => {
	it('adds routers, services and middlewares of every protocol, marked with their protocol', () => {
		expect(node('route', 'pg')).toMatchObject({ transport: 'tcp', protocol: 'TCP', subtitle: 'db.example.com', tls: true });
		expect(node('route', 'names')).toMatchObject({ transport: 'udp', protocol: 'UDP', subtitle: 'dns', tls: false });
		expect(node('route', 'app')).toMatchObject({ transport: 'http', protocol: '' });
		expect(node('service', 'db')).toMatchObject({ transport: 'tcp', protocol: 'TCP', subtitle: 'db:5432', count: 2 });
		expect(node('service', 'resolver')).toMatchObject({ transport: 'udp', protocol: 'UDP', subtitle: '10.0.0.53:53' });
		expect(node('service', 'web')).toMatchObject({ transport: 'http', protocol: 'HTTPS' });
		expect(node('middleware', 'office')).toMatchObject({ transport: 'tcp', protocol: 'TCP' });
	});

	it('links them like HTTP ones', () => {
		expect(has('route-service', 'route:pg', 'service:db')).toBe(true);
		expect(has('route-middleware', 'route:pg', 'middleware:office')).toBe(true);
		expect(has('route-entrypoint', 'route:pg', 'entrypoint:postgres')).toBe(true);
		expect(has('route-service', 'route:names', 'service:resolver')).toBe(true);
		expect(has('route-entrypoint', 'route:names', 'entrypoint:dns')).toBe(true);
	});

	it('describes entrypoints from their configuration and keeps the ones known by name only', () => {
		expect(node('entrypoint', 'websecure')).toMatchObject({ subtitle: ':443', transport: 'tcp', protocol: '', tls: true, readOnly: false });
		expect(node('entrypoint', 'dns')).toMatchObject({ subtitle: ':53', transport: 'udp', protocol: 'UDP', readOnly: true, external: true });
		expect(node('entrypoint', 'postgres')).toMatchObject({ subtitle: '', transport: 'tcp', readOnly: true });
	});
});

describe('links stay inside one protocol', () => {
	const refused = (a: [any, string], b: [any, string]) => {
		const result = connect(a, b);
		return !result.ok && result.error;
	};

	it('refuses a service, middleware or entrypoint of another protocol', () => {
		expect(refused(['route', 'app'], ['service', 'db'])).toBe('protocol');
		expect(refused(['route', 'pg'], ['service', 'web'])).toBe('protocol');
		expect(refused(['route', 'names'], ['service', 'db'])).toBe('protocol');
		expect(refused(['route', 'app'], ['middleware', 'office'])).toBe('protocol');
		expect(refused(['route', 'pg'], ['middleware', 'auth'])).toBe('protocol');
		expect(refused(['route', 'pg'], ['chain', 'secure'])).toBe('protocol');
		expect(refused(['chain', 'secure'], ['middleware', 'office'])).toBe('protocol');
		expect(refused(['route', 'app'], ['entrypoint', 'dns'])).toBe('protocol');
		expect(refused(['route', 'names'], ['entrypoint', 'websecure'])).toBe('protocol');
		// UDP has neither middlewares nor TLS.
		expect(refused(['route', 'names'], ['resolver', 'le'])).toBe('protocol');
	});

	it('allows the same protocol and writes the router of that protocol', () => {
		const tcp = connect(['route', 'pg'], ['entrypoint', 'websecure']);
		expect(tcp.ok).toBe(true);
		if (tcp.ok) {
			const change = buildChange(tcp.connection, 'add');
			expect(change).toMatchObject({ endpoint: 'routers', name: 'pg', input: { protocol: 'tcp', entry_points: ['postgres', 'websecure'], service: 'db' } });
		}
		expect(connect(['route', 'pg'], ['resolver', 'le']).ok).toBe(true);
		const udp = connect(['service', 'resolver'], ['route', 'names']);
		expect(!udp.ok && udp.error).toBe('already');
	});
});

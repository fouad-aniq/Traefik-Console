/**
 * Service contracts for core-v2.
 * - GET /api/v1/services → Resource<ServiceConfig>[] (HTTP, TCP and UDP; managed + external, external are read-only),
 *   each with its `protocol` and `used_by` (the routers of that protocol that send traffic to it) computed by the server
 * - PUT /api/v1/services/{name}  body {protocol, servers[{url}|{address}], …protocol options}
 * - GET /api/v1/services/{name}/diagnostics?protocol= → ServiceDiagnostic (camelCase, bounded runtime snapshot)
 */
import type { Resource } from '../../core/types';

export type ServiceProtocol = 'http' | 'tcp' | 'udp';
export const PROTOCOLS: ServiceProtocol[] = ['http', 'tcp', 'udp'];
/** Each protocol is its own tab and route base, so a service named "tcp" never collides with a tab. */
export const BASE: Record<ServiceProtocol, string> = { http: '/services', tcp: '/tcp-services', udp: '/udp-services' };

/** An HTTP backend has a URL; a TCP or UDP backend has a host:port address. */
export interface ServerEndpoint {
	url?: string;
	address?: string;
}
export interface ServiceHealthCheck {
	path: string;
	interval?: string;
	timeout?: string;
}
export interface TCPHealthCheck {
	interval?: string;
	timeout?: string;
	port?: number;
	send?: string;
	expect?: string;
}
export interface TCPOptions {
	tls?: boolean;
	server_name?: string;
	proxy_protocol?: number;
	dial_timeout?: string;
	termination_delay?: string;
	health_check?: TCPHealthCheck | null;
}
export interface ServiceConfig {
	protocol?: ServiceProtocol;
	pass_host_header?: boolean | null;
	servers: ServerEndpoint[] | null;
	health_check?: ServiceHealthCheck | null;
	insecure_skip_verify?: boolean;
	tcp?: TCPOptions | null;
	/** Computed by the server on read (never stored); absent when usage could not be read. */
	used_by?: ServiceUsage;
}
/** Only what the "Used by" dialog shows about each router. */
export interface ServiceUsage {
	routers: {
		name: string;
		rule?: string;
		entry_points?: string[] | null;
		service?: string;
		tls: boolean;
		read_only: boolean;
	}[];
}
export type ServiceResource = Resource<ServiceConfig>;

export interface ServiceInput {
	protocol: ServiceProtocol;
	pass_host_header?: boolean;
	servers: ServerEndpoint[];
	health_check?: ServiceHealthCheck;
	insecure_skip_verify?: boolean;
	tcp?: TCPOptions;
}

export interface ServiceDiagnostic {
	protocol: string;
	name: string;
	observed: boolean;
	runtimeStatus: string;
	healthStatus: 'healthy' | 'unhealthy' | 'mixed' | 'unknown' | string;
	serverStatus?: Record<string, number>;
	routers: { name: string; status: string; entryPoints?: string[] }[] | null;
	routersTruncated?: boolean;
	observedAt: string;
}

export interface ServiceForm {
	name: string;
	protocol: ServiceProtocol;
	passHost: 'default' | 'yes' | 'no';
	/** Whole values: a URL for HTTP, host:port for TCP/UDP. */
	servers: string[];
	healthEnabled: boolean;
	healthPath: string;
	healthInterval: string;
	healthTimeout: string;
	insecureSkipVerify: boolean;
	// TCP only
	tls: boolean;
	serverName: string;
	proxyProtocol: '0' | '1' | '2';
	dialTimeout: string;
	terminationDelay: string;
	healthPort: string;
	healthSend: string;
	healthExpect: string;
}

export const NAME_PATTERN = /^[a-z][a-z0-9_-]{0,62}$/;
export const MAX_SERVERS = 64;
export const SCHEMES = ['http', 'https', 'h2c'] as const;

export const protocolOf = (c: ServiceConfig | null | undefined): ServiceProtocol =>
	(c?.protocol ?? 'http') as ServiceProtocol;

export const newForm = (protocol: ServiceProtocol = 'http'): ServiceForm => ({
	name: '',
	protocol,
	passHost: 'default',
	servers: [''],
	healthEnabled: false,
	healthPath: '/',
	healthInterval: '',
	healthTimeout: '',
	insecureSkipVerify: false,
	tls: false,
	serverName: '',
	proxyProtocol: '0',
	dialTimeout: '',
	terminationDelay: '',
	healthPort: '',
	healthSend: '',
	healthExpect: '',
});

export function formFrom(name: string, c: ServiceConfig): ServiceForm {
	const protocol = protocolOf(c);
	const servers = serversOf(c);
	const t = c.tcp ?? {};
	const th = t.health_check;
	return {
		...newForm(protocol),
		name,
		passHost: c.pass_host_header == null ? 'default' : c.pass_host_header ? 'yes' : 'no',
		servers: servers.length ? servers : [''],
		healthEnabled: protocol === 'tcp' ? !!th : !!c.health_check,
		healthPath: c.health_check?.path || '/',
		healthInterval: (protocol === 'tcp' ? th?.interval : c.health_check?.interval) ?? '',
		healthTimeout: (protocol === 'tcp' ? th?.timeout : c.health_check?.timeout) ?? '',
		insecureSkipVerify: !!c.insecure_skip_verify,
		tls: !!t.tls,
		serverName: t.server_name ?? '',
		proxyProtocol: String(t.proxy_protocol ?? 0) as ServiceForm['proxyProtocol'],
		dialTimeout: t.dial_timeout ?? '',
		terminationDelay: t.termination_delay ?? '',
		healthPort: th?.port ? String(th.port) : '',
		healthSend: th?.send ?? '',
		healthExpect: th?.expect ?? '',
	};
}

const set = <T extends Record<string, unknown>>(o: T) =>
	Object.fromEntries(
		Object.entries(o).filter(([, v]) => v !== '' && v !== undefined && v !== false && v !== 0),
	) as Partial<T>;

/** Strict payload: optional blocks are omitted when unset (PUT replaces the whole definition). */
export function toInput(f: ServiceForm): ServiceInput {
	if (f.protocol === 'udp') return { protocol: 'udp', servers: f.servers.map((a) => ({ address: a.trim() })) };
	if (f.protocol === 'tcp') {
		const health = f.healthEnabled
			? set({
					interval: f.healthInterval.trim(),
					timeout: f.healthTimeout.trim(),
					port: f.healthPort.trim() ? Number(f.healthPort) : 0,
					send: f.healthSend,
					expect: f.healthSend ? f.healthExpect : '',
				})
			: undefined;
		const tcp: TCPOptions = {
			...set({
				tls: f.tls,
				server_name: f.tls ? f.serverName.trim() : '',
				proxy_protocol: Number(f.proxyProtocol),
				dial_timeout: f.dialTimeout.trim(),
				termination_delay: f.terminationDelay.trim(),
			}),
			...(health ? { health_check: health } : {}),
		};
		return {
			protocol: 'tcp',
			servers: f.servers.map((a) => ({ address: a.trim() })),
			...(f.tls && f.insecureSkipVerify ? { insecure_skip_verify: true } : {}),
			...(Object.keys(tcp).length ? { tcp } : {}),
		};
	}
	return {
		protocol: 'http',
		servers: f.servers.map((u) => ({ url: u.trim() })),
		...(f.passHost === 'default' ? {} : { pass_host_header: f.passHost === 'yes' }),
		...(f.healthEnabled
			? {
					health_check: {
						path: f.healthPath.trim() || '/',
						...(f.healthInterval.trim() ? { interval: f.healthInterval.trim() } : {}),
						...(f.healthTimeout.trim() ? { timeout: f.healthTimeout.trim() } : {}),
					},
				}
			: {}),
		...(f.insecureSkipVerify ? { insecure_skip_verify: true } : {}),
	};
}

export const serversOf = (c: ServiceConfig | null | undefined) =>
	(c?.servers ?? []).map((s) => s.url ?? s.address ?? '').filter(Boolean);

/** The serversTransport the console creates next to a service, if its options need one. */
export function transportOf(
	name: string,
	c: ServiceConfig,
): { name: string; definition: Record<string, unknown> } | null {
	if (protocolOf(c) === 'http')
		return c.insecure_skip_verify
			? { name: `${name}-insecure-transport`, definition: { insecureSkipVerify: true } }
			: null;
	if (protocolOf(c) !== 'tcp' || !c.tcp) return null;
	const t = c.tcp;
	const tls = t.tls ? set({ serverName: t.server_name ?? '', insecureSkipVerify: !!c.insecure_skip_verify }) : {};
	const definition = {
		...(t.proxy_protocol ? { proxyProtocol: { version: t.proxy_protocol } } : {}),
		...set({ dialTimeout: t.dial_timeout ?? '', terminationDelay: t.termination_delay ?? '' }),
		...(Object.keys(tls).length ? { tls } : {}),
	};
	return Object.keys(definition).length ? { name: `${name}-tcp-transport`, definition } : null;
}

/**
 * The service as Traefik's dynamic configuration declares it (what the file provider is given),
 * mirroring core-v2's renderer: only values that were set are written.
 */
export function serviceDefinition(name: string, c: ServiceConfig): Record<string, unknown> {
	const servers = c.servers ?? [];
	const protocol = protocolOf(c);
	const transport = transportOf(name, c);
	if (protocol !== 'http') {
		if (!servers.length && !c.tcp) return {};
		const h = c.tcp?.health_check;
		return {
			loadBalancer: {
				servers: servers.map((s) => ({
					address: s.address,
					...(protocol === 'tcp' && c.tcp?.tls ? { tls: true } : {}),
				})),
				...(h
					? {
							healthCheck: set({
								interval: h.interval ?? '',
								timeout: h.timeout ?? '',
								port: h.port ?? 0,
								send: h.send ?? '',
								expect: h.expect ?? '',
							}),
						}
					: {}),
				...(transport ? { serversTransport: transport.name } : {}),
			},
		};
	}
	if (!servers.length && c.pass_host_header == null && !c.health_check) return {};
	const health = c.health_check
		? set({ path: c.health_check.path, interval: c.health_check.interval ?? '', timeout: c.health_check.timeout ?? '' })
		: undefined;
	return {
		loadBalancer: {
			servers: servers.map((s) => ({ url: s.url })),
			...(c.pass_host_header == null ? {} : { passHostHeader: c.pass_host_header }),
			...(health ? { healthCheck: health } : {}),
			...(transport ? { serversTransport: transport.name } : {}),
		},
	};
}

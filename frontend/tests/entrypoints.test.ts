import { describe, expect, it } from 'vitest';
import { formFrom, newForm, toInput, withKind } from '../src/features/entrypoints/api';
import {
	entryPointErrors,
	portConflict,
	validIPOrCIDR,
	type ValidationGroup,
} from '../src/features/entrypoints/validation';
import {
	displayAddress,
	kindOf,
	type EntryPointConfig,
	type EntryPointForm,
	type EntryPointResource,
} from '../src/features/entrypoints/models';

const resource = (
	name: string,
	config: Partial<EntryPointConfig> = {},
	extra: Partial<EntryPointResource> = {},
): EntryPointResource => ({
	id: 'entrypoint:' + name,
	name,
	version: 1,
	origin: 'managed',
	provider: 'static',
	status: 'active',
	read_only: false,
	live_health: 'unknown',
	config: { protocol: 'tcp', port: 80, ...config },
	...extra,
});
const form = (patch: Partial<EntryPointForm>): EntryPointForm => ({
	...newForm(),
	name: 'app',
	port: '8443',
	...patch,
});
const errors = (
	group: ValidationGroup,
	f: EntryPointForm,
	others: EntryPointResource[] = [],
	resolvers: string[] = [],
) =>
	Object.keys(
		entryPointErrors(group, { form: f, creating: true, others, resolvers, resolversKnown: true }, (key) => key),
	);

describe('toInput sends only what applies and differs from Traefik defaults', () => {
	it('keeps a plain entrypoint to protocol and port', () => {
		expect(toInput(form({}))).toEqual({ kind: 'http', protocol: 'tcp', port: 8443 });
	});

	it('drops every TCP-only option for a UDP entrypoint', () => {
		const input = toInput(
			form({
				kind: 'udp',
				protocol: 'udp',
				port: '53',
				udpTimeout: '10s',
				asDefault: true,
				certResolver: 'le',
				http3: true,
				forwardedMode: 'all',
				readTimeout: '5s',
			}),
		);
		expect(input).toEqual({ kind: 'udp', protocol: 'udp', port: 53, udp: { timeout: '10s' } });
	});

	it('keeps only the listener, PROXY protocol and timeouts for a TCP service', () => {
		const input = toInput(
			form({
				kind: 'tcp',
				port: '5432',
				asDefault: true,
				redirect: true,
				redirectTarget: 'websecure',
				allowAcmeBypass: true,
				certResolver: 'le',
				http3: true,
				middlewares: ['auth@file'],
				forwardedMode: 'all',
				maxHeaderBytes: '1',
				maxConcurrentStreams: '5',
				proxyMode: 'list',
				proxyIPs: ['10.0.0.1'],
				idleTimeout: '5m',
				metrics: false,
			}),
		);
		expect(input).toEqual({
			kind: 'tcp',
			protocol: 'tcp',
			port: 5432,
			as_default: true,
			proxy_protocol: { trusted_ips: ['10.0.0.1'] },
			transport: { idle_timeout: '5m' },
			observability: { disable_metrics: true },
		});
	});

	it('writes options that belong together, and nothing a hidden control still holds', () => {
		const input = toInput(
			form({
				kind: 'https',
				host: ' 192.0.2.1 ',
				asDefault: true,
				redirect: true,
				redirectTarget: 'websecure',
				redirectPort: '9999',
				redirectTemporary: true,
				certResolver: '',
				tlsDomains: [{ main: 'example.com', sans: [] }], // domains need a resolver
				http3: true,
				http3Port: '',
				forwardedMode: 'list',
				forwardedIPs: ['10.0.0.0/8'],
				proxyMode: 'none',
				proxyIPs: ['10.0.0.1'], // off: list ignored
				tracing: false,
				traceVerbosity: 'detailed', // verbosity is meaningless without tracing
				deniedCharacters: ['slash'],
				disableSanitizePath: true,
				maxHeaderBytes: '2097152',
				readTimeout: '30s',
				maxConcurrentStreams: '100',
			}),
		);
		expect(input).toEqual({
			kind: 'https',
			protocol: 'tcp',
			port: 8443,
			host: '192.0.2.1',
			as_default: true,
			http: {
				redirect: { to: 'websecure', temporary: true },
				tls: {},
				denied_encoded_characters: ['slash'],
				disable_sanitize_path: true,
				max_header_bytes: 2097152,
			},
			http3: {},
			forwarded_headers: { trusted_ips: ['10.0.0.0/8'] },
			transport: { read_timeout: '30s' },
			http2: { max_concurrent_streams: 100 },
			observability: { disable_tracing: true },
		});
	});

	it('redirects to a bare port, trusts everyone and adopts explicitly', () => {
		const input = toInput(
			form({
				redirect: true,
				redirectPort: '443',
				redirectScheme: 'http',
				forwardedMode: 'all',
				forwardedIPs: ['10.0.0.1'],
				proxyMode: 'all',
				kind: 'https',
				certResolver: 'le',
				tlsDomains: [
					{ main: ' example.com ', sans: ['*.example.com'] },
					{ main: '', sans: [] },
				],
			}),
			true,
		);
		expect(input.adopted).toBe(true);
		// The plain web type never sends entrypoint TLS, whatever the hidden TLS fields still hold.
		expect(toInput(form({ kind: 'http', certResolver: 'le', tlsOptions: 'modern@file' })).http).toBeUndefined();
		expect(input.http).toEqual({
			redirect: { to: ':443', scheme: 'http' },
			tls: { cert_resolver: 'le', domains: [{ main: 'example.com', sans: ['*.example.com'] }] },
		});
		expect(input.forwarded_headers).toEqual({ insecure: true });
		expect(input.proxy_protocol).toEqual({ insecure: true });
	});

	it('round-trips a stored entrypoint through the form', () => {
		const config: EntryPointConfig = {
			kind: 'https',
			protocol: 'tcp',
			port: 443,
			host: '192.0.2.1',
			as_default: true,
			reuse_port: true,
			allow_acme_bypass: true,
			forwarded_headers: { trusted_ips: ['10.0.0.0/8'], connection: ['Upgrade'], not_append_x_forwarded_for: true },
			proxy_protocol: { trusted_ips: ['10.0.0.1'] },
			transport: { read_timeout: '30s', grace_timeout: '15s', keep_alive_max_requests: 100 },
			http: {
				redirect: { to: ':8443', scheme: 'http', temporary: true, priority: 10 },
				middlewares: ['secure@file'],
				tls: {
					cert_resolver: 'le',
					options: 'modern@file',
					domains: [{ main: 'example.com', sans: ['*.example.com'] }],
				},
				denied_encoded_characters: ['slash'],
				encode_query_semicolons: true,
				disable_sanitize_path: true,
				max_header_bytes: 2097152,
				alias_headers_strategy: 'reject',
			},
			http2: { max_concurrent_streams: 100 },
			http3: { advertised_port: 8443 },
			observability: { disable_access_logs: true, trace_verbosity: 'detailed' },
		};
		expect(toInput(formFrom(resource('edge', config)))).toEqual(config);
	});
});

describe('the type drives the protocol and the usual companions', () => {
	it('fills the usual name and port while they are free, and never overwrites what was typed', () => {
		const secure = resource('websecure', { kind: 'https', port: 443, http: { tls: {} } });
		const web = withKind(newForm(), 'http', [secure], true);
		expect(web).toMatchObject({
			kind: 'http',
			protocol: 'tcp',
			name: 'web',
			port: '80',
			redirect: true,
			redirectTarget: 'websecure',
		});
		expect(withKind(newForm(), 'http', [resource('web', { port: 80 })], true)).toMatchObject({
			name: '',
			port: '',
			redirect: false,
		});
		expect(withKind(newForm(), 'https', [], true)).toMatchObject({
			kind: 'https',
			name: 'websecure',
			port: '443',
			asDefault: true,
		});
		expect(withKind(newForm(), 'https', [resource('other', { port: 8443, as_default: true })], true).asDefault).toBe(
			false,
		);
		// Switching type replaces a suggested name and port, drops the suggested redirect, keeps typed values.
		expect(withKind(web, 'udp', [secure], true)).toMatchObject({
			kind: 'udp',
			protocol: 'udp',
			name: '',
			port: '',
			redirect: false,
		});
		expect(withKind({ ...web, name: 'edge', port: '8080' }, 'tcp', [secure], true)).toMatchObject({
			kind: 'tcp',
			protocol: 'tcp',
			name: 'edge',
			port: '8080',
		});
	});

	it('only changes the type and protocol of an existing entrypoint', () => {
		const stored = formFrom(resource('edge', { kind: 'http', port: 8080, http: { redirect: { to: 'websecure' } } }));
		expect(withKind(stored, 'https', [], false)).toEqual({ ...stored, kind: 'https' });
	});

	it('reads the type the server states, or works it out', () => {
		expect(kindOf({ kind: 'tcp', port: 1 })).toBe('tcp');
		expect(kindOf({ port: 1, http: { tls: {} } })).toBe('https');
		expect(kindOf({ port: 1, protocol: 'udp' })).toBe('udp');
		expect(kindOf({ port: 1 })).toBe('http');
		expect(displayAddress({ port: 53, protocol: 'udp', address: ':53/udp' })).toBe(':53');
		expect(displayAddress({ port: 443, host: '::1' })).toBe('[::1]:443');
	});
});

describe('client-side checks mirror the server rules', () => {
	const others = [
		resource('web', { port: 80 }),
		resource('dns', { protocol: 'udp', port: 53 }),
		resource('quic', { port: 7443, http3: {} }),
		resource('local', { port: 9000, host: '127.0.0.1' }),
	];

	it('checks the listener: name, port, conflicts and bind address', () => {
		expect(errors('listener', form({ name: 'Bad Name' }), others)).toEqual(['listener.name']);
		expect(errors('listener', form({ name: 'web', port: '81' }), others)).toEqual(['listener.name']);
		expect(errors('listener', form({ port: '70000' }), others)).toEqual(['listener.port']);
		expect(errors('listener', form({ port: '80' }), others)).toEqual(['listener.port']);
		expect(errors('listener', form({ port: '80', reusePort: true }), others)).toEqual([]);
		expect(errors('listener', form({ port: '9000' }), others)).toEqual(['listener.port']);
		expect(errors('listener', form({ port: '9000', host: '127.0.0.2' }), others)).toEqual([]);
		expect(errors('listener', form({ kind: 'udp', protocol: 'udp', port: '7443' }), others)).toEqual(['listener.port']); // taken by HTTP/3
		expect(errors('listener', form({ port: '53' }), others)).toEqual([]); // TCP 53 next to UDP 53 is fine
		expect(errors('listener', form({ host: 'not a host' }), others)).toEqual(['listener.host']);
		expect(errors('listener', form({ kind: 'udp', protocol: 'udp', port: '5000', udpTimeout: '0s' }), others)).toEqual([
			'listener.udpTimeout',
		]);
		// The type and the protocol must agree: a web or TCP entrypoint never listens on UDP.
		for (const kind of ['http', 'https', 'tcp'] as const)
			expect(errors('listener', form({ kind, protocol: 'udp', port: '5000' }), others)).toEqual(['listener.protocol']);
		expect(errors('listener', form({ kind: 'udp', protocol: 'tcp', port: '5000' }), others)).toEqual([
			'listener.protocol',
		]);
	});

	it('checks options that depend on each other', () => {
		expect(errors('http', form({ port: '53', http3: true }), others)).toEqual(['http.http3']);
		expect(errors('http', form({ redirect: true }), others)).toEqual(['http.redirectTo']);
		expect(errors('http', form({ redirect: true, redirectTarget: 'dns' }), others)).toEqual(['http.redirectTo']); // UDP target
		expect(errors('http', form({ redirect: true, redirectPort: '8443' }), others)).toEqual(['http.redirectTo']); // own port
		expect(errors('http', form({ redirect: true, redirectTarget: 'web' }), others)).toEqual([]);
		expect(errors('http', form({ kind: 'https', certResolver: 'gone' }), others, ['le'])).toEqual([
			'http.certResolver',
		]);
		expect(
			errors(
				'http',
				form({ kind: 'https', certResolver: 'le', tlsDomains: [{ main: 'bad domain', sans: [] }] }),
				others,
				['le'],
			),
		).toEqual(['http.tlsDomains']);
		expect(errors('http', form({ middlewares: ['auth'] }), others)).toEqual(['http.middlewares']);
		expect(errors('trust', form({ forwardedMode: 'list' }), others)).toEqual(['trust.forwardedIPs']);
		expect(errors('trust', form({ proxyMode: 'list', proxyIPs: ['lb.internal'] }), others)).toEqual(['trust.proxyIPs']);
		expect(errors('advanced', form({ readTimeout: 'soon', maxHeaderBytes: '-1', idleTimeout: '90' }), others)).toEqual([
			'advanced.readTimeout',
			'advanced.maxHeaderBytes',
		]);
		// Options the type does not have are never sent, so they are never checked either.
		expect(errors('http', form({ kind: 'udp', protocol: 'udp', redirect: true }), others)).toEqual([]);
		expect(errors('http', form({ kind: 'tcp', redirect: true }), others)).toEqual([]);
		expect(errors('trust', form({ kind: 'tcp', forwardedMode: 'list' }), others)).toEqual([]);
		expect(errors('trust', form({ kind: 'tcp', proxyMode: 'list' }), others)).toEqual(['trust.proxyIPs']);
		expect(errors('advanced', form({ kind: 'tcp', maxHeaderBytes: '-1', readTimeout: 'soon' }), others)).toEqual([
			'advanced.readTimeout',
		]);
	});

	it('names the entrypoint in the way', () => {
		expect(
			portConflict({ form: form({ port: '80' }), creating: true, others, resolvers: [], resolversKnown: true }),
		).toEqual({ name: 'web', reason: 'port' });
		// The entrypoint being edited is not in conflict with itself.
		expect(
			portConflict({
				form: form({ name: 'web', port: '80' }),
				creating: false,
				others,
				resolvers: [],
				resolversKnown: true,
			}),
		).toBeNull();
	});

	it('accepts IPv4, IPv6 and CIDR ranges only', () => {
		for (const ok of ['10.0.0.1', '10.0.0.0/8', '::1', '2001:db8::/32', 'fd00::1'])
			expect(validIPOrCIDR(ok), ok).toBe(true);
		for (const bad of ['10.0.0.256', '10.0.0.0/33', 'example.com', '10.0.0.0/8/1', '', '2001:db8::/129'])
			expect(validIPOrCIDR(bad), bad).toBe(false);
	});
});

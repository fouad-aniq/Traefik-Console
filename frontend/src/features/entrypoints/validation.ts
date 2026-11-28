import { durationMilliseconds } from '../resolvers/validation';
import { isWeb, protocolsOf, type EntryPointForm, type EntryPointResource } from './models';

export type ValidationGroup = 'listener' | 'http' | 'trust' | 'advanced';

export const NAME_PATTERN = /^[a-z][a-z0-9_-]{0,62}$/;
const HOSTNAME = /^[A-Za-z0-9]([A-Za-z0-9.-]{0,251}[A-Za-z0-9])?$/;
const DOMAIN = /^(\*\.)?[A-Za-z0-9]([A-Za-z0-9.-]{0,251}[A-Za-z0-9])?$/;
const HEADER = /^[A-Za-z0-9!#$%&'*+.^_`|~-]{1,128}$/;
const MIDDLEWARE = /^[^\s@]{1,128}@[A-Za-z0-9_-]{1,64}$/;
const TLS_OPTIONS = /^[^\s@]{1,128}(@[A-Za-z0-9_-]{1,64})?$/;

export const validPort = (value: string) =>
	/^\d{1,5}$/.test(value.trim()) && Number(value) >= 1 && Number(value) <= 65535;

function validIPv4(value: string) {
	const parts = value.split('.');
	return parts.length === 4 && parts.every((n) => /^(0|[1-9]\d{0,2})$/.test(n) && Number(n) <= 255);
}
function validIPv6(value: string) {
	if (!/^[0-9a-fA-F:.]+$/.test(value) || !value.includes(':')) return false;
	try {
		new URL(`http://[${value}]`);
		return true;
	} catch {
		return false;
	}
}
export const validIP = (value: string) => validIPv4(value) || validIPv6(value);

/** One IP address or CIDR range, v4 or v6. */
export function validIPOrCIDR(value: string): boolean {
	const [address, prefix, extra] = value.split('/');
	if (extra !== undefined) return false;
	if (prefix === undefined) return validIP(address);
	if (!/^\d{1,3}$/.test(prefix)) return false;
	return validIPv4(address) ? Number(prefix) <= 32 : validIPv6(address) && Number(prefix) <= 128;
}
export const validHeaderName = (value: string) => HEADER.test(value);
export const validMiddlewareReference = (value: string) => MIDDLEWARE.test(value);
export const validDomainName = (value: string) => DOMAIN.test(value);

/** Empty (Traefik default), a Go duration, or a bare number of seconds. At most 24h. */
function validDuration(value: string, allowZero: boolean): boolean {
	const v = value.trim();
	if (!v) return true;
	const ms = /^\d+$/.test(v) ? Number(v) * 1000 : durationMilliseconds(v);
	return ms !== null && ms <= 86_400_000 && (allowZero ? ms >= 0 : ms > 0);
}
const validCount = (value: string, max = 2_147_483_647) =>
	!value.trim() || (/^\d+$/.test(value.trim()) && Number(value) <= max);

export interface ValidationContext {
	form: EntryPointForm;
	creating: boolean;
	/** Every other entrypoint (the one being edited is excluded by name). */
	others: EntryPointResource[];
	resolvers: string[];
	/** false when the resolver list could not be loaded: defer to the server. */
	resolversKnown: boolean;
}

/** The entrypoint, if any, that already listens where this form wants to (same port and transport). */
export function portConflict(ctx: ValidationContext): { name: string; reason: 'port' | 'http3' | 'udpHttp3' } | null {
	const { form } = ctx;
	if (!validPort(form.port)) return null;
	const port = Number(form.port);
	const host = form.host.trim().toLowerCase();
	for (const other of ctx.others) {
		const c = other.config;
		if (other.name === form.name || c.port !== port) continue;
		const otherHost = (c.host ?? '').toLowerCase();
		if (host && otherHost && host !== otherHost) continue;
		const protocol = c.protocol ?? 'tcp';
		if (protocol === form.protocol && !form.reusePort) return { name: other.name, reason: 'port' };
		if (form.protocol === 'tcp' && form.http3 && protocol === 'udp') return { name: other.name, reason: 'http3' };
		if (form.protocol === 'udp' && protocol === 'tcp' && c.http3) return { name: other.name, reason: 'udpHttp3' };
	}
	return null;
}

/**
 * Client-side mirror of the core-v2 entrypoint checks, keyed `group.field`. The server stays
 * authoritative; this keeps obviously invalid input out of the review step.
 * `c` resolves a message from the entryPointUI.errors catalog.
 */
export function entryPointErrors(
	group: ValidationGroup,
	ctx: ValidationContext,
	c: (key: string, args?: Record<string, string | number>) => string,
): Record<string, string> {
	const { form: f } = ctx;
	const errors: Record<string, string> = {};
	const fail = (field: string, message: string, args?: Record<string, string | number>) => {
		errors[group + '.' + field] ??= c(message, args);
	};
	const tcp = f.protocol === 'tcp';
	const web = isWeb(f.kind);

	if (group === 'listener') {
		// The type and the protocol must agree: a web or TCP entrypoint never listens on UDP.
		if (!protocolsOf(f.kind).includes(f.protocol)) fail('protocol', f.kind === 'udp' ? 'protocolUdp' : 'protocolTcp');
		if (!NAME_PATTERN.test(f.name)) fail('name', 'name');
		else if (ctx.creating && ctx.others.some((e) => e.name === f.name)) fail('name', 'duplicateName');
		if (!validPort(f.port)) fail('port', 'port');
		else {
			const conflict = portConflict(ctx);
			if (conflict?.reason === 'port')
				fail('port', 'portTaken', { port: f.port, protocol: f.protocol, name: conflict.name });
			if (conflict?.reason === 'udpHttp3') fail('port', 'portTakenByHttp3', { port: f.port, name: conflict.name });
		}
		const host = f.host.trim();
		if (host && !validIP(host) && !HOSTNAME.test(host)) fail('host', 'host');
		if (!tcp && !validDuration(f.udpTimeout, false)) fail('udpTimeout', 'durationPositive');
	} else if (!tcp) {
		return errors; // a UDP entrypoint has nothing else
	} else if (group === 'http') {
		if (!web) return errors; // a TCP service has no HTTP behaviour
		if (f.redirect) {
			if (!f.redirectTarget) {
				if (!validPort(f.redirectPort)) fail('redirectTo', 'redirectTarget');
				else if (f.redirectPort.trim() === f.port.trim()) fail('redirectTo', 'redirectSelf');
			} else if (f.redirectTarget === f.name) fail('redirectTo', 'redirectSelf');
			else if (!ctx.others.some((e) => e.name === f.redirectTarget && (e.config.protocol ?? 'tcp') === 'tcp'))
				fail('redirectTo', 'redirectTarget');
			if (!validCount(f.redirectPriority)) fail('redirectPriority', 'count');
		}
		if (f.kind === 'https') {
			if (f.certResolver && ctx.resolversKnown && !ctx.resolvers.includes(f.certResolver))
				fail('certResolver', 'resolver');
			if (f.tlsOptions.trim() && !TLS_OPTIONS.test(f.tlsOptions.trim())) fail('tlsOptions', 'tlsOptions');
			if (
				f.certResolver &&
				f.tlsDomains.some(
					(d) =>
						(d.main.trim() ? !DOMAIN.test(d.main.trim()) : d.sans.length > 0) || d.sans.some((s) => !DOMAIN.test(s)),
				)
			)
				fail('tlsDomains', 'domains');
		}
		if (f.middlewares.length > 32 || f.middlewares.some((m) => !MIDDLEWARE.test(m))) fail('middlewares', 'middlewares');
		if (f.http3) {
			if (f.http3Port.trim() && !validPort(f.http3Port)) fail('http3Port', 'port');
			const conflict = portConflict(ctx);
			if (conflict?.reason === 'http3') fail('http3', 'http3PortTaken', { port: f.port, name: conflict.name });
		}
	} else if (group === 'trust') {
		if (
			web &&
			f.forwardedMode === 'list' &&
			(!f.forwardedIPs.length || f.forwardedIPs.length > 64 || f.forwardedIPs.some((ip) => !validIPOrCIDR(ip)))
		)
			fail('forwardedIPs', 'trustedIPs');
		if (web && (f.connectionHeaders.length > 32 || f.connectionHeaders.some((h) => !HEADER.test(h))))
			fail('connectionHeaders', 'headers');
		if (
			f.proxyMode === 'list' &&
			(!f.proxyIPs.length || f.proxyIPs.length > 64 || f.proxyIPs.some((ip) => !validIPOrCIDR(ip)))
		)
			fail('proxyIPs', 'trustedIPs');
	} else if (group === 'advanced') {
		for (const field of [
			'readTimeout',
			'writeTimeout',
			'idleTimeout',
			'graceTimeout',
			'requestAcceptGraceTimeout',
			'keepAliveMaxTime',
		] as const) {
			if (!validDuration(f[field], true)) fail(field, 'duration');
		}
		for (const field of [
			'keepAliveMaxRequests',
			'maxHeaderBytes',
			'maxConcurrentStreams',
			'maxDecoderHeaderTableSize',
			'maxEncoderHeaderTableSize',
		] as const) {
			if (field !== 'keepAliveMaxRequests' && !web) continue;
			if (!validCount(f[field])) fail(field, 'count');
		}
	}
	return errors;
}

/** Server field path (after `entrypoint:<name>.`) → form key, so a 422 lands on the right field. */
export const SERVER_FIELDS: Record<string, string> = {
	kind: 'listener.kind',
	protocol: 'listener.protocol',
	'http.tls': 'listener.kind',
	http: 'listener.kind',
	http2: 'listener.kind',
	forwarded_headers: 'listener.kind',
	allow_acme_bypass: 'listener.kind',
	port: 'listener.port',
	host: 'listener.host',
	'udp.timeout': 'listener.udpTimeout',
	as_default: 'listener.name',
	'http.redirect.to': 'http.redirectTo',
	'http.redirect.scheme': 'http.redirectTo',
	'http.redirect.priority': 'http.redirectPriority',
	'http.middlewares': 'http.middlewares',
	'http.tls.options': 'http.tlsOptions',
	'http.tls.cert_resolver': 'http.certResolver',
	'http.tls.domains': 'http.tlsDomains',
	http3: 'http.http3',
	'http3.advertised_port': 'http.http3Port',
	'forwarded_headers.trusted_ips': 'trust.forwardedIPs',
	'forwarded_headers.connection': 'trust.connectionHeaders',
	'proxy_protocol.trusted_ips': 'trust.proxyIPs',
	'transport.read_timeout': 'advanced.readTimeout',
	'transport.write_timeout': 'advanced.writeTimeout',
	'transport.idle_timeout': 'advanced.idleTimeout',
	'transport.grace_timeout': 'advanced.graceTimeout',
	'transport.request_accept_grace_timeout': 'advanced.requestAcceptGraceTimeout',
	'transport.keep_alive_max_time': 'advanced.keepAliveMaxTime',
	'transport.keep_alive_max_requests': 'advanced.keepAliveMaxRequests',
	'http.max_header_bytes': 'advanced.maxHeaderBytes',
	'http2.max_concurrent_streams': 'advanced.maxConcurrentStreams',
	'http2.max_decoder_header_table_size': 'advanced.maxDecoderHeaderTableSize',
	'http2.max_encoder_header_table_size': 'advanced.maxEncoderHeaderTableSize',
};

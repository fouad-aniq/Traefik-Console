import { MAX_SERVERS, NAME_PATTERN, SCHEMES, type ServiceForm } from './models';

export interface ServiceMessages {
	invalidName: string; nameTaken: string; invalidUrl: string; urlQuery: string; urlScheme: string;
	serverCount: string; duplicateUrl: string; invalidPath: string; invalidDuration: string;
	/** TCP/UDP (optional so older callers keep working). */
	invalidAddress?: string; invalidPort?: string; invalidServerName?: string; expectNeedsSend?: string; invalidPayload?: string;
}
export type ServiceErrors = Record<string, string>;

const validPort = (p: string) => /^\d{1,5}$/.test(p) && Number(p) >= 1 && Number(p) <= 65535 && String(Number(p)) === p;

/** Mirror of core-v2 url checks: http/https/h2c, host, no credentials, fragment or query. */
export function backendError(value: string, m: ServiceMessages): string {
	const v = value.trim();
	if (!v || v.length > 4096 || /\s/.test(v)) return m.invalidUrl;
	if (/^[a-z][a-z0-9+.-]*:\/\//i.test(v) && !/^(https?|h2c):\/\//i.test(v)) return m.urlScheme;
	// The port first: the URL parser rejects a bad port without saying which part is wrong.
	const port = /^[a-z0-9+.-]+:\/\/(?:\[[^\]]*\]|[^:/?#]*):([^/?#]*)/i.exec(v)?.[1];
	if (port !== undefined && !validPort(port)) return m.invalidPort ?? m.invalidUrl;
	let u: URL;
	try { u = new URL(v); } catch { return m.invalidUrl; }
	if (!SCHEMES.includes(u.protocol.slice(0, -1) as (typeof SCHEMES)[number])) return m.urlScheme;
	if (!u.hostname || u.username || u.password || u.hash || v.includes('#')) return m.invalidUrl;
	if (u.search || v.includes('?')) return m.urlQuery;
	return '';
}

/** Mirror of core-v2 address checks: host:port (IPv6 bracketed), port 1–65535, nothing else. */
export function addressError(value: string, m: ServiceMessages): string {
	const v = value.trim();
	const bad = m.invalidAddress ?? m.invalidUrl;
	if (!v || v.length > 261 || /[\s/?#@]/.test(v) || v.includes('://')) return bad;
	const match = /^(\[[^\]]+\]|[^:[\]]+):([^:]*)$/.exec(v);
	if (!match || !match[2]) return bad; // a missing port: ask for host and port
	if (!validPort(match[2])) return m.invalidPort ?? bad;
	return '';
}

const UNIT_MS: Record<string, number> = { ns: 1e-6, us: 1e-3, 'µs': 1e-3, ms: 1, s: 1000, m: 60000, h: 3600000 };
/** Go time.ParseDuration subset: positive and at most one hour (core-v2 limit). */
export function durationError(value: string): boolean {
	const v = value.trim();
	if (!v) return false;
	if (!/^(\d+(\.\d+)?(ns|us|µs|ms|s|m|h))+$/.test(v)) return true;
	let total = 0;
	for (const [, n, unit] of v.matchAll(/(\d+(?:\.\d+)?)(ns|us|µs|ms|s|m|h)/g)) total += Number(n) * UNIT_MS[unit];
	return !(total > 0 && total <= 3600000);
}

export function serviceErrors(f: ServiceForm, ctx: { creating: boolean; existingNames: string[] }, m: ServiceMessages): ServiceErrors {
	const e: ServiceErrors = {};
	if (!NAME_PATTERN.test(f.name)) e.name = m.invalidName;
	else if (ctx.creating && ctx.existingNames.includes(f.name)) e.name = m.nameTaken;
	const seen = new Set<string>();
	if (f.servers.length < 1 || f.servers.length > MAX_SERVERS) e.servers = m.serverCount;
	const check = f.protocol === 'http' ? backendError : addressError;
	f.servers.forEach((value, i) => {
		const msg = check(value, m);
		if (msg) e[`server.${i}`] = msg;
		else if (seen.has(value.trim())) e[`server.${i}`] = m.duplicateUrl;
		seen.add(value.trim());
	});
	if (f.protocol === 'udp') return e;
	if (f.healthEnabled) {
		if (f.protocol === 'http') {
			const p = f.healthPath.trim() || '/';
			if (!p.startsWith('/') || p.length > 2048 || /[\r\n\0]/.test(p)) e.healthPath = m.invalidPath;
		}
		if (durationError(f.healthInterval)) e.healthInterval = m.invalidDuration;
		if (durationError(f.healthTimeout)) e.healthTimeout = m.invalidDuration;
	}
	if (f.protocol === 'tcp') {
		if (f.tls && f.serverName.trim() && !/^[A-Za-z0-9*]([A-Za-z0-9.-]{0,251}[A-Za-z0-9])?$/.test(f.serverName.trim())) e.serverName = m.invalidServerName ?? m.invalidUrl;
		if (durationError(f.dialTimeout)) e.dialTimeout = m.invalidDuration;
		if (durationError(f.terminationDelay)) e.terminationDelay = m.invalidDuration;
		if (f.healthEnabled) {
			if (f.healthPort.trim() && !validPort(f.healthPort.trim())) e.healthPort = m.invalidPort ?? m.invalidUrl;
			if (f.healthSend.length > 1024 || f.healthExpect.length > 1024) e.healthSend = m.invalidPayload ?? m.invalidUrl;
			else if (f.healthExpect && !f.healthSend) e.healthExpect = m.expectNeedsSend ?? m.invalidUrl;
		}
	}
	return e;
}

import { normalizeDomain } from '../domains/models';
import { MAX_ENTRYPOINTS, MAX_HOSTS, MAX_MIDDLEWARES, NAME_PATTERN, isNoBackend, parseHosts, type RouteForm } from './models';

export type RouteField =
	| 'name' | 'domainName' | 'subdomain' | 'hosts' | 'pathPrefix' | 'customRule'
	| 'entryPoints' | 'service' | 'middlewares' | 'priority' | 'tlsOptions';

/** Which tab owns each field, so a failed submit can jump to the right place. */
export const FIELD_TAB: Record<RouteField, 'route' | 'traffic' | 'tls'> = {
	name: 'route', domainName: 'route', subdomain: 'route', hosts: 'route', pathPrefix: 'route', customRule: 'route',
	entryPoints: 'traffic', service: 'traffic', priority: 'traffic', middlewares: 'tls', tlsOptions: 'tls'
};

/** Messages are passed in so this module stays free of localization. */
export interface RouteMessages {
	invalidName: string; nameTaken: string; domainRequired: string; invalidSubdomain: string; invalidHosts: string;
	invalidPathPrefix: string; ruleRequired: string; tooManyEntryPoints: string; serviceRequired: string;
	/** Optional so older callers keep working. */
	noBackendNeedsMiddleware?: string;
	/** TCP: matching hostnames reads the SNI, which needs TLS. */
	hostsNeedTls?: string;
	/** TCP and UDP: the entrypoints are chosen, never defaulted. */
	entryPointsRequired?: string;
	duplicateMiddleware: string; tooManyMiddlewares: string; invalidPriority: string; invalidTlsOptions: string;
}
export interface RouteContext { creating: boolean; existingNames: string[]; domainNames: string[] | null }

const LABEL = /^[a-z0-9](?:[a-z0-9-]*[a-z0-9])?$/;
const OPTIONS_NAME = /^[A-Za-z0-9][A-Za-z0-9_.@-]{0,127}$/;

export function subdomainError(value: string): boolean {
	const v = value.trim().toLowerCase();
	if (!v) return false;
	return v.length > 190 || v.split('.').some((l) => !l || l.length > 63 || !LABEL.test(l));
}

export function routeErrors(form: RouteForm, ctx: RouteContext, m: RouteMessages): Partial<Record<RouteField, string>> {
	const e: Partial<Record<RouteField, string>> = {};
	if (!NAME_PATTERN.test(form.name)) e.name = m.invalidName;
	else if (ctx.creating && ctx.existingNames.includes(form.name)) e.name = m.nameTaken;

	const udp = form.protocol === 'udp';
	const tcp = form.protocol === 'tcp';

	if (!udp) {
		if (form.mode === 'domain') {
			if (!form.domainName || (ctx.domainNames && !ctx.domainNames.includes(form.domainName))) e.domainName = m.domainRequired;
			if (subdomainError(form.subdomain)) e.subdomain = m.invalidSubdomain;
		} else if (form.mode === 'host') {
			const hosts = parseHosts(form.hosts).map(normalizeDomain);
			if (!hosts.length || hosts.length > MAX_HOSTS || hosts.some((h) => !h || h.startsWith('*.')) || new Set(hosts).size !== hosts.length) e.hosts = m.invalidHosts;
			// Without TLS a TCP connection has no SNI to read: only "every connection" can match it.
			else if (tcp && !form.tlsEnabled) e.hosts = m.hostsNeedTls ?? m.invalidHosts;
		} else if (form.mode === 'rule') {
			const rule = form.customRule.trim();
			if (!rule || rule.length > 4096 || /[\x00-\x08\x0b\x0c\x0e-\x1f]/.test(rule)) e.customRule = m.ruleRequired;
		}
		const prefix = form.pathPrefix.trim();
		if (!tcp && (form.mode === 'domain' || form.mode === 'host') && prefix && (!prefix.startsWith('/') || prefix.length > 512 || /[`\\\s?#\x00-\x1f]/.test(prefix))) e.pathPrefix = m.invalidPathPrefix;
	}

	if (form.entryPoints.length > MAX_ENTRYPOINTS) e.entryPoints = m.tooManyEntryPoints;
	// A TCP or UDP router takes every connection of its entrypoints: they are always chosen explicitly,
	// so it never lands on the HTTP entrypoints by default.
	else if ((tcp || udp) && !form.entryPoints.length) e.entryPoints = m.entryPointsRequired ?? m.tooManyEntryPoints;
	const service = form.service.trim();
	// Without a backend, a middleware has to answer the request (a redirect, say): a router needs one or the other.
	if (!service) e.service = m.serviceRequired;
	else if (!udp && !tcp && isNoBackend(service) && !form.middlewares.length) e.service = m.noBackendNeedsMiddleware ?? m.serviceRequired;
	if (!udp) {
		if (form.middlewares.length > MAX_MIDDLEWARES) e.middlewares = m.tooManyMiddlewares;
		else if (new Set(form.middlewares).size !== form.middlewares.length) e.middlewares = m.duplicateMiddleware;
	}

	const priority = form.priority.trim();
	if (!udp && priority && (!/^-?\d+$/.test(priority) || !Number.isSafeInteger(Number(priority)))) e.priority = m.invalidPriority;
	if (!udp && form.mode !== 'domain' && form.tlsEnabled && !(tcp && form.tlsPassthrough) && form.tlsOptions.trim() && !OPTIONS_NAME.test(form.tlsOptions.trim())) e.tlsOptions = m.invalidTlsOptions;
	return e;
}

import { normalizeDomain } from '../domains/models';
import {
	MAX_ENTRYPOINTS,
	MAX_HOSTS,
	MAX_MIDDLEWARES,
	NAME_PATTERN,
	hostOn,
	isNoBackend,
	parseHosts,
	type RouteForm,
} from './models';

export type RouteField =
	| 'name'
	| 'domainName'
	| 'subdomain'
	| 'extraHosts'
	| 'hosts'
	| 'pathPrefix'
	| 'customRule'
	| 'entryPoints'
	| 'service'
	| 'middlewares'
	| 'priority'
	| 'tlsOptions';

/** Which tab owns each field, so a failed submit can jump to the right place. */
export const FIELD_TAB: Record<RouteField, 'route' | 'traffic' | 'tls'> = {
	name: 'route',
	domainName: 'route',
	subdomain: 'route',
	extraHosts: 'route',
	hosts: 'route',
	pathPrefix: 'route',
	customRule: 'route',
	entryPoints: 'traffic',
	service: 'traffic',
	priority: 'traffic',
	middlewares: 'tls',
	tlsOptions: 'tls',
};

/** Messages are passed in so this module stays free of localization. */
export interface RouteMessages {
	invalidName: string;
	nameTaken: string;
	domainRequired: string;
	invalidSubdomain: string;
	invalidHosts: string;
	invalidPathPrefix: string;
	ruleRequired: string;
	tooManyEntryPoints: string;
	serviceRequired: string;
	/** Optional so older callers keep working. */
	noBackendNeedsMiddleware?: string;
	/** TCP: matching hostnames reads the SNI, which needs TLS. */
	hostsNeedTls?: string;
	/** TCP and UDP: the entrypoints are chosen, never defaulted. */
	entryPointsRequired?: string;
	duplicateMiddleware: string;
	tooManyMiddlewares: string;
	invalidPriority: string;
	invalidTlsOptions: string;
	/** Extra hostnames: the same hostname twice. Optional so older callers keep working. */
	duplicateHost?: string;
	/** Extra hostnames: Traefik gives a router one certificate resolver. */
	resolverMismatch?: string;
}
export interface RouteContext {
	creating: boolean;
	existingNames: string[];
	domainNames: string[] | null;
	/** Main name of a managed domain, to find the same hostname listed twice. */
	mainOf?: (domain: string) => string | undefined;
	/** Resolver a managed domain gets its certificates from ('' for uploaded certificates). */
	resolverOf?: (domain: string) => string | undefined;
}

/** At most this many extra hostnames on one router (core-v2 accepts 50). */
export const MAX_EXTRA_HOSTS = 20;

/** One message per extra hostname row ('' when the row is fine). */
export function extraHostErrors(form: RouteForm, ctx: RouteContext, m: RouteMessages): string[] {
	const seen = new Set<string>();
	const main = ctx.mainOf?.(form.domainName);
	if (main) seen.add(hostOn(main, form.subdomain));
	// The first resolver met (main hostname first) is the router's; uploaded certificates mix freely.
	let resolver = ctx.resolverOf?.(form.domainName) || '';
	return form.extraHosts.map((h) => {
		if (!h.domainName || (ctx.domainNames && !ctx.domainNames.includes(h.domainName))) return m.domainRequired;
		if (subdomainError(h.subdomain)) return m.invalidSubdomain;
		const domainMain = ctx.mainOf?.(h.domainName);
		if (domainMain) {
			const host = hostOn(domainMain, h.subdomain);
			if (seen.has(host)) return m.duplicateHost ?? m.invalidHosts;
			seen.add(host);
		}
		const own = ctx.resolverOf?.(h.domainName) || '';
		if (own && resolver && own !== resolver) return m.resolverMismatch ?? m.domainRequired;
		if (own) resolver = resolver || own;
		return '';
	});
}

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
			if (!form.domainName || (ctx.domainNames && !ctx.domainNames.includes(form.domainName)))
				e.domainName = m.domainRequired;
			if (subdomainError(form.subdomain)) e.subdomain = m.invalidSubdomain;
			const extra = extraHostErrors(form, ctx, m).find(Boolean);
			if (extra) e.extraHosts = extra;
			else if (form.extraHosts.length > MAX_EXTRA_HOSTS) e.extraHosts = m.invalidHosts;
		} else if (form.mode === 'host') {
			const hosts = parseHosts(form.hosts).map(normalizeDomain);
			if (
				!hosts.length ||
				hosts.length > MAX_HOSTS ||
				hosts.some((h) => !h || h.startsWith('*.')) ||
				new Set(hosts).size !== hosts.length
			)
				e.hosts = m.invalidHosts;
			// Without TLS a TCP connection has no SNI to read: only "every connection" can match it.
			else if (tcp && !form.tlsEnabled) e.hosts = m.hostsNeedTls ?? m.invalidHosts;
		} else if (form.mode === 'rule') {
			const rule = form.customRule.trim();
			if (!rule || rule.length > 4096 || /[\x00-\x08\x0b\x0c\x0e-\x1f]/.test(rule)) e.customRule = m.ruleRequired;
		}
		const prefix = form.pathPrefix.trim();
		if (
			!tcp &&
			(form.mode === 'domain' || form.mode === 'host') &&
			prefix &&
			(!prefix.startsWith('/') || prefix.length > 512 || /[`\\\s?#\x00-\x1f]/.test(prefix))
		)
			e.pathPrefix = m.invalidPathPrefix;
	}

	if (form.entryPoints.length > MAX_ENTRYPOINTS) e.entryPoints = m.tooManyEntryPoints;
	// A TCP or UDP router takes every connection of its entrypoints: they are always chosen explicitly,
	// so it never lands on the HTTP entrypoints by default.
	else if ((tcp || udp) && !form.entryPoints.length) e.entryPoints = m.entryPointsRequired ?? m.tooManyEntryPoints;
	const service = form.service.trim();
	// Without a backend, a middleware has to answer the request (a redirect, say): a router needs one or the other.
	if (!service) e.service = m.serviceRequired;
	else if (!udp && !tcp && isNoBackend(service) && !form.middlewares.length)
		e.service = m.noBackendNeedsMiddleware ?? m.serviceRequired;
	if (!udp) {
		if (form.middlewares.length > MAX_MIDDLEWARES) e.middlewares = m.tooManyMiddlewares;
		else if (new Set(form.middlewares).size !== form.middlewares.length) e.middlewares = m.duplicateMiddleware;
	}

	const priority = form.priority.trim();
	if (!udp && priority && (!/^-?\d+$/.test(priority) || !Number.isSafeInteger(Number(priority))))
		e.priority = m.invalidPriority;
	if (
		!udp &&
		form.mode !== 'domain' &&
		form.tlsEnabled &&
		!(tcp && form.tlsPassthrough) &&
		form.tlsOptions.trim() &&
		!OPTIONS_NAME.test(form.tlsOptions.trim())
	)
		e.tlsOptions = m.invalidTlsOptions;
	return e;
}

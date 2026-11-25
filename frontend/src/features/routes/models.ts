/**
 * Router contracts for core-v2.
 * - GET /api/v1/routers → Resource<RouterConfig>[] (managed + external; external are read-only)
 * - PUT /api/v1/routers/{name} body RouterInput. The backend compiles `rule` from one of:
 *     domain_name (+ subdomain)  → host derived from a managed Domain, TLS taken from that Domain
 *     domains[]                  → explicit hostnames, TLS chosen on the router
 *     custom_rule                → a Traefik rule written by hand, TLS chosen on the router
 *   plus an optional path_prefix for the first two.
 * - entry_points may be empty: core-v2 then selects the default TCP entrypoints.
 */
import type { Resource } from '../../core/types';
import { normalizeDomain } from '../domains/models';

/** Traefik keeps HTTP, TCP and UDP routers in separate sections; a router never changes protocol. */
export type RouterProtocol = 'http' | 'tcp' | 'udp';
export const ROUTER_PROTOCOLS: RouterProtocol[] = ['http', 'tcp', 'udp'];
/** Each router opens under the base path of its own protocol, so the dialog knows which editor to show. */
export const ROUTER_BASE: Record<RouterProtocol, string> = { http: '/routers', tcp: '/tcp-routers', udp: '/udp-routers' };
export const routerProtocolOf = (c: { protocol?: string } | null | undefined): RouterProtocol => (c?.protocol === 'tcp' || c?.protocol === 'udp' ? c.protocol : 'http');

export interface RouterTLS {
	enabled?: boolean;
	/** TCP only: forward the TLS connection untouched (Traefik reads only the SNI). */
	passthrough?: boolean;
	cert_resolver?: string;
	options?: string;
	domains?: { main: string; sans?: string[] | null }[] | null;
}
export interface RouterConfig {
	/** http when absent. */
	protocol?: RouterProtocol;
	entry_points: string[] | null;
	domain_name?: string;
	subdomain?: string;
	domains: string[] | null;
	path_prefix?: string;
	custom_rule?: string;
	rule: string;
	service: string;
	middlewares?: string[] | null;
	priority?: number;
	tls?: RouterTLS | null;
}
export type RouterResource = Resource<RouterConfig>;

export interface RouterInput {
	/** Sent only for TCP and UDP routers: an HTTP router's payload is unchanged. */
	protocol?: 'tcp' | 'udp';
	entry_points: string[];
	domain_name?: string;
	subdomain?: string;
	domains?: string[];
	path_prefix?: string;
	custom_rule?: string;
	service: string;
	middlewares?: string[];
	priority?: number;
	tls?: { enabled: true; passthrough?: true; cert_resolver?: string; options?: string };
}

/** `any` (TCP only) matches every connection: HostSNI(`*`). */
export type MatchMode = 'domain' | 'host' | 'rule' | 'any';
export const MATCH_ANY = 'HostSNI(`*`)';

export interface RouteForm {
	protocol: RouterProtocol;
	name: string;
	mode: MatchMode;
	domainName: string;
	subdomain: string;
	/** One hostname per line (commas also accepted). */
	hosts: string;
	pathPrefix: string;
	customRule: string;
	entryPoints: string[];
	service: string;
	middlewares: string[];
	priority: string;
	tlsEnabled: boolean;
	/** TCP only. */
	tlsPassthrough: boolean;
	tlsResolver: string;
	tlsOptions: string;
}

export const NAME_PATTERN = /^[a-z][a-z0-9_-]{0,62}$/;
export const MAX_ENTRYPOINTS = 8;
export const MAX_MIDDLEWARES = 64;
export const MAX_HOSTS = 20;

export const newForm = (protocol: RouterProtocol = 'http'): RouteForm => ({
	protocol,
	name: '', mode: protocol === 'tcp' ? 'any' : 'domain', domainName: '', subdomain: '', hosts: '', pathPrefix: '', customRule: '',
	entryPoints: [], service: '', middlewares: [], priority: '',
	// A TCP router matching every connection is usually plain TCP; HTTP routers default to HTTPS.
	tlsEnabled: protocol === 'http', tlsPassthrough: false, tlsResolver: '', tlsOptions: ''
});

export const parseHosts = (text: string) => text.split(/[\n,]+/).map((s) => s.trim()).filter(Boolean);

export function formFrom(name: string, c: RouterConfig): RouteForm {
	const protocol = routerProtocolOf(c);
	const base = newForm(protocol);
	const custom = c.custom_rule || c.rule || '';
	const mode: MatchMode = protocol === 'tcp' && custom === MATCH_ANY && !c.domain_name ? 'any'
		: c.domain_name ? 'domain' : c.custom_rule ? 'rule' : (c.domains ?? []).length ? 'host' : 'rule';
	return {
		...base,
		name,
		mode,
		domainName: c.domain_name ?? '',
		subdomain: c.subdomain ?? '',
		hosts: mode === 'host' ? (c.domains ?? []).join('\n') : '',
		pathPrefix: c.path_prefix ?? '',
		customRule: mode === 'rule' ? custom : '',
		entryPoints: [...(c.entry_points ?? [])],
		service: c.service ?? '',
		middlewares: [...(c.middlewares ?? [])],
		priority: c.priority ? String(c.priority) : '',
		tlsEnabled: mode === 'domain' ? true : !!c.tls,
		tlsPassthrough: !!c.tls?.passthrough,
		tlsResolver: c.tls?.cert_resolver ?? '',
		tlsOptions: c.tls?.options ?? ''
	};
}

/**
 * A managed copy of a router Traefik already has (another provider or file): same match, entry points,
 * service, middlewares and TLS, with a priority one above the original so the copy takes over while
 * the original stays as a fallback. References are qualified with the source provider when needed.
 */
export function customizeForm(source: RouterResource): RouteForm {
	const c = source.config;
	const qualify = (ref: string) => (ref.includes('@') || source.provider === 'file' || !source.provider ? ref : `${ref}@${source.provider}`);
	const form = formFrom(source.name.split('@')[0] + '-custom', { ...c, service: c.service ? qualify(c.service) : c.service, middlewares: (c.middlewares ?? []).map(qualify) });
	const rule = c.custom_rule || c.rule || '';
	form.priority = String((c.priority || rule.length) + 1);
	return form;
}

/** Strict payload: only the fields of the chosen match mode are sent; unset optional values are omitted. */
export function toInput(form: RouteForm): RouterInput {
	const out: RouterInput = { entry_points: form.entryPoints.map((e) => e.trim()), service: form.service.trim() };
	// A UDP router has only entry points and a service.
	if (form.protocol === 'udp') return { protocol: 'udp', ...out };
	if (form.protocol === 'tcp') out.protocol = 'tcp';
	if (form.mode === 'domain') {
		out.domain_name = form.domainName;
		if (form.subdomain.trim()) out.subdomain = form.subdomain.trim().toLowerCase();
	} else if (form.mode === 'host') {
		out.domains = parseHosts(form.hosts).map((h) => normalizeDomain(h) ?? h);
	} else if (form.mode === 'any') {
		out.custom_rule = MATCH_ANY;
	} else {
		out.custom_rule = form.customRule.trim();
	}
	if (form.protocol === 'http' && (form.mode === 'domain' || form.mode === 'host') && form.pathPrefix.trim()) out.path_prefix = form.pathPrefix.trim();
	// HTTP and TCP routers both take middlewares (each from its own pool); UDP returned above.
	if (form.middlewares.length) out.middlewares = form.middlewares.map((m) => m.trim());
	const priority = form.priority.trim();
	if (priority && Number(priority) !== 0) out.priority = Number(priority);
	if (form.mode !== 'domain' && form.tlsEnabled) {
		out.tls = { enabled: true };
		if (form.protocol === 'tcp' && form.tlsPassthrough) {
			// A passed-through connection is not decrypted: there is no certificate to configure.
			out.tls.passthrough = true;
		} else {
			if (form.tlsResolver) out.tls.cert_resolver = form.tlsResolver;
			if (form.tlsOptions.trim()) out.tls.options = form.tlsOptions.trim();
		}
	}
	return out;
}

/** Hostnames the router will answer on, before the backend compiles them (for the live preview). */
export function previewHosts(form: RouteForm, domainMain: string | undefined): string[] {
	if (form.mode === 'domain') {
		if (!domainMain) return [];
		const base = domainMain.replace(/^\*\./, '');
		const sub = form.subdomain.trim().toLowerCase();
		return [sub ? `${sub}.${base}` : base];
	}
	if (form.mode === 'host') return parseHosts(form.hosts).map((h) => normalizeDomain(h) ?? h.toLowerCase());
	return [];
}

/** Client-side twin of core-v2 `CompileRouterRule` / `CompileTCPRule`, used only to preview the rule before the server compiles it. */
export function previewRule(form: RouteForm, domainMain: string | undefined): string {
	if (form.protocol === 'udp') return '';
	if (form.mode === 'any') return MATCH_ANY;
	if (form.mode === 'rule') return form.customRule.trim();
	const tcp = form.protocol === 'tcp';
	const hosts = previewHosts(form, domainMain).map((h) => (tcp ? 'HostSNI(`' : 'Host(`') + h + '`)');
	if (!hosts.length) return '';
	const expr = hosts.length > 1 ? '(' + hosts.join(' || ') + ')' : hosts[0];
	let prefix = form.pathPrefix.trim();
	if (tcp || !prefix) return expr;
	if (!prefix.startsWith('/')) prefix = '/' + prefix;
	return `${expr} && PathPrefix(\`${prefix}\`)`;
}

/**
 * Traefik needs a service on every router. A router that only redirects uses this built-in one;
 * the middleware attached to the router answers the request and the service is never reached.
 */
export const NOOP_SERVICE = 'noop@internal';
export const isNoBackend = (ref: string) => ref === NOOP_SERVICE;

/** `name@file` and `name` are the same managed object. */
export const referenceBase = (ref: string) => ref.replace(/@file$/, '');
export const isExternalRef = (ref: string) => ref.includes('@') && !ref.endsWith('@file') && !isNoBackend(ref);

/**
 * The router as Traefik declares it in the file provider (what `GET /routers` already carries,
 * written the way core-v2 renders it): only values that were set appear.
 */
export function routerDefinition(c: RouterConfig): Record<string, unknown> {
	const protocol = routerProtocolOf(c);
	const tls = c.tls
		? {
			...(c.tls.passthrough ? { passthrough: true } : {}),
			...(c.tls.cert_resolver ? { certResolver: c.tls.cert_resolver } : {}),
			...(c.tls.options ? { options: c.tls.options } : {}),
			...(c.tls.domains?.length ? { domains: c.tls.domains.map((d) => ({ main: d.main, ...(d.sans?.length ? { sans: d.sans } : {}) })) } : {})
		}
		: undefined;
	if (protocol === 'udp') {
		if (!c.service && !(c.entry_points ?? []).length) return {};
		return { ...((c.entry_points ?? []).length ? { entryPoints: c.entry_points } : {}), service: c.service ?? '' };
	}
	if (!c.rule && !c.service && !(c.entry_points ?? []).length) return {};
	return {
		...((c.entry_points ?? []).length ? { entryPoints: c.entry_points } : {}),
		rule: c.rule ?? '',
		service: c.service ?? '',
		...((c.middlewares ?? []).length ? { middlewares: c.middlewares } : {}),
		...(c.priority ? { priority: c.priority } : {}),
		...(tls ? { tls } : {})
	};
}

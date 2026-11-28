/**
 * Items for the "used by" dialogs: what references a resource, described well
 * enough to recognise it (its rule, hosts or type) before opening it.
 */
import { api } from '../api/api';
import { list } from '../../core/types';

export type RelatedKind = 'route' | 'chain' | 'middleware' | 'domain' | 'certificate' | 'resolver' | 'entrypoint';

export interface RelatedItem {
	name: string;
	kind: RelatedKind;
	/** one identifying line: a router rule, a domain's hostnames, a middleware type */
	summary?: string;
	/** short facts shown as chips */
	facts?: string[];
	/** view route; absent when the Console has no page for the item (e.g. a TCP router) */
	path?: string;
	/** edit route, only when the item can be changed from the Console */
	editPath?: string;
}

const enc = encodeURIComponent;

interface Owned {
	name: string;
	read_only?: boolean;
	origin?: string;
}
const editable = (r: Owned) => !r.read_only && (r.origin ?? 'managed') === 'managed';

export interface RouterLike extends Owned {
	config: { rule?: string; entry_points?: string[] | null; service?: string; tls?: unknown };
}
/** `base` is the router page of its protocol: /routers (HTTP), /tcp-routers or /udp-routers. */
export function routeItem(r: RouterLike, base = '/routers'): RelatedItem {
	const facts = [
		...(r.config.entry_points ?? []),
		...(r.config.service ? ['→ ' + r.config.service] : []),
		...(r.config.tls ? ['TLS'] : []),
	];
	return {
		name: r.name,
		kind: 'route',
		summary: r.config.rule || undefined,
		facts,
		path: base + '/' + enc(r.name),
		editPath: editable(r) ? base + '/' + enc(r.name) + '/edit' : undefined,
	};
}

export interface ChainLike extends Owned {
	config: { middlewares?: string[] | null };
}
export function chainItem(c: ChainLike, members: string[] = c.config.middlewares ?? []): RelatedItem {
	return {
		name: c.name,
		kind: 'chain',
		summary: members.join(' → ') || undefined,
		path: '/chains/' + enc(c.name),
		editPath: editable(c) ? '/chains/' + enc(c.name) + '/edit' : undefined,
	};
}

export interface DomainLike extends Owned {
	config: { main?: string; sans?: string[] | null; method?: string; resolver?: string; certificate?: string };
}
export function domainItem(d: DomainLike): RelatedItem {
	const hosts = [d.config.main, ...(d.config.sans ?? [])].filter(Boolean) as string[];
	const source = d.config.method === 'resolver' ? d.config.resolver : d.config.certificate;
	return {
		name: d.name,
		kind: 'domain',
		summary: hosts.join(', ') || undefined,
		facts: source ? [source] : [],
		path: '/domains/' + enc(d.name),
		editPath: editable(d) ? '/domains/' + enc(d.name) + '/edit' : undefined,
	};
}

export interface MiddlewareLike extends Owned {
	config: { type?: string; plugin_name?: string };
}
export function middlewareItem(m: MiddlewareLike): RelatedItem {
	return {
		name: m.name,
		kind: 'middleware',
		summary: m.config.type === 'plugin' && m.config.plugin_name ? 'plugin · ' + m.config.plugin_name : m.config.type,
		path: '/middlewares/' + enc(m.name),
		editPath: editable(m) ? '/middlewares/' + enc(m.name) + '/edit' : undefined,
	};
}

/** A certificate as core-v2 describes it in a usage list (models.CertificateRef). */
export interface CertificateLike {
	source: 'uploaded' | 'acme' | string;
	id: string;
	name: string;
	names: string[];
	validity: string;
	not_after?: string;
}
export function certificateItem(c: CertificateLike, validityLabel: (v: string) => string): RelatedItem {
	const tab = c.source === 'acme' ? 'automatic/' : 'uploaded/';
	return {
		name: c.name,
		kind: 'certificate',
		summary: c.names.join(', ') || undefined,
		facts: [validityLabel(c.validity)],
		path: '/certificates/' + tab + enc(c.id),
	};
}

/** Used when only a name is known (the referenced object is not loaded). */
export const namedItem = (name: string, kind: RelatedKind, base: string): RelatedItem => ({
	name,
	kind,
	path: base + enc(name),
});

export const listRouterSummaries = async (signal?: AbortSignal) =>
	list(await api.get<RouterLike[] | null>('/api/v1/routers', { signal }));

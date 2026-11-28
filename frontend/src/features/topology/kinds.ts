import { Globe, KeyRound, Layers, Link2, LogIn, Network, Route, ShieldCheck } from '@lucide/svelte';
import type { NodeKind, Relation } from './graph';

/** One icon and one colour token per kind, shared by the map, its legend and the link dialog. */
export const KIND_ICON: Record<NodeKind, typeof Route> = {
	entrypoint: LogIn,
	domain: Globe,
	resolver: KeyRound,
	certificate: ShieldCheck,
	route: Route,
	chain: Link2,
	middleware: Layers,
	service: Network,
};
export const kindColor = (kind: NodeKind) => `var(--kind-${kind})`;

/** An edge takes the colour of what is being referenced. */
export const RELATION_KIND: Record<Relation, NodeKind> = {
	'route-entrypoint': 'entrypoint',
	'route-domain': 'domain',
	'route-resolver': 'resolver',
	'route-middleware': 'middleware',
	'route-service': 'service',
	'chain-middleware': 'chain',
	'domain-resolver': 'resolver',
	'domain-certificate': 'certificate',
	'middleware-service': 'service',
};

/** Where each kind is managed (HTTP objects; TCP and UDP ones have their own base, see TopologyPage). */
export const KIND_PAGE: Partial<Record<NodeKind, string>> = {
	route: '/routers',
	service: '/services',
	middleware: '/middlewares',
	chain: '/chains',
	domain: '/domains',
	resolver: '/resolvers',
	certificate: '/certificates/uploaded',
	entrypoint: '/entrypoints',
};

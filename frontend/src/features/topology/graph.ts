/**
 * Configuration map: a pure model of how the Traefik objects reference each other.
 *
 *   resolver / certificate → domain → route ← entrypoint
 *                                     route → chain ⟲ middleware   (same lane: a chain brackets its members)
 *                                     route → middleware
 *                                     route → service
 *
 * Nothing here talks to the network or to the DOM. The page feeds it the lists
 * the other pages already read (GET /routers, /services, …) and gets back
 * nodes, edges, a layered layout, the rule deciding whether two nodes may be
 * linked, and the exact PUT body that creates or removes a link.
 */
import type { Resource } from '../../core/types';
import { formFrom, referenceBase, routerProtocolOf, toInput, MAX_ENTRYPOINTS, MAX_MIDDLEWARES, type RouterInput, type RouterResource } from '../routes/models';
import { MAX_CHAIN_MEMBERS, middlewareProtocolOf, type ChainInput, type ChainResource, type MiddlewareResource } from '../middlewares/models';
import { protocolOf as serviceProtocolOf, serversOf, type ServiceResource } from '../services/models';
import { displayAddress, kindOf, type EntryPointResource } from '../entrypoints/models';
import { coversAll, hasWildcard, namesOf, type DomainInput, type DomainResource } from '../domains/models';
import type { ResolverResource } from '../resolvers/models';
import type { CertificateResource } from '../certificates/models';

/** The Traefik section a router, service or middleware lives in; for an entrypoint, what it listens on. */
export type Transport = 'http' | 'tcp' | 'udp';

export type NodeKind = 'resolver' | 'certificate' | 'entrypoint' | 'domain' | 'route' | 'chain' | 'middleware' | 'service';

/** Left-to-right lanes; kinds sharing a lane are stacked in this order. */
export const COLUMNS: NodeKind[][] = [['resolver', 'certificate'], ['entrypoint', 'domain'], ['route'], ['chain', 'middleware'], ['service']];
export const KINDS: NodeKind[] = COLUMNS.flat();
const COLUMN_OF = Object.fromEntries(COLUMNS.flatMap((kinds, index) => kinds.map((kind) => [kind, index]))) as Record<NodeKind, number>;

/**
 * A relation is named after the resource that stores it ("route-middleware" lives in the
 * route). `middleware-service` (an `errors` middleware pointing at a service) is displayed only.
 */
export type Relation =
	| 'route-entrypoint' | 'route-domain' | 'route-resolver' | 'route-middleware' | 'route-service'
	| 'chain-middleware' | 'domain-resolver' | 'domain-certificate' | 'middleware-service';

export interface GraphNode {
	id: string;
	kind: NodeKind;
	name: string;
	/** Technical one-liner (rule, backend URL, middleware type, hostname). Never localized. */
	subtitle: string;
	/** Members of a chain, backends of a service. */
	count: number;
	/** Shown next to the name: how Traefik talks to an HTTP service's backends (HTTP, HTTPS, H2C), or TCP / UDP for objects of those protocols. */
	protocol: string;
	/** Routers, services, middlewares, chains and entrypoints; '' for the rest. Links are only allowed inside one protocol. */
	transport: Transport | '';
	external: boolean;
	provider: string;
	readOnly: boolean;
	/** Referenced by something but not present in the inventory. */
	missing: boolean;
	health: string;
	error: string;
	tls: boolean;
	version: number;
	resource?: Resource<any>;
}

export interface GraphEdge {
	id: string;
	/** Left end and right end as drawn (request flow). */
	from: string;
	to: string;
	relation: Relation;
	/** The node whose configuration holds the reference, and the node it points at. */
	subject: string;
	object: string;
	/** 1-based position for ordered relations (middlewares of a route, members of a chain). */
	order?: number;
}

export interface Graph {
	nodes: GraphNode[];
	edges: GraphEdge[];
	byId: Map<string, GraphNode>;
}

export interface Inventory {
	routers: RouterResource[];
	services: ServiceResource[];
	middlewares: MiddlewareResource[];
	chains: ChainResource[];
	domains: DomainResource[];
	resolvers: ResolverResource[];
	certificates: CertificateResource[];
	entryPoints: string[];
	/** tcp or udp per entrypoint name; an entrypoint that is not listed carries TCP. */
	entryPointProtocols?: Record<string, string>;
	/** The full entrypoints when the server sent them: address, type and where they are managed. */
	entryPointResources?: EntryPointResource[];
	/** Kinds whose list could not be read: references to them are shown, never flagged as missing. */
	failed?: NodeKind[];
}

export const nodeId = (kind: NodeKind, name: string) => `${kind}:${name}`;

const hostsOfRule = (rule: string) => [...rule.matchAll(/Host(?:SNI)?\(`([^`]+)`\)/g)].map((m) => m[1]);

function routeSubtitle(r: RouterResource): string {
	const rule = r.config.rule ?? '';
	const hosts = hostsOfRule(rule);
	const prefix = /PathPrefix\(`([^`]+)`\)/.exec(rule)?.[1] ?? '';
	// A plain "hosts + optional prefix" rule reads better as a URL; anything else stays a rule.
	const simple = hosts.length > 0 && rule.replace(/Host(?:SNI)?\(`[^`]+`\)|PathPrefix\(`[^`]+`\)|[()&|\s]/g, '') === '';
	return simple ? hosts.join(', ') + prefix : rule;
}

/** "HTTP", "HTTPS", "H2C", or several joined when the backends differ. */
export function protocolOf(urls: string[]): string {
	const schemes = urls.map((u) => /^([a-z][a-z0-9+.-]*):\/\//i.exec(u.trim())?.[1].toUpperCase()).filter((s): s is string => !!s);
	return [...new Set(schemes)].join(' / ');
}

export function buildGraph(inv: Inventory): Graph {
	const nodes: GraphNode[] = [];
	const byId = new Map<string, GraphNode>();
	const edges: GraphEdge[] = [];
	const failed = new Set(inv.failed ?? []);

	const add = (kind: NodeKind, name: string, extra: Partial<GraphNode> = {}) => {
		const id = nodeId(kind, name);
		const existing = byId.get(id);
		if (existing) return existing;
		const node: GraphNode = { id, kind, name, subtitle: '', count: 0, protocol: '', transport: '', external: false, provider: '', readOnly: false, missing: false, health: '', error: '', tls: false, version: 0, ...extra };
		nodes.push(node);
		byId.set(id, node);
		return node;
	};
	const fromResource = (kind: NodeKind, r: Resource<any>, extra: Partial<GraphNode> = {}) =>
		add(kind, r.name, { external: r.origin === 'external', provider: r.provider ?? '', readOnly: !!r.read_only, health: r.live_health ?? '', error: r.live_error_msg ?? '', version: r.version ?? 0, resource: r, ...extra });

	/** TCP and UDP objects say so next to their name; HTTP is the unmarked case. */
	const tag = (transport: Transport) => (transport === 'http' ? '' : transport.toUpperCase());
	const listens = (name: string): Transport => (inv.entryPointProtocols?.[name] === 'udp' ? 'udp' : 'tcp');
	for (const e of inv.entryPointResources ?? []) {
		const transport: Transport = e.config.protocol === 'udp' ? 'udp' : 'tcp';
		const redirect = e.config.http?.redirect?.to;
		fromResource('entrypoint', e, { subtitle: displayAddress(e.config) + (redirect ? ' → ' + redirect : ''), transport, protocol: transport === 'udp' ? 'UDP' : '', tls: kindOf(e.config) === 'https' });
	}
	for (const name of inv.entryPoints) add('entrypoint', name, { readOnly: true, transport: listens(name), protocol: listens(name) === 'udp' ? 'UDP' : '' });
	for (const r of inv.resolvers) fromResource('resolver', r, { subtitle: r.config.type === 'custom' ? 'custom' : [r.config.challenge, r.config.provider].filter(Boolean).join(' · ') });
	for (const c of inv.certificates) fromResource('certificate', c, { subtitle: (c.config.domains ?? []).join(', '), health: c.config.status ?? '' });
	for (const d of inv.domains) fromResource('domain', d, { subtitle: [d.config.main, ...(d.config.sans ?? [])].filter(Boolean).join(', '), tls: true });
	for (const s of inv.services) {
		// HTTP backends are URLs, TCP and UDP ones are host:port addresses.
		const transport = serviceProtocolOf(s.config), servers = serversOf(s.config);
		fromResource('service', s, { subtitle: servers[0] ?? '', count: servers.length, transport, protocol: transport === 'http' ? protocolOf(servers) : tag(transport) });
	}
	for (const m of inv.middlewares) fromResource('middleware', m, { subtitle: m.config.type === 'plugin' && m.config.plugin_name ? m.config.plugin_name : m.config.type ?? '', transport: middlewareProtocolOf(m.config), protocol: tag(middlewareProtocolOf(m.config)) });
	for (const c of inv.chains) fromResource('chain', c, { count: (c.config.middlewares ?? []).length, transport: 'http' });
	for (const r of inv.routers) {
		const transport = routerProtocolOf(r.config);
		// A UDP router has no rule: what identifies it is where it listens.
		fromResource('route', r, { subtitle: routeSubtitle(r) || (r.config.entry_points ?? []).join(', '), tls: transport !== 'udp' && (!!r.config.tls || !!r.config.domain_name), transport, protocol: tag(transport) });
	}

	/**
	 * Resolve a reference written inside `owner`. Managed objects are stored under their bare
	 * name (`auth`, also written `auth@file`); external ones carry their provider (`auth@docker`),
	 * and Traefik lets an external router omit the suffix for objects of its own provider.
	 */
	const resolve = (kinds: NodeKind[], ref: string, owner: GraphNode): GraphNode => {
		const candidates = [ref, referenceBase(ref)];
		if (owner.external && owner.provider && !ref.includes('@')) candidates.push(`${ref}@${owner.provider}`);
		for (const kind of kinds) for (const name of candidates) {
			const hit = byId.get(nodeId(kind, name));
			if (hit) return hit;
		}
		const kind = kinds[0];
		return add(kind, referenceBase(ref), { missing: !failed.has(kind), external: ref.includes('@') && !ref.endsWith('@file'), provider: ref.includes('@') ? ref.split('@').pop()! : '', readOnly: true });
	};
	const link = (relation: Relation, subject: GraphNode, object: GraphNode, order?: number) => {
		// Drawn left to right: whichever end sits in the earlier lane is the start.
		const forward = COLUMN_OF[subject.kind] < COLUMN_OF[object.kind] || (COLUMN_OF[subject.kind] === COLUMN_OF[object.kind]);
		const [from, to] = forward ? [subject, object] : [object, subject];
		const id = `${relation}|${subject.id}|${object.id}`;
		if (edges.some((e) => e.id === id)) return;
		edges.push({ id, from: from.id, to: to.id, relation, subject: subject.id, object: object.id, order });
	};

	for (const r of inv.routers) {
		const route = byId.get(nodeId('route', r.name))!;
		const c = r.config;
		for (const ep of c.entry_points ?? []) link('route-entrypoint', route, resolve(['entrypoint'], ep, route));
		if (c.domain_name) link('route-domain', route, resolve(['domain'], c.domain_name, route));
		else if (c.tls?.cert_resolver) link('route-resolver', route, resolve(['resolver'], c.tls.cert_resolver, route));
		(c.middlewares ?? []).forEach((ref, i) => link('route-middleware', route, resolve(['middleware', 'chain'], ref, route), i + 1));
		if (c.service) link('route-service', route, resolve(['service'], c.service, route));
	}
	for (const c of inv.chains) {
		const chain = byId.get(nodeId('chain', c.name))!;
		(c.config.middlewares ?? []).forEach((ref, i) => link('chain-middleware', chain, resolve(['middleware', 'chain'], ref, chain), i + 1));
	}
	for (const d of inv.domains) {
		const domain = byId.get(nodeId('domain', d.name))!;
		if (d.config.method === 'resolver' && d.config.resolver) link('domain-resolver', domain, resolve(['resolver'], d.config.resolver, domain));
		if (d.config.method === 'files' && d.config.certificate) link('domain-certificate', domain, resolve(['certificate'], d.config.certificate, domain));
	}
	for (const m of inv.middlewares) {
		const target = m.config.type === 'errors' ? (m.config.spec as { service?: unknown } | null)?.service : undefined;
		if (typeof target === 'string' && target) {
			const mw = byId.get(nodeId('middleware', m.name))!;
			link('middleware-service', mw, resolve(['service'], target, mw));
		}
	}
	return { nodes, edges, byId };
}

/* ------------------------------------------------------------------ */
/* Layout                                                              */
/* ------------------------------------------------------------------ */

export const NODE_W = 236;
export const NODE_H = 58;
export const ROW_GAP = 14;
export const COL_GAP = 96;
export const HEADER_H = 34;
export const GROUP_GAP = 30;

export interface Placed { x: number; y: number; column: number }
export interface LaneHeader { kind: NodeKind; x: number; y: number; count: number }
export interface Layout {
	positions: Map<string, Placed>;
	headers: LaneHeader[];
	width: number;
	height: number;
}

/**
 * Layered layout. Lanes are fixed by kind; inside a lane, nodes are ordered by the average
 * position of what they connect to (two barycenter sweeps starting from the routes), which
 * keeps related objects on the same height and removes most edge crossings.
 */
/**
 * Keeps only the objects the Console manages, or only external ones (Docker labels, files written
 * by hand, Traefik's own). Entrypoints always stay: every route starts there. Links to a removed
 * object are dropped with it.
 */
export function filterOwnership(graph: Graph, keep: 'managed' | 'external'): Graph {
	const nodes = graph.nodes.filter((n) => n.kind === 'entrypoint' || n.external === (keep === 'external'));
	const ids = new Set(nodes.map((n) => n.id));
	return { nodes, edges: graph.edges.filter((e) => ids.has(e.from) && ids.has(e.to)), byId: new Map(nodes.map((n) => [n.id, n])) };
}

export function layoutGraph(graph: Graph, hidden: ReadonlySet<NodeKind> = new Set()): Layout {
	const visible = graph.nodes.filter((n) => !hidden.has(n.kind));
	const neighbours = new Map<string, string[]>();
	for (const e of graph.edges) {
		(neighbours.get(e.from) ?? neighbours.set(e.from, []).get(e.from)!).push(e.to);
		(neighbours.get(e.to) ?? neighbours.set(e.to, []).get(e.to)!).push(e.from);
	}
	const byKind = new Map<NodeKind, GraphNode[]>();
	for (const kind of KINDS) byKind.set(kind, visible.filter((n) => n.kind === kind).sort((a, b) => a.name.localeCompare(b.name)));

	const rank = new Map<string, number>();
	const place = (kind: NodeKind, against: NodeKind[] | null) => {
		const list = byKind.get(kind)!;
		if (against) {
			const score = (n: GraphNode) => {
				const ys = (neighbours.get(n.id) ?? []).filter((id) => against.includes(graph.byId.get(id)!.kind) && rank.has(id)).map((id) => rank.get(id)!);
				return ys.length ? ys.reduce((a, b) => a + b, 0) / ys.length : Number.POSITIVE_INFINITY;
			};
			const scores = new Map(list.map((n) => [n.id, score(n)]));
			list.sort((a, b) => (scores.get(a.id)! - scores.get(b.id)!) || a.name.localeCompare(b.name));
		}
		list.forEach((n, i) => rank.set(n.id, i));
	};
	const sweep = () => {
		place('domain', ['route']); place('entrypoint', ['route']);
		place('resolver', ['domain', 'route']); place('certificate', ['domain']);
		place('chain', ['route']); placeMiddlewares(); place('service', ['route']);
	};
	/**
	 * Chains share the middleware lane. Their members come first, grouped per chain in running
	 * order, so the bracket that ties a chain to its members stays short; the rest follow the routes.
	 */
	const placeMiddlewares = () => {
		place('middleware', ['route']);
		const list = byKind.get('middleware')!;
		const member = new Map<string, number>();
		for (const e of graph.edges) {
			if (e.relation !== 'chain-middleware' || !rank.has(e.subject)) continue;
			const key = rank.get(e.subject)! * 1000 + (e.order ?? 0);
			member.set(e.object, Math.min(member.get(e.object) ?? Infinity, key));
		}
		const before = new Map(list.map((n, i) => [n.id, i]));
		list.sort((a, b) => (member.get(a.id) ?? Infinity) - (member.get(b.id) ?? Infinity) || before.get(a.id)! - before.get(b.id)!);
		list.forEach((n, i) => rank.set(n.id, i));
	};
	place('route', null);
	sweep();
	place('route', ['service', 'domain']);
	sweep();

	const lanes = COLUMNS.map((kinds) => kinds.filter((k) => byKind.get(k)!.length > 0)).filter((kinds) => kinds.length > 0);
	const laneHeight = (kinds: NodeKind[]) => kinds.reduce((h, k, i) => h + (i ? GROUP_GAP : 0) + HEADER_H + byKind.get(k)!.length * (NODE_H + ROW_GAP) - ROW_GAP, 0);
	const height = Math.max(0, ...lanes.map(laneHeight));
	const positions = new Map<string, Placed>();
	const headers: LaneHeader[] = [];
	lanes.forEach((kinds, column) => {
		const x = column * (NODE_W + COL_GAP);
		let y = Math.round((height - laneHeight(kinds)) / 2);
		kinds.forEach((kind, i) => {
			if (i) y += GROUP_GAP;
			const list = byKind.get(kind)!;
			headers.push({ kind, x, y, count: list.length });
			y += HEADER_H;
			for (const n of list) { positions.set(n.id, { x, y, column }); y += NODE_H + ROW_GAP; }
			y -= ROW_GAP;
		});
	});
	return { positions, headers, width: lanes.length ? lanes.length * (NODE_W + COL_GAP) - COL_GAP : 0, height };
}

/**
 * Edges as drawn: the request pipeline of each route, literally.
 *
 *   no middleware:   route ───────────────────────────▶ service
 *   middlewares:     route ─▶ 1st ─▶ 2nd ─▶ … ─▶ last ─▶ service
 *
 * The model keeps "route uses middleware n" and "route sends to service" as links of the route
 * (that is where they are configured and removed); only where each line starts changes: at the
 * previous step of the same route instead of at the route. Steps that are not visible (hidden
 * kind) are skipped, so the pipeline stays connected.
 */
export function pipelineEdges(graph: Graph, visible: (id: string) => boolean): GraphEdge[] {
	const last = new Map<string, string>();
	const steps = graph.edges.filter((e) => e.relation === 'route-middleware').sort((a, b) => (a.order ?? 0) - (b.order ?? 0));
	const drawn = new Map<string, string>();
	for (const e of steps) {
		if (!visible(e.subject) || !visible(e.object)) continue;
		drawn.set(e.id, last.get(e.subject) ?? e.subject);
		last.set(e.subject, e.object);
	}
	return graph.edges.flatMap((e) => {
		if (!visible(e.from) || !visible(e.to)) return [];
		if (e.relation === 'route-middleware') return [{ ...e, from: drawn.get(e.id)!, to: e.object }];
		if (e.relation === 'route-service') return [{ ...e, from: last.get(e.subject) ?? e.subject, to: e.object }];
		return [e];
	});
}

/**
 * SVG path of an edge between two placed nodes. Same-lane links leave and land on the right:
 * a pipeline hop between two middlewares stays close, a chain's bracket (`wide`) sits further out.
 */
export function edgePath(a: Placed, b: Placed, wide = false): string {
	const y1 = a.y + NODE_H / 2, y2 = b.y + NODE_H / 2;
	if (a.column === b.column) {
		const x = a.x + NODE_W, bow = sameLaneBow(y1, y2, wide);
		return `M ${x} ${y1} C ${x + bow} ${y1}, ${x + bow} ${y2}, ${x} ${y2}`;
	}
	const x1 = a.x + NODE_W, x2 = b.x;
	const bend = Math.max(48, (x2 - x1) * 0.5);
	return `M ${x1} ${y1} C ${x1 + bend} ${y1}, ${x2 - bend} ${y2}, ${x2} ${y2}`;
}

const sameLaneBow = (y1: number, y2: number, wide: boolean) => (wide ? 62 : 26) + Math.min(wide ? 44 : 30, Math.abs(y2 - y1) / 8);

/** Point at `t` on the same curve, for the order badge. */
export function edgePoint(a: Placed, b: Placed, t = 0.5, wide = false): { x: number; y: number } {
	const y1 = a.y + NODE_H / 2, y2 = b.y + NODE_H / 2;
	let p: [number, number][];
	if (a.column === b.column) {
		const x = a.x + NODE_W, bow = sameLaneBow(y1, y2, wide);
		p = [[x, y1], [x + bow, y1], [x + bow, y2], [x, y2]];
	} else {
		const x1 = a.x + NODE_W, x2 = b.x, bend = Math.max(48, (x2 - x1) * 0.5);
		p = [[x1, y1], [x1 + bend, y1], [x2 - bend, y2], [x2, y2]];
	}
	const u = 1 - t;
	const at = (i: 0 | 1) => u * u * u * p[0][i] + 3 * u * u * t * p[1][i] + 3 * u * t * t * p[2][i] + t * t * t * p[3][i];
	return { x: at(0), y: at(1) };
}

/** Everything on the traffic path through a node: what leads to it and what it leads to. */
export function pathThrough(graph: Graph, start: string): { nodes: Set<string>; edges: Set<string> } {
	const nodes = new Set([start]);
	const edges = new Set<string>();
	const walk = (forward: boolean) => {
		const queue = [start];
		const seen = new Set([start]);
		while (queue.length) {
			const id = queue.shift()!;
			for (const e of graph.edges) {
				const [here, next] = forward ? [e.from, e.to] : [e.to, e.from];
				if (here !== id) continue;
				edges.add(e.id);
				nodes.add(next);
				if (!seen.has(next)) { seen.add(next); queue.push(next); }
			}
		}
	};
	walk(true);
	walk(false);
	// A request that reaches a middleware belongs to a route and runs that route's whole
	// pipeline, down to its service: light up every step of each route on the path.
	const routes = [...nodes].filter((id) => graph.byId.get(id)?.kind === 'route');
	const queue = [...routes];
	const seen = new Set(routes);
	while (queue.length) {
		const id = queue.shift()!;
		for (const e of graph.edges) {
			if (e.from !== id || e.relation === 'route-entrypoint' || e.relation === 'route-domain' || e.relation === 'route-resolver') continue;
			edges.add(e.id);
			nodes.add(e.to);
			if (!seen.has(e.to)) { seen.add(e.to); queue.push(e.to); }
		}
	}
	return { nodes, edges };
}

/* ------------------------------------------------------------------ */
/* Connecting two nodes                                                */
/* ------------------------------------------------------------------ */

export interface Connection {
	relation: Exclude<Relation, 'middleware-service'>;
	/** The resource that is rewritten. */
	subject: GraphNode;
	/** What it will reference. */
	object: GraphNode;
}

/** Why two nodes cannot be linked; the page turns the code into a sentence. */
export type ConnectError =
	| 'same' | 'incompatible' | 'missing' | 'readOnly' | 'already' | 'tooMany' | 'cycle'
	| 'domainTls' | 'notServerCertificate' | 'notCovered' | 'wildcardChallenge' | 'required' | 'protocol';

export type ConnectResult = { ok: true; connection: Connection } | { ok: false; error: ConnectError; subject?: GraphNode };

const refsEqual = (ref: string, node: GraphNode) => ref === node.name || referenceBase(ref) === node.name;

/** [subject kind, object kinds, relation] — the only pairs that can be linked. */
const RULES: [NodeKind, NodeKind[], Connection['relation']][] = [
	['route', ['entrypoint'], 'route-entrypoint'],
	['route', ['domain'], 'route-domain'],
	['route', ['resolver'], 'route-resolver'],
	['route', ['middleware', 'chain'], 'route-middleware'],
	['route', ['service'], 'route-service'],
	['chain', ['middleware', 'chain'], 'chain-middleware'],
	['domain', ['resolver'], 'domain-resolver'],
	['domain', ['certificate'], 'domain-certificate']
];

/**
 * Traefik keeps HTTP, TCP and UDP in separate sections: a router only uses services and
 * middlewares of its own protocol, a UDP router only listens on UDP entrypoints and has neither
 * middlewares nor TLS, and a chain only holds HTTP middlewares.
 */
function crossesProtocols(relation: Connection['relation'], subject: GraphNode, object: GraphNode): boolean {
	if (subject.kind !== 'route') return relation === 'chain-middleware' && object.transport === 'tcp';
	const own = subject.transport || 'http';
	switch (relation) {
		case 'route-service': return !!object.transport && object.transport !== own;
		case 'route-middleware': return own === 'udp' || (object.transport || 'http') !== own;
		case 'route-entrypoint': return !!object.transport && (object.transport === 'udp') !== (own === 'udp');
		case 'route-domain': case 'route-resolver': return own === 'udp';
	}
	return false;
}

function containsChain(graph: Graph, from: GraphNode, target: GraphNode, seen = new Set<string>()): boolean {
	if (from.id === target.id) return true;
	if (seen.has(from.id)) return false;
	seen.add(from.id);
	return graph.edges.some((e) => e.relation === 'chain-middleware' && e.subject === from.id && containsChain(graph, graph.byId.get(e.object)!, target, seen));
}

/**
 * Decide what dropping `a` onto `b` means. Direction does not matter: "middleware → route"
 * and "route → middleware" both attach the middleware to the route.
 */
export function canConnect(graph: Graph, a: GraphNode, b: GraphNode): ConnectResult {
	if (a.id === b.id) return { ok: false, error: 'same' };
	let rule: (typeof RULES)[number] | undefined;
	let subject = a, object = b;
	for (const candidate of RULES) {
		// chain ↔ chain is ambiguous by kind alone: the node the user dropped ON receives the member.
		if (candidate[0] === b.kind && candidate[1].includes(a.kind)) { rule = candidate; subject = b; object = a; break; }
		if (candidate[0] === a.kind && candidate[1].includes(b.kind)) { rule = candidate; subject = a; object = b; break; }
	}
	if (!rule) return { ok: false, error: 'incompatible' };
	const relation = rule[2];
	const fail = (error: ConnectError): ConnectResult => ({ ok: false, error, subject });
	if (subject.missing || object.missing) return fail('missing');
	if (subject.readOnly || !subject.resource) return fail('readOnly');
	if (graph.edges.some((e) => e.relation === relation && e.subject === subject.id && e.object === object.id)) return fail('already');
	if (crossesProtocols(relation, subject, object)) return fail('protocol');

	if (subject.kind === 'route') {
		const c = (subject.resource as RouterResource).config;
		if (relation === 'route-entrypoint' && (c.entry_points ?? []).length >= MAX_ENTRYPOINTS) return fail('tooMany');
		if (relation === 'route-middleware' && (c.middlewares ?? []).length >= MAX_MIDDLEWARES) return fail('tooMany');
		if (relation === 'route-resolver' && c.domain_name) return fail('domainTls');
	}
	if (relation === 'chain-middleware') {
		if (((subject.resource as ChainResource).config.middlewares ?? []).length >= MAX_CHAIN_MEMBERS) return fail('tooMany');
		if (object.kind === 'chain' && containsChain(graph, object, subject)) return fail('cycle');
	}
	if (subject.kind === 'domain') {
		const names = namesOf((subject.resource as DomainResource).config);
		if (relation === 'domain-certificate') {
			const cert = (object.resource as CertificateResource | undefined)?.config;
			if (!cert || cert.purpose !== 'server') return fail('notServerCertificate');
			if (!coversAll(cert.domains ?? [], names)) return fail('notCovered');
		} else {
			const challenge = (object.resource as ResolverResource | undefined)?.config.challenge;
			if (hasWildcard(names) && challenge && challenge !== 'dns01') return fail('wildcardChallenge');
		}
	}
	return { ok: true, connection: { relation, subject, object } };
}

/** Relations that can be removed on their own. A route always needs a service; a domain always needs a certificate source. */
export function canDisconnect(graph: Graph, edge: GraphEdge): ConnectResult {
	const subject = graph.byId.get(edge.subject)!, object = graph.byId.get(edge.object)!;
	if (edge.relation === 'middleware-service') return { ok: false, error: 'incompatible', subject };
	if (subject.readOnly || !subject.resource) return { ok: false, error: 'readOnly', subject };
	if (edge.relation === 'route-service' || edge.relation === 'route-domain' || edge.relation === 'domain-resolver' || edge.relation === 'domain-certificate') return { ok: false, error: 'required', subject };
	if (edge.relation === 'chain-middleware' && ((subject.resource as ChainResource).config.middlewares ?? []).length <= 1) return { ok: false, error: 'required', subject };
	return { ok: true, connection: { relation: edge.relation, subject, object } };
}

/* ------------------------------------------------------------------ */
/* The write                                                           */
/* ------------------------------------------------------------------ */

export interface ConnectOptions {
	/** Final ordered list for route-middleware / chain-middleware (the dialog lets the user reorder). */
	order?: string[];
	/** route-domain: optional subdomain and path prefix. */
	subdomain?: string;
	pathPrefix?: string;
}

export type Change =
	| { endpoint: 'routers'; name: string; version: number; input: RouterInput }
	| { endpoint: 'chains'; name: string; version: number; input: ChainInput }
	| { endpoint: 'domains'; name: string; version: number; input: DomainInput };

/** Ordered references currently stored on the subject of an ordered relation. */
export function currentOrder(c: Connection): string[] {
	if (c.relation === 'route-middleware') return [...((c.subject.resource as RouterResource).config.middlewares ?? [])];
	if (c.relation === 'chain-middleware') return [...((c.subject.resource as ChainResource).config.middlewares ?? [])];
	return [];
}

/**
 * The full PUT body with one reference added or removed. Routes go through the same
 * formFrom → toInput path as the route dialog, so every other field is sent back unchanged.
 */
export function buildChange(c: Connection, op: 'add' | 'remove', opts: ConnectOptions = {}): Change {
	const { subject, object } = c;
	const without = (refs: string[]) => refs.filter((ref) => !refsEqual(ref, object));
	if (subject.kind === 'chain') {
		const members = currentOrder(c);
		return { endpoint: 'chains', name: subject.name, version: subject.version, input: { middlewares: op === 'remove' ? without(members) : opts.order ?? [...members, object.name] } };
	}
	if (subject.kind === 'domain') {
		const d = (subject.resource as DomainResource).config;
		return {
			endpoint: 'domains', name: subject.name, version: subject.version,
			input: { main: d.main, sans: [...(d.sans ?? [])], ...(c.relation === 'domain-resolver' ? { method: 'resolver', resolver: object.name } : { method: 'files', certificate: object.name }) }
		};
	}
	const form = formFrom(subject.name, (subject.resource as RouterResource).config);
	switch (c.relation) {
		case 'route-entrypoint':
			form.entryPoints = op === 'remove' ? without(form.entryPoints) : [...form.entryPoints, object.name];
			break;
		case 'route-middleware':
			form.middlewares = op === 'remove' ? without(form.middlewares) : opts.order ?? [...form.middlewares, object.name];
			break;
		case 'route-service':
			form.service = object.name;
			break;
		case 'route-domain':
			// A hand-written rule has no path prefix of its own; hosts mode keeps the one it had.
			if (form.mode === 'rule') form.pathPrefix = '';
			form.mode = 'domain';
			form.domainName = object.name;
			form.subdomain = opts.subdomain ?? '';
			if (opts.pathPrefix !== undefined) form.pathPrefix = opts.pathPrefix;
			break;
		case 'route-resolver':
			form.tlsEnabled = true;
			form.tlsResolver = op === 'remove' ? '' : object.name;
			break;
	}
	return { endpoint: 'routers', name: subject.name, version: subject.version, input: toInput(form) };
}

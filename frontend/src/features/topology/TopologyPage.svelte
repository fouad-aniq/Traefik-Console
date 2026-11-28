<script lang="ts">
	/**
	 * Configuration map: every Traefik object and what it references, as one left-to-right picture.
	 *
	 *   resolvers / certificates → domains + entrypoints → routes → chains + middlewares → services
	 *   (a chain sits above the middlewares and is tied to its members by a dotted bracket)
	 * - Lines follow the request: route → its middlewares in order → service, or route → service when it has none.
	 *
	 * - Read-only by itself: it shows the same lists as the other pages (see graph.ts).
	 * - Dragging one object onto another (or "Link to…" in the side panel) opens ConnectDialog,
	 *   which writes the link through the usual preview → apply protocol.
	 * - Edit and Add open the existing resource dialogs in place; the map is read again afterwards.
	 */
	import { onMount, onDestroy, tick } from 'svelte';
	import { Plus, Minus, Maximize, RefreshCw, Search, X, Link2, Unlink, Pencil, ExternalLink } from '@lucide/svelte';
	import Button from '../../shared/ui/Button.svelte';
	import Notice from '../../shared/ui/Notice.svelte';
	import MenuPopover from '../../shared/ui/MenuPopover.svelte';
	import StatusBadge from '../../shared/ui/StatusBadge.svelte';
	import NodeCard from './NodeCard.svelte';
	import ConnectDialog from './ConnectDialog.svelte';
	import RouteDialog from '../routes/RouteDialog.svelte';
	import ServiceDialog from '../services/ServiceDialog.svelte';
	import MiddlewareDialog from '../middlewares/MiddlewareDialog.svelte';
	import ChainDialog from '../middlewares/ChainDialog.svelte';
	import DomainDialog from '../domains/DomainDialog.svelte';
	import ResolverDialog from '../resolvers/ResolverDialog.svelte';
	import EntryPointDialog from '../entrypoints/EntryPointDialog.svelte';
	import { listEntryPointInventory } from '../entrypoints/api';
	import { displayAddress, kindOf, type EntryPointResource } from '../entrypoints/models';
	import { listRouters } from '../routes/api';
	import { listServices } from '../services/api';
	import { listChains, listMiddlewares } from '../middlewares/api';
	import { listDomains } from '../domains/api';
	import { listResolvers } from '../resolvers/api';
	import { listCertificates } from '../certificates/api';
	import {
		KINDS,
		NODE_H,
		NODE_W,
		buildGraph,
		canConnect,
		canDisconnect,
		edgePath,
		edgePoint,
		layoutGraph,
		pathThrough,
		pipelineEdges,
		type ConnectError,
		type ConnectResult,
		type Connection,
		type GraphEdge,
		type GraphNode,
		type Inventory,
		type NodeKind,
		type Transport,
		filterOwnership,
	} from './graph';
	import {
		loadMapHiddenKinds,
		saveMapHiddenKinds,
		loadMapOwnership,
		saveMapOwnership,
		type MapOwnership,
	} from '../../shared/preferences/preferences';
	import { KIND_ICON, KIND_PAGE, RELATION_KIND, kindColor } from './kinds';
	import { errorMessage } from '../../shared/api/errorMessage';
	import { flash } from '../../shared/ui/flash.svelte';
	import { t } from '../../shared/localization/i18n';
	import { ROUTER_BASE, type RouterResource } from '../routes/models';
	import { BASE as SERVICE_BASE, serversOf, type ServiceResource } from '../services/models';
	import { MIDDLEWARE_BASE } from '../middlewares/models';
	import type { DomainResource } from '../domains/models';
	import type { ResolverResource } from '../resolvers/models';
	import type { CertificateResource } from '../certificates/models';

	let { navigate }: { navigate: (p: string) => void } = $props();

	const tp = (key: string, args?: Record<string, string | number>) => t('topology.' + key, args);

	/* ---------------------------------------------------------------- data */

	const EMPTY: Inventory = {
		routers: [],
		services: [],
		middlewares: [],
		chains: [],
		domains: [],
		resolvers: [],
		certificates: [],
		entryPoints: [],
	};
	let inventory = $state.raw<Inventory>(EMPTY);
	let loading = $state(true);
	let refreshing = $state(false);
	let error = $state('');
	let failed = $state<NodeKind[]>([]);

	let generation = 0;
	let disposed = false;
	let controller = new AbortController();

	async function load(silent = false) {
		const own = ++generation;
		controller.abort();
		controller = new AbortController();
		const signal = controller.signal;
		if (silent) refreshing = true;
		else loading = true;
		const next: Inventory = { ...EMPTY };
		// Each list is read on its own: one failing endpoint must not blank the whole map.
		const jobs: [NodeKind, Promise<unknown>][] = [
			// Every protocol: HTTP, TCP and UDP routers, services and middlewares share the map.
			[
				'route',
				listRouters(signal).then((v) => {
					next.routers = v;
				}),
			],
			[
				'service',
				listServices(signal).then((v) => {
					next.services = v;
				}),
			],
			[
				'middleware',
				listMiddlewares(signal).then((v) => {
					next.middlewares = v;
				}),
			],
			[
				'chain',
				listChains(signal).then((v) => {
					next.chains = v;
				}),
			],
			[
				'domain',
				listDomains(signal).then((v) => {
					next.domains = v;
				}),
			],
			[
				'resolver',
				listResolvers(signal).then((v) => {
					next.resolvers = v;
				}),
			],
			[
				'certificate',
				listCertificates(signal).then((v) => {
					next.certificates = v;
				}),
			],
			[
				'entrypoint',
				listEntryPointInventory(signal).then((v) => {
					next.entryPoints = v.names;
					next.entryPointProtocols = v.protocols;
					next.entryPointResources = v.items;
				}),
			],
		];
		const settled = await Promise.allSettled(jobs.map(([, p]) => p));
		if (disposed || own !== generation) return;
		const missing = jobs.filter((_, i) => settled[i].status === 'rejected').map(([kind]) => kind);
		const first = settled.find((s) => s.status === 'rejected') as PromiseRejectedResult | undefined;
		const nothing = missing.length === jobs.length;
		if (nothing) {
			error = errorMessage(first?.reason, tp('loadFailed'));
			if (!silent) inventory = EMPTY;
		} else {
			error = '';
			inventory = { ...next, failed: missing };
		}
		failed = nothing ? [] : missing;
		const firstLoad = loading;
		loading = false;
		refreshing = false;
		if (selection?.type === 'node' && !graph.byId.has(selection.id)) selection = null;
		if (selection?.type === 'edge' && !graph.edges.some((e) => e.id === selection!.id)) selection = null;
		if (firstLoad) {
			await tick();
			measure();
			fit();
		}
	}

	onMount(() => {
		measure();
		void load();
	});
	// A notice above the map moves it down: keep it ending at the bottom of the window.
	$effect(() => {
		void error;
		void failed.length;
		void tick().then(measure);
	});
	onDestroy(() => {
		disposed = true;
		controller.abort();
		stopGesture();
	});

	/* --------------------------------------------------------------- graph */

	/** Switched-off kinds are remembered in this browser, so the map opens the way the person left it. */
	let hidden = $state<NodeKind[]>(loadMapHiddenKinds(KINDS));
	let search = $state('');

	/** All objects, or only managed or only external ones (remembered in this browser). */
	let ownership = $state<MapOwnership>(loadMapOwnership());

	const graph = $derived(buildGraph(inventory));
	const shown = $derived(ownership === 'all' ? graph : filterOwnership(graph, ownership));
	const layout = $derived(layoutGraph(shown, new Set(hidden)));
	const nodes = $derived(shown.nodes.filter((n) => layout.positions.has(n.id)));
	/** Drawn as the request pipeline: route → its middlewares in order → service (see pipelineEdges). */
	const edges = $derived(pipelineEdges(shown, (id) => layout.positions.has(id)));
	const counts = $derived(
		Object.fromEntries(KINDS.map((k) => [k, shown.nodes.filter((n) => n.kind === k).length])) as Record<
			NodeKind,
			number
		>,
	);
	/** Nodes that need a right-hand port: every link start, plus chain members (their bracket lands on the right). */
	const rightPorts = $derived(
		new Set(
			edges.flatMap((e) =>
				layout.positions.get(e.from)!.column === layout.positions.get(e.to)!.column ? [e.from, e.to] : [e.from],
			),
		),
	);

	const query = $derived(search.trim().toLowerCase());
	const matches = $derived(
		query
			? new Set(nodes.filter((n) => `${n.name} ${n.subtitle}`.toLowerCase().includes(query)).map((n) => n.id))
			: null,
	);

	type Selection = { type: 'node'; id: string } | { type: 'edge'; id: string } | null;
	let selection = $state<Selection>(null);
	let hovered = $state('');
	const selectedNode = $derived(selection?.type === 'node' ? (graph.byId.get(selection.id) ?? null) : null);
	const selectedEdge = $derived(
		selection?.type === 'edge' ? (graph.edges.find((e) => e.id === selection!.id) ?? null) : null,
	);

	function setOwnership(value: MapOwnership) {
		ownership = value;
		saveMapOwnership(value);
		if (selectedNode && !shown.byId.has(selectedNode.id)) selection = null;
		void tick().then(fit);
	}

	function toggleKind(kind: NodeKind) {
		hidden = hidden.includes(kind) ? hidden.filter((k) => k !== kind) : [...hidden, kind];
		saveMapHiddenKinds(hidden);
		if (selectedNode && hidden.includes(selectedNode.kind)) selection = null;
		void tick().then(fit);
	}

	/* ------------------------------------------------------------ viewport */

	const MIN_ZOOM = 0.25,
		MAX_ZOOM = 1.6,
		PAD = 36;
	let viewport = $state<HTMLDivElement | null>(null);
	let vw = $state(0);
	let vh = $state(0);
	let frameHeight = $state(560);
	let view = $state({ x: PAD, y: PAD, k: 1 });
	const clamp = (v: number, lo: number, hi: number) => Math.min(hi, Math.max(lo, v));

	/** Height of the floating legend: the fitted map starts below it. */
	let legendH = $state(0);
	/** Readable before complete: fitting never shrinks text below this; a taller map scrolls instead. */
	const READABLE_ZOOM = 0.8;

	function measure() {
		if (!viewport) return;
		// The map takes the rest of the window (24px: the page's bottom padding).
		frameHeight = Math.max(440, Math.round(window.innerHeight - viewport.getBoundingClientRect().top - 24 - 1));
	}
	/**
	 * Fit the width, and the height as long as the text stays readable. When the map is taller
	 * than that, it starts at the top and the rest is a scroll away.
	 */
	function fit() {
		const w = vw || viewport?.clientWidth || 0,
			h = vh || viewport?.clientHeight || 0;
		if (!w || !h || !layout.width) return;
		const top = PAD + legendH;
		const byWidth = (w - PAD * 2) / layout.width;
		const byHeight = (h - top - PAD) / Math.max(layout.height, 1);
		const k = clamp(Math.min(byWidth, Math.max(byHeight, Math.min(READABLE_ZOOM, byWidth))), MIN_ZOOM, 1);
		const spare = h - top - PAD - layout.height * k;
		view = { k, x: Math.round((w - layout.width * k) / 2), y: Math.round(top + Math.max(0, spare / 2)) };
	}
	function zoomAt(factor: number, cx = vw / 2, cy = vh / 2) {
		const k = clamp(view.k * factor, MIN_ZOOM, MAX_ZOOM);
		const ratio = k / view.k;
		view = { k, x: cx - (cx - view.x) * ratio, y: cy - (cy - view.y) * ratio };
	}
	const local = (e: { clientX: number; clientY: number }) => {
		const rect = viewport!.getBoundingClientRect();
		return { x: e.clientX - rect.left, y: e.clientY - rect.top };
	};
	const toWorld = (p: { x: number; y: number }) => ({ x: (p.x - view.x) / view.k, y: (p.y - view.y) / view.k });

	/** Scroll moves the map; Ctrl/⌘ + scroll (and trackpad pinch) zooms around the pointer. */
	function wheel(e: WheelEvent) {
		if ((e.target as Element).closest('.overlay')) return;
		e.preventDefault();
		if (e.ctrlKey || e.metaKey) {
			const p = local(e);
			zoomAt(Math.exp(-e.deltaY * 0.01), p.x, p.y);
		} else view = { ...view, x: view.x - (e.shiftKey ? e.deltaY : e.deltaX), y: view.y - (e.shiftKey ? 0 : e.deltaY) };
	}
	$effect(() => {
		const el = viewport;
		if (!el) return;
		el.addEventListener('wheel', wheel, { passive: false });
		return () => el.removeEventListener('wheel', wheel);
	});

	/** Bring a node into view without changing the zoom. */
	function reveal(id: string) {
		const p = layout.positions.get(id);
		if (!p || !vw || !vh) return;
		const left = p.x * view.k + view.x,
			top = p.y * view.k + view.y;
		const right = left + NODE_W * view.k,
			bottom = top + NODE_H * view.k;
		const reserve = selection && vw > 767 ? 348 : 0;
		let { x, y } = view;
		if (left < PAD) x += PAD - left;
		else if (right > vw - PAD - reserve) x -= right - (vw - PAD - reserve);
		if (top < PAD) y += PAD - top;
		else if (bottom > vh - PAD) y -= bottom - (vh - PAD);
		view = { ...view, x, y };
	}

	/* ------------------------------------------------- pan, select, connect */

	/**
	 * One link gesture, two ways in:
	 *  drag — press an object, move, release over another one;
	 *  pick — "Link to…" in the side panel (also the keyboard and touch path), then choose the other object.
	 */
	let link = $state<{
		from: GraphNode;
		mode: 'drag' | 'pick';
		x: number;
		y: number;
		px: number;
		py: number;
		target: GraphNode | null;
	} | null>(null);
	const verdicts = $derived.by(() => {
		const from = link?.from;
		return from ? new Map<string, ConnectResult>(nodes.map((n) => [n.id, canConnect(graph, from, n)])) : null;
	});
	let press: { node: GraphNode | null; x: number; y: number; vx: number; vy: number; moved: boolean } | null = null;
	let swallowClick = false;

	/** What stays bright: the path through the hovered (else selected) object, or the selected link. */
	const focus = $derived.by(() => {
		if (link) return null;
		const id = hovered || (selection?.type === 'node' ? selection.id : '');
		if (id && graph.byId.has(id)) return pathThrough(graph, id);
		if (selectedEdge)
			return { nodes: new Set([selectedEdge.from, selectedEdge.to]), edges: new Set([selectedEdge.id]) };
		return null;
	});

	function reason(result: ConnectResult, a: GraphNode, b: GraphNode): string {
		if (result.ok) return '';
		const code: ConnectError = result.error;
		if (code === 'protocol') {
			// "TCP routers cannot use a UDP service": name both protocols and what was refused.
			const route = a.kind === 'route' ? a : b.kind === 'route' ? b : null;
			if (!route) return tp('reason.incompatible', { a: tp('kind.' + a.kind), b: tp('kind.' + b.kind) });
			const other = route === a ? b : a;
			return tp('reason.protocol', {
				a: tp('transport.' + (route.transport || 'http')),
				b: tp('transport.' + (other.transport || 'http')),
				kind: tp('kind.' + other.kind).toLowerCase(),
			});
		}
		return tp('reason.' + code, {
			subject: result.subject?.name ?? '',
			a: tp('kind.' + a.kind),
			b: tp('kind.' + b.kind),
		});
	}

	function trackPointer(e: PointerEvent) {
		if (!link || !viewport) return;
		const p = local(e),
			w = toWorld(p);
		const el = document.elementFromPoint(e.clientX, e.clientY)?.closest<HTMLElement>('[data-node-id]');
		const target = el ? (graph.byId.get(el.dataset.nodeId ?? '') ?? null) : null;
		link = { ...link, x: w.x, y: w.y, px: p.x, py: p.y, target: target && target.id !== link.from.id ? target : null };
	}

	function gestureMove(e: PointerEvent) {
		if (!press) return;
		const dx = e.clientX - press.x,
			dy = e.clientY - press.y;
		if (!press.moved && Math.hypot(dx, dy) < 5) return;
		press.moved = true;
		if (!press.node) {
			view = { ...view, x: press.vx + dx, y: press.vy + dy };
			return;
		}
		if (!link) {
			if (press.node.missing) return;
			const p = local(e),
				w = toWorld(p);
			link = { from: press.node, mode: 'drag', x: w.x, y: w.y, px: p.x, py: p.y, target: null };
			hovered = '';
		}
		trackPointer(e);
	}
	function gestureEnd() {
		const done = press;
		stopGesture();
		if (!done) return;
		if (done.moved) {
			swallowClick = true;
			setTimeout(() => (swallowClick = false));
		}
		if (done.node && link?.mode === 'drag') {
			const { from, target } = link;
			link = null;
			if (target) complete(from, target);
		} else if (!done.node && !done.moved) {
			// A plain click on the background: leave pick mode, else clear the selection.
			if (link) link = null;
			else selection = null;
		}
	}
	function stopGesture() {
		press = null;
		window.removeEventListener('pointermove', gestureMove);
		window.removeEventListener('pointerup', gestureEnd);
		window.removeEventListener('pointercancel', gestureEnd);
	}
	function startGesture(e: PointerEvent, node: GraphNode | null) {
		if (e.button !== 0) return;
		press = { node, x: e.clientX, y: e.clientY, vx: view.x, vy: view.y, moved: false };
		window.addEventListener('pointermove', gestureMove);
		window.addEventListener('pointerup', gestureEnd);
		window.addEventListener('pointercancel', gestureEnd);
	}
	function backgroundDown(e: PointerEvent) {
		if ((e.target as Element).closest('.node, .overlay, .edge')) return;
		startGesture(e, null);
	}
	function nodeDown(e: PointerEvent, node: GraphNode) {
		if (link?.mode === 'pick') return;
		startGesture(e, node);
	}
	function nodeClick(node: GraphNode) {
		if (swallowClick) return;
		if (link?.mode === 'pick') {
			const from = link.from;
			if (node.id === from.id) return;
			link = null;
			complete(from, node);
			return;
		}
		selection = { type: 'node', id: node.id };
	}
	function startPick(node: GraphNode) {
		const p = layout.positions.get(node.id)!;
		link = { from: node, mode: 'pick', x: p.x + NODE_W, y: p.y + NODE_H / 2, px: 0, py: 0, target: null };
	}
	function complete(from: GraphNode, target: GraphNode) {
		const result = canConnect(graph, from, target);
		if (result.ok) {
			connecting = { connection: result.connection, op: 'add' };
			return;
		}
		flash({ tone: 'info', title: tp('cannotLink'), message: reason(result, from, target) });
	}
	function keydown(e: KeyboardEvent) {
		if (e.key !== 'Escape' || connecting || editor) return;
		if (link) {
			link = null;
			stopGesture();
			e.preventDefault();
		} else if (selection) selection = null;
	}

	const rubber = $derived.by(() => {
		if (!link) return '';
		const p = layout.positions.get(link.from.id);
		if (!p) return '';
		const right = link.x >= p.x + NODE_W / 2;
		const x1 = right ? p.x + NODE_W : p.x,
			y1 = p.y + NODE_H / 2;
		const bend = Math.max(40, Math.abs(link.x - x1) * 0.5) * (right ? 1 : -1);
		return `M ${x1} ${y1} C ${x1 + bend} ${y1}, ${link.x - bend} ${link.y}, ${link.x} ${link.y}`;
	});
	const targetVerdict = $derived(link?.target ? (verdicts?.get(link.target.id) ?? null) : null);
	const linkHint = $derived.by(() => {
		if (!link) return '';
		if (!link.target || !targetVerdict)
			return link.mode === 'pick' ? tp('pickHint', { name: link.from.name }) : tp('dropHint');
		if (!targetVerdict.ok) return reason(targetVerdict, link.from, link.target);
		const c = targetVerdict.connection;
		return tp('drop.' + c.relation, { subject: c.subject.name, object: c.object.name });
	});

	/* -------------------------------------------------------------- dialogs */

	let connecting = $state<{ connection: Connection; op: 'add' | 'remove' } | null>(null);

	type EditorKind = 'route' | 'service' | 'middleware' | 'chain' | 'domain' | 'resolver' | 'entrypoint';
	const EDITORS: EditorKind[] = ['route', 'service', 'middleware', 'chain', 'domain', 'resolver', 'entrypoint'];
	/** `protocol` opens the router, service or middleware editor of the object's own protocol. */
	let editor = $state<{ kind: EditorKind; mode: 'create' | 'edit'; name: string; protocol?: Transport } | null>(null);
	const editorKind = (n: GraphNode): EditorKind | null =>
		(EDITORS as NodeKind[]).includes(n.kind) && !n.readOnly && !n.missing && n.resource ? (n.kind as EditorKind) : null;
	function edit(n: GraphNode) {
		const kind = editorKind(n);
		if (kind) editor = { kind, mode: 'edit', name: n.name, protocol: n.transport || undefined };
	}

	/**
	 * The resource dialogs close by navigating to their list page. On the map that means
	 * "close and read the map again"; any other destination is a real navigation.
	 */
	function editorNavigate(path: string) {
		if (editor && LIST_PAGES.has(path.split('?')[0])) {
			editor = null;
			void load(true);
			return;
		}
		navigate(path);
	}
	/** TCP and UDP routers, services and middlewares open under the base of their own protocol. */
	function pageOf(n: GraphNode): string | undefined {
		const transport = n.transport || 'http';
		if (n.kind === 'route') return ROUTER_BASE[transport];
		if (n.kind === 'service') return SERVICE_BASE[transport];
		if (n.kind === 'middleware') return MIDDLEWARE_BASE[transport === 'tcp' ? 'tcp' : 'http'];
		return KIND_PAGE[n.kind];
	}
	const LIST_PAGES = new Set<string>([
		...Object.values(KIND_PAGE),
		...Object.values(ROUTER_BASE),
		...Object.values(SERVICE_BASE),
		...Object.values(MIDDLEWARE_BASE),
	]);
	const pagePath = (n: GraphNode) => (pageOf(n) && !n.missing ? `${pageOf(n)}/${encodeURIComponent(n.name)}` : '');

	function removeLink(edge: GraphEdge) {
		const result = canDisconnect(graph, edge);
		if (result.ok) connecting = { connection: result.connection, op: 'remove' };
	}

	/* ------------------------------------------------------------ side panel */

	interface Fact {
		label: string;
		value: string;
		code?: boolean;
	}
	const KNOWN_HEALTH = ['loaded', 'enabled', 'error', 'pending_deploy', 'disabled', 'warning'];
	const healthTone = (h: string) =>
		(
			({
				loaded: 'success',
				enabled: 'success',
				error: 'danger',
				pending_deploy: 'info',
				disabled: 'neutral',
				warning: 'warning',
			}) as Record<string, 'success' | 'danger' | 'info' | 'neutral' | 'warning'>
		)[h] ?? 'neutral';

	function factsOf(n: GraphNode): Fact[] {
		const out: Fact[] = [];
		const add = (key: string, value: string | undefined | null, code = false) => {
			if (value) out.push({ label: tp('fact.' + key), value, code });
		};
		if (!n.resource) return out;
		if (n.kind === 'route') {
			const c = (n.resource as RouterResource).config;
			if (n.transport && n.transport !== 'http') add('transport', tp('transport.' + n.transport));
			add('rule', c.rule, true);
			// TLS wording is about HTTP(S); a TCP router only says whether it has TLS, a UDP one has none.
			if (n.transport === 'tcp') add('tls', c.tls || c.domain_name ? (c.tls?.cert_resolver ?? tp('badge.tls')) : '');
			else if (n.transport !== 'udp')
				add(
					'tls',
					!c.tls && !c.domain_name
						? t('routeV2.httpOnly')
						: c.tls?.cert_resolver
							? t('routeV2.httpsResolver', { name: c.tls.cert_resolver })
							: t('routeV2.https'),
				);
			if (c.priority) add('priority', String(c.priority));
		} else if (n.kind === 'service') {
			const c = (n.resource as ServiceResource).config;
			add('protocol', n.protocol);
			add('backends', serversOf(c).join('\n'), true);
			add('healthCheck', c.health_check?.path, true);
		} else if (n.kind === 'entrypoint') {
			const c = (n.resource as EntryPointResource).config;
			add('address', displayAddress(c), true);
			add('transport', tp('transport.' + (n.transport || 'tcp')));
			add('serves', t('entryPointUI.kind.' + kindOf(c)));
		} else if (n.kind === 'middleware') {
			if (n.transport === 'tcp') add('transport', tp('transport.tcp'));
			add('type', n.subtitle);
		} else if (n.kind === 'domain') {
			const c = (n.resource as DomainResource).config;
			add('names', [c.main, ...(c.sans ?? [])].join('\n'), true);
		} else if (n.kind === 'resolver') {
			const c = (n.resource as ResolverResource).config;
			add('challenge', c.challenge);
			add('provider', c.provider);
			add('environment', c.environment);
		} else if (n.kind === 'certificate') {
			const c = (n.resource as CertificateResource).config;
			add('names', (c.domains ?? []).join('\n'), true);
			add('issuer', c.issuer);
			add('expires', c.not_after ? new Date(c.not_after).toLocaleDateString() : '');
		}
		return out;
	}
	const facts = $derived(selectedNode ? factsOf(selectedNode) : []);

	interface Row {
		edge: GraphEdge;
		other: GraphNode;
		removable: boolean;
	}
	const rowsOf = (n: GraphNode, side: 'subject' | 'object'): Row[] =>
		graph.edges
			.filter((e) => e[side] === n.id)
			.map((edge) => ({
				edge,
				other: graph.byId.get(side === 'subject' ? edge.object : edge.subject)!,
				removable: canDisconnect(graph, edge).ok,
			}))
			.sort(
				(a, b) =>
					KINDS.indexOf(a.other.kind) - KINDS.indexOf(b.other.kind) ||
					(a.edge.order ?? 0) - (b.edge.order ?? 0) ||
					a.other.name.localeCompare(b.other.name),
			);
	const groups = $derived(
		selectedNode
			? [
					{ title: tp('panel.uses'), rows: rowsOf(selectedNode, 'subject') },
					{ title: tp('panel.usedBy'), rows: rowsOf(selectedNode, 'object') },
				].filter((g) => g.rows.length)
			: [],
	);
	const edgeNames = (e: GraphEdge) => ({
		subject: graph.byId.get(e.subject)!.name,
		object: graph.byId.get(e.object)!.name,
	});
	const edgeRemoval = $derived(selectedEdge ? canDisconnect(graph, selectedEdge) : null);
	const edgeColor = (e: GraphEdge) =>
		kindColor(e.relation === 'route-middleware' ? graph.byId.get(e.object)!.kind : RELATION_KIND[e.relation]);

	function go(id: string) {
		selection = { type: 'node', id };
		void tick().then(() => reveal(id));
	}
	const dots = $derived(22 * view.k);
</script>

<svelte:window onresize={measure} onkeydown={keydown} />

{#if error || failed.length}<div class="notices">
		{#if error}<Notice tone="error" message={error}>
				<Button size="sm" onclick={() => load()}>{t('common.retry')}</Button>
			</Notice>{/if}
		{#if failed.length}
			<Notice tone="warning" message={tp('partial', { list: failed.map((k) => tp('kinds.' + k)).join(', ') })}>
				<Button size="sm" onclick={() => load(true)}>{t('common.retry')}</Button>
			</Notice>
		{/if}
	</div>{/if}

<section class="map" aria-labelledby="map-title" aria-describedby="map-subtitle">
	<div class="toolbar">
		<div class="title-block">
			<h1 id="map-title" class="map-title">{tp('title')}</h1>
			<p id="map-subtitle" class="sr-only">{tp('subtitle')}</p>
		</div>
		<label class="search">
			<Search size={16} aria-hidden="true" />
			<span class="sr-only">{tp('search')}</span>
			<input type="search" bind:value={search} placeholder={tp('search')} autocomplete="off" spellcheck="false" />
		</label>
		<div class="ownership" role="group" aria-label={tp('ownership.label')}>
			{#each ['all', 'managed', 'external'] as const as value (value)}
				<button type="button" class="seg" aria-pressed={ownership === value} onclick={() => setOwnership(value)}>
					{tp('ownership.' + value)}
				</button>
			{/each}
		</div>
		<div class="actions">
			<Button onclick={() => load(true)} pending={refreshing} disabled={loading || refreshing}>
				<RefreshCw size={16} aria-hidden="true" />{t('common.refresh')}
			</Button>
			<MenuPopover label={tp('add')} align="end">
				{#snippet trigger()}<Plus size={16} aria-hidden="true" /><span>{tp('add')}</span>{/snippet}
				{#snippet children(close)}
					{#each EDITORS as kind (kind)}
						{@const Icon = KIND_ICON[kind]}
						<button
							type="button"
							role="menuitem"
							onclick={() => {
								close();
								editor = { kind, mode: 'create', name: '' };
							}}
						>
							<span class="menu-glyph" style:color={kindColor(kind)}><Icon size={16} aria-hidden="true" /></span>{tp(
								'kind.' + kind,
							)}
						</button>
					{/each}
				{/snippet}
			</MenuPopover>
		</div>
	</div>

	<!-- The surface itself only pans; every object on it is a real button. -->
	<!-- svelte-ignore a11y_no_static_element_interactions -->
	<div
		class="viewport"
		class:linking={!!link}
		bind:this={viewport}
		bind:clientWidth={vw}
		bind:clientHeight={vh}
		style:height="{frameHeight}px"
		style:background-size="{dots}px {dots}px"
		style:background-position="{view.x}px {view.y}px"
		onpointerdown={backgroundDown}
		onpointermove={(e) => {
			if (link?.mode === 'pick') trackPointer(e);
		}}
	>
		<!-- Kinds double as the legend and the layer filter; they float over the map like the zoom controls. -->
		<div class="legend overlay" role="group" aria-label={tp('legend')} bind:clientHeight={legendH}>
			{#each KINDS as kind (kind)}
				{@const Icon = KIND_ICON[kind]}
				<button
					type="button"
					class="chip"
					style:--kind={kindColor(kind)}
					aria-pressed={!hidden.includes(kind)}
					title={tp('toggleKind')}
					onclick={() => toggleKind(kind)}
				>
					<Icon size={14} aria-hidden="true" /><span class="chip-label">{tp('kinds.' + kind)}</span><span
						class="count numeric"
					>
						{counts[kind]}
					</span>
				</button>
			{/each}
		</div>
		{#if loading}
			<p class="state" role="status">{tp('loading')}</p>
		{:else if !nodes.length}
			<div class="state overlay">
				<h2>{tp('empty')}</h2>
				<p>{tp('emptyHelp')}</p>
				{#if !graph.nodes.length}
					<div class="state-actions">
						<Button onclick={() => (editor = { kind: 'service', mode: 'create', name: '' })}>
							{t('routeV2.openServices')}
						</Button>
						<Button variant="primary" onclick={() => (editor = { kind: 'route', mode: 'create', name: '' })}>
							{t('routes.create')}
						</Button>
					</div>
				{/if}
			</div>
		{:else}
			<div
				class="world"
				style:transform="translate({view.x}px, {view.y}px) scale({view.k})"
				style:width="{layout.width}px"
				style:height="{layout.height}px"
				style:--node-w="{NODE_W}px"
				style:--node-h="{NODE_H}px"
			>
				<svg class="edges" width={layout.width} height={layout.height} aria-hidden="true">
					{#each edges as edge (edge.id)}
						{@const a = layout.positions.get(edge.from)!}
						{@const b = layout.positions.get(edge.to)!}
						{@const bracket = edge.relation === 'chain-middleware'}
						{@const d = edgePath(a, b, bracket)}
						{@const on = focus?.edges.has(edge.id) ?? false}
						<!-- Pointer-only shortcut; the side panel lists every link with the same actions. -->
						<!-- svelte-ignore a11y_click_events_have_key_events, a11y_no_static_element_interactions -->
						<g
							class="edge"
							class:on
							class:dim={(!!focus && !on) || !!link}
							class:broken={graph.byId.get(edge.object)!.missing}
							class:bracket
							class:selected={selection?.type === 'edge' && selection.id === edge.id}
							style:--edge={edgeColor(edge)}
							onclick={() => {
								if (!swallowClick && !link) selection = { type: 'edge', id: edge.id };
							}}
						>
							<path class="hit" {d} />
							<path class="line" {d} />
							{#if edge.order && on}
								{@const p = edgePoint(a, b, a.column === b.column ? 0.5 : 0.3, bracket)}
								<circle class="badge" cx={p.x} cy={p.y} r="9" />
								<text class="badge-text" x={p.x} y={p.y}>{edge.order}</text>
							{/if}
						</g>
					{/each}
					{#if rubber}<path
							class="rubber"
							class:ok={targetVerdict?.ok}
							class:no={targetVerdict && !targetVerdict.ok}
							d={rubber}
						/>{/if}
				</svg>

				{#each layout.headers as header (header.kind)}
					{@const Icon = KIND_ICON[header.kind]}
					<div class="lane" style:left="{header.x}px" style:top="{header.y}px" style:--kind={kindColor(header.kind)}>
						<Icon size={14} aria-hidden="true" /><span>{tp('kinds.' + header.kind)}</span><span class="count numeric">
							{header.count}
						</span>
					</div>
				{/each}

				{#each nodes as node (node.id)}
					{@const p = layout.positions.get(node.id)!}
					{@const verdict = verdicts?.get(node.id)}
					{@const source = link?.from.id === node.id}
					{@const picked = selection?.type === 'node' && selection.id === node.id}
					<button
						type="button"
						class="node"
						data-node-id={node.id}
						style:left="{p.x}px"
						style:top="{p.y}px"
						style:--kind={kindColor(node.kind)}
						class:selected={picked}
						class:source
						class:can={!!verdict?.ok}
						class:missing={node.missing}
						class:match={!!matches?.has(node.id)}
						class:dim={(!!focus && !focus.nodes.has(node.id)) ||
							(!!matches && !matches.has(node.id)) ||
							(!!verdict && !verdict.ok && !source)}
						aria-label="{tp('kind.' + node.kind)} {node.name}"
						aria-pressed={picked}
						onpointerdown={(e) => nodeDown(e, node)}
						onclick={() => nodeClick(node)}
						ondblclick={() => edit(node)}
						onpointerenter={() => {
							if (!link && !press) hovered = node.id;
						}}
						onpointerleave={() => {
							if (hovered === node.id) hovered = '';
						}}
					>
						{#if p.column > 0 && node.kind !== 'entrypoint'}<span class="port in" aria-hidden="true"></span>{/if}
						<NodeCard {node} />
						{#if node.kind !== 'service' && (node.kind !== 'middleware' || rightPorts.has(node.id))}<span
								class="port out"
								aria-hidden="true"
							></span>{/if}
					</button>
				{/each}
			</div>

			{#if link}
				{@const floating = link.mode === 'drag' || !!link.target}
				<p
					class="link-hint overlay"
					class:floating
					class:ok={targetVerdict?.ok}
					class:no={targetVerdict && !targetVerdict.ok}
					style:left={floating ? `${Math.max(8, Math.min(link.px + 16, vw - 316))}px` : undefined}
					style:top={floating ? `${link.py + 18}px` : undefined}
					role="status"
				>
					{linkHint}
					{#if !floating}<button type="button" onclick={() => (link = null)}>{t('common.cancel')}</button>{/if}
				</p>
			{:else}
				<p class="help overlay">{tp('hint')}</p>
			{/if}

			<div class="zoom overlay" role="group" aria-label={tp('zoom')}>
				<button
					type="button"
					aria-label={tp('zoomOut')}
					title={tp('zoomOut')}
					onclick={() => zoomAt(1 / 1.25)}
					disabled={view.k <= MIN_ZOOM}
				>
					<Minus size={16} aria-hidden="true" />
				</button>
				<span class="numeric">{Math.round(view.k * 100)}%</span>
				<button
					type="button"
					aria-label={tp('zoomIn')}
					title={tp('zoomIn')}
					onclick={() => zoomAt(1.25)}
					disabled={view.k >= MAX_ZOOM}
				>
					<Plus size={16} aria-hidden="true" />
				</button>
				<button type="button" aria-label={tp('fit')} title={tp('fit')} onclick={fit}>
					<Maximize size={16} aria-hidden="true" />
				</button>
			</div>

			{#if selectedNode}
				{@const n = selectedNode}
				{@const page = pagePath(n)}
				<aside class="panel overlay" aria-label={tp('panel.title')}>
					<header>
						<NodeCard node={n} showKind />
						<button type="button" class="icon" aria-label={tp('panel.close')} onclick={() => (selection = null)}>
							<X size={18} aria-hidden="true" />
						</button>
					</header>
					<div class="panel-body">
						{#if n.missing}
							<Notice tone="error" message={tp('panel.missing', { kind: tp('kind.' + n.kind), name: n.name })} />
						{:else if n.external}
							<Notice message={tp('panel.external', { provider: n.provider || tp('badge.external') })} />
						{:else if n.kind === 'entrypoint'}
							<Notice message={tp('panel.entrypoint')} />
						{/if}
						{#if n.error}<Notice tone="error" message={n.error} />{/if}
						{#if !n.missing && n.health && n.kind !== 'certificate'}
							<div>
								<StatusBadge
									size="sm"
									tone={healthTone(n.health)}
									text={tp('health.' + (KNOWN_HEALTH.includes(n.health) ? n.health : 'unknown'))}
								/>
							</div>
						{/if}
						{#if facts.length}
							<dl class="facts">
								{#each facts as fact (fact.label)}<div>
										<dt>{fact.label}</dt>
										<dd class:code={fact.code}>{fact.value}</dd>
									</div>{/each}
							</dl>
						{/if}
						{#each groups as group (group.title)}
							<h3>{group.title}<span class="count numeric">{group.rows.length}</span></h3>
							<ul class="links">
								{#each group.rows as row (row.edge.id)}
									{@const Icon = KIND_ICON[row.other.kind]}
									<li>
										<button
											type="button"
											class="link-row"
											style:--kind={kindColor(row.other.kind)}
											onclick={() => go(row.other.id)}
										>
											<Icon size={15} aria-hidden="true" />
											<span class="link-name">{row.other.name}</span>
											<span class="link-kind">
												{row.edge.order ? tp('panel.position', { n: row.edge.order }) : tp('kind.' + row.other.kind)}
											</span>
										</button>
										{#if row.removable}
											<button
												type="button"
												class="icon danger"
												aria-label="{tp('panel.removeLink')}: {row.other.name}"
												title={tp('panel.removeLink')}
												onclick={() => removeLink(row.edge)}
											>
												<Unlink size={15} aria-hidden="true" />
											</button>
										{/if}
									</li>
								{/each}
							</ul>
						{/each}
						{#if !groups.length}<p class="muted">{tp('panel.unlinked')}</p>{/if}
					</div>
					<footer>
						{#if !n.missing}<Button size="sm" variant="primary" onclick={() => startPick(n)}>
								<Link2 size={14} aria-hidden="true" />{tp('panel.linkTo')}
							</Button>{/if}
						{#if editorKind(n)}<Button size="sm" onclick={() => edit(n)}>
								<Pencil size={14} aria-hidden="true" />{t('related.edit')}
							</Button>{/if}
						{#if page}<Button size="sm" variant="ghost" onclick={() => navigate(page)}>
								<ExternalLink size={14} aria-hidden="true" />{tp('panel.open')}
							</Button>{/if}
					</footer>
				</aside>
			{:else if selectedEdge}
				{@const e = selectedEdge}
				<aside class="panel overlay" aria-label={tp('panel.title')}>
					<header>
						<div class="edge-title">
							<span class="kind-label">{tp('panel.link')}</span><strong>
								{tp('edge.' + e.relation, edgeNames(e))}
							</strong>
						</div>
						<button type="button" class="icon" aria-label={tp('panel.close')} onclick={() => (selection = null)}>
							<X size={18} aria-hidden="true" />
						</button>
					</header>
					<div class="panel-body">
						{#each [e.subject, e.object] as id (id)}
							<button type="button" class="end" onclick={() => go(id)}>
								<NodeCard node={graph.byId.get(id)!} showKind />
							</button>
						{/each}
						{#if e.order}<p class="muted">{tp('panel.position', { n: e.order })}</p>{/if}
						{#if edgeRemoval && !edgeRemoval.ok}<p class="muted">
								{tp(
									'panel.cannotRemove.' +
										(edgeRemoval.error === 'readOnly'
											? 'readOnly'
											: edgeRemoval.error === 'required'
												? 'required'
												: 'other'),
								)}
							</p>{/if}
					</div>
					{#if edgeRemoval?.ok}
						<footer>
							<Button size="sm" variant="danger" onclick={() => removeLink(e)}>
								<Unlink size={14} aria-hidden="true" />{tp('panel.removeLink')}
							</Button>
						</footer>
					{/if}
				</aside>
			{/if}
		{/if}
	</div>
</section>

{#if connecting}
	{#key connecting}
		<ConnectDialog
			connection={connecting.connection}
			op={connecting.op}
			onclose={() => (connecting = null)}
			onapplied={() => load(true)}
		/>
	{/key}
{/if}

{#if editor}
	{#key `${editor.kind}:${editor.mode}:${editor.name}`}
		{@const props = {
			mode: editor.mode,
			name: editor.name,
			navigate: editorNavigate,
			onchanged: () => {
				void load(true);
			},
		}}
		{#if editor.kind === 'route'}<RouteDialog {...props} protocol={editor.protocol ?? 'http'} />
		{:else if editor.kind === 'service'}<ServiceDialog {...props} protocol={editor.protocol ?? 'http'} />
		{:else if editor.kind === 'middleware'}<MiddlewareDialog
				{...props}
				protocol={editor.protocol === 'tcp' ? 'tcp' : 'http'}
			/>
		{:else if editor.kind === 'entrypoint'}<EntryPointDialog {...props} />
		{:else if editor.kind === 'chain'}<ChainDialog {...props} />
		{:else if editor.kind === 'domain'}<DomainDialog {...props} />
		{:else}<ResolverDialog {...props} />{/if}
	{/key}
{/if}

<style>
	.notices {
		display: flex;
		flex-direction: column;
		gap: 12px;
		margin-bottom: 16px;
	}
	.notices:empty {
		display: none;
	}

	.map {
		border: 1px solid var(--border);
		border-top-color: var(--connection-border);
		border-radius: var(--radius-card);
		background: var(--surface);
		overflow: hidden;
	}
	.toolbar {
		display: flex;
		flex-wrap: wrap;
		align-items: center;
		gap: 10px 14px;
		padding: 10px 12px 10px 16px;
		border-bottom: 1px solid var(--border);
	}
	.title-block {
		display: flex;
		align-items: baseline;
		min-width: 0;
		margin-right: 6px;
	}
	.map-title {
		margin: 0;
		font-size: 17px;
		line-height: 24px;
		font-weight: 600;
		letter-spacing: -0.01em;
		white-space: nowrap;
	}
	.actions {
		display: flex;
		align-items: center;
		gap: 8px;
		margin-left: auto;
	}
	.search {
		display: flex;
		align-items: center;
		gap: 8px;
		flex: 0 1 280px;
		min-width: 180px;
		height: var(--control-h);
		padding: 0 12px;
		border: 1px solid var(--outline);
		border-radius: var(--radius-control);
		background: var(--surface);
		color: var(--text-muted);
	}
	.search:focus-within {
		outline: 2px solid var(--focus-ring);
		outline-offset: 2px;
	}
	.search input {
		flex: 1;
		min-width: 0;
		border: 0;
		outline: 0;
		background: transparent;
		color: var(--text);
		font-size: 14px;
	}
	.legend {
		top: 12px;
		left: 12px;
		right: 12px;
		display: flex;
		flex-wrap: wrap;
		gap: 6px;
		z-index: 1;
		pointer-events: none;
	}
	.legend .chip {
		pointer-events: auto;
	}
	.ownership {
		display: inline-flex;
		border: 1px solid var(--border);
		border-radius: 8px;
		overflow: hidden;
	}
	.seg {
		padding: 4px 10px;
		font-size: 12px;
		line-height: 18px;
		color: var(--text-muted);
		background: transparent;
		border: 0;
		cursor: pointer;
	}
	.seg + .seg {
		border-left: 1px solid var(--border);
	}
	.seg[aria-pressed='true'] {
		color: var(--text);
		background: var(--surface-2, var(--accent-soft));
		font-weight: 500;
	}
	.chip {
		display: inline-flex;
		align-items: center;
		gap: 6px;
		height: 30px;
		padding: 0 11px;
		border: 1px solid var(--border);
		border-radius: 999px;
		background: var(--surface);
		color: var(--text);
		font-size: 13px;
		font-weight: 500;
		cursor: pointer;
		box-shadow: 0 1px 3px rgb(0 0 0 / 0.08);
	}
	.chip :global(svg) {
		color: var(--kind);
	}
	.chip:hover {
		background: var(--surface-muted);
	}
	.chip[aria-pressed='false'] {
		color: var(--text-muted);
		border-style: dashed;
		background: var(--canvas);
		box-shadow: none;
	}
	.chip[aria-pressed='false'] :global(svg) {
		color: var(--text-muted);
	}
	.chip[aria-pressed='false'] .chip-label {
		text-decoration: line-through;
	}
	.count {
		color: var(--text-muted);
		font-size: 12px;
		font-weight: 500;
	}

	.viewport {
		position: relative;
		overflow: hidden;
		touch-action: none;
		user-select: none;
		-webkit-user-select: none;
		cursor: grab;
		background-color: var(--canvas);
		background-image: radial-gradient(
			circle,
			color-mix(in srgb, var(--outline) 38%, transparent) 1px,
			transparent 1.4px
		);
	}
	.viewport:active {
		cursor: grabbing;
	}
	.viewport.linking {
		cursor: crosshair;
	}
	.world {
		position: absolute;
		left: 0;
		top: 0;
		transform-origin: 0 0;
	}
	.edges {
		position: absolute;
		left: 0;
		top: 0;
		overflow: visible;
	}

	.edge {
		cursor: pointer;
	}
	.edge .hit {
		fill: none;
		stroke: transparent;
		stroke-width: 14;
		pointer-events: stroke;
	}
	.edge .line {
		fill: none;
		stroke: var(--edge);
		stroke-width: 1.5;
		stroke-opacity: 0.6;
		pointer-events: none;
		transition: stroke-opacity 0.12s;
	}
	.edge:hover .line,
	.edge.on .line,
	.edge.selected .line {
		stroke-opacity: 1;
		stroke-width: 2.25;
	}
	.edge.selected .line {
		stroke-width: 3;
	}
	.edge.dim .line {
		stroke-opacity: 0.12;
	}
	.edge.bracket .line {
		stroke-dasharray: 2 5;
		stroke-linecap: round;
		stroke-width: 2;
		stroke-opacity: 0.85;
	}
	.edge.bracket.on .line,
	.edge.bracket.selected .line {
		stroke-width: 2.75;
	}
	.edge.bracket.dim .line {
		stroke-opacity: 0.14;
	}
	.edge.broken .line {
		stroke: var(--danger-text);
		stroke-dasharray: 5 5;
	}
	.badge {
		fill: var(--surface);
		stroke: var(--edge);
		stroke-width: 1.5;
	}
	.badge-text {
		fill: var(--text);
		font-size: 11px;
		font-weight: 600;
		text-anchor: middle;
		dominant-baseline: central;
		pointer-events: none;
	}
	.rubber {
		fill: none;
		stroke: var(--connection);
		stroke-width: 2;
		stroke-dasharray: 6 6;
		pointer-events: none;
		animation: march 0.5s linear infinite;
	}
	.rubber.ok {
		stroke: var(--success-text);
		stroke-dasharray: none;
		animation: none;
	}
	.rubber.no {
		stroke: var(--danger-text);
	}
	@keyframes march {
		to {
			stroke-dashoffset: -12;
		}
	}
	@media (prefers-reduced-motion: reduce) {
		.rubber {
			animation: none;
		}
	}

	.lane {
		position: absolute;
		display: flex;
		align-items: center;
		gap: 6px;
		width: var(--node-w);
		height: 24px;
		font-size: 12px;
		line-height: 16px;
		font-weight: 600;
		letter-spacing: 0.05em;
		text-transform: uppercase;
		color: var(--text-muted);
		white-space: nowrap;
	}
	.lane :global(svg) {
		color: var(--kind);
	}

	.node {
		position: absolute;
		display: flex;
		align-items: center;
		width: var(--node-w);
		height: var(--node-h);
		padding: 0 12px;
		border: 1px solid var(--border);
		border-left: 3px solid var(--kind);
		border-radius: var(--radius-card);
		background: var(--surface);
		color: var(--text);
		font: inherit;
		cursor: pointer;
		box-shadow: 0 1px 2px rgb(0 0 0 / 0.08);
		transition:
			opacity 0.12s,
			box-shadow 0.12s,
			border-color 0.12s;
		touch-action: none;
	}
	/* Compact on the map: more of each name and address fits on a node. */
	.node :global(.name) {
		font-size: 13.5px;
		line-height: 19px;
	}
	.node :global(.sub) {
		font-size: 11.5px;
		line-height: 16px;
	}
	.node :global(.glyph) {
		width: 30px;
		height: 30px;
	}
	.node:hover {
		border-color: var(--outline);
		border-left-color: var(--kind);
		box-shadow: 0 4px 14px rgb(0 0 0 / 0.14);
	}
	.node.match {
		box-shadow: 0 0 0 2px var(--accent);
	}
	.node.selected {
		border-color: var(--kind);
		box-shadow: 0 0 0 3px color-mix(in srgb, var(--kind) 28%, transparent);
	}
	.node.dim {
		opacity: 0.28;
	}
	.node.missing {
		border-style: dashed;
		border-color: var(--danger-text);
		border-left-style: solid;
	}
	.node.source {
		border-color: var(--connection);
		box-shadow: 0 0 0 3px color-mix(in srgb, var(--connection) 30%, transparent);
	}
	.node.can {
		border-color: var(--success-text);
		border-left-color: var(--kind);
		box-shadow: 0 0 0 2px color-mix(in srgb, var(--success-text) 35%, transparent);
		cursor: copy;
	}
	.linking .node:not(.can):not(.source) {
		cursor: not-allowed;
	}
	.port {
		position: absolute;
		top: 50%;
		width: 9px;
		height: 9px;
		margin-top: -4.5px;
		border: 1.5px solid var(--kind);
		border-radius: 50%;
		background: var(--surface);
	}
	.port.in {
		left: -7px;
	}
	.port.out {
		right: -5px;
	}
	.node:hover .port,
	.node.selected .port,
	.node.source .port {
		background: var(--kind);
	}

	.overlay {
		position: absolute;
		cursor: default;
		user-select: text;
		-webkit-user-select: text;
	}
	.state {
		position: absolute;
		inset: 0;
		display: flex;
		flex-direction: column;
		align-items: center;
		justify-content: center;
		gap: 8px;
		margin: 0;
		padding: 24px;
		text-align: center;
		color: var(--text-muted);
	}
	.state h2 {
		margin: 0;
		font-size: 20px;
		line-height: 28px;
		color: var(--text);
	}
	.state p {
		margin: 0;
		font-size: 14px;
	}
	.state-actions {
		display: flex;
		gap: 12px;
		margin-top: 12px;
	}

	.help,
	.link-hint {
		left: 50%;
		bottom: 14px;
		transform: translateX(-50%);
		margin: 0;
		max-width: calc(100% - 240px);
		padding: 6px 12px;
		border: 1px solid var(--border);
		border-radius: 999px;
		background: var(--surface);
		color: var(--text-muted);
		font-size: 12px;
		line-height: 18px;
		text-align: center;
		box-shadow: 0 2px 8px rgb(0 0 0 / 0.12);
		pointer-events: none;
	}
	.link-hint {
		display: flex;
		align-items: center;
		gap: 10px;
		color: var(--text);
		border-color: var(--connection-border);
		background: var(--connection-soft);
		pointer-events: auto;
	}
	.link-hint.floating {
		bottom: auto;
		transform: none;
		max-width: 300px;
		border-radius: var(--radius-control);
		text-align: left;
		pointer-events: none;
		z-index: 3;
	}
	.link-hint.ok {
		border-color: color-mix(in srgb, var(--success-text) 40%, transparent);
		background: var(--success-bg);
		color: var(--success-text);
	}
	.link-hint.no {
		border-color: color-mix(in srgb, var(--danger-text) 40%, transparent);
		background: var(--danger-bg);
		color: var(--danger-text);
	}
	.link-hint button {
		border: 0;
		background: transparent;
		padding: 0;
		color: var(--connection);
		font: inherit;
		font-weight: 600;
		cursor: pointer;
	}

	.zoom {
		left: 14px;
		bottom: 14px;
		display: flex;
		align-items: center;
		gap: 2px;
		padding: 3px;
		border: 1px solid var(--border);
		border-radius: var(--radius-control);
		background: var(--surface);
		box-shadow: 0 2px 8px rgb(0 0 0 / 0.12);
	}
	.zoom button,
	.icon {
		display: grid;
		place-items: center;
		width: 32px;
		height: 32px;
		border: 0;
		border-radius: 4px;
		background: transparent;
		color: var(--text-muted);
		cursor: pointer;
		flex: none;
	}
	.zoom button:hover:not(:disabled),
	.icon:hover {
		background: var(--surface-muted);
		color: var(--text);
	}
	.zoom button:disabled {
		opacity: 0.45;
		cursor: default;
	}
	.zoom span {
		min-width: 44px;
		text-align: center;
		font-size: 12px;
		color: var(--text-muted);
	}
	.icon.danger:hover {
		background: var(--danger-bg);
		color: var(--danger-text);
	}

	.panel {
		top: 12px;
		right: 12px;
		bottom: 12px;
		width: 324px;
		max-width: calc(100% - 24px);
		display: flex;
		flex-direction: column;
		border: 1px solid var(--border);
		border-top: 2px solid var(--connection);
		border-radius: var(--radius-card);
		background: var(--surface);
		box-shadow: 0 12px 32px rgb(0 0 0 / 0.22);
		overflow: hidden;
		z-index: 2;
	}
	.panel header {
		display: flex;
		align-items: flex-start;
		gap: 8px;
		padding: 14px 10px 14px 16px;
		border-bottom: 1px solid var(--border);
	}
	.panel-body {
		flex: 1;
		min-height: 0;
		overflow-y: auto;
		overscroll-behavior: contain;
		display: flex;
		flex-direction: column;
		gap: 14px;
		padding: 16px;
	}
	.panel footer {
		display: flex;
		flex-wrap: wrap;
		gap: 8px;
		padding: 12px 16px;
		border-top: 1px solid var(--border);
		background: var(--canvas);
	}
	.panel h3 {
		display: flex;
		align-items: center;
		gap: 8px;
		margin: 4px 0 -4px;
		font-size: 12px;
		line-height: 16px;
		font-weight: 600;
		letter-spacing: 0.05em;
		text-transform: uppercase;
		color: var(--text-muted);
	}
	.muted {
		margin: 0;
		color: var(--text-muted);
		font-size: 13px;
		line-height: 19px;
	}
	.facts {
		display: flex;
		flex-direction: column;
		gap: 10px;
		margin: 0;
	}
	.facts dt {
		font-size: 12px;
		line-height: 16px;
		color: var(--text-muted);
	}
	.facts dd {
		margin: 2px 0 0;
		font-size: 13px;
		line-height: 19px;
		overflow-wrap: anywhere;
		white-space: pre-line;
	}
	.facts dd.code {
		font-family: ui-monospace, SFMono-Regular, Consolas, monospace;
		font-size: 12px;
	}
	.links {
		list-style: none;
		margin: 0;
		padding: 0;
		display: flex;
		flex-direction: column;
		gap: 4px;
	}
	.links li {
		display: flex;
		align-items: center;
		gap: 4px;
	}
	.link-row {
		display: flex;
		align-items: center;
		gap: 8px;
		flex: 1;
		min-width: 0;
		min-height: 34px;
		padding: 4px 8px;
		border: 1px solid var(--border);
		border-radius: var(--radius-control);
		background: var(--surface);
		color: var(--text);
		font: inherit;
		font-size: 13px;
		text-align: left;
		cursor: pointer;
	}
	.link-row:hover {
		background: var(--surface-muted);
		border-color: var(--outline);
	}
	.link-row :global(svg) {
		flex: none;
		color: var(--kind);
	}
	.link-name {
		flex: 1;
		min-width: 0;
		overflow: hidden;
		text-overflow: ellipsis;
		white-space: nowrap;
		font-weight: 500;
	}
	.link-kind {
		flex: none;
		color: var(--text-muted);
		font-size: 12px;
	}
	.edge-title {
		display: flex;
		flex-direction: column;
		gap: 2px;
		flex: 1;
		min-width: 0;
		font-size: 14px;
		line-height: 20px;
		overflow-wrap: anywhere;
	}
	.kind-label {
		font-size: 11px;
		line-height: 14px;
		letter-spacing: 0.04em;
		text-transform: uppercase;
		font-weight: 600;
		color: var(--connection);
	}
	.end {
		display: block;
		width: 100%;
		padding: 10px 12px;
		border: 1px solid var(--border);
		border-radius: var(--radius-control);
		background: var(--canvas);
		font: inherit;
		cursor: pointer;
	}
	.end:hover {
		border-color: var(--outline);
	}
	.menu-glyph {
		display: grid;
		place-items: center;
	}

	@media (max-width: 767px) {
		.help {
			display: none;
		}
		.panel {
			top: auto;
			left: 8px;
			right: 8px;
			bottom: 8px;
			width: auto;
			max-height: 62%;
		}
		.search {
			flex-basis: 100%;
		}
	}
</style>

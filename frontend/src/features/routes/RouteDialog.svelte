<script lang="ts">
	/**
	 * Route dialog (create / edit / view) on the direct write protocol.
	 *
	 *   Route tab        → name + how requests are matched (managed domain, explicit hosts, or a hand-written rule)
	 *   Traffic tab      → entrypoints, target service, priority
	 *   TLS & middleware → TLS (only when the host is not bound to a managed domain) and ordered middlewares
	 *   Review tab       → PUT /routers/{name}?dry_run=true + If-Match returns the preview (nothing saved)
	 *   Apply            → the same PUT without dry_run, with one Idempotency-Key per attempt
	 */
	import { onMount, onDestroy, tick } from 'svelte';
	import { stringify } from 'yaml';
	import { Route as RouteIcon, CircleAlert, ArrowUp, ArrowDown, Trash2 } from '@lucide/svelte';
	import ResourceDialog from '../../shared/ui/ResourceDialog.svelte';
	import Field from '../../shared/ui/Field.svelte';
	import Tabs from '../../shared/ui/Tabs.svelte';
	import Button from '../../shared/ui/Button.svelte';
	import Notice from '../../shared/ui/Notice.svelte';
	import Checkbox from '../../shared/ui/Checkbox.svelte';
	import SearchableSelect from '../../shared/ui/SearchableSelect.svelte';
	import SearchableMultiSelect from '../../shared/ui/SearchableMultiSelect.svelte';
	import SummaryList from '../../shared/ui/SummaryList.svelte';
	import CodePreview from '../../shared/ui/CodePreview.svelte';
	import RelativeTime from '../../shared/ui/RelativeTime.svelte';
	import { navigationGuard } from '../../lib/stores/navigationGuard';
	import { needsManualRestart, newIdempotencyKey, operationOf, announceApplied } from '../../core/mutations';
	import { operationTone } from '../changes/models';
	import type { Operation, OperationPreview } from '../../core/types';
	import { listChains, listDomains, listEntryPointInfo, listMiddlewares, listResolvers, listRouters, listServices, putRouter, type EntryPointInfo, type NamedRef } from './api';
	import { MAX_MIDDLEWARES, NOOP_SERVICE, ROUTER_BASE, formFrom, isExternalRef, isNoBackend, newForm, previewHosts, previewRule, referenceBase, routerDefinition, routerProtocolOf, toInput, type MatchMode, type RouteForm, type RouterProtocol, type RouterResource } from './models';
	import { FIELD_TAB, routeErrors, type RouteField } from './validation';
	import { ApiError, NetworkError } from '../../shared/api/client';
	import { errorMessage } from '../../shared/api/errorMessage';
	import { t } from '../../shared/localization/i18n';

	let { mode = 'create', name = '', protocol = 'http', routers = undefined, listError = '', navigate, onchanged }: {
		mode?: string; name?: string;
		/** The protocol of a new router, and of the pickers to load; an existing router's own protocol wins once it is read. */
		protocol?: RouterProtocol;
		/** The page's list: the dialog reads names and the router to edit from it instead of asking again. `null` while the page is still loading; left out, the dialog reads the list itself (the map opens it that way). */
		routers?: RouterResource[] | null; listError?: string;
		navigate: (p: string) => void; onchanged?: () => void
	} = $props();

	const r = (key: string, args?: Record<string, string | number>) => t('routes.' + key, args);
	const rr = (key: string, args?: Record<string, string | number>) => t('routeV2.' + key, args);
	const rv2 = (key: string, args?: Record<string, string | number>) => t('resolverV2.' + key, args);

	const creating = $derived(mode === 'create');
	const viewing = $derived(mode === 'view');
	const TABS = { route: 'route-route', traffic: 'route-traffic', tls: 'route-tls', review: 'route-review' } as const;

	// The dialog is keyed by its protocol (the page remounts it), so the initial value is the right one.
	// svelte-ignore state_referenced_locally
	let form = $state<RouteForm>(newForm(protocol));
	const parent = $derived(ROUTER_BASE[form.protocol]);
	const tcp = $derived(form.protocol === 'tcp');
	const udp = $derived(form.protocol === 'udp');
	const http = $derived(form.protocol === 'http');
	let existing = $state<RouterResource[]>([]);
	let listReady = $state(false);
	let pickersDone = $state(false);
	let loadError = $state('');
	// Viewing needs nothing but the router itself; only the editor loads the reference pickers.
	const loading = $derived(!loadError && !(listReady && (viewing || pickersDone)));
	let busy = $state(false);
	let suspended = $state(false);
	let error = $state('');
	// A UDP router has nothing to match: its dialog starts at the traffic tab.
	// svelte-ignore state_referenced_locally
	let step = $state<string>(protocol === 'udp' ? TABS.traffic : TABS.route);
	const firstStep = $derived(udp ? TABS.traffic : TABS.route);
	let baseline = $state('');
	let submitted = $state(false);
	let fields = $state<Partial<Record<RouteField, string>>>({});

	// Reference pickers. Each is loaded independently so one failing endpoint does not hide the others.
	let domains = $state<NamedRef[]>([]);
	let services = $state<NamedRef[]>([]);
	let middlewares = $state<NamedRef[]>([]);
	let chains = $state<NamedRef[]>([]);
	let resolvers = $state<NamedRef[]>([]);
	let entryPoints = $state<EntryPointInfo[]>([]);
	let pickersFailed = $state<string[]>([]);
	let domainsLoaded = $state(false);

	let conflict = $state(false);
	let staged = $state<RouterResource | null>(null);
	let review = $state<OperationPreview | null>(null);
	let reviewError = $state('');
	let applyKey = $state('');
	let uncertain = $state(false);
	let result = $state<Operation | null>(null);
	let manualRestart = $state(false);

	let disposed = false;
	const controller = new AbortController();

	/** Managed names are unique whatever the protocol; Traefik's own routers may share a name across protocols, so the URL's protocol breaks the tie. */
	const findRouter = (all: RouterResource[]) => all.find((d) => d.name === name && routerProtocolOf(d.config) === protocol) ?? all.find((d) => d.name === name);
	const current = $derived(findRouter(existing));
	const version = () => current?.version ?? 0;
	const editable = $derived(!viewing && (creating || (!!current && !current.read_only)));
	const locked = $derived(busy || !!result);
	const serverErrors = $derived(review?.validation_errors ?? []);
	const dirty = $derived(editable && !!baseline && JSON.stringify($state.snapshot(form)) !== baseline && !result);

	const domain = $derived(domains.find((d) => d.name === form.domainName));
	const hosts = $derived(previewHosts(form, domain?.config.main));
	const rule = $derived(previewRule(form, domain?.config.main));

	const messages = $derived({
		noBackendNeedsMiddleware: rr('noBackendNeedsMiddleware'), hostsNeedTls: rr('hostsNeedTls'),
		invalidName: r('invalidName'), nameTaken: rr('nameTaken'), domainRequired: rr('domainRequired'), invalidSubdomain: r('invalidSubdomain'),
		invalidHosts: rr('invalidHosts'), invalidPathPrefix: r('invalidPathPrefix'), ruleRequired: rr('ruleRequired'),
		tooManyEntryPoints: rr('tooManyEntryPoints'), serviceRequired: r('invalidService'), duplicateMiddleware: rr('duplicateMiddleware'),
		tooManyMiddlewares: rr('tooManyMiddlewares'), invalidPriority: rr('invalidPriority'), invalidTlsOptions: rr('invalidTlsOptions'),
		entryPointsRequired: rr('entryPointsRequired')
	});
	const context = $derived({ creating, existingNames: existing.map((d) => d.name), domainNames: domainsLoaded ? domains.map((d) => d.name) : null });
	const count = (tab: 'route' | 'traffic' | 'tls') => (submitted ? Object.keys(fields).filter((k) => FIELD_TAB[k as RouteField] === tab).length : 0);
	const tabs = $derived.by(() => {
		const traffic = { id: TABS.traffic, label: r('tabTraffic'), errorCount: count('traffic') + (udp ? count('route') : 0), errorLabel: rr('fixErrors') };
		const review = { id: TABS.review, label: r('review') };
		if (udp) return [traffic, review];
		return [
			{ id: TABS.route, label: r('tabRoute'), errorCount: count('route'), errorLabel: rr('fixErrors') },
			traffic,
			{ id: TABS.tls, label: r('tabTlsMw'), errorCount: count('tls'), errorLabel: rr('fixErrors') },
			review
		];
	});
	/** The editor's steps in order; Back and Continue walk this list. */
	const order = $derived(tabs.map((x) => x.id));
	const stepAfter = (delta: number) => order[Math.max(0, Math.min(order.length - 1, order.indexOf(step as (typeof order)[number]) + delta))];
	const tabFor = (key: 'route' | 'traffic' | 'tls') => (udp && key === 'route' ? TABS.traffic : TABS[key]);

	/** Keep the saved value selectable even if the picker no longer lists it (external or removed references). */
	const withCurrent = (options: { value: string; label: string; description?: string; badge?: string }[], value: string) =>
		value && !options.some((o) => o.value === value) ? [{ value, label: value, badge: isExternalRef(value) ? rr('externalBadge') : undefined }, ...options] : options;

	const domainOptions = $derived(withCurrent(domains.map((d) => ({ value: d.name, label: d.config.main as string, description: d.name })), form.domainName));
	const serviceOptions = $derived(withCurrent([
		// Only an HTTP router can answer from a middleware: TCP and UDP routers always need a service.
		...(http ? [{ value: NOOP_SERVICE, label: rr('noBackend'), description: rr('noBackendDescription'), badge: undefined as string | undefined }] : []),
		// Traefik itself reports noop@internal as a service; it is already offered above as "No backend".
		// A router only uses services of its own protocol.
		...services.filter((s) => s.name !== NOOP_SERVICE && (s.config?.protocol ?? 'http') === form.protocol).map((s) => ({
			value: s.name, label: s.name,
			description: s.origin === 'managed' ? ((s.config.servers as { url?: string; address?: string }[] | null)?.[0]?.url ?? (s.config.servers as { address?: string }[] | null)?.[0]?.address ?? '') : s.provider,
			badge: s.origin === 'external' ? rr('externalBadge') : undefined
		}))
	], form.service));
	const middlewareOptions = $derived(
		[...middlewares.filter((m) => (m.config?.protocol ?? 'http') === form.protocol).map((m) => ({ value: m.name, label: m.name, description: (m.config.type as string) ?? '', badge: m.origin === 'external' ? rr('externalBadge') : undefined })),
		 ...(http ? chains : []).map((c) => ({ value: c.name, label: c.name, description: rr('chainBadge'), badge: undefined as string | undefined }))]
			.filter((o) => !form.middlewares.includes(o.value))
	);
	const resolverOptions = $derived([{ value: '', label: rr('tlsResolverNone') }, ...withCurrent(resolvers.map((x) => ({ value: x.name, label: x.name })), form.tlsResolver).filter((o) => o.value)]);
	/** A UDP router can only listen on a UDP entrypoint, the others on a TCP one. */
	const kindEntryPoints = $derived(entryPoints.filter((e) => (udp ? e.protocol === 'udp' : e.protocol !== 'udp')).map((e) => e.name));
	/** Entrypoints HTTP routers already listen on (web and websecure are TCP entrypoints that carry HTTP). */
	const httpEntryPoints = $derived(new Set(existing.filter((x) => routerProtocolOf(x.config) === 'http' && x.name !== name).flatMap((x) => x.config.entry_points ?? [])));
	const entryPointOptions = $derived([...new Set([...kindEntryPoints, ...form.entryPoints])].map((e) => ({
		value: e, label: e, description: tcp && httpEntryPoints.has(e) ? rr('entryPointSharedHttp') : undefined
	})));
	/** A TCP router matching every connection comes before HTTP routers: on a shared entrypoint they stop answering. */
	const takenOver = $derived(tcp && form.mode === 'any' ? form.entryPoints.filter((e) => httpEntryPoints.has(e)) : []);
	/** Services the router can use: its own protocol, without Traefik's built-in no-backend one. */
	const kindServices = $derived(services.filter((s) => s.name !== NOOP_SERVICE && (s.config?.protocol ?? 'http') === form.protocol));

	$effect(() => {
		navigationGuard.setDirty(dirty);
		return () => navigationGuard.setDirty(false);
	});

	onMount(() => {
		const unsub = navigationGuard.subscribe(() => (suspended = navigationGuard.confirmPending));
		if (!viewing) void loadPickers();
		return unsub;
	});
	onDestroy(() => { disposed = true; controller.abort(); });

	async function loadPickers(kind: RouterProtocol = protocol) {
		const sig = controller.signal;
		// Only what the protocol uses: a UDP router needs no domains, middlewares or resolvers.
		const jobs: [string, Promise<unknown>][] = [];
		if (kind !== 'udp') jobs.push(['domains', listDomains(sig).then((v) => { domains = v; domainsLoaded = true; })]);
		jobs.push(['services', listServices(sig).then((v) => { services = v; })]);
		// Chains group HTTP middlewares only; a TCP router picks from the TCP middlewares.
		if (kind === 'http') jobs.push(['middlewares', Promise.all([listMiddlewares(sig), listChains(sig)]).then(([m, c]) => { middlewares = m; chains = c; })]);
		if (kind === 'tcp') jobs.push(['middlewares', listMiddlewares(sig).then((m) => { middlewares = m; chains = []; })]);
		if (kind !== 'udp') jobs.push(['resolvers', listResolvers(sig).then((v) => { resolvers = v; })]);
		jobs.push(['entrypoints', listEntryPointInfo(sig).then((v) => { entryPoints = v; })]);
		const settled = await Promise.allSettled(jobs.map(([, p]) => p));
		if (disposed) return;
		pickersFailed = jobs.filter((_, i) => settled[i].status === 'rejected').map(([k]) => k);
		pickersDone = true;
	}

	// The list comes with the page: nothing is fetched for it here unless the dialog was opened without one.
	let initialised = false;
	$effect(() => {
		if (initialised) return;
		if (routers) { initialised = true; init(routers); }
		else if (listError) { initialised = true; loadError = listError; }
		else if (routers === undefined) { initialised = true; void readList(); }
	});

	/** Only for a dialog opened without a page list. */
	async function readList() {
		try {
			init(await listRouters(controller.signal));
		} catch (e) {
			if (!disposed) loadError = errorMessage(e, r('requestFailed'));
		}
	}

	/** The page owns the list: ask it to read again, then start over from what it returns. */
	function retry() {
		loadError = '';
		if (routers === undefined) { initialised = true; void readList(); return; }
		initialised = false; onchanged?.();
	}

	function init(all: RouterResource[]) {
		try {
			existing = all;
			if (!creating) {
				const item = findRouter(all);
				if (!item) throw new Error(r('requestFailed'));
				form = formFrom(item.name, item.config);
				if (!viewing && form.protocol !== protocol) void loadPickers(form.protocol);
				step = form.protocol === 'udp' ? TABS.traffic : step;
			}
			baseline = JSON.stringify($state.snapshot(form));
			listReady = true;
		} catch (e) {
			loadError = errorMessage(e, r('requestFailed'));
		}
	}

	/**
	 * Steps whose errors are shown: Continue checks the steps already walked (null = every step, for
	 * the review). A router without a backend gets its middleware on a later step, so that one waits.
	 */
	let scope = $state<string[] | null>(null);
	const validateNow = () => {
		const all = routeErrors($state.snapshot(form), context, messages);
		if (!scope) return all;
		const shown = scope;
		return Object.fromEntries(Object.entries(all).filter(([k]) =>
			shown.includes(tabFor(FIELD_TAB[k as RouteField])) && !(k === 'service' && isNoBackend(form.service)))) as typeof all;
	};

	function invalidate() {
		review = null; staged = null; reviewError = ''; applyKey = ''; uncertain = false; error = ''; conflict = false;
		if (submitted) fields = validateNow();
	}

	/** Moves to another step; going forward first checks the steps left behind. */
	async function goTo(id: string) {
		const from = order.indexOf(step as (typeof order)[number]);
		const to = order.indexOf(id as (typeof order)[number]);
		if (to > from) {
			const walked = order.slice(0, to) as string[];
			scope = scope ? [...new Set([...scope, ...walked])] : walked;
			submitted = true;
			fields = validateNow();
			if (Object.keys(fields).length) { await focusFirstError(); return; }
		}
		step = id;
	}

	function chooseMode(value: string) {
		const next = value as MatchMode;
		if (tcp && next !== form.mode) {
			// A TCP hostname is read from the TLS handshake (SNI), so matching one turns TLS on; matching
			// every connection has no name to read, so it turns TLS off.
			if (next === 'any') { form.tlsEnabled = false; form.tlsPassthrough = false; }
			else if (form.mode === 'any') form.tlsEnabled = true;
		}
		form.mode = next; invalidate();
	}
	function addMiddleware(ref: string) {
		if (!ref || form.middlewares.length >= MAX_MIDDLEWARES) return;
		form.middlewares = [...form.middlewares, ref]; invalidate();
	}
	function moveMiddleware(index: number, delta: number) {
		const next = [...form.middlewares];
		[next[index], next[index + delta]] = [next[index + delta], next[index]];
		form.middlewares = next; invalidate();
	}
	function removeMiddleware(index: number) { form.middlewares = form.middlewares.filter((_, i) => i !== index); invalidate(); }

	function validate(): boolean {
		scope = null;
		submitted = true;
		fields = validateNow();
		return Object.keys(fields).length === 0;
	}

	async function focusFirstError() {
		const key = Object.keys(fields)[0] as RouteField | undefined;
		if (!key) return;
		step = tabFor(FIELD_TAB[key]);
		await tick();
		const target = document.querySelector<HTMLElement>('.resource-dialog [aria-invalid="true"]');
		target?.focus();
		target?.scrollIntoView({ block: 'nearest' });
	}

	async function selectStep(id: string) {
		if (busy || result || id === step) return;
		if (id === TABS.review) { await stageAndReview(); return; }
		await goTo(id);
	}

	function describeFailure(e: unknown, fallback: string) {
		if (e instanceof ApiError && e.code === 'ERR_VERSION_CONFLICT') { conflict = true; return rv2('conflict'); }
		const base = errorMessage(e, fallback);
		return e instanceof ApiError && e.operationId ? `${base} (${rv2('operation')} ${e.operationId})` : base;
	}

	async function stageAndReview() {
		if (busy || !editable) return;
		if (!validate()) { await focusFirstError(); return; }
		busy = true; error = ''; conflict = false;
		try {
			const preview = await putRouter(form.name, toInput($state.snapshot(form)), version(), { dryRun: true, signal: controller.signal });
			if (disposed) return;
			step = TABS.review;
			review = preview.preview ?? null; applyKey = newIdempotencyKey();
		} catch (e) {
			if (disposed) return;
			if (e instanceof ApiError && (e.status === 422 || e.status === 400)) step = firstStep;
			error = describeFailure(e, rr('stageFailed'));
		} finally {
			if (!disposed) busy = false;
		}
	}

	async function refreshVersion() {
		busy = true;
		try {
			existing = await listRouters(controller.signal);
			conflict = false; error = '';
		} catch (e) {
			error = describeFailure(e, rv2('reviewFailed'));
		} finally {
			if (!disposed) busy = false;
		}
	}

	async function apply() {
		if (!review || busy || serverErrors.length) return;
		busy = true; error = '';
		try {
			const saved = await putRouter(form.name, toInput($state.snapshot(form)), version(), { idempotencyKey: applyKey, signal: controller.signal });
			if (disposed) return;
			result = operationOf(saved, t('writeV2.noChange')); manualRestart = needsManualRestart(saved); uncertain = false; staged = saved.resource ?? null;
			navigationGuard.setDirty(false);
			onchanged?.();
			// Applied: back to the table; the outcome is announced as a flash.
			if (announceApplied(result, manualRestart, form.name)) { navigate(parent); return; }
		} catch (e) {
			if (disposed) return;
			if (e instanceof NetworkError) {
				uncertain = true;
				error = rv2('applyUncertain');
			} else {
				if (e instanceof ApiError && [404, 409, 410, 412, 422, 428].includes(e.status)) { review = null; applyKey = ''; uncertain = false; step = firstStep; }
				error = describeFailure(e, rv2('applyFailed'));
			}
		} finally {
			if (!disposed) busy = false;
		}
	}

	function close() {
		if (busy) return;
		navigationGuard.requestNavigation(() => {
			if (staged || result) onchanged?.();
			navigate(parent);
		});
	}

	const tlsLabel = (c: RouterResource['config']) => !c.tls ? (routerProtocolOf(c) === 'tcp' ? rr('plainTcp') : rr('httpOnly'))
		: c.tls.passthrough ? rr('tlsPassthroughValue')
		: c.tls.cert_resolver ? rr('httpsResolver', { name: c.tls.cert_resolver })
		: routerProtocolOf(c) === 'tcp' ? rr('tlsTerminated') : rr('https');
	const domainTlsText = (d: NamedRef) => d.config.method === 'resolver' ? rr('domainTlsResolver', { name: d.config.resolver }) : rr('domainTlsFiles', { name: d.config.certificate });
	const SERVICE_BASE = { http: '/services', tcp: '/tcp-services', udp: '/udp-services' } as const;
	const serviceLink = (ref: string, p: RouterProtocol = form.protocol) => (isExternalRef(ref) ? '' : SERVICE_BASE[p] + '/' + encodeURIComponent(referenceBase(ref)));
	const stagedConfig = $derived(staged?.config);
	const viewYaml = $derived.by(() => {
		const definition = current ? routerDefinition(current.config) : {};
		return Object.keys(definition).length ? stringify(definition) : '';
	});
</script>

<ResourceDialog title={viewing ? name : rr((creating ? 'dialogCreate.' : 'dialogEdit.') + form.protocol)} description={viewing ? (http ? t('topology.kind.route') : rr('kind.' + form.protocol)) : ''} closeLabel={t('common.close')} onclose={close} {suspended}>
	{#snippet icon()}<RouteIcon size={32} aria-hidden="true" />{/snippet}
	{#snippet navigation()}
		{#if !loading && !loadError && !viewing}<Tabs {tabs} selected={step} onselect={selectStep} label={r('editorTabs')} />{/if}
	{/snippet}

	{#if loading}
		<p role="status">{rr('loading')}</p>
	{:else if loadError}
		<div class="stack">
			<Notice tone="error" message={loadError} />
			<div><Button onclick={retry}>{t('common.retry')}</Button></div>
		</div>
	{:else if viewing && current}
		{@const c = current.config}
		<div class="stack">
			{#if current.read_only}<Notice message={rr('externalNote')} />{/if}
			<SummaryList>
				<div><dt>{r('name')}</dt><dd>{current.name}</dd></div>
				<div><dt>{rr('protocolColumn')}</dt><dd>{rr('kind.' + routerProtocolOf(c))}</dd></div>
				<div><dt>{r('source')}</dt><dd>{current.origin === 'managed' ? r('managed') : r('external')} · {current.provider}</dd></div>
				<div><dt>{rv2('stateColumn')}</dt><dd>{rv2('status.' + current.status)}</dd></div>
				{#if !udp}<div><dt>{r('rule')}</dt><dd><code>{c.rule || '—'}</code></dd></div>{/if}
				{#if c.domain_name}<div><dt>{r('baseDomain')}</dt><dd><button type="button" class="link" onclick={() => navigate('/domains/' + encodeURIComponent(c.domain_name ?? ''))}>{c.domain_name}</button></dd></div>{/if}
				<div><dt>{r('entryPoints')}</dt><dd>{(c.entry_points ?? []).join(', ') || '—'}</dd></div>
				<div><dt>{r('service')}</dt><dd>{#if isNoBackend(c.service)}{rr('noBackendValue')}{:else if serviceLink(c.service, routerProtocolOf(c))}<button type="button" class="link" onclick={() => navigate(serviceLink(c.service, routerProtocolOf(c)))}>{c.service}</button>{:else}{c.service || '—'}{/if}</dd></div>
				{#if !udp}<div><dt>{r('tlsSection')}</dt><dd>{tlsLabel(current.config)}{c.tls?.options ? ` · ${c.tls.options}` : ''}</dd></div>{/if}
				{#if c.priority}<div><dt>{r('priority')}</dt><dd>{c.priority}</dd></div>{/if}
				{#if current.updated_at}<div><dt>{rr('updated')}</dt><dd><RelativeTime value={current.updated_at} /></dd></div>{/if}
			</SummaryList>
			{#if current.live_error_msg}<Notice tone="error" message={current.live_error_msg} />{/if}
			{#if routerProtocolOf(c) !== 'udp'}
				<h3>{r('middlewaresSection')}</h3>
				{#if (c.middlewares ?? []).length}
					<ol class="members">{#each c.middlewares ?? [] as m, i (i + m)}<li><span class="order">{i + 1}</span><span class="url">{m}</span></li>{/each}</ol>
				{:else}
					<p class="muted">{r('noMiddlewaresAttached')}</p>
				{/if}
			{/if}
			{#if viewYaml}<CodePreview label={rr('definition')} code={viewYaml} language="yaml" />{/if}
		</div>
	{:else if viewing}
		<Notice tone="error" message={r('requestFailed')} />
	{:else}
		<div class="stack">
			{#if !editable}<Notice tone="warning" message={rr('notEditable')} />{/if}
			{#if pickersFailed.length}
				<Notice tone="warning" message={rr('pickersFailed', { list: pickersFailed.map((k) => rr('picker.' + k)).join(', ') })}>
					<Button size="sm" onclick={() => loadPickers()}>{t('common.retry')}</Button>
				</Notice>
			{/if}
			{#if error}
				<Notice tone="error" message={error}>
					{#if conflict}<Button size="sm" onclick={refreshVersion} disabled={busy}>{rv2('refreshDraft')}</Button>{/if}
				</Notice>
			{/if}
			{#if submitted && Object.keys(fields).length && step !== TABS.review}
				<div class="validation-banner" role="alert">
					<CircleAlert size={16} aria-hidden="true" />
					<span>{rv2(Object.keys(fields).length === 1 ? 'fixFieldsOne' : 'fixFields', { count: Object.keys(fields).length })}</span>
					<button type="button" onclick={focusFirstError}>{rv2('goToField')}</button>
				</div>
			{/if}

			{#snippet nameField()}
				<Field id="route-name" label={r('name')} required help={creating ? r('nameHelp') : r('immutable')} error={submitted ? fields.name : ''}>
					{#snippet children(f)}
						<input id={f.id} bind:value={form.name} oninput={invalidate} readonly={!creating} disabled={locked || !editable} aria-invalid={f.invalid || undefined} aria-describedby={f.describedBy} autocomplete="off" spellcheck="false" placeholder={tcp ? 'postgres' : udp ? 'dns' : 'my-app'} />
					{/snippet}
				</Field>
			{/snippet}

			{#if !udp}
			<div id="panel-route-route" role="tabpanel" aria-labelledby="tab-route-route" hidden={step !== TABS.route} class="stack">
				{@render nameField()}
				<Field id="route-mode" label={rr('matchMode')} required help={rr('mode.' + (tcp && form.mode === 'host' ? 'hostTcp' : form.mode) + 'Hint')}>
					{#snippet children(f)}
						<SearchableSelect id={f.id} value={form.mode} onchange={chooseMode} disabled={locked || !editable} ariaDescribedby={f.describedBy}
							options={[
								...(tcp ? [{ value: 'any', label: rr('mode.any') }] : []),
								{ value: 'domain', label: rr('mode.domain') },
								{ value: 'host', label: tcp ? rr('mode.hostTcp') : rr('mode.host') },
								{ value: 'rule', label: rr('mode.rule') }
							]} />
					{/snippet}
				</Field>

				{#if form.mode === 'domain'}
					<!-- One hostname line: [subdomain] . [managed domain] -->
					<Field id="route-domain" label={rr('hostname')} required help={r('subdomainHelp')} error={submitted ? (fields.domainName || fields.subdomain) : ''}>
						{#snippet children(f)}
							<div class="hostname-row">
								<input id="route-subdomain" class="subdomain" bind:value={form.subdomain} oninput={invalidate} disabled={locked || !editable} aria-label={r('subdomain')} aria-invalid={(submitted && !!fields.subdomain) || undefined} aria-describedby={f.describedBy} autocomplete="off" spellcheck="false" placeholder="app" />
								<span class="dot" aria-hidden="true">.</span>
								<div class="base-domain">
									<SearchableSelect id={f.id} value={form.domainName} options={domainOptions} onchange={(v) => { form.domainName = v; invalidate(); }} placeholder={r('selectDomain')} searchPlaceholder={r('selectDomain')} emptyMessage={r('noDomainsAvailable')} disabled={locked || !editable || (!domainOptions.length)} ariaInvalid={submitted && !!fields.domainName} ariaDescribedby={f.describedBy} />
								</div>
							</div>
						{/snippet}
					</Field>
					{#if domainsLoaded && !domains.length}
						<Notice message={rr('noDomains')}><Button size="sm" onclick={() => navigate('/domains/new')}>{rr('openDomains')}</Button></Notice>
					{/if}
				{:else if form.mode === 'host'}
					<Field id="route-hosts" label={rr('hosts')} required help={rr('hostsHelp')} error={submitted ? fields.hosts : ''}>
						{#snippet children(f)}
							<textarea id={f.id} rows="3" bind:value={form.hosts} oninput={invalidate} disabled={locked || !editable} aria-invalid={f.invalid || undefined} aria-describedby={f.describedBy} autocomplete="off" spellcheck="false" placeholder="app.example.com"></textarea>
						{/snippet}
					</Field>
				{:else if form.mode === 'rule'}
					<Field id="route-rule" label={rr('customRule')} required help={rr('customRuleHelp')} error={submitted ? fields.customRule : ''}>
						{#snippet children(f)}
							<textarea id={f.id} class="mono" rows="3" bind:value={form.customRule} oninput={invalidate} disabled={locked || !editable} aria-invalid={f.invalid || undefined} aria-describedby={f.describedBy} autocomplete="off" spellcheck="false" placeholder={'Host(`app.example.com`) && Method(`GET`)'}></textarea>
						{/snippet}
					</Field>
				{/if}

				{#if http && form.mode !== 'rule'}
					<Field id="route-path" label={r('pathPrefix')} help={r('pathPrefixHelp')} error={submitted ? fields.pathPrefix : ''}>
						{#snippet children(f)}
							<input id={f.id} bind:value={form.pathPrefix} oninput={invalidate} disabled={locked || !editable} aria-invalid={f.invalid || undefined} aria-describedby={f.describedBy} autocomplete="off" spellcheck="false" placeholder="/api" />
						{/snippet}
					</Field>
				{/if}
				{#if rule}
					<div class="preview">
						<span class="muted">{r('ruleSummary')}</span>
						<code>{rule}</code>
					</div>
				{/if}
			</div>
			{/if}

			<div id="panel-route-traffic" role="tabpanel" aria-labelledby="tab-route-traffic" hidden={step !== TABS.traffic} class="stack">
				{#if udp}
					{@render nameField()}
					<Notice message={rr('udpNote')} />
				{/if}
				<Field id="route-service" label={r('service')} required help={r('serviceHelp')} error={submitted ? fields.service : ''}>
					{#snippet children(f)}
						<SearchableSelect id={f.id} value={form.service} options={serviceOptions} onchange={(v) => { form.service = v; invalidate(); }} placeholder={r('selectService')} searchPlaceholder={r('searchServices')} emptyMessage={r('noMatchingServices')} disabled={locked || !editable} ariaInvalid={f.invalid} ariaDescribedby={f.describedBy} />
					{/snippet}
				</Field>
				{#if isNoBackend(form.service)}<Notice message={rr('noBackendHint')} />{/if}
				{#if !kindServices.length && pickersDone && !pickersFailed.includes('services')}
					<Notice message={rr(http ? 'noServices' : tcp ? 'noServicesTcp' : 'noServicesUdp')}><Button size="sm" onclick={() => navigate(SERVICE_BASE[form.protocol] + '/new')}>{rr(http ? 'openServices' : tcp ? 'openServicesTcp' : 'openServicesUdp')}</Button></Notice>
				{/if}

				<Field id="route-entrypoints" label={r('entryPoints')} required={!http} help={http ? rr('entryPointsDefault') : rr('entryPointsChoose', { kind: form.protocol.toUpperCase() })} error={submitted ? fields.entryPoints : ''}>
					{#snippet children(f)}
						<SearchableMultiSelect id={f.id} values={form.entryPoints} options={entryPointOptions} onchange={(v) => { form.entryPoints = v; invalidate(); }} placeholder={r('selectEntryPoints')} searchPlaceholder={r('searchEntryPoints')} emptyMessage={r('noMatchingEntryPoints')} disabled={locked || !editable} ariaInvalid={f.invalid} ariaDescribedby={f.describedBy} />
					{/snippet}
				</Field>
				{#if pickersDone && !pickersFailed.includes('entrypoints') && !kindEntryPoints.length && !http}
					<Notice tone="warning" message={rr(udp ? 'noEntryPointsUdp' : 'noEntryPointsTcp')} />
				{/if}
				{#if takenOver.length}
					<Notice tone="warning" message={rr('takesOverHttp', { list: takenOver.join(', ') })} />
				{/if}
				{#if !udp}
				<Field id="route-priority" label={r('priorityDisclosure')} help={r('priorityHelp')} error={submitted ? fields.priority : ''}>
					{#snippet children(f)}
						<input id={f.id} bind:value={form.priority} oninput={invalidate} inputmode="numeric" disabled={locked || !editable} aria-invalid={f.invalid || undefined} aria-describedby={f.describedBy} autocomplete="off" spellcheck="false" placeholder={r('priorityPlaceholder')} />
					{/snippet}
				</Field>
				{/if}
			</div>

			{#if !udp}
			<div id="panel-route-tls" role="tabpanel" aria-labelledby="tab-route-tls" hidden={step !== TABS.tls} class="stack">
				<h3>{r('tlsSection')}</h3>
				{#if tcp && form.mode === 'any'}
					<p class="muted">{rr('tlsAnyHint')}</p>
				{:else if form.mode === 'domain' && !form.tlsPassthrough}
					{#if domain}
						<Notice message={rr('domainTls', { text: domainTlsText(domain) })} />
					{:else}
						<p class="muted">{rr('domainTlsPending')}</p>
					{/if}
				{:else}
					{#if tcp}
						<Checkbox id="route-tls-passthrough" checked={form.tlsPassthrough} label={rr('tlsPassthrough')} help={rr('tlsPassthroughHelp')} disabled={locked || !editable} onchange={(v) => { form.tlsPassthrough = v; if (v) form.tlsEnabled = true; invalidate(); }} />
					{/if}
					<Checkbox id="route-tls-enabled" checked={form.tlsEnabled || form.tlsPassthrough} label={rr(tcp ? 'tlsTerminate' : 'tlsEnable')} help={rr(tcp ? 'tlsTerminateHelp' : 'tlsEnableHelp')} disabled={locked || !editable || form.tlsPassthrough} onchange={(v) => { form.tlsEnabled = v; invalidate(); }} />
					{#if form.tlsEnabled && !form.tlsPassthrough}
						<Field id="route-tls-resolver" label={r('certResolver')} help={r('certResolverHelp')}>
							{#snippet children(f)}
								<SearchableSelect id={f.id} value={form.tlsResolver} options={resolverOptions} onchange={(v) => { form.tlsResolver = v; invalidate(); }} placeholder={r('selectResolver')} searchPlaceholder={r('searchResolvers')} emptyMessage={r('noResolvers')} disabled={locked || !editable} ariaDescribedby={f.describedBy} />
							{/snippet}
						</Field>
						<Field id="route-tls-options" label={r('tlsOptions')} help={r('tlsOptionsHelp')} error={submitted ? fields.tlsOptions : ''}>
							{#snippet children(f)}
								<input id={f.id} bind:value={form.tlsOptions} oninput={invalidate} disabled={locked || !editable} aria-invalid={f.invalid || undefined} aria-describedby={f.describedBy} autocomplete="off" spellcheck="false" placeholder="default" />
							{/snippet}
						</Field>
					{/if}
				{/if}

				<h3>{r('middlewaresSection')}</h3>
				<p class="muted">{http ? r('middlewaresHelp') : rr('tcpMiddlewaresHelp')}</p>
				{#if submitted && fields.middlewares}<Notice tone="error" message={fields.middlewares} />{/if}
				<Field id="route-add-middleware" label={r('addMiddleware')}>
					{#snippet children(f)}
						<SearchableSelect id={f.id} value="" options={middlewareOptions} onchange={addMiddleware} placeholder={r('selectMiddleware')} searchPlaceholder={r('searchMiddlewares')} emptyMessage={r('noMiddlewaresAvailable')} disabled={locked || !editable || form.middlewares.length >= MAX_MIDDLEWARES || !middlewareOptions.length} />
					{/snippet}
				</Field>
				{#if form.middlewares.length}
					<ol class="members">
						{#each form.middlewares as m, index (m)}
							<li>
								<span class="order">{index + 1}</span>
								<span class="url">{m}{#if isExternalRef(m)} <span class="muted">· {rr('externalBadge')}</span>{/if}</span>
								<div class="member-actions">
									<Button size="sm" disabled={locked || !editable || index === 0} onclick={() => moveMiddleware(index, -1)}><ArrowUp size={14} aria-hidden="true" /><span class="sr-only">{r('moveUp')} {m}</span></Button>
									<Button size="sm" disabled={locked || !editable || index === form.middlewares.length - 1} onclick={() => moveMiddleware(index, 1)}><ArrowDown size={14} aria-hidden="true" /><span class="sr-only">{r('moveDown')} {m}</span></Button>
									<Button size="sm" variant="danger" disabled={locked || !editable} onclick={() => removeMiddleware(index)}><Trash2 size={14} aria-hidden="true" /><span class="sr-only">{r('removeMiddleware')} {m}</span></Button>
								</div>
							</li>
						{/each}
					</ol>
				{:else}
					<p class="muted">{r('noMiddlewaresAttached')}</p>
				{/if}
				{#if tcp && pickersDone && !pickersFailed.includes('middlewares') && !middlewareOptions.length && !form.middlewares.length}
					<Notice message={rr('noTcpMiddlewares')}><Button size="sm" onclick={() => navigate('/tcp-middlewares/new')}>{rr('createTcpMiddleware')}</Button></Notice>
				{/if}
			</div>
			{/if}

			<div id="panel-route-review" role="tabpanel" aria-labelledby="tab-route-review" hidden={step !== TABS.review} class="stack">
				{#if result}
					<Notice tone={operationTone(result.status)} title={t('operationV2.status.' + result.status)} message={result.summary}>
						{#if result.id}<span class="muted">{t('operationV2.id', { id: result.id })}</span>{/if}
					</Notice>
					{#if manualRestart}<Notice tone="warning" message={t('writeV2.manualRestart')} />{/if}
				{:else if review}
					<SummaryList>
						<div><dt>{r('name')}</dt><dd>{form.name}</dd></div>
						<div><dt>{rr('protocolColumn')}</dt><dd>{rr('kind.' + form.protocol)}</dd></div>
						{#if !udp}<div><dt>{r('rule')}</dt><dd><code>{stagedConfig?.rule || rule}</code></dd></div>{/if}
						<div><dt>{r('entryPoints')}</dt><dd>{(stagedConfig?.entry_points ?? form.entryPoints).join(', ') || rr('entryPointsAuto')}</dd></div>
						<div><dt>{r('service')}</dt><dd>{isNoBackend(form.service) ? rr('noBackendValue') : form.service}</dd></div>
						{#if !udp}<div><dt>{r('tlsSection')}</dt><dd>{stagedConfig ? tlsLabel({ ...stagedConfig, protocol: form.protocol }) : form.tlsPassthrough ? rr('tlsPassthroughValue') : form.mode === 'domain' || form.tlsEnabled ? rr(tcp ? 'tlsTerminated' : 'https') : rr(tcp ? 'plainTcp' : 'httpOnly')}</dd></div>{/if}
						{#if http && form.middlewares.length}<div><dt>{r('middlewaresSection')}</dt><dd>{form.middlewares.join(' → ')}</dd></div>{/if}
						{#if !udp && form.priority.trim() && Number(form.priority) !== 0}<div><dt>{r('priority')}</dt><dd>{form.priority.trim()}</dd></div>{/if}
						{#if staged?.updated_at}<div><dt>{rr('updated')}</dt><dd><RelativeTime value={staged.updated_at} /></dd></div>{/if}
						<div><dt>{rv2('pendingTotal')}</dt><dd>{review.changes?.length ?? 0}</dd></div>
					</SummaryList>
					{#if serverErrors.length}
						<Notice tone="error" title={rv2('serverErrors')}>
							<ul class="reasons">{#each serverErrors as ve}<li>{ve.item_id}{ve.field ? ' · ' + ve.field : ''}: {ve.message}</li>{/each}</ul>
						</Notice>
					{/if}
					{#if review.requires_restart}
						<Notice tone="warning" message={t('resolverUI.lifecycleHelp')}>
							{#if review.restart_reasons?.length}<ul class="reasons">{#each review.restart_reasons as reason}<li>{reason}</li>{/each}</ul>{/if}
						</Notice>
					{/if}
					{#if review.static_diff}<CodePreview label={rv2('diff.static')} code={review.static_diff} language="diff" />{/if}
					{#if review.dynamic_diff}<CodePreview label={rv2('diff.dynamic')} code={review.dynamic_diff} language="diff" />{/if}
					{#if !review.static_diff && !review.dynamic_diff}<Notice tone="info" message={rr('noFileChanges')} />{/if}
				{:else if reviewError}
					<Notice tone="error" message={reviewError}><Button size="sm" onclick={stageAndReview}>{t('common.retry')}</Button></Notice>
				{:else}
					<p role="status">{rv2('staging')}</p>
				{/if}
			</div>
		</div>
	{/if}

	{#snippet footer()}
		<Button onclick={close} disabled={busy}>{result || viewing ? t('common.close') : t('common.cancel')}</Button>
		{#if viewing && current && !current.read_only}
			<Button variant="primary" onclick={() => navigate(ROUTER_BASE[routerProtocolOf(current.config)] + '/' + encodeURIComponent(current.name) + '/edit')}>{r('edit')}</Button>
		{/if}
		{#if !loading && !loadError && !viewing && !result && editable}
			{#if step === TABS.review}
				<Button onclick={() => (step = stepAfter(-1))} disabled={busy}>{t('domainUI.back')}</Button>
				{#if review}
					<Button variant="primary" onclick={apply} pending={busy} disabled={busy || serverErrors.length > 0}>{t('domainUI.' + (busy ? 'applying' : uncertain ? 'retryApply' : 'apply'))}</Button>
				{/if}
			{:else if stepAfter(1) === TABS.review}
				{#if stepAfter(-1) !== step}<Button onclick={() => (step = stepAfter(-1))} disabled={busy}>{t('domainUI.back')}</Button>{/if}
				<Button variant="primary" onclick={stageAndReview} pending={busy} disabled={busy}>{rv2('stageReview')}</Button>
			{:else}
				{#if stepAfter(-1) !== step}<Button onclick={() => (step = stepAfter(-1))} disabled={busy}>{t('domainUI.back')}</Button>{/if}
				<Button variant="primary" onclick={() => goTo(stepAfter(1))} disabled={busy}>{t('domainUI.continue')}</Button>
			{/if}
		{/if}
	{/snippet}
</ResourceDialog>

<style>
	.stack, .stack:not([hidden]) { display: flex; flex-direction: column; gap: 24px; }
	.stack[hidden] { display: none; }
	h3 { display: flex; align-items: center; gap: 12px; margin: 0; font-size: 13px; font-weight: 600; color: var(--text); }
	h3::after { content: ''; flex: 1; height: 1px; background: var(--border); }
	.muted { margin: 0; color: var(--text-muted); font-size: 14px; line-height: 21px; }
	.reasons { margin: 8px 0 0; padding-left: 20px; font-size: 14px; line-height: 22px; }
	textarea { width: 100%; box-sizing: border-box; resize: vertical; font: inherit; }
	textarea.mono, code { font-family: var(--font-mono, ui-monospace, monospace); font-size: 13px; }
	.preview { display: flex; flex-direction: column; gap: 6px; padding: 12px 14px; border: 1px solid var(--border); border-radius: var(--radius-control); background: var(--surface-muted); }
	.preview code { overflow-wrap: anywhere; }
	.members { list-style: none; margin: 0; padding: 0; display: flex; flex-direction: column; gap: 8px; }
	.members li { display: flex; align-items: center; gap: 12px; padding: 8px 12px; border: 1px solid var(--border); border-radius: var(--radius-control); background: var(--surface-muted); }
	.member-actions { display: flex; gap: 6px; margin-left: auto; }
	.order { min-width: 24px; color: var(--text-muted); font-size: 13px; }
	.url { overflow-wrap: anywhere; font-size: 14px; }
	.link { border: 0; background: transparent; padding: 0; color: var(--connection); font: inherit; cursor: pointer; }
	.link:hover { text-decoration: underline; }
	.validation-banner { display: flex; align-items: center; gap: 8px; padding: 10px 14px; border: 1px solid color-mix(in srgb, var(--danger-text) 30%, transparent); border-radius: var(--radius-card); background: var(--danger-bg); color: var(--danger-text); font-size: 13px; line-height: 18px; }
	.validation-banner span { flex: 1; }
	.validation-banner button { border: 0; background: transparent; color: inherit; font: inherit; font-weight: 600; cursor: pointer; padding: 2px 4px; }
	.validation-banner button:hover { text-decoration: underline; }
	.hostname-row { display: flex; align-items: center; gap: 8px; min-width: 0; }
	.hostname-row .subdomain { flex: 2 1 0; min-width: 0; }
	.hostname-row .dot { flex: none; font-size: 20px; font-weight: 600; line-height: 1; color: var(--text-muted); }
	.hostname-row .base-domain { flex: 3 1 0; min-width: 0; }
	@media (max-width: 480px) { .hostname-row { flex-wrap: wrap; } .hostname-row .subdomain, .hostname-row .base-domain { flex-basis: 100%; } .hostname-row .dot { display: none; } }
</style>

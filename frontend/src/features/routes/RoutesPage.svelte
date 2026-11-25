<script lang="ts">
	import { onMount, onDestroy } from 'svelte';
	import { ChevronDown } from '@lucide/svelte';
	import DataTable from '../../shared/ui/DataTable.svelte';
	import RowActions from '../../shared/ui/RowActions.svelte';
	import PageHeader from '../../shared/ui/PageHeader.svelte';
	import Button from '../../shared/ui/Button.svelte';
	import MenuPopover from '../../shared/ui/MenuPopover.svelte';
	import Notice from '../../shared/ui/Notice.svelte';
	import StatusBadge from '../../shared/ui/StatusBadge.svelte';
	import ValueTooltip from '../../shared/ui/ValueTooltip.svelte';
	import RelativeTime from '../../shared/ui/RelativeTime.svelte';
	import ConfirmDialog from '../../shared/ui/ConfirmDialog.svelte';
	import RouteDialog from './RouteDialog.svelte';
	import { deleteRouter, listRouters } from './api';
	import { ROUTER_BASE, ROUTER_PROTOCOLS, isExternalRef, isNoBackend, referenceBase, routerProtocolOf, type RouterProtocol, type RouterResource } from './models';
	import { ApiError } from '../../shared/api/client';
	import { errorMessage } from '../../shared/api/errorMessage';
	import { needsManualRestart } from '../../core/mutations';
	import { t } from '../../shared/localization/i18n';
	import type { SortState } from '../../shared/ui/tableTypes';

	/** `tab` is the protocol of the open dialog (it comes from the URL: /routers, /tcp-routers, /udp-routers);
	 * the list itself always shows every protocol. */
	let { navigate, tab = 'http', mode = 'list', name = '' }: { navigate: (p: string) => void; tab?: string; mode?: string; name?: string } = $props();

	const dialogProtocol = $derived((ROUTER_PROTOCOLS as string[]).includes(tab) ? (tab as RouterProtocol) : 'http');

	const r = (key: string, args?: Record<string, string | number>) => t('routes.' + key, args);
	const rr = (key: string, args?: Record<string, string | number>) => t('routeV2.' + key, args);
	const rv2 = (key: string, args?: Record<string, string | number>) => t('resolverV2.' + key, args);
	const ru = (key: string) => t('resolverUI.' + key);

	let rows = $state<RouterResource[]>([]);
	let loading = $state(true);
	let error = $state('');
	let actionError = $state('');
	let restartNotice = $state(false);
	let search = $state('');
	let ownership = $state('');
	let protocolFilter = $state('');
	let page = $state(1);
	let pageSize = $state(25);
	let sort = $state<SortState>({ column: 'name', direction: 'asc' });
	let pendingDelete = $state<{ name: string } | null>(null);
	let deleting = $state(false);

	let generation = 0;
	let disposed = false;
	let controller = new AbortController();

	const counts = $derived(Object.fromEntries(ROUTER_PROTOCOLS.map((p) => [p, rows.filter((x) => routerProtocolOf(x.config) === p).length])) as Record<RouterProtocol, number>);

	const columns = $derived([
		{ id: 'name', label: r('name'), sortable: true, width: 'clamp(160px, 18vw, 260px)' },
		{ id: 'protocol', label: rr('protocolColumn'), sortable: true, width: '110px' },
		{ id: 'rule', label: r('rule'), width: '300px' },
		{ id: 'entryPoints', label: r('entryPoints'), width: '160px' },
		{ id: 'service', label: r('service'), sortable: true, width: '190px' },
		{ id: 'tls', label: rr('tlsColumn'), sortable: true, width: '150px' },
		{ id: 'health', label: rr('runtimeHealth'), sortable: true, width: '140px' },
		{ id: 'updatedAt', label: rr('updated'), sortable: true, width: '170px' },
		{ id: 'actions', label: '', hideLabel: true, width: '128px' }
	]);
	const filters = $derived([
		{ id: 'protocol', label: rr('protocolColumn'), options: [{ value: '', label: r('all') }, ...ROUTER_PROTOCOLS.map((p) => ({ value: p, label: rr(`tab.${p}`) + (!loading && !error ? ` (${counts[p]})` : '') }))] },
		{ id: 'ownership', label: r('ownership'), options: [{ value: '', label: r('all') }, { value: 'managed', label: r('managed') }, { value: 'external', label: r('external') }] },
	]);

	const tlsOf = (x: RouterResource) => (x.config.tls ? (x.config.tls.passthrough ? 'passthrough' : x.config.tls.cert_resolver ? 'https-resolver' : 'https') : 'http');
	const sortValue = (x: RouterResource, column: string) =>
		column === 'updatedAt' ? x.updated_at ?? '' : column === 'health' ? x.live_health : column === 'state' ? x.status
			: column === 'service' ? x.config.service ?? '' : column === 'tls' ? tlsOf(x) : column === 'protocol' ? routerProtocolOf(x.config) : x.name;
	const filtered = $derived.by(() => {
		const q = search.trim().toLowerCase();
		const items = rows.filter((x) => (!ownership || x.origin === ownership) && (!protocolFilter || routerProtocolOf(x.config) === protocolFilter) &&
			(!q || `${x.name} ${routerProtocolOf(x.config)} ${x.config.rule ?? ''} ${(x.config.entry_points ?? []).join(' ')} ${x.config.service ?? ''} ${x.origin === 'external' ? x.provider : ''}`.toLowerCase().includes(q)));
		return items.toSorted((a, b) => sortValue(a, sort.column).localeCompare(sortValue(b, sort.column)) * (sort.direction === 'desc' ? -1 : 1));
	});
	const pageRows = $derived(filtered.slice((page - 1) * pageSize, page * pageSize));
	const hasFilters = $derived(!!(search || ownership || protocolFilter));

	async function load() {
		const own = ++generation;
		controller.abort();
		controller = new AbortController();
		const signal = controller.signal;
		loading = true;
		error = '';
		const results = await Promise.allSettled([listRouters(signal)]);
		if (disposed || own !== generation) return;
		if (results[0].status === 'fulfilled') { rows = results[0].value; page = 1; }
		else { rows = []; error = errorMessage(results[0].reason, r('requestFailed')); }
		loading = false;
	}

	onMount(() => { void load(); });
	onDestroy(() => { disposed = true; controller.abort(); });

	function clearFilters() { search = ''; ownership = ''; protocolFilter = ''; page = 1; }
	function onfilterchange(id: string, v: string) {
		if (id === 'ownership') ownership = v;
		if (id === 'protocol') protocolFilter = v;
		page = 1;
	}

	async function confirmDelete() {
		const target = pendingDelete;
		if (!target || deleting) return;
		deleting = true;
		actionError = '';
		try {
			// The version read with the list guards the delete; a 409 is surfaced, never retried.
			const res = await deleteRouter(target.name, rows.find((r) => r.name === target.name)?.version ?? 0);
			if (needsManualRestart(res)) restartNotice = true;
			pendingDelete = null;
			await load();
		} catch (e) {
			pendingDelete = null;
			actionError = e instanceof ApiError && e.status === 409 ? (e.message || rr('deleteFailed')) : errorMessage(e, rr('deleteFailed'));
		} finally {
			deleting = false;
		}
	}

	const healthTone = (h: string) => (({ loaded: 'success', enabled: 'success', error: 'danger', pending_deploy: 'info', disabled: 'neutral', warning: 'warning', unknown: 'neutral' }) as Record<string, 'success' | 'danger' | 'info' | 'neutral' | 'warning'>)[h] ?? 'neutral';
	const KNOWN_HEALTH = ['loaded', 'enabled', 'error', 'pending_deploy', 'disabled', 'warning'];
	const canChange = (x: RouterResource) => !x.read_only;
	/** Each router opens under the base path of its own protocol, so the dialog knows which editor to show. */
	const open = (x: RouterResource, suffix = '') => navigate(ROUTER_BASE[routerProtocolOf(x.config)] + '/' + encodeURIComponent(x.name) + suffix);
	const create = (p: RouterProtocol) => navigate(ROUTER_BASE[p] + '/new');
	const SERVICE_BASE: Record<RouterProtocol, string> = { http: '/services', tcp: '/tcp-services', udp: '/udp-services' };
	const serviceTarget = (ref: string, p: RouterProtocol) => (isExternalRef(ref) ? '' : SERVICE_BASE[p] + '/' + encodeURIComponent(referenceBase(ref)));
	const tlsText = (x: RouterResource) => (!x.config.tls ? rr('httpOnly')
		: x.config.tls.passthrough ? rr('tlsPassthroughValue')
		: x.config.tls.cert_resolver ? rr('httpsResolver', { name: x.config.tls.cert_resolver })
		: routerProtocolOf(x.config) === 'tcp' ? rr('tlsTerminated') : rr('https'));
	const protocolTone = (p: RouterProtocol) => (({ http: 'success', tcp: 'connection', udp: 'neutral' }) as Record<RouterProtocol, 'success' | 'connection' | 'neutral'>)[p];
</script>

{#snippet createMenu()}
	<MenuPopover label={rr('createRouter')} align="end" primary>
		{#snippet trigger()}<span>{rr('createRouter')}</span><ChevronDown size={16} aria-hidden="true" />{/snippet}
		{#snippet children(close)}
			{#each ROUTER_PROTOCOLS as p (p)}
				<button type="button" role="menuitem" onclick={() => { close(); create(p); }}>
					<span class="option"><strong>{rr(`kind.${p}`)}</strong><small>{rr(`createHelp.${p}`)}</small></span>
				</button>
			{/each}
		{/snippet}
	</MenuPopover>
{/snippet}

<PageHeader title={r('title')} subtitle={r('subtitle')} />

<div class="notices">
	{#if actionError}<Notice tone="error" message={actionError} dismissible onDismiss={() => (actionError = '')} />{/if}
	{#if restartNotice}<Notice tone="warning" message={t('writeV2.manualRestart')} dismissible onDismiss={() => (restartNotice = false)} />{/if}
	{#if error}<Notice tone="error" message={error} />{/if}
</div>

<DataTable
	{columns}
	rows={pageRows}
	getRowId={(x: RouterResource) => routerProtocolOf(x.config) + ':' + x.name}
	searchValue={search}
	onsearch={(v: string) => { search = v; page = 1; }}
	searchPlaceholder={r('search')}
	{filters}
	filterValues={{ protocol: protocolFilter, ownership }}
	{onfilterchange}
	{sort}
	onsort={(column: string) => { sort = { column, direction: sort.column === column && sort.direction === 'asc' ? 'desc' : 'asc' }; page = 1; }}
	{page}
	{pageSize}
	total={filtered.length}
	onpage={(v: number) => (page = v)}
	onpagesize={(v: number) => { pageSize = v; page = 1; }}
	{loading}
	error={!!error}
	onretry={load}
	regionLabel={r('title')}
	minWidth={1450}
>
	{#snippet toolbarActions()}
		{@render createMenu()}
	{/snippet}

	{#snippet rowSnippet(row: RouterResource)}
		{@const protocol = routerProtocolOf(row.config)}
		{@const target = serviceTarget(row.config.service ?? '', protocol)}
		{@const eps = row.config.entry_points ?? []}
		<td>
			<div class="identity">
				<span class="name"><ValueTooltip text={row.name} truncate onactivate={() => open(row)}><strong>{row.name}</strong></ValueTooltip></span>
				{#if row.origin === 'external'}<StatusBadge size="sm" tone="info" text={ru('externalShort')} />{/if}
			</div>
		</td>
		<td><StatusBadge size="sm" tone={protocolTone(protocol)} text={rr(`tab.${protocol}`)} /></td>
		<td>{#if row.config.rule}<ValueTooltip text={row.config.rule} truncate><code>{row.config.rule}</code></ValueTooltip>{:else}—{/if}</td>
		<td>{eps.length ? eps.join(', ') : '—'}</td>
		<td>
			{#if !row.config.service}—
			{:else if isNoBackend(row.config.service)}<span class="muted">{rr('noBackendValue')}</span>
			{:else if target}<button type="button" class="link" onclick={() => navigate(target)}>{row.config.service}</button>
			{:else}<ValueTooltip text={row.config.service} truncate>{row.config.service}</ValueTooltip>{/if}
		</td>
		<td>{#if protocol === 'udp'}—{:else}<StatusBadge size="sm" tone={row.config.tls ? 'success' : 'neutral'} text={protocol === 'tcp' && !row.config.tls ? rr('plainTcp') : tlsText(row)} />{/if}</td>
		<td><StatusBadge size="sm" tone={healthTone(row.live_health)} text={rr('liveHealth.' + (KNOWN_HEALTH.includes(row.live_health) ? row.live_health : 'unknown'))} /></td>
		<td>{#if row.updated_at}<RelativeTime value={row.updated_at} />{:else}—{/if}</td>
		<td>
			<RowActions label={row.name} actions={[
				{ label: rr('view'), onclick: () => open(row) },
				canChange(row) && { label: rr('editAction'), onclick: () => open(row, '/edit') },
				canChange(row) && { label: rv2('delete.action'), variant: 'danger', onclick: () => (pendingDelete = { name: row.name }) }
			]} />
		</td>
	{/snippet}

	{#snippet emptyState()}
		<h2>{r(hasFilters ? 'noMatches' : 'empty')}</h2>
		<p>{r('emptyHelp')}</p>
		{#if hasFilters}<Button onclick={clearFilters}>{rr('clearFilters')}</Button>{:else}{@render createMenu()}{/if}
	{/snippet}
</DataTable>


<ConfirmDialog
	open={!!pendingDelete}
	danger
	title={rr('delete.title')}
	message={pendingDelete ? rr('delete.message', { name: pendingDelete.name }) : ''}
	cancelLabel={t('common.cancel')}
	confirmLabel={rv2('delete.confirm')}
	oncancel={() => { if (!deleting) pendingDelete = null; }}
	onconfirm={confirmDelete}
/>

{#if mode !== 'list'}
	{#key `${dialogProtocol}:${mode}:${name}`}
		<RouteDialog {mode} {name} protocol={dialogProtocol} {navigate} routers={loading || error ? null : rows} listError={error} onchanged={load} />
	{/key}
{/if}

<style>
	.notices { display: flex; flex-direction: column; gap: 12px; margin-bottom: 24px; }
	.notices:empty { display: none; }
	code { font-family: var(--font-mono, ui-monospace, monospace); font-size: 13px; }
	.link { border: 0; background: transparent; padding: 0; color: var(--connection); font: inherit; cursor: pointer; }
	.link:hover { text-decoration: underline; }
	/* The name trigger bleeds 10px past its box on hover: keep 8px of visible space. */
	.identity { display: flex; align-items: center; gap: 18px; min-width: 0; }
	.name { display: block; min-width: 0; flex: 1; max-width: 100%; }
	.name strong { font-weight: 600; }
	.muted { color: var(--text-muted); font-size: 14px; }
</style>

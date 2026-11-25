<script lang="ts">
	import { onMount, onDestroy } from 'svelte';
	import DataTable from '../../shared/ui/DataTable.svelte';
	import RowActions from '../../shared/ui/RowActions.svelte';
	import PageHeader from '../../shared/ui/PageHeader.svelte';
	import Tabs from '../../shared/ui/Tabs.svelte';
	import Button from '../../shared/ui/Button.svelte';
	import Notice from '../../shared/ui/Notice.svelte';
	import StatusBadge from '../../shared/ui/StatusBadge.svelte';
	import ValueTooltip from '../../shared/ui/ValueTooltip.svelte';
	import RelativeTime from '../../shared/ui/RelativeTime.svelte';
	import ReferenceCount from '../../shared/ui/ReferenceCount.svelte';
	import RelatedItemsDialog from '../../shared/ui/RelatedItemsDialog.svelte';
	import { chainItem, routeItem, type RelatedItem } from '../../shared/ui/related';
	import ConfirmDialog from '../../shared/ui/ConfirmDialog.svelte';
	import MiddlewareDialog from './MiddlewareDialog.svelte';
	import ChainDialog from './ChainDialog.svelte';
	import ChainMembers from './ChainMembers.svelte';
	import { deleteChain, deleteMiddleware, listChains, listMiddlewares } from './api';
	import { MIDDLEWARE_BASE, PROTOCOLS, chainMembers, externalType, middlewareProtocolOf, usageCount, type ChainResource, type MiddlewareResource, type MiddlewareUsage, type Protocol } from './models';
	import { ApiError } from '../../shared/api/client';
	import { errorMessage } from '../../shared/api/errorMessage';
	import { needsManualRestart } from '../../core/mutations';
	import { t } from '../../shared/localization/i18n';
	import type { SortState } from '../../shared/ui/tableTypes';

	/** `protocol` is the protocol of the open middleware dialog (from the URL: /middlewares or /tcp-middlewares); the list shows both. */
	let { navigate, tab = 'middlewares', protocol = 'http', mode = 'list', name = '' }: { navigate: (p: string) => void; tab?: string; protocol?: Protocol; mode?: string; name?: string } = $props();

	const m = (key: string, args?: Record<string, string | number>) => t('middlewares.' + key, args);
	const mv = (key: string, args?: Record<string, string | number>) => t('middlewareV2.' + key, args);
	const rv2 = (key: string, args?: Record<string, string | number>) => t('resolverV2.' + key, args);
	const ru = (key: string) => t('resolverUI.' + key);

	const chainsTab = $derived(tab === 'chains');
	const base = $derived(chainsTab ? '/chains' : '/middlewares');

	let middlewares = $state<MiddlewareResource[]>([]);
	let chains = $state<ChainResource[]>([]);
	let loading = $state(true);
	let error = $state('');
	let actionError = $state('');
	let restartNotice = $state(false);
	let search = $state('');
	let typeFilter = $state('');
	let originFilter = $state('');
	let protocolFilter = $state('');
	let page = $state(1);
	let pageSize = $state(25);
	let sort = $state<SortState>({ column: 'name', direction: 'asc' });
	let usage = $state<{ name: string; items: RelatedItem[] } | null>(null);
	let pendingDelete = $state<{ name: string } | null>(null);
	let deleting = $state(false);

	let generation = 0;
	let disposed = false;
	let controller = new AbortController();

	type Row = { name: string; type: string; protocol: Protocol; resource: MiddlewareResource | ChainResource; members: string[]; usedBy?: MiddlewareUsage };
	const rows = $derived<Row[]>(chainsTab
		? chains.map((c) => ({ name: c.name, type: 'chain', protocol: 'http' as Protocol, resource: c, members: chainMembers(c.config), usedBy: c.config.used_by }))
		: middlewares.map((x) => ({ name: x.name, type: x.config.type === 'external' ? externalType(x.config.spec) : x.config.type, protocol: middlewareProtocolOf(x.config), resource: x, members: [], usedBy: x.config.used_by })));
	const counts = $derived(Object.fromEntries(PROTOCOLS.map((p) => [p, rows.filter((r) => r.protocol === p).length])) as Record<Protocol, number>);

	/** "Used by" comes with the list (core-v2 `used_by`): routes first, then chains. TCP middlewares serve TCP routers. */
	const usageItems = (u: MiddlewareUsage | undefined, protocol: Protocol = 'http'): RelatedItem[] => [
		...(u?.routers ?? []).map((r) => routeItem({ name: r.name, read_only: r.read_only, config: { rule: r.rule, entry_points: r.entry_points, service: r.service, tls: r.tls || undefined } }, protocol === 'tcp' ? '/tcp-routers' : '/routers')),
		...(u?.chains ?? []).map((c) => chainItem({ name: c.name, read_only: c.read_only, config: { middlewares: c.members } }))
	];
	const usageUnknown = $derived(rows.some((r) => !r.usedBy));

	const typeLabel = (type: string) => (type === 'external' ? m('external') : type === 'plugin' ? mv('pluginType') : type === 'chain' ? m('chainType') : (t(`middlewares.types.${type}.label`) || type));

	const columns = $derived([
		{ id: 'name', label: m('name'), sortable: true, width: 'clamp(180px, 25vw, 340px)' },
		...(chainsTab ? [{ id: 'members', label: m('members'), width: '200px' }] : [{ id: 'protocol', label: mv('protocolColumn'), sortable: true, width: '120px' }, { id: 'type', label: m('type'), sortable: true, width: '200px' }]),
		{ id: 'usedBy', label: m('usedBy'), width: '150px' },
		{ id: 'updatedAt', label: mv('updated'), sortable: true, width: '170px' },
		{ id: 'actions', label: '', hideLabel: true, width: '128px' }
	]);
	const types = $derived([...new Set(middlewares.map((x) => x.config.type))].sort());
	const filters = $derived([
		...(chainsTab ? [] : [{ id: 'protocol', label: mv('protocolColumn'), options: [{ value: '', label: m('all') }, ...PROTOCOLS.map((p) => ({ value: p, label: mv(`tab.${p}`) + (!loading && !error ? ` (${counts[p]})` : '') }))] }, { id: 'type', label: m('type'), options: [{ value: '', label: m('all') }, ...types.map((tp) => ({ value: tp, label: typeLabel(tp) }))] }]),
		{ id: 'origin', label: ru('ownership'), options: [{ value: '', label: ru('all') }, { value: 'managed', label: ru('managed') }, { value: 'external', label: ru('external') }] }
	]);

	const sortValue = (row: Row, column: string) =>
		column === 'updatedAt' ? row.resource.updated_at ?? '' : column === 'type' ? row.type : column === 'protocol' ? row.protocol : column === 'state' ? row.resource.status : row.name;
	const filtered = $derived.by(() => {
		const q = search.trim().toLowerCase();
		const items = rows.filter((r) => (!typeFilter || r.type === typeFilter) && (!originFilter || r.resource.origin === originFilter) && (chainsTab || !protocolFilter || r.protocol === protocolFilter) &&
			(!q || `${r.name} ${r.type} ${r.members.join(' ')}`.toLowerCase().includes(q)));
		return items.toSorted((a, b) => sortValue(a, sort.column).localeCompare(sortValue(b, sort.column)) * (sort.direction === 'desc' ? -1 : 1));
	});
	const pageRows = $derived(filtered.slice((page - 1) * pageSize, page * pageSize));
	const hasFilters = $derived(!!(search || typeFilter || originFilter || (!chainsTab && protocolFilter)));

	async function load() {
		const own = ++generation;
		controller.abort();
		controller = new AbortController();
		const signal = controller.signal;
		loading = true;
		error = '';
		// One request: the active tab's list, which carries its usage (core-v2 `used_by`).
		const onChains = chainsTab;
		try {
			if (onChains) chains = await listChains(signal);
			else middlewares = await listMiddlewares(signal);
			if (disposed || own !== generation) return;
			fresh[onChains ? 'chains' : 'middlewares'] = true;
			page = 1;
		} catch (e) {
			if (disposed || own !== generation) return;
			if (onChains) chains = []; else middlewares = [];
			error = errorMessage(e, m('requestFailed'));
		}
		loading = false;
	}

	// Each tab's list is read once and kept: switching back shows it without a request. A write
	// (or a retry) marks both stale, since it can change the other tab's usage too.
	let fresh = $state({ middlewares: false, chains: false });
	/** Marks both lists stale; the effect below reads the visible one (exactly one request). */
	function refresh() {
		fresh = { middlewares: false, chains: false };
	}
	$effect(() => {
		if (!fresh[chainsTab ? 'chains' : 'middlewares']) void load();
		else { loading = false; error = ''; }
	});
	onDestroy(() => { disposed = true; controller.abort(); });

	function switchTab(id: string) {
		search = ''; typeFilter = ''; originFilter = ''; protocolFilter = ''; page = 1;
		navigate(id === 'chains' ? '/chains' : '/middlewares');
	}
	function clearFilters() { search = ''; typeFilter = ''; originFilter = ''; protocolFilter = ''; page = 1; }

	async function confirmDelete() {
		const target = pendingDelete;
		if (!target || deleting) return;
		deleting = true;
		actionError = '';
		try {
			// The version read with the list guards the delete; a 409 is surfaced, never retried.
			const version = (chainsTab ? chains : middlewares).find((x) => x.name === target.name && x.origin === 'managed')?.version ?? 0;
			const res = await (chainsTab ? deleteChain : deleteMiddleware)(target.name, version);
			if (needsManualRestart(res)) restartNotice = true;
			pendingDelete = null;
			refresh();
		} catch (e) {
			pendingDelete = null;
			actionError = e instanceof ApiError && e.status === 409 ? (e.message || mv('inUse')) : errorMessage(e, mv('deleteFailed'));
		} finally {
			deleting = false;
		}
	}

	const canChange = (r: Row) => !r.resource.read_only;
	const open = (r: Row, suffix = '') => navigate((chainsTab ? base : MIDDLEWARE_BASE[r.protocol]) + '/' + encodeURIComponent(r.name) + suffix);
	// One create dialog for both protocols: the protocol is picked in it.
	const create = () => navigate(base + '/new');
</script>

<PageHeader title={m('title')} subtitle={m('subtitle')} />

<div class="resource-tabs"><Tabs tabs={[{ id: 'middlewares', label: m('tabMiddlewares') }, { id: 'chains', label: m('tabChains') }]} selected={chainsTab ? 'chains' : 'middlewares'} onselect={switchTab} label={m('viewsLabel')} /></div>

<div class="notices">
	{#if usageUnknown && !loading && !error}<Notice tone="warning" message={mv('usageUnavailable')} />{/if}
	{#if actionError}<Notice tone="error" message={actionError} dismissible onDismiss={() => (actionError = '')} />{/if}
	{#if restartNotice}<Notice tone="warning" message={t('writeV2.manualRestart')} dismissible onDismiss={() => (restartNotice = false)} />{/if}
	{#if error}<Notice tone="error" message={error} />{/if}
</div>

<div id={'panel-' + (chainsTab ? 'chains' : 'middlewares')} role="tabpanel" aria-labelledby={'tab-' + (chainsTab ? 'chains' : 'middlewares')}>
<DataTable
	{columns}
	rows={pageRows}
	getRowId={(r: Row) => r.protocol + ':' + r.name}
	searchValue={search}
	onsearch={(v: string) => { search = v; page = 1; }}
	searchPlaceholder={m(chainsTab ? 'searchChains' : 'search')}
	{filters}
	filterValues={{ protocol: protocolFilter, type: typeFilter, origin: originFilter }}
	onfilterchange={(id: string, v: string) => { if (id === 'type') typeFilter = v; if (id === 'origin') originFilter = v; if (id === 'protocol') protocolFilter = v; page = 1; }}
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
	regionLabel={m(chainsTab ? 'tabChains' : 'tabMiddlewares')}
	minWidth={1250}
>
	{#snippet toolbarActions()}
		<Button variant="primary" onclick={create}>{m(chainsTab ? 'chainCreateTitle' : 'createTitle')}</Button>
	{/snippet}

	{#snippet rowSnippet(row: Row)}
		{@const users = usageCount(row.usedBy)}
		<td>
			<div class="identity">
				<span class="name"><ValueTooltip text={row.name} truncate onactivate={() => open(row)}><strong>{row.name}</strong></ValueTooltip></span>
				{#if row.resource.origin === 'external'}<StatusBadge size="sm" tone="info" text={ru('externalShort')} />{/if}
			</div>
		</td>
		{#if chainsTab}
			<td><ChainMembers members={row.members} label={m('members')} /></td>
		{:else}
			<td><StatusBadge size="sm" tone={row.protocol === 'tcp' ? 'connection' : 'success'} text={mv(`tab.${row.protocol}`)} /></td>
			<td><StatusBadge size="sm" tone="connection" text={typeLabel(row.type)} /></td>
		{/if}
		<td>
			{#if row.usedBy}<ReferenceCount count={users} onclick={() => (usage = { name: row.name, items: usageItems(row.usedBy, row.protocol) })} />
			{:else}<span class="muted">{t('certificateFlow.unknown')}</span>{/if}
		</td>
		<td>{#if row.resource.updated_at}<RelativeTime value={row.resource.updated_at} />{:else}—{/if}</td>
		<td>
			<RowActions label={row.name} actions={[
				{ label: m('view'), onclick: () => open(row) },
				canChange(row) && { label: m('edit'), onclick: () => open(row, '/edit') },
				canChange(row) && { label: rv2('delete.action'), variant: 'danger', disabled: users > 0, onclick: () => (pendingDelete = { name: row.name }) }
			]} />
		</td>
	{/snippet}

	{#snippet emptyState()}
		<h2>{m(hasFilters ? 'noMatches' : chainsTab ? 'chainsEmpty' : 'empty')}</h2>
		<p>{m(hasFilters ? 'noMatchesHelp' : chainsTab ? 'chainsEmptyHelp' : 'emptyHelp')}</p>
		<Button onclick={() => (hasFilters ? clearFilters() : create())}>{hasFilters ? mv('clearFilters') : m(chainsTab ? 'createChain' : 'create')}</Button>
	{/snippet}
</DataTable>
</div>

<ConfirmDialog
	open={!!pendingDelete}
	danger
	title={mv(chainsTab ? 'deleteChain.title' : 'delete.title')}
	message={pendingDelete ? mv(chainsTab ? 'deleteChain.message' : 'delete.message', { name: pendingDelete.name }) : ''}
	cancelLabel={t('common.cancel')}
	confirmLabel={rv2('delete.confirm')}
	oncancel={() => { if (!deleting) pendingDelete = null; }}
	onconfirm={confirmDelete}
/>

{#if mode !== 'list'}
	{#key `${tab}:${protocol}:${mode}:${name}`}
		{#if chainsTab}
			<ChainDialog {mode} {name} {navigate} onchanged={refresh} />
		{:else}
			<MiddlewareDialog {mode} {name} {protocol} {navigate} onchanged={refresh} />
		{/if}
	{/key}
{/if}

{#if usage}
	<RelatedItemsDialog title={m('usageTitle') + ' · ' + usage.name} description={m('usageDescription')} items={usage.items} {navigate} onclose={() => (usage = null)} />
{/if}

<style>
	.resource-tabs { margin-bottom: 24px; }
	.notices { display: flex; flex-direction: column; gap: 12px; margin-bottom: 24px; }
	.notices:empty { display: none; }
	/* The name trigger bleeds 10px past its box on hover: keep 8px of visible space. */
	.identity { display: flex; align-items: center; gap: 18px; min-width: 0; }
	.name { display: block; min-width: 0; flex: 1; max-width: 100%; }
	.name strong { font-weight: 600; }
	.muted { color: var(--text-muted); font-size: 14px; }
</style>

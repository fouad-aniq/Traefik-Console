<script lang="ts">
	import { onDestroy } from 'svelte';
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
	import ReferenceCount from '../../shared/ui/ReferenceCount.svelte';
	import RelatedItemsDialog from '../../shared/ui/RelatedItemsDialog.svelte';
	import { routeItem, type RelatedItem } from '../../shared/ui/related';
	import ConfirmDialog from '../../shared/ui/ConfirmDialog.svelte';
	import ServiceDialog from './ServiceDialog.svelte';
	import ServerList from './ServerList.svelte';
	import { deleteService, listServices } from './api';
	import { BASE, PROTOCOLS, protocolOf, serversOf, type ServiceProtocol, type ServiceResource, type ServiceUsage } from './models';
	import { ApiError } from '../../shared/api/client';
	import { errorMessage } from '../../shared/api/errorMessage';
	import { needsManualRestart } from '../../core/mutations';
	import { t } from '../../shared/localization/i18n';
	import type { SortState } from '../../shared/ui/tableTypes';

	/** `tab` is the protocol of the open dialog (it comes from the URL of /services, /tcp-services, /udp-services);
	 * the list itself always shows every protocol. */
	let { navigate, tab = 'http', mode = 'list', name = '' }: { navigate: (p: string) => void; tab?: string; mode?: string; name?: string } = $props();

	const s = (key: string, args?: Record<string, string | number>) => t('services.' + key, args);
	const sv = (key: string, args?: Record<string, string | number>) => t('serviceV2.' + key, args);
	const rv2 = (key: string, args?: Record<string, string | number>) => t('resolverV2.' + key, args);
	const ru = (key: string) => t('resolverUI.' + key);

	const dialogProtocol = $derived((PROTOCOLS as string[]).includes(tab) ? (tab as ServiceProtocol) : 'http');

	/** One list holds every protocol and each service's usage (core-v2 `used_by`). */
	let all = $state<ServiceResource[]>([]);
	let loaded = $state(false);
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
	let usage = $state<{ name: string; protocol: ServiceProtocol; items: RelatedItem[] } | null>(null);
	let pendingDelete = $state<{ name: string } | null>(null);
	let deleting = $state(false);

	let generation = 0;
	let disposed = false;
	let controller = new AbortController();

	const counts = $derived(Object.fromEntries(PROTOCOLS.map((p) => [p, all.filter((r) => protocolOf(r.config) === p).length])) as Record<ServiceProtocol, number>);

	/** "Used by" comes with the list (core-v2 `used_by`): the routers that send traffic here, which
	 * open on the router page of the service's own protocol. */
	const usageItems = (u: ServiceUsage | undefined, protocol: ServiceProtocol): RelatedItem[] =>
		(u?.routers ?? []).map((r) => {
			const base = { http: '/routers', tcp: '/tcp-routers', udp: '/udp-routers' }[protocol];
			return routeItem({ name: r.name, read_only: r.read_only, config: { rule: r.rule, entry_points: r.entry_points, service: r.service, tls: r.tls || undefined } }, base);
		});
	const usageUnknown = $derived(all.some((r) => !r.config.used_by));

	const columns = [
		{ id: 'name', label: s('name'), sortable: true, width: 'clamp(180px, 22vw, 320px)' },
		{ id: 'protocol', label: sv('protocolColumn'), sortable: true, width: '190px' },
		{ id: 'servers', label: s('backends'), width: '300px' },
		{ id: 'health', label: sv('runtimeHealth'), sortable: true, width: '150px' },
		{ id: 'usedBy', label: sv('usedBy'), width: '150px' },
		{ id: 'updatedAt', label: sv('updated'), sortable: true, width: '170px' },
		{ id: 'actions', label: '', hideLabel: true, width: '128px' }
	];
	const filters = $derived([
		{ id: 'protocol', label: sv('protocolColumn'), options: [{ value: '', label: ru('all') }, ...PROTOCOLS.map((p) => ({ value: p, label: sv(`tab.${p}`) + (loaded && !error ? ` (${counts[p]})` : '') }))] },
		{ id: 'ownership', label: ru('ownership'), options: [{ value: '', label: ru('all') }, { value: 'managed', label: ru('managed') }, { value: 'external', label: ru('external') }] }
	]);

	const sortValue = (r: ServiceResource, column: string) =>
		column === 'updatedAt' ? r.updated_at ?? '' : column === 'health' ? r.live_health : column === 'protocol' ? protocolOf(r.config) : r.name;
	const filtered = $derived.by(() => {
		const q = search.trim().toLowerCase();
		const items = all.filter((r) => (!ownership || r.origin === ownership) && (!protocolFilter || protocolOf(r.config) === protocolFilter) &&
			(!q || `${r.name} ${r.provider} ${protocolOf(r.config)} ${serversOf(r.config).join(' ')}`.toLowerCase().includes(q)));
		return items.toSorted((a, b) => sortValue(a, sort.column).localeCompare(sortValue(b, sort.column)) * (sort.direction === 'desc' ? -1 : 1) || a.name.localeCompare(b.name));
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
		try {
			const items = await listServices(signal);
			if (disposed || own !== generation) return;
			all = items;
			page = 1;
		} catch (e) {
			if (disposed || own !== generation) return;
			all = [];
			error = errorMessage(e, s('requestFailed'));
		}
		loaded = true;
		loading = false;
	}

	$effect(() => { if (!loaded) void load(); });
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
			const res = await deleteService(target.name, all.find((r) => r.name === target.name)?.version ?? 0);
			if (needsManualRestart(res)) restartNotice = true;
			pendingDelete = null;
			await load();
		} catch (e) {
			pendingDelete = null;
			actionError = e instanceof ApiError && e.status === 409 ? (e.message || sv('inUse')) : errorMessage(e, sv('deleteFailed'));
		} finally {
			deleting = false;
		}
	}

	/** TCP options at a glance: TLS and PROXY protocol, the two that change what backends receive. */
	const tcpTags = (r: ServiceResource) => [
		...(r.config.tcp?.tls ? ['TLS'] : []),
		...(r.config.tcp?.proxy_protocol ? [sv('proxyShort', { version: r.config.tcp.proxy_protocol })] : []),
		...(r.config.tcp?.health_check ? [sv('healthShort')] : [])
	];

	const healthTone = (h: string) => (({ loaded: 'success', enabled: 'success', error: 'danger', pending_deploy: 'info', disabled: 'neutral', unknown: 'neutral' }) as Record<string, 'success' | 'danger' | 'info' | 'neutral'>)[h] ?? 'neutral';
	const protocolTone = (p: ServiceProtocol) => (({ http: 'success', tcp: 'connection', udp: 'neutral' }) as Record<ServiceProtocol, 'success' | 'connection' | 'neutral'>)[p];
	const canChange = (r: ServiceResource) => !r.read_only;
	/** Each service opens under the base path of its own protocol, so the dialog knows which editor to show. */
	const open = (r: ServiceResource, suffix = '') => navigate(BASE[protocolOf(r.config)] + '/' + encodeURIComponent(r.name) + suffix);
	const create = (p: ServiceProtocol) => navigate(BASE[p] + '/new');
</script>

{#snippet createMenu()}
	<MenuPopover label={sv('createService')} align="end" primary>
		{#snippet trigger()}<span>{sv('createService')}</span><ChevronDown size={16} aria-hidden="true" />{/snippet}
		{#snippet children(close)}
			{#each PROTOCOLS as p (p)}
				<button type="button" role="menuitem" onclick={() => { close(); create(p); }}>
					<span class="option"><strong>{sv(`kind.${p}`)}</strong><small>{sv(`createHelp.${p}`)}</small></span>
				</button>
			{/each}
		{/snippet}
	</MenuPopover>
{/snippet}

<PageHeader title={s('title')} subtitle={sv('subtitleAll')} />

<div class="notices">
	{#if usageUnknown && !loading && !error}<Notice tone="warning" message={sv('usageUnavailable')} />{/if}
	{#if actionError}<Notice tone="error" message={actionError} dismissible onDismiss={() => (actionError = '')} />{/if}
	{#if restartNotice}<Notice tone="warning" message={t('writeV2.manualRestart')} dismissible onDismiss={() => (restartNotice = false)} />{/if}
	{#if error}<Notice tone="error" message={error} />{/if}
	{#if (protocolFilter === 'tcp' || protocolFilter === 'udp') && !loading && !error}<Notice message={sv(`routersNote.${protocolFilter}`)} />{/if}
</div>

<DataTable
	{columns}
	rows={pageRows}
	getRowId={(r: ServiceResource) => r.name}
	searchValue={search}
	onsearch={(v: string) => { search = v; page = 1; }}
	searchPlaceholder={sv('search')}
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
	regionLabel={s('title')}
	minWidth={1200}
>
	{#snippet toolbarActions()}
		{@render createMenu()}
	{/snippet}

	{#snippet rowSnippet(row: ServiceResource)}
		{@const protocol = protocolOf(row.config)}
		{@const users = row.config.used_by?.routers.length ?? 0}
		<td>
			<div class="identity">
				<span class="name"><ValueTooltip text={row.name} truncate onactivate={() => open(row)}><strong>{row.name}</strong></ValueTooltip></span>
				{#if row.origin === 'external'}<StatusBadge size="sm" tone="info" text={ru('externalShort')} />{/if}
			</div>
		</td>
		<td>
			<div class="tags">
				<StatusBadge size="sm" tone={protocolTone(protocol)} text={sv(`tab.${protocol}`)} />
				{#if protocol === 'tcp'}{#each tcpTags(row) as tag (tag)}<StatusBadge size="sm" tone="neutral" text={tag} />{/each}{/if}
			</div>
		</td>
		<td><div class="servers"><ServerList urls={serversOf(row.config)} label={s('backends')} tag={protocol === 'http' ? '' : row.config.tcp?.tls ? 'tls' : protocol} /></div></td>
		<td><StatusBadge size="sm" tone={healthTone(row.live_health)} text={sv('liveHealth.' + (['loaded', 'enabled', 'error', 'pending_deploy', 'disabled'].includes(row.live_health) ? row.live_health : 'unknown'))} /></td>
		<td>
			{#if row.config.used_by}<ReferenceCount count={users} onclick={() => (usage = { name: row.name, protocol, items: usageItems(row.config.used_by, protocol) })} />
			{:else}<span class="muted">{t('certificateFlow.unknown')}</span>{/if}
		</td>
		<td>{#if row.updated_at}<RelativeTime value={row.updated_at} />{:else}—{/if}</td>
		<td>
			<RowActions label={row.name} actions={[
				{ label: sv('view'), onclick: () => open(row) },
				canChange(row) && { label: sv('editAction'), onclick: () => open(row, '/edit') },
				canChange(row) && { label: rv2('delete.action'), variant: 'danger', disabled: users > 0, onclick: () => (pendingDelete = { name: row.name }) }
			]} />
		</td>
	{/snippet}

	{#snippet emptyState()}
		<h2>{hasFilters ? s('noMatches') : sv('emptyAll')}</h2>
		<p>{sv('emptyHelpAll')}</p>
		{#if hasFilters}<Button onclick={clearFilters}>{sv('clearFilters')}</Button>{:else}{@render createMenu()}{/if}
	{/snippet}
</DataTable>

<ConfirmDialog
	open={!!pendingDelete}
	danger
	title={sv('delete.title')}
	message={pendingDelete ? sv('delete.message', { name: pendingDelete.name }) : ''}
	cancelLabel={t('common.cancel')}
	confirmLabel={rv2('delete.confirm')}
	oncancel={() => { if (!deleting) pendingDelete = null; }}
	onconfirm={confirmDelete}
/>

{#if mode !== 'list'}
	{#key `${dialogProtocol}:${mode}:${name}`}
		<ServiceDialog {mode} {name} protocol={dialogProtocol} services={loaded && !error ? all : null} listError={error} {navigate} onchanged={load} />
	{/key}
{/if}

{#if usage}
	<RelatedItemsDialog title={sv('usedBy') + ' · ' + usage.name} description={sv(`routersHelp${usage.protocol === 'http' ? '' : 'Transport'}`)} items={usage.items} {navigate} onclose={() => (usage = null)} />
{/if}

<style>
	.notices { display: flex; flex-direction: column; gap: 12px; margin-bottom: 24px; }
	.notices:empty { display: none; }
	.servers { display: flex; align-items: center; min-width: 0; }
	.servers > :global(*) { min-width: 0; }
	.tags { display: flex; flex-wrap: wrap; gap: 6px; }
	/* The name trigger bleeds 10px past its box on hover: keep 8px of visible space. */
	.identity { display: flex; align-items: center; gap: 18px; min-width: 0; }
	.name { display: block; min-width: 0; flex: 1; max-width: 100%; }
	.name strong { font-weight: 600; }
	.muted { color: var(--text-muted); font-size: 14px; }
	.option { display: flex; flex-direction: column; gap: 2px; min-width: 0; }
	.option strong { font-weight: 600; color: var(--text); }
	.option small { font-size: 12px; line-height: 16px; color: var(--text-muted); white-space: normal; }
</style>

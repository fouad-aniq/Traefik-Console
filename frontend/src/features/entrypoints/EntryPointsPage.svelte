<script lang="ts">
	import { onMount, onDestroy } from 'svelte';
	import DataTable from '../../shared/ui/DataTable.svelte';
	import RowActions from '../../shared/ui/RowActions.svelte';
	import PageHeader from '../../shared/ui/PageHeader.svelte';
	import Button from '../../shared/ui/Button.svelte';
	import Notice from '../../shared/ui/Notice.svelte';
	import StatusBadge from '../../shared/ui/StatusBadge.svelte';
	import ValueTooltip from '../../shared/ui/ValueTooltip.svelte';
	import RelativeTime from '../../shared/ui/RelativeTime.svelte';
	import ReferenceCount from '../../shared/ui/ReferenceCount.svelte';
	import RelatedItemsDialog from '../../shared/ui/RelatedItemsDialog.svelte';
	import {
		listRouterSummaries,
		namedItem,
		routeItem,
		type RelatedItem,
		type RouterLike,
	} from '../../shared/ui/related';
	import ConfirmDialog from '../../shared/ui/ConfirmDialog.svelte';
	import EntryPointDialog from './EntryPointDialog.svelte';
	import { listEntryPointResources, deleteEntryPoint } from './api';
	import { KINDS, displayAddress, kindOf, usageCount, type EntryPointResource, type Kind } from './models';
	import { ApiError } from '../../shared/api/client';
	import { errorMessage } from '../../shared/api/errorMessage';
	import { needsManualRestart } from '../../core/mutations';
	import { t } from '../../shared/localization/i18n';
	import type { SortState } from '../../shared/ui/tableTypes';

	let {
		navigate,
		mode = 'list',
		name = '',
	}: { navigate: (p: string) => void; mode?: string; name?: string } = $props();

	const c = (key: string, args?: Record<string, string | number>) => t('entryPointUI.' + key, args);

	let rows = $state<EntryPointResource[]>([]);
	let loading = $state(true);
	let error = $state('');
	let actionError = $state('');
	let restartNotice = $state(false);
	let search = $state('');
	let origin = $state('');
	let kind = $state('');
	let page = $state(1);
	let pageSize = $state(25);
	let sort = $state<SortState>({ column: 'name', direction: 'asc' });
	let pendingDelete = $state<EntryPointResource | null>(null);
	let deleting = $state(false);
	let usage = $state<{ name: string; items: RelatedItem[]; loading: boolean } | null>(null);

	let generation = 0;
	let disposed = false;
	let controller = new AbortController();

	const columns = $derived([
		{ id: 'name', label: c('name'), sortable: true, width: 'clamp(170px, 20vw, 280px)' },
		{ id: 'kind', label: c('kind.label'), sortable: true, width: '170px' },
		{ id: 'address', label: c('address'), sortable: true, width: '190px' },
		{ id: 'behaviour', label: c('behaviour'), width: 'clamp(200px, 24vw, 340px)' },
		{ id: 'usedBy', label: c('usedBy'), width: '150px' },
		{ id: 'updatedAt', label: c('updated'), sortable: true, width: '160px' },
		{ id: 'actions', label: '', hideLabel: true, width: '128px' },
	]);
	const filters = $derived([
		{
			id: 'kind',
			label: c('kind.label'),
			options: [{ value: '', label: c('all') }, ...KINDS.map((value) => ({ value, label: c('kind.' + value) }))],
		},
		{
			id: 'origin',
			label: c('source'),
			options: [
				{ value: '', label: c('all') },
				{ value: 'managed', label: c('managed') },
				{ value: 'external', label: c('external') },
			],
		},
	]);

	const protocolOf = (row: EntryPointResource) => (row.config.protocol === 'udp' ? 'udp' : 'tcp');
	const kindTone = (k: Kind) => (({ http: 'neutral', https: 'success', tcp: 'connection', udp: 'info' }) as const)[k];
	const filtered = $derived.by(() => {
		const q = search.trim().toLowerCase();
		const items = rows.filter(
			(row) =>
				(!origin || row.origin === origin) &&
				(!kind || kindOf(row.config) === kind) &&
				(!q ||
					`${row.name} ${displayAddress(row.config)} ${protocolOf(row)} ${row.config.http?.redirect?.to ?? ''} ${row.config.http?.tls?.cert_resolver ?? ''}`
						.toLowerCase()
						.includes(q)),
		);
		const direction = sort.direction === 'desc' ? -1 : 1;
		return items.toSorted((a, b) => {
			const byName = a.name.localeCompare(b.name);
			if (sort.column === 'address') return ((a.config.port ?? 0) - (b.config.port ?? 0)) * direction || byName;
			if (sort.column === 'kind') return kindOf(a.config).localeCompare(kindOf(b.config)) * direction || byName;
			if (sort.column === 'updatedAt')
				return (a.updated_at ?? '').localeCompare(b.updated_at ?? '') * direction || byName;
			return byName * direction;
		});
	});
	const pageRows = $derived(filtered.slice((page - 1) * pageSize, page * pageSize));
	const hasFilters = $derived(!!(search || origin || kind));

	async function load() {
		const own = ++generation;
		controller.abort();
		controller = new AbortController();
		loading = true;
		error = '';
		try {
			const items = await listEntryPointResources(controller.signal);
			if (disposed || own !== generation) return;
			rows = items;
			page = 1;
		} catch (e) {
			if (disposed || own !== generation) return;
			rows = [];
			error = errorMessage(e, c('loadFailed'));
		}
		loading = false;
	}

	onMount(() => {
		void load();
	});
	onDestroy(() => {
		disposed = true;
		controller.abort();
	});

	function clearFilters() {
		search = '';
		origin = '';
		kind = '';
		page = 1;
	}

	async function confirmDelete() {
		const target = pendingDelete;
		if (!target || deleting) return;
		deleting = true;
		actionError = '';
		try {
			// The version read with the list guards the delete; a conflict is surfaced, never retried.
			const res = await deleteEntryPoint(target.name, rows.find((r) => r.name === target.name)?.version ?? 0);
			if (needsManualRestart(res)) restartNotice = true;
			pendingDelete = null;
			await load();
		} catch (e) {
			pendingDelete = null;
			// In-use and last-entrypoint refusals carry their own explanation.
			actionError =
				e instanceof ApiError && e.code === 'ERR_VERSION_CONFLICT'
					? c('conflict')
					: errorMessage(e, c('delete.failed'));
		} finally {
			deleting = false;
		}
	}

	const ROUTER_BASE: Record<string, string> = { http: '/routers', tcp: '/tcp-routers', udp: '/udp-routers' };
	/**
	 * "Used by": the list names what references the entrypoint. Routers are then read once so each
	 * one shows its rule and opens on the page of its own protocol; if that read fails the names stay.
	 */
	async function showUsage(row: EntryPointResource) {
		const u = row.config.usage ?? {};
		const fixed: RelatedItem[] = [
			...(u.resolvers ?? []).map((n) => namedItem(n, 'resolver', '/resolvers/')),
			...(u.entry_points ?? []).map((n) => ({
				...namedItem(n, 'entrypoint', '/entrypoints/'),
				summary: c('usage.redirectsHere'),
			})),
		];
		const names = u.routers ?? [];
		usage = {
			name: row.name,
			items: [...names.map((n) => namedItem(n, 'route', '/routers/')), ...fixed],
			loading: names.length > 0,
		};
		if (!names.length) return;
		let routers: RouterLike[] = [];
		try {
			routers = await listRouterSummaries(controller.signal);
		} catch {
			/* names only */
		}
		if (disposed || usage?.name !== row.name) return;
		const items = names.map((n) => {
			const router = routers.find((r) => r.name === n);
			return router
				? routeItem(router, ROUTER_BASE[(router.config as { protocol?: string }).protocol ?? 'http'] ?? '/routers')
				: namedItem(n, 'route', '/routers/');
		});
		usage = { name: row.name, items: [...items, ...fixed], loading: false };
	}
	const path = (row: EntryPointResource, suffix = '') => '/entrypoints/' + encodeURIComponent(row.name) + suffix;
</script>

<PageHeader title={c('title')} subtitle={c('subtitle')} />

<div class="notices">
	{#if actionError}<Notice tone="error" message={actionError} dismissible onDismiss={() => (actionError = '')} />{/if}
	{#if restartNotice}<Notice
			tone="warning"
			message={t('writeV2.manualRestart')}
			dismissible
			onDismiss={() => (restartNotice = false)}
		/>{/if}
	{#if error}<Notice tone="error" message={error} />{/if}
</div>

<DataTable
	{columns}
	rows={pageRows}
	getRowId={(r: EntryPointResource) => r.name}
	searchValue={search}
	onsearch={(v: string) => {
		search = v;
		page = 1;
	}}
	searchPlaceholder={c('search')}
	{filters}
	filterValues={{ origin, kind }}
	onfilterchange={(id: string, v: string) => {
		if (id === 'origin') origin = v;
		else if (id === 'kind') kind = v;
		page = 1;
	}}
	{sort}
	onsort={(column: string) => {
		sort = { column, direction: sort.column === column && sort.direction === 'asc' ? 'desc' : 'asc' };
		page = 1;
	}}
	{page}
	{pageSize}
	total={filtered.length}
	onpage={(v: number) => (page = v)}
	onpagesize={(v: number) => {
		pageSize = v;
		page = 1;
	}}
	{loading}
	error={!!error}
	onretry={load}
	regionLabel={c('title')}
	minWidth={1180}
>
	{#snippet toolbarActions()}
		<Button variant="primary" onclick={() => navigate('/entrypoints/new')}>{c('create')}</Button>
	{/snippet}

	{#snippet rowSnippet(row: EntryPointResource)}
		{@const users = usageCount(row.config.usage)}
		{@const rowKind = kindOf(row.config)}
		<td>
			<div class="identity">
				<span class="name">
					<ValueTooltip text={row.name} truncate onactivate={() => navigate(path(row))}>
						<strong>{row.name}</strong>
					</ValueTooltip>
				</span>
				{#if row.origin === 'external'}<StatusBadge
						size="sm"
						tone="info"
						text={c('externalShort')}
					/>{:else if row.config.adopted}<StatusBadge size="sm" tone="neutral" text={c('adoptedShort')} />{/if}
			</div>
		</td>
		<td><StatusBadge size="sm" tone={kindTone(rowKind)} text={c('kind.short.' + rowKind)} /></td>
		<td>
			<!-- The address without its /udp suffix, then the protocol once. -->
			<div class="address">
				<code>{displayAddress(row.config) || '—'}</code><span class="protocol">{protocolOf(row).toUpperCase()}</span>
			</div>
		</td>
		<td>
			<div class="tags">
				{#if row.config.http?.redirect}<StatusBadge
						size="sm"
						tone="connection"
						text={c('badge.redirect', { to: row.config.http.redirect.to })}
					/>{/if}
				{#if row.config.http?.tls?.cert_resolver}<StatusBadge
						size="sm"
						tone="neutral"
						text={c('badge.tlsResolver', { resolver: row.config.http.tls.cert_resolver })}
					/>{/if}
				{#if row.config.http3}<StatusBadge size="sm" tone="neutral" text="HTTP/3" />{/if}
				{#if row.config.as_default}<StatusBadge size="sm" tone="neutral" text={c('badge.default')} />{/if}
				{#if row.config.http?.middlewares?.length}<StatusBadge
						size="sm"
						tone="neutral"
						text={c('badge.middlewares', { count: row.config.http.middlewares.length })}
					/>{/if}
				{#if row.config.proxy_protocol}<StatusBadge size="sm" tone="neutral" text={c('badge.proxy')} />{/if}
				{#if row.config.forwarded_headers?.insecure || row.config.proxy_protocol?.insecure}<StatusBadge
						size="sm"
						tone="warning"
						text={c('badge.insecure')}
					/>{/if}
				{#if !row.config.http?.redirect && !row.config.http?.tls?.cert_resolver && !row.config.http3 && !row.config.as_default && !row.config.http?.middlewares?.length && !row.config.proxy_protocol && !row.config.forwarded_headers?.insecure}<span
						class="muted"
					>
						—
					</span>{/if}
			</div>
		</td>
		<td><ReferenceCount count={users} onclick={() => showUsage(row)} /></td>
		<td>
			{#if row.updated_at}<RelativeTime value={row.updated_at} />{:else}<span class="muted">—</span>{/if}
		</td>
		<td>
			<RowActions
				label={row.name}
				actions={[
					{ label: c('view.action'), onclick: () => navigate(path(row)) },
					!row.read_only && { label: c('editAction'), onclick: () => navigate(path(row, '/edit')) },
					row.read_only &&
						row.config.adoptable && { label: c('adopt.action'), onclick: () => navigate(path(row, '/edit')) },
					// Releasing an adopted entrypoint keeps the listener, so it is allowed while in use.
					!row.read_only && {
						label: c(row.config.adopted ? 'release.action' : 'delete.action'),
						variant: 'danger',
						disabled: !row.config.adopted && users > 0,
						onclick: () => (pendingDelete = row),
					},
				]}
			/>
		</td>
	{/snippet}

	{#snippet emptyState()}
		<h2>{c(hasFilters ? 'noMatches' : 'empty')}</h2>
		<p>{c(hasFilters ? 'adjustFilters' : 'emptyHelp')}</p>
		<Button onclick={() => (hasFilters ? clearFilters() : navigate('/entrypoints/new'))}>
			{c(hasFilters ? 'clearFilters' : 'create')}
		</Button>
	{/snippet}
</DataTable>

<ConfirmDialog
	open={!!pendingDelete}
	danger
	title={c(pendingDelete?.config.adopted ? 'release.title' : 'delete.title')}
	message={pendingDelete
		? c(pendingDelete.config.adopted ? 'release.message' : 'delete.message', { name: pendingDelete.name })
		: ''}
	cancelLabel={t('common.cancel')}
	confirmLabel={c(pendingDelete?.config.adopted ? 'release.confirm' : 'delete.confirm')}
	oncancel={() => {
		if (!deleting) pendingDelete = null;
	}}
	onconfirm={confirmDelete}
/>

{#if mode !== 'list'}
	{#key `${mode}:${name}`}
		<EntryPointDialog {mode} {name} {navigate} onchanged={load} />
	{/key}
{/if}

{#if usage}
	<RelatedItemsDialog
		title={c('usedBy') + ' · ' + usage.name}
		description={c('usage.help')}
		items={usage.items}
		loading={usage.loading}
		{navigate}
		onclose={() => (usage = null)}
	/>
{/if}

<style>
	.notices {
		display: flex;
		flex-direction: column;
		gap: 12px;
		margin-bottom: 24px;
	}
	.notices:empty {
		display: none;
	}
	/* The name trigger bleeds 10px past its box on hover: keep 8px of visible space. */
	.identity {
		display: flex;
		align-items: center;
		gap: 18px;
		min-width: 0;
	}
	.name {
		display: block;
		min-width: 0;
		flex: 1;
		max-width: 100%;
	}
	.name strong {
		font-weight: 600;
	}
	.address {
		display: flex;
		align-items: baseline;
		gap: 8px;
		min-width: 0;
	}
	.address code {
		font-size: 13px;
		overflow: hidden;
		text-overflow: ellipsis;
		white-space: nowrap;
	}
	.protocol {
		flex: none;
		font-size: 12px;
		color: var(--text-muted);
		letter-spacing: 0.04em;
	}
	.tags {
		display: flex;
		flex-wrap: wrap;
		gap: 6px;
	}
	.muted {
		color: var(--text-muted);
		font-size: 14px;
	}
</style>

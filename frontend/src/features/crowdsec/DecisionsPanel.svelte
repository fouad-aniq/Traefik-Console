<script lang="ts">
	import { onDestroy, onMount } from 'svelte';
	import DataTable from '../../shared/ui/DataTable.svelte';
	import Button from '../../shared/ui/Button.svelte';
	import Notice from '../../shared/ui/Notice.svelte';
	import ConfirmDialog from '../../shared/ui/ConfirmDialog.svelte';
	import StatusBadge from '../../shared/ui/StatusBadge.svelte';
	import ValueTooltip from '../../shared/ui/ValueTooltip.svelte';
	import { errorMessage } from '../../shared/api/errorMessage';
	import { t } from '../../shared/localization/i18n';
	import type { SortState } from '../../shared/ui/tableTypes';
	import { deleteDecision, listDecisions } from './api';
	import type { CrowdSecDecision } from './models';

	let {
		onadd,
		reloadKey = 0,
		canAdd = true,
		canDelete = true,
	}: { onadd: () => void; reloadKey?: number; canAdd?: boolean; canDelete?: boolean } = $props();
	const c = (key: string, args?: Record<string, string | number>) => t('crowdsecV2.' + key, args);

	let rows = $state<CrowdSecDecision[]>([]);
	let loading = $state(true);
	let error = $state('');
	let actionError = $state('');
	let search = $state('');
	let typeFilter = $state('');
	let page = $state(1);
	let pageSize = $state(25);
	let sort = $state<SortState>({ column: 'value', direction: 'asc' });
	let pending = $state<CrowdSecDecision | null>(null);
	let removing = $state(false);
	let disposed = false;
	let controller = new AbortController();
	let generation = 0;

	const columns = $derived([
		{ id: 'value', label: c('decisions.value'), sortable: true, width: 'clamp(160px, 24vw, 260px)' },
		{ id: 'type', label: c('decisions.type'), sortable: true, width: '120px' },
		{ id: 'origin', label: c('decisions.origin'), sortable: true, width: '140px' },
		{ id: 'scenario', label: c('decisions.scenario'), sortable: true, width: 'clamp(180px, 28vw, 340px)' },
		{ id: 'duration', label: c('decisions.remaining'), width: '140px' },
		{ id: 'actions', label: '', hideLabel: true, width: '120px' },
	]);
	const types = $derived([...new Set(rows.map((r) => r.type).filter(Boolean))].sort());
	const filters = $derived([
		{
			id: 'type',
			label: c('decisions.type'),
			options: [{ value: '', label: t('resolverUI.all') }, ...types.map((v) => ({ value: v, label: v }))],
		},
	]);
	const sortValue = (r: CrowdSecDecision, col: string) =>
		col === 'value'
			? r.value
			: col === 'type'
				? r.type
				: col === 'origin'
					? r.origin
					: col === 'scenario'
						? r.scenario
						: '';
	const filtered = $derived.by(() => {
		const q = search.trim().toLowerCase();
		return rows
			.filter(
				(r) =>
					(!typeFilter || r.type === typeFilter) &&
					(!q || `${r.value} ${r.scenario} ${r.origin} ${r.type}`.toLowerCase().includes(q)),
			)
			.toSorted(
				(a, b) =>
					sortValue(a, sort.column).localeCompare(sortValue(b, sort.column), undefined, { numeric: true }) *
					(sort.direction === 'desc' ? -1 : 1),
			);
	});
	const pageRows = $derived(filtered.slice((page - 1) * pageSize, page * pageSize));

	async function load() {
		const own = ++generation;
		controller.abort();
		controller = new AbortController();
		loading = true;
		error = '';
		try {
			const result = await listDecisions(controller.signal);
			if (disposed || own !== generation) return;
			rows = result;
			page = 1;
		} catch (e) {
			if (disposed || own !== generation) return;
			rows = [];
			error = errorMessage(e, c('decisions.loadFailed'));
		} finally {
			if (!disposed && own === generation) loading = false;
		}
	}
	onMount(() => void load());
	onDestroy(() => {
		disposed = true;
		controller.abort();
	});
	$effect(() => {
		if (reloadKey > 0) void load();
	});

	async function confirmRemove() {
		const target = pending;
		if (!target || removing) return;
		removing = true;
		actionError = '';
		try {
			await deleteDecision(target.id);
			pending = null;
			await load();
		} catch (e) {
			pending = null;
			actionError = errorMessage(e, c('decisions.removeFailed'));
		} finally {
			removing = false;
		}
	}
</script>

{#if error}<div class="notice">
		<Notice tone="error" message={error}><Button size="sm" onclick={load}>{t('common.retry')}</Button></Notice>
	</div>{/if}
{#if actionError}<div class="notice">
		<Notice tone="error" message={actionError} dismissible onDismiss={() => (actionError = '')} />
	</div>{/if}

<DataTable
	{columns}
	rows={pageRows}
	getRowId={(r: CrowdSecDecision) => String(r.id) + ':' + r.value}
	searchValue={search}
	onsearch={(v: string) => {
		search = v;
		page = 1;
	}}
	searchPlaceholder={c('decisions.search')}
	{filters}
	filterValues={{ type: typeFilter }}
	onfilterchange={(_id: string, v: string) => {
		typeFilter = v;
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
	regionLabel={c('section.decisions')}
	minWidth={900}
>
	{#snippet toolbarActions()}
		<Button onclick={load} pending={loading} disabled={loading}>{c('refresh')}</Button>
		{#if canAdd}<Button variant="primary" onclick={onadd}>{c('decisions.add')}</Button>{/if}
	{/snippet}
	{#snippet rowSnippet(row: CrowdSecDecision)}
		<td>
			<ValueTooltip text={row.value} truncate><strong>{row.value}</strong></ValueTooltip><span class="scope">
				{row.scope}
			</span>
		</td>
		<td><StatusBadge size="sm" tone={row.type === 'ban' ? 'danger' : 'neutral'} text={row.type || '—'} /></td>
		<td>{row.origin || '—'}</td>
		<td><ValueTooltip text={row.scenario} truncate>{row.scenario || '—'}</ValueTooltip></td>
		<td>{row.duration || '—'}</td>
		<td>
			{#if canDelete}<Button size="sm" variant="danger" onclick={() => (pending = row)}>
					{c('decisions.remove')}
				</Button>{:else}—{/if}
		</td>
	{/snippet}
	{#snippet emptyState()}
		<h2>{c(search || typeFilter ? 'decisions.noMatches' : 'decisions.empty')}</h2>
		<p>{c(search || typeFilter ? 'decisions.adjust' : 'decisions.emptyHelp')}</p>
		{#if search || typeFilter}<Button
				onclick={() => {
					search = '';
					typeFilter = '';
					page = 1;
				}}
			>
				{c('decisions.clear')}
			</Button>{:else if canAdd}<Button onclick={onadd}>{c('decisions.add')}</Button>{/if}
	{/snippet}
</DataTable>

<ConfirmDialog
	open={!!pending}
	danger
	title={c('decisions.removeTitle')}
	message={pending ? c('decisions.removeMessage', { value: pending.value }) : ''}
	cancelLabel={t('common.cancel')}
	confirmLabel={c('decisions.removeConfirm')}
	oncancel={() => {
		if (!removing) pending = null;
	}}
	onconfirm={confirmRemove}
/>

<style>
	.notice {
		margin-bottom: 16px;
	}
	.scope {
		display: block;
		color: var(--text-muted);
		font-size: 12px;
	}
</style>

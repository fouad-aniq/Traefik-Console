<script lang="ts">
	import { onMount } from 'svelte';
	import DataTable from '../../../shared/ui/DataTable.svelte';
	import Button from '../../../shared/ui/Button.svelte';
	import StatusBadge from '../../../shared/ui/StatusBadge.svelte';
	import RelativeTime from '../../../shared/ui/RelativeTime.svelte';
	import ValueTooltip from '../../../shared/ui/ValueTooltip.svelte';
	import { errorMessage } from '../../../shared/api/errorMessage';
	import { t } from '../../../shared/localization/i18n';
	import type { DataTableColumn, DataTableFilter } from '../../../shared/ui/tableTypes';
	import { recentActivity } from '../api';
	import type { AuthEvent } from '../models';

	const a = (key: string, args?: Record<string, string | number>) => t('authScreens.activity.' + key, args);
	const eventLabel = (e: AuthEvent) => t('authScreens.events.' + e.event);

	let rows = $state<AuthEvent[]>([]);
	let loading = $state(true);
	let error = $state('');
	let search = $state('');
	let outcome = $state('');
	let page = $state(1);
	let pageSize = $state(25);

	async function load() {
		loading = true;
		try {
			rows = (await recentActivity()).items;
			error = '';
		} catch (err) {
			error = errorMessage(err, a('loadFailed'));
		} finally {
			loading = false;
		}
	}
	onMount(load);

	const filtered = $derived(rows.filter((r) => {
		const q = search.trim().toLowerCase();
		return (!outcome || r.outcome === outcome) && (!q || [eventLabel(r), r.detail, r.ip].some((v) => v.toLowerCase().includes(q)));
	}));
	const pageRows = $derived(filtered.slice((page - 1) * pageSize, page * pageSize));

	const columns: DataTableColumn[] = [
		{ id: 'event', label: a('colEvent') },
		{ id: 'outcome', label: a('colOutcome'), width: '130px' },
		{ id: 'detail', label: a('colDetail') },
		{ id: 'address', label: a('colAddress'), width: '150px' },
		{ id: 'when', label: a('colWhen'), width: '150px' }
	];
	const filters: DataTableFilter[] = $derived([{ id: 'outcome', label: a('colOutcome'), options: [
		{ value: '', label: a('all') }, { value: 'success', label: a('success') }, { value: 'failure', label: a('failure') }
	] }]);
</script>

<DataTable
	{columns}
	rows={pageRows}
	getRowId={(r: AuthEvent) => r.at + r.event + r.ip + r.detail}
	searchValue={search}
	onsearch={(v: string) => { search = v; page = 1; }}
	searchPlaceholder={a('search')}
	{filters}
	filterValues={{ outcome }}
	onfilterchange={(_: string, v: string) => { outcome = v; page = 1; }}
	{page}
	{pageSize}
	total={filtered.length}
	onpage={(v: number) => (page = v)}
	onpagesize={(v: number) => { pageSize = v; page = 1; }}
	{loading}
	error={!!error}
	onretry={load}
	regionLabel={a('title')}
	minWidth={860}
>
	{#snippet rowSnippet(row: AuthEvent)}
		<td><strong class="event">{eventLabel(row)}</strong></td>
		<td><StatusBadge size="sm" tone={row.outcome === 'failure' ? 'danger' : 'success'} text={a(row.outcome)} /></td>
		<td>{#if row.detail}<ValueTooltip text={row.detail} truncate>{row.detail}</ValueTooltip>{:else}<span class="muted">—</span>{/if}</td>
		<td><span class="mono">{row.ip || '—'}</span></td>
		<td><RelativeTime value={row.at} /></td>
	{/snippet}

	{#snippet emptyState()}
		<h2>{search || outcome ? a('noMatches') : a('empty')}</h2>
		<p>{search || outcome ? a('adjustFilters') : a('emptyHelp')}</p>
		{#if search || outcome}<Button onclick={() => { search = ''; outcome = ''; }}>{a('clearFilters')}</Button>{/if}
	{/snippet}
</DataTable>

<style>
	.event { font-weight: 600; }
	.muted { color: var(--text-muted); }
	.mono { font-family: ui-monospace, SFMono-Regular, Consolas, monospace; font-size: 13px; }
</style>

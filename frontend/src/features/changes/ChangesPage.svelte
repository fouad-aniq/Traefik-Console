<script lang="ts">
	/**
	 * Snapshots & history.
	 *
	 *   Snapshots → restore points taken on demand (and automatically before a restore or a static change).
	 *               Create, rename, pin, delete, compare and restore (with a preview and a restore token).
	 *   History   → the operations log: every write, restore and recovery, with recover/abandon for a stuck one.
	 *
	 * Writes are applied directly by the other pages; there is no pending draft to review here.
	 */
	import { onMount, onDestroy } from 'svelte';
	import { Camera } from '@lucide/svelte';
	import PageHeader from '../../shared/ui/PageHeader.svelte';
	import Tabs from '../../shared/ui/Tabs.svelte';
	import Button from '../../shared/ui/Button.svelte';
	import Notice from '../../shared/ui/Notice.svelte';
	import StatusBadge from '../../shared/ui/StatusBadge.svelte';
	import RelativeTime from '../../shared/ui/RelativeTime.svelte';
	import ValueTooltip from '../../shared/ui/ValueTooltip.svelte';
	import ConfirmDialog from '../../shared/ui/ConfirmDialog.svelte';
	import DataTable from '../../shared/ui/DataTable.svelte';
	import RowActions from '../../shared/ui/RowActions.svelte';
	import SnapshotDialog from './SnapshotDialog.svelte';
	import SnapshotCreateDialog from './SnapshotCreateDialog.svelte';
	import OperationDialog from './OperationDialog.svelte';
	import { deleteSnapshot, listOperations, listSnapshots, patchSnapshot } from './api';
	import { classify, formatBytes, needsAttention, operationBadge, originTone } from './models';
	import type { Operation, Snapshot } from '../../core/types';
	import { errorMessage } from '../../shared/api/errorMessage';
	import { t } from '../../shared/localization/i18n';
	import type { SortState } from '../../shared/ui/tableTypes';

	let { navigate, tab = 'snapshots', mode = 'list', id = '' }: { navigate: (p: string) => void; tab?: string; mode?: string; id?: string } = $props();

	const c = (key: string, args?: Record<string, string | number>) => t('changesV2.' + key, args);

	const history = $derived(tab === 'history');

	let snapshots = $state<Snapshot[]>([]);
	let snapshotsNext = $state(0);
	let snapshotsError = $state('');
	let operations = $state<Operation[]>([]);
	let operationsNext = $state(0);
	let operationsError = $state('');
	let loading = $state(true);
	let loadingMore = $state(false);

	let search = $state('');
	let originFilter = $state('');
	let statusFilter = $state('');
	let page = $state(1);
	let pageSize = $state(25);
	let sort = $state<SortState>({ column: 'id', direction: 'desc' });

	let notice = $state<{ tone: 'info' | 'warning' | 'error'; text: string } | null>(null);
	let deleting = $state<Snapshot | null>(null);
	let busyId = $state(0);

	let generation = 0;
	let disposed = false;
	let controller = new AbortController();

	const stuck = $derived(operations.filter(needsAttention));

	async function load() {
		const own = ++generation;
		controller.abort();
		controller = new AbortController();
		const signal = controller.signal;
		loading = true;
		const [s, o] = await Promise.allSettled([listSnapshots(25, 0, signal), listOperations(25, 0, signal)]);
		if (disposed || own !== generation) return;
		if (s.status === 'fulfilled') { snapshots = s.value.items ?? []; snapshotsNext = s.value.next_cursor; snapshotsError = ''; }
		else { snapshots = []; snapshotsNext = 0; snapshotsError = errorMessage(s.reason, c('snapshotsFailed')); }
		if (o.status === 'fulfilled') { operations = o.value.items ?? []; operationsNext = o.value.next_cursor; operationsError = ''; }
		else { operations = []; operationsNext = 0; operationsError = errorMessage(o.reason, c('historyFailed')); }
		loading = false;
	}

	async function more() {
		if (loadingMore) return;
		loadingMore = true;
		try {
			if (history) {
				const next = await listOperations(25, operationsNext, controller.signal);
				if (disposed) return;
				operations = [...operations, ...(next.items ?? [])]; operationsNext = next.next_cursor;
			} else {
				const next = await listSnapshots(25, snapshotsNext, controller.signal);
				if (disposed) return;
				snapshots = [...snapshots, ...(next.items ?? [])]; snapshotsNext = next.next_cursor;
			}
		} catch (e) {
			if (!disposed) notice = { tone: 'error', text: errorMessage(e, c(history ? 'historyFailed' : 'snapshotsFailed')) };
		} finally {
			if (!disposed) loadingMore = false;
		}
	}

	onMount(() => { void load(); });
	onDestroy(() => { disposed = true; controller.abort(); });

	async function togglePin(row: Snapshot) {
		if (busyId) return;
		busyId = row.id; notice = null;
		try {
			const updated = await patchSnapshot(row.id, { pinned: !row.pinned });
			if (disposed) return;
			snapshots = snapshots.map((s) => (s.id === updated.id ? updated : s));
		} catch (e) {
			if (!disposed) notice = { tone: 'error', text: errorMessage(e, c('pinFailed')) };
		} finally {
			if (!disposed) busyId = 0;
		}
	}

	async function remove() {
		const row = deleting;
		if (!row || busyId) return;
		busyId = row.id; notice = null;
		try {
			await deleteSnapshot(row.id, row.pinned);
			if (disposed) return;
			snapshots = snapshots.filter((s) => s.id !== row.id);
			deleting = null;
		} catch (e) {
			if (disposed) return;
			deleting = null;
			const outcome = classify(e);
			notice = { tone: outcome === 'pinned' ? 'warning' : 'error', text: outcome === 'pinned' ? c('pinnedHelp') : errorMessage(e, c('deleteFailed')) };
			await load();
		} finally {
			if (!disposed) busyId = 0;
		}
	}

	const snapshotColumns = $derived([
		{ id: 'id', label: c('snapshot'), sortable: true, width: 'clamp(200px, 26vw, 360px)' },
		{ id: 'origin', label: c('origin'), sortable: true, width: '150px' },
		{ id: 'created', label: c('when'), sortable: true, width: '150px' },
		{ id: 'items', label: c('items'), sortable: true, width: '90px' },
		{ id: 'size', label: c('size'), sortable: true, width: '110px' },
		{ id: 'actions', label: '', hideLabel: true, width: '140px' }
	]);
	const operationColumns = $derived([
		{ id: 'id', label: c('operation'), sortable: true, width: '110px' },
		{ id: 'kind', label: c('kind'), sortable: true, width: '150px' },
		{ id: 'created', label: c('when'), sortable: true, width: '150px' },
		{ id: 'summary', label: c('summary'), width: 'clamp(220px, 30vw, 460px)' },
		{ id: 'status', label: c('status'), sortable: true, width: '190px' },
		{ id: 'actions', label: '', hideLabel: true, width: '130px' }
	]);
	const snapshotFilters = $derived([{ id: 'origin', label: c('origin'), options: [{ value: '', label: c('all') }, ...[...new Set(snapshots.map((s) => s.origin))].map((value) => ({ value, label: c('originLabel.' + value) }))] }]);
	const operationFilters = $derived([{ id: 'status', label: c('status'), options: [{ value: '', label: c('all') }, ...[...new Set(operations.map((o) => o.status))].map((value) => ({ value, label: c('statusLabel.' + value) }))] }]);

	const filteredSnapshots = $derived.by(() => {
		const q = search.trim().toLowerCase();
		const items = snapshots.filter((s) => (!originFilter || s.origin === originFilter) && (!q || `${s.id} ${s.name} ${s.description}`.toLowerCase().includes(q)));
		const dir = sort.direction === 'asc' ? 1 : -1;
		const key = (s: Snapshot) => (sort.column === 'origin' ? s.origin : sort.column === 'created' ? s.created_at : sort.column === 'items' ? s.item_count : sort.column === 'size' ? s.size_bytes : s.id);
		return items.toSorted((a, b) => (key(a) > key(b) ? 1 : key(a) < key(b) ? -1 : 0) * dir);
	});
	const filteredOperations = $derived.by(() => {
		const q = search.trim().toLowerCase();
		const items = operations.filter((o) => (!statusFilter || o.status === statusFilter) && (!q || `${o.id} ${o.kind} ${o.target} ${o.summary}`.toLowerCase().includes(q)));
		const dir = sort.direction === 'asc' ? 1 : -1;
		const key = (o: Operation) => (sort.column === 'status' ? o.status : sort.column === 'kind' ? o.kind : sort.column === 'created' ? o.created_at : o.id);
		return items.toSorted((a, b) => (key(a) > key(b) ? 1 : key(a) < key(b) ? -1 : 0) * dir);
	});
	const snapshotRows = $derived(filteredSnapshots.slice((page - 1) * pageSize, page * pageSize));
	const operationRows = $derived(filteredOperations.slice((page - 1) * pageSize, page * pageSize));
	const hasFilters = $derived(!!(search || originFilter || statusFilter));

	function resetView() { search = ''; originFilter = ''; statusFilter = ''; page = 1; sort = { column: 'id', direction: 'desc' }; }
	const selectTab = (next: string) => { resetView(); navigate(next === 'history' ? '/changes/history' : '/changes'); };
	const closeDialog = () => navigate(history ? '/changes/history' : '/changes');
</script>

<PageHeader title={t('nav.changes')} subtitle={c('subtitle')}>
	{#snippet actions()}
		<Button variant="primary" onclick={() => navigate('/changes/snapshots/new')}><Camera size={16} aria-hidden="true" /> {c('createSnapshot')}</Button>
	{/snippet}
</PageHeader>

<div class="resource-tabs">
	<Tabs
		tabs={[{ id: 'snapshots', label: c('tabs.snapshots') }, { id: 'history', label: stuck.length ? c('tabs.historyAttention', { count: stuck.length }) : c('tabs.history') }]}
		selected={history ? 'history' : 'snapshots'}
		onselect={selectTab}
		label={t('nav.changes')}
	/>
</div>

<div class="stack">
	{#if notice}<Notice tone={notice.tone} message={notice.text} dismissible onDismiss={() => (notice = null)} />{/if}

	{#if !history}
		<div id="panel-snapshots" role="tabpanel" aria-labelledby="tab-snapshots" class="stack">
			{#if snapshotsError}<Notice tone="error" message={snapshotsError}><Button size="sm" onclick={load}>{t('common.retry')}</Button></Notice>{/if}
			<DataTable
				columns={snapshotColumns}
				rows={snapshotRows}
				getRowId={(s: Snapshot) => String(s.id)}
				searchValue={search}
				onsearch={(v: string) => { search = v; page = 1; }}
				searchPlaceholder={c('searchSnapshots')}
				filters={snapshotFilters}
				filterValues={{ origin: originFilter }}
				onfilterchange={(_: string, v: string) => { originFilter = v; page = 1; }}
				{sort}
				onsort={(column: string) => { sort = { column, direction: sort.column === column && sort.direction === 'desc' ? 'asc' : 'desc' }; page = 1; }}
				{page}
				{pageSize}
				total={filteredSnapshots.length}
				onpage={(v: number) => (page = v)}
				onpagesize={(v: number) => { pageSize = v; page = 1; }}
				{loading}
				error={!!snapshotsError}
				onretry={load}
				regionLabel={c('tabs.snapshots')}
				minWidth={1100}
			>
				{#snippet rowSnippet(row: Snapshot)}
					<td>
						<div class="name">
							<strong>#{row.id} · {row.name}</strong>
							{#if row.pinned}<StatusBadge size="sm" tone="info" text={c('pinned')} />{/if}
							{#if row.known_good}<StatusBadge size="sm" tone="success" text={c('knownGood')} />{/if}
						</div>
						{#if row.description}<ValueTooltip text={row.description} truncate><span class="muted">{row.description}</span></ValueTooltip>{/if}
					</td>
					<td><StatusBadge size="sm" tone={originTone(row.origin)} text={c('originLabel.' + row.origin)} /></td>
					<td><RelativeTime value={row.created_at} /></td>
					<td>{row.item_count}</td>
					<td>{formatBytes(row.size_bytes)}</td>
					<td>
						<RowActions label={'#' + row.id} actions={[
							{ label: c('details'), context: '#' + row.id, onclick: () => navigate('/changes/snapshots/' + row.id) },
							{ label: c('restore'), context: '#' + row.id, onclick: () => navigate('/changes/snapshots/' + row.id + '/restore') },
							{ label: c(row.pinned ? 'unpin' : 'pin'), context: '#' + row.id, disabled: busyId === row.id, onclick: () => togglePin(row) },
							{ label: c('delete'), context: '#' + row.id, variant: 'danger', disabled: busyId === row.id, onclick: () => (deleting = row) }
						]} />
					</td>
				{/snippet}
				{#snippet emptyState()}
					<h2>{c(hasFilters ? 'noMatches' : 'snapshotsEmpty')}</h2>
					<p>{c('snapshotsEmptyHelp')}</p>
					{#if hasFilters}<Button onclick={resetView}>{c('clearFilters')}</Button>{:else}<Button variant="primary" onclick={() => navigate('/changes/snapshots/new')}>{c('createSnapshot')}</Button>{/if}
				{/snippet}
			</DataTable>
			{#if snapshotsNext}<div><Button onclick={more} pending={loadingMore} disabled={loadingMore}>{c('loadMore')}</Button></div>{/if}
			<p class="muted">{c('retentionNote')}</p>
		</div>
	{:else}
		<div id="panel-history" role="tabpanel" aria-labelledby="tab-history" class="stack">
			{#if operationsError}<Notice tone="error" message={operationsError}><Button size="sm" onclick={load}>{t('common.retry')}</Button></Notice>{/if}
			{#if stuck.length}
				<Notice tone="warning" title={c('attentionTitle')} message={c('attentionHelp')}>
					<ul class="reasons">{#each stuck as o (o.id)}<li><button type="button" class="link" onclick={() => navigate('/changes/history/' + o.id)}>#{o.id} · {c('statusLabel.' + o.status)}</button></li>{/each}</ul>
				</Notice>
			{/if}
			<DataTable
				columns={operationColumns}
				rows={operationRows}
				getRowId={(o: Operation) => String(o.id)}
				searchValue={search}
				onsearch={(v: string) => { search = v; page = 1; }}
				searchPlaceholder={c('searchOperations')}
				filters={operationFilters}
				filterValues={{ status: statusFilter }}
				onfilterchange={(_: string, v: string) => { statusFilter = v; page = 1; }}
				{sort}
				onsort={(column: string) => { sort = { column, direction: sort.column === column && sort.direction === 'desc' ? 'asc' : 'desc' }; page = 1; }}
				{page}
				{pageSize}
				total={filteredOperations.length}
				onpage={(v: number) => (page = v)}
				onpagesize={(v: number) => { pageSize = v; page = 1; }}
				{loading}
				error={!!operationsError}
				onretry={load}
				regionLabel={c('tabs.history')}
				minWidth={1000}
			>
				{#snippet rowSnippet(row: Operation)}
					<td><strong>#{row.id}</strong></td>
					<td>{row.kind}{#if row.target}<span class="muted"> · {row.target}</span>{/if}</td>
					<td><RelativeTime value={row.created_at} /></td>
					<td><ValueTooltip text={row.summary} truncate>{row.summary || '—'}</ValueTooltip></td>
					<td>
						<StatusBadge size="sm" tone={operationBadge(row.status)} text={c('statusLabel.' + row.status)} />
						{#if row.requires_restart}<span class="muted"> · {c('restartNeeded')}</span>{/if}
					</td>
					<td><RowActions actions={[{ label: c('details'), context: '#' + row.id, onclick: () => navigate('/changes/history/' + row.id) }]} /></td>
				{/snippet}
				{#snippet emptyState()}
					<h2>{c(hasFilters ? 'noMatches' : 'historyEmpty')}</h2>
					<p>{c('historyEmptyHelp')}</p>
					{#if hasFilters}<Button onclick={resetView}>{c('clearFilters')}</Button>{/if}
				{/snippet}
			</DataTable>
			{#if operationsNext}<div><Button onclick={more} pending={loadingMore} disabled={loadingMore}>{c('loadMore')}</Button></div>{/if}
		</div>
	{/if}
</div>

<ConfirmDialog
	open={!!deleting}
	danger
	title={c('deleteTitle', { id: deleting?.id ?? 0 })}
	message={deleting?.pinned ? c('deletePinnedMessage', { name: deleting?.name ?? '' }) : c('deleteMessage', { name: deleting?.name ?? '' })}
	cancelLabel={t('common.cancel')}
	confirmLabel={deleting?.pinned ? c('deleteForce') : c('delete')}
	oncancel={() => { if (!busyId) deleting = null; }}
	onconfirm={remove}
/>

{#if mode === 'create'}
	<SnapshotCreateDialog {navigate} oncreated={load} />
{:else if mode === 'view' || mode === 'restore'}
	{#key `${mode}:${id}`}
		<SnapshotDialog {mode} {id} {navigate} onchanged={load} onclose={closeDialog} />
	{/key}
{:else if mode === 'operation'}
	{#key id}
		<OperationDialog {id} onchanged={load} onclose={closeDialog} />
	{/key}
{/if}

<style>
	.resource-tabs { margin-bottom: 24px; }
	.stack { display: flex; flex-direction: column; gap: 20px; }
	.muted { margin: 0; color: var(--text-muted); font-size: 14px; line-height: 21px; }
	.reasons { margin: 8px 0 0; padding-left: 20px; font-size: 14px; line-height: 22px; }
	.name { display: flex; align-items: center; gap: 8px; flex-wrap: wrap; }
	.link { border: 0; background: transparent; padding: 0; color: var(--connection); font: inherit; cursor: pointer; }
	.link:hover { text-decoration: underline; }
</style>

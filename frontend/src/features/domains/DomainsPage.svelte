<script lang="ts">
	import { onMount, onDestroy } from 'svelte';
	import { ListTree } from '@lucide/svelte';
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
	import { certificateItem, routeItem, type RelatedItem } from '../../shared/ui/related';
	import ConfirmDialog from '../../shared/ui/ConfirmDialog.svelte';
	import DomainDialog from './DomainDialog.svelte';
	import DomainSummaryDialog from './DomainSummaryDialog.svelte';
	import { deleteDomain, listDomains } from './api';
	import { referenceOf, routersOf, usageCount, type CertificateState, type DomainResource } from './models';
	import { validityTone } from '../certificates/models';
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

	const c = (key: string, args?: Record<string, string | number>) => t('domainFlow.' + key, args);
	const u = (key: string) => t('domainUI.' + key);
	const v2 = (key: string, args?: Record<string, string | number>) => t('domainV2.' + key, args);
	const rv2 = (key: string, args?: Record<string, string | number>) => t('resolverV2.' + key, args);

	let rows = $state<DomainResource[]>([]);
	let loading = $state(true);
	let error = $state('');
	let actionError = $state('');
	let restartNotice = $state(false);
	let search = $state('');
	let method = $state('');
	let page = $state(1);
	let pageSize = $state(25);
	let sort = $state<SortState>({ column: 'main', direction: 'asc' });
	let usage = $state<DomainResource | null>(null);

	/** "Used by" items come with the list (core-v2 `usage`): routes first, then certificates. */
	const usageItems = (row: DomainResource): RelatedItem[] => [
		...(row.config.usage?.routers ?? []).map((r) =>
			routeItem({
				name: r.name,
				read_only: r.read_only,
				config: { rule: r.rule, entry_points: r.entry_points, service: r.service, tls: r.tls || undefined },
			}),
		),
		...(row.config.usage?.certificates ?? []).map((c) => certificateItem(c, (v) => t('certificateUI.' + v))),
	];
	const usageUnknown = $derived(rows.some((r) => !r.config.usage));
	let pendingDelete = $state<DomainResource | null>(null);
	let deleting = $state(false);

	let generation = 0;
	let disposed = false;
	let controller = new AbortController();

	const columns = $derived([
		{ id: 'main', label: c('domainName'), sortable: true, width: 'clamp(180px, 25vw, 360px)' },
		{ id: 'method', label: c('method'), sortable: true, width: '140px' },
		{ id: 'reference', label: c('reference'), sortable: true, width: '200px' },
		{ id: 'certificate', label: c('certificateState'), width: '176px' },
		{ id: 'usedBy', label: c('usedBy'), width: '150px' },
		{ id: 'updatedAt', label: c('updated'), sortable: true, width: '170px' },
		{ id: 'actions', label: u('actions'), hideLabel: true, width: '128px' },
	]);
	const filters = $derived([
		{
			id: 'method',
			label: c('method'),
			options: [
				{ value: '', label: u('all') },
				{ value: 'resolver', label: c('automatic') },
				{ value: 'files', label: c('uploaded') },
			],
		},
	]);

	const stateOf = (row: DomainResource): CertificateState => row.config.certificate_state ?? 'unavailable';
	const sortValue = (row: DomainResource, column: string) =>
		column === 'updatedAt'
			? (row.updated_at ?? '')
			: column === 'method'
				? row.config.method
				: column === 'reference'
					? referenceOf(row.config)
					: column === 'state'
						? row.status
						: row.config.main;
	const filtered = $derived.by(() => {
		const q = search.trim().toLowerCase();
		const items = rows.filter(
			(r) =>
				(!method || r.config.method === method) &&
				(!q ||
					`${r.config.main} ${(r.config.sans ?? []).join(' ')} ${r.config.resolver ?? ''} ${r.config.certificate ?? ''} ${r.name}`
						.toLowerCase()
						.includes(q)),
		);
		return items.toSorted(
			(a, b) =>
				sortValue(a, sort.column).localeCompare(sortValue(b, sort.column)) * (sort.direction === 'desc' ? -1 : 1),
		);
	});
	const pageRows = $derived(filtered.slice((page - 1) * pageSize, page * pageSize));
	const hasFilters = $derived(!!(search || method));

	async function load() {
		const own = ++generation;
		controller.abort();
		controller = new AbortController();
		const signal = controller.signal;
		loading = true;
		error = '';
		try {
			const list = await listDomains(signal);
			if (disposed || own !== generation) return;
			rows = list;
			page = 1;
		} catch (e) {
			if (disposed || own !== generation) return;
			rows = [];
			error = errorMessage(e, u('loadFailed'));
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
		method = '';
		page = 1;
	}

	async function confirmDelete() {
		const target = pendingDelete;
		if (!target || deleting) return;
		deleting = true;
		actionError = '';
		try {
			// The version read with the list guards the delete; a 409 is surfaced, never retried.
			const res = await deleteDomain(target.name, rows.find((r) => r.name === target.name)?.version ?? 0);
			if (needsManualRestart(res)) restartNotice = true;
			pendingDelete = null;
			await load();
		} catch (e) {
			pendingDelete = null;
			actionError =
				e instanceof ApiError && e.status === 409 ? e.message || rv2('conflict') : errorMessage(e, v2('deleteFailed'));
		} finally {
			deleting = false;
		}
	}

	const stateTone = (s: CertificateState) =>
		s === 'missingCertificate' || s === 'notCovered'
			? 'danger'
			: s === 'notObserved' || s === 'unavailable'
				? 'neutral'
				: validityTone(s);
	const stateLabel = (s: CertificateState) =>
		['valid', 'expiring', 'expired', 'not_yet_valid'].includes(s) ? t('certificateUI.' + s) : c(s);
	const refPath = (r: DomainResource) =>
		r.config.method === 'resolver'
			? '/resolvers/' + encodeURIComponent(r.config.resolver ?? '')
			: '/certificates/uploaded/' + encodeURIComponent(r.config.certificate ?? '');
	const canChange = (r: DomainResource) => !r.read_only;
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
	{#if usageUnknown && !loading && !error}<Notice tone="warning" message={c('usageUnavailable')} />{/if}
</div>

<DataTable
	{columns}
	rows={pageRows}
	getRowId={(r: DomainResource) => r.name}
	searchValue={search}
	onsearch={(v: string) => {
		search = v;
		page = 1;
	}}
	searchPlaceholder={c('search')}
	{filters}
	filterValues={{ method }}
	onfilterchange={(id: string, v: string) => {
		if (id === 'method') method = v;
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
	minWidth={1500}
>
	{#snippet toolbarActions()}
		<Button variant="primary" onclick={() => navigate('/domains/new')}>{c('add')}</Button>
	{/snippet}

	{#snippet rowSnippet(row: DomainResource)}
		{@const state = stateOf(row)}
		<td>
			<div class="identity">
				<span class="name">
					<ValueTooltip
						text={row.config.main}
						truncate
						onactivate={() => navigate('/domains/' + encodeURIComponent(row.name))}
					>
						<strong>{row.config.main}</strong>
					</ValueTooltip>
				</span>
				{#if row.config.sans?.length}<ValueTooltip
						compact
						label={c('additionalNames')}
						text={row.config.sans.join('\n')}
					>
						<ListTree size={16} aria-hidden="true" />
					</ValueTooltip>{/if}
			</div>
		</td>
		<td>
			<StatusBadge
				size="sm"
				tone={row.config.method === 'resolver' ? 'connection' : 'info'}
				text={c(row.config.method === 'resolver' ? 'automatic' : 'uploaded')}
			/>
		</td>
		<td>
			<ValueTooltip text={referenceOf(row.config)} truncate onactivate={() => navigate(refPath(row))}>
				{referenceOf(row.config) || '—'}
			</ValueTooltip>
		</td>
		<td><StatusBadge size="sm" tone={stateTone(state)} text={stateLabel(state)} /></td>
		<td>
			{#if row.config.usage}<ReferenceCount count={usageCount(row)} onclick={() => (usage = row)} />
			{:else}<span class="muted">{c('unknown')}</span>{/if}
		</td>
		<td>
			{#if row.updated_at}<RelativeTime value={row.updated_at} />{:else}—{/if}
		</td>
		<td>
			<RowActions
				label={row.name}
				actions={[
					{ label: c('view'), onclick: () => navigate('/domains/' + encodeURIComponent(row.name)) },
					canChange(row) && {
						label: u('editAction'),
						onclick: () => navigate('/domains/' + encodeURIComponent(row.name) + '/edit'),
					},
					canChange(row) && {
						label: rv2('delete.action'),
						variant: 'danger',
						disabled: routersOf(row).length > 0,
						onclick: () => (pendingDelete = row),
					},
				]}
			/>
		</td>
	{/snippet}

	{#snippet emptyState()}
		<h2>{c(hasFilters ? 'noMatches' : 'empty')}</h2>
		<p>{c(hasFilters ? 'adjustFilters' : 'emptyHelp')}</p>
		<Button onclick={() => (hasFilters ? clearFilters() : navigate('/domains/new'))}>
			{c(hasFilters ? 'clearFilters' : 'add')}
		</Button>
	{/snippet}
</DataTable>

<ConfirmDialog
	open={!!pendingDelete}
	danger
	title={v2('delete.title')}
	message={pendingDelete ? v2('delete.message', { name: pendingDelete.config.main }) : ''}
	cancelLabel={t('common.cancel')}
	confirmLabel={rv2('delete.confirm')}
	oncancel={() => {
		if (!deleting) pendingDelete = null;
	}}
	onconfirm={confirmDelete}
/>

{#if mode !== 'list'}
	{#key `${mode}:${name}`}
		{#if mode === 'view'}
			<DomainSummaryDialog {name} {navigate} {rows} {loading} onreload={load} />
		{:else}
			<DomainDialog {mode} {name} {navigate} onchanged={load} />
		{/if}
	{/key}
{/if}

{#if usage}
	<RelatedItemsDialog
		title={c('usedBy') + ' · ' + usage.config.main}
		description={c('usageHelp')}
		items={usageItems(usage)}
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
	}
	.name strong {
		font-weight: 600;
	}
	.muted {
		color: var(--text-muted);
		font-size: 14px;
	}
</style>

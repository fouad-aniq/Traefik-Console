<script lang="ts">
	import { onMount, onDestroy } from 'svelte';
	import DataTable from '../../shared/ui/DataTable.svelte';
	import RowActions from '../../shared/ui/RowActions.svelte';
	import PageHeader from '../../shared/ui/PageHeader.svelte';
	import Button from '../../shared/ui/Button.svelte';
	import Notice from '../../shared/ui/Notice.svelte';
	import StatusBadge from '../../shared/ui/StatusBadge.svelte';
	import ValueTooltip from '../../shared/ui/ValueTooltip.svelte';
	import ConfirmDialog from '../../shared/ui/ConfirmDialog.svelte';
	import ResolverDialog from './ResolverDialog.svelte';
	import { listResolvers, listProviders, deleteResolver } from './api';
	import type { Provider, ResolverResource } from './models';
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

	const c = (key: string, args?: Record<string, string | number>) => t('resolverUI.' + key, args);
	const v2 = (key: string, args?: Record<string, string | number>) => t('resolverV2.' + key, args);

	let rows = $state<ResolverResource[]>([]);
	let providers = $state<Provider[]>([]);
	let loading = $state(true);
	let error = $state('');
	let actionError = $state('');
	let restartNotice = $state(false);
	let search = $state('');
	let origin = $state('');
	let challenge = $state('');
	let environment = $state('');
	let page = $state(1);
	let pageSize = $state(25);
	let sort = $state<SortState>({ column: 'name', direction: 'asc' });
	let pendingDelete = $state<ResolverResource | null>(null);
	let deleting = $state(false);

	let generation = 0;
	let disposed = false;
	let controller = new AbortController();

	const columns = $derived([
		{ id: 'name', label: c('name'), sortable: true, width: 'clamp(180px, 26vw, 360px)' },
		{ id: 'challenge', label: c('challengeColumn'), sortable: true, width: '132px' },
		{ id: 'provider', label: c('provider'), sortable: true, width: '190px' },
		{ id: 'environment', label: c('environment'), sortable: true, width: '190px' },
		{ id: 'actions', label: c('actions'), hideLabel: true, width: '172px' },
	]);

	const filters = $derived([
		{
			id: 'origin',
			label: c('ownership'),
			options: [
				{ value: '', label: c('all') },
				{ value: 'managed', label: c('managed') },
				{ value: 'external', label: c('external') },
			],
		},
		{
			id: 'challenge',
			label: c('challenge'),
			options: [
				{ value: '', label: c('all') },
				...['dns01', 'http01', 'tlsalpn01'].map((value) => ({ value, label: c(value) })),
			],
		},
		{
			id: 'environment',
			label: c('environment'),
			options: [
				{ value: '', label: c('all') },
				{ value: 'production', label: c('production') },
				{ value: 'staging', label: c('staging') },
				{ value: 'custom', label: c('customCA') },
			],
		},
	]);

	const providerName = (code = '') => providers.find((p) => p.provider_code === code)?.display_name || code;
	const sortValue = (row: ResolverResource, column: string) => {
		switch (column) {
			case 'name':
				return row.name;
			case 'challenge':
				return row.config.challenge ?? '';
			case 'provider':
				return providerName(row.config.provider);
			case 'environment':
				return row.config.environment ?? '';
			case 'state':
				return row.status;
			default:
				return '';
		}
	};

	const filtered = $derived.by(() => {
		const q = search.trim().toLowerCase();
		const items = rows.filter(
			(row) =>
				(!origin || row.origin === origin) &&
				(!challenge || row.config.challenge === challenge) &&
				(!environment || row.config.environment === environment) &&
				(!q ||
					`${row.name} ${providerName(row.config.provider)} ${row.config.environment ?? ''} ${row.config.challenge ?? ''}`
						.toLowerCase()
						.includes(q)),
		);
		return items.toSorted(
			(a, b) =>
				sortValue(a, sort.column).localeCompare(sortValue(b, sort.column)) * (sort.direction === 'desc' ? -1 : 1),
		);
	});
	const pageRows = $derived(filtered.slice((page - 1) * pageSize, page * pageSize));
	const hasFilters = $derived(!!(search || origin || challenge || environment));

	async function load() {
		const own = ++generation;
		controller.abort();
		controller = new AbortController();
		const signal = controller.signal;
		loading = true;
		error = '';
		const [resolvers] = await Promise.allSettled([listResolvers(signal)]);
		if (disposed || own !== generation) return;
		if (resolvers.status === 'fulfilled') {
			rows = resolvers.value;
			page = 1;
		} else {
			rows = [];
			error = errorMessage(resolvers.reason, c('loadFailed'));
		}
		loading = false;
	}

	onMount(() => {
		void load();
		void listProviders(controller.signal)
			.then((v) => {
				if (!disposed) providers = v.providers;
			})
			.catch(() => {});
	});
	onDestroy(() => {
		disposed = true;
		controller.abort();
	});

	function clearFilters() {
		search = '';
		origin = '';
		challenge = '';
		environment = '';
		page = 1;
	}

	async function confirmDelete() {
		const target = pendingDelete;
		if (!target || deleting) return;
		deleting = true;
		actionError = '';
		try {
			// The version read with the list guards the delete; a 409 is surfaced, never retried.
			const res = await deleteResolver(target.name, rows.find((r) => r.name === target.name)?.version ?? 0);
			if (needsManualRestart(res)) restartNotice = true;
			pendingDelete = null;
			await load();
		} catch (e) {
			pendingDelete = null;
			actionError =
				e instanceof ApiError && e.status === 409
					? v2('conflict') + (e.message ? ' ' + e.message : '')
					: errorMessage(e, v2('deleteFailed'));
		} finally {
			deleting = false;
		}
	}
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
	getRowId={(r: ResolverResource) => r.name}
	searchValue={search}
	onsearch={(v: string) => {
		search = v;
		page = 1;
	}}
	searchPlaceholder={c('search')}
	{filters}
	filterValues={{ origin, challenge, environment }}
	onfilterchange={(id: string, v: string) => {
		if (id === 'origin') origin = v;
		else if (id === 'challenge') challenge = v;
		else if (id === 'environment') environment = v;
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
	minWidth={1100}
>
	{#snippet toolbarActions()}
		<Button variant="primary" onclick={() => navigate('/resolvers/new')}>{c('create')}</Button>
	{/snippet}

	{#snippet rowSnippet(row: ResolverResource)}
		<td>
			<div class="identity">
				<span class="name">
					<ValueTooltip
						text={row.name}
						truncate
						onactivate={() => navigate('/resolvers/' + encodeURIComponent(row.name))}
					>
						<strong>{row.name}</strong>
					</ValueTooltip>
				</span>
				{#if row.origin === 'external'}<StatusBadge size="sm" tone="info" text={c('externalShort')} />{/if}
			</div>
		</td>
		<td>
			<StatusBadge
				size="sm"
				tone={row.config.challenge ? 'connection' : 'neutral'}
				text={row.config.challenge
					? c(row.config.challenge)
					: row.config.type === 'custom'
						? c('custom')
						: c('notReported')}
			/>
		</td>
		<td>
			{#if providerName(row.config.provider)}
				<ValueTooltip text={providerName(row.config.provider)} truncate>
					{providerName(row.config.provider)}
				</ValueTooltip>
			{:else}
				<span class="muted" aria-label={c('notReported')}>—</span>
			{/if}
		</td>
		<td>
			<StatusBadge
				size="sm"
				tone={row.config.environment === 'staging' ? 'warning' : 'neutral'}
				text={row.config.environment
					? c(row.config.environment === 'custom' ? 'customCA' : row.config.environment)
					: c('notReported')}
			/>
		</td>
		<td>
			<RowActions
				label={row.name}
				actions={[
					{
						label: c(row.read_only ? 'view' : 'editAction'),
						onclick: () => navigate('/resolvers/' + encodeURIComponent(row.name) + (row.read_only ? '' : '/edit')),
					},
					!row.read_only && { label: v2('delete.action'), variant: 'danger', onclick: () => (pendingDelete = row) },
				]}
			/>
		</td>
	{/snippet}

	{#snippet emptyState()}
		<h2>{c(hasFilters ? 'noMatches' : 'empty')}</h2>
		<p>{c(hasFilters ? 'adjustFilters' : 'emptyHelp')}</p>
		<Button onclick={() => (hasFilters ? clearFilters() : navigate('/resolvers/new'))}>
			{c(hasFilters ? 'clearFilters' : 'create')}
		</Button>
	{/snippet}
</DataTable>

<ConfirmDialog
	open={!!pendingDelete}
	danger
	title={v2('delete.title')}
	message={pendingDelete ? v2('delete.message', { name: pendingDelete.name }) : ''}
	cancelLabel={t('common.cancel')}
	confirmLabel={v2('delete.confirm')}
	oncancel={() => {
		if (!deleting) pendingDelete = null;
	}}
	onconfirm={confirmDelete}
/>

{#if mode !== 'list'}
	{#key `${mode}:${name}`}
		<ResolverDialog {mode} {name} {navigate} onchanged={load} />
	{/key}
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
	.muted {
		color: var(--text-muted);
	}
</style>

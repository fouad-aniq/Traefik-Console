<script lang="ts">
	import { onDestroy, onMount } from 'svelte';
	import DataTable from '../../shared/ui/DataTable.svelte';
	import ResourceDialog from '../../shared/ui/ResourceDialog.svelte';
	import SummaryList from '../../shared/ui/SummaryList.svelte';
	import RelativeTime from '../../shared/ui/RelativeTime.svelte';
	import StatusBadge from '../../shared/ui/StatusBadge.svelte';
	import Notice from '../../shared/ui/Notice.svelte';
	import Button from '../../shared/ui/Button.svelte';
	import ValueTooltip from '../../shared/ui/ValueTooltip.svelte';
	import { errorMessage } from '../../shared/api/errorMessage';
	import { t } from '../../shared/localization/i18n';
	import { getAlert, listAlerts } from './api';
	import { countryName, isPrivateAddress, type CrowdSecAlert } from './models';
	import { prefs } from '../../shared/preferences/preferences.svelte';

	const c = (key: string, args?: Record<string, string | number>) => t('crowdsecV2.' + key, args);
	let rows = $state<CrowdSecAlert[]>([]);
	let loading = $state(true);
	let error = $state('');
	let loaded = $state(false);
	let hasNext = $state(false);
	let observedAt = $state('');
	let searchInput = $state('');
	let scenario = $state('');
	let origin = $state('');
	let page = $state(1);
	let pageSize = $state(25);
	let selected = $state<CrowdSecAlert | null>(null);
	let detailLoading = $state(false);
	let detailError = $state('');
	let disposed = false;
	let controller = new AbortController();
	let generation = 0;
	let searchTimer: ReturnType<typeof setTimeout> | undefined;

	const columns = $derived([
		{ id: 'created', label: c('alerts.time'), width: '150px' },
		{ id: 'scenario', label: c('alerts.scenario'), width: 'clamp(220px, 28vw, 360px)' },
		{ id: 'source', label: c('alerts.source'), width: '190px' },
		{ id: 'country', label: c('alerts.country'), width: '120px' },
		{ id: 'events', label: c('alerts.events'), width: '100px' },
		{ id: 'decision', label: c('alerts.decision'), width: '130px' },
		{ id: 'actions', label: '', hideLabel: true, width: '112px' }
	]);
	const filters = $derived([{ id: 'origin', label: c('alerts.origin'), options: [
		{ value: '', label: t('resolverUI.all') },
		...['crowdsec', 'CAPI', 'cscli', 'lists'].map((value) => ({ value, label: value }))
	] }]);

	async function load() {
		const own = ++generation;
		controller.abort();
		controller = new AbortController();
		loading = true;
		error = '';
		try {
			const result = await listAlerts({ page: page - 1, limit: pageSize, sinceHours: 24, scenario, origin }, controller.signal);
			if (disposed || own !== generation) return;
			rows = result.items ?? [];
			hasNext = result.has_more;
			observedAt = result.observed_at;
			loaded = true;
		} catch (e) {
			if (disposed || own !== generation) return;
			error = errorMessage(e, c('alerts.loadFailed'));
		} finally {
			if (!disposed && own === generation) loading = false;
		}
	}

	function debounceScenario(value: string) {
		searchInput = value;
		clearTimeout(searchTimer);
		searchTimer = setTimeout(() => {
			scenario = value.trim();
			page = 1;
			void load();
		}, 250);
	}

	async function showDetails(row: CrowdSecAlert) {
		selected = row;
		detailLoading = true;
		detailError = '';
		try {
			selected = await getAlert(row.id);
		} catch (e) {
			detailError = errorMessage(e, c('alerts.detailFailed'));
		} finally {
			detailLoading = false;
		}
	}

	onMount(() => void load());
	onDestroy(() => {
		disposed = true;
		clearTimeout(searchTimer);
		controller.abort();
	});
</script>

{#if error}
	<div class="notice"><Notice tone="error" message={error}><Button size="sm" onclick={load}>{t('common.retry')}</Button></Notice></div>
{/if}
{#if loaded && observedAt}
	<p class="observed muted">{c('stats.updated')} <RelativeTime value={observedAt} /></p>
{/if}
<DataTable
	{columns}
	{rows}
	getRowId={(row: CrowdSecAlert) => String(row.id)}
	searchValue={searchInput}
	onsearch={debounceScenario}
	searchPlaceholder={c('alerts.search')}
	{filters}
	filterValues={{ origin }}
	onfilterchange={(_id: string, value: string) => { origin = value; page = 1; void load(); }}
	{page}
	{pageSize}
	total={0}
	{hasNext}
	pageResultCount={rows.length}
	onpage={(value: number) => { page = value; void load(); }}
	onpagesize={(value: number) => { pageSize = value; page = 1; void load(); }}
	{loading}
	error={!!error && !loaded}
	onretry={load}
	regionLabel={c('alerts.title')}
	minWidth={1050}
>
	{#snippet toolbarActions()}<Button onclick={load} pending={loading} disabled={loading}>{c('refresh')}</Button>{/snippet}
	{#snippet rowSnippet(row: CrowdSecAlert)}
		<td><RelativeTime value={row.created_at} /></td>
		<td><ValueTooltip text={row.reason} truncate><strong>{row.reason || '—'}</strong></ValueTooltip>{#if row.kind}<span class="secondary">{row.kind}</span>{/if}</td>
		<td><ValueTooltip text={row.value} truncate>{row.value || '—'}</ValueTooltip><span class="secondary">{row.scope}</span></td>
		<td>{row.country ? `${countryName(row.country, prefs.language)} (${row.country})` : isPrivateAddress(row.value) ? t('crowdsecV2.dashboard.localNetwork') : '—'}</td>
		<td class="numeric">{row.events_count}</td>
		<td>{#if row.decisions?.length}<StatusBadge size="sm" tone="danger" text={row.decisions[0].type || c('alerts.hasDecision')} />{:else}—{/if}</td>
		<td><Button size="sm" onclick={() => showDetails(row)}>{c('alerts.details')}</Button></td>
	{/snippet}
	{#snippet emptyState()}
		<h2>{c(scenario || origin ? 'alerts.noMatches' : 'alerts.empty')}</h2>
		<p>{c(scenario || origin ? 'alerts.adjust' : 'alerts.emptyHelp')}</p>
		{#if scenario || origin}<Button onclick={() => { searchInput = ''; scenario = ''; origin = ''; page = 1; void load(); }}>{c('decisions.clear')}</Button>{/if}
	{/snippet}
</DataTable>

{#if selected}
	<ResourceDialog title={c('alerts.detailTitle')} description={selected.reason} closeLabel={t('common.close')} onclose={() => (selected = null)}>
		{#if detailError}<Notice tone="error" message={detailError} />{/if}
		{#if detailLoading}<p role="status">{t('table.loading')}</p>{:else}
			<SummaryList>
				<div><dt>{c('alerts.time')}</dt><dd><RelativeTime value={selected.created_at} /></dd></div>
				<div><dt>{c('alerts.scenario')}</dt><dd>{selected.reason || '—'}</dd></div>
				<div><dt>{c('alerts.source')}</dt><dd>{selected.scope}: {selected.value || '—'}</dd></div>
				<div><dt>{c('alerts.country')}</dt><dd>{selected.country || '—'}</dd></div>
				<div><dt>{c('alerts.events')}</dt><dd>{selected.events_count}</dd></div>
			</SummaryList>
			{#if selected.decisions?.length}
				<section class="decision-list"><h3>{c('alerts.decisions')}</h3>
					{#each selected.decisions as decision (decision.id)}<p><StatusBadge size="sm" tone="danger" text={decision.type} /> {decision.scope}: {decision.value} · {decision.duration}</p>{/each}
				</section>
			{/if}
		{/if}
		{#snippet footer()}<Button onclick={() => (selected = null)}>{t('common.close')}</Button>{/snippet}
	</ResourceDialog>
{/if}

<style>
	.notice { margin-bottom: 16px; }
	.observed { margin: 0 0 12px; }
	.muted, .secondary { color: var(--text-muted); font-size: 13px; }
	.secondary { display: block; font-size: 12px; }
	.numeric { font-variant-numeric: tabular-nums; }
	.decision-list { margin-top: 24px; }
	.decision-list h3 { font-size: 16px; margin: 0 0 12px; }
	.decision-list p { display: flex; flex-wrap: wrap; align-items: center; gap: 8px; }
</style>

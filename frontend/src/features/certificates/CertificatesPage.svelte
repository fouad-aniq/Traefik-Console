<script lang="ts">
	import { onMount, onDestroy } from 'svelte';
	import { ListTree } from '@lucide/svelte';
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
	import ConfirmDialog from '../../shared/ui/ConfirmDialog.svelte';
	import CertificateDialog from './CertificateDialog.svelte';
	import CertificateUploadDialog from './CertificateUploadDialog.svelte';
	import { deleteCertificate, getInventory, listCertificates } from './api';
	import {
		acmeKey,
		acmeNames,
		usageCount,
		usageItems,
		validityOf,
		validityTone,
		type AcmeCertificate,
		type AcmeObservation,
		type CertificateResource,
		type CertificateUsage,
	} from './models';
	import { ApiError } from '../../shared/api/client';
	import { errorMessage } from '../../shared/api/errorMessage';
	import { needsManualRestart } from '../../core/mutations';
	import { t } from '../../shared/localization/i18n';
	import type { DataTableColumn, SortState } from '../../shared/ui/tableTypes';

	let {
		navigate,
		tab = 'automatic',
		mode = 'list',
		name = '',
	}: { navigate: (p: string) => void; tab?: string; mode?: string; name?: string } = $props();

	const c = (key: string, args?: Record<string, string | number>) => t('certificateFlow.' + key, args);
	const old = (key: string) => t('certificateUI.' + key);
	const v2 = (key: string, args?: Record<string, string | number>) => t('certV2.' + key, args);
	const rv2 = (key: string, args?: Record<string, string | number>) => t('resolverV2.' + key, args);

	const uploaded = $derived(tab === 'uploaded');
	const title = $derived(c(uploaded ? 'uploaded' : 'automatic'));

	let automatic = $state<AcmeCertificate[]>([]);
	let observations = $state<AcmeObservation[]>([]);
	let partial = $state(false);
	let files = $state<CertificateResource[]>([]);
	let loading = $state(true);
	let errors = $state<Record<string, string>>({});
	let actionError = $state('');
	let restartNotice = $state(false);
	let search = $state('');
	let validity = $state('');
	let page = $state(1);
	let pageSize = $state(25);
	let sort = $state<SortState>({ column: 'name', direction: 'asc' });
	let usage = $state<{ name: string; used_by: CertificateUsage } | null>(null);
	let pendingDelete = $state<CertificateResource | null>(null);
	let deleting = $state(false);

	let generation = 0;
	let disposed = false;
	let controller = new AbortController();

	const columns = $derived<DataTableColumn[]>([
		{
			id: 'name',
			label: c(uploaded ? 'certificateName' : 'coveredNames'),
			sortable: true,
			width: 'clamp(200px, 25vw, 360px)',
		},
		...(uploaded ? [] : [{ id: 'resolver', label: c('resolver'), sortable: true, width: '188px' }]),
		{ id: 'issuer', label: c('issuer'), sortable: true, width: '230px' },
		{ id: 'expires', label: c('expires'), sortable: true, width: '170px' },
		{ id: 'validity', label: c('validity'), sortable: true, width: '160px' },
		{ id: 'usedBy', label: c('usage'), width: '150px' },
		{ id: 'actions', label: old('actions'), hideLabel: true, width: uploaded ? '128px' : '96px' },
	]);

	const filters = $derived([
		{
			id: 'validity',
			label: c('validity'),
			options: [
				{ value: '', label: old('all') },
				...['valid', 'expiring', 'expired', 'not_yet_valid'].map((value) => ({ value, label: old(value) })),
			],
		},
	]);

	type Row = {
		key: string;
		label: string;
		names: string[];
		resolver: string;
		issuer: string;
		expires: string;
		validity: string;
		state: string;
		usedBy?: CertificateUsage;
		acme?: AcmeCertificate;
		file?: CertificateResource;
	};
	const rows = $derived<Row[]>(
		uploaded
			? files.map((f) => ({
					key: f.name,
					label: f.name,
					names: f.config.domains ?? [],
					resolver: '',
					issuer: f.config.issuer,
					expires: f.config.not_after,
					validity: validityOf(f.config.status),
					state: f.status,
					usedBy: f.config.used_by,
					file: f,
				}))
			: automatic.map((a) => ({
					key: acmeKey(a),
					label: a.main_domain,
					names: acmeNames(a),
					resolver: a.resolver_name,
					issuer: a.issuer,
					expires: a.expires_at,
					validity: validityOf(a.status),
					state: '',
					usedBy: a.used_by,
					acme: a,
				})),
	);

	const sortValue = (row: Row, column: string) =>
		column === 'expires'
			? row.expires
			: column === 'validity'
				? row.validity
				: column === 'state'
					? row.state
					: column === 'resolver'
						? row.resolver
						: column === 'issuer'
							? row.issuer
							: row.label;
	const filtered = $derived.by(() => {
		const q = search.trim().toLowerCase();
		const items = rows.filter(
			(r) =>
				(!validity || r.validity === validity) &&
				(!q || `${r.label} ${r.names.join(' ')} ${r.issuer} ${r.resolver}`.toLowerCase().includes(q)),
		);
		return items.toSorted(
			(a, b) =>
				sortValue(a, sort.column).localeCompare(sortValue(b, sort.column)) * (sort.direction === 'desc' ? -1 : 1),
		);
	});
	const pageRows = $derived(filtered.slice((page - 1) * pageSize, page * pageSize));
	const hasFilters = $derived(!!(search || validity));
	const tableError = $derived(errors[uploaded ? 'uploaded' : 'automatic'] ?? '');
	/** Usage is computed by core-v2; it is missing only when the domains or routers could not be read. */
	const usageUnknown = $derived(uploaded ? files.some((f) => !f.config.used_by) : automatic.some((a) => !a.used_by));

	/** Storage nothing has requested a certificate from yet is expected to be empty or absent. */
	const isExpectedEmpty = (o: AcmeObservation) =>
		o.in_use === false && (o.state === 'uninitialized' || o.state === 'missing' || o.state === 'empty');
	const incomplete = $derived(
		partial && observations.some((o) => o.state !== 'observed' && o.state !== 'empty' && !isExpectedEmpty(o)),
	);

	/** Groups ACME storage observations by state; fully observed storage needs no explanation. */
	const observationGroups = $derived.by(() => {
		const known = new Set([
			'not_configured',
			'not_mapped',
			'unreadable',
			'missing',
			'invalid',
			'changing',
			'uninitialized',
			'incomplete',
			'empty',
		]);
		const groups = new Map<string, string[]>();
		for (const o of observations) {
			if (o.state === 'observed') continue;
			if (isExpectedEmpty(o)) continue;
			const state = known.has(o.state) ? o.state : 'invalid';
			groups.set(state, [...(groups.get(state) ?? []), o.resolver]);
		}
		return [...groups].map(([state, resolvers]) => ({ state, resolvers: resolvers.toSorted().join(', ') }));
	});

	async function load() {
		const own = ++generation;
		controller.abort();
		controller = new AbortController();
		const signal = controller.signal;
		loading = true;
		const results = await Promise.allSettled([getInventory(signal), listCertificates(signal)]);
		if (disposed || own !== generation) return;
		const next: Record<string, string> = {};
		if (results[0].status === 'fulfilled') {
			automatic = results[0].value.certificates;
			observations = results[0].value.observations;
			partial = results[0].value.partial;
		} else {
			automatic = [];
			observations = [];
			partial = false;
			next.automatic = errorMessage(results[0].reason, c('loadFailed'));
		}
		if (results[1].status === 'fulfilled') files = results[1].value;
		else {
			files = [];
			next.uploaded = errorMessage(results[1].reason, c('loadFailed'));
		}
		// Consumers are known only when both lists load; otherwise every warning is kept.
		errors = next;
		page = 1;
		loading = false;
	}

	onMount(() => {
		void load();
	});
	onDestroy(() => {
		disposed = true;
		controller.abort();
	});

	function switchTab(id: string) {
		search = '';
		validity = '';
		page = 1;
		sort = { column: 'name', direction: 'asc' };
		navigate('/certificates/' + id);
	}
	function clearFilters() {
		search = '';
		validity = '';
		page = 1;
	}

	async function confirmDelete() {
		const target = pendingDelete;
		if (!target || deleting) return;
		deleting = true;
		actionError = '';
		try {
			// The version read with the list guards the delete; a 409 is surfaced, never retried.
			const res = await deleteCertificate(target.name, files.find((f) => f.name === target.name)?.version ?? 0);
			if (needsManualRestart(res)) restartNotice = true;
			pendingDelete = null;
			await load();
		} catch (e) {
			pendingDelete = null;
			actionError =
				e instanceof ApiError && e.status === 409
					? rv2('conflict') + (e.message ? ' ' + e.message : '')
					: errorMessage(e, v2('deleteFailed'));
		} finally {
			deleting = false;
		}
	}

	const viewPath = (row: Row) =>
		'/certificates/' +
		(row.file ? 'uploaded/' + encodeURIComponent(row.file.name) : 'automatic/' + encodeURIComponent(row.key));
	const canChange = (f: CertificateResource) => !f.read_only;
</script>

<PageHeader {title} subtitle={c(uploaded ? 'uploadedHelp' : 'automaticHelp')} />

<div class="notices">
	{#if actionError}<Notice tone="error" message={actionError} dismissible onDismiss={() => (actionError = '')} />{/if}
	{#if restartNotice}<Notice
			tone="warning"
			message={t('writeV2.manualRestart')}
			dismissible
			onDismiss={() => (restartNotice = false)}
		/>{/if}
</div>

<div class="resource-tabs">
	<Tabs
		tabs={[
			{ id: 'automatic', label: c('automatic') },
			{ id: 'uploaded', label: c('uploaded') },
		]}
		selected={uploaded ? 'uploaded' : 'automatic'}
		onselect={switchTab}
		label={c('views')}
	/>
</div>
<div id={'panel-' + (uploaded ? 'automatic' : 'uploaded')} hidden></div>
<div
	id={'panel-' + (uploaded ? 'uploaded' : 'automatic')}
	role="tabpanel"
	aria-labelledby={'tab-' + (uploaded ? 'uploaded' : 'automatic')}
>
	{#if !uploaded && !loading}
		{#if incomplete}<div class="inventory-notice"><Notice tone="warning" message={c('partial')} /></div>{/if}
		{#each observationGroups as group (group.state)}
			<div class="inventory-notice">
				<Notice
					tone={group.state === 'empty' ? 'info' : 'warning'}
					message={c('observation.' + group.state, { resolvers: group.resolvers })}
				/>
			</div>
		{/each}
	{/if}
	{#if usageUnknown && !loading && !tableError}<div class="inventory-notice">
			<Notice tone="warning" message={c('usageUnavailable')} />
		</div>{/if}
	{#if tableError}<div class="inventory-notice"><Notice tone="error" message={tableError} /></div>{/if}

	<DataTable
		{columns}
		rows={pageRows}
		getRowId={(r: Row) => r.key}
		{loading}
		error={!!tableError}
		onretry={load}
		regionLabel={title}
		minWidth={uploaded ? 1400 : 1250}
		searchValue={search}
		searchPlaceholder={c(uploaded ? 'searchUploaded' : 'searchAutomatic')}
		onsearch={(v: string) => {
			search = v;
			page = 1;
		}}
		{filters}
		filterValues={{ validity }}
		onfilterchange={(id: string, v: string) => {
			if (id === 'validity') validity = v;
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
	>
		{#snippet toolbarActions()}
			{#if uploaded}<Button variant="primary" onclick={() => navigate('/certificates/uploaded/new')}>
					{c('upload')}
				</Button>{/if}
		{/snippet}

		{#snippet rowSnippet(row: Row)}
			<td>
				<div class="identity">
					<span class="name">
						<ValueTooltip text={row.label} truncate onactivate={() => navigate(viewPath(row))}>
							<strong>{row.label}</strong>
						</ValueTooltip>
					</span>
					{#if row.names.length > 1 || (uploaded && row.names.length)}
						<ValueTooltip compact label={c('coveredNames')} text={row.names.join('\n')}>
							<ListTree size={16} aria-hidden="true" />
						</ValueTooltip>
					{/if}
					{#if row.file?.config.purpose === 'ca'}<StatusBadge size="sm" text={c('trustFile')} />{/if}
				</div>
			</td>
			{#if !uploaded}<td>
					<ValueTooltip
						text={row.resolver}
						truncate
						onactivate={() => navigate('/resolvers/' + encodeURIComponent(row.resolver))}
					>
						{row.resolver || '—'}
					</ValueTooltip>
				</td>{/if}
			<td><ValueTooltip text={row.issuer} truncate>{row.issuer || '—'}</ValueTooltip></td>
			<td><RelativeTime value={row.expires} /></td>
			<td><StatusBadge size="sm" tone={validityTone(row.validity as 'valid')} text={old(row.validity)} /></td>
			<td>
				{#if row.usedBy}<ReferenceCount
						count={usageCount(row.usedBy)}
						onclick={() => (usage = { name: row.label, used_by: row.usedBy! })}
					/>
				{:else}<span class="muted">{c('unknown')}</span>{/if}
			</td>
			<td>
				<RowActions
					actions={[
						{ label: c('view'), onclick: () => navigate(viewPath(row)) },
						!!row.file &&
							canChange(row.file) && {
								label: c('replace'),
								onclick: () => navigate('/certificates/uploaded/' + encodeURIComponent(row.file!.name) + '/replace'),
							},
						!!row.file &&
							canChange(row.file) && {
								label: rv2('delete.action'),
								variant: 'danger',
								onclick: () => (pendingDelete = row.file ?? null),
							},
					]}
				/>
			</td>
		{/snippet}

		{#snippet emptyState()}
			<h2>
				{c(hasFilters ? 'noMatches' : uploaded ? 'noUploads' : incomplete ? 'noAutomaticUnknown' : 'noAutomatic')}
			</h2>
			<p>
				{c(
					hasFilters
						? 'adjustFilters'
						: uploaded
							? 'noUploadsHelp'
							: incomplete
								? 'noAutomaticUnknownHelp'
								: 'noAutomaticHelp',
				)}
			</p>
			<Button
				onclick={() => (hasFilters ? clearFilters() : navigate(uploaded ? '/certificates/uploaded/new' : '/resolvers'))}
			>
				{c(hasFilters ? 'clearFilters' : uploaded ? 'upload' : 'openResolvers')}
			</Button>
		{/snippet}
	</DataTable>
</div>

<ConfirmDialog
	open={!!pendingDelete}
	danger
	title={v2('delete.title')}
	message={pendingDelete
		? pendingDelete.config.used_by?.domains.length
			? v2('delete.used', { name: pendingDelete.name, count: pendingDelete.config.used_by.domains.length })
			: v2('delete.message', { name: pendingDelete.name })
		: ''}
	cancelLabel={t('common.cancel')}
	confirmLabel={rv2('delete.confirm')}
	oncancel={() => {
		if (!deleting) pendingDelete = null;
	}}
	onconfirm={confirmDelete}
/>

{#if mode !== 'list'}
	{#key `${mode}:${tab}:${name}`}
		{#if mode === 'view'}
			<CertificateDialog
				{name}
				collection={uploaded ? 'uploaded' : 'automatic'}
				{navigate}
				{automatic}
				{files}
				{loading}
				onreload={load}
				ondelete={(f) => (pendingDelete = f)}
			/>
		{:else}
			<CertificateUploadDialog {mode} {name} {navigate} onchanged={load} />
		{/if}
	{/key}
{/if}

{#if usage}
	<RelatedItemsDialog
		title={c('usage') + ' · ' + usage.name}
		description={c('domainLinksHelp')}
		items={usageItems(usage.used_by)}
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
	.resource-tabs,
	.inventory-notice {
		margin-bottom: 24px;
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
		font-size: 13px;
	}
</style>

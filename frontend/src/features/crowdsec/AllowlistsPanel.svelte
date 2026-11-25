<script lang="ts">
	import { onDestroy, onMount } from 'svelte';
	import DataTable from '../../shared/ui/DataTable.svelte';
	import ResourceDialog from '../../shared/ui/ResourceDialog.svelte';
	import ConfirmDialog from '../../shared/ui/ConfirmDialog.svelte';
	import Field from '../../shared/ui/Field.svelte';
	import Button from '../../shared/ui/Button.svelte';
	import Notice from '../../shared/ui/Notice.svelte';
	import StatusBadge from '../../shared/ui/StatusBadge.svelte';
	import SearchableSelect from '../../shared/ui/SearchableSelect.svelte';
	import { errorMessage } from '../../shared/api/errorMessage';
	import { t } from '../../shared/localization/i18n';
	import type { SortState } from '../../shared/ui/tableTypes';
	import { addAllowlistEntry, checkAllowlist, createAllowlist, deleteAllowlist, listAllowlists, removeAllowlistEntry } from './api';
	import { looksLikeIpOrCidr, type CrowdSecAllowlist } from './models';

	let { canWrite = false, reloadKey = 0 }: { canWrite?: boolean; reloadKey?: number } = $props();
	const c = (key: string, args?: Record<string, string | number>) => t('crowdsecV2.' + key, args);
	const PREFIX = 'traefik-console-';
	type EntryRow = { id: string; list: string; value: string; comment: string; expiration: string; managed: boolean };
	let lists = $state<CrowdSecAllowlist[]>([]);
	let loading = $state(true);
	let error = $state('');
	let actionError = $state('');
	let search = $state('');
	let page = $state(1);
	let pageSize = $state(25);
	let sort = $state<SortState>({ column: 'list', direction: 'asc' });
	let dialog = $state<'create' | 'entry' | 'check' | null>(null);
	let targetList = $state('');
	let name = $state(PREFIX);
	let description = $state('');
	let value = $state('');
	let expiration = $state('');
	let busy = $state(false);
	let result = $state('');
	let pendingEntry = $state<EntryRow | null>(null);
	let pendingList = $state<CrowdSecAllowlist | null>(null);
	let disposed = false;
	let controller = new AbortController();
	let generation = 0;

	const rows = $derived(lists.flatMap((list) => (list.items.length ? list.items : [{ value: '', description: '', expiration: '' }]).map((entry) => ({
		id: `${list.name}:${entry.value}`, list: list.name, value: entry.value, comment: entry.description || list.description,
		expiration: entry.expiration || '', managed: list.console_managed
	}))) as EntryRow[]);
	const filtered = $derived.by(() => {
		const q = search.trim().toLowerCase();
		return rows.filter((row) => !q || `${row.list} ${row.value} ${row.comment}`.toLowerCase().includes(q)).toSorted((a, b) => {
			const left = sort.column === 'value' ? a.value : a.list;
			const right = sort.column === 'value' ? b.value : b.list;
			return left.localeCompare(right, undefined, { numeric: true }) * (sort.direction === 'desc' ? -1 : 1);
		});
	});
	const pageRows = $derived(filtered.slice((page - 1) * pageSize, page * pageSize));
	const columns = $derived([
		{ id: 'list', label: c('allowlists.name'), sortable: true, width: 'clamp(190px, 25vw, 300px)' },
		{ id: 'value', label: c('allowlists.value'), sortable: true, width: '200px' },
		{ id: 'comment', label: c('allowlists.comment'), width: 'clamp(180px, 26vw, 330px)' },
		{ id: 'expiration', label: c('allowlists.expiration'), width: '180px' },
		{ id: 'managed', label: c('allowlists.managed'), width: '150px' },
		{ id: 'actions', label: '', hideLabel: true, width: '210px' }
	]);

	async function load() {
		const own = ++generation;
		controller.abort();
		controller = new AbortController();
		loading = true;
		error = '';
		try {
			lists = (await listAllowlists(controller.signal)) ?? [];
			page = 1;
		} catch (e) {
			if (disposed || own !== generation) return;
			error = errorMessage(e, c('allowlists.loadFailed'));
		} finally {
			if (!disposed && own === generation) loading = false;
		}
	}

	function openEntry(listName = '') {
		dialog = 'entry';
		targetList = listName || lists[0]?.name || '';
		value = '';
		description = '';
		expiration = '';
		result = '';
		actionError = '';
	}
	function closeDialog() {
		if (!busy) {
			dialog = null;
			result = '';
			actionError = '';
		}
	}
	async function submit() {
		if (busy) return;
		actionError = '';
		result = '';
		if (dialog === 'check') {
			if (!looksLikeIpOrCidr(value.trim())) {
				actionError = c('ban.ipInvalid');
				return;
			}
		} else if (dialog === 'create') {
			if (!/^traefik-console-[a-z][a-z0-9_-]*$/.test(name) || !description.trim()) {
				actionError = c('allowlists.invalid');
				return;
			}
		} else if (dialog === 'entry' && (!targetList || !looksLikeIpOrCidr(value.trim()))) {
			actionError = c('ban.ipInvalid');
			return;
		}
		busy = true;
		try {
			if (dialog === 'create') {
				await createAllowlist({ name: name.trim(), description: description.trim() });
				result = c('allowlists.created');
				await load();
			} else if (dialog === 'entry') {
				await addAllowlistEntry(targetList, { value: value.trim(), description: description.trim(), expiration });
				result = c('allowlists.entryAdded');
				await load();
			} else if (dialog === 'check') {
				const checked = await checkAllowlist(value.trim());
				result = c(checked.allowlisted ? 'allowlists.checkAllowed' : 'allowlists.checkDenied', { value: value.trim(), reason: checked.reason || '—' });
			}
		} catch (e) {
			actionError = errorMessage(e, c('allowlists.actionFailed'));
		} finally {
			busy = false;
		}
	}
	async function confirmEntry() {
		if (!pendingEntry || busy) return;
		busy = true;
		try {
			await removeAllowlistEntry(pendingEntry.list, pendingEntry.value);
			pendingEntry = null;
			await load();
		} catch (e) {
			pendingEntry = null;
			actionError = errorMessage(e, c('allowlists.actionFailed'));
		} finally {
			busy = false;
		}
	}
	async function confirmList() {
		if (!pendingList || busy) return;
		busy = true;
		try {
			await deleteAllowlist(pendingList.name);
			pendingList = null;
			await load();
		} catch (e) {
			pendingList = null;
			actionError = errorMessage(e, c('allowlists.actionFailed'));
		} finally {
			busy = false;
		}
	}

	onMount(() => void load());
	onDestroy(() => { disposed = true; controller.abort(); });
	$effect(() => { if (reloadKey > 0) void load(); });
</script>

{#if actionError}<div class="notice"><Notice tone="error" message={actionError} dismissible onDismiss={() => (actionError = '')} /></div>{/if}
{#if !canWrite}<div class="notice"><Notice tone="info" message={c('allowlists.readOnly')} /></div>{/if}
<DataTable
	{columns}
	rows={pageRows}
	getRowId={(row: EntryRow) => row.id}
	searchValue={search}
	onsearch={(input: string) => { search = input; page = 1; }}
	searchPlaceholder={c('allowlists.search')}
	{sort}
	onsort={(column: string) => { sort = { column, direction: sort.column === column && sort.direction === 'asc' ? 'desc' : 'asc' }; page = 1; }}
	{page}
	{pageSize}
	total={filtered.length}
	onpage={(input: number) => (page = input)}
	onpagesize={(input: number) => { pageSize = input; page = 1; }}
	{loading}
	error={!!error && !lists.length}
	onretry={load}
	regionLabel={c('allowlists.title')}
	minWidth={1100}
>
	{#snippet toolbarActions()}
		<Button onclick={load} pending={loading} disabled={loading}>{c('refresh')}</Button>
		<Button onclick={() => { dialog = 'check'; value = ''; result = ''; actionError = ''; }}>{c('allowlists.check')}</Button>
		<Button onclick={() => openEntry()} disabled={!canWrite || !lists.length}>{c('allowlists.addEntry')}</Button>
		<Button variant="primary" onclick={() => { dialog = 'create'; name = PREFIX; description = ''; result = ''; actionError = ''; }} disabled={!canWrite}>{c('allowlists.add')}</Button>
	{/snippet}
	{#snippet rowSnippet(row: EntryRow)}
		<td><strong>{row.list}</strong></td>
		<td>{row.value || '—'}</td>
		<td>{row.comment || '—'}</td>
		<td>{row.expiration || c('allowlists.never')}</td>
		<td><StatusBadge size="sm" tone={row.managed ? 'info' : 'neutral'} text={c(row.managed ? 'allowlists.cloud' : 'allowlists.local')} /></td>
		<td><div class="row-actions">
			<Button size="sm" onclick={() => openEntry(row.list)} disabled={!canWrite}>{c('allowlists.addEntry')}</Button>
			{#if row.value}<Button size="sm" variant="danger" onclick={() => (pendingEntry = row)} disabled={!canWrite}>{c('allowlists.removeEntry')}</Button>{/if}
			{#if row.list.startsWith(PREFIX)}<Button size="sm" variant="danger" onclick={() => (pendingList = lists.find((list) => list.name === row.list) ?? null)} disabled={!canWrite}>{c('allowlists.deleteList')}</Button>{/if}
		</div></td>
	{/snippet}
	{#snippet emptyState()}
		<h2>{c(search ? 'allowlists.noMatches' : 'allowlists.empty')}</h2>
		<p>{c(search ? 'allowlists.adjust' : 'allowlists.emptyHelp')}</p>
		{#if search}<Button onclick={() => { search = ''; page = 1; }}>{c('decisions.clear')}</Button>{:else}<Button onclick={() => (dialog = 'create')} disabled={!canWrite}>{c('allowlists.add')}</Button>{/if}
	{/snippet}
</DataTable>

{#if dialog}
	<ResourceDialog title={c('allowlists.' + dialog + 'Title')} description={c('allowlists.' + dialog + 'Help')} closeLabel={t('common.close')} onclose={closeDialog}>
		<div class="stack">
			{#if actionError}<Notice tone="error" message={actionError} />{/if}
			{#if result}<Notice tone="info" message={result} />{/if}
			{#if dialog === 'create'}
				<Field id="allowlist-name" label={c('allowlists.name')} required help={c('allowlists.nameHelp')}><input id="allowlist-name" bind:value={name} disabled={busy} autocomplete="off" spellcheck="false" /></Field>
				<Field id="allowlist-description" label={c('allowlists.comment')} required><input id="allowlist-description" bind:value={description} disabled={busy} maxlength="512" /></Field>
			{:else if dialog === 'entry'}
				<Field id="allowlist-target" label={c('allowlists.name')} required><SearchableSelect id="allowlist-target" value={targetList} options={lists.map((list) => ({ value: list.name, label: list.name }))} onchange={(input) => (targetList = input)} disabled={busy} /></Field>
			{/if}
			{#if dialog !== 'create'}
				<Field id="allowlist-value" label={c('allowlists.value')} required help={c('ban.ipHelp')}><input id="allowlist-value" bind:value={value} disabled={busy} autocomplete="off" spellcheck="false" placeholder="203.0.113.7 · 198.51.100.0/24" /></Field>
			{/if}
			{#if dialog === 'entry'}
				<Field id="allowlist-entry-comment" label={c('allowlists.comment')}><input id="allowlist-entry-comment" bind:value={description} disabled={busy} maxlength="512" /></Field>
				<Field id="allowlist-expiration" label={c('allowlists.expiration')} help={c('allowlists.expirationHelp')}><SearchableSelect id="allowlist-expiration" value={expiration} options={[{ value: '', label: c('allowlists.never') }, { value: '1h', label: '1h' }, { value: '24h', label: '24h' }, { value: '168h', label: '168h' }]} onchange={(input) => (expiration = input)} disabled={busy} /></Field>
			{/if}
		</div>
		{#snippet footer()}
			<Button onclick={closeDialog} disabled={busy}>{t('common.cancel')}</Button>
			<Button variant="primary" onclick={submit} pending={busy} disabled={busy}>{c('allowlists.' + dialog + 'Submit')}</Button>
		{/snippet}
	</ResourceDialog>
{/if}

<ConfirmDialog open={!!pendingEntry} danger title={c('allowlists.removeEntryTitle')} message={pendingEntry ? c('allowlists.removeEntryMessage', { value: pendingEntry.value, name: pendingEntry.list }) : ''} cancelLabel={t('common.cancel')} confirmLabel={c('allowlists.removeEntry')} oncancel={() => { if (!busy) pendingEntry = null; }} onconfirm={confirmEntry} />
<ConfirmDialog open={!!pendingList} danger title={c('allowlists.deleteListTitle')} message={pendingList ? c('allowlists.deleteListMessage', { name: pendingList.name }) : ''} cancelLabel={t('common.cancel')} confirmLabel={c('allowlists.deleteList')} oncancel={() => { if (!busy) pendingList = null; }} onconfirm={confirmList} />

<style>
	.notice { margin-bottom: 16px; }
	.stack { display: grid; gap: 20px; }
	.row-actions { display: flex; flex-direction: column; align-items: stretch; gap: 4px; }
</style>

<script lang="ts">
	import { onMount } from 'svelte';
	import DataTable from '../../../shared/ui/DataTable.svelte';
	import Button from '../../../shared/ui/Button.svelte';
	import StatusBadge from '../../../shared/ui/StatusBadge.svelte';
	import RowActions from '../../../shared/ui/RowActions.svelte';
	import RelativeTime from '../../../shared/ui/RelativeTime.svelte';
	import ConfirmDialog from '../../../shared/ui/ConfirmDialog.svelte';
	import Notice from '../../../shared/ui/Notice.svelte';
	import { flash } from '../../../shared/ui/flash.svelte';
	import { errorMessage } from '../../../shared/api/errorMessage';
	import { t } from '../../../shared/localization/i18n';
	import type { DataTableColumn } from '../../../shared/ui/tableTypes';
	import { listSessions, revokeOtherSessions, revokeSession } from '../api';
	import type { SessionInfo } from '../models';
	import { deviceLabel } from './device';

	const s = (key: string, args?: Record<string, string | number>) => t('authScreens.sessions.' + key, args);

	let rows = $state<SessionInfo[]>([]);
	let loading = $state(true);
	let error = $state('');
	let actionError = $state('');
	let search = $state('');
	let page = $state(1);
	let pageSize = $state(25);

	async function load() {
		loading = true;
		try {
			rows = (await listSessions()).items;
			error = '';
		} catch (err) {
			error = errorMessage(err, s('loadFailed'));
		} finally {
			loading = false;
		}
	}
	onMount(load);

	const device = (r: SessionInfo) => deviceLabel(r.user_agent, s('on'), s('unknownDevice'));
	const method = (r: SessionInfo) => (r.method === 'oauth' ? r.provider || s('external') : s('password'));
	const filtered = $derived(rows.filter((r) => {
		const q = search.trim().toLowerCase();
		return !q || [device(r), r.ip, method(r)].some((v) => v.toLowerCase().includes(q));
	}));
	const pageRows = $derived(filtered.slice((page - 1) * pageSize, page * pageSize));
	const others = $derived(rows.filter((r) => !r.current).length);

	const columns: DataTableColumn[] = [
		{ id: 'device', label: s('colDevice') },
		{ id: 'address', label: s('colAddress'), width: '170px' },
		{ id: 'method', label: s('colMethod'), width: '170px' },
		{ id: 'signedIn', label: s('colSignedIn'), width: '150px' },
		{ id: 'lastActive', label: s('colLastActive'), width: '150px' },
		{ id: 'actions', label: s('colActions'), hideLabel: true, width: '120px' }
	];

	let pendingRevoke = $state<SessionInfo | null>(null);
	let confirmOthers = $state(false);
	let busy = $state(false);

	async function revokeOne() {
		if (!pendingRevoke) return;
		busy = true;
		try {
			await revokeSession(pendingRevoke.id);
			flash({ tone: 'success', title: s('revokedOne', { device: device(pendingRevoke) }) });
			pendingRevoke = null;
			await load();
		} catch (err) {
			actionError = errorMessage(err, s('revokeFailed'));
			pendingRevoke = null;
		} finally {
			busy = false;
		}
	}

	async function revokeAll() {
		confirmOthers = false;
		busy = true;
		try {
			const { revoked } = await revokeOtherSessions();
			flash({ tone: 'success', title: s('revokedMany', { count: revoked }) });
			await load();
		} catch (err) {
			actionError = errorMessage(err, s('revokeFailed'));
		} finally {
			busy = false;
		}
	}
</script>

{#if actionError}<div class="notices"><Notice tone="error" message={actionError} dismissible onDismiss={() => (actionError = '')} /></div>{/if}

<DataTable
	{columns}
	rows={pageRows}
	getRowId={(r: SessionInfo) => r.id}
	searchValue={search}
	onsearch={(v: string) => { search = v; page = 1; }}
	searchPlaceholder={s('search')}
	{page}
	{pageSize}
	total={filtered.length}
	onpage={(v: number) => (page = v)}
	onpagesize={(v: number) => { pageSize = v; page = 1; }}
	{loading}
	error={!!error}
	onretry={load}
	regionLabel={s('title')}
	minWidth={900}
>
	{#snippet toolbarActions()}
		<Button onclick={() => (confirmOthers = true)} disabled={others === 0 || busy}>{s('revokeOthers')}</Button>
	{/snippet}

	{#snippet rowSnippet(row: SessionInfo)}
		<td>
			<span class="device"><strong>{device(row)}</strong>{#if row.current}<StatusBadge size="sm" tone="connection" text={s('thisBrowser')} />{/if}</span>
		</td>
		<td><span class="mono">{row.ip || '—'}</span></td>
		<td>{method(row)}</td>
		<td><RelativeTime value={row.signed_in_at} /></td>
		<td><RelativeTime value={row.last_seen_at} /></td>
		<td>
			{#if !row.current}
				<RowActions label={device(row)} actions={[{ label: s('signOut'), variant: 'danger', onclick: () => (pendingRevoke = row) }]} />
			{/if}
		</td>
	{/snippet}

	{#snippet emptyState()}
		<h2>{s('noMatches')}</h2>
		<p>{s('adjustSearch')}</p>
		<Button onclick={() => (search = '')}>{s('clearSearch')}</Button>
	{/snippet}
</DataTable>

<p class="footnote">{s('lifetimeHelp')}</p>

<ConfirmDialog open={!!pendingRevoke} danger title={s('revokeOneTitle')} message={pendingRevoke ? s('revokeOneBody', { device: device(pendingRevoke), ip: pendingRevoke.ip || '—' }) : ''} confirmLabel={s('signOut')} cancelLabel={t('common.cancel')} oncancel={() => (pendingRevoke = null)} onconfirm={revokeOne} />
<ConfirmDialog open={confirmOthers} danger title={s('revokeOthersTitle')} message={s('revokeOthersBody', { count: others })} confirmLabel={s('revokeOthers')} cancelLabel={t('common.cancel')} oncancel={() => (confirmOthers = false)} onconfirm={revokeAll} />

<style>
	.notices { margin-bottom: 16px; }
	.device { display: inline-flex; align-items: center; gap: 10px; flex-wrap: wrap; }
	.device strong { font-weight: 600; }
	.mono { font-family: ui-monospace, SFMono-Regular, Consolas, monospace; font-size: 13px; }
	.footnote { margin: 12px 0 0; color: var(--text-muted); font-size: 13px; line-height: 20px; }
</style>

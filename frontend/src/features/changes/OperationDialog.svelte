<script lang="ts">
	/** One entry of the operations log. A stuck one (`recovery_required`/`cleanup_pending`) can be recovered or abandoned. */
	import { onMount, onDestroy } from 'svelte';
	import { ListChecks } from '@lucide/svelte';
	import ResourceDialog from '../../shared/ui/ResourceDialog.svelte';
	import Button from '../../shared/ui/Button.svelte';
	import Notice from '../../shared/ui/Notice.svelte';
	import StatusBadge from '../../shared/ui/StatusBadge.svelte';
	import SummaryList from '../../shared/ui/SummaryList.svelte';
	import CodePreview from '../../shared/ui/CodePreview.svelte';
	import RelativeTime from '../../shared/ui/RelativeTime.svelte';
	import ConfirmDialog from '../../shared/ui/ConfirmDialog.svelte';
	import { abandonOperation, getOperation, recoverOperation } from './api';
	import { needsAttention, operationBadge, operationTone } from './models';
	import type { Operation } from '../../core/types';
	import { errorMessage } from '../../shared/api/errorMessage';
	import { t } from '../../shared/localization/i18n';

	let { id = '', onchanged, onclose }: { id?: string; onchanged?: () => void; onclose: () => void } = $props();
	const c = (key: string, args?: Record<string, string | number>) => t('changesV2.' + key, args);

	const operationId = $derived(Number(id));
	let operation = $state<Operation | null>(null);
	let loading = $state(true);
	let loadError = $state('');
	let busy = $state<'' | 'recover' | 'abandon'>('');
	let problem = $state('');
	let confirmAbandon = $state(false);
	let outcome = $state<Operation | null>(null);
	let disposed = false;
	const controller = new AbortController();

	async function load() {
		loading = true; loadError = '';
		try {
			if (!Number.isInteger(operationId) || operationId < 1) { loadError = c('operationNotFound'); return; }
			const op = await getOperation(operationId, controller.signal);
			if (!disposed) operation = op;
		} catch (e) {
			if (!disposed) loadError = errorMessage(e, c('operationFailed'));
		} finally {
			if (!disposed) loading = false;
		}
	}
	onMount(() => { void load(); });
	onDestroy(() => { disposed = true; controller.abort(); });

	async function act(kind: 'recover' | 'abandon') {
		if (busy || !operation) return;
		busy = kind; problem = ''; confirmAbandon = false;
		try {
			const op = await (kind === 'recover' ? recoverOperation(operation.id) : abandonOperation(operation.id));
			if (disposed) return;
			operation = op; outcome = op;
			onchanged?.();
		} catch (e) {
			if (!disposed) problem = errorMessage(e, c(kind === 'recover' ? 'recoverFailed' : 'abandonFailed'));
		} finally {
			if (!disposed) busy = '';
		}
	}
</script>

<ResourceDialog title={c('operationTitle', { id: operationId || 0 })} description={c('operationDescription')} closeLabel={t('common.close')} onclose={() => { if (!busy) onclose(); }}>
	{#snippet icon()}<ListChecks size={32} aria-hidden="true" />{/snippet}

	{#if loading}
		<p role="status">{c('loading')}</p>
	{:else if loadError}
		<div class="stack"><Notice tone="error" message={loadError} /><div><Button onclick={load}>{t('common.retry')}</Button></div></div>
	{:else if operation}
		<div class="stack">
			{#if outcome}<Notice tone={operationTone(outcome.status)} title={c('statusLabel.' + outcome.status)} message={outcome.summary} />{/if}
			<SummaryList>
				<div><dt>{c('operation')}</dt><dd>#{operation.id}</dd></div>
				<div><dt>{c('kind')}</dt><dd>{operation.kind}{operation.target ? ' · ' + operation.target : ''}</dd></div>
				<div><dt>{c('status')}</dt><dd><StatusBadge size="sm" tone={operationBadge(operation.status)} text={c('statusLabel.' + operation.status)} /></dd></div>
				{#if operation.stage}<div><dt>{c('stage')}</dt><dd>{operation.stage}</dd></div>{/if}
				{#if operation.reason_code}<div><dt>{c('reason')}</dt><dd><code>{operation.reason_code}</code></dd></div>{/if}
				<div><dt>{c('summary')}</dt><dd>{operation.summary || '—'}</dd></div>
				<div><dt>{c('started')}</dt><dd><RelativeTime value={operation.created_at} /></dd></div>
				{#if operation.finished_at}<div><dt>{c('finished')}</dt><dd><RelativeTime value={operation.finished_at} /></dd></div>{/if}
				<div><dt>{c('restart')}</dt><dd>{operation.requires_restart ? c('restartNeeded') : c('restartNo')}</dd></div>
			</SummaryList>
			{#if operation.status === 'restart_required'}<Notice tone="warning" title={c('manualRestartTitle')} message={c('manualRestartHelp')} />{/if}
			{#if operation.error_log}<CodePreview label={c('errorLog')} code={operation.error_log} language="yaml" />{/if}
			{#if needsAttention(operation)}
				<Notice tone="warning" title={c('stuckTitle')} message={c('stuckHelp')} />
			{/if}
			{#if problem}<Notice tone="error" message={problem} />{/if}
		</div>
	{/if}

	{#snippet footer()}
		<Button onclick={onclose} disabled={!!busy}>{t('common.close')}</Button>
		{#if operation && needsAttention(operation)}
			<Button variant="danger" onclick={() => (confirmAbandon = true)} disabled={!!busy} pending={busy === 'abandon'}>{c('abandon')}</Button>
			<Button variant="primary" onclick={() => act('recover')} disabled={!!busy} pending={busy === 'recover'}>{busy === 'recover' ? c('recovering') : c('recover')}</Button>
		{/if}
	{/snippet}
</ResourceDialog>

<ConfirmDialog
	open={confirmAbandon}
	danger
	title={c('abandonTitle')}
	message={c('abandonMessage')}
	cancelLabel={t('common.cancel')}
	confirmLabel={c('abandon')}
	oncancel={() => (confirmAbandon = false)}
	onconfirm={() => act('abandon')}
/>

<style>
	.stack { display: flex; flex-direction: column; gap: 20px; }
	code { font-family: var(--font-mono, ui-monospace, monospace); font-size: 13px; overflow-wrap: anywhere; }
</style>

<script lang="ts">
	/** Takes a restore point of what Traefik runs now: POST /api/v1/snapshots {name, description}. */
	import { onDestroy } from 'svelte';
	import { Camera } from '@lucide/svelte';
	import ResourceDialog from '../../shared/ui/ResourceDialog.svelte';
	import Button from '../../shared/ui/Button.svelte';
	import Field from '../../shared/ui/Field.svelte';
	import Notice from '../../shared/ui/Notice.svelte';
	import { createSnapshot } from './api';
	import { classify } from './models';
	import { ApiError } from '../../shared/api/client';
	import { errorMessage } from '../../shared/api/errorMessage';
	import { t } from '../../shared/localization/i18n';
	import type { Snapshot } from '../../core/types';

	let { navigate, oncreated }: { navigate: (p: string) => void; oncreated?: () => void } = $props();
	const c = (key: string, args?: Record<string, string | number>) => t('changesV2.' + key, args);

	let name = $state('');
	let description = $state('');
	let busy = $state(false);
	let problem = $state('');
	let nameError = $state('');
	let created = $state<Snapshot | null>(null);
	let disposed = false;
	onDestroy(() => { disposed = true; });

	async function submit() {
		if (busy) return;
		const trimmed = name.trim();
		nameError = trimmed ? (trimmed.length > 100 ? c('nameTooLong') : '') : c('nameRequired');
		if (nameError) return;
		busy = true; problem = '';
		try {
			const snap = await createSnapshot({ name: trimmed, description: description.trim() });
			if (disposed) return;
			created = snap;
			oncreated?.();
		} catch (e) {
			if (disposed) return;
			const outcome = classify(e);
			if (e instanceof ApiError && e.fieldErrors.name) nameError = e.fieldErrors.name;
			problem = outcome === 'quota' ? c('quotaHelp') : outcome === 'recovery' ? c('recoveryHelp') : outcome === 'conflict' ? c('busyHelp') : errorMessage(e, c('createFailed'));
		} finally {
			if (!disposed) busy = false;
		}
	}

	const close = () => { if (!busy) navigate('/changes'); };
</script>

<ResourceDialog title={c('createTitle')} description={c('createDescription')} closeLabel={t('common.close')} onclose={close}>
	{#snippet icon()}<Camera size={32} aria-hidden="true" />{/snippet}

	<form class="stack" onsubmit={(e) => { e.preventDefault(); void submit(); }}>
		{#if created}
			<Notice tone="info" title={c('createdTitle', { id: created.id })} message={c('createdHelp', { name: created.name })} />
		{:else}
			<Field label={c('name')} help={c('nameHelp')} error={nameError} required>
				{#snippet children(f)}
					<input id={f.id} bind:value={name} maxlength="100" autocomplete="off" spellcheck="false" disabled={busy} aria-required="true" aria-invalid={f.invalid || undefined} aria-describedby={f.describedBy} placeholder={c('namePlaceholder')} />
				{/snippet}
			</Field>
			<Field label={c('description')} help={c('descriptionHelp')}>
				{#snippet children(f)}
					<textarea id={f.id} bind:value={description} rows="3" disabled={busy} aria-describedby={f.describedBy}></textarea>
				{/snippet}
			</Field>
			{#if problem}<Notice tone="error" message={problem} />{/if}
		{/if}
	</form>

	{#snippet footer()}
		{#if created}
			<Button onclick={() => navigate('/changes/snapshots/' + created!.id)}>{c('details')}</Button>
			<Button variant="primary" onclick={() => navigate('/changes')}>{t('common.close')}</Button>
		{:else}
			<Button onclick={close} disabled={busy}>{t('common.cancel')}</Button>
			<Button variant="primary" onclick={submit} pending={busy} disabled={busy}>{busy ? c('creating') : c('createConfirm')}</Button>
		{/if}
	{/snippet}
</ResourceDialog>

<style>
	.stack { display: flex; flex-direction: column; gap: 20px; }
</style>

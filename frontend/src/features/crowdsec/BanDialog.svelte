<script lang="ts">
	import { onDestroy } from 'svelte';
	import { ShieldAlert } from '@lucide/svelte';
	import ResourceDialog from '../../shared/ui/ResourceDialog.svelte';
	import Field from '../../shared/ui/Field.svelte';
	import SearchableSelect from '../../shared/ui/SearchableSelect.svelte';
	import Button from '../../shared/ui/Button.svelte';
	import Notice from '../../shared/ui/Notice.svelte';
	import { errorMessage } from '../../shared/api/errorMessage';
	import { NetworkError } from '../../shared/api/client';
	import { navigationGuard } from '../../lib/stores/navigationGuard';
	import { t } from '../../shared/localization/i18n';
	import { addDecision } from './api';
	import { looksLikeIpOrCidr } from './models';

	let { onclose, ondone }: { onclose: () => void; ondone: () => void } = $props();
	const c = (key: string, args?: Record<string, string | number>) => t('crowdsecV2.' + key, args);

	const DURATIONS = ['1h', '4h', '24h', '72h', '168h'];
	let ip = $state('');
	let duration = $state('4h');
	let reason = $state('');
	let submitted = $state(false);
	let busy = $state(false);
	let error = $state('');
	let done = $state(false);
	const controller = new AbortController();
	$effect(() => {
		navigationGuard.setDirty(!done && !!(ip.trim() || reason.trim()));
		return () => navigationGuard.setDirty(false);
	});
	onDestroy(() => controller.abort());

	const ipError = $derived(!ip.trim() ? c('ban.ipRequired') : looksLikeIpOrCidr(ip.trim()) ? '' : c('ban.ipInvalid'));
	const reasonError = $derived(reason.length > 256 || /[\r\n\0]/.test(reason) ? c('ban.reasonInvalid') : '');

	async function submit() {
		submitted = true;
		if (ipError || reasonError || busy) return;
		busy = true;
		error = '';
		try {
			await addDecision({ ip: ip.trim(), duration, reason: reason.trim() }, controller.signal);
			done = true;
			ondone();
		} catch (e) {
			error = e instanceof NetworkError ? c('ban.uncertain') : errorMessage(e, c('ban.failed'));
		} finally {
			busy = false;
		}
	}
</script>

<ResourceDialog title={c('ban.title')} description={c('ban.description')} closeLabel={t('common.close')} {onclose} suspended={busy}>
	{#snippet icon()}<ShieldAlert size={36} aria-hidden="true" />{/snippet}
	<div class="stack">
		{#if done}
			<Notice tone="info" title={c('ban.doneTitle')} message={c('ban.done', { ip: ip.trim(), duration })} />
		{:else}
			{#if error}<Notice tone="error" message={error} />{/if}
			<Field id="ban-ip" label={c('ban.ip')} required help={submitted && ipError ? '' : c('ban.ipHelp')} error={submitted ? ipError : ''}>
				{#snippet children(f)}
					<input id={f.id} bind:value={ip} disabled={busy} aria-invalid={f.invalid || undefined} aria-describedby={f.describedBy} autocomplete="off" spellcheck="false" placeholder="203.0.113.7 · 198.51.100.0/24" />
				{/snippet}
			</Field>
			<Field id="ban-duration" label={c('ban.duration')} help={c('ban.durationHelp')}>
				{#snippet children(f)}
					<SearchableSelect id={f.id} value={duration} options={DURATIONS.map((d) => ({ value: d, label: c('ban.durations.' + d) }))} onchange={(v) => (duration = v)} disabled={busy} ariaDescribedby={f.describedBy} />
				{/snippet}
			</Field>
			<Field id="ban-reason" label={c('ban.reason')} help={submitted && reasonError ? '' : c('ban.reasonHelp')} error={submitted ? reasonError : ''}>
				{#snippet children(f)}
					<input id={f.id} bind:value={reason} disabled={busy} aria-invalid={f.invalid || undefined} aria-describedby={f.describedBy} autocomplete="off" maxlength="256" />
				{/snippet}
			</Field>
		{/if}
	</div>
	{#snippet footer()}
		<Button onclick={onclose} disabled={busy}>{done ? t('common.close') : t('common.cancel')}</Button>
		{#if !done}<Button variant="primary" onclick={submit} pending={busy} disabled={busy}>{c('ban.submit')}</Button>{/if}
	{/snippet}
</ResourceDialog>

<style>
	.stack { display: grid; gap: 20px; }
</style>

<script lang="ts">
	import { KeyRound } from '@lucide/svelte';
	import ResourceDialog from '../../../shared/ui/ResourceDialog.svelte';
	import Field from '../../../shared/ui/Field.svelte';
	import Notice from '../../../shared/ui/Notice.svelte';
	import Button from '../../../shared/ui/Button.svelte';
	import PasswordInput from '../PasswordInput.svelte';
	import { flash } from '../../../shared/ui/flash.svelte';
	import { ApiError } from '../../../shared/api/client';
	import { errorMessage } from '../../../shared/api/errorMessage';
	import { t } from '../../../shared/localization/i18n';
	import { changePassword } from '../api';
	import { startSession } from '../session.svelte';
	import { passwordStrength } from '../strength';
	import { MIN_PASSWORD } from '../models';

	let { onclose }: { onclose: () => void } = $props();
	const s = (key: string, args?: Record<string, string | number>) => t('authScreens.security.' + key, args);

	let current = $state('');
	let next = $state('');
	let repeat = $state('');
	let errors = $state<Record<string, string>>({});
	let formError = $state('');
	let busy = $state(false);
	const strength = $derived(passwordStrength(next));

	async function save() {
		if (busy) return;
		const e: Record<string, string> = {};
		if (!current) e.current_password = s('currentRequired');
		if ([...next].length < MIN_PASSWORD) e.password = t('authScreens.passwordRule', { min: MIN_PASSWORD });
		else if (next !== repeat) e.confirm = t('authScreens.passwordsDiffer');
		errors = e;
		formError = '';
		if (Object.keys(e).length) return;
		busy = true;
		try {
			startSession(await changePassword({ current_password: current, new_password: next }));
			flash({ tone: 'success', title: s('passwordChanged') });
			onclose();
		} catch (err) {
			if (err instanceof ApiError && Object.keys(err.fieldErrors).length) errors = { ...err.fieldErrors };
			else formError = errorMessage(err, s('passwordFailed'));
		} finally {
			busy = false;
		}
	}
</script>

<ResourceDialog title={s('changePassword')} description={s('changePasswordHelp')} closeLabel={t('common.close')} onclose={() => { if (!busy) onclose(); }}>
	{#snippet icon()}<KeyRound size={32} aria-hidden="true" />{/snippet}
	<form class="stack" onsubmit={(e) => { e.preventDefault(); void save(); }} novalidate>
		{#if formError}<Notice tone="error" message={formError} />{/if}
		<Field id="pw-current" label={s('currentPassword')} error={errors.current_password} required>
			{#snippet children({ id, describedBy, invalid })}<PasswordInput {id} bind:value={current} {describedBy} {invalid} />{/snippet}
		</Field>
		<h3>{s('newPasswordSection')}</h3>
		<Field id="pw-new" label={s('newPassword')} help={t('authScreens.passwordRule', { min: MIN_PASSWORD })} error={errors.password} required>
			{#snippet children({ id, describedBy, invalid })}<PasswordInput {id} bind:value={next} {describedBy} {invalid} autocomplete="new-password" />{/snippet}
		</Field>
		<div class="meter" data-level={strength} aria-live="polite">
			<span></span><span></span><span></span>
			<em>{next ? t('authScreens.strength.' + ['short', 'fair', 'good', 'strong'][strength]) : ''}</em>
		</div>
		<Field id="pw-repeat" label={t('authScreens.confirmLabel')} error={errors.confirm} required>
			{#snippet children({ id, describedBy, invalid })}<PasswordInput {id} bind:value={repeat} {describedBy} {invalid} autocomplete="new-password" />{/snippet}
		</Field>
		<button type="submit" hidden aria-hidden="true" tabindex="-1"></button>
	</form>
	{#snippet footer()}
		<Button onclick={onclose} disabled={busy}>{t('common.cancel')}</Button>
		<Button variant="primary" onclick={save} pending={busy} pendingLabel={s('updating')}>{s('updatePassword')}</Button>
	{/snippet}
</ResourceDialog>

<style>
	.stack { display: flex; flex-direction: column; gap: 24px; }
	h3 { display: flex; align-items: center; gap: 12px; margin: 0; font-size: 13px; font-weight: 600; color: var(--text); }
	h3::after { content: ''; flex: 1; height: 1px; background: var(--border); }
	.meter { display: grid; grid-template-columns: repeat(3, 1fr) auto; align-items: center; gap: 6px; margin-top: -16px; max-width: 560px; min-height: 18px; }
	.meter span { height: 4px; border-radius: 2px; background: var(--border); }
	.meter em { font-style: normal; font-size: 12px; color: var(--text-muted); min-width: 72px; text-align: right; }
	.meter[data-level='1'] span:nth-child(-n + 1), .meter[data-level='2'] span:nth-child(-n + 2) { background: var(--accent); }
	.meter[data-level='3'] span { background: var(--connection); }
</style>

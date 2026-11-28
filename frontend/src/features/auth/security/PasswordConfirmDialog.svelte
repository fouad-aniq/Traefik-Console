<script lang="ts">
	/**
	 * Re-enter the password before a change to how the console can be signed
	 * into. The caller runs the request; a rejected password keeps the dialog
	 * open with the error under the field.
	 */
	import ConfirmDialog from '../../../shared/ui/ConfirmDialog.svelte';
	import Field from '../../../shared/ui/Field.svelte';
	import Notice from '../../../shared/ui/Notice.svelte';
	import PasswordInput from '../PasswordInput.svelte';
	import { t } from '../../../shared/localization/i18n';

	let {
		open,
		title,
		message,
		confirmLabel,
		danger = false,
		error = '',
		pending = false,
		oncancel,
		onconfirm,
	}: {
		open: boolean;
		title: string;
		message: string;
		confirmLabel: string;
		danger?: boolean;
		/** server message shown under the field (wrong password, rate limit…) */
		error?: string;
		pending?: boolean;
		oncancel: () => void;
		onconfirm: (password: string) => void;
	} = $props();

	let password = $state('');
	let localError = $state('');
	$effect(() => {
		if (!open) {
			password = '';
			localError = '';
		}
	});

	function confirm() {
		if (pending) return;
		if (!password) {
			localError = t('authScreens.security.currentRequired');
			return;
		}
		localError = '';
		onconfirm(password);
	}
</script>

<ConfirmDialog
	{open}
	{title}
	{danger}
	{confirmLabel}
	cancelLabel={t('common.cancel')}
	oncancel={() => {
		if (!pending) oncancel();
	}}
	onconfirm={confirm}
>
	<p class="message">{message}</p>
	<form
		class="field"
		onsubmit={(e) => {
			e.preventDefault();
			confirm();
		}}
	>
		<Field id="confirm-password" label={t('authScreens.security.currentPassword')} error={localError || error}>
			{#snippet children({ id, describedBy, invalid })}<PasswordInput
					{id}
					bind:value={password}
					{describedBy}
					{invalid}
					disabled={pending}
				/>{/snippet}
		</Field>
	</form>
	{#if pending}<Notice tone="info" message={t('authScreens.security.checking')} />{/if}
</ConfirmDialog>

<style>
	.message {
		margin: 0 0 16px;
	}
	.field {
		margin-bottom: 4px;
	}
	.field :global(.field) {
		max-width: none;
	}
</style>

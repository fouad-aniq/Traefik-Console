<script lang="ts">
	import { Eye, EyeOff } from '@lucide/svelte';
	import { t } from '../../shared/localization/i18n';

	let {
		id,
		value = $bindable(''),
		describedBy,
		invalid = false,
		autocomplete = 'current-password',
		disabled = false,
	}: {
		id: string;
		value?: string;
		describedBy?: string;
		invalid?: boolean;
		autocomplete?: 'current-password' | 'new-password';
		disabled?: boolean;
	} = $props();

	let visible = $state(false);
</script>

<div class="password">
	<input
		{id}
		type={visible ? 'text' : 'password'}
		bind:value
		{autocomplete}
		{disabled}
		spellcheck="false"
		autocapitalize="off"
		aria-describedby={describedBy}
		aria-invalid={invalid}
		maxlength={512}
	/>
	<button
		type="button"
		class="toggle"
		onclick={() => (visible = !visible)}
		aria-label={t(visible ? 'authScreens.hidePassword' : 'authScreens.showPassword')}
		aria-pressed={visible}
		{disabled}
	>
		{#if visible}<EyeOff size={18} aria-hidden="true" />{:else}<Eye size={18} aria-hidden="true" />{/if}
	</button>
</div>

<style>
	.password {
		position: relative;
	}
	.password input {
		padding-right: 48px !important;
	}
	.toggle {
		position: absolute;
		top: 50%;
		right: 4px;
		transform: translateY(-50%);
		width: 36px;
		height: 36px;
		display: inline-flex;
		align-items: center;
		justify-content: center;
		border: none;
		background: transparent;
		color: var(--text-muted);
		border-radius: var(--radius-control);
		cursor: pointer;
	}
	.toggle:hover {
		color: var(--text);
		background: var(--surface-muted);
	}
</style>

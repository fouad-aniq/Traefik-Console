<script lang="ts">
	/**
	 * Basic Auth users as user name + password rows. core-v2 hashes each password with bcrypt and
	 * stores only the hashes, in a private file: nothing typed here is kept or shown again.
	 */
	import { Eye, EyeOff, Plus, X } from '@lucide/svelte';
	import { t } from '../../shared/localization/i18n';
	import type { Credential } from './models';

	let {
		users = $bindable(),
		disabled = false,
		errors = {},
		required = false,
		onchange
	}: {
		users: Credential[];
		disabled?: boolean;
		/** Keyed `users` (the whole list) or `credentials.<index>` (one row). */
		errors?: Record<string, string>;
		required?: boolean;
		onchange: () => void;
	} = $props();

	const v = (key: string, args?: Record<string, string | number>) => t('middlewareV2.' + key, args);
	let revealed = $state<Record<number, boolean>>({});

	function update(index: number, patch: Partial<Credential>) {
		users = users.with(index, { ...users[index], ...patch });
		onchange();
	}
	function add() {
		users = [...users, { username: '', password: '' }];
		onchange();
	}
	function remove(index: number) {
		users = users.length > 1 ? users.toSpliced(index, 1) : [{ username: '', password: '' }];
		revealed = {};
		onchange();
	}
</script>

<div class="credentials" role="group" aria-labelledby="credentials-label">
	<div class="header">
		<span class="label" id="credentials-label">{v('credentialsLabel')}{#if required}<span class="required" aria-hidden="true">*</span>{/if}</span>
		<button type="button" class="add" {disabled} onclick={add}><Plus size={14} aria-hidden="true" /><span>{v('addUser')}</span></button>
	</div>
	<p class="help" id="credentials-help">{v('credentialsHelp')}</p>
	<div class="rows">
		{#each users as user, index (index)}
			{@const error = errors[`credentials.${index}`]}
			<div class="row">
				<input
					id={`credential-${index}-username`}
					class="input"
					type="text"
					value={user.username}
					placeholder={v('usernamePlaceholder')}
					autocomplete="off"
					spellcheck="false"
					{disabled}
					aria-label={v('usernameLabel', { number: index + 1 })}
					aria-invalid={error ? 'true' : undefined}
					aria-describedby={error ? `credential-${index}-error` : 'credentials-help'}
					oninput={(e) => update(index, { username: e.currentTarget.value })}
				/>
				<div class="secret">
					<input
						id={`credential-${index}-password`}
						class="input"
						type={revealed[index] ? 'text' : 'password'}
						value={user.password}
						placeholder={v('passwordPlaceholder')}
						autocomplete="new-password"
						spellcheck="false"
						{disabled}
						aria-label={v('passwordLabel', { number: index + 1 })}
						aria-invalid={error ? 'true' : undefined}
						aria-describedby={error ? `credential-${index}-error` : 'credentials-help'}
						oninput={(e) => update(index, { password: e.currentTarget.value })}
					/>
					<button type="button" class="reveal" {disabled} aria-pressed={!!revealed[index]} aria-label={t(revealed[index] ? 'middlewares.config.concealValue' : 'middlewares.config.revealValue')} onclick={() => (revealed[index] = !revealed[index])}>
						{#if revealed[index]}<EyeOff size={18} aria-hidden="true" />{:else}<Eye size={18} aria-hidden="true" />{/if}
					</button>
				</div>
				<button type="button" class="remove" {disabled} aria-label={v('removeUser', { number: index + 1 })} onclick={() => remove(index)}><X size={16} aria-hidden="true" /></button>
			</div>
			{#if error}<p class="error" id={`credential-${index}-error`} role="alert">{error}</p>{/if}
		{/each}
	</div>
	{#if errors.users}<p class="error" role="alert">{errors.users}</p>{/if}
</div>

<style>
	.credentials { display: flex; flex-direction: column; gap: 6px; width: 100%; max-width: 560px; }
	.header { display: flex; align-items: center; justify-content: space-between; gap: 12px; }
	.label { font-size: 14px; font-weight: 500; line-height: 20px; color: var(--text); }
	.required { color: var(--danger-text); margin-inline-start: 4px; }
	.help { margin: 0 0 4px; font-size: 13px; line-height: 19px; color: var(--text-muted); }
	.add {
		display: inline-flex; align-items: center; gap: 4px; padding: 3px 8px;
		font: inherit; font-size: 12px; font-weight: 500; color: var(--text);
		background: var(--surface-muted); border: 1px solid var(--border); border-radius: 4px; cursor: pointer;
	}
	.add:hover:not(:disabled) { border-color: var(--accent); color: var(--accent); }
	.add:disabled { opacity: 0.5; cursor: default; }
	.rows { display: flex; flex-direction: column; gap: 8px; }
	.row { display: flex; align-items: center; gap: 8px; width: 100%; }
	.input {
		flex: 1; min-width: 0; min-height: 40px; padding: 7px 12px; width: 100%;
		background: var(--surface); color: var(--text);
		border: 1px solid var(--outline); border-radius: var(--radius-control);
		font: inherit; font-size: 16px; line-height: 24px;
	}
	.row > .input { flex: 2; }
	.input[aria-invalid='true'] { border-color: var(--danger-text); }
	.secret { position: relative; flex: 3; min-width: 0; display: flex; }
	.secret .input { padding-right: 44px; }
	.reveal, .remove {
		display: grid; place-items: center; width: 36px; height: 36px; flex-shrink: 0;
		border: 1px solid var(--border); border-radius: 6px; background: var(--surface-muted);
		color: var(--text-muted); cursor: pointer;
	}
	.reveal { position: absolute; right: 2px; top: 2px; border: 0; background: transparent; }
	.reveal:hover:not(:disabled) { color: var(--text); background: var(--surface-muted); }
	.reveal:focus-visible { outline: 2px solid var(--connection); outline-offset: -2px; }
	.remove:hover:not(:disabled) { color: var(--danger-text); }
	.reveal:disabled, .remove:disabled { opacity: 0.5; cursor: default; }
	.error { margin: 0; color: var(--danger-text); font-size: 14px; line-height: 20px; font-weight: 500; }
</style>

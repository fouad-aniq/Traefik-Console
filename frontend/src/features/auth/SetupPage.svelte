<script lang="ts">
	/**
	 * First-run setup, shown only while the server reports no administrator.
	 * Two real steps: prove access to the host (the one-time token from the
	 * server log), then choose the administrator's name and password.
	 */
	import AuthLayout from './AuthLayout.svelte';
	import PasswordInput from './PasswordInput.svelte';
	import Field from '../../shared/ui/Field.svelte';
	import Notice from '../../shared/ui/Notice.svelte';
	import Button from '../../shared/ui/Button.svelte';
	import { ApiError } from '../../shared/api/client';
	import { errorMessage } from '../../shared/api/errorMessage';
	import { t } from '../../shared/localization/i18n';
	import { setupAdmin } from './api';
	import { setupAlreadyDone, startSession } from './session.svelte';
	import { passwordStrength } from './strength';
	import { MIN_PASSWORD, USERNAME_PATTERN } from './models';

	let step = $state<1 | 2>(1);
	let token = $state('');
	let username = $state('admin');
	let password = $state('');
	let confirm = $state('');
	let pending = $state(false);
	let errors = $state<Record<string, string>>({});
	let formError = $state('');

	const strength = $derived(passwordStrength(password));
	const strengthLabel = $derived(
		['', t('authScreens.strength.fair'), t('authScreens.strength.good'), t('authScreens.strength.strong')][strength],
	);

	function continueToAccount(e: SubmitEvent) {
		e.preventDefault();
		errors = {};
		if (token.replace(/[\s-]/g, '').replace(/^TCS(?=.{16}$)/i, '').length !== 16) {
			errors = { setup_token: t('authScreens.setup.tokenInvalid') };
			return;
		}
		step = 2;
		queueMicrotask(() => document.getElementById('setup-username')?.focus());
	}

	async function create(e: SubmitEvent) {
		e.preventDefault();
		if (pending) return;
		const next: Record<string, string> = {};
		if (!USERNAME_PATTERN.test(username.trim())) next.username = t('authScreens.setup.usernameRule');
		if ([...password].length < MIN_PASSWORD) next.password = t('authScreens.passwordRule', { min: MIN_PASSWORD });
		else if (password !== confirm) next.confirm = t('authScreens.passwordsDiffer');
		errors = next;
		formError = '';
		if (Object.keys(next).length) return;
		pending = true;
		try {
			startSession(await setupAdmin({ setup_token: token.trim(), username: username.trim(), password }));
		} catch (err) {
			if (err instanceof ApiError && err.code === 'AUTH_SETUP_COMPLETE') {
				setupAlreadyDone();
				return;
			}
			if (err instanceof ApiError && err.code === 'AUTH_INVALID_SETUP_TOKEN') {
				step = 1;
				errors = { setup_token: t('authScreens.setup.tokenRejected') };
				return;
			}
			if (err instanceof ApiError && Object.keys(err.fieldErrors).length) {
				errors = { ...err.fieldErrors };
			}
			formError = errorMessage(err, t('authScreens.setup.failed'));
		} finally {
			pending = false;
		}
	}
</script>

<AuthLayout title={t('authScreens.setup.title')} lead={t('authScreens.setup.lead')}>
	<ol class="steps" aria-label={t('authScreens.setup.progress')}>
		<li class:current={step === 1} class:done={step === 2} aria-current={step === 1 ? 'step' : undefined}>
			{t('authScreens.setup.stepToken')}
		</li>
		<li class:current={step === 2} aria-current={step === 2 ? 'step' : undefined}>
			{t('authScreens.setup.stepAccount')}
		</li>
	</ol>

	{#if step === 1}
		<form class="auth-form" onsubmit={continueToAccount} novalidate>
			<p class="explain">{t('authScreens.setup.tokenWhere')}</p>
			<pre class="command"><code>docker compose logs console | grep TCS-</code></pre>
			<Field id="setup-token" label={t('authScreens.setup.tokenLabel')} error={errors.setup_token} required>
				{#snippet children({ id, describedBy, invalid })}
					<input
						{id}
						bind:value={token}
						autocomplete="off"
						spellcheck="false"
						autocapitalize="characters"
						class="token"
						placeholder="TCS-XXXX-XXXX-XXXX-XXXX"
						aria-describedby={describedBy}
						aria-invalid={invalid}
						maxlength={128}
					/>
				{/snippet}
			</Field>
			<Button type="submit" variant="primary">{t('authScreens.setup.continue')}</Button>
		</form>
	{:else}
		<form class="auth-form" onsubmit={create} novalidate>
			{#if formError}<Notice type="error" message={formError} />{/if}
			<Field
				id="setup-username"
				label={t('authScreens.usernameLabel')}
				help={t('authScreens.setup.usernameHelp')}
				error={errors.username}
				required
			>
				{#snippet children({ id, describedBy, invalid })}
					<input
						{id}
						bind:value={username}
						autocomplete="username"
						spellcheck="false"
						autocapitalize="off"
						aria-describedby={describedBy}
						aria-invalid={invalid}
						maxlength={64}
					/>
				{/snippet}
			</Field>
			<Field
				id="setup-password"
				label={t('authScreens.passwordLabel')}
				help={t('authScreens.passwordRule', { min: MIN_PASSWORD })}
				error={errors.password}
				required
			>
				{#snippet children({ id, describedBy, invalid })}
					<PasswordInput {id} bind:value={password} {describedBy} {invalid} autocomplete="new-password" />
				{/snippet}
			</Field>
			<div class="meter" data-level={strength} aria-live="polite">
				<span></span><span></span><span></span>
				<em>{password ? (strength === 0 ? t('authScreens.strength.short') : strengthLabel) : ''}</em>
			</div>
			<Field id="setup-confirm" label={t('authScreens.confirmLabel')} error={errors.confirm} required>
				{#snippet children({ id, describedBy, invalid })}
					<PasswordInput {id} bind:value={confirm} {describedBy} {invalid} autocomplete="new-password" />
				{/snippet}
			</Field>
			<div class="actions">
				<Button onclick={() => (step = 1)} disabled={pending}>{t('authScreens.back')}</Button>
				<Button type="submit" variant="primary" {pending} pendingLabel={t('authScreens.setup.creating')}>
					{t('authScreens.setup.create')}
				</Button>
			</div>
		</form>
	{/if}
</AuthLayout>

<style>
	.steps {
		display: flex;
		gap: 8px;
		margin: 28px 0 0;
		padding: 0;
		list-style: none;
		counter-reset: step;
	}
	.steps li {
		counter-increment: step;
		flex: 1;
		display: flex;
		align-items: center;
		gap: 8px;
		padding-top: 10px;
		border-top: 3px solid var(--border);
		color: var(--text-muted);
		font-size: 13px;
		line-height: 18px;
	}
	.steps li::before {
		content: counter(step);
		display: inline-flex;
		align-items: center;
		justify-content: center;
		width: 20px;
		height: 20px;
		border-radius: 50%;
		border: 1.5px solid currentColor;
		font-size: 11px;
		font-weight: 600;
	}
	.steps li.current {
		border-top-color: var(--accent);
		color: var(--text);
		font-weight: 500;
	}
	.steps li.done {
		border-top-color: var(--connection);
		color: var(--connection);
	}

	.auth-form {
		display: flex;
		flex-direction: column;
		gap: 20px;
		margin-top: 24px;
	}
	.auth-form :global(.field) {
		max-width: none;
	}
	/* Shown like the log line; matching is case-insensitive either way. */
	.auth-form :global(input.token) {
		font-family: ui-monospace, SFMono-Regular, Consolas, monospace;
		text-transform: uppercase;
		letter-spacing: 0.04em;
	}
	.auth-form > :global(button[type='submit']) {
		align-self: flex-start;
	}
	.explain {
		margin: 0;
		font-size: 14px;
		line-height: 21px;
		color: var(--text);
	}
	.auth-form :global(.notice p) {
		margin: 0;
	}
	.command {
		margin: -8px 0 0;
		padding: 10px 12px;
		border-radius: var(--radius-control);
		background: var(--surface-muted);
		border: 1px solid var(--border);
		font-size: 13px;
		overflow-x: auto;
	}
	.actions {
		display: flex;
		justify-content: space-between;
		gap: 12px;
		flex-wrap: wrap;
	}

	.meter {
		display: grid;
		grid-template-columns: repeat(3, 1fr) auto;
		align-items: center;
		gap: 6px;
		margin-top: -12px;
		min-height: 18px;
	}
	.meter span {
		height: 4px;
		border-radius: 2px;
		background: var(--border);
	}
	.meter em {
		font-style: normal;
		font-size: 12px;
		color: var(--text-muted);
		min-width: 64px;
		text-align: right;
	}
	.meter[data-level='1'] span:nth-child(-n + 1),
	.meter[data-level='2'] span:nth-child(-n + 2) {
		background: var(--accent);
	}
	.meter[data-level='3'] span {
		background: var(--connection);
	}
	@media (max-width: 767px) {
		.actions {
			flex-direction: column-reverse;
		}
		.actions :global(button) {
			width: 100%;
		}
	}
</style>

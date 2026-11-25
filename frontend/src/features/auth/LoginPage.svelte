<script lang="ts">
	import { onDestroy } from 'svelte';
	import { LogIn, Copy, Check, X } from '@lucide/svelte';
	import AuthLayout from './AuthLayout.svelte';
	import PasswordInput from './PasswordInput.svelte';
	import Field from '../../shared/ui/Field.svelte';
	import Notice from '../../shared/ui/Notice.svelte';
	import Button from '../../shared/ui/Button.svelte';
	import { ApiError } from '../../shared/api/client';
	import { errorMessage } from '../../shared/api/errorMessage';
	import { t } from '../../shared/localization/i18n';
	import { signIn, signInWithProviderPath } from './api';
	import { auth, bootstrap, startSession } from './session.svelte';

	/** Error code the server put in the hash after a failed external sign-in. */
	let { externalError = '' }: { externalError?: string } = $props();

	let username = $state('');
	let password = $state('');
	let pending = $state(false);
	let error = $state('');
	let retryIn = $state(0);
	let showRecovery = $state(false);
	let copied = $state(false);
	const RESET_COMMAND = 'server admin reset-password';

	async function copyCommand() {
		try {
			await navigator.clipboard.writeText(RESET_COMMAND);
			copied = true;
			setTimeout(() => (copied = false), 1500);
		} catch {
			/* clipboard unavailable (plain http): the command stays selectable */
		}
	}
	let timer: ReturnType<typeof setInterval> | undefined;
	onDestroy(() => clearInterval(timer));

	const EXTERNAL_CODES = ['not_allowed', 'no_identity', 'provider_denied', 'provider_error', 'provider_unreachable', 'invalid_state', 'invalid_token', 'not_configured', 'rate_limited', 'busy', 'setup_required', 'server_error'];
	const externalMessage = $derived(
		externalError ? t('authScreens.external.' + (EXTERNAL_CODES.includes(externalError) ? externalError : 'server_error')) : ''
	);

	const notice = $derived(auth.notice ? t('authScreens.login.notice.' + auth.notice) : '');

	function countdown(seconds: number) {
		retryIn = seconds;
		clearInterval(timer);
		timer = setInterval(() => {
			retryIn -= 1;
			if (retryIn <= 0) {
				clearInterval(timer);
				error = '';
			}
		}, 1000);
	}

	async function submit(e: SubmitEvent) {
		e.preventDefault();
		if (pending || retryIn > 0) return;
		error = '';
		auth.notice = '';
		if (!username.trim() || !password) {
			error = t('authScreens.login.missing');
			return;
		}
		pending = true;
		try {
			startSession(await signIn({ username: username.trim(), password }));
			password = '';
		} catch (err) {
			password = '';
			if (err instanceof ApiError && err.code === 'AUTH_SETUP_REQUIRED') {
				void bootstrap();
				return;
			}
			if (err instanceof ApiError && err.code === 'AUTH_RATE_LIMITED' && err.retryAfter) {
				countdown(err.retryAfter);
			}
			error = err instanceof ApiError && err.code === 'AUTH_INVALID_CREDENTIALS'
				? t('authScreens.login.invalid')
				: errorMessage(err, t('authScreens.login.failed'));
			queueMicrotask(() => document.getElementById('login-password')?.focus());
		} finally {
			pending = false;
		}
	}
</script>

<AuthLayout centered title={t('authScreens.login.title')} lead={t('authScreens.login.lead')}>
	<div class="stack">
		{#if notice}<Notice type="info" message={notice} />{/if}
		{#if externalMessage}<Notice type="error" title={t('authScreens.external.title')} message={externalMessage} />{/if}

		<form class="auth-form" onsubmit={submit} novalidate>
			{#if error}<Notice type="error" message={retryIn > 0 ? t('authScreens.login.locked', { seconds: retryIn }) : error} />{/if}
			<Field id="login-username" label={t('authScreens.usernameLabel')}>
				{#snippet children({ id, describedBy, invalid })}
					<input {id} bind:value={username} autocomplete="username" spellcheck="false" autocapitalize="off" aria-describedby={describedBy} aria-invalid={invalid} maxlength={64} />
				{/snippet}
			</Field>
			<Field id="login-password" label={t('authScreens.passwordLabel')}>
				{#snippet children({ id, describedBy, invalid })}
					<PasswordInput {id} bind:value={password} {describedBy} {invalid} />
				{/snippet}
			</Field>
			<Button type="submit" variant="primary" {pending} pendingLabel={t('authScreens.login.signingIn')} disabled={retryIn > 0}>{t('authScreens.login.submit')}</Button>
		</form>

		{#if auth.providers.length}
			<div class="or" role="separator"><span>{t('authScreens.login.or')}</span></div>
			<div class="providers">
				{#each auth.providers as provider (provider.id)}
					<a class="external btn-secondary" href={signInWithProviderPath(provider.id)}>
						<LogIn size={16} aria-hidden="true" />
						{t('authScreens.login.signInWith', { name: provider.display_name })}
					</a>
				{/each}
			</div>
		{/if}
	</div>

	{#snippet below()}
	<div class="forgot">
			<button type="button" class="link" aria-expanded={showRecovery} aria-controls="recovery-help" onclick={() => (showRecovery = !showRecovery)}>
				{t('authScreens.login.forgotLink')}
			</button>
			{#if showRecovery}
				<section id="recovery-help" class="recovery" aria-labelledby="recovery-title">
					<header>
						<h2 id="recovery-title">{t('authScreens.login.lockedTitle')}</h2>
						<button type="button" class="close" aria-label={t('common.close')} onclick={() => (showRecovery = false)}><X size={16} aria-hidden="true" /></button>
					</header>
					<p>{t('authScreens.login.lockedHelp')}</p>
					<div class="command">
						<code>{RESET_COMMAND}</code>
						<button type="button" class="copy" onclick={copyCommand} aria-label={t('authScreens.login.copyCommand')} title={t('authScreens.login.copyCommand')}>
							{#if copied}<Check size={16} aria-hidden="true" />{:else}<Copy size={16} aria-hidden="true" />{/if}
						</button>
					</div>
					<p class="small">{t('authScreens.login.whereToRun')}</p>
					<span class="sr-only" role="status">{copied ? t('authScreens.login.copied') : ''}</span>
				</section>
			{/if}
		</div>
	{/snippet}
</AuthLayout>

<style>
	.stack { display: flex; flex-direction: column; gap: 20px; margin-top: 28px; }
	.auth-form { display: flex; flex-direction: column; gap: 20px; }
	.auth-form :global(.field) { max-width: none; }
	.auth-form > :global(button[type='submit']) { width: 100%; }
	.providers { display: flex; flex-direction: column; gap: 10px; }
	.external { width: 100%; font-weight: 500; }
	.or { display: grid; grid-template-columns: 1fr auto 1fr; align-items: center; gap: 12px; color: var(--text-muted); font-size: 13px; }
	.or::before, .or::after { content: ''; border-top: 1px solid var(--border); }
	.forgot { display: flex; flex-direction: column; align-items: flex-start; gap: 12px; margin-top: 16px; }
	.link { padding: 2px 0; border: none; background: none; color: var(--accent); font: inherit; font-size: 14px; cursor: pointer; }
	.link:hover { text-decoration: underline; }
	.recovery { align-self: stretch; padding: 14px 16px; border: 1px solid var(--border); border-radius: var(--radius-card); background: var(--canvas); }
	.recovery header { display: flex; align-items: center; justify-content: space-between; gap: 8px; }
	.recovery h2 { margin: 0; font-size: 14px; line-height: 20px; font-weight: 600; }
	.recovery p { margin: 6px 0 0; font-size: 14px; line-height: 21px; color: var(--text-muted); }
	.recovery p.small { font-size: 13px; }
	.close { display: grid; place-items: center; width: 28px; height: 28px; border: none; border-radius: var(--radius-control); background: transparent; color: var(--text-muted); cursor: pointer; }
	.close:hover { background: var(--surface-muted); color: var(--text); }
	.command { display: flex; align-items: center; gap: 8px; margin-top: 10px; padding: 4px 4px 4px 12px; border: 1px solid var(--border); border-radius: var(--radius-control); background: var(--surface); }
	.command code { flex: 1; min-width: 0; font-size: 13px; line-height: 20px; overflow-wrap: anywhere; }
	.copy { display: grid; place-items: center; flex: none; width: 32px; height: 32px; border: none; border-radius: var(--radius-control); background: transparent; color: var(--text-muted); cursor: pointer; }
	.copy:hover { background: var(--surface-muted); color: var(--text); }
	.sr-only { position: absolute; width: 1px; height: 1px; overflow: hidden; clip-path: inset(50%); white-space: nowrap; }
	.stack :global(.notice p) { margin: 0; }
</style>

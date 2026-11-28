<script lang="ts">
	import { Copy, Check, LogIn } from '@lucide/svelte';
	import ResourceDialog from '../../../shared/ui/ResourceDialog.svelte';
	import LineField from './LineField.svelte';
	import Select from '../../../shared/ui/Select.svelte';
	import Notice from '../../../shared/ui/Notice.svelte';
	import Button from '../../../shared/ui/Button.svelte';
	import Checkbox from '../../../shared/ui/Checkbox.svelte';
	import PasswordInput from '../PasswordInput.svelte';
	import PasswordConfirmDialog from './PasswordConfirmDialog.svelte';
	import { flash } from '../../../shared/ui/flash.svelte';
	import { ApiError } from '../../../shared/api/client';
	import { errorMessage } from '../../../shared/api/errorMessage';
	import { t } from '../../../shared/localization/i18n';
	import { saveProvider } from '../api';
	import { CALLBACK_PATH, type ProviderSettings } from '../models';
	import { PRESETS, TEMPLATES, presetOf, type Preset } from './presets';
	import type { ProviderKind } from '../models';

	let {
		provider,
		editing,
		loading,
		onclose,
		onsaved,
	}: {
		/** the provider being edited; undefined while creating (or still loading) */
		provider?: ProviderSettings;
		editing: boolean;
		loading: boolean;
		onclose: () => void;
		onsaved: () => Promise<void> | void;
	} = $props();

	const p = (key: string, args?: Record<string, string | number>) => t('authScreens.providers.' + key, args);
	const defaultCallback = typeof window !== 'undefined' ? window.location.origin + CALLBACK_PATH : CALLBACK_PATH;

	// ------------------------------------------------------------------ form state
	let kind = $state<ProviderKind>('oidc');
	let form = $state({
		display_name: '',
		issuer: '',
		authorize_url: '',
		token_url: '',
		userinfo_url: '',
		client_id: '',
		identity_claim: 'email',
		redirect_url: defaultCallback,
	});
	let scopesText = $state('openid email profile');
	let identitiesText = $state('');
	let clientSecret = $state('');
	let clearSecret = $state(false);
	let initialized = $state(false);

	$effect(() => {
		if (initialized || (editing && !provider)) return;
		if (provider) {
			kind = provider.kind;
			form = {
				display_name: provider.display_name,
				issuer: provider.issuer,
				authorize_url: provider.authorize_url,
				token_url: provider.token_url,
				userinfo_url: provider.userinfo_url,
				client_id: provider.client_id,
				identity_claim: provider.identity_claim,
				redirect_url: provider.redirect_url || defaultCallback,
			};
			scopesText = provider.scopes.join(' ');
			identitiesText = provider.allowed_identities.join('\n');
		}
		initialized = true;
	});

	/** Which provider the current values look like (drives the help texts and placeholders). */
	const preset = $derived(presetOf({ kind, ...form }));
	let issuerPlaceholder = $state('https://auth.example.com/application/o/console/');

	/**
	 * Fill the inputs from a template. A one-shot action, not a mode: the client
	 * credentials and allowed accounts are kept, everything stays editable.
	 */
	function applyTemplate(next: Preset) {
		const d = PRESETS[next];
		const known = Object.values(PRESETS).map((x) => x.display_name);
		kind = d.kind;
		form = {
			...form,
			display_name: !form.display_name || known.includes(form.display_name) ? d.display_name : form.display_name,
			issuer: d.issuer,
			authorize_url: d.authorize_url,
			token_url: d.token_url,
			userinfo_url: d.userinfo_url,
			identity_claim: d.identity_claim,
		};
		scopesText = d.scopes.join(' ');
		errors = {};
		if (d.issuerPlaceholder) {
			issuerPlaceholder = d.issuerPlaceholder;
			queueMicrotask(() => document.getElementById('prov-issuer')?.focus());
		}
	}

	/** Switching the protocol keeps what was typed and resets the protocol defaults. */
	function changeKind(next: ProviderKind) {
		if (next === kind) return;
		kind = next;
		const d = PRESETS[next === 'oidc' ? 'oidc' : 'oauth2'];
		form = { ...form, identity_claim: d.identity_claim };
		scopesText = d.scopes.join(' ');
		errors = {};
	}

	// ------------------------------------------------------------------ validation and save
	let errors = $state<Record<string, string>>({});
	let formError = $state('');
	let confirming = $state(false);
	let saving = $state(false);
	let confirmError = $state('');
	let copied = $state(false);

	function validate(): boolean {
		const e: Record<string, string> = {};
		const url = (v: string) => /^https?:\/\/\S+$/.test(v.trim());
		if (!form.display_name.trim()) e.display_name = p('required.display_name');
		if (kind === 'oidc' && !url(form.issuer)) e.issuer = p('required.url');
		if (kind === 'oauth2') {
			for (const f of ['authorize_url', 'token_url', 'userinfo_url'] as const)
				if (!url(form[f])) e[f] = p('required.url');
		}
		if (!form.client_id.trim()) e.client_id = p('required.client_id');
		if (kind === 'oauth2' && !clientSecret && !provider?.credential_stored)
			e.client_secret = p('required.client_secret');
		if (!identitiesText.split(/[\n,]/).some((s) => s.trim())) e.allowed_identities = p('required.allowed');
		if (kind === 'oauth2' && !form.identity_claim.trim()) e.identity_claim = p('required.claim');
		errors = e;
		formError = Object.keys(e).length ? p('fixFields') : '';
		return !Object.keys(e).length;
	}

	function requestSave() {
		if (validate()) {
			confirmError = '';
			confirming = true;
		}
	}

	async function save(password: string) {
		saving = true;
		confirmError = '';
		try {
			const saved = await saveProvider(provider?.id ?? '', {
				kind,
				display_name: form.display_name.trim(),
				issuer: kind === 'oidc' ? form.issuer.trim() : '',
				authorize_url: kind === 'oauth2' ? form.authorize_url.trim() : '',
				token_url: kind === 'oauth2' ? form.token_url.trim() : '',
				userinfo_url: kind === 'oauth2' ? form.userinfo_url.trim() : '',
				client_id: form.client_id.trim(),
				client_secret: clientSecret,
				clear_client_secret: clearSecret,
				scopes: scopesText.split(/\s+/).filter(Boolean),
				identity_claim: form.identity_claim.trim(),
				allowed_identities: identitiesText
					.split(/[\n,]/)
					.map((s) => s.trim())
					.filter(Boolean),
				redirect_url: form.redirect_url.trim(),
				current_password: password,
			});
			confirming = false;
			flash({ tone: 'success', title: p(editing ? 'updated' : 'added', { name: saved.display_name }) });
			await onsaved();
			onclose();
		} catch (err) {
			if (err instanceof ApiError && err.fieldErrors.current_password) {
				confirmError = err.fieldErrors.current_password;
				return;
			}
			if (err instanceof ApiError && err.code === 'AUTH_RATE_LIMITED') {
				confirmError = err.message;
				return;
			}
			confirming = false;
			if (err instanceof ApiError && Object.keys(err.fieldErrors).length) {
				errors = { ...err.fieldErrors };
				formError = err.code === 'ERR_VALIDATION_FAILED' ? p('fixFields') : err.message;
			} else {
				formError = errorMessage(err, p('saveFailed'));
			}
		} finally {
			saving = false;
		}
	}

	async function copyCallback() {
		try {
			await navigator.clipboard.writeText(form.redirect_url);
			copied = true;
			setTimeout(() => (copied = false), 1500);
		} catch {
			/* clipboard unavailable: the field stays selectable */
		}
	}
</script>

<ResourceDialog
	title={editing ? (provider?.display_name ?? p('editTitle')) : p('addTitle')}
	description={editing ? p('editDescription') : p('addDescription')}
	closeLabel={t('common.close')}
	onclose={() => {
		if (!saving) onclose();
	}}
	suspended={confirming}
>
	{#snippet icon()}<LogIn size={32} aria-hidden="true" />{/snippet}

	{#if editing && !provider}
		{#if loading}
			<p role="status">{p('loading')}</p>
		{:else}
			<Notice tone="error" message={p('notFound')} />
		{/if}
	{:else}
		<form
			class="stack"
			onsubmit={(e) => {
				e.preventDefault();
				requestSave();
			}}
			novalidate
		>
			{#if formError}<Notice tone="error" message={formError} />{/if}

			{#if !editing}
				<div class="templates" role="group" aria-label={p('templates')}>
					<span class="templates-label">{p('startFrom')}</span>
					<div class="template-buttons">
						{#each TEMPLATES as template (template)}
							<Button size="sm" onclick={() => applyTemplate(template)}>{p('template.' + template)}</Button>
						{/each}
					</div>
				</div>
			{/if}

			<h3>{p('sectionProvider')}</h3>
			<div class="lines">
				<LineField id="prov-kind" label={p('type')} info={p('typeInfo')} required>
					{#snippet children({ id, describedBy })}
						<Select
							{id}
							value={kind}
							aria-describedby={describedBy}
							onchange={(e) => changeKind((e.currentTarget as HTMLSelectElement).value as ProviderKind)}
						>
							<option value="oidc">{p('kind.oidc')}</option>
							<option value="oauth2">{p('kind.oauth2')}</option>
						</Select>
					{/snippet}
				</LineField>
				<LineField
					id="prov-name"
					label={p('displayName')}
					info={p('displayNameInfo')}
					error={errors.display_name}
					required
				>
					{#snippet children({ id, describedBy, invalid })}<input
							{id}
							bind:value={form.display_name}
							maxlength={40}
							placeholder="Authentik"
							aria-describedby={describedBy}
							aria-invalid={invalid}
						/>{/snippet}
				</LineField>
			</div>

			<h3>{p('sectionConnection')}</h3>
			<div class="lines">
				{#if kind === 'oidc'}
					<LineField id="prov-issuer" label={p('issuer')} info={p('issuerInfo')} error={errors.issuer} required>
						{#snippet children({ id, describedBy, invalid })}<input
								{id}
								type="url"
								bind:value={form.issuer}
								placeholder={issuerPlaceholder}
								spellcheck="false"
								aria-describedby={describedBy}
								aria-invalid={invalid}
							/>{/snippet}
					</LineField>
				{:else}
					<LineField
						id="prov-authorize"
						label={p('authorizeUrl')}
						info={p('authorizeInfo')}
						error={errors.authorize_url}
						required
					>
						{#snippet children({ id, describedBy, invalid })}<input
								{id}
								type="url"
								bind:value={form.authorize_url}
								placeholder="https://provider.example.com/oauth/authorize"
								spellcheck="false"
								aria-describedby={describedBy}
								aria-invalid={invalid}
							/>{/snippet}
					</LineField>
					<LineField id="prov-token" label={p('tokenUrl')} info={p('tokenInfo')} error={errors.token_url} required>
						{#snippet children({ id, describedBy, invalid })}<input
								{id}
								type="url"
								bind:value={form.token_url}
								placeholder="https://provider.example.com/oauth/token"
								spellcheck="false"
								aria-describedby={describedBy}
								aria-invalid={invalid}
							/>{/snippet}
					</LineField>
					<LineField
						id="prov-userinfo"
						label={p('userinfoUrl')}
						info={p('userinfoInfo')}
						error={errors.userinfo_url}
						required
					>
						{#snippet children({ id, describedBy, invalid })}<input
								{id}
								type="url"
								bind:value={form.userinfo_url}
								placeholder="https://provider.example.com/api/user"
								spellcheck="false"
								aria-describedby={describedBy}
								aria-invalid={invalid}
							/>{/snippet}
					</LineField>
				{/if}
				<LineField id="prov-client" label={p('clientId')} info={p('clientIdInfo')} error={errors.client_id} required>
					{#snippet children({ id, describedBy, invalid })}<input
							{id}
							bind:value={form.client_id}
							spellcheck="false"
							autocomplete="off"
							aria-describedby={describedBy}
							aria-invalid={invalid}
						/>{/snippet}
				</LineField>
				<LineField
					id="prov-secret"
					label={p('clientSecret')}
					info={provider?.credential_stored ? p('secretKeptInfo') : p('secretInfo')}
					error={errors.client_secret}
					required={kind === 'oauth2' && !provider?.credential_stored}
				>
					{#snippet children({ id, describedBy, invalid })}
						<PasswordInput
							{id}
							bind:value={clientSecret}
							{describedBy}
							{invalid}
							autocomplete="new-password"
							disabled={clearSecret}
						/>
						{#if provider?.credential_stored}<span class="hint">{p('secretKept')}</span>{/if}
						{#if provider?.credential_stored && kind === 'oidc'}
							<Checkbox
								checked={clearSecret}
								label={p('clearSecret')}
								help={p('clearSecretHelp')}
								onchange={(v) => (clearSecret = v)}
							/>
						{/if}
					{/snippet}
				</LineField>
				<LineField
					id="prov-callback"
					label={p('callback')}
					info={p('callbackInfo')}
					error={errors.redirect_url}
					required
				>
					{#snippet children({ id, describedBy, invalid })}
						<div class="inline">
							<input
								{id}
								bind:value={form.redirect_url}
								spellcheck="false"
								class="mono"
								aria-describedby={describedBy}
								aria-invalid={invalid}
							/>
							<button
								type="button"
								class="icon-button"
								onclick={copyCallback}
								aria-label={p('copyCallback')}
								title={p('copyCallback')}
							>
								{#if copied}<Check size={16} aria-hidden="true" />{:else}<Copy size={16} aria-hidden="true" />{/if}
							</button>
						</div>
					{/snippet}
				</LineField>
			</div>

			<h3>{p('sectionAccess')}</h3>
			<div class="lines">
				<LineField
					id="prov-allowed"
					label={p('allowed')}
					info={p('allowedHelp.' + preset)}
					error={errors.allowed_identities}
					required
				>
					{#snippet children({ id, describedBy, invalid })}<textarea
							{id}
							rows="3"
							bind:value={identitiesText}
							spellcheck="false"
							placeholder={preset === 'github' ? 'octocat' : 'you@example.com'}
							aria-describedby={describedBy}
							aria-invalid={invalid}></textarea>{/snippet}
				</LineField>
				<LineField
					id="prov-claim"
					label={p('claim')}
					info={p('claimInfo')}
					error={errors.identity_claim}
					required={kind === 'oauth2'}
				>
					{#snippet children({ id, describedBy, invalid })}<input
							{id}
							bind:value={form.identity_claim}
							spellcheck="false"
							placeholder={kind === 'oidc' ? 'email' : 'login'}
							aria-describedby={describedBy}
							aria-invalid={invalid}
						/>{/snippet}
				</LineField>
				<LineField id="prov-scopes" label={p('scopes')} info={p('scopesInfo')} error={errors.scopes}>
					{#snippet children({ id, describedBy, invalid })}<input
							{id}
							bind:value={scopesText}
							spellcheck="false"
							placeholder={kind === 'oidc' ? 'openid email profile' : 'read:user'}
							aria-describedby={describedBy}
							aria-invalid={invalid}
						/>{/snippet}
				</LineField>
			</div>
			<button type="submit" hidden aria-hidden="true" tabindex="-1"></button>
		</form>
	{/if}

	{#snippet footer()}
		<Button onclick={onclose} disabled={saving}>{t('common.cancel')}</Button>
		{#if !(editing && !provider)}
			<Button variant="primary" onclick={requestSave} disabled={saving}>
				{editing ? p('saveChanges') : p('addProvider')}
			</Button>
		{/if}
	{/snippet}
</ResourceDialog>

<PasswordConfirmDialog
	open={confirming}
	title={p('confirmTitle')}
	message={p(editing ? 'confirmUpdate' : 'confirmAdd', { name: form.display_name.trim() })}
	confirmLabel={editing ? p('saveChanges') : p('addProvider')}
	error={confirmError}
	pending={saving}
	oncancel={() => (confirming = false)}
	onconfirm={save}
/>

<style>
	/* Same section titles as the entry point and service dialogs; one field per line. */
	.stack {
		display: flex;
		flex-direction: column;
		gap: 24px;
	}
	.lines {
		display: flex;
		flex-direction: column;
		gap: 18px;
	}
	h3 {
		display: flex;
		align-items: center;
		gap: 12px;
		margin: 0;
		font-size: 13px;
		font-weight: 600;
		color: var(--text);
	}
	h3::after {
		content: '';
		flex: 1;
		height: 1px;
		background: var(--border);
	}
	/* A plain row of fill-in buttons, aligned with the field lines below. */
	.templates {
		display: grid;
		grid-template-columns: 180px minmax(0, 1fr);
		gap: 8px 20px;
		align-items: center;
	}
	.templates-label {
		color: var(--text);
		font-size: 14px;
		font-weight: 500;
	}
	.template-buttons {
		display: flex;
		flex-wrap: wrap;
		gap: 8px;
	}
	@media (max-width: 767px) {
		.templates {
			grid-template-columns: 1fr;
		}
	}
	.hint {
		color: var(--text-muted);
		font-size: 13px;
		line-height: 18px;
	}
	.inline {
		display: flex;
		gap: 8px;
		align-items: center;
	}
	.inline input {
		flex: 1;
		min-width: 0;
	}
	.mono {
		font-family: ui-monospace, SFMono-Regular, Consolas, monospace;
		font-size: 14px !important;
	}
	.icon-button {
		display: grid;
		place-items: center;
		width: 40px;
		height: var(--control-h);
		flex: none;
		padding: 0;
		border: 1px solid var(--outline);
		border-radius: var(--radius-control);
		background: var(--surface);
		color: var(--text-muted);
		cursor: pointer;
	}
	.icon-button:hover {
		color: var(--text);
		background: var(--surface-muted);
	}
</style>

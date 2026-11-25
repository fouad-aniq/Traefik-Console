<script lang="ts">
	/**
	 * Create / edit / view a certificate resolver against core-v2.
	 *
	 * Flow (create/edit):
	 *   open        → read the resolver version (GET /resolvers), providers, entrypoints
	 *   tabs        → Identity · Challenge · Options (client-side validation per tab)
	 *   Review tab  → PUT /resolvers/{name}?dry_run=true (+ If-Match / If-None-Match) returns the preview:
	 *                 diff, server validation errors, restart/mount requirements. Nothing is saved.
	 *   Apply       → the same PUT without dry_run, with one Idempotency-Key per attempt: it is validated,
	 *                 published, verified and committed in that one request.
	 */
	import { onMount, onDestroy, tick } from 'svelte';
	import { KeyRound, ExternalLink, X, CircleAlert, ShieldCheck, ShieldQuestion } from '@lucide/svelte';
	import { stringify } from 'yaml';
	import ResourceDialog from '../../shared/ui/ResourceDialog.svelte';
	import Field from '../../shared/ui/Field.svelte';
	import Checkbox from '../../shared/ui/Checkbox.svelte';
	import Tabs from '../../shared/ui/Tabs.svelte';
	import Button from '../../shared/ui/Button.svelte';
	import Notice from '../../shared/ui/Notice.svelte';
	import SummaryList from '../../shared/ui/SummaryList.svelte';
	import CodePreview from '../../shared/ui/CodePreview.svelte';
	import StatusBadge from '../../shared/ui/StatusBadge.svelte';
	import SearchableSelect from '../../shared/ui/SearchableSelect.svelte';
	import ProviderFieldInput from './ProviderField.svelte';
	import { navigationGuard } from '../../lib/stores/navigationGuard';
	import { needsManualRestart, newIdempotencyKey, operationOf, announceApplied } from '../../core/mutations';
	import { operationTone } from '../changes/models';
	import type { Operation, OperationPreview } from '../../core/types';
	import { getResolver, listEntryPoints, listProviders, listResolvers, newForm, formFrom, providerFields, putResolver, toInput } from './api';
	import { resolverErrors, validNameserver, type ValidationGroup } from './validation';
	import type { Provider, ProviderField, ResolverForm, ResolverResource } from './models';
	import { isSecretField } from './models';
	import { ApiError, NetworkError } from '../../shared/api/client';
	import { errorMessage } from '../../shared/api/errorMessage';
	import { t } from '../../shared/localization/i18n';

	let {
		mode = 'view',
		name = '',
		navigate,
		onchanged
	}: {
		mode?: string;
		name?: string;
		navigate: (p: string) => void;
		onchanged?: () => void;
	} = $props();

	const c = (key: string, args?: Record<string, string | number>) => t('resolverUI.' + key, args);
	const v2 = (key: string, args?: Record<string, string | number>) => t('resolverV2.' + key, args);

	// ---- state ------------------------------------------------------------
	let r = $state<ResolverForm>(newForm());
	let current = $state<ResolverResource | null>(null);
	let providers = $state<Provider[]>([]);
	let customCodes = $state<string[]>([]);
	let names = $state<string[]>([]);
	let entryPoints = $state<string[]>([]);
	let entryPointsKnown = $state(false);

	let loading = $state(true);
	let loadError = $state('');
	let busy = $state(false);
	let suspended = $state(false);
	let error = $state('');
	let step = $state('resolver-identity');
	let baseline = $state('');
	let fields = $state<Record<string, string>>({});
	let validatedGroups = $state<ValidationGroup[]>([]);
	let nameserverDraft = $state('');
	let nameserverError = $state('');

	// Direct write state: the form is previewed with ?dry_run=true, then saved with the same body and key
	let conflict = $state(false);
	let staged = $state(false);
	let review = $state<OperationPreview | null>(null);
	let reviewError = $state('');
	let applyKey = $state('');
	let uncertain = $state(false); // apply response lost: keep body+key for an exact retry
	let result = $state<Operation | null>(null);
	let manualRestart = $state(false);

	let disposed = false;
	const controller = new AbortController();

	// ---- derived ----------------------------------------------------------
	const creating = $derived(mode === 'create');
	const editing = $derived(
		(mode === 'create' || mode === 'edit') && (!current || (!current.read_only))
	);
	const provider = $derived(providers.find((p) => p.provider_code === r.provider));
	const groups = $derived(providerFields(provider, r.alternative));
	const configuredOptional = $derived(groups.optional.filter((f) => r.values[f.env_var]).length);
	const dirty = $derived(editing && !!baseline && JSON.stringify($state.snapshot(r)) !== baseline && !result);
	const locked = $derived(busy || !!result);
	const tabIds = $derived<string[]>(
		r.type === 'custom' ? ['identity', 'challenge', 'review'] : ['identity', 'challenge', 'advanced', 'review']
	);
	const tabs = $derived(
		tabIds.map((id) => {
			const label = v2('tab.' + id);
			const count = Object.keys(fields).filter((k) => k.startsWith(id + '.')).length;
			return { id: 'resolver-' + id, label, errorCount: count, errorLabel: c('tabErrors', { tab: label, count }) };
		})
	);
	const stepName = $derived(step.replace('resolver-', ''));
	const tabErrors = $derived(Object.entries(fields).filter(([key]) => key.startsWith(stepName + '.')));
	const serverErrors = $derived(review?.validation_errors ?? []);
	const lastEditTab = $derived(tabIds[tabIds.length - 2]);
	// core-v2 rejects apply (ERR_RECREATE_REQUIRED) until Traefik's container already has every
	// *_FILE variable, so block the button instead of letting it fail.
	const mounts = $derived(review?.env_vars_to_mount ?? []);
	const mountsMissing = $derived(mounts.some((m) => !m.already_set));
	const composeSnippet = $derived(
		['environment:', ...mounts.map((m) => `  ${m.env_var_name}: ${m.file_path}`), 'volumes:', `  - <SECRETS_DIR>:${mounts[0]?.file_path.replace(/\/[^/]*$/, '') ?? '/etc/traefik/secrets'}:ro`].join('\n')
	);

	$effect(() => {
		navigationGuard.setDirty(dirty);
		return () => navigationGuard.setDirty(false);
	});

	// ---- loading ----------------------------------------------------------
	onMount(() => {
		const unsub = navigationGuard.subscribe(() => (suspended = navigationGuard.confirmPending));
		void load();
		return unsub;
	});
	onDestroy(() => {
		disposed = true;
		controller.abort();
		r.values = {};
	});

	async function load() {
		loading = true;
		loadError = '';
		const signal = controller.signal;
		try {
			const [discovery, all, resource] = await Promise.all([
				listProviders(signal),
				listResolvers(signal),
				mode === 'create' ? Promise.resolve(null) : getResolver(name, signal)
			]);
			if (disposed) return;
			providers = discovery.providers;
			customCodes = discovery.custom_provider_codes;
			names = all.map((i) => i.name);
			if (resource) {
				current = resource;
				r = formFrom(resource);
			}
			baseline = JSON.stringify($state.snapshot(r));
		} catch (e) {
			if (!disposed) loadError = errorMessage(e, c('loadFailed'));
		} finally {
			if (!disposed) loading = false;
		}
		// Entrypoints are only needed for HTTP-01; a failure never blocks the dialog.
		try {
			const eps = await listEntryPoints(signal);
			if (!disposed) { entryPoints = eps; entryPointsKnown = true; }
		} catch { /* surfaced on the HTTP-01 field */ }
	}

	// ---- editing ----------------------------------------------------------
	function context() {
		return { form: r, creating, existingNames: names, provider, customProviderCodes: customCodes, entryPoints, entryPointsKnown };
	}
	const collect = (group: ValidationGroup) => resolverErrors(group, context(), c);

	function invalidate() {
		// Any edit makes the staged review stale; it is staged again on entering Review.
		review = null;
		reviewError = '';
		staged = false;
		applyKey = '';
		uncertain = false;
		error = '';
		conflict = false;
		fields = Object.assign({}, ...validatedGroups.map(collect));
	}
	function changeType(type: string) {
		if (type === r.type) return;
		r = { ...newForm(), name: r.name, type: type as ResolverForm['type'] };
		nameserverDraft = '';
		validatedGroups = [];
		invalidate();
	}
	function changeProvider(value: string) {
		if (value === r.provider) return;
		r.provider = value;
		r.alternative = 0;
		r.values = {};
		invalidate();
	}
	function changeAlternative(value: string) {
		r.alternative = Number(value);
		r.values = {};
		invalidate();
	}
	function toggleExternal(on: boolean) {
		r.externalCredentials = on;
		r.values = {};
		if (on && !customCodes.includes(r.provider)) r.provider = '';
		if (!on && !providers.some((p) => p.provider_code === r.provider)) r.provider = providers[0]?.provider_code ?? '';
		r.alternative = 0;
		invalidate();
	}
	function setValue(field: ProviderField, value: string) {
		if (value === '') delete r.values[field.env_var];
		else r.values[field.env_var] = value;
		invalidate();
	}
	function validateGroups(list: string[]) {
		const wanted = list.filter((g): g is ValidationGroup => g === 'identity' || g === 'challenge' || g === 'advanced');
		validatedGroups = [...new Set([...validatedGroups, ...wanted])];
		fields = Object.assign({}, ...validatedGroups.map(collect));
		return !wanted.some((g) => Object.keys(fields).some((k) => k.startsWith(g + '.')));
	}
	function commitNameserver(raw = nameserverDraft) {
		const value = raw.trim();
		if (!value) return true;
		if (!validNameserver(value) || r.nameservers.length >= 8) { nameserverError = c('invalidNameservers'); return false; }
		if (!r.nameservers.some((n) => n.toLowerCase() === value.toLowerCase())) { r.nameservers = [...r.nameservers, value]; invalidate(); }
		nameserverDraft = '';
		nameserverError = '';
		return true;
	}
	function removeNameserver(index: number) {
		r.nameservers = r.nameservers.filter((_, i) => i !== index);
		nameserverError = '';
		invalidate();
	}

	function controlId(key: string) {
		const [, field] = key.split('.');
		const special: Record<string, string> = { name: 'resolver-name', email: 'resolver-email', caUrl: 'resolver-ca-url', provider: 'resolver-provider', entryPoint: 'resolver-entrypoint', yaml: 'resolver-yaml', keyType: 'resolver-key', nameservers: 'resolver-nameservers', delay: 'resolver-delay' };
		return special[field] ?? 'credential-' + field;
	}
	function errorLabel(key: string) {
		const [group, field] = key.split('.');
		const known = ['name', 'email', 'caUrl', 'provider', 'entryPoint', 'yaml', 'keyType', 'nameservers', 'delay'];
		return group === 'challenge' && !known.includes(field) ? t('resolverUI.providerFields.' + field) : c(field);
	}
	async function focusField(key: string) {
		step = 'resolver-' + key.split('.')[0];
		await tick();
		const target = document.getElementById(controlId(key));
		for (let p = target?.parentElement; p; p = p.parentElement) if (p instanceof HTMLDetailsElement) p.open = true;
		await tick();
		target?.focus();
		target?.scrollIntoView({ block: 'nearest' });
	}
	async function focusFirstError() {
		const key = Object.keys(fields)[0];
		if (key) await focusField(key);
	}

	async function selectStep(value: string) {
		if (busy || result) return;
		if (!editing) { step = value; return; }
		const target = value.replace('resolver-', '');
		if (target === 'review') { await stageAndReview(); return; }
		const from = tabIds.indexOf(stepName);
		const to = tabIds.indexOf(target);
		if (to > from && !validateGroups(tabIds.slice(0, to))) { await focusFirstError(); return; }
		step = value;
	}

	// ---- direct write protocol --------------------------------------------
	function describeFailure(e: unknown, fallback: string) {
		if (e instanceof ApiError && e.code === 'ERR_VERSION_CONFLICT') {
			conflict = true;
			return v2('conflict');
		}
		const base = errorMessage(e, fallback);
		return e instanceof ApiError && e.operationId ? `${base} (${v2('operation')} ${e.operationId})` : base;
	}

	async function stageAndReview() {
		if (busy || !editing) return;
		if (r.type === 'acme' && r.challenge === 'dns01' && !commitNameserver()) {
			fields = { ...fields, 'advanced.nameservers': nameserverError };
			await focusField('advanced.nameservers');
			return;
		}
		if (!validateGroups(tabIds.filter((i) => i !== 'review'))) { await focusFirstError(); return; }
		busy = true;
		error = '';
		conflict = false;
		try {
			const preview = await putResolver(r.name, toInput($state.snapshot(r)), current?.version ?? 0, { dryRun: true, signal: controller.signal });
			if (disposed) return;
			review = preview.preview ?? null;
			applyKey = newIdempotencyKey();
			step = 'resolver-review';
		} catch (e) {
			if (disposed) return;
			if (e instanceof ApiError && e.status === 422) {
				const mapped = mapFieldErrors(e);
				if (Object.keys(mapped).length) { fields = { ...fields, ...mapped }; await focusFirstError(); }
			}
			error = describeFailure(e, v2('stageFailed'));
		} finally {
			if (!disposed) busy = false;
		}
	}

	/** Server field_errors → form keys (best effort; core-v2 mostly returns one message). */
	function mapFieldErrors(e: ApiError) {
		const alias: Record<string, string> = { email: 'identity.email', ca_url: 'identity.caUrl', name: 'identity.name', provider: 'challenge.provider', entry_point: 'challenge.entryPoint', custom_yaml: 'challenge.yaml', key_type: 'advanced.keyType', nameservers: 'advanced.nameservers', delay: 'advanced.delay' };
		const mapped: Record<string, string> = {};
		for (const [path, message] of Object.entries(e.fieldErrors)) {
			const key = path.replace(/^values\./, '');
			const target = alias[path] ?? (groups.required.concat(groups.optional).some((f) => f.env_var === key) ? 'challenge.' + key : undefined);
			if (target) mapped[target] = message;
		}
		return mapped;
	}

	async function refreshVersion() {
		// Explicit user action after a version conflict: re-read the resource, keep the form input.
		busy = true;
		try {
			if (!creating) current = await getResolver(name, controller.signal);
			conflict = false;
			error = '';
		} catch (e) {
			error = describeFailure(e, v2('reviewFailed'));
		} finally {
			if (!disposed) busy = false;
		}
	}

	async function apply() {
		if (!review || busy || serverErrors.length || mountsMissing) return;
		busy = true;
		error = '';
		try {
			// Same body + key on every retry of an uncertain attempt.
			const saved = await putResolver(r.name, toInput($state.snapshot(r)), current?.version ?? 0, { idempotencyKey: applyKey, signal: controller.signal });
			if (disposed) return;
			result = operationOf(saved, t('writeV2.noChange'));
			manualRestart = needsManualRestart(saved);
			uncertain = false;
			staged = true;
			navigationGuard.setDirty(false);
			onchanged?.();
			// Applied: back to the table; the outcome is announced as a flash.
			if (announceApplied(result, manualRestart, r.name)) { navigate('/resolvers'); return; }
		} catch (e) {
			if (disposed) return;
			if (e instanceof NetworkError) {
				uncertain = true;
				error = v2('applyUncertain');
			} else {
				if (e instanceof ApiError && [404, 409, 410, 412, 422, 428].includes(e.status)) {
					review = null;
					applyKey = '';
					uncertain = false;
					step = 'resolver-' + lastEditTab;
				}
				error = describeFailure(e, v2('applyFailed'));
			}
		} finally {
			if (!disposed) busy = false;
		}
	}

	function close() {
		if (busy) return;
		navigationGuard.requestNavigation(() => {
			if (staged || result) onchanged?.();
			navigate('/resolvers');
		});
	}


	const renderedYaml = $derived(current?.config.rendered_node ? stringify(current.config.rendered_node) : '');
	const authLabel = (index: number) => {
		const key = `resolverUI.authNames.${r.provider}.${index}`;
		const known = t(key);
		return known === key ? (provider?.auth_alternatives?.[index]?.alternative_name ?? String(index)) : known;
	};
	const fieldLabel = (f: ProviderField) => {
		const key = 'resolverUI.providerFields.' + f.env_var;
		const known = t(key);
		return known === key ? f.env_var : known;
	};
	const fieldHelp = (f: ProviderField, required: boolean) => {
		const key = 'resolverUI.providerHelp.' + f.env_var;
		const known = t(key);
		return [
			known === key ? f.description : known,
			f.default_value && !isSecretField(f) ? c('defaultValue') + ' ' + f.default_value : '',
			f.type === 'duration' ? c('durationHelp') : '',
			f.bounds ? c('allowedValues') + ' ' + f.bounds : ''
		].filter(Boolean).join(' ');
	};
</script>

<ResourceDialog
	title={mode === 'create' ? c('create') : mode === 'edit' ? c('edit') : name}
	description={mode === 'create' || mode === 'edit' ? '' : t('topology.kind.resolver')}
	closeLabel={t('common.close')}
	onclose={close}
	{suspended}
>
	{#snippet icon()}<KeyRound size={32} aria-hidden="true" />{/snippet}

	{#snippet navigation()}
		{#if !loading && !loadError && editing}
			<Tabs {tabs} selected={step} onselect={selectStep} label={c('tabs')} />
		{/if}
	{/snippet}

	{#if loading}
		<p role="status">{t('domainFlow.loading')}</p>
	{:else if loadError}
		<div class="dialog-content">
			<Notice tone="error" message={loadError} />
			<div><Button onclick={load}>{t('common.retry')}</Button></div>
		</div>
	{:else}
		<div class="dialog-content">
			{#if error}
				<Notice tone="error" message={error}>
					{#if conflict}<Button size="sm" onclick={refreshVersion} disabled={busy}>{v2('refreshDraft')}</Button>{/if}
					{#if uncertain}<Button size="sm" onclick={apply} disabled={busy}>{c('retryApply')}</Button>{/if}
				</Notice>
			{/if}

			{#if editing && tabErrors.length && step !== 'resolver-review'}
				<div class="validation-banner" role="alert">
					<CircleAlert size={16} aria-hidden="true" />
					<span>{v2(tabErrors.length === 1 ? 'fixFieldsOne' : 'fixFields', { count: tabErrors.length })}</span>
					<button type="button" onclick={() => focusField(tabErrors[0][0])}>{v2('goToField')}</button>
				</div>
			{/if}

			{#if editing}
				<!-- ============ Identity ============ -->
				<div class="stack" id="panel-resolver-identity" role="tabpanel" aria-labelledby="tab-resolver-identity" hidden={step !== 'resolver-identity'}>
					<Field id="resolver-name" label={c('name')} required error={fields['identity.name']} help={fields['identity.name'] ? '' : c('nameHelp')}>
						{#snippet children(f)}
							<input id={f.id} bind:value={r.name} oninput={invalidate} disabled={locked || mode === 'edit'} aria-invalid={f.invalid || undefined} aria-describedby={f.describedBy} autocomplete="off" spellcheck="false" placeholder="letsencrypt" />
						{/snippet}
					</Field>
					<Field id="resolver-type" label={c('type')} required>
						{#snippet children(f)}
							<SearchableSelect id={f.id} value={r.type} options={[{ value: 'acme', label: c('guided') }, { value: 'custom', label: c('custom') }]} onchange={changeType} disabled={locked || mode === 'edit'} />
						{/snippet}
					</Field>
					{#if r.type === 'acme'}
						<Field id="resolver-email" label={c('email')} required error={fields['identity.email']}>
							{#snippet children(f)}
								<input id={f.id} type="email" bind:value={r.email} oninput={invalidate} disabled={locked} aria-invalid={f.invalid || undefined} aria-describedby={f.describedBy} autocomplete="email" placeholder="admin@example.com" />
							{/snippet}
						</Field>
						<Field id="resolver-environment" label={c('environment')} help={mode === 'edit' ? v2('environmentLocked') : c('environmentHelp')}>
							{#snippet children(f)}
								<SearchableSelect id={f.id} value={r.environment} options={[{ value: 'staging', label: c('staging') }, { value: 'production', label: c('production') }, { value: 'custom', label: c('customCA') }]} onchange={(v) => { r.environment = v as ResolverForm['environment']; invalidate(); }} disabled={locked || mode === 'edit'} />
							{/snippet}
						</Field>
						{#if r.environment === 'custom'}
							<Field id="resolver-ca-url" label={c('caUrl')} required error={fields['identity.caUrl']}>
								{#snippet children(f)}
									<input id={f.id} bind:value={r.caUrl} oninput={invalidate} type="url" placeholder="https://acme.example.com/directory" disabled={locked} aria-invalid={f.invalid || undefined} aria-describedby={f.describedBy} />
								{/snippet}
							</Field>
						{/if}
					{/if}
				</div>

				<!-- ============ Challenge ============ -->
				<div class="stack" id="panel-resolver-challenge" role="tabpanel" aria-labelledby="tab-resolver-challenge" hidden={step !== 'resolver-challenge'}>
					{#if r.type === 'custom'}
						<Field id="resolver-yaml" label={c('yaml')} required error={fields['challenge.yaml']} help={c('customHelp')}>
							{#snippet children(f)}
								<textarea id={f.id} bind:value={r.customYaml} oninput={invalidate} rows="10" disabled={locked} spellcheck="false" aria-invalid={f.invalid || undefined} aria-describedby={f.describedBy} placeholder={'acme:\n  email: operator@example.com\n  httpChallenge:\n    entryPoint: web'}></textarea>
							{/snippet}
						</Field>
						<Checkbox id="resolver-external" checked={r.externalCredentials} label={c('externalCredentials')} disabled={locked} onchange={(v) => { r.externalCredentials = v; invalidate(); }} />
					{:else}
						<Field id="resolver-challenge" label={c('challenge')} required>
							{#snippet children(f)}
								<SearchableSelect id={f.id} value={r.challenge} options={['dns01', 'http01', 'tlsalpn01'].map((value) => ({ value, label: c(value) }))} onchange={(v) => { r.challenge = v as ResolverForm['challenge']; invalidate(); }} disabled={locked} />
							{/snippet}
						</Field>

						{#if r.challenge === 'dns01'}
							{#if r.externalCredentials}
								<Field id="resolver-provider" label={v2('providerCode')} required error={fields['challenge.provider']} help={v2('providerCodeHelp')}>
									{#snippet children(f)}
										<SearchableSelect id={f.id} value={r.provider} options={customCodes.map((code) => ({ value: code, label: providers.find((p) => p.provider_code === code)?.display_name ?? code }))} onchange={(v) => { r.provider = v; invalidate(); }} disabled={locked} ariaInvalid={f.invalid} ariaDescribedby={f.describedBy} searchPlaceholder={c('searchProviders')} />
									{/snippet}
								</Field>
							{:else}
								<Field id="resolver-provider" label={c('provider')} required error={fields['challenge.provider']}>
									{#snippet children(f)}
										<SearchableSelect id={f.id} value={r.provider} options={providers.map((p) => ({ value: p.provider_code, label: p.display_name }))} onchange={changeProvider} disabled={locked} ariaInvalid={f.invalid} ariaDescribedby={f.describedBy} searchPlaceholder={c('searchProviders')} />
										{#if provider}
											<!-- Compact provenance line: verification state, audit note, documentation. -->
											<p class="provider-meta">
												<span class="provider-status" class:audited={provider.verification_status === 'source_audited'}>
													{#if provider.verification_status === 'source_audited'}<ShieldCheck size={13} aria-hidden="true" />{:else}<ShieldQuestion size={13} aria-hidden="true" />{/if}
													{v2('verification.' + provider.verification_status).replace(/:\s*$/, '')}
												</span>
												{#if provider.source_audit_notes}<span class="provider-notes">{provider.source_audit_notes}</span>{/if}
												<a class="provider-documentation" href={provider.doc_url} target="_blank" rel="noopener noreferrer">{c('documentation')}<ExternalLink size={12} aria-hidden="true" /></a>
											</p>
										{/if}
									{/snippet}
								</Field>
								{#if provider}
									{#if (provider.auth_alternatives?.length ?? 0) > 1}
										<Field id="resolver-auth" label={c('authentication')} required help={provider.auth_alternatives?.[r.alternative]?.description ?? ''}>
											{#snippet children(f)}
												<SearchableSelect id={f.id} value={String(r.alternative)} options={(provider?.auth_alternatives ?? []).map((a, i) => ({ value: String(i), label: authLabel(i) }))} onchange={changeAlternative} disabled={locked} />
											{/snippet}
										</Field>
									{/if}
									{#key `${r.provider}:${r.alternative}`}
										{#each groups.required as field (field.env_var)}
											<ProviderFieldInput {field} label={fieldLabel(field)} help={fieldHelp(field, true)} required value={r.values[field.env_var]} error={fields['challenge.' + field.env_var]} disabled={locked} onchange={(v) => setValue(field, v)} />
										{/each}
										{#if groups.optional.length}
											<details class="options">
												<summary>{c('moreOptions')} <span>{configuredOptional ? c('configured', { count: configuredOptional }) : c('providerDefaults')}</span></summary>
												<div class="stack option-fields">
													{#each groups.optional as field (field.env_var)}
														<ProviderFieldInput {field} label={fieldLabel(field)} help={fieldHelp(field, false)} value={r.values[field.env_var]} error={fields['challenge.' + field.env_var]} disabled={locked} onchange={(v) => setValue(field, v)} />
													{/each}
												</div>
											</details>
										{/if}
									{/key}
								{/if}
							{/if}
							<Checkbox id="resolver-external" checked={r.externalCredentials} label={v2('externalCredentialsGuided')} disabled={locked} onchange={toggleExternal} />
						{:else if r.challenge === 'http01'}
							<Field id="resolver-entrypoint" label={c('entryPoint')} required error={fields['challenge.entryPoint']} help={c('httpHelp')}>
								{#snippet children(f)}
									<SearchableSelect id={f.id} value={r.entryPoint} options={entryPoints.map((e) => ({ value: e, label: e }))} onchange={(v) => { r.entryPoint = v; invalidate(); }} disabled={locked || !entryPoints.length} ariaInvalid={f.invalid} ariaDescribedby={f.describedBy} placeholder={c('selectEntryPoint')} />
								{/snippet}
							</Field>
							{#if !entryPointsKnown}<Notice tone="warning" message={c('startupFailed')} />{:else if !entryPoints.length}<Notice message={c('noEntryPoints')} />{/if}
						{:else}
							<Notice message={c('tlsHelp')} />
						{/if}
					{/if}
				</div>

				<!-- ============ Options ============ -->
				{#if r.type === 'acme'}
					<div class="stack" id="panel-resolver-advanced" role="tabpanel" aria-labelledby="tab-resolver-advanced" hidden={step !== 'resolver-advanced'}>
						<Field id="resolver-key" label={c('keyType')} error={fields['advanced.keyType']} help={c('keyTypeHelp')}>
							{#snippet children(f)}
								<SearchableSelect id={f.id} value={r.keyType} options={[{ value: '', label: c('defaultKey') }, ...['EC256', 'EC384', 'RSA2048', 'RSA4096', 'RSA8192'].map((value) => ({ value, label: value }))]} onchange={(v) => { r.keyType = v; invalidate(); }} disabled={locked} ariaInvalid={f.invalid} ariaDescribedby={f.describedBy} />
							{/snippet}
						</Field>
						{#if r.challenge === 'dns01'}
							<Field id="resolver-nameservers" label={c('nameservers')} error={nameserverError || fields['advanced.nameservers']} help={c('nameserversHelp')}>
								{#snippet children(f)}
									<div class="nameserver-box" class:invalid={f.invalid}>
										{#each r.nameservers as item, index (index)}
											<span class="nameserver-chip"><span>{item}</span><button type="button" disabled={locked} aria-label={`${c('remove')} ${item}`} onclick={() => removeNameserver(index)}><X size={12} aria-hidden="true" /></button></span>
										{/each}
										<input id={f.id} class="nameserver-entry" data-embedded-input type="text" bind:value={nameserverDraft} disabled={locked} aria-invalid={f.invalid || undefined} aria-describedby={f.describedBy} placeholder={r.nameservers.length ? c('addNameserver') : '1.1.1.1:53'} autocomplete="off" spellcheck="false" oninput={() => (nameserverError = '')}
											onkeydown={(event) => {
												if (event.key === 'Enter' || event.key === ',') { event.preventDefault(); commitNameserver(event.currentTarget.value); }
												else if (event.key === 'Backspace' && !event.currentTarget.value && r.nameservers.length) removeNameserver(r.nameservers.length - 1);
											}}
											onblur={(event) => { if (event.currentTarget.value.trim()) commitNameserver(event.currentTarget.value); }} />
									</div>
								{/snippet}
							</Field>
							<Field id="resolver-delay" label={c('delay')} error={fields['advanced.delay']} help={c('delayHelp')}>
								{#snippet children(f)}
									<input id={f.id} placeholder="0s" bind:value={r.delay} oninput={invalidate} disabled={locked} aria-invalid={f.invalid || undefined} aria-describedby={f.describedBy} />
								{/snippet}
							</Field>
							<details class="options">
								<summary>{c('propagation')}</summary>
								<div class="stack option-fields">
									{#each ['requireAllRNS', 'disableANSChecks', 'disableChecks'] as key}
										<Checkbox checked={r.propagation[key as keyof ResolverForm['propagation']]} label={c(key)} disabled={locked} onchange={(v) => { r.propagation[key as keyof ResolverForm['propagation']] = v; invalidate(); }} />
									{/each}
								</div>
							</details>
						{/if}
					</div>
				{/if}

				<!-- ============ Review ============ -->
				<div class="stack" id="panel-resolver-review" role="tabpanel" aria-labelledby="tab-resolver-review" hidden={step !== 'resolver-review'}>
					{#if result}
						<Notice tone={operationTone(result.status)} title={t('operationV2.status.' + result.status)} message={result.summary}>
							{#if result.id}<span class="muted">{t('operationV2.id', { id: result.id })}</span>{/if}
						</Notice>
						{#if manualRestart}<Notice tone="warning" message={t('writeV2.manualRestart')} />{:else if result.requires_restart}<Notice tone="warning" message={c('lifecycleHelp')} />{/if}
					{:else if review}
						<SummaryList>
							<div><dt>{c('name')}</dt><dd>{r.name}</dd></div>
							<div><dt>{c('type')}</dt><dd>{r.type === 'custom' ? c('custom') : c('guided')}</dd></div>
							{#if r.type === 'acme'}
								<div><dt>{c('environment')}</dt><dd>{c(r.environment === 'custom' ? 'customCA' : r.environment)}</dd></div>
								<div><dt>{c('challenge')}</dt><dd>{c(r.challenge)}</dd></div>
								{#if r.challenge === 'dns01'}<div><dt>{c('provider')}</dt><dd>{provider?.display_name || r.provider}</dd></div>{/if}
							{/if}
							<div><dt>{v2('pendingTotal')}</dt><dd>{review.changes?.length ?? 0}</dd></div>
						</SummaryList>

						{#if serverErrors.length}
							<Notice tone="error" title={v2('serverErrors')}>
								<ul class="reasons">{#each serverErrors as ve}<li>{ve.item_id}{ve.field ? ' · ' + ve.field : ''}: {ve.message}</li>{/each}</ul>
							</Notice>
						{/if}

						{#if review.requires_restart}
							<Notice tone="warning" title={c('restartRequired')} message={c('lifecycleHelp')}>
								{#if review.restart_reasons?.length}<ul class="reasons">{#each review.restart_reasons as reason}<li>{reason}</li>{/each}</ul>{/if}
							</Notice>
						{/if}

						{#if review.env_vars_to_mount?.length}
							<h3>{v2('mounts.title')}</h3>
							<p class="muted">{v2('mounts.help')}</p>
							<ul class="mounts">
								{#each review.env_vars_to_mount as m}
									<li>
										<div class="mount-text"><code>{m.env_var_name}</code><span class="mount-path">{m.file_path}</span></div>
										<StatusBadge size="sm" tone={m.already_set ? 'success' : 'warning'} text={v2(m.via_env_file ? 'mounts.managed' : m.already_set ? 'mounts.set' : 'mounts.missing')} />
									</li>
								{/each}
							</ul>
							{#if mountsMissing}
								<Notice tone="warning" title={v2('mounts.blockedTitle')} message={v2('mounts.blocked')} />
								<CodePreview label={v2('mounts.snippet')} code={composeSnippet} language="yaml" />
							{/if}
						{/if}

						{#if review.static_diff}<CodePreview label={v2('diff.static')} code={review.static_diff} language="diff" />{/if}
						{#if review.dynamic_diff}<CodePreview label={v2('diff.dynamic')} code={review.dynamic_diff} language="diff" />{/if}
						{#if !review.static_diff && !review.dynamic_diff}<p class="muted">{v2('diff.none')}</p>{/if}
					{:else if reviewError}
						<Notice tone="error" message={reviewError}>
							<Button size="sm" onclick={stageAndReview}>{t('common.retry')}</Button>
						</Notice>
					{:else}
						<p role="status">{v2('staging')}</p>
					{/if}
				</div>
			{:else if current}
				<!-- ============ View (read-only / pending deletion) ============ -->
				{#if current.origin === 'external'}<Notice message={c('externalHelp')} />{/if}
				<SummaryList>
					<div><dt>{c('name')}</dt><dd>{current.name}</dd></div>
					<div><dt>{c('type')}</dt><dd>{current.config.type === 'custom' ? c('custom') : c('guided')}</dd></div>
					<div><dt>{c('ownership')}</dt><dd>{c(current.origin)}</dd></div>
					<div><dt>{v2('stateColumn')}</dt><dd>{v2('status.' + current.status)}</dd></div>
					{#if current.config.email}<div><dt>{c('email')}</dt><dd>{current.config.email}</dd></div>{/if}
					{#if current.config.environment}<div><dt>{c('environment')}</dt><dd>{c(current.config.environment === 'custom' ? 'customCA' : current.config.environment)}</dd></div>{/if}
					{#if current.config.challenge}<div><dt>{c('challenge')}</dt><dd>{c(current.config.challenge)}</dd></div>{/if}
					{#if current.config.provider}<div><dt>{c('provider')}</dt><dd>{providers.find((p) => p.provider_code === current?.config.provider)?.display_name ?? current.config.provider}</dd></div>{/if}
					{#if current.config.entry_point}<div><dt>{c('entryPoint')}</dt><dd>{current.config.entry_point}</dd></div>{/if}
					{#if current.config.storage}<div><dt>{c('storage')}</dt><dd>{current.config.storage}</dd></div>{/if}
				</SummaryList>
				{#each current.config.warnings ?? [] as warning}<p class="muted">{warning}</p>{/each}
				{#if renderedYaml}<CodePreview label={v2('rendered')} code={renderedYaml} language="yaml" />{/if}
			{/if}
		</div>
	{/if}

	{#snippet footer()}
		<Button onclick={close} disabled={busy}>{result || !editing ? t('common.close') : t('common.cancel')}</Button>
		{#if !loading && !loadError && !result}
			{#if editing}
				{#if step === 'resolver-review'}
					<Button onclick={() => (step = 'resolver-' + lastEditTab)} disabled={busy}>{c('back')}</Button>
					{#if review}
						<Button variant="primary" onclick={apply} pending={busy} disabled={busy || serverErrors.length > 0 || mountsMissing}>{c(busy ? 'applying' : uncertain ? 'retryApply' : 'apply')}</Button>
					{/if}
				{:else}
					{#if stepName !== 'identity'}
						<Button onclick={() => selectStep('resolver-' + tabIds[Math.max(0, tabIds.indexOf(stepName) - 1)])} disabled={busy}>{c('back')}</Button>
					{/if}
					<Button variant="primary" onclick={() => selectStep('resolver-' + tabIds[tabIds.indexOf(stepName) + 1])} pending={busy} disabled={busy}>
						{stepName === lastEditTab ? v2('stageReview') : c('continue')}
					</Button>
				{/if}
			{:else if current && !current.read_only}
				<Button variant="primary" onclick={() => navigate('/resolvers/' + encodeURIComponent(current!.name) + '/edit')}>{c('edit')}</Button>
			{/if}
		{/if}
	{/snippet}
</ResourceDialog>

<style>
	.dialog-content { display: flex; flex-direction: column; gap: 24px; }
	.stack:not([hidden]) { display: flex; flex-direction: column; gap: 24px; }
	h3 { display: flex; align-items: center; gap: 12px; margin: 0; font-size: 13px; font-weight: 600; color: var(--text); }
	h3::after { content: ''; flex: 1; height: 1px; background: var(--border); }
	.muted { margin: 0; color: var(--text-muted); font-size: 14px; line-height: 21px; }
	.validation-banner { display: flex; align-items: center; gap: 8px; padding: 10px 14px; border: 1px solid color-mix(in srgb, var(--danger-text) 30%, transparent); border-radius: var(--radius-card); background: var(--danger-bg); color: var(--danger-text); font-size: 13px; line-height: 18px; }
	.validation-banner span { flex: 1; }
	.validation-banner button { border: 0; background: transparent; color: inherit; font: inherit; font-weight: 600; cursor: pointer; padding: 2px 4px; }
	.validation-banner button:hover { text-decoration: underline; }
	.provider-meta { display: flex; flex-wrap: wrap; align-items: center; gap: 4px 10px; margin: 0; font-size: 12px; line-height: 18px; color: var(--text-muted); }
	.provider-status { display: inline-flex; align-items: center; gap: 4px; font-weight: 500; color: var(--text-muted); }
	.provider-status.audited { color: var(--success-text); }
	.provider-notes { min-width: 0; }
	.provider-documentation { display: inline-flex; align-items: center; gap: 4px; font-size: 12px; line-height: 18px; color: var(--text-muted); text-decoration: none; }
	.provider-documentation:hover { color: var(--connection); text-decoration: underline; }
	.options summary { cursor: pointer; font-size: 14px; font-weight: 500; line-height: 24px; }
	/* Status hint sits on the same line as the toggle label. */
	.options summary span { color: var(--text-muted); font-weight: 400; margin-left: 8px; }
	.options summary span::before { content: '·'; margin-right: 8px; }
	.option-fields { padding-top: 20px; }
	textarea { resize: vertical; }
	.reasons { margin: 8px 0 0; padding-left: 20px; font-size: 14px; line-height: 22px; }
	.mounts { list-style: none; margin: 0; padding: 0; display: flex; flex-direction: column; gap: 8px; }
	.mounts li { display: flex; align-items: center; justify-content: space-between; gap: 12px; padding: 10px 14px; border: 1px solid var(--border); border-radius: var(--radius-card); background: var(--surface); }
	.mount-text { display: flex; flex-direction: column; gap: 2px; min-width: 0; }
	.mount-text code { font-size: 13px; font-weight: 600; overflow-wrap: anywhere; }
	.mount-path { color: var(--text-muted); font-size: 12px; overflow-wrap: anywhere; }
	.nameserver-box { display: flex; flex-wrap: wrap; align-items: center; gap: 6px; min-height: var(--control-h); padding: 4px 8px; background: var(--surface); border: 1px solid var(--outline); border-radius: var(--radius-control); }
	.nameserver-box:focus-within { outline: 2px solid var(--focus-ring); outline-offset: -1px; }
	.nameserver-box.invalid { border-color: var(--danger-text); }
	.nameserver-chip { display: inline-flex; align-items: center; gap: 4px; max-width: 100%; padding: 2px 8px; background: var(--surface-muted); border: 1px solid var(--border); border-radius: 4px; color: var(--text); font-size: 13px; line-height: 20px; }
	.nameserver-chip span { overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
	.nameserver-chip button { display: grid; place-items: center; flex: none; min-width: 20px; min-height: 20px; padding: 0; border: 0; border-radius: 2px; background: transparent; color: var(--text-muted); cursor: pointer; }
	.nameserver-chip button:hover:not(:disabled) { color: var(--danger-text); }
	.nameserver-box input.nameserver-entry { flex: 1 1 120px; width: auto; min-width: 110px; min-height: 28px; padding: 2px 4px; border: 0; border-radius: 0; background: transparent; outline: 0; color: var(--text); font: inherit; font-size: 16px; line-height: 24px; }
	.nameserver-box input.nameserver-entry::placeholder { color: var(--text-muted); }
</style>

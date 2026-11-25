<script lang="ts">
	/**
	 * Add or edit a domain against core-v2.
	 *
	 *   open       → GET /domains (names + versions), /resolvers, /certificates, /entrypoints
	 *   Domain tab → hostname, additional names (identifier generated, never typed)
	 *   Certificate tab → method (resolver | uploaded file), the existing resolver or certificate
	 *   Review tab → PUT /domains/{name}?dry_run=true + If-Match returns the preview (core-v2 re-checks
	 *                the binding); nothing is saved
	 *   Apply      → the same PUT without dry_run, with one Idempotency-Key per attempt
	 */
	import { onMount, onDestroy, tick } from 'svelte';
	import { Globe, CircleAlert } from '@lucide/svelte';
	import ResourceDialog from '../../shared/ui/ResourceDialog.svelte';
	import Field from '../../shared/ui/Field.svelte';
	import Tabs from '../../shared/ui/Tabs.svelte';
	import Button from '../../shared/ui/Button.svelte';
	import Notice from '../../shared/ui/Notice.svelte';
	import SummaryList from '../../shared/ui/SummaryList.svelte';
	import CodePreview from '../../shared/ui/CodePreview.svelte';
	import RelativeTime from '../../shared/ui/RelativeTime.svelte';
	import SearchableSelect from '../../shared/ui/SearchableSelect.svelte';
	import RadioCards from '../../shared/ui/RadioCards.svelte';
	import { navigationGuard } from '../../lib/stores/navigationGuard';
	import { needsManualRestart, newIdempotencyKey, operationOf, announceApplied } from '../../core/mutations';
	import { operationTone } from '../changes/models';
	import type { Operation, OperationPreview } from '../../core/types';
	import { listDomains, putDomain } from './api';
	import { domainErrors, toInput, type DomainField, type DomainGroup } from './validation';
	import { coversAll, domainKey, newForm, normalizeDomain, parseNames, referenceOf, type DomainForm, type DomainResource } from './models';
	import { listResolvers } from '../resolvers/api';
	import { listCertificates } from '../certificates/api';
	import type { ResolverResource } from '../resolvers/models';
	import { validityOf, type CertificateResource } from '../certificates/models';
	import { ApiError, NetworkError } from '../../shared/api/client';
	import { errorMessage } from '../../shared/api/errorMessage';
	import { t } from '../../shared/localization/i18n';

	let { mode = 'create', name = '', navigate, onchanged }: { mode?: string; name?: string; navigate: (p: string) => void; onchanged?: () => void } = $props();

	const c = (key: string, args?: Record<string, string | number>) => t('domainFlow.' + key, args);
	const u = (key: string) => t('domainUI.' + key);
	const v2 = (key: string, args?: Record<string, string | number>) => t('domainV2.' + key, args);
	const rv2 = (key: string, args?: Record<string, string | number>) => t('resolverV2.' + key, args);

	const creating = $derived(mode === 'create');
	const parent = '/domains';

	// New domains start on automatic certificates, the common case.
	let form = $state<DomainForm>({ ...newForm(), method: 'resolver' });
	let existing = $state<DomainResource[]>([]);
	let resolvers = $state<ResolverResource[]>([]);
	let certificates = $state<CertificateResource[]>([]);
	let choicesError = $state('');
	let loading = $state(true);
	let choicesLoading = $state(true);
	let loadError = $state('');
	let busy = $state(false);
	let suspended = $state(false);
	let error = $state('');
	let step = $state('domain-identity');
	let baseline = $state('');
	let fields = $state<Partial<Record<DomainField, string>>>({});
	let validated = $state<DomainGroup[]>([]);

	let conflict = $state(false);
	let staged = $state<DomainResource | null>(null);
	let review = $state<OperationPreview | null>(null);
	let reviewError = $state('');
	let applyKey = $state('');
	let uncertain = $state(false);
	let result = $state<Operation | null>(null);
	let manualRestart = $state(false);

	let disposed = false;
	const controller = new AbortController();

	const hosts = $derived([normalizeDomain(form.main) ?? form.main.trim(), ...parseNames(form.sans).map((s) => normalizeDomain(s) ?? s)].filter(Boolean));
	const current = $derived(existing.find((d) => d.name === name));
	const version = () => current?.version ?? 0;
	const editable = $derived(creating || !current || (!current.read_only));
	const dirty = $derived(editable && !!baseline && JSON.stringify($state.snapshot(form)) !== baseline && !result);
	const locked = $derived(busy || !!result);
	const serverErrors = $derived(review?.validation_errors ?? []);
	const mounts = $derived(review?.env_vars_to_mount ?? []);
	const mountsMissing = $derived(mounts.some((m) => !m.already_set));

	const resolverOptions = $derived(
		resolvers.map((r) => ({
			value: r.name,
			label: r.name,
			description: [r.config.provider, r.config.environment, r.config.challenge, r.origin === 'external' ? u('external') : ''].filter(Boolean).join(' · ')
		}))
	);
	const certificateOptions = $derived(
		certificates.filter((r) => r.config.purpose === 'server' && coversAll(r.config.domains ?? [], hosts)).map((r) => ({
			value: r.name,
			label: r.name,
			description: (r.config.domains ?? []).join(', '),
			badge: t('certificateUI.' + validityOf(r.config.status))
		}))
	);
	const options = $derived(form.method === 'resolver' ? resolverOptions : certificateOptions);
	const selectedResolver = $derived(resolvers.find((r) => r.name === form.reference));
	const selectedCertificate = $derived(certificates.find((r) => r.name === form.reference));

	const errorsFor = (group: DomainGroup) => ({ domain: ['main', 'sans'], certificate: ['method', 'reference'] })[group].filter((k) => fields[k as DomainField]).length;
	const tabs = $derived([
		{ id: 'domain-identity', label: u('domainTab'), errorCount: errorsFor('domain'), errorLabel: c('fixDomain') },
		{ id: 'domain-certificate', label: u('certificateTab'), errorCount: errorsFor('certificate'), errorLabel: c('fixCertificate') },
		{ id: 'domain-review', label: u('review') }
	]);
	const errorCount = $derived(Object.keys(fields).length);
	const messages = $derived({
		invalidName: v2('invalidName'), nameTaken: v2('nameTaken'), invalidDomain: u('invalidDomain'), invalidSans: u('invalidSans'),
		chooseMethod: u('chooseMethod'), chooseExisting: u('chooseExisting'), notCovered: c('notCoveredHelp'), wildcard: u('wildcard')
	});

	$effect(() => {
		navigationGuard.setDirty(dirty);
		return () => navigationGuard.setDirty(false);
	});

	onMount(() => {
		const unsub = navigationGuard.subscribe(() => (suspended = navigationGuard.confirmPending));
		void load();
		return unsub;
	});
	onDestroy(() => { disposed = true; controller.abort(); });

	async function load() {
		loading = true;
		loadError = '';
		try {
			const [all] = await Promise.all([listDomains(controller.signal)]);
			if (disposed) return;
			existing = all;
			if (!creating) {
				const item = all.find((d) => d.name === name);
				if (!item) throw new Error(u('loadFailed'));
				form = { name: item.name, nameTouched: true, main: item.config.main, sans: (item.config.sans ?? []).join('\n'), method: item.config.method, reference: referenceOf(item.config) };
			}
			baseline = JSON.stringify($state.snapshot(form));
		} catch (e) {
			if (!disposed) loadError = errorMessage(e, u('loadFailed'));
		} finally {
			if (!disposed) loading = false;
		}
		void loadChoices();
	}

	async function loadChoices() {
		choicesLoading = true;
		choicesError = '';
		const [r, cert] = await Promise.allSettled([listResolvers(controller.signal), listCertificates(controller.signal)]);
		if (disposed) return;
		if (r.status === 'fulfilled') resolvers = r.value; else choicesError = u('choicesFailed');
		if (cert.status === 'fulfilled') certificates = cert.value; else choicesError = u('choicesFailed');
		// Entrypoints are optional (default websecure); a failure never blocks the dialog.
		choicesLoading = false;
	}

	function collect(group: DomainGroup) {
		return domainErrors(group, $state.snapshot(form), {
			creating,
			existingNames: existing.map((d) => d.name),
			optionValues: options.map((o) => o.value),
			referenceExistsButUncovered: !!selectedCertificate,
			resolverChallenge: selectedResolver?.config.challenge
		}, messages);
	}
	function revalidate() {
		fields = Object.assign({}, ...validated.map(collect));
	}
	function invalidate() {
		// Any edit makes the staged review stale; it is staged again on entering Review.
		review = null; staged = null; reviewError = ''; applyKey = ''; uncertain = false; error = ''; conflict = false;
		revalidate();
	}
	function setMain(value: string) {
		form.main = value;
		if (creating && !form.nameTouched) {
			const normalized = normalizeDomain(value);
			form.name = normalized ? uniqueKey(normalized) : '';
		}
		invalidate();
	}
	// The identifier is generated here (never typed); a different domain already using the key gets a numeric suffix.
	function uniqueKey(host: string) {
		const base = domainKey(host);
		const taken = (key: string) => existing.some((d) => d.name === key && d.config.main !== host);
		let key = base;
		for (let i = 2; taken(key) && i < 100; i++) key = base.slice(0, 60) + '-' + i;
		return key;
	}
	function chooseMethod(value: string) {
		if (form.method === value) return;
		form.method = value as DomainForm['method'];
		form.reference = '';
		invalidate();
	}
	function validateGroups(groups: DomainGroup[]) {
		validated = [...new Set([...validated, ...groups])];
		fields = Object.assign({}, ...validated.map(collect));
		return !groups.some((g) => errorsFor(g) > 0);
	}

	const controlId = (key: DomainField) => ({ main: 'domain-main', sans: 'domain-sans', method: 'domain-method', reference: 'domain-reference' })[key];
	const tabOf = (key: DomainField) => (key === 'method' || key === 'reference' ? 'domain-certificate' : 'domain-identity');
	async function focusFirstError() {
		const key = (Object.keys(fields) as DomainField[])[0];
		if (!key) return;
		step = tabOf(key);
		await tick();
		const target = document.getElementById(controlId(key)) ?? document.querySelector<HTMLElement>('.resource-dialog input[type=radio]');
		target?.focus();
		target?.scrollIntoView({ block: 'nearest' });
	}

	async function selectStep(id: string) {
		if (busy || result) return;
		if (id === 'domain-review') { await stageAndReview(); return; }
		if (id === 'domain-certificate' && !validateGroups(['domain'])) { await focusFirstError(); return; }
		step = id;
	}

	function describeFailure(e: unknown, fallback: string) {
		if (e instanceof ApiError && e.code === 'ERR_VERSION_CONFLICT') { conflict = true; return rv2('conflict'); }
		const base = errorMessage(e, fallback);
		return e instanceof ApiError && e.operationId ? `${base} (${rv2('operation')} ${e.operationId})` : base;
	}

	async function stageAndReview() {
		if (busy || !editable) return;
		if (!validateGroups(['domain', 'certificate'])) { await focusFirstError(); return; }
		busy = true; error = ''; conflict = false;
		try {
			const preview = await putDomain(form.name, toInput($state.snapshot(form)), version(), { dryRun: true, signal: controller.signal });
			if (disposed) return;
			step = 'domain-review';
			review = preview.preview ?? null; applyKey = newIdempotencyKey();
		} catch (e) {
			if (disposed) return;
			if (e instanceof ApiError && (e.status === 422 || e.status === 400)) {
				// core-v2 returns one sentence; route it to the field it is about.
				const message = errorMessage(e, v2('stageFailed'));
				fields = /resolver|certificate|wildcard|dns-01/i.test(message) ? { reference: message } : { main: message };
				await focusFirstError();
			}
			error = describeFailure(e, rv2('stageFailed'));
		} finally {
			if (!disposed) busy = false;
		}
	}

	async function refreshVersion() {
		busy = true;
		try {
			existing = await listDomains(controller.signal);
			conflict = false; error = '';
		} catch (e) {
			error = describeFailure(e, rv2('reviewFailed'));
		} finally {
			if (!disposed) busy = false;
		}
	}

	async function apply() {
		if (!review || busy || serverErrors.length || mountsMissing) return;
		busy = true; error = '';
		try {
			// Same body + key on every retry of an uncertain attempt.
			const saved = await putDomain(form.name, toInput($state.snapshot(form)), version(), { idempotencyKey: applyKey, signal: controller.signal });
			if (disposed) return;
			result = operationOf(saved, t('writeV2.noChange')); manualRestart = needsManualRestart(saved); uncertain = false; staged = saved.resource ?? null;
			navigationGuard.setDirty(false);
			onchanged?.();
			// Applied: back to the table; the outcome is announced as a flash.
			if (announceApplied(result, manualRestart, form.name)) { navigate(parent); return; }
		} catch (e) {
			if (disposed) return;
			if (e instanceof NetworkError) {
				uncertain = true;
				error = rv2('applyUncertain');
			} else {
				if (e instanceof ApiError && [404, 409, 410, 412, 422, 428].includes(e.status)) { review = null; applyKey = ''; uncertain = false; step = 'domain-certificate'; }
				error = describeFailure(e, rv2('applyFailed'));
			}
		} finally {
			if (!disposed) busy = false;
		}
	}

	function close() {
		if (busy) return;
		navigationGuard.requestNavigation(() => {
			if (staged || result) onchanged?.();
			navigate(parent);
		});
	}

</script>

<ResourceDialog title={u(creating ? 'add' : 'edit')} closeLabel={t('common.close')} onclose={close} {suspended}>
	{#snippet icon()}<Globe size={32} aria-hidden="true" />{/snippet}
	{#snippet navigation()}
		{#if !loading && !loadError}<Tabs {tabs} selected={step} onselect={selectStep} label={u('tabs')} />{/if}
	{/snippet}

	{#if loading}
		<p role="status">{c('loading')}</p>
	{:else if loadError}
		<div class="stack">
			<Notice tone="error" message={loadError} />
			<div><Button onclick={load}>{t('common.retry')}</Button></div>
		</div>
	{:else}
		<div class="stack">
			{#if !editable}<Notice tone="warning" message={v2('notEditable')} />{/if}
			{#if error}
				<Notice tone="error" message={error}>
					{#if conflict}<Button size="sm" onclick={refreshVersion} disabled={busy}>{rv2('refreshDraft')}</Button>{/if}
				</Notice>
			{/if}
			{#if errorCount && step !== 'domain-review'}
				<div class="validation-banner" role="alert">
					<CircleAlert size={16} aria-hidden="true" />
					<span>{rv2(errorCount === 1 ? 'fixFieldsOne' : 'fixFields', { count: errorCount })}</span>
					<button type="button" onclick={focusFirstError}>{rv2('goToField')}</button>
				</div>
			{/if}

			<div id="panel-domain-identity" role="tabpanel" aria-labelledby="tab-domain-identity" hidden={step !== 'domain-identity'} class="stack">
				<Field id="domain-main" label={u('domainName')} required help={u('domainHelp')} error={fields.main}>
					{#snippet children(f)}
						<input id={f.id} value={form.main} oninput={(e) => setMain(e.currentTarget.value)} disabled={locked || !editable} aria-invalid={f.invalid || undefined} aria-describedby={f.describedBy} autocomplete="off" spellcheck="false" placeholder="example.com" />
					{/snippet}
				</Field>
				<Field id="domain-sans" label={u('alternativeNames')} help={u('sansHelp')} error={fields.sans}>
					{#snippet children(f)}
						<textarea id={f.id} value={form.sans} oninput={(e) => { form.sans = e.currentTarget.value; invalidate(); }} disabled={locked || !editable} aria-invalid={f.invalid || undefined} aria-describedby={f.describedBy} rows="3" placeholder="www.example.com" spellcheck="false"></textarea>
					{/snippet}
				</Field>
			</div>

			<div id="panel-domain-certificate" role="tabpanel" aria-labelledby="tab-domain-certificate" hidden={step !== 'domain-certificate'} class="stack">
				<div id="domain-method">
					<RadioCards legend={u('method')} required value={form.method} name="domain-method-group" disabled={locked || !editable} error={fields.method} onchange={chooseMethod}
						options={[{ value: 'resolver', label: u('resolverMethod'), description: u('resolverHint') }, { value: 'files', label: u('fileMethod'), description: u('fileHint') }]} />
				</div>
				{#if choicesError}<Notice tone="warning" message={choicesError}><Button size="sm" onclick={loadChoices} disabled={choicesLoading}>{t('common.retry')}</Button></Notice>{/if}
				{#if choicesLoading}
					<p role="status" class="muted">{u('loadingChoices')}</p>
				{:else if form.method}
					<Field id="domain-reference" label={u(form.method === 'resolver' ? 'resolver' : 'certificate')} required help={form.method === 'resolver' ? u('resolverHelp') : c('suitableCertificates')} error={fields.reference}>
						{#snippet children(f)}
							<SearchableSelect id={f.id} value={form.reference} {options} onchange={(v) => { form.reference = v; invalidate(); }} placeholder={u('selectExisting')} searchPlaceholder={u('searchChoices')} emptyMessage={u('noOptions')} disabled={locked || !editable || !options.length} ariaInvalid={f.invalid} ariaDescribedby={f.describedBy} />
						{/snippet}
					</Field>
					{#if !options.length}
						<Notice message={u(form.method === 'resolver' ? 'noResolvers' : 'noCertificates')} />
						<div><Button onclick={() => navigate(form.method === 'resolver' ? '/resolvers' : '/certificates/uploaded')}>{c(form.method === 'resolver' ? 'openResolvers' : 'openUploaded')}</Button></div>
					{/if}
				{/if}
			</div>

			<div id="panel-domain-review" role="tabpanel" aria-labelledby="tab-domain-review" hidden={step !== 'domain-review'} class="stack">
				{#if result}
					<Notice tone={operationTone(result.status)} title={t('operationV2.status.' + result.status)} message={result.summary}>
						{#if result.id}<span class="muted">{t('operationV2.id', { id: result.id })}</span>{/if}
					</Notice>
					{#if manualRestart}<Notice tone="warning" message={t('writeV2.manualRestart')} />{/if}
				{:else if review}
					<SummaryList>
						<div><dt>{u('domainName')}</dt><dd>{staged?.config.main ?? form.main}</dd></div>
						<div><dt>{c('additionalNames')}</dt><dd>{(staged?.config.sans ?? parseNames(form.sans)).join(', ') || '—'}</dd></div>
						<div><dt>{u('method')}</dt><dd>{u(form.method === 'resolver' ? 'resolverMethod' : 'fileMethod')}</dd></div>
						<div><dt>{c('reference')}</dt><dd>{form.reference}</dd></div>
						{#if staged?.updated_at}<div><dt>{u('updated')}</dt><dd><RelativeTime value={staged.updated_at} /></dd></div>{/if}
						<div><dt>{rv2('pendingTotal')}</dt><dd>{review.changes?.length ?? 0}</dd></div>
					</SummaryList>
					{#if serverErrors.length}
						<Notice tone="error" title={rv2('serverErrors')}>
							<ul class="reasons">{#each serverErrors as ve}<li>{ve.item_id}{ve.field ? ' · ' + ve.field : ''}: {ve.message}</li>{/each}</ul>
						</Notice>
					{/if}
					{#if review.requires_restart}
						<Notice tone="warning" message={t('resolverUI.lifecycleHelp')}>
							{#if review.restart_reasons?.length}<ul class="reasons">{#each review.restart_reasons as reason}<li>{reason}</li>{/each}</ul>{/if}
						</Notice>
					{/if}
					{#if mountsMissing}<Notice tone="warning" title={rv2('mounts.blockedTitle')} message={rv2('mounts.blocked')} />{/if}
					{#if review.domain_diff}<CodePreview label={v2('domainFileDiff')} code={review.domain_diff} language="diff" />{/if}
					{#if review.static_diff}<CodePreview label={rv2('diff.static')} code={review.static_diff} language="diff" />{/if}
					{#if review.dynamic_diff}<CodePreview label={rv2('diff.dynamic')} code={review.dynamic_diff} language="diff" />{/if}
				{:else if reviewError}
					<Notice tone="error" message={reviewError}><Button size="sm" onclick={stageAndReview}>{t('common.retry')}</Button></Notice>
				{:else}
					<p role="status">{rv2('staging')}</p>
				{/if}
			</div>
		</div>
	{/if}

	{#snippet footer()}
		<Button onclick={close} disabled={busy}>{result ? t('common.close') : u('cancel')}</Button>
		{#if !loading && !loadError && !result && editable}
			{#if step === 'domain-review'}
				<Button onclick={() => (step = 'domain-certificate')} disabled={busy}>{u('back')}</Button>
				{#if review}
					<Button variant="primary" onclick={apply} pending={busy} disabled={busy || serverErrors.length > 0 || mountsMissing}>{u(busy ? 'applying' : uncertain ? 'retryApply' : 'apply')}</Button>
				{/if}
			{:else if step === 'domain-certificate'}
				<Button onclick={() => (step = 'domain-identity')} disabled={busy}>{u('back')}</Button>
				<Button variant="primary" onclick={stageAndReview} pending={busy} disabled={busy || choicesLoading}>{rv2('stageReview')}</Button>
			{:else}
				<Button variant="primary" onclick={() => selectStep('domain-certificate')} disabled={busy}>{u('continue')}</Button>
			{/if}
		{/if}
	{/snippet}
</ResourceDialog>

<style>
	.stack, .stack:not([hidden]) { display: flex; flex-direction: column; gap: 24px; }
	.stack[hidden] { display: none; }
	.muted { margin: 0; color: var(--text-muted); font-size: 14px; line-height: 21px; }
	.reasons { margin: 8px 0 0; padding-left: 20px; font-size: 14px; line-height: 22px; }
	textarea { resize: vertical; min-height: 88px; }
	.validation-banner { display: flex; align-items: center; gap: 8px; padding: 10px 14px; border: 1px solid color-mix(in srgb, var(--danger-text) 30%, transparent); border-radius: var(--radius-card); background: var(--danger-bg); color: var(--danger-text); font-size: 13px; line-height: 18px; }
	.validation-banner span { flex: 1; }
	.validation-banner button { border: 0; background: transparent; color: inherit; font: inherit; font-weight: 600; cursor: pointer; padding: 2px 4px; }
	.validation-banner button:hover { text-decoration: underline; }
</style>

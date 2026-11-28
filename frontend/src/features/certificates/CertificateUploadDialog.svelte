<script lang="ts">
	/**
	 * Upload (create) or replace an imported certificate against core-v2.
	 *
	 *   open       → GET /certificates (names, versions, purpose)
	 *   Files tab  → name, purpose, certificate chain, private key (client-side checks)
	 *   Review tab → PUT /certificates/{name}?dry_run=true + If-Match parses the PEM, checks the key pair
	 *                and returns the preview; nothing is saved
	 *   Apply      → the same PUT without dry_run, with one Idempotency-Key per attempt
	 */
	import { onMount, onDestroy, tick } from 'svelte';
	import { ShieldCheck, CircleAlert } from '@lucide/svelte';
	import ResourceDialog from '../../shared/ui/ResourceDialog.svelte';
	import Field from '../../shared/ui/Field.svelte';
	import Tabs from '../../shared/ui/Tabs.svelte';
	import Button from '../../shared/ui/Button.svelte';
	import Notice from '../../shared/ui/Notice.svelte';
	import SummaryList from '../../shared/ui/SummaryList.svelte';
	import CodePreview from '../../shared/ui/CodePreview.svelte';
	import StatusBadge from '../../shared/ui/StatusBadge.svelte';
	import RelativeTime from '../../shared/ui/RelativeTime.svelte';
	import SearchableSelect from '../../shared/ui/SearchableSelect.svelte';
	import PemInput from './PemInput.svelte';
	import { navigationGuard } from '../../lib/stores/navigationGuard';
	import { needsManualRestart, newIdempotencyKey, operationOf, announceApplied } from '../../core/mutations';
	import { operationTone } from '../changes/models';
	import type { Operation, OperationPreview } from '../../core/types';
	import { listCertificates, putCertificate } from './api';
	import { certificateErrors, toInput, type CertificateField } from './validation';
	import { newForm, type CertificateForm, type CertificateResource } from './models';
	import { ApiError, NetworkError } from '../../shared/api/client';
	import { errorMessage } from '../../shared/api/errorMessage';
	import { t } from '../../shared/localization/i18n';

	let {
		mode = 'create',
		name = '',
		navigate,
		onchanged,
	}: { mode?: string; name?: string; navigate: (p: string) => void; onchanged?: () => void } = $props();

	const c = (key: string, args?: Record<string, string | number>) => t('certificateFlow.' + key, args);
	const old = (key: string) => t('certificateUI.' + key);
	const v2 = (key: string, args?: Record<string, string | number>) => t('certV2.' + key, args);
	const rv2 = (key: string, args?: Record<string, string | number>) => t('resolverV2.' + key, args);

	const replacing = $derived(mode === 'replace');
	const parent = '/certificates/uploaded';

	let form = $state<CertificateForm>(newForm());
	let existing = $state<CertificateResource[]>([]);
	/** Version of the stored certificate being replaced; 0 (create) when the name is new. */
	const version = () => existing.find((i) => i.name === form.name)?.version ?? 0;
	let loading = $state(true);
	let loadError = $state('');
	let busy = $state(false);
	let suspended = $state(false);
	let error = $state('');
	let step = $state('upload-files');
	let baseline = $state('');
	let fields = $state<Partial<Record<CertificateField, string>>>({});
	let validated = $state(false);

	let conflict = $state(false);
	let staged = $state<CertificateResource | null>(null);
	let review = $state<OperationPreview | null>(null);
	let reviewError = $state('');
	let applyKey = $state('');
	let uncertain = $state(false);
	let result = $state<Operation | null>(null);
	let manualRestart = $state(false);

	let disposed = false;
	const controller = new AbortController();

	const dirty = $derived(!!baseline && JSON.stringify($state.snapshot(form)) !== baseline && !result);
	const locked = $derived(busy || !!result);
	const serverErrors = $derived(review?.validation_errors ?? []);
	const mounts = $derived(review?.env_vars_to_mount ?? []);
	const mountsMissing = $derived(mounts.some((m) => !m.already_set));
	const errorCount = $derived(Object.keys(fields).length);
	const tabs = $derived([
		{ id: 'upload-files', label: c('files'), errorCount, errorLabel: c('fixFiles') },
		{ id: 'upload-review', label: c('review') },
	]);
	const messages = $derived({
		invalidName: old('invalidName'),
		nameTaken: v2('nameTaken'),
		invalidChain: old('invalidChain'),
		invalidKey: old('invalidKey'),
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
	onDestroy(() => {
		disposed = true;
		controller.abort();
		// Never keep key material around after the dialog closes.
		form.keyPem = '';
		form.certPem = '';
	});

	async function load() {
		loading = true;
		loadError = '';
		try {
			const [all] = await Promise.all([listCertificates(controller.signal)]);
			if (disposed) return;
			existing = all;
			if (replacing) {
				const current = all.find((i) => i.name === name);
				if (!current) throw new Error(c('notObserved'));
				form = { ...newForm(), name: current.name, purpose: current.config.purpose };
			}
			baseline = JSON.stringify($state.snapshot(form));
		} catch (e) {
			if (!disposed) loadError = errorMessage(e, c('loadFailed'));
		} finally {
			if (!disposed) loading = false;
		}
	}

	function collect() {
		return certificateErrors(form, { creating: !replacing, existingNames: existing.map((i) => i.name) }, messages);
	}
	function invalidate() {
		// Any edit makes the staged review stale; it is staged again on entering Review.
		review = null;
		staged = null;
		reviewError = '';
		applyKey = '';
		uncertain = false;
		error = '';
		conflict = false;
		if (validated) fields = collect();
	}
	function setPurpose(value: string) {
		form.purpose = value as CertificateForm['purpose'];
		if (value === 'ca') form.keyPem = '';
		invalidate();
	}

	const controlId = (field: CertificateField) =>
		({ name: 'upload-name', certPem: 'upload-chain', keyPem: 'upload-key' })[field];
	async function focusFirstError() {
		const key = Object.keys(fields)[0] as CertificateField | undefined;
		if (!key) return;
		step = 'upload-files';
		await tick();
		const target = document.getElementById(controlId(key));
		target?.focus();
		target?.scrollIntoView({ block: 'nearest' });
	}

	function describeFailure(e: unknown, fallback: string) {
		if (e instanceof ApiError && e.code === 'ERR_VERSION_CONFLICT') {
			conflict = true;
			return rv2('conflict');
		}
		const base = errorMessage(e, fallback);
		return e instanceof ApiError && e.operationId ? `${base} (${rv2('operation')} ${e.operationId})` : base;
	}

	async function selectStep(id: string) {
		if (busy || result) return;
		if (id === 'upload-review') {
			await stageAndReview();
			return;
		}
		step = id;
	}

	async function stageAndReview() {
		if (busy) return;
		validated = true;
		fields = collect();
		if (errorCount) {
			await focusFirstError();
			return;
		}
		busy = true;
		error = '';
		conflict = false;
		try {
			const preview = await putCertificate(form.name, toInput($state.snapshot(form)), version(), {
				dryRun: true,
				signal: controller.signal,
			});
			if (disposed) return;
			step = 'upload-review';
			review = preview.preview ?? null;
			applyKey = newIdempotencyKey();
		} catch (e) {
			if (disposed) return;
			if (e instanceof ApiError && (e.status === 422 || e.status === 400)) {
				// core-v2 returns one message for unparsable PEM or a mismatched key pair.
				const message = errorMessage(e, c('checkFailed'));
				fields = form.purpose === 'server' && /key/i.test(message) ? { keyPem: message } : { certPem: message };
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
			existing = await listCertificates(controller.signal);
			conflict = false;
			error = '';
		} catch (e) {
			error = describeFailure(e, rv2('reviewFailed'));
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
			const saved = await putCertificate(form.name, toInput($state.snapshot(form)), version(), {
				idempotencyKey: applyKey,
				signal: controller.signal,
			});
			if (disposed) return;
			result = operationOf(saved, t('writeV2.noChange'));
			manualRestart = needsManualRestart(saved);
			uncertain = false;
			staged = saved.resource ?? null;
			form.keyPem = '';
			navigationGuard.setDirty(false);
			onchanged?.();
			// Applied: back to the table; the outcome is announced as a flash.
			if (announceApplied(result, manualRestart, form.name)) {
				navigate(parent);
				return;
			}
		} catch (e) {
			if (disposed) return;
			if (e instanceof NetworkError) {
				uncertain = true;
				error = rv2('applyUncertain');
			} else {
				if (e instanceof ApiError && [404, 409, 410, 412, 422, 428].includes(e.status)) {
					review = null;
					applyKey = '';
					uncertain = false;
					step = 'upload-files';
				}
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

<ResourceDialog
	title={c(replacing ? 'replaceTitle' : 'upload')}
	description={c('uploadHelp')}
	closeLabel={t('common.close')}
	onclose={close}
	{suspended}
>
	{#snippet icon()}<ShieldCheck size={32} aria-hidden="true" />{/snippet}
	{#snippet navigation()}
		{#if !loading && !loadError}<Tabs
				{tabs}
				selected={step}
				onselect={selectStep}
				label={c(replacing ? 'replaceTitle' : 'upload')}
			/>{/if}
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
			{#if error}
				<Notice tone="error" message={error}>
					{#if conflict}<Button size="sm" onclick={refreshVersion} disabled={busy}>{rv2('refreshDraft')}</Button>{/if}
				</Notice>
			{/if}
			{#if errorCount && step === 'upload-files'}
				<div class="validation-banner" role="alert">
					<CircleAlert size={16} aria-hidden="true" />
					<span>{c('fixFiles')}</span>
					<button type="button" onclick={focusFirstError}>{rv2('goToField')}</button>
				</div>
			{/if}

			<div
				id="panel-upload-files"
				role="tabpanel"
				aria-labelledby="tab-upload-files"
				hidden={step !== 'upload-files'}
				class="stack"
			>
				<Field
					id="upload-name"
					label={c('certificateName')}
					required
					help={c(replacing ? 'immutableName' : 'nameHelp')}
					error={fields.name}
				>
					{#snippet children(f)}
						<input
							id={f.id}
							bind:value={form.name}
							placeholder="shop-certificate"
							readonly={replacing}
							disabled={locked}
							aria-invalid={f.invalid || undefined}
							aria-describedby={f.describedBy}
							oninput={invalidate}
							autocomplete="off"
							spellcheck="false"
						/>
					{/snippet}
				</Field>
				{#if !replacing}
					<Field
						id="upload-purpose"
						label={v2('purpose')}
						help={v2(form.purpose === 'ca' ? 'purposeCaHelp' : 'purposeServerHelp')}
					>
						{#snippet children(f)}
							<SearchableSelect
								id={f.id}
								value={form.purpose}
								options={[
									{ value: 'server', label: v2('purposeServer') },
									{ value: 'ca', label: v2('purposeCa') },
								]}
								onchange={setPurpose}
								disabled={locked}
							/>
						{/snippet}
					</Field>
				{/if}
				<Field
					id="upload-chain"
					label={c('certificateFile')}
					required
					help={c('certificateFileHelp')}
					error={fields.certPem}
				>
					{#snippet children(f)}
						<PemInput
							id={f.id}
							bind:value={form.certPem}
							disabled={locked}
							invalid={f.invalid}
							describedBy={f.describedBy}
							onchange={invalidate}
							onerror={(message) => (fields = { ...fields, certPem: message })}
						/>
					{/snippet}
				</Field>
				{#if form.purpose === 'server'}
					<Field id="upload-key" label={c('keyFile')} required help={c('keyFileHelp')} error={fields.keyPem}>
						{#snippet children(f)}
							<PemInput
								id={f.id}
								bind:value={form.keyPem}
								secret
								disabled={locked}
								invalid={f.invalid}
								describedBy={f.describedBy}
								onchange={invalidate}
								onerror={(message) => (fields = { ...fields, keyPem: message })}
							/>
						{/snippet}
					</Field>
				{:else}
					<Notice message={c('trustFileHelp')} />
				{/if}
			</div>

			<div
				id="panel-upload-review"
				role="tabpanel"
				aria-labelledby="tab-upload-review"
				hidden={step !== 'upload-review'}
				class="stack"
			>
				{#if result}
					<Notice
						tone={operationTone(result.status)}
						title={t('operationV2.status.' + result.status)}
						message={result.summary}
					>
						{#if result.id}<span class="muted">{t('operationV2.id', { id: result.id })}</span>{/if}
					</Notice>
					{#if manualRestart}<Notice tone="warning" message={t('writeV2.manualRestart')} />{/if}
				{:else if review}
					<SummaryList>
						<div>
							<dt>{c('certificateName')}</dt>
							<dd>{form.name}</dd>
						</div>
						<div>
							<dt>{c('action')}</dt>
							<dd>{c(replacing ? 'replace' : 'upload')}</dd>
						</div>
						<div>
							<dt>{v2('purpose')}</dt>
							<dd>{v2(form.purpose === 'ca' ? 'purposeCa' : 'purposeServer')}</dd>
						</div>
						{#if staged}
							<div>
								<dt>{c('coveredNames')}</dt>
								<dd>{(staged.config.domains ?? []).join(', ') || '—'}</dd>
							</div>
							<div>
								<dt>{c('issuer')}</dt>
								<dd>{staged.config.issuer || '—'}</dd>
							</div>
							<div>
								<dt>{c('expires')}</dt>
								<dd><RelativeTime value={staged.config.not_after} /></dd>
							</div>
							<div>
								<dt>{c('validity')}</dt>
								<dd>
									<StatusBadge
										size="sm"
										tone={staged.config.status === 'valid' ? 'success' : 'warning'}
										text={old(staged.config.status)}
									/>
								</dd>
							</div>
						{/if}
						<div>
							<dt>{rv2('pendingTotal')}</dt>
							<dd>{review.changes?.length ?? 0}</dd>
						</div>
					</SummaryList>
					<Notice tone="info" message={c('pairChecked')} />
					{#if serverErrors.length}
						<Notice tone="error" title={rv2('serverErrors')}>
							<ul class="reasons">
								{#each serverErrors as ve}<li>{ve.item_id}{ve.field ? ' · ' + ve.field : ''}: {ve.message}</li>{/each}
							</ul>
						</Notice>
					{/if}
					{#if review.requires_restart}
						<Notice tone="warning" message={t('resolverUI.lifecycleHelp')}>
							{#if review.restart_reasons?.length}<ul class="reasons">
									{#each review.restart_reasons as reason}<li>{reason}</li>{/each}
								</ul>{/if}
						</Notice>
					{/if}
					{#if mountsMissing}
						<Notice tone="warning" title={rv2('mounts.blockedTitle')} message={rv2('mounts.blocked')} />
					{/if}
					{#if review.static_diff}<CodePreview
							label={rv2('diff.static')}
							code={review.static_diff}
							language="diff"
						/>{/if}
					{#if review.dynamic_diff}<CodePreview
							label={rv2('diff.dynamic')}
							code={review.dynamic_diff}
							language="diff"
						/>{/if}
					{#if !review.static_diff && !review.dynamic_diff}<p class="muted">{rv2('diff.none')}</p>{/if}
				{:else if reviewError}
					<Notice tone="error" message={reviewError}>
						<Button size="sm" onclick={stageAndReview}>{t('common.retry')}</Button>
					</Notice>
				{:else}
					<p role="status">{rv2('staging')}</p>
				{/if}
			</div>
		</div>
	{/if}

	{#snippet footer()}
		<Button onclick={close} disabled={busy}>{result ? t('common.close') : t('resolverUI.cancel')}</Button>
		{#if !loading && !loadError && !result}
			{#if step === 'upload-review'}
				<Button onclick={() => (step = 'upload-files')} disabled={busy}>{old('back')}</Button>
				{#if review}
					<Button
						variant="primary"
						onclick={apply}
						pending={busy}
						disabled={busy || serverErrors.length > 0 || mountsMissing}
					>
						{t('resolverUI.' + (busy ? 'applying' : uncertain ? 'retryApply' : 'apply'))}
					</Button>
				{/if}
			{:else}
				<Button variant="primary" onclick={stageAndReview} pending={busy} disabled={busy}>{rv2('stageReview')}</Button>
			{/if}
		{/if}
	{/snippet}
</ResourceDialog>

<style>
	.stack,
	.stack:not([hidden]) {
		display: flex;
		flex-direction: column;
		gap: 24px;
	}
	.stack[hidden] {
		display: none;
	}
	.muted {
		margin: 0;
		color: var(--text-muted);
		font-size: 14px;
		line-height: 21px;
	}
	.reasons {
		margin: 8px 0 0;
		padding-left: 20px;
		font-size: 14px;
		line-height: 22px;
	}
	.validation-banner {
		display: flex;
		align-items: center;
		gap: 8px;
		padding: 10px 14px;
		border: 1px solid color-mix(in srgb, var(--danger-text) 30%, transparent);
		border-radius: var(--radius-card);
		background: var(--danger-bg);
		color: var(--danger-text);
		font-size: 13px;
		line-height: 18px;
	}
	.validation-banner span {
		flex: 1;
	}
	.validation-banner button {
		border: 0;
		background: transparent;
		color: inherit;
		font: inherit;
		font-weight: 600;
		cursor: pointer;
		padding: 2px 4px;
	}
	.validation-banner button:hover {
		text-decoration: underline;
	}
</style>

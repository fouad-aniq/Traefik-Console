<script lang="ts">
	/**
	 * Create / edit / take over / view an entrypoint (traefik.yml `entryPoints.<name>`) against core-v2.
	 *
	 * The type chosen first decides everything else: which protocol it listens on, which tabs exist
	 * and which options are sent. A UDP entrypoint is a listener and a session timeout; a TCP service
	 * adds PROXY protocol and timeouts; the web types add redirect, TLS, middlewares, HTTP/3,
	 * forwarded headers and request limits.
	 *
	 * Flow (create/edit):
	 *   tabs    → Listener · HTTP & TLS · Client addresses · Limits (client-side validation per tab)
	 *   Review  → PUT /entrypoints/{name}?dry_run=true returns the traefik.yml diff and warnings
	 *   Apply   → the same PUT without dry_run, one Idempotency-Key per attempt. Traefik restarts.
	 */
	import { onMount, onDestroy, tick } from 'svelte';
	import { LogIn, CircleAlert, X, Plus } from '@lucide/svelte';
	import { stringify } from 'yaml';
	import ResourceDialog from '../../shared/ui/ResourceDialog.svelte';
	import Field from '../../shared/ui/Field.svelte';
	import Checkbox from '../../shared/ui/Checkbox.svelte';
	import Tabs from '../../shared/ui/Tabs.svelte';
	import Button from '../../shared/ui/Button.svelte';
	import Notice from '../../shared/ui/Notice.svelte';
	import SummaryList from '../../shared/ui/SummaryList.svelte';
	import CodePreview from '../../shared/ui/CodePreview.svelte';
	import SearchableSelect from '../../shared/ui/SearchableSelect.svelte';
	import SearchableMultiSelect from '../../shared/ui/SearchableMultiSelect.svelte';
	import ChipInput from './ChipInput.svelte';
	import { navigationGuard } from '../../lib/stores/navigationGuard';
	import { needsManualRestart, newIdempotencyKey, operationOf, announceApplied } from '../../core/mutations';
	import { operationTone } from '../changes/models';
	import type { Operation, OperationPreview } from '../../core/types';
	import { listEntryPointResources, newForm, withKind, formFrom, putEntryPoint, toInput } from './api';
	import { listResolvers } from '../resolvers/api';
	import { listMiddlewares, listChains } from '../middlewares/api';
	import {
		entryPointErrors,
		portConflict,
		validDomainName,
		validHeaderName,
		validIPOrCIDR,
		validMiddlewareReference,
		validPort,
		SERVER_FIELDS,
		type ValidationGroup,
	} from './validation';
	import {
		ENCODED_CHARACTERS,
		KINDS,
		displayAddress,
		isWeb,
		kindOf,
		protocolsOf,
		usageCount,
		type EncodedCharacter,
		type EntryPointForm,
		type EntryPointResource,
		type Kind,
		type TrustMode,
	} from './models';
	import { ApiError, NetworkError } from '../../shared/api/client';
	import { errorMessage } from '../../shared/api/errorMessage';
	import { t } from '../../shared/localization/i18n';

	let {
		mode = 'view',
		name = '',
		navigate,
		onchanged,
	}: {
		mode?: string;
		name?: string;
		navigate: (p: string) => void;
		onchanged?: () => void;
	} = $props();

	const c = (key: string, args?: Record<string, string | number>) => t('entryPointUI.' + key, args);

	// ---- state ------------------------------------------------------------
	let f = $state<EntryPointForm>(newForm());
	let current = $state<EntryPointResource | null>(null);
	let all = $state<EntryPointResource[]>([]);
	let resolvers = $state<string[]>([]);
	let resolversKnown = $state(false);
	let middlewareNames = $state<string[]>([]);

	let loading = $state(true);
	let loadError = $state('');
	let busy = $state(false);
	let suspended = $state(false);
	let error = $state('');
	let step = $state('entrypoint-listener');
	let baseline = $state('');
	let fields = $state<Record<string, string>>({});
	let chipErrors = $state<Record<string, string>>({});
	let validatedGroups = $state<ValidationGroup[]>([]);
	let customMiddleware = $state('');
	// The redirect goes to a bare port instead of another entrypoint.
	let redirectUsePort = $state(false);

	let conflict = $state(false);
	let review = $state<OperationPreview | null>(null);
	let applyKey = $state('');
	let uncertain = $state(false); // apply response lost: keep body+key for an exact retry
	let result = $state<Operation | null>(null);
	let manualRestart = $state(false);
	let applied = $state(false);

	let disposed = false;
	const controller = new AbortController();
	// Chip inputs hold a typed value until it is committed; committing all of them precedes validation.
	const chips: Record<string, { commit: () => boolean } | undefined> = $state({});

	// ---- derived ----------------------------------------------------------
	const creating = $derived(mode === 'create');
	// An entrypoint declared in traefik.yml is edited by taking it over, when the console can hold all of it.
	const adopting = $derived(mode === 'edit' && current?.origin === 'external' && !!current.config.adoptable);
	const editing = $derived(creating || (mode === 'edit' && !!current && (!current.read_only || adopting)));
	const web = $derived(isWeb(f.kind));
	const tcp = $derived(f.protocol === 'tcp');
	const others = $derived(all.filter((e) => e.name !== (creating ? '' : name)));
	const dirty = $derived(editing && !!baseline && JSON.stringify($state.snapshot(f)) !== baseline && !result);
	const locked = $derived(busy || !!result);
	// The type decides which tabs exist: nothing but the listener for UDP, no HTTP tab for a TCP service.
	const tabIds = $derived<string[]>(
		web
			? ['listener', 'http', 'trust', 'advanced', 'review']
			: tcp
				? ['listener', 'trust', 'advanced', 'review']
				: ['listener', 'review'],
	);
	const tabs = $derived(
		tabIds.map((id) => {
			const label = c('tab.' + id);
			const count = Object.keys(fields).filter((k) => k.startsWith(id + '.')).length;
			return { id: 'entrypoint-' + id, label, errorCount: count, errorLabel: c('tabErrors', { tab: label, count }) };
		}),
	);
	const stepName = $derived(step.replace('entrypoint-', ''));
	const tabErrors = $derived(Object.entries(fields).filter(([key]) => key.startsWith(stepName + '.')));
	const serverErrors = $derived(review?.validation_errors ?? []);
	const lastEditTab = $derived(tabIds[tabIds.length - 2]);
	// An existing entrypoint keeps its protocol, so only the types of that protocol are offered.
	const kindOptions = $derived(
		KINDS.filter((kind) => creating || protocolsOf(kind).includes(f.protocol)).map((value) => ({
			value,
			label: c('kind.' + value),
			description: c('kind.' + value + 'Help'),
		})),
	);
	const protocolOptions = $derived(protocolsOf(f.kind).map((value) => ({ value, label: value.toUpperCase() })));
	const redirectTargets = $derived(others.filter((e) => (e.config.protocol ?? 'tcp') === 'tcp'));
	const conflictNow = $derived(portConflict({ form: f, creating, others, resolvers, resolversKnown }));
	const middlewareOptions = $derived(
		[...new Set([...middlewareNames.map((n) => n + '@file'), ...f.middlewares])].map((value) => ({
			value,
			label: value,
		})),
	);
	const portLabel = $derived(validPort(f.port) ? f.port.trim() : '…');
	const usage = $derived(current?.config.usage);
	const renderedYaml = $derived(
		current?.config.rendered_node ? stringify({ [current.name]: current.config.rendered_node }) : '',
	);
	const limitsSet = $derived(
		!!(
			f.maxHeaderBytes ||
			f.deniedCharacters.length ||
			f.encodeQuerySemicolons ||
			f.disableSanitizePath ||
			f.aliasHeadersStrategy !== 'keep'
		),
	);
	const http2Set = $derived(!!(f.maxConcurrentStreams || f.maxDecoderHeaderTableSize || f.maxEncoderHeaderTableSize));

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
	});

	async function load() {
		loading = true;
		loadError = '';
		const signal = controller.signal;
		try {
			all = await listEntryPointResources(signal);
			if (disposed) return;
			if (creating) {
				// A first entrypoint is most often the web one: start there, with its usual name and port.
				f = withKind(newForm(), 'http', all, true);
			} else {
				const found = all.find((e) => e.name === name);
				if (!found) throw new Error(c('notFound'));
				current = found;
				f = formFrom(found);
				redirectUsePort = f.redirect && !f.redirectTarget;
			}
			baseline = JSON.stringify($state.snapshot(f));
		} catch (e) {
			if (!disposed) loadError = errorMessage(e, c('loadFailed'));
		} finally {
			if (!disposed) loading = false;
		}
		// Pickers only: a failure never blocks the dialog, the server validates the references.
		void listResolvers(signal)
			.then((v) => {
				if (!disposed) {
					resolvers = v.map((r) => r.name);
					resolversKnown = true;
				}
			})
			.catch(() => {});
		void Promise.all([listMiddlewares(signal), listChains(signal)])
			.then(([mws, chains]) => {
				if (disposed) return;
				middlewareNames = [
					...mws.filter((m) => m.origin === 'managed' && m.config.protocol !== 'tcp').map((m) => m.name),
					...chains.filter((ch) => ch.origin === 'managed').map((ch) => ch.name),
				].sort();
			})
			.catch(() => {});
	}

	// ---- editing ----------------------------------------------------------
	const context = () => ({ form: f, creating, others, resolvers, resolversKnown });
	const collect = (group: ValidationGroup) =>
		entryPointErrors(group, context(), (key, args) => c('errors.' + key, args));

	function invalidate() {
		// Any edit makes the review stale; it is requested again on entering Review.
		review = null;
		applyKey = '';
		uncertain = false;
		error = '';
		conflict = false;
		fields = Object.assign({}, ...validatedGroups.map(collect));
	}
	function set<K extends keyof EntryPointForm>(key: K, value: EntryPointForm[K]) {
		f[key] = value;
		invalidate();
	}
	function changeKind(value: string) {
		if (value === f.kind) return;
		f = withKind($state.snapshot(f) as EntryPointForm, value as Kind, all, creating);
		redirectUsePort = f.redirect && !f.redirectTarget;
		// Errors of tabs the new type does not have must not linger.
		validatedGroups = validatedGroups.filter((g) => g === 'listener');
		chipErrors = {};
		invalidate();
	}
	function chooseRedirectTarget(value: string) {
		redirectUsePort = value === ':port';
		f.redirectTarget = redirectUsePort ? '' : value;
		if (!redirectUsePort) f.redirectPort = '';
		invalidate();
	}
	function toggleCharacter(character: EncodedCharacter, denied: boolean) {
		f.deniedCharacters = denied
			? [...f.deniedCharacters, character]
			: f.deniedCharacters.filter((x) => x !== character);
		invalidate();
	}
	function addCustomMiddleware() {
		const value = customMiddleware.trim();
		if (!value) return;
		if (!validMiddlewareReference(value)) {
			chipErrors = { ...chipErrors, middlewares: c('errors.middleware') };
			return;
		}
		if (!f.middlewares.includes(value)) f.middlewares = [...f.middlewares, value];
		customMiddleware = '';
		chipErrors = { ...chipErrors, middlewares: '' };
		invalidate();
	}
	function chipError(key: string, message: string) {
		chipErrors = { ...chipErrors, [key]: message };
	}
	/** Commits every half-typed chip value; reports the first one that is not valid. */
	function commitChips(): string | null {
		for (const [key, input] of Object.entries(chips)) {
			if (input && !input.commit()) return key;
		}
		return null;
	}

	function validateGroups(list: string[]) {
		const wanted = list.filter(
			(g): g is ValidationGroup => g === 'listener' || g === 'http' || g === 'trust' || g === 'advanced',
		);
		validatedGroups = [...new Set([...validatedGroups, ...wanted])];
		fields = Object.assign({}, ...validatedGroups.map(collect));
		return !wanted.some((g) => Object.keys(fields).some((k) => k.startsWith(g + '.')));
	}
	const controlId = (key: string) => 'entrypoint-' + key.split('.')[1];
	async function focusField(key: string) {
		const tab = key.split('.')[0];
		step = 'entrypoint-' + (tabIds.includes(tab) ? tab : 'listener');
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
		if (!editing) {
			step = value;
			return;
		}
		const target = value.replace('entrypoint-', '');
		if (target === 'review') {
			await stageAndReview();
			return;
		}
		const from = tabIds.indexOf(stepName);
		const to = tabIds.indexOf(target);
		if (to > from) {
			const pending = commitChips();
			if (pending) {
				await focusField(pending);
				return;
			}
			if (!validateGroups(tabIds.slice(0, to))) {
				await focusFirstError();
				return;
			}
		}
		step = value;
	}

	// ---- direct write protocol --------------------------------------------
	function describeFailure(e: unknown, fallback: string) {
		if (e instanceof ApiError && e.code === 'ERR_VERSION_CONFLICT') {
			conflict = true;
			return c('conflict');
		}
		let base = errorMessage(e, fallback);
		// A refused field may sit in a collapsed or hidden part of the form: always say what was refused.
		if (e instanceof ApiError && e.status === 422) {
			const reasons = [...new Set(Object.values(e.fieldErrors))];
			if (reasons.length) base += ' ' + reasons.join(' ');
		}
		return e instanceof ApiError && e.operationId ? `${base} (${c('operation')} ${e.operationId})` : base;
	}
	/** Server field_errors (`entrypoint:<name>.<path>`) → form keys. */
	function mapFieldErrors(e: ApiError) {
		const mapped: Record<string, string> = {};
		for (const [path, message] of Object.entries(e.fieldErrors)) {
			const target = SERVER_FIELDS[path.replace(/^entrypoint:[^.]*\./, '')];
			if (target) mapped[target] ??= message;
		}
		return mapped;
	}
	const body = () => toInput($state.snapshot(f) as EntryPointForm, adopting);

	async function stageAndReview() {
		if (busy || !editing) return;
		const pending = commitChips();
		if (pending) {
			await focusField(pending);
			return;
		}
		if (!validateGroups(tabIds.filter((i) => i !== 'review'))) {
			await focusFirstError();
			return;
		}
		busy = true;
		error = '';
		conflict = false;
		try {
			const preview = await putEntryPoint(f.name, body(), adopting ? 0 : (current?.version ?? 0), {
				dryRun: true,
				signal: controller.signal,
			});
			if (disposed) return;
			review = preview.preview ?? null;
			applyKey = newIdempotencyKey();
			step = 'entrypoint-review';
		} catch (e) {
			if (disposed) return;
			if (e instanceof ApiError && e.status === 422) {
				const mapped = mapFieldErrors(e);
				if (Object.keys(mapped).length) {
					fields = { ...fields, ...mapped };
					await focusFirstError();
				}
			}
			error = describeFailure(e, c('stageFailed'));
		} finally {
			if (!disposed) busy = false;
		}
	}

	async function refreshVersion() {
		// Explicit user action after a version conflict: re-read the resource, keep the form input.
		busy = true;
		try {
			all = await listEntryPointResources(controller.signal);
			if (!creating) current = all.find((e) => e.name === name) ?? current;
			conflict = false;
			error = '';
		} catch (e) {
			error = describeFailure(e, c('loadFailed'));
		} finally {
			if (!disposed) busy = false;
		}
	}

	async function apply() {
		if (!review || busy || serverErrors.length) return;
		busy = true;
		error = '';
		try {
			// Same body + key on every retry of an uncertain attempt.
			const saved = await putEntryPoint(f.name, body(), adopting ? 0 : (current?.version ?? 0), {
				idempotencyKey: applyKey,
				signal: controller.signal,
			});
			if (disposed) return;
			result = operationOf(saved, t('writeV2.noChange'));
			manualRestart = needsManualRestart(saved);
			uncertain = false;
			applied = true;
			navigationGuard.setDirty(false);
			onchanged?.();
			if (announceApplied(result, manualRestart, f.name)) {
				navigate('/entrypoints');
				return;
			}
		} catch (e) {
			if (disposed) return;
			if (e instanceof NetworkError) {
				uncertain = true;
				error = c('applyUncertain');
			} else {
				if (e instanceof ApiError && [403, 404, 409, 410, 412, 422, 428].includes(e.status)) {
					if (e.status === 422) fields = { ...fields, ...mapFieldErrors(e) };
					review = null;
					applyKey = '';
					uncertain = false;
					step = 'entrypoint-' + lastEditTab;
				}
				error = describeFailure(e, c('applyFailed'));
			}
		} finally {
			if (!disposed) busy = false;
		}
	}

	function close() {
		if (busy) return;
		navigationGuard.requestNavigation(() => {
			if (applied) onchanged?.();
			navigate('/entrypoints');
		});
	}

	const trustOptions = (kind: 'forwarded' | 'proxy') =>
		(['none', 'list', 'all'] as TrustMode[]).map((value) => ({
			value,
			label: c(`${kind}.${value}`),
			description: c(`${kind}.${value}Help`),
		}));
	const fallback = (value: string) => c('defaultValue', { value });
	const usageNames = $derived([
		...(usage?.routers ?? []).map((n) => c('usage.router', { name: n })),
		...(usage?.resolvers ?? []).map((n) => c('usage.resolver', { name: n })),
		...(usage?.entry_points ?? []).map((n) => c('usage.entryPoint', { name: n })),
	]);
</script>

{#snippet duration(
	key:
		'readTimeout' | 'writeTimeout' | 'idleTimeout' | 'graceTimeout' | 'requestAcceptGraceTimeout' | 'keepAliveMaxTime',
	initial: string,
)}
	<Field
		id={'entrypoint-' + key}
		label={c('advanced.' + key)}
		error={fields['advanced.' + key]}
		help={c('advanced.' + key + 'Help')}
	>
		{#snippet children(slot)}
			<input
				id={slot.id}
				value={f[key]}
				oninput={(e) => set(key, e.currentTarget.value)}
				disabled={locked}
				placeholder={fallback(initial)}
				aria-invalid={slot.invalid || undefined}
				aria-describedby={slot.describedBy}
				autocomplete="off"
				spellcheck="false"
			/>
		{/snippet}
	</Field>
{/snippet}

{#snippet count(
	key:
		| 'keepAliveMaxRequests'
		| 'maxHeaderBytes'
		| 'maxConcurrentStreams'
		| 'maxDecoderHeaderTableSize'
		| 'maxEncoderHeaderTableSize',
	initial: string,
)}
	<Field
		id={'entrypoint-' + key}
		label={c('advanced.' + key)}
		error={fields['advanced.' + key]}
		help={c('advanced.' + key + 'Help')}
	>
		{#snippet children(slot)}
			<input
				id={slot.id}
				inputmode="numeric"
				value={f[key]}
				oninput={(e) => set(key, e.currentTarget.value)}
				disabled={locked}
				placeholder={fallback(initial)}
				aria-invalid={slot.invalid || undefined}
				aria-describedby={slot.describedBy}
				autocomplete="off"
			/>
		{/snippet}
	</Field>
{/snippet}

{#snippet trusted(key: 'forwardedIPs' | 'proxyIPs')}
	<Field
		id={'entrypoint-' + key}
		label={c('trustedIPs')}
		required
		error={chipErrors[key] || fields['trust.' + key]}
		help={c('trustedIPsHelp')}
	>
		{#snippet children(slot)}
			<ChipInput
				id={slot.id}
				bind:this={chips['trust.' + key]}
				values={f[key]}
				placeholder="10.0.0.0/8"
				addPlaceholder={c('chips.add')}
				removeLabel={c('chips.remove')}
				invalidMessage={c('errors.ip')}
				validate={validIPOrCIDR}
				invalid={slot.invalid}
				describedBy={slot.describedBy}
				disabled={locked}
				onchange={(v) => set(key, v)}
				onerror={(m) => chipError(key, m)}
			/>
		{/snippet}
	</Field>
{/snippet}

<ResourceDialog
	title={creating ? c('create') : mode === 'edit' ? (adopting ? c('adopt.title') : c('edit')) : name}
	description={creating || mode === 'edit' ? '' : t('topology.kind.entrypoint')}
	closeLabel={t('common.close')}
	onclose={close}
	{suspended}
>
	{#snippet icon()}<LogIn size={32} aria-hidden="true" />{/snippet}

	{#snippet navigation()}
		{#if !loading && !loadError && editing}
			<Tabs {tabs} selected={step} onselect={selectStep} label={c('tabs')} />
		{/if}
	{/snippet}

	{#if loading}
		<p role="status">{c('loading')}</p>
	{:else if loadError}
		<div class="stack">
			<Notice tone="error" message={loadError} />
			<div><Button onclick={load}>{t('common.retry')}</Button></div>
		</div>
	{:else if editing}
		<div class="stack">
			{#if error}
				<Notice tone="error" message={error}>
					{#if conflict}<Button size="sm" onclick={refreshVersion} disabled={busy}>{c('refresh')}</Button>{/if}
					{#if uncertain}<Button size="sm" onclick={apply} disabled={busy}>{c('retryApply')}</Button>{/if}
				</Notice>
			{/if}
			{#if tabErrors.length && step !== 'entrypoint-review'}
				<div class="validation-banner" role="alert">
					<CircleAlert size={16} aria-hidden="true" />
					<span>{c(tabErrors.length === 1 ? 'fixFieldsOne' : 'fixFields', { count: tabErrors.length })}</span>
					<button type="button" onclick={() => focusField(tabErrors[0][0])}>{c('goToField')}</button>
				</div>
			{/if}

			<!-- ============ Listener ============ -->
			<div
				class="stack"
				id="panel-entrypoint-listener"
				role="tabpanel"
				aria-labelledby="tab-entrypoint-listener"
				hidden={step !== 'entrypoint-listener'}
			>
				{#if adopting}<Notice title={c('adopt.noticeTitle')} message={c('adopt.notice')} />{/if}
				<Field
					id="entrypoint-kind"
					label={c('kind.label')}
					required
					error={fields['listener.kind']}
					help={fields['listener.kind'] ? '' : c('kind.help')}
				>
					{#snippet children(slot)}
						<SearchableSelect
							id={slot.id}
							value={f.kind}
							options={kindOptions}
							onchange={changeKind}
							disabled={locked || kindOptions.length < 2}
							ariaInvalid={slot.invalid}
							ariaDescribedby={slot.describedBy}
						/>
					{/snippet}
				</Field>
				<Field
					id="entrypoint-name"
					label={c('name')}
					required
					error={fields['listener.name']}
					help={fields['listener.name'] ? '' : creating ? c('nameHelp') : c('nameLocked')}
				>
					{#snippet children(slot)}
						<input
							id={slot.id}
							value={f.name}
							oninput={(e) => set('name', e.currentTarget.value)}
							disabled={locked || !creating}
							aria-invalid={slot.invalid || undefined}
							aria-describedby={slot.describedBy}
							autocomplete="off"
							spellcheck="false"
							placeholder="websecure"
						/>
					{/snippet}
				</Field>
				<div class="pair">
					<Field
						id="entrypoint-protocol"
						label={c('protocol')}
						required
						error={fields['listener.protocol']}
						help={fields['listener.protocol'] ? '' : c(tcp ? 'protocolTcpHelp' : 'protocolUdpHelp')}
					>
						{#snippet children(slot)}
							<!-- Only the protocols the type can listen on are offered. -->
							<SearchableSelect
								id={slot.id}
								value={f.protocol}
								options={protocolOptions}
								onchange={(v) => set('protocol', v as EntryPointForm['protocol'])}
								disabled={locked || protocolOptions.length < 2}
								ariaInvalid={slot.invalid}
								ariaDescribedby={slot.describedBy}
							/>
						{/snippet}
					</Field>
					<Field
						id="entrypoint-port"
						label={c('port')}
						required
						error={fields['listener.port']}
						help={fields['listener.port'] ? '' : c('portHelp')}
					>
						{#snippet children(slot)}
							<input
								id={slot.id}
								inputmode="numeric"
								value={f.port}
								oninput={(e) => set('port', e.currentTarget.value)}
								disabled={locked}
								aria-invalid={slot.invalid || undefined}
								aria-describedby={slot.describedBy}
								autocomplete="off"
								placeholder={tcp ? '443' : '53'}
							/>
						{/snippet}
					</Field>
				</div>
				{#if !fields['listener.port'] && conflictNow?.reason === 'port'}
					<Notice
						tone="warning"
						message={c('errors.portTaken', {
							port: f.port,
							protocol: f.protocol.toUpperCase(),
							name: conflictNow.name,
						})}
					/>
				{/if}
				{#if tcp}
					<Checkbox
						id="entrypoint-default"
						checked={f.asDefault}
						label={c('asDefault')}
						help={c('asDefaultHelp')}
						disabled={locked}
						onchange={(v) => set('asDefault', v)}
					/>
				{:else}
					<Field
						id="entrypoint-udpTimeout"
						label={c('udpTimeout')}
						error={fields['listener.udpTimeout']}
						help={c('udpTimeoutHelp')}
					>
						{#snippet children(slot)}
							<input
								id={slot.id}
								value={f.udpTimeout}
								oninput={(e) => set('udpTimeout', e.currentTarget.value)}
								disabled={locked}
								placeholder={fallback('3s')}
								aria-invalid={slot.invalid || undefined}
								aria-describedby={slot.describedBy}
								autocomplete="off"
							/>
						{/snippet}
					</Field>
				{/if}
				<details class="options" open={!!(f.host || f.reusePort || fields['listener.host'])}>
					<summary>{c('moreOptions')} <span>{f.host || f.reusePort ? c('configured') : c('defaults')}</span></summary>
					<div class="stack option-fields">
						<Field
							id="entrypoint-host"
							label={c('host')}
							error={fields['listener.host']}
							help={fields['listener.host'] ? '' : c('hostHelp')}
						>
							{#snippet children(slot)}
								<input
									id={slot.id}
									value={f.host}
									oninput={(e) => set('host', e.currentTarget.value)}
									disabled={locked}
									aria-invalid={slot.invalid || undefined}
									aria-describedby={slot.describedBy}
									autocomplete="off"
									spellcheck="false"
									placeholder={c('hostPlaceholder')}
								/>
							{/snippet}
						</Field>
						<Checkbox
							id="entrypoint-reusePort"
							checked={f.reusePort}
							label={c('reusePort')}
							help={c('reusePortHelp')}
							disabled={locked}
							onchange={(v) => set('reusePort', v)}
						/>
					</div>
				</details>
			</div>

			{#if web}
				<!-- ============ HTTP & TLS ============ -->
				<div
					class="stack"
					id="panel-entrypoint-http"
					role="tabpanel"
					aria-labelledby="tab-entrypoint-http"
					hidden={step !== 'entrypoint-http'}
				>
					<h3>{c('http.redirectSection')}</h3>
					<Checkbox
						id="entrypoint-redirect"
						checked={f.redirect}
						label={c('http.redirect')}
						help={c('http.redirectHelp')}
						disabled={locked}
						onchange={(v) => set('redirect', v)}
					/>
					{#if f.redirect}
						<div class="nested">
							<Field id="entrypoint-redirectTo" label={c('http.redirectTo')} required error={fields['http.redirectTo']}>
								{#snippet children(slot)}
									<SearchableSelect
										id={slot.id}
										value={f.redirectTarget || (redirectUsePort ? ':port' : '')}
										placeholder={c('http.redirectChoose')}
										options={[
											...redirectTargets.map((e) => ({
												value: e.name,
												label: e.name,
												description: displayAddress(e.config),
												badge: e.config.http?.tls ? 'TLS' : undefined,
											})),
											{ value: ':port', label: c('http.redirectOtherPort') },
										]}
										onchange={chooseRedirectTarget}
										disabled={locked}
										ariaInvalid={slot.invalid}
										ariaDescribedby={slot.describedBy}
									/>
								{/snippet}
							</Field>
							{#if redirectUsePort}
								<Field
									id="entrypoint-redirectPort"
									label={c('http.redirectPort')}
									required
									help={c('http.redirectPortHelp')}
								>
									{#snippet children(slot)}
										<input
											id={slot.id}
											inputmode="numeric"
											value={f.redirectPort}
											oninput={(e) => set('redirectPort', e.currentTarget.value)}
											disabled={locked}
											placeholder="443"
											aria-describedby={slot.describedBy}
											autocomplete="off"
										/>
									{/snippet}
								</Field>
							{/if}
							<div class="pair">
								<Field id="entrypoint-redirectScheme" label={c('http.redirectScheme')}>
									{#snippet children(slot)}
										<SearchableSelect
											id={slot.id}
											value={f.redirectScheme}
											options={[
												{ value: 'https', label: 'https' },
												{ value: 'http', label: 'http' },
											]}
											onchange={(v) => set('redirectScheme', v as EntryPointForm['redirectScheme'])}
											disabled={locked}
										/>
									{/snippet}
								</Field>
								<Field id="entrypoint-redirectKind" label={c('http.redirectKind')}>
									{#snippet children(slot)}
										<SearchableSelect
											id={slot.id}
											value={f.redirectTemporary ? 'temporary' : 'permanent'}
											options={[
												{ value: 'permanent', label: c('http.redirectPermanent') },
												{ value: 'temporary', label: c('http.redirectTemporary') },
											]}
											onchange={(v) => set('redirectTemporary', v === 'temporary')}
											disabled={locked}
										/>
									{/snippet}
								</Field>
							</div>
							<Field
								id="entrypoint-redirectPriority"
								label={c('http.redirectPriority')}
								error={fields['http.redirectPriority']}
								help={c('http.redirectPriorityHelp')}
							>
								{#snippet children(slot)}
									<input
										id={slot.id}
										inputmode="numeric"
										value={f.redirectPriority}
										oninput={(e) => set('redirectPriority', e.currentTarget.value)}
										disabled={locked}
										placeholder={c('http.redirectPriorityDefault')}
										aria-invalid={slot.invalid || undefined}
										aria-describedby={slot.describedBy}
										autocomplete="off"
									/>
								{/snippet}
							</Field>
						</div>
					{/if}
					<Checkbox
						id="entrypoint-acmeBypass"
						checked={f.allowAcmeBypass}
						label={c('http.allowAcmeBypass')}
						help={c('http.allowAcmeBypassHelp')}
						disabled={locked}
						onchange={(v) => set('allowAcmeBypass', v)}
					/>

					<h3>{c('http.tlsSection')}</h3>
					{#if f.kind === 'https'}
						<p class="muted">{c('http.tlsSectionHelp')}</p>
						{#if f.redirect}<Notice message={c('http.tlsWithRedirect')} />{/if}
						<Field
							id="entrypoint-certResolver"
							label={c('http.certResolver')}
							error={fields['http.certResolver']}
							help={f.certResolver ? c('http.certResolverHelp') : c('http.noResolverNote')}
						>
							{#snippet children(slot)}
								<SearchableSelect
									id={slot.id}
									value={f.certResolver}
									options={[
										{ value: '', label: c('http.noResolver') },
										...[...new Set([...resolvers, ...(f.certResolver ? [f.certResolver] : [])])].map((r) => ({
											value: r,
											label: r,
										})),
									]}
									onchange={(v) => set('certResolver', v)}
									disabled={locked}
									ariaInvalid={slot.invalid}
									ariaDescribedby={slot.describedBy}
								/>
							{/snippet}
						</Field>
						{#if f.certResolver}
							<div class="nested" id="entrypoint-tlsDomains" tabindex="-1">
								<p class="muted"><strong>{c('http.tlsDomains')}</strong> · {c('http.tlsDomainsHelp')}</p>
								{#each f.tlsDomains as domain, index (index)}
									<div class="domain-row">
										<Field id={'entrypoint-domain-' + index} label={c('http.domainMain')}>
											{#snippet children(slot)}
												<input
													id={slot.id}
													value={domain.main}
													oninput={(e) => {
														f.tlsDomains[index].main = e.currentTarget.value;
														invalidate();
													}}
													disabled={locked}
													placeholder="example.com"
													autocomplete="off"
													spellcheck="false"
												/>
											{/snippet}
										</Field>
										<Field
											id={'entrypoint-domain-sans-' + index}
											label={c('http.domainSans')}
											error={chipErrors['sans' + index]}
										>
											{#snippet children(slot)}
												<ChipInput
													id={slot.id}
													bind:this={chips['http.tlsDomains.' + index]}
													values={domain.sans}
													placeholder="*.example.com"
													addPlaceholder={c('chips.add')}
													removeLabel={c('chips.remove')}
													invalidMessage={c('errors.domain')}
													validate={validDomainName}
													invalid={slot.invalid}
													describedBy={slot.describedBy}
													disabled={locked}
													onchange={(v) => {
														f.tlsDomains[index].sans = v;
														invalidate();
													}}
													onerror={(m) => chipError('sans' + index, m)}
												/>
											{/snippet}
										</Field>
										<button
											type="button"
											class="icon-button"
											disabled={locked}
											aria-label={c('http.removeDomain')}
											onclick={() => {
												f.tlsDomains = f.tlsDomains.filter((_, i) => i !== index);
												invalidate();
											}}
										>
											<X size={16} aria-hidden="true" />
										</button>
									</div>
								{/each}
								{#if fields['http.tlsDomains']}<Notice tone="error" message={fields['http.tlsDomains']} />{/if}
								<button
									type="button"
									class="add-row"
									onclick={() => {
										f.tlsDomains = [...f.tlsDomains, { main: '', sans: [] }];
										invalidate();
									}}
									disabled={locked || f.tlsDomains.length >= 16}
								>
									<Plus size={16} aria-hidden="true" />{c('http.addDomain')}
								</button>
							</div>
						{/if}
						<Field
							id="entrypoint-tlsOptions"
							label={c('http.tlsOptions')}
							error={fields['http.tlsOptions']}
							help={c('http.tlsOptionsHelp')}
						>
							{#snippet children(slot)}
								<input
									id={slot.id}
									value={f.tlsOptions}
									oninput={(e) => set('tlsOptions', e.currentTarget.value)}
									disabled={locked}
									placeholder="default"
									aria-invalid={slot.invalid || undefined}
									aria-describedby={slot.describedBy}
									autocomplete="off"
									spellcheck="false"
								/>
							{/snippet}
						</Field>
					{:else}
						<Notice message={c('http.tlsPerRouter')} />
					{/if}
					<div id="entrypoint-http3" tabindex="-1">
						<Checkbox
							checked={f.http3}
							label={c('http.http3')}
							help={c('http.http3Help', { port: portLabel })}
							disabled={locked}
							onchange={(v) => set('http3', v)}
						/>
					</div>
					{#if f.http3}
						<div class="nested">
							{#if fields['http.http3']}<Notice
									tone="error"
									message={fields['http.http3']}
								/>{:else if f.kind !== 'https'}<Notice tone="warning" message={c('http.http3NeedsTls')} />{/if}
							<Field
								id="entrypoint-http3Port"
								label={c('http.http3Port')}
								error={fields['http.http3Port']}
								help={c('http.http3PortHelp')}
							>
								{#snippet children(slot)}
									<input
										id={slot.id}
										inputmode="numeric"
										value={f.http3Port}
										oninput={(e) => set('http3Port', e.currentTarget.value)}
										disabled={locked}
										placeholder={portLabel}
										aria-invalid={slot.invalid || undefined}
										aria-describedby={slot.describedBy}
										autocomplete="off"
									/>
								{/snippet}
							</Field>
						</div>
					{/if}

					<h3>{c('http.middlewaresSection')}</h3>
					<Field
						id="entrypoint-middlewares"
						label={c('http.middlewares')}
						error={fields['http.middlewares']}
						help={c('http.middlewaresHelp')}
					>
						{#snippet children(slot)}
							<SearchableMultiSelect
								id={slot.id}
								values={f.middlewares}
								options={middlewareOptions}
								maxItems={32}
								placeholder={c('http.middlewaresPlaceholder')}
								onchange={(v) => set('middlewares', v)}
								disabled={locked}
								ariaInvalid={slot.invalid}
								ariaDescribedby={slot.describedBy}
							/>
						{/snippet}
					</Field>
					<details class="options">
						<summary>{c('http.middlewareOther')}</summary>
						<div class="stack option-fields">
							<Field
								id="entrypoint-middlewareOther"
								label={c('http.middlewareOtherLabel')}
								error={chipErrors.middlewares}
								help={c('http.middlewareOtherHelp')}
							>
								{#snippet children(slot)}
									<div class="inline">
										<input
											id={slot.id}
											bind:value={customMiddleware}
											disabled={locked}
											placeholder="auth@docker"
											aria-invalid={slot.invalid || undefined}
											aria-describedby={slot.describedBy}
											autocomplete="off"
											spellcheck="false"
											onkeydown={(e) => {
												if (e.key === 'Enter') {
													e.preventDefault();
													addCustomMiddleware();
												}
											}}
										/>
										<Button onclick={addCustomMiddleware} disabled={locked || !customMiddleware.trim()}>
											{c('http.middlewareAdd')}
										</Button>
									</div>
								{/snippet}
							</Field>
						</div>
					</details>
				</div>
			{/if}

			{#if tcp}
				<!-- ============ Client addresses ============ -->
				<div
					class="stack"
					id="panel-entrypoint-trust"
					role="tabpanel"
					aria-labelledby="tab-entrypoint-trust"
					hidden={step !== 'entrypoint-trust'}
				>
					{#if web}
						<h3>{c('forwarded.section')}</h3>
						<Field id="entrypoint-forwardedMode" label={c('forwarded.legend')} help={c('forwarded.sectionHelp')}>
							{#snippet children(slot)}
								<SearchableSelect
									id={slot.id}
									value={f.forwardedMode}
									options={trustOptions('forwarded')}
									onchange={(v) => set('forwardedMode', v as TrustMode)}
									disabled={locked}
									ariaDescribedby={slot.describedBy}
								/>
							{/snippet}
						</Field>
						{#if f.forwardedMode === 'list'}
							<div class="nested">{@render trusted('forwardedIPs')}</div>
						{:else if f.forwardedMode === 'all'}
							<Notice tone="warning" message={c('forwarded.allWarning')} />
						{/if}
						<details class="options" open={!!(f.connectionHeaders.length || f.notAppendXForwardedFor)}>
							<summary>
								{c('forwarded.more')}
								<span>{f.connectionHeaders.length || f.notAppendXForwardedFor ? c('configured') : c('defaults')}</span>
							</summary>
							<div class="stack option-fields">
								<Field
									id="entrypoint-connectionHeaders"
									label={c('forwarded.connection')}
									error={chipErrors.connectionHeaders || fields['trust.connectionHeaders']}
									help={c('forwarded.connectionHelp')}
								>
									{#snippet children(slot)}
										<ChipInput
											id={slot.id}
											bind:this={chips['trust.connectionHeaders']}
											values={f.connectionHeaders}
											max={32}
											placeholder="Upgrade"
											addPlaceholder={c('chips.add')}
											removeLabel={c('chips.remove')}
											invalidMessage={c('errors.header')}
											validate={validHeaderName}
											invalid={slot.invalid}
											describedBy={slot.describedBy}
											disabled={locked}
											onchange={(v) => set('connectionHeaders', v)}
											onerror={(m) => chipError('connectionHeaders', m)}
										/>
									{/snippet}
								</Field>
								<Checkbox
									id="entrypoint-notAppend"
									checked={f.notAppendXForwardedFor}
									label={c('forwarded.notAppend')}
									help={c('forwarded.notAppendHelp')}
									disabled={locked}
									onchange={(v) => set('notAppendXForwardedFor', v)}
								/>
							</div>
						</details>
					{/if}

					<h3>{c('proxy.section')}</h3>
					<Field id="entrypoint-proxyMode" label={c('proxy.legend')} help={c('proxy.sectionHelp')}>
						{#snippet children(slot)}
							<SearchableSelect
								id={slot.id}
								value={f.proxyMode}
								options={trustOptions('proxy')}
								onchange={(v) => set('proxyMode', v as TrustMode)}
								disabled={locked}
								ariaDescribedby={slot.describedBy}
							/>
						{/snippet}
					</Field>
					{#if f.proxyMode === 'list'}<div class="nested">{@render trusted('proxyIPs')}</div>{/if}
					{#if f.proxyMode !== 'none'}<Notice
							tone="warning"
							message={c(f.proxyMode === 'all' ? 'proxy.allWarning' : 'proxy.enabledWarning')}
						/>{/if}
				</div>

				<!-- ============ Limits ============ -->
				<div
					class="stack"
					id="panel-entrypoint-advanced"
					role="tabpanel"
					aria-labelledby="tab-entrypoint-advanced"
					hidden={step !== 'entrypoint-advanced'}
				>
					<Notice message={c('advanced.intro')} />
					<h3>{c('advanced.timeouts')}</h3>
					<div class="pair">
						{@render duration('readTimeout', '60s')}
						{@render duration('writeTimeout', '0s')}
						{@render duration('idleTimeout', '180s')}
						{@render duration('keepAliveMaxTime', '0s')}
						{@render count('keepAliveMaxRequests', '0')}
					</div>
					<h3>{c('advanced.shutdown')}</h3>
					<div class="pair">
						{@render duration('graceTimeout', '10s')}
						{@render duration('requestAcceptGraceTimeout', '0s')}
					</div>
					{#if web}
						<details class="options" open={limitsSet || !!fields['advanced.maxHeaderBytes']}>
							<summary>{c('advanced.requests')} <span>{limitsSet ? c('configured') : c('defaults')}</span></summary>
							<div class="stack option-fields">
								{@render count('maxHeaderBytes', '1048576')}
								<fieldset class="checks" disabled={locked}>
									<legend>{c('advanced.deniedCharacters')}</legend>
									<p class="muted small">{c('advanced.deniedCharactersHelp')}</p>
									{#each ENCODED_CHARACTERS as character (character)}
										<Checkbox
											checked={f.deniedCharacters.includes(character)}
											label={c('advanced.character.' + character)}
											disabled={locked}
											onchange={(v) => toggleCharacter(character, v)}
										/>
									{/each}
								</fieldset>
								<Field
									id="entrypoint-aliasHeaders"
									label={c('advanced.aliasHeaders')}
									help={c('advanced.aliasHeadersHelp')}
								>
									{#snippet children(slot)}
										<SearchableSelect
											id={slot.id}
											value={f.aliasHeadersStrategy}
											options={(['keep', 'delete', 'reject'] as const).map((value) => ({
												value,
												label: c('advanced.alias.' + value),
											}))}
											onchange={(v) => set('aliasHeadersStrategy', v as EntryPointForm['aliasHeadersStrategy'])}
											disabled={locked}
											ariaDescribedby={slot.describedBy}
										/>
									{/snippet}
								</Field>
								<Checkbox
									id="entrypoint-querySemicolons"
									checked={f.encodeQuerySemicolons}
									label={c('advanced.encodeQuerySemicolons')}
									help={c('advanced.encodeQuerySemicolonsHelp')}
									disabled={locked}
									onchange={(v) => set('encodeQuerySemicolons', v)}
								/>
								<Checkbox
									id="entrypoint-sanitizePath"
									checked={!f.disableSanitizePath}
									label={c('advanced.sanitizePath')}
									help={c('advanced.sanitizePathHelp')}
									disabled={locked}
									onchange={(v) => set('disableSanitizePath', !v)}
								/>
								{#if f.disableSanitizePath}<Notice tone="warning" message={c('advanced.sanitizePathWarning')} />{/if}
							</div>
						</details>
						<details
							class="options"
							open={http2Set ||
								!!(
									fields['advanced.maxConcurrentStreams'] ||
									fields['advanced.maxDecoderHeaderTableSize'] ||
									fields['advanced.maxEncoderHeaderTableSize']
								)}
						>
							<summary>{c('advanced.http2')} <span>{http2Set ? c('configured') : c('defaults')}</span></summary>
							<div class="stack option-fields">
								{@render count('maxConcurrentStreams', '250')}
								<div class="pair">
									{@render count('maxDecoderHeaderTableSize', '4096')}
									{@render count('maxEncoderHeaderTableSize', '4096')}
								</div>
							</div>
						</details>
					{/if}
					<details class="options" open={!f.accessLogs || !f.metrics || !f.tracing || f.traceVerbosity === 'detailed'}>
						<summary>
							{c('advanced.observability')}
							<span>
								{!f.accessLogs || !f.metrics || !f.tracing || f.traceVerbosity === 'detailed'
									? c('configured')
									: c('defaults')}
							</span>
						</summary>
						<div class="stack option-fields">
							<p class="muted small">{c('advanced.observabilityHelp')}</p>
							<Checkbox
								checked={f.accessLogs}
								label={c('advanced.accessLogs')}
								disabled={locked}
								onchange={(v) => set('accessLogs', v)}
							/>
							<Checkbox
								checked={f.metrics}
								label={c('advanced.metrics')}
								disabled={locked}
								onchange={(v) => set('metrics', v)}
							/>
							<Checkbox
								checked={f.tracing}
								label={c('advanced.tracing')}
								disabled={locked}
								onchange={(v) => set('tracing', v)}
							/>
							{#if f.tracing}
								<Field
									id="entrypoint-traceVerbosity"
									label={c('advanced.traceVerbosity')}
									help={c('advanced.traceVerbosityHelp')}
								>
									{#snippet children(slot)}
										<SearchableSelect
											id={slot.id}
											value={f.traceVerbosity}
											options={(['minimal', 'detailed'] as const).map((value) => ({
												value,
												label: c('advanced.verbosity.' + value),
											}))}
											onchange={(v) => set('traceVerbosity', v as EntryPointForm['traceVerbosity'])}
											disabled={locked}
											ariaDescribedby={slot.describedBy}
										/>
									{/snippet}
								</Field>
							{/if}
						</div>
					</details>
				</div>
			{/if}

			<!-- ============ Review ============ -->
			<div
				class="stack"
				id="panel-entrypoint-review"
				role="tabpanel"
				aria-labelledby="tab-entrypoint-review"
				hidden={step !== 'entrypoint-review'}
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
							<dt>{c('name')}</dt>
							<dd>{f.name}</dd>
						</div>
						<div>
							<dt>{c('kind.label')}</dt>
							<dd>{c('kind.' + f.kind)}</dd>
						</div>
						<div>
							<dt>{c('address')}</dt>
							<dd>
								<code>
									{(f.host.trim().includes(':') ? `[${f.host.trim()}]` : f.host.trim()) + ':' + f.port.trim()}
								</code>
								{f.protocol.toUpperCase()}
							</dd>
						</div>
						{#if tcp}<div>
								<dt>{c('summary.routing')}</dt>
								<dd>
									{web && f.redirect
										? c('summary.redirect', { to: f.redirectTarget || ':' + f.redirectPort, scheme: f.redirectScheme })
										: c(f.asDefault ? 'summary.default' : 'summary.explicit')}
								</dd>
							</div>{/if}
						{#if web}
							<div>
								<dt>{c('summary.tls')}</dt>
								<dd>
									{f.kind === 'https'
										? f.certResolver
											? c('summary.tlsResolver', { resolver: f.certResolver })
											: c('summary.tlsOn')
										: c('summary.tlsOff')}
								</dd>
							</div>
							{#if f.http3}<div>
									<dt>HTTP/3</dt>
									<dd>{c('summary.http3', { port: f.port.trim() })}</dd>
								</div>{/if}
							{#if f.middlewares.length}<div>
									<dt>{c('http.middlewares')}</dt>
									<dd>{f.middlewares.join(', ')}</dd>
								</div>{/if}
							{#if f.forwardedMode !== 'none'}<div>
									<dt>{c('forwarded.section')}</dt>
									<dd>{f.forwardedMode === 'all' ? c('forwarded.all') : f.forwardedIPs.join(', ')}</dd>
								</div>{/if}
						{/if}
						{#if tcp && f.proxyMode !== 'none'}<div>
								<dt>{c('proxy.section')}</dt>
								<dd>{f.proxyMode === 'all' ? c('proxy.all') : f.proxyIPs.join(', ')}</dd>
							</div>{/if}
					</SummaryList>

					{#if serverErrors.length}
						<Notice tone="error" title={c('serverErrors')}>
							<ul class="reasons">
								{#each serverErrors as ve}<li>{ve.item_id}{ve.field ? ' · ' + ve.field : ''}: {ve.message}</li>{/each}
							</ul>
						</Notice>
					{/if}
					{#if review.requires_restart}<Notice
							tone="warning"
							title={c('restartTitle')}
							message={c('restartHelp')}
						/>{/if}
					{#if validPort(f.port) && (creating || Number(f.port) !== current?.config.port || (web && f.http3) !== !!current?.config.http3)}
						<Notice
							title={c('publishTitle')}
							message={web && f.http3
								? c('publishHttp3', { port: f.port.trim() })
								: c('publish', { port: f.port.trim(), protocol: f.protocol.toUpperCase() })}
						/>
					{/if}
					{#if review.validation_warnings?.length}
						<Notice tone="warning" title={t('common.warning')}>
							<ul class="reasons">
								{#each review.validation_warnings as w}<li>{w.message}</li>{/each}
							</ul>
						</Notice>
					{/if}
					{#if review.static_diff}<CodePreview
							label={c('diffStatic')}
							code={review.static_diff}
							language="diff"
						/>{:else}<Notice message={c('diffNone')} />{/if}
				{:else}
					<p role="status">{c('staging')}</p>
				{/if}
			</div>
		</div>
	{:else if current}
		<!-- ============ View ============ -->
		<div class="stack">
			{#if current.origin === 'external'}
				<Notice
					title={c('view.externalTitle')}
					message={current.config.adoptable
						? c('view.externalAdoptable')
						: c('view.externalBlocked') + ' ' + (current.config.adopt_blocker ?? '')}
				/>
			{/if}
			<SummaryList>
				<div>
					<dt>{c('name')}</dt>
					<dd>{current.name}</dd>
				</div>
				<div>
					<dt>{c('kind.label')}</dt>
					<dd>{c('kind.' + kindOf(current.config))}</dd>
				</div>
				<div>
					<dt>{c('address')}</dt>
					<dd><code>{displayAddress(current.config)}</code> {(current.config.protocol ?? 'tcp').toUpperCase()}</dd>
				</div>
				<div>
					<dt>{c('source')}</dt>
					<dd>{c(current.origin === 'external' ? 'external' : current.config.adopted ? 'adopted' : 'managed')}</dd>
				</div>
				{#if current.config.http?.redirect}<div>
						<dt>{c('summary.routing')}</dt>
						<dd>
							{c('summary.redirect', {
								to: current.config.http.redirect.to,
								scheme: current.config.http.redirect.scheme ?? 'https',
							})}
						</dd>
					</div>{/if}
				{#if current.config.protocol !== 'udp'}<div>
						<dt>{c('summary.tls')}</dt>
						<dd>
							{current.config.http?.tls
								? current.config.http.tls.cert_resolver
									? c('summary.tlsResolver', { resolver: current.config.http.tls.cert_resolver })
									: c('summary.tlsOn')
								: c('summary.tlsOff')}
						</dd>
					</div>{/if}
				<div>
					<dt>{c('usedBy')}</dt>
					<dd>{usageCount(usage) ? usageNames.join(', ') : t('related.notUsed')}</dd>
				</div>
			</SummaryList>
			{#each current.config.warnings ?? [] as warning}<p class="muted">{warning}</p>{/each}
			{#if renderedYaml}<CodePreview label={c('rendered')} code={renderedYaml} language="yaml" />{/if}
		</div>
	{/if}

	{#snippet footer()}
		<Button onclick={close} disabled={busy}>{result || !editing ? t('common.close') : t('common.cancel')}</Button>
		{#if !loading && !loadError && !result}
			{#if editing}
				{#if step === 'entrypoint-review'}
					<Button onclick={() => (step = 'entrypoint-' + lastEditTab)} disabled={busy}>{t('common.back')}</Button>
					{#if review}
						<Button variant="primary" onclick={apply} pending={busy} disabled={busy || serverErrors.length > 0}>
							{c(busy ? 'applying' : uncertain ? 'retryApply' : 'apply')}
						</Button>
					{/if}
				{:else}
					{#if stepName !== 'listener'}
						<Button
							onclick={() => selectStep('entrypoint-' + tabIds[Math.max(0, tabIds.indexOf(stepName) - 1)])}
							disabled={busy}
						>
							{t('common.back')}
						</Button>
					{/if}
					<Button
						variant="primary"
						onclick={() => selectStep('entrypoint-' + tabIds[tabIds.indexOf(stepName) + 1])}
						pending={busy}
						disabled={busy}
					>
						{stepName === lastEditTab ? c('stageReview') : c('continue')}
					</Button>
				{/if}
			{:else if current && !current.read_only}
				<Button
					variant="primary"
					onclick={() => navigate('/entrypoints/' + encodeURIComponent(current!.name) + '/edit')}
				>
					{c('edit')}
				</Button>
			{:else if current?.config.adoptable}
				<Button
					variant="primary"
					onclick={() => navigate('/entrypoints/' + encodeURIComponent(current!.name) + '/edit')}
				>
					{c('adopt.action')}
				</Button>
			{/if}
		{/if}
	{/snippet}
</ResourceDialog>

<style>
	/* Same building blocks as the service and resolver dialogs: stacked fields, ruled section titles,
	   fields indented under the checkbox they belong to, collapsed optional groups. */
	.stack,
	.stack:not([hidden]) {
		display: flex;
		flex-direction: column;
		gap: 24px;
	}
	.stack[hidden] {
		display: none;
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
	.muted {
		margin: 0;
		color: var(--text-muted);
		font-size: 14px;
		line-height: 21px;
	}
	.muted strong {
		color: var(--text);
		font-weight: 500;
	}
	.small {
		font-size: 13px;
		line-height: 20px;
	}
	.reasons {
		margin: 8px 0 0;
		padding-left: 20px;
		font-size: 14px;
		line-height: 22px;
	}
	.nested {
		display: flex;
		flex-direction: column;
		gap: 20px;
		box-sizing: border-box;
		max-width: 560px;
		padding-left: 16px;
		border-left: 2px solid var(--border);
		margin-left: 9px;
	}
	.nested:focus {
		outline: none;
	}
	.nested :global(.field) {
		max-width: none;
	}
	.pair {
		display: grid;
		grid-template-columns: repeat(2, minmax(0, 1fr));
		gap: 16px;
		max-width: 560px;
		align-items: start;
	}
	.options {
		border-top: 1px solid var(--border);
		padding-top: 16px;
		max-width: 560px;
	}
	.options summary {
		cursor: pointer;
		font-size: 14px;
		font-weight: 500;
		line-height: 24px;
		color: var(--text);
	}
	.options summary span {
		color: var(--text-muted);
		font-weight: 400;
		margin-left: 8px;
	}
	.options summary span::before {
		content: '·';
		margin-right: 8px;
	}
	.option-fields {
		padding-top: 20px;
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
	.domain-row {
		display: grid;
		grid-template-columns: minmax(0, 1fr) minmax(0, 1.3fr) 40px;
		gap: 12px;
		align-items: start;
	}
	.icon-button {
		display: grid;
		place-items: center;
		width: 40px;
		height: var(--control-h);
		margin-top: 28px;
		padding: 0;
		border: 1px solid var(--outline);
		border-radius: var(--radius-control);
		background: var(--surface);
		color: var(--text-muted);
		cursor: pointer;
	}
	.icon-button:hover:not(:disabled) {
		color: var(--danger-text);
		border-color: var(--danger-text);
	}
	/* Same dashed "add" row as the service dialog's backends. */
	.add-row {
		display: inline-flex;
		align-items: center;
		justify-content: center;
		gap: 8px;
		width: 100%;
		min-height: var(--control-h);
		border: 1px dashed var(--outline);
		border-radius: var(--radius-control);
		background: transparent;
		color: var(--text);
		font: inherit;
		font-size: 14px;
		font-weight: 500;
		cursor: pointer;
	}
	.add-row:hover:not(:disabled) {
		background: var(--surface-muted);
		border-style: solid;
	}
	.add-row:disabled {
		opacity: 0.5;
		cursor: default;
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
	.checks {
		margin: 0;
		padding: 0;
		border: 0;
		display: flex;
		flex-direction: column;
		gap: 12px;
		min-width: 0;
	}
	.checks legend {
		padding: 0;
		font-size: 14px;
		font-weight: 500;
		line-height: 24px;
		color: var(--text);
	}
	code {
		font-size: 13px;
	}
	@media (max-width: 767px) {
		.pair,
		.domain-row {
			grid-template-columns: 1fr;
		}
		.icon-button {
			margin-top: 0;
			width: 100%;
		}
		.add-row {
			min-height: var(--touch-h);
		}
	}
</style>

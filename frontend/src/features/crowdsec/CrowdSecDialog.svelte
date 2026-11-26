<script lang="ts">
	/**
	 * The one CrowdSec settings dialog: how the Console reaches CrowdSec, whether it manages the Traefik
	 * bouncer, and monitoring. A setting given by a CROWDSEC_* variable is read-only and named.
	 *   tabs → Review (PUT /crowdsec/connection?dry_run=true) → Apply (PUT /crowdsec/connection)
	 * Routers keep the bouncer middleware they already carry; attaching it is done on the router.
	 */
	import { onDestroy, onMount, tick } from 'svelte';
	import { CircleAlert, X, Copy, KeyRound, PlugZap, ShieldCheck, Undo2, UserPlus } from '@lucide/svelte';
	import ResourceDialog from '../../shared/ui/ResourceDialog.svelte';
	import Tabs from '../../shared/ui/Tabs.svelte';
	import Field from '../../shared/ui/Field.svelte';
	import SelectMenu from '../../shared/ui/SelectMenu.svelte';
	import Checkbox from '../../shared/ui/Checkbox.svelte';
	import Button from '../../shared/ui/Button.svelte';
	import Notice from '../../shared/ui/Notice.svelte';
	import StatusBadge from '../../shared/ui/StatusBadge.svelte';
	import SummaryList from '../../shared/ui/SummaryList.svelte';
	import CodePreview from '../../shared/ui/CodePreview.svelte';
	import { navigationGuard } from '../../lib/stores/navigationGuard';
	import { announceApplied, needsManualRestart, newIdempotencyKey, operationOf } from '../../core/mutations';
	import { operationTone } from '../changes/models';
	import type { Operation, OperationPreview } from '../../core/types';
	import { ApiError, NetworkError } from '../../shared/api/client';
	import { errorMessage } from '../../shared/api/errorMessage';
	import { t } from '../../shared/localization/i18n';
	import { readCatalogPage } from '../plugins/api';
	import { knownVersions } from '../plugins/models';
	import { getConfig, putConnection, testConnection } from './api';
	import { bouncerCommand, lockedBy, looksLikeIpOrCidr, machineCommand, parseCidrs, testTone, type ConnectionSettings, type ConnectionTest, type CrowdSecStatus, type ProtectionMode } from './models';

	let { status, navigate, onchanged }: { status: CrowdSecStatus; navigate: (p: string) => void; onchanged?: () => void } = $props();

	const c = (key: string, args?: Record<string, string | number>) => t('crowdsecV2.connection.' + key, args);
	/** The lines a bouncer middleware managed outside the Console needs to use the Console's key. */
	const externalKeySnippet = (file: string, appSec: boolean) =>
		[`crowdsecLapiKeyFile: ${file}`, ...(appSec ? [`crowdsecAppsecKeyFile: ${file}`] : [])].join('\n');
	const rv2 = (key: string, args?: Record<string, string | number>) => t('resolverV2.' + key, args);
	const MODULE = 'github.com/maxlerebourg/crowdsec-bouncer-traefik-plugin';
	const STABLE = /^v\d+\.\d+\.\d+$/;
	const URL_RE = /^https?:\/\/[^/\s?#@]+\/?$/;
	const env = (field: string) => lockedBy(status.environment, field);
	/** Help text of a field: the variable that locks it, otherwise the normal help. */
	const help = (field: string, text = '') => { const v = env(field); return v ? c('lockedBy') + ' ' + v.name : text; };

	// The form starts from the status at open time; later refreshes must not overwrite edits.
	// svelte-ignore state_referenced_locally
	const cfg = status.config;
	/** The registered CrowdSec container is running: the Console can create credentials in it. */
	// svelte-ignore state_referenced_locally
	const accessible = status.runtime?.accessible ?? status.engine.state === 'running';
	/** Same shape as the server's generated credentials: 32 random bytes, lowercase hex. */
	function newSecret() {
		const bytes = crypto.getRandomValues(new Uint8Array(32));
		return Array.from(bytes, (b) => b.toString(16).padStart(2, '0')).join('');
	}
	// svelte-ignore state_referenced_locally
	const canGenerate = !env('lapi_key') && accessible && (status.capabilities?.auto_enroll ?? false);
	// svelte-ignore state_referenced_locally
	const canCreateMachine = !env('machine_id') && !env('machine_password') && accessible;
	// svelte-ignore state_referenced_locally
	const keyPresent = status.key.present;
	let form = $state({
		lapiURL: cfg.lapi_url ?? '',
		/** Create a bouncer key in the container when the change is applied (default when none is saved). */
		/** Credentials created in the container on apply; the value is proposed here so it can be shown. */
		generateKey: !keyPresent && canGenerate,
		generatedKey: !keyPresent && canGenerate ? newSecret() : '',
		generateMachine: false,
		generatedPassword: '',
		lapiKey: '',
		machineID: cfg.machine_id ?? '',
		machinePassword: '',
		caCertificate: '',
		clientCertificate: '',
		clientKey: '',
		protection: (cfg.integration_mode === 'external' ? 'external' : cfg.enabled ? 'managed' : 'off') as ProtectionMode,
		bouncerMode: cfg.mode === 'live' ? 'live' : 'stream',
		pluginVersion: '',
		failurePolicy: String(cfg.update_max_failure ?? 0),
		httpTimeout: cfg.http_timeout_seconds ?? 10,
		appSec: cfg.appsec_enabled ?? false,
		trustedProxies: (cfg.trusted_proxy_cidrs ?? []).join('\n'),
		clientTrusted: (cfg.client_trusted_cidrs ?? []).join('\n'),
		appSecURL: cfg.appsec_url ?? '',
		metricsURL: cfg.metrics_url ?? '',
		metricsToken: '',
		accessLogSource: cfg.access_log_source ?? '',
		protectedText: (cfg.protected_cidrs ?? []).join('\n'),
		readTimeout: cfg.read_timeout_seconds ?? 20,
		alertLimit: cfg.alert_limit ?? 500
	});
	const pluginMissing = $derived(status.plugin.state === 'missing');

	type Step = 'connection' | 'protection' | 'monitoring' | 'review';
	let step = $state<Step>('connection');
	let version = $state(0);
	let loading = $state(true);
	let loadError = $state('');
	let versions = $state<string[]>([]);
	let submitted = $state(false);
	let busy = $state(false);
	let error = $state('');
	let conflict = $state(false);
	let testing = $state(false);
	let probe = $state<ConnectionTest | null>(null);
	let testError = $state('');
	let review = $state<OperationPreview | null>(null);
	let applyKey = $state('');
	let uncertain = $state(false);
	let result = $state<Operation | null>(null);
	let manualRestart = $state(false);
	let dirty = $state(false);
	let copied = $state('');
	let suspended = $state(false);
	let disposed = false;
	const controller = new AbortController();

	const cidrs = (text: string) => parseCidrs(text);
	const badCidr = (text: string) => cidrs(text).some((v) => !v.includes('/') || !looksLikeIpOrCidr(v));
	const fields = $derived.by(() => {
		const out: Record<string, string> = {};
		if (!env('lapi_url') && !URL_RE.test(form.lapiURL.trim())) out.lapiURL = c('lapiURLInvalid');
		if (!form.generateKey && !keyPresent && !env('lapi_key') && !form.lapiKey.trim()) out.lapiKey = c('keyRequired');
		if (!form.generateMachine && !env('machine_id') && form.machinePassword && !form.machineID.trim()) out.machineID = c('machineIDRequired');
		if (!env('client_certificate') && !!form.clientCertificate.trim() !== !!form.clientKey.trim()) out.clientKey = c('clientPairRequired');
		if (form.protection === 'managed') {
			if (pluginMissing && form.pluginVersion && !STABLE.test(form.pluginVersion.trim())) out.pluginVersion = c('versionInvalid');
			if (form.httpTimeout < 1 || form.httpTimeout > 60) out.httpTimeout = c('httpTimeoutInvalid');
			if (badCidr(form.trustedProxies)) out.trustedProxies = c('cidrInvalid');
			if (badCidr(form.clientTrusted)) out.clientTrusted = c('cidrInvalid');
			if (form.appSec && !env('appsec_url') && !form.appSecURL.trim()) out.appSecURL = c('appSecURLRequired');
			else if (form.appSec && !env('appsec_url') && !URL_RE.test(form.appSecURL.trim())) out.appSecURL = c('urlInvalid');
		}
		if (!env('metrics_url') && form.metricsURL.trim() && !/^https?:\/\/[^/\s?#@]+(\/metrics)?\/?$/.test(form.metricsURL.trim())) out.metricsURL = c('metricsURLInvalid');
		if (!env('protected_cidrs') && badCidr(form.protectedText)) out.protected = c('cidrInvalid');
		if (!env('read_timeout_seconds') && (form.readTimeout < 1 || form.readTimeout > 120)) out.readTimeout = c('readTimeoutInvalid');
		if (!env('alert_limit') && (form.alertLimit < 1 || form.alertLimit > 1000)) out.alertLimit = c('alertLimitInvalid');
		return out;
	});
	const TAB_OF: Record<string, Step> = { lapiURL: 'connection', lapiKey: 'connection', machineID: 'connection', clientKey: 'connection', pluginVersion: 'protection', httpTimeout: 'protection', trustedProxies: 'protection', clientTrusted: 'protection', appSecURL: 'protection' };
	const tabOf = (key: string): Step => TAB_OF[key] ?? 'monitoring';
	const countOn = (tab: Step) => Object.keys(fields).filter((k) => tabOf(k) === tab).length;
	const tabs = $derived([
		{ id: 'connection', label: c('tab.connection'), errorCount: submitted ? countOn('connection') : 0, errorLabel: c('fixErrors') },
		{ id: 'protection', label: c('tab.protection'), errorCount: submitted ? countOn('protection') : 0, errorLabel: c('fixErrors') },
		{ id: 'monitoring', label: c('tab.monitoring'), errorCount: submitted ? countOn('monitoring') : 0, errorLabel: c('fixErrors') },
		{ id: 'review', label: c('tab.review') }
	]);
	const order: Step[] = ['connection', 'protection', 'monitoring', 'review'];
	const err = (key: string) => (submitted ? fields[key] ?? '' : '');
	const locked = $derived(busy || !!result);
	const serverErrors = $derived(review?.validation_errors ?? []);
	const configured = (values: unknown[]) => { const n = values.filter((v) => v !== '' && v !== false && v !== undefined).length; return n ? c('configuredCount', { count: n }) : c('notSet'); };

	$effect(() => {
		navigationGuard.setDirty(dirty && !result);
		return () => navigationGuard.setDirty(false);
	});

	// The unsaved-changes confirmation is a dialog of its own: this modal steps aside while it is open.
	onMount(() => navigationGuard.subscribe(() => (suspended = navigationGuard.confirmPending)));

	onMount(async () => {
		try {
			const current = await getConfig(controller.signal);
			if (!disposed) version = current.version ?? 0;
		} catch (e) {
			if (!disposed) loadError = errorMessage(e, c('loadFailed'));
		} finally {
			if (!disposed) loading = false;
		}
		if (!pluginMissing) return;
		try {
			const page = await readCatalogPage({ module: MODULE, limit: 1, offset: 0 }, controller.signal);
			if (disposed) return;
			const entry = page.items.find((i) => i.module_name.toLowerCase() === MODULE);
			versions = (entry ? knownVersions(entry).filter((v) => STABLE.test(v)) : []).toSorted((a, b) => b.localeCompare(a, undefined, { numeric: true }));
			if (!form.pluginVersion) form.pluginVersion = versions[0] ?? '';
		} catch {
			/* the version stays a text field */
		}
	});
	onDestroy(() => {
		disposed = true;
		controller.abort();
	});

	function touch() {
		dirty = true;
		error = '';
		conflict = false;
		review = null;
		applyKey = '';
		uncertain = false;
		probe = null;
	}

	function body(): ConnectionSettings {
		const out: ConnectionSettings = { lapi_url: form.lapiURL.trim() };
		if (!env('lapi_key')) {
			if (form.generateKey) {
				out.generate_key = true;
				out.lapi_key = form.generatedKey;
			} else if (form.lapiKey.trim()) out.lapi_key = form.lapiKey.trim();
		}
		if (form.generateMachine) {
			out.generate_machine = true;
			out.machine_password = form.generatedPassword;
		} else {
			if (!env('machine_id')) out.machine_id = form.machineID.trim();
			if (!env('machine_password') && form.machinePassword) out.machine_password = form.machinePassword;
		}
		if (!env('ca_certificate') && form.caCertificate.trim()) out.ca_certificate = form.caCertificate.trim();
		if (!env('client_certificate') && form.clientCertificate.trim()) {
			out.client_certificate = form.clientCertificate.trim();
			out.client_key = form.clientKey.trim();
		}
		if (!env('appsec_url')) out.appsec_url = form.appSecURL.trim();
		if (!env('metrics_url')) out.metrics_url = form.metricsURL.trim();
		if (!env('metrics_token') && form.metricsToken) out.metrics_token = form.metricsToken;
		if (!env('access_log_source')) out.access_log_source = form.accessLogSource.trim();
		if (!env('read_timeout_seconds')) out.read_timeout_seconds = form.readTimeout;
		if (!env('alert_limit')) out.alert_limit = form.alertLimit;
		if (!env('protected_cidrs')) out.protected_cidrs = cidrs(form.protectedText);
		out.protection = {
			mode: form.protection,
			bouncer_mode: form.bouncerMode,
			update_max_failure: Number(form.failurePolicy),
			http_timeout_seconds: form.httpTimeout,
			appsec_enabled: form.protection === 'managed' && form.appSec,
			trusted_proxy_cidrs: cidrs(form.trustedProxies),
			client_trusted_cidrs: cidrs(form.clientTrusted),
			...(pluginMissing && form.pluginVersion.trim() ? { plugin_version: form.pluginVersion.trim() } : {}),
			...(status.plugin.alias ? { plugin_alias: status.plugin.alias } : {}),
			...(status.middleware.name ? { middleware_name: status.middleware.name } : {})
		};
		return out;
	}

	/** Collapsible groups only open or close when the person (or an error to fix) asks: never on a value change. */
	let groups = $state({ machine: false, tls: false, bouncer: false, limits: false });
	const GROUP_OF: Record<string, keyof typeof groups> = { machineID: 'machine', clientKey: 'tls', httpTimeout: 'bouncer', trustedProxies: 'bouncer', clientTrusted: 'bouncer', protected: 'limits', readTimeout: 'limits', alertLimit: 'limits' };

	async function focusFirstError() {
		const key = Object.keys(fields)[0];
		if (!key) return;
		step = tabOf(key);
		for (const field of Object.keys(fields)) if (GROUP_OF[field]) groups[GROUP_OF[field]] = true;
		await tick();
		document.querySelector<HTMLElement>(`#panel-cs-${step} [aria-invalid="true"]`)?.focus();
	}

	async function selectStep(id: string) {
		if (busy || result || id === step) return;
		if (id === 'review') {
			await stageAndReview();
			return;
		}
		step = id as Step;
	}

	function describeFailure(e: unknown, fallback: string) {
		if (e instanceof ApiError && e.code === 'ERR_VERSION_CONFLICT') {
			conflict = true;
			return rv2('conflict');
		}
		const base = errorMessage(e, fallback);
		return e instanceof ApiError && e.operationId ? `${base} (${rv2('operation')} ${e.operationId})` : base;
	}

	async function stageAndReview() {
		submitted = true;
		if (Object.keys(fields).length) {
			await focusFirstError();
			return;
		}
		if (busy || loading) return;
		busy = true;
		error = '';
		try {
			const preview = await putConnection(body(), version, { dryRun: true, signal: controller.signal });
			if (disposed) return;
			review = preview.preview ?? null;
			applyKey = newIdempotencyKey();
			step = 'review';
		} catch (e) {
			if (!disposed) error = describeFailure(e, c('saveFailed'));
		} finally {
			if (!disposed) busy = false;
		}
	}

	async function apply() {
		if (!review || busy || serverErrors.length) return;
		busy = true;
		error = '';
		try {
			const saved = await putConnection(body(), version, { idempotencyKey: applyKey, signal: controller.signal });
			if (disposed) return;
			result = operationOf(saved, t('writeV2.noChange'));
			manualRestart = needsManualRestart(saved);
			version = saved.resource?.version ?? version;
			dirty = false;
			navigationGuard.setDirty(false);
			onchanged?.();
			if (announceApplied(result, manualRestart, c('subject'))) navigate('/crowdsec');
		} catch (e) {
			if (disposed) return;
			if (e instanceof NetworkError) {
				uncertain = true;
				error = rv2('applyUncertain');
			} else {
				if (e instanceof ApiError && [404, 409, 410, 412, 422, 428].includes(e.status)) {
					review = null;
					applyKey = '';
					step = 'connection';
				}
				error = describeFailure(e, c('saveFailed'));
			}
		} finally {
			if (!disposed) busy = false;
		}
	}

	async function runTest() {
		submitted = true;
		if (fields.lapiURL || testing) return;
		testing = true;
		testError = '';
		try {
			const res = await testConnection(body(), controller.signal);
			if (!disposed) probe = res;
		} catch (e) {
			if (!disposed) testError = errorMessage(e, c('testFailed'));
		} finally {
			if (!disposed) testing = false;
		}
	}

	function close() {
		if (busy) return;
		navigationGuard.requestNavigation(() => navigate('/crowdsec'));
	}

	async function copy(text: string) {
		try {
			await navigator.clipboard.writeText(text);
			copied = text;
			setTimeout(() => { if (copied === text) copied = ''; }, 2000);
		} catch {
			copied = '';
		}
	}

	function createKey() {
		form.generateKey = true;
		form.generatedKey = newSecret();
		touch();
	}
	function undoKey() {
		form.generateKey = false;
		form.generatedKey = '';
		touch();
	}
	function createMachine() {
		form.generateMachine = true;
		groups.machine = true;
		form.generatedPassword = newSecret();
		touch();
	}
	function undoMachine() {
		form.generateMachine = false;
		form.generatedPassword = '';
		touch();
	}
	/** Only the parts that are set up are reported; the Local API always is. */
	const probeRows = $derived(probe ? ([['lapi', probe.lapi], ['machine', probe.machine], ['appsec', probe.appsec], ['metrics', probe.metrics]] as const).filter(([id, state]) => id === 'lapi' || state !== 'not_configured') : []);
	const probeTone = $derived(probeRows.every(([, state]) => testTone(state) === 'success') ? 'success' : probeRows.some(([, state]) => testTone(state) === 'danger') ? 'danger' : 'warning');
</script>

{#snippet command(text: string)}
	<div class="command">
		<code>{text}</code>
		<Button size="sm" variant="ghost" onclick={() => copy(text)}><Copy size={16} aria-hidden="true" /> {copied === text ? c('copied') : c('copy')}</Button>
	</div>
{/snippet}

{#snippet text(id: string, key: string, field: string, value: string, set: (v: string) => void, label: string, helpText = '', placeholder = '', type = 'text')}
	<Field {id} {label} help={help(field, helpText)} error={err(key)}>
		{#snippet children(f)}<input id={f.id} {type} value={value} oninput={(e) => { set(e.currentTarget.value); touch(); }} disabled={locked || !!env(field)} aria-invalid={f.invalid || undefined} aria-describedby={f.describedBy} {placeholder} autocomplete="off" spellcheck="false" />{/snippet}
	</Field>
{/snippet}

{#snippet area(id: string, key: string, field: string, value: string, set: (v: string) => void, label: string, helpText = '', placeholder = '')}
	<Field {id} {label} help={help(field, helpText)} error={err(key)}>
		{#snippet children(f)}<textarea id={f.id} rows="3" value={value} oninput={(e) => { set(e.currentTarget.value); touch(); }} disabled={locked || !!env(field)} aria-invalid={f.invalid || undefined} aria-describedby={f.describedBy} {placeholder} spellcheck="false"></textarea>{/snippet}
	</Field>
{/snippet}

<ResourceDialog title={c('title')} description={c('description')} closeLabel={t('common.close')} onclose={close} {suspended}>
	{#snippet icon()}<ShieldCheck size={32} aria-hidden="true" />{/snippet}
	{#snippet navigation()}<Tabs {tabs} selected={step} onselect={selectStep} label={c('title')} />{/snippet}

	<div class="stack">
		{#if loadError}<Notice tone="error" message={loadError} />{/if}
		{#if error}
			<Notice tone="error" message={error}>
				{#if conflict}<Button size="sm" onclick={() => { navigationGuard.setDirty(false); window.location.reload(); }}>{t('crowdsecV2.setup.reloadLatest')}</Button>{/if}
			</Notice>
		{/if}
		{#if submitted && Object.keys(fields).length && step !== 'review'}
			<div class="validation-banner" role="alert">
				<CircleAlert size={16} aria-hidden="true" />
				<span>{c('fixFieldsCount', { count: Object.keys(fields).length })}</span>
				<button type="button" onclick={focusFirstError}>{rv2('goToField')}</button>
			</div>
		{/if}

		{#if probe}
			<div class="probe tone-{probeTone}" role="status">
				<PlugZap size={16} aria-hidden="true" />
				<span class="probe-title">{c('testTitle')}</span>
				<ul>
					{#each probeRows as [id, state] (id)}
						<li><span>{c('probe.' + id)}</span><StatusBadge size="sm" tone={testTone(state)} text={c('result.' + state)} /></li>
					{/each}
				</ul>
				<button type="button" class="probe-close" aria-label={t('common.close')} onclick={() => (probe = null)}><X size={14} aria-hidden="true" /></button>
			</div>
		{/if}
		{#if testError}<Notice tone="error" message={testError} />{/if}

		<div id="panel-cs-connection" role="tabpanel" aria-labelledby="tab-connection" hidden={step !== 'connection'} class="stack">
			{@render text('cs-lapi-url', 'lapiURL', 'lapi_url', form.lapiURL, (v) => (form.lapiURL = v), c('lapiURL'), c('lapiURLHelp'), 'http://crowdsec:8080')}
			{#if env('lapi_key')}
				<Field id="cs-key-env" label={c('bouncerKey')} help={help('lapi_key')}>
					{#snippet children(f)}<input id={f.id} type="password" value="••••••••" disabled aria-describedby={f.describedBy} />{/snippet}
				</Field>
			{:else}
				<Field id="cs-lapi-key" label={c('bouncerKey')} help={c(form.generateKey ? 'keyGeneratedHelp' : keyPresent ? 'keyKeepHelp' : 'keyFieldHelp')} error={err('lapiKey')}>
					{#snippet children(f)}
						{#if form.generateKey}
							<input id={f.id} type="password" value={form.generatedKey} readonly aria-describedby={f.describedBy} />
						{:else}
							<input id={f.id} type="password" value={form.lapiKey} oninput={(e) => { form.lapiKey = e.currentTarget.value; touch(); }} disabled={locked} aria-invalid={f.invalid || undefined} aria-describedby={f.describedBy} placeholder={keyPresent ? '••••••••' : ''} autocomplete="off" spellcheck="false" />
						{/if}
						{#if canGenerate}
							<div class="field-actions">
								{#if form.generateKey}
									<Button size="sm" onclick={undoKey} disabled={locked}><Undo2 size={16} aria-hidden="true" /> {c('undo')}</Button>
								{:else}
									<Button size="sm" onclick={createKey} disabled={locked}><KeyRound size={16} aria-hidden="true" /> {c(keyPresent ? 'createNewKey' : 'createKey')}</Button>
								{/if}
							</div>
						{/if}
					{/snippet}
				</Field>
				{#if !canGenerate && !keyPresent}
					<div class="hint">
						<p class="muted">{c(status.engine.state === 'running' ? 'keyManualHelp' : 'keyNoContainerHelp')}</p>
						{@render command(bouncerCommand(status.runtime))}
					</div>
				{/if}
			{/if}
			<details class="options" bind:open={groups.machine}>
				<summary>{c('machineTitle')} <span>{form.generateMachine ? c('machinePlannedShort') : configured([form.machineID])}</span></summary>
				<div class="option-fields">
					<p class="muted">{c(form.generateMachine ? 'machinePlannedHelp' : 'machineHelp')}</p>
					<Field id="cs-machine-id" label={c('machineID')} help={help('machine_id')} error={err('machineID')}>
						{#snippet children(f)}<input id={f.id} value={form.generateMachine ? '' : form.machineID} placeholder={form.generateMachine ? c('machineIDAssigned') : 'traefik-console-reader'} readonly={form.generateMachine} oninput={(e) => { form.machineID = e.currentTarget.value; touch(); }} disabled={locked || !!env('machine_id')} aria-invalid={f.invalid || undefined} aria-describedby={f.describedBy} autocomplete="off" spellcheck="false" />{/snippet}
					</Field>
					<Field id="cs-machine-password" label={c('machinePassword')} help={form.generateMachine ? c('machineGeneratedHelp') : help('machine_password')}>
						{#snippet children(f)}
							{#if form.generateMachine}
								<input id={f.id} type="password" value={form.generatedPassword} readonly aria-describedby={f.describedBy} />
							{:else}
								<input id={f.id} type="password" value={form.machinePassword} oninput={(e) => { form.machinePassword = e.currentTarget.value; touch(); }} disabled={locked || !!env('machine_password')} aria-describedby={f.describedBy} autocomplete="off" placeholder={status.sources?.machine !== 'none' ? '••••••••' : ''} />
							{/if}
							{#if canCreateMachine}
								<div class="field-actions">
									{#if form.generateMachine}
										<Button size="sm" onclick={undoMachine} disabled={locked}><Undo2 size={16} aria-hidden="true" /> {c('undo')}</Button>
									{:else}
										<Button size="sm" onclick={createMachine} disabled={locked}><UserPlus size={16} aria-hidden="true" /> {c(status.machine?.state === 'ok' ? 'createNewMachine' : 'createMachine')}</Button>
									{/if}
								</div>
							{/if}
						{/snippet}
					</Field>
					{#if !env('machine_id') && !accessible}<p class="muted">{c('machineHow')}</p>{@render command(machineCommand(status.runtime))}{/if}
				</div>
			</details>
			<details class="options" bind:open={groups.tls}>
				<summary>{c('tlsTitle')} <span>{configured([status.sources?.ca_certificate !== 'none' ? 'x' : '', status.sources?.client_certificate !== 'none' ? 'x' : ''])}</span></summary>
				<div class="option-fields">
					{@render area('cs-ca', 'ca', 'ca_certificate', form.caCertificate, (v) => (form.caCertificate = v), c('caCertificate'), c('caCertificateHelp'), status.sources?.ca_certificate === 'saved' ? c('storedReplace') : '-----BEGIN CERTIFICATE-----')}
					{@render area('cs-client-cert', 'clientCertificate', 'client_certificate', form.clientCertificate, (v) => (form.clientCertificate = v), c('clientCertificate'), c('clientCertificateHelp'), status.sources?.client_certificate === 'saved' ? c('storedReplace') : '-----BEGIN CERTIFICATE-----')}
					{@render area('cs-client-key', 'clientKey', 'client_key', form.clientKey, (v) => (form.clientKey = v), c('clientKey'), '', '-----BEGIN PRIVATE KEY-----')}
				</div>
			</details>
		</div>

		<div id="panel-cs-protection" role="tabpanel" aria-labelledby="tab-protection" hidden={step !== 'protection'} class="stack">
			<Field id="cs-protection" label={c('protection')} help={c('protectionHelp.' + form.protection)}>
				{#snippet children(f)}<SelectMenu id={f.id} value={form.protection} disabled={locked} options={[{ value: 'off', label: c('protectionMode.off') }, { value: 'managed', label: c('protectionMode.managed') }, { value: 'external', label: c('protectionMode.external') }]} onchange={(v) => { form.protection = v as ProtectionMode; touch(); }} />{/snippet}
			</Field>
			{#if form.protection === 'external'}
				<!-- Your own middleware: Console provides the credentials, you reference the key file. -->
				<div class="nested stack">
					<p class="muted">{c('externalReviewPointer')}</p>
					<p class="muted">{c('externalMachineNote')}</p>
				</div>
			{/if}
			{#if form.protection === 'managed'}
				<div class="nested">
					<Field id="cs-bouncer-mode" label={c('bouncerMode')} help={c('bouncerModeHelp.' + form.bouncerMode)}>
						{#snippet children(f)}<SelectMenu id={f.id} value={form.bouncerMode} disabled={locked} options={[{ value: 'stream', label: c('bouncerModeOption.stream') }, { value: 'live', label: c('bouncerModeOption.live') }]} onchange={(v) => { form.bouncerMode = v; touch(); }} />{/snippet}
					</Field>
					<Field id="cs-failure" label={c('failurePolicy')} help={c('failurePolicyHelp')}>
						{#snippet children(f)}<SelectMenu id={f.id} value={form.failurePolicy} disabled={locked} options={[{ value: '0', label: c('failClosed') }, { value: '-1', label: c('failOpen') }]} onchange={(v) => { form.failurePolicy = v; touch(); }} />{/snippet}
					</Field>
					{#if pluginMissing}
						<Field id="cs-version" label={c('pluginVersion')} help={c('pluginVersionHelp')} error={err('pluginVersion')}>
							{#snippet children(f)}
								{#if versions.length}
									<SelectMenu id={f.id} value={form.pluginVersion} disabled={locked} options={versions.map((v, i) => ({ value: v, label: i === 0 ? `${v} · ${c('latest')}` : v }))} onchange={(v) => { form.pluginVersion = v; touch(); }} />
								{:else}
									<input id={f.id} bind:value={form.pluginVersion} oninput={touch} disabled={locked} aria-invalid={f.invalid || undefined} aria-describedby={f.describedBy} placeholder="v1.8.2" autocomplete="off" spellcheck="false" />
								{/if}
							{/snippet}
						</Field>
					{:else}
						<SummaryList>
							<div><dt>{c('pluginVersion')}</dt><dd>{status.plugin.alias} · {status.plugin.version || '—'}</dd></div>
							{#if status.middleware.name}<div><dt>{c('middleware')}</dt><dd>{status.middleware.name}</dd></div>{/if}
						</SummaryList>
					{/if}
					<Checkbox id="cs-appsec" checked={form.appSec} label={c('appSec')} help={c('appSecHelp')} disabled={locked} onchange={(v) => { form.appSec = v; touch(); }} />
					{#if form.appSec}
						<div class="nested">
							{@render text('cs-appsec-url', 'appSecURL', 'appsec_url', form.appSecURL, (v) => (form.appSecURL = v), c('appSecURL'), c('appSecURLHelp'), 'http://crowdsec:7422')}
						</div>
					{/if}
					<details class="options" bind:open={groups.bouncer}>
						<summary>{c('bouncerAdvanced')}</summary>
						<div class="option-fields">
							<Field id="cs-http-timeout" label={c('httpTimeout')} help={c('httpTimeoutHelp')} error={err('httpTimeout')}>
								{#snippet children(f)}<input id={f.id} type="number" min="1" max="60" bind:value={form.httpTimeout} oninput={touch} disabled={locked} aria-invalid={f.invalid || undefined} aria-describedby={f.describedBy} />{/snippet}
							</Field>
							{@render area('cs-proxies', 'trustedProxies', '', form.trustedProxies, (v) => (form.trustedProxies = v), c('trustedProxies'), c('trustedProxiesHelp'), '172.18.0.0/16')}
							{@render area('cs-client-trusted', 'clientTrusted', '', form.clientTrusted, (v) => (form.clientTrusted = v), c('clientTrusted'), c('clientTrustedHelp'), '10.0.0.0/8')}
						</div>
					</details>
				</div>
			{/if}
		</div>

		<div id="panel-cs-monitoring" role="tabpanel" aria-labelledby="tab-monitoring" hidden={step !== 'monitoring'} class="stack">
			{@render text('cs-metrics-url', 'metricsURL', 'metrics_url', form.metricsURL, (v) => (form.metricsURL = v), c('metricsURL'), c('metricsURLHelp'), 'http://crowdsec:6060/metrics')}
			{@render text('cs-metrics-token', 'metricsToken', 'metrics_token', form.metricsToken, (v) => (form.metricsToken = v), c('metricsToken'), c('metricsTokenHelp'), cfg.metrics_token_set ? '••••••••' : '', 'password')}
			{@render text('cs-access-log', 'accessLogSource', 'access_log_source', form.accessLogSource, (v) => (form.accessLogSource = v), c('accessLogSource'), c('accessLogSourceHelp'), '/var/log/traefik/access.log')}
			<details class="options" bind:open={groups.limits}>
				<summary>{c('limitsTitle')}</summary>
				<div class="option-fields">
					{@render area('cs-protected', 'protected', 'protected_cidrs', form.protectedText, (v) => (form.protectedText = v), c('protectedCIDRs'), c('protectedCIDRsHelp'), '127.0.0.0/8')}
					<div class="pair">
						<Field id="cs-read-timeout" label={c('readTimeout')} help={help('read_timeout_seconds', c('readTimeoutHelp'))} error={err('readTimeout')}>
							{#snippet children(f)}<input id={f.id} type="number" min="1" max="120" bind:value={form.readTimeout} oninput={touch} disabled={locked || !!env('read_timeout_seconds')} aria-invalid={f.invalid || undefined} aria-describedby={f.describedBy} />{/snippet}
						</Field>
						<Field id="cs-alert-limit" label={c('alertLimit')} help={help('alert_limit', c('alertLimitHelp'))} error={err('alertLimit')}>
							{#snippet children(f)}<input id={f.id} type="number" min="1" max="1000" bind:value={form.alertLimit} oninput={touch} disabled={locked || !!env('alert_limit')} aria-invalid={f.invalid || undefined} aria-describedby={f.describedBy} />{/snippet}
						</Field>
					</div>
				</div>
			</details>
		</div>

		<div id="panel-cs-review" role="tabpanel" aria-labelledby="tab-review" hidden={step !== 'review'} class="stack">
			{#if result}
				<Notice tone={operationTone(result.status)} title={t('operationV2.status.' + result.status)} message={result.summary} />
				{#if manualRestart}<Notice tone="warning" message={t('writeV2.manualRestart')} />{/if}
				{#if form.protection === 'external'}{@render outsideBouncer()}{/if}
			{:else if review}
				<SummaryList>
					<div><dt>{c('lapiURL')}</dt><dd>{form.lapiURL || '—'}</dd></div>
					<div><dt>{c('bouncerKey')}</dt><dd>{env('lapi_key') ? c('storedInEnvironment') : form.generateKey ? c('keyOption.generate') : form.lapiKey.trim() ? c('keyOption.enter') : c('keyOption.keep')}</dd></div>
					{#if form.generateMachine}<div><dt>{c('machineTitle')}</dt><dd>{c('machinePlannedShort')}</dd></div>{/if}
					<div><dt>{c('protection')}</dt><dd>{c('protectionMode.' + form.protection)}</dd></div>
					<div><dt>{rv2('pendingTotal')}</dt><dd>{review.changes?.length ?? 0}</dd></div>
				</SummaryList>
				{#if serverErrors.length}
					<Notice tone="error" title={rv2('serverErrors')}><ul class="reasons">{#each serverErrors as ve}<li>{ve.item_id}{ve.field ? ' · ' + ve.field : ''}: {ve.message}</li>{/each}</ul></Notice>
				{/if}
				{#if form.protection === 'external'}{@render outsideBouncer()}{/if}
				{#if review.requires_restart}<Notice tone="warning" message={c('restartHelp')} />{/if}
				{#if review.static_diff}<CodePreview label={rv2('diff.static')} code={review.static_diff} language="diff" />{/if}
				{#if review.dynamic_diff}<CodePreview label={rv2('diff.dynamic')} code={review.dynamic_diff} language="diff" />{/if}
				{#if !review.static_diff && !review.dynamic_diff}<Notice tone="info" message={c('noFileChanges')} />{/if}
			{:else}
				<p role="status">{rv2('staging')}</p>
			{/if}
		</div>
	</div>

	{#snippet footer()}
		<Button onclick={close} disabled={busy}>{result ? t('common.close') : t('common.cancel')}</Button>
		{#if !result}
			{#if step !== 'review'}
				<Button onclick={runTest} pending={testing} disabled={testing || busy || loading}><PlugZap size={18} aria-hidden="true" /> {c('test')}</Button>
			{:else}
				<Button onclick={() => (step = 'monitoring')} disabled={busy}>{t('domainUI.back')}</Button>
			{/if}
			{#if step === 'review'}
				{#if review}<Button variant="primary" onclick={apply} pending={busy} disabled={busy || serverErrors.length > 0}>{t('domainUI.' + (busy ? 'applying' : uncertain ? 'retryApply' : 'apply'))}</Button>{/if}
			{:else if step === 'monitoring'}
				<Button variant="primary" onclick={stageAndReview} pending={busy} disabled={busy || loading}>{rv2('stageReview')}</Button>
			{:else}
				<Button variant="primary" onclick={() => (step = order[order.indexOf(step) + 1])} disabled={busy}>{t('domainUI.continue')}</Button>
			{/if}
		{/if}
	{/snippet}
</ResourceDialog>

<!-- What a bouncer middleware managed outside the Console needs: the key file, never machine credentials. -->
{#snippet outsideBouncer()}
	<section class="stack" aria-label={c('externalKeySnippet')}>
		{#if env('lapi_key')}
			<Notice tone="info" title={c('externalReviewTitle')} message={c('externalKeyFromEnv')} />
		{:else if status.key_file && (status.key.present || form.generateKey || form.lapiKey.trim())}
			<Notice tone="info" title={c('externalReviewTitle')} message={c('externalKeyHelp')} />
			<CodePreview label={c('externalKeySnippet')} code={externalKeySnippet(status.key_file, form.appSec)} language="yaml" />
			<p class="muted">{c('externalMachineNote')}</p>
			<p class="muted">{c('externalKeyRotate')}</p>
		{:else}
			<Notice tone="warning" title={c('externalReviewTitle')} message={c('externalKeyMissing')} />
		{/if}
	</section>
{/snippet}

<style>
	.stack { display: flex; flex-direction: column; gap: 20px; }
	.stack[hidden] { display: none; }
	.muted { color: var(--text-muted); font-size: 13px; line-height: 19px; margin: 0; }
	.nested { display: flex; flex-direction: column; gap: 20px; box-sizing: border-box; max-width: 560px; padding-left: 16px; border-left: 2px solid var(--border); margin-left: 9px; }
	.nested :global(.field) { max-width: none; }
	.pair { display: grid; grid-template-columns: repeat(2, minmax(0, 1fr)); gap: 16px; max-width: 560px; }
	.options { border-top: 1px solid var(--border); padding-top: 16px; max-width: 560px; }
	.options summary { cursor: pointer; font-size: 14px; font-weight: 500; line-height: 24px; color: var(--text); }
	.options summary span { color: var(--text-muted); font-weight: 400; margin-left: 8px; }
	.options summary span::before { content: '·'; margin-right: 8px; }
	.option-fields { display: flex; flex-direction: column; gap: 20px; margin-top: 16px; }
	.hint { display: grid; gap: 8px; }
	.field-actions { display: flex; flex-wrap: wrap; gap: 8px; margin-top: 8px; }
	.command { display: flex; align-items: center; gap: 8px; justify-content: space-between; padding: 4px 4px 4px 12px; border: 1px solid var(--border); border-radius: var(--radius-control); background: var(--surface-muted); max-width: 560px; }
	.command code { font-size: 12px; overflow-wrap: anywhere; }
	.probe { display: flex; align-items: center; flex-wrap: wrap; gap: 8px 14px; padding: 10px 12px 10px 14px; border-radius: var(--radius-card); border: 1px solid var(--border); background: var(--surface-muted); font-size: 13px; line-height: 18px; }
	.probe.tone-success { border-color: color-mix(in srgb, var(--success-text) 30%, transparent); background: var(--success-bg); }
	.probe.tone-danger { border-color: color-mix(in srgb, var(--danger-text) 30%, transparent); background: var(--danger-bg); }
	.probe.tone-warning { border-color: color-mix(in srgb, var(--warning-text) 30%, transparent); background: var(--warning-bg); }
	.probe-title { font-weight: 600; }
	.probe ul { display: flex; flex-wrap: wrap; gap: 6px 16px; list-style: none; margin: 0; padding: 0; flex: 1; }
	.probe li { display: inline-flex; align-items: center; gap: 6px; color: var(--text-muted); }
	.probe-close { display: grid; place-items: center; width: 24px; height: 24px; border: 0; border-radius: 4px; background: transparent; color: var(--text-muted); cursor: pointer; }
	.probe-close:hover { background: color-mix(in srgb, var(--text) 8%, transparent); color: var(--text); }
	.reasons { margin: 8px 0 0; padding-left: 20px; }
	.validation-banner { display: flex; align-items: center; gap: 8px; padding: 10px 14px; border: 1px solid color-mix(in srgb, var(--danger-text) 30%, transparent); border-radius: var(--radius-card); background: var(--danger-bg); color: var(--danger-text); font-size: 13px; line-height: 18px; }
	.validation-banner span { flex: 1; }
	.validation-banner button { border: 0; background: transparent; color: inherit; font: inherit; font-weight: 600; cursor: pointer; padding: 2px 4px; }
	@media (max-width: 767px) { .pair { grid-template-columns: 1fr; } }
</style>

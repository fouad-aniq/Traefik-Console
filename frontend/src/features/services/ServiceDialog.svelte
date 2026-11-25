<script lang="ts">
	/**
	 * Service dialog (create / edit / view) on the direct write protocol, for HTTP, TCP and UDP.
	 *
	 *   Identity tab → name (and, for HTTP, the Host header behaviour)
	 *   Backends tab (TCP and UDP: also the name, as one "Service" tab) → backend servers (URLs for HTTP, host:port for TCP/UDP), HTTP health check and TLS
	 *   Options tab  → TCP only: TLS to backends, PROXY protocol, health check, connection timeouts
	 *   Review tab   → PUT /services/{name}?dry_run=true + If-Match returns the preview (nothing saved)
	 *   Apply        → the same PUT without dry_run, with one Idempotency-Key per attempt
	 *
	 * View mode shows the definition as YAML and a read-only runtime check (GET …/diagnostics?protocol=).
	 */
	import { onMount, onDestroy, tick } from 'svelte';
	import { Network, CircleAlert, Plus } from '@lucide/svelte';
	import { stringify } from 'yaml';
	import ResourceDialog from '../../shared/ui/ResourceDialog.svelte';
	import Field from '../../shared/ui/Field.svelte';
	import Tabs from '../../shared/ui/Tabs.svelte';
	import Button from '../../shared/ui/Button.svelte';
	import Notice from '../../shared/ui/Notice.svelte';
	import Checkbox from '../../shared/ui/Checkbox.svelte';
	import SelectMenu from '../../shared/ui/SelectMenu.svelte';
	import BackendServer from './BackendServer.svelte';
	import SummaryList from '../../shared/ui/SummaryList.svelte';
	import CodePreview from '../../shared/ui/CodePreview.svelte';
	import RelativeTime from '../../shared/ui/RelativeTime.svelte';
	import StatusBadge from '../../shared/ui/StatusBadge.svelte';
	import { navigationGuard } from '../../lib/stores/navigationGuard';
	import { needsManualRestart, newIdempotencyKey, operationOf, announceApplied } from '../../core/mutations';
	import { operationTone } from '../changes/models';
	import type { Operation, OperationPreview } from '../../core/types';
	import { diagnoseService, listServices, putService } from './api';
	import { BASE, MAX_SERVERS, formFrom, newForm, protocolOf, serviceDefinition, toInput, transportOf, type ServiceDiagnostic, type ServiceForm, type ServiceProtocol, type ServiceResource } from './models';
	import { serviceErrors } from './validation';
	import { ApiError, NetworkError } from '../../shared/api/client';
	import { errorMessage } from '../../shared/api/errorMessage';
	import { t } from '../../shared/localization/i18n';

	let { mode = 'create', name = '', protocol = 'http', services = undefined, listError = '', navigate, onchanged }: {
		mode?: string; name?: string; protocol?: ServiceProtocol;
		/** The page's list (every protocol): the dialog reads names and the service to edit from it instead of asking again. `null` while the page is still loading; left out, the dialog reads the list itself (the map opens it that way). */
		services?: ServiceResource[] | null; listError?: string;
		navigate: (p: string) => void; onchanged?: () => void
	} = $props();

	const s = (key: string, args?: Record<string, string | number>) => t('services.' + key, args);
	const sv = (key: string, args?: Record<string, string | number>) => t('serviceV2.' + key, args);
	const rv2 = (key: string, args?: Record<string, string | number>) => t('resolverV2.' + key, args);

	const creating = $derived(mode === 'create');
	const viewing = $derived(mode === 'view');
	const http = $derived(protocol === 'http');
	const tcp = $derived(protocol === 'tcp');
	const parent = $derived(BASE[protocol]);

	// svelte-ignore state_referenced_locally
	let form = $state<ServiceForm>(newForm(protocol));
	let existing = $state<ServiceResource[]>([]);
	let loading = $state(true);
	let loadError = $state('');
	let busy = $state(false);
	let suspended = $state(false);
	let error = $state('');
	// TCP and UDP services have nothing to configure beyond a name and servers: one tab holds both.
	// svelte-ignore state_referenced_locally
	let step = $state(protocol === 'http' ? 'service-identity' : 'service-backends');
	// svelte-ignore state_referenced_locally
	const firstStep = protocol === 'http' ? 'service-identity' : 'service-backends';
	let baseline = $state('');
	let submitted = $state(false);
	let fields = $state<Record<string, string>>({});

	let conflict = $state(false);
	let staged = $state<ServiceResource | null>(null);
	let review = $state<OperationPreview | null>(null);
	let reviewError = $state('');
	let applyKey = $state('');
	let uncertain = $state(false);
	let result = $state<Operation | null>(null);
	let manualRestart = $state(false);

	let diagnostic = $state<ServiceDiagnostic | null>(null);
	let diagnosing = $state(false);
	let diagnosticError = $state('');

	let disposed = false;
	const controller = new AbortController();

	const current = $derived(existing.find((d) => d.name === name && protocolOf(d.config) === protocol));
	const version = () => current?.version ?? 0;
	const editable = $derived(!viewing && (creating || (!!current && !current.read_only)));
	const locked = $derived(busy || !!result);
	const serverErrors = $derived(review?.validation_errors ?? []);
	const dirty = $derived(editable && !!baseline && JSON.stringify($state.snapshot(form)) !== baseline && !result);
	/** Managed names are unique whatever the protocol (one console item per name). */
	const takenNames = $derived(existing.filter((d) => d.origin === 'managed').map((d) => d.name));

	const messages = $derived({
		invalidName: s('invalidName'), nameTaken: sv('nameTaken'), invalidUrl: s('invalidUrl'), urlQuery: sv('urlQuery'), urlScheme: sv('urlScheme'),
		serverCount: s('serverCount'), duplicateUrl: sv('duplicateUrl'), invalidPath: sv('invalidPath'), invalidDuration: sv('invalidDuration'),
		invalidAddress: sv('invalidAddress'), invalidPort: s('invalidPort'), invalidServerName: sv('invalidServerName'), expectNeedsSend: sv('expectNeedsSend'), invalidPayload: sv('invalidPayload')
	});

	/** Which tab holds each field, so errors are counted and focused on the right one. */
	const OPTION_FIELDS = ['serverName', 'dialTimeout', 'terminationDelay', 'healthPort', 'healthSend', 'healthExpect'];
	const tabOf = (key: string) => key === 'name' ? firstStep : tcp && (OPTION_FIELDS.includes(key) || key.startsWith('health')) ? 'service-options' : 'service-backends';
	const countOn = (tab: string) => Object.keys(fields).filter((k) => tabOf(k) === tab).length;
	const tabs = $derived([
		...(http ? [{ id: 'service-identity', label: s('identity'), errorCount: submitted ? countOn('service-identity') : 0, errorLabel: sv('fixErrors') }] : []),
		{ id: 'service-backends', label: http ? s('backends') : sv('serviceTab'), errorCount: submitted ? countOn('service-backends') : 0, errorLabel: sv('fixErrors') },
		...(tcp ? [{ id: 'service-options', label: sv('optionsTab'), errorCount: submitted ? countOn('service-options') : 0, errorLabel: sv('fixErrors') }] : []),
		{ id: 'service-review', label: s('review') }
	]);
	const order = $derived(tabs.map((x) => x.id));
	const previousStep = $derived(order[order.indexOf(step) - 1]);

	$effect(() => {
		navigationGuard.setDirty(dirty);
		return () => navigationGuard.setDirty(false);
	});

	onMount(() => {
		const unsub = navigationGuard.subscribe(() => (suspended = navigationGuard.confirmPending));
		return unsub;
	});
	onDestroy(() => { disposed = true; controller.abort(); });

	// The list comes with the page: nothing is fetched here until a write needs a fresh read after a conflict.
	let initialised = false;
	$effect(() => {
		if (initialised) return;
		if (services) { initialised = true; init(services); }
		else if (listError) { initialised = true; loadError = listError; loading = false; }
		else if (services === undefined) { initialised = true; void readList(); }
	});

	/** Only for a dialog opened without a page list. */
	async function readList() {
		try {
			init(await listServices(controller.signal));
		} catch (e) {
			if (disposed) return;
			loadError = errorMessage(e, s('requestFailed')); loading = false;
		}
	}

	/** The page owns the list: ask it to read again, then start over from what it returns. */
	function retry() {
		loadError = ''; loading = true;
		if (services === undefined) { initialised = true; void readList(); return; }
		initialised = false; onchanged?.();
	}

	function init(all: ServiceResource[]) {
		try {
			existing = all;
			if (!creating) {
				const item = all.find((d) => d.name === name && protocolOf(d.config) === protocol);
				if (!item) throw new Error(s('requestFailed'));
				form = formFrom(item.name, item.config);
				if (mode === 'edit') step = 'service-backends';
			}
			baseline = JSON.stringify($state.snapshot(form));
		} catch (e) {
			loadError = errorMessage(e, s('requestFailed'));
		} finally {
			loading = false;
		}
	}

	const errorsNow = () => serviceErrors($state.snapshot(form), { creating, existingNames: takenNames }, messages);

	function invalidate() {
		review = null; staged = null; reviewError = ''; applyKey = ''; uncertain = false; error = ''; conflict = false;
		if (submitted) fields = errorsNow();
	}

	function setServer(index: number, value: string) { form.servers[index] = value; invalidate(); }
	function removeServer(index: number) { form.servers = form.servers.filter((_, i) => i !== index); invalidate(); }
	function addServer() { if (form.servers.length < MAX_SERVERS) { form.servers = [...form.servers, '']; invalidate(); } }

	function validate(): boolean {
		submitted = true;
		fields = errorsNow();
		return Object.keys(fields).length === 0;
	}

	async function focusFirstError() {
		const key = Object.keys(fields)[0];
		if (!key) return;
		step = tabOf(key);
		await tick();
		const target = document.querySelector<HTMLElement>(`#panel-${step} [aria-invalid="true"]`);
		target?.focus();
		target?.scrollIntoView({ block: 'nearest' });
	}

	async function selectStep(id: string) {
		if (busy || result || id === step) return;
		if (id === 'service-review') { await stageAndReview(); return; }
		if (step === firstStep && id !== firstStep) {
			submitted = true;
			const all = errorsNow();
			const { name: _n, ...rest } = fields;
			fields = all.name ? { ...rest, name: all.name } : rest;
			if (all.name) { await focusFirstError(); return; }
		}
		step = id;
	}

	function describeFailure(e: unknown, fallback: string) {
		if (e instanceof ApiError && e.code === 'ERR_VERSION_CONFLICT') { conflict = true; return sv('conflict'); }
		const base = errorMessage(e, fallback);
		return e instanceof ApiError && e.operationId ? `${base} (${rv2('operation')} ${e.operationId})` : base;
	}

	async function stageAndReview() {
		if (busy || !editable) return;
		if (!validate()) { await focusFirstError(); return; }
		busy = true; error = ''; conflict = false;
		try {
			const preview = await putService(form.name, toInput($state.snapshot(form)), version(), { dryRun: true, signal: controller.signal });
			if (disposed) return;
			step = 'service-review';
			review = preview.preview ?? null; applyKey = newIdempotencyKey();
		} catch (e) {
			if (disposed) return;
			if (e instanceof ApiError && (e.status === 422 || e.status === 400)) step = 'service-backends';
			error = describeFailure(e, sv('stageFailed'));
		} finally {
			if (!disposed) busy = false;
		}
	}

	async function refreshVersion() {
		busy = true;
		try {
			existing = await listServices(controller.signal);
			conflict = false; error = '';
		} catch (e) {
			error = describeFailure(e, rv2('reviewFailed'));
		} finally {
			if (!disposed) busy = false;
		}
	}

	async function apply() {
		if (!review || busy || serverErrors.length) return;
		busy = true; error = '';
		try {
			const saved = await putService(form.name, toInput($state.snapshot(form)), version(), { idempotencyKey: applyKey, signal: controller.signal });
			if (disposed) return;
			result = operationOf(saved, t('writeV2.noChange')); manualRestart = needsManualRestart(saved); uncertain = false; staged = saved.resource ?? null;
			navigationGuard.setDirty(false);
			onchanged?.();
			// Applied: back to the table; the outcome is announced as a flash.
			if (announceApplied(result, manualRestart, form.name)) { navigate('/services'); return; }
		} catch (e) {
			if (disposed) return;
			if (e instanceof NetworkError) {
				uncertain = true;
				error = rv2('applyUncertain');
			} else {
				if (e instanceof ApiError && [404, 409, 410, 412, 422, 428].includes(e.status)) { review = null; applyKey = ''; uncertain = false; step = 'service-backends'; }
				error = describeFailure(e, rv2('applyFailed'));
			}
		} finally {
			if (!disposed) busy = false;
		}
	}

	async function runDiagnostic() {
		if (diagnosing || !current) return;
		diagnosing = true; diagnosticError = '';
		try {
			const d = await diagnoseService(current.name, protocol, controller.signal);
			if (!disposed) diagnostic = d;
		} catch (e) {
			if (!disposed) diagnosticError = errorMessage(e, sv('diagnosticFailed'));
		} finally {
			if (!disposed) diagnosing = false;
		}
	}

	function close() {
		if (busy) return;
		navigationGuard.requestNavigation(() => {
			if (staged || result) onchanged?.();
			navigate('/services');
		});
	}

	/** View mode shows the service the way Traefik's dynamic configuration declares it. */
	const definition = $derived(current ? serviceDefinition(current.name, current.config) : {});
	const viewYaml = $derived(Object.keys(definition).length ? stringify(definition) : '');
	const transport = $derived(current && !current.read_only ? transportOf(current.name, current.config) : null);
	const proxyLabel = (v: number | string | undefined) => (Number(v) ? sv('proxyVersion', { version: Number(v) }) : sv('proxyOff'));
	/** "Using default settings" or "N set", beside a collapsible section's title. */
	const configured = (values: unknown[]) => { const n = values.filter((v) => v !== '' && v !== false && v !== '0').length; return n ? sv('configuredCount', { count: n }) : sv('defaults'); };

	const healthTone = (h: string) => (({ healthy: 'success', unhealthy: 'danger', mixed: 'warning', unknown: 'neutral' }) as Record<string, 'success' | 'danger' | 'warning' | 'neutral'>)[h] ?? 'neutral';
	const titleKey = $derived(viewing ? '' : creating ? `create.${protocol}` : `edit.${protocol}`);
</script>

<ResourceDialog title={viewing ? name : sv(titleKey)} description={viewing ? sv(`kind.${protocol}`) : ''} closeLabel={t('common.close')} onclose={close} {suspended}>
	{#snippet icon()}<Network size={32} aria-hidden="true" />{/snippet}
	{#snippet navigation()}
		{#if !loading && !loadError && !viewing}<Tabs {tabs} selected={step} onselect={selectStep} label={s('editorTabs')} />{/if}
	{/snippet}

	{#if loading}
		<p role="status">{sv('loading')}</p>
	{:else if loadError}
		<div class="stack">
			<Notice tone="error" message={loadError} />
			<div><Button onclick={retry}>{t('common.retry')}</Button></div>
		</div>
	{:else if viewing && current}
		{@const c = current.config}
		<div class="stack">
			{#if current.read_only}<Notice message={sv('externalNote')} />{/if}
			<SummaryList>
				<div><dt>{s('name')}</dt><dd>{current.name}</dd></div>
				<div><dt>{sv('protocolLabel')}</dt><dd>{sv(`kind.${protocol}`)}</dd></div>
				<div><dt>{s('ownership')}</dt><dd>{current.origin === 'managed' ? s('managed') : s('external')} · {current.provider}</dd></div>
				<div><dt>{rv2('stateColumn')}</dt><dd>{rv2('status.' + current.status)}</dd></div>
				{#if http}
					<div><dt>{sv('passHostHeader')}</dt><dd>{c.pass_host_header == null ? s('default') : c.pass_host_header ? s('enabled') : s('disabled')}</dd></div>
					<div><dt>{sv('verifyTls')}</dt><dd>{c.insecure_skip_verify ? sv('verifyOff') : sv('verifyOn')}</dd></div>
					{#if c.health_check}<div><dt>{sv('healthCheck')}</dt><dd>{c.health_check.path}{c.health_check.interval ? ` · ${c.health_check.interval}` : ''}{c.health_check.timeout ? ` · ${c.health_check.timeout}` : ''}</dd></div>{/if}
				{:else if tcp}
					<div><dt>{sv('tlsToBackends')}</dt><dd>{c.tcp?.tls ? (c.insecure_skip_verify ? sv('verifyOff') : sv('verifyOn')) + (c.tcp?.server_name ? ` · ${c.tcp.server_name}` : '') : sv('plainTcp')}</dd></div>
					<div><dt>{sv('proxyProtocol')}</dt><dd>{proxyLabel(c.tcp?.proxy_protocol)}</dd></div>
					{#if c.tcp?.health_check}<div><dt>{sv('healthCheck')}</dt><dd>{[c.tcp.health_check.interval, c.tcp.health_check.timeout, c.tcp.health_check.port ? sv('portValue', { port: c.tcp.health_check.port }) : ''].filter(Boolean).join(' · ') || sv('defaults')}</dd></div>{/if}
				{/if}
				{#if current.updated_at}<div><dt>{sv('updated')}</dt><dd><RelativeTime value={current.updated_at} /></dd></div>{/if}
			</SummaryList>
			{#if current.live_error_msg}<Notice tone="error" message={current.live_error_msg} />{/if}
			{#if viewYaml}
				<CodePreview label={sv('definition')} code={viewYaml} language="yaml" />
				{#if transport}<CodePreview label={sv('transportDefinition', { name: transport.name })} code={stringify({ [transport.name]: transport.definition })} language="yaml" />{/if}
			{:else}
				<Notice message={sv('noDefinition')} />
			{/if}
			<h3>{sv('runtime')}</h3>
			{#if diagnosticError}<Notice tone="error" message={diagnosticError} />{/if}
			{#if diagnostic}
				<SummaryList>
					<div><dt>{sv('runtimeStatus')}</dt><dd>{diagnostic.observed ? diagnostic.runtimeStatus || '—' : sv('notObserved')}</dd></div>
					<div><dt>{sv('serverHealth')}</dt><dd><StatusBadge size="sm" tone={healthTone(diagnostic.healthStatus)} text={sv('health.' + diagnostic.healthStatus)} /></dd></div>
					{#if diagnostic.serverStatus && Object.keys(diagnostic.serverStatus).length}<div><dt>{sv('servers')}</dt><dd>{Object.entries(diagnostic.serverStatus).map(([k, n]) => `${n} ${k}`).join(' · ')}</dd></div>{/if}
					<div><dt>{sv('observedAt')}</dt><dd><RelativeTime value={diagnostic.observedAt} /></dd></div>
				</SummaryList>
				{#if diagnostic.routers?.length}
					<ul class="routers">{#each diagnostic.routers as r (r.name)}<li>{#if http}<button type="button" class="link" onclick={() => navigate('/routes/' + encodeURIComponent(r.name))}>{r.name}</button>{:else}<span class="router-name">{r.name}</span>{/if} <span class="muted">{r.status}{r.entryPoints?.length ? ' · ' + r.entryPoints.join(', ') : ''}</span></li>{/each}</ul>
					{#if diagnostic.routersTruncated}<p class="muted">{sv('routersTruncated')}</p>{/if}
				{/if}
			{/if}
			<div><Button onclick={runDiagnostic} pending={diagnosing} disabled={diagnosing}>{sv(diagnostic ? 'recheck' : 'check')}</Button></div>
		</div>
	{:else if viewing}
		<Notice tone="error" message={s('requestFailed')} />
	{:else}
		<div class="stack">
			{#if !editable}<Notice tone="warning" message={sv('notEditable')} />{/if}
			{#if error}
				<Notice tone="error" message={error}>
					{#if conflict}<Button size="sm" onclick={refreshVersion} disabled={busy}>{rv2('refreshDraft')}</Button>{/if}
				</Notice>
			{/if}
			{#if submitted && Object.keys(fields).length && step !== 'service-review'}
				<div class="validation-banner" role="alert">
					<CircleAlert size={16} aria-hidden="true" />
					<span>{rv2(Object.keys(fields).length === 1 ? 'fixFieldsOne' : 'fixFields', { count: Object.keys(fields).length })}</span>
					<button type="button" onclick={focusFirstError}>{rv2('goToField')}</button>
				</div>
			{/if}

			{#if http}
			<div id="panel-service-identity" role="tabpanel" aria-labelledby="tab-service-identity" hidden={step !== 'service-identity'} class="stack">
				{@render nameField()}
				<Field id="service-host" label={s('passHost')} help={s('passHostHelp')}>
					{#snippet children(f)}
						<SelectMenu id={f.id} value={form.passHost} disabled={locked || !editable} options={[{ value: 'default', label: s('default') }, { value: 'yes', label: s('enabled') }, { value: 'no', label: s('disabled') }]} onchange={(v) => { form.passHost = v as ServiceForm['passHost']; invalidate(); }} />
					{/snippet}
				</Field>
			</div>
			{/if}

			<div id="panel-service-backends" role="tabpanel" aria-labelledby="tab-service-backends" hidden={step !== 'service-backends'} class="stack">
				{#if !http}
					{@render nameField()}
					<Notice message={sv(`about.${protocol}`)} />
				{/if}
				<section class="backends" aria-labelledby="service-backends-title">
					<div class="backends-head">
						<h3 id="service-backends-title" class="section-title">{s('backends')}</h3>
						<p class="muted">{sv(http ? 'backendsHelp' : 'addressHelp')}</p>
						<p class="muted small">{sv('maxServers', { count: MAX_SERVERS })}</p>
					</div>
					{#if submitted && fields.servers}<Notice tone="error" message={fields.servers} />{/if}
					{#each form.servers as server, index (index)}
						<BackendServer
							id={`service-url-${index}`}
							kind={http ? 'url' : 'address'}
							number={index + 1}
							value={server}
							onchange={(v) => setServer(index, v)}
							onremove={() => removeServer(index)}
							removable={form.servers.length > 1}
							error={submitted ? fields[`server.${index}`] : ''}
							disabled={locked || !editable}
						/>
					{/each}
					<button type="button" class="add-backend" class:url={http} onclick={addServer} disabled={locked || !editable || form.servers.length >= MAX_SERVERS}>
						<Plus size={16} aria-hidden="true" /> {s('addBackend')}
					</button>
				</section>

				{#if http}
					<Checkbox id="service-health-enabled" checked={form.healthEnabled} label={sv('healthEnable')} help={sv('healthEnableHelp')} disabled={locked || !editable} onchange={(v) => { form.healthEnabled = v; invalidate(); }} />
					{#if form.healthEnabled}
						<Field id="service-health-path" label={sv('healthPath')} help={sv('healthPathHelp')} error={submitted ? fields.healthPath : ''}>
							{#snippet children(f)}
								<input id={f.id} bind:value={form.healthPath} oninput={invalidate} disabled={locked || !editable} aria-invalid={f.invalid || undefined} aria-describedby={f.describedBy} autocomplete="off" spellcheck="false" placeholder="/health" />
							{/snippet}
						</Field>
						{@render duration('service-health-interval', 'healthInterval', sv('healthInterval'), '10s')}
						{@render duration('service-health-timeout', 'healthTimeout', sv('healthTimeout'), '3s')}
					{/if}
					<Checkbox id="service-skip-verify" checked={form.insecureSkipVerify} label={sv('skipVerify')} help={sv('skipVerifyHelp')} disabled={locked || !editable} onchange={(v) => { form.insecureSkipVerify = v; invalidate(); }} />
				{/if}
			</div>

			{#if tcp}
				<div id="panel-service-options" role="tabpanel" aria-labelledby="tab-service-options" hidden={step !== 'service-options'} class="stack">
					<p class="muted">{sv('optionsHelp')}</p>
					<Checkbox id="service-tls" checked={form.tls} label={sv('tlsEnable')} help={sv('tlsEnableHelp')} disabled={locked || !editable} onchange={(v) => { form.tls = v; invalidate(); }} />
					{#if form.tls}
						<div class="nested">
							<Field id="service-server-name" label={sv('serverName')} help={sv('serverNameHelp')} error={submitted ? fields.serverName : ''}>
								{#snippet children(f)}
									<input id={f.id} bind:value={form.serverName} oninput={invalidate} disabled={locked || !editable} aria-invalid={f.invalid || undefined} aria-describedby={f.describedBy} autocomplete="off" spellcheck="false" placeholder="db.example.com" />
								{/snippet}
							</Field>
							<Checkbox id="service-skip-verify" checked={form.insecureSkipVerify} label={sv('skipVerify')} help={sv('skipVerifyTcpHelp')} disabled={locked || !editable} onchange={(v) => { form.insecureSkipVerify = v; invalidate(); }} />
						</div>
					{/if}

					<Field id="service-proxy-protocol" label={sv('proxyProtocol')} help={sv('proxyProtocolHelp')}>
						{#snippet children(f)}
							<SelectMenu id={f.id} value={form.proxyProtocol} disabled={locked || !editable} options={[{ value: '0', label: sv('proxyOff') }, { value: '1', label: sv('proxyVersion', { version: 1 }) }, { value: '2', label: sv('proxyVersion', { version: 2 }) }]} onchange={(v) => { form.proxyProtocol = v as ServiceForm['proxyProtocol']; invalidate(); }} />
						{/snippet}
					</Field>

					<Checkbox id="service-health-enabled" checked={form.healthEnabled} label={sv('healthEnable')} help={sv('tcpHealthHelp')} disabled={locked || !editable} onchange={(v) => { form.healthEnabled = v; invalidate(); }} />
					{#if form.healthEnabled}
						<div class="nested">
							<div class="pair">
								{@render duration('service-health-interval', 'healthInterval', sv('healthInterval'), '30s')}
								{@render duration('service-health-timeout', 'healthTimeout', sv('healthTimeout'), '5s')}
							</div>
							<Field id="service-health-port" label={sv('healthPort')} help={sv('healthPortHelp')} error={submitted ? fields.healthPort : ''}>
								{#snippet children(f)}
									<input id={f.id} bind:value={form.healthPort} oninput={invalidate} disabled={locked || !editable} aria-invalid={f.invalid || undefined} aria-describedby={f.describedBy} inputmode="numeric" autocomplete="off" placeholder={sv('sameAsBackend')} />
								{/snippet}
							</Field>
							<Field id="service-health-send" label={sv('healthSend')} help={sv('healthSendHelp')} error={submitted ? fields.healthSend : ''}>
								{#snippet children(f)}
									<input id={f.id} bind:value={form.healthSend} oninput={invalidate} disabled={locked || !editable} aria-invalid={f.invalid || undefined} aria-describedby={f.describedBy} autocomplete="off" spellcheck="false" placeholder="PING" />
								{/snippet}
							</Field>
							<Field id="service-health-expect" label={sv('healthExpect')} help={sv('healthExpectHelp')} error={submitted ? fields.healthExpect : ''}>
								{#snippet children(f)}
									<input id={f.id} bind:value={form.healthExpect} oninput={invalidate} disabled={locked || !editable || !form.healthSend} aria-invalid={f.invalid || undefined} aria-describedby={f.describedBy} autocomplete="off" spellcheck="false" placeholder="PONG" />
								{/snippet}
							</Field>
						</div>
					{/if}

					<details class="options" open={!!(form.dialTimeout || form.terminationDelay || fields.dialTimeout || fields.terminationDelay)}>
						<summary>{sv('connection')} <span>{configured([form.dialTimeout, form.terminationDelay])}</span></summary>
						<div class="option-fields">
							<div class="pair">
								{@render duration('service-dial-timeout', 'dialTimeout', sv('dialTimeout'), '30s', sv('dialTimeoutHelp'))}
								{@render duration('service-termination-delay', 'terminationDelay', sv('terminationDelay'), '100ms', sv('terminationDelayHelp'))}
							</div>
						</div>
					</details>
				</div>
			{/if}

			<div id="panel-service-review" role="tabpanel" aria-labelledby="tab-service-review" hidden={step !== 'service-review'} class="stack">
				{#if result}
					<Notice tone={operationTone(result.status)} title={t('operationV2.status.' + result.status)} message={result.summary}>
						{#if result.id}<span class="muted">{t('operationV2.id', { id: result.id })}</span>{/if}
					</Notice>
					{#if manualRestart}<Notice tone="warning" message={t('writeV2.manualRestart')} />{/if}
				{:else if review}
					<SummaryList>
						<div><dt>{s('name')}</dt><dd>{form.name}</dd></div>
						<div><dt>{sv('protocolLabel')}</dt><dd>{sv(`kind.${protocol}`)}</dd></div>
						<div><dt>{s('backends')}</dt><dd>{form.servers.map((u) => u.trim()).join(' · ')}</dd></div>
						{#if form.healthEnabled}<div><dt>{sv('healthCheck')}</dt><dd>{http ? form.healthPath || '/' : [form.healthInterval, form.healthTimeout].filter(Boolean).join(' · ') || sv('defaults')}</dd></div>{/if}
						{#if tcp}
							<div><dt>{sv('tlsToBackends')}</dt><dd>{form.tls ? (form.insecureSkipVerify ? sv('verifyOff') : sv('verifyOn')) : sv('plainTcp')}</dd></div>
							{#if form.proxyProtocol !== '0'}<div><dt>{sv('proxyProtocol')}</dt><dd>{proxyLabel(form.proxyProtocol)}</dd></div>{/if}
						{:else if http && form.insecureSkipVerify}<div><dt>{sv('verifyTls')}</dt><dd>{sv('verifyOff')}</dd></div>{/if}
						{#if staged?.updated_at}<div><dt>{sv('updated')}</dt><dd><RelativeTime value={staged.updated_at} /></dd></div>{/if}
						<div><dt>{rv2('pendingTotal')}</dt><dd>{review.changes?.length ?? 0}</dd></div>
					</SummaryList>
					{#if form.insecureSkipVerify && (http || form.tls)}<Notice tone="warning" message={sv('skipVerifyWarning')} />{/if}
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
					{#if review.static_diff}<CodePreview label={rv2('diff.static')} code={review.static_diff} language="diff" />{/if}
					{#if review.dynamic_diff}<CodePreview label={rv2('diff.dynamic')} code={review.dynamic_diff} language="diff" />{/if}
					{#if !review.static_diff && !review.dynamic_diff}<Notice tone="info" message={sv('noFileChanges')} />{/if}
				{:else if reviewError}
					<Notice tone="error" message={reviewError}><Button size="sm" onclick={stageAndReview}>{t('common.retry')}</Button></Notice>
				{:else}
					<p role="status">{rv2('staging')}</p>
				{/if}
			</div>
		</div>
	{/if}

	{#snippet footer()}
		<Button onclick={close} disabled={busy}>{result || viewing ? t('common.close') : t('common.cancel')}</Button>
		{#if viewing && current && !current.read_only}
			<Button variant="primary" onclick={() => navigate(parent + '/' + encodeURIComponent(current.name) + '/edit')}>{s('edit')}</Button>
		{/if}
		{#if !loading && !loadError && !viewing && !result && editable}
			{#if step !== firstStep}
				<Button onclick={() => (step = previousStep)} disabled={busy}>{t('domainUI.back')}</Button>
			{/if}
			{#if step === 'service-review'}
				{#if review}
					<Button variant="primary" onclick={apply} pending={busy} disabled={busy || serverErrors.length > 0}>{t('domainUI.' + (busy ? 'applying' : uncertain ? 'retryApply' : 'apply'))}</Button>
				{/if}
			{:else if (http && step === 'service-identity') || (tcp && step === 'service-backends')}
				<Button variant="primary" onclick={() => selectStep(order[order.indexOf(step) + 1])} disabled={busy}>{t('domainUI.continue')}</Button>
			{:else}
				<Button variant="primary" onclick={stageAndReview} pending={busy} disabled={busy}>{rv2('stageReview')}</Button>
			{/if}
		{/if}
	{/snippet}
</ResourceDialog>

{#snippet nameField()}
	<Field id="service-name" label={s('name')} required help={creating ? s('nameHelp') : s('immutable')} error={submitted ? fields.name : ''}>
		{#snippet children(f)}
			<input id={f.id} bind:value={form.name} oninput={invalidate} readonly={!creating} disabled={locked || !editable} aria-invalid={f.invalid || undefined} aria-describedby={f.describedBy} autocomplete="off" spellcheck="false" placeholder={tcp ? 'postgres' : protocol === 'udp' ? 'dns' : 'my-app'} />
		{/snippet}
	</Field>
{/snippet}

{#snippet duration(id: string, key: 'healthInterval' | 'healthTimeout' | 'dialTimeout' | 'terminationDelay', label: string, placeholder: string, help: string = sv('durationHelp'))}
	<Field {id} {label} {help} error={submitted ? fields[key] : ''}>
		{#snippet children(f)}
			<input id={f.id} bind:value={form[key]} oninput={invalidate} disabled={locked || !editable} aria-invalid={f.invalid || undefined} aria-describedby={f.describedBy} autocomplete="off" spellcheck="false" {placeholder} />
		{/snippet}
	</Field>
{/snippet}

<style>
	.stack, .stack:not([hidden]) { display: flex; flex-direction: column; gap: 24px; }
	.stack[hidden] { display: none; }
	h3 { display: flex; align-items: center; gap: 12px; margin: 0; font-size: 13px; font-weight: 600; color: var(--text); }
	h3::after { content: ''; flex: 1; height: 1px; background: var(--border); }
	.muted { margin: 0; color: var(--text-muted); font-size: 14px; line-height: 21px; }
	.reasons { margin: 8px 0 0; padding-left: 20px; font-size: 14px; line-height: 22px; }
	.routers { list-style: none; margin: 0; padding: 0; display: flex; flex-direction: column; gap: 8px; }
	.router-name { font-weight: 500; }
	.backends { display: flex; flex-direction: column; gap: 12px; max-width: 680px; }
	.backends-head { display: flex; flex-direction: column; gap: 2px; margin-bottom: 4px; }
	.section-title { font-size: 14px; font-weight: 500; line-height: 20px; }
	.section-title::after { content: none; }
	.small { font-size: 13px; line-height: 20px; }
	/* As wide as the address fields: the icon buttons' column stays empty. */
	.add-backend {
		display: inline-flex;
		align-items: center;
		justify-content: center;
		gap: 8px;
		width: calc(100% - 40px);
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
	.add-backend.url { width: calc(100% - 80px); }
	.add-backend:hover:not(:disabled) { background: var(--surface-muted); border-style: solid; }
	.add-backend:disabled { opacity: 0.5; cursor: default; }
	/* Fields that belong to the checkbox above them. */
	.nested { display: flex; flex-direction: column; gap: 20px; box-sizing: border-box; max-width: 560px; padding-left: 16px; border-left: 2px solid var(--border); margin-left: 9px; }
	.nested :global(.field) { max-width: none; }
	.pair { display: grid; grid-template-columns: repeat(2, minmax(0, 1fr)); gap: 16px; max-width: 560px; }
	.options { border-top: 1px solid var(--border); padding-top: 16px; max-width: 560px; }
	.options summary { cursor: pointer; font-size: 14px; font-weight: 500; line-height: 24px; color: var(--text); }
	.options summary span { color: var(--text-muted); font-weight: 400; margin-left: 8px; }
	.options summary span::before { content: '·'; margin-right: 8px; }
	.option-fields { padding-top: 20px; }
	.link { border: 0; background: transparent; padding: 0; color: var(--connection); font: inherit; cursor: pointer; }
	.link:hover { text-decoration: underline; }
	.validation-banner { display: flex; align-items: center; gap: 8px; padding: 10px 14px; border: 1px solid color-mix(in srgb, var(--danger-text) 30%, transparent); border-radius: var(--radius-card); background: var(--danger-bg); color: var(--danger-text); font-size: 13px; line-height: 18px; }
	.validation-banner span { flex: 1; }
	.validation-banner button { border: 0; background: transparent; color: inherit; font: inherit; font-weight: 600; cursor: pointer; padding: 2px 4px; }
	.validation-banner button:hover { text-decoration: underline; }
	@media (max-width: 767px) {
		.pair { grid-template-columns: 1fr; }
		.add-backend, .add-backend.url { width: calc(100% - 52px); min-height: var(--touch-h); }
	}
</style>

<script lang="ts">
	/**
	 * Middleware dialog (create / edit / view) on the direct write protocol.
	 *
	 *   Middleware tab    → name, type (plugin name for plugins)
	 *   Configuration tab → schema-driven form or YAML; basicAuth users are an htpasswd list
	 *   Review tab        → PUT /middlewares/{name}?dry_run=true + If-Match returns the preview (nothing saved)
	 *   Apply             → the same PUT without dry_run, with one Idempotency-Key per attempt
	 *
	 * Secret values come back as "[redacted]"; sending the marker back keeps the stored value.
	 */
	import { onMount, onDestroy, tick } from 'svelte';
	import { parse, stringify } from 'yaml';
	import { Layers, CircleAlert } from '@lucide/svelte';
	import ResourceDialog from '../../shared/ui/ResourceDialog.svelte';
	import Field from '../../shared/ui/Field.svelte';
	import Tabs from '../../shared/ui/Tabs.svelte';
	import Button from '../../shared/ui/Button.svelte';
	import Notice from '../../shared/ui/Notice.svelte';
	import SummaryList from '../../shared/ui/SummaryList.svelte';
	import CodePreview from '../../shared/ui/CodePreview.svelte';
	import RelativeTime from '../../shared/ui/RelativeTime.svelte';
	import SelectMenu from '../../shared/ui/SelectMenu.svelte';
	import SearchableSelect from '../../shared/ui/SearchableSelect.svelte';
	import YamlCodeEditor from '../../shared/ui/YamlCodeEditor.svelte';
	import ConfigFields from './ConfigFields.svelte';
	import CredentialsEditor from './CredentialsEditor.svelte';
	import { navigationGuard } from '../../lib/stores/navigationGuard';
	import { needsManualRestart, newIdempotencyKey, operationOf, announceApplied } from '../../core/mutations';
	import { operationTone } from '../changes/models';
	import type { Operation, OperationPreview } from '../../core/types';
	import { listMiddlewares, listServices, putMiddleware, type ServiceRef } from './api';
	import {
		ALLOWED_TYPES,
		MIDDLEWARE_BASE,
		PROTOCOLS,
		TCP_TYPES,
		TYPE_CATEGORIES,
		hasRedacted,
		middlewareProtocolOf,
		type Protocol,
		isValidMiddlewareName,
		nativeSchema,
		serializeConfig,
		stateFromConfig,
		type FormState,
		type MiddlewareResource,
		type SchemaField,
		emptyCredential,
		type Credential,
	} from './models';
	import { activeCredentialUsers, findUnknownKeys, flattenSchemaFields, validateForm } from './validation';
	import { ApiError, NetworkError } from '../../shared/api/client';
	import { errorMessage } from '../../shared/api/errorMessage';
	import { t } from '../../shared/localization/i18n';

	let {
		mode = 'create',
		name = '',
		protocol: urlProtocol = 'http',
		navigate,
		onchanged,
	}: {
		mode?: string;
		name?: string;
		/** The protocol of a new middleware (from the URL: /middlewares or /tcp-middlewares); an existing one keeps its own. */
		protocol?: Protocol;
		navigate: (p: string) => void;
		onchanged?: () => void;
	} = $props();

	const m = (key: string, args?: Record<string, string | number>) => t('middlewares.' + key, args);
	const mv = (key: string, args?: Record<string, string | number>) => t('middlewareV2.' + key, args);
	const rv2 = (key: string, args?: Record<string, string | number>) => t('resolverV2.' + key, args);

	const creating = $derived(mode === 'create');
	const viewing = $derived(mode === 'view');
	// svelte-ignore state_referenced_locally
	let protocol = $state<Protocol>(urlProtocol);
	const tcp = $derived(protocol === 'tcp');
	const parent = $derived(MIDDLEWARE_BASE[protocol]);

	let existing = $state<MiddlewareResource[]>([]);
	let services = $state<ServiceRef[]>([]);
	let loading = $state(true);
	let loadError = $state('');
	let busy = $state(false);
	let suspended = $state(false);
	let error = $state('');
	let step = $state('middleware-identity');
	let baseline = $state('');
	let submitted = $state(false);
	let fields = $state<Record<string, string>>({});

	let mwName = $state('');
	let mwType = $state('');
	let pluginName = $state('');
	let formState = $state<FormState>({});
	let yamlMode = $state(false);
	let yamlText = $state('');
	let yamlNote = $state('');
	let usersMode = $state<'keep' | 'replace'>('replace');
	let credentials = $state<Credential[]>([emptyCredential()]);
	let drafts = $state<
		Record<string, { formState: FormState; yamlMode: boolean; yamlText: string; credentials: Credential[] }>
	>({});

	let conflict = $state(false);
	let staged = $state<MiddlewareResource | null>(null);
	let review = $state<OperationPreview | null>(null);
	let reviewError = $state('');
	let applyKey = $state('');
	let uncertain = $state(false);
	let result = $state<Operation | null>(null);
	let manualRestart = $state(false);

	let disposed = false;
	const controller = new AbortController();

	// Traefik's own middlewares may share a name across protocols: the URL's protocol breaks the tie.
	const findItem = (all: MiddlewareResource[]) =>
		all.find((d) => d.name === name && middlewareProtocolOf(d.config) === urlProtocol) ??
		all.find((d) => d.name === name);
	const current = $derived(findItem(existing));
	const version = () => current?.version ?? 0;
	const editable = $derived(!viewing && (creating || (!!current && !current.read_only)));
	const isPlugin = $derived(mwType === 'plugin');
	const schemaNode = $derived<Record<string, SchemaField> | null>(nativeSchema[protocol][mwType] ?? null);
	const locked = $derived(busy || !!result);
	const serverErrors = $derived(review?.validation_errors ?? []);
	const dirty = $derived(editable && !!baseline && JSON.stringify(snapshot()) !== baseline && !result);

	function snapshot() {
		return {
			protocol,
			mwName,
			mwType,
			pluginName,
			formState: $state.snapshot(formState),
			yamlMode,
			yamlText,
			usersMode,
			credentials: $state.snapshot(credentials),
		};
	}

	/** YAML document: {type: spec} for native types, the bare spec for plugins. */
	const yamlDoc = $derived.by(() => {
		if (!yamlMode && !isPlugin)
			return { valid: true, error: '', spec: null as Record<string, unknown> | null, docType: '' };
		try {
			const doc = parse(yamlText);
			if (isPlugin) {
				if (doc === null || doc === undefined)
					return { valid: true, error: '', spec: {} as Record<string, unknown>, docType: '' };
				if (typeof doc !== 'object' || Array.isArray(doc))
					return { valid: false, error: m('yamlNotAnObject'), spec: null, docType: '' };
				return { valid: true, error: '', spec: doc as Record<string, unknown>, docType: '' };
			}
			if (!doc || typeof doc !== 'object' || Array.isArray(doc))
				return { valid: false, error: m('yamlNotAnObject'), spec: null, docType: '' };
			const keys = Object.keys(doc);
			if (keys.length !== 1) return { valid: false, error: m('yamlSingleType'), spec: null, docType: keys[0] ?? '' };
			const spec = (doc as Record<string, unknown>)[keys[0]];
			if (!spec || typeof spec !== 'object' || Array.isArray(spec))
				return { valid: false, error: m('yamlNotAnObject'), spec: null, docType: keys[0] };
			return { valid: true, error: '', spec: spec as Record<string, unknown>, docType: keys[0] };
		} catch {
			return { valid: false, error: m('yamlInvalid'), spec: null, docType: '' };
		}
	});

	const builtSpec = $derived.by<Record<string, unknown> | null>(() => {
		if (!mwType) return {};
		if (yamlMode || isPlugin) return yamlDoc.valid ? yamlDoc.spec : null;
		const spec = serializeConfig(formState);
		if (mwType === 'basicAuth' && usersMode === 'replace') {
			// Plaintext passwords travel once to core-v2, which hashes them (bcrypt) into the users file.
			delete spec.users;
			delete spec.usersFile;
			const active = activeCredentialUsers(credentials);
			if (active.length) spec.users_credentials = active;
		}
		return spec;
	});

	const storedUsers = $derived(
		!creating &&
			mwType === 'basicAuth' &&
			!!current &&
			(current.config.spec?.users != null || current.config.spec?.usersFile != null),
	);
	const containsStoredSecrets = $derived(!creating && !!current && hasRedacted(current.config.spec));

	const typeOptions = $derived(
		tcp
			? TCP_TYPES.map((tp) => ({ value: tp, label: m(`types.${tp}.label`), description: m(`types.${tp}.tcpHint`) }))
			: [
					...TYPE_CATEGORIES.flatMap((cat) =>
						cat.types
							.filter((tp) => (ALLOWED_TYPES as readonly string[]).includes(tp))
							.map((tp) => ({ value: tp, label: m(`types.${tp}.label`), description: m(`types.${tp}.hint`) })),
					),
					{ value: 'plugin', label: mv('pluginType'), description: mv('pluginTypeHint') },
				],
	);
	const serviceOptions = $derived([
		{ value: '', label: mv('selectNone') },
		...services.map((s) => ({
			value: s.name,
			label: s.name + (s.read_only ? ` (${m('external')})` : ''),
			description: s.config?.type ?? '',
		})),
	]);

	const IDENTITY_KEYS = ['name', 'type', 'plugin_name'];
	const identityErrors = $derived(submitted ? IDENTITY_KEYS.filter((k) => fields[k]).length : 0);
	const configErrors = $derived(submitted ? Object.keys(fields).filter((k) => !IDENTITY_KEYS.includes(k)).length : 0);
	const tabs = $derived([
		{ id: 'middleware-identity', label: m('tabMiddleware'), errorCount: identityErrors, errorLabel: m('fixErrors') },
		{
			id: 'middleware-configuration',
			label: m('tabConfiguration'),
			errorCount: configErrors,
			errorLabel: m('fixErrors'),
		},
		{ id: 'middleware-review', label: m('tabReview') },
	]);

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
	});

	function initFrom(item: MiddlewareResource) {
		protocol = middlewareProtocolOf(item.config);
		mwName = item.name;
		mwType = item.config.type;
		pluginName = item.config.plugin_name ?? '';
		const spec = item.config.spec ?? {};
		const schema = nativeSchema[protocol];
		if (item.config.type === 'plugin' || !schema[item.config.type]) {
			yamlMode = true;
			yamlText = Object.keys(spec).length ? stringify(spec) : '';
		} else {
			formState = stateFromConfig(schema[item.config.type], spec);
		}
		usersMode = 'keep';
	}

	async function load() {
		loading = true;
		loadError = '';
		try {
			const [all] = await Promise.all([listMiddlewares(controller.signal)]);
			if (disposed) return;
			existing = all;
			if (creating && !tcp) {
				// Opened from a plugin ("Create middleware"): start on that plugin.
				const plugin = new URLSearchParams(window.location.hash.split('?')[1] ?? '').get('plugin');
				if (plugin && /^[a-z][a-z0-9_-]{0,62}$/.test(plugin)) {
					mwType = 'plugin';
					pluginName = plugin;
					yamlMode = true;
				}
			}
			if (!creating) {
				const item = findItem(all);
				if (!item) throw new Error(m('requestFailed'));
				initFrom(item);
				if (mode === 'edit') step = 'middleware-configuration';
			}
			baseline = JSON.stringify(snapshot());
		} catch (e) {
			if (!disposed) loadError = errorMessage(e, m('requestFailed'));
		} finally {
			if (!disposed) loading = false;
		}
		// Only the HTTP errors middleware points at a service.
		if (!tcp) loadServices();
	}

	function invalidate() {
		review = null;
		staged = null;
		reviewError = '';
		applyKey = '';
		uncertain = false;
		error = '';
		conflict = false;
	}

	/**
	 * Protocol picked on create: the flow is the same for HTTP and TCP, only the types (and their
	 * fields) change, so switching starts the type and its configuration over.
	 */
	function onProtocolChange(value: string) {
		const next = value === 'tcp' ? 'tcp' : 'http';
		if (next === protocol) return;
		protocol = next;
		mwType = '';
		pluginName = '';
		formState = {};
		yamlMode = false;
		yamlText = '';
		yamlNote = '';
		credentials = [emptyCredential()];
		usersMode = 'replace';
		drafts = {};
		fields = Object.fromEntries(Object.entries(fields).filter(([k]) => k === 'name'));
		if (next === 'http' && !servicesLoaded) loadServices();
		invalidate();
	}

	let servicesLoaded = false;
	function loadServices() {
		servicesLoaded = true;
		void listServices(controller.signal).then(
			(v) => {
				if (!disposed) services = v;
			},
			() => {
				servicesLoaded = false;
			},
		);
	}

	function onTypeChange(value: string) {
		if (value === mwType) return;
		if (mwType)
			drafts[mwType] = {
				formState: $state.snapshot(formState),
				yamlMode,
				yamlText,
				credentials: $state.snapshot(credentials),
			};
		mwType = value;
		const saved = drafts[value];
		formState = saved?.formState ?? {};
		yamlMode = saved?.yamlMode ?? value === 'plugin';
		yamlText = saved?.yamlText ?? '';
		credentials = saved?.credentials ?? [emptyCredential()];
		usersMode = 'replace';
		fields = {};
		yamlNote = '';
		invalidate();
	}

	function setMode(value: string) {
		const toYaml = value === 'yaml';
		if (toYaml === yamlMode || isPlugin) return;
		yamlNote = '';
		if (toYaml) {
			// Never copy typed passwords into the YAML editor: YAML takes Traefik's own `users` hashes.
			const { users_credentials: _plain, ...rest } = builtSpec ?? {};
			yamlText = stringify({ [mwType]: rest });
			yamlMode = true;
		} else {
			const doc = yamlDoc;
			if (!doc.valid || !doc.spec) {
				yamlNote = doc.error || m('yamlInvalid');
				return;
			}
			if (doc.docType !== mwType) {
				yamlNote = m('yamlTypeMismatch', { expected: mwType });
				return;
			}
			const unknown = schemaNode ? findUnknownKeys(schemaNode, doc.spec) : [];
			if (unknown.length) {
				yamlNote = m('yamlUnknownFields', { fields: unknown.join(', ') });
				return;
			}
			formState = stateFromConfig(schemaNode ?? {}, doc.spec);
			yamlMode = false;
		}
		invalidate();
	}

	function identityErrorsNow(): Record<string, string> {
		const errs: Record<string, string> = {};
		if (!isValidMiddlewareName(mwName)) errs.name = m('invalidName');
		else if (creating && existing.some((d) => d.name === mwName || d.name === mwName + '@file'))
			errs.name = mv('nameTaken');
		if (!mwType) errs.type = m('typeRequired');
		if (isPlugin && !pluginName.trim()) errs.plugin_name = mv('pluginRequired');
		return errs;
	}

	function validateAll(): boolean {
		const errs = identityErrorsNow();
		if (isPlugin) {
			if (!yamlDoc.valid) errs.yaml = yamlDoc.error;
		} else if (mwType && schemaNode) {
			if (yamlMode) {
				if (!yamlDoc.valid) errs.yaml = yamlDoc.error;
				else if (yamlDoc.docType !== mwType) errs.yaml = m('yamlTypeMismatch', { expected: mwType });
				else {
					const unknown = findUnknownKeys(schemaNode, yamlDoc.spec ?? {});
					if (unknown.length) errs.yaml = m('yamlUnknownFields', { fields: unknown.join(', ') });
				}
			} else if (builtSpec) {
				Object.assign(
					errs,
					validateForm(
						protocol,
						mwType,
						flattenSchemaFields(schemaNode),
						formState,
						builtSpec,
						{ users: credentials, mode: mwType === 'basicAuth' && usersMode === 'replace' ? 'replace' : 'none' },
						[],
						mwName,
						m,
					),
				);
			}
			const spec = builtSpec ?? {};
			if (
				mwType === 'basicAuth' &&
				!spec.users_credentials &&
				!spec.users &&
				!spec.usersFile &&
				!(usersMode === 'keep' && storedUsers)
			)
				errs.users = mv('usersRequired');
			if (mwType === 'digestAuth' && !spec.users && !spec.usersFile) errs.users = mv('usersRequired');
		}
		fields = errs;
		submitted = true;
		return Object.keys(errs).length === 0;
	}

	const stepOf = (key: string) => (IDENTITY_KEYS.includes(key) ? 'middleware-identity' : 'middleware-configuration');
	async function focusFirstError() {
		const key = Object.keys(fields)[0];
		if (!key) return;
		step = stepOf(key);
		await tick();
		const target = document.querySelector<HTMLElement>(
			'.resource-dialog [aria-invalid="true"], .resource-dialog [data-invalid="true"]',
		);
		for (let el = target?.parentElement; el; el = el.parentElement)
			if (el instanceof HTMLDetailsElement) el.open = true;
		await tick();
		target?.focus();
		target?.scrollIntoView({ block: 'nearest' });
	}

	async function selectStep(id: string) {
		if (busy || result || id === step) return;
		if (id === 'middleware-review') {
			await stageAndReview();
			return;
		}
		if (id === 'middleware-configuration' && step === 'middleware-identity') {
			submitted = true;
			fields = identityErrorsNow();
			if (Object.keys(fields).length) {
				await focusFirstError();
				return;
			}
		}
		step = id;
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
		if (busy || !editable) return;
		if (!validateAll()) {
			await focusFirstError();
			return;
		}
		busy = true;
		error = '';
		conflict = false;
		try {
			const spec = builtSpec ?? {};
			const preview = await putMiddleware(
				mwName,
				{
					...(tcp ? { protocol: 'tcp' as const } : {}),
					type: mwType,
					...(isPlugin ? { plugin_name: pluginName.trim() } : {}),
					spec,
				},
				version(),
				{ dryRun: true, signal: controller.signal },
			);
			if (disposed) return;
			step = 'middleware-review';
			review = preview.preview ?? null;
			applyKey = newIdempotencyKey();
		} catch (e) {
			if (disposed) return;
			if (e instanceof ApiError && (e.status === 422 || e.status === 400)) {
				fields = { [isPlugin ? 'yaml' : 'type']: errorMessage(e, mv('stageFailed')) };
				step = 'middleware-configuration';
			}
			error = describeFailure(e, mv('stageFailed'));
		} finally {
			if (!disposed) busy = false;
		}
	}

	async function refreshVersion() {
		busy = true;
		try {
			existing = await listMiddlewares(controller.signal);
			conflict = false;
			error = '';
		} catch (e) {
			error = describeFailure(e, rv2('reviewFailed'));
		} finally {
			if (!disposed) busy = false;
		}
	}

	async function apply() {
		if (!review || busy || serverErrors.length) return;
		busy = true;
		error = '';
		try {
			const spec = builtSpec ?? {};
			const saved = await putMiddleware(
				mwName,
				{
					...(tcp ? { protocol: 'tcp' as const } : {}),
					type: mwType,
					...(isPlugin ? { plugin_name: pluginName.trim() } : {}),
					spec,
				},
				version(),
				{ idempotencyKey: applyKey, signal: controller.signal },
			);
			if (disposed) return;
			result = operationOf(saved, t('writeV2.noChange'));
			manualRestart = needsManualRestart(saved);
			uncertain = false;
			staged = saved.resource ?? null;
			navigationGuard.setDirty(false);
			onchanged?.();
			// Applied: back to the table; the outcome is announced as a flash.
			if (announceApplied(result, manualRestart, mwName)) {
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
					step = 'middleware-configuration';
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

	const viewYaml = $derived(
		current
			? stringify(
					current.config.type === 'plugin'
						? { plugin: { [current.config.plugin_name ?? '']: current.config.spec ?? {} } }
						: { [current.config.type]: current.config.spec ?? {} },
				)
			: '',
	);
	const typeLabel = (type: string) =>
		type === 'external' ? m('external') : type === 'plugin' ? mv('pluginType') : m(`types.${type}.label`);
</script>

<ResourceDialog
	title={viewing ? name : creating ? m('createTitle') : tcp ? mv('editTcpTitle') : m('editTitle')}
	description={viewing ? (tcp ? mv('kind.tcp') : t('topology.kind.middleware')) : ''}
	closeLabel={t('common.close')}
	onclose={close}
	{suspended}
>
	{#snippet icon()}<Layers size={32} aria-hidden="true" />{/snippet}
	{#snippet navigation()}
		{#if !loading && !loadError && !viewing}<Tabs
				{tabs}
				selected={step}
				onselect={selectStep}
				label={m('editorTabs')}
			/>{/if}
	{/snippet}

	{#if loading}
		<p role="status">{mv('loading')}</p>
	{:else if loadError}
		<div class="stack">
			<Notice tone="error" message={loadError} />
			<div><Button onclick={load}>{t('common.retry')}</Button></div>
		</div>
	{:else if viewing && current}
		<div class="stack">
			<SummaryList>
				<div>
					<dt>{m('name')}</dt>
					<dd>{current.name}</dd>
				</div>
				<div>
					<dt>{m('type')}</dt>
					<dd>{typeLabel(current.config.type)}</dd>
				</div>
				<div>
					<dt>{mv('protocolColumn')}</dt>
					<dd>{mv('kind.' + protocol)}</dd>
				</div>
				{#if current.config.plugin_name}<div>
						<dt>{mv('pluginName')}</dt>
						<dd>{current.config.plugin_name}</dd>
					</div>{/if}
				<div>
					<dt>{m('ownership')}</dt>
					<dd>{current.origin === 'managed' ? m('managed') : m('external')} · {current.provider}</dd>
				</div>
				<div>
					<dt>{rv2('stateColumn')}</dt>
					<dd>{rv2('status.' + current.status)}</dd>
				</div>
				{#if current.updated_at}<div>
						<dt>{mv('updated')}</dt>
						<dd><RelativeTime value={current.updated_at} /></dd>
					</div>{/if}
			</SummaryList>
			{#if current.live_error_msg}<Notice tone="error" message={current.live_error_msg} />{/if}
			{#if containsStoredSecrets}<Notice message={mv('redactedNotice')} />{/if}
			<CodePreview label={m('safeProjection')} code={viewYaml} language="yaml" />
		</div>
	{:else if viewing}
		<Notice tone="error" message={m('requestFailed')} />
	{:else}
		<div class="stack">
			{#if !editable}<Notice tone="warning" message={mv('notEditable')} />{/if}
			{#if error}
				<Notice tone="error" message={error}>
					{#if conflict}<Button size="sm" onclick={refreshVersion} disabled={busy}>{rv2('refreshDraft')}</Button>{/if}
				</Notice>
			{/if}
			{#if submitted && Object.keys(fields).length && step !== 'middleware-review'}
				<div class="validation-banner" role="alert">
					<CircleAlert size={16} aria-hidden="true" />
					<span>
						{rv2(Object.keys(fields).length === 1 ? 'fixFieldsOne' : 'fixFields', {
							count: Object.keys(fields).length,
						})}
					</span>
					<button type="button" onclick={focusFirstError}>{rv2('goToField')}</button>
				</div>
			{/if}

			<div
				id="panel-middleware-identity"
				role="tabpanel"
				aria-labelledby="tab-middleware-identity"
				hidden={step !== 'middleware-identity'}
				class="stack"
			>
				{#if creating}
					<Field id="middleware-protocol" label={mv('protocolColumn')} required help={mv(`createHelp.${protocol}`)}>
						{#snippet children(f)}
							<SelectMenu
								id={f.id}
								value={protocol}
								disabled={locked}
								onchange={onProtocolChange}
								options={PROTOCOLS.map((p) => ({ value: p, label: mv(`kind.${p}`) }))}
							/>
						{/snippet}
					</Field>
				{/if}
				{#if tcp}<Notice message={mv('tcpAbout')} />{/if}
				<Field
					id="middleware-name"
					label={m('name')}
					required
					help={creating ? m('nameHelp') : m('immutable')}
					error={submitted ? fields.name : ''}
				>
					{#snippet children(f)}
						<input
							id={f.id}
							bind:value={mwName}
							oninput={invalidate}
							readonly={!creating}
							disabled={locked || !editable}
							aria-invalid={f.invalid || undefined}
							aria-describedby={f.describedBy}
							autocomplete="off"
							spellcheck="false"
							placeholder={t('middlewares.placeholders.name')}
						/>
					{/snippet}
				</Field>
				<Field
					id="middleware-type"
					label={m('type')}
					required={creating}
					help={creating ? '' : m('immutable')}
					error={submitted ? fields.type : ''}
				>
					{#snippet children(f)}
						{#if creating}
							<SearchableSelect
								id={f.id}
								value={mwType}
								options={typeOptions}
								placeholder={m('selectType')}
								searchPlaceholder={m('searchTypes')}
								emptyMessage={m('noTypes')}
								disabled={locked}
								required
								ariaInvalid={f.invalid}
								ariaDescribedby={f.describedBy}
								onchange={onTypeChange}
							/>
						{:else}
							<input id={f.id} value={typeLabel(mwType)} readonly disabled aria-describedby={f.describedBy} />
						{/if}
					{/snippet}
				</Field>
				{#if isPlugin}
					<Field
						id="middleware-plugin"
						label={mv('pluginName')}
						required
						help={mv('pluginNameHelp')}
						error={submitted ? fields.plugin_name : ''}
					>
						{#snippet children(f)}
							<input
								id={f.id}
								bind:value={pluginName}
								oninput={invalidate}
								readonly={!creating}
								disabled={locked || !editable}
								aria-invalid={f.invalid || undefined}
								aria-describedby={f.describedBy}
								autocomplete="off"
								spellcheck="false"
								placeholder="crowdsec-bouncer"
							/>
						{/snippet}
					</Field>
				{/if}
			</div>

			<div
				id="panel-middleware-configuration"
				role="tabpanel"
				aria-labelledby="tab-middleware-configuration"
				hidden={step !== 'middleware-configuration'}
				class="stack"
			>
				{#if !mwType}
					<Notice message={m('selectTypeFirst')} />
				{:else}
					{#if containsStoredSecrets}<Notice message={mv('redactedNotice')} />{/if}
					{#if !isPlugin}
						<Field id="middleware-mode" label={m('modeLabel')}>
							{#snippet children(f)}
								<SelectMenu
									id={f.id}
									value={yamlMode ? 'yaml' : 'form'}
									options={[
										{ value: 'form', label: m('modeForm') },
										{ value: 'yaml', label: m('modeYaml') },
									]}
									disabled={locked || !editable}
									onchange={setMode}
								/>
							{/snippet}
						</Field>
						{#if yamlNote}<Notice tone="warning" message={yamlNote} />{/if}
					{/if}
					{#if yamlMode || isPlugin}
						<Field
							id="middleware-yaml"
							label={m('yamlLabel')}
							help={isPlugin ? mv('pluginSpecHelp') : m('yamlHelp')}
							error={submitted ? fields.yaml : ''}
							required={!isPlugin}
						>
							{#snippet children(f)}
								<YamlCodeEditor
									id={f.id}
									label={m('yamlLabel')}
									bind:value={yamlText}
									oninput={invalidate}
									disabled={locked || !editable}
									invalid={!!fields.yaml}
									describedBy={f.describedBy}
								/>
							{/snippet}
						</Field>
					{:else if schemaNode}
						{#if mwType === 'basicAuth'}
							{#if storedUsers}
								<Field id="middleware-users-mode" label={mv('usersStored')} help={mv('usersStoredHelp')}>
									{#snippet children(f)}
										<SelectMenu
											id={f.id}
											value={usersMode}
											options={[
												{ value: 'keep', label: mv('usersKeep') },
												{ value: 'replace', label: mv('usersReplace') },
											]}
											disabled={locked || !editable}
											onchange={(v) => {
												usersMode = v as 'keep' | 'replace';
												invalidate();
											}}
										/>
									{/snippet}
								</Field>
							{/if}
							{#if usersMode === 'replace' || !storedUsers}
								<CredentialsEditor
									bind:users={credentials}
									required
									disabled={locked || !editable}
									errors={submitted ? fields : {}}
									onchange={invalidate}
								/>
							{/if}
						{/if}
						<ConfigFields
							schemaFields={schemaNode}
							bind:form={formState}
							disabled={locked || !editable}
							errors={fields}
							{submitted}
							{serviceOptions}
							activeType={mwType}
							oninvalidate={invalidate}
						/>
						{#if mwType === 'digestAuth' && fields.users}<Notice tone="error" message={fields.users} />{/if}
					{/if}
				{/if}
			</div>

			<div
				id="panel-middleware-review"
				role="tabpanel"
				aria-labelledby="tab-middleware-review"
				hidden={step !== 'middleware-review'}
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
							<dt>{m('name')}</dt>
							<dd>{mwName}</dd>
						</div>
						<div>
							<dt>{m('type')}</dt>
							<dd>{isPlugin ? `${mv('pluginType')} · ${pluginName}` : typeLabel(mwType)}</dd>
						</div>
						<div>
							<dt>{mv('protocolColumn')}</dt>
							<dd>{mv('kind.' + protocol)}</dd>
						</div>
						{#if staged?.updated_at}<div>
								<dt>{mv('updated')}</dt>
								<dd><RelativeTime value={staged.updated_at} /></dd>
							</div>{/if}
						<div>
							<dt>{rv2('pendingTotal')}</dt>
							<dd>{review.changes?.length ?? 0}</dd>
						</div>
					</SummaryList>
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
					{#if !review.static_diff && !review.dynamic_diff}<Notice tone="info" message={mv('noFileChanges')} />{/if}
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
		<Button onclick={close} disabled={busy}>{result || viewing ? t('common.close') : t('common.cancel')}</Button>
		{#if viewing && current && !current.read_only}
			<Button variant="primary" onclick={() => navigate(parent + '/' + encodeURIComponent(current.name) + '/edit')}>
				{m('edit')}
			</Button>
		{/if}
		{#if !loading && !loadError && !viewing && !result && editable}
			{#if step === 'middleware-review'}
				<Button onclick={() => (step = 'middleware-configuration')} disabled={busy}>{m('back')}</Button>
				{#if review}
					<Button variant="primary" onclick={apply} pending={busy} disabled={busy || serverErrors.length > 0}>
						{t('domainUI.' + (busy ? 'applying' : uncertain ? 'retryApply' : 'apply'))}
					</Button>
				{/if}
			{:else if step === 'middleware-configuration'}
				<Button onclick={() => (step = 'middleware-identity')} disabled={busy}>{m('back')}</Button>
				<Button variant="primary" onclick={stageAndReview} pending={busy} disabled={busy || !mwType}>
					{rv2('stageReview')}
				</Button>
			{:else}
				<Button variant="primary" onclick={() => selectStep('middleware-configuration')} disabled={busy}>
					{m('continue')}
				</Button>
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

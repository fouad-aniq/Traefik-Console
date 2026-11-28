<script lang="ts">
	import Field from '../../shared/ui/Field.svelte';
	import Checkbox from '../../shared/ui/Checkbox.svelte';
	import SelectMenu from '../../shared/ui/SelectMenu.svelte';
	import SearchableSelect from '../../shared/ui/SearchableSelect.svelte';
	import SearchableMultiSelect from '../../shared/ui/SearchableMultiSelect.svelte';
	import Button from '../../shared/ui/Button.svelte';
	import { Eye, EyeOff, Plus, X } from '@lucide/svelte';
	import { t } from '../../shared/localization/i18n';
	import { emptyLeaf, fieldKind, isSensitiveKey, type FormState, type LeafState, type SchemaField } from './models';
	import { numberBounds } from './validation';
	import { remainingTypeGroups, remainingRequiredFields, fieldExample } from './formPresentation';

	/**
	 * Schema-driven configuration controls. Every leaf of the live middleware
	 * schema renders here exactly once — nothing is hidden silently. Presence
	 * is explicit: untouched fields stay omitted; set values (including
	 * explicit false/0/empty) serialize verbatim.
	 */
	let {
		schemaFields,
		form = $bindable(),
		disabled = false,
		errors,
		submitted,
		sensitiveMode = 'none',
		serviceOptions = [],
		activeType = '',
		oninvalidate,
	}: {
		schemaFields: Record<string, SchemaField>;
		/** Presence-aware leaf state owned by the dialog; mutated in place. */
		form: FormState;
		disabled?: boolean;
		errors: Record<string, string>;
		submitted?: boolean;
		/** 'retain' hides stored sensitive leaves behind Stored indicators. */
		sensitiveMode?: 'none' | 'retain' | 'replace';
		serviceOptions?: { value: string; label: string; description?: string }[];
		activeType?: string;
		oninvalidate: () => void;
	} = $props();

	const c = (key: string, args?: Record<string, string | number>) => t(`middlewares.fields.${key}`, args);
	const g = (key: string, args?: Record<string, string | number>) => t(`middlewares.config.${key}`, args);

	let openState: Record<string, boolean> = $state({});
	let revealed: Record<string, boolean> = $state({});

	interface FieldGroup {
		id: string;
		labelKey?: string;
		descriptionKey?: string;
		fields: string[];
		collapsible?: boolean;
		plain?: boolean;
	}

	const TYPE_GROUPS: Record<string, FieldGroup[]> = {
		forwardAuth: [
			{
				id: 'service',
				fields: ['address'],
				plain: true,
			},
			{
				id: 'redirection',
				labelKey: 'forwardAuthRedirection',
				descriptionKey: 'forwardAuthRedirectionDesc',
				fields: ['authSigninURL', 'trustForwardHeader'],
				collapsible: true,
			},
			{
				id: 'headers',
				labelKey: 'forwardAuthHeaders',
				descriptionKey: 'forwardAuthHeadersDesc',
				fields: [
					'authResponseHeaders',
					'authRequestHeaders',
					'headerField',
					'authResponseHeadersRegex',
					'addAuthCookiesToResponse',
				],
				collapsible: true,
			},
			{
				id: 'body',
				labelKey: 'forwardAuthBody',
				descriptionKey: 'forwardAuthBodyDesc',
				fields: [
					'forwardBody',
					'maxBodySize',
					'maxResponseBodySize',
					'preserveLocationHeader',
					'preserveRequestMethod',
				],
				collapsible: true,
			},
			{
				id: 'tls',
				labelKey: 'forwardAuthTls',
				descriptionKey: 'forwardAuthTlsDesc',
				fields: ['tls'],
				collapsible: true,
			},
		],
		basicAuth: [
			{
				id: 'options',
				labelKey: 'moreOptions',
				descriptionKey: 'authOptionsDesc',
				fields: ['realm', 'headerField', 'removeHeader', 'usersFile'],
				collapsible: true,
			},
		],
		digestAuth: [
			{
				id: 'options',
				labelKey: 'moreOptions',
				descriptionKey: 'authOptionsDesc',
				fields: ['realm', 'headerField', 'removeHeader', 'usersFile'],
				collapsible: true,
			},
		],
		rateLimit: [
			{
				id: 'rate',
				fields: ['average'],
				plain: true,
			},
			{
				id: 'burst',
				labelKey: 'rateBurstPeriod',
				descriptionKey: 'rateBurstPeriodDesc',
				fields: ['burst', 'period'],
				collapsible: true,
			},
			{
				id: 'criterion',
				labelKey: 'rateCriterion',
				descriptionKey: 'rateCriterionDesc',
				fields: ['sourceCriterion'],
				collapsible: true,
			},
			{
				id: 'redis',
				labelKey: 'rateRedis',
				descriptionKey: 'rateRedisDesc',
				fields: ['redis'],
				collapsible: true,
			},
		],
		inFlightReq: [
			{
				id: 'limits',
				fields: ['amount'],
				plain: true,
			},
			{
				id: 'criterion',
				labelKey: 'rateCriterion',
				descriptionKey: 'rateCriterionDesc',
				fields: ['sourceCriterion'],
				collapsible: true,
			},
		],
		buffering: [
			{
				id: 'request',
				fields: ['maxRequestBodyBytes'],
				plain: true,
			},
			{
				id: 'requestMemory',
				labelKey: 'bufferingRequestMemory',
				descriptionKey: 'bufferingRequestMemoryDesc',
				fields: ['memRequestBodyBytes'],
				collapsible: true,
			},
			{
				id: 'response',
				labelKey: 'bufferingResponse',
				descriptionKey: 'bufferingResponseDesc',
				fields: ['maxResponseBodyBytes', 'memResponseBodyBytes'],
				collapsible: true,
			},
			{
				id: 'retry',
				labelKey: 'bufferingRetry',
				descriptionKey: 'bufferingRetryDesc',
				fields: ['retryExpression'],
				collapsible: true,
			},
		],
		circuitBreaker: [
			{
				id: 'expression',
				fields: ['expression'],
				plain: true,
			},
			{
				id: 'durations',
				labelKey: 'circuitBreakerDurations',
				descriptionKey: 'circuitBreakerDurationsDesc',
				fields: ['checkPeriod', 'fallbackDuration', 'recoveryDuration'],
				collapsible: true,
			},
			{
				id: 'response',
				labelKey: 'circuitBreakerResponse',
				descriptionKey: 'circuitBreakerResponseDesc',
				fields: ['responseCode'],
				collapsible: true,
			},
		],
		retry: [
			{
				id: 'attempts',
				fields: ['attempts'],
				plain: true,
			},
			{
				id: 'intervals',
				labelKey: 'retryIntervals',
				descriptionKey: 'retryIntervalsDesc',
				fields: ['initialInterval', 'timeout'],
				collapsible: true,
			},
			{
				id: 'conditions',
				labelKey: 'retryConditions',
				descriptionKey: 'retryConditionsDesc',
				fields: ['status', 'maxRequestBodyBytes', 'disableRetryOnNetworkError', 'retryNonIdempotentMethod'],
				collapsible: true,
			},
		],
		ipAllowList: [
			{
				id: 'sourceRange',
				fields: ['sourceRange'],
				plain: true,
			},
			{
				id: 'strategy',
				labelKey: 'ipAllowListStrategy',
				descriptionKey: 'ipAllowListStrategyDesc',
				fields: ['ipStrategy'],
				collapsible: true,
			},
			{
				id: 'rejection',
				labelKey: 'ipAllowListResponse',
				descriptionKey: 'ipAllowListResponseDesc',
				fields: ['rejectStatusCode'],
				collapsible: true,
			},
		],
		ipWhiteList: [
			{
				id: 'sourceRange',
				fields: ['sourceRange'],
				plain: true,
			},
			{
				id: 'strategy',
				labelKey: 'ipAllowListStrategy',
				descriptionKey: 'ipAllowListStrategyDesc',
				fields: ['ipStrategy'],
				collapsible: true,
			},
			{
				id: 'rejection',
				labelKey: 'ipAllowListResponse',
				descriptionKey: 'ipAllowListResponseDesc',
				fields: ['rejectStatusCode'],
				collapsible: true,
			},
		],
		...remainingTypeGroups,
	};

	function leafLabel(key: string): string {
		if (activeType === 'digestAuth' && key === 'usersFile') {
			const localized = t('middlewares.labels.usersFileDigest');
			if (localized && !localized.startsWith('middlewares.labels.')) return localized;
		}
		const localized = t(`middlewares.labels.${key}`);
		if (localized && !localized.startsWith('middlewares.labels.')) return localized;
		return key.replace(/([a-z0-9])([A-Z])/g, '$1 $2').replace(/^./, (ch) => ch.toUpperCase());
	}

	function isFieldRequired(type: string, key: string, path: string): boolean {
		if (type in remainingRequiredFields) return remainingRequiredFields[type].includes(path);
		if (type === 'buffering' && key === 'maxRequestBodyBytes') return true;
		if (type === 'retry' && key === 'attempts') return true;
		if (type === 'inFlightReq' && key === 'amount') return true;
		if (type === 'rateLimit' && key === 'average') return true;
		if (type === 'forwardAuth' && key === 'address') return true;
		if (type === 'stripPrefix' && key === 'prefixes') return true;
		if (type === 'stripPrefixRegex' && key === 'regex') return true;
		if ((type === 'replacePathRegex' || type === 'redirectRegex') && (key === 'regex' || key === 'replacement'))
			return true;
		if (type === 'errors' && (key === 'status' || key === 'service')) return true;
		if (type === 'circuitBreaker' && key === 'expression') return true;
		if ((type === 'ipAllowList' || type === 'ipWhiteList') && key === 'sourceRange') return true;
		if (type === 'redirectScheme' && key === 'scheme') return true;
		if (type === 'addPrefix' && key === 'prefix') return true;
		if (type === 'replacePath' && key === 'path') return true;
		return false;
	}

	/** Mirrors the hint keys from validation.ts for help text. */
	const HINT_HELP_KEYS: Record<string, string> = {
		customRequestHeaders: 'helpHeaderMap',
		customResponseHeaders: 'helpHeaderMap',
		sslProxyHeaders: 'helpHeaderMap',
		prefix: 'helpPath',
		path: 'helpPath',
		prefixes: 'helpPathList',
		regex: 'helpRegex',
		replacement: 'helpReplacement',
		scheme: 'helpScheme',
		port: 'helpPort',
		expression: 'helpExpression',
		retryExpression: 'helpExpression',
		period: 'helpDuration',
		checkPeriod: 'helpDuration',
		fallbackDuration: 'helpDuration',
		recoveryDuration: 'helpDuration',
		initialInterval: 'helpDuration',
		timeout: 'helpDuration',
		dialTimeout: 'helpDuration',
		readTimeout: 'helpDuration',
		writeTimeout: 'helpDuration',
		attempts: 'helpAttempts',
		responseCode: 'helpStatusSingle',
		rejectStatusCode: 'helpStatusSingle',
		amount: 'helpAmount',
		sourceRange: 'helpCidrList',
		excludedIPs: 'helpCidrList',
		depth: 'helpDepth',
		ipv6Subnet: 'helpIpv6Subnet',
		address: 'helpAddress',
		authSigninURL: 'helpSigninUrl',
		authResponseHeadersRegex: 'helpRegex',
		accessControlMaxAge: 'helpSeconds',
		stsSeconds: 'helpSeconds',
		encodings: 'helpEncodings',
		includedContentTypes: 'helpMediaList',
		excludedContentTypes: 'helpMediaList',
		defaultEncoding: 'helpEncoding',
		minResponseBodyBytes: 'helpBytes',
		maxRequestBodyBytes: 'helpBytes',
		maxResponseBodyBytes: 'helpBytes',
		memRequestBodyBytes: 'helpBytes',
		memResponseBodyBytes: 'helpBytes',
		status: 'helpStatusList',
		statusRewrites: 'helpStatusRewrites',
		service: 'helpServiceRef',
		query: 'helpQuery',
		middlewares: 'helpChainMembers',
		headerField: 'helpHeaderName',
		requestHeaderName: 'helpHeaderName',
		realm: 'helpRealm',
		endpoints: 'helpRedisEndpoints',
		db: 'helpRedisDb',
		poolSize: 'helpRedisPool',
		maxActiveConns: 'helpRedisPool',
		minIdleConns: 'helpRedisPool',
		insecureSkipVerify: 'helpSkipVerify',
		caOptional: 'helpCaOptional',
		pem: 'helpPem',
		ca: 'helpAsset',
		cert: 'helpAsset',
		key: 'helpAsset',
		usersFile: 'helpAsset',
	};

	function leafHelp(key: string, fullPath: string): string {
		if (activeType === 'encodedCharacters') return '';
		const hintKey = HINT_HELP_KEYS[fullPath] ?? HINT_HELP_KEYS[key] ?? '';
		return hintKey ? c(hintKey) : '';
	}

	const ph = (key: string, args?: Record<string, string | number>) => t(`middlewares.placeholders.${key}`, args);
	function placeholderFor(key: string, path: string, kind: string): string {
		const example = fieldExample(activeType, path);
		if (kind === 'list') return example ? ph('listExample', { value: example }) : ph('list');
		if (example) return ph('example', { value: example });
		if (kind === 'number') return ph('example', { value: '0' });
		return ph('text', { label: leafLabel(key).toLocaleLowerCase() });
	}
	function mapPlaceholder(key: string, side: 'key' | 'value'): string {
		if (key === 'statusRewrites') return ph(side === 'key' ? 'statusFrom' : 'statusTo');
		if (key === 'customRequestHeaders') return ph('example', { value: side === 'key' ? 'X-Request-ID' : 'console' });
		if (key === 'customResponseHeaders')
			return ph('example', { value: side === 'key' ? 'X-Frame-Options' : 'SAMEORIGIN' });
		if (key === 'sslProxyHeaders') return ph('example', { value: side === 'key' ? 'X-Forwarded-Proto' : 'https' });
		return g(side === 'key' ? 'keyPlaceholder' : 'valuePlaceholder');
	}

	function isObject(node: SchemaField): boolean {
		return fieldKind(node) === 'object';
	}

	/** Sensitive leaves are hidden (retain) — the stored values are never returned. */
	function storedLeaf(key: string): boolean {
		return sensitiveMode === 'retain' && isSensitiveKey(key);
	}

	function isLeafConfigured(leaf: LeafState | undefined): boolean {
		if (!leaf) return false;
		if (leaf.kind === 'bool') return leaf.value === true;
		if (leaf.kind === 'text') return leaf.value.trim().length > 0;
		if (leaf.kind === 'number') return leaf.raw.trim().length > 0;
		if (leaf.kind === 'tri') return leaf.value === 'yes' || leaf.value === 'no';
		if (leaf.kind === 'list') return leaf.items.length > 0;
		if (leaf.kind === 'map') return leaf.entries.some((entry) => entry.key.trim().length > 0);
		return true;
	}

	function subtreeHasContent(fields: Record<string, SchemaField>, base: string): boolean {
		for (const [key, node] of Object.entries(fields)) {
			const p = base ? `${base}.${key}` : key;
			if (sensitiveMode === 'retain' && isSensitiveKey(key)) continue;
			if (isObject(node)) {
				if (subtreeHasContent(node.fields, p)) return true;
			} else if (isLeafConfigured(form[p])) return true;
		}
		return false;
	}

	function subtreeErrorCount(fields: Record<string, SchemaField>, base: string): number {
		let count = 0;
		for (const [key, node] of Object.entries(fields)) {
			const p = base ? `${base}.${key}` : key;
			if (isObject(node)) count += subtreeErrorCount(node.fields, p);
			else if (submitted && errors[p]) count++;
		}
		return count;
	}

	function groupConfiguredCount(fields: string[]): number {
		let count = 0;
		for (const key of fields) {
			const node = schemaFields[key];
			if (!node) continue;
			if (isObject(node)) {
				if (subtreeHasContent({ [key]: node }, '')) count++;
			} else if (isLeafConfigured(form[key])) {
				count++;
			}
		}
		return count;
	}

	function groupErrorCount(fields: string[]): number {
		let count = 0;
		for (const key of fields) {
			const node = schemaFields[key];
			if (!node) continue;
			if (isObject(node)) {
				count += subtreeErrorCount(node.fields, key);
			} else if (submitted && errors[key]) {
				count++;
			}
		}
		return count;
	}
</script>

{#snippet leafControl(key: string, node: SchemaField, prefix: string)}
	{@const p = prefix ? `${prefix}.${key}` : key}
	{@const kind = fieldKind(node)}
	{@const label = leafLabel(key)}
	{@const help = leafHelp(key, p)}
	{@const error = submitted ? (errors[p] ?? '') : ''}
	{@const leaf = form[p] ?? emptyLeaf(kind)}
	{@const concealed = isSensitiveKey(key) && sensitiveMode === 'replace'}
	{@const required = isFieldRequired(activeType, key, p)}
	{#if kind === 'bool'}
		<Checkbox
			id={`mw-${p}`}
			checked={leaf.kind === 'bool' && leaf.value}
			{label}
			help={error ? '' : help}
			{disabled}
			onchange={(checked) => {
				if (checked) form[p] = { kind: 'bool', present: true, value: true };
				else delete form[p];
				oninvalidate();
			}}
		/>
		{#if error}<p class="inline-error" id={`mw-${p}-error`} role="alert">{error}</p>{/if}
	{:else if kind === 'tri'}
		<Field id={`mw-${p}`} {label} help={error ? '' : help} {error} {required}>
			{#snippet children(f)}
				<SelectMenu
					id={f.id}
					{required}
					value={leaf.kind === 'tri' ? leaf.value : 'unset'}
					options={[
						{ value: 'unset', label: g('triDefault') },
						{ value: 'yes', label: g('triYes') },
						{ value: 'no', label: g('triNo') },
					]}
					{disabled}
					onchange={(value) => {
						if (value === 'unset') {
							delete form[p];
						} else {
							form[p] = { kind: 'tri', value: value as 'yes' | 'no' };
						}
						oninvalidate();
					}}
				/>
			{/snippet}
		</Field>
	{:else if kind === 'number'}
		{@const bounds = numberBounds(p)}
		<Field id={`mw-${p}`} {label} help={error ? '' : help} {error} {required}>
			{#snippet children(f)}
				<input
					id={f.id}
					type="number"
					step="1"
					min={bounds.min}
					max={bounds.max}
					value={leaf.kind === 'number' ? leaf.raw : ''}
					placeholder={placeholderFor(key, p, kind)}
					{disabled}
					{required}
					aria-required={required ? 'true' : undefined}
					inputmode="numeric"
					aria-invalid={error ? 'true' : undefined}
					aria-describedby={f.describedBy}
					oninput={(event) => {
						const val = event.currentTarget.value;
						if (val === '') {
							delete form[p];
						} else {
							form[p] = { kind: 'number', raw: val };
						}
						oninvalidate();
					}}
				/>
			{/snippet}
		</Field>
	{:else if kind === 'list'}
		<div class="tag-control" role="group" aria-label={label}>
			<label class="control-label" for={`mw-${p}-tag-input`}>
				{label}{#if required}<span class="required" aria-hidden="true">*</span>{/if}
			</label>
			<div class="tag-box" class:has-error={!!error}>
				{#each leaf.kind === 'list' ? leaf.items : [] as item, index (index)}
					<span class="tag-chip">
						<span class="tag-chip-text">{item}</span>
						<button
							type="button"
							class="tag-chip-remove"
							{disabled}
							aria-label={g('removeTag', { tag: item }) || `Remove ${item}`}
							onclick={() => {
								const currentItems = leaf.kind === 'list' ? leaf.items : [];
								const updated = currentItems.toSpliced(index, 1);
								if (updated.length === 0) {
									delete form[p];
								} else {
									form[p] = { kind: 'list', items: updated, present: true };
								}
								oninvalidate();
							}}
						>
							<X size={12} aria-hidden="true" />
						</button>
					</span>
				{/each}
				<input
					id={`mw-${p}-tag-input`}
					class="tag-input"
					type="text"
					placeholder={placeholderFor(key, p, kind)}
					aria-required={required ? 'true' : undefined}
					aria-invalid={error ? 'true' : undefined}
					aria-describedby={[help ? `mw-${p}-help` : '', error ? `mw-${p}-error` : ''].filter(Boolean).join(' ') ||
						undefined}
					{disabled}
					aria-label={label}
					onkeydown={(event) => {
						if (event.key === 'Enter' || (event.key === ',' && !key.toLowerCase().includes('regex'))) {
							event.preventDefault();
							const val = event.currentTarget.value.trim().replace(/^,+|,+$/g, '');
							if (val) {
								const currentItems = leaf.kind === 'list' ? leaf.items : [];
								form[p] = { kind: 'list', items: [...currentItems, val], present: true };
								event.currentTarget.value = '';
								oninvalidate();
							}
						} else if (event.key === 'Backspace' && event.currentTarget.value === '') {
							const currentItems = leaf.kind === 'list' ? leaf.items : [];
							if (currentItems.length > 0) {
								const updated = currentItems.slice(0, -1);
								if (updated.length === 0) delete form[p];
								else form[p] = { kind: 'list', items: updated, present: true };
								oninvalidate();
							}
						}
					}}
				/>
			</div>
			{#if help}<p class="control-help" id={`mw-${p}-help`}>{help}</p>{/if}
			{#if error}<p class="control-error" id={`mw-${p}-error`} role="alert">{error}</p>{/if}
		</div>
	{:else if kind === 'map' || kind === 'mapint'}
		<div class="map-control" role="group" aria-label={label}>
			<div class="map-header">
				<label class="control-label map-label" for={`mw-${p}-add`}>
					{label}{#if HINT_HELP_KEYS[key] === 'helpHeaderMap'}<span class="map-hint">{g('headerMapHint')}</span>{/if}
				</label>
				<button
					id={`mw-${p}-add`}
					type="button"
					class="add-inline-btn"
					{disabled}
					onclick={() => {
						const currentEntries = leaf.kind === 'map' ? leaf.entries : [];
						form[p] = { kind: 'map', numeric: kind === 'mapint', entries: [...currentEntries, { key: '', value: '' }] };
						oninvalidate();
					}}
				>
					<Plus size={14} aria-hidden="true" />
					<span>{g('addEntryCompact')}</span>
				</button>
			</div>
			{#if leaf.kind === 'map' && leaf.entries.length > 0}
				<div class="map-entries">
					{#each leaf.entries as entry, index (index)}
						<div class="entry-row map-row">
							<input
								id={`mw-${p}-${index}-key`}
								class="entry-input"
								type="text"
								value={entry.key}
								placeholder={mapPlaceholder(key, 'key')}
								{disabled}
								aria-label={`${label} — ${g('keyLabel')} ${index + 1}`}
								aria-invalid={error ? 'true' : undefined}
								oninput={(event) => {
									form[p] = {
										kind: 'map',
										numeric: kind === 'mapint',
										entries: (leaf.kind === 'map' ? [...leaf.entries] : []).with(index, {
											key: event.currentTarget.value,
											value: entry.value,
										}),
									};
									oninvalidate();
								}}
							/>
							<input
								id={`mw-${p}-${index}-value`}
								class="entry-input"
								type={kind === 'mapint' ? 'number' : 'text'}
								min={kind === 'mapint' ? 100 : undefined}
								max={kind === 'mapint' ? 599 : undefined}
								step={kind === 'mapint' ? 1 : undefined}
								value={entry.value}
								aria-invalid={error ? 'true' : undefined}
								aria-describedby={error ? `mw-${p}-error` : undefined}
								placeholder={mapPlaceholder(key, 'value')}
								{disabled}
								aria-label={`${label} — ${g('valueLabel')} ${index + 1}`}
								oninput={(event) => {
									form[p] = {
										kind: 'map',
										numeric: kind === 'mapint',
										entries: (leaf.kind === 'map' ? [...leaf.entries] : []).with(index, {
											key: entry.key,
											value: event.currentTarget.value,
										}),
									};
									oninvalidate();
								}}
							/>
							<button
								type="button"
								class="entry-remove"
								{disabled}
								aria-label={g('removeEntry', { number: index + 1 })}
								onclick={() => {
									const updated = (leaf.kind === 'map' ? [...leaf.entries] : []).toSpliced(index, 1);
									if (updated.length === 0) {
										delete form[p];
									} else {
										form[p] = { kind: 'map', numeric: kind === 'mapint', entries: updated };
									}
									oninvalidate();
								}}
							>
								<X size={16} aria-hidden="true" />
							</button>
						</div>
					{/each}
				</div>
			{/if}
			{#if help && HINT_HELP_KEYS[key] !== 'helpHeaderMap'}<p class="control-help" id={`mw-${p}-help`}>{help}</p>{/if}
			{#if error}<p class="control-error" id={`mw-${p}-error`} role="alert">{error}</p>{/if}
		</div>
	{:else if HINT_HELP_KEYS[key] === 'helpServiceRef'}
		<Field id={`mw-${p}`} {label} help={error ? '' : help} {error} {required}>
			{#snippet children(f)}
				<SearchableSelect
					id={f.id}
					{required}
					value={leaf.kind === 'text' ? leaf.value : ''}
					options={serviceOptions.filter((option) => option.value)}
					placeholder={g('selectService')}
					searchPlaceholder={g('searchServices')}
					emptyMessage={g('noServices')}
					{disabled}
					ariaInvalid={!!error}
					ariaDescribedby={f.describedBy}
					onchange={(value) => {
						if (!value) delete form[p];
						else form[p] = { kind: 'text', value };
						oninvalidate();
					}}
				/>
			{/snippet}
		</Field>
	{:else if HINT_HELP_KEYS[key] === 'helpScheme' || HINT_HELP_KEYS[key] === 'helpEncoding'}
		<Field id={`mw-${p}`} {label} help={error ? '' : help} {error} {required}>
			{#snippet children(f)}
				<SelectMenu
					id={f.id}
					{required}
					value={leaf.kind === 'text' ? leaf.value : ''}
					options={[
						{ value: '', label: required ? ph('scheme') : g('triDefault') },
						...(HINT_HELP_KEYS[key] === 'helpScheme' ? ['http', 'https'] : ['gzip', 'br', 'zstd']).map((value) => ({
							value,
							label: value,
						})),
					]}
					{disabled}
					onchange={(value) => {
						if (!value) delete form[p];
						else form[p] = { kind: 'text', value };
						oninvalidate();
					}}
				/>
			{/snippet}
		</Field>
	{:else}
		<Field id={`mw-${p}`} {label} help={error ? '' : help} {error} {required}>
			{#snippet children(f)}
				<div class="text-entry" class:has-reveal={concealed}>
					<input
						id={f.id}
						type={concealed && !revealed[p] ? 'password' : 'text'}
						placeholder={concealed ? ph('secret') : placeholderFor(key, p, kind)}
						value={leaf.kind === 'text' ? leaf.value : ''}
						{disabled}
						{required}
						aria-required={required ? 'true' : undefined}
						spellcheck="false"
						autocomplete={concealed ? 'new-password' : 'off'}
						aria-invalid={error ? 'true' : undefined}
						aria-describedby={f.describedBy}
						oninput={(event) => {
							const val = event.currentTarget.value;
							if (val === '') {
								delete form[p];
							} else {
								form[p] = { kind: 'text', value: val };
							}
							oninvalidate();
						}}
					/>
					{#if concealed}
						<button
							type="button"
							class="reveal"
							{disabled}
							aria-label={revealed[p] ? g('concealValue') : g('revealValue')}
							aria-pressed={!!revealed[p]}
							onclick={() => {
								revealed[p] = !revealed[p];
							}}
						>
							{#if revealed[p]}<EyeOff size={18} aria-hidden="true" />{:else}<Eye size={18} aria-hidden="true" />{/if}
						</button>
					{/if}
				</div>
			{/snippet}
		</Field>
	{/if}
{/snippet}

{#snippet renderFieldNode(key: string, node: SchemaField, prefix: string)}
	{@const p = prefix ? `${prefix}.${key}` : key}
	{@const hiddenStored = sensitiveMode === 'retain' && isSensitiveKey(key)}
	{@const kind = fieldKind(node)}
	{#if activeType === 'passTLSClientCert' && (p === 'info.subject' || p === 'info.issuer')}
		<Field id={`mw-${p}`} label={leafLabel(key)} error={submitted ? (errors[p] ?? '') : ''}>
			{#snippet children(f)}
				<SearchableMultiSelect
					id={f.id}
					values={Object.keys(node.fields).filter((child) => {
						const value = form[`${p}.${child}`];
						return value?.kind === 'bool' && value.value;
					})}
					options={Object.keys(node.fields).map((child) => ({ value: child, label: leafLabel(child) }))}
					maxItems={Object.keys(node.fields).length}
					placeholder={g('selectCertificateFields')}
					searchPlaceholder={g('searchCertificateFields')}
					emptyMessage={g('noCertificateFields')}
					{disabled}
					ariaDescribedby={f.describedBy}
					onchange={(selected) => {
						for (const child of Object.keys(node.fields)) {
							const path = `${p}.${child}`;
							if (selected.includes(child)) form[path] = { kind: 'bool', value: true, present: true };
							else {
								const old = form[path];
								if (old?.kind === 'bool' && old.value) delete form[path];
							}
						}
						oninvalidate();
					}}
				/>
			{/snippet}
		</Field>
	{:else if isObject(node) && !hiddenStored}
		{@const errorCount = submitted === true ? subtreeErrorCount({ [key]: node }, prefix) : 0}
		{@const autoOpen = subtreeHasContent({ [key]: node }, prefix) || (submitted === true && errorCount > 0)}
		<details
			class="nested"
			open={openState[p] ?? autoOpen}
			ontoggle={(event) => {
				openState[p] = (event.currentTarget as HTMLDetailsElement).open;
			}}
		>
			<summary data-invalid={errorCount ? 'true' : undefined}>
				<span class="nested-label">{leafLabel(key)}</span>
				{#if subtreeHasContent({ [key]: node }, prefix)}<span class="configured">{g('configured')}</span>{/if}
				{#if errorCount}<span class="error-count">{errorCount}</span>{/if}
			</summary>
			<div class="nested-body">
				{@render renderLevel(node.fields, p)}
			</div>
		</details>
	{:else if hiddenStored}
		<div class="stored-row">
			<span class="stored-label">{leafLabel(key)}</span>
			<span class="stored-pill">{g('storedSensitive')}</span>
		</div>
	{:else}
		<div class="field-wrapper">
			{@render leafControl(key, node, prefix)}
		</div>
	{/if}
{/snippet}

{#snippet renderLevel(fields: Record<string, SchemaField>, prefix: string)}
	{#each Object.entries(fields) as [key, node] (prefix ? `${prefix}.${key}` : key)}
		{@render renderFieldNode(key, node, prefix)}
	{/each}
{/snippet}

{#if TYPE_GROUPS[activeType]}
	{@const typeGroups = TYPE_GROUPS[activeType]}
	{@const groupedKeys = new Set(typeGroups.flatMap((gr) => gr.fields))}
	{@const unassigned = Object.keys(schemaFields).filter(
		(k) => !groupedKeys.has(k) && !(activeType === 'basicAuth' && k === 'users'),
	)}
	{#each typeGroups as group (group.id)}
		{@const matching = group.fields.filter((k) => schemaFields[k])}
		{#if matching.length > 0}
			{@const count = groupConfiguredCount(matching)}
			{@const errCount = groupErrorCount(matching)}
			{#if group.collapsible}
				<details
					class="options config-group-collapsible"
					id={`group-${group.id}`}
					open={openState[group.id] ?? (count > 0 || errCount > 0)}
					ontoggle={(event) => {
						openState[group.id] = (event.currentTarget as HTMLDetailsElement).open;
					}}
				>
					<summary>
						{t(`middlewares.groups.${group.labelKey}`)}
						<span>{count > 0 ? g('configuredCount', { count }) : g('defaultOptions')}</span>
					</summary>
					<div class="option-fields">
						{#if group.descriptionKey}
							<p class="muted">{t(`middlewares.groups.${group.descriptionKey}`)}</p>
						{/if}
						<div class="group-content">
							{#each matching as key (key)}
								{#if matching.length === 1 && isObject(schemaFields[key])}
									{@render renderLevel(schemaFields[key].fields, key)}
								{:else}
									{@render renderFieldNode(key, schemaFields[key], '')}
								{/if}
							{/each}
						</div>
					</div>
				</details>
			{:else if group.plain}
				<div class="group-plain" id={`group-${group.id}`}>
					{#if group.descriptionKey && group.fields.length > 1}
						<p class="muted">{t(`middlewares.groups.${group.descriptionKey}`)}</p>
					{/if}
					<div class="group-content">
						{#each matching as key (key)}
							{@render renderFieldNode(key, schemaFields[key], '')}
						{/each}
					</div>
				</div>
			{:else}
				<fieldset class="config-group" id={`group-${group.id}`}>
					<legend class="group-legend">
						<span class="group-title">{t(`middlewares.groups.${group.labelKey}`)}</span>
						{#if count > 0}
							<span class="configured-pill">{g('configuredCount', { count })}</span>
						{/if}
					</legend>
					{#if group.descriptionKey}
						<p class="group-desc">{t(`middlewares.groups.${group.descriptionKey}`)}</p>
					{/if}
					<div class="group-content">
						{#each matching as key (key)}
							{@render renderFieldNode(key, schemaFields[key], '')}
						{/each}
					</div>
				</fieldset>
			{/if}
		{/if}
	{/each}
	{#if unassigned.length > 0}
		<fieldset class="config-group">
			<legend class="group-legend">
				<span class="group-title">{t('middlewares.groups.otherSettings')}</span>
			</legend>
			<p class="group-desc">{t('middlewares.groups.otherSettingsDesc')}</p>
			<div class="group-content">
				{#each unassigned as key (key)}
					{@render renderFieldNode(key, schemaFields[key], '')}
				{/each}
			</div>
		</fieldset>
	{/if}
{:else}
	{@render renderLevel(schemaFields, '')}
{/if}

<style>
	.nested {
		border: 1px solid var(--border);
		border-radius: 6px;
		background: var(--surface);
		max-width: 640px;
	}
	.nested > summary {
		display: flex;
		align-items: center;
		gap: 10px;
		padding: 10px 14px;
		font-size: 14px;
		font-weight: 500;
		line-height: 24px;
		cursor: pointer;
		user-select: none;
		color: var(--text);
		list-style: none;
		border-radius: 6px;
	}
	.nested > summary::-webkit-details-marker {
		display: none;
	}
	.nested > summary::before {
		content: '';
		width: 8px;
		height: 8px;
		border-right: 2px solid var(--text-muted);
		border-bottom: 2px solid var(--text-muted);
		transform: rotate(-45deg);
		transition: transform 120ms ease;
		flex-shrink: 0;
	}
	.nested[open] > summary::before {
		transform: rotate(45deg);
	}
	.nested > summary[data-invalid='true'] {
		color: var(--danger-text);
	}
	.nested-label {
		flex: 0 1 auto;
	}
	.configured {
		font-size: 12px;
		line-height: 18px;
		color: var(--connection);
		border: 1px solid var(--border);
		border-radius: 10px;
		padding: 0 8px;
	}
	.error-count {
		display: inline-flex;
		align-items: center;
		justify-content: center;
		min-width: 18px;
		height: 18px;
		padding: 0 4px;
		border: 1px solid currentColor;
		border-radius: 10px;
		font-size: 12px;
		color: var(--danger-text);
	}
	.nested-body {
		display: flex;
		flex-direction: column;
		gap: 20px;
		padding: 16px 14px;
		border-top: 1px solid var(--border);
	}
	.inline-error {
		width: 100%;
		margin: 4px 0 0;
		color: var(--danger-text);
		font-size: 13px;
		line-height: 18px;
		font-weight: 500;
	}
	.control-label {
		font-size: 14px;
		font-weight: 500;
		line-height: 20px;
		color: var(--text);
	}
	.control-help {
		margin: 0;
		color: var(--text-muted);
		font-size: 14px;
		line-height: 20px;
	}
	.control-error {
		margin: 0;
		color: var(--danger-text);
		font-size: 14px;
		line-height: 20px;
		font-weight: 500;
	}
	.tag-control {
		display: flex;
		flex-direction: column;
		gap: 6px;
		width: 100%;
		max-width: 560px;
	}
	.tag-box {
		display: flex;
		flex-wrap: wrap;
		align-items: center;
		gap: 6px;
		min-height: 40px;
		padding: 4px 8px;
		background: var(--surface);
		border: 1px solid var(--outline);
		border-radius: var(--radius-control);
		transition:
			border-color 120ms ease,
			box-shadow 120ms ease;
	}
	.tag-box:focus-within {
		border-color: var(--outline-focus, var(--accent));
		outline: 2px solid var(--outline-focus, var(--accent));
		outline-offset: -1px;
	}
	.tag-box.has-error {
		border-color: var(--danger-text);
	}
	.tag-chip {
		display: inline-flex;
		align-items: center;
		gap: 4px;
		padding: 2px 8px;
		background: var(--surface-muted);
		border: 1px solid var(--border);
		border-radius: 4px;
		font-size: 13px;
		line-height: 20px;
		color: var(--text);
	}
	.tag-chip-text {
		max-width: 220px;
		overflow: hidden;
		text-overflow: ellipsis;
		white-space: nowrap;
	}
	.tag-chip-remove {
		display: grid;
		place-items: center;
		padding: 0;
		border: 0;
		background: transparent;
		color: var(--text-muted);
		cursor: pointer;
		border-radius: 2px;
	}
	.tag-chip-remove:hover:not(:disabled) {
		color: var(--danger-text);
	}
	.tag-chip-remove:disabled {
		opacity: 0.5;
		cursor: default;
	}
	.tag-input {
		flex: 1 1 120px;
		min-width: 80px;
		border: 0;
		background: transparent;
		color: var(--text);
		font: inherit;
		font-size: 16px;
		line-height: 24px;
		padding: 2px 4px;
		outline: none;
	}
	.tag-input::placeholder {
		color: var(--text-muted);
		font-size: 16px;
	}
	.map-control {
		display: flex;
		flex-direction: column;
		gap: 6px;
		width: 100%;
		max-width: 560px;
	}
	.map-label {
		display: flex;
		align-items: baseline;
		flex-wrap: wrap;
		gap: 8px;
	}
	.map-hint {
		font-size: 12px;
		line-height: 18px;
		font-weight: 400;
		color: var(--text-muted);
	}
	.map-header {
		display: flex;
		align-items: center;
		justify-content: space-between;
		gap: 12px;
	}
	.add-inline-btn {
		display: inline-flex;
		align-items: center;
		gap: 4px;
		padding: 3px 8px;
		font-size: 12px;
		font-weight: 500;
		color: var(--text);
		background: var(--surface-muted);
		border: 1px solid var(--border);
		border-radius: 4px;
		cursor: pointer;
		transition: all 120ms ease;
	}
	.add-inline-btn:hover:not(:disabled) {
		background: var(--surface-highlight, var(--surface));
		border-color: var(--outline-focus, var(--accent));
		color: var(--accent);
	}
	.add-inline-btn:disabled {
		opacity: 0.5;
		cursor: default;
	}
	.map-entries {
		display: flex;
		flex-direction: column;
		gap: 8px;
		width: 100%;
	}
	.entry-row {
		display: flex;
		align-items: center;
		gap: 8px;
		width: 100%;
	}
	.map-row .entry-input:first-child {
		flex: 2;
	}
	.map-row .entry-input:last-of-type {
		flex: 3;
	}
	.entry-input {
		flex: 1;
		min-width: 0;
		min-height: 40px;
		padding: 7px 12px;
		background: var(--surface);
		color: var(--text);
		border: 1px solid var(--outline);
		border-radius: var(--radius-control);
		font: inherit;
		font-size: 16px;
		line-height: 24px;
	}
	.entry-input[aria-invalid='true'] {
		border-color: var(--danger-text);
	}
	.entry-remove,
	.reveal {
		display: grid;
		place-items: center;
		width: 36px;
		height: 36px;
		flex-shrink: 0;
		border: 1px solid var(--border);
		border-radius: 6px;
		background: var(--surface-muted);
		color: var(--text-muted);
		cursor: pointer;
	}
	.entry-remove:hover:not(:disabled) {
		color: var(--danger-text);
	}
	.entry-remove:disabled,
	.reveal:disabled {
		opacity: 0.5;
		cursor: default;
	}
	.text-entry {
		display: block;
	}
	.text-entry :global(input) {
		width: 100%;
	}
	.text-entry.has-reveal {
		position: relative;
		display: block;
	}
	.text-entry.has-reveal :global(input) {
		padding-right: 48px;
	}
	.reveal {
		position: absolute;
		right: 2px;
		top: 2px;
		border: 0;
		background: transparent;
	}
	.reveal:hover:not(:disabled) {
		color: var(--text);
		background: var(--surface-muted);
	}
	.reveal:focus-visible {
		outline: 2px solid var(--connection);
		outline-offset: -2px;
	}
	.stored-row {
		display: flex;
		align-items: center;
		flex-wrap: wrap;
		gap: 10px;
		padding: 8px 12px;
		border: 1px solid var(--border);
		border-radius: 6px;
		max-width: 560px;
	}
	.stored-label {
		font-weight: 500;
		font-size: 14px;
	}
	.stored-pill {
		font-size: 12px;
		line-height: 18px;
		color: var(--text-muted);
		border: 1px solid var(--border);
		border-radius: 10px;
		padding: 0 8px;
		background: var(--surface-muted);
	}
	.config-group {
		border: 1px solid var(--border);
		border-radius: 8px;
		padding: 16px 20px;
		margin: 0 0 20px 0;
		background: var(--surface);
		max-width: 560px;
		width: 100%;
	}
	.group-plain {
		width: 100%;
		max-width: 560px;
		margin-bottom: 24px;
	}
	.options {
		border-top: 1px solid var(--border);
		padding-top: 16px;
		padding-bottom: 16px;
		max-width: 560px;
		width: 100%;
	}
	.options[open] {
		padding-bottom: 24px;
	}
	.options summary {
		cursor: pointer;
		font-size: 14px;
		font-weight: 500;
		line-height: 24px;
		color: var(--text);
	}
	.required {
		color: var(--danger-text);
		margin-inline-start: 4px;
	}
	/* Secondary text sits beside the title, as in every other collapsible section. */
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
	.muted {
		margin: 0 0 16px 0;
		color: var(--text-muted);
		font-size: 14px;
		line-height: 21px;
	}
	.group-legend {
		display: flex;
		align-items: center;
		gap: 8px;
		padding: 0 6px;
		font-size: 14px;
		font-weight: 600;
		color: var(--text);
	}
	.group-title {
		letter-spacing: -0.01em;
	}
	.configured-pill {
		font-size: 11px;
		font-weight: 500;
		line-height: 16px;
		color: var(--connection);
		background: var(--surface-muted);
		border: 1px solid var(--border);
		border-radius: 10px;
		padding: 0 6px;
	}
	.group-desc {
		margin: 2px 0 16px 0;
		font-size: 12px;
		line-height: 18px;
		color: var(--text-muted);
	}
	.group-content {
		display: flex;
		flex-direction: column;
		gap: 16px;
	}
	.field-wrapper {
		display: flex;
		flex-direction: column;
		width: 100%;
		max-width: 560px;
	}
	.field-wrapper :global(.field) {
		width: 100%;
	}
	@media (max-width: 767px) {
		.tag-box {
			min-height: var(--touch-h);
		}
		.entry-input {
			min-height: 44px;
		}
		.entry-remove {
			width: 44px;
			height: 44px;
		}
		.reveal {
			width: 40px;
			height: 40px;
		}
	}
</style>

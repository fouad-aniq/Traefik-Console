<script lang="ts">
	/**
	 * One backend server row.
	 *
	 *   HTTP:     [protocol] [host] : [port]  (/) (−)    and, when added, a path input below
	 *   TCP/UDP:  [host] : [port]  (−)
	 *
	 * Typing or pasting a whole URL (or host:port/path) into the host field fills every field. The
	 * value given to and received from the parent is the whole URL or address.
	 */
	import { CircleMinus, Slash } from '@lucide/svelte';
	import SelectMenu from '../../shared/ui/SelectMenu.svelte';
	import { SCHEMES } from './models';
	import { carriesMore, joinAddress, joinUrl, parseAddress, parseUrl } from './backendParts';
	import { t } from '../../shared/localization/i18n';

	let {
		id,
		kind = 'url',
		number,
		value,
		onchange,
		onremove,
		error = '',
		disabled = false,
		removable = true,
	}: {
		id: string;
		/** url: an HTTP backend; address: a TCP or UDP backend. */
		kind?: 'url' | 'address';
		number: number;
		value: string;
		onchange: (value: string) => void;
		onremove: () => void;
		error?: string;
		disabled?: boolean;
		removable?: boolean;
	} = $props();

	const sv = (key: string, args?: Record<string, string | number>) => t('serviceV2.' + key, args);
	const url = $derived(kind === 'url');

	let scheme = $state('http');
	let host = $state('');
	let port = $state('');
	let path = $state('');
	let pathOpen = $state(false);
	let received: string | undefined;

	function load(v: string) {
		if (url) {
			const p = parseUrl(v, scheme);
			({ scheme, host, port, path } = p);
			if (path) pathOpen = true;
		} else {
			({ host, port } = parseAddress(v));
		}
	}
	$effect(() => {
		if (value !== received) {
			load(value);
			received = value;
		}
	});

	function emit() {
		received = url ? joinUrl({ scheme, host, port, path }) : joinAddress({ host, port });
		onchange(received);
	}
	function hostInput(text: string) {
		if (carriesMore(text)) load(text);
		else host = text;
		emit();
	}
	function togglePath() {
		if (pathOpen) {
			path = '';
			pathOpen = false;
			emit();
		} else pathOpen = true;
	}

	/** Point at the field at fault when it can be told from the value alone. */
	const portBad = $derived(port.trim() !== '' && !(/^\d{1,5}$/.test(port.trim()) && +port >= 1 && +port <= 65535));
	const portMissing = $derived(!url && !!error && host.trim() !== '' && port.trim() === '');
	const hostBad = $derived(!!error && !portBad && !portMissing);
</script>

<div class="backend">
	<div class="row">
		<div class="control" class:invalid={!!error} class:disabled class:url>
			{#if url}
				<div class="addon">
					<label class="sr-only" for={`${id}-scheme`}>{sv('protocol')}</label>
					<SelectMenu
						id={`${id}-scheme`}
						value={scheme}
						options={SCHEMES.map((v) => ({ value: v, label: v }))}
						{disabled}
						onchange={(v) => {
							scheme = v;
							emit();
						}}
					/>
				</div>
			{/if}
			<input
				{id}
				class="host"
				data-embedded-input
				value={host}
				oninput={(e) => hostInput(e.currentTarget.value)}
				placeholder={sv(url ? 'hostPlaceholder' : 'addressHostPlaceholder')}
				aria-label={sv('hostLabel', { number })}
				aria-invalid={hostBad || undefined}
				aria-describedby={error ? `${id}-error` : undefined}
				{disabled}
				autocomplete="off"
				spellcheck="false"
			/>
			<span class="colon" aria-hidden="true">:</span>
			<input
				id={`${id}-port`}
				class="port"
				data-embedded-input
				value={port}
				oninput={(e) => {
					port = e.currentTarget.value.trim();
					emit();
				}}
				placeholder={sv(url ? 'portOptional' : 'port')}
				aria-label={sv('portLabel', { number })}
				aria-invalid={portBad || portMissing || undefined}
				aria-describedby={error ? `${id}-error` : undefined}
				inputmode="numeric"
				{disabled}
				autocomplete="off"
			/>
		</div>
		{#if url}
			<button
				type="button"
				class="icon"
				class:active={pathOpen}
				onclick={togglePath}
				{disabled}
				aria-pressed={pathOpen}
				aria-label={sv(pathOpen ? 'removePath' : 'addPath', { number })}
				title={sv(pathOpen ? 'removePath' : 'addPath', { number })}
			>
				<Slash size={16} aria-hidden="true" />
			</button>
		{/if}
		<button
			type="button"
			class="icon remove"
			onclick={onremove}
			disabled={disabled || !removable}
			aria-label={sv('removeServer', { number })}
			title={sv('removeServer', { number })}
		>
			<CircleMinus size={18} aria-hidden="true" />
		</button>
	</div>
	{#if url && pathOpen}
		<div class="path-row">
			<label class="sr-only" for={`${id}-path`}>{sv('pathLabel', { number })}</label>
			<div class="control path">
				<span class="prefix" aria-hidden="true">{sv('pathPrefix')}</span>
				<input
					id={`${id}-path`}
					data-embedded-input
					value={path}
					oninput={(e) => {
						path = e.currentTarget.value;
						emit();
					}}
					placeholder="/api"
					{disabled}
					autocomplete="off"
					spellcheck="false"
				/>
			</div>
		</div>
	{/if}
	{#if error}<p class="field-error" id={`${id}-error`} role="alert">{error}</p>{/if}
</div>

<style>
	.backend {
		display: flex;
		flex-direction: column;
		gap: 6px;
		min-width: 0;
	}
	.row {
		display: flex;
		align-items: center;
		gap: 4px;
		min-width: 0;
	}
	/* Same height, border, radius and type as every other input. */
	.control {
		flex: 1;
		display: flex;
		align-items: stretch;
		min-width: 0;
		min-height: var(--control-h);
		border: 1px solid var(--outline);
		border-radius: var(--radius-control);
		background: var(--surface);
		overflow: hidden;
	}
	.control:focus-within {
		outline: 2px solid var(--focus-ring, var(--connection));
		outline-offset: 2px;
	}
	.control.invalid {
		border-color: var(--danger-text);
		box-shadow: 0 0 0 3px color-mix(in srgb, var(--danger-text) 16%, transparent);
	}
	.control.disabled {
		opacity: 0.6;
	}
	.addon {
		flex: none;
		display: flex;
		width: 96px;
		border-right: 1px solid var(--outline);
		background: var(--surface-muted);
	}
	.addon :global(.select-menu) {
		display: flex;
		height: 100%;
	}
	.addon :global(button) {
		height: 100%;
		min-height: 0;
		gap: 6px;
		border: 0;
		border-radius: 0;
		background: transparent;
		padding: 0 10px 0 12px;
		font-size: 16px;
	}
	.addon :global(button svg) {
		width: 16px;
		height: 16px;
	}
	.addon :global(button:focus-visible) {
		outline: none;
	}
	input {
		min-width: 0;
		border: 0;
		background: transparent;
		color: var(--text);
		font: inherit;
		font-size: 16px;
		line-height: 24px;
		padding: 7px 12px;
		outline: none;
	}
	.host {
		flex: 1;
	}
	.colon {
		display: flex;
		align-items: center;
		color: var(--text-muted);
		font-size: 16px;
	}
	.port {
		flex: none;
		width: 92px;
		padding-left: 8px;
	}
	input[aria-invalid='true'] {
		color: var(--danger-text);
	}
	.icon {
		flex: none;
		display: inline-flex;
		align-items: center;
		justify-content: center;
		width: 36px;
		height: 36px;
		border: 0;
		border-radius: 50%;
		background: transparent;
		color: var(--text-muted);
		cursor: pointer;
	}
	.icon:hover:not(:disabled) {
		background: var(--surface-muted);
		color: var(--text);
	}
	.icon.active {
		color: var(--connection);
		background: var(--connection-soft);
	}
	.remove:hover:not(:disabled) {
		background: var(--danger-bg);
		color: var(--danger-text);
	}
	.icon:disabled {
		opacity: 0.35;
		cursor: default;
	}
	/* The path sits under the address, aligned with the host field. */
	.path-row {
		display: flex;
		padding-left: 96px;
		padding-right: 80px;
	}
	.control.path .prefix {
		display: flex;
		align-items: center;
		padding-left: 12px;
		color: var(--text-muted);
		font-size: 13px;
		white-space: nowrap;
	}
	.control.path input {
		flex: 1;
	}
	.field-error {
		margin: 0;
		font-size: 13px;
		line-height: 20px;
		color: var(--danger-text);
	}
	@media (max-width: 767px) {
		.control {
			min-height: var(--touch-h);
		}
		.addon {
			width: 84px;
		}
		.port {
			width: 72px;
		}
		.icon {
			width: 44px;
			height: 44px;
		}
		.path-row {
			padding-left: 0;
			padding-right: 0;
		}
	}
</style>

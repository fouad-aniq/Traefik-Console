<script lang="ts">
	/**
	 * One hostname of a domain-bound router, drawn like a backend server row of a service:
	 *
	 *   [ subdomain              . | example.com ▾ ]  (−)
	 *
	 * The subdomain is optional (empty: the domain itself); the domain is one of the managed domains.
	 */
	import { CircleMinus } from '@lucide/svelte';
	import SearchableSelect from '../../shared/ui/SearchableSelect.svelte';
	import { t } from '../../shared/localization/i18n';

	let {
		id,
		domainId,
		number,
		subdomain,
		domain,
		options,
		onsubdomain,
		ondomain,
		onremove,
		error = '',
		disabled = false,
		removable = true,
	}: {
		/** id of the subdomain input */
		id: string;
		/** id of the domain picker (its listbox is `${domainId}-listbox`) */
		domainId: string;
		number: number;
		subdomain: string;
		domain: string;
		options: { value: string; label: string; description?: string; badge?: string }[];
		onsubdomain: (value: string) => void;
		ondomain: (value: string) => void;
		onremove: () => void;
		error?: string;
		disabled?: boolean;
		removable?: boolean;
	} = $props();

	const r = (key: string, args?: Record<string, string | number>) => t('routes.' + key, args);
	const rr = (key: string, args?: Record<string, string | number>) => t('routeV2.' + key, args);
</script>

<div class="hostname">
	<div class="row">
		<div class="control" class:invalid={!!error} class:disabled>
			<input
				{id}
				class="sub"
				data-embedded-input
				value={subdomain}
				oninput={(e) => onsubdomain(e.currentTarget.value)}
				placeholder={rr('subdomainPlaceholder')}
				aria-label={rr('subdomainLabel', { number })}
				aria-invalid={!!error || undefined}
				aria-describedby={error ? `${id}-error` : undefined}
				{disabled}
				autocomplete="off"
				spellcheck="false"
			/>
			<span class="dot" aria-hidden="true">.</span>
			<div class="addon">
				<SearchableSelect
					id={domainId}
					value={domain}
					{options}
					onchange={ondomain}
					placeholder={r('selectDomain')}
					searchPlaceholder={r('selectDomain')}
					emptyMessage={r('noDomainsAvailable')}
					ariaLabel={rr('domainLabel', { number })}
					ariaInvalid={!!error}
					ariaDescribedby={error ? `${id}-error` : undefined}
					disabled={disabled || !options.length}
				/>
			</div>
		</div>
		<button
			type="button"
			class="icon remove"
			onclick={onremove}
			disabled={disabled || !removable}
			aria-label={rr('removeHostname', { number })}
			title={rr('removeHostname', { number })}
		>
			<CircleMinus size={18} aria-hidden="true" />
		</button>
	</div>
	{#if error}<p class="field-error" id={`${id}-error`} role="alert">{error}</p>{/if}
</div>

<style>
	/* Same box, height and type as the backend server rows of a service. */
	.hostname {
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
	input {
		flex: 2 1 0;
		min-width: 0;
		border: 0;
		background: transparent;
		color: var(--text);
		font: inherit;
		font-size: 16px;
		line-height: 24px;
		padding: 7px 4px 7px 12px;
		outline: none;
	}
	.dot {
		display: flex;
		align-items: center;
		padding: 0 2px 0 0;
		color: var(--text-muted);
		font-size: 16px;
		font-weight: 600;
	}
	/* The domain sits on the right like the port of a backend: a muted addon holding the picker. */
	.addon {
		flex: 3 1 0;
		display: flex;
		min-width: 150px;
		border-left: 1px solid var(--outline);
		background: var(--surface-muted);
	}
	.addon :global(.searchable-select) {
		display: flex;
		height: 100%;
	}
	.addon :global(button[role='combobox']) {
		height: 100%;
		min-height: 0;
		border: 0;
		border-radius: 0;
		background: transparent;
		padding: 0 10px 0 12px;
	}
	.addon :global(button[role='combobox']:focus-visible) {
		outline: none;
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
	.remove:hover:not(:disabled) {
		background: var(--danger-bg);
		color: var(--danger-text);
	}
	.icon:disabled {
		opacity: 0.35;
		cursor: default;
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
			min-width: 120px;
		}
		.icon {
			width: 44px;
			height: 44px;
		}
	}
</style>

<script lang="ts">
	import FieldControl from '../../shared/ui/Field.svelte';
	import SearchableSelect from '../../shared/ui/SearchableSelect.svelte';
	import { Eye, EyeOff } from '@lucide/svelte';
	import { t } from '../../shared/localization/i18n';
	import { isMasked, isSecretField, type ProviderField } from './models';

	let {
		field,
		label,
		help,
		required = false,
		value = '',
		error = '',
		disabled = false,
		onchange,
	}: {
		field: ProviderField;
		label: string;
		help: string;
		required?: boolean;
		value?: string;
		error?: string;
		disabled?: boolean;
		onchange: (v: string) => void;
	} = $props();

	// core-v2 returns `••••••••` for a stored secret; sending it back unchanged retains it.
	// It can never create a missing value, so "Replace" always clears to a fresh input.
	const stored = $derived(isMasked(value));
	// Only an explicit `true` marks a credential. Anything else (missing, or a
	// value mangled in transit) is a plain setting such as a TTL or a timeout.
	const secret = $derived(isSecretField(field));
	const numeric = $derived(field.type === 'integer');
	let revealed = $state(false);
	const c = (key: string) => t('resolverUI.' + key);
	const choices = $derived([
		{ value: '', label: c('providerDefault') + (field.default_value ? ` (${field.default_value})` : '') },
		...(field.type === 'boolean'
			? [
					{ value: 'true', label: c('yes') },
					{ value: 'false', label: c('no') },
				]
			: field.bounds?.includes('|')
				? field.bounds.split('|').map((v) => ({ value: v, label: v }))
				: []),
	]);
</script>

<FieldControl id={'credential-' + field.env_var} {label} {required} {error} help={error ? '' : help}>
	{#snippet children(f)}
		{#if stored}
			<div class="stored">
				<span>{c('stored')}</span>
				<button
					id={f.id}
					type="button"
					class="btn-secondary"
					aria-describedby={f.describedBy}
					{disabled}
					onclick={() => {
						revealed = false;
						onchange('');
					}}
				>
					{c('replace')}
				</button>
			</div>
		{:else if field.type === 'boolean' || field.bounds?.includes('|')}
			<SearchableSelect
				id={f.id}
				value={value || ''}
				options={choices}
				{onchange}
				{disabled}
				{required}
				ariaInvalid={f.invalid}
				ariaDescribedby={f.describedBy}
			/>
		{:else}
			<div class="secret-entry" class:has-secret={secret}>
				{#if field.env_var === 'GCE_SERVICE_ACCOUNT'}
					<textarea
						id={f.id}
						{value}
						oninput={(e) => onchange(e.currentTarget.value)}
						rows="4"
						class:concealed={secret && !revealed}
						autocomplete="off"
						spellcheck="false"
						{disabled}
						aria-required={required || undefined}
						aria-invalid={f.invalid || undefined}
						aria-describedby={f.describedBy}></textarea>
				{:else}
					<input
						id={f.id}
						type={secret && !revealed ? 'password' : 'text'}
						inputmode={numeric ? 'numeric' : undefined}
						{value}
						oninput={(e) => onchange(e.currentTarget.value)}
						placeholder={secret ? '' : (field.default_value ?? '')}
						autocomplete={secret ? 'new-password' : 'off'}
						spellcheck="false"
						{disabled}
						aria-required={required || undefined}
						aria-invalid={f.invalid || undefined}
						aria-describedby={f.describedBy}
					/>
				{/if}
				{#if secret}
					<button
						type="button"
						class="reveal"
						onclick={() => (revealed = !revealed)}
						{disabled}
						aria-label={c(revealed ? 'hide' : 'show') + ' ' + label}
						aria-pressed={revealed}
					>
						{#if revealed}<EyeOff size={18} aria-hidden="true" />{:else}<Eye size={18} aria-hidden="true" />{/if}
					</button>
				{/if}
			</div>
		{/if}
	{/snippet}
</FieldControl>

<style>
	.stored,
	.secret-entry {
		display: flex;
		align-items: start;
		gap: 8px;
		min-width: 0;
	}
	.stored {
		align-items: center;
		flex-wrap: wrap;
		border: 1px solid var(--border);
		padding: 8px 12px;
		border-radius: 6px;
	}
	.stored span {
		flex: 1;
		font-size: 14px;
		color: var(--text-muted);
	}
	.secret-entry input,
	.secret-entry textarea {
		flex: 1;
		min-width: 0;
	}
	.concealed {
		-webkit-text-security: disc;
	}
	.secret-entry.has-secret {
		position: relative;
		display: block;
	}
	.secret-entry.has-secret :global(input),
	.secret-entry.has-secret :global(textarea) {
		padding-right: 48px;
	}
	.reveal {
		position: absolute;
		right: 2px;
		top: 2px;
		display: flex;
		align-items: center;
		justify-content: center;
		width: 36px;
		height: 36px;
		border: 0;
		border-radius: 4px;
		background: transparent;
		color: var(--text-muted);
		cursor: pointer;
	}
	.reveal:hover:not(:disabled) {
		color: var(--text);
		background: var(--surface-muted);
	}
	.reveal:disabled {
		opacity: 0.6;
		cursor: default;
	}
	.reveal:focus-visible {
		outline: 2px solid var(--connection);
		outline-offset: -2px;
	}
	@media (max-width: 767px) {
		.reveal {
			width: 40px;
			height: 40px;
		}
	}
</style>

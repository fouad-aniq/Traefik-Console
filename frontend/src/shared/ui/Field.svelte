<script lang="ts">
	import { CircleAlert } from '@lucide/svelte';
	import { nanoid } from '../nanoid';
	import type { Snippet } from 'svelte';
	import type { FieldSlot } from './fieldTypes';

	let {
		id: providedId = '',
		label,
		help = '',
		error = '',
		required = false,
		children
	}: {
		id?: string;
		label: string;
		help?: string;
		error?: string;
		required?: boolean;
		children: Snippet<[FieldSlot]>;
	} = $props();

	const generatedId = nanoid();
	const uid = $derived(providedId || generatedId);
	const describedBy = $derived([help ? `${uid}-help` : '', error ? `${uid}-error` : ''].filter(Boolean).join(' '));
</script>

<div class="field">
	<label for={uid}>{label}{#if required}<span class="required" aria-hidden="true">*</span>{/if}</label>
	{@render children({ id: uid, describedBy: describedBy || undefined, invalid: !!error })}
	{#if help}
		<p class="help" id="{uid}-help">{help}</p>
	{/if}
	{#if error}
		<p class="error" id="{uid}-error" role="alert"><CircleAlert size={14} aria-hidden="true" /><span>{error}</span></p>
	{/if}
</div>

<style>
	.field {
		display: flex;
		flex-direction: column;
		gap: 8px;
		width: 100%;
		max-width: 560px;
	}
	label {
		font-size: 14px;
		font-weight: 500;
		line-height: 20px;
		color: var(--text);
	}
 .field :global(input:not([type='checkbox']):not([type='radio']):not([role='searchbox']):not([data-embedded-input])),
 .field :global(textarea) {
  width: 100%; min-width: 0; min-height: var(--control-h); padding: 7px 12px;
  background: var(--surface); color: var(--text); border: 1px solid var(--outline);
  border-radius: var(--radius-control); font: inherit; font-size: 16px; line-height: 24px;
 }
 .field :global(input[aria-invalid='true']:not([type='checkbox']):not([type='radio']):not([role='searchbox']):not([data-embedded-input])), .field :global(textarea[aria-invalid='true']) { border-color: var(--danger-text); box-shadow: 0 0 0 3px color-mix(in srgb, var(--danger-text) 16%, transparent); }
 .field :global(input:disabled), .field :global(textarea:disabled) { opacity: 0.6; }
 .field :global(input[readonly]:not([type='checkbox']):not([type='radio']):not([role='searchbox']):not([data-embedded-input])), .field :global(textarea[readonly]) { background: var(--surface-muted); cursor: default; }
 @media (max-width: 767px) { .field :global(input:not([type='checkbox']):not([type='radio']):not([role='searchbox']):not([data-embedded-input])), .field :global(textarea) { min-height: var(--touch-h); } }
	.required {
		color: var(--danger-text);
  margin-inline-start: 4px;
	}
	.help {
		margin: 0;
		color: var(--text-muted);
		font-size: 13px;
		line-height: 18px;
	}
	.error {
		margin: 0;
		display: flex;
		align-items: flex-start;
		gap: 6px;
		color: var(--danger-text);
		font-size: 13px;
		line-height: 18px;
		font-weight: 400;
	}
	.error :global(svg) { flex: none; margin-top: 2px; }
</style>

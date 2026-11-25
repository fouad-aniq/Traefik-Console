<script lang="ts">
	import { CircleAlert } from '@lucide/svelte';
	import { nanoid } from '../nanoid';

	/**
	 * Mutually exclusive choice rendered as bordered option rows, matching Checkbox and Field.
	 * Native radio inputs stay in the DOM (visually hidden) for keyboard and screen readers.
	 */
	let {
		legend,
		value = '',
		options,
		required = false,
		disabled = false,
		error = '',
		name: providedName = '',
		onchange
	}: {
		legend: string;
		value?: string;
		options: { value: string; label: string; description?: string }[];
		required?: boolean;
		disabled?: boolean;
		error?: string;
		name?: string;
		onchange?: (value: string) => void;
	} = $props();

	const generated = nanoid();
	const group = $derived(providedName || generated);
</script>

<fieldset class="radio-cards"  aria-describedby={error ? `${group}-error` : undefined}>
	<legend>{legend}{#if required}<span class="required" aria-hidden="true">*</span>{/if}</legend>
	<div class="grid">
		{#each options as option (option.value)}
			<label class="card" class:checked={value === option.value} class:disabled>
				<input type="radio" name={group} value={option.value} checked={value === option.value} {disabled} onchange={() => onchange?.(option.value)} />
				<span class="dot" aria-hidden="true"></span>
				<span class="text">
					<span class="label">{option.label}</span>
					{#if option.description}<span class="help">{option.description}</span>{/if}
				</span>
			</label>
		{/each}
	</div>
	{#if error}<p class="error" id="{group}-error" role="alert"><CircleAlert size={14} aria-hidden="true" /><span>{error}</span></p>{/if}
</fieldset>

<style>
	.radio-cards { margin: 0; padding: 0; border: 0; min-width: 0; display: flex; flex-direction: column; gap: 8px; width: 100%; max-width: 560px; }
	legend { padding: 0; margin-bottom: 8px; font-size: 14px; font-weight: 500; line-height: 20px; color: var(--text); }
	.required { color: var(--danger-text); margin-inline-start: 4px; }
	.grid { display: grid; grid-template-columns: 1fr 1fr; gap: 12px; }
	.card { position: relative; display: flex; align-items: flex-start; gap: 12px; padding: 12px 14px; background: var(--surface); border: 1px solid var(--outline); border-radius: var(--radius-control); cursor: pointer; transition: border-color 120ms ease, background-color 120ms ease; }
	.card:hover:not(.disabled) { border-color: var(--connection-border); }
	.card.checked { border-color: var(--connection); background: var(--connection-soft); }
	.card:has(input:focus-visible) { outline: 2px solid var(--focus-ring); outline-offset: 2px; }
	.card.disabled { opacity: 0.6; cursor: not-allowed; }
	input { position: absolute; opacity: 0; width: 1px; height: 1px; margin: 0; pointer-events: none; }
	.dot { flex: 0 0 18px; width: 18px; height: 18px; margin-top: 1px; border: 1px solid var(--outline); border-radius: 50%; background: var(--surface); box-shadow: inset 0 0 0 4px var(--surface); transition: background-color 120ms ease, border-color 120ms ease; }
	.checked .dot { background: var(--connection); border-color: var(--connection); }
	.text { display: flex; flex-direction: column; gap: 2px; min-width: 0; }
	.label { font-size: 14px; font-weight: 500; line-height: 20px; color: var(--text); }
	.help { font-size: 13px; line-height: 18px; color: var(--text-muted); }
	.error { margin: 0; display: flex; align-items: flex-start; gap: 6px; color: var(--danger-text); font-size: 13px; line-height: 18px; }
	.error :global(svg) { flex: none; margin-top: 2px; }
	@media (max-width: 767px) { .grid { grid-template-columns: 1fr; } .card { min-height: var(--touch-h); } }
</style>

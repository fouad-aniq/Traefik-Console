<script lang="ts">
	import { Check } from '@lucide/svelte';
	import { nanoid } from '../nanoid';

	/**
	 * Boolean field styled like the other form fields: a bordered control row with a custom
	 * box, label and optional help. The native input stays in the DOM (visually hidden) so
	 * keyboard, screen-reader and form behaviour are unchanged.
	 */
	let {
		id: providedId = '',
		checked = false,
		label,
		help = '',
		disabled = false,
		onchange
	}: {
		id?: string;
		checked?: boolean;
		label: string;
		help?: string;
		disabled?: boolean;
		onchange?: (checked: boolean) => void;
	} = $props();

	const generated = nanoid();
	const uid = $derived(providedId || generated);
</script>

<label class="checkbox" class:checked class:disabled for={uid}>
	<input
		id={uid}
		type="checkbox"
		{checked}
		{disabled}
		aria-describedby={help ? `${uid}-help` : undefined}
		onchange={(e) => onchange?.(e.currentTarget.checked)}
	/>
	<span class="box" aria-hidden="true">{#if checked}<Check size={12} strokeWidth={3.5} />{/if}</span>
	<span class="text">
		<span class="label">{label}</span>
		{#if help}<span class="help" id="{uid}-help">{help}</span>{/if}
	</span>
</label>

<style>
	.checkbox {
		position: relative;
		display: flex;
		align-items: flex-start;
		gap: 12px;
		width: 100%;
		max-width: 560px;
		padding: 12px 14px;
		background: var(--surface);
		border: 1px solid var(--outline);
		border-radius: var(--radius-control);
		cursor: pointer;
		transition: border-color 120ms ease, background-color 120ms ease;
	}
	.checkbox:hover:not(.disabled) { border-color: var(--connection-border); }
	.checkbox.checked { border-color: var(--connection); background: var(--connection-soft); }
	.checkbox:has(input:focus-visible) { outline: 2px solid var(--focus-ring); outline-offset: 2px; }
	.checkbox.disabled { opacity: 0.6; cursor: not-allowed; }
	input {
		position: absolute;
		opacity: 0;
		width: 1px;
		height: 1px;
		margin: 0;
		pointer-events: none;
	}
	.box {
		display: inline-flex;
		align-items: center;
		justify-content: center;
		flex: 0 0 18px;
		width: 18px;
		height: 18px;
		margin-top: 1px;
		border: 1px solid var(--outline);
		border-radius: 4px;
		background: var(--surface);
		color: #fff;
		transition: background-color 120ms ease, border-color 120ms ease;
	}
	.checked .box { background: var(--connection); border-color: var(--connection); }
	.text { display: flex; flex-direction: column; gap: 2px; min-width: 0; }
	.label { font-size: 14px; font-weight: 500; line-height: 20px; color: var(--text); }
	.help { font-size: 13px; line-height: 18px; color: var(--text-muted); }
	@media (max-width: 767px) { .checkbox { min-height: var(--touch-h); } }
</style>

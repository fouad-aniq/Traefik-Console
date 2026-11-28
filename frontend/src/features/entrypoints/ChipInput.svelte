<script lang="ts">
	/**
	 * A short list of text values (IP ranges, header names, host names) edited as chips.
	 * Enter or comma adds the typed value; Backspace on an empty input removes the last one.
	 * A value that fails `validate` stays in the input and reports `invalidMessage`.
	 */
	import { X } from '@lucide/svelte';

	let {
		id,
		values,
		placeholder = '',
		addPlaceholder = '',
		removeLabel,
		invalidMessage = '',
		max = 64,
		disabled = false,
		invalid = false,
		describedBy = undefined,
		validate = () => true,
		onchange,
		onerror,
	}: {
		id: string;
		values: string[];
		placeholder?: string;
		addPlaceholder?: string;
		removeLabel: string;
		invalidMessage?: string;
		max?: number;
		disabled?: boolean;
		invalid?: boolean;
		describedBy?: string;
		validate?: (value: string) => boolean;
		onchange: (values: string[]) => void;
		onerror?: (message: string) => void;
	} = $props();

	let draft = $state('');

	/** Adds whatever is typed. Returns false when it is not acceptable. */
	export function commit(raw = draft): boolean {
		const value = raw.trim();
		if (!value) return true;
		if (!validate(value) || values.length >= max) {
			onerror?.(invalidMessage);
			return false;
		}
		if (!values.some((v) => v.toLowerCase() === value.toLowerCase())) onchange([...values, value]);
		draft = '';
		onerror?.('');
		return true;
	}
	function remove(index: number) {
		onchange(values.filter((_, i) => i !== index));
		onerror?.('');
	}
</script>

<div class="chip-box" class:invalid>
	{#each values as item, index (item)}
		<span class="chip">
			<span>{item}</span><button
				type="button"
				{disabled}
				aria-label={`${removeLabel} ${item}`}
				onclick={() => remove(index)}
			>
				<X size={12} aria-hidden="true" />
			</button>
		</span>
	{/each}
	<input
		{id}
		class="chip-entry"
		data-embedded-input
		type="text"
		bind:value={draft}
		{disabled}
		aria-invalid={invalid || undefined}
		aria-describedby={describedBy}
		placeholder={values.length ? addPlaceholder : placeholder}
		autocomplete="off"
		spellcheck="false"
		oninput={() => onerror?.('')}
		onkeydown={(event) => {
			if (event.key === 'Enter' || event.key === ',') {
				event.preventDefault();
				commit(event.currentTarget.value);
			} else if (event.key === 'Backspace' && !event.currentTarget.value && values.length) remove(values.length - 1);
		}}
		onblur={(event) => {
			if (event.currentTarget.value.trim()) commit(event.currentTarget.value);
		}}
	/>
</div>

<style>
	.chip-box {
		display: flex;
		flex-wrap: wrap;
		align-items: center;
		gap: 6px;
		min-height: var(--control-h);
		padding: 4px 8px;
		background: var(--surface);
		border: 1px solid var(--outline);
		border-radius: var(--radius-control);
	}
	.chip-box:focus-within {
		outline: 2px solid var(--focus-ring);
		outline-offset: -1px;
	}
	.chip-box.invalid {
		border-color: var(--danger-text);
	}
	.chip {
		display: inline-flex;
		align-items: center;
		gap: 4px;
		max-width: 100%;
		padding: 2px 8px;
		background: var(--surface-muted);
		border: 1px solid var(--border);
		border-radius: 4px;
		color: var(--text);
		font-size: 13px;
		line-height: 20px;
	}
	.chip span {
		overflow: hidden;
		text-overflow: ellipsis;
		white-space: nowrap;
	}
	.chip button {
		display: grid;
		place-items: center;
		flex: none;
		min-width: 20px;
		min-height: 20px;
		padding: 0;
		border: 0;
		border-radius: 2px;
		background: transparent;
		color: var(--text-muted);
		cursor: pointer;
	}
	.chip button:hover:not(:disabled) {
		color: var(--danger-text);
	}
	.chip-box input.chip-entry {
		flex: 1 1 120px;
		width: auto;
		min-width: 110px;
		min-height: 28px;
		padding: 2px 4px;
		border: 0;
		border-radius: 0;
		background: transparent;
		outline: 0;
		color: var(--text);
		font: inherit;
		font-size: 16px;
		line-height: 24px;
	}
	.chip-box input.chip-entry::placeholder {
		color: var(--text-muted);
	}
</style>

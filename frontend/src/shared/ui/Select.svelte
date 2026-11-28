<script lang="ts">
	import type { Snippet } from 'svelte';
	import type { HTMLSelectAttributes } from 'svelte/elements';
	import { ChevronDown } from '@lucide/svelte';

	/** Native keyboard/menu behavior with one consistent control and chevron. */
	let {
		children,
		...attributes
	}: Omit<HTMLSelectAttributes, 'children' | 'multiple' | 'size'> & {
		children: Snippet;
	} = $props();
</script>

<span class="select-control">
	<select {...attributes}>{@render children()}</select>
	<ChevronDown size={18} strokeWidth={1.75} aria-hidden="true" />
</span>

<style>
	.select-control {
		display: grid;
		position: relative;
		min-width: 0;
		width: 100%;
	}
	select {
		appearance: none;
		width: 100%;
		min-width: 0;
		min-height: var(--control-h);
		padding: 7px 40px 7px 12px;
		border: 1px solid var(--outline);
		border-radius: var(--radius-control);
		background: var(--surface);
		color: var(--text);
		font: inherit;
		font-size: 16px;
		line-height: 24px;
		cursor: pointer;
	}
	.select-control :global(svg) {
		position: absolute;
		right: 12px;
		top: 50%;
		transform: translateY(-50%);
		color: var(--text-muted);
		pointer-events: none;
	}
	select:hover:not(:disabled) {
		border-color: var(--text-muted);
	}
	select:disabled {
		opacity: 0.6;
		cursor: default;
	}
	.select-control:has(select:disabled) :global(svg) {
		opacity: 0.6;
	}
	@media (max-width: 767px) {
		select {
			min-height: var(--touch-h);
		}
	}
	@media (forced-colors: active) {
		select {
			appearance: auto;
			padding-right: 12px;
		}
		.select-control :global(svg) {
			display: none;
		}
	}
</style>

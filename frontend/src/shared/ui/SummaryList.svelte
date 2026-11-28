<script lang="ts">
	/**
	 * Label / value summary used by view and review dialogs.
	 *
	 * Each `<div><dt/><dd/></div>` is one row of a shared two-column grid
	 * (subgrid), so every label lines up with its value whatever the value
	 * holds — text, a badge, a button, a timestamp. Rows sit in one bordered
	 * panel separated by hairlines.
	 */
	import type { Snippet } from 'svelte';
	let { children }: { children: Snippet } = $props();
</script>

<dl class="review-summary">{@render children()}</dl>

<style>
	.review-summary {
		display: grid;
		grid-template-columns: fit-content(220px) minmax(0, 1fr);
		column-gap: 24px;
		margin: 0;
		border: 1px solid var(--border);
		border-radius: var(--radius-card);
		background: var(--surface);
		overflow: hidden;
	}
	.review-summary :global(> div) {
		display: grid;
		grid-column: 1/-1;
		grid-template-columns: subgrid;
		align-items: center;
		min-height: 44px;
		padding: 8px 16px;
		border-top: 1px solid var(--border);
	}
	.review-summary :global(> div:first-child) {
		border-top: 0;
	}
	.review-summary :global(dt),
	.review-summary :global(dd) {
		margin: 0;
		min-width: 0;
		font-size: 14px;
		line-height: 20px;
		overflow-wrap: anywhere;
	}
	.review-summary :global(dt) {
		min-width: 120px;
		color: var(--text-muted);
		font-size: 13px;
		font-weight: 500;
	}
	.review-summary :global(dd) {
		color: var(--text);
	}
	.review-summary :global(dd code) {
		font-size: 13px;
	}
	.review-summary :global(dd .timestamp) {
		padding: 0;
		margin: 0;
		max-width: 100%;
	}
	@media (max-width: 480px) {
		.review-summary {
			grid-template-columns: minmax(0, 1fr);
		}
		.review-summary :global(> div) {
			grid-template-columns: minmax(0, 1fr);
			row-gap: 2px;
			align-items: start;
		}
		.review-summary :global(dt) {
			min-width: 0;
		}
	}
</style>

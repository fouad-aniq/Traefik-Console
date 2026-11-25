<script lang="ts">
	import type { Snippet } from 'svelte';

	/** Form/review action footer: secondary (left) and primary (right) actions. */
	let {
		secondary,
		primary
	}: {
		secondary?: Snippet;
		primary?: Snippet;
	} = $props();
</script>

<footer class="action-footer">
	<div class="left">{#if secondary}{@render secondary()}{/if}</div>
	<div class="right">{#if primary}{@render primary()}{/if}</div>
</footer>

<style>
	.action-footer {
		display: flex;
		align-items: center;
		justify-content: space-between;
		gap: 12px;
  flex-wrap: wrap;
		padding: 16px 24px;
		border: 1px solid var(--border);
  border-radius: var(--radius-card);
		position: sticky;
		bottom: 0;
		background: var(--surface);
  max-width: 880px;
  width: 100%;
  isolation: isolate;
		z-index: 5;
	}
	.left,
	.right {
		display: flex;
		gap: 12px;
		align-items: center;
	}
	@media (max-width: 767px) {
		.action-footer {
			display: grid;
   grid-template-columns: minmax(0, 1fr);
   padding: 16px;
   padding-bottom: max(16px, env(safe-area-inset-bottom));
		}
		.action-footer :global(button) {
			min-height: var(--touch-h);
   width: 100%;
		}
	}
</style>

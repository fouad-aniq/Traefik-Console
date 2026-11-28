<script lang="ts">
	/**
	 * "Used by" table cell, the same on every page: "N resources" opens the related-items
	 * dialog (which says what they are); nothing using the item shows a muted empty state.
	 */
	import { Link2 } from '@lucide/svelte';
	import { t } from '../localization/i18n';
	let { count, onclick }: { count: number; onclick: () => void } = $props();
	const label = $derived(t(count === 1 ? 'related.resourceCountOne' : 'related.resourceCount', { count }));
</script>

{#if count > 0}<button class="reference-count" {onclick} aria-haspopup="dialog">
		<Link2 size={15} aria-hidden="true" />{label}
	</button>{:else}<span class="not-used">{t('related.notUsed')}</span>{/if}

<style>
	.reference-count {
		display: inline-flex;
		align-items: center;
		gap: 8px;
		padding: 7px 10px;
		border: 1px solid var(--border);
		border-radius: var(--radius-control);
		background: var(--surface-muted);
		color: var(--connection);
		font: inherit;
		font-size: 14px;
		cursor: pointer;
		white-space: nowrap;
	}
	.reference-count:hover {
		border-color: var(--connection);
	}
	.not-used {
		color: var(--text-muted);
		font-size: 14px;
		font-style: italic;
		white-space: nowrap;
	}
	@media (max-width: 767px) {
		.reference-count {
			min-height: 44px;
		}
	}
</style>

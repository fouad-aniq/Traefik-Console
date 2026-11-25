<script lang="ts">
	import { t } from '../localization/i18n';

	/**
	 * Removable active-filter summaries (accepted FE-D04). Chips summarize
	 * state; the filter controls remain the source of truth. Removal is
	 * keyboard-operable and announced via the button's accessible name.
	 */
	export interface ActiveFilter {
		id: string;
		label: string;
	}

	let {
		filters,
		onremove,
		onclear
	}: {
		filters: ActiveFilter[];
		onremove: (id: string) => void;
		onclear: () => void;
	} = $props();
</script>

{#if filters.length > 0}
	<div class="summaries" role="group" aria-label={t('demo.clearFilters')}>
		{#each filters as filter (filter.id)}
			<span class="chip">
				<span class="chip-label">{filter.label}</span>
				<button
					type="button"
					class="chip-remove"
					aria-label={t('demo.removeFilter', { value: filter.label })}
					onclick={() => onremove(filter.id)}
				>
					✕
				</button>
			</span>
		{/each}
		<button type="button" class="clear clear-all" onclick={onclear}>{t('demo.clearFilters')}</button>
	</div>
{/if}

<style>
	.summaries {
		display: flex;
		flex-wrap: wrap;
		align-items: center;
		gap: 8px;
		margin: 8px 0 0;
	}
	.chip {
		display: inline-flex;
		align-items: center;
		gap: 4px;
		background: var(--accent-soft);
		color: var(--accent);
		border: 1px solid color-mix(in srgb, var(--accent) 30%, transparent);
		border-radius: 999px;
		padding: 2px 4px 2px 10px;
		font-size: 14px;
		line-height: 20px;
	}
	.chip button {
		background: transparent;
		border: none;
		color: inherit;
		cursor: pointer;
		font-size: 12px;
		min-width: 24px;
		min-height: 24px;
		border-radius: 999px;
	}
	.chip button:hover {
		background: color-mix(in srgb, var(--accent) 20%, transparent);
	}
	.clear {
		background: transparent;
		border: none;
		color: var(--text-muted);
		cursor: pointer;
		font-size: 14px;
		text-decoration: underline;
		min-height: var(--touch-h);
		padding: 0 8px;
	}
</style>

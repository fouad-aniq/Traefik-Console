<script lang="ts">
 import type { Snippet } from 'svelte';
 import SelectMenu from './SelectMenu.svelte';
	import { t } from '../localization/i18n';

	/** Bounded pagination: 25/50/100 page sizes, truthful range, boundaries disabled. */
	let {
		page,
		pageSize,
		total,
		onpage,
		onpagesize,
  pageSizeControl, hasNext, resultCount
	}: {
		page: number;
		pageSize: number;
		total: number;
		onpage: (page: number) => void;
		onpagesize: (size: number) => void;
  hasNext?: boolean;
  resultCount?: number;
  pageSizeControl?: Snippet<[id: string, value: number, setValue: (size: number) => void]>;
	} = $props();

	const pageSizeId = $props.id();
	const sizes = [25, 50, 100];
	const pageCount = $derived(Math.max(1, Math.ceil(total / pageSize)));
	const from = $derived(total === 0 ? 0 : (page - 1) * pageSize + 1);
	const to = $derived(Math.min(total, page * pageSize));
</script>

<div class="pagination">
	<div class="size">
		<label for={pageSizeId}>{t('table.itemsPerPage')}</label>
  {#if pageSizeControl}
   {@render pageSizeControl(pageSizeId, pageSize, onpagesize)}
  {:else}
  <SelectMenu id={pageSizeId} value={String(pageSize)} options={sizes.map(size=>({value:String(size),label:String(size)}))} onchange={value=>onpagesize(Number(value))} />
  {/if}
	</div>
	<span class="range numeric">{hasNext === undefined ? t('table.showing', { from, to, total }) : t('table.pageResults', { page, count: resultCount ?? pageSize })}</span>
	<div class="buttons">
		<button
			type="button"
			class="btn-secondary"
			aria-label={t('table.previous')}
			disabled={page <= 1}
			onclick={() => onpage(page - 1)}
		>
			‹
		</button>
		<span class="page-indicator numeric">{hasNext === undefined ? `${page} / ${pageCount}` : t('table.pageNumber', {page})}</span>
		<button
			type="button"
			class="btn-secondary"
			aria-label={t('table.next')}
			disabled={hasNext === undefined ? page >= pageCount : !hasNext}
			onclick={() => onpage(page + 1)}
		>
			›
		</button>
	</div>
</div>

<style>
 @media (max-width: 767px) { .range { width: 100%; order: 3; } }
	.pagination {
		display: flex;
		flex-wrap: wrap;
		align-items: center;
		gap: 12px 20px;
		padding: 16px;
  border-top: 1px solid var(--border);
	}
	.size {
		display: inline-flex;
		align-items: center;
		gap: 8px;
		font-size: 14px;
		color: var(--text-muted);
	}
 .size :global(.select-menu), .size :global(.select-menu) { width: 88px; }

	.range {
		font-size: 14px;
		color: var(--text-muted);
	}
	.buttons {
		display: inline-flex;
		align-items: center;
		gap: 8px;
		margin-left: auto;
	}
	.buttons button {
		min-width: var(--touch-h);
		min-height: var(--touch-h);
		font-size: 16px;
	}
	.page-indicator {
		font-size: 14px;
		color: var(--text-muted);
		min-width: 48px;
		text-align: center;
	}
</style>

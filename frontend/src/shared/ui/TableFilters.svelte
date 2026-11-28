<script lang="ts">
	import { tick } from 'svelte';
	import { Funnel, ChevronsUpDown, ChevronRight, ChevronLeft, Check } from '@lucide/svelte';
	import MenuPopover from './MenuPopover.svelte';
	import { t } from '../localization/i18n';
	import type { DataTableFilter } from './tableTypes';
	let {
		filters,
		values,
		onchange,
	}: { filters: DataTableFilter[]; values: Record<string, string>; onchange: (key: string, value: string) => void } =
		$props();
	let category = $state<string | null>(null);
	let content: HTMLDivElement;
	const selected = $derived(filters.find((f) => f.id === category));
	const options = $derived(selected?.options ?? []);
	const count = $derived(filters.filter((f) => !!values[f.id]).length);
	function backOnLeft(event: KeyboardEvent) {
		if (event.key === 'ArrowLeft') {
			event.preventDefault();
			void show(null);
		}
	}
	async function show(next: typeof category) {
		category = next;
		await tick();
		content?.querySelector<HTMLElement>('[role^="menuitem"]')?.focus();
	}
</script>

<MenuPopover
	label={`${t('table.filters')}${count ? ` ${count}` : ''}`}
	onopenchange={(open) => {
		if (!open) category = null;
	}}
>
	{#snippet trigger()}
		<Funnel size={16} aria-hidden="true" />{t('table.filters')}
		{#if count}<span class="filter-count" aria-hidden="true">{count}</span>{/if}
		<ChevronsUpDown size={14} aria-hidden="true" />
	{/snippet}
	<div bind:this={content}>
		{#if category === null}
			{#each filters as filter (filter.id)}
				<button
					type="button"
					role="menuitem"
					onclick={() => show(filter.id)}
					onkeydown={(event: KeyboardEvent) => {
						if (event.key === 'ArrowRight') {
							event.preventDefault();
							void show(filter.id);
						}
					}}
				>
					<span>{filter.label}</span><ChevronRight size={16} aria-hidden="true" />
				</button>
			{/each}
		{:else}
			<button type="button" role="menuitem" class="back" onkeydown={backOnLeft} onclick={() => show(null)}>
				<ChevronLeft size={16} aria-hidden="true" />{selected?.label}
			</button>
			<div role="group" aria-label={selected?.label}>
				{#each options as option (option.value)}
					<button
						type="button"
						role="menuitemradio"
						onkeydown={backOnLeft}
						aria-checked={(category ? (values[category] ?? '') : '') === option.value}
						onclick={() => category && onchange(category, option.value)}
					>
						<span class="check">
							{#if (category ? (values[category] ?? '') : '') === option.value}<Check
									size={16}
									aria-hidden="true"
								/>{/if}
						</span>{option.value === '' ? t('demo.filterAll') : option.label}
					</button>
				{/each}
			</div>
		{/if}
	</div>
</MenuPopover>

<style>
	span:first-child {
		flex: 1;
	}
	.filter-count {
		background: var(--accent-soft);
		color: var(--accent);
		border-radius: 4px;
		padding: 0 6px;
	}
	button[role='menuitem'] {
		justify-content: space-between;
	}
	button.back {
		justify-content: flex-start;
		border-bottom: 1px solid var(--border);
		border-radius: 0;
		margin-bottom: 6px;
	}
	.check {
		display: inline-flex;
		flex: 0 0 16px !important;
		width: 16px;
	}
</style>

<script lang="ts">
	/**
	 * Right-aligned table row actions.
	 *
	 * Up to two actions render as buttons. With more, the first non-danger
	 * action stays visible and the rest move into a compact "More actions"
	 * menu, so the actions column keeps one fixed, narrow width. Destructive
	 * actions are listed last in the menu, after a separator.
	 */
	import { Ellipsis } from '@lucide/svelte';
	import Button from './Button.svelte';
	import MenuPopover from './MenuPopover.svelte';
	import { t } from '../localization/i18n';
	import type { RowAction } from './tableTypes';

	let { actions, label = '' }: {
		/** falsy entries are skipped so callers can write `cond && {...}` */
		actions: (RowAction | false | null | undefined)[];
		/** names the row for the menu's accessible label, e.g. the resource name */
		label?: string;
	} = $props();

	const list = $derived(actions.filter((a): a is RowAction => !!a));
	const collapsed = $derived(list.length > 2);
	const lead = $derived(collapsed ? (list.find((a) => a.variant !== 'danger') ?? list[0]) : undefined);
	const inline = $derived(collapsed ? (lead ? [lead] : []) : list);
	const menu = $derived(collapsed ? list.filter((a) => a !== lead) : []);
	const menuSafe = $derived(menu.filter((a) => a.variant !== 'danger'));
	const menuDanger = $derived(menu.filter((a) => a.variant === 'danger'));
	const menuLabel = $derived(t('table.moreActions') + (label ? ` ${label}` : ''));
</script>

{#snippet item(action: RowAction, close: () => void)}
	<button
		type="button"
		role="menuitem"
		class:danger={action.variant === 'danger'}
		disabled={action.disabled}
		onclick={() => { close(); action.onclick(); }}
	>
		{action.label}{#if action.context}<span class="sr-only"> {action.context}</span>{/if}
	</button>
{/snippet}

<div class="row-actions">
	{#each inline as action (action.label)}
		<Button size="sm" variant={action.variant ?? 'secondary'} disabled={action.disabled} onclick={action.onclick}>
			{action.label}{#if action.context}<span class="sr-only"> {action.context}</span>{/if}
		</Button>
	{/each}
	{#if menu.length}
		<MenuPopover label={menuLabel} align="end" compact>
			{#snippet trigger()}<Ellipsis size={16} aria-hidden="true" />{/snippet}
			{#snippet children(close)}
				{#each menuSafe as action (action.label)}{@render item(action, close)}{/each}
				{#if menuSafe.length && menuDanger.length}<div class="separator" role="separator"></div>{/if}
				{#each menuDanger as action (action.label)}{@render item(action, close)}{/each}
			{/snippet}
		</MenuPopover>
	{/if}
</div>

<style>
	.row-actions { display: flex; align-items: center; justify-content: flex-end; gap: 8px; width: 100%; }
	.separator { height: 1px; margin: 4px 2px; background: var(--border); }
	.row-actions :global(button[role='menuitem'].danger) { color: var(--danger-text); }
	.row-actions :global(button[role='menuitem'].danger:hover),
	.row-actions :global(button[role='menuitem'].danger:focus-visible) { background: var(--danger-bg); color: var(--danger-text); }
</style>

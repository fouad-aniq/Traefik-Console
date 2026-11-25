<script lang="ts">
	/**
	 * Accessible tabs: roving tabindex, arrow-key navigation, active underline
	 * (no decorative pills). Tab identity is caller-owned.
	 */
	let {
		tabs,
		selected,
		onselect,
		label
	}: {
		tabs: { id: string; label: string; errorCount?: number; errorLabel?: string }[];
		selected: string;
		onselect: (id: string) => void;
		label: string;
	} = $props();

	function selectTabAndFocus(id: string) {
		onselect(id);
		queueMicrotask(() => {
			const target = document.getElementById(`tab-${id}`);
			if (target?.getAttribute('aria-selected') === 'true') target.focus();
		});
	}

	function onKeydown(e: KeyboardEvent) {
		const index = tabs.findIndex((tab) => tab.id === selected);
		if (index === -1) return;
		if (e.key === 'ArrowRight') {
			e.preventDefault();
			const next = tabs[(index + 1) % tabs.length].id;
			selectTabAndFocus(next);
		} else if (e.key === 'ArrowLeft') {
			e.preventDefault();
			const prev = tabs[(index - 1 + tabs.length) % tabs.length].id;
			selectTabAndFocus(prev);
		} else if (e.key === 'Home') {
			e.preventDefault();
			selectTabAndFocus(tabs[0].id);
		} else if (e.key === 'End') {
			e.preventDefault();
			selectTabAndFocus(tabs[tabs.length - 1].id);
		}
	}
</script>

<div class="tabs-bar" role="tablist" aria-label={label}>
	{#each tabs as tab (tab.id)}
		<button
			type="button"
			role="tab"
			id={`tab-${tab.id}`}
			aria-selected={tab.id === selected}
			aria-controls={`panel-${tab.id}`}
			tabindex={tab.id === selected ? 0 : -1}
			class:active={tab.id === selected}
            data-invalid={tab.errorCount ? true : undefined}
            aria-describedby={tab.errorCount ? `tab-${tab.id}-errors` : undefined}
			onclick={() => onselect(tab.id)}
			onkeydown={onKeydown}
		>
			{tab.label}{#if tab.errorCount}<span class="tab-error-count">{tab.errorCount}</span>{/if}
		</button>
        {#if tab.errorCount}<span class="tab-error-description" id={`tab-${tab.id}-errors`}>{tab.errorLabel}</span>{/if}
	{/each}
</div>

<style>
	.tabs-bar {
		display: flex;
		gap: 4px;
		border-bottom: 1px solid var(--border);
		overflow-x: auto;
	}
	button {
		background: transparent;
		border: none;
		border-bottom: 2px solid transparent;
		color: var(--text-muted);
		padding: 10px 14px;
		font-size: 14px;
		line-height: 20px;
		cursor: pointer;
		white-space: nowrap;
		min-height: var(--touch-h);
	}
 .tab-error-description {position:absolute;width:1px;height:1px;padding:0;margin:-1px;overflow:hidden;clip:rect(0,0,0,0);white-space:nowrap;border:0;}
 .tab-error-count {display:inline-flex;align-items:center;justify-content:center;min-width:18px;height:18px;padding:0 4px;margin-left:6px;border-radius:10px;font-size:11px;font-weight:600;line-height:16px;background:var(--danger-text);color:var(--surface);}
 button[data-invalid='true'] {color:var(--danger-text);}
 button.active[data-invalid='true'] {border-bottom-color:var(--danger-text);}
	button:hover {
		color: var(--text);
	}
	button.active {
		color: var(--accent);
		border-bottom-color: var(--accent);
		font-weight: 500;
	}
</style>

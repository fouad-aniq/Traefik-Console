<script lang="ts">
	import { onMount, tick, type Snippet } from 'svelte';
	let {
		label,
		align = 'start',
		compact = false,
		primary = false,
		trigger,
		children,
		onopenchange,
	}: {
		label: string;
		align?: 'start' | 'end';
		/** small trigger and narrow panel, for table rows */ compact?: boolean;
		/** accent trigger, for a page's main create action */ primary?: boolean;
		trigger: Snippet;
		children: Snippet<[() => void]>;
		onopenchange?: (open: boolean) => void;
	} = $props();
	const id = $props.id();
	let anchor: HTMLButtonElement;
	let panel: HTMLDivElement;
	let open = $state(false);
	function position() {
		if (!anchor || !panel || !open) return;
		const rect = anchor.getBoundingClientRect();
		const width = panel.offsetWidth,
			height = panel.offsetHeight;
		panel.style.left = `${Math.max(8, Math.min(align === 'end' ? rect.right - width : rect.left, innerWidth - width - 8))}px`;
		panel.style.top = `${Math.max(8, Math.min(rect.bottom + 8, innerHeight - height - 8))}px`;
	}
	function close() {
		if (open) panel.hidePopover?.();
		anchor.focus();
	}
	function focusFirst() {
		panel.querySelector<HTMLElement>('[role^="menuitem"]:not(:disabled)')?.focus();
	}
	async function toggled() {
		open = panel.matches(':popover-open');
		onopenchange?.(open);
		if (open) {
			await tick();
			position();
			focusFirst();
		}
	}
	function keyboard(event: KeyboardEvent) {
		if (!['ArrowDown', 'ArrowUp', 'Home', 'End', 'Tab'].includes(event.key)) return;
		if (event.key === 'Tab') {
			panel.hidePopover();
			return;
		}
		event.preventDefault();
		const items = [...panel.querySelectorAll<HTMLButtonElement>('[role^="menuitem"]:not(:disabled)')];
		const index = items.indexOf(document.activeElement as HTMLButtonElement);
		const next =
			event.key === 'Home'
				? 0
				: event.key === 'End'
					? items.length - 1
					: (index + (event.key === 'ArrowUp' ? -1 : 1) + items.length) % items.length;
		items[next]?.focus();
	}
	onMount(() => {
		const observer = new ResizeObserver(position);
		observer.observe(panel);
		const resize = () => {
			if (open) panel.hidePopover();
		};
		window.addEventListener('resize', resize);
		return () => {
			observer.disconnect();
			window.removeEventListener('resize', resize);
		};
	});
</script>

<button
	bind:this={anchor}
	type="button"
	class="menu-trigger"
	class:btn-secondary={!primary}
	class:btn-primary={primary}
	class:compact
	popovertarget={id}
	aria-label={label}
	aria-haspopup="menu"
	aria-expanded={open}
	onkeydown={(event: KeyboardEvent) => {
		if (event.key === 'ArrowDown' || event.key === 'ArrowUp') {
			event.preventDefault();
			panel.showPopover();
		}
	}}
>
	{@render trigger()}
</button>
<div
	bind:this={panel}
	{id}
	class="menu-panel"
	class:compact
	popover="auto"
	role="menu"
	tabindex="-1"
	aria-label={label}
	ontoggle={toggled}
	onkeydown={keyboard}
>
	{@render children(close)}
</div>

<style>
	.menu-trigger:not(.btn-primary) {
		display: inline-flex;
		align-items: center;
		justify-content: center;
		gap: 10px;
		min-height: 44px;
		font-size: 14px;
	}
	.menu-trigger:not(.btn-primary):hover,
	.menu-trigger:not(.btn-primary)[aria-expanded='true'] {
		background: var(--surface-muted);
		border-color: var(--outline);
	}
	.menu-trigger.compact {
		min-height: 32px;
		min-width: 32px;
		padding: 4px 8px;
	}
	.menu-panel.compact {
		width: auto;
		min-width: 168px;
		max-width: min(280px, calc(100vw - 16px));
	}
	.menu-trigger:focus-visible {
		outline-color: var(--outline);
		box-shadow: 0 0 0 4px var(--surface-muted);
	}
	.menu-panel {
		position: fixed;
		margin: 0;
		padding: 6px;
		width: min(280px, calc(100vw - 16px));
		max-height: min(360px, calc(100dvh - 16px));
		overflow-y: auto;
		border: 1px solid var(--border);
		border-radius: 6px;
		background: var(--surface);
		color: var(--text);
		box-shadow: 0 12px 32px rgb(0 0 0 / 0.22);
	}
	.menu-panel :global(button[role^='menuitem']) {
		display: flex;
		align-items: center;
		gap: 10px;
		min-height: 36px;
		width: 100%;
		padding: 8px 10px;
		border: 0;
		border-radius: 4px;
		background: transparent;
		color: var(--text-muted);
		text-align: left;
		font-size: 14px;
		line-height: 20px;
		cursor: pointer;
	}
	.menu-panel :global(button[role^='menuitem']:hover),
	.menu-panel :global(button[role^='menuitem']:focus-visible) {
		background: var(--surface-muted);
		color: var(--text);
		outline: 2px solid transparent;
	}
	.menu-panel :global(button[aria-checked='true']) {
		color: var(--text);
	}
	.menu-panel :global(button:disabled) {
		opacity: 0.5;
		cursor: default;
	}
	@media (max-width: 767px) {
		.menu-panel :global(button[role^='menuitem']) {
			min-height: 44px;
		}
		.menu-trigger.compact {
			min-height: 44px;
			min-width: 44px;
		}
	}
</style>

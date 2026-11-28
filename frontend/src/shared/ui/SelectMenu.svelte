<script lang="ts">
	import { onMount, tick } from 'svelte';
	import { Check, ChevronDown } from '@lucide/svelte';

	/** Select-only combobox. Pending value changes only on explicit selection. */
	let {
		id,
		value,
		options,
		onchange,
		disabled = false,
		required = false,
	}: {
		id: string;
		value: string;
		options: { value: string; label: string }[];
		onchange: (value: string) => void;
		disabled?: boolean;
		required?: boolean;
	} = $props();
	const uid = $props.id();
	let trigger: HTMLButtonElement;
	let panel: HTMLDivElement;
	let open = $state(false);
	let active = $state(0);
	let buffer = '';
	let lastTyped = 0;
	const selected = $derived(options.find((option) => option.value === value));

	function position() {
		if (!open || !panel || !trigger) return;
		const rect = trigger.getBoundingClientRect();
		const below = innerHeight - rect.bottom - 16,
			above = rect.top - 16;
		const useAbove = below < Math.min(panel.scrollHeight, 180) && above > below;
		panel.style.width = `${Math.min(rect.width, innerWidth - 16)}px`;
		panel.style.maxHeight = `${Math.max(44, Math.min(240, useAbove ? above : below))}px`;
		panel.style.left = `${Math.max(8, Math.min(rect.left, innerWidth - panel.offsetWidth - 8))}px`;
		panel.style.top = `${useAbove ? Math.max(8, rect.top - panel.offsetHeight - 6) : rect.bottom + 6}px`;
	}
	function revealActive() {
		const option = panel?.children[active] as HTMLElement | undefined;
		if (!option) return;
		if (option.offsetTop < panel.scrollTop) panel.scrollTop = option.offsetTop;
		else if (option.offsetTop + option.offsetHeight > panel.scrollTop + panel.clientHeight)
			panel.scrollTop = option.offsetTop + option.offsetHeight - panel.clientHeight;
	}
	async function expand() {
		if (disabled || !options.length || !panel || !trigger) return;
		active = Math.max(
			0,
			options.findIndex((option) => option.value === value),
		);
		if (typeof panel.showPopover === 'function') panel.showPopover();
		open = true;
		await tick();
		if (!open || !panel || !trigger) return;
		position();
		revealActive();
		trigger.focus();
	}
	function close(restoreFocus = true) {
		if (panel?.matches(':popover-open') && typeof panel.hidePopover === 'function') panel.hidePopover();
		open = false;
		if (restoreFocus) trigger?.focus();
	}
	function choose(index: number) {
		if (disabled || !options[index]) return;
		onchange(options[index].value);
		close();
	}
	async function keyboard(event: KeyboardEvent) {
		if (event.key === 'Tab') {
			if (open) close(false);
			return;
		}
		if (event.key === 'Escape') {
			if (open) {
				event.preventDefault();
				event.stopPropagation();
				close();
			}
			return;
		}
		if (['Enter', ' '].includes(event.key)) {
			event.preventDefault();
			event.stopPropagation();
			if (open) choose(active);
			else await expand();
			return;
		}
		if (['ArrowDown', 'ArrowUp', 'Home', 'End'].includes(event.key)) {
			event.preventDefault();
			event.stopPropagation();
			if (!open) await expand();
			else if (event.key === 'ArrowDown') active = (active + 1) % options.length;
			else if (event.key === 'ArrowUp') active = (active - 1 + options.length) % options.length;
			if (event.key === 'Home') active = 0;
			if (event.key === 'End') active = options.length - 1;
			await tick();
			revealActive();
			return;
		}
		if (event.key.length === 1 && !event.ctrlKey && !event.metaKey && !event.altKey) {
			event.preventDefault();
			if (!open) await expand();
			const now = Date.now();
			buffer = now - lastTyped > 600 ? event.key : buffer + event.key;
			lastTyped = now;
			const index = options.findIndex((option) =>
				option.label.toLocaleLowerCase().startsWith(buffer.toLocaleLowerCase()),
			);
			if (index >= 0) {
				active = index;
				await tick();
				revealActive();
			}
		}
	}
	onMount(() => {
		const observer = new ResizeObserver(position);
		observer.observe(trigger);
		const dismiss = (event: Event) => {
			if (open && !(event.target instanceof Node && panel?.contains(event.target))) position();
		};
		window.addEventListener('resize', dismiss);
		document.addEventListener('scroll', dismiss, true);
		return () => {
			observer.disconnect();
			window.removeEventListener('resize', dismiss);
			document.removeEventListener('scroll', dismiss, true);
		};
	});
</script>

<span class="select-menu">
	<button
		bind:this={trigger}
		{id}
		type="button"
		role="combobox"
		{disabled}
		aria-expanded={open}
		aria-haspopup="listbox"
		aria-controls={`${uid}-options`}
		aria-required={required || undefined}
		aria-activedescendant={open ? `${uid}-option-${active}` : undefined}
		onclick={() => (open ? close() : void expand())}
		onkeydown={keyboard}
	>
		<span>{selected?.label ?? value}</span><ChevronDown size={18} aria-hidden="true" />
	</button>
	<div
		bind:this={panel}
		id={`${uid}-options`}
		class="options"
		popover="auto"
		role="listbox"
		aria-labelledby={id}
		ontoggle={() => {
			open = panel?.matches(':popover-open') ?? false;
		}}
	>
		{#each options as option, index (option.value)}
			<div
				id={`${uid}-option-${index}`}
				role="option"
				aria-selected={option.value === value}
				tabindex="-1"
				class:active={active === index}
				onclick={() => choose(index)}
				onpointerdown={(event: PointerEvent) => event.preventDefault()}
				onpointermove={() => (active = index)}
				onkeydown={(event: KeyboardEvent) => {
					if (event.key === 'Enter' || event.key === ' ') {
						event.preventDefault();
						choose(index);
					}
				}}
			>
				<span class="check">
					{#if option.value === value}<Check size={16} aria-hidden="true" />{/if}
				</span>
				<span>{option.label}</span>
			</div>
		{/each}
	</div>
</span>

<style>
	.select-menu {
		display: block;
		width: 100%;
		min-width: 0;
	}
	button {
		display: flex;
		align-items: center;
		justify-content: space-between;
		gap: 12px;
		width: 100%;
		min-height: var(--control-h);
		padding: 7px 12px;
		background: var(--surface);
		color: var(--text);
		border: 1px solid var(--outline);
		border-radius: var(--radius-control);
		font: inherit;
		font-size: 16px;
		line-height: 24px;
		text-align: left;
		cursor: pointer;
	}
	button :global(svg) {
		flex-shrink: 0;
		color: var(--text-muted);
	}
	button[aria-expanded='true'],
	button:hover:not(:disabled) {
		border-color: var(--connection, var(--outline));
	}
	button:disabled {
		opacity: 0.6;
		cursor: default;
	}
	.options {
		position: fixed;
		margin: 0;
		padding: 4px;
		overflow-y: auto;
		overscroll-behavior: contain;
		border: 1px solid var(--border);
		border-radius: 6px;
		background: var(--surface);
		color: var(--text);
		box-shadow: 0 8px 24px rgb(0 0 0 / 0.22);
	}
	[role='option'] {
		display: flex;
		align-items: center;
		gap: 10px;
		min-height: 36px;
		padding: 8px 10px;
		border-radius: 4px;
		font-size: 14px;
		line-height: 20px;
		cursor: pointer;
	}
	[role='option'].active {
		background: var(--surface-muted);
	}
	[role='option'][aria-selected='true'] {
		font-weight: 500;
	}
	.check {
		display: inline-flex;
		flex: 0 0 16px;
		width: 16px;
	}
	@media (max-width: 767px) {
		button,
		[role='option'] {
			min-height: 44px;
		}
	}
	@media (forced-colors: active) {
		button,
		.options {
			border-color: ButtonText;
		}
		[role='option'].active {
			background: Highlight;
			color: HighlightText;
		}
	}
</style>

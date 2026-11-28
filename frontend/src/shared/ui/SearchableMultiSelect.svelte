<script lang="ts">
	import { onMount, tick } from 'svelte';
	import { t } from '../localization/i18n';
	import { Check, ChevronDown, Search, X } from '@lucide/svelte';

	let {
		id,
		values = [],
		options = [],
		placeholder = 'Select options...',
		searchPlaceholder = 'Search...',
		emptyMessage = 'No matching options',
		disabled = false,
		required = false,
		ariaInvalid = false,
		ariaDescribedby = undefined,
		searchable = undefined,
		maxItems = 8,
		onchange,
	}: {
		id: string;
		values: string[];
		options: { value: string; label: string; description?: string }[];
		placeholder?: string;
		searchPlaceholder?: string;
		emptyMessage?: string;
		disabled?: boolean;
		required?: boolean;
		ariaInvalid?: boolean;
		ariaDescribedby?: string;
		/** Show the search box. Default: only when the list is long enough to need one. */
		searchable?: boolean;
		maxItems?: number;
		onchange: (values: string[]) => void;
	} = $props();

	const uid = $props.id();
	let trigger: HTMLButtonElement | undefined = $state();
	/** the input-like box holding the badges and the trigger; the panel anchors to it */
	let box: HTMLDivElement | undefined = $state();
	let panel: HTMLDivElement | undefined = $state();
	let searchInput: HTMLInputElement | undefined = $state();
	let listEl: HTMLDivElement | undefined = $state();
	/** Short, fixed lists (a type, a mode) are plain selects; long ones get a search box. */
	const SEARCH_FROM = 9;
	const showSearch = $derived(searchable ?? options.length >= SEARCH_FROM);
	let typed = '';
	let typedAt = 0;
	let open = $state(false);
	let search = $state('');
	let active = $state(0);

	const selectedSet = $derived(new Set(values));

	const filtered = $derived(
		options.filter(
			(o) =>
				o.label.toLowerCase().includes(search.toLowerCase()) ||
				(o.description && o.description.toLowerCase().includes(search.toLowerCase())),
		),
	);

	function position() {
		if (!open || !panel || !box) return;
		const rect = box.getBoundingClientRect();
		const below = innerHeight - rect.bottom - 16;
		const above = rect.top - 16;
		const useAbove = below < Math.min(panel.scrollHeight, 240) && above > below;
		panel.style.width = `${Math.min(rect.width, innerWidth - 16)}px`;
		panel.style.maxHeight = `${Math.max(60, Math.min(280, useAbove ? above : below))}px`;
		panel.style.left = `${Math.max(8, Math.min(rect.left, innerWidth - panel.offsetWidth - 8))}px`;
		panel.style.top = `${useAbove ? Math.max(8, rect.top - panel.offsetHeight - 6) : rect.bottom + 6}px`;
	}

	function revealActive() {
		if (!panel) return;
		const listbox = panel.querySelector('[role="listbox"]') as HTMLElement | null;
		if (!listbox) return;
		const option = listbox.children[active] as HTMLElement | undefined;
		if (!option) return;
		const bounds = option.getBoundingClientRect();
		const viewport = listbox.getBoundingClientRect();
		if (bounds.top < viewport.top) listbox.scrollTop -= viewport.top - bounds.top;
		else if (bounds.bottom > viewport.bottom) listbox.scrollTop += bounds.bottom - viewport.bottom;
	}

	async function expand() {
		if (disabled || !panel || !trigger) return;
		search = '';
		const selectedIndex = options.findIndex((option) => selectedSet.has(option.value));
		active = selectedIndex >= 0 ? selectedIndex : 0;
		if (typeof panel.showPopover === 'function') {
			panel.showPopover();
		}
		open = true;
		await tick();
		position();
		if (showSearch) searchInput?.focus({ preventScroll: true });
		else listEl?.focus({ preventScroll: true });
		const listbox = panel.querySelector('[role="listbox"]');
		if (listbox) listbox.scrollTop = 0;
		revealActive();
	}

	function close(restoreFocus = true) {
		if (typeof panel?.hidePopover === 'function') {
			try {
				panel.hidePopover();
			} catch {}
		}
		open = false;
		search = '';
		if (restoreFocus) trigger?.focus();
	}

	function toggle(optionValue: string) {
		if (disabled) return;
		let next: string[];
		if (selectedSet.has(optionValue)) {
			next = values.filter((v) => v !== optionValue);
		} else {
			if (values.length >= maxItems) return;
			next = [...values, optionValue];
		}
		onchange(next);
	}

	function removeItem(itemValue: string, e?: MouseEvent) {
		e?.stopPropagation();
		if (disabled) return;
		onchange(values.filter((v) => v !== itemValue));
	}

	function onSearchKeydown(event: KeyboardEvent) {
		if (event.key === 'Tab') {
			close(false);
			return;
		}
		if (event.key === 'Escape') {
			event.preventDefault();
			event.stopPropagation();
			close(true);
			return;
		}
		if (event.key === 'ArrowDown') {
			event.preventDefault();
			if (filtered.length > 0) {
				active = (active + 1) % filtered.length;
				revealActive();
			}
			return;
		}
		if (event.key === 'ArrowUp') {
			event.preventDefault();
			if (filtered.length > 0) {
				active = (active - 1 + filtered.length) % filtered.length;
				revealActive();
			}
			return;
		}
		if (event.key === 'Enter' || event.key === ' ') {
			event.preventDefault();
			if (filtered.length > 0 && active >= 0 && active < filtered.length) {
				toggle(filtered[active].value);
			}
			return;
		}
	}

	/** Keyboard for the list when there is no search box: arrows, Home/End, Enter/Space, type-ahead. */
	function onListKeydown(event: KeyboardEvent) {
		if (showSearch) return;
		if (event.key === ' ') {
			event.preventDefault();
			if (filtered[active]) toggle(filtered[active].value);
			return;
		}
		if (event.key === 'Home' || event.key === 'End') {
			event.preventDefault();
			active = event.key === 'Home' ? 0 : Math.max(0, filtered.length - 1);
			revealActive();
			return;
		}
		if (event.key.length === 1 && !event.ctrlKey && !event.metaKey && !event.altKey) {
			const now = Date.now();
			typed = now - typedAt > 600 ? event.key : typed + event.key;
			typedAt = now;
			const index = filtered.findIndex((o) => o.label.toLowerCase().startsWith(typed.toLowerCase()));
			if (index >= 0) {
				active = index;
				revealActive();
			}
			return;
		}
		onSearchKeydown(event);
	}

	function onTriggerKeydown(event: KeyboardEvent) {
		if (event.key === 'Escape') {
			if (open) {
				event.preventDefault();
				close(true);
			}
			return;
		}
		// Backspace on the empty trigger removes the last badge, like a tag input.
		if (event.key === 'Backspace' && !open && values.length && !disabled) {
			event.preventDefault();
			onchange(values.slice(0, -1));
			return;
		}
		if (['Enter', ' ', 'ArrowDown', 'ArrowUp'].includes(event.key)) {
			event.preventDefault();
			if (!open) {
				void expand();
			}
		}
	}

	onMount(() => {
		if (!box) return;
		const observer = new ResizeObserver(position);
		observer.observe(box);
		const dismiss = (event: Event) => {
			if (open && !(event.target instanceof Node && panel?.contains(event.target))) {
				position();
			}
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

<div class="searchable-multiselect">
	<!-- Input-like box: selected values are removable badges inside it; the
       combobox trigger fills the rest of the line and opens the list. -->
	<!-- svelte-ignore a11y_click_events_have_key_events, a11y_no_static_element_interactions -->
	<div
		bind:this={box}
		class="control"
		class:invalid={ariaInvalid}
		class:open
		class:disabled
		onclick={(event) => {
			if (event.target === box && !disabled) {
				trigger?.focus();
				if (!open) void expand();
			}
		}}
	>
		{#if values.length > 0}
			<ul class="badges" role="list" aria-label={t('middlewares.multiselect.selected')}>
				{#each values as val (val)}
					{@const opt = options.find((o) => o.value === val)}
					<li class="chip">
						<span class="chip-text">{opt ? opt.label : val}</span>
						{#if !disabled}
							<button
								type="button"
								class="chip-remove"
								aria-label={t('middlewares.multiselect.remove', { label: opt ? opt.label : val })}
								onclick={(e) => removeItem(val, e)}
							>
								<X size={12} aria-hidden="true" />
							</button>
						{/if}
					</li>
				{/each}
			</ul>
		{/if}
		<button
			bind:this={trigger}
			{id}
			type="button"
			role="combobox"
			class="trigger"
			{disabled}
			aria-expanded={open}
			aria-haspopup="listbox"
			aria-controls={id ? `${id}-listbox` : `${uid}-listbox`}
			aria-required={required || undefined}
			aria-invalid={ariaInvalid || undefined}
			aria-describedby={ariaDescribedby}
			aria-activedescendant={open && filtered.length > 0 ? `${id || uid}-opt-${active}` : undefined}
			onclick={() => (open ? close(true) : void expand())}
			onkeydown={onTriggerKeydown}
		>
			{#if values.length === 0}
				<span class="placeholder">{placeholder}</span>
			{:else}
				<span class="sr-only">{values.map((v) => options.find((o) => o.value === v)?.label || v).join(', ')}</span>
			{/if}
			<ChevronDown size={18} aria-hidden="true" />
		</button>
	</div>

	<div
		bind:this={panel}
		id={id ? `${id}-panel` : `${uid}-panel`}
		class="dropdown-panel"
		popover="auto"
		ontoggle={() => {
			open = panel?.matches(':popover-open') ?? false;
			if (!open) search = '';
		}}
	>
		{#if showSearch}
			<div class="search-box">
				<Search size={16} class="search-icon" aria-hidden="true" />
				<input
					bind:this={searchInput}
					type="text"
					role="searchbox"
					placeholder={searchPlaceholder}
					aria-label={searchPlaceholder}
					bind:value={search}
					onkeydown={onSearchKeydown}
					oninput={() => {
						active = 0;
						position();
					}}
				/>
				{#if search}
					<button
						type="button"
						class="clear-btn"
						aria-label={t('middlewares.multiselect.clear')}
						onclick={() => {
							search = '';
							searchInput?.focus();
						}}
					>
						<X size={14} aria-hidden="true" />
					</button>
				{/if}
			</div>
		{/if}

		<div
			bind:this={listEl}
			onkeydown={onListKeydown}
			id={id ? `${id}-listbox` : `${uid}-listbox`}
			class="options-list"
			role="listbox"
			aria-multiselectable="true"
			aria-labelledby={id}
			tabindex="-1"
		>
			{#if filtered.length === 0}
				<div class="empty-state" role="option" aria-selected="false" aria-disabled="true">{emptyMessage}</div>
			{:else}
				{#each filtered as option, index (option.value)}
					{@const isChecked = selectedSet.has(option.value)}
					<div
						id={`${id || uid}-opt-${index}`}
						role="option"
						tabindex="-1"
						aria-selected={isChecked}
						class="option-item"
						class:active={active === index}
						class:selected={isChecked}
						onclick={() => toggle(option.value)}
						onkeydown={(e) => {
							if (e.key === 'Enter' || e.key === ' ') {
								e.preventDefault();
								toggle(option.value);
							}
						}}
						onpointerdown={(e) => e.preventDefault()}
						onpointermove={() => (active = index)}
					>
						<span class="checkbox-box" class:checked={isChecked}>
							{#if isChecked}
								<Check size={14} aria-hidden="true" />
							{/if}
						</span>
						<span class="option-content">
							<span class="option-label">{option.label}</span>
							{#if option.description}
								<span class="option-desc">{option.description}</span>
							{/if}
						</span>
					</div>
				{/each}
			{/if}
		</div>

		<div class="panel-footer">
			<span class="footer-count">
				{t('middlewares.multiselect.count', { count: values.length, max: maxItems })}
			</span>
			<button type="button" class="done-btn" onclick={() => close(true)}>
				{t('middlewares.multiselect.done')}
			</button>
		</div>
	</div>
</div>

<style>
	.searchable-multiselect {
		display: flex;
		flex-direction: column;
		gap: 8px;
		width: 100%;
		min-width: 0;
		position: relative;
	}
	.control {
		display: flex;
		flex-wrap: wrap;
		align-items: center;
		gap: 6px;
		width: 100%;
		min-height: var(--control-h, 40px);
		padding: 4px 4px 4px 8px;
		background: var(--surface);
		border: 1px solid var(--outline);
		border-radius: var(--radius-control, 6px);
		box-sizing: border-box;
		cursor: pointer;
	}
	.control:hover:not(.disabled),
	.control.open {
		border-color: var(--connection, var(--outline));
	}
	.control:focus-within {
		outline: 2px solid var(--focus-ring);
		outline-offset: -1px;
	}
	.control.invalid {
		border-color: var(--danger-text);
	}
	.control.disabled {
		opacity: 0.6;
		cursor: default;
	}
	.badges {
		display: contents;
		list-style: none;
	}
	.trigger {
		flex: 1 1 64px;
		display: flex;
		align-items: center;
		justify-content: space-between;
		gap: 8px;
		min-width: 64px;
		min-height: 30px;
		padding: 0 6px 0 4px;
		border: 0;
		background: transparent;
		color: var(--text);
		font: inherit;
		font-size: 16px;
		line-height: 24px;
		text-align: left;
		cursor: pointer;
	}
	.trigger:focus-visible {
		outline: none;
	}
	.trigger :global(svg) {
		flex-shrink: 0;
		margin-left: auto;
		color: var(--text-muted);
	}
	.trigger:disabled {
		cursor: default;
	}
	.placeholder {
		color: var(--text-muted);
		padding-left: 4px;
	}
	.chip {
		display: inline-flex;
		align-items: center;
		gap: 2px;
		max-width: 100%;
		padding: 1px 2px 1px 8px;
		background: var(--connection-soft);
		color: var(--text);
		border: 1px solid var(--connection-border);
		border-radius: 4px;
		font-size: 13px;
		line-height: 22px;
	}
	.chip-text {
		overflow: hidden;
		text-overflow: ellipsis;
		white-space: nowrap;
	}
	.chip-remove {
		display: inline-flex;
		align-items: center;
		justify-content: center;
		width: 20px;
		height: 20px;
		padding: 0;
		border: none;
		background: transparent;
		color: var(--text-muted);
		cursor: pointer;
		border-radius: 3px;
	}
	.chip-remove:hover {
		color: var(--danger-text);
		background: var(--danger-bg);
	}
	.dropdown-panel {
		position: fixed;
		margin: 0;
		padding: 6px;
		flex-direction: column;
		gap: 6px;
		border: 1px solid var(--border);
		border-radius: 6px;
		background: var(--surface);
		color: var(--text);
		box-shadow: 0 8px 24px rgb(0 0 0 / 0.22);
		box-sizing: border-box;
		z-index: 1000;
	}
	.dropdown-panel:not(:popover-open) {
		display: none;
	}
	.dropdown-panel:popover-open {
		display: flex;
	}
	.search-box {
		display: flex;
		align-items: center;
		gap: 6px;
		padding: 4px 8px;
		border: 1px solid var(--outline);
		border-radius: 4px;
		background: var(--surface-muted);
	}
	.search-box :global(.search-icon) {
		color: var(--text-muted);
		flex-shrink: 0;
	}
	.search-box input {
		flex: 1;
		width: 100%;
		min-width: 0;
		min-height: var(--control-h);
		border: 1px solid var(--outline);
		border-radius: var(--radius-control);
		background: var(--surface);
		color: var(--text);
		font: inherit;
		font-size: 16px;
		line-height: 24px;
		padding: 7px 12px;
	}
	@media (max-width: 767px) {
		.search-box input {
			min-height: var(--touch-h);
		}
	}
	.clear-btn {
		display: inline-flex;
		align-items: center;
		justify-content: center;
		padding: 2px;
		border: none;
		background: transparent;
		color: var(--text-muted);
		cursor: pointer;
		border-radius: 2px;
	}
	.options-list:focus {
		outline: none;
	}
	.options-list {
		overflow-y: auto;
		overscroll-behavior: contain;
		max-height: 200px;
	}
	.option-item {
		display: flex;
		align-items: center;
		gap: 8px;
		min-height: 32px;
		padding: 6px 8px;
		border-radius: 4px;
		font-size: 14px;
		line-height: 20px;
		cursor: pointer;
	}
	.option-item.active {
		background: var(--surface-muted);
	}
	.checkbox-box {
		display: inline-flex;
		align-items: center;
		justify-content: center;
		flex: 0 0 16px;
		width: 16px;
		height: 16px;
		border: 1px solid var(--outline);
		border-radius: 3px;
		background: var(--surface);
		color: #fff;
	}
	.checkbox-box.checked {
		background: var(--connection, #3182ce);
		border-color: var(--connection, #3182ce);
	}
	.option-content {
		display: flex;
		flex-direction: column;
		min-width: 0;
		overflow: hidden;
	}
	.option-label {
		overflow: hidden;
		text-overflow: ellipsis;
		white-space: nowrap;
	}
	.option-desc {
		font-size: 12px;
		color: var(--text-muted);
	}
	.panel-footer {
		display: flex;
		align-items: center;
		justify-content: space-between;
		padding: 6px 8px 2px 8px;
		border-top: 1px solid var(--border);
		font-size: 12px;
	}
	.footer-count {
		color: var(--text-muted);
	}
	.done-btn {
		padding: 4px 10px;
		border: 1px solid var(--outline);
		border-radius: 4px;
		background: var(--surface-muted);
		color: var(--text);
		font: inherit;
		font-size: 12px;
		cursor: pointer;
	}
	.done-btn:hover {
		background: var(--surface);
		border-color: var(--connection, #3182ce);
	}
	.empty-state {
		padding: 12px 8px;
		font-size: 13px;
		color: var(--text-muted);
		text-align: center;
	}
	@media (max-width: 767px) {
		button[role='combobox'],
		.option-item {
			min-height: 44px;
		}
	}
</style>

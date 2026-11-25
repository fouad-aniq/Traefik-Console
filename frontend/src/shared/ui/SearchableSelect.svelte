<script lang="ts">
  import { onMount, tick } from 'svelte';
  import { Check, ChevronDown, Search, X } from '@lucide/svelte';

  let {
    id,
    value = '',
    options = [],
    placeholder = 'Select...',
    searchPlaceholder = 'Search...',
    emptyMessage = 'No matching options',
    disabled = false,
    required = false,
    ariaLabel = '',
    ariaInvalid = false,
    ariaDescribedby = undefined,
    searchable = undefined,
    actionLabel = '',
    onchange,
  }: {
    id: string;
    value: string;
    options: { value: string; label: string; description?: string; badge?: string }[];
    placeholder?: string;
    searchPlaceholder?: string;
    emptyMessage?: string;
    disabled?: boolean;
    required?: boolean;
    ariaLabel?: string;
    ariaInvalid?: boolean;
    ariaDescribedby?: string;
    /** Show the search box. Default: only when the list is long enough to need one. */
    searchable?: boolean;
    actionLabel?: string;
    onchange: (value: string) => void;
  } = $props();

  const uid = $props.id();
  let trigger: HTMLButtonElement | undefined = $state();
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

  const filtered = $derived(
    options.filter((o) =>
      o.label.toLowerCase().includes(search.toLowerCase()) ||
      (o.description && o.description.toLowerCase().includes(search.toLowerCase()))
    )
  );

  const selected = $derived(options.find((o) => o.value === value));

  function position() {
    if (!open || !panel || !trigger) return;
    const rect = trigger.getBoundingClientRect();
    const below = innerHeight - rect.bottom - 16;
    const above = rect.top - 16;
    const useAbove = below < Math.min(panel.scrollHeight, 220) && above > below;
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
    const idx = options.findIndex((o) => o.value === value);
    active = idx >= 0 ? idx : 0;
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

  function choose(index: number) {
    if (disabled || !filtered[index]) return;
    onchange(filtered[index].value);
    close(true);
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
    if (event.key === 'Enter') {
      event.preventDefault();
      if (filtered.length > 0 && active >= 0 && active < filtered.length) {
        choose(active);
      }
      return;
    }
  }

  /** Keyboard for the list when there is no search box: arrows, Home/End, Enter/Space, type-ahead. */
  function onListKeydown(event: KeyboardEvent) {
    if (showSearch) return;
    if (event.key === ' ') {
      event.preventDefault();
      if (filtered[active]) choose(active);
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
      if (index >= 0) { active = index; revealActive(); }
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
    if (['Enter', ' ', 'ArrowDown', 'ArrowUp'].includes(event.key)) {
      event.preventDefault();
      if (!open) {
        void expand();
      }
    }
  }

  onMount(() => {
    if (!trigger) return;
    const observer = new ResizeObserver(position);
    observer.observe(trigger);
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

<span class="searchable-select">
  <button
    bind:this={trigger}
    {id}
    type="button"
    role="combobox"
    {value}
    aria-label={ariaLabel || undefined}
    {disabled}
    aria-expanded={open}
    aria-haspopup="listbox"
    aria-controls={id ? `${id}-listbox` : `${uid}-listbox`}
    aria-required={required || undefined}
    aria-invalid={ariaInvalid || undefined}
    aria-describedby={ariaDescribedby}
    aria-activedescendant={open && filtered.length > 0 ? `${id || uid}-opt-${active}` : undefined}
    class:invalid={ariaInvalid}
    onclick={() => (open ? close(true) : void expand())}
    onkeydown={onTriggerKeydown}
  >
    <span class="label" class:placeholder={!selected}>
      {selected ? selected.label : placeholder}
    </span>
    <ChevronDown size={18} aria-hidden="true" />
  </button>

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
          aria-label="Clear search"
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
      aria-labelledby={id}
      tabindex="-1"
    >
      {#if filtered.length === 0}
        <div class="empty-state" role="option" aria-selected="false" aria-disabled="true">{emptyMessage}</div>
      {:else}
        {#each filtered as option, index (option.value)}
          <div
            id={`${id || uid}-opt-${index}`}
            role="option"
            tabindex="-1"
            aria-selected={option.value === value}
            class="option-item"
            class:active={active === index}
            class:selected={option.value === value}
            onclick={() => choose(index)}
            onkeydown={(e) => {
              if (e.key === 'Enter' || e.key === ' ') {
                e.preventDefault();
                choose(index);
              }
            }}
            onpointerdown={(e) => e.preventDefault()}
            onpointermove={() => (active = index)}
          >
            <span class="check">
              {#if option.value === value}
                <Check size={16} aria-hidden="true" />
              {/if}
            </span>
            <span class="option-content">
              <span class="option-label">{option.label}{#if option.badge}<span class="option-badge">{option.badge}</span>{/if}</span>
              {#if option.description}
                <span class="option-desc">{option.description}</span>
              {/if}
            </span>
            {#if actionLabel}<span class="option-action">{actionLabel}</span>{/if}
          </div>
        {/each}
      {/if}
    </div>
  </div>
</span>

<style>
 .option-label { display:flex; align-items:center; flex-wrap:wrap; gap:8px; }
 .option-badge { font-size:11px; line-height:18px; padding:0 5px; border:1px solid var(--border); border-radius:4px; color:var(--connection); background:var(--surface-muted); }
 .option-action { margin-left:auto; flex-shrink:0; padding:4px 10px; border:1px solid var(--outline); border-radius:var(--radius-control); font-size:13px; color:var(--text); }
  .searchable-select {
    display: block;
    width: 100%;
    min-width: 0;
    position: relative;
  }
  button[role='combobox'] {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 12px;
    width: 100%;
    min-height: var(--control-h, 36px);
    padding: 7px 12px;
    background: var(--surface);
    color: var(--text);
    border: 1px solid var(--outline);
    border-radius: var(--radius-control, 4px);
    font: inherit;
    font-size: 16px;
    line-height: 24px;
    text-align: left;
    cursor: pointer;
    box-sizing: border-box;
  }
  button[role='combobox'] :global(svg) {
    flex-shrink: 0;
    color: var(--text-muted);
  }
  button[role='combobox'][aria-expanded='true'],
  button[role='combobox']:hover:not(:disabled) {
    border-color: var(--connection, var(--outline));
  }
  button[role='combobox'].invalid {
    border-color: var(--danger, #e53e3e);
  }
  button[role='combobox']:disabled {
    opacity: 0.6;
    cursor: default;
  }
  .label {
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }
  .label.placeholder {
    color: var(--text-muted);
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
  @media (max-width:767px) { .search-box input { min-height:var(--touch-h); } }
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
  .clear-btn:hover {
    color: var(--text);
  }
  .options-list:focus { outline: none; }
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
  .option-item.selected {
    font-weight: 500;
  }
  .check {
    display: inline-flex;
    align-items: center;
    justify-content: center;
    flex: 0 0 16px;
    width: 16px;
    color: var(--connection, #3182ce);
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
  @media (forced-colors: active) {
    button[role='combobox'],
    .dropdown-panel {
      border-color: ButtonText;
    }
    .option-item.active {
      background: Highlight;
      color: HighlightText;
    }
  }
</style>

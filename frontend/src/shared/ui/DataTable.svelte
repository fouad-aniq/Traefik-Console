<script lang="ts" generics="Row">
	import { onMount, type Snippet } from 'svelte';
 import { Search, ArrowDownUp, ArrowUp, ArrowDown, RefreshCw } from '@lucide/svelte';
 import TableFilters from './TableFilters.svelte';
	import { t } from '../localization/i18n';
	import ActiveFilterSummaries, { type ActiveFilter } from './ActiveFilterSummaries.svelte';
	import Pagination from './Pagination.svelte';
	import type { DataTableColumn, DataTableFilter, SortState } from './tableTypes';

	let {
		columns,
		rows,
		getRowId,
		searchValue = '',
		onsearch,
		searchPlaceholder = '',
		filters = [],
		filterValues = {},
		onfilterchange,
		sort = { column: '', direction: 'none' },
		onsort,
		page,
		pageSize,
		total,
		onpage,
		onpagesize,
		loading = false,
		error = false,
		rowSnippet,
		emptyState,
		toolbarActions,
  onretry, regionLabel, hasNext, pageResultCount, minWidth
	}: {
		columns: DataTableColumn[];
		rows: Row[];
		getRowId: (row: Row) => string;
		searchValue?: string;
		onsearch: (value: string) => void;
		searchPlaceholder?: string;
		filters?: DataTableFilter[];
		filterValues?: Record<string, string>;
		onfilterchange?: (id: string, value: string) => void;
		sort?: SortState;
		onsort?: (column: string) => void;
		page: number;
		pageSize: number;
		total: number;
		onpage: (page: number) => void;
		onpagesize: (size: number) => void;
		loading?: boolean;
		error?: boolean;
		rowSnippet: Snippet<[Row]>;
		emptyState: Snippet;
		toolbarActions?: Snippet;
  onretry?: () => void;
  regionLabel?: string;
  hasNext?: boolean;
  pageResultCount?: number;
  minWidth?: number;
	} = $props();

	let searchInputId = $props.id();
 const hasActions=$derived(columns.at(-1)?.id==='actions');
 let scrollElement=$state<HTMLDivElement>();
 let primaryWidth=$state(0),actionWidth=$state(0);
 /** Columns hidden to the left / right of the visible part: shown as a soft shadow on the pinned columns. */
 let moreLeft=$state(false),moreRight=$state(false);
 const edges=()=>{const el=scrollElement;if(!el)return;moreLeft=el.scrollLeft>1;moreRight=el.scrollLeft+el.clientWidth<el.scrollWidth-1;};
 onMount(()=>{
  if(!scrollElement)return;
  const first=scrollElement.querySelector('th:first-child'),last=scrollElement.querySelector('th:last-child');
  const measure=()=>{primaryWidth=first?.getBoundingClientRect().width||0;actionWidth=hasActions?last?.getBoundingClientRect().width||0:0;edges();};
  const resize=new ResizeObserver(measure);resize.observe(scrollElement);const table=scrollElement.querySelector('table');if(table)resize.observe(table);if(first)resize.observe(first);if(last)resize.observe(last);measure();return()=>resize.disconnect();
 });

	const activeSummaries = $derived.by(() => {
		const list: ActiveFilter[] = [];
		if (searchValue) list.push({ id: '__search__', label: `“${searchValue}”` });
		for (const filter of filters) {
			const value = filterValues[filter.id] ?? '';
			if (value) {
				const option = filter.options.find((o) => o.value === value);
				list.push({ id: filter.id, label: option ? option.label : value });
			}
		}
		return list;
	});

	function ariaSort(columnId: string): 'ascending' | 'descending' | 'none' {
		if (sort.column !== columnId || sort.direction === 'none') return 'none';
		return sort.direction === 'asc' ? 'ascending' : 'descending';
	}

</script>

<div class="toolbar">
	<div class="search">
  <Search size={18} aria-hidden="true" class="search-icon" />
		<label class="sr-only" for={searchInputId}>{t('table.search')}</label>
		<input
			id={searchInputId}
			type="search"
			placeholder={searchPlaceholder || t('table.search')}
			value={searchValue}
			oninput={(e: Event) => onsearch((e.currentTarget as HTMLInputElement).value)}
		/>
	</div>
 {#if filters.length}<TableFilters {filters} values={filterValues} onchange={(id,value) => onfilterchange?.(id,value)} />{/if}
 {#if onretry}<button type="button" class="btn-secondary refresh" aria-label={t('common.refresh')} disabled={loading} onclick={onretry}><RefreshCw size={17} aria-hidden="true" /></button>{/if}
	{#if toolbarActions}
		<div class="actions">{@render toolbarActions()}</div>
	{/if}
</div>

<ActiveFilterSummaries
	filters={activeSummaries}
	onremove={(id) => {
		if (id === '__search__') onsearch('');
		else onfilterchange?.(id, '');
	}}
	onclear={() => {
		onsearch('');
		for (const filter of filters) onfilterchange?.(filter.id, '');
	}}
/>

<div class="table-container" class:loading class:has-state={loading || error || rows.length === 0}>
 <!-- svelte-ignore a11y_no_noninteractive_tabindex -->
 <div class="scroll" class:more-left={moreLeft} class:more-right={moreRight} onscroll={edges} bind:this={scrollElement} style:scroll-padding-left={`${primaryWidth+8}px`} style:scroll-padding-right={`${actionWidth+8}px`} tabindex="0" role="region" aria-label={regionLabel || t('table.results')}>
  <table class:has-actions={hasActions} aria-busy={loading} style:min-width={`${minWidth ?? Math.max(760, columns.length * 150)}px`}>
   <colgroup>{#each columns as column,index (column.id)}<col style:width={column.width || (column.id==='actions'?'112px':undefined)} />{/each}</colgroup>
   <thead><tr>{#each columns as column (column.id)}
    <th scope="col" aria-sort={ariaSort(column.id)}>
     {#if column.sortable && onsort}
      <button type="button" class="sort-btn" disabled={loading || error} onclick={() => onsort(column.id)}>
       <span>{column.label}</span><span class="indicator" aria-hidden="true">{#if sort.column !== column.id || sort.direction === 'none'}<ArrowDownUp size={14} />{:else if sort.direction === 'asc'}<ArrowUp size={14} />{:else}<ArrowDown size={14} />{/if}</span>
      </button>
     {:else}<span class:sr-only={column.hideLabel}>{column.label}</span>{/if}
    </th>
   {/each}</tr></thead>
   <tbody>
    {#if loading}
     {#each [0,1,2,3,4] as row}<tr class="skeleton-row" aria-hidden="true">{#each columns as column,index (column.id)}<td><span class="skeleton" class:short={index>1}></span>{#if index<2}<span class="skeleton secondary"></span>{/if}</td>{/each}</tr>{/each}
    {:else if error || rows.length === 0}
     <tr class="state-row"><td colspan={columns.length}><div class="state-space" aria-hidden="true"></div></td></tr>
    {:else}
     {#each rows as row (getRowId(row))}<tr>{@render rowSnippet(row)}</tr>{/each}
    {/if}
   </tbody>
  </table>
 </div>
 {#if loading || error || rows.length === 0}
  <div class="state" class:loading-state={loading} class:error-state={error} class:empty-state={!loading && !error} role={error ? 'alert' : 'status'}>
   {#if loading}<p class="loading-label">{t('table.loading')}</p>
   {:else if error}<h2>{t('table.loadFailed')}</h2><p>{t('table.loadFailedHelp')}</p>{#if onretry}<button type="button" class="btn-secondary" onclick={onretry}><RefreshCw size={16} aria-hidden="true" />{t('common.retry')}</button>{/if}
   {:else}{@render emptyState()}{/if}
  </div>
 {/if}
</div>
{#if !loading && !error && (rows.length || hasNext !== undefined)}
 <Pagination {page} {pageSize} {total} {onpage} {onpagesize} {hasNext} resultCount={pageResultCount ?? rows.length} />
{/if}

<style>
 .toolbar { display:flex; flex-wrap:wrap; align-items:center; gap:12px; }
 .search { position:relative; flex:1 1 280px; max-width:400px; min-width:0; }
 .search :global(.search-icon) { position:absolute; left:12px; top:50%; transform:translateY(-50%); pointer-events:none; color:var(--text-muted); }
 .search input { min-height:44px; border:1px solid var(--outline); border-radius:6px; background:var(--surface-muted); color:var(--text); padding:10px 12px 10px 36px; width:100%; min-width:0; font-size:14px; }
 .refresh { display:grid; place-items:center; width:44px; height:44px; padding:0; }
 .actions { margin-left:auto; display:flex; align-items:center; gap:12px; }
 .table-container { position:relative; margin-top:24px; border-top:1px solid var(--connection-border); border-bottom:1px solid var(--border); }
 .scroll { position:relative; overflow-x:auto;isolation:isolate; }
 table { width:100%; table-layout:fixed; border-collapse:collapse; font-size:14px; }
 th { height:48px; padding:8px 16px; border-bottom:1px solid var(--border); text-align:left; font-weight:500; font-size:12px; color:var(--text-muted); background:var(--canvas); }
 .sort-btn { display:flex; align-items:center; gap:8px; background:transparent; border:0; padding:0; color:inherit; font:inherit; cursor:pointer; min-height:32px;text-align:left; }
 .sort-btn:disabled { cursor:default; }
 .indicator { display:flex; color:var(--text-muted); }
 th[aria-sort='ascending'] .indicator,th[aria-sort='descending'] .indicator { color:var(--connection); }
 tbody :global(td) { height:64px; padding:8px 16px; border-bottom:1px solid var(--border); overflow-wrap:anywhere; }
 tbody :global(tr:last-child td) { border-bottom:0; }
 tbody :global(tr:hover td) { background:var(--surface); }
 th:first-child,tbody :global(tr:not(.state-row) td:first-child){position:sticky;left:0;z-index:2;background:var(--canvas);box-shadow:1px 0 0 var(--border);}
 .has-actions th:last-child,.has-actions :global(tbody tr:not(.state-row) td:last-child){position:sticky;right:0;z-index:2;background:var(--canvas);box-shadow:-1px 0 0 var(--border);text-align:right;}
 th:first-child,.has-actions th:last-child{z-index:3;}
 .more-left th:first-child,.more-left tbody :global(tr:not(.state-row) td:first-child){box-shadow:1px 0 0 var(--border),16px 0 16px -8px rgb(0 0 0 / .35);}
 .more-right .has-actions th:last-child,.more-right .has-actions :global(tbody tr:not(.state-row) td:last-child){box-shadow:-1px 0 0 var(--border),-16px 0 16px -8px rgb(0 0 0 / .35);}
 tbody :global(tr:hover td:first-child),.has-actions :global(tbody tr:hover td:last-child){background:var(--surface);}
 .has-actions :global(tbody td:last-child button){white-space:nowrap;}
 tbody :global(td:last-child) { text-align:right; }
 @media(max-width:767px){col:first-child{width:148px!important}th,tbody :global(td){padding-inline:12px}.has-actions col:last-child{width:112px!important}.has-actions th:last-child,.has-actions :global(tbody td:last-child){padding-inline:8px}.has-actions :global(tbody td:last-child button){min-height:44px;min-width:44px;}}
 tbody :global(a) { color:var(--text); font-weight:600; }
 tbody :global(button) { white-space:normal; }
 .state-space { min-height:360px; }
 .state-row td { padding:0; }
 .state { position:absolute; inset:49px 0 0; display:flex; flex-direction:column; align-items:center; justify-content:center; text-align:center; gap:16px; padding:32px 20px; }
 .state h2 { margin:0; font-size:20px; line-height:28px; font-weight:600; }
 .state p,.state :global(p) { margin:0; max-width:48ch; font-size:14px; line-height:20px; color:var(--text-muted); }
 .state button { display:inline-flex; align-items:center; gap:8px; min-height:44px; }
 .loading-label { padding:12px 20px; border:1px solid var(--border); border-radius:6px; background:var(--canvas); }
 .skeleton { display:block; width:75%; height:12px; border-radius:3px; background:var(--surface-muted); }
 .skeleton.short { width:52%; }
 .skeleton.secondary { margin-top:8px; width:42%; height:8px; }
 @media(max-width:767px) { .search { flex-basis:100%; max-width:none; } .table-container { margin-inline:-16px; } .actions { margin-left:auto; } }
</style>

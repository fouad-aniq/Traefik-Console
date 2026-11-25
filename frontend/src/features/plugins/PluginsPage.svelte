<script lang="ts">
    import {onMount, onDestroy} from 'svelte';
    import {ArrowRight, CircleAlert, CircleCheck, CircleHelp} from '@lucide/svelte';
    import DataTable from '../../shared/ui/DataTable.svelte';
    import RowActions from '../../shared/ui/RowActions.svelte';
    import PageHeader from '../../shared/ui/PageHeader.svelte';

    import Button from '../../shared/ui/Button.svelte';
    import Notice from '../../shared/ui/Notice.svelte';
    import StatusBadge from '../../shared/ui/StatusBadge.svelte';
    import ValueTooltip from '../../shared/ui/ValueTooltip.svelte';
    import ConfirmDialog from '../../shared/ui/ConfirmDialog.svelte';
    import ReferenceCount from '../../shared/ui/ReferenceCount.svelte';
    import RelatedItemsDialog from '../../shared/ui/RelatedItemsDialog.svelte';
    import { middlewareItem, type RelatedItem } from '../../shared/ui/related';
    import RelativeTime from '../../shared/ui/RelativeTime.svelte';
    import PluginDialog from './PluginDialog.svelte';
    import {deletePlugin, listMiddlewareRefs, listPlugins, syncCatalog, type MiddlewareRef} from './api';
    import {usersOf, type PluginResource} from './models';
    import {ApiError} from '../../shared/api/client';
    import {errorMessage} from '../../shared/api/errorMessage';
    import {t} from '../../shared/localization/i18n';
    import {needsManualRestart} from '../../core/mutations';
    import type {SortState} from '../../shared/ui/tableTypes';

    let {navigate, mode = 'list', name = '', module = ''}: {
        navigate: (p: string) => void;
        tab?: string;
        mode?: string;
        name?: string;
        module?: string
    } = $props();

    const p = (key: string, args?: Record<string, string | number>) => t('pluginsV2.' + key, args);
    const rv2 = (key: string, args?: Record<string, string | number>) => t('resolverV2.' + key, args);
    const ru = (key: string, args?: Record<string, string | number>) => t('resolverUI.' + key, args);

    let plugins = $state<PluginResource[]>([]);
    let middlewares = $state<MiddlewareRef[]>([]);
    let middlewaresFailed = $state(false);
    const updates = $derived(Object.fromEntries(plugins.map((x) => [x.name, x.update])));
    const checkedAt = $derived(plugins.find((x) => x.update?.checked_at)?.update?.checked_at ?? '');
    const lastFailed = $derived(plugins.some((x) => x.update?.last_attempt_failed));
    let updateError = $state('');
    let checking = $state(false);
    let updateFilter = $state('');
    const stale = $derived(plugins.some((x) => x.update) && (lastFailed || !checkedAt || Date.now() - Date.parse(checkedAt.replace(' ', 'T') + 'Z') > 86400000));

    let loading = $state(true);
    let error = $state('');
    let actionError = $state('');
    let restartNotice = $state(false);
    let search = $state('');

    let originFilter = $state('');
    let page = $state(1);
    let pageSize = $state(25);
    let sort = $state<SortState>({column: 'name', direction: 'asc'});
    let usage = $state<{ name: string; items: RelatedItem[] } | null>(null);
    let pendingDelete = $state<PluginResource | null>(null);
    let deleting = $state(false);

    let generation = 0;
    let disposed = false;
    let controller = new AbortController();

    const columns = $derived([
        {id: 'name', label: p('alias'), sortable: true, width: 'clamp(160px, 22vw, 300px)'},
        {id: 'module', label: p('module'), sortable: true, width: 'clamp(200px, 30vw, 400px)'},
        {id: 'version', label: p('version'), sortable: true, width: '210px'},
        {id: 'origin', label: ru('ownership'), sortable: true, width: '170px'},
        {id: 'state', label: rv2('stateColumn'), sortable: true, width: '160px'},
        {id: 'usedBy', label: p('usedBy'), width: '150px'},
        {id: 'actions', label: '', hideLabel: true, width: '172px'}
    ]);
    const filters = $derived([
        {
            id: 'update',
            label: p('updateStatus'),
            options: [{value: '', label: ru('all')}, {value: 'available', label: p('updatesOnly')}]
        },
        {
            id: 'origin',
            label: ru('ownership'),
            options: [{value: '', label: ru('all')}, {value: 'managed', label: ru('managed')}, {
                value: 'external',
                label: ru('external')
            }]
        }
    ]);

    const users = (alias: string) => usersOf(alias, middlewares);
    const sortValue = (row: PluginResource, column: string) => {
        switch (column) {
            case 'name':
                return row.name;
            case 'module':
                return row.config.module_name ?? '';
            case 'version':
                return row.config.version ?? '';
            case 'origin':
                return row.origin;
            case 'state':
                return row.status;
            default:
                return '';
        }
    };
    const filtered = $derived.by(() => {
        const q = search.trim().toLowerCase();
        const items = plugins.filter((row) =>
            (!updateFilter || updates[row.name]?.state === 'update_available') && (!originFilter || row.origin === originFilter) &&
            (!q || `${row.name} ${row.config.module_name} ${row.config.version}`.toLowerCase().includes(q))
        );
        return items.toSorted((a, b) => sortValue(a, sort.column).localeCompare(sortValue(b, sort.column), undefined, {numeric: true}) * (sort.direction === 'desc' ? -1 : 1));
    });
    const pageRows = $derived(filtered.slice((page - 1) * pageSize, page * pageSize));
    const hasFilters = $derived(!!(search || originFilter || updateFilter));

    async function checkUpdates() {
        if (checking) return;
        checking = true;
        updateError = '';
        try {
            await syncCatalog();
            await load();
        } catch (e) {
            await load();
            updateError = errorMessage(e, p('checksUnavailable'));
        } finally {
            checking = false;
        }
    }

    async function load() {
        const own = ++generation;
        controller.abort();
        controller = new AbortController();
        const signal = controller.signal;
        loading = true;
        error = '';
        updateError = '';
        const [list, mws] = await Promise.allSettled([listPlugins(signal), listMiddlewareRefs(signal)]);
        if (disposed || own !== generation) return;
        if (list.status === 'fulfilled') {
            plugins = list.value;
            page = 1;
        } else {
            plugins = [];
            error = errorMessage(list.reason, p('loadFailed'));
        }
        // Usage links are context: without them removal is still checked by the server.
        middlewaresFailed = mws.status !== 'fulfilled';
        middlewares = mws.status === 'fulfilled' ? mws.value : [];
        loading = false;
    }

    onMount(() => {
        void load();
    });
    onDestroy(() => {
        disposed = true;
        controller.abort();
    });

    function remove(row: PluginResource) {
        const blocking = users(row.name);
        if (blocking.length) {
            actionError = p('inUse', {name: row.name, users: blocking.map((x) => x.name).join(', ')});
            return;
        }
        actionError = '';
        pendingDelete = row;
    }

    async function confirmDelete() {
        const target = pendingDelete;
        if (!target || deleting) return;
        deleting = true;
        actionError = '';
        try {
            const res = await deletePlugin(target.name, plugins.find((x) => x.name === target.name)?.version ?? 0);
            if (needsManualRestart(res)) restartNotice = true;
            pendingDelete = null;
            await load();
        } catch (e) {
            pendingDelete = null;
            actionError = e instanceof ApiError && e.status === 409 ? p('conflict') + (e.message ? ' ' + e.message : '') : errorMessage(e, p('deleteFailed'));
        } finally {
            deleting = false;
        }
    }

    const canChange = (r: PluginResource) => !r.read_only;
    const open = (r: PluginResource, suffix = '') => navigate('/plugins/' + encodeURIComponent(r.name) + suffix);
</script>

<PageHeader title={p('title')} subtitle={p('subtitle')}/>

<div class="notices">
    {#if actionError}
        <Notice tone="error" message={actionError} dismissible onDismiss={() => (actionError = '')}/>
    {/if}
    {#if restartNotice}
        <Notice tone="warning" message={t('writeV2.manualRestart')} dismissible onDismiss={() => (restartNotice = false)}/>
    {/if}
    {#if error }
        <Notice tone="error" message={error}/>
    {/if}
    {#if middlewaresFailed && !loading }
        <Notice tone="info" message={p('usageUnavailable')}/>
    {/if}
    {#if updateError}
        <Notice tone="warning" message={updateError}/>
    {:else if stale}
        <Notice tone="warning" message={p('checksStale')}/>
    {/if}
</div>


<DataTable
        {columns}
        rows={pageRows}
        getRowId={(r: PluginResource) => r.name}
        searchValue={search}
        onsearch={(v: string) => { search = v; page = 1; }}
        searchPlaceholder={p('search')}
        {filters}
        filterValues={{ origin: originFilter, update: updateFilter }}
        onfilterchange={(id: string, v: string) => { if (id === 'origin') originFilter = v; else if (id === 'update') updateFilter = v; page = 1; }}
        {sort}
        onsort={(column: string) => { sort = { column, direction: sort.column === column && sort.direction === 'asc' ? 'desc' : 'asc' }; page = 1; }}
        {page}
        {pageSize}
        total={filtered.length}
        onpage={(v: number) => (page = v)}
        onpagesize={(v: number) => { pageSize = v; page = 1; }}
        {loading}
        error={!!error}
        onretry={load}
        regionLabel={p('title')}
        minWidth={1180}
>
    {#snippet toolbarActions()}
        <Button onclick={checkUpdates} pending={checking} disabled={checking}>{p('checkUpdates')}</Button>
        <Button variant="primary" onclick={() => navigate('/plugins/new')}>{p('install')}</Button>
    {/snippet}

    {#snippet rowSnippet(row: PluginResource)}
        {@const used = users(row.name)}
        {@const check = updates[row.name]}
        <td>
            <ValueTooltip text={row.name} truncate onactivate={() => open(row)}><strong>{row.name}</strong>
            </ValueTooltip>
        </td>
        <td>
            <ValueTooltip text={row.config.module_name} truncate>{row.config.module_name || '—'}</ValueTooltip>
        </td>
        <td>
            <div class="version">
                {#if check?.state === 'update_available'}
                    <span class="current outdated">{row.config.version || '—'}</span>
                    <ArrowRight size={14} aria-hidden="true" class="version-arrow"/>
                    <span class="latest">{check.latest_version}</span>
                    <span class="sr-only">{p('updateTo', { version: check.latest_version ?? '' })}</span>
                {:else if check?.state === 'up_to_date'}
                    <span class="current">{row.config.version || '—'}</span>
                    <span class="ok" title={p('checkState.up_to_date')}><CircleCheck size={16} aria-hidden="true"/><span class="sr-only">{p('checkState.up_to_date')}</span></span>
                {:else}
                    <span class="current">{row.config.version || '—'}</span>
                    {#if check}
                        <span class="state {check.state}" title={p('checkState.' + check.state)}>
                            {#if check.state === 'not_in_catalog'}<CircleHelp size={16} aria-hidden="true"/>{:else}<CircleAlert size={16} aria-hidden="true"/>{/if}
                            <span class="sr-only">{p('checkState.' + check.state)}</span>
                        </span>
                    {/if}
                {/if}
            </div>
        </td>
        <td>
            <StatusBadge size="sm" tone={row.origin === 'external' ? 'info' : 'neutral'}
                         text={row.origin === 'external' ? ru('externalShort') : ru('managed')}/>
        </td>
        <td>
        </td>
        <td>
            <ReferenceCount count={used.length}
                            onclick={() => (usage = { name: row.name, items: middlewares.filter((m) => used.some((u) => u.name === m.name)).map(middlewareItem) })}/>
        </td>
        <td>
            <RowActions label={row.name} actions={[
                !!updates[row.name]?.can_update && canChange(row) && { label: p('update'), variant: 'primary', onclick: () => open(row, '/update') },
                { label: canChange(row) ? p('edit') : ru('view'), onclick: () => open(row, canChange(row) ? '/edit' : '') },
                canChange(row) && { label: p('remove'), variant: 'danger', onclick: () => remove(row) }
            ]} />
        </td>
    {/snippet}

    {#snippet emptyState()}
        <h2>{p(hasFilters ? 'noMatches' : 'empty')}</h2>
        <p>{p(hasFilters ? 'adjustFilters' : 'emptyHelp')}</p>
        <Button onclick={() => { if (hasFilters) { search = ''; originFilter = '';updateFilter=''; page = 1; } else navigate('/plugins/new'); }}>{hasFilters ? ru('clearFilters') : p('install')}</Button>
    {/snippet}
</DataTable>
{#if checkedAt}
    <p class="scope-note">{p('updatesChecked')}
        <RelativeTime value={checkedAt.replace(' ', 'T') + 'Z'}/>
    </p>
{/if}

<ConfirmDialog
        open={!!pendingDelete}
        danger
        title={p('removeTitle')}
        message={pendingDelete ? p('removeMessage', { name: pendingDelete.name }) : ''}
        cancelLabel={t('common.cancel')}
        confirmLabel={p('removeConfirm')}
        oncancel={() => { if (!deleting) pendingDelete = null; }}
        onconfirm={confirmDelete}
/>

{#if usage}
    <RelatedItemsDialog title={p('usedBy') + ' · ' + usage.name} description={p('usageHelp')} items={usage.items}
                        {navigate} onclose={() => (usage = null)}/>
{/if}

{#if mode !== 'list'}
    {#key `${mode}:${name}:${module}`}
        <PluginDialog {mode} {name} {module} knownPlugins={loading || error ? undefined : plugins}
                      knownMiddlewares={loading || middlewaresFailed ? undefined : middlewares} {navigate}
                      onChanged={load}/>
    {/key}
{/if}

<style>


    .notices {
        display: flex;
        flex-direction: column;
        gap: 12px;
        margin-bottom: 24px;
    }

    .notices:empty {
        display: none;
    }

    .version {
        display: flex;
        flex-wrap: wrap;
        align-items: center;
        gap: 4px 6px;
        font-variant-numeric: tabular-nums;
    }

    .version .outdated {
        color: var(--warning-text);
        font-weight: 500;
    }

    .version .latest {
        color: var(--success-text);
        font-weight: 600;
    }

    .version :global(.version-arrow) {
        color: var(--text-muted);
        flex-shrink: 0;
    }

    .version .ok {
        display: inline-flex;
        color: var(--success-text);
    }

    .version .state {
        display: inline-flex;
        color: var(--text-muted);
    }

    .version .state.unknown {
        color: var(--warning-text);
    }

    .sr-only {
        position: absolute;
        width: 1px;
        height: 1px;
        overflow: hidden;
        clip: rect(0 0 0 0);
        white-space: nowrap;
    }

    .module {
        display: block;
        margin-top: 2px;
        color: var(--text-muted);
        font-size: 12px;
        overflow-wrap: anywhere;
    }

    .scope-note {
        margin-top: 20px;
        color: var(--text-muted);
        font-size: 14px;
        line-height: 21px;
    }

</style>

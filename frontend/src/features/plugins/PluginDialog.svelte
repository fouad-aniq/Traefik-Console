<script lang="ts">
    /**
     * Install / update / view a Traefik plugin declaration against core-v2.
     *
     *   create  → source "module" (alias + module + version) or "snippet" (a pasted Plugin Shop YAML,
     *             optionally creating its middleware); from the catalog the module and version are prefilled
     *   edit    → only the version changes (alias and module are the plugin's identity)
     *   upgrade → POST /plugins/{alias}/update: the server moves a managed plugin to the newest cataloged version
     *   review  → PUT /plugins/{alias} | POST /plugins/yaml with ?dry_run=true (If-Match) previews; the same call without dry_run applies
     *   view    → read-only (external declarations in traefik.yml)
     */
    import {onMount, onDestroy} from 'svelte';
    import {Puzzle, ExternalLink} from '@lucide/svelte';
    import ResourceDialog from '../../shared/ui/ResourceDialog.svelte';
    import SearchableSelect from '../../shared/ui/SearchableSelect.svelte';
    import Checkbox from '../../shared/ui/Checkbox.svelte';
    import Field from '../../shared/ui/Field.svelte';
    import Tabs from '../../shared/ui/Tabs.svelte';
    import Button from '../../shared/ui/Button.svelte';
    import Notice from '../../shared/ui/Notice.svelte';
    import SummaryList from '../../shared/ui/SummaryList.svelte';
    import CodePreview from '../../shared/ui/CodePreview.svelte';
    import StatusBadge from '../../shared/ui/StatusBadge.svelte';
    import YamlCodeEditor from '../../shared/ui/YamlCodeEditor.svelte';
    import {navigationGuard} from '../../lib/stores/navigationGuard';
    import {needsManualRestart, newIdempotencyKey, operationOf, announceApplied, type WriteOptions} from '../../core/mutations';
    import {operationTone} from '../changes/models';
    import type {Operation, OperationPreview} from '../../core/types';
    import {
        installPluginYaml,
        listMiddlewareRefs,
        listPlugins,
        putPlugin,
        type MiddlewareRef,
        readCatalogPage,
        syncCatalog,
        updatePlugin,
        installFromCatalog
    } from './api';
    import {
        PLUGIN_REFUSALS,
        compareVersions,
        formFrom,
        moduleLink,
        newForm,
        parseSnippet,
        pluginErrors,
        suggestAlias,
        availableAlias,
        moduleKey,
        knownVersions,
        stableVersion,
        toInput,
        type Messages,
        type PluginForm,
        type PluginResource,
        type CatalogPlugin,
        type PluginUpdateInfo
    } from './models';
    import {ApiError, NetworkError} from '../../shared/api/client';
    import {errorMessage} from '../../shared/api/errorMessage';
    import {t} from '../../shared/localization/i18n';

    /** knownPlugins / knownMiddlewares: what the page already loaded, so opening the dialog does not read them again. */
    let {mode = 'view', name = '', module = '', knownPlugins, knownMiddlewares, navigate, onChanged}: {
        mode?: string;
        name?: string;
        module?: string;
        knownPlugins?: PluginResource[];
        knownMiddlewares?: MiddlewareRef[];
        navigate: (p: string) => void;
		onChanged?: () => void
    } = $props();

    const p = (key: string, args?: Record<string, string | number>) => t('pluginsV2.' + key, args);
    const rv2 = (key: string, args?: Record<string, string | number>) => t('resolverV2.' + key, args);
    const ru = (key: string, args?: Record<string, string | number>) => t('resolverUI.' + key, args);

    const messages: Messages = {
        moduleInstalled: p('error.moduleInstalled'),
        nameRequired: p('error.nameRequired'),
        invalidName: p('error.invalidName'),
        nameTaken: p('error.nameTaken'),
        moduleRequired: p('error.moduleRequired'),
        invalidModule: p('error.invalidModule'),
        versionRequired: p('error.versionRequired'),
        invalidVersion: p('error.invalidVersion'),
        snippetRequired: p('error.snippetRequired'),
        snippetYaml: p('error.snippetYaml'),
        snippetShape: p('error.snippetShape'),
        snippetMultiple: p('error.snippetMultiple'),
        snippetFields: p('error.snippetFields'),
        snippetInvalid: p('error.snippetInvalid'),
        middlewareTaken: p('error.middlewareTaken'),
        invalidMiddlewareName: p('error.invalidMiddlewareName')
    };

    let form = $state<PluginForm>(newForm());
    let current = $state<PluginResource | null>(null);
    let names = $state<string[]>([]);
    let installedModules = $state<Record<string, string>>({});
    let target = $state<PluginUpdateInfo | null>(null);
    let choice = $state<'catalog' | 'form' | 'yaml'>('catalog');
    let catalogItems = $state<CatalogPlugin[]>([]);
    let catalogLoading = $state(false);
    let catalogError = $state('');
    let inventoryKnown = $state(true);
    let syncing = $state(false);
    let prereleases = $state(false);
    let manualTag = $state(false);
    let middlewareNames = $state<string[]>([]);
    let catalogEntry = $state<CatalogPlugin | null>(null);
    let loading = $state(true);
    let loadError = $state('');
    let busy = $state(false);
    let suspended = $state(false);
    let error = $state('');
    let step = $state<'form' | 'review'>('form');
    let submitted = $state(false);
    let baseline = $state('');

    let conflict = $state(false);
    let staged = $state(false);
    let review = $state<OperationPreview | null>(null);
    let reviewError = $state('');
    let applyKey = $state('');
    let uncertain = $state(false);
    let result = $state<Operation | null>(null);
    let manualRestart = $state(false);

    let disposed = false;
    const controller = new AbortController();

    const creating = $derived(mode === 'create');
    const upgrading = $derived(mode === 'upgrade');
    const catalogMode = $derived(creating && choice === 'catalog');
    const fromCatalog = $derived(catalogMode && !!catalogEntry);
    const editing = $derived((mode === 'create' || mode === 'edit' || mode === 'upgrade') && (!current || (!current.read_only)));
    const snippet = $derived(creating && form.source === 'snippet' ? parseSnippet(form.snippet) : null);
    const errors = $derived(pluginErrors(form, {
        creating,
        existingNames: names,
        existingMiddlewares: middlewareNames,
        installedModules
    }, messages));
    const shown = $derived(submitted ? errors : ({} as Record<string, string>));
    const dirty = $derived(editing && !!baseline && JSON.stringify($state.snapshot(form)) !== baseline && !result);
    const locked = $derived(busy || !!result);
    const serverErrors = $derived(review?.validation_errors ?? []);
    const alias = $derived(snippet?.ok ? form.name.trim() || snippet.alias : form.name.trim());
    const versionBefore = $derived((mode === 'edit' || mode === 'upgrade' ? current?.config.version : '') ?? '');

    $effect(() => {
        navigationGuard.setDirty(dirty);
        return () => navigationGuard.setDirty(false);
    });

    onMount(() => {
        const unsub = navigationGuard.subscribe(() => (suspended = navigationGuard.confirmPending));
        void load();
        return unsub;
    });
    onDestroy(() => {
        disposed = true;
        controller.abort();
    });

    async function load() {
        loading = true;
        loadError = '';
        const signal = controller.signal;
        try {
            const [all, mws] = await Promise.all([
                knownPlugins ? Promise.resolve(knownPlugins) : listPlugins(signal),
                creating ? (knownMiddlewares ? Promise.resolve(knownMiddlewares) : listMiddlewareRefs(signal)) : Promise.resolve([])
            ]);
            if (disposed) return;
            names = all.map((x) => x.name);
            installedModules = Object.fromEntries(all.flatMap(x => [x.config.module_name].filter((v): v is string => !!v).map(v => [moduleKey(v), x.name])));
            middlewareNames = mws.map((x) => x.name);
            if (!creating) {
                const found = all.find((x) => x.name === name) ?? null;
                if (!found) {
                    loadError = p('notFound');
                    return;
                }
                current = found;
                form = formFrom(found);
                if (mode === 'upgrade') {
                    target = found.update ?? null;
                    if (target?.can_update && target.latest_version) form.version = target.latest_version;
                }
            } else if (module) {
                // Arriving from the catalog: take the module and its latest version from the cached entry.
                const entry = (await readCatalogPage({
                    module,
                    limit: 1,
                    offset: 0
                }, signal)).items.find((x) => x.module_name === module);
                if (!entry) throw new Error(p('catalogEntryMissing'));
                catalogEntry = entry;
                if (disposed) return;
                form.module = module;
                form.version = stableVersion(entry);
                form.name = availableAlias(module, names);
                if (!catalogItems.some((x) => x.module_name === entry.module_name)) catalogItems = [entry, ...catalogItems];
            }
            if (creating && !module) void loadCatalogOptions();
            baseline = JSON.stringify($state.snapshot(form));
        } catch (e) {
            if (!disposed) loadError = errorMessage(e, p('loadFailed'));
        } finally {
            if (!disposed) loading = false;
        }
    }

    /** Every available middleware plugin, read page by page (the server caps a page at 100). */
    async function loadCatalogOptions() {
        if (catalogLoading) return;
        catalogLoading = true;
        catalogError = '';
        try {
            const all: CatalogPlugin[] = [];
            for (let offset = 0; offset < 2000; offset += 100) {
                const data = await readCatalogPage({limit: 100, offset, available: true}, controller.signal);
                if (disposed) return;
                inventoryKnown = data.inventory_known !== false;
                all.push(...(data.items ?? []));
                if (!inventoryKnown || offset + 100 >= data.total) break;
            }
            catalogItems = all.filter((x) => x.plugin_type === 'middleware');
        } catch (e) {
            if (!disposed) catalogError = errorMessage(e, p('catalogFailed'));
        } finally {
            if (!disposed) catalogLoading = false;
        }
    }

    async function refreshCatalog() {
        if (syncing) return;
        syncing = true;
        catalogError = '';
        try {
            await syncCatalog(controller.signal);
            await loadCatalogOptions();
        } catch {
            if (!disposed) catalogError = p('syncFailed');
        } finally {
            if (!disposed) syncing = false;
        }
    }

    /** Picking a plugin fills the identity and preselects its newest stable version. */
    function pickPlugin(moduleName: string) {
        const entry = catalogItems.find((x) => x.module_name === moduleName);
        if (!entry) return;
        catalogEntry = entry;
        manualTag = false;
        prereleases = false;
        form = {
            ...newForm(),
            name: availableAlias(entry.module_name, names),
            module: entry.module_name,
            version: stableVersion(entry)
        };
        invalidate();
    }

    // Same tab strip as the other dialogs: the plugin form, then the review.
    const tabs = $derived([
        {id: 'plugin-form', label: p('tabPlugin')},
        {id: 'plugin-review', label: p('tabReview')}
    ]);

    async function selectStep(id: string) {
        if (busy || result) return;
        if (id === 'plugin-review') { if (step !== 'review') await stageAndReview(); return; }
        step = 'form';
    }

    const sourceHelp = $derived(p(choice === 'catalog' ? 'fromCatalogHelp' : choice === 'form' ? 'optionFormHelp' : 'source.snippetHelp'));

    function chooseSource(v: string) {
        choice = v as typeof choice;
        form.source = choice === 'yaml' ? 'snippet' : 'module';
        invalidate();
    }

    const versions = $derived.by(() => {
        if (upgrading) return (target?.versions ?? []).filter((v) => (compareVersions(v, current?.config.version ?? '') ?? 0) > 0);
        return fromCatalog && catalogEntry ? knownVersions(catalogEntry) : [];
    });
    const isPrerelease = (v: string) => v.split('+')[0].includes('-');
    const visibleVersions = $derived(versions.filter((v) => prereleases || !isPrerelease(v) || v === form.version));
    const canStage = $derived(!upgrading || (!!target?.can_update && visibleVersions.length > 0));

    function invalidate() {
        review = null;
        reviewError = '';
        staged = false;
        applyKey = '';
        uncertain = false;
        error = '';
        conflict = false;
    }

    function describeFailure(e: unknown, fallback: string) {
        if (e instanceof ApiError && PLUGIN_REFUSALS.includes(e.code ?? '')) return errorMessage(e, fallback);
        if (e instanceof ApiError && e.code === 'ERR_VERSION_CONFLICT') {
            conflict = true;
            return rv2('conflict');
        }
        const base = errorMessage(e, fallback);
        return e instanceof ApiError && e.operationId ? `${base} (${rv2('operation')} ${e.operationId})` : base;
    }

    async function stageAndReview() {
        if (busy || !editing) return;
        submitted = true;
        if (Object.keys(errors).length) return;
        if (fromCatalog && catalogEntry && !manualTag && !knownVersions(catalogEntry).includes(form.version)) {
            error = p('versionWithdrawn');
            return;
        }
        busy = true;
        error = '';
        conflict = false;
        try {
            const preview = await write({dryRun: true, signal: controller.signal});
            if (disposed) return;
            review = preview.preview ?? null;
            applyKey = newIdempotencyKey();
            step = 'review';
        } catch (e) {
            if (!disposed) error = describeFailure(e, p('stageFailed'));
        } finally {
            if (!disposed) busy = false;
        }
    }

    /** One request per source: update, catalog install, YAML snippet or a full PUT. Dry-run and save share it. */
    function write(opts: WriteOptions) {
        if (upgrading) return updatePlugin(form.name.trim(), form.version.trim(), current?.version ?? 0, opts);
        if (creating && fromCatalog && !manualTag && form.source === 'module') return installFromCatalog(form.name.trim(), toInput($state.snapshot(form)), opts);
        if (creating && form.source === 'snippet') {
            return installPluginYaml({
                raw_yaml: form.snippet,
                ...(form.name.trim() ? {short_name: form.name.trim()} : {}),
                ...(form.middlewareName.trim() ? {middleware_name: form.middlewareName.trim()} : {})
            }, opts);
        }
        return putPlugin(form.name.trim(), toInput($state.snapshot(form)), current?.version ?? 0, opts);
    }

    async function refreshVersion() {
        busy = true;
        try {
            const all = await listPlugins(controller.signal);
            current = all.find((x) => x.name === name) ?? current;
            conflict = false;
            error = '';
        } catch (e) {
            error = describeFailure(e, rv2('reviewFailed'));
        } finally {
            if (!disposed) busy = false;
        }
    }

    async function apply() {
        if (!review || busy || serverErrors.length) return;
        busy = true;
        error = '';
        try {
            const saved = await write({idempotencyKey: applyKey, signal: controller.signal});
            if (disposed) return;
            result = operationOf(saved, t('writeV2.noChange'));
            manualRestart = needsManualRestart(saved);
            uncertain = false;
            staged = true;
            navigationGuard.setDirty(false);
            onChanged?.();
            // Applied: back to the table; the outcome is announced as a flash.
            if (announceApplied(result, manualRestart, form.name)) { navigate('/plugins'); return; }
        } catch (e) {
            if (disposed) return;
            if (e instanceof NetworkError) {
                uncertain = true;
                error = rv2('applyUncertain');
            } else {
                if (e instanceof ApiError && [404, 409, 410, 412, 422, 428].includes(e.status)) {
                    review = null;
                    applyKey = '';
                    uncertain = false;
                    step = 'form';
                }
                error = describeFailure(e, rv2('applyFailed'));
            }
        } finally {
            if (!disposed) busy = false;
        }
    }

    function close() {
        if (busy) return;
        navigationGuard.requestNavigation(() => {
            if (staged || result) onChanged?.();
            navigate('/plugins');
        });
    }

    const link = $derived(moduleLink(form.module.trim()));
    const sourceOptions = $derived([
        {value: 'module', label: p('source.module'), description: p('source.moduleHelp')},
        {value: 'snippet', label: p('source.snippet'), description: p('source.snippetHelp')}
    ]);
</script>

{#snippet versionControl()}
    <Field id="plugin-version" label={p('version')} required
           help={shown.version ? '' : p(upgrading ? 'upgradeVersionHelp' : catalogMode && !manualTag ? 'knownVersionsHelp' : 'versionHelp')}
           error={shown.version}>
        {#snippet children(f)}
            {#if upgrading || (catalogMode && !manualTag)}
                <SearchableSelect id={f.id} value={form.version} options={visibleVersions.map((v) => ({ value: v, label: v }))}
                        onchange={(v) => { form.version = v; invalidate(); }} placeholder={p('selectVersion')} searchPlaceholder={p('selectVersion')}
                        disabled={locked || (catalogMode && !catalogEntry)} ariaInvalid={f.invalid} ariaDescribedby={f.describedBy} />
            {:else}
                <input id={f.id} bind:value={form.version} oninput={invalidate} disabled={locked}
                       aria-invalid={f.invalid || undefined} aria-describedby={f.describedBy} autocomplete="off"
                       spellcheck="false" placeholder="v1.2.0"/>
            {/if}
        {/snippet}
    </Field>
    {#if upgrading || (catalogMode && !manualTag)}
        <Checkbox label={p('includePrereleases')} checked={prereleases} disabled={locked}
                  onchange={(v) => (prereleases = v)}/>
    {/if}
    {#if catalogMode && catalogEntry}
        <Checkbox label={p('manualTag')} checked={manualTag} disabled={locked}
                  onchange={(v) => { manualTag = v; invalidate(); }}/>
        {#if manualTag}
            <Notice tone="warning" message={p('manualTagHelp')}/>
        {/if}
    {/if}
{/snippet}

<ResourceDialog
        title={p(mode === 'create' ? 'installTitle' : mode === 'edit' ? 'editTitle' : mode === 'upgrade' ? 'updateTitle' : 'detailsTitle')}
        description={upgrading ? p('upgradeDescription') : ''}
        closeLabel={t('common.close')}
        onclose={close}
        {suspended}
>
    {#snippet icon()}
        <Puzzle size={32} aria-hidden="true"/>
    {/snippet}

    {#snippet navigation()}
        {#if !loading && !loadError && editing && !result}
            <Tabs {tabs} selected={step === 'review' ? 'plugin-review' : 'plugin-form'} onselect={selectStep} label={p('tabsLabel')}/>
        {/if}
    {/snippet}

    {#if loading}
        <p role="status">{p('loading')}</p>
    {:else if loadError}
        <div class="stack">
            <Notice tone="error" message={loadError}/>
            <div class="row">
                <Button onclick={load}>{t('common.retry')}</Button>
                {#if creating && module}
                    <Button onclick={() => navigate('/plugins/new')}>{p('installModule')}</Button>
                {/if}
            </div>
        </div>
    {:else}
        <!-- One panel at a time; the other tab's panel id still exists for aria-controls. -->
        {#if editing && !result}<div id={step === 'review' ? 'panel-plugin-form' : 'panel-plugin-review'} role="tabpanel" hidden></div>{/if}
        <div class="stack" id={editing ? (step === 'review' ? 'panel-plugin-review' : 'panel-plugin-form') : undefined} role={editing ? 'tabpanel' : undefined} aria-labelledby={editing ? (step === 'review' ? 'tab-plugin-review' : 'tab-plugin-form') : undefined}>
            {#if error}
                <Notice tone="error" message={error}>
                    {#if conflict}
                        <Button size="sm" onclick={refreshVersion} disabled={busy}>{rv2('refreshDraft')}</Button>
                    {/if}
                    {#if uncertain}
                        <Button size="sm" onclick={apply} disabled={busy}>{t('domainUI.retryApply')}</Button>
                    {/if}
                </Notice>
            {/if}

            {#if editing && step === 'form'}
                {#if upgrading}
                    <SummaryList>
                        <div>
                            <dt>{p('alias')}</dt>
                            <dd>{form.name}</dd>
                        </div>
                        <div>
                            <dt>{p('module')}</dt>
                            <dd>{form.module}</dd>
                        </div>
                        <div>
                            <dt>{p('currentVersion')}</dt>
                            <dd>{current?.config.version}</dd>
                        </div>
                    </SummaryList>
                    {#if target?.can_update && visibleVersions.length}
                        {@render versionControl()}
                    {:else}
                        <Notice tone="info" message={p(target ? 'upgradeNone' : 'upgradeUnknown')}/>
                    {/if}
                    {#if link}<a class="module-link" href={link} target="_blank" rel="noopener noreferrer">
                        <ExternalLink size={14} aria-hidden="true"/>{p('sourceLink')}</a>{/if}
                {:else}
                    {#if creating}
                        <Field id="plugin-source" label={p('addFrom')} help={sourceHelp}>
                            {#snippet children(f)}
                                <SearchableSelect id={f.id} value={choice} disabled={locked} onchange={chooseSource}
                                                  ariaDescribedby={f.describedBy}
                                                  options={[{ value: 'catalog', label: p('fromCatalog') }, { value: 'form', label: p('optionForm') }, { value: 'yaml', label: p('optionYaml') }]}/>
                            {/snippet}
                        </Field>
                    {/if}
                    {#if creating && choice === 'catalog'}
                        {#if !inventoryKnown}
                            <Notice tone="warning" message={p('inventoryUnavailable')}>
                                <Button size="sm" onclick={loadCatalogOptions}>{t('common.retry')}</Button>
                            </Notice>
                        {:else if catalogError}
                            <Notice tone="error" message={catalogError}>
                                <Button size="sm" onclick={loadCatalogOptions}>{t('common.retry')}</Button>
                            </Notice>
                        {/if}
                    {/if}
                    {#if !creating || choice !== 'yaml'}
                        <Field id="plugin-name" label={p('alias')} required help={shown.name ? '' : p('aliasHelp')}
                               error={shown.name}>
                            {#snippet children(f)}
                                <input id={f.id} bind:value={form.name} oninput={invalidate} readonly={!creating}
                                       disabled={locked} aria-invalid={f.invalid || undefined}
                                       aria-describedby={f.describedBy} autocomplete="off" spellcheck="false"
                                       placeholder="crowdsec-bouncer"/>
                            {/snippet}
                        </Field>
                        <Field id="plugin-module" label={p('module')} required
                               help={shown.module ? '' : p(catalogMode ? 'pluginFieldHelp' : 'moduleHelp')}
                               error={shown.module}>
                            {#snippet children(f)}
                                {#if catalogMode}
                                    {#if catalogLoading}
                                        <p class="muted" role="status">{p('loading')}</p>
                                    {:else}
                                        <SearchableSelect id={f.id} value={catalogEntry?.module_name ?? ''}
                                                          options={catalogItems.map((x) => ({ value: x.module_name, label: x.display_name || x.module_name, description: x.summary || x.module_name }))}
                                                          placeholder={p('pluginPlaceholder')}
                                                          searchPlaceholder={p('searchCatalog')}
                                                          emptyMessage={p('catalogNoMatches')}
                                                          disabled={locked || !inventoryKnown} ariaInvalid={f.invalid}
                                                          ariaDescribedby={f.describedBy} onchange={pickPlugin}/>
                                    {/if}
                                {:else}
                                    <input id={f.id} bind:value={form.module} oninput={invalidate} readonly={!creating}
                                           disabled={locked} aria-invalid={f.invalid || undefined}
                                           aria-describedby={f.describedBy} autocomplete="off" spellcheck="false"
                                           placeholder="github.com/org/plugin"/>
                                {/if}
                            {/snippet}
                        </Field>
                        {#if catalogMode && !catalogLoading && !catalogItems.length && inventoryKnown && !catalogError}
                            <div class="row">
                                <Button size="sm" onclick={refreshCatalog} pending={syncing}
                                        disabled={syncing}>{syncing ? p('syncing') : p('refreshCatalog')}</Button>
                            </div>
                        {/if}
                        {#if catalogMode}
                            {@render versionControl()}
                        {:else}
                            <Field id="plugin-version" label={p('version')} required
                                   help={shown.version ? '' : p('versionHelp')} error={shown.version}>
                                {#snippet children(f)}
                                    <input id={f.id} bind:value={form.version} oninput={invalidate} disabled={locked}
                                           aria-invalid={f.invalid || undefined} aria-describedby={f.describedBy}
                                           autocomplete="off" spellcheck="false" placeholder="v1.2.0"/>
                                {/snippet}
                            </Field>
                        {/if}
                        {#if link}<a class="module-link" href={link} target="_blank" rel="noopener noreferrer">
                            <ExternalLink size={14} aria-hidden="true"/>{p('sourceLink')}</a>{/if}
                    {:else}
                        <Field id="plugin-snippet" label={p('snippet')} required
                               help={shown.snippet ? '' : p('snippetHelp')} error={shown.snippet}>
                            {#snippet children(f)}
                                <YamlCodeEditor id={f.id} label={p('snippet')} bind:value={form.snippet}
                                                oninput={invalidate} disabled={locked} invalid={!!shown.snippet}
                                                describedBy={f.describedBy}
                                                placeholder={'experimental:\n  plugins:\n    example:\n      moduleName: github.com/org/plugin\n      version: v1.0.0'}/>
                            {/snippet}
                        </Field>
                        {#if snippet?.ok}
                            <SummaryList>
                                <div>
                                    <dt>{p('module')}</dt>
                                    <dd>{snippet.module}</dd>
                                </div>
                                <div>
                                    <dt>{p('version')}</dt>
                                    <dd>{snippet.version}</dd>
                                </div>
                                {#if snippet.middleware}
                                    <div>
                                        <dt>{p('middleware')}</dt>
                                        <dd>{snippet.middleware}</dd>
                                    </div>
                                {/if}
                            </SummaryList>
                            <Field id="plugin-name" label={p('alias')} required={snippet.needsAlias}
                                   help={shown.name ? '' : p(snippet.needsAlias ? 'aliasNeeded' : 'aliasOptional', { alias: snippet.alias })}
                                   error={shown.name}>
                                {#snippet children(f)}
                                    <input id={f.id} bind:value={form.name} oninput={invalidate} disabled={locked}
                                           aria-invalid={f.invalid || undefined} aria-describedby={f.describedBy}
                                           autocomplete="off" spellcheck="false" placeholder={snippet.alias}/>
                                {/snippet}
                            </Field>
                            {#if snippet.middleware}
                                <Field id="plugin-middleware" label={p('middlewareName')}
                                       help={shown.middlewareName ? '' : p('middlewareHelp', { name: snippet.middleware })}
                                       error={shown.middlewareName}>
                                    {#snippet children(f)}
                                        <input id={f.id} bind:value={form.middlewareName} oninput={invalidate}
                                               disabled={locked} aria-invalid={f.invalid || undefined}
                                               aria-describedby={f.describedBy} autocomplete="off" spellcheck="false"
                                               placeholder={snippet.middleware}/>
                                    {/snippet}
                                </Field>
                            {/if}
                        {/if}
                    {/if}
                {/if}
                <Notice tone="warning" title={p('trustTitle')} message={p('trustHelp')}/>
            {:else if editing && step === 'review'}
                {#if result}
                    <Notice tone={operationTone(result.status)} title={t('operationV2.status.' + result.status)}
                            message={result.summary}>
                        {#if result.id}<span class="muted">{t('operationV2.id', {id: result.id})}</span>{/if}
                    </Notice>
                    {#if manualRestart}<Notice tone="warning" message={t('writeV2.manualRestart')}/>{/if}
                    {#if result.status === 'succeeded'}
                        <Button onclick={() => navigate('/middlewares/new?plugin='+encodeURIComponent(alias))}>{p('createMiddleware')}</Button>
                    {/if}
                {:else if review}
                    <SummaryList>
                        <div>
                            <dt>{p('alias')}</dt>
                            <dd>{alias}</dd>
                        </div>
                        <div>
                            <dt>{p('module')}</dt>
                            <dd>{snippet?.ok ? snippet.module : form.module}</dd>
                        </div>
                        {#if mode !== 'create' && versionBefore && versionBefore !== form.version.trim()}
                            <div>
                                <dt>{p('version')}</dt>
                                <dd>{p('versionChange', {from: versionBefore, to: form.version.trim()})}</dd>
                            </div>
                        {:else}
                            <div>
                                <dt>{p('version')}</dt>
                                <dd>{snippet?.ok ? snippet.version : form.version}</dd>
                            </div>
                        {/if}
                        {#if snippet?.ok && snippet.middleware}
                            <div>
                                <dt>{p('middleware')}</dt>
                                <dd>{form.middlewareName.trim() || snippet.middleware}</dd>
                            </div>
                        {/if}
                        <div>
                            <dt>{rv2('pendingTotal')}</dt>
                            <dd>{review.changes?.length ?? 0}</dd>
                        </div>
                    </SummaryList>
                    {#if serverErrors.length}
                        <Notice tone="error" title={rv2('serverErrors')}>
                            <ul class="reasons">
                                {#each serverErrors as ve}
                                    <li>{ve.item_id}{ve.field ? ' · ' + ve.field : ''}: {ve.message}</li>
                                {/each}
                            </ul>
                        </Notice>
                    {/if}
                    {#if review.requires_restart}
                        <Notice tone="warning" title={ru('restartRequired')} message={p('restartHelp')}>
                            {#if review.restart_reasons?.length}
                                <ul class="reasons">
                                    {#each review.restart_reasons as reason}
                                        <li>{reason}</li>
                                    {/each}
                                </ul>
                            {/if}
                        </Notice>
                    {/if}
                    {#if review.static_diff}
                        <CodePreview label={rv2('diff.static')} code={review.static_diff} language="diff"/>
                    {/if}
                    {#if review.dynamic_diff}
                        <CodePreview label={rv2('diff.dynamic')} code={review.dynamic_diff} language="diff"/>
                    {/if}
                    {#if !review.static_diff && !review.dynamic_diff}<p class="muted">{rv2('diff.none')}</p>{/if}
                {:else if reviewError}
                    <Notice tone="error" message={reviewError}>
                        <Button size="sm" onclick={stageAndReview}>{t('common.retry')}</Button>
                    </Notice>
                {:else}
                    <p role="status">{rv2('staging')}</p>
                {/if}
            {:else if current}
                {#if current.origin === 'external'}
                    <Notice message={p('externalHelp')}/>
                {/if}
                <SummaryList>
                    <div>
                        <dt>{p('alias')}</dt>
                        <dd>{current.name}</dd>
                    </div>
                    <div>
                        <dt>{p('module')}</dt>
                        <dd>{current.config.module_name || '—'}</dd>
                    </div>
                    <div>
                        <dt>{p('version')}</dt>
                        <dd>{current.config.version || '—'}</dd>
                    </div>
                    <div>
                        <dt>{ru('ownership')}</dt>
                        <dd>{current.origin === 'external' ? ru('external') : ru('managed')}</dd>
                    </div>
                </SummaryList>
                {#if moduleLink(current.config.module_name)}<a class="module-link"
                                                               href={moduleLink(current.config.module_name)}
                                                               target="_blank" rel="noopener noreferrer">
                    <ExternalLink size={14} aria-hidden="true"/>{p('sourceLink')}</a>{/if}
            {/if}
        </div>
    {/if}

    {#snippet footer()}
        <Button onclick={close} disabled={busy}>{result ? t('common.close') : ru('cancel')}</Button>
        {#if !loading && !loadError && !result}
            {#if editing}
                {#if step === 'review'}
                    <Button onclick={() => (step = 'form')} disabled={busy}>{ru('back')}</Button>
                    {#if review}
                        <Button variant="primary" onclick={apply} pending={busy}
                                disabled={busy || serverErrors.length > 0}>{ru(busy ? 'applying' : uncertain ? 'retryApply' : 'apply')}</Button>
                    {/if}
                {:else}
                    <Button variant="primary" onclick={stageAndReview} pending={busy}
                            disabled={busy || !canStage}>{rv2('stageReview')}</Button>
                {/if}
            {:else if current && !current.read_only}
                <Button variant="primary"
                        onclick={() => navigate('/plugins/' + encodeURIComponent(current!.name) + '/edit')}>{p('edit')}</Button>
            {/if}
        {/if}
    {/snippet}
</ResourceDialog>

<style>
    .stack {
        display: flex;
        flex-direction: column;
        gap: 24px;
    }

    .row {
        display: flex;
        gap: 12px;
    }

    .muted {
        margin: 0;
        color: var(--text-muted);
        font-size: 14px;
        line-height: 21px;
    }

    .reasons {
        margin: 8px 0 0;
        padding-left: 20px;
        font-size: 14px;
        line-height: 22px;
    }

    .module-link {
        display: inline-flex;
        align-items: center;
        align-self: flex-start;
        gap: 6px;
        font-size: 13px;
        line-height: 20px;
        color: var(--text-muted);
        text-decoration: none;
    }

    .module-link:hover {
        color: var(--connection);
        text-decoration: underline;
    }
</style>

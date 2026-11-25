<script lang="ts">
	/**
	 * Middleware chain dialog (create / edit / view) on the direct write protocol (dry-run preview, then apply).
	 * A chain is an ordered list of middleware references: PUT /chains/{name} {middlewares}.
	 */
	import { onMount, onDestroy, tick } from 'svelte';
	import { stringify } from 'yaml';
	import { Link2, ArrowUp, ArrowDown, Trash2, CircleAlert } from '@lucide/svelte';
	import ResourceDialog from '../../shared/ui/ResourceDialog.svelte';
	import Field from '../../shared/ui/Field.svelte';
	import Tabs from '../../shared/ui/Tabs.svelte';
	import Button from '../../shared/ui/Button.svelte';
	import Notice from '../../shared/ui/Notice.svelte';
	import SummaryList from '../../shared/ui/SummaryList.svelte';
	import CodePreview from '../../shared/ui/CodePreview.svelte';
	import RelativeTime from '../../shared/ui/RelativeTime.svelte';
	import SearchableSelect from '../../shared/ui/SearchableSelect.svelte';
	import { navigationGuard } from '../../lib/stores/navigationGuard';
	import { needsManualRestart, newIdempotencyKey, operationOf, announceApplied } from '../../core/mutations';
	import { operationTone } from '../changes/models';
	import type { Operation, OperationPreview } from '../../core/types';
	import { listChains, listMiddlewares, putChain } from './api';
	import { MAX_CHAIN_MEMBERS, chainMembers, isValidMiddlewareName, middlewareProtocolOf, referenceBaseName, type ChainResource, type MiddlewareResource } from './models';
	import { ApiError, NetworkError } from '../../shared/api/client';
	import { errorMessage } from '../../shared/api/errorMessage';
	import { t } from '../../shared/localization/i18n';

	let { mode = 'create', name = '', navigate, onchanged }: { mode?: string; name?: string; navigate: (p: string) => void; onchanged?: () => void } = $props();

	const m = (key: string, args?: Record<string, string | number>) => t('middlewares.' + key, args);
	const mv = (key: string, args?: Record<string, string | number>) => t('middlewareV2.' + key, args);
	const rv2 = (key: string, args?: Record<string, string | number>) => t('resolverV2.' + key, args);

	const creating = $derived(mode === 'create');
	const viewing = $derived(mode === 'view');
	const parent = '/chains';

	let chains = $state<ChainResource[]>([]);
	let middlewares = $state<MiddlewareResource[]>([]);
	let inventoryError = $state('');
	let loading = $state(true);
	let loadError = $state('');
	let busy = $state(false);
	let suspended = $state(false);
	let error = $state('');
	let step = $state('chain-members');
	let baseline = $state('');
	let submitted = $state(false);
	let fields = $state<Record<string, string>>({});

	let chainName = $state('');
	let members = $state<string[]>([]);
	let memberToAdd = $state('');

	let conflict = $state(false);
	let staged = $state<ChainResource | null>(null);
	let review = $state<OperationPreview | null>(null);
	let reviewError = $state('');
	let applyKey = $state('');
	let uncertain = $state(false);
	let result = $state<Operation | null>(null);
	let manualRestart = $state(false);

	let disposed = false;
	const controller = new AbortController();

	const current = $derived(chains.find((c) => c.name === name));
	const version = () => current?.version ?? 0;
	const editable = $derived(!viewing && (creating || (!!current && !current.read_only)));
	const locked = $derived(busy || !!result);
	const serverErrors = $derived(review?.validation_errors ?? []);
	const dirty = $derived(editable && !!baseline && JSON.stringify({ chainName, members }) !== baseline && !result);

	/** Every reference the backend can resolve: middlewares and other chains (never itself). */
	const memberOptions = $derived([
		...middlewares.map((x) => ({ value: x.name, label: x.name, badge: m('middlewareKind'), description: x.config.type === 'external' ? m('external') : x.config.type === 'plugin' ? mv('pluginType') : m(`types.${x.config.type}.label`) })),
		...chains.filter((x) => x.name !== chainName).map((x) => ({ value: x.name, label: x.name, badge: m('chainType'), description: m('nestedChain') }))
	]);
	/** Same view as a middleware: the chain as Traefik reads it, members in request order. */
	const viewYaml = $derived(current ? stringify({ chain: { middlewares: chainMembers(current.config) } }) : '');
	const knownNames = $derived(new Set(memberOptions.map((o) => referenceBaseName(o.value))));

	const tabs = $derived([
		{ id: 'chain-members', label: m('tabChain'), errorCount: submitted ? Object.keys(fields).length : 0, errorLabel: m('fixErrors') },
		{ id: 'chain-review', label: m('tabReview') }
	]);

	$effect(() => {
		navigationGuard.setDirty(dirty);
		return () => navigationGuard.setDirty(false);
	});

	onMount(() => {
		const unsub = navigationGuard.subscribe(() => (suspended = navigationGuard.confirmPending));
		void load();
		return unsub;
	});
	onDestroy(() => { disposed = true; controller.abort(); });

	async function load() {
		loading = true; loadError = ''; inventoryError = '';
		try {
			const [allChains, mws] = await Promise.allSettled([listChains(controller.signal), listMiddlewares(controller.signal)]);
			if (disposed) return;
			if (allChains.status === 'rejected') throw allChains.reason;
			chains = allChains.value;
			// A chain groups HTTP middlewares only (Traefik has no TCP chain).
			if (mws.status === 'fulfilled') middlewares = mws.value.filter((x) => middlewareProtocolOf(x.config) === 'http'); else inventoryError = m('inventoryUnavailable');
			if (!creating) {
				const item = allChains.value.find((c) => c.name === name);
				if (!item) throw new Error(m('requestFailed'));
				chainName = item.name;
				members = [...chainMembers(item.config)];
			}
			baseline = JSON.stringify({ chainName, members });
		} catch (e) {
			if (!disposed) loadError = errorMessage(e, m('requestFailed'));
		} finally {
			if (!disposed) loading = false;
		}
	}

	function invalidate() {
		review = null; staged = null; reviewError = ''; applyKey = ''; uncertain = false; error = ''; conflict = false;
	}

	function addMember(value: string) {
		if (!value) return;
		if (members.length >= MAX_CHAIN_MEMBERS) { fields = { ...fields, middlewares: m('chainTooLong') }; memberToAdd = ''; return; }
		members = [...members, value];
		memberToAdd = '';
		const { middlewares: _drop, ...rest } = fields; fields = rest;
		invalidate();
	}
	function move(index: number, delta: number) {
		const next = [...members];
		const target = index + delta;
		if (target < 0 || target >= next.length) return;
		[next[index], next[target]] = [next[target], next[index]];
		members = next;
		invalidate();
	}
	function remove(index: number) { members = members.filter((_, i) => i !== index); invalidate(); }

	function validateAll(): boolean {
		const errs: Record<string, string> = {};
		if (!isValidMiddlewareName(chainName)) errs.name = m('invalidName');
		else if (creating && (chains.some((c) => c.name === chainName || c.name === chainName + '@file') || middlewares.some((x) => x.name === chainName || x.name === chainName + '@file'))) errs.name = mv('nameTaken');
		if (members.length === 0) errs.middlewares = m('chainEmpty');
		else if (members.length > MAX_CHAIN_MEMBERS) errs.middlewares = m('chainTooLong');
		else if (members.some((x) => /@(tcp|udp)$/.test(x.trim()) || !/^[^\s@]+(@[a-z]+)?$/.test(x.trim()))) errs.middlewares = m('invalidMember');
		else if (members.some((x) => referenceBaseName(x.trim()) === chainName)) errs.middlewares = m('selfReference');
		else if (!inventoryError && members.some((x) => !knownNames.has(referenceBaseName(x.trim())) && !x.includes('@'))) errs.middlewares = m('invalidMember');
		fields = errs;
		submitted = true;
		return Object.keys(errs).length === 0;
	}

	async function focusFirstError() {
		step = 'chain-members';
		await tick();
		document.querySelector<HTMLElement>('.resource-dialog [aria-invalid="true"], .resource-dialog [data-invalid="true"]')?.focus();
	}

	async function selectStep(id: string) {
		if (busy || result || id === step) return;
		if (id === 'chain-review') { await stageAndReview(); return; }
		step = id;
	}

	function describeFailure(e: unknown, fallback: string) {
		if (e instanceof ApiError && e.code === 'ERR_VERSION_CONFLICT') { conflict = true; return rv2('conflict'); }
		const base = errorMessage(e, fallback);
		return e instanceof ApiError && e.operationId ? `${base} (${rv2('operation')} ${e.operationId})` : base;
	}

	async function stageAndReview() {
		if (busy || !editable) return;
		if (!validateAll()) { await focusFirstError(); return; }
		busy = true; error = ''; conflict = false;
		try {
			const preview = await putChain(chainName, { middlewares: members.map((x) => x.trim()) }, version(), { dryRun: true, signal: controller.signal });
			if (disposed) return;
			step = 'chain-review';
			review = preview.preview ?? null; applyKey = newIdempotencyKey();
		} catch (e) {
			if (disposed) return;
			if (e instanceof ApiError && (e.status === 422 || e.status === 400)) fields = { middlewares: errorMessage(e, mv('stageFailed')) };
			error = describeFailure(e, mv('stageFailed'));
		} finally {
			if (!disposed) busy = false;
		}
	}

	async function refreshVersion() {
		busy = true;
		try {
			chains = await listChains(controller.signal);
			conflict = false; error = '';
		} catch (e) {
			error = describeFailure(e, rv2('reviewFailed'));
		} finally {
			if (!disposed) busy = false;
		}
	}

	async function apply() {
		if (!review || busy || serverErrors.length) return;
		busy = true; error = '';
		try {
			const saved = await putChain(chainName, { middlewares: members.map((x) => x.trim()) }, version(), { idempotencyKey: applyKey, signal: controller.signal });
			if (disposed) return;
			result = operationOf(saved, t('writeV2.noChange')); manualRestart = needsManualRestart(saved); uncertain = false; staged = saved.resource ?? null;
			navigationGuard.setDirty(false);
			onchanged?.();
			// Applied: back to the table; the outcome is announced as a flash.
			if (announceApplied(result, manualRestart, chainName)) { navigate(parent); return; }
		} catch (e) {
			if (disposed) return;
			if (e instanceof NetworkError) {
				uncertain = true;
				error = rv2('applyUncertain');
			} else {
				if (e instanceof ApiError && [404, 409, 410, 412, 422, 428].includes(e.status)) { review = null; applyKey = ''; uncertain = false; step = 'chain-members'; }
				error = describeFailure(e, rv2('applyFailed'));
			}
		} finally {
			if (!disposed) busy = false;
		}
	}

	function close() {
		if (busy) return;
		navigationGuard.requestNavigation(() => {
			if (staged || result) onchanged?.();
			navigate(parent);
		});
	}

</script>

<ResourceDialog title={viewing ? name : creating ? m('chainCreateTitle') : m('chainEditTitle')} description={viewing ? t('topology.kind.chain') : m('chainDialogDescription')} closeLabel={t('common.close')} onclose={close} {suspended}>
	{#snippet icon()}<Link2 size={32} aria-hidden="true" />{/snippet}
	{#snippet navigation()}
		{#if !loading && !loadError && !viewing}<Tabs {tabs} selected={step} onselect={selectStep} label={m('editorTabs')} />{/if}
	{/snippet}

	{#if loading}
		<p role="status">{mv('loading')}</p>
	{:else if loadError}
		<div class="stack">
			<Notice tone="error" message={loadError} />
			<div><Button onclick={load}>{t('common.retry')}</Button></div>
		</div>
	{:else if viewing && current}
		<div class="stack">
			<SummaryList>
				<div><dt>{m('name')}</dt><dd>{current.name}</dd></div>
				<div><dt>{m('ownership')}</dt><dd>{current.origin === 'managed' ? m('managed') : m('external')} · {current.provider}</dd></div>
				<div><dt>{rv2('stateColumn')}</dt><dd>{rv2('status.' + current.status)}</dd></div>
				{#if current.updated_at}<div><dt>{mv('updated')}</dt><dd><RelativeTime value={current.updated_at} /></dd></div>{/if}
			</SummaryList>
			<CodePreview label={m('safeProjection')} code={viewYaml} language="yaml" />
		</div>
	{:else}
		<div class="stack">
			{#if !editable}<Notice tone="warning" message={mv('notEditable')} />{/if}
			{#if inventoryError}<Notice tone="warning" message={inventoryError} />{/if}
			{#if error}
				<Notice tone="error" message={error}>
					{#if conflict}<Button size="sm" onclick={refreshVersion} disabled={busy}>{rv2('refreshDraft')}</Button>{/if}
				</Notice>
			{/if}
			{#if submitted && Object.keys(fields).length && step !== 'chain-review'}
				<div class="validation-banner" role="alert">
					<CircleAlert size={16} aria-hidden="true" />
					<span>{rv2(Object.keys(fields).length === 1 ? 'fixFieldsOne' : 'fixFields', { count: Object.keys(fields).length })}</span>
					<button type="button" onclick={focusFirstError}>{rv2('goToField')}</button>
				</div>
			{/if}

			<div id="panel-chain-members" role="tabpanel" aria-labelledby="tab-chain-members" hidden={step !== 'chain-members'} class="stack">
				<Field id="chain-name" label={m('name')} required help={creating ? m('nameHelp') : m('immutable')} error={submitted ? fields.name : ''}>
					{#snippet children(f)}
						<input id={f.id} bind:value={chainName} oninput={invalidate} readonly={!creating} disabled={locked || !editable} aria-invalid={f.invalid || undefined} aria-describedby={f.describedBy} autocomplete="off" spellcheck="false" placeholder={m('chainNamePlaceholder')} />
					{/snippet}
				</Field>
				<Field id="chain-member-add" label={m('members')} required help={m('membersHelp')} error={submitted ? fields.middlewares : ''}>
					{#snippet children(f)}
						<SearchableSelect id={f.id} value={memberToAdd} options={memberOptions.filter((o) => !members.includes(o.value))} searchable placeholder={m('selectMember')} searchPlaceholder={m('searchMembers')} emptyMessage={m('noMembers')} disabled={locked || !editable} ariaInvalid={f.invalid} ariaDescribedby={f.describedBy} onchange={addMember} />
					{/snippet}
				</Field>
				{#if members.length === 0}
					<p class="muted">{m('membersEmpty')}</p>
				{:else}
					<ol class="members" aria-label={m('memberOrder')}>
						{#each members as member, index (index + member)}
							<li>
								<span class="order">{index + 1}</span>
								<span class="member-name">{member}</span>
								<span class="member-actions">
									<Button size="sm" disabled={locked || !editable || index === 0} onclick={() => move(index, -1)}><ArrowUp size={14} aria-hidden="true" /><span class="sr-only">{m('moveUpNamed', { name: member })}</span></Button>
									<Button size="sm" disabled={locked || !editable || index === members.length - 1} onclick={() => move(index, 1)}><ArrowDown size={14} aria-hidden="true" /><span class="sr-only">{m('moveDownNamed', { name: member })}</span></Button>
									<Button size="sm" variant="danger" disabled={locked || !editable} onclick={() => remove(index)}><Trash2 size={14} aria-hidden="true" /><span class="sr-only">{m('removeNamed', { name: member })}</span></Button>
								</span>
							</li>
						{/each}
					</ol>
				{/if}
			</div>

			<div id="panel-chain-review" role="tabpanel" aria-labelledby="tab-chain-review" hidden={step !== 'chain-review'} class="stack">
				{#if result}
					<Notice tone={operationTone(result.status)} title={t('operationV2.status.' + result.status)} message={result.summary}>
						{#if result.id}<span class="muted">{t('operationV2.id', { id: result.id })}</span>{/if}
					</Notice>
					{#if manualRestart}<Notice tone="warning" message={t('writeV2.manualRestart')} />{/if}
				{:else if review}
					<SummaryList>
						<div><dt>{m('name')}</dt><dd>{chainName}</dd></div>
						<div><dt>{m('members')}</dt><dd>{members.join(' → ')}</dd></div>
						{#if staged?.updated_at}<div><dt>{mv('updated')}</dt><dd><RelativeTime value={staged.updated_at} /></dd></div>{/if}
						<div><dt>{rv2('pendingTotal')}</dt><dd>{review.changes?.length ?? 0}</dd></div>
					</SummaryList>
					{#if serverErrors.length}
						<Notice tone="error" title={rv2('serverErrors')}>
							<ul class="reasons">{#each serverErrors as ve}<li>{ve.item_id}{ve.field ? ' · ' + ve.field : ''}: {ve.message}</li>{/each}</ul>
						</Notice>
					{/if}
					{#if review.requires_restart}
						<Notice tone="warning" message={t('resolverUI.lifecycleHelp')}>
							{#if review.restart_reasons?.length}<ul class="reasons">{#each review.restart_reasons as reason}<li>{reason}</li>{/each}</ul>{/if}
						</Notice>
					{/if}
					{#if review.static_diff}<CodePreview label={rv2('diff.static')} code={review.static_diff} language="diff" />{/if}
					{#if review.dynamic_diff}<CodePreview label={rv2('diff.dynamic')} code={review.dynamic_diff} language="diff" />{/if}
					{#if !review.static_diff && !review.dynamic_diff}<Notice tone="info" message={mv('noFileChanges')} />{/if}
				{:else if reviewError}
					<Notice tone="error" message={reviewError}><Button size="sm" onclick={stageAndReview}>{t('common.retry')}</Button></Notice>
				{:else}
					<p role="status">{rv2('staging')}</p>
				{/if}
			</div>
		</div>
	{/if}

	{#snippet footer()}
		<Button onclick={close} disabled={busy}>{result || viewing ? t('common.close') : t('common.cancel')}</Button>
		{#if viewing && current && !current.read_only}
			<Button variant="primary" onclick={() => navigate('/chains/' + encodeURIComponent(current.name) + '/edit')}>{m('edit')}</Button>
		{/if}
		{#if !loading && !loadError && !viewing && !result && editable}
			{#if step === 'chain-review'}
				<Button onclick={() => (step = 'chain-members')} disabled={busy}>{m('back')}</Button>
				{#if review}
					<Button variant="primary" onclick={apply} pending={busy} disabled={busy || serverErrors.length > 0}>{t('domainUI.' + (busy ? 'applying' : uncertain ? 'retryApply' : 'apply'))}</Button>
				{/if}
			{:else}
				<Button variant="primary" onclick={stageAndReview} pending={busy} disabled={busy}>{rv2('stageReview')}</Button>
			{/if}
		{/if}
	{/snippet}
</ResourceDialog>

<style>
	.stack, .stack:not([hidden]) { display: flex; flex-direction: column; gap: 24px; }
	.stack[hidden] { display: none; }
	.muted { margin: 0; color: var(--text-muted); font-size: 14px; line-height: 21px; }
	.reasons { margin: 8px 0 0; padding-left: 20px; font-size: 14px; line-height: 22px; }
	.members { list-style: none; margin: 0; padding: 0; display: flex; flex-direction: column; gap: 8px; }
	.members li { display: flex; align-items: center; gap: 12px; padding: 8px 12px; border: 1px solid var(--border); border-radius: var(--radius-control); background: var(--surface-muted); }
	.order { min-width: 24px; color: var(--text-muted); font-size: 13px; }
	.member-name { flex: 1; min-width: 0; overflow-wrap: anywhere; font-size: 14px; }
	.member-actions { display: flex; gap: 6px; }
	.validation-banner { display: flex; align-items: center; gap: 8px; padding: 10px 14px; border: 1px solid color-mix(in srgb, var(--danger-text) 30%, transparent); border-radius: var(--radius-card); background: var(--danger-bg); color: var(--danger-text); font-size: 13px; line-height: 18px; }
	.validation-banner span { flex: 1; }
	.validation-banner button { border: 0; background: transparent; color: inherit; font: inherit; font-weight: 600; cursor: pointer; padding: 2px 4px; }
	.validation-banner button:hover { text-decoration: underline; }
</style>

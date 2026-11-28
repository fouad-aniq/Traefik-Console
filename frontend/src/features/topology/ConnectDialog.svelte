<script lang="ts">
	/**
	 * Link dialog: opened when one object is dropped onto another on the map (or a link is removed).
	 *
	 *   Link tab   → what will be linked, plus the only settings that link needs
	 *                (position of a middleware, subdomain of a domain binding, …)
	 *   Review tab → PUT {resource}?dry_run=true + If-Match returns the preview (nothing saved)
	 *   Apply      → the same PUT without dry_run, with one Idempotency-Key per attempt
	 *
	 * The body is the whole resource with one reference changed (see graph.ts buildChange),
	 * so the write goes through exactly the same protocol as the resource's own dialog.
	 */
	import { onDestroy } from 'svelte';
	import { Link2, Unlink, ArrowRight, ArrowUp, ArrowDown } from '@lucide/svelte';
	import ResourceDialog from '../../shared/ui/ResourceDialog.svelte';
	import Field from '../../shared/ui/Field.svelte';
	import Tabs from '../../shared/ui/Tabs.svelte';
	import Button from '../../shared/ui/Button.svelte';
	import Notice from '../../shared/ui/Notice.svelte';
	import RadioCards from '../../shared/ui/RadioCards.svelte';
	import SummaryList from '../../shared/ui/SummaryList.svelte';
	import CodePreview from '../../shared/ui/CodePreview.svelte';
	import NodeCard from './NodeCard.svelte';
	import { buildChange, currentOrder, type Connection } from './graph';
	import {
		encodeName,
		needsManualRestart,
		newIdempotencyKey,
		operationOf,
		announceApplied,
		saveResource,
	} from '../../core/mutations';
	import { operationTone } from '../changes/models';
	import type { Operation, OperationPreview } from '../../core/types';
	import { formFrom, hostOn, previewRule, referenceBase, ruleFor, type RouterResource } from '../routes/models';
	import { subdomainError } from '../routes/validation';
	import type { DomainResource } from '../domains/models';
	import { ApiError, NetworkError } from '../../shared/api/client';
	import { errorMessage } from '../../shared/api/errorMessage';
	import { t } from '../../shared/localization/i18n';

	let {
		connection,
		op = 'add',
		onclose,
		onapplied,
	}: {
		connection: Connection;
		op?: 'add' | 'remove';
		onclose: () => void;
		/** the change is saved: the map must be read again */
		onapplied: () => void;
	} = $props();

	const tp = (key: string, args?: Record<string, string | number>) => t('topology.' + key, args);
	const rv2 = (key: string, args?: Record<string, string | number>) => t('resolverV2.' + key, args);
	const TABS = { link: 'link-setup', review: 'link-review' } as const;

	// The dialog is keyed on the connection, so these are fixed for its lifetime.
	// svelte-ignore state_referenced_locally
	const { relation, subject, object } = connection;
	const names = { subject: subject.name, object: object.name };
	const ordered = relation === 'route-middleware' || relation === 'chain-middleware';
	const route = subject.kind === 'route' ? (subject.resource as RouterResource) : null;
	const routeForm = route ? formFrom(route.name, route.config) : null;
	const domainMain =
		relation === 'route-domain' ? ((object.resource as DomainResource | undefined)?.config.main ?? '') : '';

	// svelte-ignore state_referenced_locally
	let order = $state<string[]>(
		op === 'add' && ordered ? [...currentOrder(connection), object.name] : currentOrder(connection),
	);
	/** The route already answers on a managed domain: the new one is added as another hostname, unless the user moves it. */
	// svelte-ignore state_referenced_locally
	const hasDomain =
		relation === 'route-domain' && op === 'add' && routeForm?.mode === 'domain' && !!routeForm.domainName;
	let domainMode = $state<'add' | 'replace'>('add');
	// Same app, other domain: start from the subdomain it has today.
	let subdomain = $state(hasDomain ? routeForm!.subdomain : '');
	let pathPrefix = $state(routeForm && routeForm.mode !== 'rule' ? routeForm.pathPrefix : '');
	let step = $state<string>(TABS.link);
	let busy = $state(false);
	let error = $state('');
	let conflict = $state(false);
	let submitted = $state(false);
	let review = $state<OperationPreview | null>(null);
	let applyKey = $state('');
	let uncertain = $state(false);
	let result = $state<Operation | null>(null);
	let manualRestart = $state(false);

	let disposed = false;
	const controller = new AbortController();
	onDestroy(() => {
		disposed = true;
		controller.abort();
	});

	const isNew = (ref: string) => op === 'add' && ref === object.name;
	const isRemoved = (ref: string) => op === 'remove' && (ref === object.name || referenceBase(ref) === object.name);
	const serverErrors = $derived(review?.validation_errors ?? []);
	const subdomainInvalid = $derived(relation === 'route-domain' && subdomainError(subdomain));
	const prefixInvalid = $derived.by(() => {
		const p = pathPrefix.trim();
		return relation === 'route-domain' && !!p && (!p.startsWith('/') || p.length > 512 || /[`\\\s?#\x00-\x1f]/.test(p));
	});
	const rule = $derived.by(() => {
		if (!routeForm || relation !== 'route-domain' || op !== 'add') return '';
		// Adding: the hostnames the router has today (as core-v2 compiled them) plus the new one.
		if (hasDomain && domainMode === 'add')
			return ruleFor(
				[...(route?.config.domains ?? []), hostOn(domainMain, subdomain)],
				pathPrefix,
				routeForm.protocol === 'tcp',
			);
		return previewRule(
			{ ...routeForm, mode: 'domain', domainName: object.name, subdomain, pathPrefix, extraHosts: [] },
			domainMain,
		);
	});
	const change = $derived(
		buildChange(connection, op, {
			order: $state.snapshot(order),
			subdomain: subdomain.trim().toLowerCase(),
			pathPrefix: pathPrefix.trim(),
			domainMode,
		}),
	);

	/** What the subject references today for single-valued relations (the value being replaced). */
	const replaced = $derived.by(() => {
		if (op !== 'add') return '';
		if (relation === 'route-service') return route?.config.service ?? '';
		if (relation === 'route-resolver') return route?.config.tls?.cert_resolver ?? '';
		if (relation === 'route-domain') return route?.config.domain_name ?? '';
		if (relation === 'domain-resolver' || relation === 'domain-certificate') {
			const d = (subject.resource as DomainResource).config;
			return (d.method === 'resolver' ? d.resolver : d.certificate) ?? '';
		}
		return '';
	});
	const entryPointsAfter = $derived(change.endpoint === 'routers' ? change.input.entry_points : []);

	const tabs = $derived([
		{
			id: TABS.link,
			label: tp('dialog.tabLink'),
			errorCount: submitted && (subdomainInvalid || prefixInvalid) ? 1 : 0,
			errorLabel: t('routeV2.fixErrors'),
		},
		{ id: TABS.review, label: tp('dialog.tabReview') },
	]);

	function invalidate() {
		review = null;
		applyKey = '';
		uncertain = false;
		error = '';
		conflict = false;
	}
	function move(index: number, delta: number) {
		const target = index + delta;
		if (target < 0 || target >= order.length) return;
		const next = [...order];
		[next[index], next[target]] = [next[target], next[index]];
		order = next;
		invalidate();
	}

	function describeFailure(e: unknown, fallback: string) {
		if (e instanceof ApiError && (e.code === 'ERR_VERSION_CONFLICT' || e.status === 412)) {
			conflict = true;
			return tp('dialog.conflict');
		}
		const base = errorMessage(e, fallback);
		return e instanceof ApiError && e.operationId ? `${base} (${rv2('operation')} ${e.operationId})` : base;
	}
	const send = (dryRun: boolean) =>
		saveResource<unknown>(
			`/api/v1/${change.endpoint}/${encodeName(change.name)}`,
			change.input,
			change.version,
			dryRun ? { dryRun: true, signal: controller.signal } : { idempotencyKey: applyKey, signal: controller.signal },
		);

	async function stageAndReview() {
		if (busy || result) return;
		submitted = true;
		if (subdomainInvalid || prefixInvalid) {
			step = TABS.link;
			return;
		}
		busy = true;
		error = '';
		conflict = false;
		try {
			const preview = await send(true);
			if (disposed) return;
			review = preview.preview ?? null;
			applyKey = newIdempotencyKey();
			step = TABS.review;
		} catch (e) {
			if (!disposed) error = describeFailure(e, rv2('stageFailed'));
		} finally {
			if (!disposed) busy = false;
		}
	}

	async function apply() {
		if (!review || busy || serverErrors.length) return;
		busy = true;
		error = '';
		try {
			const saved = await send(false);
			if (disposed) return;
			result = operationOf(saved, t('writeV2.noChange'));
			manualRestart = needsManualRestart(saved);
			uncertain = false;
			onapplied();
			// Applied: back to the map; the outcome is announced as a flash.
			if (announceApplied(result, manualRestart, subject.name)) onclose();
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
					step = TABS.link;
				}
				error = describeFailure(e, rv2('applyFailed'));
			}
		} finally {
			if (!disposed) busy = false;
		}
	}

	async function selectStep(id: string) {
		if (busy || result || id === step) return;
		if (id === TABS.review) await stageAndReview();
		else step = id;
	}
	const close = () => {
		if (!busy) onclose();
	};
</script>

<ResourceDialog
	title={op === 'remove' ? tp('dialog.removeTitle') : tp('action.' + relation)}
	closeLabel={t('common.close')}
	onclose={close}
>
	{#snippet icon()}{#if op === 'remove'}<Unlink size={32} aria-hidden="true" />{:else}<Link2
				size={32}
				aria-hidden="true"
			/>{/if}{/snippet}
	{#snippet navigation()}<Tabs {tabs} selected={step} onselect={selectStep} label={tp('dialog.tabs')} />{/snippet}

	<div class="stack">
		{#if error}
			<Notice tone="error" message={error}>
				{#if conflict}<Button
						size="sm"
						onclick={() => {
							onapplied();
							onclose();
						}}
					>
						{tp('dialog.refreshClose')}
					</Button>{/if}
			</Notice>
		{/if}

		<div
			id="panel-{TABS.link}"
			role="tabpanel"
			aria-labelledby="tab-{TABS.link}"
			hidden={step !== TABS.link}
			class="stack"
		>
			<!-- Read left to right like the map: what is referenced, then what references it. -->
			<div class="pair" class:removing={op === 'remove'}>
				<div class="end"><NodeCard node={object} showKind /></div>
				<span class="join" aria-hidden="true">
					{#if op === 'remove'}<Unlink size={18} />{:else}<ArrowRight size={18} />{/if}
				</span>
				<div class="end"><NodeCard node={subject} showKind /></div>
			</div>
			<p class="lead">{tp(`explain.${op}.${relation}`, names)}</p>

			{#if ordered}
				<h3>{tp('dialog.order')}</h3>
				{#if op === 'add'}<p class="muted">{tp('dialog.orderHelp')}</p>{/if}
				<ol class="members">
					{#each order as ref, index (ref)}
						<li class:new={isNew(ref)} class:removed={isRemoved(ref)}>
							<span class="order">{index + 1}</span>
							<span class="url">{ref}</span>
							{#if isNew(ref)}<span class="pill">{tp('dialog.newBadge')}</span>{/if}
							{#if isRemoved(ref)}<span class="pill danger">{tp('dialog.removedBadge')}</span>{/if}
							{#if op === 'add'}
								<div class="member-actions">
									<Button size="sm" disabled={busy || !!result || index === 0} onclick={() => move(index, -1)}>
										<ArrowUp size={14} aria-hidden="true" /><span class="sr-only">{t('routes.moveUp')} {ref}</span>
									</Button>
									<Button
										size="sm"
										disabled={busy || !!result || index === order.length - 1}
										onclick={() => move(index, 1)}
									>
										<ArrowDown size={14} aria-hidden="true" /><span class="sr-only">{t('routes.moveDown')} {ref}</span>
									</Button>
								</div>
							{/if}
						</li>
					{/each}
				</ol>
			{:else if relation === 'route-entrypoint'}
				<SummaryList>
					<div>
						<dt>{tp('dialog.current')}</dt>
						<dd>{(route?.config.entry_points ?? []).join(', ') || t('routeV2.entryPointsAuto')}</dd>
					</div>
					<div>
						<dt>{tp('dialog.after')}</dt>
						<dd>{entryPointsAfter.join(', ') || t('routeV2.entryPointsAuto')}</dd>
					</div>
				</SummaryList>
			{:else if relation === 'route-domain' && op === 'remove'}
				<SummaryList>
					<div>
						<dt>{tp('dialog.current')}</dt>
						<dd><code>{route?.config.rule || '—'}</code></dd>
					</div>
					<div>
						<dt>{tp('dialog.after')}</dt>
						<dd>{tp('dialog.hostsWithout', { domain: object.name })}</dd>
					</div>
				</SummaryList>
			{:else if relation === 'route-domain'}
				{#if hasDomain}
					<RadioCards
						legend={tp('dialog.domainChoice')}
						value={domainMode}
						name="link-domain-mode"
						disabled={busy || !!result}
						options={[
							{
								value: 'add',
								label: tp('dialog.domainAdd'),
								description: tp('dialog.domainAddHelp', { domain: route?.config.domain_name ?? '' }),
							},
							{ value: 'replace', label: tp('dialog.domainReplace'), description: tp('dialog.domainReplaceHelp') },
						]}
						onchange={(v) => {
							domainMode = v as 'add' | 'replace';
							invalidate();
						}}
					/>
				{/if}
				<Field
					id="link-subdomain"
					label={tp('dialog.hostname')}
					help={t('routes.subdomainHelp')}
					error={submitted && subdomainInvalid ? t('routes.invalidSubdomain') : ''}
				>
					{#snippet children(f)}
						<div class="hostname-row">
							<input
								id={f.id}
								class="subdomain"
								bind:value={subdomain}
								oninput={invalidate}
								disabled={busy || !!result}
								aria-invalid={f.invalid || undefined}
								aria-describedby={f.describedBy}
								autocomplete="off"
								spellcheck="false"
								placeholder="app"
							/>
							<span class="dot" aria-hidden="true">.</span>
							<code class="base">{domainMain.replace(/^\*\./, '') || object.name}</code>
						</div>
					{/snippet}
				</Field>
				<Field
					id="link-path"
					label={t('routes.pathPrefix')}
					help={t('routes.pathPrefixHelp')}
					error={submitted && prefixInvalid ? t('routes.invalidPathPrefix') : ''}
				>
					{#snippet children(f)}
						<input
							id={f.id}
							bind:value={pathPrefix}
							oninput={invalidate}
							disabled={busy || !!result}
							aria-invalid={f.invalid || undefined}
							aria-describedby={f.describedBy}
							autocomplete="off"
							spellcheck="false"
							placeholder="/api"
						/>
					{/snippet}
				</Field>
				<SummaryList>
					<div>
						<dt>{tp('dialog.current')}</dt>
						<dd><code>{route?.config.rule || '—'}</code></dd>
					</div>
					<div>
						<dt>{tp('dialog.after')}</dt>
						<dd><code>{rule || '—'}</code></dd>
					</div>
				</SummaryList>
				{#if !hasDomain || domainMode === 'replace'}<Notice tone="warning" message={tp('dialog.domainNote')} />{/if}
			{:else if op === 'add'}
				<SummaryList>
					<div>
						<dt>{tp('dialog.current')}</dt>
						<dd>{replaced || tp('dialog.none')}</dd>
					</div>
					<div>
						<dt>{tp('dialog.after')}</dt>
						<dd>{object.name}</dd>
					</div>
				</SummaryList>
				{#if replaced}<Notice tone="warning" message={tp('dialog.replaces', { name: replaced })} />{/if}
			{/if}
		</div>

		<div
			id="panel-{TABS.review}"
			role="tabpanel"
			aria-labelledby="tab-{TABS.review}"
			hidden={step !== TABS.review}
			class="stack"
		>
			{#if result}
				<Notice
					tone={operationTone(result.status)}
					title={t('operationV2.status.' + result.status)}
					message={result.summary}
				>
					{#if result.id}<span class="muted">{t('operationV2.id', { id: result.id })}</span>{/if}
				</Notice>
				{#if manualRestart}<Notice tone="warning" message={t('writeV2.manualRestart')} />{/if}
			{:else if review}
				<SummaryList>
					<div>
						<dt>{t('topology.kind.' + subject.kind)}</dt>
						<dd>{subject.name}</dd>
					</div>
					<div>
						<dt>{tp('dialog.change')}</dt>
						<dd>{tp(`explain.${op}.${relation}`, names)}</dd>
					</div>
					<div>
						<dt>{rv2('pendingTotal')}</dt>
						<dd>{review.changes?.length ?? 0}</dd>
					</div>
				</SummaryList>
				{#if serverErrors.length}
					<Notice tone="error" title={rv2('serverErrors')}>
						<ul class="reasons">
							{#each serverErrors as ve}<li>{ve.item_id}{ve.field ? ' · ' + ve.field : ''}: {ve.message}</li>{/each}
						</ul>
					</Notice>
				{/if}
				{#if review.requires_restart}
					<Notice tone="warning" message={t('resolverUI.lifecycleHelp')}>
						{#if review.restart_reasons?.length}<ul class="reasons">
								{#each review.restart_reasons as reason}<li>{reason}</li>{/each}
							</ul>{/if}
					</Notice>
				{/if}
				{#if review.static_diff}<CodePreview
						label={rv2('diff.static')}
						code={review.static_diff}
						language="diff"
					/>{/if}
				{#if review.dynamic_diff}<CodePreview
						label={rv2('diff.dynamic')}
						code={review.dynamic_diff}
						language="diff"
					/>{/if}
				{#if !review.static_diff && !review.dynamic_diff}<Notice
						tone="info"
						message={t('routeV2.noFileChanges')}
					/>{/if}
			{:else}
				<p role="status">{rv2('staging')}</p>
			{/if}
		</div>
	</div>

	{#snippet footer()}
		<Button onclick={close} disabled={busy}>{result ? t('common.close') : t('common.cancel')}</Button>
		{#if !result}
			{#if step === TABS.review}
				<Button onclick={() => (step = TABS.link)} disabled={busy}>{t('domainUI.back')}</Button>
				{#if review}
					<Button variant="primary" onclick={apply} pending={busy} disabled={busy || serverErrors.length > 0}>
						{t('domainUI.' + (busy ? 'applying' : uncertain ? 'retryApply' : 'apply'))}
					</Button>
				{/if}
			{:else}
				<Button variant="primary" onclick={stageAndReview} pending={busy} disabled={busy}>{rv2('stageReview')}</Button>
			{/if}
		{/if}
	{/snippet}
</ResourceDialog>

<style>
	.stack,
	.stack:not([hidden]) {
		display: flex;
		flex-direction: column;
		gap: 24px;
	}
	.stack[hidden] {
		display: none;
	}
	h3 {
		display: flex;
		align-items: center;
		gap: 12px;
		margin: 0;
		font-size: 13px;
		font-weight: 600;
		color: var(--text);
	}
	h3::after {
		content: '';
		flex: 1;
		height: 1px;
		background: var(--border);
	}
	.muted {
		margin: 0;
		color: var(--text-muted);
		font-size: 14px;
		line-height: 21px;
	}
	.lead {
		margin: 0;
		font-size: 14px;
		line-height: 21px;
	}
	.reasons {
		margin: 8px 0 0;
		padding-left: 20px;
		font-size: 14px;
		line-height: 22px;
	}
	code {
		font-family: var(--font-mono, ui-monospace, monospace);
		font-size: 13px;
		overflow-wrap: anywhere;
	}
	.pair {
		display: grid;
		grid-template-columns: minmax(0, 1fr) auto minmax(0, 1fr);
		align-items: center;
		gap: 12px;
	}
	.end {
		min-width: 0;
		padding: 12px 14px;
		border: 1px solid var(--border);
		border-radius: var(--radius-card);
		background: var(--canvas);
	}
	.join {
		display: grid;
		place-items: center;
		width: 32px;
		height: 32px;
		border-radius: 50%;
		border: 1px solid var(--connection-border);
		background: var(--connection-soft);
		color: var(--connection);
	}
	.removing .join {
		border-color: color-mix(in srgb, var(--danger-text) 30%, transparent);
		background: var(--danger-bg);
		color: var(--danger-text);
	}
	.members {
		list-style: none;
		margin: 0;
		padding: 0;
		display: flex;
		flex-direction: column;
		gap: 8px;
	}
	.members li {
		display: flex;
		align-items: center;
		gap: 12px;
		min-height: 50px;
		padding: 8px 12px;
		border: 1px solid var(--border);
		border-radius: var(--radius-control);
		background: var(--surface-muted);
	}
	.members li.new {
		border-color: var(--connection-border);
		background: var(--connection-soft);
	}
	.members li.removed {
		border-color: color-mix(in srgb, var(--danger-text) 30%, transparent);
		background: var(--danger-bg);
	}
	.members li.removed .url {
		text-decoration: line-through;
	}
	.member-actions {
		display: flex;
		gap: 6px;
		margin-left: auto;
	}
	.order {
		min-width: 24px;
		color: var(--text-muted);
		font-size: 13px;
	}
	.url {
		overflow-wrap: anywhere;
		font-size: 14px;
	}
	.pill {
		padding: 0 7px;
		border-radius: 4px;
		border: 1px solid color-mix(in srgb, var(--connection) 35%, transparent);
		color: var(--connection);
		font-size: 12px;
		line-height: 18px;
	}
	.pill.danger {
		border-color: color-mix(in srgb, var(--danger-text) 30%, transparent);
		color: var(--danger-text);
	}
	.hostname-row {
		display: flex;
		align-items: center;
		gap: 8px;
		min-width: 0;
	}
	.hostname-row .subdomain {
		flex: 2 1 0;
		min-width: 0;
	}
	.hostname-row .dot {
		flex: none;
		font-size: 20px;
		font-weight: 600;
		line-height: 1;
		color: var(--text-muted);
	}
	.hostname-row .base {
		flex: 3 1 0;
		min-width: 0;
	}
	@media (max-width: 480px) {
		.pair {
			grid-template-columns: minmax(0, 1fr);
		}
		.join {
			justify-self: center;
			transform: rotate(90deg);
		}
	}
</style>

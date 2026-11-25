<script lang="ts">
	/**
	 * One restore point (view) and its restore (restore).
	 *
	 *   View    → metadata (rename / pin), the resources it holds, its saved YAML (secrets redacted),
	 *             and a comparison with any other snapshot.
	 *   Restore → GET restore-preview (diff + restore_token) → POST restore with the token and a stable
	 *             Idempotency-Key. A pre_restore backup is taken first. A stale token (409) re-reads the preview.
	 */
	import { onMount, onDestroy } from 'svelte';
	import { History } from '@lucide/svelte';
	import ResourceDialog from '../../shared/ui/ResourceDialog.svelte';
	import Button from '../../shared/ui/Button.svelte';
	import Notice from '../../shared/ui/Notice.svelte';
	import Field from '../../shared/ui/Field.svelte';
	import SearchableSelect from '../../shared/ui/SearchableSelect.svelte';
	import Checkbox from '../../shared/ui/Checkbox.svelte';
	import Tabs from '../../shared/ui/Tabs.svelte';
	import StatusBadge from '../../shared/ui/StatusBadge.svelte';
	import SummaryList from '../../shared/ui/SummaryList.svelte';
	import CodePreview from '../../shared/ui/CodePreview.svelte';
	import RelativeTime from '../../shared/ui/RelativeTime.svelte';
	import { compareSnapshots, getRestorePreview, getSnapshot, getSnapshotConfig, getSnapshotResources, listSnapshots, patchSnapshot, restoreSnapshot, type SnapshotConfig } from './api';
	import { classify, formatBytes, groupChanges, originTone, pathForItem, snapshotLabel, type Outcome } from './models';
	import { needsManualRestart, newIdempotencyKey } from '../../core/mutations';
	import type { OperationPreview, RestoreResult, Snapshot, SnapshotResource } from '../../core/types';
	import { NetworkError } from '../../shared/api/client';
	import { errorMessage } from '../../shared/api/errorMessage';
	import { t } from '../../shared/localization/i18n';

	let { mode = 'view', id = '', navigate, onchanged, onclose }: { mode?: string; id?: string; navigate: (p: string) => void; onchanged?: () => void; onclose: () => void } = $props();

	const c = (key: string, args?: Record<string, string | number>) => t('changesV2.' + key, args);

	const restoring = $derived(mode === 'restore');
	const snapshotId = $derived(Number(id));

	let loading = $state(true);
	let loadError = $state('');
	let snapshot = $state<Snapshot | null>(null);

	// restore
	let preview = $state<OperationPreview | null>(null);
	let previewProblem = $state<{ outcome: Outcome; text: string } | null>(null);
	let key = $state('');
	let skipBackup = $state(false);
	let busy = $state(false);
	let uncertain = $state(false);
	let problem = $state<{ outcome: Outcome; text: string } | null>(null);
	let result = $state<RestoreResult | null>(null);

	// view
	let view = $state('resources');
	let resources = $state<SnapshotResource[] | null>(null);
	let config = $state<SnapshotConfig | null>(null);
	let viewProblem = $state('');
	let others = $state<Snapshot[]>([]);
	let compareWith = $state('');
	let comparison = $state<OperationPreview | null>(null);
	let comparing = $state(false);
	let compareProblem = $state('');
	let editName = $state('');
	let editDescription = $state('');
	let saving = $state(false);
	let saveProblem = $state('');

	let disposed = false;
	const controller = new AbortController();

	const errors = $derived(preview?.validation_errors ?? []);
	const warnings = $derived(preview?.validation_warnings ?? []);
	const missingEnv = $derived((preview?.env_vars_to_mount ?? []).filter((e) => !e.already_set));
	const blocked = $derived(errors.length > 0 || missingEnv.length > 0);
	const hasDiff = $derived(!!(preview?.static_diff || preview?.dynamic_diff || preview?.domain_diff));
	const groups = $derived(groupChanges(preview?.changes));
	const dirty = $derived(!!snapshot && (editName.trim() !== snapshot.name || editDescription.trim() !== snapshot.description));

	onMount(() => { void load(); });
	onDestroy(() => { disposed = true; controller.abort(); });

	async function load() {
		loading = true; loadError = ''; previewProblem = null; preview = null; problem = null; viewProblem = '';
		try {
			if (!Number.isInteger(snapshotId) || snapshotId < 1) { loadError = c('notFound'); return; }
			const snap = await getSnapshot(snapshotId, controller.signal);
			if (disposed) return;
			snapshot = snap; editName = snap.name; editDescription = snap.description;
			if (restoring) await loadPreview(); else await loadView();
		} catch (e) {
			if (!disposed) loadError = classify(e) === 'notFound' ? c('notFound') : errorMessage(e, c('snapshotFailed'));
		} finally {
			if (!disposed) loading = false;
		}
	}

	async function loadPreview() {
		previewProblem = null;
		try {
			const fresh = await getRestorePreview(snapshotId, controller.signal);
			if (disposed) return;
			preview = fresh; key = newIdempotencyKey(); uncertain = false;
		} catch (e) {
			if (disposed) return;
			const outcome = classify(e);
			previewProblem = { outcome, text: outcome === 'recovery' ? c('recoveryHelp') : outcome === 'drift' ? c('driftHelp') : errorMessage(e, c('previewFailed')) };
		}
	}

	async function loadView() {
		const [r, cfg, list] = await Promise.allSettled([getSnapshotResources(snapshotId, controller.signal), getSnapshotConfig(snapshotId, controller.signal), listSnapshots(100, 0, controller.signal)]);
		if (disposed) return;
		resources = r.status === 'fulfilled' ? (r.value ?? []) : null;
		config = cfg.status === 'fulfilled' ? cfg.value : null;
		others = list.status === 'fulfilled' ? list.value.items.filter((s) => s.id !== snapshotId) : [];
		if (r.status === 'rejected' && cfg.status === 'rejected') viewProblem = errorMessage(r.reason, c('configFailed'));
	}

	async function save() {
		if (!snapshot || saving || !dirty) return;
		const name = editName.trim();
		if (!name) { saveProblem = c('nameRequired'); return; }
		saving = true; saveProblem = '';
		try {
			const updated = await patchSnapshot(snapshot.id, { name, description: editDescription.trim() });
			if (disposed) return;
			snapshot = updated; editName = updated.name; editDescription = updated.description;
			onchanged?.();
		} catch (e) {
			if (!disposed) saveProblem = errorMessage(e, c('saveFailed'));
		} finally {
			if (!disposed) saving = false;
		}
	}

	async function togglePin() {
		if (!snapshot || saving) return;
		saving = true; saveProblem = '';
		try {
			const updated = await patchSnapshot(snapshot.id, { pinned: !snapshot.pinned });
			if (disposed) return;
			snapshot = updated;
			onchanged?.();
		} catch (e) {
			if (!disposed) saveProblem = errorMessage(e, c('pinFailed'));
		} finally {
			if (!disposed) saving = false;
		}
	}

	async function compare() {
		const other = Number(compareWith);
		if (!other || comparing) return;
		comparing = true; compareProblem = ''; comparison = null;
		try {
			// from = the other snapshot, to = this one: what changes when going from `other` to this snapshot.
			const result = await compareSnapshots(other, snapshotId, controller.signal);
			if (!disposed) comparison = result;
		} catch (e) {
			if (!disposed) compareProblem = errorMessage(e, c('compareFailed'));
		} finally {
			if (!disposed) comparing = false;
		}
	}

	async function restore() {
		if (!preview?.restore_token || busy || blocked) return;
		busy = true; problem = null;
		try {
			const res = await restoreSnapshot(snapshotId, { restore_token: preview.restore_token, idempotency_key: key, skip_backup: skipBackup || undefined }, controller.signal);
			if (disposed) return;
			result = res; uncertain = false; preview = null;
			onchanged?.();
		} catch (e) {
			if (disposed) return;
			if (e instanceof NetworkError) {
				// Unknown outcome: retry only with the SAME key so the restore cannot apply twice.
				uncertain = true;
				problem = { outcome: 'other', text: c('restoreUncertain') };
			} else {
				const outcome = classify(e);
				problem = { outcome, text: ({
					recovery: c('recoveryHelp'), restoreStale: c('restoreStale'), drift: c('driftHelp'), versionConflict: c('restoreStale'),
					conflict: c('busyHelp'), quota: c('quotaHelp'), rolledBack: c('rolledBackHelp'), pinned: c('pinnedHelp'),
					notFound: c('notFound'), validation: errorMessage(e, c('restoreFailed')), other: errorMessage(e, c('restoreFailed'))
				} as Record<Outcome, string>)[outcome] };
				// A definite refusal invalidates the preview: read it again before another attempt.
				preview = null; key = ''; uncertain = false;
				if (outcome === 'restoreStale' || outcome === 'versionConflict' || outcome === 'conflict' || outcome === 'rolledBack') { onchanged?.(); await loadPreview(); }
			}
		} finally {
			if (!disposed) busy = false;
		}
	}

	const close = () => { if (!busy) onclose(); };
	const viewTabs = $derived([
		{ id: 'resources', label: c('tabResources') },
		{ id: 'config', label: c('tabConfig') },
		{ id: 'compare', label: c('tabCompare') }
	]);
</script>

<ResourceDialog title={restoring ? c('restoreTitle') : c('detailsTitle')} description={restoring ? c('restoreDescription') : c('detailsDescription')} closeLabel={t('common.close')} onclose={close}>
	{#snippet icon()}<History size={32} aria-hidden="true" />{/snippet}

	{#if loading}
		<p role="status">{c('loading')}</p>
	{:else if loadError}
		<div class="stack">
			<Notice tone="error" message={loadError} />
			<div><Button onclick={load}>{t('common.retry')}</Button></div>
		</div>
	{:else if snapshot}
		<div class="stack">
			<SummaryList>
				<div><dt>{c('snapshot')}</dt><dd>{snapshotLabel(snapshot)}</dd></div>
				<div><dt>{c('origin')}</dt><dd><StatusBadge size="sm" tone={originTone(snapshot.origin)} text={c('originLabel.' + snapshot.origin)} />{#if snapshot.pinned} <StatusBadge size="sm" tone="info" text={c('pinned')} />{/if}{#if snapshot.known_good} <StatusBadge size="sm" tone="success" text={c('knownGood')} />{/if}</dd></div>
				<div><dt>{c('when')}</dt><dd><RelativeTime value={snapshot.created_at} /></dd></div>
				<div><dt>{c('items')}</dt><dd>{snapshot.item_count} · {formatBytes(snapshot.size_bytes)}</dd></div>
				{#if snapshot.description}<div><dt>{c('description')}</dt><dd>{snapshot.description}</dd></div>{/if}
				{#if snapshot.source_operation_id}<div><dt>{c('sourceOperation')}</dt><dd><button type="button" class="link" onclick={() => navigate('/changes/history/' + snapshot!.source_operation_id)}>#{snapshot.source_operation_id}</button></dd></div>{/if}
			</SummaryList>

			{#if restoring}
				{#if result}
					<Notice tone={needsManualRestart(result) ? 'warning' : 'info'} title={needsManualRestart(result) ? c('manualRestartTitle') : c('restoredTitle')} message={needsManualRestart(result) ? c('manualRestartHelp') : result.operation.summary || c('restoredHint')}>
						{#if result.backup_snapshot_id}
							<Button size="sm" onclick={() => navigate('/changes/snapshots/' + result!.backup_snapshot_id)}>{c('viewBackup', { id: result.backup_snapshot_id })}</Button>
						{/if}
					</Notice>
					{#if result.warnings?.length}
						<Notice tone="warning" title={c('warningsTitle')}><ul class="reasons">{#each result.warnings as w}<li>{w.item_id}{w.field ? ' · ' + w.field : ''}: {w.message}</li>{/each}</ul></Notice>
					{/if}
				{:else}
					<Notice tone="warning" title={c('restoreWarningTitle')} message={skipBackup ? c('restoreWarningNoBackup') : c('restoreWarning')} />
					{#if previewProblem}
						<Notice tone="error" title={previewProblem.outcome === 'recovery' ? c('recoveryTitle') : undefined} message={previewProblem.text}>
							{#if previewProblem.outcome !== 'recovery' && previewProblem.outcome !== 'drift'}<Button size="sm" onclick={loadPreview}>{t('common.retry')}</Button>{/if}
						</Notice>
					{:else if preview}
						{#if errors.length}
							<Notice tone="error" title={c('errorsTitle')}>
								<ul class="reasons">{#each errors as ve}{@const path = pathForItem(ve.item_id)}<li>{#if path}<button type="button" class="link" onclick={() => navigate(path)}>{ve.item_id}</button>{:else}{ve.item_id}{/if}{ve.field ? ' · ' + ve.field : ''}: {ve.message}</li>{/each}</ul>
							</Notice>
						{/if}
						{#if missingEnv.length}
							<Notice tone="error" title={c('envTitle')} message={c('envHelp')}>
								<ul class="reasons">{#each missingEnv as e}<li><code>{e.env_var_name}</code> → <code>{e.file_path}</code></li>{/each}</ul>
							</Notice>
						{/if}
						{#if warnings.length}
							<Notice tone="warning" title={c('warningsTitle')}><ul class="reasons">{#each warnings as w}<li>{w.item_id}{w.field ? ' · ' + w.field : ''}: {w.message}</li>{/each}</ul></Notice>
						{/if}
						{#if preview.requires_restart}
							<Notice tone="info" title={c('restartTitle')} message={c('restartHelp')}>
								{#if preview.restart_reasons?.length}<ul class="reasons">{#each preview.restart_reasons as reason}<li>{reason}</li>{/each}</ul>{/if}
							</Notice>
						{/if}
						{#each groups as group (group.kind)}
							<section aria-labelledby={'g-' + group.kind}>
								<h3 id={'g-' + group.kind}>{c('group.' + group.kind)} <span class="count">{group.items.length}</span></h3>
								<ul class="changes">{#each group.items as item (item.id)}<li><span class="item-name">{item.name}</span><StatusBadge size="sm" tone={item.action === 'deleted' ? 'danger' : item.action === 'created' ? 'info' : 'warning'} text={c('action.' + item.action)} /><span class="muted">{c('scope.' + item.scope)}</span></li>{/each}</ul>
							</section>
						{/each}
						{#if hasDiff}
							<details class="diff">
								<summary>{c('showDiff')}</summary>
								<div class="stack">
									{#if preview.static_diff}<CodePreview label={c('diffStatic')} code={preview.static_diff} language="diff" />{/if}
									{#if preview.dynamic_diff}<CodePreview label={c('diffDynamic')} code={preview.dynamic_diff} language="diff" />{/if}
									{#if preview.domain_diff}<CodePreview label={c('diffDomains')} code={preview.domain_diff} language="diff" />{/if}
								</div>
							</details>
						{:else if !groups.length}
							<Notice tone="info" message={c('identical')} />
						{/if}
						<Checkbox checked={skipBackup} label={c('skipBackup')} help={c('skipBackupHelp')} disabled={busy} onchange={(v) => (skipBackup = v)} />
					{:else}
						<p role="status">{c('loadingPreview')}</p>
					{/if}
					{#if problem}<Notice tone={problem.outcome === 'rolledBack' || problem.outcome === 'restoreStale' ? 'warning' : 'error'} title={problem.outcome === 'recovery' ? c('recoveryTitle') : undefined} message={problem.text} />{/if}
				{/if}
			{:else}
				<form class="edit" onsubmit={(e) => { e.preventDefault(); void save(); }}>
					<Field label={c('name')} error={saveProblem && !editName.trim() ? saveProblem : ''}>
						{#snippet children(f)}<input id={f.id} bind:value={editName} maxlength="100" autocomplete="off" disabled={saving} aria-invalid={f.invalid || undefined} aria-describedby={f.describedBy} />{/snippet}
					</Field>
					<Field label={c('description')}>
						{#snippet children(f)}<textarea id={f.id} bind:value={editDescription} rows="2" disabled={saving}></textarea>{/snippet}
					</Field>
					<div class="edit-actions">
						<Button type="submit" size="sm" disabled={!dirty || saving} pending={saving}>{c('save')}</Button>
						<Button size="sm" onclick={togglePin} disabled={saving}>{snapshot.pinned ? c('unpin') : c('pin')}</Button>
					</div>
					{#if saveProblem && editName.trim()}<Notice tone="error" message={saveProblem} />{/if}
				</form>

				{#if viewProblem}<Notice tone="error" message={viewProblem} />{/if}
				<Tabs tabs={viewTabs} selected={view} onselect={(v) => (view = v)} label={c('viewsLabel')} />
				{#if view === 'resources'}
					<div id="panel-resources" role="tabpanel" aria-labelledby="tab-resources">
						{#if resources === null}<Notice tone="error" message={c('resourcesFailed')} />
						{:else if resources.length === 0}<p class="muted">{c('resourcesEmpty')}</p>
						{:else}
							<ul class="changes">{#each resources as r (r.kind + ':' + r.name)}<li><span class="item-name">{r.name}</span><span class="muted">{c('group.' + r.kind)}</span></li>{/each}</ul>
						{/if}
					</div>
				{:else if view === 'config'}
					<div id="panel-config" role="tabpanel" aria-labelledby="tab-config" class="stack">
						{#if !config}<Notice tone="info" message={c('configUnavailable')} />
						{:else}
							<p class="muted">{c('yamlHelp')}</p>
							{#each [{ label: c('diffStatic'), text: config.static_yaml }, { label: c('diffDynamic'), text: config.dynamic_yaml }, { label: c('diffDomains'), text: config.domains_yaml }] as part (part.label)}
								{#if part.text.trim()}<CodePreview label={part.label} code={part.text} language="yaml" />{/if}
							{/each}
						{/if}
					</div>
				{:else}
					<div id="panel-compare" role="tabpanel" aria-labelledby="tab-compare" class="stack">
						{#if others.length === 0}<p class="muted">{c('compareNone')}</p>
						{:else}
							<Field label={c('compareWith')} help={c('compareHelp', { id: snapshot.id })}>
								{#snippet children(f)}
									<SearchableSelect id={f.id} value={compareWith} options={others.map((o) => ({ value: String(o.id), label: snapshotLabel(o) }))} placeholder={c('choose')} searchPlaceholder={c('choose')} onchange={(v) => { compareWith = v; comparison = null; compareProblem = ''; }} ariaDescribedby={f.describedBy} />
								{/snippet}
							</Field>
							<div><Button size="sm" onclick={compare} disabled={!compareWith || comparing} pending={comparing}>{c('compare')}</Button></div>
							{#if compareProblem}<Notice tone="error" message={compareProblem} />{/if}
							{#if comparison}
								{#if comparison.changes?.length || comparison.added?.length || comparison.removed?.length || comparison.changed?.length}
									{#each [{ key: 'added', list: comparison.added }, { key: 'changed', list: comparison.changed }, { key: 'removed', list: comparison.removed }] as part (part.key)}
										{#if part.list?.length}
											<section>
												<h3>{c('compare_' + part.key)} <span class="count">{part.list.length}</span></h3>
												<ul class="changes">{#each part.list as r (r.kind + ':' + r.name)}<li><span class="item-name">{r.name}</span><span class="muted">{c('group.' + r.kind)}</span></li>{/each}</ul>
											</section>
										{/if}
									{/each}
									{#if comparison.static_diff}<CodePreview label={c('diffStatic')} code={comparison.static_diff} language="diff" />{/if}
									{#if comparison.dynamic_diff}<CodePreview label={c('diffDynamic')} code={comparison.dynamic_diff} language="diff" />{/if}
									{#if comparison.domain_diff}<CodePreview label={c('diffDomains')} code={comparison.domain_diff} language="diff" />{/if}
								{:else}
									<Notice tone="info" message={c('compareIdentical')} />
								{/if}
							{/if}
						{/if}
					</div>
				{/if}
			{/if}
		</div>
	{/if}

	{#snippet footer()}
		<Button onclick={close} disabled={busy}>{t('common.close')}</Button>
		{#if !loading && !loadError && snapshot && !result}
			{#if !restoring}
				<Button variant="primary" onclick={() => navigate('/changes/snapshots/' + snapshot!.id + '/restore')}>{c('restore')}</Button>
			{:else if preview}
				<Button variant="danger" onclick={restore} pending={busy} disabled={busy || blocked}>{busy ? c('restoring') : uncertain ? c('retryRestore') : c('restoreConfirm')}</Button>
			{/if}
		{/if}
	{/snippet}
</ResourceDialog>

<style>
	.stack { display: flex; flex-direction: column; gap: 20px; }
	.edit { display: flex; flex-direction: column; gap: 12px; }
	.edit-actions { display: flex; gap: 8px; }
	.muted { margin: 0; color: var(--text-muted); font-size: 14px; line-height: 21px; }
	.reasons { margin: 8px 0 0; padding-left: 20px; font-size: 14px; line-height: 22px; }
	code { font-family: var(--font-mono, ui-monospace, monospace); font-size: 13px; overflow-wrap: anywhere; }
	h3 { display: flex; align-items: center; gap: 8px; margin: 0 0 8px; font-size: 13px; font-weight: 600; }
	.count { color: var(--text-muted); font-weight: 400; }
	.changes { list-style: none; margin: 0; padding: 0; display: flex; flex-direction: column; gap: 8px; }
	.changes li { display: flex; align-items: center; gap: 12px; padding: 8px 12px; border: 1px solid var(--border); border-radius: var(--radius-control); background: var(--surface-muted); }
	.item-name { font-weight: 600; overflow-wrap: anywhere; }
	.changes .muted { margin-left: auto; font-size: 13px; }
	.diff summary { cursor: pointer; font-size: 14px; font-weight: 600; padding: 4px 0; }
	.diff .stack { margin-top: 12px; }
	.link { border: 0; background: transparent; padding: 0; color: var(--connection); font: inherit; cursor: pointer; }
	.link:hover { text-decoration: underline; }
</style>

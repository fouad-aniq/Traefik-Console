<script lang="ts">
	import { onMount } from 'svelte';
	import { LogIn, Plus, Pencil, Trash2 } from '@lucide/svelte';
	import Button from '../../../shared/ui/Button.svelte';
	import Notice from '../../../shared/ui/Notice.svelte';
	import StatusBadge from '../../../shared/ui/StatusBadge.svelte';
	import RelativeTime from '../../../shared/ui/RelativeTime.svelte';
	import PasswordConfirmDialog from './PasswordConfirmDialog.svelte';
	import ProviderDialog from './ProviderDialog.svelte';
	import { flash } from '../../../shared/ui/flash.svelte';
	import { errorMessage } from '../../../shared/api/errorMessage';
	import { ApiError } from '../../../shared/api/client';
	import { t } from '../../../shared/localization/i18n';
	import { deleteProvider, listProviders } from '../api';
	import { auth } from '../session.svelte';
	import type { ProviderSettings } from '../models';
	import { presetOf } from './presets';

	let { navigate, mode, id }: { navigate: (path: string) => void; mode: 'list' | 'create' | 'edit'; id: string } =
		$props();
	const p = (key: string, args?: Record<string, string | number>) => t('authScreens.providers.' + key, args);

	let rows = $state<ProviderSettings[]>([]);
	let loading = $state(true);
	let error = $state('');
	async function load() {
		loading = true;
		try {
			rows = (await listProviders()).items;
			auth.providers = rows.map((r) => ({ id: r.id, display_name: r.display_name }));
			error = '';
		} catch (err) {
			error = errorMessage(err, p('loadFailed'));
		} finally {
			loading = false;
		}
	}
	onMount(load);

	function hostOf(raw: string): string {
		try {
			return new URL(raw).host;
		} catch {
			return raw;
		}
	}

	// ------------------------------------------------------------------ delete
	let pendingDelete = $state<ProviderSettings | null>(null);
	let deleting = $state(false);
	let deleteError = $state('');

	async function confirmDelete(password: string) {
		if (!pendingDelete) return;
		deleting = true;
		deleteError = '';
		try {
			await deleteProvider(pendingDelete.id, password);
			flash({ tone: 'success', title: p('deleted', { name: pendingDelete.display_name }) });
			pendingDelete = null;
			await load();
		} catch (err) {
			deleteError =
				err instanceof ApiError && err.fieldErrors.current_password
					? err.fieldErrors.current_password
					: errorMessage(err, p('deleteFailed'));
		} finally {
			deleting = false;
		}
	}
</script>

<div class="bar">
	<p class="lead">{p('lead')}</p>
	{#if rows.length}
		<Button variant="primary" onclick={() => navigate('/settings/security/providers/new')}>
			<Plus size={16} aria-hidden="true" />
			{p('add')}
		</Button>
	{/if}
</div>

{#if error}
	<Notice tone="error" message={error}><Button size="sm" onclick={load}>{t('common.retry')}</Button></Notice>
{:else if loading && !rows.length}
	<p class="muted" role="status">{p('loading')}</p>
{:else if !rows.length}
	<div class="empty">
		<span class="empty-icon" aria-hidden="true"><LogIn size={28} /></span>
		<h2>{p('empty')}</h2>
		<p>{p('emptyHelp')}</p>
		<Button variant="primary" onclick={() => navigate('/settings/security/providers/new')}>
			<Plus size={16} aria-hidden="true" />
			{p('add')}
		</Button>
	</div>
{:else}
	<ul class="cards" aria-label={p('title')}>
		{#each rows as row (row.id)}
			<li class="card">
				<header>
					<div class="identity">
						<h2>{row.display_name}</h2>
						<StatusBadge
							size="sm"
							tone={row.kind === 'oidc' ? 'connection' : 'info'}
							text={p('template.' + presetOf(row))}
						/>
					</div>
					<div class="actions">
						<Button
							size="sm"
							onclick={() => navigate('/settings/security/providers/' + encodeURIComponent(row.id) + '/edit')}
						>
							<Pencil size={14} aria-hidden="true" />
							{p('edit')}
						</Button>
						<Button
							size="sm"
							variant="danger"
							onclick={() => {
								deleteError = '';
								pendingDelete = row;
							}}
						>
							<Trash2 size={14} aria-hidden="true" />
							{p('delete')}
						</Button>
					</div>
				</header>
				<div class="preview" aria-hidden="true">
					<LogIn size={15} />
					{t('authScreens.login.signInWith', { name: row.display_name })}
				</div>
				<dl>
					<div>
						<dt>{p('colType')}</dt>
						<dd>{p('kind.' + row.kind)}</dd>
					</div>
					<div>
						<dt>{p('colAddress')}</dt>
						<dd class="mono">{hostOf(row.kind === 'oidc' ? row.issuer : row.authorize_url)}</dd>
					</div>
					<div>
						<dt>{p('colAccounts')}</dt>
						<dd>{row.allowed_identities.join(', ')}</dd>
					</div>
					<div>
						<dt>{p('colSecret')}</dt>
						<dd>
							<StatusBadge
								size="sm"
								tone={row.credential_stored ? 'success' : 'neutral'}
								text={row.credential_stored ? p('secretStored') : p('secretNone')}
							/>
						</dd>
					</div>
					<div>
						<dt>{p('colUpdated')}</dt>
						<dd><RelativeTime value={row.updated_at} /></dd>
					</div>
				</dl>
			</li>
		{/each}
	</ul>
{/if}

<PasswordConfirmDialog
	open={!!pendingDelete}
	danger
	title={p('deleteTitle', { name: pendingDelete?.display_name ?? '' })}
	message={p('deleteBody')}
	confirmLabel={p('delete')}
	error={deleteError}
	pending={deleting}
	oncancel={() => (pendingDelete = null)}
	onconfirm={confirmDelete}
/>

{#if mode !== 'list'}
	{#key `${mode}:${id}`}
		<ProviderDialog
			provider={mode === 'edit' ? rows.find((r) => r.id === id) : undefined}
			{loading}
			editing={mode === 'edit'}
			onclose={() => navigate('/settings/security/providers')}
			onsaved={load}
		/>
	{/key}
{/if}

<style>
	.bar {
		display: flex;
		flex-wrap: wrap;
		align-items: center;
		justify-content: space-between;
		gap: 12px 24px;
		margin-bottom: 20px;
		max-width: 880px;
	}
	.lead {
		margin: 0;
		color: var(--text-muted);
		font-size: 14px;
		line-height: 21px;
		max-width: 600px;
	}
	.muted {
		color: var(--text-muted);
	}
	.cards {
		list-style: none;
		margin: 0;
		padding: 0;
		display: flex;
		flex-direction: column;
		gap: 16px;
		max-width: 880px;
	}
	.card {
		background: var(--surface);
		border: 1px solid var(--border);
		border-top-color: var(--connection-border);
		border-radius: var(--radius-card);
		padding: 20px 24px;
	}
	.card header {
		display: flex;
		flex-wrap: wrap;
		align-items: center;
		justify-content: space-between;
		gap: 12px;
	}
	.identity {
		display: flex;
		align-items: center;
		gap: 10px;
		min-width: 0;
	}
	.identity h2 {
		margin: 0;
		font-size: 18px;
		line-height: 26px;
		font-weight: 600;
		overflow-wrap: anywhere;
	}
	.actions {
		display: flex;
		gap: 8px;
	}
	.preview {
		display: inline-flex;
		align-items: center;
		gap: 8px;
		margin-top: 14px;
		padding: 7px 14px;
		border: 1px solid var(--border);
		border-radius: var(--radius-control);
		background: var(--canvas);
		color: var(--text);
		font-size: 14px;
		font-weight: 500;
	}
	dl {
		display: grid;
		grid-template-columns: 180px minmax(0, 1fr);
		gap: 10px 20px;
		margin: 18px 0 0;
		padding-top: 16px;
		border-top: 1px solid var(--border);
	}
	dl div {
		display: contents;
	}
	dt {
		color: var(--text-muted);
		font-size: 14px;
		line-height: 22px;
	}
	dd {
		margin: 0;
		font-size: 14px;
		line-height: 22px;
		overflow-wrap: anywhere;
	}
	.mono {
		font-family: ui-monospace, SFMono-Regular, Consolas, monospace;
		font-size: 13px;
	}
	.empty {
		max-width: 880px;
		text-align: center;
		padding: 48px 24px;
		border: 1px dashed var(--outline);
		border-radius: var(--radius-card);
		background: var(--surface);
	}
	.empty-icon {
		display: inline-grid;
		place-items: center;
		width: 56px;
		height: 56px;
		border-radius: 50%;
		background: var(--connection-soft);
		color: var(--connection);
	}
	.empty h2 {
		margin: 16px 0 6px;
		font-size: 18px;
		line-height: 26px;
	}
	.empty p {
		margin: 0 auto 20px;
		max-width: 460px;
		color: var(--text-muted);
		font-size: 14px;
		line-height: 21px;
	}
	@media (max-width: 767px) {
		.card {
			padding: 16px;
		}
		dl {
			grid-template-columns: 1fr;
			gap: 2px;
		}
		dd {
			margin-bottom: 8px;
		}
	}
</style>

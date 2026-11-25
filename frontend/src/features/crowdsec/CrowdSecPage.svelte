<script lang="ts">
	/**
	 * CrowdSec: one status card, one Configure button (one dialog), and what CrowdSec observed.
	 * Tabs only for the lists that need their own table: decisions and allowlists.
	 */
	import { onDestroy, onMount } from 'svelte';
	import { Lock, Settings2, ShieldCheck, ShieldOff } from '@lucide/svelte';
	import PageHeader from '../../shared/ui/PageHeader.svelte';
	import Notice from '../../shared/ui/Notice.svelte';
	import Button from '../../shared/ui/Button.svelte';
	import Surface from '../../shared/ui/Surface.svelte';
	import StatusBadge from '../../shared/ui/StatusBadge.svelte';
	import Tabs from '../../shared/ui/Tabs.svelte';
	import { errorMessage } from '../../shared/api/errorMessage';
	import { t } from '../../shared/localization/i18n';
	import { getStats, getStatus } from './api';
	import { lockedBy, type CrowdSecStats, type CrowdSecStatus, type StatsWindow, type StepTone } from './models';
	import StatsPanel from './StatsPanel.svelte';
	import DecisionsPanel from './DecisionsPanel.svelte';
	import AllowlistsPanel from './AllowlistsPanel.svelte';
	import CrowdSecDialog from './CrowdSecDialog.svelte';
	import BanDialog from './BanDialog.svelte';

	let { navigate, mode = 'list', tab = 'overview' }: { navigate: (p: string) => void; mode?: string; tab?: string } = $props();
	const c = (key: string, args?: Record<string, string | number>) => t('crowdsecV2.' + key, args);
	const p = (key: string, args?: Record<string, string | number>) => t('crowdsecV2.page.' + key, args);
	const configuring = $derived(['settings', 'setup', 'wizard', 'disable', 'rotate'].includes(mode));

	let status = $state<CrowdSecStatus | null>(null);
	/** Without a LAPI address the Console has no CrowdSec to talk to: the page offers to configure one. */
	const connected = $derived(!!status?.lapi.url);
	let statusError = $state('');
	let statusLoading = $state(true);
	let reloadKey = $state(0);
	let disposed = false;
	let statusGen = 0;
	let statusController = new AbortController();

	let stats = $state<CrowdSecStats | null>(null);
	let statsError = $state('');
	let statsLoading = $state(false);
	let statsWindow = $state<StatsWindow>('24h');
	let statsGen = 0;
	let statsController = new AbortController();

	const caps = $derived(status?.capabilities);
	const tabs = $derived([
		{ id: 'overview', label: p('tabs.overview') },
		...(caps?.decisions ? [{ id: 'decisions', label: c('tabs.decisions') }] : []),
		...(caps?.allowlist_read ? [{ id: 'allowlists', label: c('tabs.allowlists') }] : [])
	]);
	const current = $derived(tabs.some((x) => x.id === tab) ? tab : 'overview');

	async function loadStatus() {
		const own = ++statusGen;
		statusController.abort();
		statusController = new AbortController();
		statusLoading = true;
		statusError = '';
		try {
			const result = await getStatus(statusController.signal);
			if (disposed || own !== statusGen) return;
			status = result;
			if (result.lapi.url && !stats && !statsLoading) void loadStats();
		} catch (e) {
			if (disposed || own !== statusGen) return;
			statusError = errorMessage(e, c('protection.loadFailed'));
		} finally {
			if (!disposed && own === statusGen) statusLoading = false;
		}
	}

	async function loadStats() {
		const own = ++statsGen;
		statsController.abort();
		statsController = new AbortController();
		statsLoading = true;
		statsError = '';
		try {
			const result = await getStats(statsWindow, statsController.signal);
			if (disposed || own !== statsGen) return;
			stats = result;
		} catch (e) {
			if (disposed || own !== statsGen) return;
			statsError = errorMessage(e, c('stats.loadFailed'));
		} finally {
			if (!disposed && own === statsGen) statsLoading = false;
		}
	}

	onMount(() => { void loadStatus(); });
	onDestroy(() => { disposed = true; statusController.abort(); statsController.abort(); });
	const refreshAll = () => { void loadStatus(); if (connected) void loadStats(); reloadKey++; };
	const configure = () => navigate('/crowdsec/settings');
	const setWindow = (w: StatsWindow) => { statsWindow = w; void loadStats(); };

	const protection = $derived(!status ? 'off' : status.config.integration_mode === 'external' ? 'external' : status.config.enabled ? 'managed' : 'off');
	const protectedCount = $derived((status?.routers ?? []).filter((r) => r.protected).length);
	const heroTone = $derived<StepTone>(protection === 'off' ? 'neutral' : status?.complete ? 'success' : 'warning');

	/** The facts a person checks at a glance; each says where its value comes from. */
	const facts = $derived.by(() => {
		if (!status) return [];
		const env = (field: string) => !!lockedBy(status?.environment, field);
		const metrics = status.metrics?.state;
		const machine = status.machine?.state ?? 'not_configured';
		const tone = (good: boolean, neutral = false): StepTone => (good ? 'success' : neutral ? 'neutral' : 'warning');
		return [
			{ id: 'lapi', tone: (status.lapi.state === 'ok' ? 'success' : status.lapi.state === 'unchecked' ? 'neutral' : 'danger') as StepTone, text: c('state.lapi.' + status.lapi.state), detail: status.lapi.url, locked: env('lapi_url') },
			{ id: 'key', tone: tone(status.key.present), text: p('keyState.' + (status.key.present ? 'present' : 'missing')), detail: '', locked: env('lapi_key') },
			{ id: 'engine', tone: (status.engine.state === 'running' ? 'success' : status.engine.state === 'stopped' ? 'danger' : 'neutral') as StepTone, text: c('state.engine.' + status.engine.state), detail: status.engine.name ?? '', locked: false },
			{ id: 'machine', tone: (machine === 'ok' ? 'success' : machine === 'not_configured' ? 'neutral' : 'danger') as StepTone, text: c('state.machine.' + machine), detail: status.machine?.id ?? '', locked: env('machine_id') },
			{ id: 'metrics', tone: tone(metrics === 'available', !metrics), text: p('metrics.' + (metrics ?? 'not_configured')), detail: '', locked: env('metrics_url') }
		];
	});
</script>

<PageHeader title={c('title')} subtitle={connected ? c('subtitle') : p('subtitleDisconnected')}>
	{#snippet actions()}
		{#if connected}<Button onclick={refreshAll} pending={statusLoading} disabled={statusLoading}>{c('refresh')}</Button>{/if}
		{#if status}<Button variant={connected ? 'secondary' : 'primary'} onclick={configure}><Settings2 size={18} aria-hidden="true" /> {p('configure')}</Button>{/if}
	{/snippet}
</PageHeader>

{#if statusError}
	<Notice tone="error" message={statusError}><Button size="sm" onclick={loadStatus}>{t('common.retry')}</Button></Notice>
{:else if statusLoading && !status}
	<p role="status">{c('protection.loading')}</p>
{:else if status && !connected}
	<Surface class="welcome">
		<span class="welcome-icon" aria-hidden="true"><ShieldCheck size={28} /></span>
		<div class="welcome-text">
			<h2>{p('onboarding.title')}</h2>
			<p class="muted">{p(status.capabilities?.auto_enroll ? 'onboarding.connectAutoHelp' : 'onboarding.help')}</p>
			{#if status.environment?.length}<p class="muted env"><Lock size={14} aria-hidden="true" /> {p('onboarding.environment', { count: status.environment.length })}</p>{/if}
		</div>
		<Button variant="primary" onclick={configure}>{p('onboarding.cta')}</Button>
	</Surface>
{:else if status}
	<Surface class="summary">
		<div class="summary-head">
			<span class="summary-icon tone-{heroTone}" aria-hidden="true">{#if protection === 'off'}<ShieldOff size={22} />{:else}<ShieldCheck size={22} />{/if}</span>
			<div class="summary-title">
				<h2>{p('protection.' + protection)}</h2>
				<p class="muted">{protection === 'managed' ? p('protection.managedHelp', { count: protectedCount, total: (status.routers ?? []).length }) : p('protection.' + protection + 'Help')}</p>
			</div>
		</div>
		<dl class="facts">
			{#each facts as fact (fact.id)}
				<div>
					<dt>{p('chip.' + fact.id)}{#if fact.locked}<span class="lock" title={p('lockedHint')}><Lock size={12} aria-hidden="true" /><span class="sr-only">{p('lockedHint')}</span></span>{/if}</dt>
					<dd><StatusBadge size="sm" tone={fact.tone} text={fact.text} />{#if fact.detail}<span class="detail" title={fact.detail}>{fact.detail}</span>{/if}</dd>
				</div>
			{/each}
		</dl>
	</Surface>

	{#if tabs.length > 1}
		<div class="tabs"><Tabs {tabs} selected={current} label={c('title')} onselect={(id) => navigate(id === 'overview' ? '/crowdsec' : '/crowdsec/' + id)} /></div>
	{/if}

	<div class="tab-body" role={tabs.length > 1 ? 'tabpanel' : undefined} aria-labelledby={tabs.length > 1 ? 'tab-' + current : undefined}>
		{#if current === 'decisions'}
			<DecisionsPanel onadd={() => navigate('/crowdsec/decisions/new')} canAdd={caps?.decision_create ?? false} canDelete={caps?.decision_delete ?? false} {reloadKey} />
		{:else if current === 'allowlists'}
			<AllowlistsPanel canWrite={caps?.allowlist_write ?? false} {reloadKey} />
		{:else}
			<StatsPanel {stats} loading={statsLoading} error={statsError} window={statsWindow} alerts={caps?.alerts ?? false} {reloadKey} onwindow={setWindow} onrefresh={loadStats} onsetup={configure} />
		{/if}
	</div>
{/if}

{#if configuring && status}
	<CrowdSecDialog {status} {navigate} onchanged={refreshAll} />
{/if}
{#if mode === 'ban' && status?.capabilities?.decision_create}
	<BanDialog onclose={() => navigate('/crowdsec/decisions')} ondone={() => { reloadKey++; void loadStatus(); }} />
{/if}

<style>
	.muted { color: var(--text-muted); font-size: 14px; line-height: 20px; margin: 0; }
	.sr-only { position: absolute; width: 1px; height: 1px; overflow: hidden; clip: rect(0 0 0 0); white-space: nowrap; }
	:global(.welcome) { display: flex; flex-wrap: wrap; align-items: center; gap: 16px 20px; }
	.welcome-icon, .summary-icon { display: grid; place-items: center; width: 48px; height: 48px; border-radius: 12px; background: var(--surface-muted); color: var(--text-muted); flex-shrink: 0; }
	.welcome-icon { background: var(--connection-soft); color: var(--connection); }
	.welcome-text { flex: 1 1 360px; display: grid; gap: 4px; }
	.welcome-text h2, .summary-title h2 { margin: 0; font-size: 18px; line-height: 26px; font-weight: 600; }
	.env { display: flex; align-items: center; gap: 6px; font-size: 13px; }
	:global(.summary) { display: grid; gap: 20px; }
	.summary-head { display: flex; align-items: center; gap: 16px; }
	.summary-icon.tone-success { background: var(--success-bg); color: var(--success-text); }
	.summary-icon.tone-warning { background: var(--warning-bg); color: var(--warning-text); }
	.facts { display: grid; grid-template-columns: repeat(auto-fit, minmax(170px, 1fr)); gap: 16px; margin: 0; padding-top: 20px; border-top: 1px solid var(--border); }
	.facts div { display: grid; gap: 6px; min-width: 0; align-content: start; }
	.facts dt { display: flex; align-items: center; gap: 6px; font-size: 13px; color: var(--text-muted); }
	.facts dd { margin: 0; display: grid; gap: 4px; justify-items: start; min-width: 0; }
	.detail { font-size: 12px; color: var(--text-muted); font-family: ui-monospace, SFMono-Regular, Menlo, monospace; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; max-width: 100%; }
	.lock { display: inline-grid; place-items: center; color: var(--connection); }
	.tabs { margin: 24px 0 20px; }
	.tab-body { margin-top: 24px; min-height: 200px; }
	.tabs + .tab-body { margin-top: 0; }
</style>

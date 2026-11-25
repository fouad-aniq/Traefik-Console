<script lang="ts" module>
	export type SecurityTab = 'account' | 'providers' | 'sessions' | 'activity';
	export type SecurityMode = 'list' | 'create' | 'edit' | 'password';
</script>

<script lang="ts">
	/**
	 * Security: the administrator account, its sign-in providers, where it is
	 * signed in, and recent security activity. Same shape as the other pages:
	 * header with the tab's main action, routed tabs, tables, dialogs.
	 */
	import { KeyRound } from '@lucide/svelte';
	import PageHeader from '../../../shared/ui/PageHeader.svelte';
	import Tabs from '../../../shared/ui/Tabs.svelte';
	import Button from '../../../shared/ui/Button.svelte';
	import { t } from '../../../shared/localization/i18n';
	import AccountPanel from './AccountPanel.svelte';
	import PasswordDialog from './PasswordDialog.svelte';
	import ProvidersPanel from './ProvidersPanel.svelte';
	import SessionsPanel from './SessionsPanel.svelte';
	import ActivityPanel from './ActivityPanel.svelte';
	import { auth } from '../session.svelte';

	let { navigate, tab, mode = 'list', id = '' }: {
		navigate: (path: string) => void;
		tab: SecurityTab;
		mode?: SecurityMode;
		id?: string;
	} = $props();

	const s = (key: string, args?: Record<string, string | number>) => t('authScreens.security.' + key, args);
	const BASE = '/settings/security';
	const paths: Record<SecurityTab, string> = { account: BASE, providers: `${BASE}/providers`, sessions: `${BASE}/sessions`, activity: `${BASE}/activity` };

	const tabs = $derived([
		{ id: 'account', label: s('tabs.account') },
		{ id: 'providers', label: auth.providers.length ? s('tabs.providersCount', { count: auth.providers.length }) : s('tabs.providers') },
		{ id: 'sessions', label: s('tabs.sessions') },
		{ id: 'activity', label: s('tabs.activity') }
	]);
</script>

<PageHeader title={s('title')} subtitle={s('subtitle')}>
	{#snippet actions()}
		{#if tab === 'account'}
			<Button variant="primary" onclick={() => navigate(`${BASE}/password`)}><KeyRound size={16} aria-hidden="true" /> {s('changePassword')}</Button>
		{/if}
	{/snippet}
</PageHeader>

<div class="resource-tabs">
	<Tabs {tabs} selected={tab} onselect={(next) => navigate(paths[next as SecurityTab])} label={s('title')} />
</div>

<div id="panel-{tab}" role="tabpanel" aria-labelledby="tab-{tab}">
	{#if tab === 'account'}
		<AccountPanel />
	{:else if tab === 'providers'}
		<ProvidersPanel {navigate} mode={mode === 'create' || mode === 'edit' ? mode : 'list'} {id} />
	{:else if tab === 'sessions'}
		<SessionsPanel />
	{:else}
		<ActivityPanel />
	{/if}
</div>

{#if mode === 'password'}
	<PasswordDialog onclose={() => navigate(BASE)} />
{/if}

<style>
	.resource-tabs { margin-bottom: 24px; }
</style>

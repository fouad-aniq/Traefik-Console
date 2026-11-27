<script lang="ts">
	/**
	 * Accepted-design application shell.
	 *
	 * Layout: desktop ≥1200px — fixed 232px sidebar, 64px topbar, main content
	 * max 1440px centered; tablet 768–1199px — 64px icon rail expandable to an
	 * overlay, 56px topbar; mobile <768px — 280px drawer (capped to viewport
	 * minus 32px) with backdrop, focus containment and Escape-to-close.
	 *
	 * The topbar carries breadcrumbs, a truthful pending-changes link (no
	 * invented aggregate count), the theme control, the language control and
	 * the account menu. Navigation sections are always present, grouped by task
	 * (TLS & domains, traffic, security & extensions, administration) and
	 * ordered by build dependency within each group; destinations without
	 * delivered features are labelled and never act like working feature links.
	 */
	import {
		Layers,
		LayoutDashboard,
		History,
		Globe,
		Route,
		Network,
		Puzzle,
		ShieldCheck,
		ShieldAlert,
		Menu,
		X,
		PanelLeftClose,
		PanelLeftOpen,
		Camera,
		KeyRound,
		ExternalLink,
		Workflow,
		LogIn,
		Lock,
		CircleUserRound,
		CircleArrowUp,
		ArrowRight,
		Tag
	} from '@lucide/svelte';
	import { onDestroy, onMount, type Snippet } from 'svelte';
	import TrafficMark from '../shared/ui/TrafficMark.svelte';
	import FlashHost from '../shared/ui/FlashHost.svelte';
	import ConfirmDialog from '../shared/ui/ConfirmDialog.svelte';
	import Notice from '../lib/components/Notice.svelte';
	import { navigationGuard } from '../lib/stores/navigationGuard';
	import { errorMessage } from '../shared/api/errorMessage';
	import { adoptConfig, getConfigState } from '../features/changes/api';
	import type { ConfigState } from '../core/types';
	import { t } from '../shared/localization/i18n';
	import { prefs, getResolvedTheme, setLanguage, setSidebarCollapsed, setThemePreference, type ThemePreference } from '../shared/preferences/preferences.svelte';

	interface NavSection {
		id: string;
		route: string;
		labelKey: string;
		icon: typeof LayoutDashboard;
		/** true when the destination has a delivered page behind it */
		delivered: boolean;
	}

	interface NavGroup {
		id: string;
		/** absent for the ungrouped entry point (Overview) */
		labelKey?: string;
		items: NavSection[];
	}

	/*
	 * Navigation is grouped by the job the user is doing, and ordered inside each
	 * group by build dependency: a resolver issues certificates, certificates
	 * secure domains; middlewares and services are what a route assembles.
	 */
	const GROUPS: NavGroup[] = [
		{
			id: 'home',
			items: [
				{ id: 'map', route: '/', labelKey: 'nav.map', icon: Workflow, delivered: true }
			]
		},
		{
			id: 'tls',
			labelKey: 'nav.groups.tls',
			items: [
				{ id: 'resolvers', route: '/resolvers', labelKey: 'nav.resolvers', icon: KeyRound, delivered: true },
				{ id: 'certificates', route: '/certificates', labelKey: 'nav.certificates', icon: ShieldCheck, delivered: true },
				{ id: 'domains', route: '/domains', labelKey: 'nav.domains', icon: Globe, delivered: true }
			]
		},
		{
			id: 'traffic',
			labelKey: 'nav.groups.traffic',
			items: [
				{ id: 'entrypoints', route: '/entrypoints', labelKey: 'nav.entrypoints', icon: LogIn, delivered: true },
				{ id: 'middlewares', route: '/middlewares', labelKey: 'nav.middlewares', icon: Layers, delivered: true },
				{ id: 'services', route: '/services', labelKey: 'nav.services', icon: Network, delivered: true },
				{ id: 'routes', route: '/routers', labelKey: 'nav.routes', icon: Route, delivered: true }
			]
		},
		{
			id: 'security',
			labelKey: 'nav.groups.security',
			items: [
				{ id: 'plugins', route: '/plugins', labelKey: 'nav.plugins', icon: Puzzle, delivered: true },
				{ id: 'crowdsec', route: '/crowdsec', labelKey: 'nav.crowdsec', icon: ShieldAlert, delivered: true }
			]
		},
		{
			id: 'admin',
			labelKey: 'nav.groups.admin',
			items: [
				{ id: 'changes', route: '/changes', labelKey: 'nav.changes', icon: History, delivered: true },
				{ id: 'security', route: '/settings/security', labelKey: 'nav.security', icon: Lock, delivered: true }
			]
		}
	];

	const SECTIONS: NavSection[] = GROUPS.flatMap((g) => g.items);

	/** Console build version, injected from package.json at build time. */
	const APP_VERSION = __APP_VERSION__;
	const RELEASES_URL = 'https://github.com/fouad-aniq/traefik-console/releases';

	let {
		currentRoute,
		navigate,
		onLogout,
		username = '',
		banner,
		children
	}: {
		currentRoute: string;
		navigate: (path: string) => void;
		onLogout?: () => Promise<void> | void;
		username?: string;
		banner?: Snippet;
		children: Snippet;
	} = $props();

	let confirmPending = $state(false);
	onMount(() => navigationGuard.subscribe(() => {
		confirmPending = navigationGuard.confirmPending;
	}));

	// Installation state: drift, a pending manual restart, an operation that needs recovery.
	let configState = $state<ConfigState | null>(null);
	let adopting = $state(false);
	let adoptError = $state('');
	let archivedDismissed = $state(false);
	let stateTimer: ReturnType<typeof setInterval> | undefined;
	let stateController = new AbortController();
	let stateDisposed = false;

	async function refreshState() {
		stateController.abort();
		stateController = new AbortController();
		try {
			const next = await getConfigState(stateController.signal);
			if (!stateDisposed) configState = next;
		} catch {
			// The banner is advisory: keep the last known state when the request fails.
		}
	}

	async function adopt() {
		if (adopting) return;
		adopting = true; adoptError = '';
		try {
			await adoptConfig();
			await refreshState();
		} catch (err) {
			adoptError = errorMessage(err, t('shell.adoptFailed'));
		} finally {
			adopting = false;
		}
	}

	// The first read comes from the route effect below, which also runs on mount;
	// reading here too would abort that request on every page load.
	// Coming back to the tab re-reads at once, so a release published meanwhile shows without waiting.
	function refreshWhenVisible() {
		if (document.visibilityState === 'visible') void refreshState();
	}
	onMount(() => {
		stateTimer = setInterval(() => { void refreshState(); }, 20_000);
		document.addEventListener('visibilitychange', refreshWhenVisible);
	});
	onDestroy(() => {
		stateDisposed = true; stateController.abort(); if (stateTimer) clearInterval(stateTimer);
		document.removeEventListener('visibilitychange', refreshWhenVisible);
	});
	// Re-read after every navigation: a write on the page just left may have changed the state.
	$effect(() => { void currentRoute; void refreshState(); });

	// Available plugin updates come with the polled config state: the server
	// compares the installed plugins with the synced catalog, nothing is guessed.
	const pluginUpdates = $derived(configState?.plugin_updates ?? 0);
	// The server checks GitHub's releases and only reports one newer than this build.
	const consoleUpdate = $derived(configState?.console_update ?? null);
	const pluginUpdatesLabel = $derived(
		pluginUpdates === 1 ? t('shell.pluginUpdateOne') : t('shell.pluginUpdatesMany', { count: pluginUpdates })
	);
	/** Count shown as a badge beside a navigation item (0 hides it). */
	function navBadge(section: NavSection): number {
		return section.id === 'plugins' ? pluginUpdates : 0;
	}

	const archivedCount = $derived((configState?.archived_drafts ?? 0) + (configState?.archived_secret_drafts ?? 0));

	let drawerOpen = $state(false);

	// Sidebar: full width on wide screens (collapsible to the icon rail, remembered), always the
	// rail on tablets (no room for both), a drawer behind the hamburger on phones.
	const WIDE = '(min-width: 1200px)';
	let wide = $state(typeof window !== 'undefined' && typeof window.matchMedia === 'function' ? window.matchMedia(WIDE).matches : true);
	onMount(() => {
		if (typeof window.matchMedia !== 'function') return;
		const query = window.matchMedia(WIDE);
		const update = () => (wide = query.matches);
		update();
		query.addEventListener('change', update);
		return () => query.removeEventListener('change', update);
	});
	const rail = $derived(!wide || prefs.sidebarCollapsed);
	let themeMenuOpen = $state(false);
	let languageMenuOpen = $state(false);
	let accountMenuOpen = $state(false);
	let topbarLogoutError = $state<string | null>(null);
	let topbarLoggingOut = $state(false);

	function handleSignOut() {
		if (topbarLoggingOut) return;
		accountMenuOpen = false;
		topbarLogoutError = null;
		navigationGuard.requestNavigation(async () => {
			topbarLoggingOut = true;
			try {
				await onLogout?.();
			} catch (err: any) {
				topbarLogoutError = errorMessage(err, t('auth.settings.logoutFailed')) || 'Sign out failed';
			} finally {
				topbarLoggingOut = false;
			}
		});
	}
	let drawerPanel: HTMLDivElement | null = $state(null);
	let drawerTrigger: HTMLButtonElement | null = $state(null);

	const themes: { value: ThemePreference; labelKey: string }[] = [
		{ value: 'system', labelKey: 'shell.themeSystem' },
		{ value: 'light', labelKey: 'shell.themeLight' },
		{ value: 'dark', labelKey: 'shell.themeDark' }
	];

	const currentSection = $derived(
		SECTIONS.find((s) => (
			s.route === '/'
				? currentRoute === '/'
				: currentRoute.startsWith(s.route) ||
				  (s.id === 'middlewares' && /^\/(chains|tcp-middlewares)/.test(currentRoute)) ||
				  (s.id === 'routes' && /^\/(routes|tcp-routers|udp-routers)/.test(currentRoute)) ||
				  (s.id === 'services' && /^\/(tcp|udp)-services/.test(currentRoute))
		)) ?? SECTIONS[0]
	);

	function go(section: NavSection) {
		drawerOpen = false;
		navigate(section.route);
	}

	function onDrawerKeydown(e: KeyboardEvent) {
		if (e.key === 'Escape') {
			drawerOpen = false;
			drawerTrigger?.focus();
			return;
		}
		if (e.key !== 'Tab' || !drawerPanel) return;
		const focusables = drawerPanel.querySelectorAll<HTMLElement>(
			'button:not([disabled]), [href], input, select, textarea'
		);
		if (focusables.length === 0) return;
		const first = focusables[0];
		const last = focusables[focusables.length - 1];
		if (e.shiftKey && document.activeElement === first) {
			e.preventDefault();
			last.focus();
		} else if (!e.shiftKey && document.activeElement === last) {
			e.preventDefault();
			first.focus();
		}
	}

	$effect(() => {
		// Open the drawer with focus on the panel; restore focus on close.
		if (drawerOpen) {
			queueMicrotask(() => drawerPanel?.focus());
		}
	});

	const themeLabel = $derived(
		themes.find((th) => th.value === prefs.themePreference)?.labelKey ?? 'shell.themeSystem'
	);
</script>

<svelte:window
	onkeydown={(e) => {
		if (e.key === 'Escape') {
			themeMenuOpen = false;
			languageMenuOpen = false;
			accountMenuOpen = false;
		}
	}}
/>

<!-- One navigation definition, rendered in the sidebar/rail and the mobile
     drawer. `scope` keeps group label ids unique while both are mounted. -->
{#snippet navGroups(scope: string)}
	<nav class="nav-list" aria-label={t('app.name')}>
		{#each GROUPS as group (group.id)}
			{@const labelId = `${scope}-nav-${group.id}`}
			<div
				class="nav-group"
				class:labelled={!!group.labelKey}
				role={group.labelKey ? 'group' : undefined}
				aria-labelledby={group.labelKey ? labelId : undefined}
			>
				{#if group.labelKey}
					<div class="nav-group-label" id={labelId}>{t(group.labelKey)}</div>
				{/if}
				<ul class="nav-group-items">
					{#each group.items as section (section.id)}
						{@const active = currentSection.id === section.id}
						{@const badge = navBadge(section)}
						<li>
							<button
								type="button"
								class="nav-item"
								class:active
								aria-current={active ? 'page' : undefined}
								title={badge > 0 ? `${t(section.labelKey)} · ${pluginUpdatesLabel}` : t(section.labelKey)}
								onclick={() => go(section)}
							>
								<span class="nav-icon" aria-hidden="true"><section.icon size={18} /></span>
								<span class="nav-text">
									<span class="nav-label">{t(section.labelKey)}</span>
									{#if !section.delivered}
										<span class="nav-tag">{t('shell.notDelivered')}</span>
									{/if}
									{#if badge > 0}
										<span class="sr-only">{pluginUpdatesLabel}</span>
									{/if}
								</span>
								{#if badge > 0}
									<span class="nav-badge numeric" aria-hidden="true">{badge > 99 ? '99+' : badge}</span>
								{/if}
							</button>
						</li>
					{/each}
				</ul>
			</div>
		{/each}
	</nav>
{/snippet}

{#snippet shellFooter()}
	<div class="shell-footer">
		{#if consoleUpdate}
			<!-- A newer release: one card with both versions and a link to what changed. -->
			<a
				class="update-link"
				href={consoleUpdate.url}
				target="_blank"
				rel="noopener noreferrer"
				title={t('shell.updateAvailableHint', { version: consoleUpdate.version })}
				aria-label={t('shell.updateAvailableHint', { version: consoleUpdate.version })}
			>
				<span class="update-icon" aria-hidden="true"><CircleArrowUp size={16} /></span>
				<span class="update-body">
					<span class="update-title">{t('shell.updateTitle')}</span>
					<span class="update-versions numeric">
						<span class="update-from">v{APP_VERSION}</span>
						<ArrowRight size={12} aria-hidden="true" />
						<span class="update-to">v{consoleUpdate.version}</span>
					</span>
					<span class="update-cta">{t('shell.updateCta')}<ExternalLink size={12} aria-hidden="true" /></span>
				</span>
			</a>
		{:else}
			<a
				class="release-link"
				href={RELEASES_URL}
				target="_blank"
				rel="noopener noreferrer"
				title={t('shell.releaseNotes', { version: APP_VERSION })}
				aria-label={t('shell.releaseNotes', { version: APP_VERSION })}
			>
				<span class="release-icon" aria-hidden="true"><Tag size={14} /></span>
				<span class="release-label">{t('shell.version')}</span>
				<span class="release-version numeric">v{APP_VERSION}</span>
			</a>
		{/if}
	</div>
{/snippet}

<ConfirmDialog
 open={confirmPending}
 title={t('navigation.leaveTitle')}
 message={t('navigation.leaveBody')}
 cancelLabel={t('navigation.keepEditing')}
 confirmLabel={t('navigation.discard')}
 oncancel={() => navigationGuard.cancelDiscard()}
 onconfirm={() => navigationGuard.confirmDiscard()}
/>

<FlashHost />

<div class="app-layout">
	<a
		class="skip-link"
		href="#main-content"
		onclick={(e) => {
			e.preventDefault();
			const main = document.getElementById('main-content');
			if (main) {
				main.setAttribute('tabindex', '-1');
				main.focus();
			}
		}}
		onkeydown={(e) => {
			if (e.key === 'Enter' || e.key === ' ') {
				e.preventDefault();
				const main = document.getElementById('main-content');
				if (main) {
					main.setAttribute('tabindex', '-1');
					main.focus();
				}
			}
		}}
	>
		{t('app.skipToContent')}
	</a>

	{#if banner}
		<div class="banner-area">
			{@render banner()}
		</div>
	{/if}

	<div class="shell-body">
		<!-- Desktop sidebar / tablet rail -->
		<aside class="sidebar" class:rail aria-label={t('shell.sidebar')}>
			<div class="brand-row">
				<span class="brand-mark" aria-hidden="true"><TrafficMark size={22} /></span>
				<span class="brand-title">Traefik Console</span>
				{#if wide}
					{@const label = t(prefs.sidebarCollapsed ? 'shell.expandSidebar' : 'shell.collapseSidebar')}
					<button
						type="button"
						class="sidebar-toggle"
						aria-label={label}
						aria-expanded={!prefs.sidebarCollapsed}
						title={label}
						onclick={() => setSidebarCollapsed(!prefs.sidebarCollapsed)}
					>
						{#if prefs.sidebarCollapsed}<PanelLeftOpen size={18} aria-hidden="true" />{:else}<PanelLeftClose size={18} aria-hidden="true" />{/if}
					</button>
				{/if}
			</div>
			{@render navGroups('sidebar')}
			{@render shellFooter()}
		</aside>

		<!-- Mobile drawer. The backdrop is a button on purpose: pointer dismissal
		     only; keyboard dismissal is Escape and the drawer close control. -->
		{#if drawerOpen}
			<button
				type="button"
				class="drawer-backdrop"
				aria-label={t('shell.closeMenu')}
				tabindex="-1"
				onclick={() => { drawerOpen = false; drawerTrigger?.focus(); }}
			></button>
			<div
				class="drawer"
				role="dialog"
				aria-modal="true"
				aria-label={t('shell.openMenu')}
				tabindex="-1"
				bind:this={drawerPanel}
				onkeydown={onDrawerKeydown}
			>
				<div class="drawer-head">
					<div class="flex items-center gap-6">
						<TrafficMark size={22} />
						<span class="brand-title">Traefik Console</span>
					</div>
					<button
						type="button"
						class="icon-btn"
						aria-label={t('shell.closeMenu')}
						onclick={() => { drawerOpen = false; drawerTrigger?.focus(); }}
					>
						<X size={20} aria-hidden="true" />
					</button>
				</div>
				{@render navGroups('drawer')}
				{@render shellFooter()}
			</div>
		{/if}

		<div class="content-area">
			<header class="topbar">
				<div class="topbar-left">
					<button
						type="button"
						class="icon-btn hamburger"
						aria-label={t('shell.openMenu')}
						aria-expanded={drawerOpen}
						bind:this={drawerTrigger}
						onclick={() => { drawerOpen = !drawerOpen; }}
					>
						<Menu size={20} aria-hidden="true" />
					</button>
					<nav class="breadcrumbs text-sm" aria-label={t('shell.breadcrumbHome')}>
						<span class="crumb-root">{t('shell.breadcrumbHome')}</span>
						<span class="crumb-sep" aria-hidden="true">/</span>
						<span aria-current="page">{t(currentSection.labelKey)}</span>
					</nav>
				</div>
				<div class="topbar-actions">
					<a class="snapshot-link text-sm" href="#/changes/snapshots/new" aria-label={t('shell.createSnapshot')} title={t('shell.createSnapshotHint')}>
						<Camera size={16} aria-hidden="true" />
						<span class="snapshot-label">{t('shell.createSnapshot')}</span>
					</a>

					<div class="menu-anchor">
						<button
							type="button"
							class="icon-btn theme-btn"
							aria-label={t('shell.theme')}
							aria-expanded={themeMenuOpen}
							onclick={() => { themeMenuOpen = !themeMenuOpen; languageMenuOpen = false; accountMenuOpen = false; }}
						>
							<span aria-hidden="true">{getResolvedTheme() === 'dark' ? '🌙' : '☀️'}</span>
						</button>
						{#if themeMenuOpen}
							<div class="menu" role="menu" aria-label={t('shell.theme')}>
								{#each themes as th (th.value)}
									<button
										type="button"
										role="menuitemradio"
										aria-checked={prefs.themePreference === th.value}
										onclick={() => { setThemePreference(th.value); themeMenuOpen = false; }}
									>
										{t(th.labelKey)}{#if prefs.themePreference === th.value}<span aria-hidden="true"> ✓</span>{/if}
									</button>
								{/each}
							</div>
						{/if}
					</div>

					<div class="menu-anchor">
						<button
							type="button"
							class="icon-btn lang-btn"
							aria-label={`${t('shell.language')}: ${prefs.language.toUpperCase()}`}
							aria-expanded={languageMenuOpen}
							onclick={() => { languageMenuOpen = !languageMenuOpen; themeMenuOpen = false; accountMenuOpen = false; }}
						>
							<span class="text-sm">{prefs.language.toUpperCase()}</span>
						</button>
						{#if languageMenuOpen}
							<div class="menu" role="menu" aria-label={t('shell.language')}>
								<button
									type="button"
									role="menuitemradio"
									aria-checked={prefs.language === 'en'}
									onclick={() => { setLanguage('en'); languageMenuOpen = false; }}
								>
									{t('shell.languageEn')}{#if prefs.language === 'en'}<span aria-hidden="true"> ✓</span>{/if}
								</button>
								<button
									type="button"
									role="menuitemradio"
									aria-checked={prefs.language === 'fr'}
									onclick={() => { setLanguage('fr'); languageMenuOpen = false; }}
								>
									{t('shell.languageFr')}{#if prefs.language === 'fr'}<span aria-hidden="true"> ✓</span>{/if}
								</button>
							</div>
						{/if}
					</div>

					<!-- The account menu exists when the host provides sign-out (always, once signed in). -->
					{#if onLogout}
					<div class="menu-anchor">
						<button
							type="button"
							class="icon-btn"
							aria-label={t('shell.account')}
							aria-expanded={accountMenuOpen}
							onclick={() => { accountMenuOpen = !accountMenuOpen; themeMenuOpen = false; languageMenuOpen = false; }}
						>
							<CircleUserRound size={18} aria-hidden="true" />
						</button>
						{#if accountMenuOpen}
							<div class="menu" role="menu" aria-label={t('shell.account')}>
								{#if username}<div class="menu-user" role="presentation">{t('shell.signedInAs', { name: username })}</div>{/if}
								<button
									type="button"
									role="menuitem"
									onclick={() => { accountMenuOpen = false; navigate('/settings/security'); }}
								>
									{t('nav.security')}
								</button>
								<button
									type="button"
									role="menuitem"
									onclick={handleSignOut}
								>
									{t('shell.signOut')}
								</button>
							</div>
						{/if}
					</div>
					{/if}
				</div>
			</header>

			<main id="main-content" class="main-content">
				{#if configState}
					<div class="state-banners" aria-live="polite">
						{#if configState.recovery_operation}
							<Notice type="danger" title={t('shell.recoveryTitle')}>
								<p class="state-text">{t('shell.recoveryHelp')}</p>
								<a class="btn-secondary btn-sm" href={'#/changes/history/' + configState.recovery_operation.id}>{t('shell.recoveryAction')}</a>
							</Notice>
						{/if}
						{#if configState.drifted}
							<Notice type="warning" title={t('shell.driftTitle')}>
								<p class="state-text">{t('shell.driftHelp')}</p>
								{#if configState.drifted_files?.length}<ul class="state-files">{#each configState.drifted_files as f}<li><code>{f}</code></li>{/each}</ul>{/if}
								<button type="button" class="btn-secondary btn-sm" onclick={adopt} disabled={adopting || configState.running_operation}>{adopting ? t('shell.adopting') : t('shell.adopt')}</button>
								{#if adoptError}<span class="state-error" role="alert">{adoptError}</span>{/if}
							</Notice>
						{/if}
						{#if configState.restart_required}
							<Notice type="warning" title={t('shell.restartTitle')} message={t('shell.restartHelp')} />
						{/if}
						{#if archivedCount > 0 && !archivedDismissed}
							<Notice type="info" title={t('shell.archivedTitle')} message={t('shell.archivedHelp', { count: archivedCount })} dismissible={true} onDismiss={() => { archivedDismissed = true; }} />
						{/if}
					</div>
				{/if}
				{#if topbarLogoutError}
					<div class="topbar-error mb-16" role="alert">
						<Notice type="danger" message={topbarLogoutError} dismissible={true} onDismiss={() => { topbarLogoutError = null; }} />
					</div>
				{/if}
				{#if !currentSection.delivered && !currentRoute.startsWith('/foundation') && currentSection.id !== 'settings'}
					<div class="card undelivered-card">
						<div class="undelivered-head">
							<span class="undelivered-icon" aria-hidden="true"><currentSection.icon size={28} /></span>
							<div>
								<h1 class="text-xl">{t(currentSection.labelKey)}</h1>
								<p class="text-sm text-muted mt-4">{t('shell.notDelivered')}</p>
							</div>
						</div>
						<div class="mt-16">
							<div class="notice tone-info" role="status">
								<div class="head">
									<strong class="title notice-title">{t('shell.notDelivered')}</strong>
								</div>
								<div class="body notice-message">
									{t('shell.notDeliveredHint')}
								</div>
							</div>
						</div>
					</div>
				{:else}
					{@render children()}
				{/if}
			</main>
		</div>
	</div>
</div>

<style>
	.app-layout {
		min-height: 100vh;
		display: flex;
		flex-direction: column;
	}
	.banner-area {
		width: 100%;
		flex-shrink: 0;
	}
	.shell-body {
		display: flex;
		flex: 1;
		min-width: 0;
		position: relative;
	}
	.skip-link {
		position: absolute;
		left: -9999px;
		top: 0;
		background: var(--surface);
		color: var(--text);
		padding: 8px 16px;
		z-index: 100;
	}
	.skip-link:focus {
		left: 0;
	}

	/* Sidebar: desktop 232px, tablet 64px rail */
	.sidebar {
		position: sticky;
		top: 0;
		height: 100vh;
		width: var(--sidebar-w);
		background: var(--surface);
		border-right: 1px solid var(--border);
		display: flex;
		flex-direction: column;
		z-index: 30;
		flex-shrink: 0;
		transition: width 160ms ease;
	}
	.sidebar-toggle {
		display: grid;
		place-items: center;
		flex: none;
		width: 32px;
		height: 32px;
		margin-left: auto;
		padding: 0;
		border: none;
		border-radius: var(--radius-control);
		background: transparent;
		color: var(--text-muted);
		cursor: pointer;
	}
	.sidebar-toggle:hover {
		background: var(--surface-muted);
		color: var(--text);
	}
	.sidebar-toggle:focus-visible {
		outline: 2px solid var(--outline);
		outline-offset: 2px;
	}
	.sidebar.rail .sidebar-toggle {
		margin-left: 0;
	}
	.brand-row {
		display: flex;
		align-items: center;
		gap: 8px;
		min-height: var(--topbar-h);
		padding: 0 10px 0 16px;
		border-bottom: 1px solid var(--border);
		margin-bottom: 12px;
	}
	.brand-mark {
		color: var(--accent);
		display: inline-flex;
	}
	.brand-title {
		font-weight: 600;
		font-size: 16px;
		white-space: nowrap;
		overflow: hidden;
		text-overflow: ellipsis;
	}
	/* Grouped navigation: titled groups, accepted accent-soft active state. */
	.nav-list {
		display: flex;
		flex-direction: column;
		gap: 18px;
		padding: 4px 8px 16px;
		overflow-y: auto;
		flex: 1;
		min-height: 0;
	}
	.nav-group {
		display: flex;
		flex-direction: column;
		gap: 6px;
	}
	.nav-group-label {
		display: flex;
		align-items: center;
		gap: 8px;
		padding: 0 4px 0 12px;
		font-size: 11px;
		line-height: 16px;
		font-weight: 600;
		letter-spacing: 0.08em;
		text-transform: uppercase;
		color: var(--text-muted);
		white-space: nowrap;
	}
	.nav-group-label::after {
		content: '';
		flex: 1;
		height: 1px;
		background: var(--border);
	}
	.nav-group-items {
		position: relative;
		display: flex;
		flex-direction: column;
		gap: 2px;
		list-style: none;
		margin: 0;
		padding: 0;
	}
	.nav-item {
		position: relative;
		display: flex;
		align-items: center;
		gap: 10px;
		background: transparent;
		border: none;
		border-radius: var(--radius-control);
		color: var(--text-muted);
		padding: 8px 12px;
		min-height: var(--touch-h);
		cursor: pointer;
		text-align: left;
		width: 100%;
		transition: background-color 150ms ease, color 150ms ease;
	}
	.nav-item:hover {
		background: var(--surface-muted);
		color: var(--text);
	}
	.nav-item.active {
		background: var(--accent-soft);
		color: var(--accent);
		font-weight: 500;
	}
	.nav-icon {
		display: inline-flex;
		flex-shrink: 0;
	}
	.nav-text {
		display: flex;
		flex-direction: column;
		align-items: flex-start;
		min-width: 0;
		flex: 1;
		gap: 2px;
	}
	.nav-label {
		font-size: 14px;
		line-height: 18px;
		word-break: break-word;
	}
	.nav-tag {
		font-size: 10px;
		line-height: 14px;
		padding: 0 4px;
		border-radius: 3px;
		background: var(--surface-muted);
		color: var(--text-muted);
		border: 1px solid var(--border);
		white-space: nowrap;
	}
	@media (prefers-reduced-motion: reduce) {
		.sidebar,
		.nav-item {
			transition: none;
		}
	}
	/* Footer: the release version link required by APP-SHELL.md. */
	.shell-footer {
		margin-top: auto;
		display: flex;
		flex-direction: column;
		gap: 12px;
		padding: 12px 16px 16px;
		border-top: 1px solid var(--border);
	}
	/* Count badge beside a navigation item (plugin updates). */
	.nav-badge {
		flex-shrink: 0;
		min-width: 20px;
		height: 20px;
		padding: 0 6px;
		border-radius: 10px;
		background: var(--accent);
		color: var(--on-accent);
		font-size: 11px;
		line-height: 20px;
		font-weight: 600;
		text-align: center;
	}
	/* Version: a quiet row when current, a card with both versions when a release is newer. */
	.release-link {
		display: flex;
		align-items: center;
		gap: 8px;
		padding: 8px 10px;
		border: 1px solid var(--border);
		border-radius: var(--radius-control);
		color: var(--text-muted);
		font-size: 12px;
		line-height: 18px;
		text-decoration: none;
	}
	.release-link:hover {
		border-color: var(--outline);
		color: var(--text);
	}
	.release-icon {
		display: inline-flex;
		color: var(--connection);
	}
	.release-label {
		flex: 1;
		min-width: 0;
	}
	.release-version {
		padding: 0 6px;
		border-radius: 4px;
		background: var(--surface-muted);
		color: var(--text);
		font-family: ui-monospace, SFMono-Regular, Consolas, monospace;
		font-size: 11.5px;
		font-weight: 500;
	}
	.update-link {
		display: flex;
		align-items: flex-start;
		gap: 10px;
		padding: 10px 12px;
		border: 1px solid color-mix(in srgb, var(--accent) 45%, transparent);
		border-radius: var(--radius-control);
		background: var(--accent-soft);
		color: var(--text);
		font-size: 12px;
		line-height: 18px;
		text-decoration: none;
	}
	.update-link:hover {
		border-color: var(--accent);
	}
	.update-icon {
		position: relative;
		display: grid;
		place-items: center;
		flex: none;
		width: 26px;
		height: 26px;
		border-radius: 50%;
		background: var(--accent);
		color: var(--on-accent);
	}
	.update-body {
		display: flex;
		flex-direction: column;
		gap: 2px;
		min-width: 0;
	}
	.update-title {
		font-weight: 600;
		font-size: 13px;
	}
	.update-versions {
		display: inline-flex;
		align-items: center;
		gap: 4px;
		font-family: ui-monospace, SFMono-Regular, Consolas, monospace;
		font-size: 11.5px;
		color: var(--text-muted);
	}
	.update-to {
		color: var(--accent);
		font-weight: 600;
	}
	.update-cta {
		display: inline-flex;
		align-items: center;
		gap: 4px;
		margin-top: 2px;
		color: var(--accent);
		font-weight: 500;
	}
	.update-link:hover .update-cta {
		text-decoration: underline;
	}

	.content-area {
		flex: 1;
		min-width: 0;
		min-height: 100vh;
		display: flex;
		flex-direction: column;
	}

	.topbar {
		height: var(--topbar-h);
		display: flex;
		align-items: center;
		justify-content: space-between;
		gap: 16px;
		padding: 0 32px;
		background: var(--surface);
		border-bottom: 1px solid var(--border);
		position: sticky;
		top: 0;
		z-index: 20;
	}
	.topbar-left {
		display: flex;
		align-items: center;
		gap: 12px;
		min-width: 0;
	}
	/* Phones only. Scoped under .topbar so the shared .icon-btn rule (declared later, same
	   specificity) cannot show it on wider screens. */
	.topbar .hamburger {
		display: none;
	}
	.breadcrumbs {
		display: flex;
		align-items: center;
		gap: 8px;
		color: var(--text-muted);
		white-space: nowrap;
		overflow: hidden;
	}
	.breadcrumbs [aria-current] {
		color: var(--text);
	}
	.crumb-sep {
		color: var(--border);
	}
	.topbar-actions {
		display: flex;
		align-items: center;
		gap: 8px;
	}
	.state-banners {
		display: flex;
		flex-direction: column;
		gap: 12px;
		margin-bottom: 16px;
	}
	/* No banner to show: no empty band above the page. */
	.state-banners:not(:has(*)) {
		display: none;
	}
	.state-text { margin: 0 0 8px; }
	.state-files {
		margin: 8px 0;
		padding-left: 20px;
		font-size: 13px;
	}
	.state-error {
		margin-left: 8px;
		color: var(--danger, inherit);
		font-size: 13px;
	}
	.snapshot-link {
		display: inline-flex;
		align-items: center;
		gap: 6px;
		color: var(--text);
		border: 1px solid var(--border);
		border-radius: var(--radius-control);
		padding: 6px 10px;
		min-height: var(--touch-h);
		white-space: nowrap;
	}
	.snapshot-link:hover {
		background: var(--surface-muted);
		text-decoration: none;
	}
	.icon-btn {
		background: transparent;
		border: 1px solid transparent;
		color: var(--text);
		min-width: var(--touch-h);
		min-height: var(--touch-h);
		border-radius: var(--radius-control);
		cursor: pointer;
		display: inline-flex;
		align-items: center;
		justify-content: center;
	}
	.icon-btn:hover {
		background: var(--surface-muted);
	}
	.icon-btn[aria-expanded='true'] {
		background: var(--surface-muted);
	}
	.lang-btn {
		border: 1px solid var(--border);
	}
	.menu-anchor {
		position: relative;
	}
	.menu {
		position: absolute;
		right: 0;
		top: calc(100% + 4px);
		background: var(--surface);
		border: 1px solid var(--border);
		border-radius: var(--radius-control);
		box-shadow: 0 8px 24px rgba(0, 0, 0, 0.18);
		min-width: 160px;
		padding: 4px;
		z-index: 50;
	}
	.menu button {
		display: block;
		width: 100%;
		text-align: left;
		background: transparent;
		border: none;
		color: var(--text);
		padding: 10px 12px;
		font-size: 14px;
		border-radius: var(--radius-control);
		cursor: pointer;
		min-height: var(--touch-h);
	}
	.menu button:hover {
		background: var(--surface-muted);
	}
	.menu-user {
		padding: 8px 12px 10px;
		margin-bottom: 4px;
		border-bottom: 1px solid var(--border);
		color: var(--text-muted);
		font-size: 13px;
		white-space: nowrap;
	}

	.main-content {
		padding: 24px;
		max-width: none;
		width: 100%;
		margin: 0 auto;
		flex: 1;
	}

	/* Drawer (mobile) */
	.drawer-backdrop {
		display: none;
	}
	.drawer {
		display: none;
	}

	/* Icon rail: tablets always, wide screens when collapsed. */
	.sidebar.rail {
		width: 64px;
	}
	.sidebar.rail .brand-title,
	.sidebar.rail .release-label,
	.sidebar.rail .release-version,
	.sidebar.rail .update-body {
		display: none;
	}
	.sidebar.rail .release-link,
	.sidebar.rail .update-link {
		justify-content: center;
		padding: 8px;
		border-color: transparent;
	}
	.sidebar.rail .update-link {
		background: transparent;
	}
	.sidebar.rail .shell-footer {
		padding: 12px 8px;
		align-items: center;
	}
	/* Rail: the nav badge sits on the icon's top-right corner. */
	.sidebar.rail .nav-badge {
		position: absolute;
		top: 2px;
		right: 6px;
		min-width: 16px;
		height: 16px;
		line-height: 16px;
		padding: 0 4px;
		font-size: 10px;
		box-shadow: 0 0 0 2px var(--surface);
	}
	.sidebar.rail .brand-row {
		flex-direction: column;
		justify-content: center;
		gap: 6px;
		padding: 12px 8px;
	}
	.sidebar.rail .nav-item {
		justify-content: center;
		padding: 10px 8px;
	}
	/* Rail: group names stay available to assistive tech; groups are
	   separated visually by a short divider instead. */
	.sidebar.rail .nav-list {
		gap: 8px;
	}
	.sidebar.rail .nav-group-label {
		position: absolute;
		width: 1px;
		height: 1px;
		overflow: hidden;
		clip: rect(0 0 0 0);
		white-space: nowrap;
	}
	.sidebar.rail .nav-group.labelled::before {
		content: '';
		width: 24px;
		height: 1px;
		margin: 0 auto 2px;
		background: var(--border);
	}
	.sidebar.rail .nav-text {
		position: absolute;
		width: 1px;
		height: 1px;
		overflow: hidden;
		clip: rect(0 0 0 0);
	}

	@media (max-width: 1199px) {
		.topbar {
			padding: 0 16px;
			height: 56px;
		}
		.main-content {
			padding: 24px 16px;
		}
	}

	@media (max-width: 767px) {
		.sidebar {
			display: none;
		}
		.topbar .hamburger {
			display: inline-flex;
		}
		.snapshot-link span.snapshot-label {
			display: none;
		}
		.main-content {
			padding: 16px;
		}
		.drawer-backdrop {
			display: block;
			position: fixed;
			inset: 0;
			background: rgba(0, 0, 0, 0.45);
			border: none;
			padding: 0;
			z-index: 60;
		}
		.drawer {
			display: flex;
			flex-direction: column;
			position: fixed;
			top: 0;
			left: 0;
			bottom: 0;
			width: min(280px, calc(100vw - 32px));
			background: var(--surface);
			border-right: 1px solid var(--border);
			z-index: 61;
			outline: none;
		}
		.drawer-head {
			display: flex;
			align-items: center;
			justify-content: space-between;
			padding: 16px;
			border-bottom: 1px solid var(--border);
		}
		.drawer .brand-title {
			display: block;
			font-weight: 600;
			font-size: 16px;
		}
		.drawer .nav-list {
			flex: 1;
		}
		.drawer .nav-item {
			justify-content: flex-start;
			padding: 8px 12px;
		}
		.drawer .nav-text {
			position: static;
			width: auto;
			height: auto;
			clip: auto;
			overflow: visible;
		}
	}

	.undelivered-card {
		max-width: 720px;
	}
	.undelivered-head {
		display: flex;
		align-items: center;
		gap: 16px;
	}
	.undelivered-icon {
		display: inline-flex;
		align-items: center;
		justify-content: center;
		width: 48px;
		height: 48px;
		border-radius: var(--radius-control);
		background: var(--surface-muted);
		color: var(--accent);
	}
</style>

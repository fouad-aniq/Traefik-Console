<script lang="ts">
	import { onMount } from 'svelte';
	import ConsoleShell from './shell/ConsoleShell.svelte';
	import { createNavigation } from './shell/navigation';
	import { initPreferences } from './shared/preferences/preferences.svelte';
	import ResolversPage from './features/resolvers/ResolversPage.svelte';
	import EntryPointsPage from './features/entrypoints/EntryPointsPage.svelte';
	import PluginsPage from './features/plugins/PluginsPage.svelte';
	import CertificatesPage from './features/certificates/CertificatesPage.svelte';
	import DomainsPage from './features/domains/DomainsPage.svelte';
	import ServicesPage from './features/services/ServicesPage.svelte';
	import MiddlewaresPage from './features/middlewares/MiddlewaresPage.svelte';
	import RoutesPage from './features/routes/RoutesPage.svelte';
	import ChangesPage from './features/changes/ChangesPage.svelte';
	import CrowdSecPage from './features/crowdsec/CrowdSecPage.svelte';
	import TopologyPage from './features/topology/TopologyPage.svelte';
	import PageHeader from './shared/ui/PageHeader.svelte';
	import Notice from './shared/ui/Notice.svelte';
	import { t } from './shared/localization/i18n';
	import SetupPage from './features/auth/SetupPage.svelte';
	import LoginPage from './features/auth/LoginPage.svelte';
	import UnavailablePage from './features/auth/UnavailablePage.svelte';
	import SecurityPage, { type SecurityTab, type SecurityMode } from './features/auth/security/SecurityPage.svelte';
	import { auth, bootstrap, signOut } from './features/auth/session.svelte';

	// The server decides which screen is shown (setup, sign-in or the console);
	// the hash only selects a page once signed in.
	let currentRoute = $state(window.location.hash.slice(1) || '/');
	const navigation = createNavigation((path) => { currentRoute = path; });
	const navigate = navigation.navigate;

	/** Where to go after signing in: the page the user asked for, never an auth screen. */
	let returnTo = '/';

	onMount(() => {
		const teardown = initPreferences();
		const stop = navigation.start();
		void bootstrap();
		return () => { teardown(); stop(); };
	});

	const authPath = (r: string) => /^\/(login|setup)(\?|$)/.test(r);
	const externalError = $derived(new URLSearchParams(currentRoute.split('?')[1] ?? '').get('error') ?? '');

	// Keep the address bar truthful about the screen on display.
	$effect(() => {
		const phase = auth.phase;
		const r = currentRoute;
		if (phase === 'setup' && !r.startsWith('/setup')) {
			if (!authPath(r)) returnTo = r;
			navigate('/setup');
		} else if (phase === 'login' && !r.startsWith('/login')) {
			if (!authPath(r)) returnTo = r;
			navigate('/login');
		} else if (phase === 'ready' && authPath(r)) {
			const target = returnTo;
			returnTo = '/';
			navigate(target);
		}
	});

	const route = $derived.by(() => {
		const r = currentRoute.split('?')[0];
		if (r === '/resolvers') return { page: 'resolvers', mode: 'list', name: '' };
		if (r === '/resolvers/new') return { page: 'resolvers', mode: 'create', name: '' };
		const edit = r.match(/^\/resolvers\/([^/]+)\/edit$/);
		if (edit) return { page: 'resolvers', mode: 'edit', name: decodeURIComponent(edit[1]) };
		const view = r.match(/^\/resolvers\/([^/]+)$/);
		if (view) return { page: 'resolvers', mode: 'view', name: decodeURIComponent(view[1]) };
		const entryPoint = r.match(/^\/entrypoints(?:\/(new)|\/([^/]+)(\/edit)?)?$/);
		if (entryPoint) {
			const [, isNew, seg, edit] = entryPoint;
			if (isNew) return { page: 'entrypoints', mode: 'create', name: '' };
			if (seg) return { page: 'entrypoints', mode: edit ? 'edit' : 'view', name: decodeURIComponent(seg) };
			return { page: 'entrypoints', mode: 'list', name: '' };
		}
		if (r === '/certificates') return { page: 'certificates', tab: 'automatic', mode: 'list', name: '' };
		const certs = r.match(/^\/certificates\/(automatic|uploaded)(?:\/([^/]+))?(?:\/(replace))?$/);
		if (certs) {
			const [, tab, seg, action] = certs;
			if (!seg) return { page: 'certificates', tab, mode: 'list', name: '' };
			if (tab === 'uploaded' && seg === 'new' && !action) return { page: 'certificates', tab, mode: 'create', name: '' };
			return { page: 'certificates', tab, mode: action ? 'replace' : 'view', name: decodeURIComponent(seg) };
		}
		if (r === '/domains') return { page: 'domains', mode: 'list', name: '' };
		if (r === '/domains/new') return { page: 'domains', mode: 'create', name: '' };
		const dEdit = r.match(/^\/domains\/([^/]+)\/edit$/);
		if (dEdit) return { page: 'domains', mode: 'edit', name: decodeURIComponent(dEdit[1]) };
		const dView = r.match(/^\/domains\/([^/]+)$/);
		if (dView) return { page: 'domains', mode: 'view', name: decodeURIComponent(dView[1]) };
		// HTTP and TCP middlewares share one list; TCP ones open under /tcp-middlewares.
		const mwRoute = r.match(/^\/(middlewares|tcp-middlewares|chains)(?:\/(new)|\/([^/]+)(\/edit)?)?$/);
		if (mwRoute) {
			const [, base, isNew, seg, edit] = mwRoute;
			const tab = base === 'chains' ? 'chains' : 'middlewares';
			const protocol: 'http' | 'tcp' = base === 'tcp-middlewares' ? 'tcp' : 'http';
			if (isNew) return { page: 'middlewares', tab, protocol, mode: 'create', name: '' };
			if (seg) return { page: 'middlewares', tab, protocol, mode: edit ? 'edit' : 'view', name: decodeURIComponent(seg) };
			return { page: 'middlewares', tab, protocol, mode: 'list', name: '' };
		}
		// HTTP services live under /services, TCP and UDP ones under their own bases (one tab each).
		const svcRoute = r.match(/^\/(services|tcp-services|udp-services)(?:\/(new)|\/([^/]+)(\/edit)?)?$/);
		if (svcRoute) {
			const [, base, isNew, seg, edit] = svcRoute;
			const tab = base === 'services' ? 'http' : base.slice(0, 3);
			if (isNew) return { page: 'services', tab, mode: 'create', name: '' };
			if (seg) return { page: 'services', tab, mode: edit ? 'edit' : 'view', name: decodeURIComponent(seg) };
			return { page: 'services', tab, mode: 'list', name: '' };
		}
		// `/routes` is the old address of this page; it keeps working.
		// One list for every protocol: TCP and UDP routers open their own dialog under their own base.
		const routeMatch = r.match(/^\/(routers|routes|tcp-routers|udp-routers)(?:\/(new)|\/([^/]+)(\/edit)?)?$/);
		if (routeMatch) {
			const [, base, isNew, seg, edit] = routeMatch;
			const tab = base === 'tcp-routers' ? 'tcp' : base === 'udp-routers' ? 'udp' : 'http';
			if (isNew) return { page: 'routes', tab, mode: 'create', name: '' };
			if (seg) return { page: 'routes', tab, mode: edit ? 'edit' : 'view', name: decodeURIComponent(seg) };
			return { page: 'routes', tab, mode: 'list', name: '' };
		}
		const plugin = r.match(/^\/plugins(?:\/(catalog)(?:\/([^/]+)\/(install))?|\/(new)|\/([^/]+)(\/edit|\/update)?)?$/);
		if (plugin) {
			const [, cat, mod, install, isNew, seg, edit] = plugin;
			if (install) return { page: 'plugins', tab: 'catalog', mode: 'create', name: '', module: decodeURIComponent(mod) };
			if (cat) return { page: 'plugins', tab: 'installed', mode: 'create', name: '', module: '' };
			if (isNew) return { page: 'plugins', tab: 'installed', mode: 'create', name: '', module: '' };
			if (seg) return { page: 'plugins', tab: 'installed', mode: edit === '/update' ? 'upgrade' : edit ? 'edit' : 'view', name: decodeURIComponent(seg), module: '' };
			return { page: 'plugins', tab: 'installed', mode: 'list', name: '', module: '' };
		}
		const crowdsec = r.match(/^\/crowdsec(?:\/(decisions\/new|wizard|setup|disable|rotate|settings|overview|alerts|decisions|allowlists|integration|protection))?$/);
		if (crowdsec) {
			const seg = crowdsec[1];
			const tab = seg === 'protection' ? 'integration' : seg || 'overview';
			return { page: 'crowdsec', mode: seg === 'decisions/new' ? 'ban' : seg === 'wizard' || seg === 'setup' || seg === 'disable' || seg === 'rotate' || seg === 'settings' ? seg : 'list', tab };
		}
		const changes = r.match(/^\/changes(?:\/(history)(?:\/(\d+))?|\/snapshots\/(new|\d+)(\/restore)?)?$/);
		if (changes) {
			const [, hist, op, snap, restore] = changes;
			if (hist) return op ? { page: 'changes', tab: 'history', mode: 'operation', id: op } : { page: 'changes', tab: 'history', mode: 'list', id: '' };
			if (snap === 'new') return { page: 'changes', tab: 'snapshots', mode: 'create', id: '' };
			if (snap) return { page: 'changes', tab: 'snapshots', mode: restore ? 'restore' : 'view', id: snap };
			return { page: 'changes', tab: 'snapshots', mode: 'list', id: '' };
		}
		const security = r.match(/^\/settings(?:\/security(?:\/(password|providers|sessions|activity)(?:\/(new)|\/([^/]+)\/edit)?)?)?$/);
		if (security) {
			const [, section, isNew, providerId] = security;
			if (section === 'password') return { page: 'security', tab: 'account', mode: 'password', name: '' };
			if (section === 'providers') return { page: 'security', tab: 'providers', mode: isNew ? 'create' : providerId ? 'edit' : 'list', name: providerId ? decodeURIComponent(providerId) : '' };
			return { page: 'security', tab: section ?? 'account', mode: 'list', name: '' };
		}
		if (r === '/' || r === '/map') return { page: 'map' };
		return { page: 'pending' };
	});
</script>

{#if auth.phase === 'loading'}
	<div class="boot" role="status" aria-live="polite">{t('authScreens.loading')}</div>
{:else if auth.phase === 'unavailable'}
	<UnavailablePage />
{:else if auth.phase === 'setup'}
	<SetupPage />
{:else if auth.phase === 'login'}
	<LoginPage {externalError} />
{:else}
<ConsoleShell {currentRoute} {navigate} onLogout={signOut} username={auth.session?.user.username ?? ''}>
	{#if route.page === 'resolvers'}
		<ResolversPage {navigate} mode={route.mode} name={route.name} />
	{:else if route.page === 'entrypoints'}
		<EntryPointsPage {navigate} mode={route.mode} name={route.name} />
	{:else if route.page === 'domains'}
		<DomainsPage {navigate} mode={route.mode} name={route.name} />
	{:else if route.page === 'services'}
		<ServicesPage {navigate} tab={route.tab} mode={route.mode} name={route.name} />
	{:else if route.page === 'plugins'}
		<PluginsPage {navigate} tab={route.tab} mode={route.mode} name={route.name} module={route.module} />
	{:else if route.page === 'crowdsec'}
		<CrowdSecPage {navigate} mode={route.mode} tab={route.tab} />
	{:else if route.page === 'changes'}
		<ChangesPage {navigate} tab={route.tab} mode={route.mode} id={route.id} />
	{:else if route.page === 'routes'}
		<RoutesPage {navigate} tab={route.tab} mode={route.mode} name={route.name} />
	{:else if route.page === 'middlewares'}
		<MiddlewaresPage {navigate} tab={route.tab} protocol={route.protocol === 'tcp' ? 'tcp' : 'http'} mode={route.mode} name={route.name} />
	{:else if route.page === 'certificates'}
		<CertificatesPage {navigate} tab={route.tab} mode={route.mode} name={route.name} />
	{:else if route.page === 'map'}
		<TopologyPage {navigate} />
	{:else if route.page === 'security'}
		<SecurityPage {navigate} tab={route.tab as SecurityTab} mode={route.mode as SecurityMode} id={route.name} />
	{:else}
		<PageHeader title={t('table.notFoundTitle')} subtitle="" />
		<Notice message={t('table.notFoundHelp')}>
			<button class="btn-primary" onclick={() => navigate('/')}>{t('nav.map')}</button>
		</Notice>
	{/if}
</ConsoleShell>
{/if}

<style>
	.boot { min-height: 100svh; display: grid; place-items: center; color: var(--text-muted); font-size: 14px; }
</style>

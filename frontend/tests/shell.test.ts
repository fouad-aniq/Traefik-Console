import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/svelte';
import { createRawSnippet } from 'svelte';
// The API client binds `fetch` at import time, so install a forwarding stub first.
vi.hoisted(() => {
	(globalThis as any).__mockFetch = async () => { throw new Error('no mock'); };
	globalThis.fetch = ((...args: unknown[]) => (globalThis as any).__mockFetch(...args)) as typeof fetch;
});
import ConsoleShell from '../src/shell/ConsoleShell.svelte';
import { prefs } from '../src/shared/preferences/preferences.svelte';

const page = createRawSnippet(() => ({ render: () => '<p>page</p>' }));
const state = (extra = {}) => ({ static_hash: '', dynamic_hash: '', domains_hash: '', published_static_hash: '', published_dynamic_hash: '', published_domains_hash: '', drifted: false, drifted_files: null, restart_required: false, running_operation: false, archived_drafts: 0, archived_secret_drafts: 0, legacy_snapshots_imported: 0, plugin_updates: 0, ...extra });
let paths: string[] = [];
let configState = state();

function viewport(wide: boolean) {
	window.matchMedia = ((query: string) => ({ matches: query.includes('min-width: 1200px') ? wide : false, media: query, addEventListener() {}, removeEventListener() {}, addListener() {}, removeListener() {}, onchange: null, dispatchEvent: () => false })) as typeof window.matchMedia;
}

beforeEach(() => {
	paths = [];
	configState = state();
	localStorage.clear();
	prefs.sidebarCollapsed = false;
	(globalThis as any).__mockFetch = async (path: string) => {
		paths.push(path);
		return new Response(JSON.stringify(configState), { status: 200, headers: { 'Content-Type': 'application/json' } });
	};
});
afterEach(() => cleanup());

const mount = () => render(ConsoleShell, { currentRoute: '/domains', navigate: () => {}, children: page });

describe('config state polling', () => {
	it('reads the state once on load (no aborted duplicate) and never calls /plugins', async () => {
		viewport(true);
		mount();
		await waitFor(() => expect(paths).toContain('/api/v1/config/state'));
		await new Promise((r) => setTimeout(r, 50));
		expect(paths.filter((p) => p === '/api/v1/config/state')).toHaveLength(1);
		expect(paths.some((p) => p.includes('/plugins'))).toBe(false);
	});
	it('shows the plugin update count as a badge on the Plugins item', async () => {
		viewport(true);
		configState = state({ plugin_updates: 2 });
		const { container } = mount();
		await waitFor(() => expect(container.querySelector('aside .nav-badge')?.textContent).toBe('2'));
		expect(screen.getAllByText('2 plugin updates available').length).toBeGreaterThan(0);
		expect(container.querySelector('.update-notice')).toBeNull();
	});
	it('shows a newer Console release next to the version, linking to its notes', async () => {
		viewport(true);
		configState = state();
		const { container } = mount();
		await waitFor(() => expect(paths).toContain('/api/v1/config/state'));
		expect(container.querySelector('.update-link')).toBeNull();
		configState = state({ console_update: { version: '0.1.0-rc.4', url: 'https://github.com/fouad-aniq/Traefik-Console/releases/tag/v0.1.0-rc.4' } });
		// Coming back to the tab re-reads the state at once instead of waiting for the next poll.
		Object.defineProperty(document, 'visibilityState', { configurable: true, get: () => 'visible' });
		document.dispatchEvent(new Event('visibilitychange'));
		await waitFor(() => expect(container.querySelector('.update-link')).not.toBeNull());
		const link = container.querySelector<HTMLAnchorElement>('.update-link')!;
		expect(link.textContent).toContain('v0.1.0-rc.4 available');
		expect(link.href).toBe('https://github.com/fouad-aniq/Traefik-Console/releases/tag/v0.1.0-rc.4');
		expect(link.target).toBe('_blank');
	});
});

describe('sidebar', () => {
	it('is expanded on wide screens and collapses to the rail, remembering the choice', async () => {
		viewport(true);
		const { container } = mount();
		const aside = container.querySelector('aside.sidebar')!;
		expect(aside.classList.contains('rail')).toBe(false);
		const toggle = screen.getByRole('button', { name: 'Collapse sidebar' });
		expect(toggle.getAttribute('aria-expanded')).toBe('true');
		await fireEvent.click(toggle);
		expect(aside.classList.contains('rail')).toBe(true);
		expect(localStorage.getItem('tc_prefs_sidebar')).toBe('collapsed');
		await fireEvent.click(screen.getByRole('button', { name: 'Expand sidebar' }));
		expect(aside.classList.contains('rail')).toBe(false);
		expect(localStorage.getItem('tc_prefs_sidebar')).toBe('expanded');
	});
	it('starts collapsed when that was the saved choice', () => {
		viewport(true);
		prefs.sidebarCollapsed = true;
		const { container } = mount();
		expect(container.querySelector('aside.sidebar')!.classList.contains('rail')).toBe(true);
	});
	it('is always the rail on narrower screens, without a collapse control', () => {
		viewport(false);
		const { container } = mount();
		expect(container.querySelector('aside.sidebar')!.classList.contains('rail')).toBe(true);
		expect(screen.queryByRole('button', { name: /Collapse sidebar|Expand sidebar/ })).toBeNull();
	});
	it('opens the drawer from the hamburger without a collapse control in it', async () => {
		viewport(false);
		const { container } = mount();
		await fireEvent.click(screen.getByRole('button', { name: 'Open navigation menu' }));
		const drawer = container.querySelector('.drawer')!;
		expect(drawer).toBeTruthy();
		expect(drawer.querySelector('.sidebar-toggle')).toBeNull();
	});
});

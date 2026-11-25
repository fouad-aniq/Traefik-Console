import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/svelte';
// The API client binds `fetch` at import time, so install a forwarding stub first.
vi.hoisted(() => {
	(globalThis as any).__mockFetch = async () => { throw new Error('no mock'); };
	globalThis.fetch = ((...args: unknown[]) => (globalThis as any).__mockFetch(...args)) as typeof fetch;
});
import RoutesPage from '../src/features/routes/RoutesPage.svelte';
import RouteDialog from '../src/features/routes/RouteDialog.svelte';
import ServiceDialog from '../src/features/services/ServiceDialog.svelte';
import { formFrom, isExternalRef, newForm, previewRule, routerDefinition, toInput } from '../src/features/routes/models';
import { routeErrors, subdomainError } from '../src/features/routes/validation';
import { flashes } from '../src/shared/ui/flash.svelte';

const preview = (extra = {}) => ({ changes: [{ action: 'created', id: 'router:my-route', kind: 'router', name: 'my-route', scope: 'dynamic' }], static_diff: '', dynamic_diff: '+r: {}', requires_restart: false, ...extra });
const operation = (extra = {}) => ({ id: 7, kind: 'router.create', target: 'my-route', status: 'succeeded', summary: 'Applied 1 change', requires_restart: false, created_at: 'now', ...extra });
const envelope = (kind: string, name: string, config: unknown, version = 1, extra = {}) => ({ id: `${kind}:${name}`, name, version, origin: 'managed', provider: 'file', status: 'active', read_only: false, live_health: 'loaded', updated_at: '2026-01-01T00:00:00Z', config, ...extra });
const router = (name: string, config: Record<string, unknown> = {}, version = 1, extra = {}) =>
	envelope('router', name, { entry_points: ['websecure'], domains: ['app.example.com'], rule: 'Host(`app.example.com`)', service: 'web', tls: { enabled: true, cert_resolver: 'le' }, ...config }, version, extra);
const domain = envelope('domain', 'main-domain', { name: 'main-domain', main: '*.example.com', method: 'resolver', resolver: 'le', state: 'ready' });
const web = envelope('service', 'web', { servers: [{ url: 'http://app:8080' }] });
const auth = envelope('middleware', 'auth', { type: 'basicAuth' });
const resolver = envelope('resolver', 'le', { challenge: 'http01' });

interface Call { method: string; path: string; headers: Record<string, string>; body?: any }
let calls: Call[] = [];
let handlers: Record<string, () => Response | Promise<Response>> = {};
const json = (body: unknown, status = 200) => new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } });

beforeEach(() => {
	flashes.splice(0);
	calls = [];
	HTMLDialogElement.prototype.showModal ??= function () { this.setAttribute('open', ''); };
	HTMLDialogElement.prototype.close ??= function () { this.removeAttribute('open'); };
	(globalThis as any).__mockFetch = async (path: string, init: RequestInit = {}) => {
		const headers: Record<string, string> = {};
		(init.headers as Headers).forEach((v, k) => { headers[k.toLowerCase()] = v; });
		calls.push({ method: init.method ?? 'GET', path, headers, body: init.body ? JSON.parse(String(init.body)) : undefined });
		const key = `${init.method ?? 'GET'} ${path}`;
		if (handlers[key]) return handlers[key]();
		return json({ code: 'ERR_NOT_FOUND', message: 'nf', operation_id: 'op', retryable: false }, 404);
	};
});
afterEach(() => { cleanup(); handlers = {}; });

const base = (routers: unknown) => ({
	'GET /api/v1/routers': () => json(routers),
	'GET /api/v1/domains': () => json([domain]),
	'GET /api/v1/services': () => json([web]),
	'GET /api/v1/middlewares': () => json([auth]),
	'GET /api/v1/chains': () => json(null),
	'GET /api/v1/resolvers': () => json([resolver]),
	'GET /api/v1/entrypoints': () => json({ entry_points: ['web', 'websecure'] })
});

async function pick(id: string, name: RegExp) {
	await fireEvent.click(await waitFor(() => document.getElementById(id)!));
	const listbox = await waitFor(() => document.getElementById(id + '-listbox')!);
	await fireEvent.click(await within(listbox).findByRole('option', { name }));
}

const messages = { invalidName: 'n', nameTaken: 't', domainRequired: 'd', invalidSubdomain: 's', invalidHosts: 'h', invalidPathPrefix: 'p', ruleRequired: 'r', tooManyEntryPoints: 'e', serviceRequired: 'sv', duplicateMiddleware: 'dm', tooManyMiddlewares: 'tm', invalidPriority: 'pr', invalidTlsOptions: 'o' };
const ctx = { creating: true, existingNames: ['taken'], domainNames: ['main-domain'] };

describe('model and validation', () => {
	it('sends only the fields of the chosen match mode', () => {
		expect(toInput({ ...newForm(), name: 'a', domainName: 'main-domain', subdomain: ' App ', service: 'web', pathPrefix: '/api' }))
			.toEqual({ entry_points: [], domain_name: 'main-domain', subdomain: 'app', path_prefix: '/api', service: 'web' });
		expect(toInput({ ...newForm(), name: 'a', mode: 'host', hosts: 'A.Example.com\napp.example.com', service: 'web', tlsEnabled: true, tlsResolver: 'le', priority: '10', middlewares: ['auth'], entryPoints: ['websecure'] }))
			.toEqual({ entry_points: ['websecure'], domains: ['a.example.com', 'app.example.com'], service: 'web', priority: 10, middlewares: ['auth'], tls: { enabled: true, cert_resolver: 'le' } });
		expect(toInput({ ...newForm(), name: 'a', mode: 'rule', customRule: ' Path(`/x`) ', service: 'web', tlsEnabled: false, pathPrefix: '/ignored' }))
			.toEqual({ entry_points: [], custom_rule: 'Path(`/x`)', service: 'web' });
	});
	it('loads an existing router back into the right mode', () => {
		expect(formFrom('a', { entry_points: null, domains: ['x.example.com'], rule: 'Host(`x.example.com`)', service: 'web', domain_name: 'main-domain', subdomain: 'x' }).mode).toBe('domain');
		const host = formFrom('a', { entry_points: ['web'], domains: ['x.example.com'], rule: 'r', service: 'web', tls: { enabled: true, cert_resolver: 'le' } });
		expect(host).toMatchObject({ mode: 'host', hosts: 'x.example.com', tlsEnabled: true, tlsResolver: 'le' });
		expect(formFrom('a', { entry_points: [], domains: null, rule: 'PathPrefix(`/x`)', custom_rule: 'PathPrefix(`/x`)', service: 'web' })).toMatchObject({ mode: 'rule', customRule: 'PathPrefix(`/x`)', tlsEnabled: false });
	});
	it('previews the rule the way the backend compiles it', () => {
		expect(previewRule({ ...newForm(), domainName: 'm', subdomain: 'app', pathPrefix: 'api' }, '*.example.com')).toBe('Host(`app.example.com`) && PathPrefix(`/api`)');
		expect(previewRule({ ...newForm(), mode: 'host', hosts: 'a.example.com, b.example.com' }, undefined)).toBe('(Host(`a.example.com`) || Host(`b.example.com`))');
	});
	it('validates names, hosts, subdomain, priority and middleware duplicates', () => {
		expect(subdomainError('a.b')).toBe(false);
		expect(subdomainError('-bad')).toBe(true);
		const bad = routeErrors({ ...newForm(), name: 'Bad Name', mode: 'host', hosts: '*.example.com', pathPrefix: 'x', priority: '1.5', middlewares: ['a', 'a'] }, ctx, messages);
		expect(bad).toMatchObject({ name: 'n', hosts: 'h', pathPrefix: 'p', service: 'sv', priority: 'pr', middlewares: 'dm' });
		expect(routeErrors({ ...newForm(), name: 'taken', domainName: 'nope', service: 'web' }, ctx, messages)).toMatchObject({ name: 't', domainName: 'd' });
		expect(routeErrors({ ...newForm(), name: 'ok', domainName: 'main-domain', service: 'web' }, ctx, messages)).toEqual({});
		expect(routeErrors({ ...newForm(), name: 'ok', mode: 'rule', customRule: '', service: 'web' }, ctx, messages)).toMatchObject({ customRule: 'r' });
	});
});

describe('list', () => {
	it('shows rule, service link and TLS', async () => {
		handlers = base([router('shop'), router('blog', { tls: null, service: 'api@docker' })]);
		render(RoutesPage, { navigate: () => {} });
		await waitFor(() => expect(screen.getAllByText('shop')[0]).toBeTruthy());
		expect(screen.getByText('HTTPS · le')).toBeTruthy();
		expect(screen.getByText('HTTP only')).toBeTruthy();
		// Only references to managed services link to the Services page; external ones are plain text.
		expect([...document.querySelectorAll('button.link')].map((b) => b.textContent?.trim())).toEqual(['web']);
	});
	it('shows external routers read-only', async () => {
		handlers = base([router('dash@docker', {}, 1, { origin: 'external', provider: 'docker', read_only: true })]);
		render(RoutesPage, { navigate: () => {} });
		await waitFor(() => expect(screen.getAllByText('dash@docker')[0]).toBeTruthy());
		expect(screen.queryByRole('menuitem', { name: 'Edit' })).toBeNull();
		expect(screen.queryByRole('button', { name: 'Edit' })).toBeNull();
		expect(screen.queryByRole('menuitem', { name: 'Delete' })).toBeNull();
		expect(screen.queryByRole('button', { name: 'Delete' })).toBeNull();
	});
	it('shows the empty state for a null list', async () => {
		handlers = base(null);
		render(RoutesPage, { navigate: () => {} });
		await waitFor(() => expect(screen.getByText('No HTTP routers yet')).toBeTruthy());
	});
	it('deletes with the current version and surfaces a server conflict', async () => {
		handlers = {
			...base([router('shop', {}, 4)]),
			'DELETE /api/v1/routers/shop': () => json({ code: 'ERR_VERSION_CONFLICT', message: 'route changed', operation_id: 'o', retryable: false }, 409)
		};
		render(RoutesPage, { navigate: () => {} });
		await waitFor(() => expect(screen.getAllByText('shop')[0]).toBeTruthy());
		await fireEvent.click(screen.getByRole('menuitem', { name: 'Delete' }));
		await fireEvent.click(await screen.findByRole('button', { name: 'Confirm deletion' }));
		await waitFor(() => expect(screen.getByText(/route changed/)).toBeTruthy());
		expect(calls.find((c) => c.method === 'DELETE')!.headers['if-match']).toBe('"4"');
	});
});

describe('create flow', () => {
	async function openCreate() {
		render(RoutesPage, { navigate: () => {}, mode: 'create' });
		await waitFor(() => expect(document.getElementById('route-name')).toBeTruthy());
	}
	it('previews with a dry run, then applies with a create precondition and one key', async () => {
		handlers = {
			...base([]),
			'PUT /api/v1/routers/my-route?dry_run=true': () => json({ preview: preview(), restart_required: false }),
			'PUT /api/v1/routers/my-route': () => json({ operation: operation(), resource: router('my-route', { domain_name: 'main-domain', subdomain: 'app' }), restart_required: false })
		};
		await openCreate();
		await fireEvent.input(document.getElementById('route-name')!, { target: { value: 'my-route' } });
		await pick('route-domain', /example\.com/);
		await fireEvent.input(document.getElementById('route-subdomain')!, { target: { value: 'app' } });
		expect(screen.getByText('Host(`app.example.com`)')).toBeTruthy();
		await fireEvent.click(screen.getByRole('button', { name: 'Continue' }));
		await pick('route-service', /web/);
		await fireEvent.click(screen.getByRole('button', { name: 'Continue' }));
		await pick('route-add-middleware', /auth/);
		await fireEvent.click(screen.getByRole('button', { name: 'Review change' }));
		await screen.findByRole('button', { name: 'Apply changes' });

		const dry = calls.find((c) => c.method === 'PUT')!;
		expect(dry.path).toBe('/api/v1/routers/my-route?dry_run=true');
		expect(dry.headers['if-none-match']).toBe('*');
		expect(dry.body).toEqual({ entry_points: [], domain_name: 'main-domain', subdomain: 'app', service: 'web', middlewares: ['auth'] });

		await fireEvent.click(await screen.findByRole('button', { name: 'Apply changes' }));
		await waitFor(() => expect(flashes.some((f) => f.title.startsWith('Applied'))).toBe(true));
		const apply = calls.filter((c) => c.method === 'PUT')[1];
		expect(apply.path).toBe('/api/v1/routers/my-route');
		expect(apply.headers['idempotency-key']).toMatch(/^[A-Za-z0-9_-]{16,128}$/);
		expect(calls.some((c) => c.path.includes('/draft'))).toBe(false);
	});
	it('sends explicit hostnames with TLS settings chosen on the route', async () => {
		handlers = { ...base([]), 'PUT /api/v1/routers/plain?dry_run=true': () => json({ preview: preview(), restart_required: false }) };
		await openCreate();
		await fireEvent.input(document.getElementById('route-name')!, { target: { value: 'plain' } });
		await pick('route-mode', /^Hostnames/);
		await fireEvent.input(await waitFor(() => document.getElementById('route-hosts')!), { target: { value: 'App.Example.com' } });
		await fireEvent.input(document.getElementById('route-path')!, { target: { value: '/api' } });
		await fireEvent.click(screen.getByRole('button', { name: 'Continue' }));
		await pick('route-service', /web/);
		await fireEvent.input(document.getElementById('route-priority')!, { target: { value: '20' } });
		await fireEvent.click(screen.getByRole('button', { name: 'Continue' }));
		await pick('route-tls-resolver', /le/);
		await fireEvent.click(screen.getByRole('button', { name: 'Review change' }));
		await waitFor(() => expect(calls.some((c) => c.method === 'PUT')).toBe(true));
		expect(calls.find((c) => c.method === 'PUT')!.body).toEqual({
			entry_points: [], domains: ['app.example.com'], path_prefix: '/api', service: 'web', priority: 20, tls: { enabled: true, cert_resolver: 'le' }
		});
	});
	it('does not call the server while required fields are missing', async () => {
		handlers = base([]);
		await openCreate();
		await fireEvent.input(document.getElementById('route-name')!, { target: { value: 'Bad Name' } });
		// Continue checks the step: it stays on the match step until it is valid.
		await fireEvent.click(screen.getByRole('button', { name: 'Continue' }));
		await waitFor(() => expect(document.getElementById('route-name')!.getAttribute('aria-invalid')).toBe('true'));
		expect(screen.getAllByText('Select one of your domains.').length).toBeGreaterThan(0);
		expect(screen.queryByRole('button', { name: 'Review change' })).toBeNull();
		expect(calls.some((c) => c.method === 'PUT')).toBe(false);
	});
	it('refuses a name already in use', async () => {
		handlers = base([router('taken')]);
		await openCreate();
		await waitFor(() => expect(calls.some((c) => c.path === '/api/v1/routers')).toBe(true));
		await fireEvent.input(document.getElementById('route-name')!, { target: { value: 'taken' } });
		await fireEvent.click(screen.getByRole('button', { name: 'Continue' }));
		await waitFor(() => expect(screen.getAllByText(/already exists/).length).toBeGreaterThan(0));
		expect(calls.some((c) => c.method === 'PUT')).toBe(false);
	});
});

describe('edit and view', () => {
	it('loads an existing router into the form and keeps its saved service even if unlisted', async () => {
		handlers = base([router('shop', { service: 'api@docker' })]);
		render(RoutesPage, { navigate: () => {}, mode: 'edit', name: 'shop' });
		await waitFor(() => expect((document.getElementById('route-name') as HTMLInputElement).value).toBe('shop'));
		expect((document.getElementById('route-name') as HTMLInputElement).readOnly).toBe(true);
		expect((document.getElementById('route-hosts') as HTMLTextAreaElement).value).toBe('app.example.com');
		expect(document.getElementById('route-service')!.textContent).toContain('api@docker');
	});
	it('shows an external router read-only in view mode', async () => {
		handlers = base([router('dash@docker', {}, 1, { origin: 'external', provider: 'docker', read_only: true })]);
		render(RoutesPage, { navigate: () => {}, mode: 'view', name: 'dash@docker' });
		await waitFor(() => expect(screen.getByText(/defined outside the console/)).toBeTruthy());
		expect(screen.queryByRole('button', { name: 'Edit router' })).toBeNull();
	});
});

const PICKERS = ['domains', 'services', 'middlewares', 'chains', 'resolvers', 'entrypoints'];
const paths = () => calls.map((c) => `${c.method} ${c.path}`);
const count = (path: string) => calls.filter((c) => c.method === 'GET' && c.path === path).length;

describe('requests', () => {
	it('reads the routers once, and nothing else, to show the list', async () => {
		handlers = base([router('shop')]);
		render(RoutesPage, { navigate: () => {} });
		await waitFor(() => expect(screen.getAllByText('shop')[0]).toBeTruthy());
		expect(paths()).toEqual(['GET /api/v1/routers']);
	});
	it('opens the create dialog from the list already loaded: the pickers once each, the routers never again', async () => {
		handlers = base([router('shop')]);
		render(RoutesPage, { navigate: () => {}, mode: 'create' });
		await waitFor(() => expect(document.getElementById('route-name')).toBeTruthy());
		await waitFor(() => expect(PICKERS.every((p) => count('/api/v1/' + p) === 1)).toBe(true));
		expect(count('/api/v1/routers')).toBe(1);
		expect(calls).toHaveLength(1 + PICKERS.length);
	});
	it('opens the editor from the list already loaded too', async () => {
		handlers = base([router('shop')]);
		render(RoutesPage, { navigate: () => {}, mode: 'edit', name: 'shop' });
		await waitFor(() => expect((document.getElementById('route-name') as HTMLInputElement).value).toBe('shop'));
		expect(count('/api/v1/routers')).toBe(1);
		expect(calls).toHaveLength(1 + PICKERS.length);
	});
	it('shows a router without any request of its own: the list has everything the view needs', async () => {
		handlers = base([router('shop', { middlewares: ['auth'] })]);
		render(RoutesPage, { navigate: () => {}, mode: 'view', name: 'shop' });
		await waitFor(() => expect(document.querySelector('dialog')!.textContent).toContain('service:'));
		expect(paths()).toEqual(['GET /api/v1/routers']);
	});
	it('does not read the routers again after a review', async () => {
		handlers = { ...base([]), 'PUT /api/v1/routers/plain?dry_run=true': () => json({ preview: preview(), restart_required: false }) };
		render(RoutesPage, { navigate: () => {}, mode: 'create' });
		await waitFor(() => expect(document.getElementById('route-name')).toBeTruthy());
		await fireEvent.input(document.getElementById('route-name')!, { target: { value: 'plain' } });
		await pick('route-mode', /^Hostnames/);
		await fireEvent.input(await waitFor(() => document.getElementById('route-hosts')!), { target: { value: 'app.example.com' } });
		await fireEvent.click(screen.getByRole('button', { name: 'Continue' }));
		await pick('route-service', /web/);
		await fireEvent.click(screen.getByRole('button', { name: 'Continue' }));
		await fireEvent.click(screen.getByRole('button', { name: 'Review change' }));
		await screen.findByRole('button', { name: 'Apply changes' });
		expect(count('/api/v1/routers')).toBe(1);
	});
	it('says so inside the dialog when the list could not be read, and reads it again on retry', async () => {
		let attempts = 0;
		handlers = { ...base([router('shop')]), 'GET /api/v1/routers': () => (++attempts === 1 ? json({ code: 'ERR_INTERNAL', message: 'The list is down.', operation_id: 'o', retryable: true }, 500) : json([router('shop')])) };
		render(RoutesPage, { navigate: () => {}, mode: 'edit', name: 'shop' });
		await waitFor(() => expect(screen.getAllByText(/The list is down/).length).toBeGreaterThan(1));
		await fireEvent.click(within(document.querySelector('dialog')!).getByRole('button', { name: 'Retry' }));
		await waitFor(() => expect((document.getElementById('route-name') as HTMLInputElement).value).toBe('shop'));
		expect(count('/api/v1/routers')).toBe(2);
	});
	it('reads the routers itself when it is opened without a list, as on the map', async () => {
		handlers = base([router('shop')]);
		render(RouteDialog, { mode: 'edit', name: 'shop', navigate: () => {} });
		await waitFor(() => expect((document.getElementById('route-name') as HTMLInputElement).value).toBe('shop'));
		expect(count('/api/v1/routers')).toBe(1);
	});
	it('the service dialog reads the services itself when it is opened without a list, as on the map', async () => {
		handlers = { 'GET /api/v1/services': () => json([envelope('service', 'web', { servers: [{ url: 'http://app:8080' }], used_by: { routers: [] } })]) };
		render(ServiceDialog, { mode: 'edit', name: 'web', navigate: () => {} });
		await waitFor(() => expect((document.getElementById('service-name') as HTMLInputElement).value).toBe('web'));
		expect(count('/api/v1/services')).toBe(1);
	});
});

describe('table', () => {
	it('has no Source column: an external router carries a badge next to its name', async () => {
		handlers = base([router('shop'), router('dash@docker', {}, 1, { origin: 'external', provider: 'docker', read_only: true })]);
		render(RoutesPage, { navigate: () => {} });
		await waitFor(() => expect(screen.getAllByText('shop')[0]).toBeTruthy());
		expect(screen.queryByRole('columnheader', { name: /Source/ })).toBeNull();
		const shop = screen.getAllByText('shop')[0].closest('tr') as HTMLElement;
		const dash = screen.getAllByText('dash@docker')[0].closest('tr') as HTMLElement;
		expect(within(dash).getByText('External')).toBeTruthy();
		expect(within(shop).queryByText('External')).toBeNull();
	});
	it('still filters by ownership, and finds an external router by its provider', async () => {
		handlers = base([router('shop'), router('dash@docker', {}, 1, { origin: 'external', provider: 'docker', read_only: true })]);
		render(RoutesPage, { navigate: () => {} });
		await waitFor(() => expect(screen.getAllByText('shop')[0]).toBeTruthy());
		await fireEvent.input(screen.getByRole('searchbox'), { target: { value: 'docker' } });
		await waitFor(() => expect(screen.queryByText('shop')).toBeNull());
		expect(screen.getAllByText('dash@docker').length).toBeGreaterThan(0);
	});
	it('shows a router without a backend as such, not as a service', async () => {
		handlers = base([router('old-site', { service: 'noop@internal', middlewares: ['to-https'], tls: null })]);
		render(RoutesPage, { navigate: () => {} });
		await waitFor(() => expect(screen.getAllByText('old-site')[0]).toBeTruthy());
		expect(screen.getByText('No backend (middleware answers)')).toBeTruthy();
		expect(document.querySelectorAll('button.link')).toHaveLength(0);
	});
});

describe('view', () => {
	it('shows the router the way Traefik declares it', async () => {
		handlers = base([router('shop', { entry_points: ['websecure'], middlewares: ['auth'], priority: 5, tls: { enabled: true, cert_resolver: 'le', options: 'modern' } })]);
		render(RoutesPage, { navigate: () => {}, mode: 'view', name: 'shop' });
		const code = await waitFor(() => { const el = document.querySelector('dialog pre, dialog code.yaml, dialog [class*="code"]'); expect(el).toBeTruthy(); return el!; });
		const text = document.querySelector('dialog')!.textContent ?? '';
		for (const line of ['entryPoints:', 'rule: Host(`app.example.com`)', 'service: web', 'middlewares:', '- auth', 'priority: 5', 'certResolver: le', 'options: modern']) expect(text).toContain(line);
		expect(code).toBeTruthy();
	});
	it('shows a router without a backend with its middleware', async () => {
		handlers = base([router('old-site', { service: 'noop@internal', middlewares: ['to-https'], tls: null, entry_points: ['web'] })]);
		render(RoutesPage, { navigate: () => {}, mode: 'view', name: 'old-site' });
		await waitFor(() => expect(document.querySelector('dialog')!.textContent).toContain('service:'));
		const dialog = document.querySelector('dialog')!;
		expect(within(dialog).getByText('No backend (middleware answers)')).toBeTruthy();
		expect(dialog.textContent).toContain('service: noop@internal');
		expect(dialog.textContent).not.toContain('tls:');
	});
	it('shows an external router with what Traefik reports, and the External source', async () => {
		handlers = base([router('dash@docker', { tls: null }, 1, { origin: 'external', provider: 'docker', read_only: true })]);
		render(RoutesPage, { navigate: () => {}, mode: 'view', name: 'dash@docker' });
		await waitFor(() => expect(document.querySelector('dialog')!.textContent).toContain('service:'));
		expect(document.querySelector('dialog')!.textContent).toMatch(/external · docker/i);
		expect(screen.getByRole('region', { name: 'Definition in Traefik' })).toBeTruthy();
	});
});

describe('a router without a backend', () => {
	async function reachService() {
		render(RoutesPage, { navigate: () => {}, mode: 'create' });
		await waitFor(() => expect(document.getElementById('route-name')).toBeTruthy());
		await fireEvent.input(document.getElementById('route-name')!, { target: { value: 'old-site' } });
		await pick('route-mode', /^Hostnames/);
		await fireEvent.input(await waitFor(() => document.getElementById('route-hosts')!), { target: { value: 'old.example.com' } });
		await fireEvent.click(screen.getByRole('button', { name: 'Continue' }));
	}
	const dry = () => ({ ...base([]), 'PUT /api/v1/routers/old-site?dry_run=true': () => json({ preview: preview(), restart_required: false }) });

	it('is offered first in the service list, with the reason', async () => {
		handlers = dry();
		await reachService();
		await fireEvent.click(await waitFor(() => document.getElementById('route-service')!));
		const options = within(await waitFor(() => document.getElementById('route-service-listbox')!)).getAllByRole('option');
		expect(options[0].textContent).toContain('No backend (a middleware answers)');
		expect(options.map((o) => o.textContent).join(' ')).toContain('web');
	});
	it('still opens when Traefik itself reports noop@internal as a service, and lists it once', async () => {
		const noop = envelope('service', 'noop@internal', { servers: null }, 1, { origin: 'external', provider: 'internal', read_only: true });
		handlers = { ...dry(), 'GET /api/v1/services': () => json([web, noop]) };
		await reachService();
		await fireEvent.click(await waitFor(() => document.getElementById('route-service')!));
		const options = within(await waitFor(() => document.getElementById('route-service-listbox')!)).getAllByRole('option');
		expect(options.filter((o) => o.textContent?.includes('noop@internal'))).toHaveLength(0);
		expect(options.filter((o) => o.textContent?.includes('No backend'))).toHaveLength(1);
	});
	it('needs a middleware that answers', async () => {
		handlers = dry();
		await reachService();
		await pick('route-service', /No backend/);
		expect(screen.getByText(/the Console writes the built-in noop@internal/)).toBeTruthy();
		await fireEvent.click(screen.getByRole('button', { name: 'Continue' }));
		await fireEvent.click(screen.getByRole('button', { name: 'Review change' }));
		await waitFor(() => expect(screen.getAllByText(/Attach a middleware that answers the request/).length).toBeGreaterThan(0));
		expect(calls.some((c) => c.method === 'PUT')).toBe(false);
	});
	it('is saved as noop@internal once a middleware is attached', async () => {
		handlers = dry();
		await reachService();
		await pick('route-service', /No backend/);
		await fireEvent.click(screen.getByRole('button', { name: 'Continue' }));
		await pick('route-add-middleware', /auth/);
		await fireEvent.click(screen.getByRole('button', { name: 'Review change' }));
		await waitFor(() => expect(calls.some((c) => c.method === 'PUT')).toBe(true));
		expect(calls.find((c) => c.method === 'PUT')!.body).toEqual({ entry_points: [], domains: ['old.example.com'], service: 'noop@internal', middlewares: ['auth'], tls: { enabled: true } });
	});
	it('still has to choose something: an empty service is refused', async () => {
		handlers = dry();
		await reachService();
		await fireEvent.click(screen.getByRole('button', { name: 'Continue' }));
		await waitFor(() => expect(screen.getAllByText('Select or enter a valid target service reference.').length).toBeGreaterThan(0));
		expect(calls.some((c) => c.method === 'PUT')).toBe(false);
	});
	it('is not mistaken for an external reference', () => {
		expect(isExternalRef('noop@internal')).toBe(false);
		expect(isExternalRef('api@docker')).toBe(true);
		expect(isExternalRef('web@file')).toBe(false);
	});
});

describe('router definition', () => {
	it('writes only what is set, in the order Traefik documents', () => {
		expect(routerDefinition({ entry_points: ['web'], domains: null, rule: 'Host(`a.example`)', service: 'web' })).toEqual({ entryPoints: ['web'], rule: 'Host(`a.example`)', service: 'web' });
		expect(Object.keys(routerDefinition({ entry_points: ['websecure'], domains: null, rule: 'r', service: 's', middlewares: ['m'], priority: 3, tls: { enabled: true, cert_resolver: 'le', options: 'o', domains: [{ main: '*.a.example', sans: ['a.example'] }, { main: 'b.example' }] } })))
			.toEqual(['entryPoints', 'rule', 'service', 'middlewares', 'priority', 'tls']);
		expect(routerDefinition({ entry_points: ['websecure'], domains: null, rule: 'r', service: 's', tls: { enabled: true, cert_resolver: 'le', domains: [{ main: '*.a.example', sans: ['a.example'] }, { main: 'b.example', sans: null }] } }).tls)
			.toEqual({ certResolver: 'le', domains: [{ main: '*.a.example', sans: ['a.example'] }, { main: 'b.example' }] });
	});
	it('writes an empty tls block for TLS without options, and none without TLS', () => {
		expect(routerDefinition({ entry_points: [], domains: null, rule: 'r', service: 's', tls: { enabled: true } }).tls).toEqual({});
		expect('tls' in routerDefinition({ entry_points: [], domains: null, rule: 'r', service: 's', tls: null })).toBe(false);
	});
	it('is empty for a router Traefik reported nothing about', () => {
		expect(routerDefinition({ entry_points: null, domains: null, rule: '', service: '' })).toEqual({});
	});
});

describe('validation of the service', () => {
	const form = { ...newForm(), name: 'a', mode: 'host' as const, hosts: 'a.example.com' };
	it('needs a service, or the no-backend choice with a middleware', () => {
		expect(routeErrors({ ...form, service: '' }, ctx, messages).service).toBe('sv');
		expect(routeErrors({ ...form, service: 'web' }, ctx, messages).service).toBeUndefined();
		expect(routeErrors({ ...form, service: 'noop@internal' }, ctx, { ...messages, noBackendNeedsMiddleware: 'nbm' }).service).toBe('nbm');
		expect(routeErrors({ ...form, service: 'noop@internal' }, ctx, messages).service).toBe('sv');
		expect(routeErrors({ ...form, service: 'noop@internal', middlewares: ['to-https'] }, ctx, messages).service).toBeUndefined();
	});
});

import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/svelte';
// The API client binds `fetch` at import time, so install a forwarding stub first.
vi.hoisted(() => {
	(globalThis as any).__mockFetch = async () => { throw new Error('no mock'); };
	globalThis.fetch = ((...args: unknown[]) => (globalThis as any).__mockFetch(...args)) as typeof fetch;
});
import ServicesPage from '../src/features/services/ServicesPage.svelte';
import { formFrom, newForm, toInput } from '../src/features/services/models';
import { addressError, backendError, durationError } from '../src/features/services/validation';
import { carriesMore, joinAddress, joinUrl, parseAddress, parseUrl } from '../src/features/services/backendParts';
import { flashes } from '../src/shared/ui/flash.svelte';

const preview = (extra = {}) => ({ changes: [{ action: 'created', id: 'service:my-app', kind: 'service', name: 'my-app', scope: 'dynamic' }], static_diff: '', dynamic_diff: '+s: {}', requires_restart: false, ...extra });
const operation = (extra = {}) => ({ id: 5, kind: 'service.create', target: 'my-app', status: 'succeeded', summary: 'Applied 1 change', requires_restart: false, created_at: 'now', ...extra });
const none = { routers: [] };
const service = (name: string, config: Record<string, unknown> = { servers: [{ url: 'http://app:8080' }] }, version = 1, extra = {}) => ({ id: 'service:' + name, name, version, origin: 'managed', provider: 'file', status: 'active', read_only: false, live_health: 'loaded', updated_at: '2026-01-01T00:00:00Z', config: { used_by: none, ...config }, ...extra });
/** A router as the server sends it in `used_by`: only what the dialog shows. */
const usedBy = (...names: string[]) => ({ routers: names.map((name) => ({ name, rule: `Host(\`${name}.example\`)`, entry_points: ['websecure'], service: 'x', tls: true, read_only: false })) });

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

const base = (list: unknown) => ({ 'GET /api/v1/services': () => json(list) });

describe('model and validation', () => {
	const m = { invalidName: 'n', nameTaken: 't', invalidUrl: 'u', urlQuery: 'q', urlScheme: 's', serverCount: 'c', duplicateUrl: 'd', invalidPath: 'p', invalidDuration: 'dur' };
	it('omits unset optional blocks and sends only what is configured', () => {
		expect(toInput({ ...newForm(), name: 'a', servers: [' http://app:80 '] })).toEqual({ protocol: 'http', servers: [{ url: 'http://app:80' }] });
		expect(toInput({ ...newForm(), name: 'a', servers: ['h2c://app:80'], passHost: 'no', healthEnabled: true, healthPath: '/up', healthInterval: '10s', insecureSkipVerify: true }))
			.toEqual({ protocol: 'http', servers: [{ url: 'h2c://app:80' }], pass_host_header: false, health_check: { path: '/up', interval: '10s' }, insecure_skip_verify: true });
	});
	it('loads an existing service back into the form', () => {
		const f = formFrom('a', { servers: null, pass_host_header: true, health_check: { path: '/h', timeout: '2s' } });
		expect(f).toMatchObject({ name: 'a', passHost: 'yes', servers: [''], healthEnabled: true, healthPath: '/h', healthTimeout: '2s' });
	});
	it('mirrors the backend URL rules', () => {
		expect(backendError('http://app:8080/api', m)).toBe('');
		expect(backendError('h2c://app:80', m)).toBe('');
		expect(backendError('http://app?x=1', m)).toBe('q');
		expect(backendError('ftp://app', m)).toBe('s');
		expect(backendError('http://user:pw@app', m)).toBe('u');
		expect(backendError('http://app:99999', m)).toBe('u');
		expect(backendError('', m)).toBe('u');
	});
	it('validates Go-style durations up to one hour', () => {
		expect(durationError('10s')).toBe(false);
		expect(durationError('1m30s')).toBe(false);
		expect(durationError('')).toBe(false);
		expect(durationError('2h')).toBe(true);
		expect(durationError('0s')).toBe(true);
		expect(durationError('soon')).toBe(true);
	});
});

describe('list', () => {
	it('reads usage from the services list alone and blocks Delete while used', async () => {
		handlers = base([service('web', { servers: [{ url: 'http://app:8080' }], used_by: usedBy('shop') }), service('api', { servers: [{ url: 'http://a:1' }, { url: 'http://b:2' }] })]);
		render(ServicesPage, { navigate: () => {} });
		await waitFor(() => expect(screen.getAllByText('web')[0]).toBeTruthy());
		expect(calls.map((c) => c.path)).toEqual(['/api/v1/services']); // no /routers request
		expect(screen.getByText('1 resource')).toBeTruthy();
		expect(screen.getByText('Not used')).toBeTruthy();
		const del = screen.getAllByRole('menuitem', { name: 'Delete' }) as HTMLButtonElement[];
		expect(del.filter((b) => b.disabled)).toHaveLength(1);
	});
	it('opens the routers that use a service from the server usage', async () => {
		handlers = base([service('web', { servers: [{ url: 'http://app:8080' }], used_by: usedBy('shop', 'admin') })]);
		render(ServicesPage, { navigate: () => {} });
		await fireEvent.click(await screen.findByText('2 resources'));
		const dialog = await screen.findByRole('dialog');
		expect(dialog.textContent).toContain('shop');
		expect(dialog.textContent).toContain('admin');
	});
	it('says usage is unknown when the server could not read routers', async () => {
		handlers = base([{ ...service('web'), config: { servers: [{ url: 'http://app:8080' }] } }]);
		render(ServicesPage, { navigate: () => {} });
		await waitFor(() => expect(screen.getByText('Unavailable')).toBeTruthy());
		expect(screen.getByText(/Router usage could not be read/)).toBeTruthy();
		// Delete stays available: the server still refuses a service in use.
		expect((screen.getByRole('menuitem', { name: 'Delete' }) as HTMLButtonElement).disabled).toBe(false);
	});
	it('marks external services with a badge instead of a Source column, and filters by ownership', async () => {
		handlers = base([service('web'), service('whoami@docker', { servers: [{ url: 'http://c:3' }] }, 1, { origin: 'external', provider: 'docker', read_only: true })]);
		render(ServicesPage, { navigate: () => {} });
		await waitFor(() => expect(screen.getAllByText('whoami@docker')[0]).toBeTruthy());
		expect(screen.queryByRole('columnheader', { name: 'Source' })).toBeNull();
		expect(screen.getAllByText('External')).toHaveLength(1);
		await fireEvent.click(screen.getByRole('button', { name: /Filters/ }));
		await fireEvent.click(screen.getByRole('menuitem', { name: 'Source' }));
		await fireEvent.click(await screen.findByRole('menuitemradio', { name: /External/ }));
		await waitFor(() => expect(screen.queryByText('web')).toBeNull());
		expect(screen.getAllByText('whoami@docker').length).toBeGreaterThan(0);
	});
	it('shows a single backend as is', async () => {
		handlers = base([service('web', { servers: [{ url: 'http://app:8080' }] })]);
		render(ServicesPage, { navigate: () => {} });
		await waitFor(() => expect(screen.getAllByText('http://app:8080').length).toBeGreaterThan(0));
		expect(screen.queryByText(/^\+\d/)).toBeNull();
	});
	it('shows the first backend, +N and every server with its protocol on hover', async () => {
		handlers = base([service('api', { servers: [{ url: 'http://a:1' }, { url: 'https://b:2/x' }, { url: 'h2c://c:3' }] })]);
		render(ServicesPage, { navigate: () => {} });
		await waitFor(() => expect(screen.getByText('+2')).toBeTruthy());
		expect(screen.getByText('http://a:1')).toBeTruthy();
		const trigger = screen.getByRole('button', { name: 'Backend servers: 3 backend servers' });
		expect(trigger).toBeTruthy();
		const panel = document.getElementById(trigger.getAttribute('aria-describedby') ?? '') ?? document.querySelector('.value-tooltip.rich')!;
		expect(panel.textContent).toContain('3 backend servers');
		for (const text of ['http', 'a:1', 'https', 'b:2/x', 'h2c', 'c:3']) expect(panel.textContent).toContain(text);
	});
	it('shows external services read-only', async () => {
		handlers = base([service('ext@docker', { servers: [{ url: 'http://c:3' }] }, 1, { origin: 'external', provider: 'docker', read_only: true })]);
		render(ServicesPage, { navigate: () => {} });
		await waitFor(() => expect(screen.getAllByText('ext@docker')[0]).toBeTruthy());
		expect(screen.queryByRole('menuitem', { name: 'Edit' })).toBeNull();
		expect(screen.queryByRole('button', { name: 'Edit' })).toBeNull();
		expect(screen.queryByRole('menuitem', { name: 'Delete' })).toBeNull();
		expect(screen.queryByRole('button', { name: 'Delete' })).toBeNull();
	});
	it('shows the empty state for a null list', async () => {
		handlers = base(null);
		render(ServicesPage, { navigate: () => {} });
		await waitFor(() => expect(screen.getByText('No services yet')).toBeTruthy());
	});
	it('deletes with the current version and surfaces the server conflict', async () => {
		handlers = {
			...base([service('free', { servers: [{ url: 'http://app:8080' }] }, 7)]),
			'DELETE /api/v1/services/free': () => json({ code: 'ERR_CONFLICT', message: 'service is used by router r1', operation_id: 'o', retryable: false }, 409)
		};
		render(ServicesPage, { navigate: () => {} });
		await waitFor(() => expect(screen.getAllByText('free')[0]).toBeTruthy());
		await fireEvent.click(screen.getByRole('menuitem', { name: 'Delete' }));
		await fireEvent.click(await screen.findByRole('button', { name: 'Confirm deletion' }));
		await waitFor(() => expect(screen.getByText(/used by router r1/)).toBeTruthy());
		expect(calls.find((c) => c.method === 'DELETE')!.headers['if-match']).toBe('"7"');
	});
});

describe('create flow', () => {
	async function fillName(value: string) {
		await waitFor(() => expect(document.getElementById('service-name')).toBeTruthy());
		await fireEvent.input(document.getElementById('service-name')!, { target: { value } });
	}
	it('previews with a dry run, then applies with a create precondition and one key', async () => {
		handlers = {
			...base([]),
			'PUT /api/v1/services/my-app?dry_run=true': () => json({ preview: preview(), restart_required: false }),
			'PUT /api/v1/services/my-app': () => json({ operation: operation(), resource: service('my-app'), restart_required: false })
		};
		render(ServicesPage, { navigate: () => {}, mode: 'create' });
		await fillName('my-app');
		await fireEvent.click(screen.getByRole('button', { name: 'Continue' }));
		await fireEvent.input(await waitFor(() => document.getElementById('service-url-0')!), { target: { value: 'http://app:8080' } });
		await fireEvent.click(screen.getByRole('button', { name: 'Review change' }));
		await screen.findByRole('button', { name: 'Apply changes' });

		const dry = calls.find((c) => c.method === 'PUT')!;
		expect(dry.path).toBe('/api/v1/services/my-app?dry_run=true');
		expect(dry.headers['if-none-match']).toBe('*');
		expect(dry.body).toEqual({ protocol: 'http', servers: [{ url: 'http://app:8080' }] });

		await fireEvent.click(await screen.findByRole('button', { name: 'Apply changes' }));
		await waitFor(() => expect(flashes.some((f) => f.title.startsWith('Applied'))).toBe(true));
		const apply = calls.filter((c) => c.method === 'PUT')[1];
		expect(apply.path).toBe('/api/v1/services/my-app');
		expect(apply.headers['idempotency-key']).toMatch(/^[A-Za-z0-9_-]{16,128}$/);
		expect(calls.some((c) => c.path.includes('/draft'))).toBe(false);
	});
	it('includes the optional health check and TLS option when chosen', async () => {
		handlers = { ...base([]), 'PUT /api/v1/services/my-app?dry_run=true': () => json({ preview: preview(), restart_required: false }) };
		render(ServicesPage, { navigate: () => {}, mode: 'create' });
		await fillName('my-app');
		await fireEvent.click(screen.getByRole('button', { name: 'Continue' }));
		await fireEvent.input(await waitFor(() => document.getElementById('service-url-0')!), { target: { value: 'https://app:8443' } });
		await fireEvent.click(screen.getByLabelText(/Enable health check/));
		await fireEvent.input(await waitFor(() => document.getElementById('service-health-path')!), { target: { value: '/up' } });
		await fireEvent.input(document.getElementById('service-health-interval')!, { target: { value: '10s' } });
		await fireEvent.click(screen.getByLabelText(/Do not verify backend TLS/));
		await fireEvent.click(screen.getByRole('button', { name: 'Review change' }));
		await waitFor(() => expect(calls.some((c) => c.method === 'PUT')).toBe(true));
		expect(calls.find((c) => c.method === 'PUT')!.body).toEqual({ protocol: 'http', servers: [{ url: 'https://app:8443' }], health_check: { path: '/up', interval: '10s' }, insecure_skip_verify: true });
	});
	it('rejects a query string and a bad name without calling the server', async () => {
		handlers = base([]);
		render(ServicesPage, { navigate: () => {}, mode: 'create' });
		await fillName('Bad Name');
		await fireEvent.click(screen.getByRole('button', { name: 'Continue' }));
		await waitFor(() => expect(document.getElementById('service-name')!.getAttribute('aria-invalid')).toBe('true'));
		await fireEvent.input(document.getElementById('service-name')!, { target: { value: 'ok' } });
		await fireEvent.click(screen.getByRole('button', { name: 'Continue' }));
		await fireEvent.input(await waitFor(() => document.getElementById('service-url-0')!), { target: { value: 'http://app:8080/x?y=1' } });
		await fireEvent.click(screen.getByRole('button', { name: 'Review change' }));
		await waitFor(() => expect(screen.getAllByText(/query string/).length).toBeGreaterThan(0));
		expect(calls.some((c) => c.method === 'PUT')).toBe(false);
	});
	it('refuses a name already in use', async () => {
		handlers = base([service('taken')]);
		render(ServicesPage, { navigate: () => {}, mode: 'create' });
		await waitFor(() => expect(document.getElementById('service-name')).toBeTruthy());
		await fireEvent.input(document.getElementById('service-name')!, { target: { value: 'taken' } });
		await fireEvent.click(screen.getByRole('button', { name: 'Continue' }));
		await waitFor(() => expect(screen.getAllByText(/already exists/).length).toBeGreaterThan(0));
	});
});

describe('backend servers editor', () => {
	const dry = () => ({ ...base([]), 'PUT /api/v1/services/my-app?dry_run=true': () => json({ preview: preview(), restart_required: false }) });
	async function openBackends() {
		render(ServicesPage, { navigate: () => {}, mode: 'create' });
		await waitFor(() => expect(document.getElementById('service-name')).toBeTruthy());
		await fireEvent.input(document.getElementById('service-name')!, { target: { value: 'my-app' } });
		await fireEvent.click(screen.getByRole('button', { name: 'Continue' }));
		return waitFor(() => { const el = document.getElementById('service-url-0') as HTMLInputElement | null; expect(el).toBeTruthy(); return el!; });
	}
	const review = async () => {
		await fireEvent.click(screen.getByRole('button', { name: 'Review change' }));
		await waitFor(() => expect(calls.some((c) => c.method === 'PUT')).toBe(true));
		return calls.find((c) => c.method === 'PUT')!.body.servers;
	};
	/** The protocol menu's listbox (SelectMenu links it with aria-controls). */
	const schemeList = (index: number) => document.getElementById(document.getElementById(`service-url-${index}-scheme`)!.getAttribute('aria-controls')!)!;
	const pickScheme = async (index: number, scheme: string) => {
		await fireEvent.click(document.getElementById(`service-url-${index}-scheme`)!);
		await fireEvent.click(await within(schemeList(index)).findByRole('option', { name: scheme }));
	};

	it('offers every protocol Traefik accepts for HTTP servers', async () => {
		handlers = dry();
		await openBackends();
		await fireEvent.click(document.getElementById('service-url-0-scheme')!);
		expect(within(schemeList(0)).getAllByRole('option').map((o) => o.textContent?.trim())).toEqual(['http', 'https', 'h2c']);
	});
	it('joins the chosen protocol with the address, path included, in one input', async () => {
		handlers = dry();
		const input = await openBackends();
		await pickScheme(0, 'https');
		await fireEvent.input(input, { target: { value: 'app.internal:8443/api/v1' } });
		expect(await review()).toEqual([{ url: 'https://app.internal:8443/api/v1' }]);
	});
	it('splits a pasted full URL into protocol and address', async () => {
		handlers = dry();
		const input = await openBackends();
		await fireEvent.input(input, { target: { value: 'h2c://grpc:50051' } });
		expect(input.value).toBe('grpc');
		expect((document.getElementById('service-url-0-port') as HTMLInputElement).value).toBe('50051');
		expect(document.getElementById('service-url-0-scheme')!.textContent).toContain('h2c');
		expect(await review()).toEqual([{ url: 'h2c://grpc:50051' }]);
	});
	it('names an unsupported protocol instead of nesting it', async () => {
		handlers = dry();
		const input = await openBackends();
		await fireEvent.input(input, { target: { value: 'tcp://db:5432' } });
		await fireEvent.click(screen.getByRole('button', { name: 'Review change' }));
		await waitFor(() => expect(screen.getAllByText('Use http, https or h2c.').length).toBeGreaterThan(0));
		expect(input.getAttribute('aria-invalid')).toBe('true');
		expect(calls.some((c) => c.method === 'PUT')).toBe(false);
	});
	it('adds and removes servers, and always keeps one', async () => {
		handlers = dry();
		const first = await openBackends();
		const remove = (n: number) => screen.getByRole('button', { name: `Remove backend ${n}` }) as HTMLButtonElement;
		expect(remove(1).disabled).toBe(true);
		await fireEvent.input(first, { target: { value: 'a:1' } });
		await fireEvent.click(screen.getByRole('button', { name: 'Add backend' }));
		await fireEvent.input(document.getElementById('service-url-1')!, { target: { value: 'b:2' } });
		await fireEvent.click(screen.getByRole('button', { name: 'Add backend' }));
		await fireEvent.input(document.getElementById('service-url-2')!, { target: { value: 'c:3' } });
		await fireEvent.click(remove(2));
		expect((document.getElementById('service-url-1') as HTMLInputElement).value).toBe('c');
		expect((document.getElementById('service-url-1-port') as HTMLInputElement).value).toBe('3');
		expect(await review()).toEqual([{ url: 'http://a:1' }, { url: 'http://c:3' }]);
	});
	it('flags an empty and a duplicated server on its own row', async () => {
		handlers = dry();
		const first = await openBackends();
		await fireEvent.input(first, { target: { value: 'a:1' } });
		await fireEvent.click(screen.getByRole('button', { name: 'Add backend' }));
		await fireEvent.input(document.getElementById('service-url-1')!, { target: { value: 'http://a:1' } });
		await fireEvent.click(screen.getByRole('button', { name: 'Add backend' }));
		await fireEvent.click(screen.getByRole('button', { name: 'Review change' }));
		await waitFor(() => expect(screen.getByText('This backend is listed twice.')).toBeTruthy());
		expect(document.getElementById('service-url-0')!.getAttribute('aria-invalid')).toBeNull();
		expect(document.getElementById('service-url-1')!.getAttribute('aria-invalid')).toBe('true');
		expect(document.getElementById('service-url-2')!.getAttribute('aria-invalid')).toBe('true');
		expect(calls.some((c) => c.method === 'PUT')).toBe(false);
	});
	it('loads an existing service into protocol and address, and edits it with its version', async () => {
		handlers = {
			...base([service('web', { servers: [{ url: 'https://app:8443/base' }, { url: 'h2c://grpc:50051' }] }, 4)]),
			'PUT /api/v1/services/web?dry_run=true': () => json({ preview: preview(), restart_required: false })
		};
		render(ServicesPage, { navigate: () => {}, mode: 'edit', name: 'web' });
		const first = await waitFor(() => { const el = document.getElementById('service-url-0') as HTMLInputElement | null; expect(el).toBeTruthy(); return el!; });
		expect(first.value).toBe('app');
		expect((document.getElementById('service-url-0-port') as HTMLInputElement).value).toBe('8443');
		// A stored path opens the path field; a backend without one keeps it closed.
		expect((document.getElementById('service-url-0-path') as HTMLInputElement).value).toBe('/base');
		expect(document.getElementById('service-url-1-path')).toBeNull();
		expect(document.getElementById('service-url-0-scheme')!.textContent).toContain('https');
		expect((document.getElementById('service-url-1') as HTMLInputElement).value).toBe('grpc');
		await pickScheme(1, 'http');
		const sent = calls.length;
		expect(await review()).toEqual([{ url: 'https://app:8443/base' }, { url: 'http://grpc:50051' }]);
		expect(calls.slice(sent).find((c) => c.method === 'PUT')!.headers['if-match']).toBe('"4"');
		// The usage the list carried is never sent back.
		expect(calls.find((c) => c.method === 'PUT')!.body.used_by).toBeUndefined();
	});
});

describe('view and runtime check', () => {
	it('shows the service as Traefik declares it', async () => {
		handlers = base([service('web', { servers: [{ url: 'http://a:1' }, { url: 'https://b:2' }], pass_host_header: false, health_check: { path: '/up', interval: '10s' }, insecure_skip_verify: true, used_by: usedBy('shop') })]);
		render(ServicesPage, { navigate: () => {}, mode: 'view', name: 'web' });
		const dialog = await screen.findByRole('dialog');
		await waitFor(() => expect(dialog.textContent).toContain('loadBalancer:'));
		const yaml = dialog.querySelector('pre, code')!.textContent!;
		expect(yaml).toBe('loadBalancer:\n  servers:\n    - url: http://a:1\n    - url: https://b:2\n  passHostHeader: false\n  healthCheck:\n    path: /up\n    interval: 10s\n  serversTransport: web-insecure-transport\n');
		expect(yaml).not.toContain('used_by');
		expect(yaml).not.toContain('shop');
	});
	it('explains a service with no definition (Traefik internal)', async () => {
		handlers = base([service('api@internal', { servers: null }, 1, { origin: 'external', provider: 'internal', read_only: true })]);
		render(ServicesPage, { navigate: () => {}, mode: 'view', name: 'api@internal' });
		await waitFor(() => expect(screen.getByText(/declares this service internally/)).toBeTruthy());
		expect(screen.queryByRole('button', { name: 'Edit service' })).toBeNull();
	});
	it('checks an external service in Traefik by its qualified name', async () => {
		handlers = {
			...base([service('api@internal', { servers: null }, 1, { origin: 'external', provider: 'internal', read_only: true })]),
			'GET /api/v1/services/api%40internal/diagnostics': () => json({ protocol: 'http', name: 'api@internal', observed: true, runtimeStatus: 'enabled', healthStatus: 'unknown', routers: [{ name: 'dashboard@internal', status: 'enabled', entryPoints: ['traefik'] }], observedAt: '2026-01-01T00:00:00Z' })
		};
		render(ServicesPage, { navigate: () => {}, mode: 'view', name: 'api@internal' });
		await fireEvent.click(await screen.findByRole('button', { name: 'Check in Traefik' }));
		await waitFor(() => expect(screen.getByText('dashboard@internal')).toBeTruthy());
		expect(screen.queryByText(/could not be completed/)).toBeNull();
	});
	it('reports a failed runtime check without breaking the dialog', async () => {
		handlers = {
			...base([service('web')]),
			'GET /api/v1/services/web/diagnostics': () => json({ code: 'ERR_TRAEFIK_UNAVAILABLE', message: 'Traefik runtime observation is unavailable.', operation_id: 'o', retryable: true }, 502)
		};
		render(ServicesPage, { navigate: () => {}, mode: 'view', name: 'web' });
		await fireEvent.click(await screen.findByRole('button', { name: 'Check in Traefik' }));
		await waitFor(() => expect(screen.getByText(/unavailable/i)).toBeTruthy());
		expect(screen.getByRole('button', { name: 'Check in Traefik' })).toBeTruthy();
	});
	it('reads the runtime snapshot from the diagnostics endpoint on demand', async () => {
		handlers = {
			...base([service('web')]),
			'GET /api/v1/services/web/diagnostics': () => json({ protocol: 'http', name: 'web', observed: true, runtimeStatus: 'enabled', healthStatus: 'mixed', serverStatus: { UP: 1, DOWN: 1 }, routers: [{ name: 'shop', status: 'enabled', entryPoints: ['websecure'] }], observedAt: '2026-01-01T00:00:00Z' })
		};
		render(ServicesPage, { navigate: () => {}, mode: 'view', name: 'web' });
		await fireEvent.click(await screen.findByRole('button', { name: 'Check in Traefik' }));
		await waitFor(() => expect(screen.getByText('Mixed')).toBeTruthy());
		expect(screen.getByText('1 UP · 1 DOWN')).toBeTruthy();
		expect(screen.getAllByText('shop').length).toBeGreaterThan(0);
	});
});

describe('backend fields', () => {
	it('splits and joins URLs, keeping what was typed', () => {
		expect(parseUrl('https://app.internal:8443/api/v1')).toEqual({ scheme: 'https', host: 'app.internal', port: '8443', path: '/api/v1' });
		expect(parseUrl('http://[fd00::5]:8080/x')).toEqual({ scheme: 'http', host: 'fd00::5', port: '8080', path: '/x' });
		expect(parseUrl('app:80/base', 'h2c')).toEqual({ scheme: 'h2c', host: 'app', port: '80', path: '/base' });
		expect(parseUrl('tcp://db:5432').host).toBe('tcp://db:5432'); // shown as typed, validation names it
		expect(joinUrl({ scheme: 'https', host: 'fd00::5', port: '8443', path: 'api' })).toBe('https://[fd00::5]:8443/api');
		expect(joinUrl({ scheme: 'http', host: 'app', port: '', path: '' })).toBe('http://app');
		expect(joinUrl({ scheme: 'http', host: ' ', port: '80', path: '' })).toBe('');
	});
	it('splits and joins TCP/UDP addresses', () => {
		expect(parseAddress('db.internal:5432')).toEqual({ host: 'db.internal', port: '5432' });
		expect(parseAddress('tcp://db:5432')).toEqual({ host: 'db', port: '5432' });
		expect(parseAddress('[fd00::5]:53')).toEqual({ host: 'fd00::5', port: '53' });
		expect(parseAddress('fd00::5')).toEqual({ host: 'fd00::5', port: '' });
		expect(joinAddress({ host: 'fd00::5', port: '53' })).toBe('[fd00::5]:53');
		expect(joinAddress({ host: '', port: '' })).toBe('');
		expect(carriesMore('db')).toBe(false);
		expect(carriesMore('db:5432')).toBe(true);
		expect(carriesMore('fd00::5')).toBe(false);
	});
	it('mirrors the core-v2 address rules', () => {
		const m = { invalidName: 'n', nameTaken: 't', invalidUrl: 'u', urlQuery: 'q', urlScheme: 's', serverCount: 'c', duplicateUrl: 'd', invalidPath: 'p', invalidDuration: 'dur', invalidAddress: 'a', invalidPort: 'port' };
		expect(addressError('db:5432', m)).toBe('');
		expect(addressError('[fd00::5]:53', m)).toBe('');
		expect(addressError('db', m)).toBe('a');
		expect(addressError('db:', m)).toBe('a');
		expect(addressError('db:0', m)).toBe('port');
		expect(addressError('db:65536', m)).toBe('port');
		expect(addressError('fd00::5:53', m)).toBe('a');
		expect(addressError('tcp://db:5432', m)).toBe('a');
		expect(addressError('db:5432/x', m)).toBe('a');
		expect(backendError('http://app:0', m)).toBe('port');
	});
});

describe('backend server row', () => {
	const dry = () => ({ ...base([]), 'PUT /api/v1/services/my-app?dry_run=true': () => json({ preview: preview(), restart_required: false }) });
	async function openBackends() {
		render(ServicesPage, { navigate: () => {}, mode: 'create' });
		await waitFor(() => expect(document.getElementById('service-name')).toBeTruthy());
		await fireEvent.input(document.getElementById('service-name')!, { target: { value: 'my-app' } });
		await fireEvent.click(screen.getByRole('button', { name: 'Continue' }));
		return waitFor(() => { const el = document.getElementById('service-url-0') as HTMLInputElement | null; expect(el).toBeTruthy(); return el!; });
	}
	const sent = async () => {
		await fireEvent.click(screen.getByRole('button', { name: 'Review change' }));
		await waitFor(() => expect(calls.some((c) => c.method === 'PUT')).toBe(true));
		return calls.find((c) => c.method === 'PUT')!.body.servers;
	};

	it('builds the URL from separate host and port fields; the port is optional for HTTP', async () => {
		handlers = dry();
		const host = await openBackends();
		await fireEvent.input(host, { target: { value: '10.0.0.5' } });
		await fireEvent.input(document.getElementById('service-url-0-port')!, { target: { value: '8080' } });
		await fireEvent.click(screen.getByRole('button', { name: 'Add backend' }));
		await fireEvent.input(document.getElementById('service-url-1')!, { target: { value: 'app.internal' } });
		expect(await sent()).toEqual([{ url: 'http://10.0.0.5:8080' }, { url: 'http://app.internal' }]);
	});
	it('adds the optional path below the row, and removes it again', async () => {
		handlers = dry();
		const host = await openBackends();
		await fireEvent.input(host, { target: { value: 'app' } });
		expect(document.getElementById('service-url-0-path')).toBeNull();
		const toggle = screen.getByRole('button', { name: 'Add a path to backend 1' });
		expect(toggle.getAttribute('aria-pressed')).toBe('false');
		await fireEvent.click(toggle);
		const path = await waitFor(() => { const el = document.getElementById('service-url-0-path') as HTMLInputElement | null; expect(el).toBeTruthy(); return el!; });
		await fireEvent.input(path, { target: { value: 'api' } });
		expect(screen.getByRole('button', { name: 'Remove the path of backend 1' }).getAttribute('aria-pressed')).toBe('true');
		expect(await sent()).toEqual([{ url: 'http://app/api' }]);

		calls = [];
		await fireEvent.click(screen.getByRole('button', { name: 'Back' }));
		await fireEvent.click(screen.getByRole('button', { name: 'Remove the path of backend 1' }));
		expect(document.getElementById('service-url-0-path')).toBeNull();
		expect(await sent()).toEqual([{ url: 'http://app' }]);
	});
	it('fills protocol, host, port and path from a URL pasted into the host field', async () => {
		handlers = dry();
		const host = await openBackends();
		await fireEvent.input(host, { target: { value: 'https://app.internal:8443/api' } });
		expect(host.value).toBe('app.internal');
		expect((document.getElementById('service-url-0-port') as HTMLInputElement).value).toBe('8443');
		expect((document.getElementById('service-url-0-path') as HTMLInputElement).value).toBe('/api');
		expect(document.getElementById('service-url-0-scheme')!.textContent).toContain('https');
		expect(await sent()).toEqual([{ url: 'https://app.internal:8443/api' }]);
	});
	it('points at the port when it is the field at fault', async () => {
		handlers = dry();
		const host = await openBackends();
		await fireEvent.input(host, { target: { value: 'app' } });
		await fireEvent.input(document.getElementById('service-url-0-port')!, { target: { value: '99999' } });
		await fireEvent.click(screen.getByRole('button', { name: 'Review change' }));
		await waitFor(() => expect(screen.getAllByText('Use a port between 1 and 65535.').length).toBeGreaterThan(0));
		expect(document.getElementById('service-url-0-port')!.getAttribute('aria-invalid')).toBe('true');
		expect(host.getAttribute('aria-invalid')).toBeNull();
		expect(calls.some((c) => c.method === 'PUT')).toBe(false);
	});
});

const tcpSvc = (name: string, config: Record<string, unknown> = {}, version = 1, extra = {}) => service(name, { protocol: 'tcp', servers: [{ address: 'db:5432' }], ...config }, version, extra);
const udpSvc = (name: string, config: Record<string, unknown> = {}, version = 1, extra = {}) => service(name, { protocol: 'udp', servers: [{ address: 'dns:53' }], ...config }, version, extra);

describe('one list for every protocol', () => {
	const mixed = () => base([service('web'), tcpSvc('db', { tcp: { tls: true, proxy_protocol: 2 }, used_by: usedBy('pg@docker') }), tcpSvc('cache', { servers: [{ address: 'redis:6379' }, { address: 'redis2:6379' }] }), udpSvc('dns')]);
	const rowOf = (name: string) => screen.getAllByText(name)[0].closest('tr') as HTMLElement;
	async function chooseProtocol(label: RegExp) {
		await fireEvent.click(screen.getByRole('button', { name: /Filters/ }));
		await fireEvent.click(screen.getByRole('menuitem', { name: 'Protocol' }));
		await fireEvent.click(await screen.findByRole('menuitemradio', { name: label }));
	}

	it('shows HTTP, TCP and UDP services in one table from a single request, with a Protocol column', async () => {
		handlers = mixed();
		render(ServicesPage, { navigate: () => {} });
		await waitFor(() => expect(screen.getAllByText('web').length).toBeGreaterThan(0));
		for (const n of ['db', 'cache', 'dns']) expect(screen.getAllByText(n).length).toBeGreaterThan(0);
		expect(screen.getByRole('columnheader', { name: /Protocol/ })).toBeTruthy();
		expect(screen.queryByRole('tab')).toBeNull();
		expect(within(rowOf('web')).getByText('HTTP')).toBeTruthy();
		expect(within(rowOf('db')).getByText('TCP')).toBeTruthy();
		expect(within(rowOf('dns')).getByText('UDP')).toBeTruthy();
		expect(calls.filter((c) => c.path === '/api/v1/services')).toHaveLength(1);
	});
	it('shows what matters for each protocol in its row', async () => {
		handlers = mixed();
		render(ServicesPage, { navigate: () => {} });
		await waitFor(() => expect(screen.getAllByText('db').length).toBeGreaterThan(0));
		expect(within(rowOf('db')).getByText('TLS')).toBeTruthy();
		expect(within(rowOf('db')).getByText('PROXY v2')).toBeTruthy();
		expect(within(rowOf('db')).getAllByText('db:5432').length).toBeGreaterThan(0);
		expect(within(rowOf('cache')).getByText('+1')).toBeTruthy();
		expect(within(rowOf('web')).queryByText('TLS')).toBeNull();
	});
	it('filters by protocol, with a count per protocol', async () => {
		handlers = mixed();
		render(ServicesPage, { navigate: () => {} });
		await waitFor(() => expect(screen.getAllByText('web').length).toBeGreaterThan(0));
		await chooseProtocol(/TCP \(2\)/);
		await waitFor(() => expect(screen.queryByText('web')).toBeNull());
		expect(screen.queryByText('dns')).toBeNull();
		expect(screen.getAllByText('db').length).toBeGreaterThan(0);
		expect(screen.getAllByText('cache').length).toBeGreaterThan(0);
		expect(screen.getByText(/TCP routers send traffic to these services/)).toBeTruthy();
		expect(calls.filter((c) => c.path === '/api/v1/services')).toHaveLength(1);
	});
	it('combines the protocol filter with the search', async () => {
		handlers = mixed();
		render(ServicesPage, { navigate: () => {} });
		await waitFor(() => expect(screen.getAllByText('web').length).toBeGreaterThan(0));
		await chooseProtocol(/TCP/);
		await fireEvent.input(screen.getByRole('searchbox'), { target: { value: 'redis' } });
		await waitFor(() => expect(screen.queryByText('db')).toBeNull());
		expect(screen.getAllByText('cache').length).toBeGreaterThan(0);
	});
	it('finds a protocol by typing its name in the search', async () => {
		handlers = mixed();
		render(ServicesPage, { navigate: () => {} });
		await waitFor(() => expect(screen.getAllByText('web').length).toBeGreaterThan(0));
		await fireEvent.input(screen.getByRole('searchbox'), { target: { value: 'udp' } });
		await waitFor(() => expect(screen.queryByText('web')).toBeNull());
		expect(screen.getAllByText('dns').length).toBeGreaterThan(0);
	});
	it('creates through one dropdown that offers every protocol', async () => {
		handlers = mixed();
		const paths: string[] = [];
		render(ServicesPage, { navigate: (p: string) => paths.push(p) });
		await screen.findAllByText('web');
		expect(screen.queryByRole('button', { name: 'Create TCP service' })).toBeNull();
		const items = screen.getAllByRole('menuitem').filter((m) => /service/.test(m.textContent ?? '') && /Create|HTTP|TCP|UDP/.test(m.textContent ?? ''));
		expect(items.map((m) => m.querySelector('strong')?.textContent)).toEqual(['HTTP service', 'TCP service', 'UDP service']);
		await fireEvent.click(screen.getByRole('menuitem', { name: /^TCP service/ }));
		await fireEvent.click(screen.getByRole('menuitem', { name: /^UDP service/ }));
		await fireEvent.click(screen.getByRole('menuitem', { name: /^HTTP service/ }));
		expect(paths).toEqual(['/tcp-services/new', '/udp-services/new', '/services/new']);
	});
	it('opens each service under the base path of its protocol', async () => {
		handlers = mixed();
		const paths: string[] = [];
		render(ServicesPage, { navigate: (p: string) => paths.push(p) });
		await screen.findAllByText('web');
		await fireEvent.click(within(rowOf('db')).getByRole('button', { name: 'View' }));
		await fireEvent.click(within(rowOf('dns')).getByRole('menuitem', { name: 'Edit' }));
		await fireEvent.click(within(rowOf('web')).getByRole('button', { name: 'View' }));
		expect(paths).toEqual(['/tcp-services/db', '/udp-services/dns/edit', '/services/web']);
	});
	it('lists the routers of a TCP service and opens them on the TCP router page', async () => {
		handlers = mixed();
		const paths: string[] = [];
		render(ServicesPage, { navigate: (p: string) => paths.push(p) });
		await screen.findAllByText('db');
		await fireEvent.click(within(rowOf('db')).getByText('1 resource'));
		const dialog = await screen.findByRole('dialog');
		expect(dialog.textContent).toContain('pg@docker');
		await fireEvent.click(within(dialog).getByRole('button', { name: 'pg@docker' }));
		expect(paths).toContain('/tcp-routers/pg%40docker');
	});
	it('returns to the one list after a TCP dialog is closed', async () => {
		handlers = mixed();
		const paths: string[] = [];
		render(ServicesPage, { navigate: (p: string) => paths.push(p), tab: 'tcp', mode: 'view', name: 'db' });
		await fireEvent.click((await screen.findAllByRole('button', { name: 'Close' }))[0]);
		expect(paths).toContain('/services');
		expect(paths.some((p) => p.startsWith('/tcp-services'))).toBe(false);
	});
	it('opens every dialog from the list already loaded, without asking for services again', async () => {
		for (const [protocol, mode, name] of [['http', 'create', ''], ['tcp', 'create', ''], ['udp', 'create', ''], ['http', 'edit', 'web'], ['tcp', 'view', 'db']] as const) {
			cleanup();
			calls = [];
			handlers = mixed();
			render(ServicesPage, { navigate: () => {}, tab: protocol, mode, name });
			const dialog = await screen.findByRole('dialog');
			await waitFor(() => expect(within(dialog).queryByText('Loading…')).toBeNull());
			await screen.findAllByText('web');
			expect(calls.filter((c) => c.method === 'GET' && c.path === '/api/v1/services'), `${protocol} ${mode}`).toHaveLength(1);
		}
	});
	it('shows the list error inside the dialog when the list could not be loaded', async () => {
		handlers = { 'GET /api/v1/services': () => json({ code: 'ERR_INTERNAL', message: 'down' }, 500) };
		render(ServicesPage, { navigate: () => {}, tab: 'tcp', mode: 'create' });
		const dialog = await screen.findByRole('dialog');
		await waitFor(() => expect(within(dialog).getByRole('alert')).toBeTruthy());
		expect(document.getElementById('service-name')).toBeNull();
		// Retry asks the page for the list again and then shows the form.
		handlers = mixed();
		await fireEvent.click(within(dialog).getByRole('button', { name: 'Retry' }));
		await waitFor(() => expect(document.getElementById('service-name')).toBeTruthy());
	});
	it('offers the same dropdown in the empty state, and clears filters when they hide everything', async () => {
		handlers = base([]);
		render(ServicesPage, { navigate: () => {} });
		await waitFor(() => expect(screen.getByText('No services yet')).toBeTruthy());
		expect(screen.getAllByRole('button', { name: 'Create service' }).length).toBeGreaterThan(0);
		cleanup();
		handlers = base([service('web')]);
		render(ServicesPage, { navigate: () => {} });
		await screen.findAllByText('web');
		await chooseProtocol(/UDP \(0\)/);
		await waitFor(() => expect(screen.getByText('No matching services')).toBeTruthy());
	});
});

describe('TCP and UDP service editors', () => {
	const dry = () => ({ ...base([]), 'PUT /api/v1/services/postgres?dry_run=true': () => json({ preview: preview(), restart_required: false }) });
	async function start(protocol: 'tcp' | 'udp' = 'tcp', name = 'postgres') {
		render(ServicesPage, { navigate: () => {}, tab: protocol, mode: 'create' });
		await waitFor(() => expect(document.getElementById('service-name')).toBeTruthy());
		expect(screen.getAllByText(protocol === 'tcp' ? 'Create TCP service' : 'Create UDP service').length).toBeGreaterThan(0);
		await fireEvent.input(document.getElementById('service-name')!, { target: { value: name } });
		return waitFor(() => { const el = document.getElementById('service-url-0') as HTMLInputElement | null; expect(el).toBeTruthy(); return el!; });
	}

	it('puts the name and the servers on one Service tab', async () => {
		handlers = dry();
		await start('tcp');
		const labels = () => screen.getAllByRole('tab').map((t) => t.textContent?.replace(/\s+/g, ' ').trim());
		expect(labels()).toEqual(['Service', 'Options', 'Review changes']);
		const panel = document.getElementById('panel-service-backends')!;
		expect(panel.contains(document.getElementById('service-name'))).toBe(true);
		expect(panel.contains(document.getElementById('service-url-0'))).toBe(true);
		expect(document.getElementById('panel-service-identity')).toBeNull();
		expect(panel.hidden).toBe(false);
		expect(screen.queryByRole('button', { name: 'Back' })).toBeNull();
		cleanup();
		render(ServicesPage, { navigate: () => {}, tab: 'udp', mode: 'create' });
		await waitFor(() => expect(document.getElementById('service-name')).toBeTruthy());
		expect(labels()).toEqual(['Service', 'Review changes']);
		expect(screen.queryByRole('button', { name: 'Continue' })).toBeNull();
		expect(screen.getByRole('button', { name: 'Review change' })).toBeTruthy();
	});
	it('keeps HTTP on separate Identity and Backend servers tabs', async () => {
		handlers = base([]);
		render(ServicesPage, { navigate: () => {}, tab: 'http', mode: 'create' });
		await waitFor(() => expect(document.getElementById('service-name')).toBeTruthy());
		expect(screen.getAllByRole('tab').map((t) => t.textContent?.replace(/\s+/g, ' ').trim())).toEqual(['Identity', 'Backend servers', 'Review changes']);
		expect(document.getElementById('panel-service-identity')!.contains(document.getElementById('service-name'))).toBe(true);
	});
	it('reports a missing name on the Service tab without leaving it', async () => {
		handlers = dry();
		await start('tcp', '');
		await fireEvent.click(screen.getByRole('button', { name: 'Continue' }));
		await waitFor(() => expect(document.getElementById('service-name')!.getAttribute('aria-invalid')).toBe('true'));
		expect(document.getElementById('panel-service-backends')!.hidden).toBe(false);
		expect(calls.some((c) => c.method === 'PUT')).toBe(false);
	});
	it('has host and port only, and needs the port', async () => {
		handlers = dry();
		const host = await start();
		expect(document.getElementById('service-url-0-scheme')).toBeNull();
		expect(screen.queryByRole('button', { name: /path/ })).toBeNull();
		await fireEvent.input(host, { target: { value: 'db.internal' } });
		await fireEvent.click(screen.getByRole('button', { name: 'Continue' }));
		await fireEvent.click(screen.getByRole('button', { name: 'Review change' }));
		await waitFor(() => expect(screen.getAllByText(/Enter a host and a port/).length).toBeGreaterThan(0));
		expect(document.getElementById('service-url-0-port')!.getAttribute('aria-invalid')).toBe('true');
		expect(calls.some((c) => c.method === 'PUT')).toBe(false);
	});
	it('sends plain servers when no option is set', async () => {
		handlers = dry();
		const host = await start();
		await fireEvent.input(host, { target: { value: 'tcp://10.0.0.5:5432' } });
		expect(host.value).toBe('10.0.0.5');
		await fireEvent.click(screen.getByRole('button', { name: 'Continue' }));
		await waitFor(() => expect(screen.getByText(/All optional/)).toBeTruthy());
		await fireEvent.click(screen.getByRole('button', { name: 'Review change' }));
		await waitFor(() => expect(calls.some((c) => c.method === 'PUT')).toBe(true));
		expect(calls.find((c) => c.method === 'PUT')!.body).toEqual({ protocol: 'tcp', servers: [{ address: '10.0.0.5:5432' }] });
	});
	it('sends every TCP option that was chosen', async () => {
		handlers = dry();
		const host = await start();
		await fireEvent.input(host, { target: { value: 'db.internal:5432' } });
		await fireEvent.click(screen.getByRole('button', { name: 'Continue' }));
		await fireEvent.click(await screen.findByLabelText(/Connect to the backends with TLS/));
		await fireEvent.input(document.getElementById('service-server-name')!, { target: { value: 'db.example.com' } });
		await fireEvent.click(screen.getByLabelText(/Do not verify backend TLS/));
		await fireEvent.click(document.getElementById('service-proxy-protocol')!);
		await fireEvent.click(await screen.findByRole('option', { name: 'Version 2' }));
		await fireEvent.click(screen.getByLabelText(/Enable health check/));
		await fireEvent.input(document.getElementById('service-health-interval')!, { target: { value: '10s' } });
		await fireEvent.input(document.getElementById('service-health-port')!, { target: { value: '8008' } });
		await fireEvent.input(document.getElementById('service-health-send')!, { target: { value: 'PING' } });
		await fireEvent.input(document.getElementById('service-health-expect')!, { target: { value: 'PONG' } });
		await fireEvent.input(document.getElementById('service-dial-timeout')!, { target: { value: '5s' } });
		await fireEvent.click(screen.getByRole('button', { name: 'Review change' }));
		await waitFor(() => expect(calls.some((c) => c.method === 'PUT')).toBe(true));
		expect(calls.find((c) => c.method === 'PUT')!.body).toEqual({
			protocol: 'tcp',
			servers: [{ address: 'db.internal:5432' }],
			insecure_skip_verify: true,
			tcp: { tls: true, server_name: 'db.example.com', proxy_protocol: 2, dial_timeout: '5s', health_check: { interval: '10s', port: 8008, send: 'PING', expect: 'PONG' } }
		});
	});
	it('takes the user to the Options tab for an invalid option', async () => {
		handlers = dry();
		const host = await start();
		await fireEvent.input(host, { target: { value: 'db:5432' } });
		await fireEvent.click(screen.getByRole('button', { name: 'Continue' }));
		await fireEvent.click(await screen.findByLabelText(/Enable health check/));
		await fireEvent.input(document.getElementById('service-health-interval')!, { target: { value: 'soon' } });
		await fireEvent.click(screen.getByRole('button', { name: 'Back' }));
		expect(document.getElementById('panel-service-backends')!.hidden).toBe(false);
		await fireEvent.click(screen.getByRole('tab', { name: /Review/ }));
		await waitFor(() => expect(document.getElementById('panel-service-options')!.hidden).toBe(false));
		expect(document.getElementById('service-health-interval')!.getAttribute('aria-invalid')).toBe('true');
		expect(calls.some((c) => c.method === 'PUT')).toBe(false);
	});
	it('loads a TCP service with its options and keeps it a TCP service', async () => {
		handlers = {
			...base([tcpSvc('postgres', { servers: [{ address: '[fd00::5]:5432' }], insecure_skip_verify: true, tcp: { tls: true, server_name: 'db.example.com', proxy_protocol: 1, termination_delay: '200ms', health_check: { interval: '30s' } } }, 6)]),
			'PUT /api/v1/services/postgres?dry_run=true': () => json({ preview: preview(), restart_required: false })
		};
		render(ServicesPage, { navigate: () => {}, tab: 'tcp', mode: 'edit', name: 'postgres' });
		const host = await waitFor(() => { const el = document.getElementById('service-url-0') as HTMLInputElement | null; expect(el).toBeTruthy(); return el!; });
		expect(host.value).toBe('fd00::5');
		expect((document.getElementById('service-url-0-port') as HTMLInputElement).value).toBe('5432');
		await fireEvent.click(screen.getByRole('tab', { name: /Options/ }));
		expect((document.getElementById('service-server-name') as HTMLInputElement).value).toBe('db.example.com');
		expect(document.getElementById('service-proxy-protocol')!.textContent).toContain('Version 1');
		expect((document.getElementById('service-termination-delay') as HTMLInputElement).value).toBe('200ms');
		await fireEvent.click(screen.getByRole('button', { name: 'Review change' }));
		await waitFor(() => expect(calls.some((c) => c.method === 'PUT')).toBe(true));
		const put = calls.find((c) => c.method === 'PUT')!;
		expect(put.headers['if-match']).toBe('"6"');
		expect(put.body).toEqual({ protocol: 'tcp', servers: [{ address: '[fd00::5]:5432' }], insecure_skip_verify: true, tcp: { tls: true, server_name: 'db.example.com', proxy_protocol: 1, termination_delay: '200ms', health_check: { interval: '30s' } } });
	});
	it('shows a TCP service as Traefik declares it, with its transport, and checks it in Traefik as TCP', async () => {
		handlers = {
			...base([tcpSvc('postgres', { insecure_skip_verify: true, tcp: { tls: true, proxy_protocol: 2, health_check: { interval: '10s', port: 8008 } } })]),
			'GET /api/v1/services/postgres/diagnostics?protocol=tcp': () => json({ protocol: 'tcp', name: 'postgres', observed: true, runtimeStatus: 'enabled', healthStatus: 'healthy', serverStatus: { UP: 1 }, routers: [{ name: 'pg@docker', status: 'enabled', entryPoints: ['postgres'] }], observedAt: '2026-01-01T00:00:00Z' })
		};
		render(ServicesPage, { navigate: () => {}, tab: 'tcp', mode: 'view', name: 'postgres' });
		const dialog = await screen.findByRole('dialog');
		await waitFor(() => expect(dialog.textContent).toContain('loadBalancer:'));
		const [svc, transport] = [...dialog.querySelectorAll('pre')].map((x) => x.textContent);
		expect(svc).toBe('loadBalancer:\n  servers:\n    - address: db:5432\n      tls: true\n  healthCheck:\n    interval: 10s\n    port: 8008\n  serversTransport: postgres-tcp-transport\n');
		expect(transport).toBe('postgres-tcp-transport:\n  proxyProtocol:\n    version: 2\n  tls:\n    insecureSkipVerify: true\n');
		expect(dialog.textContent).toContain('TCP service');
		await fireEvent.click(screen.getByRole('button', { name: 'Check in Traefik' }));
		await waitFor(() => expect(screen.getByText('Healthy')).toBeTruthy());
		expect(screen.getByText('pg@docker')).toBeTruthy();
		expect(screen.queryByRole('button', { name: 'pg@docker' })).toBeNull(); // no page for TCP routers
	});
	it('creates a UDP service with servers only', async () => {
		handlers = { ...base([]), 'PUT /api/v1/services/dns?dry_run=true': () => json({ preview: preview(), restart_required: false }) };
		const host = await start('udp', 'dns');
		expect(screen.queryByRole('tab', { name: /Options/ })).toBeNull();
		await fireEvent.input(host, { target: { value: '10.0.0.53' } });
		await fireEvent.input(document.getElementById('service-url-0-port')!, { target: { value: '53' } });
		await fireEvent.click(screen.getByRole('button', { name: 'Review change' }));
		await waitFor(() => expect(calls.some((c) => c.method === 'PUT')).toBe(true));
		expect(calls.find((c) => c.method === 'PUT')!.body).toEqual({ protocol: 'udp', servers: [{ address: '10.0.0.53:53' }] });
	});
	it('refuses a name already used by a service of another protocol', async () => {
		handlers = base([service('db')]);
		render(ServicesPage, { navigate: () => {}, tab: 'tcp', mode: 'create' });
		await waitFor(() => expect(document.getElementById('service-name')).toBeTruthy());
		await fireEvent.input(document.getElementById('service-name')!, { target: { value: 'db' } });
		await fireEvent.click(screen.getByRole('button', { name: 'Continue' }));
		await waitFor(() => expect(screen.getAllByText(/already exists/).length).toBeGreaterThan(0));
	});
});

import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/svelte';
// The API client binds `fetch` at import time, so install a forwarding stub first.
vi.hoisted(() => {
	(globalThis as any).__mockFetch = async () => {
		throw new Error('no mock');
	};
	globalThis.fetch = ((...args: unknown[]) => (globalThis as any).__mockFetch(...args)) as typeof fetch;
});
import DomainsPage from '../src/features/domains/DomainsPage.svelte';
import RadioCards from '../src/shared/ui/RadioCards.svelte';
import { flashes } from '../src/shared/ui/flash.svelte';

const preview = (extra = {}) => ({
	changes: [
		{ action: 'created', id: 'domain:domain-shop-test', kind: 'domain', name: 'domain-shop-test', scope: 'dynamic' },
	],
	static_diff: '',
	dynamic_diff: '+d: {}',
	requires_restart: false,
	...extra,
});
const operation = (extra = {}) => ({
	id: 4,
	kind: 'domain.create',
	target: 'domain-shop-test',
	status: 'succeeded',
	summary: 'Applied 1 change',
	requires_restart: false,
	created_at: 'now',
	...extra,
});
const domain = (name: string, config = {}, version = 1) => ({
	id: 'domain:' + name,
	name,
	version,
	origin: 'managed',
	provider: 'file',
	status: 'active',
	read_only: false,
	live_health: 'unknown',
	updated_at: '2026-01-01T00:00:00Z',
	config: {
		name,
		main: name + '.test',
		sans: null,
		method: 'resolver',
		resolver: 'le',
		used_by: null,
		state: 'configured_unverified',
		usage: { routers: [], certificates: [] },
		certificate_state: 'notObserved',
		...config,
	},
});
const routerRef = (name: string) => ({
	name,
	rule: 'Host(`' + name + '.shop.test`)',
	entry_points: ['websecure'],
	service: name,
	tls: true,
	read_only: false,
});
const certRef = (main: string) => ({
	source: 'acme',
	id: 'fp-' + main,
	name: main,
	names: [main],
	validity: 'valid',
	covers: true,
});
const used = (routers: string[], certs: string[] = []) => ({
	used_by: routers,
	usage: { routers: routers.map(routerRef), certificates: certs.map(certRef) },
});
const resolver = (name: string, config = {}) => ({
	id: 'resolver:' + name,
	name,
	version: 1,
	origin: 'managed',
	provider: 'static',
	status: 'active',
	read_only: false,
	live_health: 'unknown',
	config: { type: 'acme', environment: 'staging', challenge: 'dns01', provider: 'cloudflare', ...config },
});
const acme = (main: string) => ({
	resolver_name: 'le',
	main_domain: main,
	sans: null,
	issuer: 'R3',
	serial_number: '1',
	fingerprint: 'fp',
	not_before: '2026-01-01T00:00:00Z',
	expires_at: '2099-01-01T00:00:00Z',
	days_left: 90,
	status: 'valid',
});

interface Call {
	method: string;
	path: string;
	headers: Record<string, string>;
	body?: any;
}
let calls: Call[] = [];
let handlers: Record<string, () => Response | Promise<Response>> = {};
const json = (body: unknown, status = 200) =>
	new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } });

beforeEach(() => {
	flashes.splice(0);
	calls = [];
	HTMLDialogElement.prototype.showModal ??= function () {
		this.setAttribute('open', '');
	};
	HTMLDialogElement.prototype.close ??= function () {
		this.removeAttribute('open');
	};
	(globalThis as any).__mockFetch = async (path: string, init: RequestInit = {}) => {
		const headers: Record<string, string> = {};
		(init.headers as Headers).forEach((v, k) => {
			headers[k.toLowerCase()] = v;
		});
		calls.push({
			method: init.method ?? 'GET',
			path,
			headers,
			body: init.body ? JSON.parse(String(init.body)) : undefined,
		});
		const key = `${init.method ?? 'GET'} ${path}`;
		if (handlers[key]) return handlers[key]();
		return json({ code: 'ERR_NOT_FOUND', message: 'nf', operation_id: 'op', retryable: false }, 404);
	};
});
afterEach(() => {
	cleanup();
	handlers = {};
});

const base = (list: unknown) => ({
	'GET /api/v1/domains': () => json(list),
	'GET /api/v1/certificates': () => json([]),
	'GET /api/v1/acme/certificates': () => json({ certificates: [acme('shop.test')], observations: [], partial: false }),
	'GET /api/v1/resolvers': () => json([resolver('le')]),
	'GET /api/v1/entrypoints': () => json({ entry_points: ['web', 'websecure'] }),
});

describe('DomainsPage states', () => {
	it('lists domains with the certificate state and usage computed by core-v2, in one request', async () => {
		handlers = base([
			domain('shop', { ...used(['r1'], ['app.shop.test']), certificate_state: 'valid' }),
			domain('blog', { certificate_state: 'expiring' }),
		]);
		render(DomainsPage, { navigate: () => {} });
		await waitFor(() => expect(screen.getAllByText('shop.test')[0]).toBeTruthy());
		expect(screen.getAllByText('Valid').length).toBeGreaterThan(0);
		expect(screen.getAllByText(/Expiring/).length).toBeGreaterThan(0);
		expect(screen.getByText('2 resources')).toBeTruthy(); // one route + one certificate
		expect(screen.getByText('Not used')).toBeTruthy();
		expect(calls.map((c) => c.path)).toEqual(['/api/v1/domains']);
	});
	it('opens the Used by dialog with routes and certificates, without another request', async () => {
		const navigate = vi.fn();
		handlers = base([domain('shop', used(['portainer'], ['portainer.shop.test']))]);
		render(DomainsPage, { navigate });
		await fireEvent.click(await screen.findByRole('button', { name: '2 resources' }));
		const dialog = await screen.findByRole('dialog');
		expect(dialog.textContent).toContain('Host(`portainer.shop.test`)');
		expect(dialog.textContent).toContain('Certificate');
		expect(calls).toHaveLength(1);
		await fireEvent.click(screen.getByRole('button', { name: 'portainer.shop.test' }));
		expect(navigate).toHaveBeenCalledWith('/certificates/automatic/fp-portainer.shop.test');
	});
	it('shows "Unavailable" with a warning when core-v2 could not read the usage', async () => {
		handlers = base([domain('shop', { usage: undefined, certificate_state: 'unavailable' })]);
		render(DomainsPage, { navigate: () => {} });
		await waitFor(() => expect(screen.getAllByText('shop.test')[0]).toBeTruthy());
		expect(screen.getByText('Unavailable')).toBeTruthy();
		expect(screen.getByText('Unknown')).toBeTruthy(); // certificate state
		expect(screen.getByText(/Could not read the routers using these domains/)).toBeTruthy();
	});
	it('shows the empty state for a null list', async () => {
		handlers = base(null);
		render(DomainsPage, { navigate: () => {} });
		await waitFor(() => expect(screen.getByText('No domains yet')).toBeTruthy());
	});
	it('shows an error with retry and recovers', async () => {
		handlers = {
			...base([]),
			'GET /api/v1/domains': () =>
				json({ code: 'ERR_INTERNAL', message: 'db is locked', operation_id: 'o', retryable: true }, 500),
		};
		render(DomainsPage, { navigate: () => {} });
		await waitFor(() => expect(screen.getAllByText('db is locked').length).toBeGreaterThan(0));
		handlers['GET /api/v1/domains'] = () => json([domain('back')]);
		await fireEvent.click(screen.getAllByRole('button', { name: /retry|try again/i })[0]);
		await waitFor(() => expect(screen.getAllByText('back.test')[0]).toBeTruthy());
	});
	it('disables Delete while a router uses the domain', async () => {
		handlers = base([domain('shop', used(['r1'])), domain('blog', used([], ['blog.test']))]);
		render(DomainsPage, { navigate: () => {} });
		await waitFor(() => expect(screen.getAllByText('blog.test')[0]).toBeTruthy());
		// A certificate does not block deleting the domain; a router does.
		const buttons = screen.getAllByRole('menuitem', { name: 'Delete' }) as HTMLButtonElement[];
		expect(buttons.filter((b) => b.disabled)).toHaveLength(1);
	});
});

describe('add flow', () => {
	async function fillDomain(main: string) {
		await waitFor(() => expect(document.getElementById('domain-main')).toBeTruthy());
		await fireEvent.input(document.getElementById('domain-main')!, { target: { value: main } });
	}
	it('derives the id, previews with a dry run, then applies with a create precondition and one key', async () => {
		handlers = {
			...base([]),
			'PUT /api/v1/domains/domain-shop-test?dry_run=true': () => json({ preview: preview(), restart_required: false }),
			'PUT /api/v1/domains/domain-shop-test': () =>
				json({
					operation: operation(),
					resource: domain('domain-shop-test', { main: 'shop.test' }),
					restart_required: false,
				}),
		};
		render(DomainsPage, { navigate: () => {}, mode: 'create' });
		await fillDomain('Shop.test');
		expect(document.getElementById('domain-name')).toBeNull();

		await fireEvent.click(screen.getByRole('button', { name: 'Continue' }));
		await fireEvent.click(await screen.findByLabelText(/Automatic/));
		await fireEvent.click(await waitFor(() => document.getElementById('domain-reference')!));
		await fireEvent.click(await screen.findByRole('option', { name: /le/ }));
		await fireEvent.click(screen.getByRole('button', { name: 'Review change' }));
		await screen.findByRole('button', { name: 'Apply changes' });

		const dry = calls.find((c) => c.method === 'PUT')!;
		expect(dry.path).toBe('/api/v1/domains/domain-shop-test?dry_run=true');
		expect(dry.headers['if-none-match']).toBe('*');
		expect(dry.body).toEqual({ main: 'shop.test', sans: [], method: 'resolver', resolver: 'le' });

		await fireEvent.click(await screen.findByRole('button', { name: 'Apply changes' }));
		await waitFor(() => expect(flashes.some((f) => f.title.startsWith('Applied'))).toBe(true));
		const apply = calls.filter((c) => c.method === 'PUT')[1];
		expect(apply.path).toBe('/api/v1/domains/domain-shop-test');
		expect(apply.headers['idempotency-key']).toMatch(/^[A-Za-z0-9_-]{16,128}$/);
		expect(calls.some((c) => c.path.includes('/draft'))).toBe(false);
	});
	it('blocks Continue on an invalid hostname and stages nothing', async () => {
		handlers = base([]);
		render(DomainsPage, { navigate: () => {}, mode: 'create' });
		await fillDomain('not a domain');
		await fireEvent.click(screen.getByRole('button', { name: 'Continue' }));
		await waitFor(() => expect(document.getElementById('domain-main')!.getAttribute('aria-invalid')).toBe('true'));
		expect(calls.some((c) => c.method === 'PUT')).toBe(false);
	});
	it('routes a server binding error to the certificate field and keeps the form', async () => {
		handlers = {
			...base([]),
			'PUT /api/v1/domains/domain-shop-test?dry_run=true': () =>
				json(
					{
						code: 'ERR_VALIDATION',
						message: 'wildcard domains (*.example.com) require a DNS-01 challenge resolver',
						operation_id: 'o',
						retryable: false,
					},
					422,
				),
		};
		render(DomainsPage, { navigate: () => {}, mode: 'create' });
		await fillDomain('shop.test');
		await fireEvent.click(screen.getByRole('button', { name: 'Continue' }));
		await fireEvent.click(await screen.findByLabelText(/Automatic/));
		await fireEvent.click(await waitFor(() => document.getElementById('domain-reference')!));
		await fireEvent.click(await screen.findByRole('option', { name: /le/ }));
		await fireEvent.click(screen.getByRole('button', { name: 'Review change' }));
		await waitFor(() => expect(document.getElementById('domain-reference')!.getAttribute('aria-invalid')).toBe('true'));
		expect((document.getElementById('domain-main') as HTMLInputElement).value).toBe('shop.test');
	});
});

describe('view, probe and delete', () => {
	it('checks what Traefik serves and shows which hostname was checked', async () => {
		handlers = {
			...base([domain('shop', used(['r1']))]),
			'POST /api/v1/domains/shop/probe': () =>
				json({
					state: 'matches',
					trust: 'trusted',
					hostname: 'r1.shop.test',
					address: '127.0.0.1:18443',
					observed_at: '2026-01-01T00:00:00Z',
				}),
		};
		render(DomainsPage, { navigate: () => {}, mode: 'view', name: 'shop' });
		await fireEvent.click(await screen.findByRole('button', { name: 'Check Traefik' }));
		await waitFor(() => expect(screen.getByText('Matches expected certificate')).toBeTruthy());
		expect(screen.getByText('r1.shop.test')).toBeTruthy();
		expect(calls.some((c) => c.method === 'POST' && c.path.endsWith('/probe'))).toBe(true);
	});
	it('reports an unreachable entry point without a meaningless trust value', async () => {
		handlers = {
			...base([domain('shop', used(['r1']))]),
			'POST /api/v1/domains/shop/probe': () =>
				json({
					state: 'unreachable',
					trust: 'unknown',
					hostname: 'r1.shop.test',
					address: '127.0.0.1:443',
					observed_at: '2026-01-01T00:00:00Z',
					error: 'Nothing answered at 127.0.0.1:443 for the websecure entry point.',
				}),
		};
		render(DomainsPage, { navigate: () => {}, mode: 'view', name: 'shop' });
		await fireEvent.click(await screen.findByRole('button', { name: 'Check Traefik' }));
		expect(await screen.findByText(/Nothing answered at 127.0.0.1:443/)).toBeTruthy();
		expect(screen.queryByText('unknown')).toBeNull();
	});
	it('offers no check for a domain no router uses, and offers to create a route', async () => {
		handlers = base([domain('shop')]);
		render(DomainsPage, { navigate: () => {}, mode: 'view', name: 'shop' });
		await waitFor(() => expect(screen.getAllByText('shop.test').length).toBeGreaterThan(0));
		expect(screen.queryByRole('button', { name: 'Check Traefik' })).toBeNull();
		expect(screen.getByRole('button', { name: 'Create HTTPS router' })).toBeTruthy();
	});
	it('shows the view as one summary with the certificate state, without tabs', async () => {
		handlers = base([domain('shop', { ...used(['r1'], ['app.shop.test']), certificate_state: 'valid' })]);
		render(DomainsPage, { navigate: () => {}, mode: 'view', name: 'shop' });
		await waitFor(() => expect(screen.getByText('Certificate validity')).toBeTruthy());
		expect(screen.queryAllByRole('tab')).toHaveLength(0);
		expect(screen.queryByText(/observation is incomplete/)).toBeNull();
	});
	it('deletes with the version read with the list', async () => {
		handlers = {
			...base([domain('blog', {}, 8)]),
			'DELETE /api/v1/domains/blog': () => json({ operation: operation(), restart_required: false }),
		};
		render(DomainsPage, { navigate: () => {} });
		await waitFor(() => expect(screen.getAllByText('blog.test')[0]).toBeTruthy());
		await fireEvent.click(screen.getAllByRole('menuitem', { name: 'Delete' })[0]);
		await fireEvent.click(await screen.findByRole('button', { name: 'Confirm deletion' }));
		await waitFor(() => expect(calls.some((c) => c.method === 'DELETE')).toBe(true));
		expect(calls.find((c) => c.method === 'DELETE')!.headers['if-match']).toBe('"8"');
	});
});

describe('RadioCards', () => {
	it('reports the chosen value and shows its error', async () => {
		const seen: string[] = [];
		render(RadioCards, {
			legend: 'Method',
			value: '',
			options: [
				{ value: 'a', label: 'Alpha' },
				{ value: 'b', label: 'Beta', description: 'second' },
			],
			error: 'Pick one',
			onchange: (v: string) => seen.push(v),
		});
		await fireEvent.click(screen.getByLabelText(/Beta/));
		expect(seen).toEqual(['b']);
		expect(screen.getByRole('alert').textContent).toContain('Pick one');
	});
});

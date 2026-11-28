import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/svelte';
// The API client binds `fetch` at import time, so install a forwarding stub first.
vi.hoisted(() => {
	(globalThis as any).__mockFetch = async () => {
		throw new Error('no mock');
	};
	globalThis.fetch = ((...args: unknown[]) => (globalThis as any).__mockFetch(...args)) as typeof fetch;
});
import ServicesPage from '../src/features/services/ServicesPage.svelte';
import { flashes } from '../src/shared/ui/flash.svelte';
import { reconnect } from '../src/core/mutations';

/* Create, edit and delete for every protocol, with the successes and the failures a person can hit. */

type Protocol = 'http' | 'tcp' | 'udp';
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
const failure = (status: number, code: string, message: string) =>
	json({ code, message, operation_id: 'op-1', retryable: false }, status);

const preview = (name: string) => ({
	changes: [{ action: 'created', id: 'service:' + name, kind: 'service', name, scope: 'dynamic' }],
	static_diff: '',
	dynamic_diff: '+s: {}',
	requires_restart: false,
});
const operation = (target: string) => ({
	id: 5,
	kind: 'service.create',
	target,
	status: 'succeeded',
	summary: 'Applied 1 change',
	requires_restart: false,
	created_at: 'now',
});
const none = { routers: [] };
const DEFAULTS: Record<Protocol, Record<string, unknown>> = {
	http: { servers: [{ url: 'http://app:8080' }] },
	tcp: { protocol: 'tcp', servers: [{ address: 'db:5432' }] },
	udp: { protocol: 'udp', servers: [{ address: 'dns:53' }] },
};
const svc = (protocol: Protocol, name: string, config: Record<string, unknown> = {}, version = 1, extra = {}) => ({
	id: 'service:' + name,
	name,
	version,
	origin: 'managed',
	provider: 'file',
	status: 'active',
	read_only: false,
	live_health: 'loaded',
	updated_at: '2026-01-01T00:00:00Z',
	config: { used_by: none, ...DEFAULTS[protocol], ...config },
	...extra,
});
const used = (...names: string[]) => ({
	routers: names.map((name) => ({
		name,
		rule: 'Host(`x`)',
		entry_points: ['web'],
		service: 'x',
		tls: false,
		read_only: false,
	})),
});
const list = (rows: unknown) => ({ 'GET /api/v1/services': () => json(rows) });

beforeEach(() => {
	// These tests cover the manual retry offered once the automatic reconnect gave up.
	reconnect.windowMs = 0;
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
		const handler = handlers[`${init.method ?? 'GET'} ${path}`];
		if (handler) return handler();
		return failure(404, 'ERR_NOT_FOUND', 'not found');
	};
});
afterEach(() => {
	cleanup();
	handlers = {};
});

const writes = (method = 'PUT') => calls.filter((c) => c.method === method);
const applies = () => writes().filter((c) => !c.path.includes('dry_run'));
const rowOf = (name: string) => screen.getAllByText(name)[0].closest('tr') as HTMLElement;

/* ---------------------------------------------------------------- create */

/** What a person types for each protocol. */
const CREATE: Record<Protocol, { name: string; tab?: Protocol; fill: () => Promise<void>; body: unknown }> = {
	http: {
		name: 'web',
		fill: async () => {
			await fireEvent.click(screen.getByRole('button', { name: 'Continue' }));
			await fireEvent.input(await waitFor(() => document.getElementById('service-url-0')!), {
				target: { value: 'http://app:8080' },
			});
		},
		body: { protocol: 'http', servers: [{ url: 'http://app:8080' }] },
	},
	tcp: {
		name: 'postgres',
		tab: 'tcp',
		fill: async () => {
			await fireEvent.input(await waitFor(() => document.getElementById('service-url-0')!), {
				target: { value: 'db.internal' },
			});
			await fireEvent.input(document.getElementById('service-url-0-port')!, { target: { value: '5432' } });
		},
		body: { protocol: 'tcp', servers: [{ address: 'db.internal:5432' }] },
	},
	udp: {
		name: 'dns',
		tab: 'udp',
		fill: async () => {
			await fireEvent.input(await waitFor(() => document.getElementById('service-url-0')!), {
				target: { value: '10.0.0.53' },
			});
			await fireEvent.input(document.getElementById('service-url-0-port')!, { target: { value: '53' } });
		},
		body: { protocol: 'udp', servers: [{ address: '10.0.0.53:53' }] },
	},
};

async function fillCreate(protocol: Protocol, navigate: (to: string) => void = () => {}) {
	const c = CREATE[protocol];
	render(ServicesPage, { navigate, mode: 'create', ...(c.tab ? { tab: c.tab } : {}) });
	await waitFor(() => expect(document.getElementById('service-name')).toBeTruthy());
	await fireEvent.input(document.getElementById('service-name')!, { target: { value: c.name } });
	await c.fill();
	return c;
}
/** The review button sits on the last tab; from an earlier one, Continue gets there. */
const review = async () => {
	if (!screen.queryByRole('button', { name: 'Review change' }))
		await fireEvent.click(screen.getByRole('button', { name: 'Continue' }));
	await fireEvent.click(await screen.findByRole('button', { name: 'Review change' }));
};
const apply = async () => fireEvent.click(await screen.findByRole('button', { name: 'Apply changes' }));

describe.each(['http', 'tcp', 'udp'] as Protocol[])('creating a %s service', (protocol) => {
	const c = CREATE[protocol];
	const path = `/api/v1/services/${c.name}`;
	const ok = () => ({
		...list([]),
		[`PUT ${path}?dry_run=true`]: () => json({ preview: preview(c.name), restart_required: false }),
		[`PUT ${path}`]: () =>
			json({ operation: operation(c.name), resource: svc(protocol, c.name), restart_required: false }),
	});

	it('previews, then applies once with a create precondition, and returns to the list', async () => {
		handlers = ok();
		const navigate = vi.fn();
		await fillCreate(protocol, navigate);
		await review();
		await apply();
		await waitFor(() => expect(navigate).toHaveBeenCalledWith('/services'));
		const [dry] = writes();
		expect(dry.path).toBe(`${path}?dry_run=true`);
		expect(dry.headers['if-none-match']).toBe('*');
		expect(dry.body).toEqual(c.body);
		expect(applies()).toHaveLength(1);
		expect(applies()[0].headers['if-none-match']).toBe('*');
		expect(applies()[0].headers['idempotency-key']).toMatch(/^[A-Za-z0-9_-]{16,128}$/);
		expect(applies()[0].body).toEqual(c.body);
		expect(flashes.some((f) => f.title.startsWith('Applied'))).toBe(true);
	});

	it('never writes before the review is confirmed', async () => {
		handlers = ok();
		await fillCreate(protocol);
		await review();
		await screen.findByRole('button', { name: 'Apply changes' });
		expect(applies()).toHaveLength(0);
	});

	it('shows why the preview was refused and stays on the form', async () => {
		handlers = {
			...ok(),
			[`PUT ${path}?dry_run=true`]: () => failure(422, 'ERR_VALIDATION_FAILED', 'server 0 is not reachable'),
		};
		await fillCreate(protocol);
		await review();
		await waitFor(() => expect(screen.getAllByText(/server 0 is not reachable/).length).toBeGreaterThan(0));
		expect(screen.queryByRole('button', { name: 'Apply changes' })).toBeNull();
		expect(applies()).toHaveLength(0);
	});

	it('goes back to the form when the name was taken meanwhile (409)', async () => {
		handlers = { ...ok(), [`PUT ${path}`]: () => failure(409, 'ERR_VERSION_CONFLICT', 'the service already exists') };
		await fillCreate(protocol);
		await review();
		await apply();
		await waitFor(() =>
			expect(screen.getAllByText(/service changed since the form was opened/).length).toBeGreaterThan(0),
		);
		expect(screen.queryByRole('button', { name: 'Apply changes' })).toBeNull();
		expect(screen.queryByRole('button', { name: /Continue|Review change/ })).toBeTruthy();
	});

	it('reports a rollback and asks for a new review (422)', async () => {
		handlers = {
			...ok(),
			[`PUT ${path}`]: () => failure(422, 'ERR_DEPLOY_ROLLED_BACK', 'Traefik rejected the change; it was rolled back'),
		};
		await fillCreate(protocol);
		await review();
		await apply();
		await waitFor(() => expect(screen.getAllByText(/rolled back/).length).toBeGreaterThan(0));
		expect(screen.queryByRole('button', { name: 'Apply changes' })).toBeNull();
		expect(screen.queryByRole('button', { name: /Continue|Review change/ })).toBeTruthy();
	});

	it('shows the server message when recovery is required (409)', async () => {
		handlers = {
			...ok(),
			[`PUT ${path}`]: () =>
				failure(409, 'ERR_RECOVERY_REQUIRED', 'Apply failed at runtime_verification. Recover or abandon operation 43.'),
		};
		await fillCreate(protocol);
		await review();
		await apply();
		await waitFor(() => expect(screen.getAllByText(/Recover or abandon operation 43/).length).toBeGreaterThan(0));
	});

	it('shows an unexpected server error (500) without leaving the review', async () => {
		handlers = { ...ok(), [`PUT ${path}`]: () => failure(500, 'ERR_INTERNAL', 'Something went wrong.') };
		const navigate = vi.fn();
		await fillCreate(protocol, navigate);
		await review();
		await apply();
		await waitFor(() => expect(screen.getAllByText(/Something went wrong/).length).toBeGreaterThan(0));
		expect(navigate).not.toHaveBeenCalled();
		expect(flashes.some((f) => f.title.startsWith('Applied'))).toBe(false);
	});

	it('retries a lost response with the same idempotency key', async () => {
		let attempts = 0;
		handlers = {
			...ok(),
			[`PUT ${path}`]: () => {
				attempts++;
				if (attempts === 1) throw new TypeError('Failed to fetch');
				return json({ operation: operation(c.name), resource: svc(protocol, c.name), restart_required: false });
			},
		};
		const navigate = vi.fn();
		await fillCreate(protocol, navigate);
		await review();
		await apply();
		await waitFor(() => expect(screen.getAllByText(/response was lost/i).length).toBeGreaterThan(0));
		expect(navigate).not.toHaveBeenCalled();
		await fireEvent.click(await screen.findByRole('button', { name: 'Retry same operation' }));
		await waitFor(() => expect(navigate).toHaveBeenCalledWith('/services'));
		const [first, second] = applies();
		expect(first.headers['idempotency-key']).toBe(second.headers['idempotency-key']);
	});

	it('asks for a manual restart when Traefik needs one, but still saves', async () => {
		handlers = {
			...ok(),
			[`PUT ${path}`]: () =>
				json(
					{
						operation: operation(c.name),
						resource: svc(protocol, c.name),
						restart_required: true,
						error: { code: 'ERR_MANUAL_RESTART_REQUIRED' },
					},
					202,
				),
		};
		await fillCreate(protocol);
		await review();
		await apply();
		await waitFor(() => expect(applies()).toHaveLength(1));
		await waitFor(() => expect(flashes.length).toBeGreaterThan(0));
	});

	it('refuses an invalid form without calling the server', async () => {
		handlers = ok();
		render(ServicesPage, { navigate: () => {}, mode: 'create', ...(c.tab ? { tab: c.tab } : {}) });
		await waitFor(() => expect(document.getElementById('service-name')).toBeTruthy());
		await fireEvent.input(document.getElementById('service-name')!, { target: { value: 'Bad Name' } });
		await fireEvent.click(screen.getByRole('button', { name: /Continue|Review change/ }));
		await waitFor(() => expect(document.getElementById('service-name')!.getAttribute('aria-invalid')).toBe('true'));
		expect(writes()).toHaveLength(0);
	});
});

describe('creating a service that cannot be saved', () => {
	it('refuses a TCP address without a port and a port out of range', async () => {
		handlers = list([]);
		await fillCreate('tcp');
		await fireEvent.input(document.getElementById('service-url-0-port')!, { target: { value: '' } });
		await review();
		await waitFor(() => expect(screen.getAllByText(/host and a port/).length).toBeGreaterThan(0));
		await fireEvent.input(document.getElementById('service-url-0-port')!, { target: { value: '70000' } });
		await review();
		await waitFor(() =>
			expect(document.getElementById('service-url-0-port')!.getAttribute('aria-invalid')).toBe('true'),
		);
		expect(writes()).toHaveLength(0);
	});
	it('refuses a name that another protocol already uses', async () => {
		handlers = list([svc('udp', 'shared')]);
		render(ServicesPage, { navigate: () => {}, mode: 'create', tab: 'tcp' });
		await waitFor(() => expect(document.getElementById('service-name')).toBeTruthy());
		await fireEvent.input(document.getElementById('service-name')!, { target: { value: 'shared' } });
		await fireEvent.click(screen.getByRole('button', { name: /Continue|Review change/ }));
		await waitFor(() => expect(screen.getAllByText(/already exists/).length).toBeGreaterThan(0));
		expect(writes()).toHaveLength(0);
	});
});

/* ---------------------------------------------------------------- edit */

describe.each(['http', 'tcp', 'udp'] as Protocol[])('editing a %s service', (protocol) => {
	const name = 'edited';
	const path = `/api/v1/services/${name}`;
	const stored = () => svc(protocol, name, {}, 4);
	const ok = () => ({
		...list([stored()]),
		[`PUT ${path}?dry_run=true`]: () => json({ preview: preview(name), restart_required: false }),
		[`PUT ${path}`]: () =>
			json({ operation: operation(name), resource: svc(protocol, name, {}, 5), restart_required: false }),
	});
	const open = async (navigate: (to: string) => void = () => {}) => {
		render(ServicesPage, { navigate, mode: 'edit', name, ...(protocol === 'http' ? {} : { tab: protocol }) });
		return waitFor(() => {
			const el = document.getElementById('service-url-0') as HTMLInputElement | null;
			expect(el).toBeTruthy();
			return el!;
		});
	};
	const change = async () => {
		const host = await open();
		await fireEvent.input(host, { target: { value: 'changed.internal' } });
	};
	const toReview = review;

	it('loads the stored service and keeps its name fixed', async () => {
		handlers = ok();
		const host = await open();
		expect(host.value).toBe(protocol === 'http' ? 'app' : protocol === 'tcp' ? 'db' : 'dns');
		const nameInput = document.getElementById('service-name') as HTMLInputElement | null;
		if (nameInput) expect(nameInput.disabled || nameInput.readOnly).toBe(true);
	});

	it('applies with the version the list carried', async () => {
		handlers = ok();
		const navigate = vi.fn();
		const host = await open(navigate);
		await fireEvent.input(host, { target: { value: 'changed.internal' } });
		await toReview();
		await apply();
		await waitFor(() => expect(navigate).toHaveBeenCalledWith('/services'));
		expect(writes()[0].headers['if-match']).toBe('"4"');
		expect(writes()[0].headers['if-none-match']).toBeUndefined();
		expect(applies()).toHaveLength(1);
		expect(applies()[0].headers['if-match']).toBe('"4"');
		expect(applies()[0].body.protocol).toBe(protocol);
		expect(applies()[0].body.used_by).toBeUndefined();
		expect(JSON.stringify(applies()[0].body)).toContain('changed.internal');
	});

	it('refreshes and asks again when someone else changed it first (409)', async () => {
		handlers = {
			...ok(),
			[`PUT ${path}`]: () => failure(409, 'ERR_VERSION_CONFLICT', 'the service was changed by someone else'),
		};
		await change();
		await toReview();
		await apply();
		await waitFor(() =>
			expect(screen.getAllByText(/service changed since the form was opened/).length).toBeGreaterThan(0),
		);
		expect(screen.queryByRole('button', { name: 'Apply changes' })).toBeNull();
	});

	it('reports a service deleted meanwhile (404)', async () => {
		handlers = { ...ok(), [`PUT ${path}`]: () => failure(404, 'ERR_NOT_FOUND', 'service not found') };
		await change();
		await toReview();
		await apply();
		await waitFor(() => expect(screen.getAllByText(/not found/).length).toBeGreaterThan(0));
	});

	it('shows a refused change (422) and writes nothing', async () => {
		handlers = {
			...ok(),
			[`PUT ${path}?dry_run=true`]: () => failure(422, 'ERR_VALIDATION_FAILED', 'the change is not valid'),
		};
		await change();
		await toReview();
		await waitFor(() => expect(screen.getAllByText(/not valid/).length).toBeGreaterThan(0));
		expect(applies()).toHaveLength(0);
	});

	it('still reviews an edit that changes nothing', async () => {
		handlers = ok();
		await open();
		await toReview();
		await waitFor(() => expect(writes()).toHaveLength(1));
		expect(writes()[0].path).toContain('dry_run=true');
	});
});

describe('editing a service that is gone or not editable', () => {
	it('does not write when the service is not in the list', async () => {
		handlers = list([svc('tcp', 'other')]);
		render(ServicesPage, { navigate: () => {}, mode: 'edit', name: 'missing', tab: 'tcp' });
		await waitFor(() => expect(calls.length).toBeGreaterThan(0));
		expect(writes()).toHaveLength(0);
	});
	it('does not offer to change an external service', async () => {
		handlers = list([svc('tcp', 'ext', {}, 1, { origin: 'external', read_only: true, provider: 'docker' })]);
		render(ServicesPage, { navigate: () => {}, mode: 'edit', name: 'ext', tab: 'tcp' });
		await waitFor(() => expect(calls.length).toBeGreaterThan(0));
		expect(screen.queryByRole('button', { name: 'Review change' })).toBeNull();
		expect(writes()).toHaveLength(0);
	});
});

/* ---------------------------------------------------------------- delete */

async function askToDelete(name: string) {
	await waitFor(() => expect(screen.getAllByText(name)[0]).toBeTruthy());
	await fireEvent.click(within(rowOf(name)).getByRole('menuitem', { name: 'Delete' }));
}
const confirm = async () => fireEvent.click(await screen.findByRole('button', { name: 'Confirm deletion' }));

describe.each(['http', 'tcp', 'udp'] as Protocol[])('deleting a %s service', (protocol) => {
	const name = 'gone';
	const path = `/api/v1/services/${name}`;
	let rows: unknown[] = [];
	const deleted = () => ({ operation: { ...operation(name), kind: 'service.delete' }, restart_required: false });
	beforeEach(() => {
		rows = [svc(protocol, name, {}, 7), svc('http', 'keep', {}, 2)];
	});
	const serve = (answer: () => Response | Promise<Response>) => {
		handlers = { 'GET /api/v1/services': () => json(rows), [`DELETE ${path}`]: answer };
	};

	it('deletes with the version from the list, then reloads', async () => {
		serve(() => {
			rows = rows.filter((r: any) => r.name !== name);
			return json(deleted());
		});
		render(ServicesPage, { navigate: () => {} });
		await askToDelete(name);
		await confirm();
		// The list is empty while it reloads: wait for the second read and the surviving row.
		await waitFor(() => {
			expect(calls.filter((c) => c.path === '/api/v1/services')).toHaveLength(2);
			expect(screen.getAllByText('keep').length).toBeGreaterThan(0);
		});
		expect(screen.queryByText(name)).toBeNull();
		expect(writes('DELETE')).toHaveLength(1);
		expect(writes('DELETE')[0].headers['if-match']).toBe('"7"');
	});

	it('asks first, and cancelling deletes nothing', async () => {
		serve(() => json(deleted()));
		render(ServicesPage, { navigate: () => {} });
		await askToDelete(name);
		expect(await screen.findByText(/Delete gone\?/)).toBeTruthy();
		await fireEvent.click(screen.getAllByRole('button', { name: 'Cancel' }).at(-1)!);
		await waitFor(() => expect(screen.queryByRole('button', { name: 'Confirm deletion' })).toBeNull());
		expect(writes('DELETE')).toHaveLength(0);
		expect(screen.getAllByText(name).length).toBeGreaterThan(0);
	});

	it('says a router still uses it (409) and keeps the row', async () => {
		serve(() => failure(409, 'ERR_CONFLICT', 'service gone is used by router shop'));
		render(ServicesPage, { navigate: () => {} });
		await askToDelete(name);
		await confirm();
		await waitFor(() => expect(screen.getByText(/used by router shop/)).toBeTruthy());
		expect(screen.getAllByText(name).length).toBeGreaterThan(0);
	});

	it('reports a version conflict (409) with the server message', async () => {
		serve(() => failure(409, 'ERR_VERSION_CONFLICT', 'the service was changed since you opened the list'));
		render(ServicesPage, { navigate: () => {} });
		await askToDelete(name);
		await confirm();
		await waitFor(() => expect(screen.getByText(/changed since you opened the list/)).toBeTruthy());
	});

	it('reports a service that is already gone (404)', async () => {
		serve(() => failure(404, 'ERR_NOT_FOUND', 'service not found'));
		render(ServicesPage, { navigate: () => {} });
		await askToDelete(name);
		await confirm();
		await waitFor(() => expect(screen.getAllByText(/not found|could not|failed/i).length).toBeGreaterThan(0));
		expect(writes('DELETE')).toHaveLength(1);
	});

	it('reports a Traefik that cannot be reached to check the routers (502)', async () => {
		serve(() =>
			failure(
				502,
				'ERR_TRAEFIK_UNAVAILABLE',
				'Traefik runtime observation is unavailable. Retry when the API is reachable.',
			),
		);
		render(ServicesPage, { navigate: () => {} });
		await askToDelete(name);
		await confirm();
		await waitFor(() => expect(screen.getByText(/Traefik runtime observation is unavailable/)).toBeTruthy());
		expect(screen.getAllByText(name).length).toBeGreaterThan(0);
	});

	it('reports a rolled back deletion (422) and keeps the row', async () => {
		serve(() => failure(422, 'ERR_DEPLOY_ROLLED_BACK', 'Traefik rejected the change; it was rolled back'));
		render(ServicesPage, { navigate: () => {} });
		await askToDelete(name);
		await confirm();
		await waitFor(() => expect(screen.getByText(/rolled back/)).toBeTruthy());
		expect(screen.getAllByText(name).length).toBeGreaterThan(0);
	});

	it('reports an unexpected error (500) and a lost connection', async () => {
		serve(() => failure(500, 'ERR_INTERNAL', 'Something went wrong.'));
		render(ServicesPage, { navigate: () => {} });
		await askToDelete(name);
		await confirm();
		await waitFor(() =>
			expect(screen.getAllByText(/Something went wrong|The deletion failed/).length).toBeGreaterThan(0),
		);
		cleanup();
		calls = [];
		serve(() => {
			throw new TypeError('Failed to fetch');
		});
		render(ServicesPage, { navigate: () => {} });
		await askToDelete(name);
		await confirm();
		await waitFor(() => expect(writes('DELETE')).toHaveLength(1));
		expect(screen.getAllByText(name).length).toBeGreaterThan(0);
	});

	it('warns that Traefik must be restarted when the change is saved but not live (202)', async () => {
		serve(() => {
			rows = rows.filter((r: any) => r.name !== name);
			return json({ ...deleted(), restart_required: true, error: { code: 'ERR_MANUAL_RESTART_REQUIRED' } }, 202);
		});
		render(ServicesPage, { navigate: () => {} });
		await askToDelete(name);
		await confirm();
		await waitFor(() => expect(screen.getByText(/Restart Traefik yourself/)).toBeTruthy());
	});

	it('cannot be deleted while a router uses it', async () => {
		rows = [svc(protocol, name, { used_by: used('shop') }, 7)];
		serve(() => json(deleted()));
		render(ServicesPage, { navigate: () => {} });
		await waitFor(() => expect(screen.getAllByText(name)[0]).toBeTruthy());
		const item = within(rowOf(name)).getByRole('menuitem', { name: 'Delete' }) as HTMLButtonElement;
		expect(item.disabled).toBe(true);
		await fireEvent.click(item);
		expect(screen.queryByRole('button', { name: 'Confirm deletion' })).toBeNull();
		expect(writes('DELETE')).toHaveLength(0);
	});

	it('cannot be deleted when it comes from another provider', async () => {
		rows = [svc(protocol, name, {}, 1, { origin: 'external', read_only: true, provider: 'docker' })];
		serve(() => json(deleted()));
		render(ServicesPage, { navigate: () => {} });
		await waitFor(() => expect(screen.getAllByText(name)[0]).toBeTruthy());
		expect(within(rowOf(name)).queryByRole('menuitem', { name: 'Delete' })).toBeNull();
	});
});

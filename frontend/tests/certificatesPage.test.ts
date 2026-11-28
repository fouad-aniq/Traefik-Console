import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/svelte';
// The API client binds `fetch` at import time, so install a forwarding stub first.
vi.hoisted(() => {
	(globalThis as any).__mockFetch = async () => {
		throw new Error('no mock');
	};
	globalThis.fetch = ((...args: unknown[]) => (globalThis as any).__mockFetch(...args)) as typeof fetch;
});
import CertificatesPage from '../src/features/certificates/CertificatesPage.svelte';
import Checkbox from '../src/shared/ui/Checkbox.svelte';
import { flashes } from '../src/shared/ui/flash.svelte';

const CERT = '-----BEGIN CERTIFICATE-----\nAAA\n-----END CERTIFICATE-----';
const KEY = '-----BEGIN PRIVATE KEY-----\nBBB\n-----END PRIVATE KEY-----';
const preview = (extra = {}) => ({
	changes: [{ action: 'created', id: 'tls:shop', kind: 'tls', name: 'shop', scope: 'dynamic' }],
	static_diff: '',
	dynamic_diff: '+tls: {}',
	requires_restart: false,
	...extra,
});
const operation = (extra = {}) => ({
	id: 9,
	kind: 'tls.create',
	target: 'shop',
	status: 'succeeded',
	summary: 'Applied 1 change',
	requires_restart: false,
	created_at: 'now',
	...extra,
});
const cert = (name: string, config = {}, version = 1) => ({
	id: 'tls:certificate:' + name,
	name,
	version,
	status: 'active',
	origin: 'managed',
	provider: 'file',
	read_only: false,
	live_health: 'unknown',
	config: {
		name,
		source: 'imported',
		cert_file: '/etc/traefik/secrets/c.secret',
		key_file: '',
		fingerprint: 'ff',
		domains: ['shop.test'],
		issuer: 'CN=Test CA',
		not_before: '2026-01-01T00:00:00Z',
		not_after: '2099-01-01T00:00:00Z',
		algorithm: 'RSA',
		status: 'valid',
		purpose: 'server',
		...config,
	},
});
const acme = (main: string) => ({
	resolver_name: 'le',
	main_domain: main,
	sans: null,
	issuer: 'R3',
	serial_number: '01',
	fingerprint: 'fp-' + main,
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

const base = (files: unknown, inventory: unknown = { certificates: [], observations: [], partial: false }) => ({
	'GET /api/v1/certificates': () => json(files),
	'GET /api/v1/acme/certificates': () => json(inventory),
});

describe('CertificatesPage states', () => {
	it('lists uploaded certificates', async () => {
		handlers = base([cert('shop'), cert('old', { status: 'expired' })]);
		render(CertificatesPage, { navigate: () => {}, tab: 'uploaded' });
		await waitFor(() => expect(screen.getAllByText('shop')[0]).toBeTruthy());
		expect(screen.getAllByText('old')[0]).toBeTruthy();
	});
	it('shows the empty state for a null list', async () => {
		handlers = base(null);
		render(CertificatesPage, { navigate: () => {}, tab: 'uploaded' });
		await waitFor(() => expect(screen.getByText('No uploaded certificates yet')).toBeTruthy());
	});
	it('shows an error with retry and recovers', async () => {
		handlers = {
			...base([]),
			'GET /api/v1/certificates': () =>
				json({ code: 'ERR_INTERNAL', message: 'db is locked', operation_id: 'o', retryable: true }, 500),
		};
		render(CertificatesPage, { navigate: () => {}, tab: 'uploaded' });
		await waitFor(() => expect(screen.getAllByText('db is locked').length).toBeGreaterThan(0));
		handlers['GET /api/v1/certificates'] = () => json([cert('back')]);
		await fireEvent.click(screen.getAllByRole('button', { name: /retry|try again/i })[0]);
		await waitFor(() => expect(screen.getAllByText('back')[0]).toBeTruthy());
	});
	it('lists automatic certificates and explains partial ACME observation', async () => {
		handlers = base([], {
			certificates: [acme('a.test')],
			observations: [{ resolver: 'le', state: 'not_mapped', invalid_entries: 0 }],
			partial: true,
		});
		render(CertificatesPage, { navigate: () => {}, tab: 'automatic' });
		await waitFor(() => expect(screen.getAllByText('a.test')[0]).toBeTruthy());
		expect(screen.getByText(/outside \/etc\/traefik\/acme/)).toBeTruthy();
	});
	it('does not warn about empty ACME storage of resolvers nothing uses, but still warns about unreadable files', async () => {
		handlers = base([], {
			certificates: [],
			observations: [{ resolver: 'le', state: 'uninitialized', invalid_entries: 0, in_use: false }],
			partial: true,
		});
		render(CertificatesPage, { navigate: () => {}, tab: 'automatic' });
		await waitFor(() => expect(screen.getByText('No automatic certificates observed')).toBeTruthy());
		expect(screen.queryByText(/has not written anything/)).toBeNull();
		expect(screen.queryByText(/incomplete/)).toBeNull();
		cleanup();
		handlers = base([], {
			certificates: [],
			observations: [{ resolver: 'le', state: 'unreadable', invalid_entries: 0 }],
			partial: true,
		});
		render(CertificatesPage, { navigate: () => {}, tab: 'automatic' });
		await waitFor(() => expect(screen.getByText(/not allowed to read the ACME storage/)).toBeTruthy());
	});
	it('keeps the warning when core-v2 reports the resolver in use', async () => {
		handlers = base([], {
			certificates: [],
			observations: [{ resolver: 'le', state: 'uninitialized', invalid_entries: 0, in_use: true }],
			partial: true,
		});
		render(CertificatesPage, { navigate: () => {}, tab: 'automatic' });
		await waitFor(() => expect(screen.getByText(/has not written anything/)).toBeTruthy());
	});
});

const usedBy = {
	domains: [{ name: 'domain-aniqhomelab-ovh', main: 'aniqhomelab.ovh', source: 'claudflare', read_only: false }],
	routers: [
		{
			name: 'portainer',
			rule: 'Host(`portainer.aniqhomelab.ovh`)',
			entry_points: ['websecure'],
			service: 'portainer',
			tls: true,
			read_only: false,
		},
	],
};
const none = { domains: [], routers: [] };

describe('certificate usage comes with the certificates', () => {
	it('loads the page with the two certificate endpoints only', async () => {
		handlers = base([cert('shop', { used_by: none })], {
			certificates: [{ ...acme('portainer.aniqhomelab.ovh'), used_by: usedBy }],
			observations: [],
			partial: false,
		});
		render(CertificatesPage, { navigate: () => {}, tab: 'automatic' });
		await waitFor(() => expect(screen.getAllByText('portainer.aniqhomelab.ovh')[0]).toBeTruthy());
		expect(calls.map((c) => c.path).sort()).toEqual(['/api/v1/acme/certificates', '/api/v1/certificates']);
	});

	it('gives the uploaded table one header per cell, including Used by', async () => {
		handlers = base([cert('shop', { used_by: none })]);
		const { container } = render(CertificatesPage, { navigate: () => {}, tab: 'uploaded' });
		await waitFor(() => expect(screen.getAllByText('shop')[0]).toBeTruthy());
		const headers = container.querySelectorAll('thead th');
		const cells = container.querySelector('tbody tr')!.querySelectorAll('td');
		expect(headers.length).toBe(cells.length);
		expect([...headers].map((h) => h.textContent?.trim())).toContain('Used by');
	});

	it('opens the used-by dialog from the count, with names that link to the views', async () => {
		const navigate = vi.fn();
		handlers = base([cert('shop', { used_by: usedBy })]);
		render(CertificatesPage, { navigate, tab: 'uploaded' });
		await fireEvent.click(await screen.findByRole('button', { name: '2 resources' }));
		const dialog = await screen.findByRole('dialog');
		expect(dialog.textContent).toContain('aniqhomelab.ovh');
		expect(dialog.querySelector('.count')).toBeNull();
		expect([...dialog.querySelectorAll('button')].map((b) => b.textContent?.trim())).not.toContain('View');
		await fireEvent.click(screen.getByRole('button', { name: 'portainer' }));
		expect(navigate).toHaveBeenCalledWith('/routers/portainer');
	});

	it('gives automatic certificates the same Used by column and dialog', async () => {
		const navigate = vi.fn();
		handlers = base([], {
			certificates: [
				{ ...acme('portainer.aniqhomelab.ovh'), used_by: usedBy },
				{ ...acme('lonely.example'), used_by: none },
			],
			observations: [],
			partial: false,
		});
		const { container } = render(CertificatesPage, { navigate, tab: 'automatic' });
		await waitFor(() => expect(screen.getAllByText('portainer.aniqhomelab.ovh')[0]).toBeTruthy());
		const headers = [...container.querySelectorAll('thead th')].map((h) => h.textContent?.trim());
		expect(headers).toContain('Used by');
		expect(container.querySelector('tbody tr')!.querySelectorAll('td').length).toBe(headers.length);
		expect(screen.getByText('Not used')).toBeTruthy();
		await fireEvent.click(screen.getByRole('button', { name: '2 resources' }));
		const dialog = await screen.findByRole('dialog');
		expect(dialog.textContent).toContain('Host(`portainer.aniqhomelab.ovh`)');
		await fireEvent.click(screen.getByRole('button', { name: 'domain-aniqhomelab-ovh' }));
		expect(navigate).toHaveBeenCalledWith('/domains/domain-aniqhomelab-ovh');
	});

	it('shows "Not used" for an unused certificate and "Unavailable" with a warning when usage is unknown', async () => {
		handlers = base([cert('spare', { used_by: none }), cert('mystery')]);
		const { container } = render(CertificatesPage, { navigate: () => {}, tab: 'uploaded' });
		await waitFor(() => expect(screen.getAllByText('spare')[0]).toBeTruthy());
		expect(container.querySelector('.not-used')?.textContent).toBe('Not used');
		expect(container.textContent).not.toMatch(/\b0\b(?! days)/);
		expect(screen.getByText('Unavailable')).toBeTruthy();
		expect(screen.getByText(/Could not read what uses these certificates/)).toBeTruthy();
	});

	it('warns on the automatic tab too when usage is unknown', async () => {
		handlers = base([], { certificates: [acme('unknown.example')], observations: [], partial: false });
		render(CertificatesPage, { navigate: () => {}, tab: 'automatic' });
		expect(await screen.findByText(/Could not read what uses these certificates/)).toBeTruthy();
	});

	it('warns before deleting a certificate that domains use', async () => {
		handlers = base([cert('shop', { used_by: usedBy })]);
		render(CertificatesPage, { navigate: () => {}, tab: 'uploaded' });
		await waitFor(() => expect(screen.getAllByText('shop')[0]).toBeTruthy());
		await fireEvent.click(screen.getAllByRole('menuitem', { name: 'Delete' })[0]);
		expect(await screen.findByText(/is used by 1 domain/)).toBeTruthy();
	});

	it('keeps the certificate view to its summary (usage lives in the table)', async () => {
		const a = { ...acme('portainer.aniqhomelab.ovh'), used_by: usedBy };
		handlers = base([], { certificates: [a], observations: [], partial: false });
		render(CertificatesPage, { navigate: () => {}, tab: 'automatic', mode: 'view', name: a.fingerprint });
		await waitFor(() => expect(screen.getAllByText('Covered domain names').length).toBeGreaterThan(0));
		expect(screen.queryByRole('tab', { name: /Used by/ })).toBeNull();
		expect(document.getElementById('panel-cert-domains')).toBeNull();
	});
});

describe('upload flow', () => {
	it('validates locally, previews with a dry run, then applies with a create precondition and one key', async () => {
		handlers = {
			...base([]),
			'PUT /api/v1/certificates/shop?dry_run=true': () => json({ preview: preview(), restart_required: false }),
			'PUT /api/v1/certificates/shop': () =>
				json({ operation: operation(), resource: cert('shop'), restart_required: false }),
		};
		render(CertificatesPage, { navigate: () => {}, tab: 'uploaded', mode: 'create' });
		await waitFor(() => expect(document.getElementById('upload-name')).toBeTruthy());

		// Nothing is sent while required fields are empty.
		await fireEvent.click(screen.getByRole('button', { name: 'Review change' }));
		await waitFor(() => expect(document.getElementById('upload-name')!.getAttribute('aria-invalid')).toBe('true'));
		expect(calls.some((c) => c.method === 'PUT')).toBe(false);

		await fireEvent.input(document.getElementById('upload-name')!, { target: { value: 'shop' } });
		await fireEvent.input(document.getElementById('upload-chain-pem')!, { target: { value: CERT } });
		await fireEvent.input(document.getElementById('upload-key-pem')!, { target: { value: KEY } });
		await fireEvent.click(screen.getByRole('button', { name: 'Review change' }));
		await screen.findByRole('button', { name: 'Apply changes' });

		const dry = calls.find((c) => c.method === 'PUT')!;
		expect(dry.path).toBe('/api/v1/certificates/shop?dry_run=true');
		expect(dry.headers['if-none-match']).toBe('*');
		expect(dry.body).toEqual({ purpose: 'server', cert_pem: CERT, key_pem: KEY });

		await fireEvent.click(await screen.findByRole('button', { name: 'Apply changes' }));
		await waitFor(() => expect(flashes.some((f) => f.title.startsWith('Applied'))).toBe(true));
		const apply = calls.filter((c) => c.method === 'PUT')[1];
		expect(apply.path).toBe('/api/v1/certificates/shop');
		expect(apply.headers['idempotency-key']).toMatch(/^[A-Za-z0-9_-]{16,128}$/);
		expect(calls.some((c) => c.path.includes('/draft'))).toBe(false);
	});
	it('replaces a certificate with If-Match set to its version', async () => {
		handlers = {
			...base([cert('shop', {}, 3)]),
			'GET /api/v1/certificates/shop': () => json(cert('shop', {}, 3)),
			'PUT /api/v1/certificates/shop?dry_run=true': () => json({ preview: preview(), restart_required: false }),
		};
		render(CertificatesPage, { navigate: () => {}, tab: 'uploaded', mode: 'replace', name: 'shop' });
		await waitFor(() => expect(document.getElementById('upload-chain-pem')).toBeTruthy());
		await fireEvent.input(document.getElementById('upload-chain-pem')!, { target: { value: CERT } });
		await fireEvent.input(document.getElementById('upload-key-pem')!, { target: { value: KEY } });
		await fireEvent.click(screen.getByRole('button', { name: 'Review change' }));
		await waitFor(() => expect(calls.some((c) => c.method === 'PUT')).toBe(true));
		expect(calls.find((c) => c.method === 'PUT')!.headers['if-match']).toBe('"3"');
	});
	it('shows the server PEM error on the file field and keeps the form', async () => {
		handlers = {
			...base([]),
			'PUT /api/v1/certificates/shop?dry_run=true': () =>
				json(
					{
						code: 'ERR_VALIDATION',
						message: 'invalid PEM certificate chain format',
						operation_id: 'o',
						retryable: false,
					},
					422,
				),
		};
		render(CertificatesPage, { navigate: () => {}, tab: 'uploaded', mode: 'create' });
		await waitFor(() => expect(document.getElementById('upload-name')).toBeTruthy());
		await fireEvent.input(document.getElementById('upload-name')!, { target: { value: 'shop' } });
		await fireEvent.input(document.getElementById('upload-chain-pem')!, { target: { value: CERT } });
		await fireEvent.input(document.getElementById('upload-key-pem')!, { target: { value: KEY } });
		await fireEvent.click(screen.getByRole('button', { name: 'Review change' }));
		await waitFor(() => expect(document.getElementById('upload-chain')!.getAttribute('aria-invalid')).toBe('true'));
		expect((document.getElementById('upload-name') as HTMLInputElement).value).toBe('shop');
	});
	it('keeps the dialog open when the file picker is cancelled', async () => {
		handlers = base([]);
		const navigate = vi.fn();
		render(CertificatesPage, { navigate, tab: 'uploaded', mode: 'create' });
		const picker = await waitFor(() => {
			const el = document.getElementById('upload-chain');
			if (!el) throw new Error('no picker');
			return el;
		});
		// Closing the native file chooser fires a bubbling "cancel" on the input.
		picker.dispatchEvent(new Event('cancel', { bubbles: true, cancelable: true }));
		expect(navigate).not.toHaveBeenCalled();
		// Escape on the dialog itself still closes it.
		document.querySelector('.resource-dialog')!.dispatchEvent(new Event('cancel', { cancelable: true }));
		await waitFor(() => expect(navigate).toHaveBeenCalledWith('/certificates/uploaded'));
	});
	it('omits the key for a CA trust file', async () => {
		handlers = {
			...base([]),
			'PUT /api/v1/certificates/ca1?dry_run=true': () => json({ preview: preview(), restart_required: false }),
		};
		render(CertificatesPage, { navigate: () => {}, tab: 'uploaded', mode: 'create' });
		await waitFor(() => expect(document.getElementById('upload-name')).toBeTruthy());
		await fireEvent.input(document.getElementById('upload-name')!, { target: { value: 'ca1' } });
		await fireEvent.input(document.getElementById('upload-chain-pem')!, { target: { value: CERT } });
		await fireEvent.click(document.getElementById('upload-purpose')!);
		await fireEvent.click(await screen.findByRole('option', { name: 'CA trust file' }));
		await waitFor(() => expect(document.getElementById('upload-key')).toBeNull());
		await fireEvent.click(screen.getByRole('button', { name: 'Review change' }));
		await waitFor(() => expect(calls.some((c) => c.method === 'PUT')).toBe(true));
		expect(calls.find((c) => c.method === 'PUT')!.body).toEqual({ purpose: 'ca', cert_pem: CERT });
	});
});

describe('delete', () => {
	it('deletes with the version read with the list', async () => {
		handlers = {
			...base([cert('shop', {}, 5)]),
			'DELETE /api/v1/certificates/shop': () => json({ operation: operation(), restart_required: false }),
		};
		render(CertificatesPage, { navigate: () => {}, tab: 'uploaded' });
		await waitFor(() => expect(screen.getAllByText('shop')[0]).toBeTruthy());
		await fireEvent.click(screen.getAllByRole('menuitem', { name: 'Delete' })[0]);
		await fireEvent.click(await screen.findByRole('button', { name: 'Confirm deletion' }));
		await waitFor(() => expect(calls.some((c) => c.method === 'DELETE')).toBe(true));
		expect(calls.find((c) => c.method === 'DELETE')!.headers['if-match']).toBe('"5"');
	});
});

describe('Checkbox', () => {
	it('toggles through the native input and reports the new value', async () => {
		const seen: boolean[] = [];
		render(Checkbox, {
			id: 'cb',
			checked: false,
			label: 'Allow',
			help: 'Explains it',
			onchange: (v: boolean) => seen.push(v),
		});
		const input = document.getElementById('cb') as HTMLInputElement;
		expect(input.getAttribute('aria-describedby')).toBe('cb-help');
		await fireEvent.click(screen.getByText('Allow'));
		expect(seen).toEqual([true]);
	});
	it('does not fire when disabled', async () => {
		const seen: boolean[] = [];
		render(Checkbox, { id: 'cb2', label: 'Nope', disabled: true, onchange: (v: boolean) => seen.push(v) });
		await fireEvent.click(screen.getByText('Nope'));
		expect(seen).toEqual([]);
	});
});

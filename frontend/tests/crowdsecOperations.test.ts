import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/svelte';
vi.hoisted(() => {
	(globalThis as any).__mockFetch = async () => { throw new Error('no mock'); };
	globalThis.fetch = ((...args: unknown[]) => (globalThis as any).__mockFetch(...args)) as typeof fetch;
});
import AlertsPanel from '../src/features/crowdsec/AlertsPanel.svelte';
import AllowlistsPanel from '../src/features/crowdsec/AllowlistsPanel.svelte';
import { listHubItems, getMetrics } from '../src/features/crowdsec/api';

const json = (body: unknown, code = 200) => new Response(JSON.stringify(body), { status: code, headers: { 'Content-Type': 'application/json' } });
interface Call { method: string; path: string; body?: any }
let calls: Call[] = [];
let handlers: Record<string, () => Response> = {};

beforeEach(() => {
	calls = [];
	HTMLDialogElement.prototype.showModal ??= function () { this.setAttribute('open', ''); };
	HTMLDialogElement.prototype.close ??= function () { this.removeAttribute('open'); };
	(globalThis as any).__mockFetch = async (path: string, init: RequestInit = {}) => {
		calls.push({ method: init.method ?? 'GET', path, body: init.body ? JSON.parse(String(init.body)) : undefined });
		const key = `${init.method ?? 'GET'} ${path}`;
		if (handlers[key]) return handlers[key]();
		return json({ code: 'ERR_NOT_FOUND', message: 'not found' }, 404);
	};
});
afterEach(() => { cleanup(); handlers = {}; });

const alert = { id: 2, scope: 'Ip', value: '203.0.113.9', reason: 'crowdsecurity/ssh-bf', country: 'FR', as: 'Example', events_count: 4, created_at: '2026-11-10T11:00:00Z' };

describe('alert operations', () => {
	it('lists a bounded page and opens native alert details', async () => {
		handlers = {
			'GET /api/v1/crowdsec/alerts?page=0&limit=25&since_hours=24': () => json({ items: [alert], page: 0, limit: 25, has_more: true, observed_at: '2026-11-10T12:00:00Z' }),
			'GET /api/v1/crowdsec/alerts/2': () => json({ ...alert, decisions: [{ id: 9, type: 'ban', scope: 'Ip', value: alert.value, duration: '1h', origin: 'crowdsec', scenario: alert.reason }] })
		};
		render(AlertsPanel);
		expect((await screen.findAllByText('crowdsecurity/ssh-bf')).length).toBeGreaterThan(0);
		expect(screen.getByText(/France \(FR\)/)).toBeTruthy();
		await fireEvent.click(screen.getByRole('button', { name: 'Details' }));
		expect(await screen.findByText('Alert details')).toBeTruthy();
		expect((await screen.findAllByText(/Ip: 203.0.113.9/)).length).toBeGreaterThan(0);
		expect(await screen.findByText('Associated decisions')).toBeTruthy();
		await fireEvent.click((await screen.findAllByRole('button', { name: 'Close' })).at(-1)!);
		await fireEvent.click(screen.getByRole('button', { name: 'Next page' }));
		await waitFor(() => expect(calls.some((call) => call.path.includes('page=1&'))).toBe(true));
	});
});

describe('allowlist operations', () => {
	it('creates a list and removes an exact entry', async () => {
		const allowlist = { name: 'traefik-console-safe', description: 'Trusted networks', items: [{ value: '198.51.100.0/24', description: 'lab' }], console_managed: false };
		handlers = {
			'GET /api/v1/crowdsec/allowlists': () => json([allowlist]),
			'POST /api/v1/crowdsec/allowlists': () => json({ created: true }, 201),
			'DELETE /api/v1/crowdsec/allowlists/traefik-console-safe/entries': () => json({ removed: true })
		};
		render(AllowlistsPanel, { canWrite: true });
		await fireEvent.click(await screen.findByRole('button', { name: 'Create allowlist' }));
		await fireEvent.input(document.getElementById('allowlist-name')!, { target: { value: 'traefik-console-office' } });
		await fireEvent.input(document.getElementById('allowlist-description')!, { target: { value: 'Office network' } });
		await fireEvent.click(screen.getByRole('button', { name: 'Create', exact: true }));
		await waitFor(() => expect(calls.some((call) => call.method === 'POST')).toBe(true));
		expect(calls.find((call) => call.method === 'POST')!.body).toEqual({ name: 'traefik-console-office', description: 'Office network' });
		await waitFor(() => expect((screen.getByRole('button', { name: 'Create', exact: true }) as HTMLButtonElement).disabled).toBe(false));
		await fireEvent.click(screen.getByRole('button', { name: 'Remove entry' }));
		await screen.findByText('Remove this allowlist entry?');
		await fireEvent.click((await screen.findAllByRole('button', { name: 'Remove entry' })).at(-1)!);
		await waitFor(() => expect(calls.some((call) => call.method === 'DELETE')).toBe(true));
		expect(calls.find((call) => call.method === 'DELETE')!.body).toEqual({ value: '198.51.100.0/24' });
	});
});

describe('read-only hub and metrics clients', () => {
	it('reads installed items and preserves unmeasured counters', async () => {
		const items = [{ name: 'crowdsecurity/traefik', type: 'collections', status: 'installed' }];
		const metrics = { state: 'available', parsed_access_log_events: 42, bouncer_pulls: null };
		handlers = {
			'GET /api/v1/crowdsec/hub': () => json(items),
			'GET /api/v1/crowdsec/metrics': () => json(metrics)
		};
		expect(await listHubItems()).toEqual(items);
		expect(await getMetrics()).toEqual(metrics);
		expect(calls.map((call) => call.method)).toEqual(['GET', 'GET']);
	});
});

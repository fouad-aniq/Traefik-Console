import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/svelte';
// The API client binds `fetch` at import time, so install a forwarding stub first.
vi.hoisted(() => {
	(globalThis as any).__mockFetch = async () => { throw new Error('no mock'); };
	globalThis.fetch = ((...args: unknown[]) => (globalThis as any).__mockFetch(...args)) as typeof fetch;
});
import CrowdSecPage from '../src/features/crowdsec/CrowdSecPage.svelte';
import { barWidth, decisionPath, heatLevel, isPrivateAddress, looksLikeIpOrCidr, parseCidrs, type CrowdSecStatus } from '../src/features/crowdsec/models';
import { flashes } from '../src/shared/ui/flash.svelte';
import { navigationGuard } from '../src/lib/stores/navigationGuard';

const preview = (extra = {}) => ({ changes: [{ action: 'updated', id: 'crowdsec:config', kind: 'crowdsec', name: 'config', scope: 'dynamic' }], static_diff: '', dynamic_diff: '+crowdsec', requires_restart: false, ...extra });
const operation = (extra = {}) => ({ id: 7, kind: 'crowdsec.config', target: 'config', status: 'succeeded', summary: 'done', requires_restart: false, created_at: 'now', ...extra });
const saved = (enabled: boolean) => ({ operation: operation(), resource: { enabled, version: 2 }, restart_required: false });
const status = (over: Partial<CrowdSecStatus> = {}): CrowdSecStatus => ({
	engine: { state: 'running', name: 'crowdsec', image: 'crowdsecurity/crowdsec' },
	plugin: { state: 'missing' },
	middleware: { state: 'missing' },
	key: { present: false },
	lapi: { state: 'unchecked', url: 'http://crowdsec:8080' },
	bouncers: [],
	routers: [
		{ name: 'app', origin: 'managed', protected: false, pending: false },
		{ name: 'legacy', origin: 'external', protected: false, pending: false }
	],
	config: { enabled: false, mode: 'stream', lapi_url: 'http://crowdsec:8080', auto_enroll: false, plugin_version: 'v1.3.5' },
	complete: false,
	...over
});
const complete = () => status({
	plugin: { state: 'installed', alias: 'crowdsec-bouncer', module_name: 'github.com/maxlerebourg/crowdsec-bouncer-traefik-plugin', version: 'v1.8.1', origin: 'managed' },
	middleware: { state: 'installed', name: 'crowdsec-bouncer', plugin_alias: 'crowdsec-bouncer', origin: 'managed' },
	key: { present: true }, lapi: { state: 'ok', url: 'http://crowdsec:8080' },
	routers: [{ name: 'app', origin: 'managed', protected: true, pending: false }],
	config: { enabled: true, mode: 'stream', lapi_url: 'http://crowdsec:8080', auto_enroll: false, plugin_version: 'v1.8.1' },
	machine: { state: 'ok', id: 'machine' },
	ingestion: { state: 'verified' },
	remediation: { state: 'verified' },
	capabilities: { auto_enroll: true, alerts: true, decisions: true, decision_create: true, decision_delete: true, allowlist_read: true, allowlist_write: true, appsec: true },
	complete: true
});
const stats = (over = {}) => ({
	available: true, window_hours: 24, generated_at: '2026-11-10T12:00:00Z', alerts: 7, truncated: false, active_decisions: 3,
	decisions_by_type: [{ key: 'ban', count: 3 }], decisions_by_origin: [{ key: 'crowdsec', count: 3 }],
	top_countries: [{ key: 'FR', count: 4 }, { key: '??', count: 1 }], top_scenarios: [{ key: 'crowdsecurity/ssh-bf', count: 5 }], top_sources: [{ key: '203.0.113.9', count: 5 }],
	timeline: [{ start: '2026-11-10T10:00:00Z', count: 2 }, { start: '2026-11-10T11:00:00Z', count: 5 }], bucket_hours: 1,
	bouncers: [{ name: 'traefik-console-op-1', ip_address: '172.18.0.4', type: 'crowdsec-bouncer-traefik-plugin', version: 'v1.8.1', last_pull: '2026-11-10T11:59:00Z', revoked: false }],
	partial: [], ...over
});

interface Call { method: string; path: string; headers: Record<string, string>; body?: any }
let calls: Call[] = [];
let handlers: Record<string, () => Response | Promise<Response>> = {};
const json = (body: unknown, code = 200) => new Response(JSON.stringify(body), { status: code, headers: { 'Content-Type': 'application/json' } });
const catalog = (versions: string[]) => json({ items: versions.length ? [{ module_name: 'github.com/maxlerebourg/crowdsec-bouncer-traefik-plugin', display_name: 'CrowdSec', summary: '', versions, plugin_type: 'middleware', latest_version: versions[0], snippet_yaml: '', fetched_at: '', install_state: 'not_installed', can_update: false }] : [], total: versions.length ? 1 : 0, limit: 1, offset: 0, last_synced_at: '', inventory_known: true, refresh: { last_success_at: '', last_attempt_at: '', last_attempt_failed: false } });

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
afterEach(() => { cleanup(); handlers = {}; navigationGuard.setDirty(false); navigationGuard.cancelDiscard(); });

const enabledButton = async (name: string) => {
	const button = (await screen.findByRole('button', { name })) as HTMLButtonElement;
	await waitFor(() => expect(button.disabled).toBe(false));
	return button;
};

const base = (s: CrowdSecStatus, st: unknown = stats()) => ({
	'GET /api/v1/crowdsec/status': () => json(s),
	'GET /api/v1/crowdsec/stats?window=24h': () => json(st),
	'GET /api/v1/crowdsec/config': () => json({ enabled: false, version: 1 })
});

describe('models', () => {
	it('recognises private sources', () => {
		for (const v of ['10.1.2.3', '172.20.0.5', '192.168.80.2', '127.0.0.1', '169.254.1.1', '::1', 'fd00::1', 'fe80::1', '10.0.0.0/8']) expect(isPrivateAddress(v)).toBe(true);
		for (const v of ['203.0.113.9', '172.32.0.1', '8.8.8.8', '2001:db8::1', 'nope']) expect(isPrivateAddress(v)).toBe(false);
	});
	it('buckets map counts on a log scale', () => {
		expect(heatLevel(0, 60)).toBe(0);
		expect(heatLevel(1, 60)).toBe(1);
		expect(heatLevel(60, 60)).toBe(5);
		expect(heatLevel(1, 1)).toBe(5);
	});
	it('validates addresses and builds safe paths', () => {
		expect(looksLikeIpOrCidr('203.0.113.7')).toBe(true);
		expect(looksLikeIpOrCidr('198.51.100.0/24')).toBe(true);
		expect(looksLikeIpOrCidr('2001:db8::/32')).toBe(true);
		expect(looksLikeIpOrCidr('999.1.1.1')).toBe(false);
		expect(looksLikeIpOrCidr('10.0.0.0/40')).toBe(false);
		expect(looksLikeIpOrCidr('example.com')).toBe(false);
		expect(decisionPath('192.0.2.0/24')).toBe('/api/v1/crowdsec/decisions/192.0.2.0/24');
		expect(parseCidrs('10.0.0.0/8, 10.0.0.0/8\n172.18.0.0/16')).toEqual(['10.0.0.0/8', '172.18.0.0/16']);
		expect(barWidth(0, 10)).toBe(0);
		expect(barWidth(1, 1000)).toBe(2);
		expect(barWidth(5, 10)).toBe(50);
	});
});

const putOK = () => json({ operation: operation(), resource: { version: 2 }, restart_required: false });
const dryRun = () => json({ preview: preview(), restart_required: false });

describe('overview', () => {
	it('offers one Configure action when no LAPI address is known', async () => {
		handlers = base(status({ lapi: { state: 'unchecked', url: '' } }));
		const navigate = vi.fn();
		render(CrowdSecPage, { navigate });
		const buttons = await screen.findAllByRole('button', { name: /Configure/ });
		await fireEvent.click(buttons[0]);
		expect(navigate).toHaveBeenCalledWith('/crowdsec/settings');
		expect(screen.queryByRole('tab')).toBeNull();
		expect(calls.some((c) => c.path.startsWith('/api/v1/crowdsec/stats'))).toBe(false);
	});
	it('shows one status card, the map and only the list tabs once connected', async () => {
		handlers = base(complete());
		render(CrowdSecPage, { navigate: () => {} });
		for (const name of ['Overview', 'Decisions', 'Allowlists']) expect(await screen.findByRole('tab', { name })).toBeTruthy();
		expect(screen.queryByRole('tab', { name: 'Alerts' })).toBeNull();
		expect(screen.queryByRole('tab', { name: 'Integration' })).toBeNull();
		expect(await screen.findByText('Where attacks come from')).toBeTruthy();
		expect(screen.getByText('Protection on')).toBeTruthy();
		expect(screen.getAllByRole('button', { name: /Configure/ })).toHaveLength(1);
		expect(screen.queryByRole('button', { name: 'Disable protection' })).toBeNull();
	});
	it('shows the map even when no attack was detected', async () => {
		handlers = base(complete(), stats({ alerts: 0, top_countries: [], timeline: [] }));
		render(CrowdSecPage, { navigate: () => {} });
		expect(await screen.findByText('Where attacks come from')).toBeTruthy();
		expect(await screen.findByText('No attack detected in this period')).toBeTruthy();
	});
	it('reads statistics even while protection is off', async () => {
		handlers = base(status({ key: { present: true }, lapi: { state: 'ok', url: 'http://crowdsec:8080' }, capabilities: { auto_enroll: false, alerts: false, decisions: true, decision_create: false, decision_delete: false, allowlist_read: false, allowlist_write: false, appsec: false, metrics: false, hub_read: false } }));
		render(CrowdSecPage, { navigate: () => {} });
		expect(await screen.findByText('Protection off')).toBeTruthy();
		await waitFor(() => expect(calls.some((c) => c.path === '/api/v1/crowdsec/stats?window=24h')).toBe(true));
		expect(screen.getByRole('tab', { name: 'Decisions' })).toBeTruthy();
	});
	it('marks values that come from the environment', async () => {
		handlers = base({ ...complete(), environment: [{ name: 'CROWDSEC_LAPI_URL', field: 'lapi_url', secret: false, value: 'http://crowdsec:8080' }] });
		render(CrowdSecPage, { navigate: () => {} });
		expect((await screen.findAllByText('Set by an environment variable')).length).toBe(1);
	});
});

describe('settings dialog', () => {
	const env = [
		{ name: 'CROWDSEC_LAPI_URL', field: 'lapi_url', secret: false, value: 'http://crowdsec:8080' },
		{ name: 'CROWDSEC_METRICS_TOKEN', field: 'metrics_token', secret: true }
	];
	const catalogHandler = { 'GET /api/v1/plugins/catalog/page?module=github.com%2Fmaxlerebourg%2Fcrowdsec-bouncer-traefik-plugin&limit=1&offset=0&order=asc': () => catalog(['v1.9.0-rc.1', 'v1.8.2', 'v1.8.1']) };
	it('generates the key by default, locks environment values, then reviews and applies', async () => {
		handlers = {
			...base({ ...status(), environment: env, capabilities: { ...complete().capabilities!, auto_enroll: true } }),
			...catalogHandler,
			'PUT /api/v1/crowdsec/connection?dry_run=true': dryRun,
			'PUT /api/v1/crowdsec/connection': putOK
		};
		const navigate = vi.fn();
		render(CrowdSecPage, { navigate, mode: 'settings' });
		const url = (await screen.findByLabelText(/LAPI address/)) as HTMLInputElement;
		expect(url.disabled).toBe(true);
		expect(screen.getByText('Set by CROWDSEC_LAPI_URL')).toBeTruthy();
		const key = screen.getByLabelText('Bouncer key') as HTMLInputElement;
		expect(key.readOnly).toBe(true);
		expect(key.type).toBe('password');
		expect(key.value).toMatch(/^[0-9a-f]{64}$/);
		await fireEvent.click(await screen.findByRole('tab', { name: 'Monitoring' }));
		expect((screen.getByLabelText('Metrics token') as HTMLInputElement).disabled).toBe(true);
		await fireEvent.input(screen.getByLabelText('Metrics endpoint'), { target: { value: 'http://crowdsec:6060/metrics' } });
		await fireEvent.click(await enabledButton('Review change'));
		await waitFor(() => expect(calls.some((c) => c.path === '/api/v1/crowdsec/connection?dry_run=true')).toBe(true));
		const put = calls.find((c) => c.path === '/api/v1/crowdsec/connection?dry_run=true')!;
		expect(put.headers['if-match']).toBe('"1"');
		expect(put.body).toMatchObject({ generate_key: true, lapi_key: key.value, metrics_url: 'http://crowdsec:6060/metrics', protection: { mode: 'off' } });
		expect(put.body.metrics_token).toBeUndefined();
		expect(put.body.protection.routers).toBeUndefined();
		await fireEvent.click(await screen.findByRole('button', { name: 'Apply changes' }));
		await waitFor(() => expect(navigate).toHaveBeenCalledWith('/crowdsec'));
		expect(calls.filter((c) => c.method === 'PUT').at(-1)!.headers['idempotency-key']).toMatch(/^[A-Za-z0-9_-]{16,128}$/);
	});
	it('enables managed protection with the newest stable plugin', async () => {
		handlers = { ...base({ ...status(), capabilities: { ...complete().capabilities!, auto_enroll: true } }), ...catalogHandler, 'PUT /api/v1/crowdsec/connection?dry_run=true': dryRun };
		render(CrowdSecPage, { navigate: () => {}, mode: 'settings' });
		await fireEvent.click(await screen.findByRole('tab', { name: 'Protection' }));
		await fireEvent.click(screen.getByRole('combobox', { name: 'Traefik protection' }));
		await fireEvent.click(await screen.findByRole('option', { name: 'Managed by the Console' }));
		await waitFor(() => expect(screen.getByRole('combobox', { name: 'Bouncer plugin' }).textContent).toContain('v1.8.2'));
		await fireEvent.click(screen.getByRole('tab', { name: 'Review' }));
		await waitFor(() => expect(calls.some((c) => c.path === '/api/v1/crowdsec/connection?dry_run=true')).toBe(true));
		const put = calls.find((c) => c.path === '/api/v1/crowdsec/connection?dry_run=true')!;
		expect(put.body.protection).toMatchObject({ mode: 'managed', bouncer_mode: 'stream', plugin_version: 'v1.8.2', update_max_failure: 0 });
	});
	it('asks for the AppSec endpoint right under the AppSec option', async () => {
		handlers = { ...base({ ...complete(), runtime: { accessible: true, docker_configured: true } }), 'PUT /api/v1/crowdsec/connection?dry_run=true': dryRun };
		render(CrowdSecPage, { navigate: () => {}, mode: 'settings' });
		await fireEvent.click(await screen.findByRole('tab', { name: 'Protection' }));
		expect(screen.queryByLabelText('AppSec endpoint')).toBeNull();
		await fireEvent.click(await screen.findByRole('checkbox', { name: /Enable the AppSec WAF/ }));
		expect(screen.queryByText('Enter the AppSec endpoint.')).toBeNull();
		await fireEvent.click(screen.getByRole('tab', { name: 'Review' }));
		expect(await screen.findByText('Enter the AppSec endpoint.')).toBeTruthy();
		expect(calls.some((c) => c.method === 'PUT')).toBe(false);
		await fireEvent.input(screen.getByLabelText('AppSec endpoint'), { target: { value: 'http://crowdsec:7422' } });
		await fireEvent.click(screen.getByRole('tab', { name: 'Review' }));
		await waitFor(() => expect(calls.some((c) => c.path === '/api/v1/crowdsec/connection?dry_run=true')).toBe(true));
		expect(calls.find((c) => c.method === 'PUT')!.body).toMatchObject({ appsec_url: 'http://crowdsec:7422', protection: { appsec_enabled: true } });
	});
	it('shows the cscli command when the key cannot be generated, and blocks a bad address', async () => {
		handlers = base(status({ lapi: { state: 'unchecked', url: '' }, engine: { state: 'not_configured' } }));
		render(CrowdSecPage, { navigate: () => {}, mode: 'settings' });
		expect(await screen.findByText('docker exec crowdsec cscli bouncers add traefik-console')).toBeTruthy();
		expect(screen.getByLabelText('Bouncer key')).toBeTruthy();
		expect(screen.queryByRole('button', { name: /Create/ })).toBeNull();
		await fireEvent.input(screen.getByLabelText(/LAPI address/), { target: { value: 'http://crowdsec:8080/v1' } });
		await fireEvent.click(screen.getByRole('tab', { name: 'Review' }));
		expect(await screen.findByText('Enter an http:// or https:// address without a path, query or credentials.')).toBeTruthy();
		expect(calls.some((c) => c.method === 'PUT')).toBe(false);
	});
	it('steps aside for the unsaved-changes question and closes once discarded', async () => {
		handlers = base(status());
		const navigate = vi.fn();
		render(CrowdSecPage, { navigate, mode: 'settings' });
		await fireEvent.input(await screen.findByLabelText(/LAPI address/), { target: { value: 'http://other:8080' } });
		await fireEvent.click(screen.getByRole('button', { name: 'Cancel' }));
		await waitFor(() => expect(navigationGuard.confirmPending).toBe(true));
		await waitFor(() => expect(document.querySelector('dialog')?.hasAttribute('open')).toBe(false));
		navigationGuard.confirmDiscard();
		expect(navigate).toHaveBeenCalledWith('/crowdsec');
	});
	it('creates the API key and monitoring access through the accessible container', async () => {
		handlers = { ...base({ ...complete(), runtime: { accessible: true, docker_configured: true, container_id: 'a'.repeat(64) } }), 'PUT /api/v1/crowdsec/connection?dry_run=true': dryRun };
		render(CrowdSecPage, { navigate: () => {}, mode: 'settings' });
		await screen.findByRole('button', { name: 'Create a new API key' });
		await fireEvent.click(await screen.findByRole('button', { name: 'Create a new API key' }));
		await fireEvent.click(screen.getByRole('button', { name: 'Create new monitoring access' }));
		const password = screen.getByLabelText('Password') as HTMLInputElement;
		expect(password.readOnly).toBe(true);
		expect(password.value).toMatch(/^[0-9a-f]{64}$/);
		expect((screen.getByLabelText('Machine ID') as HTMLInputElement).placeholder).toBe('Assigned by the container when you apply');
		await fireEvent.click(screen.getByRole('tab', { name: 'Review' }));
		await waitFor(() => expect(calls.some((c) => c.path === '/api/v1/crowdsec/connection?dry_run=true')).toBe(true));
		const put = calls.find((c) => c.path === '/api/v1/crowdsec/connection?dry_run=true')!;
		expect(put.body).toMatchObject({ generate_key: true, generate_machine: true, machine_password: password.value });
		expect(put.body.lapi_key).toMatch(/^[0-9a-f]{64}$/);
		expect(put.body.machine_id).toBeUndefined();
	});
	it('undoes a planned key next to its field', async () => {
		handlers = base({ ...complete(), runtime: { accessible: true, docker_configured: true } });
		render(CrowdSecPage, { navigate: () => {}, mode: 'settings' });
		await fireEvent.click(await screen.findByRole('button', { name: 'Create a new API key' }));
		expect((screen.getByLabelText('Bouncer key') as HTMLInputElement).value).toMatch(/^[0-9a-f]{64}$/);
		await fireEvent.click(screen.getByRole('button', { name: 'Undo' }));
		const key = screen.getByLabelText('Bouncer key') as HTMLInputElement;
		expect(key.value).toBe('');
		expect(key.readOnly).toBe(false);
		expect(key.placeholder).toBe('••••••••');
		expect(screen.getByRole('button', { name: 'Create a new API key' })).toBeTruthy();
	});
	it('keeps the machine group open when a created password is undone', async () => {
		handlers = base({ ...complete(), runtime: { accessible: true, docker_configured: true } });
		render(CrowdSecPage, { navigate: () => {}, mode: 'settings' });
		const group = (await screen.findByText('Machine credentials (optional)')).closest('details')!;
		group.open = true;
		await fireEvent(group, new Event('toggle'));
		await fireEvent.click(screen.getByRole('button', { name: 'Create new monitoring access' }));
		expect(group.open).toBe(true);
		await fireEvent.click(screen.getByRole('button', { name: 'Undo' }));
		expect(group.open).toBe(true);
	});
	it('groups private sources apart from real countries', async () => {
		handlers = base(complete(), stats({ top_countries: [{ key: 'FR', count: 3 }, { key: 'LOCAL', count: 9 }] }));
		render(CrowdSecPage, { navigate: () => {} });
		expect(await screen.findByText('9 from private networks')).toBeTruthy();
		expect(screen.getByText('Private network')).toBeTruthy();
		expect(screen.queryByText(/CrowdSec records attacks it detects/)).toBeNull();
	});
	it('tests candidate settings without saving them', async () => {
		handlers = {
			...base(status({ lapi: { state: 'unchecked', url: '' } })),
			'POST /api/v1/crowdsec/connection/test': () => json({ lapi_url: 'http://crowdsec:8080', lapi: 'invalid_key', machine: 'not_configured', appsec: 'not_configured', metrics: 'not_configured', engine: 'running', can_generate_key: true, observed_at: 'now' })
		};
		render(CrowdSecPage, { navigate: () => {}, mode: 'settings' });
		await fireEvent.input(await screen.findByLabelText(/LAPI address/), { target: { value: 'http://crowdsec:8080' } });
		await fireEvent.input(screen.getByLabelText('Bouncer key'), { target: { value: 'secret' } });
		await fireEvent.click(await enabledButton('Test connection'));
		expect(await screen.findByText('Rejected')).toBeTruthy();
		expect(calls.find((c) => c.method === 'POST')!.body).toMatchObject({ lapi_url: 'http://crowdsec:8080', lapi_key: 'secret' });
		expect(calls.some((c) => c.method === 'PUT')).toBe(false);
	});
});

describe('decisions', () => {
	const decisions = [{ id: 1, origin: 'crowdsec', type: 'ban', scope: 'Ip', value: '203.0.113.9', duration: '3h59m', scenario: 'crowdsecurity/ssh-bf' }];
	it('lists decisions and unbans one after confirmation', async () => {
		handlers = { ...base(complete()), 'GET /api/v1/crowdsec/decisions': () => json(decisions), 'DELETE /api/v1/crowdsec/decisions/id/1': () => json({ deleted: true }) };
		render(CrowdSecPage, { navigate: () => {}, tab: 'decisions' });
		await waitFor(() => expect(screen.getAllByText('203.0.113.9').length).toBeGreaterThan(0));
		await fireEvent.click(screen.getByRole('button', { name: 'Unban' }));
		await fireEvent.click((await screen.findAllByRole('button', { name: 'Unban' })).at(-1)!);
		await waitFor(() => expect(calls.some((c) => c.method === 'DELETE')).toBe(true));
	});
	it('validates and posts a ban', async () => {
		handlers = { ...base(complete()), 'GET /api/v1/crowdsec/decisions': () => json([]), 'POST /api/v1/crowdsec/decisions': () => json({ added: true }, 201) };
		render(CrowdSecPage, { navigate: () => {}, mode: 'ban' });
		await fireEvent.click(await screen.findByRole('button', { name: 'Ban' }));
		expect(await screen.findByText('Enter an address or range.')).toBeTruthy();
		await fireEvent.input(screen.getByLabelText(/IP address or range/), { target: { value: 'not-an-ip' } });
		expect(await screen.findByText('Enter a valid IP address or CIDR range.')).toBeTruthy();
		await fireEvent.input(screen.getByLabelText(/IP address or range/), { target: { value: '198.51.100.0/24' } });
		await fireEvent.click(screen.getByRole('button', { name: 'Ban' }));
		await waitFor(() => expect(calls.some((c) => c.method === 'POST')).toBe(true));
		expect(calls.find((c) => c.method === 'POST')!.body).toEqual({ ip: '198.51.100.0/24', duration: '4h', reason: '' });
	});
});

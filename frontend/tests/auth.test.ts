import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/svelte';
// The API client binds `fetch` at import time, so install a forwarding stub first.
vi.hoisted(() => {
	(globalThis as any).__mockFetch = async () => {
		throw new Error('no mock');
	};
	globalThis.fetch = ((...args: unknown[]) => (globalThis as any).__mockFetch(...args)) as typeof fetch;
});
import App from '../src/App.svelte';
import { api } from '../src/shared/api/api';
import { auth } from '../src/features/auth/session.svelte';
import { passwordStrength } from '../src/features/auth/strength';

type Call = { method: string; path: string; headers: Headers; body: any };
let calls: Call[] = [];
let handlers: Record<string, (body: any) => Response> = {};

const json = (body: unknown, status = 200, headers: Record<string, string> = {}) =>
	new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json', ...headers } });
const session = {
	user: { username: 'admin', method: 'password' },
	csrf: 'csrf-123',
	expires_at: '2099-01-01T00:00:00Z',
};
const status = (over: Record<string, unknown>) => () =>
	json({ setup_required: false, authenticated: false, providers: [], ...over });
const configState = {
	drifted: false,
	drifted_files: null,
	restart_required: false,
	running_operation: false,
	archived_drafts: 0,
	archived_secret_drafts: 0,
	plugin_updates: 0,
};

beforeEach(() => {
	calls = [];
	handlers = { 'GET /api/v1/config/state': () => json(configState) };
	window.location.hash = '#/';
	localStorage.clear();
	window.matchMedia = ((query: string) => ({
		matches: false,
		media: query,
		addEventListener() {},
		removeEventListener() {},
		addListener() {},
		removeListener() {},
		onchange: null,
		dispatchEvent: () => false,
	})) as typeof window.matchMedia;
	(globalThis as any).__mockFetch = async (path: string, init: RequestInit = {}) => {
		const method = init.method ?? 'GET';
		const body = typeof init.body === 'string' ? JSON.parse(init.body) : undefined;
		calls.push({ method, path, headers: new Headers(init.headers), body });
		const handler = handlers[`${method} ${path.split('?')[0]}`];
		return handler ? handler(body) : json({ code: 'NOT_FOUND', message: 'not mocked' }, 404);
	};
});
afterEach(() => cleanup());

describe('which screen is shown', () => {
	it('shows first-run setup while no administrator exists, and keeps the console closed', async () => {
		handlers['GET /api/v1/auth/status'] = status({ setup_required: true });
		window.location.hash = '#/routers';
		render(App);
		expect(await screen.findByRole('heading', { name: 'Set up the console' })).toBeTruthy();
		expect(window.location.hash).toBe('#/setup');
		expect(screen.queryByRole('navigation', { name: /Home/ })).toBeNull();
		expect(calls.some((c) => c.path === '/api/v1/routers')).toBe(false);
	});

	it('never shows setup once an administrator exists, even when asked for it', async () => {
		handlers['GET /api/v1/auth/status'] = status({});
		window.location.hash = '#/setup';
		render(App);
		expect(await screen.findByRole('heading', { name: 'Sign in' })).toBeTruthy();
		expect(window.location.hash).toBe('#/login');
		expect(screen.queryByText('Set up the console')).toBeNull();
	});

	it('leaves the auth screens for the console when already signed in', async () => {
		handlers['GET /api/v1/auth/status'] = status({ authenticated: true, session });
		window.location.hash = '#/login';
		render(App);
		await waitFor(() => expect(window.location.hash).toBe('#/'));
		expect(screen.queryByRole('heading', { name: 'Sign in' })).toBeNull();
	});

	it('offers a retry when the server is unreachable', async () => {
		handlers['GET /api/v1/auth/status'] = () => json({ code: 'X', message: 'down' }, 503);
		render(App);
		expect(await screen.findByRole('heading', { name: 'Console unreachable' })).toBeTruthy();
	});
});

describe('setup', () => {
	it('walks token → account and opens the console with the new session', async () => {
		handlers['GET /api/v1/auth/status'] = status({ setup_required: true });
		handlers['POST /api/v1/auth/setup'] = () => json(session);
		render(App);
		await fireEvent.input(await screen.findByLabelText(/Setup token/), {
			target: { value: 'TCS-ABCD-EFGH-IJKL-MN23' },
		});
		await fireEvent.click(screen.getByRole('button', { name: 'Continue' }));
		await fireEvent.input(screen.getByLabelText(/^Password/), { target: { value: 'a long enough password' } });
		await fireEvent.input(screen.getByLabelText(/Repeat password/), { target: { value: 'a different password!' } });
		await fireEvent.click(screen.getByRole('button', { name: 'Create administrator' }));
		expect(await screen.findByText('The two passwords are not the same.')).toBeTruthy();
		expect(calls.some((c) => c.path === '/api/v1/auth/setup')).toBe(false);

		await fireEvent.input(screen.getByLabelText(/Repeat password/), { target: { value: 'a long enough password' } });
		await fireEvent.click(screen.getByRole('button', { name: 'Create administrator' }));
		await waitFor(() => expect(auth.phase).toBe('ready'));
		const setup = calls.find((c) => c.path === '/api/v1/auth/setup')!;
		expect(setup.body).toEqual({
			setup_token: 'TCS-ABCD-EFGH-IJKL-MN23',
			username: 'admin',
			password: 'a long enough password',
		});
	});

	it('checks the token shape before continuing', async () => {
		handlers['GET /api/v1/auth/status'] = status({ setup_required: true });
		render(App);
		await fireEvent.input(await screen.findByLabelText(/Setup token/), { target: { value: 'TCS-ABCD' } });
		await fireEvent.click(screen.getByRole('button', { name: 'Continue' }));
		expect(await screen.findByText("Paste the full token from Console's logs.")).toBeTruthy();
		await fireEvent.input(screen.getByLabelText(/Setup token/), { target: { value: 'abcd efgh ijkl mn23' } });
		await fireEvent.click(screen.getByRole('button', { name: 'Continue' }));
		expect(await screen.findByLabelText(/^Password/)).toBeTruthy();
	});

	it('returns to the token step when the server rejects the token', async () => {
		handlers['GET /api/v1/auth/status'] = status({ setup_required: true });
		handlers['POST /api/v1/auth/setup'] = () =>
			json({ code: 'AUTH_INVALID_SETUP_TOKEN', message: 'bad', field_errors: { setup_token: 'bad' } }, 401);
		render(App);
		await fireEvent.input(await screen.findByLabelText(/Setup token/), {
			target: { value: 'TCS-WRON-GWRO-NGWR-ONG2' },
		});
		await fireEvent.click(screen.getByRole('button', { name: 'Continue' }));
		await fireEvent.input(screen.getByLabelText(/^Password/), { target: { value: 'a long enough password' } });
		await fireEvent.input(screen.getByLabelText(/Repeat password/), { target: { value: 'a long enough password' } });
		await fireEvent.click(screen.getByRole('button', { name: 'Create administrator' }));
		expect(await screen.findByText(/new token from its logs/)).toBeTruthy();
		expect(screen.getByLabelText(/Setup token/)).toBeTruthy();
	});
});

describe('sign-in and session', () => {
	it('signs in, then sends the CSRF token on writes and not on reads', async () => {
		handlers['GET /api/v1/auth/status'] = status({});
		handlers['POST /api/v1/auth/login'] = () => json(session);
		render(App);
		await fireEvent.input(await screen.findByLabelText('Username'), { target: { value: 'admin' } });
		await fireEvent.input(screen.getByLabelText('Password'), { target: { value: 'correct horse battery' } });
		await fireEvent.click(screen.getByRole('button', { name: 'Sign in' }));
		await waitFor(() => expect(auth.phase).toBe('ready'));
		const login = calls.find((c) => c.path === '/api/v1/auth/login')!;
		expect(login.headers.get('X-CSRF-Token')).toBeNull();

		handlers['POST /api/v1/snapshots'] = () => json({ id: 1 }, 201);
		handlers['GET /api/v1/routers'] = () => json([]);
		await api.post('/api/v1/snapshots', { name: 'x' });
		await api.get('/api/v1/routers');
		expect(calls.find((c) => c.path === '/api/v1/snapshots')!.headers.get('X-CSRF-Token')).toBe('csrf-123');
		expect(
			calls
				.filter((c) => c.path === '/api/v1/routers')
				.at(-1)!
				.headers.get('X-CSRF-Token'),
		).toBeNull();
	});

	it('shows the server message on a wrong password and counts down a lockout', async () => {
		handlers['GET /api/v1/auth/status'] = status({});
		handlers['POST /api/v1/auth/login'] = () =>
			json({ code: 'AUTH_RATE_LIMITED', message: 'slow down' }, 429, { 'Retry-After': '30' });
		render(App);
		await fireEvent.input(await screen.findByLabelText('Username'), { target: { value: 'admin' } });
		await fireEvent.input(screen.getByLabelText('Password'), { target: { value: 'nope' } });
		await fireEvent.click(screen.getByRole('button', { name: 'Sign in' }));
		expect(await screen.findByText(/Try again in 30 s/)).toBeTruthy();
		expect((screen.getByRole('button', { name: 'Sign in' }) as HTMLButtonElement).disabled).toBe(true);
	});

	it('returns to sign-in with a notice when the server ends the session', async () => {
		handlers['GET /api/v1/auth/status'] = status({ authenticated: true, session });
		render(App);
		await waitFor(() => expect(auth.phase).toBe('ready'));
		handlers['GET /api/v1/routers'] = () => json({ code: 'UNAUTHORIZED', message: 'ended' }, 401);
		await expect(api.get('/api/v1/routers')).rejects.toThrow();
		expect(await screen.findByText('Your session ended. Sign in again to continue.')).toBeTruthy();
		expect(window.location.hash).toBe('#/login');
	});

	it('explains how to reset a forgotten password, with a copy button', async () => {
		handlers['GET /api/v1/auth/status'] = status({});
		const writeText = vi.fn(async () => {});
		Object.defineProperty(navigator, 'clipboard', { value: { writeText }, configurable: true });
		render(App);
		const link = await screen.findByRole('button', { name: 'Forgot password?' });
		const username = screen.getByLabelText('Username');
		expect(screen.queryByText('server admin reset-password')).toBeNull();
		await fireEvent.click(link);
		expect(screen.getByText('Reset the password from the host')).toBeTruthy();
		expect(screen.getByText('server admin reset-password')).toBeTruthy();
		expect(screen.getByText(/console’s folder on the host/)).toBeTruthy();
		expect(username.compareDocumentPosition(link) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
		await fireEvent.click(screen.getByRole('button', { name: 'Copy command' }));
		expect(writeText).toHaveBeenCalledWith('server admin reset-password');
		expect(await screen.findByText('Command copied')).toBeTruthy();
	});

	it('signing out returns to the sign-in page without a notice', async () => {
		handlers['GET /api/v1/auth/status'] = status({ authenticated: true, session });
		handlers['POST /api/v1/auth/logout'] = () => json({ signed_out: true });
		render(App);
		await waitFor(() => expect(auth.phase).toBe('ready'));
		const { signOut } = await import('../src/features/auth/session.svelte');
		await signOut();
		expect(await screen.findByRole('heading', { name: 'Sign in' })).toBeTruthy();
		expect(screen.queryByText(/signed out/i)).toBeNull();
		expect(document.querySelector('.notice')).toBeNull();
	});

	it('the Okta template fills OpenID Connect defaults and asks for the organisation address', async () => {
		handlers['GET /api/v1/auth/status'] = status({ authenticated: true, session });
		handlers['GET /api/v1/auth/providers'] = () => json({ items: [] });
		window.location.hash = '#/settings/security/providers/new';
		render(App);
		await fireEvent.click(await screen.findByRole('button', { name: 'Okta' }));
		const issuer = screen.getByLabelText(/Issuer URL/, { selector: 'input, select, textarea' }) as HTMLInputElement;
		expect((screen.getByLabelText(/^Type/, { selector: 'input, select, textarea' }) as HTMLSelectElement).value).toBe(
			'oidc',
		);
		expect(issuer.value).toBe('');
		expect(issuer.placeholder).toBe('https://your-org.okta.com/oauth2/default');
		expect(
			(screen.getByLabelText(/Button label/, { selector: 'input, select, textarea' }) as HTMLInputElement).value,
		).toBe('Okta');
		await waitFor(() => expect(document.activeElement).toBe(issuer));
	});

	it('switching the type select changes the connection fields', async () => {
		handlers['GET /api/v1/auth/status'] = status({ authenticated: true, session });
		handlers['GET /api/v1/auth/providers'] = () => json({ items: [] });
		window.location.hash = '#/settings/security/providers/new';
		render(App);
		expect(await screen.findByLabelText(/Issuer URL/, { selector: 'input, select, textarea' })).toBeTruthy();
		await fireEvent.change(screen.getByLabelText(/^Type/, { selector: 'input, select, textarea' }), {
			target: { value: 'oauth2' },
		});
		expect(screen.queryByLabelText(/Issuer URL/, { selector: 'input, select, textarea' })).toBeNull();
		expect(screen.getByLabelText(/Authorization URL/, { selector: 'input, select, textarea' })).toBeTruthy();
	});

	it('shows one button per provider and explains a refused external sign-in', async () => {
		handlers['GET /api/v1/auth/status'] = status({
			providers: [
				{ id: 'authentik', display_name: 'Authentik' },
				{ id: 'github', display_name: 'GitHub' },
			],
		});
		window.location.hash = '#/login?error=not_allowed';
		render(App);
		const link = await screen.findByRole('link', { name: 'Sign in with Authentik' });
		expect(link.getAttribute('href')).toBe('/api/v1/auth/oauth/start?provider=authentik');
		// Provider buttons come after the password form.
		const submit = screen.getByRole('button', { name: 'Sign in' });
		expect(submit.compareDocumentPosition(link) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
		expect(screen.getByRole('link', { name: 'Sign in with GitHub' }).getAttribute('href')).toBe(
			'/api/v1/auth/oauth/start?provider=github',
		);
		expect(screen.getByText(/not on this console's allow list/)).toBeTruthy();
	});
});

describe('security page', () => {
	const provider = {
		id: 'github',
		kind: 'oauth2',
		display_name: 'GitHub',
		issuer: '',
		authorize_url: 'https://github.com/login/oauth/authorize',
		token_url: 'https://github.com/login/oauth/access_token',
		userinfo_url: 'https://api.github.com/user',
		client_id: 'abc',
		scopes: ['read:user'],
		identity_claim: 'login',
		allowed_identities: ['octocat'],
		redirect_url: 'http://localhost/api/v1/auth/oauth/callback',
		credential_stored: true,
		updated_at: '2026-11-23T10:00:00Z',
	};
	beforeEach(() => {
		handlers['GET /api/v1/auth/status'] = status({ authenticated: true, session });
	});

	it('shows each provider as a card with its sign-in button and opens the edit dialog', async () => {
		handlers['GET /api/v1/auth/providers'] = () => json({ items: [provider] });
		window.location.hash = '#/settings/security/providers';
		render(App);
		expect(await screen.findByRole('heading', { name: 'GitHub' })).toBeTruthy();
		expect(screen.getByText('Sign in with GitHub')).toBeTruthy();
		expect(screen.getByText('octocat')).toBeTruthy();
		expect(screen.queryByRole('table')).toBeNull();
		await fireEvent.click(screen.getByRole('button', { name: 'Edit' }));
		await waitFor(() => expect(window.location.hash).toBe('#/settings/security/providers/github/edit'));
		expect(await screen.findByDisplayValue('abc')).toBeTruthy();
	});

	it('adds a provider only after the password is confirmed in a dialog', async () => {
		let saved: any;
		handlers['GET /api/v1/auth/providers'] = () =>
			json({ items: saved ? [{ ...provider, ...saved, id: 'github' }] : [] });
		handlers['POST /api/v1/auth/providers'] = (body) => {
			if (body.current_password !== 'correct horse battery')
				return json(
					{
						code: 'AUTH_INVALID_CREDENTIALS',
						message: 'The current password is incorrect.',
						field_errors: { current_password: 'The current password is incorrect.' },
					},
					422,
				);
			saved = body;
			return json({ ...provider, display_name: body.display_name }, 201);
		};
		window.location.hash = '#/settings/security/providers/new';
		render(App);
		// Templates are plain fill-in buttons (Google, GitHub, Okta), not toggles.
		const githubTemplate = await screen.findByRole('button', { name: 'GitHub' });
		expect(githubTemplate.hasAttribute('aria-pressed')).toBe(false);
		expect(screen.queryByRole('button', { name: 'OpenID Connect' })).toBeNull();
		expect(screen.getByRole('button', { name: 'Okta' })).toBeTruthy();
		await fireEvent.click(githubTemplate);
		expect((screen.getByLabelText(/^Type/, { selector: 'input, select, textarea' }) as HTMLSelectElement).value).toBe(
			'oauth2',
		);
		expect(
			(screen.getByLabelText(/Token URL/, { selector: 'input, select, textarea' }) as HTMLInputElement).value,
		).toBe('https://github.com/login/oauth/access_token');
		expect(screen.queryByText('Advanced')).toBeNull();
		// Every field explains itself through an info tip wired to the control.
		const info = screen.getByRole('button', { name: 'About Client ID' });
		expect(
			screen.getByLabelText(/Client ID/, { selector: 'input, select, textarea' }).getAttribute('aria-describedby'),
		).toContain('prov-client-info');
		await fireEvent.mouseEnter(info);
		expect(info.getAttribute('aria-expanded')).toBe('true');
		await fireEvent.input(screen.getByLabelText(/Client ID/, { selector: 'input, select, textarea' }), {
			target: { value: 'abc' },
		});
		await fireEvent.input(screen.getByLabelText(/Client secret/, { selector: 'input, select, textarea' }), {
			target: { value: 'shh' },
		});
		await fireEvent.input(screen.getByLabelText(/Allowed accounts/, { selector: 'input, select, textarea' }), {
			target: { value: 'octocat' },
		});
		expect(screen.queryByText(/Offer this sign-in/)).toBeNull();
		await fireEvent.click(screen.getAllByRole('button', { name: 'Add provider' }).at(-1)!);

		const dialog = await screen.findByRole('dialog', { name: 'Confirm with your password' });
		expect(calls.some((c) => c.method === 'POST' && c.path === '/api/v1/auth/providers')).toBe(false);
		const field = dialog.querySelector('input')!;
		await fireEvent.input(field, { target: { value: 'wrong one' } });
		await fireEvent.click(screen.getAllByRole('button', { name: 'Add provider' }).at(-1)!);
		expect(await screen.findByText('The current password is incorrect.')).toBeTruthy();
		await fireEvent.input(field, { target: { value: 'correct horse battery' } });
		await fireEvent.click(screen.getAllByRole('button', { name: 'Add provider' }).at(-1)!);
		await waitFor(() => expect(window.location.hash).toBe('#/settings/security/providers'));
		expect(saved).toMatchObject({
			kind: 'oauth2',
			display_name: 'GitHub',
			client_id: 'abc',
			client_secret: 'shh',
			allowed_identities: ['octocat'],
			identity_claim: 'login',
			token_url: 'https://github.com/login/oauth/access_token',
		});
		expect(saved.enabled).toBeUndefined();
	});

	it('signs out one other session from the sessions table', async () => {
		const ua = 'Mozilla/5.0 (X11; Linux x86_64) Chrome/140.0 Safari/537.36';
		handlers['GET /api/v1/auth/sessions'] = () =>
			json({
				items: [
					{
						id: 'me',
						method: 'password',
						provider: '',
						ip: '10.0.0.2',
						user_agent: ua,
						signed_in_at: '2026-11-23T10:00:00Z',
						last_seen_at: '2026-11-23T10:00:00Z',
						current: true,
					},
					{
						id: 'other',
						method: 'oauth',
						provider: 'GitHub',
						ip: '10.0.0.9',
						user_agent: 'Mozilla/5.0 (Windows NT 10.0) Firefox/140.0',
						signed_in_at: '2026-11-23T09:00:00Z',
						last_seen_at: '2026-11-23T09:30:00Z',
						current: false,
					},
				],
			});
		handlers['POST /api/v1/auth/sessions/other/revoke'] = () => json({ revoked: true });
		window.location.hash = '#/settings/security/sessions';
		render(App);
		expect(await screen.findByText('Firefox on Windows')).toBeTruthy();
		expect(screen.getByText('This browser')).toBeTruthy();
		expect(screen.getAllByRole('button', { name: 'Sign out' })).toHaveLength(1);
		await fireEvent.click(screen.getByRole('button', { name: 'Sign out' }));
		await fireEvent.click(screen.getAllByRole('button', { name: 'Sign out' }).at(-1)!);
		await waitFor(() => expect(calls.some((c) => c.path === '/api/v1/auth/sessions/other/revoke')).toBe(true));
	});
});

describe('password strength', () => {
	it('follows the server rule: length first', () => {
		expect(passwordStrength('short')).toBe(0);
		expect(passwordStrength('abcdefghijkl')).toBe(1);
		expect(passwordStrength('Abcdefghijk1')).toBe(2);
		expect(passwordStrength('a long passphrase you remember')).toBe(3);
	});
});

// The single source of truth for "who is signed in". The SPA renders exactly
// one of: loading, unavailable, setup, login, or the console (ready), from the
// server's /auth/status answer. The CSRF token stays in memory: a reload asks
// the server again (the session cookie itself is HttpOnly).
import { api } from '../../shared/api/api';
import { ApiError } from '../../shared/api/client';
import * as authApi from './api';
import type { AuthSession, PublicProvider } from './models';

export type AuthPhase = 'loading' | 'unavailable' | 'setup' | 'login' | 'ready';
export type LoginNotice = '' | 'sessionExpired';

export const auth = $state<{
	phase: AuthPhase;
	session: AuthSession | null;
	providers: PublicProvider[];
	notice: LoginNotice;
}>({ phase: 'loading', session: null, providers: [], notice: '' });

let wired = false;

function wire() {
	if (wired) return;
	wired = true;
	api.setHeaderProvider((method): Record<string, string> =>
		method !== 'GET' && auth.session ? { 'X-CSRF-Token': auth.session.csrf } : {},
	);
	// Any protected call answering 401 means the session ended elsewhere
	// (expiry, sign-out on another device, password reset from the CLI).
	api.setUnauthorizedHandler(() => {
		if (auth.phase === 'ready') endSession('sessionExpired');
	});
}

/** Ask the server which screen to show. Safe to call again (Retry). */
export async function bootstrap(): Promise<void> {
	wire();
	auth.phase = 'loading';
	try {
		const status = await authApi.getStatus();
		auth.providers = status.providers ?? [];
		if (status.setup_required) {
			auth.session = null;
			auth.phase = 'setup';
		} else if (status.authenticated && status.session) {
			auth.session = status.session;
			auth.phase = 'ready';
		} else {
			auth.session = null;
			auth.phase = 'login';
		}
	} catch (err) {
		auth.phase = err instanceof ApiError && err.status < 500 ? 'login' : 'unavailable';
	}
}

/** Adopt a session returned by setup, sign-in or a password change. */
export function startSession(session: AuthSession): void {
	auth.session = session;
	auth.notice = '';
	auth.phase = 'ready';
}

export function endSession(notice: LoginNotice): void {
	auth.session = null;
	auth.notice = notice;
	auth.phase = 'login';
}

/** Sign out on the server first; only then drop local state. No notice: the sign-in screen is the confirmation. */
export async function signOut(): Promise<void> {
	await authApi.signOut();
	endSession('');
}

/** Setup finished elsewhere (another tab or the CLI): go to sign-in. */
export function setupAlreadyDone(): void {
	auth.phase = 'login';
	void bootstrap();
}

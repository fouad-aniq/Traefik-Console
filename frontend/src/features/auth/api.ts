import { api } from '../../shared/api/api';
import type { AuthEvent, AuthSession, AuthStatus, ProviderInput, ProviderSettings, SessionInfo } from './models';

const AUTH = '/api/v1/auth';
const SLOW = { timeoutMs: 30_000 };

export const getStatus = (signal?: AbortSignal) => api.get<AuthStatus>(`${AUTH}/status`, { signal });

export const setupAdmin = (body: { setup_token: string; username: string; password: string }) =>
	api.post<AuthSession>(`${AUTH}/setup`, body, SLOW);

export const signIn = (body: { username: string; password: string }) => api.post<AuthSession>(`${AUTH}/login`, body, SLOW);

export const signOut = () => api.post<{ signed_out: boolean }>(`${AUTH}/logout`);

export const changePassword = (body: { current_password: string; new_password: string }) =>
	api.post<AuthSession>(`${AUTH}/password`, body, SLOW);

export const listSessions = () => api.get<{ items: SessionInfo[] }>(`${AUTH}/sessions`);

export const revokeSession = (id: string) => api.post<{ revoked: boolean }>(`${AUTH}/sessions/${encodeURIComponent(id)}/revoke`);

export const revokeOtherSessions = () => api.post<{ revoked: number }>(`${AUTH}/sessions/revoke-others`);

export const recentActivity = () => api.get<{ items: AuthEvent[] }>(`${AUTH}/activity`);

export const listProviders = () => api.get<{ items: ProviderSettings[] }>(`${AUTH}/providers`);

/** Creates when id is empty, otherwise replaces that provider. */
export const saveProvider = (id: string, body: ProviderInput & { current_password: string }) =>
	id
		? api.put<ProviderSettings>(`${AUTH}/providers/${encodeURIComponent(id)}`, body, SLOW)
		: api.post<ProviderSettings>(`${AUTH}/providers`, body, SLOW);

export const deleteProvider = (id: string, current_password: string) =>
	api.post<{ deleted: boolean }>(`${AUTH}/providers/${encodeURIComponent(id)}/delete`, { current_password }, SLOW);

export const signInWithProviderPath = (id: string) => `${AUTH}/oauth/start?provider=${encodeURIComponent(id)}`;

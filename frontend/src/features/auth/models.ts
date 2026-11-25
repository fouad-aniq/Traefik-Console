// Wire types for /api/v1/auth (see api/openapi.json, tag "authentication").

export type SignInMethod = 'password' | 'oauth';

export interface AuthSession {
	user: { username: string; method: SignInMethod; provider?: string };
	/** Sent as X-CSRF-Token on writes. Memory only, never persisted. */
	csrf: string;
	expires_at: string;
}

/** One "Sign in with …" button. */
export interface PublicProvider {
	id: string;
	display_name: string;
}

export interface AuthStatus {
	setup_required: boolean;
	authenticated: boolean;
	session?: AuthSession;
	providers: PublicProvider[];
}

export type ProviderKind = 'oidc' | 'oauth2';

export interface ProviderSettings {
	id: string;
	kind: ProviderKind;
	display_name: string;
	issuer: string;
	authorize_url: string;
	token_url: string;
	userinfo_url: string;
	client_id: string;
	scopes: string[];
	identity_claim: string;
	allowed_identities: string[];
	redirect_url: string;
	credential_stored: boolean;
	updated_at: string;
}

export interface ProviderInput extends Omit<ProviderSettings, 'id' | 'credential_stored' | 'updated_at'> {
	client_secret: string;
	clear_client_secret: boolean;
}

export interface SessionInfo {
	id: string;
	method: SignInMethod;
	/** provider label for external sessions */
	provider: string;
	ip: string;
	user_agent: string;
	signed_in_at: string;
	last_seen_at: string;
	current: boolean;
}

export interface AuthEvent {
	at: string;
	event: string;
	outcome: 'success' | 'failure';
	ip: string;
	detail: string;
}

export const CALLBACK_PATH = '/api/v1/auth/oauth/callback';
export const MIN_PASSWORD = 12;
export const MAX_PASSWORD = 128;
export const USERNAME_PATTERN = /^[A-Za-z0-9._@-]{3,64}$/;

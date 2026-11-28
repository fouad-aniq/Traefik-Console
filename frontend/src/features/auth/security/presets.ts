import type { ProviderKind, ProviderSettings } from '../models';

/** oidc and oauth2 are the protocol defaults; google, github and okta are the one-click templates. */
export type Preset = 'oidc' | 'google' | 'github' | 'okta' | 'oauth2';

/** Templates offered as buttons in the add dialog. */
export const TEMPLATES: Preset[] = ['google', 'github', 'okta'];

export interface PresetDefaults {
	kind: ProviderKind;
	display_name: string;
	issuer: string;
	authorize_url: string;
	token_url: string;
	userinfo_url: string;
	scopes: string[];
	identity_claim: string;
	/** shown in an address left empty because it differs per organisation (Okta) */
	issuerPlaceholder?: string;
}

export const PRESETS: Record<Preset, PresetDefaults> = {
	oidc: {
		kind: 'oidc',
		display_name: '',
		issuer: '',
		authorize_url: '',
		token_url: '',
		userinfo_url: '',
		scopes: ['openid', 'email', 'profile'],
		identity_claim: 'email',
	},
	google: {
		kind: 'oidc',
		display_name: 'Google',
		issuer: 'https://accounts.google.com',
		authorize_url: '',
		token_url: '',
		userinfo_url: '',
		scopes: ['openid', 'email', 'profile'],
		identity_claim: 'email',
	},
	github: {
		kind: 'oauth2',
		display_name: 'GitHub',
		issuer: '',
		authorize_url: 'https://github.com/login/oauth/authorize',
		token_url: 'https://github.com/login/oauth/access_token',
		userinfo_url: 'https://api.github.com/user',
		scopes: ['read:user'],
		identity_claim: 'login',
	},
	okta: {
		kind: 'oidc',
		display_name: 'Okta',
		issuer: '',
		authorize_url: '',
		token_url: '',
		userinfo_url: '',
		scopes: ['openid', 'email', 'profile'],
		identity_claim: 'email',
		issuerPlaceholder: 'https://your-org.okta.com/oauth2/default',
	},
	oauth2: {
		kind: 'oauth2',
		display_name: '',
		issuer: '',
		authorize_url: '',
		token_url: '',
		userinfo_url: '',
		scopes: [],
		identity_claim: '',
	},
};

/** Which template a saved provider matches (for the type column and the edit form). */
export function presetOf(
	p: Pick<ProviderSettings, 'kind' | 'issuer' | 'authorize_url' | 'token_url' | 'userinfo_url'>,
): Preset {
	if (p.kind === 'oidc') {
		if (p.issuer === PRESETS.google.issuer) return 'google';
		try {
			if (/\.okta(preview)?\.com$|\.okta-emea\.com$/.test(new URL(p.issuer).hostname)) return 'okta';
		} catch {
			/* not a URL yet */
		}
		return 'oidc';
	}
	const gh = PRESETS.github;
	return p.authorize_url === gh.authorize_url && p.token_url === gh.token_url && p.userinfo_url === gh.userinfo_url
		? 'github'
		: 'oauth2';
}

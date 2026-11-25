import type { Resource } from '../../core/types';

export interface Propagation {
	delayBeforeChecks?: string;
	disableANSChecks?: boolean;
	disableChecks?: boolean;
	requireAllRNS?: boolean;
}

/** ResolverInput (request) and Resolver (response config) share these fields. */
export interface ResolverConfig {
	name?: string;
	type: 'acme' | 'custom';
	environment?: 'staging' | 'production' | 'custom';
	ca_url?: string;
	email?: string;
	key_type?: string;
	challenge?: 'dns01' | 'http01' | 'tlsalpn01';
	entry_point?: string;
	provider?: string;
	alternative?: number;
	values?: Record<string, string>;
	nameservers?: string[];
	delay?: string;
	propagation?: Propagation;
	storage?: string;
	custom_yaml?: string;
	external_credentials?: boolean;
	warnings?: string[];
	rendered_node?: Record<string, unknown>;
}

export type ResolverResource = Resource<ResolverConfig>;

export interface ProviderField {
	env_var: string;
	type: 'string' | 'secret' | 'duration' | 'integer' | 'boolean';
	description: string;
	is_secret: boolean;
	required: boolean;
	file_support: boolean;
	default_value?: string;
	bounds?: string;
	aliases?: string[];
}

export interface AuthAlternative {
	alternative_name: string;
	description: string;
	required_fields: ProviderField[] | null;
	optional_fields: ProviderField[] | null;
}

export interface Provider {
	provider_code: string;
	display_name: string;
	doc_url: string;
	form_supported: boolean;
	file_support_all: boolean;
	verification_status: string;
	source_audit_notes: string;
	auth_alternatives: AuthAlternative[] | null;
	global_options: ProviderField[] | null;
}

export interface ProviderDiscovery {
	providers: Provider[];
	custom_provider_codes: string[];
}

/** Editable form state; converted to ResolverConfig by `toInput`. */
export interface ResolverForm {
	name: string;
	type: 'acme' | 'custom';
	environment: 'staging' | 'production' | 'custom';
	caUrl: string;
	email: string;
	keyType: string;
	challenge: 'dns01' | 'http01' | 'tlsalpn01';
	entryPoint: string;
	provider: string;
	alternative: number;
	values: Record<string, string>;
	externalCredentials: boolean;
	nameservers: string[];
	delay: string;
	propagation: { requireAllRNS: boolean; disableANSChecks: boolean; disableChecks: boolean };
	customYaml: string;
}

/** The retain marker core-v2 returns for stored secrets and accepts back unchanged. */
export const SECRET_MASK = '••••••••';
export const isMasked = (value: string | undefined) => !!value && value.startsWith(SECRET_MASK);

/** A provider field is a credential only when the registry says so explicitly. */
export const isSecretField = (field: Pick<ProviderField, 'is_secret'>) => field.is_secret === true;

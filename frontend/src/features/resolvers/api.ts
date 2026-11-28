import { api } from '../../shared/api/api';
import { list } from '../../core/types';
import { encodeName, removeResource, saveResource, type WriteOptions } from '../../core/mutations';
import {
	type AuthAlternative,
	type Provider,
	type ProviderDiscovery,
	type ProviderField,
	type ResolverConfig,
	type ResolverForm,
	type ResolverResource,
	isMasked,
} from './models';

export const listResolvers = async (signal?: AbortSignal) =>
	list(await api.get<ResolverResource[] | null>('/api/v1/resolvers', { signal }));

export const getResolver = (name: string, signal?: AbortSignal) =>
	api.get<ResolverResource>('/api/v1/resolvers/' + encodeName(name), { signal });

export async function listProviders(signal?: AbortSignal): Promise<ProviderDiscovery> {
	const result = await api.get<ProviderDiscovery>('/api/v1/resolvers/providers', { signal });
	return { providers: list(result.providers), custom_provider_codes: list(result.custom_provider_codes) };
}

/** Registered entrypoint names (static configuration; no Traefik runtime needed). */
export async function listEntryPoints(signal?: AbortSignal): Promise<string[]> {
	const result = await api.get<{ entry_points: unknown[] | null }>('/api/v1/entrypoints', { signal });
	return list(result.entry_points)
		.map((entry) => (typeof entry === 'string' ? entry : String((entry as { name?: unknown })?.name ?? '')))
		.filter(Boolean);
}

export const putResolver = (name: string, input: ResolverConfig, version: number, opts?: WriteOptions) =>
	saveResource<ResolverResource>('/api/v1/resolvers/' + encodeName(name), input, version, opts);

export const deleteResolver = (name: string, version: number, opts?: WriteOptions) =>
	removeResource('/api/v1/resolvers/' + encodeName(name), version, opts);

export function newForm(): ResolverForm {
	return {
		name: '',
		type: 'acme',
		environment: 'staging', // test authority first; production needs a separate resolver
		caUrl: '',
		email: '',
		keyType: '',
		challenge: 'dns01',
		entryPoint: '',
		provider: 'cloudflare',
		alternative: 0,
		values: {},
		externalCredentials: false,
		nameservers: [],
		delay: '',
		propagation: { requireAllRNS: false, disableANSChecks: false, disableChecks: false },
		customYaml: '',
	};
}

export function formFrom(resource: ResolverResource): ResolverForm {
	const c = resource.config;
	const form = newForm();
	form.name = resource.name;
	form.type = c.type === 'custom' ? 'custom' : 'acme';
	form.environment = c.environment || 'production';
	form.caUrl = c.ca_url ?? '';
	form.email = c.email ?? '';
	form.keyType = c.key_type ?? '';
	form.challenge = c.challenge ?? 'dns01';
	form.entryPoint = c.entry_point ?? '';
	form.provider = c.provider ?? '';
	form.alternative = c.alternative ?? 0;
	form.values = { ...(c.values ?? {}) };
	form.externalCredentials = !!c.external_credentials;
	form.nameservers = [...(c.nameservers ?? [])];
	form.delay = c.delay === '0s' ? '' : (c.delay ?? '');
	form.propagation = {
		requireAllRNS: !!c.propagation?.requireAllRNS,
		disableANSChecks: !!c.propagation?.disableANSChecks,
		disableChecks: !!c.propagation?.disableChecks,
	};
	form.customYaml = c.custom_yaml ?? '';
	return form;
}

/**
 * Build the strict ResolverInput body: full payload, no nulls, no unknown keys,
 * nothing that belongs to another challenge/alternative.
 */
export function toInput(form: ResolverForm): ResolverConfig {
	if (form.type === 'custom') {
		const input: ResolverConfig = { type: 'custom', custom_yaml: form.customYaml };
		if (form.externalCredentials) input.external_credentials = true;
		return input;
	}
	const input: ResolverConfig = {
		type: 'acme',
		environment: form.environment,
		email: form.email.trim(),
		challenge: form.challenge,
	};
	if (form.environment === 'custom') input.ca_url = form.caUrl.trim();
	if (form.keyType) input.key_type = form.keyType;
	if (form.challenge === 'http01') input.entry_point = form.entryPoint;
	if (form.challenge === 'dns01') {
		input.provider = form.provider;
		if (form.externalCredentials) {
			input.external_credentials = true; // values are ignored by the server in this mode
		} else {
			input.alternative = form.alternative;
			const values = Object.fromEntries(Object.entries(form.values).filter(([, v]) => v !== '' && v != null));
			if (Object.keys(values).length) input.values = values;
		}
		if (form.nameservers.length) input.nameservers = [...form.nameservers];
		if (form.delay.trim()) input.delay = form.delay.trim();
		const p = form.propagation;
		if (p.requireAllRNS || p.disableANSChecks || p.disableChecks) {
			input.propagation = {
				...(p.requireAllRNS && { requireAllRNS: true }),
				...(p.disableANSChecks && { disableANSChecks: true }),
				...(p.disableChecks && { disableChecks: true }),
			};
		}
	}
	return input;
}

export interface FieldGroups {
	required: ProviderField[];
	optional: ProviderField[];
}

/** Required fields of the selected alternative; optional = alternative optional + provider options. */
export function providerFields(provider: Provider | undefined, alternative: number): FieldGroups {
	const auth: AuthAlternative | undefined = list(provider?.auth_alternatives)[alternative];
	const required = list(auth?.required_fields);
	const seen = new Set(required.map((f) => f.env_var));
	const optional = [...list(auth?.optional_fields), ...list(provider?.global_options)].filter((f) => {
		if (seen.has(f.env_var)) return false;
		seen.add(f.env_var);
		return true;
	});
	return { required, optional };
}

export const hasStoredValue = (value: string | undefined) => isMasked(value);

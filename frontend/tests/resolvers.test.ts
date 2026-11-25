import { describe, expect, it } from 'vitest';
import { formFrom, newForm, providerFields, toInput } from '../src/features/resolvers/api';
import { resolverErrors } from '../src/features/resolvers/validation';
import type { Provider, ResolverResource } from '../src/features/resolvers/models';

const cloudflare: Provider = {
	provider_code: 'cloudflare', display_name: 'Cloudflare', doc_url: '', form_supported: true, file_support_all: true,
	verification_status: 'source_audited', source_audit_notes: '',
	auth_alternatives: [
		{ alternative_name: 'Token', description: '', required_fields: [{ env_var: 'CLOUDFLARE_DNS_API_TOKEN', type: 'secret', description: '', is_secret: true, required: true, file_support: true }], optional_fields: [] },
		{ alternative_name: 'Key', description: '', required_fields: [{ env_var: 'CLOUDFLARE_EMAIL', type: 'string', description: '', is_secret: false, required: true, file_support: true }], optional_fields: null }
	],
	global_options: [{ env_var: 'CLOUDFLARE_TTL', type: 'integer', bounds: '120-86400', description: '', is_secret: false, required: false, file_support: true }]
};
const c = (k: string) => k;
const ctx = (form = newForm()) => ({ form, creating: true, existingNames: ['taken'], provider: cloudflare, customProviderCodes: ['cloudflare', 'route53'], entryPoints: ['web'], entryPointsKnown: true });

describe('resolver payloads', () => {
	it('builds a strict guided DNS-01 input without other-alternative or empty values', () => {
		const f = newForm();
		Object.assign(f, { name: 'le', email: 'a@b.co', environment: 'staging', challenge: 'dns01', provider: 'cloudflare', alternative: 0, nameservers: ['1.1.1.1:53'], delay: '10s' });
		f.values = { CLOUDFLARE_DNS_API_TOKEN: 'tok', CLOUDFLARE_TTL: '' };
		const input = toInput(f);
		expect(input).toEqual({ type: 'acme', environment: 'staging', email: 'a@b.co', challenge: 'dns01', provider: 'cloudflare', alternative: 0, values: { CLOUDFLARE_DNS_API_TOKEN: 'tok' }, nameservers: ['1.1.1.1:53'], delay: '10s' });
		expect(JSON.stringify(input)).not.toContain('null');
		expect(input).not.toHaveProperty('name'); // name comes from the path
	});
	it('omits values in external-credentials mode and challenge-specific fields otherwise', () => {
		const f = newForm();
		Object.assign(f, { email: 'a@b.co', challenge: 'http01', entryPoint: 'web', provider: 'cloudflare' });
		expect(toInput(f)).toEqual({ type: 'acme', environment: 'staging', email: 'a@b.co', challenge: 'http01', entry_point: 'web' });
		Object.assign(f, { challenge: 'dns01', externalCredentials: true, provider: 'route53', values: { X: 'y' } });
		const ext = toInput(f);
		expect(ext).toMatchObject({ external_credentials: true, provider: 'route53' });
		expect(ext).not.toHaveProperty('values');
		expect(ext).not.toHaveProperty('alternative');
	});
	it('sends custom YAML only', () => {
		const f = newForm();
		Object.assign(f, { type: 'custom', customYaml: 'acme: {}', email: 'ignored@x.io' });
		expect(toInput(f)).toEqual({ type: 'custom', custom_yaml: 'acme: {}' });
	});
	it('keeps masked secrets so the server retains them, and splits required/optional fields', () => {
		const res = { id: 'resolver:le', name: 'le', origin: 'managed', provider: 'static', status: 'active', read_only: false, live_health: 'unknown',
			config: { type: 'acme', environment: 'production', email: 'a@b.co', challenge: 'dns01', provider: 'cloudflare', alternative: 0, values: { CLOUDFLARE_DNS_API_TOKEN: '••••••••' }, delay: '0s' } } as ResolverResource;
		const f = formFrom(res);
		expect(f.delay).toBe('');
		expect(toInput(f).values).toEqual({ CLOUDFLARE_DNS_API_TOKEN: '••••••••' });
		const g = providerFields(cloudflare, 1);
		expect(g.required.map((x) => x.env_var)).toEqual(['CLOUDFLARE_EMAIL']);
		expect(g.optional.map((x) => x.env_var)).toEqual(['CLOUDFLARE_TTL']);
	});
});

describe('resolver validation', () => {
	it('flags identity errors and duplicates on create', () => {
		const f = newForm();
		f.name = 'taken'; f.email = 'nope';
		expect(Object.keys(resolverErrors('identity', ctx(f), c))).toEqual(['identity.name', 'identity.email']);
	});
	it('requires provider fields, accepts a stored mask, checks integer bounds and whole-second durations', () => {
		const f = newForm();
		Object.assign(f, { provider: 'cloudflare', alternative: 0 });
		expect(resolverErrors('challenge', ctx(f), c)).toHaveProperty('challenge.CLOUDFLARE_DNS_API_TOKEN');
		f.values = { CLOUDFLARE_DNS_API_TOKEN: '••••••••', CLOUDFLARE_TTL: '60' };
		expect(Object.keys(resolverErrors('challenge', ctx(f), c))).toEqual(['challenge.CLOUDFLARE_TTL']);
	});
	it('validates HTTP-01 entrypoints and advanced fields', () => {
		const f = newForm();
		Object.assign(f, { challenge: 'http01', entryPoint: 'nope' });
		expect(resolverErrors('challenge', ctx(f), c)).toHaveProperty('challenge.entryPoint');
		Object.assign(f, { challenge: 'dns01', nameservers: ['bad'], delay: '11m', keyType: 'DSA' });
		expect(Object.keys(resolverErrors('advanced', ctx(f), c)).sort()).toEqual(['advanced.delay', 'advanced.keyType', 'advanced.nameservers']);
	});
	it('requires an acme block in custom YAML', () => {
		const f = newForm();
		f.type = 'custom'; f.customYaml = 'foo: 1';
		expect(resolverErrors('challenge', ctx(f), c)).toHaveProperty('challenge.yaml');
		f.customYaml = 'acme:\n  email: a@b.co';
		expect(resolverErrors('challenge', ctx(f), c)).toEqual({});
	});
});

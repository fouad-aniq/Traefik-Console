import { describe, expect, it } from 'vitest';
import { acmeNames, newForm, usageCount, usageItems, validName, validityOf } from '../src/features/certificates/models';
import { certificateErrors, toInput } from '../src/features/certificates/validation';

const m = { invalidName: 'name', nameTaken: 'taken', invalidChain: 'chain', invalidKey: 'key' };
const CERT = '-----BEGIN CERTIFICATE-----\nAAA\n-----END CERTIFICATE-----';
const KEY = '-----BEGIN PRIVATE KEY-----\nBBB\n-----END PRIVATE KEY-----';

describe('certificate models', () => {
	it('mirrors the core-v2 name rule', () => {
		expect(validName('shop-cert_1')).toBe(true);
		for (const bad of ['', 'Shop', '1shop', 'a b', 'a'.repeat(64)]) expect(validName(bad)).toBe(false);
	});
	it('maps ACME and imported statuses to one validity vocabulary', () => {
		expect(validityOf('expiring_soon')).toBe('expiring');
		expect(validityOf('expiring')).toBe('expiring');
		expect(validityOf('expired')).toBe('expired');
		expect(validityOf('not_yet_valid')).toBe('not_yet_valid');
		expect(validityOf('valid')).toBe('valid');
	});
	it('lists covered names once, main domain first, tolerating null SANs', () => {
		const base = { resolver_name: 'le', issuer: '', serial_number: '', fingerprint: 'f', not_before: '', expires_at: '', days_left: 1, status: 'valid' };
		expect(acmeNames({ ...base, main_domain: 'a.test', sans: ['b.test', 'a.test'] })).toEqual(['a.test', 'b.test']);
		expect(acmeNames({ ...base, main_domain: 'a.test', sans: null })).toEqual(['a.test']);
	});
});

describe('certificate validation', () => {
	const ok = { ...newForm(), name: 'shop', certPem: CERT, keyPem: KEY };
	it('accepts a complete website certificate', () => {
		expect(certificateErrors(ok, { creating: true, existingNames: [] }, m)).toEqual({});
	});
	it('requires name, chain and key, and rejects a taken name only when creating', () => {
		expect(certificateErrors(newForm(), { creating: true, existingNames: [] }, m)).toEqual({ name: 'name', certPem: 'chain', keyPem: 'key' });
		expect(certificateErrors(ok, { creating: true, existingNames: ['shop'] }, m).name).toBe('taken');
		expect(certificateErrors(ok, { creating: false, existingNames: ['shop'] }, m).name).toBeUndefined();
	});
	it('does not require a key for a CA trust file and never sends one', () => {
		const ca = { ...ok, purpose: 'ca' as const, keyPem: KEY };
		expect(certificateErrors({ ...ca, keyPem: '' }, { creating: true, existingNames: [] }, m)).toEqual({});
		expect(toInput(ca)).toEqual({ purpose: 'ca', cert_pem: CERT });
		expect(toInput(ok)).toEqual({ purpose: 'server', cert_pem: CERT, key_pem: KEY });
	});
});

describe('certificate usage from core-v2', () => {
	it('turns used_by into "used by" items, domains first, names linking to the view', () => {
		const used_by = {
			domains: [{ name: 'domain-aniqhomelab-ovh', main: 'aniqhomelab.ovh', source: 'claudflare', read_only: false }],
			routers: [{ name: 'portainer', rule: 'Host(`portainer.aniqhomelab.ovh`)', entry_points: ['websecure'], service: 'portainer', tls: true, read_only: false }]
		};
		const items = usageItems(used_by);
		expect(items.map((i) => [i.kind, i.name, i.path])).toEqual([
			['domain', 'domain-aniqhomelab-ovh', '/domains/domain-aniqhomelab-ovh'],
			['route', 'portainer', '/routers/portainer']
		]);
		expect(items[0].summary).toBe('aniqhomelab.ovh');
		expect(items[0].facts).toEqual(['claudflare']);
		expect(items[1].facts).toEqual(['websecure', '→ portainer', 'TLS']);
		expect(usageCount(used_by)).toBe(2);
	});
	it('treats missing usage as empty for counting', () => {
		expect(usageItems(undefined)).toEqual([]);
		expect(usageCount(undefined)).toBe(0);
	});
});

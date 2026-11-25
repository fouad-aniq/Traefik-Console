import { describe, expect, it } from 'vitest';
import { covers, coversAll, domainKey, newForm, normalizeDomain, parseNames, validName } from '../src/features/domains/models';
import { domainErrors, toInput } from '../src/features/domains/validation';

const m = { invalidName: 'name', nameTaken: 'taken', invalidDomain: 'domain', invalidSans: 'sans', chooseMethod: 'method', chooseExisting: 'choose', notCovered: 'uncovered', wildcard: 'wild' };
const ctx = (over = {}) => ({ creating: true, existingNames: [] as string[], optionValues: ['le'], referenceExistsButUncovered: false, ...over });

describe('domain names', () => {
	it('normalizes like core-v2 and rejects what it rejects', () => {
		expect(normalizeDomain(' Example.COM. ')).toBe('example.com');
		expect(normalizeDomain('*.Example.com')).toBe('*.example.com');
		for (const bad of ['', 'localhost', '10.0.0.1', 'https://a.com', 'a.com/x', 'a.com:443', 'a*.com', '-a.com', 'a..com']) expect(normalizeDomain(bad)).toBeNull();
	});
	it('derives a valid, bounded identifier', () => {
		expect(domainKey('example.com')).toBe('domain-example-com');
		expect(domainKey('*.example.com')).toBe('domain-wildcard-example-com');
		const long = domainKey('a'.repeat(60) + '.example.com');
		expect(long.length).toBeLessThanOrEqual(63);
		expect(validName(long)).toBe(true);
	});
	it('splits additional names on lines and commas', () => {
		expect(parseNames('a.com, b.com\n\n c.com ')).toEqual(['a.com', 'b.com', 'c.com']);
	});
	it('covers hostnames like core-v2: a wildcard covers one label, a wildcard host needs the same wildcard', () => {
		expect(covers(['*.example.com'], 'shop.example.com')).toBe(true);
		expect(covers(['*.example.com'], 'a.b.example.com')).toBe(false);
		expect(covers(['*.example.com'], 'example.com')).toBe(false);
		expect(covers(['example.com'], '*.example.com')).toBe(false);
		expect(covers(['*.example.com'], '*.example.com')).toBe(true);
		expect(coversAll(['example.com', 'www.example.com'], ['example.com', 'www.example.com'])).toBe(true);
		expect(coversAll(['example.com'], ['example.com', 'www.example.com'])).toBe(false);
	});
});

describe('domain validation', () => {
	const form = { ...newForm(), name: 'domain-example-com', main: 'example.com', method: 'resolver' as const, reference: 'le' };
	it('accepts a complete form', () => {
		expect(domainErrors('domain', form, ctx(), m)).toEqual({});
		expect(domainErrors('certificate', form, ctx(), m)).toEqual({});
	});
	it('checks the generated id, hostname and additional names', () => {
		expect(domainErrors('domain', { ...form, name: 'Bad Name' }, ctx(), m).main).toBe('name');
		expect(domainErrors('domain', form, ctx({ existingNames: ['domain-example-com'] }), m).main).toBe('taken');
		expect(domainErrors('domain', form, ctx({ creating: false, existingNames: ['domain-example-com'] }), m).main).toBeUndefined();
		expect(domainErrors('domain', { ...form, main: 'nope' }, ctx(), m).main).toBe('domain');
		expect(domainErrors('domain', { ...form, sans: 'example.com' }, ctx(), m).sans).toBe('sans');
		expect(domainErrors('domain', { ...form, sans: 'a.example.com\na.example.com' }, ctx(), m).sans).toBe('sans');
	});
	it('requires a method and an available reference; flags a wildcard on a non-DNS-01 resolver', () => {
		expect(domainErrors('certificate', { ...form, method: '' }, ctx(), m).method).toBe('method');
		expect(domainErrors('certificate', { ...form, reference: 'x' }, ctx(), m).reference).toBe('choose');
		expect(domainErrors('certificate', { ...form, method: 'files', reference: 'c' }, ctx({ referenceExistsButUncovered: true }), m).reference).toBe('uncovered');
		const wild = { ...form, main: '*.example.com' };
		expect(domainErrors('certificate', wild, ctx({ resolverChallenge: 'http01' }), m).reference).toBe('wild');
		expect(domainErrors('certificate', wild, ctx({ resolverChallenge: 'dns01' }), m)).toEqual({});
		expect(domainErrors('certificate', wild, ctx(), m)).toEqual({}); // external resolver: challenge unknown
	});
	it('sends only the fields of the chosen method', () => {
		expect(toInput({ ...form, sans: 'WWW.example.com', })).toEqual({ main: 'example.com', sans: ['www.example.com'], method: 'resolver', resolver: 'le' });
		expect(toInput({ ...form, method: 'files', reference: 'shop' })).toEqual({ main: 'example.com', sans: [], method: 'files', certificate: 'shop' });
	});
});

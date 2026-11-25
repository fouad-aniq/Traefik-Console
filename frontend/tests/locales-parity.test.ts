import { describe, expect, it } from 'vitest';
import { catalogKeys, rawCatalogs, ACCEPT_LANGUAGE } from '../src/shared/localization/i18n';

describe('EN/FR catalog parity', () => {
	it('en and fr expose identical key sets', () => {
		const catalogs = rawCatalogs();
		const en = catalogKeys(catalogs.en).sort();
		const fr = catalogKeys(catalogs.fr).sort();
		expect(fr).toEqual(en);
		expect(en.length).toBeGreaterThan(20);
	});

	it('no catalog value is empty', () => {
		const catalogs = rawCatalogs();
		for (const lang of Object.keys(catalogs)) {
			for (const key of catalogKeys(catalogs[lang])) {
				const value = key.split('.').reduce<unknown>(
					(node, part) => (node && typeof node === 'object' ? (node as Record<string, unknown>)[part] : undefined),
					catalogs[lang]
				);
				expect(typeof value, `${lang}:${key}`).toBe('string');
				expect(value as string, `${lang}:${key}`).not.toHaveLength(0);
			}
		}
	});

	it('interpolation arguments match across languages', () => {
		const catalogs = rawCatalogs();
		const argsOf = (text: string) => [...text.matchAll(/\{(\w+)\}/g)].map((m) => m[1]).sort();
		for (const key of catalogKeys(catalogs.en)) {
			const en = String(key.split('.').reduce<unknown>(
				(node, part) => (node as Record<string, unknown>)[part],
				catalogs.en
			));
			const fr = String(key.split('.').reduce<unknown>(
				(node, part) => (node as Record<string, unknown>)[part],
				catalogs.fr
			));
			expect(argsOf(fr), `fr:${key}`).toEqual(argsOf(en));
		}
	});

	it('Accept-Language mapping is the bare supported tag', () => {
		expect(ACCEPT_LANGUAGE.en).toBe('en');
		expect(ACCEPT_LANGUAGE.fr).toBe('fr');
	});
});

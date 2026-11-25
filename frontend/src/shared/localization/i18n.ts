// Localization plumbing. Catalogs live in ./locales/{en,fr}.json and must stay
// in key/argument parity (enforced by tests/locales-parity.test.ts).
// Server error codes are never translated by matching prose: the API client
// surfaces structured codes and the backend's own Content-Language messages.
import { prefs } from '../preferences/preferences.svelte';
import en from './locales/en.json';
import fr from './locales/fr.json';

type Catalog = Record<string, unknown>;

const catalogs: Record<string, Catalog> = { en, fr };

export type Language = 'en' | 'fr';

export const ACCEPT_LANGUAGE: Record<Language, string> = { en: 'en', fr: 'fr' };

function lookup(catalog: Catalog, key: string): string | undefined {
	let node: unknown = catalog;
	for (const part of key.split('.')) {
		if (node && typeof node === 'object' && part in (node as Catalog)) {
			node = (node as Catalog)[part];
		} else {
			return undefined;
		}
	}
	return typeof node === 'string' ? node : undefined;
}

/**
 * Translate `key` in the selected language, interpolating {placeholders} from
 * `args`. Falls back to the English catalog, then to the key itself, so a
 * missing key is visible instead of silently blank.
 */
export function t(key: string, args?: Record<string, string | number>): string {
	const lang = prefs.language;
	let text = lookup(catalogs[lang], key) ?? lookup(catalogs.en, key) ?? key;
	if (args) {
		for (const [name, value] of Object.entries(args)) {
			text = text.replaceAll(`{${name}}`, String(value));
		}
	}
	return text;
}

/** All keys present in a catalog (dot notation), used by the parity test. */
export function catalogKeys(catalog: Catalog, prefix = ''): string[] {
	const keys: string[] = [];
	for (const [name, value] of Object.entries(catalog)) {
		const full = prefix ? `${prefix}.${name}` : name;
		if (value && typeof value === 'object') {
			keys.push(...catalogKeys(value as Catalog, full));
		} else {
			keys.push(full);
		}
	}
	return keys;
}

export function rawCatalogs(): Record<string, Catalog> {
	return catalogs;
}

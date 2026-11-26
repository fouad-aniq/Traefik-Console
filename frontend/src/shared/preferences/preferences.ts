// Nonsecret user preferences: theme, interface language, sidebar and map layer filters.
// Only these values may persist in web storage; no configuration text,
// credentials, tokens or CSRF values ever belong here (see
// docs/blueprint/frontend/TECHNICAL-REQUIREMENTS.md, "Drafts, secrets and
// language").

export type ThemePreference = 'system' | 'light' | 'dark';
export type Language = 'en' | 'fr';

const THEME_KEY = 'tc_prefs_theme';
const LANGUAGE_KEY = 'tc_prefs_lang';
const SIDEBAR_KEY = 'tc_prefs_sidebar';
const MAP_HIDDEN_KEY = 'tc_prefs_map_hidden';
const MAP_OWNERSHIP_KEY = 'tc_prefs_map_ownership';

export function loadThemePreference(): ThemePreference {
	try {
		const stored = localStorage.getItem(THEME_KEY);
		if (stored === 'light' || stored === 'dark' || stored === 'system') return stored;
	} catch {
		// storage unavailable (permissions/private mode): fall back to system
	}
	return 'system';
}

export function saveThemePreference(theme: ThemePreference): void {
	try {
		localStorage.setItem(THEME_KEY, theme);
	} catch {
		// nonfatal: preference simply does not persist
	}
}

export function loadLanguage(): Language {
	try {
		const stored = localStorage.getItem(LANGUAGE_KEY);
		if (stored === 'en' || stored === 'fr') return stored;
	} catch {
		// storage unavailable: fall back to document/browser language
	}
	const nav = typeof navigator !== 'undefined' ? navigator.language.toLowerCase() : 'en';
	return nav.startsWith('fr') ? 'fr' : 'en';
}

export function saveLanguage(language: Language): void {
	try {
		localStorage.setItem(LANGUAGE_KEY, language);
	} catch {
		// nonfatal
	}
}

export type SystemTheme = 'light' | 'dark';

export function systemTheme(): SystemTheme {
	if (typeof window === 'undefined' || !window.matchMedia) return 'light';
	return window.matchMedia('(prefers-color-scheme: dark)').matches ? 'dark' : 'light';
}

export type ResolvedTheme = SystemTheme;

/** Subscribe to OS-level scheme changes while the app is mounted. */
export function onSystemThemeChange(callback: (theme: SystemTheme) => void): () => void {
	if (typeof window === 'undefined' || !window.matchMedia) return () => {};
	const mq = window.matchMedia('(prefers-color-scheme: dark)');
	const handler = (e: MediaQueryListEvent) => callback(e.matches ? 'dark' : 'light');
	mq.addEventListener('change', handler);
	return () => mq.removeEventListener('change', handler);
}

/** Wide-screen sidebar: collapsed to the icon rail or expanded (the default). */
export function loadSidebarCollapsed(): boolean {
	try {
		return localStorage.getItem(SIDEBAR_KEY) === 'collapsed';
	} catch {
		return false;
	}
}

export function saveSidebarCollapsed(collapsed: boolean): void {
	try {
		localStorage.setItem(SIDEBAR_KEY, collapsed ? 'collapsed' : 'expanded');
	} catch {
		// Storage unavailable: the choice lasts for this page only.
	}
}

/** Map page: the node kinds the person switched off. Only known kind names are kept. */
export function loadMapHiddenKinds<T extends string>(known: readonly T[]): T[] {
	try {
		const parsed: unknown = JSON.parse(localStorage.getItem(MAP_HIDDEN_KEY) ?? '[]');
		return Array.isArray(parsed) ? known.filter((kind) => parsed.includes(kind)) : [];
	} catch {
		return [];
	}
}

export function saveMapHiddenKinds(kinds: readonly string[]): void {
	try {
		localStorage.setItem(MAP_HIDDEN_KEY, JSON.stringify(kinds));
	} catch {
		// nonfatal: the filter simply does not persist
	}
}

export type MapOwnership = 'all' | 'managed' | 'external';

/** Map page: show every object, or only the ones the Console manages, or only external ones. */
export function loadMapOwnership(): MapOwnership {
	try {
		const v = localStorage.getItem(MAP_OWNERSHIP_KEY);
		return v === 'managed' || v === 'external' ? v : 'all';
	} catch {
		return 'all';
	}
}

export function saveMapOwnership(value: MapOwnership): void {
	try {
		localStorage.setItem(MAP_OWNERSHIP_KEY, value);
	} catch {
		// nonfatal: the filter simply does not persist
	}
}

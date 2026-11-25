// Reactive preference state. Lives in a .svelte.ts module so Svelte 5 runes
// ($state/$derived) are available; imported by the shell and the API client.
// Only nonsecret theme/language values are persisted (see preferences.ts).
import {
	loadLanguage,
	loadSidebarCollapsed,
	loadThemePreference,
	onSystemThemeChange,
	saveLanguage,
	saveSidebarCollapsed,
	saveThemePreference,
	systemTheme,
	type Language,
	type ResolvedTheme,
	type ThemePreference
} from './preferences';

export type { Language, ResolvedTheme, SystemTheme, ThemePreference } from './preferences';

function applyTheme(resolved: ResolvedTheme) {
	if (typeof document !== 'undefined') {
		document.documentElement.setAttribute('data-theme', resolved);
	}
}

function applyLang(language: Language) {
	if (typeof document !== 'undefined') {
		document.documentElement.setAttribute('lang', language);
	}
}

export const prefs = $state({
	themePreference: loadThemePreference() as ThemePreference,
	system: systemTheme() as ResolvedTheme,
	language: loadLanguage() as Language,
	sidebarCollapsed: loadSidebarCollapsed()
});

export function getResolvedTheme(): ResolvedTheme {
	return prefs.themePreference === 'system' ? prefs.system : prefs.themePreference;
}

export function setThemePreference(theme: ThemePreference) {
	prefs.themePreference = theme;
	saveThemePreference(theme);
	applyTheme(getResolvedTheme());
}

export function setSystemTheme(theme: ResolvedTheme) {
	// Called by the shell's system-scheme listener; no persistence.
	prefs.system = theme;
	applyTheme(getResolvedTheme());
}

export function setSidebarCollapsed(collapsed: boolean) {
	prefs.sidebarCollapsed = collapsed;
	saveSidebarCollapsed(collapsed);
}

export function setLanguage(language: Language) {
	prefs.language = language;
	saveLanguage(language);
	applyLang(language);
}

/** Apply stored preferences once at bootstrap; returns the OS listener teardown. */
export function initPreferences(): () => void {
	applyTheme(getResolvedTheme());
	applyLang(prefs.language);
	return onSystemThemeChange(setSystemTheme);
}


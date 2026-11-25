import { prefs } from '../preferences/preferences.svelte';
import { ApiClient } from './client';

// Session wiring (CSRF header, 401 → sign-in screen) is installed by
// features/auth/session.svelte.ts once the session is known.
export const api = new ApiClient(() => prefs.language);

import { ApiError } from './client';

/** Render server-localized API prose; keep transport/internal failures user-safe. */
export function errorMessage(error: unknown, fallback: string): string {
	return error instanceof ApiError && error.message ? error.message : fallback;
}

import { describe, it, expect } from 'vitest';
import { relativeDate, fullDate } from './relativeTime';
const now = Date.parse('2026-10-25T12:00:00Z');
describe('human-readable dates', () => {
	it('handles missing and invalid dates without throwing', () => {
		expect(relativeDate('', 'en', now, 'Just now')).toBe('—');
		expect(fullDate('invalid', 'fr')).toBe('');
	});
	it('localizes past and future intervals', () => {
		expect(relativeDate('2026-10-20T12:00:00Z', 'en', now, 'Just now')).toBe('5 days ago');
		expect(relativeDate('2026-10-20T12:00:00Z', 'fr', now, 'À l’instant')).toBe('il y a 5 jours');
		expect(relativeDate('2026-10-25T14:00:00Z', 'en', now, 'Just now')).toBe('in 2 hours');
	});
	it('keeps recent dates stable and exact dates with a time zone', () => {
		expect(relativeDate('2026-10-25T11:59:35Z', 'en', now, 'Just now')).toBe('Just now');
		expect(fullDate('2026-10-20T12:26:00Z', 'en')).toContain('2026');
		expect(fullDate('2026-10-20T12:26:00Z', 'fr')).toContain('octobre');
		expect(fullDate('2026-10-20T12:26:00Z', 'en')).toMatch(/GMT|UTC|[A-Z]{2,}/);
	});
});

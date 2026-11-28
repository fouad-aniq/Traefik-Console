import { afterEach, describe, expect, it, vi } from 'vitest';
import { navigationGuard as guard } from '../src/lib/stores/navigationGuard';

afterEach(() => {
	guard.cancelDiscard();
	guard.setDirty(false);
});

describe('unsaved navigation requests', () => {
	it('executes a clean navigation immediately without a pending confirmation', () => {
		const action = vi.fn();
		expect(guard.requestNavigation(action)).toBe(true);
		expect(action).toHaveBeenCalledTimes(1);
		expect(guard.confirmPending).toBe(false);
	});
	it('cancels without executing or retaining a callback', () => {
		const action = vi.fn();
		guard.setDirty(true);
		expect(guard.requestNavigation(action)).toBe(false);
		expect(guard.confirmPending).toBe(true);
		guard.cancelDiscard();
		guard.confirmDiscard();
		expect(action).not.toHaveBeenCalled();
		expect(guard.confirmPending).toBe(false);
	});
	it('discards once, clearing dirty/pending state before navigating', () => {
		guard.setDirty(true);
		const action = vi.fn(() => {
			expect(guard.isDirty).toBe(false);
			expect(guard.confirmPending).toBe(false);
		});
		guard.requestNavigation(action);
		guard.confirmDiscard();
		guard.confirmDiscard();
		expect(action).toHaveBeenCalledTimes(1);
	});
});

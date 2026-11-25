/**
 * Short-lived notices shown after a dialog closes (for example "Applied").
 *
 * Dialogs return to their table as soon as a change is applied; the outcome
 * is announced here instead of on a result screen. Successes dismiss
 * themselves; anything that needs the user's attention stays until dismissed.
 */
export type FlashTone = 'success' | 'info' | 'warning' | 'error';

export interface Flash {
	id: number;
	tone: FlashTone;
	title: string;
	message?: string;
	/** optional hash route for a "details" link, e.g. the operation */
	href?: string;
	hrefLabel?: string;
}

const AUTO_DISMISS_MS = 6000;
let next = 1;

export const flashes = $state<Flash[]>([]);

export function dismissFlash(id: number) {
	const index = flashes.findIndex((f) => f.id === id);
	if (index >= 0) flashes.splice(index, 1);
}

export function flash(input: Omit<Flash, 'id'>): number {
	const id = next++;
	flashes.push({ id, ...input });
	if (flashes.length > 4) flashes.splice(0, flashes.length - 4);
	if (input.tone === 'success' || input.tone === 'info') setTimeout(() => dismissFlash(id), AUTO_DISMISS_MS);
	return id;
}

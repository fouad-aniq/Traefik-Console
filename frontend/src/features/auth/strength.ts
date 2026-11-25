import { MAX_PASSWORD, MIN_PASSWORD } from './models';

/**
 * A plain, explainable password check: length is what matters (the server
 * enforces 12–128). Variety only nudges a long-enough password upward.
 * 0 = too short, 1 = acceptable, 2 = good, 3 = strong.
 */
export function passwordStrength(password: string): 0 | 1 | 2 | 3 {
	const length = [...password].length;
	if (length < MIN_PASSWORD || length > MAX_PASSWORD) return 0;
	const classes = [/[a-z]/, /[A-Z]/, /[0-9]/, /[^A-Za-z0-9]/].filter((r) => r.test(password)).length;
	if (length >= 20 || (length >= 16 && classes >= 3)) return 3;
	if (length >= 16 || classes >= 3) return 2;
	return 1;
}

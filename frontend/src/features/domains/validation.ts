import { MAX_SANS, hasWildcard, normalizeDomain, parseNames, validName, type DomainForm } from './models';

export type DomainGroup = 'domain' | 'certificate';
export type DomainField = 'main' | 'sans' | 'method' | 'reference';

/** Messages are passed in so this module stays free of localization. */
export interface DomainMessages {
	invalidName: string;
	nameTaken: string;
	invalidDomain: string;
	invalidSans: string;
	chooseMethod: string;
	chooseExisting: string;
	notCovered: string;
	wildcard: string;
}

export interface DomainContext {
	creating: boolean;
	existingNames: string[];
	/** Options currently selectable for the chosen method (already filtered by coverage). */
	optionValues: string[];
	/** Whether a certificate with this name exists but does not cover every name. */
	referenceExistsButUncovered: boolean;
	/** Challenge reported for the selected resolver, when known (external resolvers report none). */
	resolverChallenge?: string;
}

export function domainErrors(
	group: DomainGroup,
	form: DomainForm,
	ctx: DomainContext,
	m: DomainMessages,
): Partial<Record<DomainField, string>> {
	const errors: Partial<Record<DomainField, string>> = {};
	if (group === 'domain') {
		const main = normalizeDomain(form.main);
		if (!main) errors.main = m.invalidDomain;
		else if (!validName(form.name)) errors.main = m.invalidName;
		else if (ctx.creating && ctx.existingNames.includes(form.name)) errors.main = m.nameTaken;
		const sans = parseNames(form.sans).map(normalizeDomain);
		const all = [main, ...sans];
		if (sans.length > MAX_SANS || sans.some((s) => !s) || new Set(all).size !== all.length) errors.sans = m.invalidSans;
		return errors;
	}
	if (form.method !== 'resolver' && form.method !== 'files') {
		errors.method = m.chooseMethod;
		return errors;
	}
	if (!form.reference || !ctx.optionValues.includes(form.reference)) {
		errors.reference =
			form.method === 'files' && form.reference && ctx.referenceExistsButUncovered ? m.notCovered : m.chooseExisting;
	} else if (
		form.method === 'resolver' &&
		hasWildcard([form.main, ...parseNames(form.sans)]) &&
		ctx.resolverChallenge &&
		ctx.resolverChallenge !== 'dns01'
	) {
		errors.reference = m.wildcard;
	}
	return errors;
}

/** Strict payload: only the fields of the chosen method are sent. */
export function toInput(form: DomainForm) {
	const main = normalizeDomain(form.main) ?? form.main.trim();
	return {
		main,
		sans: parseNames(form.sans).map((s) => normalizeDomain(s) ?? s),
		method: form.method as 'resolver' | 'files',
		...(form.method === 'resolver' ? { resolver: form.reference } : { certificate: form.reference }),
	};
}

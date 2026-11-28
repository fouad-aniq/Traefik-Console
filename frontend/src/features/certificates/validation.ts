import { MAX_PEM_BYTES, bytes, validName, type CertificateForm } from './models';
import type { CertificateInput } from './api';

export type CertificateField = 'name' | 'certPem' | 'keyPem';

/** Messages are passed in so this module stays free of localization. */
export interface CertificateMessages {
	invalidName: string;
	nameTaken: string;
	invalidChain: string;
	invalidKey: string;
}

export function certificateErrors(
	form: CertificateForm,
	opts: { creating: boolean; existingNames: string[] },
	m: CertificateMessages,
): Partial<Record<CertificateField, string>> {
	const errors: Partial<Record<CertificateField, string>> = {};
	if (!validName(form.name)) errors.name = m.invalidName;
	else if (opts.creating && opts.existingNames.includes(form.name)) errors.name = m.nameTaken;
	if (!form.certPem.includes('-----BEGIN CERTIFICATE-----') || bytes(form.certPem) > MAX_PEM_BYTES)
		errors.certPem = m.invalidChain;
	if (
		form.purpose === 'server' &&
		(!/-----BEGIN [A-Z ]*PRIVATE KEY-----/.test(form.keyPem) || bytes(form.keyPem) > MAX_PEM_BYTES)
	)
		errors.keyPem = m.invalidKey;
	return errors;
}

/** Strict payload: a CA trust file never carries a key. */
export const toInput = (form: CertificateForm): CertificateInput => ({
	purpose: form.purpose,
	cert_pem: form.certPem.trim(),
	...(form.purpose === 'server' ? { key_pem: form.keyPem.trim() } : {}),
});

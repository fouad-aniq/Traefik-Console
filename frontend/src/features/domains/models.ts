import type { Resource } from '../../core/types';

export type DomainMethod = 'resolver' | 'files';

/** Mirrors core-v2 `models.DomainConfig` (response). `used_by` is computed from routers at read time. */
export interface DomainConfig {
	name: string;
	main: string;
	sans: string[] | null;
	method: DomainMethod | '';
	resolver?: string;
	certificate?: string;
	used_by?: string[] | null;
	entry_point?: string;
	state: string;
	options?: string;
	/** Computed by core-v2: routers using the domain and certificates serving it. Absent when unknown. */
	usage?: DomainUsage;
	/** Computed by core-v2: a validity or why there is none. */
	certificate_state?: CertificateState;
}

export interface DomainUsage {
	routers: { name: string; rule?: string; entry_points?: string[] | null; service?: string; tls: boolean; read_only: boolean }[];
	certificates: { source: 'uploaded' | 'acme'; id: string; name: string; names: string[]; issuer?: string; not_after?: string; validity: string; covers: boolean }[];
}
export type CertificateState = 'valid' | 'expiring' | 'expired' | 'not_yet_valid' | 'missingCertificate' | 'notCovered' | 'notObserved' | 'unavailable';
export type DomainResource = Resource<DomainConfig>;

/** Body of PUT /api/v1/domains/{name}; the name comes from the URL, state/used_by are server-owned. */
export interface DomainInput {
	main: string;
	sans: string[];
	method: DomainMethod;
	resolver?: string;
	certificate?: string;
	entry_point?: string;
}

/** POST /api/v1/domains/{name}/probe. */
export interface DomainProbe {
	state: 'unreachable' | 'observed' | 'matches' | 'mismatch' | string;
	trust: 'unknown' | 'trusted' | 'untrusted' | string;
	hostname: string;
	address: string;
	observed_at: string;
	issuer?: string;
	names?: string;
	not_before?: string;
	not_after?: string;
	algorithm?: string;
	validity?: string;
	fingerprint?: string;
	expected?: string;
	error?: string;
}

export interface DomainForm {
	/** Stable identifier used in the URL; derived from the hostname until the user edits it. */
	name: string;
	nameTouched: boolean;
	main: string;
	/** One name per line (commas also accepted). */
	sans: string;
	method: DomainMethod | '';
	reference: string;
}
export const newForm = (): DomainForm => ({ name: '', nameTouched: false, main: '', sans: '', method: '', reference: '' });

export const NAME_PATTERN = /^[a-z][a-z0-9_-]{0,62}$/;
export const validName = (name: string) => NAME_PATTERN.test(name);
export const MAX_SANS = 50;

/** Client-side twin of core-v2 `NormalizeDomain`: a lower-case ASCII DNS name, optional leading `*.`. */
export function normalizeDomain(raw: string): string | null {
	let value = raw.trim().replace(/\.$/, '');
	const wild = value.startsWith('*.');
	if (wild) value = value.slice(2);
	if (!value || /[\s/:@?#\\*]/.test(value)) return null;
	try { value = new URL('https://' + value).hostname.toLowerCase(); } catch { return null; }
	if (value.length > 253 || !value.includes('.') || /^[\d.]+$/.test(value) || value.split('.').some((l) => !l || l.length > 63 || !/^[a-z0-9](?:[a-z0-9-]*[a-z0-9])?$/.test(l))) return null;
	return (wild ? '*.' : '') + value;
}

/** Default identifier for a hostname; long names get a hash suffix so they stay unique. */
export function domainKey(value: string): string {
	const slug = 'domain-' + value.replace(/\*/g, 'wildcard').replace(/[^a-z0-9_-]/g, '-');
	if (slug.length <= 63) return slug;
	let hash = 2166136261;
	for (const ch of value) hash = Math.imul(hash ^ ch.charCodeAt(0), 16777619);
	return slug.slice(0, 54) + '-' + (hash >>> 0).toString(16).padStart(8, '0');
}

export const parseNames = (text: string) => text.split(/[\n,]+/).map((s) => s.trim()).filter(Boolean);
export const namesOf = (c: Pick<DomainConfig, 'main' | 'sans'>) => [c.main, ...(c.sans ?? [])];

/** Same rule as core-v2 `NamesCover`: a wildcard host needs the identical wildcard; otherwise one label. */
export function covers(certNames: string[], host: string): boolean {
	const h = host.toLowerCase();
	if (h.startsWith('*.')) return certNames.some((n) => n.toLowerCase() === h);
	return certNames.some((raw) => {
		const n = raw.toLowerCase();
		if (n === h) return true;
		return n.startsWith('*.') && h.split('.').length === n.split('.').length && h.endsWith(n.slice(1));
	});
}
export const coversAll = (certNames: string[], hosts: string[]) => hosts.every((h) => covers(certNames, h));

export const hasWildcard = (names: string[]) => names.some((n) => n.trim().startsWith('*.'));
export const routersOf = (d: DomainResource) => d.config.used_by ?? [];
export const usageCount = (d: DomainResource) => (d.config.usage ? d.config.usage.routers.length + d.config.usage.certificates.length : 0);
export const referenceOf = (c: DomainConfig) => (c.method === 'resolver' ? c.resolver : c.certificate) ?? '';

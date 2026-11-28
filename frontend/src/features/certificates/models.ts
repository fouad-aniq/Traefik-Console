import { domainItem, routeItem, type RelatedItem } from '../../shared/ui/related';
import type { Resource } from '../../core/types';

/** Imported (uploaded) certificate: `config` of GET /api/v1/certificates. */
export interface ImportedCertificate {
	name: string;
	source: string;
	cert_file: string;
	key_file: string;
	fingerprint: string;
	domains: string[] | null;
	issuer: string;
	not_before: string;
	not_after: string;
	algorithm: string;
	status: string;
	purpose: 'server' | 'ca';
	used_by?: CertificateUsage;
}
export type CertificateResource = Resource<ImportedCertificate>;

/** Public certificate parsed by core-v2 from Traefik's acme.json (read-only). */
export interface AcmeCertificate {
	resolver_name: string;
	main_domain: string;
	sans?: string[] | null;
	issuer: string;
	serial_number: string;
	fingerprint: string;
	not_before: string;
	expires_at: string;
	days_left: number;
	status: 'valid' | 'expiring_soon' | 'expired' | string;
	used_by?: CertificateUsage;
}
export interface AcmeObservation {
	resolver: string;
	state: string;
	invalid_entries: number;
	/** A router or domain requests certificates from this resolver (true when unknown). */
	in_use?: boolean;
}
export interface AcmeInventory {
	certificates: AcmeCertificate[] | null;
	observations: AcmeObservation[] | null;
	partial: boolean;
}

export type Validity = 'valid' | 'expiring' | 'expired' | 'not_yet_valid';

export interface CertificateForm {
	name: string;
	purpose: 'server' | 'ca';
	certPem: string;
	keyPem: string;
}
export const newForm = (): CertificateForm => ({ name: '', purpose: 'server', certPem: '', keyPem: '' });

/** Mirrors core-v2 `resolverNameRegex`. */
export const NAME_PATTERN = /^[a-z][a-z0-9_-]{0,62}$/;
export const validName = (name: string) => NAME_PATTERN.test(name);
/** The API accepts 4 MiB bodies; one PEM file is capped well below that. */
export const MAX_PEM_BYTES = 1024 * 1024;
export const bytes = (text: string) => new TextEncoder().encode(text).length;

export const validityOf = (status: string): Validity =>
	status === 'expiring_soon' || status === 'expiring'
		? 'expiring'
		: status === 'expired'
			? 'expired'
			: status === 'not_yet_valid'
				? 'not_yet_valid'
				: 'valid';

export const validityTone = (v: Validity) =>
	(({ valid: 'success', expiring: 'warning', expired: 'danger', not_yet_valid: 'warning' }) as const)[v];

/** Stable identity of an automatic certificate (the API has no name for them). */
export const acmeKey = (c: AcmeCertificate) =>
	c.fingerprint || `${c.resolver_name}:${c.main_domain}:${c.serial_number}`;

/** Names an automatic certificate covers: main domain first, then SANs without duplicates. */
export const acmeNames = (c: AcmeCertificate) => [...new Set([c.main_domain, ...(c.sans ?? [])].filter(Boolean))];

/** What references a certificate, computed by core-v2 (`used_by`). Absent when usage is unknown. */
export interface CertificateUsage {
	domains: { name: string; main: string; sans?: string[] | null; source?: string; read_only: boolean }[];
	routers: {
		name: string;
		rule?: string;
		entry_points?: string[] | null;
		service?: string;
		tls: boolean;
		read_only: boolean;
	}[];
}

/** The usage as "used by" list items: domains first, then routes. */
export const usageItems = (u: CertificateUsage | null | undefined): RelatedItem[] => [
	...(u?.domains ?? []).map((d) =>
		domainItem({
			name: d.name,
			read_only: d.read_only,
			config: { main: d.main, sans: d.sans, method: 'resolver', resolver: d.source },
		}),
	),
	...(u?.routers ?? []).map((r) =>
		routeItem({
			name: r.name,
			read_only: r.read_only,
			config: { rule: r.rule, entry_points: r.entry_points, service: r.service, tls: r.tls || undefined },
		}),
	),
];
export const usageCount = (u: CertificateUsage | null | undefined) =>
	(u?.domains.length ?? 0) + (u?.routers.length ?? 0);

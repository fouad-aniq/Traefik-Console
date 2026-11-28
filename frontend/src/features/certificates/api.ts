import { api } from '../../shared/api/api';
import { list } from '../../core/types';
import { encodeName, removeResource, saveResource, type WriteOptions } from '../../core/mutations';
import type { AcmeCertificate, AcmeInventory, AcmeObservation, CertificateResource } from './models';

export const listCertificates = async (signal?: AbortSignal) =>
	list(await api.get<CertificateResource[] | null>('/api/v1/certificates', { signal }));

export async function getInventory(
	signal?: AbortSignal,
): Promise<{ certificates: AcmeCertificate[]; observations: AcmeObservation[]; partial: boolean }> {
	const result = await api.get<AcmeInventory>('/api/v1/acme/certificates', { signal });
	return {
		certificates: list(result.certificates),
		observations: list(result.observations),
		partial: !!result.partial,
	};
}

export interface CertificateInput {
	purpose: 'server' | 'ca';
	cert_pem: string;
	/** Omitted for CA trust files; core-v2 never stores a key for them. */
	key_pem?: string;
}

export const putCertificate = (name: string, input: CertificateInput, version: number, opts?: WriteOptions) =>
	saveResource<CertificateResource>('/api/v1/certificates/' + encodeName(name), input, version, opts);

export const deleteCertificate = (name: string, version: number, opts?: WriteOptions) =>
	removeResource('/api/v1/certificates/' + encodeName(name), version, opts);

import { api } from '../../shared/api/api';
import { list } from '../../core/types';
import { encodeName, removeResource, saveResource, type WriteOptions } from '../../core/mutations';
import type { DomainInput, DomainProbe, DomainResource } from './models';

export const listDomains = async (signal?: AbortSignal) =>
	list(await api.get<DomainResource[] | null>('/api/v1/domains', { signal }));

export const putDomain = (name: string, input: DomainInput, version: number, opts?: WriteOptions) =>
	saveResource<DomainResource>('/api/v1/domains/' + encodeName(name), input, version, opts);

/** core-v2 refuses (409) to delete a domain a router references. */
export const deleteDomain = (name: string, version: number, opts?: WriteOptions) =>
	removeResource('/api/v1/domains/' + encodeName(name), version, opts);

/** Read-only TLS handshake against the registered entrypoint target. Not a mutation. */
export const probeDomain = (name: string, signal?: AbortSignal) =>
	api.post<DomainProbe>('/api/v1/domains/' + encodeName(name) + '/probe', {}, { signal, timeoutMs: 15_000 });

import { api } from '../../shared/api/api';
import { list } from '../../core/types';
import { encodeName, removeResource, saveResource, type WriteOptions } from '../../core/mutations';
import type { ServiceDiagnostic, ServiceInput, ServiceProtocol, ServiceResource } from './models';

export const listServices = async (signal?: AbortSignal) =>
	list(await api.get<ServiceResource[] | null>('/api/v1/services', { signal }));
export const putService = (name: string, input: ServiceInput, version: number, opts?: WriteOptions) =>
	saveResource<ServiceResource>('/api/v1/services/' + encodeName(name), input, version, opts);
/** core-v2 refuses (409) to delete a service a router references, and external services are read-only. */
export const deleteService = (name: string, version: number, opts?: WriteOptions) =>
	removeResource('/api/v1/services/' + encodeName(name), version, opts);
/** Bounded read of Traefik's own runtime view; no backend URL is probed or returned. */
export const diagnoseService = (name: string, protocol: ServiceProtocol = 'http', signal?: AbortSignal) =>
	api.get<ServiceDiagnostic>(
		'/api/v1/services/' + encodeName(name) + '/diagnostics' + (protocol === 'http' ? '' : '?protocol=' + protocol),
		{ signal },
	);

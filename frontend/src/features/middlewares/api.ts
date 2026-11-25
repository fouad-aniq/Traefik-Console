import { api } from '../../shared/api/api';
import { list } from '../../core/types';
import { encodeName, removeResource, saveResource, type WriteOptions } from '../../core/mutations';
import type { ChainInput, ChainResource, MiddlewareInput, MiddlewareResource } from './models';

export const listMiddlewares = async (signal?: AbortSignal) =>
	list(await api.get<MiddlewareResource[] | null>('/api/v1/middlewares', { signal }));
export const putMiddleware = (name: string, input: MiddlewareInput, version: number, opts?: WriteOptions) =>
	saveResource<MiddlewareResource>('/api/v1/middlewares/' + encodeName(name), input, version, opts);
/** core-v2 answers 409 when a router or chain still references the middleware. */
export const deleteMiddleware = (name: string, version: number, opts?: WriteOptions) =>
	removeResource('/api/v1/middlewares/' + encodeName(name), version, opts);

export const listChains = async (signal?: AbortSignal) =>
	list(await api.get<ChainResource[] | null>('/api/v1/chains', { signal }));
export const putChain = (name: string, input: ChainInput, version: number, opts?: WriteOptions) =>
	saveResource<ChainResource>('/api/v1/chains/' + encodeName(name), input, version, opts);
export const deleteChain = (name: string, version: number, opts?: WriteOptions) =>
	removeResource('/api/v1/chains/' + encodeName(name), version, opts);

/** Services back the errors.service reference picker. */
export interface ServiceRef { name: string; read_only: boolean; config: { type?: string; protocol?: string } }
/** Only HTTP services can serve an errors middleware. */
export const listServices = async (signal?: AbortSignal) =>
	list(await api.get<ServiceRef[] | null>('/api/v1/services', { signal })).filter((s) => (s.config?.protocol ?? 'http') === 'http');

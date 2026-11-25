import { api } from '../../shared/api/api';
import { list } from '../../core/types';
import { encodeName, removeResource, saveResource, type WriteOptions } from '../../core/mutations';
import type { RouterInput, RouterResource } from './models';

export const listRouters = async (signal?: AbortSignal) =>
	list(await api.get<RouterResource[] | null>('/api/v1/routers', { signal }));
export const putRouter = (name: string, input: RouterInput, version: number, opts?: WriteOptions) =>
	saveResource<RouterResource>('/api/v1/routers/' + encodeName(name), input, version, opts);
/** Routers are leaves of the graph: nothing references them, so deletion is never refused for usage. */
export const deleteRouter = (name: string, version: number, opts?: WriteOptions) =>
	removeResource('/api/v1/routers/' + encodeName(name), version, opts);

/** Registered static entrypoint names (core-v2 requires them to be of the router's own kind when it validates). */
export async function listEntryPoints(signal?: AbortSignal): Promise<string[]> {
	const res = await api.get<{ entry_points?: string[] | null }>('/api/v1/entrypoints', { signal });
	return list(res.entry_points);
}

/** Entrypoints with the kind of traffic they carry: UDP routers can only use udp ones, the others tcp ones. */
export interface EntryPointInfo { name: string; protocol: 'tcp' | 'udp' }
export async function listEntryPointInfo(signal?: AbortSignal): Promise<EntryPointInfo[]> {
	const res = await api.get<{ entry_points?: string[] | null; protocols?: Record<string, string> | null }>('/api/v1/entrypoints', { signal });
	return list(res.entry_points).map((name) => ({ name, protocol: res.protocols?.[name] === 'udp' ? 'udp' : 'tcp' }));
}

/** Minimal shapes for the reference pickers; the full envelopes are not needed here. */
export interface NamedRef { name: string; status: string; origin: 'managed' | 'external'; provider: string; config: Record<string, any> }
const refs = async (path: string, signal?: AbortSignal) => list(await api.get<NamedRef[] | null>(path, { signal }));
/** Every service with its protocol: a router can only use those of its own (HTTP, TCP and UDP live in separate Traefik sections). */
export const listServices = (signal?: AbortSignal) => refs('/api/v1/services', signal);
export const listMiddlewares = (signal?: AbortSignal) => refs('/api/v1/middlewares', signal);
export const listChains = (signal?: AbortSignal) => refs('/api/v1/chains', signal);
export const listResolvers = (signal?: AbortSignal) => refs('/api/v1/resolvers', signal);
export const listDomains = (signal?: AbortSignal) => refs('/api/v1/domains', signal);

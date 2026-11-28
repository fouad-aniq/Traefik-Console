import { api } from '../../shared/api/api';
import { list } from '../../core/types';
import { encodeName, postWrite, removeResource, saveResource, type WriteOptions } from '../../core/mutations';
import type { CatalogPage, CatalogPlugin, InstallYamlInput, PluginConfig, PluginResource } from './models';

export const listPlugins = async (signal?: AbortSignal) =>
	list(await api.get<PluginResource[] | null>('/api/v1/plugins', { signal }));

export const putPlugin = (alias: string, input: PluginConfig, version: number, opts?: WriteOptions) =>
	saveResource<PluginResource>('/api/v1/plugins/' + encodeName(alias), input, version, opts);

export const installPluginYaml = (input: InstallYamlInput, opts?: WriteOptions) =>
	postWrite<PluginResource>('/api/v1/plugins/yaml', input, opts);

export const deletePlugin = (alias: string, version: number, opts?: WriteOptions) =>
	removeResource('/api/v1/plugins/' + encodeName(alias), version, opts);

/** Cached catalog; an empty cache is normal until the first sync. */
export const searchCatalog = async (query = '', signal?: AbortSignal) =>
	list(
		await api.get<CatalogPlugin[] | null>(
			'/api/v1/plugins/catalog' + (query.trim() ? '?q=' + encodeURIComponent(query.trim()) : ''),
			{ signal },
		),
	);

/** One server-side page of the cached catalog; the server also reports each entry's install state. */
export const readCatalogPage = (
	options: { q?: string; module?: string; limit: number; offset: number; order?: 'asc' | 'desc'; available?: boolean },
	signal?: AbortSignal,
) => {
	const params = new URLSearchParams();
	if (options.module) params.set('module', options.module);
	else if (options.q?.trim()) params.set('q', options.q.trim());
	if (options.available) params.set('availability', 'available');
	params.set('limit', String(Math.min(100, Math.max(1, options.limit))));
	params.set('offset', String(options.offset));
	params.set('order', options.order ?? 'asc');
	return api.get<CatalogPage>('/api/v1/plugins/catalog/page?' + params.toString(), { signal });
};

/** Updates a managed plugin to a cataloged version of a managed plugin; the target is chosen by the server. */
export const updatePlugin = (alias: string, version: string, current: number, opts?: WriteOptions) =>
	postWrite<PluginResource>('/api/v1/plugins/' + encodeName(alias) + '/update', { version }, opts, current);

export const syncCatalog = (signal?: AbortSignal) =>
	api.post<{ synced_count: number }>('/api/v1/plugins/catalog/sync', undefined, { signal, timeoutMs: 30_000 });

export interface MiddlewareRef {
	name: string;
	status: string;
	read_only?: boolean;
	origin?: string;
	config: { type?: string; plugin_name?: string };
}
export const listMiddlewareRefs = async (signal?: AbortSignal) =>
	list(await api.get<MiddlewareRef[] | null>('/api/v1/middlewares', { signal }));

export const installFromCatalog = (alias: string, input: PluginConfig, opts?: WriteOptions) =>
	postWrite<PluginResource>('/api/v1/plugins/catalog/install', { alias, ...input }, opts);

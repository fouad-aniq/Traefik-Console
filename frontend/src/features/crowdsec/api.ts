import { api } from '../../shared/api/api';
import { list } from '../../core/types';
import { saveResource, type WriteOptions } from '../../core/mutations';
import type { ConnectionSettings, ConnectionTest, CrowdSecAlert, CrowdSecAlertPage, CrowdSecAllowlist, CrowdSecConfig, CrowdSecDecision, CrowdSecHubItem, CrowdSecMetricsSnapshot, CrowdSecStats, CrowdSecStatus, StatsWindow } from './models';
import { decisionIDPath } from './models';

export const getStatus = (signal?: AbortSignal) => api.get<CrowdSecStatus>('/api/v1/crowdsec/status', { signal, timeoutMs: 20_000 });

export const getStats = (window: StatsWindow, signal?: AbortSignal) =>
	api.get<CrowdSecStats>('/api/v1/crowdsec/stats?window=' + window, { signal, timeoutMs: 30_000 });

/** Saves the whole integration (plugin, middleware, key, router attachments) in one operation. */
export const putConfig = (input: Partial<CrowdSecConfig>, version: number, opts?: WriteOptions) =>
	saveResource<CrowdSecConfig>('/api/v1/crowdsec/config', input, version, opts);

/** Current configuration; `version` is the If-Match value for the next save (0 = never saved). */
export const getConfig = (signal?: AbortSignal) => api.get<CrowdSecConfig>('/api/v1/crowdsec/config', { signal });

export const listDecisions = async (signal?: AbortSignal) =>
	list(await api.get<CrowdSecDecision[] | null>('/api/v1/crowdsec/decisions', { signal, timeoutMs: 20_000 }));

export const addDecision = (input: { ip: string; duration: string; reason: string }, signal?: AbortSignal) =>
	api.post<{ added: boolean }>('/api/v1/crowdsec/decisions', input, { signal });

export const deleteDecision = (id: number, signal?: AbortSignal) => api.delete<{ deleted: boolean }>(decisionIDPath(id), { signal });

export const listAlerts = (input: { page: number; limit: number; sinceHours: number; scenario?: string; source?: string; origin?: string }, signal?: AbortSignal) => {
	const query = new URLSearchParams({ page: String(input.page), limit: String(input.limit), since_hours: String(input.sinceHours) });
	if (input.scenario) query.set('scenario', input.scenario);
	if (input.source) query.set('source', input.source);
	if (input.origin) query.set('origin', input.origin);
	return api.get<CrowdSecAlertPage>('/api/v1/crowdsec/alerts?' + query, { signal, timeoutMs: 30_000 });
};

export const getAlert = (id: number, signal?: AbortSignal) => api.get<CrowdSecAlert>(`/api/v1/crowdsec/alerts/${id}`, { signal, timeoutMs: 20_000 });
export const listAllowlists = (signal?: AbortSignal) => api.get<CrowdSecAllowlist[] | null>('/api/v1/crowdsec/allowlists', { signal, timeoutMs: 30_000 });
export const createAllowlist = (input: { name: string; description: string }, signal?: AbortSignal) => api.post<{ created: boolean }>('/api/v1/crowdsec/allowlists', input, { signal });
export const addAllowlistEntry = (name: string, input: { value: string; description: string; expiration: string }, signal?: AbortSignal) => api.post<{ added: boolean }>(`/api/v1/crowdsec/allowlists/${encodeURIComponent(name)}/entries`, input, { signal });
export const removeAllowlistEntry = (name: string, value: string, signal?: AbortSignal) => api.delete<{ removed: boolean }>(`/api/v1/crowdsec/allowlists/${encodeURIComponent(name)}/entries`, { body: { value }, signal });
export const deleteAllowlist = (name: string, signal?: AbortSignal) => api.delete<{ deleted: boolean }>(`/api/v1/crowdsec/allowlists/${encodeURIComponent(name)}`, { signal });
export const checkAllowlist = (value: string, signal?: AbortSignal) => api.post<{ allowlisted: boolean; reason: string }>('/api/v1/crowdsec/allowlists/check', { value }, { signal });
export const getMetrics = (signal?: AbortSignal) => api.get<CrowdSecMetricsSnapshot>('/api/v1/crowdsec/metrics', { signal, timeoutMs: 30_000 });
export const listHubItems = (signal?: AbortSignal) => api.get<CrowdSecHubItem[] | null>('/api/v1/crowdsec/hub', { signal, timeoutMs: 30_000 });

/** Saves how the Console reaches CrowdSec; the protection (plugin, middleware, routers) is kept as is. */
export const putConnection = (input: ConnectionSettings, version: number, opts?: WriteOptions) =>
	saveResource<CrowdSecConfig>('/api/v1/crowdsec/connection', input, version, opts);

/** Probes candidate connection settings without saving them. */
export const testConnection = (input: ConnectionSettings, signal?: AbortSignal) =>
	api.post<ConnectionTest>('/api/v1/crowdsec/connection/test', input, { signal, timeoutMs: 45_000 });

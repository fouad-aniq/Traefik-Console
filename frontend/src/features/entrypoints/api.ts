import { api } from '../../shared/api/api';
import { list } from '../../core/types';
import { encodeName, removeResource, saveResource, type WriteOptions } from '../../core/mutations';
import { isWeb, kindOf, type EntryPointConfig, type EntryPointForm, type EntryPointResource, type HTTPConfig, type Kind, type TrustMode } from './models';

interface EntryPointList { entry_points: string[] | null; protocols?: Record<string, string>; items?: EntryPointResource[] | null }

/** Every entrypoint: console-managed ones and, read-only, the ones only declared in traefik.yml. */
export const listEntryPointResources = async (signal?: AbortSignal) =>
	list((await api.get<EntryPointList>('/api/v1/entrypoints', { signal })).items);

/** One read for the map: the names and protocols every picker uses, plus the full resources. */
export async function listEntryPointInventory(signal?: AbortSignal): Promise<{ names: string[]; protocols: Record<string, string>; items: EntryPointResource[] }> {
	const res = await api.get<EntryPointList>('/api/v1/entrypoints', { signal });
	const items = list(res.items);
	return { names: [...new Set([...list(res.entry_points), ...items.map((e) => e.name)])], protocols: res.protocols ?? {}, items };
}

export const getEntryPoint = (name: string, signal?: AbortSignal) =>
	api.get<EntryPointResource>('/api/v1/entrypoints/' + encodeName(name), { signal });

export const putEntryPoint = (name: string, input: EntryPointConfig, version: number, opts?: WriteOptions) =>
	saveResource<EntryPointResource>('/api/v1/entrypoints/' + encodeName(name), input, version, opts);

export const deleteEntryPoint = (name: string, version: number, opts?: WriteOptions) =>
	removeResource('/api/v1/entrypoints/' + encodeName(name), version, opts);

export function newForm(kind: Kind = 'http'): EntryPointForm {
	return {
		name: '', kind, protocol: kind === 'udp' ? 'udp' : 'tcp', port: '', host: '', asDefault: false, reusePort: false, udpTimeout: '',
		redirect: false, redirectTarget: '', redirectPort: '', redirectScheme: 'https', redirectTemporary: false, redirectPriority: '', allowAcmeBypass: false,
		certResolver: '', tlsOptions: '', tlsDomains: [], middlewares: [], http3: false, http3Port: '',
		forwardedMode: 'none', forwardedIPs: [], connectionHeaders: [], notAppendXForwardedFor: false, proxyMode: 'none', proxyIPs: [],
		readTimeout: '', writeTimeout: '', idleTimeout: '', graceTimeout: '', requestAcceptGraceTimeout: '', keepAliveMaxTime: '', keepAliveMaxRequests: '',
		maxHeaderBytes: '', deniedCharacters: [], encodeQuerySemicolons: false, disableSanitizePath: false, aliasHeadersStrategy: 'keep',
		maxConcurrentStreams: '', maxDecoderHeaderTableSize: '', maxEncoderHeaderTableSize: '',
		accessLogs: true, metrics: true, tracing: true, traceVerbosity: 'minimal'
	};
}

/** The usual name and port of each type, offered while they are still free. */
const SUGGESTED: Record<Kind, { name: string; port: string }> = {
	http: { name: 'web', port: '80' }, https: { name: 'websecure', port: '443' }, tcp: { name: '', port: '' }, udp: { name: '', port: '' }
};
const suggestedNames = Object.values(SUGGESTED).map((s) => s.name).filter(Boolean);
const suggestedPorts = Object.values(SUGGESTED).map((s) => s.port).filter(Boolean);

/**
 * Change the type of the entrypoint in the form. The protocol follows the type. While creating,
 * the usual name, port and companions of the type are filled in where the user has not typed
 * something of their own: "web" redirects to the secure entrypoint when there is one, and the
 * first secure entrypoint becomes the default. Options the new type does not have stay in the
 * form untouched; `toInput` never sends them.
 */
export function withKind(form: EntryPointForm, kind: Kind, existing: EntryPointResource[], creating: boolean): EntryPointForm {
	const next: EntryPointForm = { ...form, kind, protocol: kind === 'udp' ? 'udp' : 'tcp' };
	if (!creating) return next;
	const protocol = next.protocol;
	const nameFree = (name: string) => !!name && !existing.some((e) => e.name === name);
	const portFree = (port: string) => !!port && !existing.some((e) => String(e.config.port) === port && (e.config.protocol ?? 'tcp') === protocol);
	if (!form.name || suggestedNames.includes(form.name)) next.name = nameFree(SUGGESTED[kind].name) ? SUGGESTED[kind].name : '';
	if (!form.port || suggestedPorts.includes(form.port)) next.port = portFree(SUGGESTED[kind].port) ? SUGGESTED[kind].port : '';
	const secure = existing.find((e) => kindOf(e.config) === 'https' || ((e.config.protocol ?? 'tcp') === 'tcp' && e.config.port === 443));
	const autoRedirect = form.redirect && !!secure && form.redirectTarget === secure.name && !form.redirectPort;
	if (kind === 'http' && secure && !form.redirect) {
		next.redirect = true;
		next.redirectTarget = secure.name;
	} else if (kind !== 'http' && autoRedirect) {
		next.redirect = false;
		next.redirectTarget = '';
	}
	if (kind === 'https' && form.kind !== 'https') next.asDefault = !existing.some((e) => e.config.as_default);
	else if (form.kind === 'https') next.asDefault = false;
	return next;
}

const trustMode = (c: { insecure?: boolean; trusted_ips?: string[] } | undefined): TrustMode =>
	c?.insecure ? 'all' : c?.trusted_ips?.length ? 'list' : 'none';
const text = (n: number | undefined) => (n ? String(n) : '');

export function formFrom(resource: EntryPointResource): EntryPointForm {
	const c = resource.config;
	const form = newForm();
	form.name = resource.name;
	form.kind = kindOf(c);
	form.protocol = c.protocol === 'udp' ? 'udp' : 'tcp';
	form.port = c.port ? String(c.port) : '';
	form.host = c.host ?? '';
	form.asDefault = !!c.as_default;
	form.reusePort = !!c.reuse_port;
	form.udpTimeout = c.udp?.timeout ?? '';
	form.allowAcmeBypass = !!c.allow_acme_bypass;

	const http = c.http;
	if (http?.redirect) {
		form.redirect = true;
		const port = /^:(\d+)$/.exec(http.redirect.to);
		if (port) form.redirectPort = port[1];
		else form.redirectTarget = http.redirect.to;
		form.redirectScheme = http.redirect.scheme === 'http' ? 'http' : 'https';
		form.redirectTemporary = !!http.redirect.temporary;
		form.redirectPriority = text(http.redirect.priority);
	}
	if (http?.tls) {
		form.certResolver = http.tls.cert_resolver ?? '';
		form.tlsOptions = http.tls.options ?? '';
		form.tlsDomains = (http.tls.domains ?? []).map((d) => ({ main: d.main, sans: [...(d.sans ?? [])] }));
	}
	form.middlewares = [...(http?.middlewares ?? [])];
	form.maxHeaderBytes = text(http?.max_header_bytes);
	form.deniedCharacters = [...(http?.denied_encoded_characters ?? [])];
	form.encodeQuerySemicolons = !!http?.encode_query_semicolons;
	form.disableSanitizePath = !!http?.disable_sanitize_path;
	form.aliasHeadersStrategy = http?.alias_headers_strategy ?? 'keep';
	form.http3 = !!c.http3;
	form.http3Port = text(c.http3?.advertised_port);

	form.forwardedMode = trustMode(c.forwarded_headers);
	form.forwardedIPs = [...(c.forwarded_headers?.trusted_ips ?? [])];
	form.connectionHeaders = [...(c.forwarded_headers?.connection ?? [])];
	form.notAppendXForwardedFor = !!c.forwarded_headers?.not_append_x_forwarded_for;
	form.proxyMode = trustMode(c.proxy_protocol);
	form.proxyIPs = [...(c.proxy_protocol?.trusted_ips ?? [])];

	const tr = c.transport;
	form.readTimeout = tr?.read_timeout ?? '';
	form.writeTimeout = tr?.write_timeout ?? '';
	form.idleTimeout = tr?.idle_timeout ?? '';
	form.graceTimeout = tr?.grace_timeout ?? '';
	form.requestAcceptGraceTimeout = tr?.request_accept_grace_timeout ?? '';
	form.keepAliveMaxTime = tr?.keep_alive_max_time ?? '';
	form.keepAliveMaxRequests = text(tr?.keep_alive_max_requests);
	form.maxConcurrentStreams = text(c.http2?.max_concurrent_streams);
	form.maxDecoderHeaderTableSize = text(c.http2?.max_decoder_header_table_size);
	form.maxEncoderHeaderTableSize = text(c.http2?.max_encoder_header_table_size);
	form.accessLogs = !c.observability?.disable_access_logs;
	form.metrics = !c.observability?.disable_metrics;
	form.tracing = !c.observability?.disable_tracing;
	form.traceVerbosity = c.observability?.trace_verbosity === 'detailed' ? 'detailed' : 'minimal';
	return form;
}

/** Drops empty objects so "nothing set" is simply absent (the API rejects nulls). */
function compact<T extends object>(value: T): T | undefined {
	return Object.keys(value).length ? value : undefined;
}

/**
 * Build the strict request body: only what applies to the type and only what differs from
 * Traefik's defaults. Whatever a hidden option held is dropped, never sent.
 */
export function toInput(form: EntryPointForm, adopt = false): EntryPointConfig {
	const input: EntryPointConfig = { kind: form.kind, protocol: form.protocol, port: Number(form.port) };
	const web = isWeb(form.kind);
	if (form.host.trim()) input.host = form.host.trim();
	if (form.reusePort) input.reuse_port = true;
	if (adopt) input.adopted = true;
	if (form.protocol === 'udp') {
		if (form.udpTimeout.trim()) input.udp = { timeout: form.udpTimeout.trim() };
		return input;
	}
	if (form.asDefault) input.as_default = true;

	// HTTP behaviour belongs to the web types; a plain TCP service has none of it.
	const http: HTTPConfig = {};
	if (web && form.redirect) {
		http.redirect = { to: form.redirectTarget || ':' + form.redirectPort.trim() };
		if (form.redirectScheme === 'http') http.redirect.scheme = 'http';
		if (form.redirectTemporary) http.redirect.temporary = true;
		if (form.redirectPriority.trim()) http.redirect.priority = Number(form.redirectPriority);
	}
	if (web && form.allowAcmeBypass) input.allow_acme_bypass = true;
	if (form.kind === 'https') {
		http.tls = {};
		if (form.certResolver) http.tls.cert_resolver = form.certResolver;
		if (form.tlsOptions.trim()) http.tls.options = form.tlsOptions.trim();
		const domains = form.tlsDomains.filter((d) => d.main.trim()).map((d) => ({ main: d.main.trim(), ...(d.sans.length && { sans: [...d.sans] }) }));
		if (form.certResolver && domains.length) http.tls.domains = domains;
	}
	if (web) {
		if (form.middlewares.length) http.middlewares = [...form.middlewares];
		if (form.deniedCharacters.length) http.denied_encoded_characters = [...form.deniedCharacters];
		if (form.encodeQuerySemicolons) http.encode_query_semicolons = true;
		if (form.disableSanitizePath) http.disable_sanitize_path = true;
		if (form.maxHeaderBytes.trim()) http.max_header_bytes = Number(form.maxHeaderBytes);
		if (form.aliasHeadersStrategy !== 'keep') http.alias_headers_strategy = form.aliasHeadersStrategy;
		if (form.http3) input.http3 = form.http3Port.trim() ? { advertised_port: Number(form.http3Port) } : {};
	}
	if (compact(http)) input.http = http;

	const forwarded = !web ? {} : {
		...(form.forwardedMode === 'all' && { insecure: true }),
		...(form.forwardedMode === 'list' && form.forwardedIPs.length && { trusted_ips: [...form.forwardedIPs] }),
		...(form.connectionHeaders.length && { connection: [...form.connectionHeaders] }),
		...(form.notAppendXForwardedFor && { not_append_x_forwarded_for: true })
	};
	if (compact(forwarded)) input.forwarded_headers = forwarded;
	if (form.proxyMode === 'all') input.proxy_protocol = { insecure: true };
	else if (form.proxyMode === 'list' && form.proxyIPs.length) input.proxy_protocol = { trusted_ips: [...form.proxyIPs] };

	const duration = (value: string) => (value.trim() ? value.trim() : undefined);
	const transport = Object.fromEntries(Object.entries({
		read_timeout: duration(form.readTimeout), write_timeout: duration(form.writeTimeout), idle_timeout: duration(form.idleTimeout),
		grace_timeout: duration(form.graceTimeout), request_accept_grace_timeout: duration(form.requestAcceptGraceTimeout),
		keep_alive_max_time: duration(form.keepAliveMaxTime),
		keep_alive_max_requests: form.keepAliveMaxRequests.trim() ? Number(form.keepAliveMaxRequests) : undefined
	}).filter(([, v]) => v !== undefined && v !== 0));
	if (compact(transport)) input.transport = transport;

	const http2 = !web ? {} : Object.fromEntries(Object.entries({
		max_concurrent_streams: Number(form.maxConcurrentStreams) || 0,
		max_decoder_header_table_size: Number(form.maxDecoderHeaderTableSize) || 0,
		max_encoder_header_table_size: Number(form.maxEncoderHeaderTableSize) || 0
	}).filter(([, v]) => v > 0));
	if (compact(http2)) input.http2 = http2;

	const observability = {
		...(!form.accessLogs && { disable_access_logs: true }),
		...(!form.metrics && { disable_metrics: true }),
		...(!form.tracing && { disable_tracing: true }),
		...(form.tracing && form.traceVerbosity === 'detailed' && { trace_verbosity: 'detailed' as const })
	};
	if (compact(observability)) input.observability = observability;
	return input;
}

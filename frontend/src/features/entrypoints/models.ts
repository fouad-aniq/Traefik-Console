import type { Resource } from '../../core/types';

export type Protocol = 'tcp' | 'udp';
/**
 * What an entrypoint is for. It decides which options apply and is validated against them
 * (core-v2 `kind`): web without TLS, web with TLS for every router, a plain TCP service, or UDP.
 */
export type Kind = 'http' | 'https' | 'tcp' | 'udp';
export const KINDS: Kind[] = ['http', 'https', 'tcp', 'udp'];
/** The protocols a type may listen on: only a UDP entrypoint listens on UDP. */
export const protocolsOf = (kind: Kind): Protocol[] => (kind === 'udp' ? ['udp'] : ['tcp']);
/** Web types have HTTP behaviour (redirect, middlewares, HTTP/3, forwarded headers, request limits). */
export const isWeb = (kind: Kind) => kind === 'http' || kind === 'https';
export type EncodedCharacter = 'slash' | 'backSlash' | 'nullCharacter' | 'semicolon' | 'percent' | 'questionMark' | 'hash';
export const ENCODED_CHARACTERS: EncodedCharacter[] = ['slash', 'backSlash', 'nullCharacter', 'semicolon', 'percent', 'questionMark', 'hash'];

export interface TrustConfig { insecure?: boolean; trusted_ips?: string[] }
export interface ForwardedHeadersConfig extends TrustConfig { connection?: string[]; not_append_x_forwarded_for?: boolean }
export interface TransportConfig {
	read_timeout?: string;
	write_timeout?: string;
	idle_timeout?: string;
	grace_timeout?: string;
	request_accept_grace_timeout?: string;
	keep_alive_max_time?: string;
	keep_alive_max_requests?: number;
}
export interface RedirectConfig { to: string; scheme?: 'https' | 'http'; temporary?: boolean; priority?: number }
export interface TLSDomainConfig { main: string; sans?: string[] }
export interface TLSConfig { options?: string; cert_resolver?: string; domains?: TLSDomainConfig[] }
export interface HTTPConfig {
	redirect?: RedirectConfig;
	middlewares?: string[];
	tls?: TLSConfig;
	denied_encoded_characters?: EncodedCharacter[];
	encode_query_semicolons?: boolean;
	disable_sanitize_path?: boolean;
	max_header_bytes?: number;
	alias_headers_strategy?: 'keep' | 'delete' | 'reject';
}
export interface HTTP2Config { max_concurrent_streams?: number; max_decoder_header_table_size?: number; max_encoder_header_table_size?: number }
export interface ObservabilityConfig { disable_access_logs?: boolean; disable_metrics?: boolean; disable_tracing?: boolean; trace_verbosity?: 'minimal' | 'detailed' }
export interface EntryPointUsage { routers?: string[]; resolvers?: string[]; entry_points?: string[] }

/** Mirrors core-v2 `models.EntryPointConfig`. Request and response share the editable fields. */
export interface EntryPointConfig {
	name?: string;
	kind?: Kind;
	protocol?: Protocol;
	host?: string;
	port: number;
	as_default?: boolean;
	reuse_port?: boolean;
	allow_acme_bypass?: boolean;
	forwarded_headers?: ForwardedHeadersConfig;
	proxy_protocol?: TrustConfig;
	transport?: TransportConfig;
	http?: HTTPConfig;
	http2?: HTTP2Config;
	http3?: { advertised_port?: number };
	udp?: { timeout?: string };
	observability?: ObservabilityConfig;
	/** true takes over an entrypoint declared in traefik.yml. */
	adopted?: boolean;
	// Response only.
	address?: string;
	adoptable?: boolean;
	adopt_blocker?: string;
	usage?: EntryPointUsage;
	warnings?: string[];
	rendered_node?: Record<string, unknown>;
}

export type EntryPointResource = Resource<EntryPointConfig>;

/** How client addresses are trusted: nobody (Traefik default), a list of proxies, or everyone. */
export type TrustMode = 'none' | 'list' | 'all';

/** Editable form state. Text inputs stay strings; `toInput` converts and drops what does not apply. */
export interface EntryPointForm {
	name: string;
	kind: Kind;
	protocol: Protocol;
	port: string;
	host: string;
	asDefault: boolean;
	reusePort: boolean;
	udpTimeout: string;

	redirect: boolean;
	redirectTarget: string; // entrypoint name, or '' when a port is used
	redirectPort: string;
	redirectScheme: 'https' | 'http';
	redirectTemporary: boolean;
	redirectPriority: string;
	allowAcmeBypass: boolean;

	certResolver: string;
	tlsOptions: string;
	tlsDomains: { main: string; sans: string[] }[];
	middlewares: string[];
	http3: boolean;
	http3Port: string;

	forwardedMode: TrustMode;
	forwardedIPs: string[];
	connectionHeaders: string[];
	notAppendXForwardedFor: boolean;
	proxyMode: TrustMode;
	proxyIPs: string[];

	readTimeout: string;
	writeTimeout: string;
	idleTimeout: string;
	graceTimeout: string;
	requestAcceptGraceTimeout: string;
	keepAliveMaxTime: string;
	keepAliveMaxRequests: string;
	maxHeaderBytes: string;
	deniedCharacters: EncodedCharacter[];
	encodeQuerySemicolons: boolean;
	disableSanitizePath: boolean;
	aliasHeadersStrategy: 'keep' | 'delete' | 'reject';
	maxConcurrentStreams: string;
	maxDecoderHeaderTableSize: string;
	maxEncoderHeaderTableSize: string;
	accessLogs: boolean;
	metrics: boolean;
	tracing: boolean;
	traceVerbosity: 'minimal' | 'detailed';
}

export const usageCount = (u: EntryPointUsage | undefined) =>
	(u?.routers?.length ?? 0) + (u?.resolvers?.length ?? 0) + (u?.entry_points?.length ?? 0);

/** The type of an entrypoint, worked out from its settings when the server did not state one. */
export const kindOf = (c: EntryPointConfig): Kind =>
	c.kind ?? (c.protocol === 'udp' ? 'udp' : c.http?.tls ? 'https' : 'http');

/** `host:port` without the `/udp` suffix: the protocol is shown next to it. */
export function displayAddress(c: EntryPointConfig): string {
	if (!c.port) return (c.address ?? '').replace(/\/(tcp|udp)$/i, '');
	const host = c.host ?? '';
	return (host.includes(':') ? `[${host}]` : host) + ':' + c.port;
}

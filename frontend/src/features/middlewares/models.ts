/**
 * Middleware and chain data contracts for core-v2.
 *
 * - GET /api/v1/middlewares, /api/v1/chains → Resource<MiddlewareConfig | ChainConfig>[]
 * - PUT /api/v1/middlewares/{name}  body {type, plugin_name?, spec}
 * - PUT /api/v1/chains/{name}       body {middlewares: string[]}
 * The native field inventory (schema.json: HTTP, and the two TCP types) is a copy of core-v2's embedded
 * middleware_schema.json (Traefik v3.7.14); core-v2 exposes no schema route.
 */
import schemaJson from './schema.json';
import type { Resource } from '../../core/types';

export type Protocol = 'http' | 'tcp';

export interface SchemaField { type: string; fields: Record<string, SchemaField> }
export type MiddlewareSchema = Record<Protocol, Record<string, Record<string, SchemaField>>>;
export const nativeSchema = schemaJson as unknown as Record<Protocol, Record<string, Record<string, SchemaField>>>;

export interface MiddlewareConfig {
	/** http when absent. TCP middlewares live in their own Traefik section and serve TCP routers only. */
	protocol?: Protocol;
	type: string;
	plugin_name?: string;
	spec: Record<string, unknown> | null;
	secret_refs?: Record<string, string>;
	/** Computed by core-v2 (never sent back): routers and chains referencing this middleware. Absent when unknown. */
	used_by?: MiddlewareUsage;
}
export interface ChainConfig { middlewares: string[] | null; used_by?: MiddlewareUsage }
export interface MiddlewareUsage {
	routers: { name: string; rule?: string; entry_points?: string[] | null; service?: string; tls: boolean; read_only: boolean }[];
	chains: { name: string; members: string[]; read_only: boolean }[];
}
export const usageCount = (u: MiddlewareUsage | undefined) => (u ? u.routers.length + u.chains.length : 0);
export type MiddlewareResource = Resource<MiddlewareConfig>;
export type ChainResource = Resource<ChainConfig>;

/**
 * Type of an external (Traefik-discovered) middleware. core-v2 reports it as `external` with Traefik's
 * runtime object as the spec, which holds the configuration under its type key (e.g. `redirectRegex`).
 */
export function externalType(spec: Record<string, unknown> | null | undefined): string {
	const keys = Object.keys(spec ?? {});
	if (keys.includes('plugin')) return 'plugin';
	return keys.find((k) => (ALLOWED_TYPES as readonly string[]).includes(k) || (TCP_TYPES as readonly string[]).includes(k)) ?? 'external';
}

/** One Basic Auth user as entered; core-v2 hashes the password (bcrypt) and never stores it. */
export interface Credential { username: string; password: string }
export const emptyCredential = (): Credential => ({ username: '', password: '' });

export interface MiddlewareInput { protocol?: 'tcp'; type: string; plugin_name?: string; spec: Record<string, unknown> }
export interface ChainInput { middlewares: string[] }

/** Backend name rule (resolverNameRegex) shared by middlewares and chains. */
export const NAME_PATTERN = /^[a-z][a-z0-9_-]{0,62}$/;
export const REFERENCE_PATTERN = /^[a-z][a-z0-9_-]{0,62}(@file)?$/;
export const isValidMiddlewareName = (name: string) => NAME_PATTERN.test(name);
export const isValidReference = (reference: string) => REFERENCE_PATTERN.test(reference);
export const referenceBaseName = (reference: string) => reference.replace(/@file$/, '');
export const MAX_CHAIN_MEMBERS = 64;

/** Values core-v2 returns for secrets; sending one back keeps the stored value. */
export const REDACTED_MARKERS = ['[redacted]', '••••••••'];
export const isRedacted = (v: unknown): boolean => typeof v === 'string' && REDACTED_MARKERS.includes(v);
export function hasRedacted(v: unknown): boolean {
	if (isRedacted(v)) return true;
	if (Array.isArray(v)) return v.some(hasRedacted);
	if (v && typeof v === 'object') return Object.values(v).some(hasRedacted);
	return false;
}

/** Types core-v2 accepts for PUT /middlewares (allowedMiddlewareTypes). */
export const ALLOWED_TYPES = [
	'addPrefix', 'basicAuth', 'buffering', 'circuitBreaker', 'compress', 'contentType', 'digestAuth', 'errors',
	'forwardAuth', 'grpcWeb', 'headers', 'ipAllowList', 'inFlightReq', 'passTLSClientCert', 'rateLimit',
	'redirectRegex', 'redirectScheme', 'replacePath', 'replacePathRegex', 'retry', 'stripPrefix', 'stripPrefixRegex'
] as const;

/** Types core-v2 accepts for a TCP middleware (allowedTCPMiddlewareTypes). */
export const TCP_TYPES = ['inFlightConn', 'ipAllowList'] as const;
export const PROTOCOLS: Protocol[] = ['http', 'tcp'];
export const middlewareProtocolOf = (c: Pick<MiddlewareConfig, 'protocol'> | null | undefined): Protocol => (c?.protocol === 'tcp' ? 'tcp' : 'http');
/** Each protocol's middlewares open under their own base, so the dialog knows which editor to show. */
export const MIDDLEWARE_BASE: Record<Protocol, string> = { http: '/middlewares', tcp: '/tcp-middlewares' };

export interface TypeCategory { id: string; labelKey: string; types: string[] }
export const TYPE_CATEGORIES: TypeCategory[] = [
	{ id: 'authentication', labelKey: 'catAuthentication', types: ['basicAuth', 'digestAuth', 'forwardAuth'] },
	{ id: 'traffic', labelKey: 'catTraffic', types: ['rateLimit', 'inFlightReq', 'buffering', 'retry', 'circuitBreaker'] },
	{ id: 'security', labelKey: 'catSecurity', types: ['ipAllowList'] },
	{ id: 'headers', labelKey: 'catHeaders', types: ['headers', 'passTLSClientCert'] },
	{ id: 'routing', labelKey: 'catRouting', types: ['addPrefix', 'stripPrefix', 'stripPrefixRegex', 'replacePath', 'replacePathRegex', 'redirectRegex', 'redirectScheme'] },
	{ id: 'transform', labelKey: 'catTransform', types: ['compress', 'contentType', 'grpcWeb'] },
	{ id: 'errors', labelKey: 'catErrors', types: ['errors'] }
];

/** Sensitive keys (core-v2 redaction.sensitive); the backend stays authoritative. */
export function isSensitiveKey(key: string): boolean {
	const k = key.toLowerCase().replaceAll('_', '');
	return ['password', 'secret', 'token', 'authorization', 'cookie', 'privatekey', 'hmachash', 'hmacencoded', 'users'].some((s) => k.includes(s))
		|| ['key', 'apikey', 'lapikey', 'credentials'].includes(k);
}

/** Routers/chains that reference this name (used-by is not part of the middleware envelope). */
export const refersTo = (members: readonly string[] | null | undefined, name: string) => (members ?? []).some((m) => referenceBaseName(m) === name);

/** External middlewares carry the provider suffix in their name (e.g. `auth@docker`). */
export const chainMembers = (c: ChainConfig | null | undefined) => c?.middlewares ?? [];

/* ------------------------------------------------------------------ */
/* Presence-aware form state                                           */
/* ------------------------------------------------------------------ */

export type LeafState =
	| { kind: 'text'; value: string; present?: boolean }
	| { kind: 'number'; raw: string; present?: boolean }
	| { kind: 'bool'; present: boolean; value: boolean }
	| { kind: 'tri'; value: 'unset' | 'yes' | 'no' }
	| { kind: 'list'; items: string[]; present?: boolean }
	| { kind: 'map'; numeric?: boolean; entries: { key: string; value: string }[]; present?: boolean }
	| { kind: 'object'; present: boolean };

/** path → leaf; nested objects are implied by dotted paths. */
export type FormState = Record<string, LeafState>;

export type FieldKind = 'text' | 'number' | 'bool' | 'tri' | 'duration' | 'list' | 'map' | 'mapint' | 'object';

/** Control selection from the live schema node type. */
export function fieldKind(node: SchemaField): FieldKind {
	const type = node.type;
	if (node.fields && Object.keys(node.fields).length > 0) return 'object';
	if (type === '[]string' || type.endsWith('.Users')) return 'list';
	if (type === 'map[string]string') return 'map';
	if (type === 'map[string]int') return 'mapint';
	if (type === 'bool') return 'bool';
	if (type === '*bool') return 'tri';
	if (type === 'int' || type === 'int64' || type === '*int' || type === '*int64') return 'number';
	if (type.endsWith('.Duration')) return 'duration';
	return 'text';
}

const EMPTY_LEAVES: Record<Exclude<FieldKind, 'object'>, () => LeafState> = {
	text: () => ({ kind: 'text', value: '' }),
	number: () => ({ kind: 'number', raw: '' }),
	bool: () => ({ kind: 'bool', present: false, value: false }),
	tri: () => ({ kind: 'tri', value: 'unset' }),
	duration: () => ({ kind: 'text', value: '' }),
	list: () => ({ kind: 'list', items: [] }),
	map: () => ({ kind: 'map', entries: [] }),
	mapint: () => ({ kind: 'map', entries: [], numeric: true })
};

export function emptyLeaf(kind: FieldKind): LeafState {
	const factory = EMPTY_LEAVES[kind as Exclude<FieldKind, 'object'>];
	return factory ? factory() : { kind: 'text', value: '' };
}

/**
 * Initialize form state from the safe config projection. Presence mirrors the
 * loaded document exactly: an explicit false, 0 or empty string stays explicit
 * so an unrelated edit never silently drops it. Container objects (e.g. tls: {})
 * are represented as present object leaves.
 */
export function stateFromConfig(fields: Record<string, SchemaField>, config: Record<string, unknown>, prefix = ''): FormState {
	const state: FormState = {};
	for (const [key, node] of Object.entries(fields)) {
		const path = prefix ? `${prefix}.${key}` : key;
		const kind = fieldKind(node);
		const value = config[key];
		if (kind === 'object') {
			if (key in config && value !== undefined && value !== null && typeof value === 'object' && !Array.isArray(value)) {
				state[path] = { kind: 'object', present: true };
				Object.assign(state, stateFromConfig(node.fields, value as Record<string, unknown>, path));
			}
			continue;
		}
		if (value === undefined || value === null) continue;
		state[path] = leafFromValue(kind, value);
	}
	return state;
}

function leafFromValue(kind: FieldKind, value: unknown): LeafState {
	switch (kind) {
		case 'bool': return { kind: 'bool', present: true, value: value === true };
		case 'tri': return { kind: 'tri', value: value === true ? 'yes' : 'no' };
		case 'number': return { kind: 'number', raw: String(value), present: true };
		case 'list': return { kind: 'list', items: Array.isArray(value) ? value.filter((v): v is string => typeof v === 'string') : [], present: true };
		case 'map':
		case 'mapint': {
			const entries = value && typeof value === 'object' && !Array.isArray(value)
				? Object.entries(value as Record<string, unknown>).map(([k, v]) => ({ key: k, value: String(v ?? '') }))
				: [];
			return { kind: 'map', entries, present: true, numeric: kind === 'mapint' };
		}
		default: return { kind: 'text', value: typeof value === 'string' ? value : String(value), present: true };
	}
}

/**
 * Serialize form state to the typed config object.
 *
 * Presence rules:
 * - Only fields the user actually set or that were present in loaded config are serialized.
 * - Once set, explicit false, zero, empty strings, empty arrays and empty map values are
 *   preserved.
 * - Explicit container objects (e.g. tls: {}) serialize even when their child properties are empty.
 */
export function serializeConfig(state: FormState): Record<string, unknown> {
	const config: Record<string, unknown> = {};
	const paths = Object.keys(state).sort();
	for (const path of paths) {
		const leaf = state[path];
		if (!leaf) continue;
		if (leaf.kind === 'object') {
			if (!leaf.present) continue;
			const parts = path.split('.');
			let node = config;
			for (let i = 0; i < parts.length; i++) {
				if (typeof node[parts[i]] !== 'object' || node[parts[i]] === null) {
					node[parts[i]] = {};
				}
				node = node[parts[i]] as Record<string, unknown>;
			}
			continue;
		}
		let value: unknown;
		switch (leaf.kind) {
			case 'text':
				if (leaf.present === false) continue;
				value = leaf.value;
				break;
			case 'number': {
				if (leaf.present === false || leaf.raw.trim() === '') continue;
				const parsed = Number(leaf.raw);
				if (!Number.isInteger(parsed)) continue;
				value = parsed;
				break;
			}
			case 'bool':
				if (!leaf.present) continue;
				value = leaf.value;
				break;
			case 'tri':
				if (leaf.value === 'unset') continue;
				value = leaf.value === 'yes';
				break;
			case 'list':
				if (leaf.present === false) continue;
				value = [...leaf.items];
				break;
			case 'map': {
				if (leaf.present === false) continue;
				const entries = leaf.entries.filter((entry) => entry.key.length > 0);
				value = Object.fromEntries(entries.map((entry) => [entry.key, leaf.numeric && /^\d+$/.test(entry.value) ? Number(entry.value) : entry.value]));
				break;
			}
			default: continue;
		}
		const parts = path.split('.');
		let node = config;
		for (let i = 0; i < parts.length - 1; i++) {
			if (typeof node[parts[i]] !== 'object' || node[parts[i]] === null) node[parts[i]] = {};
			node = node[parts[i]] as Record<string, unknown>;
		}
		node[parts[parts.length - 1]] = value;
	}
	return config;
}


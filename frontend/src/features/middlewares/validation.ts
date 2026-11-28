/**
 * Client-side validation supplements for the live middleware schema.
 *
 * The schema endpoint supplies the complete field tree; this module adds the
 * semantic rules the field tree cannot express (patterns, bounds, references),
 * mirroring app/core/internal/routing/middlewares/validation.go so users get
 * early feedback. Paths are inner-config paths (the request's `config` value);
 * the backend revalidates everything and stays authoritative.
 */
import {
	fieldKind,
	isValidMiddlewareName,
	isValidReference,
	type FieldKind,
	type FormState,
	type Protocol,
	type SchemaField,
} from './models';

export interface FieldHint {
	/** Catalog key under middlewares.fields.* explaining format/units. */
	helpKey?: string;
	/** Signed bounds for number fields. */
	min?: number;
	max?: number;
	/** Scalar value pattern. */
	pattern?: RegExp;
	/** Which message a pattern failure reports. */
	patternError?: 'invalidPath' | 'invalidValue';
	/** Fixed choice list (rendered as a select). */
	choices?: string[];
	/** Values are HTTP status codes (0 means unset for single-code fields). */
	statuses?: boolean;
	/** Every list entry must start with "/". */
	pathPrefix?: boolean;
}

const INT64_MAX = 2 ** 40;
const BODY_SIZE_KEYS = new Set(['maxRequestBodyBytes', 'maxResponseBodyBytes', 'maxBodySize', 'maxResponseBodySize']);

/** Number bounds mirrored from the backend field validator (-1 only for body-size keys). */
export function numberBounds(path: string): { min: number; max: number } {
	const leaf = path.split('.').pop() ?? '';
	if (BODY_SIZE_KEYS.has(leaf)) return { min: -1, max: INT64_MAX };
	if (leaf === 'attempts') return { min: 1, max: 100 };
	if (leaf === 'responseCode' || leaf === 'rejectStatusCode') return { min: 0, max: 599 };
	if (leaf === 'ipv6Subnet') return { min: 0, max: 128 };
	if (leaf === 'depth') return { min: 0, max: INT64_MAX };
	return { min: 0, max: INT64_MAX };
}

/** Curated format help and constraints keyed by inner-config path (shared across types). */
const HINTS: Record<string, FieldHint> = {
	prefix: { helpKey: 'helpPath', pattern: /^\//, patternError: 'invalidPath' },
	path: { helpKey: 'helpPath', pattern: /^\//, patternError: 'invalidPath' },
	prefixes: { helpKey: 'helpPathList', pathPrefix: true },
	regex: { helpKey: 'helpRegex' },
	replacement: { helpKey: 'helpReplacement' },
	scheme: { helpKey: 'helpScheme', choices: ['http', 'https'] },
	port: { helpKey: 'helpPort' },
	expression: { helpKey: 'helpExpression' },
	retryExpression: { helpKey: 'helpExpression' },
	period: { helpKey: 'helpDuration' },
	checkPeriod: { helpKey: 'helpDuration' },
	fallbackDuration: { helpKey: 'helpDuration' },
	recoveryDuration: { helpKey: 'helpDuration' },
	initialInterval: { helpKey: 'helpDuration' },
	timeout: { helpKey: 'helpDuration' },
	dialTimeout: { helpKey: 'helpDuration' },
	readTimeout: { helpKey: 'helpDuration' },
	writeTimeout: { helpKey: 'helpDuration' },
	attempts: { helpKey: 'helpAttempts', min: 1, max: 100 },
	responseCode: { helpKey: 'helpStatusSingle', statuses: true, min: 0, max: 599 },
	rejectStatusCode: { helpKey: 'helpStatusSingle', statuses: true, min: 0, max: 599 },
	amount: { helpKey: 'helpAmount', min: 0, max: INT64_MAX },
	sourceRange: { helpKey: 'helpCidrList' },
	excludedIPs: { helpKey: 'helpCidrList' },
	depth: { helpKey: 'helpDepth', min: 0, max: INT64_MAX },
	ipv6Subnet: { helpKey: 'helpIpv6Subnet', min: 0, max: 128 },
	address: { helpKey: 'helpAddress' },
	authSigninURL: { helpKey: 'helpSigninUrl' },
	authResponseHeadersRegex: { helpKey: 'helpRegex' },
	accessControlMaxAge: { helpKey: 'helpSeconds', min: 0, max: INT64_MAX },
	stsSeconds: { helpKey: 'helpSeconds', min: 0, max: INT64_MAX },
	encodings: { helpKey: 'helpEncodings' },
	includedContentTypes: { helpKey: 'helpMediaList' },
	excludedContentTypes: { helpKey: 'helpMediaList' },
	defaultEncoding: { helpKey: 'helpEncoding', choices: ['gzip', 'br', 'zstd'] },
	minResponseBodyBytes: { helpKey: 'helpBytes', min: 0, max: INT64_MAX },
	maxRequestBodyBytes: { helpKey: 'helpBytes', min: -1, max: INT64_MAX },
	maxResponseBodyBytes: { helpKey: 'helpBytes', min: -1, max: INT64_MAX },
	memRequestBodyBytes: { helpKey: 'helpBytes', min: 0, max: INT64_MAX },
	memResponseBodyBytes: { helpKey: 'helpBytes', min: 0, max: INT64_MAX },
	status: { helpKey: 'helpStatusList', statuses: true },
	statusRewrites: { helpKey: 'helpStatusRewrites', statuses: true },
	service: { helpKey: 'helpServiceRef' },
	query: { helpKey: 'helpQuery' },
	middlewares: { helpKey: 'helpChainMembers' },
	headerField: { helpKey: 'helpHeaderName' },
	requestHeaderName: { helpKey: 'helpHeaderName' },
	realm: { helpKey: 'helpRealm' },
	endpoints: { helpKey: 'helpRedisEndpoints' },
	db: { helpKey: 'helpRedisDb', min: 0, max: INT64_MAX },
	poolSize: { helpKey: 'helpRedisPool', min: 0, max: INT64_MAX },
	maxActiveConns: { helpKey: 'helpRedisPool', min: 0, max: INT64_MAX },
	minIdleConns: { helpKey: 'helpRedisPool', min: 0, max: INT64_MAX },
	insecureSkipVerify: { helpKey: 'helpSkipVerify' },
	caOptional: { helpKey: 'helpCaOptional' },
	pem: { helpKey: 'helpPem' },
	ca: { helpKey: 'helpAsset' },
	cert: { helpKey: 'helpAsset' },
	key: { helpKey: 'helpAsset' },
	usersFile: { helpKey: 'helpAsset' },
};

const DURATION_LEAVES = new Set([
	'period',
	'checkPeriod',
	'fallbackDuration',
	'recoveryDuration',
	'initialInterval',
	'timeout',
	'dialTimeout',
	'readTimeout',
	'writeTimeout',
]);

function hintFor(path: string, kind: FieldKind): FieldHint {
	const leaf = path.split('.').pop() ?? '';
	const direct = HINTS[path] ?? HINTS[leaf];
	if (direct) return direct;
	if (kind === 'number') return { ...numberBounds(path) };
	if (kind === 'duration' || DURATION_LEAVES.has(leaf)) return { helpKey: 'helpDuration' };
	if (
		kind === 'list' &&
		(leaf === 'regex' ||
			(leaf.endsWith('Regex') && leaf !== 'authResponseHeadersRegex') ||
			leaf === 'accessControlAllowOriginListRegex')
	)
		return { helpKey: 'helpRegexList' };
	if (kind === 'list' && (leaf.endsWith('Headers') || leaf === 'hostsProxyHeaders'))
		return { helpKey: 'helpHeaderList' };
	if (
		kind === 'map' &&
		(leaf === 'customRequestHeaders' || leaf === 'customResponseHeaders' || leaf === 'sslProxyHeaders')
	)
		return { helpKey: 'helpHeaderMap' };
	if (kind === 'text' && (leaf === 'regex' || leaf.endsWith('Regex') || leaf === 'accessControlAllowOriginListRegex'))
		return { helpKey: 'helpRegex' };
	return {};
}

/** Client-side control/lookup supplement for one leaf path; the ledger and tests consume this. */
export function fieldHintFor(path: string, kind: FieldKind): FieldHint {
	return hintFor(path, kind);
}

const HEADER_NAME = /^[!#$%&'*+.^_`|~0-9A-Za-z-]+$/;
const GO_DURATION_PART = /(-?\d+(\.\d+)?(ns|us|µs|ms|s|m|h))/g;

/** Exact Go duration semantics: additive parts, unit list fixed, range -1s..24h. */
export function isValidDuration(value: string): boolean {
	if (!/^(-?\d+(\.\d+)?(ns|us|µs|ms|s|m|h))+$/.test(value)) return false;
	let total = 0;
	for (const part of value.match(GO_DURATION_PART) ?? []) {
		const amount = parseFloat(part);
		if (Number.isNaN(amount)) return false;
		const unit = part.replace(/^-?\d+(\.\d+)?/, '');
		const seconds =
			unit === 'h'
				? 3600
				: unit === 'm'
					? 60
					: unit === 's'
						? 1
						: unit === 'ms'
							? 0.001
							: unit === 'us' || unit === 'µs'
								? 0.000001
								: 0.000000001;
		total += amount * seconds;
	}
	return total >= -1 && total <= 24 * 3600;
}

export function isValidStatusCode(value: string): boolean {
	const code = Number(value);
	return Number.isInteger(code) && code >= 100 && code <= 599;
}

/** "500-599" or a single code, ascending when ranged (backend validMiddlewareStatus). */
export function isValidStatusRange(value: string): boolean {
	const parts = value.split('-');
	if (parts.length > 2) return false;
	let previous = 0;
	for (const part of parts) {
		if (!/^\d+$/.test(part)) return false;
		const code = Number(part);
		if (code < 100 || code > 599 || code < previous) return false;
		previous = code;
	}
	return true;
}

export function isValidCidr(value: string): boolean {
	// IPv4/IPv6 address or prefix; JS lacks net.ParseCIDR, so this is a
	// structural pre-check and the backend stays authoritative.
	if (value.includes('/')) {
		const slash = value.indexOf('/');
		return isValidIp(value.slice(0, slash)) && /^\d{1,3}$/.test(value.slice(slash + 1));
	}
	return isValidIp(value);
}

function isValidIp(value: string): boolean {
	if (value.includes(':')) return /^[0-9a-fA-F:.]+$/.test(value) && value.includes(':');
	const parts = value.split('.');
	return parts.length === 4 && parts.every((p) => /^\d{1,3}$/.test(p) && Number(p) <= 255);
}

export function isValidAuthUrl(value: string): boolean {
	try {
		const url = new URL(value);
		return (
			(url.protocol === 'http:' || url.protocol === 'https:') &&
			!!url.hostname &&
			!url.username &&
			!url.password &&
			!url.hash
		);
	} catch {
		return false;
	}
}

export function isValidHeaderName(value: string): boolean {
	return HEADER_NAME.test(value);
}

export function isValidGoRegex(value: string): boolean {
	if (value === '') return false;
	try {
		new RegExp(value);
		return true;
	} catch {
		return false;
	}
}

const MEDIA_TYPE = /^[!#$%&'*+.^_`|~0-9A-Za-z-]+\/[!#$%&'*+.^_`|~0-9A-Za-z-]+(\s*;\s*[!#$%&'*+.^_`|~0-9A-Za-z-]+=.*)?$/;
const COMPRESSION_ENCODINGS = new Set(['gzip', 'br', 'zstd']);

export interface ValidationContext {
	/** t-like accessor scoped to the middlewares catalog. */
	copy: (key: string, args?: Record<string, string | number>) => string;
}

/**
 * Validate one field path against its hint. Returns a localized message or ''.
 * The message is shown under the field, so it never repeats the label.
 */
export function validateLeaf(
	path: string,
	state: FormState,
	schemaFields: Record<string, SchemaField>,
	copy: ValidationContext['copy'],
): string {
	const leaf = state[path];
	if (!leaf) return '';
	const node = schemaFields[path];
	const kind: FieldKind = node
		? fieldKind(node)
		: leaf.kind === 'text' && DURATION_LEAVES.has(path.split('.').pop() ?? '')
			? 'duration'
			: leaf.kind;
	const hint = hintFor(path, kind);
	switch (leaf.kind) {
		case 'text': {
			const value = leaf.value;
			if (value.length === 0) return '';
			if ((kind === 'duration' || DURATION_LEAVES.has(path.split('.').pop() ?? '')) && !isValidDuration(value))
				return copy('invalidDuration');
			if (hint.pattern && !hint.pattern.test(value))
				return copy(hint.patternError === 'invalidPath' ? 'invalidPath' : 'invalidValue');
			if (hint.choices && !hint.choices.includes(value))
				return copy('invalidChoice', { values: hint.choices.join(', ') });
			if (hint.helpKey === 'helpPort' && !/^\d*$/.test(value)) return copy('invalidPortNumber');
			if (hint.helpKey === 'helpPort' && value !== '' && !(Number(value) >= 1 && Number(value) <= 65535))
				return copy('outOfBounds', { min: 1, max: 65535 });
			if (hint.helpKey === 'helpAddress' && !isValidAuthUrl(value)) return copy('invalidAddress');
			if (hint.helpKey === 'helpSigninUrl' && !isValidAuthUrl(value)) return copy('invalidAddress');
			if (hint.helpKey === 'helpRealm' && /[:\r\n]/.test(value)) return copy('invalidRealm');
			if (hint.statuses && !isValidStatusRange(value)) return copy('invalidStatusRange');
			if (hint.helpKey === 'helpRegex' && !isValidGoRegex(value)) return copy('invalidRegex');
			if (hint.helpKey === 'helpHeaderName' && !isValidHeaderName(value)) return copy('invalidHeaderName');
			if (hint.helpKey === 'helpServiceRef' && !isValidReference(value)) return copy('invalidReference');
			if (hint.helpKey === 'helpQuery' && value.includes('\n')) return copy('invalidValue');
			return '';
		}
		case 'number': {
			const raw = leaf.raw.trim();
			if (raw === '') return '';
			if (!/^-?\d+$/.test(raw)) return copy('invalidNumber');
			const parsed = Number(raw);
			const min = hint.min ?? numberBounds(path).min;
			const max = hint.max ?? numberBounds(path).max;
			if (parsed < min || parsed > max) return copy('outOfBounds', { min, max });
			if (hint.statuses && parsed !== 0 && (parsed < 100 || parsed > 599)) return copy('invalidStatusCode');
			return '';
		}
		case 'list': {
			for (const item of leaf.items) {
				if (item === '') continue;
				if (hint.statuses && !isValidStatusRange(item)) return copy('invalidStatusRange');
				if (hint.helpKey === 'helpCidrList' && !isValidCidr(item)) return copy('invalidCidr');
				if ((hint.helpKey === 'helpRegexList' || hint.helpKey === 'helpRegex') && !isValidGoRegex(item))
					return copy('invalidRegex');
				if (hint.helpKey === 'helpHeaderList' && item !== '*' && !isValidHeaderName(item))
					return copy('invalidHeaderName');
				if (hint.helpKey === 'helpMediaList' && !MEDIA_TYPE.test(item)) return copy('invalidMediaType');
				if (path === 'encodings' && !COMPRESSION_ENCODINGS.has(item)) return copy('invalidEncoding');
				if (hint.pathPrefix && !item.startsWith('/')) return copy('invalidPath');
				if (hint.helpKey === 'helpRedisEndpoints' && !/^[^\s/:]+(:\d{1,5})?$/.test(item))
					return copy('invalidRedisEndpoint');
			}
			return '';
		}
		case 'map': {
			for (const entry of leaf.entries) {
				if (entry.key === '') continue;
				if (hint.helpKey === 'helpHeaderMap' && !isValidHeaderName(entry.key)) return copy('invalidHeaderName');
				if (kind === 'mapint' && !isValidStatusCode(entry.value)) return copy('invalidStatusCode');
			}
			return '';
		}
		default:
			return '';
	}
}

/**
 * Whole-form validation. `flatFields` is the flattened leaf map for the
 * selected type; returns inner-config path → message. Structural rules that
 * span fields (sourceCriterion exclusivity, compress pairing, chain members,
 * credentials) live here; backend validation remains authoritative.
 */
export function validateForm(
	protocol: Protocol,
	type: string,
	flatFields: Record<string, SchemaField>,
	state: FormState,
	config: Record<string, unknown>,
	credentials: { users: { username: string; password: string }[]; mode: 'none' | 'replace' },
	chainMembers: string[],
	selfName: string,
	copy: ValidationContext['copy'],
): Record<string, string> {
	const errors: Record<string, string> = {};
	for (const path of Object.keys(state)) {
		const message = validateLeaf(path, state, flatFields, copy);
		if (message) errors[path] = message;
	}
	for (const [path, message] of Object.entries(configSemantics(protocol, type, config, copy))) errors[path] = message;
	if (credentials.mode === 'replace') {
		for (const [key, message] of Object.entries(validateCredentials(credentials.users, copy))) errors[key] = message;
	}
	if (type === 'chain') {
		if (chainMembers.length === 0) errors['middlewares'] = copy('chainEmpty');
		else if (chainMembers.length > 64) errors['middlewares'] = copy('chainTooLong');
		else {
			for (const member of chainMembers) {
				if (!isValidReference(member)) {
					errors['middlewares'] = copy('invalidMember', { member });
					break;
				}
				if (selfName && member.replace(/@file$/, '') === selfName) {
					errors['middlewares'] = copy('selfReference');
					break;
				}
			}
		}
	}
	return errors;
}

/** Cross-field semantic checks mirrored from the backend for early feedback. */
function configSemantics(
	protocol: Protocol,
	type: string,
	config: Record<string, unknown>,
	copy: ValidationContext['copy'],
): Record<string, string> {
	const errors: Record<string, string> = {};
	const fail = (path: string, key: string) => {
		if (!errors[path]) errors[path] = copy(key);
	};
	const str = (path: string): string => {
		let node: unknown = config;
		for (const part of path.split('.')) {
			if (node && typeof node === 'object') node = (node as Record<string, unknown>)[part];
			else return '';
		}
		return typeof node === 'string' ? node : '';
	};
	const arr = (path: string): unknown[] => {
		let node: unknown = config;
		for (const part of path.split('.')) {
			if (node && typeof node === 'object') node = (node as Record<string, unknown>)[part];
			else return [];
		}
		return Array.isArray(node) ? node : [];
	};
	const num = (path: string): number | undefined => {
		let node: unknown = config;
		for (const part of path.split('.')) {
			if (node && typeof node === 'object') node = (node as Record<string, unknown>)[part];
			else return undefined;
		}
		if (typeof node === 'number' && Number.isFinite(node)) return node;
		if (typeof node === 'string' && node.trim() !== '' && !Number.isNaN(Number(node))) return Number(node);
		return undefined;
	};
	switch (type) {
		case 'stripPrefix':
			if (arr('prefixes').length === 0) fail('prefixes', 'requiredList');
			break;
		case 'stripPrefixRegex':
			if (arr('regex').length === 0) fail('regex', 'requiredList');
			break;
		case 'replacePathRegex':
		case 'redirectRegex':
			if (str('regex') === '') fail('regex', 'requiredField');
			if (str('replacement') === '') fail('replacement', 'requiredField');
			break;
		case 'inFlightConn': {
			const amount = num('amount');
			if (amount === undefined) fail('amount', 'requiredField');
			else if (amount < 1) fail('amount', 'positiveNumber');
			break;
		}
		case 'ipAllowList':
		case 'ipWhiteList':
			if (arr('sourceRange').length === 0) fail('sourceRange', 'requiredList');
			break;
		case 'errors':
			if (arr('status').length === 0) fail('status', 'requiredList');
			if (str('service') === '') fail('service', 'requiredField');
			break;
		case 'compress': {
			const included = arr('includedContentTypes').length > 0;
			const excluded = arr('excludedContentTypes').length > 0;
			if (included && excluded) fail('excludedContentTypes', 'compressExclusive');
			break;
		}
		case 'forwardAuth':
			if (str('address') === '') fail('address', 'requiredField');
			break;
		case 'circuitBreaker':
			if (str('expression') === '') fail('expression', 'requiredField');
			break;
		case 'inFlightReq':
		case 'rateLimit': {
			if (type === 'inFlightReq' && num('amount') === undefined) fail('amount', 'requiredField');
			if (type === 'rateLimit' && num('average') === undefined) fail('average', 'requiredField');
			const criterion = (config['sourceCriterion'] as Record<string, unknown> | undefined) ?? {};
			let active = 0;
			if (Object.keys((criterion['ipStrategy'] as Record<string, unknown> | undefined) ?? {}).length > 0) active++;
			if (typeof criterion['requestHeaderName'] === 'string' && criterion['requestHeaderName'] !== '') active++;
			if (criterion['requestHost'] === true) active++;
			if (active > 1) fail('sourceCriterion.requestHost', 'sourceCriterionExclusive');
			break;
		}
		case 'retry':
			if (num('attempts') === undefined) fail('attempts', 'requiredField');
			break;
		case 'buffering':
			if (num('maxRequestBodyBytes') === undefined) fail('maxRequestBodyBytes', 'requiredField');
			break;
	}
	return errors;
}

/**
 * Credentials validation for BasicAuth/DigestAuth replacement. Hashing happens
 * on the server; the client only checks the documented bounds.
 */
export function validateCredentials(
	users: { username: string; password: string }[],
	copy: ValidationContext['copy'],
): Record<string, string> {
	const errors: Record<string, string> = {};
	const usernamePattern = /^[a-zA-Z0-9_.@-]{1,64}$/;
	const seen = new Set<string>();
	users.forEach((user, index) => {
		const key = `credentials.${index}`;
		if (user.username === '' && user.password === '') return; // entirely blank rows are ignored
		if (!usernamePattern.test(user.username)) errors[key] = copy('invalidUsername');
		else if (seen.has(user.username)) errors[key] = copy('duplicateUsername');
		else if (user.password.length < 1 || user.password.length > 72 || /[\x00\r\n]/.test(user.password))
			errors[key] = copy('invalidSecret');
		if (user.username !== '') seen.add(user.username);
	});
	return errors;
}

/** Non-blank credential rows that will actually be submitted. */
export function activeCredentialUsers(
	users: { username: string; password: string }[],
): { username: string; password: string }[] {
	return users.filter((user) => user.username !== '' || user.password !== '');
}

/**
 * Flatten a schema subtree into leaf paths (dotted), preserving order.
 */
export function flattenSchemaFields(fields: Record<string, SchemaField>, prefix = ''): Record<string, SchemaField> {
	const out: Record<string, SchemaField> = {};
	for (const [key, node] of Object.entries(fields)) {
		const path = prefix ? `${prefix}.${key}` : key;
		if (node.fields && Object.keys(node.fields).length > 0) Object.assign(out, flattenSchemaFields(node.fields, path));
		else out[path] = node;
	}
	return out;
}

/**
 * Recursively find any unknown configuration keys against the schema tree.
 * Correctly distinguishes schema containers (which have child fields) from
 * maps/lists (whose keys or elements are user-defined values, not schema fields).
 */
export function findUnknownKeys(
	schemaFields: Record<string, SchemaField>,
	config: Record<string, unknown>,
	prefix = '',
): string[] {
	const unknown: string[] = [];
	for (const [key, value] of Object.entries(config)) {
		const path = prefix ? `${prefix}.${key}` : key;
		const node = schemaFields[key];
		if (!node) {
			unknown.push(path);
			continue;
		}
		// Only recurse into schema containers (objects with defined child fields)
		if (node.fields && Object.keys(node.fields).length > 0) {
			if (value && typeof value === 'object' && !Array.isArray(value)) {
				unknown.push(...findUnknownKeys(node.fields, value as Record<string, unknown>, path));
			}
		}
	}
	return unknown;
}

export { isValidMiddlewareName };

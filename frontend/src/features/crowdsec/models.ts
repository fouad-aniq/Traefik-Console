// Types mirrored from api/openapi.json (CrowdSecStatus, CrowdSecStats, CrowdSecConfig, CrowdSecDecision).

export type PartState = 'missing' | 'installed';
export type LapiState = 'ok' | 'invalid_key' | 'unreachable' | 'unchecked';
export type EngineState = 'not_configured' | 'stopped' | 'running' | 'unavailable';
export type CrowdSecMode = 'stream' | 'live' | 'alone';

export interface CrowdSecConfig {
	integration_mode?: 'managed' | 'external';
	read_timeout_seconds?: number;
	alert_limit?: number;
	ca_certificate?: string;
	client_certificate?: string;
	client_key?: string;
	enabled: boolean;
	active_enabled?: boolean;
	state?: string;
	mode: CrowdSecMode;
	lapi_url: string;
	lapi_key?: string;
	auto_enroll: boolean;
	plugin_version: string;
	plugin_alias?: string;
	middleware_name?: string;
	routers?: string[] | null;
	trusted_proxy_cidrs?: string[] | null;
	enrollment_ids?: number[] | null;
	machine_id?: string;
	machine_password?: string;
	auto_enroll_machine?: boolean;
	machine_enrollment_id?: number;
	update_max_failure?: number;
	http_timeout_seconds?: number;
	appsec_enabled?: boolean;
	appsec_url?: string;
	appsec_failure_block?: boolean;
	appsec_unreachable_block?: boolean;
	appsec_body_limit?: number;
	client_trusted_cidrs?: string[] | null;
	middleware_owned?: boolean;
	metrics_url?: string;
	metrics_token_set?: boolean;
	access_log_source?: string;
	protected_cidrs?: string[] | null;
	/** Optimistic-concurrency counter; 0 until the first save (If-None-Match: *). */
	version: number;
}

export interface Bouncer {
	name: string;
	ip_address: string;
	type: string;
	version: string;
	last_pull: string;
	revoked: boolean;
}
export interface RouterProtection {
	name: string;
	origin: 'managed' | 'external';
	protected: boolean;
}

export interface CrowdSecCapabilities {
	auto_enroll: boolean;
	alerts: boolean;
	decisions: boolean;
	decision_create: boolean;
	decision_delete: boolean;
	allowlist_read: boolean;
	allowlist_write: boolean;
	appsec: boolean;
	metrics: boolean;
	hub_read: boolean;
}

export interface CrowdSecMetricsSnapshot {
	state: 'available' | 'unavailable' | 'authentication_rejected';
	observed_at: string;
	ingestion_state: 'unverified' | 'records_observed' | 'no_records_observed' | 'unavailable';
	parsed_access_log_events: number | null;
	lapi_requests: number | null;
	bouncer_pulls: number | null;
	appsec_processed: number | null;
	appsec_blocked: number | null;
	capi_last_pull?: string;
}

export interface CrowdSecHubItem {
	name: string;
	type: string;
	version: string;
	status: string;
}

export interface CrowdSecSources {
	lapi_url: 'environment' | 'saved' | 'none';
	bouncer_authentication: 'environment' | 'saved' | 'none';
	machine: 'environment' | 'saved' | 'none';
	ca_certificate: 'environment' | 'saved' | 'none';
	client_certificate: 'environment' | 'saved' | 'none';
	read_timeout: 'environment' | 'saved' | 'none';
	alert_limit: 'environment' | 'saved' | 'none';
	appsec_url?: SettingSource;
	metrics_url?: SettingSource;
	metrics_token?: SettingSource;
	access_log_source?: SettingSource;
	protected_cidrs?: SettingSource;
}

export type SettingSource = 'environment' | 'saved' | 'none';

/** A CROWDSEC_* variable set at startup. Its Console setting is read-only; secrets carry no value. */
export interface EnvVariable {
	name: string;
	field: string;
	secret: boolean;
	value?: string;
}

/** The container registration the Console started with (DOCKER_HOST, CROWDSEC_CONTAINER). */
export interface RuntimeRegistration {
	accessible?: boolean;
	docker_configured: boolean;
	container_id?: string;
	compose_service?: string;
}

/** PUT /crowdsec/connection and POST /crowdsec/connection/test body. Locked fields are ignored by the server. */
export interface ConnectionSettings {
	lapi_url: string;
	lapi_key?: string;
	generate_key?: boolean;
	generate_machine?: boolean;
	machine_id?: string;
	machine_password?: string;
	ca_certificate?: string;
	client_certificate?: string;
	client_key?: string;
	appsec_url?: string;
	metrics_url?: string;
	metrics_token?: string;
	access_log_source?: string;
	read_timeout_seconds?: number;
	alert_limit?: number;
	protected_cidrs?: string[];
	/** Present: also sets the Traefik protection. Routers keep their bouncer attachment. */
	protection?: ProtectionSettings;
}

export type ProtectionMode = 'off' | 'managed' | 'external';
export interface ProtectionSettings {
	mode: ProtectionMode;
	bouncer_mode?: string;
	plugin_version?: string;
	plugin_alias?: string;
	middleware_name?: string;
	update_max_failure: number;
	http_timeout_seconds?: number;
	appsec_enabled: boolean;
	trusted_proxy_cidrs?: string[];
	client_trusted_cidrs?: string[];
}

export interface ConnectionTest {
	lapi_url: string;
	lapi: 'ok' | 'reachable' | 'invalid_key' | 'unreachable' | 'not_configured';
	machine: 'ok' | 'invalid_key' | 'unreachable' | 'not_configured';
	appsec: 'reachable' | 'unreachable' | 'not_configured';
	metrics: 'available' | 'authentication_rejected' | 'unavailable' | 'not_configured';
	engine: EngineState;
	can_generate_key: boolean;
	observed_at: string;
}

/** The environment variable that locks a Console setting, if any. */
export const lockedBy = (environment: EnvVariable[] | null | undefined, field: string) =>
	(environment ?? []).find((v) => v.field === field);

export interface CrowdSecStatus {
	engine: { state: EngineState; name?: string; image?: string };
	plugin: { state: PartState; alias?: string; module_name?: string; version?: string; origin?: string };
	middleware: { state: PartState; name?: string; plugin_alias?: string; origin?: string };
	key: { present: boolean };
	/** The bouncer key file as Traefik sees it, for a bouncer configured outside the Console. */
	key_file?: string;
	lapi: { state: LapiState; url: string };
	machine?: { state: 'not_configured' | 'ok' | 'invalid_key' | 'unreachable'; id?: string };
	ingestion?: { state: 'unverified' | 'verified' | 'unavailable' | 'records_observed' | 'no_records_observed' };
	remediation?: { state: 'disabled' | 'unverified' | 'verified' | 'unavailable' };
	capabilities?: CrowdSecCapabilities;
	sources?: CrowdSecSources;
	environment?: EnvVariable[] | null;
	runtime?: RuntimeRegistration;
	observed_at?: string;
	metrics?: CrowdSecMetricsSnapshot;
	bouncers: Bouncer[] | null;
	routers: RouterProtection[] | null;
	config: CrowdSecConfig;
	complete: boolean;
}

export interface CrowdSecAlert {
	id: number;
	scope: string;
	value: string;
	reason: string;
	country: string;
	as: string;
	as_number?: string;
	kind?: string;
	events_count: number;
	created_at: string;
	decisions?: CrowdSecDecision[];
}

export interface CrowdSecAlertPage {
	items: CrowdSecAlert[];
	page: number;
	limit: number;
	has_more: boolean;
	observed_at: string;
}

export interface CrowdSecAllowlistEntry {
	value: string;
	description?: string;
	created_at?: string;
	expiration?: string;
}

export interface CrowdSecAllowlist {
	name: string;
	allowlist_id?: string;
	description: string;
	items: CrowdSecAllowlistEntry[];
	created_at?: string;
	updated_at?: string;
	console_managed: boolean;
}

export interface CountItem {
	key: string;
	count: number;
}
export interface CrowdSecStats {
	available: boolean;
	window_hours: number;
	generated_at: string;
	alerts: number;
	truncated: boolean;
	active_decisions: number;
	decisions_by_type: CountItem[] | null;
	decisions_by_origin: CountItem[] | null;
	top_countries: CountItem[] | null;
	top_scenarios: CountItem[] | null;
	top_sources: CountItem[] | null;
	timeline: { start: string; count: number }[] | null;
	bucket_hours: number;
	bouncers: Bouncer[] | null;
	metrics?: CrowdSecMetricsSnapshot;
	partial: string[] | null;
}

export interface CrowdSecDecision {
	id: number;
	origin: string;
	type: string;
	scope: string;
	value: string;
	duration: string;
	scenario: string;
}

export type StatsWindow = '1h' | '24h' | '7d' | '30d';
export const STATS_WINDOWS: StatsWindow[] = ['1h', '24h', '7d', '30d'];

export type StepTone = 'success' | 'warning' | 'danger' | 'info' | 'neutral';

export const decisionPath = (value: string) =>
	'/api/v1/crowdsec/decisions/' + value.split('/').map(encodeURIComponent).join('/');
export const decisionIDPath = (id: number) => `/api/v1/crowdsec/decisions/id/${id}`;

/** Normalises a textarea of CIDRs / IPs (one per line or comma separated). */
export function parseCidrs(text: string): string[] {
	return [
		...new Set(
			text
				.split(/[\s,]+/)
				.map((v) => v.trim())
				.filter(Boolean),
		),
	];
}

function validIPv4(value: string): boolean {
	const parts = value.split('.');
	return parts.length === 4 && parts.every((part) => /^\d{1,3}$/.test(part) && Number(part) <= 255);
}

function validIPv6(value: string): boolean {
	if (!/^[0-9a-fA-F:.]+$/.test(value)) return false;
	const halves = value.split('::');
	if (halves.length > 2) return false;
	const groups = (side: string) => (side ? side.split(':') : []);
	const left = groups(halves[0]);
	const right = halves.length === 2 ? groups(halves[1]) : [];
	if ([...left, ...right].some((part) => !part)) return false;
	const validGroup = (part: string, last: boolean) => /^[0-9a-fA-F]{1,4}$/.test(part) || (last && validIPv4(part));
	const all = [...left, ...right];
	if (!all.every((part, index) => validGroup(part, index === all.length - 1))) return false;
	const units = all.reduce((count, part) => count + (validIPv4(part) ? 2 : 1), 0);
	return halves.length === 2 ? units < 8 : units === 8;
}

export const looksLikeIpOrCidr = (value: string) => {
	const [address, prefix, extra] = value.split('/');
	if (extra !== undefined || !address) return false;
	if (prefix !== undefined && (!/^\d{1,3}$/.test(prefix) || Number(prefix) > (address.includes(':') ? 128 : 32)))
		return false;
	return address.includes(':') ? validIPv6(address) : validIPv4(address);
};

export const countryName = (code: string, language: string) => {
	if (!/^[A-Z]{2}$/.test(code)) return code === '??' ? '' : code;
	try {
		return new Intl.DisplayNames([language], { type: 'region' }).of(code) ?? code;
	} catch {
		return code;
	}
};

/** Bar width in percent relative to the largest value; never below a visible sliver for non-zero counts. */
export const barWidth = (count: number, max: number) =>
	max > 0 && count > 0 ? Math.max(2, Math.round((count / max) * 100)) : 0;

/** Tone for a connection-test result; neutral when the part is not configured. */
export const testTone = (state: string): StepTone =>
	state === 'ok' || state === 'available' || state === 'reachable'
		? 'success'
		: state === 'not_configured'
			? 'neutral'
			: state === 'invalid_key' || state === 'authentication_rejected'
				? 'danger'
				: 'warning';

/** Shell command that registers a bouncer key by hand (shown when the Console cannot generate one). */
const containerRef = (runtime: RuntimeRegistration | undefined) =>
	runtime?.container_id ? runtime.container_id.slice(0, 12) : 'crowdsec';
export const bouncerCommand = (runtime: RuntimeRegistration | undefined) =>
	`docker exec ${containerRef(runtime)} cscli bouncers add traefik-console`;

export const machineCommand = (runtime: RuntimeRegistration | undefined) =>
	`docker exec ${containerRef(runtime)} cscli machines add traefik-console-reader --auto -f -`;

/** Choropleth bucket (0 = no alerts, 1-5 increasing) on a log scale, so one noisy country does not wash out the rest. */
export const heatLevel = (count: number, max: number) => {
	if (count <= 0 || max <= 0) return 0;
	if (max === 1) return 5;
	return Math.min(5, 1 + Math.floor((Math.log(count) / Math.log(max)) * 4.999));
};

/** Private, loopback and link-local sources: CrowdSec has no country for them. */
export const isPrivateAddress = (value: string) => {
	const address = (value ?? '').split('/')[0].toLowerCase();
	if (address.includes(':')) return address === '::1' || /^f[cd]/.test(address) || /^fe[89ab]/.test(address);
	const parts = address.split('.').map(Number);
	if (parts.length !== 4 || parts.some((p) => !Number.isInteger(p) || p < 0 || p > 255)) return false;
	const [a, b] = parts;
	return (
		a === 10 || a === 127 || (a === 172 && b >= 16 && b <= 31) || (a === 192 && b === 168) || (a === 169 && b === 254)
	);
};

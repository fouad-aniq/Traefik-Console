import { parseDocument } from 'yaml';
import { providerFields } from './api';
import { isMasked, type Provider, type ResolverForm } from './models';

export type ValidationGroup = 'identity' | 'challenge' | 'advanced';

/** Non-negative Go duration (ns, us, ms, s, m, h; compound forms allowed). */
export function durationMilliseconds(value: string): number | null {
	if (value === '0') return 0;
	if (!value) return null;
	const factors: Record<string, number> = { ns: 1e-6, us: 1e-3, 'µs': 1e-3, 'μs': 1e-3, ms: 1, s: 1000, m: 60000, h: 3600000 };
	const part = /(\d+(?:\.\d*)?|\.\d+)(ns|us|µs|μs|ms|s|m|h)/gy;
	let total = 0;
	let end = 0;
	let match: RegExpExecArray | null;
	while ((match = part.exec(value))) {
		total += Number(match[1]) * factors[match[2]];
		end = part.lastIndex;
	}
	return end === value.length && Number.isFinite(total) ? total : null;
}

export function validNameserver(value: string): boolean {
	const match = /^(\[[\da-fA-F:]+\]|(?:\d{1,3}\.){3}\d{1,3}):(\d+)$/.exec(value);
	if (!match || Number(match[2]) < 1 || Number(match[2]) > 65535) return false;
	if (match[1].startsWith('[')) {
		try {
			new URL('http://' + match[1]);
			return true;
		} catch {
			return false;
		}
	}
	return match[1].split('.').every((n) => /^(0|[1-9]\d{0,2})$/.test(n) && Number(n) <= 255);
}

export interface ValidationContext {
	form: ResolverForm;
	creating: boolean;
	existingNames: string[];
	provider?: Provider;
	customProviderCodes: string[];
	entryPoints: string[];
	/** false when the entrypoint list could not be loaded: defer to the server. */
	entryPointsKnown: boolean;
}

/**
 * Pure client-side mirror of the core-v2 manager checks, keyed `group.field`.
 * The server stays authoritative; this only prevents obviously invalid staging.
 * `c` resolves an error message key from the resolverUI catalog.
 */
export function resolverErrors(group: ValidationGroup, ctx: ValidationContext, c: (key: string) => string): Record<string, string> {
	const { form: r, provider } = ctx;
	const errors: Record<string, string> = {};
	const fail = (field: string, message: string) => {
		errors[group + '.' + field] = c(message);
	};
	const duration = (value: string, max: number, allowZero = false) => {
		const ms = durationMilliseconds(value);
		return ms !== null && (allowZero ? ms >= 0 : ms > 0) && ms <= max;
	};

	if (group === 'identity') {
		if (!/^[a-z][a-z0-9_-]{0,62}$/.test(r.name)) fail('name', 'invalidName');
		else if (ctx.creating && ctx.existingNames.includes(r.name)) fail('name', 'duplicateName');
		if (r.type === 'acme') {
			if (!/^[^\s@]+@[^\s@]+\.[^\s@]+$/.test(r.email)) fail('email', 'invalidEmail');
			if (r.environment === 'custom') {
				try {
					const u = new URL(r.caUrl);
					if (u.protocol !== 'https:' || u.username || u.password || u.hash) throw new Error();
				} catch {
					fail('caUrl', 'invalidURL');
				}
			}
		}
	} else if (group === 'challenge') {
		if (r.type === 'custom') {
			if (!r.customYaml.trim() || new TextEncoder().encode(r.customYaml).length > 65536) fail('yaml', 'invalidYAML');
			else {
				try {
					const doc = parseDocument(r.customYaml);
					if (doc.errors.length) throw new Error();
					const value = doc.toJS({ maxAliasCount: 100 });
					if (!value || typeof value !== 'object' || Array.isArray(value) || !value.acme || typeof value.acme !== 'object' || Array.isArray(value.acme) || Object.keys(value).some((k) => k !== 'acme')) throw new Error();
				} catch {
					fail('yaml', 'invalidCustomYAML');
				}
			}
		} else if (r.challenge === 'dns01') {
			if (r.externalCredentials) {
				if (!ctx.customProviderCodes.includes(r.provider)) fail('provider', 'selectProvider');
			} else {
				const auth = provider?.auth_alternatives?.[r.alternative];
				if (!provider || !auth) fail('provider', 'selectProvider');
				const groups = providerFields(provider, r.alternative);
				for (const f of [...groups.required, ...groups.optional]) {
					const v = r.values[f.env_var] ?? '';
					if (groups.required.includes(f) && !v.trim()) fail(f.env_var, 'required');
					if (!v || isMasked(v)) continue;
					if (v.includes('\0') || new TextEncoder().encode(v).length > 1024 * 1024) fail(f.env_var, 'invalidAdvanced');
					if (f.type === 'integer' && (!/^\d+$/.test(v) || Number(v) > 86400 || (f.bounds === '120-86400' && Number(v) < 120))) fail(f.env_var, 'invalidInteger');
					if (f.type === 'duration') {
						// Whole seconds only: the backend delivers integer seconds to lego.
						const ms = durationMilliseconds(v);
						if (ms === null || ms <= 0 || ms > 3600000 || ms % 1000 !== 0) fail(f.env_var, 'invalidDuration');
					}
					if (f.type === 'boolean' && !['true', 'false'].includes(v)) fail(f.env_var, 'invalidAdvanced');
				}
			}
		} else if (r.challenge === 'http01' && ctx.entryPointsKnown && !ctx.entryPoints.includes(r.entryPoint)) {
			fail('entryPoint', 'selectEntryPoint');
		} else if (r.challenge === 'http01' && !r.entryPoint) {
			fail('entryPoint', 'selectEntryPoint');
		}
	} else if (group === 'advanced' && r.type === 'acme') {
		if (r.keyType && !['EC256', 'EC384', 'RSA2048', 'RSA4096', 'RSA8192'].includes(r.keyType)) fail('keyType', 'invalidKeyType');
		if (r.challenge === 'dns01') {
			if (r.nameservers.length > 8 || r.nameservers.some((v) => !validNameserver(v))) fail('nameservers', 'invalidNameservers');
			if (r.delay && !duration(r.delay, 600000, true)) fail('delay', 'invalidDuration');
		}
	}
	return errors;
}

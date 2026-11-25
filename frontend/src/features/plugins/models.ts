/**
 * Plugins (core-v2): static `experimental.plugins` declarations.
 *
 *   GET    /api/v1/plugins                installed: managed + external (read-only, from traefik.yml)
 *   PUT    /api/v1/plugins/{alias}        save {module_name, version}    (If-Match; ?dry_run=true previews)
 *   POST   /api/v1/plugins/yaml           save a pasted Traefik Plugin Shop snippet (+ optional middleware)  (If-Match)
 *   DELETE /api/v1/plugins/{alias}        remove (If-Match); 409 while an active middleware still uses it
 *   GET    /api/v1/plugins/catalog?q=     cached public catalog (max 500, name-sorted); empty until synced
 *   POST   /api/v1/plugins/catalog/sync   fetch plugins.traefik.io and replace the cache → {synced_count}
 *
 * The rules below mirror core-v2 (internal/core/plugins, internal/traefik/validation); the backend stays authoritative.
 */
import { parse, stringify } from 'yaml';
import type { Resource } from '../../core/types';

export interface PluginConfig { module_name: string; version: string }
export type PluginResource = Resource<PluginConfig> & { update?: PluginUpdateInfo };

/** How the server relates a catalog entry to the plugins installed here (computed on every read). */
export type InstallState = 'not_installed' | 'up_to_date' | 'update_available' | 'unknown';

export interface CatalogRefresh { last_attempt_at: string; last_success_at: string; last_attempt_failed: boolean }
/** The server compares each installed plugin with the cached catalog on every GET /plugins. */
export interface PluginUpdateInfo { state: 'up_to_date' | 'update_available' | 'not_in_catalog' | 'unknown'; latest_version?: string; versions: string[] | null; can_update: boolean; checked_at?: string; last_attempt_failed: boolean }
export interface CatalogPlugin {
 versions: string[];
 plugin_type: string;
	module_name: string;
	display_name: string;
	summary: string;
	latest_version: string;
	snippet_yaml: string;
	fetched_at: string;
	install_state: InstallState;
	installed_alias?: string;
	installed_version?: string;
	installed_origin?: 'managed' | 'external';
	installed_status?: string;
	/** True only for a managed plugin with a newer cataloged version. */
	can_update: boolean;
}

export interface CatalogPage { items: CatalogPlugin[]; total: number; limit: number; offset: number; last_synced_at: string; inventory_known: boolean; refresh: CatalogRefresh }

export interface InstallYamlInput { short_name?: string; raw_yaml: string; middleware_name?: string }

export const ALIAS_PATTERN = /^[a-z][a-z0-9_-]{0,62}$/;
export const MODULE_PATTERN = /^[a-zA-Z0-9._-]+(\/[a-zA-Z0-9._-]+)+$/;
export const VERSION_PATTERN = /^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(?:-([0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*))?(?:\+([0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*))?$/;
export const validVersion=(v:string)=> v.length<=128 && VERSION_PATTERN.test(v) && !(v.match(VERSION_PATTERN)?.[4]?.split('.').some(x=>/^0[0-9]+$/.test(x)));
export function moduleKey(module:string){const p=module.trim().split('/');if(p.length>=3&&p[0].toLowerCase()==='github.com')for(let i=0;i<3;i++)p[i]=p[i].toLowerCase();return p.join('/');}
export const knownVersions=(entry:CatalogPlugin)=>entry.versions?.length?entry.versions:[entry.latest_version].filter(Boolean);
export const stableVersion=(entry:CatalogPlugin)=>knownVersions(entry).find(v=>!v.split('+')[0].includes('-'))??'';
export function availableAlias(module:string,names:string[]){const base=suggestAlias(module)||'plugin';let alias=base;for(let n=2;names.includes(alias);n++)alias=base.slice(0,58)+'-'+n;return alias;}
export const MAX_SNIPPET_CHARACTERS = 20_000;
export const MAX_CATALOG_QUERY = 256;

export type Source = 'module' | 'snippet';

export interface PluginForm {
	source: Source;
	name: string;
	module: string;
	version: string;
	snippet: string;
	middlewareName: string;
}

export const newForm = (): PluginForm => ({ source: 'module', name: '', module: '', version: '', snippet: '', middlewareName: '' });

export const formFrom = (resource: PluginResource): PluginForm => ({
	...newForm(),
	name: resource.name,
	module: resource.config.module_name ?? '',
	version: resource.config.version ?? ''
});

/** A readable alias proposal from a module path: the repository name, lowercased and cleaned. */
export function suggestAlias(module: string): string {
	const last = module.split('/').filter(Boolean).pop() ?? '';
	const cleaned = last.toLowerCase().replace(/[^a-z0-9_-]+/g, '-').replace(/^[^a-z]+/, '').replace(/-+$/, '');
	return cleaned.slice(0, 63);
}

/** Error codes the plugin endpoints answer with when the install or update itself is refused. */
export const PLUGIN_REFUSALS = ['ERR_PLUGIN_ALREADY_INSTALLED', 'ERR_PLUGIN_NO_UPDATE', 'ERR_PLUGIN_NOT_IN_CATALOG'];

/** Public repository page for modules hosted on GitHub; other hosts get no link. */
export const moduleLink = (module: string) => (module.startsWith('github.com/') && MODULE_PATTERN.test(module) ? 'https://' + module : '');

export const toInput = (form: PluginForm): PluginConfig => ({ module_name: form.module.trim(), version: form.version.trim() });

export type SnippetResult =
	| { ok: true; alias: string; module: string; version: string; middleware: string; needsAlias: boolean }
	| { ok: false; reason: 'empty' | 'yaml' | 'shape' | 'multiple' | 'fields' };

const isMap = (v: unknown): v is Record<string, unknown> => !!v && typeof v === 'object' && !Array.isArray(v);

/**
 * Reads a pasted snippet the way the server does: `experimental.plugins`, a bare `plugins` map or a single
 * `moduleName/version` pair (alias required then), optionally with exactly one `http.middlewares` instance.
 */
export function parseSnippet(text: string): SnippetResult {
	if (!text.trim()) return { ok: false, reason: 'empty' };
	let root: unknown;
	try { root = parse(text, { maxAliasCount: 0 }); } catch { return { ok: false, reason: 'yaml' }; }
	if (!isMap(root) || Object.keys(root).some(k=>!['experimental','plugins','moduleName','version','http'].includes(k))) return { ok: false, reason: 'shape' };
 if(isMap(root.experimental)&&Object.keys(root.experimental).length!==1)return {ok:false,reason:'shape'};
 if(isMap(root.http)&&Object.keys(root.http).length!==1)return {ok:false,reason:'shape'};
	let defs: Record<string, unknown> | null = null;
	let needsAlias = false;
	if (isMap(root.experimental)) defs = isMap(root.experimental.plugins) ? root.experimental.plugins : null;
	else if (isMap(root.plugins)) defs = root.plugins;
	else if ('moduleName' in root) { defs = { '': root }; needsAlias = true; }
	if (!defs) return { ok: false, reason: 'shape' };
	const entries = Object.entries(defs);
	if (entries.length !== 1) return { ok: false, reason: entries.length ? 'multiple' : 'shape' };
	const [alias, spec] = entries[0];
	if (!isMap(spec) || Object.keys(spec).length !== 2 || typeof spec.moduleName !== 'string' || typeof spec.version !== 'string') return { ok: false, reason: 'fields' };
	let middleware = '';
	if (isMap(root.http)) {
		const mws = isMap(root.http.middlewares) ? Object.keys(root.http.middlewares) : [];
		if (mws.length !== 1) return { ok: false, reason: 'shape' };
		middleware = mws[0];
  const instance = (root.http.middlewares as Record<string, unknown>)[middleware];
  if (!isMap(instance) || Object.keys(instance).length !== 1 || !isMap(instance.plugin) || Object.keys(instance.plugin).length !== 1 || !(alias in instance.plugin) || !isMap(instance.plugin[alias])) return { ok: false, reason: 'shape' };
	}
	return { ok: true, alias, module: spec.moduleName, version: spec.version, middleware, needsAlias };
}

export type Messages = Record<'moduleInstalled' | 'nameRequired' | 'invalidName' | 'nameTaken' | 'moduleRequired' | 'invalidModule' | 'versionRequired' | 'invalidVersion' | 'snippetRequired' | 'snippetYaml' | 'snippetShape' | 'snippetMultiple' | 'snippetFields' | 'snippetInvalid' | 'middlewareTaken' | 'invalidMiddlewareName', string>;

export interface ValidationContext {
	creating: boolean;
	existingNames: string[];
	existingMiddlewares: string[];
	/** Installed module (lowercase) → alias, for plugins that are not pending removal. */
	installedModules?: Record<string, string>;
}

const moduleOwner = (ctx: ValidationContext, module: string) => ctx.installedModules?.[moduleKey(module)] ?? '';

export function pluginErrors(form: PluginForm, ctx: ValidationContext, m: Messages): Record<string, string> {
	const errors: Record<string, string> = {};
	if (form.source === 'snippet' && ctx.creating) {
		const result = parseSnippet(form.snippet);
		if (!result.ok) {
			errors.snippet = ({ empty: m.snippetRequired, yaml: m.snippetYaml, shape: m.snippetShape, multiple: m.snippetMultiple, fields: m.snippetFields } as const)[result.reason];
			return errors;
		}
		const alias = form.name.trim() || result.alias;
		if (result.needsAlias && !form.name.trim()) errors.name = m.nameRequired;
		else if (!ALIAS_PATTERN.test(alias)) errors.name = m.invalidName;
		else if (ctx.existingNames.includes(alias)) errors.name = m.nameTaken;
		if (!MODULE_PATTERN.test(result.module) || !validVersion(result.version)) errors.snippet = m.snippetInvalid;
		else if (moduleOwner(ctx, result.module) && moduleOwner(ctx, result.module) !== alias) errors.snippet = m.moduleInstalled.replace('{alias}', moduleOwner(ctx, result.module));
		const mw = form.middlewareName.trim() || result.middleware;
		if (result.middleware) {
			if (!ALIAS_PATTERN.test(mw)) errors.middlewareName = m.invalidMiddlewareName;
			else if (ctx.existingMiddlewares.includes(mw)) errors.middlewareName = m.middlewareTaken;
		}
		return errors;
	}
	const name = form.name.trim();
	if (ctx.creating) {
		if (!name) errors.name = m.nameRequired;
		else if (!ALIAS_PATTERN.test(name)) errors.name = m.invalidName;
		else if (ctx.existingNames.includes(name)) errors.name = m.nameTaken;
	}
	const module = form.module.trim();
	if (!module) errors.module = m.moduleRequired;
	else if (!MODULE_PATTERN.test(module)) errors.module = m.invalidModule;
	else if (ctx.creating && moduleOwner(ctx, module) && moduleOwner(ctx, module) !== name) errors.module = m.moduleInstalled.replace('{alias}', moduleOwner(ctx, module));
	const version = form.version.trim();
	if (!version) errors.version = m.versionRequired;
	else if (!validVersion(version)) errors.version = m.invalidVersion;
	return errors;
}

/** Middlewares (not pending deletion) that instantiate this plugin, as link targets. */
export function usersOf(alias: string, middlewares: { name: string; status: string; config: { type?: string; plugin_name?: string } }[]) {
	return middlewares
		.filter((x) => x.config.type === 'plugin' && x.config.plugin_name === alias)
		.map((x) => ({ name: x.name, path: '/middlewares/' + encodeURIComponent(x.name) }));
}

export const catalogUsers = (plugins: PluginResource[], module: string) =>
 plugins.filter((x) => moduleKey(x.config.module_name) === moduleKey(module));

/** Build an optional editable example only for one supported middleware. Nothing is staged. */
export function catalogExample(entry: CatalogPlugin, alias: string, version: string): string {
 try {
  const root: unknown = parse(entry.snippet_yaml, { maxAliasCount: 0 });
  if (!isMap(root) || !isMap(root.http) || !isMap(root.http.middlewares)) return '';
  const entries=Object.entries(root.http.middlewares);
  if(entries.length!==1) return '';
  const [name,mw]=entries[0];
  if(!isMap(mw) || Object.keys(mw).length!==1 || !isMap(mw.plugin) || Object.keys(mw.plugin).length!==1) return '';
  const spec=Object.values(mw.plugin)[0]; if(!isMap(spec)) return '';
  return stringify({experimental:{plugins:{[alias]:{moduleName:entry.module_name,version}}},http:{middlewares:{[name]:{plugin:{[alias]:spec}}}}});
 } catch { return ''; }
}

/** SemVer precedence for the versions the catalog reports (build metadata ignored); null when either is not a valid version. */
export function compareVersions(a: string, b: string): number | null {
	if (!validVersion(a) || !validVersion(b)) return null;
	const split = (v: string) => { const [core, pre = ''] = v.slice(1).split('+')[0].split(/-(.*)/s); return { nums: core.split('.').map(Number), pre: pre ? pre.split('.') : [] }; };
	const x = split(a), y = split(b);
	for (let i = 0; i < 3; i++) if (x.nums[i] !== y.nums[i]) return x.nums[i] < y.nums[i] ? -1 : 1;
	if (!x.pre.length || !y.pre.length) return x.pre.length === y.pre.length ? 0 : x.pre.length ? -1 : 1;
	for (let i = 0; i < Math.max(x.pre.length, y.pre.length); i++) {
		const l = x.pre[i], r = y.pre[i];
		if (l === undefined) return -1;
		if (r === undefined) return 1;
		if (l === r) continue;
		const ln = /^[0-9]+$/.test(l), rn = /^[0-9]+$/.test(r);
		if (ln && rn) return Number(l) < Number(r) ? -1 : 1;
		if (ln !== rn) return ln ? -1 : 1;
		return l < r ? -1 : 1;
	}
	return 0;
}

import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/svelte';
// The API client binds `fetch` at import time, so install a forwarding stub first.
vi.hoisted(() => {
	(globalThis as any).__mockFetch = async () => { throw new Error('no mock'); };
	globalThis.fetch = ((...args: unknown[]) => (globalThis as any).__mockFetch(...args)) as typeof fetch;
});
import App from '../src/App.svelte';
import PluginsPage from '../src/features/plugins/PluginsPage.svelte';
import { parseSnippet, pluginErrors, suggestAlias, usersOf, moduleLink, newForm, catalogExample, catalogUsers, compareVersions } from '../src/features/plugins/models';
import { flashes } from '../src/shared/ui/flash.svelte';

const preview = (extra = {}) => ({ changes: [{ action: 'created', id: 'plugin:demo', kind: 'plugin', name: 'demo', scope: 'static' }], static_diff: '+experimental:', dynamic_diff: '', requires_restart: true, ...extra });
const operation = (extra = {}) => ({ id: 7, kind: 'plugin.create', target: 'demo', status: 'succeeded', summary: 'Applied 1 change', requires_restart: true, created_at: 'now', ...extra });
const plugin = (name: string, config: Record<string, unknown> = {}, version = 1, extra = {}) => ({ id: `plugin:${name}`, name, version, origin: 'managed', provider: 'static', status: 'active', read_only: false, live_health: 'unknown', updated_at: '2026-01-01T00:00:00Z', config: { module_name: `github.com/org/${name}`, version: 'v1.0.0', ...config }, ...extra });
const bouncer = plugin('bouncer');
const external = plugin('legacy', {}, 1, { id: 'external:plugin:legacy', origin: 'external', read_only: true });
const mw = (name: string, pluginName: string) => ({ id: `middleware:${name}`, name, version: 1, status: 'active', origin: 'managed', read_only: false, config: { type: 'plugin', plugin_name: pluginName, spec: {} } });
const entry = (module: string, extra = {}) => ({ module_name: module, display_name: module.split('/').pop(), summary: 'Does things', versions:['v2.1.0','v1.2.0','v1.1.0','v1.0.0','v3.0.0-rc.1'],plugin_type:'middleware',latest_version: 'v2.1.0', snippet_yaml: '', fetched_at: '2026-01-01 10:00:00', install_state: 'not_installed', can_update: false, ...extra });
const installed = (module: string, alias: string, state: string, extra = {}) => entry(module, { install_state: state, installed_alias: alias, installed_version: 'v1.0.0', installed_origin: 'managed', can_update: state === 'update_available', ...extra });

interface Call { method: string; path: string; headers: Record<string, string>; body?: any }
let calls: Call[] = [];
let handlers: Record<string, () => Response | Promise<Response>> = {};
const json = (body: unknown, status = 200) => new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } });
const catalogJson = (items: unknown[] | null, total = items?.length ?? 0) => json({items:items ?? [],total,limit:25,offset:0,last_synced_at:'2026-01-01 10:00:00',inventory_known:true,refresh:{last_success_at:'2026-01-01 10:00:00',last_attempt_at:'',last_attempt_failed:false}});
const err = (code: string, status: number, message = 'nope') => json({ code, message, operation_id: 'op', retryable: false }, status);

beforeEach(() => {
	flashes.splice(0);
	calls = [];
	HTMLDialogElement.prototype.showModal ??= function () { this.setAttribute('open', ''); };
	HTMLDialogElement.prototype.close ??= function () { this.removeAttribute('open'); };
	(globalThis as any).__mockFetch = async (path: string, init: RequestInit = {}) => {
		const headers: Record<string, string> = {};
		(init.headers as Headers).forEach((v, k) => { headers[k.toLowerCase()] = v; });
		calls.push({ method: init.method ?? 'GET', path, headers, body: init.body ? JSON.parse(String(init.body)) : undefined });
		const key = `${init.method ?? 'GET'} ${path}`;
		if (handlers[key]) return handlers[key]();
		return err('ERR_NOT_FOUND', 404, 'nf');
	};
});
afterEach(() => { cleanup(); handlers = {}; });

// App.svelte asks for the session first; these fixtures model a signed-in administrator.
const signedIn = { setup_required: false, authenticated: true, providers: [],
	session: { user: { username: 'admin', method: 'password' }, csrf: 'c', expires_at: '2099-01-01T00:00:00Z' } };
const base = (plugins: unknown, middlewares: unknown = []) => ({
 'GET /api/v1/auth/status': () => json(signedIn),
 'GET /api/v1/plugins': () => json(plugins),
	'GET /api/v1/middlewares': () => json(middlewares)
});

const m = {
	moduleInstalled: 'mi {alias}', nameRequired: 'nr', invalidName: 'in', nameTaken: 'nt', moduleRequired: 'mr', invalidModule: 'im', versionRequired: 'vr', invalidVersion: 'iv', snippetRequired: 'sr', snippetYaml: 'sy',
	snippetShape: 'ss', snippetMultiple: 'sm', snippetFields: 'sf', snippetInvalid: 'si', middlewareTaken: 'mt', invalidMiddlewareName: 'imn'
};
const ctx = { creating: true, existingNames: ['bouncer'], existingMiddlewares: ['guard'] };

describe('models', () => {
	it('suggests an alias from the module path', () => {
		expect(suggestAlias('github.com/maxlerebourg/crowdsec-bouncer-traefik-plugin')).toBe('crowdsec-bouncer-traefik-plugin');
		expect(suggestAlias('github.com/org/9Lives.Plugin')).toBe('lives-plugin');
	});
	it('links only GitHub modules', () => {
		expect(moduleLink('github.com/org/p')).toBe('https://github.com/org/p');
		expect(moduleLink('example.com/org/p')).toBe('');
	});
	it('validates the module form like the server', () => {
		expect(pluginErrors({ ...newForm(), name: 'ok', module: 'github.com/org/p', version: 'v1.2.3-rc.1' }, ctx, m)).toEqual({});
		expect(pluginErrors({ ...newForm(), name: 'Bad Name', module: 'nomodule', version: '1.0' }, ctx, m)).toEqual({ name: 'in', module: 'im', version: 'iv' });
		expect(pluginErrors({ ...newForm(), name: 'bouncer', module: 'github.com/a/b', version: 'v1.0.0' }, ctx, m).name).toBe('nt');
		expect(pluginErrors({ ...newForm() }, ctx, m)).toEqual({ name: 'nr', module: 'mr', version: 'vr' });
	});
	it('reads Plugin Shop snippets', () => {
		const full = 'experimental:\n  plugins:\n    demo:\n      moduleName: github.com/org/demo\n      version: v1.0.0\nhttp:\n  middlewares:\n    my-demo:\n      plugin:\n        demo:\n          a: 1\n';
		expect(parseSnippet(full)).toEqual({ ok: true, alias: 'demo', module: 'github.com/org/demo', version: 'v1.0.0', middleware: 'my-demo', needsAlias: false });
		expect(parseSnippet('moduleName: github.com/org/demo\nversion: v1.0.0')).toMatchObject({ ok: true, needsAlias: true });
		expect(parseSnippet('')).toEqual({ ok: false, reason: 'empty' });
		expect(parseSnippet('a: [')).toEqual({ ok: false, reason: 'yaml' });
		expect(parseSnippet('foo: 1')).toEqual({ ok: false, reason: 'shape' });
		expect(parseSnippet('plugins:\n  a: {moduleName: x/y, version: v1.0.0}\n  b: {moduleName: x/z, version: v1.0.0}')).toEqual({ ok: false, reason: 'multiple' });
		expect(parseSnippet('plugins:\n  a: {moduleName: x/y}')).toEqual({ ok: false, reason: 'fields' });
	});
	it('applies snippet rules: alias required without one, middleware name must be free', () => {
		const snippet = 'moduleName: github.com/org/demo\nversion: v1.0.0';
		expect(pluginErrors({ ...newForm(), source: 'snippet', snippet }, ctx, m)).toEqual({ name: 'nr' });
		const withMw = 'plugins:\n  demo: {moduleName: github.com/org/demo, version: v1.0.0}\nhttp:\n  middlewares:\n    guard:\n      plugin:\n        demo: {}';
		expect(pluginErrors({ ...newForm(), source: 'snippet', snippet: withMw }, ctx, m)).toEqual({ middlewareName: 'mt' });
	});
	it('finds the middlewares that use a plugin', () => {
		const items = [mw('a', 'bouncer'), mw('c', 'other')];
		expect(usersOf('bouncer', items)).toEqual([{ name: 'a', path: '/middlewares/a' }]);
	});
});

describe('version ordering', () => {
	it('follows SemVer precedence and ignores build metadata', () => {
		expect(compareVersions('v1.0.0', 'v1.2.0')).toBe(-1);
		expect(compareVersions('v2.0.0', 'v2.0.0-rc.1')).toBe(1);
		expect(compareVersions('v1.0.0-rc.2', 'v1.0.0-rc.10')).toBe(-1);
		expect(compareVersions('v1.0.0+a', 'v1.0.0+b')).toBe(0);
		expect(compareVersions('latest', 'v1.0.0')).toBeNull();
	});
});

describe('installed list', () => {
	it('shows plugins with version, source and the middlewares using them', async () => {
		handlers = base([bouncer, external], [mw('guard', 'bouncer')]);
		render(PluginsPage, { navigate: () => {} });
		expect((await screen.findAllByText('bouncer')).length).toBeGreaterThan(0);
		expect(screen.getAllByText('github.com/org/bouncer').length).toBeGreaterThan(0);
		expect(screen.getByText('1 resource')).toBeTruthy();
		const legacy = screen.getAllByText('legacy')[0].closest('tr') as HTMLElement;
		expect(within(legacy).getByText('External')).toBeTruthy();
		expect(within(legacy).queryByRole('button', { name: 'Remove' })).toBeNull();
		expect(within(legacy).getByRole('button', { name: 'View' })).toBeTruthy();
	});
	it('refuses to remove a plugin that a middleware still uses', async () => {
		handlers = base([bouncer], [mw('guard', 'bouncer')]);
		render(PluginsPage, { navigate: () => {} });
		await fireEvent.click(await screen.findByRole('button', { name: 'Remove' }));
		expect(await screen.findByText(/still used by: guard/)).toBeTruthy();
		expect(calls.some((c) => c.method === 'DELETE')).toBe(false);
	});
	it('removes with the current version', async () => {
		handlers = { ...base([plugin('bouncer', {}, 3)]), 'DELETE /api/v1/plugins/bouncer': () => json({ operation: operation(), restart_required: true }) };
		render(PluginsPage, { navigate: () => {} });
		await fireEvent.click(await screen.findByRole('button', { name: 'Remove' }));
		await fireEvent.click(await screen.findByRole('button', { name: 'Remove plugin' }));
		await waitFor(() => expect(calls.some((c) => c.method === 'DELETE')).toBe(true));
		expect(calls.find((c) => c.method === 'DELETE')!.headers['if-match']).toBe('"3"');
	});
	it('explains an empty inventory', async () => {
		handlers = base(null);
		render(PluginsPage, { navigate: () => {} });
		expect(await screen.findByText('No plugins installed')).toBeTruthy();
	});
});

describe('install dialog', () => {
	it('previews module and version with a dry run, then applies with a create precondition and one key', async () => {
		handlers = {
			...base([]),
			'PUT /api/v1/plugins/demo?dry_run=true': () => json({ preview: preview(), restart_required: true }),
			'PUT /api/v1/plugins/demo': () => json({ operation: operation(), resource: plugin('demo', { module_name: 'github.com/org/demo', version: 'v1.4.0' }), restart_required: true })
		};
		render(PluginsPage, { navigate: () => {}, mode: 'create' });
		await manual();
		await waitFor(() => expect(document.getElementById('plugin-name')).toBeTruthy());
		await fireEvent.click(screen.getByRole('button', { name: 'Review change' }));
		expect(await screen.findByText('Enter an alias.')).toBeTruthy();
		await fireEvent.input(document.getElementById('plugin-name')!, { target: { value: 'demo' } });
		await fireEvent.input(document.getElementById('plugin-module')!, { target: { value: 'github.com/org/demo' } });
		await fireEvent.input(document.getElementById('plugin-version')!, { target: { value: 'v1.4.0' } });
		await fireEvent.click(screen.getByRole('button', { name: 'Review change' }));
		await screen.findByRole('button', { name: 'Apply changes' });
		const dry = calls.find((c) => c.method === 'PUT')!;
		expect(dry.path).toBe('/api/v1/plugins/demo?dry_run=true');
		expect(dry.headers['if-none-match']).toBe('*');
		expect(dry.body).toEqual({ module_name: 'github.com/org/demo', version: 'v1.4.0' });
		expect(await within(document.querySelector('.resource-dialog') as HTMLElement).findByText(/restarts Traefik/)).toBeTruthy();
		await fireEvent.click(await screen.findByRole('button', { name: 'Apply changes' }));
		await waitFor(() => expect(flashes.some((f) => /Applied|restart required/.test(f.title))).toBe(true));
		const apply = calls.filter((c) => c.method === 'PUT')[1];
		expect(apply.path).toBe('/api/v1/plugins/demo');
		expect(apply.headers['idempotency-key']).toMatch(/^[A-Za-z0-9_-]{16,128}$/);
		expect(calls.some((c) => c.path.includes('/draft'))).toBe(false);
	});
	it('installs from a pasted snippet through the YAML endpoint', async () => {
		handlers = { ...base([]), 'POST /api/v1/plugins/yaml?dry_run=true': () => json({ preview: preview(), restart_required: true }) };
		render(PluginsPage, { navigate: () => {}, mode: 'create' });
		await manual('yaml');
		const editor = await waitFor(() => document.getElementById('plugin-snippet') as HTMLTextAreaElement);
		const snippet = 'experimental:\n  plugins:\n    demo:\n      moduleName: github.com/org/demo\n      version: v1.0.0\nhttp:\n  middlewares:\n    my-demo:\n      plugin:\n        demo:\n          a: 1\n';
		await fireEvent.input(editor, { target: { value: snippet } });
		expect(await screen.findByText('my-demo')).toBeTruthy();
		await fireEvent.input(document.getElementById('plugin-middleware')!, { target: { value: 'edge-demo' } });
		await fireEvent.click(screen.getByRole('button', { name: 'Review change' }));
		await waitFor(() => expect(calls.some((c) => c.method === 'POST' && c.path === '/api/v1/plugins/yaml?dry_run=true')).toBe(true));
		const post = calls.find((c) => c.path === '/api/v1/plugins/yaml?dry_run=true')!;
		expect(post.headers['if-match']).toBeUndefined();
		expect(post.body).toEqual({ raw_yaml: snippet, middleware_name: 'edge-demo' });
	});
	it('prefills module, version and alias from the catalog', async () => {
		handlers = { ...base([]), 'GET /api/v1/plugins/catalog/page?module=github.com%2Forg%2Ffresh&limit=1&offset=0&order=asc': () => catalogJson([entry('github.com/org/fresh')]) };
		render(PluginsPage, { navigate: () => {}, tab: 'catalog', mode: 'create', module: 'github.com/org/fresh' });
		await waitFor(() => expect((document.getElementById('plugin-version') as HTMLInputElement | null)?.value).toBe('v2.1.0'));
		expect(document.getElementById('plugin-module')!.textContent).toContain('fresh');
		
		expect((document.getElementById('plugin-name') as HTMLInputElement).value).toBe('fresh');
	});
	it('shows the version change when updating', async () => {
		handlers = { ...base([bouncer]), 'PUT /api/v1/plugins/bouncer?dry_run=true': () => json({ preview: preview({ changes: [{ action: 'updated', id: 'plugin:bouncer', kind: 'plugin', name: 'bouncer', scope: 'static' }] }), restart_required: true }) };
		render(PluginsPage, { navigate: () => {}, mode: 'edit', name: 'bouncer' });
		await waitFor(() => expect((document.getElementById('plugin-version') as HTMLInputElement | null)?.value).toBe('v1.0.0'));
		expect((document.getElementById('plugin-name') as HTMLInputElement).readOnly).toBe(true);
		
		await fireEvent.input(document.getElementById('plugin-version')!, { target: { value: 'v1.1.0' } });
		await fireEvent.click(screen.getByRole('button', { name: 'Review change' }));
		expect(await screen.findByText('v1.0.0 → v1.1.0')).toBeTruthy();
	});
	it('updates through the server endpoint and previews the version change', async () => {
		handlers = {
			...base([withUpdate(bouncer)]),
			'POST /api/v1/plugins/bouncer/update?dry_run=true': () => json({ preview: preview({ changes: [{ action: 'updated', id: 'plugin:bouncer', kind: 'plugin', name: 'bouncer', scope: 'static' }] }), restart_required: true })
		};
		render(PluginsPage, { navigate: () => {}, mode: 'upgrade', name: 'bouncer' });
		await waitFor(() => expect((document.getElementById('plugin-version') as HTMLSelectElement | null)?.value).toBe('v2.1.0'));
		// Only versions newer than the installed v1.0.0 are offered.
		expect(within(document.getElementById('plugin-version-listbox') as HTMLElement).getAllByRole('option').map((o) => o.textContent?.trim())).toEqual(['v2.1.0', 'v1.2.0']);
		await fireEvent.click(document.getElementById('plugin-version')!);await fireEvent.click(within(document.getElementById('plugin-version-listbox')!).getByRole('option',{name:'v1.2.0'}));
		await fireEvent.click(screen.getByRole('button', { name: 'Review change' }));
		await waitFor(() => expect(calls.some((c) => c.path === '/api/v1/plugins/bouncer/update?dry_run=true')).toBe(true));
		const post = calls.find((c) => c.path === '/api/v1/plugins/bouncer/update?dry_run=true')!;
		expect(post.method).toBe('POST');
		expect(post.headers['if-match']).toBe('"1"');
		expect(post.body).toEqual({ version: 'v1.2.0' });
		expect(await screen.findByText('v1.0.0 → v1.2.0')).toBeTruthy();
	});
	it('does not stage an update when nothing newer exists', async () => {
		handlers = base([withUpdate(bouncer, 'up_to_date')]);
		render(PluginsPage, { navigate: () => {}, mode: 'upgrade', name: 'bouncer' });
		expect(await screen.findByText(/already on the newest/)).toBeTruthy();
		expect((screen.getByRole('button', { name: 'Review change' }) as HTMLButtonElement).disabled).toBe(true);
	});
	it('shows the server message for a plugin refusal instead of the version conflict text', async () => {
		handlers = { ...base([]), 'PUT /api/v1/plugins/demo?dry_run=true': () => err('ERR_PLUGIN_ALREADY_INSTALLED', 409, 'Module is already installed as “guard”.') };
		render(PluginsPage, { navigate: () => {}, mode: 'create' });
		await manual();
		await waitFor(() => expect(document.getElementById('plugin-name')).toBeTruthy());
		await fireEvent.input(document.getElementById('plugin-name')!, { target: { value: 'demo' } });
		await fireEvent.input(document.getElementById('plugin-module')!, { target: { value: 'github.com/org/demo' } });
		await fireEvent.input(document.getElementById('plugin-version')!, { target: { value: 'v1.0.0' } });
		await fireEvent.click(screen.getByRole('button', { name: 'Review change' }));
		expect(await screen.findByText(/already installed as “guard”/)).toBeTruthy();
		expect(screen.queryByRole('button', { name: 'Reload latest version' })).toBeNull();
	});
	it('blocks a module that is already installed under another alias', async () => {
		handlers = base([bouncer]);
		render(PluginsPage, { navigate: () => {}, mode: 'create' });
		await manual();
		await waitFor(() => expect(document.getElementById('plugin-name')).toBeTruthy());
		await fireEvent.input(document.getElementById('plugin-name')!, { target: { value: 'other' } });
		await fireEvent.input(document.getElementById('plugin-module')!, { target: { value: 'github.com/org/bouncer' } });
		await fireEvent.input(document.getElementById('plugin-version')!, { target: { value: 'v1.0.0' } });
		await fireEvent.click(screen.getByRole('button', { name: 'Review change' }));
		expect(await screen.findByText(/already installed as “bouncer”/)).toBeTruthy();
		expect(calls.some((c) => c.method === 'PUT')).toBe(false);
	});
	it('offers to reload the latest version after a version conflict', async () => {
		handlers = { ...base([]), 'PUT /api/v1/plugins/demo?dry_run=true': () => err('ERR_VERSION_CONFLICT', 409) };
		render(PluginsPage, { navigate: () => {}, mode: 'create' });
		await manual();
		await waitFor(() => expect(document.getElementById('plugin-name')).toBeTruthy());
		await fireEvent.input(document.getElementById('plugin-name')!, { target: { value: 'demo' } });
		await fireEvent.input(document.getElementById('plugin-module')!, { target: { value: 'github.com/org/demo' } });
		await fireEvent.input(document.getElementById('plugin-version')!, { target: { value: 'v1.0.0' } });
		await fireEvent.click(screen.getByRole('button', { name: 'Review change' }));
		expect(await screen.findByRole('button', { name: 'Reload latest version' })).toBeTruthy();
	});
	it('shows an external plugin read-only', async () => {
		handlers = base([external]);
		render(PluginsPage, { navigate: () => {}, mode: 'view', name: 'legacy' });
		expect(await screen.findByText(/cannot change it/)).toBeTruthy();
		expect(screen.queryByRole('button', { name: 'Update' })).toBeNull();
	});
	it('reports an unknown plugin', async () => {
		handlers = base([bouncer]);
		render(PluginsPage, { navigate: () => {}, mode: 'view', name: 'ghost' });
		expect(await screen.findByText('This plugin was not found.')).toBeTruthy();
	});
});


const byId=(id:string)=>waitFor(()=>{const el=document.getElementById(id);if(!el)throw new Error('missing #'+id);return el;});
async function manual(kind: 'form' | 'yaml' = 'form'){await fireEvent.click(await byId('plugin-source'));const listbox=await byId('plugin-source-listbox');await fireEvent.click(await within(listbox).findByRole('option',{name:kind==='yaml'?/^YAML/:/^Form/}));}
const dialog=()=>document.querySelector('.resource-dialog') as HTMLElement;
const moduleSelect=async()=>{await waitFor(()=>expect(document.getElementById('plugin-module')?.tagName).toBe('BUTTON'));return document.getElementById('plugin-module') as HTMLElement;};
async function pick(name:string){await fireEvent.click(await moduleSelect());await fireEvent.click(await screen.findByRole('option',{name:new RegExp(name)}));}
const withUpdate=(res:any,state='update_available',can=true)=>({...res,update:{state,latest_version:'v2.1.0',versions:['v2.1.0','v1.2.0'],can_update:can&&state==='update_available',checked_at:'2026-11-09 10:00:00',last_attempt_failed:false}});
const availablePath='GET /api/v1/plugins/catalog/page?availability=available&limit=100&offset=0&order=asc';

describe('catalog inside Add plugin',()=>{
 const versions=['v3.0.0-rc.1','v2.1.0','v1.2.0'];
 it('keeps catalog browsing out of the management page',async()=>{
  handlers=base([bouncer]);render(PluginsPage,{navigate:()=>{}});await screen.findAllByText('bouncer');expect(screen.queryByRole('tab',{name:'Catalog'})).toBeNull();expect(calls.some(c=>c.path.includes('catalog/page'))).toBe(false);
 });
 it('offers the three sources in one select, with the plugin and review tabs of the other dialogs',async()=>{
  handlers={...base([]),[availablePath]:()=>catalogJson([])};render(PluginsPage,{navigate:()=>{},mode:'create'});
  await fireEvent.click(await byId('plugin-source'));
  const listbox=await byId('plugin-source-listbox');
  for(const name of [/^From catalog/,/^Form/,/^YAML/]) expect(await within(listbox).findByRole('option',{name})).toBeTruthy();
  expect(screen.getAllByRole('tab').map((tab)=>tab.textContent?.trim())).toEqual(['Plugin','Review']);
 });
 it('fills identity from the select and preselects the newest stable version',async()=>{
  handlers={...base([]),[availablePath]:()=>catalogJson([entry('github.com/org/fresh',{plugin_type:'middleware',versions})]),'POST /api/v1/plugins/catalog/install?dry_run=true':()=>json({preview:preview(),restart_required:true})};render(PluginsPage,{navigate:()=>{},mode:'create'});
  await pick('fresh');
  expect(document.getElementById('plugin-module')!.textContent).toContain('fresh');
  expect((document.getElementById('plugin-name') as HTMLInputElement).value).toBe('fresh');expect((document.getElementById('plugin-version') as HTMLSelectElement).value).toBe('v2.1.0');
  expect(within(document.getElementById('plugin-version-listbox') as HTMLElement).queryByRole('option',{name:'v3.0.0-rc.1'})).toBeNull();await fireEvent.click(screen.getByLabelText('Show prerelease versions'));expect(within(document.getElementById('plugin-version-listbox') as HTMLElement).getByRole('option',{name:'v3.0.0-rc.1'})).toBeTruthy();
  await fireEvent.click(document.getElementById('plugin-version')!);await fireEvent.click(within(document.getElementById('plugin-version-listbox')!).getByRole('option',{name:'v1.2.0'}));await fireEvent.click(screen.getByRole('button',{name:'Review change'}));
  await waitFor(()=>expect(calls.find(c=>c.path==='/api/v1/plugins/catalog/install?dry_run=true')?.body).toEqual({alias:'fresh',module_name:'github.com/org/fresh',version:'v1.2.0'}));expect(calls.some(c=>c.path.includes('/draft'))).toBe(false);
 });
 it('shows the same fields as the form, with the module as a select and the versions of the chosen plugin',async()=>{
  handlers={...base([]),[availablePath]:()=>catalogJson([entry('github.com/org/fresh',{plugin_type:'middleware',versions})])};render(PluginsPage,{navigate:()=>{},mode:'create'});
  await waitFor(()=>expect(document.getElementById('plugin-module')?.tagName).toBe('BUTTON'));
  expect(document.getElementById('plugin-name')).toBeTruthy();expect(document.getElementById('plugin-module')?.tagName).toBe('BUTTON');expect(document.getElementById('plugin-version')?.getAttribute('role')).toBe('combobox');
  expect((document.getElementById('plugin-version') as HTMLSelectElement).disabled).toBe(true);
  await pick('fresh');expect((document.getElementById('plugin-version') as HTMLSelectElement).disabled).toBe(false);
 });
 it('loads the catalog once when the dialog opens',async()=>{
  handlers={...base([]),[availablePath]:()=>catalogJson([entry('github.com/org/fresh',{plugin_type:'middleware',versions})])};render(PluginsPage,{navigate:()=>{},mode:'create'});
  await moduleSelect();await new Promise(r=>setTimeout(r,50));
  expect(calls.filter(c=>c.path.includes('catalog/page'))).toHaveLength(1);
 });
 it('reuses what the page already loaded when the dialog opens',async()=>{
  window.location.hash='#/plugins';handlers={...base([bouncer]),[availablePath]:()=>catalogJson([entry('github.com/org/fresh',{plugin_type:'middleware',versions})])};render(App);
  await screen.findAllByText('bouncer');await fireEvent.click(await screen.findByRole('button',{name:'Add plugin'}));await moduleSelect();
  const count=(p:string)=>calls.filter(c=>c.method==='GET'&&c.path===p).length;
  expect(count('/api/v1/plugins')).toBe(1);expect(count('/api/v1/middlewares')).toBe(1);
 });
 it('disables selection when inventory is unreadable',async()=>{
  handlers={...base([]),[availablePath]:()=>json({items:[],total:0,limit:100,offset:0,inventory_known:false,refresh:{},last_synced_at:''})};render(PluginsPage,{navigate:()=>{},mode:'create'});
  expect(await screen.findByText(/Plugin declarations could not be read/)).toBeTruthy();
 });
 it('lists only middleware plugins',async()=>{
  handlers={...base([]),[availablePath]:()=>catalogJson([entry('github.com/org/provider',{plugin_type:'provider'}),entry('github.com/org/guard',{plugin_type:'middleware',versions})])};render(PluginsPage,{navigate:()=>{},mode:'create'});
  await fireEvent.click(await moduleSelect());expect(await screen.findByRole('option',{name:/guard/})).toBeTruthy();expect(screen.queryByRole('option',{name:/provider/})).toBeNull();
 });
 it('keeps typed values when switching source',async()=>{
  handlers={...base([]),[availablePath]:()=>catalogJson([])};render(PluginsPage,{navigate:()=>{},mode:'create'});await manual();await fireEvent.input(document.getElementById('plugin-name')!,{target:{value:'kept'}});await manual('yaml');expect(document.getElementById('plugin-snippet')).toBeTruthy();await manual();expect((document.getElementById('plugin-name') as HTMLInputElement).value).toBe('kept');
 });
 it('offers manual fallback after refresh failure',async()=>{
  handlers={...base([]),[availablePath]:()=>catalogJson([]),'POST /api/v1/plugins/catalog/sync':()=>err('ERR_PLUGIN_CATALOG_UNAVAILABLE',502)};render(PluginsPage,{navigate:()=>{},mode:'create'});await fireEvent.click(await within(dialog()).findByRole('button',{name:'Refresh catalog'}));expect(await screen.findByText(/Could not refresh the catalog/)).toBeTruthy();await manual();expect(document.getElementById('plugin-module')).toBeTruthy();
 });
 it('reads every catalog page',async()=>{
  handlers={...base([]),[availablePath]:()=>catalogJson([entry('github.com/org/first',{plugin_type:'middleware'})],150),'GET /api/v1/plugins/catalog/page?availability=available&limit=100&offset=100&order=asc':()=>catalogJson([entry('github.com/org/second',{plugin_type:'middleware'})],150)};render(PluginsPage,{navigate:()=>{},mode:'create'});
  await fireEvent.click(await moduleSelect());expect(await screen.findByRole('option',{name:/second/})).toBeTruthy();
 });
 it('has no serious or critical structural accessibility violations',async()=>{
  handlers={...base([]),[availablePath]:()=>catalogJson([entry('github.com/org/fresh',{plugin_type:'middleware'})])};render(PluginsPage,{navigate:()=>{},mode:'create'});await moduleSelect();const axe=(await import('axe-core')).default;const result=await axe.run(document.body,{rules:{'color-contrast':{enabled:false}}});expect(result.violations.filter(v=>v.impact==='serious'||v.impact==='critical').map(v=>v.id)).toEqual([]);
 });
});

describe('update status and application routing',()=>{
 it('loads fresh metadata without a catalog warning or an automatic sync',async()=>{
  const fresh={...withUpdate(bouncer,'up_to_date'),update:{...withUpdate(bouncer,'up_to_date').update,checked_at:new Date().toISOString().slice(0,19).replace('T',' ')}};
  handlers=base([fresh]);render(PluginsPage,{navigate:()=>{}});await screen.findByText('Up to date');
  expect(screen.queryByText(/Update metadata is stale/)).toBeNull();expect(calls.some(c=>c.path.endsWith('/catalog/sync'))).toBe(false);
 });
 it('clears a previous sync error when refreshed metadata has recovered',async()=>{
  const fresh={...withUpdate(bouncer,'up_to_date'),update:{...withUpdate(bouncer,'up_to_date').update,checked_at:new Date().toISOString().slice(0,19).replace('T',' ')}};
  handlers={...base([fresh]),'POST /api/v1/plugins/catalog/sync':()=>err('ERR_PLUGIN_CATALOG_UNAVAILABLE',502,'Catalog refresh failed')};
  render(PluginsPage,{navigate:()=>{}});await screen.findByText('Up to date');
  await fireEvent.click(screen.getByRole('button',{name:'Check for updates',exact:true}));await screen.findByText('Catalog refresh failed');
  await fireEvent.click(screen.getByRole('button',{name:'Refresh',exact:true}));
  await waitFor(()=>expect(screen.queryByText('Catalog refresh failed')).toBeNull());
  await screen.findByText('Up to date');expect(screen.queryByText(/Update metadata is stale/)).toBeNull();
 });
 it('alerts on external updates while keeping changes disabled',async()=>{
  handlers=base([withUpdate(external,'update_available',false)]);render(PluginsPage,{navigate:()=>{}});expect(await screen.findByText('Update to v2.1.0')).toBeTruthy();expect(screen.queryByRole('button',{name:'Update',exact:true})).toBeNull();
 });
 it('shows the update in the version cell without a page warning',async()=>{
  handlers=base([withUpdate(bouncer)]);render(PluginsPage,{navigate:()=>{}});
  const latest=await screen.findByText('v2.1.0');expect(latest.className).toContain('latest');
  const current=latest.parentElement!.querySelector('.outdated')!;expect(current.textContent).toBe('v1.0.0');
  expect(screen.queryByText(/Newer versions are available/)).toBeNull();
 });
 it('reports unavailable checks instead of up-to-date',async()=>{
  handlers=base([{...bouncer,update:{state:'unknown',versions:[],can_update:false,last_attempt_failed:false}}]);render(PluginsPage,{navigate:()=>{}});expect(await screen.findByText('Check unavailable')).toBeTruthy();expect(screen.queryByText('Up to date')).toBeNull();expect(screen.getByText(/Update metadata is stale/)).toBeTruthy();
 });
 it('recomputes update alerts after explicit refresh',async()=>{
  let synced=false;handlers={...base([bouncer]),'GET /api/v1/plugins':()=>json([withUpdate(bouncer,synced?'update_available':'up_to_date')]),'POST /api/v1/plugins/catalog/sync':()=>{synced=true;return json({synced_count:1});}};render(PluginsPage,{navigate:()=>{}});await screen.findByText('Up to date');await fireEvent.click(screen.getByRole('button',{name:'Check for updates'}));expect(await screen.findByText('Update to v2.1.0')).toBeTruthy();
 });
 it('opens upgrade through the real SPA router',async()=>{
  window.location.hash='#/plugins';handlers={...base([withUpdate(bouncer)])};render(App);await fireEvent.click(await screen.findByRole('button',{name:'Update',exact:true}));expect(await screen.findByRole('heading',{name:'Update plugin'})).toBeTruthy();expect(window.location.hash).toBe('#/plugins/bouncer/update');expect(screen.queryByText('Not available yet')).toBeNull();
 });
});

describe('update state labels', () => {
	it('labels a plugin that the catalog does not list', async () => {
		handlers = base([withUpdate(bouncer, 'not_in_catalog')]);
		render(PluginsPage, { navigate: () => {} });
		expect(await screen.findByText('Not in catalog')).toBeTruthy();
	});
});

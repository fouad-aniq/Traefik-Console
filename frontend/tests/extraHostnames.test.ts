import { describe, expect, it } from 'vitest';
import { formFrom, previewRule, toInput } from '../src/features/routes/models';
import { extraHostErrors, routeErrors, type RouteMessages } from '../src/features/routes/validation';
import {
	buildChange,
	buildGraph,
	canConnect,
	canDisconnect,
	nodeId,
	type Inventory,
} from '../src/features/topology/graph';

const envelope = (kind: string, name: string, config: unknown) =>
	({
		id: `${kind}:${name}`,
		name,
		version: 2,
		origin: 'managed',
		provider: 'file',
		status: 'active',
		read_only: false,
		live_health: 'loaded',
		config,
	}) as any;

const DOMAINS: Record<string, { main: string; method: string; resolver?: string }> = {
	old: { main: 'example.com', method: 'resolver', resolver: 'le' },
	next: { main: '*.example.net', method: 'resolver', resolver: 'le' },
	other: { main: 'example.org', method: 'resolver', resolver: 'le-http' },
	intranet: { main: 'intranet.example.com', method: 'files' },
};
const mainOf = (n: string) => DOMAINS[n]?.main;
const resolverOf = (n: string) =>
	DOMAINS[n] ? (DOMAINS[n].method === 'resolver' ? (DOMAINS[n].resolver ?? '') : '') : undefined;
const messages: RouteMessages = {
	invalidName: 'name',
	nameTaken: 'taken',
	domainRequired: 'domain',
	invalidSubdomain: 'subdomain',
	invalidHosts: 'hosts',
	invalidPathPrefix: 'prefix',
	ruleRequired: 'rule',
	tooManyEntryPoints: 'eps',
	serviceRequired: 'service',
	duplicateMiddleware: 'dup',
	tooManyMiddlewares: 'many',
	invalidPriority: 'prio',
	invalidTlsOptions: 'opts',
	duplicateHost: 'twice',
	resolverMismatch: 'resolver',
};
const ctx = { creating: false, existingNames: [], domainNames: Object.keys(DOMAINS), mainOf, resolverOf };

const gitConfig = {
	entry_points: ['websecure'],
	domain_name: 'old',
	subdomain: 'git',
	extra_domains: [{ domain_name: 'next', subdomain: 'git' }],
	domains: ['git.example.com', 'git.example.net'],
	path_prefix: '/api',
	rule: '(Host(`git.example.com`) || Host(`git.example.net`)) && PathPrefix(`/api`)',
	service: 'gitea',
	tls: { enabled: true, cert_resolver: 'le' },
};

describe('router form with several hostnames', () => {
	it('reads, previews and sends the extra hostnames', () => {
		const form = formFrom('git', gitConfig);
		expect(form.extraHosts).toEqual([{ domainName: 'next', subdomain: 'git' }]);
		expect(previewRule(form, mainOf('old'), mainOf)).toBe(
			'(Host(`git.example.com`) || Host(`git.example.net`)) && PathPrefix(`/api`)',
		);
		form.extraHosts.push({ domainName: 'intranet', subdomain: '' });
		expect(toInput(form)).toMatchObject({
			domain_name: 'old',
			subdomain: 'git',
			extra_domains: [{ domain_name: 'next', subdomain: 'git' }, { domain_name: 'intranet' }],
			path_prefix: '/api',
		});
	});
	it('sends no extra hostnames when none are set or the match is not a domain', () => {
		const form = formFrom('git', { ...gitConfig, extra_domains: [] });
		expect(toInput(form)).not.toHaveProperty('extra_domains');
		form.extraHosts.push({ domainName: 'next', subdomain: 'git' });
		form.mode = 'host';
		form.hosts = 'git.example.com';
		expect(toInput(form)).not.toHaveProperty('extra_domains');
	});
	it('flags a missing domain, the same hostname twice and a second resolver', () => {
		const form = formFrom('git', gitConfig);
		form.extraHosts = [
			{ domainName: '', subdomain: 'x' },
			{ domainName: 'old', subdomain: 'GIT' },
			{ domainName: 'other', subdomain: 'git' },
			{ domainName: 'intranet', subdomain: 'git' },
			{ domainName: 'next', subdomain: 'bad_label' },
		];
		expect(extraHostErrors(form, ctx, messages)).toEqual(['domain', 'twice', 'resolver', '', 'subdomain']);
		expect(routeErrors(form, ctx, messages).extraHosts).toBe('domain');
		form.extraHosts = [{ domainName: 'next', subdomain: 'git' }];
		expect(routeErrors(form, ctx, messages).extraHosts).toBeUndefined();
	});
});

describe('map with several hostnames', () => {
	const inventory = (): Inventory => ({
		entryPoints: ['websecure'],
		resolvers: [envelope('resolver', 'le', { type: 'acme', challenge: 'dns01' })],
		certificates: [],
		domains: Object.entries(DOMAINS).map(([name, d]) =>
			envelope('domain', name, { name, sans: [], state: 'ready', ...d }),
		),
		services: [envelope('service', 'gitea', { servers: [{ url: 'http://gitea:3000' }] })],
		middlewares: [],
		chains: [],
		routers: [
			envelope('router', 'git', gitConfig),
			envelope('router', 'solo', { ...gitConfig, extra_domains: [], domains: ['git.example.com'] }),
		],
	});
	const node = (g: ReturnType<typeof buildGraph>, kind: any, name: string) => g.byId.get(nodeId(kind, name))!;
	const edge = (g: ReturnType<typeof buildGraph>, route: string, domain: string) =>
		g.edges.find(
			(e) =>
				e.relation === 'route-domain' && e.subject === nodeId('route', route) && e.object === nodeId('domain', domain),
		)!;

	it('links the route to every domain it answers on', () => {
		const g = buildGraph(inventory());
		expect(edge(g, 'git', 'old')).toBeTruthy();
		expect(edge(g, 'git', 'next')).toBeTruthy();
	});
	it('adds a dropped domain as another hostname, or moves the route when asked', () => {
		const g = buildGraph(inventory());
		const result = canConnect(g, node(g, 'route', 'solo'), node(g, 'domain', 'next'));
		if (!result.ok) throw new Error(result.error);
		const add = buildChange(result.connection, 'add', { subdomain: 'git' });
		expect(add.input).toMatchObject({
			domain_name: 'old',
			subdomain: 'git',
			extra_domains: [{ domain_name: 'next', subdomain: 'git' }],
		});
		const move = buildChange(result.connection, 'add', { subdomain: 'code', domainMode: 'replace' });
		expect(move.input).toMatchObject({ domain_name: 'next', subdomain: 'code' });
		expect(move.input).not.toHaveProperty('extra_domains');
	});
	it('removes one domain of several, and keeps the last one required', () => {
		const g = buildGraph(inventory());
		const main = canDisconnect(g, edge(g, 'git', 'old'));
		if (!main.ok) throw new Error(main.error);
		// The main domain goes: the extra hostname becomes the main one.
		expect(buildChange(main.connection, 'remove').input).toMatchObject({ domain_name: 'next', subdomain: 'git' });
		expect(buildChange(main.connection, 'remove').input).not.toHaveProperty('extra_domains');
		const extra = canDisconnect(g, edge(g, 'git', 'next'));
		if (!extra.ok) throw new Error(extra.error);
		expect(buildChange(extra.connection, 'remove').input).toMatchObject({ domain_name: 'old', subdomain: 'git' });
		expect(canDisconnect(g, edge(g, 'solo', 'old'))).toMatchObject({ ok: false, error: 'required' });
	});
});

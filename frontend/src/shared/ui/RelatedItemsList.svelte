<script lang="ts">
	/**
	 * List of referencing items: each with what identifies it (rule, hosts, type),
	 * its key facts. The name opens the item's view, where it can be edited. Used inside dialogs and dialog tabs.
	 */
	import { Route, Layers, Link, Globe, ShieldCheck, KeyRound, LogIn } from '@lucide/svelte';
	import { t } from '../localization/i18n';
	import type { RelatedItem } from './related';

	let {
		items,
		loading = false,
		empty,
		navigate,
	}: { items: RelatedItem[]; loading?: boolean; empty?: string; navigate: (p: string) => void } = $props();

	const icons = {
		route: Route,
		middleware: Layers,
		chain: Link,
		domain: Globe,
		certificate: ShieldCheck,
		resolver: KeyRound,
		entrypoint: LogIn,
	};
	const k = (key: string, args?: Record<string, string | number>) => t('related.' + key, args);
</script>

{#if loading}
	<p role="status" class="state">{k('loading')}</p>
{:else if !items.length}
	<p class="state">{empty ?? k('empty')}</p>
{:else}
	<ul class="related">
		{#each items as item (item.kind + ':' + item.name)}
			{@const Icon = icons[item.kind]}
			<li>
				<span class="kind-icon" aria-hidden="true"><Icon size={18} /></span>
				<div class="body">
					<div class="head">
						{#if item.path}<button type="button" class="name" onclick={() => navigate(item.path!)}>
								{item.name}
							</button>{:else}<span class="name static">{item.name}</span>{/if}
						<span class="kind">{k('kind.' + item.kind)}</span>
					</div>
					{#if item.summary}<code class="summary" title={item.summary}>{item.summary}</code>{/if}
					{#if item.facts?.length}
						<div class="facts">
							{#each item.facts as fact}<span>{fact}</span>{/each}
						</div>
					{/if}
				</div>
			</li>
		{/each}
	</ul>
{/if}

<style>
	.state {
		margin: 0;
		color: var(--text-muted);
		font-size: 14px;
	}
	.related {
		list-style: none;
		margin: 0;
		padding: 0;
		display: flex;
		flex-direction: column;
		gap: 8px;
	}
	li {
		display: flex;
		align-items: flex-start;
		gap: 12px;
		padding: 12px 14px;
		border: 1px solid var(--border);
		border-radius: var(--radius-card);
		background: var(--surface);
	}
	.kind-icon {
		display: inline-flex;
		align-items: center;
		justify-content: center;
		flex: none;
		width: 32px;
		height: 32px;
		border-radius: var(--radius-control);
		background: var(--connection-soft);
		color: var(--connection);
	}
	.body {
		flex: 1;
		min-width: 0;
		display: flex;
		flex-direction: column;
		gap: 4px;
	}
	.head {
		display: flex;
		align-items: center;
		gap: 8px;
		min-width: 0;
	}
	.name {
		padding: 0;
		border: 0;
		background: none;
		font: inherit;
		font-weight: 600;
		font-size: 14px;
		line-height: 20px;
		color: var(--accent);
		text-align: left;
		cursor: pointer;
		overflow-wrap: anywhere;
	}
	.name:hover {
		text-decoration: underline;
	}
	.name.static {
		color: var(--text);
		cursor: default;
	}
	.name.static:hover {
		text-decoration: none;
	}
	.name:focus-visible {
		outline: 2px solid var(--outline);
		outline-offset: 2px;
		border-radius: 2px;
	}
	.kind {
		flex: none;
		font-size: 11px;
		line-height: 16px;
		padding: 0 6px;
		border-radius: 4px;
		border: 1px solid var(--border);
		color: var(--text-muted);
		text-transform: uppercase;
		letter-spacing: 0.04em;
	}
	.summary {
		display: block;
		font-size: 12px;
		line-height: 18px;
		color: var(--text-muted);
		white-space: nowrap;
		overflow: hidden;
		text-overflow: ellipsis;
	}
	.facts {
		display: flex;
		flex-wrap: wrap;
		gap: 4px;
	}
	.facts span {
		font-size: 12px;
		line-height: 18px;
		padding: 0 6px;
		border-radius: 4px;
		background: var(--surface-muted);
		color: var(--text-muted);
	}
</style>

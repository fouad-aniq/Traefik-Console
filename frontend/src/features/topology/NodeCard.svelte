<script lang="ts">
	/** The visual of one object: coloured kind icon, name, technical one-liner. Used on the map and in the link dialog. */
	import { Lock } from '@lucide/svelte';
	import type { GraphNode } from './graph';
	import { KIND_ICON, kindColor } from './kinds';
	import { t } from '../../shared/localization/i18n';

	let {
		node,
		showKind = false,
	}: {
		node: GraphNode;
		/** print the kind above the name (dialog) instead of relying on the lane header */ showKind?: boolean;
	} = $props();

	const Icon = $derived(KIND_ICON[node.kind]);
	const subtitle = $derived(
		node.missing
			? t('topology.badge.missing')
			: node.kind === 'chain'
				? t(node.count === 1 ? 'topology.membersOne' : 'topology.members', { count: node.count })
				: node.kind === 'service' && node.count > 1
					? `${node.subtitle} ${t('topology.more', { count: node.count - 1 })}`
					: node.subtitle,
	);
	const HEALTH: Record<string, string> = {
		loaded: 'ok',
		enabled: 'ok',
		valid: 'ok',
		error: 'bad',
		expired: 'bad',
		warning: 'warn',
		expiring_soon: 'warn',
		expiring: 'warn',
		disabled: 'off',
	};
	const health = $derived(node.missing ? '' : (HEALTH[node.health] ?? ''));
</script>

<span class="node-card" class:missing={node.missing} class:external={node.external} style:--kind={kindColor(node.kind)}>
	<span class="glyph" aria-hidden="true"><Icon size={18} /></span>
	<span class="text">
		{#if showKind}<span class="kind">{t('topology.kind.' + node.kind)}</span>{/if}
		<span class="name">
			<span class="label">{node.name}</span>
			{#if node.protocol}<span class="tag">{node.protocol}</span>{/if}
			<!-- External names already end in @provider; the tag is only for references that omit it. -->
			{#if node.external && !node.name.includes('@')}<span class="tag">
					{node.provider || t('topology.badge.external')}
				</span>{/if}
			{#if node.kind === 'route' && node.tls}<Lock size={12} aria-label={t('topology.badge.tls')} />{/if}
		</span>
		{#if subtitle}<span class="sub">{subtitle}</span>{/if}
	</span>
	{#if health}<span class="health {health}" title={node.error || node.health}></span>{/if}
</span>

<style>
	.node-card {
		display: flex;
		align-items: center;
		gap: 10px;
		min-width: 0;
		width: 100%;
		text-align: left;
	}
	.glyph {
		display: grid;
		place-items: center;
		flex: none;
		width: 34px;
		height: 34px;
		border-radius: 8px;
		color: var(--kind);
		background: color-mix(in srgb, var(--kind) 14%, transparent);
	}
	.text {
		display: flex;
		flex-direction: column;
		min-width: 0;
		flex: 1;
	}
	.kind {
		font-size: 11px;
		line-height: 14px;
		letter-spacing: 0.04em;
		text-transform: uppercase;
		color: var(--kind);
		font-weight: 600;
	}
	.name {
		display: flex;
		align-items: center;
		gap: 6px;
		min-width: 0;
		font-size: 14px;
		line-height: 20px;
		font-weight: 600;
		color: var(--text);
	}
	.label {
		overflow: hidden;
		text-overflow: ellipsis;
		white-space: nowrap;
	}
	.name :global(svg) {
		flex: none;
		color: var(--success-text);
	}
	.tag {
		flex: none;
		padding: 0 5px;
		border: 1px solid var(--border);
		border-radius: 4px;
		font-size: 11px;
		line-height: 16px;
		font-weight: 500;
		color: var(--text-muted);
	}
	.sub {
		overflow: hidden;
		text-overflow: ellipsis;
		white-space: nowrap;
		font-size: 12px;
		line-height: 17px;
		color: var(--text-muted);
		font-family: ui-monospace, SFMono-Regular, Consolas, monospace;
	}
	.missing .glyph {
		color: var(--danger-text);
		background: var(--danger-bg);
	}
	/* External objects (not managed by the Console) get a dashed outline. */
	.external .glyph {
		outline: 1.5px dashed var(--kind);
		outline-offset: 2px;
	}
	.missing .sub {
		color: var(--danger-text);
		font-family: inherit;
	}
	.health {
		flex: none;
		width: 8px;
		height: 8px;
		border-radius: 50%;
		background: var(--outline);
	}
	.health.ok {
		background: var(--success-text);
	}
	.health.warn {
		background: var(--warning-text);
	}
	.health.bad {
		background: var(--danger-text);
	}
</style>

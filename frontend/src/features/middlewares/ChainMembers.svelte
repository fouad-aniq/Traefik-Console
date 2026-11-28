<script lang="ts">
	/**
	 * A chain's members in the table: a compact "N middlewares" label (the row never grows), and a
	 * list icon whose hover/focus panel shows every member in request order, top to bottom.
	 */
	import { ListTree } from '@lucide/svelte';
	import ValueTooltip from '../../shared/ui/ValueTooltip.svelte';
	import { t } from '../../shared/localization/i18n';

	let { members, label }: { members: string[]; label: string } = $props();

	const m = (key: string, args?: Record<string, string | number>) => t('middlewares.' + key, args);
	const split = (ref: string) => {
		const at = ref.lastIndexOf('@');
		const provider = at > 0 ? ref.slice(at + 1) : '';
		// A bare name and name@file are the same middleware: only other providers are worth showing.
		return { name: at > 0 ? ref.slice(0, at) : ref, provider: provider === 'file' ? '' : provider };
	};
	const count = $derived(m(members.length === 1 ? 'memberCountOne' : 'memberCount', { count: members.length }));
	/** Plain-text equivalent for assistive technology. */
	const plain = $derived(members.map((x, i) => `${i + 1}. ${x}`).join('\n'));
</script>

{#if members.length}
	<div class="members">
		<ValueTooltip label={`${label}: ${count}`} text={plain}>
			<span class="trigger"><span class="count">{count}</span><ListTree size={16} aria-hidden="true" /></span>
			{#snippet detail()}
				<div class="panel">
					<p class="panel-title">{m('requestOrder')}</p>
					<ol class="flow">
						{#each members as ref, i (i)}
							{@const item = split(ref)}
							<li>
								<span class="step numeric" aria-hidden="true">{i + 1}</span>
								<span class="member-name">{item.name}</span>
								{#if item.provider}<span class="provider">@{item.provider}</span>{/if}
							</li>
						{/each}
					</ol>
				</div>
			{/snippet}
		</ValueTooltip>
	</div>
{:else}
	<span class="none">—</span>
{/if}

<style>
	.members {
		display: flex;
		align-items: center;
		min-width: 0;
	}
	/* The whole label opens the panel; the icon sits on the text's centre line. */
	.trigger {
		display: inline-flex;
		align-items: center;
		gap: 6px;
		white-space: nowrap;
		line-height: 20px;
	}
	.trigger :global(svg) {
		flex: none;
		color: var(--text-muted);
	}
	.none {
		color: var(--text-muted);
	}

	.panel {
		display: flex;
		flex-direction: column;
		gap: 10px;
		min-width: 220px;
		max-width: 360px;
	}
	.panel-title {
		margin: 0;
		font-size: 11px;
		font-weight: 600;
		line-height: 16px;
		letter-spacing: 0.06em;
		text-transform: uppercase;
		color: var(--text-muted);
	}
	/* Long chains scroll inside the panel instead of covering the page. */
	.flow {
		list-style: none;
		margin: 0;
		padding: 0;
		max-height: 320px;
		overflow-y: auto;
	}
	.flow li {
		position: relative;
		display: flex;
		align-items: center;
		gap: 10px;
		padding: 4px 0;
		font-size: 13px;
		line-height: 20px;
	}
	/* The line joining the steps shows the order requests flow through the chain. */
	.flow li:not(:last-child)::after {
		content: '';
		position: absolute;
		left: 10px;
		top: 26px;
		bottom: -6px;
		width: 2px;
		border-radius: 1px;
		background: var(--border);
	}
	.step {
		position: relative;
		z-index: 1;
		display: inline-flex;
		align-items: center;
		justify-content: center;
		flex: none;
		width: 22px;
		height: 22px;
		border-radius: 50%;
		background: var(--connection-soft);
		color: var(--connection);
		font-size: 11px;
		font-weight: 600;
	}
	.member-name {
		font-weight: 500;
		color: var(--text);
		overflow-wrap: anywhere;
	}
	.provider {
		color: var(--text-muted);
		flex: none;
	}
</style>

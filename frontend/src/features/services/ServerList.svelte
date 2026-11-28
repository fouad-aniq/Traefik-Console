<script lang="ts">
	/**
	 * A service's backend servers in the table, in the same style as a chain's members: one server
	 * is shown as is; with more, the first one, "+N" and a list icon. The whole label opens a panel
	 * listing every server with its protocol. The row never grows.
	 */
	import { ListTree } from '@lucide/svelte';
	import ValueTooltip from '../../shared/ui/ValueTooltip.svelte';
	import { t } from '../../shared/localization/i18n';

	/** tag labels addresses that carry no scheme (TCP/UDP backends): tcp, tls or udp. */
	let { urls, label, tag = '' }: { urls: string[]; label: string; tag?: string } = $props();

	const sv = (key: string, args?: Record<string, string | number>) => t('serviceV2.' + key, args);
	/** Protocol and address, so the panel can show the protocol as a tag. */
	const split = (url: string) => {
		const m = /^([a-z][a-z0-9+.-]*):\/\/(.*)$/i.exec(url);
		return m ? { scheme: m[1].toLowerCase(), address: m[2] } : { scheme: tag, address: url };
	};
	const count = $derived(sv(urls.length === 1 ? 'serverCountOne' : 'serverCount', { count: urls.length }));
	/** Plain-text equivalent for assistive technology. */
	const plain = $derived(urls.join('\n'));
</script>

{#if urls.length === 1}
	<ValueTooltip text={urls[0]} truncate><span class="url">{urls[0]}</span></ValueTooltip>
{:else if urls.length > 1}
	<ValueTooltip label={`${label}: ${count}`} text={plain} truncate>
		<span class="trigger">
			<span class="url">{urls[0]}</span>
			<span class="more numeric">+{urls.length - 1}</span>
			<ListTree size={16} aria-hidden="true" />
		</span>
		{#snippet detail()}
			<div class="panel">
				<p class="panel-title">{count}</p>
				<ul class="list">
					{#each urls as url, i (i)}
						{@const item = split(url)}
						<li>
							{#if item.scheme}<span class="scheme">{item.scheme}</span>{/if}
							<span class="address">{item.address}</span>
						</li>
					{/each}
				</ul>
			</div>
		{/snippet}
	</ValueTooltip>
{:else}
	<span class="none">—</span>
{/if}

<style>
	/* The first address shrinks with an ellipsis; the count and icon always stay visible. */
	.trigger {
		display: flex;
		align-items: center;
		gap: 6px;
		min-width: 0;
		line-height: 20px;
	}
	.trigger .url {
		flex: 0 1 auto;
		min-width: 0;
		overflow: hidden;
		text-overflow: ellipsis;
		white-space: nowrap;
	}
	.more {
		flex: none;
		color: var(--text-muted);
		font-size: 13px;
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
		min-width: 240px;
		max-width: 420px;
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
	/* Many servers scroll inside the panel instead of covering the page. */
	.list {
		list-style: none;
		margin: 0;
		padding: 0;
		max-height: 320px;
		overflow-y: auto;
		display: flex;
		flex-direction: column;
		gap: 6px;
	}
	.list li {
		display: flex;
		align-items: center;
		gap: 10px;
		font-size: 13px;
		line-height: 20px;
	}
	.scheme {
		flex: none;
		min-width: 44px;
		padding: 0 6px;
		border-radius: 4px;
		background: var(--connection-soft);
		color: var(--connection);
		font-size: 11px;
		font-weight: 600;
		line-height: 20px;
		text-align: center;
	}
	.address {
		font-weight: 500;
		color: var(--text);
		overflow-wrap: anywhere;
	}
</style>

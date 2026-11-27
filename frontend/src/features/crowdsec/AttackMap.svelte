<script lang="ts">
	/**
	 * Where detected attacks come from: a choropleth of alert source countries over a static world
	 * outline (projected at build time by tools/crowdsec-map, loaded on demand). Counts are alerts,
	 * not visitors; the same data is available as a ranked list beside the map.
	 */
	import { onMount } from 'svelte';
	import { prefs } from '../../shared/preferences/preferences.svelte';
	import { t } from '../../shared/localization/i18n';
	import { countryName, heatLevel, type CountItem } from './models';

	let { countries, total }: { countries: CountItem[]; total: number } = $props();
	const c = (key: string, args?: Record<string, string | number>) => t('crowdsecV2.dashboard.' + key, args);

	type Shape = { code: string; name: string; d: string; c: number[] };
	let world = $state<{ width: number; height: number; countries: Shape[] } | null>(null);
	let failed = $state(false);
	let hover = $state<{ code: string; x: number; y: number } | null>(null);
	let host: HTMLDivElement;

	onMount(async () => {
		try {
			world = (await import('./worldMap.json')).default as typeof world;
		} catch {
			failed = true;
		}
	});

	const counts = $derived(new Map(countries.filter((i) => /^[A-Z]{2}$/.test(i.key)).map((i) => [i.key, i.count])));
	const max = $derived(Math.max(0, ...counts.values()));
	const unknown = $derived(countries.find((i) => i.key === '??')?.count ?? 0);
	const local = $derived(countries.find((i) => i.key === 'LOCAL')?.count ?? 0);
	const top = $derived(countries.filter((i) => /^[A-Z]{2}$/.test(i.key)).slice(0, 5));
	const label = (code: string) => countryName(code, prefs.language) || code;

	function pointer(event: PointerEvent) {
		const code = (event.target as Element | null)?.getAttribute?.('data-code');
		if (!code) {
			hover = null;
			return;
		}
		const box = host.getBoundingClientRect();
		hover = { code, x: event.clientX - box.left, y: event.clientY - box.top };
	}
</script>

<div class="map" bind:this={host}>
	{#if world}
		<svg viewBox="0 0 {world.width} {world.height}" role="img" aria-label={c('mapLabel', { count: counts.size })} onpointermove={pointer} onpointerleave={() => (hover = null)}>
			<g>
				{#each world.countries as shape, i (shape.code + i)}
					{@const count = counts.get(shape.code) ?? 0}
					<path d={shape.d} data-code={shape.code} class="land level-{heatLevel(count, max)}" class:active={hover?.code === shape.code}>
						<title>{label(shape.code)}: {c('alerts', { count })}</title>
					</path>
				{/each}
			</g>
			<g aria-hidden="true">
				{#each top as item, index (item.key)}
					{@const shape = world.countries.find((s) => s.code === item.key)}
					{#if shape}
						<circle cx={shape.c[0]} cy={shape.c[1]} r={6 + 10 * (item.count / (max || 1))} class="pulse" style="animation-delay:{index * 300}ms" />
						<circle cx={shape.c[0]} cy={shape.c[1]} r="3.5" class="dot" />
					{/if}
				{/each}
			</g>
		</svg>
		<!-- Alerts without a country (no GeoIP data, private sources) are attacks too: only an empty period says none. -->
		{#if !counts.size && !unknown && !local}<p class="empty">{c('noAttacks')}</p>{/if}
		{#if hover}
			<div class="tip" style="left:{hover.x}px;top:{hover.y}px" aria-hidden="true">
				<strong>{label(hover.code)}</strong>
				<span>{c('alerts', { count: counts.get(hover.code) ?? 0 })}</span>
			</div>
		{/if}
	{:else if failed}
		<p class="muted">{c('mapFailed')}</p>
	{:else}
		<div class="placeholder" role="status">{c('mapLoading')}</div>
	{/if}
</div>
<div class="legend" aria-hidden="true" class:faded={!counts.size}>
	<span>{c('fewer')}</span>
	{#each [1, 2, 3, 4, 5] as level (level)}<span class="swatch level-{level}"></span>{/each}
	<span>{c('more')}</span>
	{#if local}<span class="unknown">{c('localOrigin', { count: local })}</span>{/if}
	{#if unknown}<span class="unknown">{c('unknownOrigin', { count: unknown })}</span>{/if}
	{#if total && counts.size}<span class="unknown">{c('countriesCount', { count: counts.size })}</span>{/if}
</div>

<style>
	.map { position: relative; width: 100%; }
	svg { display: block; width: 100%; height: auto; }
	.land { stroke: var(--surface); stroke-width: 0.6; transition: fill 160ms ease, opacity 160ms ease; cursor: default; }
	.land.active { stroke: var(--text); stroke-width: 1.2; }
	.level-0 { --heat: var(--surface-muted); }
	.level-1 { --heat: color-mix(in srgb, var(--danger-text) 22%, var(--surface-muted)); }
	.level-2 { --heat: color-mix(in srgb, var(--danger-text) 40%, var(--surface-muted)); }
	.level-3 { --heat: color-mix(in srgb, var(--danger-text) 58%, var(--surface-muted)); }
	.level-4 { --heat: color-mix(in srgb, var(--danger-text) 76%, var(--surface-muted)); }
	.level-5 { --heat: var(--danger-text); }
	.land { fill: var(--heat); }
	.legend.faded { opacity: 0.5; }
	.swatch { background: var(--heat); }
	.dot { fill: var(--accent); stroke: var(--surface); stroke-width: 1.5; pointer-events: none; }
	.pulse { fill: color-mix(in srgb, var(--accent) 30%, transparent); stroke: var(--accent); stroke-width: 1; pointer-events: none; transform-box: fill-box; transform-origin: center; animation: pulse 2.4s ease-out infinite; }
	@keyframes pulse { 0% { opacity: 0.9; transform: scale(0.4); } 100% { opacity: 0; transform: scale(1.3); } }
	@media (prefers-reduced-motion: reduce) { .pulse { animation: none; opacity: 0.35; } }
	.tip { position: absolute; transform: translate(-50%, calc(-100% - 12px)); pointer-events: none; background: var(--text); color: var(--surface); padding: 6px 10px; border-radius: 6px; font-size: 13px; line-height: 18px; display: grid; white-space: nowrap; z-index: 2; box-shadow: 0 4px 14px rgb(0 0 0 / 0.18); }
	.tip span { opacity: 0.8; font-variant-numeric: tabular-nums; }
	.placeholder { aspect-ratio: 960 / 470; border-radius: 8px; background: linear-gradient(90deg, var(--surface-muted), var(--canvas), var(--surface-muted)); background-size: 200% 100%; animation: shimmer 1.4s linear infinite; color: transparent; }
	@keyframes shimmer { to { background-position: -200% 0; } }
	@media (prefers-reduced-motion: reduce) { .placeholder { animation: none; } }
	.legend { display: flex; flex-wrap: wrap; align-items: center; gap: 6px; margin-top: 12px; font-size: 12px; color: var(--text-muted); }
	.legend.faded { opacity: 0.5; }
	.swatch { width: 22px; height: 10px; border-radius: 2px; display: inline-block; }
	.unknown { margin-left: 12px; }
	.empty { position: absolute; left: 50%; bottom: 8%; transform: translateX(-50%); margin: 0; padding: 6px 12px; border-radius: 999px; background: var(--surface); border: 1px solid var(--border); font-size: 13px; color: var(--text-muted); white-space: nowrap; }
	.muted { color: var(--text-muted); font-size: 13px; margin: 0; }
</style>

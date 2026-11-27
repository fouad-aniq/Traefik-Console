<script lang="ts">
	/**
	 * CrowdSec overview: what CrowdSec measured in the selected period. Every number comes from
	 * GET /crowdsec/stats (alerts, decisions, bouncers, metrics); nothing is estimated.
	 */
	import { Activity, Ban, Crosshair, Globe, RadioTower } from '@lucide/svelte';
	import Notice from '../../shared/ui/Notice.svelte';
	import Button from '../../shared/ui/Button.svelte';
	import Surface from '../../shared/ui/Surface.svelte';
	import RelativeTime from '../../shared/ui/RelativeTime.svelte';
	import { prefs } from '../../shared/preferences/preferences.svelte';
	import { t } from '../../shared/localization/i18n';
	import SelectMenu from '../../shared/ui/SelectMenu.svelte';
	import AttackMap from './AttackMap.svelte';
	import AlertsPanel from './AlertsPanel.svelte';
	import { STATS_WINDOWS, barWidth, countryName, type CountItem, type CrowdSecStats, type StatsWindow } from './models';

	let { stats, loading, error, window: win, alerts = false, reloadKey = 0, onwindow, onrefresh, onsetup }: {
		stats: CrowdSecStats | null;
		/** The alert list is shown under the statistics when alerts can be read. */
		alerts?: boolean;
		reloadKey?: number;
		loading: boolean;
		error: string;
		window: StatsWindow;
		onwindow: (w: StatsWindow) => void;
		onrefresh: () => void;
		onsetup: () => void;
	} = $props();

	const c = (key: string, args?: Record<string, string | number>) => t('crowdsecV2.' + key, args);
	const d = (key: string, args?: Record<string, string | number>) => t('crowdsecV2.dashboard.' + key, args);
	const timeline = $derived(stats?.timeline ?? []);
	const peak = $derived(Math.max(0, ...timeline.map((p) => p.count)));
	const bouncers = $derived((stats?.bouncers ?? []).filter((b) => !b.revoked));
	/** Real countries only: LOCAL (private sources) and ?? (no GeoIP data) are listed but not counted as origins. */
	const countries = $derived((stats?.top_countries ?? []).filter((i) => /^[A-Z]{2}$/.test(i.key)));
	const topCountry = $derived(countries[0]);
	/**
	 * Public sources without any country: CrowdSec's GeoIP enrichment is off (or the -slim image has no
	 * GeoIP database). Everything else works; only the map and the country ranking stay empty.
	 */
	const noGeoip = $derived(!!stats && stats.alerts > 0 && countries.length === 0 && (stats.top_countries ?? []).some((i) => i.key === '??' && i.count > 0));
	const topScenario = $derived(stats?.top_scenarios?.[0]);
	const localBans = $derived((stats?.decisions_by_origin ?? []).filter((i) => !/capi|lists/i.test(i.key)).reduce((sum, i) => sum + i.count, 0));
	const fmt = (n: number) => n.toLocaleString(prefs.language);
	const pointLabel = (start: string) => {
		const date = new Date(start);
		return stats && stats.bucket_hours >= 24
			? date.toLocaleDateString(prefs.language, { month: 'short', day: 'numeric' })
			: date.toLocaleString(prefs.language, { month: 'short', day: 'numeric', hour: '2-digit', minute: '2-digit' });
	};
	const label = (key: string, kind: 'country' | 'plain') => {
		if (kind === 'plain') return key;
		const name = countryName(key, prefs.language);
		if (key === 'LOCAL') return d('localNetwork');
		return key === '??' ? c('unknownCountry') : name && name !== key ? name : key;
	};
	const scenarioName = (key: string) => key.includes('/') ? key.slice(key.indexOf('/') + 1) : key;

	/** Area path of the timeline in a 0..W × 0..H box (y grows down). */
	const W = 600, H = 160;
	const area = $derived.by(() => {
		if (!timeline.length || peak <= 0) return { line: '', fill: '' };
		const step = timeline.length > 1 ? W / (timeline.length - 1) : W;
		const pts = timeline.map((p, i) => [i * step, H - (p.count / peak) * (H - 8)] as const);
		const line = pts.map(([x, y], i) => `${i ? 'L' : 'M'}${x.toFixed(1)} ${y.toFixed(1)}`).join(' ');
		return { line, fill: `${line} L${W} ${H} L0 ${H} Z` };
	});
	const spark = $derived.by(() => {
		if (!timeline.length || peak <= 0) return '';
		const step = timeline.length > 1 ? 100 / (timeline.length - 1) : 100;
		return timeline.map((p, i) => `${i ? 'L' : 'M'}${(i * step).toFixed(1)} ${(28 - (p.count / peak) * 26).toFixed(1)}`).join(' ');
	});
	let hoverIndex = $state<number | null>(null);
	function hoverPlot(event: PointerEvent) {
		const box = (event.currentTarget as SVGElement).getBoundingClientRect();
		const ratio = (event.clientX - box.left) / box.width;
		hoverIndex = Math.max(0, Math.min(timeline.length - 1, Math.round(ratio * (timeline.length - 1))));
	}
</script>

{#snippet ranking(title: string, items: CountItem[] | null | undefined, kind: 'country' | 'plain', mono = false, limit = 8)}
	<Surface class="rank">
		<h3 class="card-title">{title}</h3>
		{#if items?.length}
			{@const max = Math.max(...items.map((i) => i.count))}
			<ol class="rank-list">
				{#each items.slice(0, limit) as item (item.key)}
					<li>
						<div class="rank-row">
							<span class="rank-key" class:mono title={item.key}>{#if kind === 'country' && /^[A-Z]{2}$/.test(item.key)}<span class="code">{item.key}</span>{/if}{kind === 'plain' && !mono ? scenarioName(item.key) : label(item.key, kind)}</span>
							<strong>{fmt(item.count)}</strong>
						</div>
						<div class="bar" aria-hidden="true"><span style="width:{barWidth(item.count, max)}%"></span></div>
					</li>
				{/each}
			</ol>
		{:else}
			<p class="muted">{c('stats.nothing')}</p>
		{/if}
	</Surface>
{/snippet}

<div class="toolbar">
	<div class="window">
		<label for="crowdsec-window">{c('stats.window')}</label>
		<SelectMenu id="crowdsec-window" value={win} options={STATS_WINDOWS.map((w) => ({ value: w, label: c('stats.windows.' + w) }))} disabled={loading} onchange={(v) => onwindow(v as StatsWindow)} />
	</div>
	<div class="toolbar-end">
		{#if stats?.generated_at}<span class="muted">{c('stats.updated')} <RelativeTime value={stats.generated_at} /></span>{/if}
	</div>
</div>

{#if error}
	<Notice tone={stats ? 'warning' : 'error'} title={stats ? c('stats.staleTitle') : ''} message={error}><Button size="sm" onclick={onrefresh}>{t('common.retry')}</Button></Notice>
{/if}
{#if loading && !stats}
	<div class="tiles" aria-busy="true">{#each [1, 2, 3, 4] as i (i)}<div class="skeleton"></div>{/each}</div>
	<p role="status" class="sr-only">{c('stats.loading')}</p>
{:else if stats && !stats.available}
	<Notice tone="info" title={c('stats.unavailableTitle')} message={d('unavailable')}>
		<Button size="sm" onclick={onsetup}>{d('openConnection')}</Button>
	</Notice>
{:else if stats}
	{#if stats.partial?.length}
		<Notice tone="warning" message={c('stats.partial', { sections: stats.partial.map((s) => c('stats.section.' + s)).join(', ') })} />
	{/if}
	<div class="tiles">
		<Surface class="tile">
			<div class="tile-head"><span class="tile-icon danger"><Crosshair size={18} aria-hidden="true" /></span><span class="tile-label">{c('stats.alerts')}</span></div>
			<strong class="tile-value">{fmt(stats.alerts)}{stats.truncated ? '+' : ''}</strong>
			{#if spark}<svg class="spark" viewBox="0 0 100 30" preserveAspectRatio="none" aria-hidden="true"><path d={spark} /></svg>{/if}
			<span class="muted">{d('alertsHelp', { window: d('window.' + win).toLowerCase() })}</span>
		</Surface>
		<Surface class="tile">
			<div class="tile-head"><span class="tile-icon accent"><Ban size={18} aria-hidden="true" /></span><span class="tile-label">{c('stats.bans')}</span></div>
			<strong class="tile-value">{fmt(stats.active_decisions)}</strong>
			<span class="muted">{d('bansHelp', { local: fmt(localBans), community: fmt(Math.max(0, stats.active_decisions - localBans)) })}</span>
		</Surface>
		<Surface class="tile">
			<div class="tile-head"><span class="tile-icon connection"><Globe size={18} aria-hidden="true" /></span><span class="tile-label">{d('origins')}</span></div>
			<strong class="tile-value">{fmt(countries.length)}</strong>
			<span class="muted">{topCountry ? d('topOrigin', { name: label(topCountry.key, 'country'), count: fmt(topCountry.count) }) : c('stats.nothing')}</span>
		</Surface>
		<Surface class="tile">
			<div class="tile-head"><span class="tile-icon neutral"><Activity size={18} aria-hidden="true" /></span><span class="tile-label">{d('topScenario')}</span></div>
			<strong class="tile-value small" title={topScenario?.key}>{topScenario ? scenarioName(topScenario.key) : '—'}</strong>
			<span class="muted">{topScenario ? c('stats.alertCount', { count: fmt(topScenario.count) }) : c('stats.nothing')}</span>
		</Surface>
		<Surface class="tile">
			<div class="tile-head"><span class="tile-icon success"><RadioTower size={18} aria-hidden="true" /></span><span class="tile-label">{c('stats.bouncers')}</span></div>
			<strong class="tile-value">{fmt(bouncers.length)}</strong>
			<span class="muted">{bouncers.length ? bouncers.map((b) => b.name).join(', ') : c('stats.noBouncers')}</span>
		</Surface>
	</div>

	<div class="map-row">
		<Surface class="map-card">
			<div class="card-head"><h3 class="card-title">{d('mapTitle')}</h3><span class="muted">{d('mapHelp')}</span></div>
			{#if noGeoip}<Notice tone="info" title={d('noGeoipTitle')} message={d('noGeoip')} />{/if}
			<AttackMap countries={stats.top_countries ?? []} total={stats.alerts} />
		</Surface>
		{@render ranking(c('stats.countries'), stats.top_countries, 'country', false, 8)}
	</div>

	<Surface class="chart">
		<div class="card-head">
			<h3 class="card-title">{c('stats.timeline', { unit: c(stats.bucket_hours >= 24 ? 'stats.perDay' : 'stats.perHour') })}</h3>
			{#if peak > 0}<span class="muted">{c('stats.peak', { count: fmt(peak) })}</span>{/if}
		</div>
		{#if peak > 0}
			<div class="plot-wrap">
				<svg class="plot" viewBox="0 0 {W} {H}" preserveAspectRatio="none" role="img" aria-label={c('stats.timelineLabel', { count: stats.alerts, peak })}
					onpointermove={hoverPlot} onpointerleave={() => (hoverIndex = null)}>
					{#each [0.25, 0.5, 0.75] as g (g)}<line x1="0" x2={W} y1={H * g} y2={H * g} class="grid-line" />{/each}
					<path d={area.fill} class="area" />
					<path d={area.line} class="line" vector-effect="non-scaling-stroke" />
					{#if hoverIndex !== null}
						{@const x = timeline.length > 1 ? (hoverIndex / (timeline.length - 1)) * W : 0}
						<line x1={x} x2={x} y1="0" y2={H} class="cursor" vector-effect="non-scaling-stroke" />
					{/if}
				</svg>
				{#if hoverIndex !== null}
					<div class="plot-tip" style="left:{timeline.length > 1 ? (hoverIndex / (timeline.length - 1)) * 100 : 0}%" aria-hidden="true">
						<strong>{fmt(timeline[hoverIndex].count)}</strong> <span>{pointLabel(timeline[hoverIndex].start)}</span>
					</div>
				{/if}
			</div>
			<div class="axis"><span>{pointLabel(timeline[0].start)}</span><span>{pointLabel(timeline[timeline.length - 1].start)}</span></div>
			<details class="timeline-data"><summary>{c('stats.timelineData')}</summary><table><thead><tr><th scope="col">{c('alerts.time')}</th><th scope="col">{c('stats.alerts')}</th></tr></thead><tbody>{#each timeline as point (point.start)}<tr><td>{pointLabel(point.start)}</td><td>{point.count}</td></tr>{/each}</tbody></table></details>
		{:else}
			<p class="muted">{c('stats.noAlerts')}</p>
		{/if}
	</Surface>

	<div class="grid">
		{@render ranking(c('stats.scenarios'), stats.top_scenarios, 'plain')}
		{@render ranking(c('stats.sources'), stats.top_sources, 'plain', true)}
		{@render ranking(c('stats.byOrigin'), stats.decisions_by_origin, 'plain', true)}
		{@render ranking(c('stats.byType'), stats.decisions_by_type, 'plain', true)}
	</div>

	{#if stats.metrics}
		<Surface class="metric-strip">
			<h3 class="card-title">{d('engineMetrics')}</h3>
			<dl>
				<div><dt>{c('integration.parsedLogs')}</dt><dd>{stats.metrics.parsed_access_log_events === null ? '—' : fmt(stats.metrics.parsed_access_log_events)}</dd><small>{c('integration.' + stats.metrics.ingestion_state)}</small></div>
				<div><dt>{c('integration.lapiRequests')}</dt><dd>{stats.metrics.lapi_requests === null ? '—' : fmt(stats.metrics.lapi_requests)}</dd><small>{c('integration.sinceStart')}</small></div>
				<div><dt>{c('integration.bouncerPulls')}</dt><dd>{stats.metrics.bouncer_pulls === null ? '—' : fmt(stats.metrics.bouncer_pulls)}</dd><small>{c('integration.sinceStart')}</small></div>
				<div><dt>{c('integration.appsecMetrics')}</dt><dd>{stats.metrics.appsec_processed === null ? '—' : fmt(stats.metrics.appsec_processed)} / {stats.metrics.appsec_blocked === null ? '—' : fmt(stats.metrics.appsec_blocked)}</dd><small>{c('integration.appsecMetricHelp')}</small></div>
			</dl>
		</Surface>
	{/if}
	{#if alerts}
		<section class="latest" aria-labelledby="crowdsec-latest-alerts">
			<h3 id="crowdsec-latest-alerts" class="card-title">{d('latestAlerts')}</h3>
			{#key reloadKey}<AlertsPanel />{/key}
		</section>
	{/if}
{/if}

<style>
	.toolbar { display: flex; flex-wrap: wrap; justify-content: space-between; align-items: end; gap: 12px 24px; margin-bottom: 16px; }
	.window { display: grid; gap: 6px; width: 220px; }
	.window label { font-size: 14px; font-weight: 500; }
	.toolbar-end { display: flex; align-items: center; gap: 16px; flex-wrap: wrap; }
	.muted { color: var(--text-muted); font-size: 13px; line-height: 19px; margin: 0; overflow-wrap: anywhere; }
	.sr-only { position: absolute; width: 1px; height: 1px; overflow: hidden; clip: rect(0 0 0 0); white-space: nowrap; }
	.tiles { display: grid; grid-template-columns: repeat(auto-fit, minmax(190px, 1fr)); gap: 16px; margin-bottom: 16px; }
	.skeleton { height: 132px; border-radius: 8px; background: linear-gradient(90deg, var(--surface-muted), var(--surface), var(--surface-muted)); background-size: 200% 100%; animation: shimmer 1.4s linear infinite; }
	@keyframes shimmer { to { background-position: -200% 0; } }
	@media (prefers-reduced-motion: reduce) { .skeleton { animation: none; } }
	:global(.tile) { display: grid; gap: 6px; align-content: start; position: relative; overflow: hidden; }
	.tile-head { display: flex; align-items: center; gap: 10px; }
	.tile-icon { display: grid; place-items: center; width: 32px; height: 32px; border-radius: 8px; background: var(--surface-muted); color: var(--text-muted); }
	.tile-icon.danger { background: var(--danger-bg); color: var(--danger-text); }
	.tile-icon.accent { background: var(--accent-soft); color: var(--accent); }
	.tile-icon.connection { background: var(--connection-soft); color: var(--connection); }
	.tile-icon.success { background: var(--success-bg); color: var(--success-text); }
	.tile-label { font-size: 13px; font-weight: 500; color: var(--text-muted); }
	.tile-value { font-size: 30px; line-height: 36px; font-weight: 600; letter-spacing: -0.02em; font-variant-numeric: tabular-nums; }
	.tile-value.small { font-size: 18px; line-height: 36px; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
	.spark { width: 100%; height: 30px; overflow: visible; }
	.spark path { fill: none; stroke: var(--danger-text); stroke-width: 1.5; vector-effect: non-scaling-stroke; }
	.map-row { display: grid; grid-template-columns: minmax(0, 2.2fr) minmax(260px, 1fr); gap: 16px; margin-bottom: 16px; }
	@media (max-width: 1099px) { .map-row { grid-template-columns: 1fr; } }
	.card-head { display: flex; flex-wrap: wrap; justify-content: space-between; align-items: baseline; gap: 4px 16px; margin-bottom: 12px; }
	.card-title { margin: 0 0 12px; font-size: 15px; font-weight: 600; }
	.card-head .card-title { margin: 0; }
	.latest { margin: 24px 0 16px; }
	:global(.chart) { margin-bottom: 16px; }
	.plot-wrap { position: relative; }
	.plot { display: block; width: 100%; height: 170px; cursor: crosshair; }
	.grid-line { stroke: var(--border); stroke-width: 1; stroke-dasharray: 3 4; vector-effect: non-scaling-stroke; }
	.area { fill: color-mix(in srgb, var(--danger-text) 16%, transparent); }
	.line { fill: none; stroke: var(--danger-text); stroke-width: 2; }
	.cursor { stroke: var(--text-muted); stroke-width: 1; }
	.plot-tip { position: absolute; top: 0; transform: translateX(-50%); background: var(--text); color: var(--surface); border-radius: 6px; padding: 4px 8px; font-size: 12px; white-space: nowrap; pointer-events: none; }
	.plot-tip span { opacity: 0.8; }
	.axis { display: flex; justify-content: space-between; margin-top: 8px; font-size: 12px; color: var(--text-muted); gap: 12px; }
	.timeline-data { margin-top: 12px; font-size: 14px; }
	.timeline-data summary { cursor: pointer; color: var(--text-muted); font-size: 13px; }
	.timeline-data table { width: 100%; margin-top: 12px; border-collapse: collapse; }
	.timeline-data th, .timeline-data td { text-align: left; padding: 8px; border-bottom: 1px solid var(--border); }
	.timeline-data th { color: var(--text-muted); font-weight: 500; }
	.grid { display: grid; grid-template-columns: repeat(auto-fit, minmax(240px, 1fr)); gap: 16px; margin-bottom: 16px; }
	.rank-list { list-style: none; margin: 0; padding: 0; display: grid; gap: 12px; }
	.rank-row { display: flex; justify-content: space-between; gap: 12px; font-size: 14px; font-variant-numeric: tabular-nums; }
	.rank-key { overflow: hidden; text-overflow: ellipsis; white-space: nowrap; min-width: 0; }
	.rank-key.mono { font-family: ui-monospace, SFMono-Regular, Menlo, monospace; font-size: 13px; }
	.code { display: inline-block; min-width: 26px; margin-right: 8px; font-family: ui-monospace, SFMono-Regular, Menlo, monospace; font-size: 11px; color: var(--text-muted); }
	.bar { height: 6px; background: var(--surface-muted); border-radius: 3px; margin-top: 4px; overflow: hidden; }
	.bar span { display: block; height: 100%; background: var(--connection); border-radius: 3px; }
	:global(.metric-strip) { margin-bottom: 16px; }
	:global(.metric-strip) dl { display: grid; grid-template-columns: repeat(auto-fit, minmax(180px, 1fr)); gap: 16px; margin: 0; }
	:global(.metric-strip) dl div { display: grid; gap: 2px; }
	:global(.metric-strip) dt { font-size: 13px; color: var(--text-muted); }
	:global(.metric-strip) dd { margin: 0; font-size: 22px; font-weight: 600; font-variant-numeric: tabular-nums; }
	:global(.metric-strip) small { font-size: 12px; color: var(--text-muted); }
</style>

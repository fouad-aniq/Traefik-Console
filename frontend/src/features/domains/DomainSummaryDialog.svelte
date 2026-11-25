<script lang="ts">
	/**
	 * Domain view: the saved choice, its certificate state and what Traefik serves. What uses the
	 * domain (routes, certificates) is shown by the table's "Used by" dialog, as on every page.
	 */
	import { Globe } from '@lucide/svelte';
	import ResourceDialog from '../../shared/ui/ResourceDialog.svelte';
	import Button from '../../shared/ui/Button.svelte';
	import Notice from '../../shared/ui/Notice.svelte';
	import SummaryList from '../../shared/ui/SummaryList.svelte';
	import StatusBadge from '../../shared/ui/StatusBadge.svelte';
	import RelativeTime from '../../shared/ui/RelativeTime.svelte';
	import { probeDomain } from './api';
	import { referenceOf, routersOf, type CertificateState, type DomainProbe, type DomainResource } from './models';
	import { validityTone } from '../certificates/models';
	import { errorMessage } from '../../shared/api/errorMessage';
	import { t } from '../../shared/localization/i18n';

	let { name, navigate, rows, loading, onreload }: {
		name: string; navigate: (p: string) => void; rows: DomainResource[]; loading: boolean; onreload: () => void;
	} = $props();

	const c = (key: string) => t('domainFlow.' + key);
	const u = (key: string) => t('domainUI.' + key);
	const v2 = (key: string, args?: Record<string, string | number>) => t('domainV2.' + key, args);
	const rv2 = (key: string) => t('resolverV2.' + key);

	let busy = $state(false);
	let probe = $state<DomainProbe | null>(null);
	let probeError = $state('');
	const controller = new AbortController();

	const domain = $derived(rows.find((r) => r.name === name));
	const routers = $derived(domain ? routersOf(domain) : []);
	const certState = $derived<CertificateState>(domain?.config.certificate_state ?? 'unavailable');
	// core-v2 probes a hostname one of the domain's routes serves, so it needs at least one route.
	const eligible = $derived(!!domain && routers.length > 0);
	const editable = $derived(!!domain && !domain.read_only);
	const refPath = $derived(domain ? (domain.config.method === 'resolver' ? '/resolvers/' + encodeURIComponent(domain.config.resolver ?? '') : '/certificates/uploaded/' + encodeURIComponent(domain.config.certificate ?? '')) : '');
	const stateTone = (s: CertificateState) => s === 'missingCertificate' || s === 'notCovered' ? 'danger' : s === 'notObserved' || s === 'unavailable' ? 'neutral' : validityTone(s);
	const stateLabel = (s: CertificateState) => (['valid', 'expiring', 'expired', 'not_yet_valid'].includes(s) ? t('certificateUI.' + s) : c(s));
	const probeLabel = (s: string) => { const k = 'certificateUI.probeStates.' + s; const v = t(k); return v === k ? s : v; };

	async function check() {
		if (!eligible || busy) return;
		busy = true; probeError = '';
		try { probe = await probeDomain(name, controller.signal); }
		catch (e) { probeError = errorMessage(e, c('probeFailed')); }
		finally { busy = false; }
	}
</script>

<ResourceDialog title={domain?.config.main || name} description={c('summaryHelp')} closeLabel={t('common.close')} onclose={() => { controller.abort(); navigate('/domains'); }}>
	{#snippet icon()}<Globe size={32} aria-hidden="true" />{/snippet}

	{#if loading}
		<p role="status">{c('loading')}</p>
	{:else if !domain}
		<Notice tone="warning" message={u('loadFailed')} />
		<div><Button onclick={onreload}>{t('common.retry')}</Button></div>
	{:else}
		<div class="stack">
			<SummaryList>
				<div><dt>{c('domainName')}</dt><dd>{domain.config.main}</dd></div>
				{#if domain.config.sans?.length}<div><dt>{c('additionalNames')}</dt><dd>{domain.config.sans.join(', ')}</dd></div>{/if}
				<div><dt>{c('savedMethod')}</dt><dd><StatusBadge size="sm" tone="connection" text={c(domain.config.method === 'resolver' ? 'automatic' : 'uploaded')} /></dd></div>
				<div><dt>{c(domain.config.method === 'resolver' ? 'resolver' : 'selectedCertificate')}</dt><dd><Button size="sm" onclick={() => navigate(refPath)}>{referenceOf(domain.config)}</Button></dd></div>
				<div><dt>{c('certificateState')}</dt><dd><StatusBadge size="sm" tone={stateTone(certState)} text={stateLabel(certState)} /></dd></div>
				{#if domain.config.entry_point}<div><dt>{v2('probeEntryPoint')}</dt><dd>{domain.config.entry_point}</dd></div>{/if}
				<div><dt>{rv2('stateColumn')}</dt><dd>{rv2('status.' + domain.status)}</dd></div>
			</SummaryList>
			<p class="help">{c('savedHelp')}</p>
			{#if domain.config.method === 'resolver' && !routers.length && !domain.config.main.startsWith('*.')}
				<div><Button onclick={() => navigate('/routers/new?domain=' + encodeURIComponent(domain.name))}>{u('createRoute')}</Button></div>
			{/if}
			<section class="observed">
				<h3>{c('traefikCheck')}</h3>
				{#if probe}
					<SummaryList>
						<div><dt>{c('result')}</dt><dd><StatusBadge size="sm" tone={probe.state === 'matches' ? 'success' : probe.state === 'mismatch' ? 'danger' : 'neutral'} text={probeLabel(probe.state)} /></dd></div>
						<div><dt>{c('checkedHost')}</dt><dd>{probe.hostname} <span class="muted">({probe.address})</span></dd></div>
						<div><dt>{c('checkedAt')}</dt><dd><RelativeTime value={probe.observed_at} /></dd></div>
						{#if probe.not_after}<div><dt>{c('expires')}</dt><dd><RelativeTime value={probe.not_after} /></dd></div>{/if}
						{#if probe.issuer}<div><dt>{c('issuer')}</dt><dd>{probe.issuer}</dd></div>{/if}
						{#if probe.state !== 'unreachable'}<div><dt>{v2('trust')}</dt><dd>{probe.trust}</dd></div>{/if}
					</SummaryList>
					{#if probe.error}<Notice tone="warning" message={probe.error} />{/if}
				{:else}
					<p class="help">{c(eligible ? 'notChecked' : 'checkUnavailable')}</p>
				{/if}
				{#if probeError}<Notice tone="warning" message={probeError} />{/if}
				{#if eligible}<div><Button onclick={check} pending={busy}>{c(probe ? 'checkAgain' : 'checkTraefik')}</Button></div>{/if}
			</section>
		</div>
	{/if}

	{#snippet footer()}
		<Button onclick={() => navigate('/domains')}>{t('common.close')}</Button>
		{#if editable && domain}<Button variant="primary" onclick={() => navigate('/domains/' + encodeURIComponent(name) + '/edit')}>{c('edit')}</Button>{/if}
	{/snippet}
</ResourceDialog>

<style>
	.stack { display: flex; flex-direction: column; gap: 24px; }
	.help { margin: 0; font-size: 14px; line-height: 21px; color: var(--text-muted); }
	.muted { color: var(--text-muted); }
	h3 { font-size: 16px; font-weight: 600; margin: 0; }
	.observed { border-top: 1px solid var(--border); padding-top: 20px; display: flex; flex-direction: column; gap: 16px; }
</style>

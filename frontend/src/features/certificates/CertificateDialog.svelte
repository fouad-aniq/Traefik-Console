<script lang="ts">
	import { ShieldCheck } from '@lucide/svelte';
	import ResourceDialog from '../../shared/ui/ResourceDialog.svelte';
	import Button from '../../shared/ui/Button.svelte';
	import Notice from '../../shared/ui/Notice.svelte';
	import SummaryList from '../../shared/ui/SummaryList.svelte';
	import StatusBadge from '../../shared/ui/StatusBadge.svelte';
	import RelativeTime from '../../shared/ui/RelativeTime.svelte';
	import { acmeKey, acmeNames, validityOf, validityTone, type AcmeCertificate, type CertificateResource } from './models';
	import { t } from '../../shared/localization/i18n';

	let {
		name,
		collection,
		navigate,
		automatic,
		files,
		loading,
		onreload,
		ondelete
	}: {
		name: string;
		collection: 'automatic' | 'uploaded';
		navigate: (p: string) => void;
		automatic: AcmeCertificate[];
		files: CertificateResource[];
		loading: boolean;
		onreload: () => void;
		ondelete: (file: CertificateResource) => void;
	} = $props();

	const c = (key: string, args?: Record<string, string | number>) => t('certificateFlow.' + key, args);
	const old = (key: string) => t('certificateUI.' + key);
	const v2 = (key: string, args?: Record<string, string | number>) => t('certV2.' + key, args);
	const rv2 = (key: string) => t('resolverV2.' + key);


	const file = $derived(collection === 'uploaded' ? files.find((f) => f.name === name) : undefined);
	const acme = $derived(collection === 'automatic' ? automatic.find((a) => acmeKey(a) === name) : undefined);
	const found = $derived(!!file || !!acme);
	const parent = $derived('/certificates/' + collection);
	const heading = $derived(file?.name ?? acme?.main_domain ?? name);
	const validity = $derived(file ? validityOf(file.config.status) : acme ? validityOf(acme.status) : 'valid');
	const names = $derived(file ? (file.config.domains ?? []) : acme ? acmeNames(acme) : []);
	const editable = $derived(!!file && !file.read_only);
</script>

<ResourceDialog title={heading} description={c('detailsHelp')} closeLabel={t('common.close')} onclose={() => navigate(parent)}>
	{#snippet icon()}<ShieldCheck size={32} aria-hidden="true" />{/snippet}

	{#if loading}
		<p role="status">{c('loading')}</p>
	{:else if !found}
		<Notice tone="warning" message={c('notObserved')} />
		<div><Button onclick={onreload}>{t('common.retry')}</Button></div>
	{:else}
		<div class="stack">
			{#if file?.config.purpose === 'ca'}<Notice message={c('trustFileHelp')} />{/if}
			<SummaryList>
				<div><dt>{c('source')}</dt><dd><StatusBadge size="sm" tone="connection" text={c(file ? 'uploaded' : 'automatic')} /></dd></div>
				<div><dt>{c('coveredNames')}</dt><dd>{names.join(', ') || '—'}</dd></div>
				<div><dt>{c('issuer')}</dt><dd>{(file?.config.issuer ?? acme?.issuer) || '—'}</dd></div>
				<div><dt>{c('validity')}</dt><dd><StatusBadge size="sm" tone={validityTone(validity)} text={old(validity)} /></dd></div>
				<div><dt>{c('validFrom')}</dt><dd><RelativeTime value={file?.config.not_before ?? acme?.not_before ?? ''} /></dd></div>
				<div><dt>{c('expires')}</dt><dd><RelativeTime value={file?.config.not_after ?? acme?.expires_at ?? ''} /></dd></div>
				{#if acme}<div><dt>{c('resolver')}</dt><dd><Button size="sm" onclick={() => navigate('/resolvers/' + encodeURIComponent(acme.resolver_name))}>{acme.resolver_name}</Button></dd></div>{/if}
				{#if file}<div><dt>{rv2('stateColumn')}</dt><dd>{rv2('status.' + file.status)}</dd></div>{/if}
			</SummaryList>
			<details class="technical">
				<summary>{c('technicalDetails')}</summary>
				<SummaryList>
					{#if file?.config.algorithm}<div><dt>{old('algorithm')}</dt><dd>{file.config.algorithm}</dd></div>{/if}
					{#if acme}<div><dt>{old('serial')}</dt><dd><code>{acme.serial_number}</code></dd></div>{/if}
					<div><dt>{old('fingerprint')}</dt><dd><code>{file?.config.fingerprint ?? acme?.fingerprint}</code></dd></div>
					{#if file}<div><dt>{v2('certFile')}</dt><dd><code>{file.config.cert_file}</code></dd></div>{/if}
				</SummaryList>
			</details>
		</div>
	{/if}

	{#snippet footer()}
		<Button onclick={() => navigate(parent)}>{t('common.close')}</Button>
		{#if editable && file}
			<Button variant="danger" onclick={() => { ondelete(file); navigate(parent); }}>{c('delete')}</Button>
			<Button variant="primary" onclick={() => navigate('/certificates/uploaded/' + encodeURIComponent(file.name) + '/replace')}>{c('replace')}</Button>
		{/if}
	{/snippet}
</ResourceDialog>

<style>
	.stack:not([hidden]) { display: flex; flex-direction: column; gap: 24px; }
	code { font-size: 12px; overflow-wrap: anywhere; }
	.technical { border-top: 1px solid var(--border); padding-top: 16px; }
	.technical summary { cursor: pointer; font-size: 14px; font-weight: 500; line-height: 24px; margin-bottom: 16px; }
</style>

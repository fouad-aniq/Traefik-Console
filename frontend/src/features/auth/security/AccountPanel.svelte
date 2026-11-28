<script lang="ts">
	import SummaryList from '../../../shared/ui/SummaryList.svelte';
	import StatusBadge from '../../../shared/ui/StatusBadge.svelte';
	import RelativeTime from '../../../shared/ui/RelativeTime.svelte';
	import { t } from '../../../shared/localization/i18n';
	import { auth } from '../session.svelte';

	const s = (key: string, args?: Record<string, string | number>) => t('authScreens.security.' + key, args);
	const user = $derived(auth.session?.user);
</script>

<div class="stack">
	<SummaryList>
		<div>
			<dt>{t('authScreens.usernameLabel')}</dt>
			<dd><strong>{user?.username}</strong> <StatusBadge size="sm" tone="neutral" text={s('role')} /></dd>
		</div>
		<div>
			<dt>{s('signedInWith')}</dt>
			<dd>{user?.method === 'oauth' ? user.provider || s('methodOauth') : s('methodPassword')}</dd>
		</div>
		<div>
			<dt>{s('sessionEnds')}</dt>
			<dd>
				{#if auth.session}<RelativeTime value={auth.session.expires_at} />{/if}
			</dd>
		</div>
		<div>
			<dt>{s('providersLabel')}</dt>
			<dd>{auth.providers.length ? auth.providers.map((p) => p.display_name).join(', ') : s('providersNone')}</dd>
		</div>
	</SummaryList>
</div>

<style>
	.stack {
		display: flex;
		flex-direction: column;
		gap: 20px;
		max-width: 880px;
	}
</style>

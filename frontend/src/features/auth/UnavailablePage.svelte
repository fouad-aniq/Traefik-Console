<script lang="ts">
	import AuthLayout from './AuthLayout.svelte';
	import Button from '../../shared/ui/Button.svelte';
	import { t } from '../../shared/localization/i18n';
	import { bootstrap } from './session.svelte';
</script>

<AuthLayout title={t('authScreens.unavailable.title')} lead={t('authScreens.unavailable.lead')}>
	<div class="retry"><Button variant="primary" onclick={() => bootstrap()}>{t('authScreens.unavailable.retry')}</Button></div>
</AuthLayout>

<style>
	.retry { margin-top: 24px; }
</style>

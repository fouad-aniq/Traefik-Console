<script lang="ts">
	import { CircleCheck, CircleAlert, Info, TriangleAlert, X } from '@lucide/svelte';
	import { flashes, dismissFlash } from './flash.svelte';
	import { t } from '../localization/i18n';
	const icons = { success: CircleCheck, info: Info, warning: TriangleAlert, error: CircleAlert };
</script>

<div class="flash-host" aria-live="polite" aria-relevant="additions">
	{#each flashes as item (item.id)}
		{@const Icon = icons[item.tone]}
		<div class="flash tone-{item.tone}" role={item.tone === 'error' || item.tone === 'warning' ? 'alert' : 'status'}>
			<span class="flash-icon" aria-hidden="true"><Icon size={18} /></span>
			<div class="flash-body">
				<strong>{item.title}</strong>
				{#if item.message}<span>{item.message}</span>{/if}
				{#if item.href}<a href={item.href} onclick={() => dismissFlash(item.id)}>{item.hrefLabel}</a>{/if}
			</div>
			<button type="button" class="flash-close" aria-label={t('common.close')} onclick={() => dismissFlash(item.id)}><X size={16} aria-hidden="true" /></button>
		</div>
	{/each}
</div>

<style>
	.flash-host { position: fixed; right: 24px; bottom: 24px; z-index: 200; display: flex; flex-direction: column; gap: 10px; width: min(380px, calc(100vw - 32px)); pointer-events: none; }
	.flash { pointer-events: auto; display: flex; align-items: flex-start; gap: 10px; padding: 12px 12px 12px 14px; background: var(--surface); border: 1px solid var(--border); border-left: 3px solid var(--flash-accent); border-radius: var(--radius-card); box-shadow: 0 12px 32px rgb(0 0 0 / .18); animation: flash-in 160ms ease-out; }
	.tone-success { --flash-accent: var(--success-text); }
	.tone-info { --flash-accent: var(--connection); }
	.tone-warning { --flash-accent: var(--warning-text); }
	.tone-error { --flash-accent: var(--danger-text); }
	.flash-icon { display: inline-flex; color: var(--flash-accent); padding-top: 1px; }
	.flash-body { flex: 1; min-width: 0; display: flex; flex-direction: column; gap: 2px; font-size: 13px; line-height: 18px; color: var(--text-muted); overflow-wrap: anywhere; }
	.flash-body strong { font-size: 14px; line-height: 20px; color: var(--text); font-weight: 600; }
	.flash-body a { align-self: flex-start; margin-top: 2px; font-weight: 500; }
	.flash-close { display: inline-flex; align-items: center; justify-content: center; width: 28px; height: 28px; border: 0; border-radius: var(--radius-control); background: transparent; color: var(--text-muted); cursor: pointer; }
	.flash-close:hover { background: var(--surface-muted); color: var(--text); }
	@keyframes flash-in { from { opacity: 0; transform: translateY(8px); } to { opacity: 1; transform: none; } }
	@media (prefers-reduced-motion: reduce) { .flash { animation: none; } }
	@media (max-width: 767px) { .flash-host { right: 16px; left: 16px; bottom: 16px; width: auto; } }
</style>

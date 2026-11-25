<script lang="ts">
	import type { Snippet } from 'svelte';

	/**
	 * Status badge per the STATE-CATALOG vocabulary. Tone is a color+icon hint;
	 * the label text always carries the meaning (never color-only).
	 */
	type Tone = 'neutral' | 'success' | 'warning' | 'danger' | 'info' | 'connection';

	let {
		tone = 'neutral',
  size = 'md',
		icon = '',
		text = '',
		children
	}: {
		tone?: Tone;
  size?: 'sm' | 'md';
		icon?: string;
		text?: string;
		children?: Snippet;
	} = $props();
</script>

<span class="badge status-badge tone-{tone}" class:compact={size==='sm'}>
	{#if icon}<span class="icon" aria-hidden="true">{icon}</span>{/if}
	<span>
		{#if children}
			{@render children()}
		{:else}
			{text}
		{/if}
	</span>
</span>

<style>
	.badge {
		display: inline-flex;
		align-items: center;
		gap: 4px;
		padding: 2px 10px;
		border-radius: 999px;
		font-size: 14px;
		line-height: 20px;
		border: 1px solid var(--border);
		background: var(--surface);
		color: var(--text);
		white-space: nowrap;
	}
	.compact { font-size:12px;line-height:18px;padding:1px 7px;border-radius:4px; }
 .tone-connection { color:var(--connection);background:var(--connection-soft);border-color:color-mix(in srgb,var(--connection) 35%,transparent); }
	.tone-success {
		background: var(--success-bg);
		color: var(--success-text);
		border-color: color-mix(in srgb, var(--success-text) 30%, transparent);
	}
	.tone-warning {
		background: var(--warning-bg);
		color: var(--warning-text);
		border-color: color-mix(in srgb, var(--warning-text) 30%, transparent);
	}
	.tone-danger {
		background: var(--danger-bg);
		color: var(--danger-text);
		border-color: color-mix(in srgb, var(--danger-text) 30%, transparent);
	}
	.tone-info {
		background: var(--accent-soft);
		color: var(--accent);
		border-color: color-mix(in srgb, var(--accent) 30%, transparent);
	}
	.icon {
		font-size: 12px;
	}
</style>

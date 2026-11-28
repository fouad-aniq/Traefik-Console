<script lang="ts">
	import type { Snippet } from 'svelte';

	/**
	 * Inline notice. Persistent by default (errors stay until resolved);
	 * transient confirmations belong in toasts, not here.
	 */
	type Tone = 'info' | 'warning' | 'error' | 'danger';

	let {
		tone = 'info',
		type,
		title = '',
		message = '',
		dismissible = false,
		onDismiss,
		ondismiss,
		children,
	}: {
		tone?: Tone;
		type?: Tone;
		title?: string;
		message?: string;
		dismissible?: boolean;
		onDismiss?: () => void;
		ondismiss?: () => void;
		children?: Snippet;
	} = $props();

	const finalTone = $derived(type === 'danger' ? 'error' : type || tone || 'info');
	const role = $derived(finalTone === 'error' || finalTone === 'danger' ? 'alert' : 'status');
	const handleDismiss = () => (ondismiss || onDismiss)?.();
</script>

<div class="notice tone-{finalTone}" {role}>
	{#if title || dismissible}
		<div class="head">
			{#if title}<strong class="title notice-title">{title}</strong>{/if}
			{#if dismissible}
				<button type="button" class="dismiss dismiss-btn" aria-label="Dismiss notice" onclick={handleDismiss}>✕</button>
			{/if}
		</div>
	{/if}
	<div class="body notice-message">
		{#if message}
			<p>{message}</p>
		{/if}
		{#if children}
			{@render children()}
		{/if}
	</div>
</div>

<style>
	.notice {
		border: 1px solid var(--border);
		border-radius: var(--radius-card);
		padding: 12px 16px;
		background: var(--surface);
	}
	.tone-info {
		background: var(--connection-soft);
		border-color: var(--connection-border);
	}
	.tone-warning {
		background: var(--warning-bg);
		border-color: color-mix(in srgb, var(--warning-text) 30%, transparent);
	}
	.tone-error {
		background: var(--danger-bg);
		border-color: color-mix(in srgb, var(--danger-text) 30%, transparent);
	}
	.head {
		display: flex;
		align-items: flex-start;
		justify-content: space-between;
		gap: 12px;
	}
	.title {
		font-size: 14px;
		line-height: 20px;
	}
	.body {
		margin-top: 4px;
		font-size: 14px;
		line-height: 20px;
		color: var(--text);
	}
	.dismiss {
		background: transparent;
		border: none;
		cursor: pointer;
		color: inherit;
		min-width: var(--touch-h);
		min-height: var(--touch-h);
		margin: -10px;
		font-size: 14px;
	}
</style>

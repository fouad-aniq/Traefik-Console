<script lang="ts">
	let {
		type = 'info',
		variant = '',
		title = '',
		message = '',
		dismissible = false,
		onDismiss = null,
		children,
	}: {
		type?: string;
		variant?: string;
		title?: string;
		message?: string;
		dismissible?: boolean;
		onDismiss?: (() => void) | null;
		children?: any;
	} = $props();

	const finalType = $derived(variant || type || 'info');

	const typeMap: Record<string, string> = {
		info: 'notice-info',
		warning: 'notice-warning',
		danger: 'notice-danger',
		success: 'notice-success',
	};
</script>

<div
	class="notice {typeMap[finalType] || 'notice-info'} variant-{finalType} {finalType === 'danger'
		? 'variant-danger variant-error'
		: ''}"
	role={finalType === 'danger' ? 'alert' : 'status'}
>
	<div class="notice-body">
		{#if title}
			<div class="notice-title text-sm">{title}</div>
		{/if}
		<div class="notice-message text-sm">
			{#if children}
				{@render children()}
			{:else}
				{message}
			{/if}
		</div>
	</div>
	{#if dismissible}
		<button class="dismiss-btn" onclick={onDismiss} aria-label="Dismiss notice">✕</button>
	{/if}
</div>

<style>
	.notice {
		display: flex;
		align-items: flex-start;
		justify-content: space-between;
		padding: 12px 16px;
		border-radius: 6px;
		margin-bottom: 16px;
		border: 1px solid transparent;
	}
	.notice-title {
		font-weight: 600;
		margin-bottom: 4px;
	}
	.notice-info {
		background: var(--accent-soft);
		color: var(--text);
		border-color: var(--accent);
	}
	.notice-warning {
		background: var(--warning-bg);
		color: var(--warning-text);
		border-color: var(--warning-text);
	}
	.notice-danger {
		background: var(--danger-bg);
		color: var(--danger-text);
		border-color: var(--danger-text);
	}
	.notice-success {
		background: var(--success-bg);
		color: var(--success-text);
		border-color: var(--success-text);
	}
	.dismiss-btn {
		background: transparent;
		border: none;
		cursor: pointer;
		font-size: 14px;
		color: inherit;
		padding: 0 4px;
	}
</style>

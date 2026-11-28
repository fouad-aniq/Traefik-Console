<script lang="ts">
	import type { Snippet } from 'svelte';

	type Variant = 'primary' | 'secondary' | 'danger' | 'ghost';
	type Size = 'md' | 'sm';

	let {
		variant = 'secondary',
		size = 'md',
		type = 'button',
		disabled = false,
		pending = false,
		pendingLabel = '',
		label = '',
		onclick,
		children,
	}: {
		variant?: Variant;
		size?: Size;
		type?: 'button' | 'submit';
		disabled?: boolean;
		pending?: boolean;
		pendingLabel?: string;
		label?: string;
		onclick?: (e: MouseEvent) => void;
		children?: Snippet;
	} = $props();

	const base: Record<Variant, string> = {
		primary: 'btn-primary',
		secondary: 'btn-secondary',
		danger: 'btn-danger',
		ghost: 'btn-ghost',
	};
</script>

<button
	{type}
	class={`${base[variant]} ${size === 'sm' ? 'btn-sm' : ''}`}
	{disabled}
	aria-busy={pending || undefined}
	{onclick}
>
	{#if pending}
		<span class="spinner" aria-hidden="true"></span>
		<span>
			{#if pendingLabel}
				{pendingLabel}
			{:else if children}
				{@render children()}
			{:else}
				{label}
			{/if}
		</span>
	{:else if children}
		{@render children()}
	{:else}
		{label}
	{/if}
</button>

<style>
	.btn-ghost {
		background: transparent;
		color: var(--accent);
		border: none;
		min-height: var(--control-h);
		padding: 8px 12px;
		border-radius: var(--radius-control);
		cursor: pointer;
		display: inline-flex;
		align-items: center;
		gap: 6px;
	}
	.btn-ghost:hover {
		background: var(--accent-soft);
	}
	.btn-ghost:disabled {
		opacity: 0.6;
		cursor: default;
	}
	.btn-sm {
		min-height: 32px;
		padding: 4px 10px;
		font-size: 14px;
	}
	.spinner {
		width: 14px;
		height: 14px;
		border: 2px solid currentColor;
		border-bottom-color: transparent;
		border-radius: 50%;
		animation: spin 0.8s linear infinite;
	}
	@keyframes spin {
		to {
			transform: rotate(360deg);
		}
	}
	@media (prefers-reduced-motion: reduce) {
		.spinner {
			animation-duration: 2.4s;
		}
	}
</style>

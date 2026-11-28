<script lang="ts">
	import type { Snippet } from 'svelte';

	/**
	 * Confirmation dialog: 480px panel, focus trapped while open, Escape
	 * cancels when safe, focus returns to the trigger element on close.
	 */
	let {
		open,
		title,
		message = '',
		cancelLabel = 'Cancel',
		confirmLabel = 'Confirm',
		danger = false,
		oncancel,
		onconfirm,
		children,
	}: {
		open: boolean;
		title: string;
		message?: string;
		cancelLabel?: string;
		confirmLabel?: string;
		danger?: boolean;
		oncancel: () => void;
		onconfirm: () => void;
		children?: Snippet;
	} = $props();

	let panel: HTMLDivElement | null = $state(null);
	let previouslyFocused: HTMLElement | null = null;

	$effect(() => {
		if (open) {
			previouslyFocused = document.activeElement as HTMLElement | null;
			queueMicrotask(() => {
				panel?.querySelector<HTMLElement>('button, [href], input, select, textarea')?.focus();
			});
			return () => {
				previouslyFocused?.focus();
				previouslyFocused = null;
			};
		}
	});

	function trapFocus(e: KeyboardEvent) {
		if (e.key === 'Escape') {
			e.stopPropagation();
			oncancel();
			return;
		}
		if (e.key !== 'Tab' || !panel) return;
		const focusables = panel.querySelectorAll<HTMLElement>(
			'button:not([disabled]), [href], input:not([disabled]), select:not([disabled]), textarea:not([disabled])',
		);
		if (focusables.length === 0) return;
		const first = focusables[0];
		const last = focusables[focusables.length - 1];
		if (e.shiftKey && document.activeElement === first) {
			e.preventDefault();
			last.focus();
		} else if (!e.shiftKey && document.activeElement === last) {
			e.preventDefault();
			first.focus();
		}
	}
</script>

<svelte:window onkeydown={open ? trapFocus : undefined} />

{#if open}
	<!-- Backdrop is a button on purpose: pointer dismissal only; keyboard
	     dismissal is Escape and the Cancel control. -->
	<button type="button" class="backdrop dialog-backdrop" aria-label={cancelLabel} tabindex="-1" onclick={oncancel}
	></button>
	<div class="panel dialog-panel" role="dialog" aria-modal="true" aria-label={title} bind:this={panel}>
		<h2 class="title">{title}</h2>
		<div class="body">
			{#if children}
				{@render children()}
			{:else}
				{message}
			{/if}
		</div>
		<div class="actions dialog-actions">
			<button type="button" class="btn-secondary" onclick={oncancel}>{cancelLabel}</button>
			<button type="button" class={danger ? 'btn-danger' : 'btn-primary'} onclick={onconfirm}>
				{confirmLabel}
			</button>
		</div>
	</div>
{/if}

<style>
	.backdrop {
		position: fixed;
		inset: 0;
		background: rgba(0, 0, 0, 0.45);
		border: none;
		padding: 0;
		z-index: 40;
		cursor: default;
	}
	.panel {
		position: fixed;
		z-index: 41;
		top: 50%;
		left: 50%;
		transform: translate(-50%, -50%);
		width: min(480px, calc(100vw - 32px));
		background: var(--surface);
		border: 1px solid var(--border);
		border-radius: var(--radius-dialog);
		box-shadow: 0 12px 32px rgba(0, 0, 0, 0.25);
		padding: 24px;
	}
	.title {
		margin: 0 0 12px;
		font-size: 20px;
		line-height: 28px;
		font-weight: 600;
	}
	.body {
		font-size: 14px;
		line-height: 20px;
		color: var(--text);
	}
	.actions {
		display: flex;
		justify-content: flex-end;
		gap: 12px;
		margin-top: 20px;
	}
</style>

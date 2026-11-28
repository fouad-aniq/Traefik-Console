<script lang="ts">
	import { onMount, tick, type Snippet } from 'svelte';
	import { X } from '@lucide/svelte';
	import TrafficMark from './TrafficMark.svelte';
	let {
		title,
		description = '',
		closeLabel,
		onclose,
		suspended = false,
		children,
		footer,
		navigation,
		icon,
	}: {
		title: string;
		/** one line under the title; leave out when it would only restate the title or the steps */ description?: string;
		closeLabel: string;
		onclose: () => void;
		suspended?: boolean;
		children: Snippet;
		footer: Snippet;
		navigation?: Snippet;
		icon?: Snippet;
	} = $props();
	let element: HTMLDialogElement;
	const uid = $props.id();
	let previous: HTMLElement | null = null;
	let mounted = $state(false);
	onMount(() => {
		previous = document.activeElement as HTMLElement | null;
		const overflow = document.body.style.overflow;
		document.body.style.overflow = 'hidden';
		mounted = true;
		return () => {
			element?.close();
			document.body.style.overflow = overflow;
			previous?.focus();
		};
	});
	$effect(() => {
		if (!mounted || !element) return;
		if (suspended) element.close();
		else {
			void tick().then(() => {
				if (!suspended && element?.isConnected && !element.open) {
					element.showModal();
					focusFirstField();
				}
			});
		}
	});
	/** Start in the form, not on the close button: the first editable field, else the scrollable body. */
	let focusedOnce = false;
	function focusFirstField() {
		if (focusedOnce) return;
		focusedOnce = true;
		requestAnimationFrame(() => {
			if (!element?.isConnected || !element.open) return;
			const body = element.querySelector<HTMLElement>('.dialog-content');
			const field = body?.querySelector<HTMLElement>(
				'input:not([disabled]):not([readonly]):not([type="hidden"]), textarea:not([disabled]):not([readonly]), [role="combobox"]:not([disabled])',
			);
			(field ?? body)?.focus({ preventScroll: true });
		});
	}
	function containTab(event: KeyboardEvent) {
		if (event.key !== 'Tab') return;
		const controls = [
			...element.querySelectorAll<HTMLElement>(
				'button:not([disabled]):not([tabindex="-1"]), input:not([disabled]), textarea:not([disabled]), select:not([disabled]), a[href], [tabindex="0"]',
			),
		].filter((node) => node.getClientRects().length);
		const first = controls[0],
			last = controls.at(-1);
		if (!first || !last) {
			event.preventDefault();
			element.focus();
			return;
		}
		if (event.shiftKey && (document.activeElement === first || document.activeElement === element)) {
			event.preventDefault();
			last.focus();
		} else if (!event.shiftKey && document.activeElement === last) {
			event.preventDefault();
			first.focus();
		}
	}
</script>

<dialog
	bind:this={element}
	class="resource-dialog"
	aria-labelledby={`${uid}-title`}
	aria-describedby={description ? `${uid}-description` : undefined}
	onkeydown={containTab}
	oncancel={(event: Event) => {
		if (event.target !== element) return;
		event.preventDefault();
		onclose();
	}}
>
	<header>
		<span class="resource-icon">
			{#if icon}{@render icon()}{:else}<TrafficMark size={44} />{/if}
		</span>
		<div class="heading">
			<h2 id={`${uid}-title`}>{title}</h2>
			{#if description}<p id={`${uid}-description`}>{description}</p>{/if}
		</div>
		<button class="close" type="button" aria-label={closeLabel} onclick={onclose}>
			<X size={20} aria-hidden="true" />
		</button>
	</header>
	{#if navigation}<div class="dialog-navigation">{@render navigation()}</div>{/if}
	<!-- The bounded body remains keyboard-scrollable even in read-only/review states. -->
	<!-- svelte-ignore a11y_no_noninteractive_tabindex -->
	<div class="dialog-content" role="region" aria-label={title} tabindex="0">{@render children()}</div>
	<footer>{@render footer()}</footer>
</dialog>

<style>
	.resource-dialog {
		width: min(760px, calc(100vw - 48px));
		max-width: none;
		max-height: calc(100dvh - 64px);
		margin: auto;
		padding: 0;
		border: 1px solid var(--border);
		border-top: 2px solid var(--connection);
		border-radius: 10px;
		background: var(--surface);
		color: var(--text);
		box-shadow: 0 24px 80px rgb(0 0 0 / 0.28);
		overflow: hidden;
	}
	.resource-dialog[open] {
		display: flex;
		flex-direction: column;
	}
	.resource-dialog::backdrop {
		background: rgb(0 0 0 / 0.65);
		backdrop-filter: blur(3px);
	}
	.dialog-navigation {
		background: var(--canvas);
		flex-shrink: 0;
		padding-inline: 32px;
		border-bottom: 1px solid var(--border);
	}
	.dialog-navigation :global(.tabs-bar) {
		border-bottom: 0;
	}
	header {
		display: flex;
		gap: 20px;
		align-items: center;
		padding: 24px 32px 20px;
		flex-shrink: 0;
		border-bottom: 1px solid var(--border);
	}
	.resource-icon {
		display: grid;
		place-items: center;
		flex-shrink: 0;
		width: 44px;
		height: 44px;
		border: 0;
		background: transparent;
		color: var(--connection);
		border-radius: 6px;
	}
	.heading {
		min-width: 0;
		flex: 1;
	}
	h2 {
		margin: 0;
		font-size: 20px;
		line-height: 28px;
		font-weight: 600;
	}
	p {
		margin: 4px 0 0;
		font-size: 14px;
		line-height: 20px;
		color: var(--text-muted);
	}
	.close {
		display: grid;
		place-items: center;
		background: transparent;
		border: 0;
		color: var(--text-muted);
		width: 36px;
		height: 36px;
		border-radius: 6px;
		cursor: pointer;
		flex-shrink: 0;
	}
	.close:hover {
		background: var(--surface-muted);
		color: var(--text);
	}
	.dialog-content:focus-visible {
		outline: none;
	}
	.dialog-content {
		overflow-y: auto;
		overscroll-behavior: contain;
		padding: 24px 32px 32px;
		min-height: 0;
		scroll-padding-block: 24px;
	}
	footer {
		display: flex;
		align-items: center;
		gap: 12px;
		padding: 16px 32px;
		background: var(--canvas);
		border-top: 1px solid var(--border);
		flex-shrink: 0;
	}
	footer:not(:has(:global(.dialog-note))) :global(button:last-child) {
		margin-left: auto;
	}
	footer :global(.dialog-note) {
		margin: 0 auto 0 0;
		font-size: 12px;
		line-height: 18px;
		color: var(--text-muted);
		max-width: 250px;
	}
	@media (max-width: 767px) {
		.resource-dialog {
			width: 100%;
			height: 100dvh;
			max-height: 100dvh;
			border-radius: 0;
			border: 0;
		}
		header {
			padding: 20px 16px;
			gap: 12px;
		}
		.resource-icon {
			width: 40px;
			height: 40px;
		}
		.close {
			width: 44px;
			height: 44px;
		}
		.dialog-navigation {
			padding-inline: 8px;
		}
		.dialog-content {
			flex: 1;
			padding: 24px 20px;
		}
		footer {
			padding: 16px 20px calc(16px + env(safe-area-inset-bottom));
			flex-wrap: wrap;
		}
		footer:not(:has(:global(.dialog-note))) :global(button:last-child) {
			margin-left: auto;
		}
		footer :global(.dialog-note) {
			max-width: none;
			flex-basis: 100%;
		}
		footer :global(button) {
			flex: 1;
			min-height: 44px;
		}
	}
</style>

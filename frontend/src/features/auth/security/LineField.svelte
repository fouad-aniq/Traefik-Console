<script lang="ts">
	/**
	 * One field per line: label (with an info tip) on the left, the control on
	 * the right, the error under the control. The info text is also wired to the
	 * control through aria-describedby, so screen readers get it without the tip.
	 * Stacks on narrow screens.
	 */
	import type { Snippet } from 'svelte';
	import { Info, CircleAlert } from '@lucide/svelte';
	import { t } from '../../../shared/localization/i18n';

	let {
		id,
		label,
		info = '',
		error = '',
		required = false,
		children,
	}: {
		id: string;
		label: string;
		info?: string;
		error?: string;
		required?: boolean;
		children: Snippet<[{ id: string; describedBy?: string; invalid: boolean }]>;
	} = $props();

	let open = $state(false);
	const describedBy = $derived(
		[info ? `${id}-info` : '', error ? `${id}-error` : ''].filter(Boolean).join(' ') || undefined,
	);
</script>

<div class="line" class:invalid={!!error}>
	<div class="label">
		<label for={id}>
			{label}{#if required}<span class="required" aria-hidden="true">*</span>{/if}
		</label>
		{#if info}
			<span class="tip-anchor">
				<button
					type="button"
					class="tip-button"
					aria-label={t('authScreens.providers.aboutField', { field: label })}
					aria-expanded={open}
					onclick={() => (open = !open)}
					onmouseenter={() => (open = true)}
					onmouseleave={() => (open = false)}
					onfocus={() => (open = true)}
					onblur={() => (open = false)}
					onkeydown={(e) => {
						if (e.key === 'Escape') open = false;
					}}
				>
					<Info size={15} aria-hidden="true" />
				</button>
				<span class="tip" id="{id}-info" role="tooltip" class:open>{info}</span>
			</span>
		{/if}
	</div>
	<div class="control">
		{@render children({ id, describedBy, invalid: !!error })}
		{#if error}
			<p class="error" id="{id}-error" role="alert"><CircleAlert size={14} aria-hidden="true" /><span>{error}</span></p>
		{/if}
	</div>
</div>

<style>
	.line {
		display: grid;
		grid-template-columns: 180px minmax(0, 1fr);
		gap: 8px 20px;
		align-items: start;
	}
	.label {
		display: flex;
		align-items: center;
		gap: 6px;
		min-height: var(--control-h);
	}
	label {
		font-size: 14px;
		font-weight: 500;
		line-height: 20px;
		color: var(--text);
	}
	.required {
		color: var(--danger-text);
		margin-inline-start: 4px;
	}

	.tip-anchor {
		position: relative;
		display: inline-flex;
	}
	.tip-button {
		display: inline-grid;
		place-items: center;
		width: 24px;
		height: 24px;
		padding: 0;
		border: none;
		border-radius: 50%;
		background: transparent;
		color: var(--text-muted);
		cursor: help;
	}
	.tip-button:hover,
	.tip-button[aria-expanded='true'] {
		color: var(--connection);
		background: var(--connection-soft);
	}
	.tip {
		position: absolute;
		left: 50%;
		bottom: calc(100% + 8px);
		transform: translateX(-30%);
		z-index: 20;
		width: max-content;
		max-width: 300px;
		padding: 8px 10px;
		border-radius: var(--radius-control);
		background: var(--text);
		color: var(--surface);
		font-size: 13px;
		line-height: 19px;
		font-weight: 400;
		box-shadow: 0 6px 18px rgba(0, 0, 0, 0.18);
		/* visually hidden until opened, but always present for aria-describedby */
		clip-path: inset(50%);
		opacity: 0;
		pointer-events: none;
	}
	.tip.open {
		clip-path: none;
		opacity: 1;
	}

	.control {
		min-width: 0;
		display: flex;
		flex-direction: column;
		gap: 6px;
	}
	.control :global(input:not([type='checkbox']):not([type='radio'])),
	.control :global(textarea) {
		width: 100%;
		min-width: 0;
		min-height: var(--control-h);
		padding: 7px 12px;
		background: var(--surface);
		color: var(--text);
		border: 1px solid var(--outline);
		border-radius: var(--radius-control);
		font: inherit;
		font-size: 16px;
		line-height: 24px;
	}
	.control :global(input:disabled) {
		opacity: 0.6;
	}
	.invalid .control :global(input:not([type='checkbox'])),
	.invalid .control :global(textarea) {
		border-color: var(--danger-text);
		box-shadow: 0 0 0 3px color-mix(in srgb, var(--danger-text) 16%, transparent);
	}
	.error {
		margin: 0;
		display: flex;
		align-items: flex-start;
		gap: 6px;
		color: var(--danger-text);
		font-size: 13px;
		line-height: 18px;
	}
	.error :global(svg) {
		flex: none;
		margin-top: 2px;
	}

	@media (max-width: 767px) {
		.line {
			grid-template-columns: 1fr;
			gap: 6px;
		}
		.label {
			min-height: 0;
		}
		.tip {
			transform: translateX(-50%);
		}
		.control :global(input:not([type='checkbox']):not([type='radio'])) {
			min-height: var(--touch-h);
		}
	}
</style>

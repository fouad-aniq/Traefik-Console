<script lang="ts">
	import { onMount } from 'svelte';
	import { t } from '../localization/i18n';
	import type { createYamlEditor } from './yamlEditor';
	let {
		id,
		label,
		value = $bindable(''),
		disabled = false,
		invalid = false,
		required = false,
		describedBy,
		placeholder = '',
		oninput = () => {},
	}: {
		id: string;
		label: string;
		value?: string;
		disabled?: boolean;
		invalid?: boolean;
		required?: boolean;
		describedBy?: string;
		placeholder?: string;
		oninput?: () => void;
	} = $props();
	let host: HTMLDivElement;
	let ready = $state(false);
	let editor: ReturnType<typeof createYamlEditor> | undefined;
	onMount(() => {
		let disposed = false;
		const labelElement = host.closest('.field')?.querySelector('label');
		const focusEditor = () => editor?.focus();
		labelElement?.addEventListener('click', focusEditor);
		void import('./yamlEditor')
			.then(({ createYamlEditor }) => {
				if (disposed) return;
				const restoreFocus = document.activeElement === host.parentElement?.querySelector('textarea');
				editor = createYamlEditor(host, {
					id,
					label,
					value,
					placeholder,
					disabled,
					invalid,
					required,
					describedBy,
					onchange(text) {
						value = text;
						oninput();
					},
				});
				ready = true;
				if (restoreFocus) editor.focus();
			})
			.catch(() => {
				host.replaceChildren(); /* Keep the editable textarea on a failed lazy load. */
			});
		return () => {
			disposed = true;
			labelElement?.removeEventListener('click', focusEditor);
			editor?.destroy();
		};
	});
	$effect(() => {
		if (ready && editor) {
			editor.setValue(value);
			editor.configure({ disabled, invalid, required, describedBy });
		}
	});
</script>

<div class="yaml-code-editor" class:invalid>
	<div bind:this={host}></div>
	{#if !ready}
		<textarea
			{id}
			aria-label={label}
			bind:value
			{oninput}
			{disabled}
			{required}
			{placeholder}
			aria-invalid={invalid || undefined}
			aria-describedby={describedBy}
			spellcheck="false"
			rows="14"></textarea>
	{/if}
</div>
<p class="editor-help">{t('middlewares.config.yamlEditorHelp')}</p>

<style>
	.yaml-code-editor {
		width: 100%;
		min-width: 0;
		border: 1px solid var(--outline);
		border-radius: var(--radius-control);
		overflow: hidden;
	}
	.yaml-code-editor:focus-within {
		outline: 2px solid var(--connection);
		outline-offset: 2px;
	}
	.yaml-code-editor.invalid {
		border-color: var(--danger-text);
	}
	textarea {
		display: block;
		width: 100%;
		min-height: 280px;
		font:
			14px/1.6 ui-monospace,
			monospace;
	}
	.editor-help {
		margin: 0;
		color: var(--text-muted);
		font-size: 12px;
		line-height: 18px;
	}
</style>

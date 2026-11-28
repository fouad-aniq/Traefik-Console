<script lang="ts">
	import { Eye, EyeOff } from '@lucide/svelte';
	import { t } from '../../shared/localization/i18n';
	import { MAX_PEM_BYTES } from './models';

	let {
		id,
		value = $bindable(''),
		secret = false,
		disabled = false,
		invalid = false,
		describedBy,
		onchange,
		onerror,
	}: {
		id: string;
		value?: string;
		secret?: boolean;
		disabled?: boolean;
		invalid?: boolean;
		describedBy?: string;
		onchange: () => void;
		onerror: (message: string) => void;
	} = $props();

	let revealed = $state(false);
	let filename = $state('');
	let fileInput = $state<HTMLInputElement>();

	async function upload(event: Event) {
		const input = event.currentTarget as HTMLInputElement;
		const file = input.files?.[0];
		if (!file) return;
		if (file.size > MAX_PEM_BYTES) {
			onerror(t('certificateUI.fileTooLarge'));
			input.value = '';
			return;
		}
		try {
			const text = await file.text();
			if (!disabled) {
				value = text;
				filename = file.name;
				onchange();
			}
		} catch {
			onerror(t('certificateUI.fileReadFailed'));
		}
	}
</script>

<div class="pem-control">
	<input
		{id}
		class="file-input"
		bind:this={fileInput}
		type="file"
		accept={secret ? '.pem,.key' : '.pem,.crt,.cer'}
		aria-label={t(secret ? 'certificateUI.uploadKey' : 'certificateUI.uploadChain')}
		{disabled}
		aria-invalid={invalid || undefined}
		aria-describedby={describedBy}
		onchange={upload}
	/>
	{#if value && !filename}<span class="file-status">{t('certificateFlow.pastedContent')}</span>{/if}
	{#if filename}<span class="file-status">{filename}</span>{/if}
	<details>
		<summary>{t('certificateFlow.pasteInstead')}</summary>
		<div class="pem-wrap">
			<textarea
				id={id + '-pem'}
				aria-label={t(secret ? 'certificateFlow.pasteKey' : 'certificateFlow.pasteChain')}
				bind:value
				rows={secret ? 4 : 6}
				class:concealed={secret && !revealed}
				{disabled}
				aria-invalid={invalid || undefined}
				aria-describedby={describedBy}
				spellcheck="false"
				autocomplete="off"
				placeholder={t(secret ? 'certificateUI.keyPlaceholder' : 'certificateUI.chainPlaceholder')}
				oninput={() => {
					filename = '';
					if (fileInput) fileInput.value = '';
					onchange();
				}}></textarea>
			{#if secret}
				<button
					type="button"
					class="eye"
					aria-label={t(revealed ? 'certificateUI.hideKey' : 'certificateUI.showKey')}
					aria-pressed={revealed}
					onclick={() => (revealed = !revealed)}
					{disabled}
				>
					{#if revealed}<EyeOff size={18} aria-hidden="true" />{:else}<Eye size={18} aria-hidden="true" />{/if}
				</button>
			{/if}
		</div>
	</details>
</div>

<style>
	.pem-control {
		width: 100%;
		display: flex;
		flex-direction: column;
		gap: 12px;
	}
	.file-status {
		color: var(--text-muted);
		font-size: 13px;
		overflow-wrap: anywhere;
	}
	summary {
		cursor: pointer;
		font-size: 14px;
		font-weight: 500;
		line-height: 24px;
		color: var(--text);
	}
	.pem-wrap {
		position: relative;
		margin-top: 12px;
	}
	textarea {
		display: block;
		width: 100%;
		min-width: 0;
		resize: vertical;
		padding: 10px 42px 10px 12px;
		border: 1px solid var(--outline);
		border-radius: var(--radius-control);
		background: var(--surface);
		color: var(--text);
		font:
			13px/21px ui-monospace,
			monospace;
	}
	.concealed {
		-webkit-text-security: disc;
	}
	textarea[aria-invalid='true'] {
		border-color: var(--danger-text);
		box-shadow: 0 0 0 3px color-mix(in srgb, var(--danger-text) 16%, transparent);
	}
	.eye {
		position: absolute;
		top: 4px;
		right: 4px;
		display: grid;
		place-items: center;
		width: 36px;
		height: 36px;
		border: 0;
		border-radius: 4px;
		background: var(--surface);
		color: var(--text-muted);
		cursor: pointer;
	}
	input.file-input {
		display: block;
		width: 100%;
		min-width: 0;
		min-height: 40px;
		padding: 0;
		font: inherit;
		font-size: 14px;
		line-height: 22px;
		background: var(--surface);
		color: var(--text-muted);
		border: 1px solid var(--outline);
		border-radius: var(--radius-control);
	}
	input.file-input[aria-invalid='true'] {
		border-color: var(--danger-text);
		box-shadow: 0 0 0 3px color-mix(in srgb, var(--danger-text) 16%, transparent);
	}
	input.file-input::file-selector-button {
		font: inherit;
		min-height: 38px;
		margin-right: 12px;
		padding: 7px 12px;
		border: 0;
		border-right: 1px solid var(--border);
		background: var(--surface-muted);
		color: var(--text);
		cursor: pointer;
	}
	@media (max-width: 767px) {
		.eye {
			width: 44px;
			height: 44px;
		}
		input.file-input {
			min-height: 44px;
		}
		input.file-input::file-selector-button {
			min-height: 42px;
		}
	}
</style>

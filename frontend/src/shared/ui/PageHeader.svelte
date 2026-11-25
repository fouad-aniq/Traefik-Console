<script lang="ts">
	import type { Snippet } from 'svelte';

	/** Page header: title, one-sentence subtitle, actions on the right. */
	let {
		title,
		subtitle = '',
		breadcrumb = '',
		actions
	}: {
		title: string;
		subtitle?: string;
		breadcrumb?: string;
		actions?: Snippet;
	} = $props();
</script>

<header class="page-header">
	<div>
		{#if breadcrumb}
			<nav aria-label="Breadcrumb" class="crumbs">
				<span class="text-muted text-sm">{breadcrumb}</span>
			</nav>
		{/if}
		<h1 class="page-title">{title}</h1>
		{#if subtitle}
			<p class="text-muted text-sm subtitle">{subtitle}</p>
		{/if}
	</div>
	{#if actions}
		<div class="actions">{@render actions()}</div>
	{/if}
</header>

<style>
 .page-header { margin-bottom: 24px; gap: 16px 24px; }
 .page-title { margin: 0; font-size: 26px; line-height: 34px; font-weight: 600; letter-spacing: -0.02em; }
 @media (max-width: 767px) { .page-header { margin-bottom: 20px; } }

	.crumbs {
		margin-bottom: 8px;
	}
	.subtitle {
		margin: 8px 0 0;
		max-width: 720px;
	}
	.actions {
		display: flex;
		align-items: center;
		gap: 12px;
	}
</style>

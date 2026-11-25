<script lang="ts">
	let { size = 32 }: { size?: number } = $props();
</script>

<svg width={size} height={size} viewBox="0 0 32 32" fill="none" aria-hidden="true" class="traffic-mark">
	<path d="M7 7v12a6 6 0 0 0 6 6h12M7 13h9a6 6 0 0 0 6-6" stroke="var(--connection)" stroke-width="1.75" stroke-linecap="round" />
	<circle cx="7" cy="6" r="3" fill="var(--accent)" />
	<circle cx="22" cy="6" r="3" fill="var(--surface)" stroke="var(--connection)" stroke-width="1.75" />
	<rect x="22" y="22" width="6" height="6" rx="1.5" fill="var(--surface)" stroke="var(--connection)" stroke-width="1.75" />
</svg>

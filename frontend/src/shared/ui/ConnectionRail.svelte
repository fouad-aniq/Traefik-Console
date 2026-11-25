<script lang="ts">
	let { label, steps }: { label: string; steps: { label: string; value: string }[] } = $props();
</script>

<div class="connection-rail" style:--rail-count={steps.length}>
	<p class="rail-caption">{label}</p>
	<ol>
		{#each steps as step (step.label)}
			<li><span class="rail-node" aria-hidden="true"></span><span class="rail-label">{step.label}</span><strong>{step.value}</strong></li>
		{/each}
	</ol>
</div>

<style>
	.connection-rail { min-width: 0; }
	.rail-caption { margin: 0 0 12px; font-size: 12px; line-height: 18px; color: var(--text-muted); }
	ol { position: relative; display: grid; grid-template-columns: repeat(var(--rail-count), minmax(0, 1fr)); list-style: none; margin: 0; padding: 0; }
	ol::before { content: ''; position: absolute; top: 3px; inset-inline: 0; height: 1px; background: var(--connection-border); }
	li { position: relative; display: flex; flex-direction: column; align-items: center; text-align: center; min-width: 0; padding: 16px 8px 0; }
	li:first-child { align-items: flex-start; text-align: left; padding-left: 0; }
	li:last-child { align-items: flex-end; text-align: right; padding-right: 0; }
	.rail-node { position: absolute; top: 0; left: calc(50% - 3.5px); width: 7px; height: 7px; border: 1px solid var(--connection); background: var(--surface); border-radius: 2px; }
	li:first-child .rail-node { left: 0; background: var(--connection); }
	li:last-child .rail-node { left: auto; right: 0; }
	.rail-label { font-size: 12px; line-height: 18px; color: var(--text-muted); }
	strong { font-size: 14px; line-height: 20px; font-weight: 500; overflow-wrap: anywhere; max-width: 100%; }
	@media (max-width: 480px) {
		ol { grid-template-columns: 1fr; }
		ol::before { display: none; }
		li, li:first-child, li:last-child { align-items: flex-start; text-align: left; padding: 0 0 16px 20px; }
		li::before { content: ''; position: absolute; inset: 5px auto 0 3px; width: 1px; background: var(--connection-border); }
		li:last-child { padding-bottom: 0; }
		li:last-child::before { display: none; }
		.rail-node, li:first-child .rail-node, li:last-child .rail-node { top: 5px; left: 0; right: auto; }
	}
</style>

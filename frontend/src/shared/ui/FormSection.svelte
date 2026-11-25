<script lang="ts">
 import type { Snippet } from 'svelte';
 /** A titled group of related fields; owns section and between-field spacing. */
 let { title, description = '', children }: { title: string; description?: string; children: Snippet } = $props();
</script>

<section class="form-section">
 <header>
  <h2>{title}</h2>
  {#if description}<p>{description}</p>{/if}
 </header>
 <div class="form-fields">{@render children()}</div>
</section>

<style>
 .form-section { width: 100%; max-width: 880px; border: 1px solid var(--border); border-radius: var(--radius-card); background: var(--surface); padding: 24px; }
 .form-section h2 { margin: 0; font-size: 20px; line-height: 28px; font-weight: 600; }
 .form-section p { margin: 8px 0 0; max-width: 560px; color: var(--text-muted); font-size: 14px; line-height: 20px; }
 .form-fields { margin-top: 24px; display: flex; flex-direction: column; gap: 24px; }
 @media (max-width: 767px) { .form-section { padding: 16px; } }
</style>

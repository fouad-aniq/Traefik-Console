<script lang="ts">
 import type {Snippet} from 'svelte';
 let {children, flush=false, class:className='', labelledby}: {children:Snippet;flush?:boolean;class?:string;labelledby?:string}=$props();
</script>
<section class="surface {className}" class:flush aria-labelledby={labelledby}>{@render children()}</section>
<style>
 .surface { min-width:0; background:var(--surface); color:var(--text); border:1px solid var(--border); border-top-color:var(--connection-border); border-radius:8px; padding:24px; }
 .surface.flush { padding:0; overflow:hidden; }
 .surface > :global(header.surface-header) { padding:20px 24px; background:var(--canvas); border-bottom:1px solid var(--border); }
 .surface > :global(div.surface-body) { padding:24px; }
 .surface > :global(footer.surface-footer) { padding:16px 24px; background:var(--canvas); border-top:1px solid var(--border); display:flex; flex-wrap:wrap; gap:12px; align-items:center; }
 .surface :global(h3) { margin-top:0; }
 @media(max-width:767px) { .surface { padding:16px; }
  .surface > :global(header.surface-header),.surface > :global(div.surface-body),.surface > :global(footer.surface-footer) { padding:16px; }
  .surface > :global(footer.surface-footer) :global(.left-actions),.surface > :global(footer.surface-footer) :global(.right-actions) { display:flex; flex-wrap:wrap; gap:8px; min-width:0; }
  .surface > :global(footer.surface-footer) :global(.right-actions) { flex:1; }
  .surface > :global(footer.surface-footer) :global(button) { min-height:44px; }
 }
</style>

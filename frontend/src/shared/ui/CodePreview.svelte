<script lang="ts">
 import { collapseDiff, diffRows, yamlTokens } from './codeTokens';
 import { t } from '../localization/i18n';
 let {code,language='diff',label,id,context=3}: {code:string;language?:'diff'|'yaml';label:string;id?:string;context?:number}=$props();
 let full=$state(false);
 let expanded=$state<number[]>([]);
 let tokens=$derived(language==='yaml'?yamlTokens(code):[]);
 let rows=$derived(language==='diff'?diffRows(code):[]);
 let items=$derived(language==='diff'?collapseDiff(rows,context):[]);
 let hasGaps=$derived(items.some(i=>i.kind==='gap'));
 const count=(n:number)=>t(n===1?'common.diffUnchangedOne':'common.diffUnchanged',{count:n});
 const expand=(gap:number)=>{expanded=[...expanded,gap];};
</script>
{#if language==='diff' && hasGaps}
 <div class="toolbar"><button type="button" class="toggle" onclick={()=>{full=!full;}}>{t(full?'common.diffShowChanges':'common.diffShowFull')}</button></div>
{/if}
<!-- svelte-ignore a11y_no_noninteractive_tabindex -->
<pre {id} tabindex="0" role="region" aria-label={label} class:diff={language==='diff'}><code>{#if language==='diff'}{#if full}{#each rows as row}<span class="row {row.kind}"><span class="mark" aria-hidden="true">{row.marker}</span>{#each row.tokens as token}<span class={token.kind}>{token.text}</span>{/each}</span>{/each}{:else}{#each items as item}{#if item.kind==='row'}<span class="row {item.row.kind}"><span class="mark" aria-hidden="true">{item.row.marker}</span>{#each item.row.tokens as token}<span class={token.kind}>{token.text}</span>{/each}</span>{:else if expanded.includes(item.id)}{#each item.rows as row}<span class="row {row.kind}"><span class="mark" aria-hidden="true">{row.marker}</span>{#each row.tokens as token}<span class={token.kind}>{token.text}</span>{/each}</span>{/each}{:else}<button type="button" class="gap" onclick={()=>expand(item.id)}>⋯ {count(item.rows.length)}</button>{/if}{/each}{/if}{:else}{#each tokens as token}<span class={token.kind}>{token.text}</span>{/each}{/if}</code></pre>
<style>
 .toolbar{display:flex;justify-content:flex-end;margin-bottom:4px}
 .toggle{border:0;background:transparent;padding:2px 4px;color:var(--connection);font:inherit;font-size:13px;cursor:pointer}
 .toggle:hover{text-decoration:underline}
 pre{margin:0;min-width:0;max-width:100%;max-height:420px;overflow:auto;padding:16px 0;border:1px solid var(--border);border-radius:6px;background:var(--canvas);color:var(--text);font:12px/20px ui-monospace,SFMono-Regular,Consolas,monospace;tab-size:2;white-space:pre}
 code{display:block;min-width:max-content;padding:0 16px;font:inherit}
 .diff code{padding:0}
 .row{display:block;min-height:20px;padding:0 16px 0 0}
 .mark{display:inline-block;width:32px;padding-right:8px;text-align:right;user-select:none;color:var(--text-muted)}
 .row.added{background:var(--success-bg)}.row.added .mark{color:var(--success-text)}
 .row.removed{background:var(--danger-bg)}.row.removed .mark{color:var(--danger-text)}
 .row.meta{padding-left:16px}.row.meta .mark{display:none}
 .gap{display:block;width:100%;margin:2px 0;padding:2px 16px 2px 40px;border:0;background:var(--surface-muted);color:var(--text-muted);font:inherit;text-align:left;cursor:pointer}
 .gap:hover{color:var(--connection)}
 .hunk,.key,.reference{color:var(--connection)}.string{color:var(--success-text)}.literal{color:var(--accent)}.comment{color:var(--text-muted)}
 pre:focus-visible{outline:2px solid var(--connection);outline-offset:2px}
</style>

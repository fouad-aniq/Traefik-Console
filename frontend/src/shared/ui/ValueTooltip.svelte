<script lang="ts">
 import { onMount, onDestroy, tick, type Snippet } from 'svelte';
 /** `detail` renders structured content in the tooltip instead of `text` (which stays the plain-text equivalent). */
 let { text, truncate=false, timestamp=false, children, onactivate, label, compact=false, detail }: {text:string;truncate?:boolean;timestamp?:boolean;children:Snippet;onactivate?:()=>void;label?:string;compact?:boolean;detail?:Snippet}=$props();
 let tooltip = $state<HTMLDivElement>();
 let trigger = $state<HTMLButtonElement>();
 let content = $state<HTMLSpanElement>();
 let eligible = $state(false);
 let disposed = false;
 let left = $state(0), top = $state(0);
 let overTrigger = false, overTooltip = false;
 let hideTimer: ReturnType<typeof setTimeout> | undefined;
 const id = $props.id();
 onMount(()=>{
  const resize=new ResizeObserver(updateEligibility);
  if(trigger)resize.observe(trigger);
  if(content)resize.observe(content);
  const changes=new MutationObserver(updateEligibility);
  if(content)changes.observe(content,{childList:true,subtree:true,characterData:true});
  updateEligibility();
  return()=>{resize.disconnect();changes.disconnect();};
 });
 $effect(()=>{void text;void timestamp;void truncate;void tick().then(()=>{if(!disposed)updateEligibility();});});
 onDestroy(()=>{disposed=true;clearTimeout(hideTimer);});
 function updateEligibility(){
  if(!content)return false;
  const normalize=(value:string)=>value.replace(/\s+/g,' ').trim();
  const clipped=content.scrollWidth>content.clientWidth+1;
  const extra=normalize(text)!==normalize(content.textContent||'');
  // Nothing to reveal when the full value is empty (a placeholder such as — is shown instead).
  eligible=normalize(text)!=='' && (timestamp||clipped||extra);
  if(!eligible)hide();
  return eligible;
 }
 function show(){
  clearTimeout(hideTimer);
  if(!tooltip || !trigger || !updateEligibility())return;
  if(typeof tooltip.showPopover==='function')tooltip.showPopover();
  const anchor=trigger.getBoundingClientRect(), box=tooltip.getBoundingClientRect();
  left=Math.max(8,Math.min(anchor.left,window.innerWidth-box.width-8));
  top=anchor.top-box.height-8>=8 ? anchor.top-box.height-8 : anchor.bottom+8;
 }
 function hide(){clearTimeout(hideTimer);if(tooltip && typeof tooltip.hidePopover==='function')tooltip.hidePopover();}
 function scheduleHide(){clearTimeout(hideTimer);hideTimer=setTimeout(()=>{if(!overTrigger && !overTooltip && document.activeElement!==trigger)hide();},150);}
</script>
 <button bind:this={trigger} class="tooltip-trigger" class:interactive={!!onactivate||eligible} class:timestamp class:truncate class:compact type="button" aria-label={label} aria-describedby={eligible?id:undefined} style:cursor={onactivate||eligible?'pointer':'default'} onmouseenter={()=>{overTrigger=true;show();}} onmouseleave={()=>{overTrigger=false;scheduleHide();}} onfocus={show} onblur={scheduleHide} onclick={()=>onactivate?onactivate():show()} onkeydown={(event)=>{if(event.key==='Escape')hide();}}><span bind:this={content} class="tooltip-content">{@render children()}</span></button>
 <div bind:this={tooltip} {id} class="value-tooltip" class:time-tooltip={timestamp} class:rich={!!detail} role="tooltip" popover="auto" onmouseenter={()=>{overTooltip=true;show();}} onmouseleave={()=>{overTooltip=false;scheduleHide();}} style:left={`${left}px`} style:top={`${top}px`}>{#if detail}{@render detail()}{:else}{text}{/if}</div>
<style>
 .tooltip-trigger {display:inline-block;max-width:calc(100% + 20px);border:0;border-radius:6px;background:transparent;color:inherit;font:inherit;padding:8px 10px;margin-inline:-10px;cursor:pointer;text-align:left;vertical-align:middle;}
 .compact {display:inline-flex;align-items:center;justify-content:center;width:32px;min-width:32px;min-height:32px;padding:6px;margin:0;}
 @media(max-width:767px){.compact{width:44px;min-width:44px;min-height:44px;}}
 .truncate {display:block;width:calc(100% + 20px);}
 .truncate .tooltip-content {display:block;overflow:hidden;text-overflow:ellipsis;white-space:nowrap;}
 .timestamp .tooltip-content {white-space:nowrap;display:inline-flex;align-items:center;gap:8px;}
 .tooltip-trigger.interactive:hover,.tooltip-trigger:focus-visible {background:var(--surface-muted);}
 .tooltip-trigger:focus-visible {outline:2px solid var(--outline);outline-offset:2px;}
 .value-tooltip.rich {white-space:normal;padding:12px 14px;}
 .value-tooltip {position:fixed;inset:auto;margin:0;max-width:min(560px,calc(100vw - 16px));padding:10px 12px;border:1px solid var(--border);border-radius:6px;background:var(--surface);color:var(--text);font-size:13px;box-shadow:0 4px 12px #0003;overflow-wrap:anywhere;white-space:pre-line;}
</style>

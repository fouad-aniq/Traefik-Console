<script lang="ts">
 import { onMount } from 'svelte';
 import { Clock3 } from '@lucide/svelte';
 import { prefs } from '../preferences/preferences.svelte';
 import { t } from '../localization/i18n';
 import { relativeDate, fullDate } from './relativeTime';
 import ValueTooltip from './ValueTooltip.svelte';
 let { value }: { value?: string | null } = $props();
 let now = $state(Date.now());
 const exact = $derived(fullDate(value,prefs.language));
 onMount(()=>{const timer=setInterval(()=>{now=Date.now();},60000);return()=>clearInterval(timer);});
</script>
{#if exact}
 <ValueTooltip text={exact} timestamp><Clock3 size={14} aria-hidden="true" /><time datetime={value ?? undefined}>{relativeDate(value,prefs.language,now,t('common.justNow'))}</time></ValueTooltip>
{:else}<span>—</span>{/if}

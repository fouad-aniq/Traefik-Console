<script lang="ts">
 /** "Used by" dialog: a RelatedItemsList inside a resource dialog. */
 import { Link2 } from '@lucide/svelte';
 import ResourceDialog from './ResourceDialog.svelte';
 import Button from './Button.svelte';
 import RelatedItemsList from './RelatedItemsList.svelte';
 import { t } from '../localization/i18n';
 import type { RelatedItem } from './related';

 let { title, description, items, loading = false, navigate, onclose }:
  { title: string; description: string; items: RelatedItem[]; loading?: boolean; navigate: (p: string) => void; onclose: () => void } = $props();
</script>

<ResourceDialog {title} {description} closeLabel={t('common.close')} {onclose}>
 {#snippet icon()}<Link2 size={32} aria-hidden="true" />{/snippet}
 <RelatedItemsList {items} {loading} {navigate} />
 {#snippet footer()}<Button onclick={onclose}>{t('common.close')}</Button>{/snippet}
</ResourceDialog>

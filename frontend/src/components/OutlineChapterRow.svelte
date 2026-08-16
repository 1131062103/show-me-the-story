<script>
  import { t } from '../lib/i18n/index.js';

  export let ch;
  export let statusMeta = {};
  export let editable = false;
  export let onEdit = () => {};
</script>

<!-- svelte-ignore a11y-click-events-have-key-events -->
<!-- svelte-ignore a11y-no-static-element-interactions -->
<div
  data-outline-chapter={ch.num}
  class="bg-base-300 rounded-lg p-2.5 group {editable ? 'cursor-pointer hover:ring-1 hover:ring-primary/40' : ''} transition-shadow"
  on:click={() => editable && onEdit()}
>
  <div class="flex items-center gap-2">
    <span class="text-sm font-bold text-base-content/40 w-14 shrink-0">{$t('outline.chapter.chapterLabel', { num: ch.num })}</span>
    <span class="text-sm font-medium flex-1 min-w-0 truncate">{ch.title}</span>
    <span class="badge badge-xs {statusMeta[ch.status]?.cls || 'badge-ghost'}">{statusMeta[ch.status]?.label || ch.status}</span>
    {#if editable}
      <span class="text-xs text-primary opacity-0 group-hover:opacity-100 transition-opacity shrink-0">{$t('outline.chapter.editTag')}</span>
    {:else}
      <span class="text-xs text-base-content/45 opacity-0 group-hover:opacity-100 transition-opacity shrink-0">{$t('outline.chapter.previewTag')}</span>
    {/if}
  </div>
  {#if ch.characters?.length}
    <div class="flex flex-wrap gap-1 mt-1.5 ml-10">
      {#each ch.characters as c}
        <span class="badge badge-ghost badge-xs gap-0.5" title={c.note || ''}>
          {c.name}{#if c.first_appearance}<span class="text-warning">*</span>{/if}
        </span>
      {/each}
    </div>
  {/if}
</div>
<script>
  export let options = [];  // [{gid, label}]
  export let value = [];    // array of gid numbers (mutated in place via callbacks)
  export let disabled = false;

  function toggle(gid) {
    if (disabled) return;
    const idx = value.indexOf(gid);
    if (idx >= 0) {
      value.splice(idx, 1);
    } else {
      value.push(gid);
    }
    value = value; // trigger reactivity
  }
</script>

<div class="flex flex-wrap gap-1.5">
  {#each options as o (o.gid)}
    <button
      type="button"
      class="badge badge-sm px-2.5 py-1.5 cursor-pointer border {value.includes(o.gid) ? 'badge-primary border-primary text-primary-content' : 'badge-ghost border-base-content/20 hover:border-primary/50'}"
      class:opacity-50={disabled}
      on:click={() => toggle(o.gid)}
      disabled={disabled}
      title={o.label}
    >{o.label}</button>
  {/each}
</div>

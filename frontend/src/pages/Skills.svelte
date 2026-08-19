<script>
  import { onMount } from 'svelte';
  import { api } from '../lib/api.js';
  import { skills, addToast, taskRunning } from '../lib/stores.js';
  import { t } from '../lib/i18n/index.js';

  let builtinSkills = [];
  let externalSkills = [];

  $: {
    builtinSkills = $skills.filter(s => s.skill.source !== 'external');
    externalSkills = $skills.filter(s => s.skill.source === 'external');
  }

  onMount(async () => {
    await loadSkills();
  });

  async function loadSkills() {
    try { skills.set(await api('GET', '/api/skills')); } catch (e) {}
  }

  async function toggleSkill(id, enabled) {
    try {
      await api('PUT', '/api/skills/' + id + '/toggle', { enabled });
      addToast(enabled ? $t('skills.toast.enabled') : $t('skills.toast.disabled'), 'success');
      await loadSkills();
    } catch (e) { addToast(e.message, 'error'); }
  }
</script>

<div class="card bg-base-200 shadow-sm">
  <div class="card-body">
    <h2 class="card-title">{$t('skills.title')}</h2>
    <p class="text-sm text-base-content/60 mb-3">{$t('skills.intro')}</p>

    <h3 class="text-base font-semibold mt-2 mb-1">{$t('skills.section.builtin')}</h3>
    <div class="overflow-x-auto mb-6">
      <table class="table table-sm">
        <thead>
          <tr>
            <th>{$t('skills.col.name')}</th>
            <th>{$t('skills.col.category')}</th>
            <th>{$t('skills.col.description')}</th>
            <th>{$t('skills.col.source')}</th>
            <th>{$t('skills.col.enabled')}</th>
          </tr>
        </thead>
        <tbody>
          {#if builtinSkills.length === 0}
            <tr><td colspan="5" class="text-center text-base-content/50 py-8">{$t('skills.empty')}</td></tr>
          {:else}
            {#each builtinSkills as sv}
              <tr>
                <td class="font-medium">{sv.skill.name}</td>
                <td>{sv.skill.category}</td>
                <td class="text-base-content/60 max-w-md truncate">{sv.skill.description}</td>
                <td>{sv.skill.source === 'builtin' ? $t('skills.source.builtin') : $t('skills.source.project')}</td>
                <td>
                  <input
                    type="checkbox"
                    class="toggle toggle-primary toggle-sm"
                    checked={sv.enabled}
                    disabled={$taskRunning}
                    on:change={e => toggleSkill(sv.skill.id, e.target.checked)}
                  />
                </td>
              </tr>
            {/each}
          {/if}
        </tbody>
      </table>
    </div>

    <h3 class="text-base font-semibold mt-2 mb-1">{$t('skills.section.external')}</h3>
    <p class="text-sm text-base-content/60 mb-2">{$t('skills.external.hint')}</p>
    <div class="overflow-x-auto">
      <table class="table table-sm">
        <thead>
          <tr>
            <th>{$t('skills.col.name')}</th>
            <th>{$t('skills.col.description')}</th>
            <th>{$t('skills.col.enabled')}</th>
          </tr>
        </thead>
        <tbody>
          {#if externalSkills.length === 0}
            <tr><td colspan="3" class="text-center text-base-content/50 py-8">{$t('skills.external.empty')}</td></tr>
          {:else}
            {#each externalSkills as sv}
              <tr>
                <td class="font-medium">{sv.skill.name}</td>
                <td class="text-base-content/60 max-w-md truncate">{sv.skill.description}</td>
                <td>
                  <input
                    type="checkbox"
                    class="toggle toggle-primary toggle-sm"
                    checked={sv.enabled}
                    disabled={$taskRunning}
                    on:change={e => toggleSkill(sv.skill.id, e.target.checked)}
                  />
                </td>
              </tr>
            {/each}
          {/if}
        </tbody>
      </table>
    </div>
  </div>
</div>
<script>
  import { onMount } from 'svelte';
  import { api } from '../lib/api.js';
  import { config, progress, settings, editingCharID, editingWvID, wvFilter, addToast, showConfirm, taskRunning } from '../lib/stores.js';
  import { t } from '../lib/i18n/index.js';
  import ConfigChangePanel from '../components/ConfigChangePanel.svelte';
  import ActSelector from '../components/ActSelector.svelte';

  export let sendToChat = async () => {};

  function stripNameMarks(name) {
    return (name.startsWith('「') && name.endsWith('」')) ? name.slice(1, -1) : name;
  }

  let showCharForm = false;
  let showWvForm = false;
  let charCollapse = false;
  let wvCollapse = false;

  let charName = '', charAge = '', charAppearance = '', charPersonality = '', charBackground = '', charMotivation = '', charAbilities = '', charNotes = '';
  let charActs = [];
  let wvName = '', wvCategory = 'other', wvDescription = '', wvTags = '';
  let wvActs = [];

  // 组织管理
  let showOrgForm = false, orgCollapse = false;
  let orgName = '', orgType = '', orgDescription = '';
  let orgMembers = [];
  let orgActs = [];
  let editingOrgID = null;

  // 关系管理
  let showRelForm = false, relCollapse = false;
  let relSource = '', relTarget = '', relLabel = '';
  let editingRelID = null;

  // 幕归属选项：按全书扁平顺序给出全局幕编号（供设定按幕生效）
  $: actOptions = (() => {
    const out = [];
    const arcs = $progress?.arcs || [];
    arcs.forEach((arc, i) => {
      (arc.acts || []).forEach((act, j) => {
        out.push({ gid: out.length + 1, label: `${i + 1}-${j + 1}${act.title ? ' ' + act.title : ''}` });
      });
    });
    return out;
  })();

  $: hasActs = actOptions.length > 0;

  $: actLabelByGid = Object.fromEntries(actOptions.map(o => [o.gid, o.label]));
  function actsLabel(acts) {
    if (!acts?.length) return '';
    return acts.map(g => actLabelByGid[g] || ('幕 ' + g)).join(', ');
  }

  let localStoryCfg = { type: '', title: '', chapter_count: 30, target_words_per_chapter: 2500, writing_style: '', writing_pov: '', story_synopsis: '' };

  let storyCfgSnapshot = '';

  $: if ($config?.story) {
    const snap = JSON.stringify($config.story);
    if (snap !== storyCfgSnapshot) {
      localStoryCfg = { ...$config.story };
      storyCfgSnapshot = snap;
    }
  }

  $: hasAccepted = $progress?.chapters?.some(c => c.status === 'accepted') || false;

  $: chars = ($settings?.characters || []);
  $: allWvs = ($settings?.worldview || []);
  $: filteredWvs = $wvFilter === 'all' ? allWvs : allWvs.filter(w => w.category === $wvFilter);
  $: orgs = ($settings?.organizations || []);
  $: rels = ($settings?.relations || []);

  const entityIcons = { character: '👤', organization: '🏛️', worldview: '🌍' };

  // 关系双方可选实体（角色 / 组织 / 世界观条目）
  $: entityOptions = [
    ...chars.map(c => ({ key: 'character:' + c.id, label: '👤 ' + c.name })),
    ...orgs.map(o => ({ key: 'organization:' + o.id, label: '🏛️ ' + o.name })),
    ...allWvs.map(w => ({ key: 'worldview:' + w.id, label: '🌍 ' + w.name })),
  ];

  $: nameById = (() => {
    const m = {};
    chars.forEach(c => m[c.id] = c.name);
    orgs.forEach(o => m[o.id] = o.name);
    allWvs.forEach(w => m[w.id] = w.name);
    return m;
  })();

  $: catLabels = {
    geography: $t('config.wv.cat.geography'),
    faction: $t('config.wv.cat.faction'),
    rule: $t('config.wv.cat.rule'),
    history: $t('config.wv.cat.history'),
    other: $t('config.wv.cat.other'),
  };
  $: wvTabs = [
    ['all', $t('config.wv.cat.all')],
    ['geography', $t('config.wv.cat.geography')],
    ['faction', $t('config.wv.cat.faction')],
    ['rule', $t('config.wv.cat.rule')],
    ['history', $t('config.wv.cat.history')],
    ['other', $t('config.wv.cat.other')],
  ];

  onMount(async () => {
    try { config.set(await api('GET', '/api/config')); } catch (e) {}
    try { settings.set(await api('GET', '/api/settings')); } catch (e) {}
  });

  // 直接保存故事配置（不经过 AI），存在已确认章节且关键设定有变化时提示协调
  async function saveStoryConfig() {
    const prev = $config?.story || {};
    const story = {
      ...localStoryCfg,
      chapter_count: Number(localStoryCfg.chapter_count) || 30,
      target_words_per_chapter: Number(localStoryCfg.target_words_per_chapter) || 2500,
    };
    const settingsChanged =
      story.type !== prev.type ||
      story.writing_style !== prev.writing_style ||
      story.writing_pov !== prev.writing_pov ||
      story.story_synopsis !== prev.story_synopsis;

    try {
      const saved = await api('PUT', '/api/config', { ...($config || {}), story });
      config.set(saved);
      addToast($t('config.story.saved'), 'success');

      if (hasAccepted && settingsChanged) {
        showConfirm($t('config.story.reconcileAsk'), async () => {
          try {
            await api('POST', '/api/settings/reconcile', saved.story);
            addToast($t('config.story.reconcileStarted'), 'info');
          } catch (e) { addToast(e.message, 'error'); }
        });
      }
    } catch (e) { addToast(e.message, 'error'); }
  }

  function openCharForm(char) {
    showCharForm = true;
    if (char) {
      $editingCharID = char.id;
      charName = char.name || '';
      charAge = char.age || '';
      charAppearance = char.appearance || '';
      charPersonality = char.personality || '';
      charBackground = char.background || '';
      charMotivation = char.motivation || '';
      charAbilities = char.abilities || '';
      charNotes = char.notes || '';
      charActs = [...(char.acts || [])];
    } else {
      $editingCharID = null;
      charName = charAge = charAppearance = charPersonality = charBackground = charMotivation = charAbilities = charNotes = '';
      charActs = [];
    }
  }

  function closeCharForm() {
    showCharForm = false;
    $editingCharID = null;
  }

  async function saveCharacter() {
    if (!charName.trim()) { addToast($t('config.char.nameRequired'), 'error'); return; }
    const data = { name: charName.trim(), age: charAge, appearance: charAppearance, personality: charPersonality, background: charBackground, motivation: charMotivation, abilities: charAbilities, notes: charNotes, acts: charActs };
    try {
      if ($editingCharID) {
        await api('PUT', '/api/characters/' + $editingCharID, data);
      } else {
        await api('POST', '/api/characters', data);
      }
      addToast($t('config.char.saved'), 'success');
      closeCharForm();
      settings.set(await api('GET', '/api/settings'));
    } catch (e) { addToast(e.message, 'error'); }
  }

  async function deleteCharacter(id) {
    showConfirm($t('config.char.deleteConfirm'), async () => {
      try {
        await api('DELETE', '/api/characters/' + id);
        addToast($t('config.char.deleted'), 'success');
        settings.set(await api('GET', '/api/settings'));
      } catch (e) { addToast(e.message, 'error'); }
    });
  }

  async function submitCharacters() {
    if (chars.length === 0) { addToast($t('config.char.noneToSubmit'), 'error'); return; }
    const lines = chars.map(c => `- ${c.name}${c.age ? ', ' + c.age : ''}${c.personality ? ', ' + c.personality : ''}`).join('\n');
    await sendToChat($t('config.char.submitMsg', { n: chars.length, lines }));
    addToast($t('config.char.submitted'), 'success');
  }

  function openWvForm(item) {
    showWvForm = true;
    if (item) {
      $editingWvID = item.id;
      wvName = item.name || '';
      wvCategory = item.category || 'other';
      wvDescription = item.description || '';
      wvTags = item.tags || '';
      wvActs = [...(item.acts || [])];
    } else {
      $editingWvID = null;
      wvName = ''; wvCategory = 'other'; wvDescription = ''; wvTags = '';
      wvActs = [];
    }
  }

  function closeWvForm() {
    showWvForm = false;
    $editingWvID = null;
  }

  async function saveWorldview() {
    if (!wvName.trim() || !wvDescription.trim()) { addToast($t('config.wv.requiredFields'), 'error'); return; }
    const data = { name: wvName.trim(), category: wvCategory, description: wvDescription.trim(), tags: wvTags, acts: wvActs };
    try {
      if ($editingWvID) {
        await api('PUT', '/api/worldview/' + $editingWvID, data);
      } else {
        await api('POST', '/api/worldview', data);
      }
      addToast($t('config.wv.saved'), 'success');
      closeWvForm();
      settings.set(await api('GET', '/api/settings'));
    } catch (e) { addToast(e.message, 'error'); }
  }

  async function deleteWorldview(id) {
    showConfirm($t('config.wv.deleteConfirm'), async () => {
      try {
        await api('DELETE', '/api/worldview/' + id);
        addToast($t('config.wv.deleted'), 'success');
        settings.set(await api('GET', '/api/settings'));
      } catch (e) { addToast(e.message, 'error'); }
    });
  }

  async function submitWorldview() {
    if (allWvs.length === 0) { addToast($t('config.wv.noneToSubmit'), 'error'); return; }
    const lines = allWvs.map(w => `- [${catLabels[w.category] || w.category}] ${w.name}: ${w.description.slice(0, 50)}`).join('\n');
    await sendToChat($t('config.wv.submitMsg', { n: allWvs.length, lines }));
    addToast($t('config.wv.submitted'), 'success');
  }

  // —— 组织 CRUD ——
  function openOrgForm(org) {
    showOrgForm = true;
    if (org) {
      editingOrgID = org.id;
      orgName = org.name || '';
      orgType = org.type || '';
      orgDescription = org.description || '';
      orgMembers = [...(org.members || [])];
      orgActs = [...(org.acts || [])];
    } else {
      editingOrgID = null;
      orgName = orgType = orgDescription = '';
      orgMembers = [];
      orgActs = [];
    }
  }

  function closeOrgForm() {
    showOrgForm = false;
    editingOrgID = null;
  }

  async function saveOrganization() {
    if (!orgName.trim()) { addToast($t('config.org.nameRequired'), 'error'); return; }
    const data = { name: orgName.trim(), type: orgType, description: orgDescription, members: orgMembers, acts: orgActs };
    try {
      if (editingOrgID) {
        await api('PUT', '/api/organizations/' + editingOrgID, data);
      } else {
        await api('POST', '/api/organizations', data);
      }
      addToast($t('config.org.saved'), 'success');
      closeOrgForm();
      settings.set(await api('GET', '/api/settings'));
    } catch (e) { addToast(e.message, 'error'); }
  }

  async function deleteOrganization(id) {
    showConfirm($t('config.org.deleteConfirm'), async () => {
      try {
        await api('DELETE', '/api/organizations/' + id);
        addToast($t('config.org.deleted'), 'success');
        settings.set(await api('GET', '/api/settings'));
      } catch (e) { addToast(e.message, 'error'); }
    });
  }

  // —— 关系 CRUD ——
  function parseEntityKey(key) {
    const i = key.indexOf(':');
    return { type: key.slice(0, i), id: key.slice(i + 1) };
  }

  function openRelForm(rel) {
    showRelForm = true;
    if (rel) {
      editingRelID = rel.id;
      relSource = (rel.source_type || 'character') + ':' + rel.source_id;
      relTarget = (rel.target_type || 'character') + ':' + rel.target_id;
      relLabel = rel.label || '';
    } else {
      editingRelID = null;
      relSource = relTarget = '';
      relLabel = '';
    }
  }

  function closeRelForm() {
    showRelForm = false;
    editingRelID = null;
  }

  async function saveRelation() {
    if (!relSource || !relTarget) { addToast($t('config.rel.bothRequired'), 'error'); return; }
    if (relSource === relTarget) { addToast($t('config.rel.sameEntity'), 'error'); return; }
    if (!relLabel.trim()) { addToast($t('config.rel.labelRequired'), 'error'); return; }
    const s = parseEntityKey(relSource);
    const tt = parseEntityKey(relTarget);
    const data = { source_id: s.id, source_type: s.type, target_id: tt.id, target_type: tt.type, label: relLabel.trim() };
    try {
      if (editingRelID) {
        await api('PUT', '/api/relations/' + editingRelID, data);
      } else {
        await api('POST', '/api/relations', data);
      }
      addToast($t('config.rel.saved'), 'success');
      closeRelForm();
      settings.set(await api('GET', '/api/settings'));
    } catch (e) { addToast(e.message, 'error'); }
  }

  async function deleteRelation(id) {
    showConfirm($t('config.rel.deleteConfirm'), async () => {
      try {
        await api('DELETE', '/api/relations/' + id);
        addToast($t('config.rel.deleted'), 'success');
        settings.set(await api('GET', '/api/settings'));
      } catch (e) { addToast(e.message, 'error'); }
    });
  }
</script>

<div class="space-y-3">
  <ConfigChangePanel />
  <!-- 故事设定：基础信息 -->
  <div class="card bg-base-200 shadow-sm">
    <div class="card-body p-4 gap-2">
      <h3 class="card-title text-base">{$t('config.story.title')}</h3>
      {#if hasAccepted}
        <div class="alert alert-warning text-xs py-1.5 px-3">
          <span>{$t('config.story.acceptedHint')}</span>
        </div>
      {/if}
      <div class="grid grid-cols-1 sm:grid-cols-2 gap-x-3 gap-y-1.5">
        <div>
          <span class="text-xs text-base-content/50 mb-0.5 block">{$t('config.story.type')}</span>
          <input type="text" class="input input-sm w-full" bind:value={localStoryCfg.type} placeholder={$t('config.story.type.placeholder')} disabled={$taskRunning} />
        </div>
        <div>
          <span class="text-xs text-base-content/50 mb-0.5 block">{$t('config.story.titleField')}</span>
          <input type="text" class="input input-sm w-full" bind:value={localStoryCfg.title} placeholder={$t('config.story.title.placeholder')} disabled={$taskRunning} />
        </div>
        <div>
          <span class="text-xs text-base-content/50 mb-0.5 block">{$t('config.story.chapterCount')}</span>
          <input type="number" class="input input-sm w-full" bind:value={localStoryCfg.chapter_count} disabled={$taskRunning} />
        </div>
        <div>
          <span class="text-xs text-base-content/50 mb-0.5 block">{$t('config.story.targetWords')}</span>
          <input type="number" class="input input-sm w-full" bind:value={localStoryCfg.target_words_per_chapter} disabled={$taskRunning} />
        </div>
      </div>
      <div class="flex justify-end">
        <button class="btn btn-primary btn-xs" on:click={saveStoryConfig} disabled={$taskRunning}>{$t('common.save')}</button>
      </div>
    </div>
  </div>

  <!-- Writing Style & POV -->
  <div class="card bg-base-200 shadow-sm">
    <div class="card-body p-4 gap-2">
      <h3 class="card-title text-base">{$t('config.style.title')}</h3>
      <div>
        <span class="text-xs text-base-content/50 mb-0.5 block">{$t('config.style.label')}</span>
        <textarea class="textarea w-full h-28 text-base" bind:value={localStoryCfg.writing_style} placeholder={$t('config.style.placeholder')} disabled={$taskRunning}></textarea>
      </div>
      <div>
        <span class="text-xs text-base-content/50 mb-0.5 block">{$t('config.pov.label')}</span>
        <textarea class="textarea w-full h-20 text-base" bind:value={localStoryCfg.writing_pov} placeholder={$t('config.pov.placeholder')} disabled={$taskRunning}></textarea>
      </div>
      <div class="flex justify-end">
        <button class="btn btn-primary btn-xs" on:click={saveStoryConfig} disabled={$taskRunning}>{$t('common.save')}</button>
      </div>
    </div>
  </div>

  <!-- Story Synopsis -->
  <div class="card bg-base-200 shadow-sm">
    <div class="card-body p-4 gap-2">
      <h3 class="card-title text-base">{$t('config.synopsis.title')}</h3>
      <textarea class="textarea w-full h-40 text-base" bind:value={localStoryCfg.story_synopsis} placeholder={$t('config.synopsis.placeholder')} disabled={$taskRunning}></textarea>
      <div class="flex justify-end">
        <button class="btn btn-primary btn-xs" on:click={saveStoryConfig} disabled={$taskRunning}>{$t('common.save')}</button>
      </div>
    </div>
  </div>

  <!-- Characters -->
  <div class="card bg-base-200 shadow-sm">
    <div class="card-body p-4 gap-2">
      <!-- svelte-ignore a11y-click-events-have-key-events -->
      <!-- svelte-ignore a11y-no-static-element-interactions -->
      <div class="flex justify-between items-center cursor-pointer select-none" on:click={() => charCollapse = !charCollapse}>
        <h3 class="card-title text-base">{$t('config.char.title')} <span class="text-xs font-normal text-base-content/40">({chars.length})</span></h3>
        <svg class="w-4 h-4 text-base-content/40 transition-transform" class:rotate-180={charCollapse} viewBox="0 0 20 20" fill="currentColor"><path fill-rule="evenodd" d="M5.23 7.21a.75.75 0 011.06.02L10 11.168l3.71-3.938a.75.75 0 111.08 1.04l-4.25 4.5a.75.75 0 01-1.08 0l-4.25-4.5a.75.75 0 01.02-1.06z" clip-rule="evenodd"/></svg>
      </div>
      {#if !charCollapse}
        <div class="grid grid-cols-1 sm:grid-cols-[repeat(auto-fill,minmax(220px,1fr))] gap-2">
          {#if chars.length === 0}
            <p class="text-xs text-base-content/40 col-span-full py-2">{$t('config.char.empty')}</p>
          {:else}
            {#each chars as c}
              <div class="flex items-start gap-2.5 bg-base-300 rounded-lg p-2.5 group">
                <div class="w-8 h-8 rounded-full bg-primary/20 text-primary flex items-center justify-center text-xs font-bold shrink-0">{stripNameMarks(c.name)[0]}</div>
                <div class="flex-1 min-w-0">
                  <div class="text-sm font-medium truncate">{stripNameMarks(c.name)}</div>
                  <div class="text-xs text-base-content/40 line-clamp-1">{c.personality || c.background || c.age || ''}</div>
                  {#if (c.acts || []).length}
                    <div class="text-[11px] text-primary/70 mt-0.5 truncate">{$t('config.acts.on')}: {actsLabel(c.acts)}</div>
                  {/if}
                </div>
                <div class="flex gap-0.5 opacity-0 group-hover:opacity-100 transition-opacity shrink-0">
                  <button class="btn btn-ghost btn-xs px-1" on:click={() => openCharForm(c)} disabled={$taskRunning}>{$t('common.edit')}</button>
                  <button class="btn btn-ghost btn-xs px-1 text-error" on:click={() => deleteCharacter(c.id)} disabled={$taskRunning}>{$t('common.delete')}</button>
                </div>
              </div>
            {/each}
          {/if}
        </div>

        {#if showCharForm}
          <div class="bg-base-300 rounded-lg p-3 space-y-2 mt-1">
            <div class="grid grid-cols-1 sm:grid-cols-2 gap-x-3 gap-y-1.5">
              <div>
                <span class="text-xs text-base-content/50 mb-0.5 block">{$t('config.char.name')}</span>
                <input type="text" class="input input-sm w-full" bind:value={charName} disabled={$taskRunning} />
              </div>
              <div>
                <span class="text-xs text-base-content/50 mb-0.5 block">{$t('config.char.age')}</span>
                <input type="text" class="input input-sm w-full" bind:value={charAge} disabled={$taskRunning} />
              </div>
            </div>
            <div class="grid grid-cols-1 sm:grid-cols-2 gap-x-3 gap-y-1.5">
              <div>
                <span class="text-xs text-base-content/50 mb-0.5 block">{$t('config.char.appearance')}</span>
                <textarea class="textarea textarea-sm w-full h-14 text-sm" bind:value={charAppearance} disabled={$taskRunning}></textarea>
              </div>
              <div>
                <span class="text-xs text-base-content/50 mb-0.5 block">{$t('config.char.personality')}</span>
                <textarea class="textarea textarea-sm w-full h-14 text-sm" bind:value={charPersonality} disabled={$taskRunning}></textarea>
              </div>
              <div>
                <span class="text-xs text-base-content/50 mb-0.5 block">{$t('config.char.background')}</span>
                <textarea class="textarea textarea-sm w-full h-14 text-sm" bind:value={charBackground} disabled={$taskRunning}></textarea>
              </div>
              <div>
                <span class="text-xs text-base-content/50 mb-0.5 block">{$t('config.char.motivation')}</span>
                <textarea class="textarea textarea-sm w-full h-14 text-sm" bind:value={charMotivation} disabled={$taskRunning}></textarea>
              </div>
              <div>
                <span class="text-xs text-base-content/50 mb-0.5 block">{$t('config.char.abilities')}</span>
                <textarea class="textarea textarea-sm w-full h-14 text-sm" bind:value={charAbilities} disabled={$taskRunning}></textarea>
              </div>
              <div>
                <span class="text-xs text-base-content/50 mb-0.5 block">{$t('config.char.notes')}</span>
                <textarea class="textarea textarea-sm w-full h-14 text-sm" bind:value={charNotes} disabled={$taskRunning}></textarea>
              </div>
            </div>
            {#if hasActs}
              <div>
                <span class="text-xs text-base-content/50 mb-0.5 block">{$t('config.acts.label')}</span>
                <ActSelector options={actOptions} bind:value={charActs} disabled={$taskRunning} />
                <p class="text-[11px] text-base-content/35 mt-0.5">{$t('config.acts.hint')}</p>
              </div>
            {/if}
            <div class="flex gap-1.5">
              <button class="btn btn-success btn-xs" on:click={saveCharacter} disabled={$taskRunning}>{$t('config.char.save')}</button>
              <button class="btn btn-ghost btn-xs" on:click={closeCharForm}>{$t('common.cancel')}</button>
            </div>
          </div>
        {/if}

        <div class="flex gap-1.5">
          <button class="btn btn-primary btn-xs" on:click={() => openCharForm(null)} disabled={$taskRunning}>{$t('config.char.create')}</button>
          {#if chars.length > 0}
            <button class="btn btn-accent btn-xs" on:click={submitCharacters} disabled={$taskRunning}>{$t('config.char.submit')}</button>
          {/if}
        </div>
      {/if}
    </div>
  </div>

  <!-- Worldview -->
  <div class="card bg-base-200 shadow-sm">
    <div class="card-body p-4 gap-2">
      <!-- svelte-ignore a11y-click-events-have-key-events -->
      <!-- svelte-ignore a11y-no-static-element-interactions -->
      <div class="flex justify-between items-center cursor-pointer select-none" on:click={() => wvCollapse = !wvCollapse}>
        <h3 class="card-title text-base">{$t('config.wv.title')} <span class="text-xs font-normal text-base-content/40">({filteredWvs.length})</span></h3>
        <svg class="w-4 h-4 text-base-content/40 transition-transform" class:rotate-180={wvCollapse} viewBox="0 0 20 20" fill="currentColor"><path fill-rule="evenodd" d="M5.23 7.21a.75.75 0 011.06.02L10 11.168l3.71-3.938a.75.75 0 111.08 1.04l-4.25 4.5a.75.75 0 01-1.08 0l-4.25-4.5a.75.75 0 01.02-1.06z" clip-rule="evenodd"/></svg>
      </div>
      {#if !wvCollapse}
        <div class="tabs tabs-box tabs-xs bg-base-300 w-fit">
          {#each wvTabs as [cat, label]}
            <button class="tab tab-xs {$wvFilter === cat ? 'tab-active' : ''}" on:click={() => wvFilter.set(cat)}>
              {label}
            </button>
          {/each}
        </div>

        <div class="grid grid-cols-1 sm:grid-cols-[repeat(auto-fill,minmax(220px,1fr))] gap-2">
          {#if filteredWvs.length === 0}
            <p class="text-xs text-base-content/40 col-span-full py-2">{$t('config.wv.empty')}</p>
          {:else}
            {#each filteredWvs as w}
              <div class="flex items-start gap-2.5 bg-base-300 rounded-lg p-2.5 group">
                <div class="w-8 h-8 rounded-lg bg-accent/20 text-accent flex items-center justify-center text-xs font-bold shrink-0">{w.name[0]}</div>
                <div class="flex-1 min-w-0">
                  <div class="text-sm font-medium truncate">{w.name} <span class="text-xs font-normal text-base-content/30">[{catLabels[w.category] || w.category}]</span></div>
                  <div class="text-xs text-base-content/40 line-clamp-1">{w.description}</div>
                  {#if (w.acts || []).length}
                    <div class="text-[11px] text-primary/70 mt-0.5 truncate">{$t('config.acts.on')}: {actsLabel(w.acts)}</div>
                  {/if}
                </div>
                <div class="flex gap-0.5 opacity-0 group-hover:opacity-100 transition-opacity shrink-0">
                  <button class="btn btn-ghost btn-xs px-1" on:click={() => openWvForm(w)} disabled={$taskRunning}>{$t('common.edit')}</button>
                  <button class="btn btn-ghost btn-xs px-1 text-error" on:click={() => deleteWorldview(w.id)} disabled={$taskRunning}>{$t('common.delete')}</button>
                </div>
              </div>
            {/each}
          {/if}
        </div>

        {#if showWvForm}
          <div class="bg-base-300 rounded-lg p-3 space-y-2 mt-1">
            <div class="grid grid-cols-1 sm:grid-cols-2 gap-x-3 gap-y-1.5">
              <div>
                <span class="text-xs text-base-content/50 mb-0.5 block">{$t('config.wv.name')}</span>
                <input type="text" class="input input-sm w-full" bind:value={wvName} disabled={$taskRunning} />
              </div>
              <div>
                <span class="text-xs text-base-content/50 mb-0.5 block">{$t('config.wv.category')}</span>
                <select class="select select-sm w-full" bind:value={wvCategory} disabled={$taskRunning}>
                  <option value="geography">{$t('config.wv.cat.geography')}</option>
                  <option value="faction">{$t('config.wv.cat.faction')}</option>
                  <option value="rule">{$t('config.wv.cat.rule')}</option>
                  <option value="history">{$t('config.wv.cat.history')}</option>
                  <option value="other">{$t('config.wv.cat.other')}</option>
                </select>
              </div>
            </div>
            <div>
              <span class="text-xs text-base-content/50 mb-0.5 block">{$t('config.wv.description')}</span>
              <textarea class="textarea textarea-sm w-full h-16 text-sm" bind:value={wvDescription} disabled={$taskRunning}></textarea>
            </div>
            <div>
              <span class="text-xs text-base-content/50 mb-0.5 block">{$t('config.wv.tags')}</span>
              <input type="text" class="input input-sm w-full" bind:value={wvTags} placeholder={$t('config.wv.tags.placeholder')} disabled={$taskRunning} />
            </div>
            {#if hasActs}
              <div>
                <span class="text-xs text-base-content/50 mb-0.5 block">{$t('config.acts.label')}</span>
                <ActSelector options={actOptions} bind:value={wvActs} disabled={$taskRunning} />
                <p class="text-[11px] text-base-content/35 mt-0.5">{$t('config.acts.hint')}</p>
              </div>
            {/if}
            <div class="flex gap-1.5">
              <button class="btn btn-success btn-xs" on:click={saveWorldview} disabled={$taskRunning}>{$t('common.save')}</button>
              <button class="btn btn-ghost btn-xs" on:click={closeWvForm}>{$t('common.cancel')}</button>
            </div>
          </div>
        {/if}

        <div class="flex gap-1.5">
          <button class="btn btn-primary btn-xs" on:click={() => openWvForm(null)} disabled={$taskRunning}>{$t('config.wv.create')}</button>
          {#if allWvs.length > 0}
            <button class="btn btn-accent btn-xs" on:click={submitWorldview} disabled={$taskRunning}>{$t('config.wv.submit')}</button>
          {/if}
        </div>
      {/if}
    </div>
  </div>

  <!-- Organizations -->
  <div class="card bg-base-200 shadow-sm">
    <div class="card-body p-4 gap-2">
      <!-- svelte-ignore a11y-click-events-have-key-events -->
      <!-- svelte-ignore a11y-no-static-element-interactions -->
      <div class="flex justify-between items-center cursor-pointer select-none" on:click={() => orgCollapse = !orgCollapse}>
        <h3 class="card-title text-base">{$t('config.org.title')} <span class="text-xs font-normal text-base-content/40">({orgs.length})</span></h3>
        <svg class="w-4 h-4 text-base-content/40 transition-transform" class:rotate-180={orgCollapse} viewBox="0 0 20 20" fill="currentColor"><path fill-rule="evenodd" d="M5.23 7.21a.75.75 0 011.06.02L10 11.168l3.71-3.938a.75.75 0 111.08 1.04l-4.25 4.5a.75.75 0 01-1.08 0l-4.25-4.5a.75.75 0 01.02-1.06z" clip-rule="evenodd"/></svg>
      </div>
      {#if !orgCollapse}
        <div class="grid grid-cols-1 sm:grid-cols-[repeat(auto-fill,minmax(220px,1fr))] gap-2">
          {#if orgs.length === 0}
            <p class="text-xs text-base-content/40 col-span-full py-2">{$t('config.org.empty')}</p>
          {:else}
            {#each orgs as o}
              <div class="flex items-start gap-2.5 bg-base-300 rounded-lg p-2.5 group">
                <div class="w-8 h-8 rounded-lg bg-warning/20 text-warning flex items-center justify-center text-xs font-bold shrink-0">{o.name[0]}</div>
                <div class="flex-1 min-w-0">
                  <div class="text-sm font-medium truncate">{o.name} {#if o.type}<span class="text-xs font-normal text-base-content/30">[{o.type}]</span>{/if}</div>
                  <div class="text-xs text-base-content/40 line-clamp-1">{o.description || ''}</div>
                  {#if (o.members || []).length > 0}
                    <div class="text-xs text-base-content/35 line-clamp-1 mt-0.5">{$t('config.org.membersList', { names: (o.members || []).map(id => nameById[id] || id).join(', ') })}</div>
                  {/if}
                  {#if (o.acts || []).length}
                    <div class="text-[11px] text-primary/70 mt-0.5 truncate">{$t('config.acts.on')}: {actsLabel(o.acts)}</div>
                  {/if}
                </div>
                <div class="flex gap-0.5 opacity-0 group-hover:opacity-100 transition-opacity shrink-0">
                  <button class="btn btn-ghost btn-xs px-1" on:click={() => openOrgForm(o)} disabled={$taskRunning}>{$t('common.edit')}</button>
                  <button class="btn btn-ghost btn-xs px-1 text-error" on:click={() => deleteOrganization(o.id)} disabled={$taskRunning}>{$t('common.delete')}</button>
                </div>
              </div>
            {/each}
          {/if}
        </div>

        {#if showOrgForm}
          <div class="bg-base-300 rounded-lg p-3 space-y-2 mt-1">
            <div class="grid grid-cols-1 sm:grid-cols-2 gap-x-3 gap-y-1.5">
              <div>
                <span class="text-xs text-base-content/50 mb-0.5 block">{$t('config.org.name')}</span>
                <input type="text" class="input input-sm w-full" bind:value={orgName} disabled={$taskRunning} />
              </div>
              <div>
                <span class="text-xs text-base-content/50 mb-0.5 block">{$t('config.org.type')}</span>
                <input type="text" class="input input-sm w-full" bind:value={orgType} placeholder={$t('config.org.type.placeholder')} disabled={$taskRunning} />
              </div>
            </div>
            <div>
              <span class="text-xs text-base-content/50 mb-0.5 block">{$t('config.org.description')}</span>
              <textarea class="textarea textarea-sm w-full h-16 text-sm" bind:value={orgDescription} disabled={$taskRunning}></textarea>
            </div>
            {#if chars.length > 0}
              <div>
                <span class="text-xs text-base-content/50 mb-0.5 block">{$t('config.org.members')}</span>
                <div class="flex flex-wrap gap-x-3 gap-y-1">
                  {#each chars as c}
                    <label class="flex items-center gap-1 cursor-pointer text-sm">
                      <input type="checkbox" class="checkbox checkbox-xs" bind:group={orgMembers} value={c.id} disabled={$taskRunning} />
                      {c.name}
                    </label>
                  {/each}
                </div>
              </div>
            {/if}
            {#if hasActs}
              <div>
                <span class="text-xs text-base-content/50 mb-0.5 block">{$t('config.acts.label')}</span>
                <ActSelector options={actOptions} bind:value={orgActs} disabled={$taskRunning} />
                <p class="text-[11px] text-base-content/35 mt-0.5">{$t('config.acts.hint')}</p>
              </div>
            {/if}
            <div class="flex gap-1.5">
              <button class="btn btn-success btn-xs" on:click={saveOrganization} disabled={$taskRunning}>{$t('config.org.save')}</button>
              <button class="btn btn-ghost btn-xs" on:click={closeOrgForm}>{$t('common.cancel')}</button>
            </div>
          </div>
        {/if}

        <div class="flex gap-1.5">
          <button class="btn btn-primary btn-xs" on:click={() => openOrgForm(null)} disabled={$taskRunning}>{$t('config.org.create')}</button>
        </div>
      {/if}
    </div>
  </div>

  <!-- Relations -->
  <div class="card bg-base-200 shadow-sm">
    <div class="card-body p-4 gap-2">
      <!-- svelte-ignore a11y-click-events-have-key-events -->
      <!-- svelte-ignore a11y-no-static-element-interactions -->
      <div class="flex justify-between items-center cursor-pointer select-none" on:click={() => relCollapse = !relCollapse}>
        <h3 class="card-title text-base">{$t('config.rel.title')} <span class="text-xs font-normal text-base-content/40">({rels.length})</span></h3>
        <svg class="w-4 h-4 text-base-content/40 transition-transform" class:rotate-180={relCollapse} viewBox="0 0 20 20" fill="currentColor"><path fill-rule="evenodd" d="M5.23 7.21a.75.75 0 011.06.02L10 11.168l3.71-3.938a.75.75 0 111.08 1.04l-4.25 4.5a.75.75 0 01-1.08 0l-4.25-4.5a.75.75 0 01.02-1.06z" clip-rule="evenodd"/></svg>
      </div>
      {#if !relCollapse}
        <div class="grid grid-cols-1 sm:grid-cols-[repeat(auto-fill,minmax(260px,1fr))] gap-2">
          {#if rels.length === 0}
            <p class="text-xs text-base-content/40 col-span-full py-2">{$t('config.rel.empty')}</p>
          {:else}
            {#each rels as r}
              <div class="flex items-center gap-2 bg-base-300 rounded-lg p-2.5 group">
                <div class="flex-1 min-w-0 text-sm flex items-center gap-1.5 flex-wrap">
                  <span class="font-medium">{entityIcons[r.source_type] || ''} {nameById[r.source_id] || r.source_id}</span>
                  <span class="badge badge-xs badge-secondary">{r.label}</span>
                  <span class="text-base-content/40">→</span>
                  <span class="font-medium">{entityIcons[r.target_type] || ''} {nameById[r.target_id] || r.target_id}</span>
                </div>
                <div class="flex gap-0.5 opacity-0 group-hover:opacity-100 transition-opacity shrink-0">
                  <button class="btn btn-ghost btn-xs px-1" on:click={() => openRelForm(r)} disabled={$taskRunning}>{$t('common.edit')}</button>
                  <button class="btn btn-ghost btn-xs px-1 text-error" on:click={() => deleteRelation(r.id)} disabled={$taskRunning}>{$t('common.delete')}</button>
                </div>
              </div>
            {/each}
          {/if}
        </div>

        {#if showRelForm}
          <div class="bg-base-300 rounded-lg p-3 space-y-2 mt-1">
            <div class="grid grid-cols-1 sm:grid-cols-[1fr_auto_1fr] gap-2 items-end">
              <div>
                <span class="text-xs text-base-content/50 mb-0.5 block">{$t('config.rel.source')}</span>
                <select class="select select-sm w-full" bind:value={relSource} disabled={$taskRunning}>
                  <option value="" disabled>{$t('config.rel.entityHint')}</option>
                  {#each entityOptions as opt}
                    <option value={opt.key}>{opt.label}</option>
                  {/each}
                </select>
              </div>
              <span class="text-base-content/40 pb-1.5">→</span>
              <div>
                <span class="text-xs text-base-content/50 mb-0.5 block">{$t('config.rel.target')}</span>
                <select class="select select-sm w-full" bind:value={relTarget} disabled={$taskRunning}>
                  <option value="" disabled>{$t('config.rel.entityHint')}</option>
                  {#each entityOptions as opt}
                    <option value={opt.key}>{opt.label}</option>
                  {/each}
                </select>
              </div>
            </div>
            <div>
              <span class="text-xs text-base-content/50 mb-0.5 block">{$t('config.rel.label')}</span>
              <input type="text" class="input input-sm w-full" bind:value={relLabel} placeholder={$t('config.rel.label.placeholder')} disabled={$taskRunning} />
            </div>
            <div class="flex gap-1.5">
              <button class="btn btn-success btn-xs" on:click={saveRelation} disabled={$taskRunning}>{$t('config.rel.save')}</button>
              <button class="btn btn-ghost btn-xs" on:click={closeRelForm}>{$t('common.cancel')}</button>
            </div>
          </div>
        {/if}

        <div class="flex gap-1.5">
          <button class="btn btn-primary btn-xs" on:click={() => openRelForm(null)} disabled={$taskRunning || entityOptions.length < 2}>{$t('config.rel.create')}</button>
          {#if entityOptions.length < 2}
            <span class="text-xs text-base-content/35 self-center">{$t('config.rel.needTwo')}</span>
          {/if}
        </div>
      {/if}
    </div>
  </div>
</div>

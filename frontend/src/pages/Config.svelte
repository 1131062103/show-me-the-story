<script>
  import { onMount } from 'svelte';
  import { api } from '../lib/api.js';
  import { apiConfig, config, progress, settings, taskRunning, editingCharID, editingWvID, wvFilter, addToast } from '../lib/stores.js';

  let showCharForm = false;
  let showWvForm = false;
  let charCollapse = false;
  let wvCollapse = false;

  let charName = '', charAge = '', charAppearance = '', charPersonality = '', charBackground = '', charMotivation = '', charAbilities = '', charNotes = '';
  let wvName = '', wvCategory = 'other', wvDescription = '', wvTags = '';

  $: cfgBase = $apiConfig?.base_url || '';
  $: cfgModel = $apiConfig?.model || '';
  $: cfgKey = $apiConfig?.api_key || '';
  $: cfgTimeout = $apiConfig?.http_timeout_seconds || 300;

  let localApiCfg = { base_url: '', model: '', api_key: '', http_timeout_seconds: 300 };
  let localStoryCfg = { type: '', title: '', chapter_count: 30, target_words_per_chapter: 2500, writing_style: '', character_setting: '', world_setting: '', core_requirements: '' };

  let apiCfgInitialized = false;
  let storyCfgInitialized = false;

  $: if ($apiConfig && !apiCfgInitialized) {
    localApiCfg = { ...$apiConfig };
    apiCfgInitialized = true;
  }
  $: if ($config?.story && !storyCfgInitialized) {
    localStoryCfg = { ...$config.story };
    storyCfgInitialized = true;
  }

  $: hasAccepted = $progress?.chapters?.some(c => c.status === 'accepted') || false;

  $: chars = ($settings?.characters || []);
  $: allWvs = ($settings?.worldview || []);
  $: filteredWvs = $wvFilter === 'all' ? allWvs : allWvs.filter(w => w.category === $wvFilter);

  const catLabels = { geography: '地理', faction: '势力', rule: '规则', history: '历史', other: '其他' };
  const wvTabs = [
    ['all', '全部'],
    ['geography', '地理'],
    ['faction', '势力'],
    ['rule', '规则'],
    ['history', '历史'],
    ['other', '其他']
  ];

  onMount(async () => {
    try { apiConfig.set(await api('GET', '/api/config/api')); } catch (e) {}
    try { config.set(await api('GET', '/api/config')); } catch (e) {}
    try { settings.set(await api('GET', '/api/settings')); } catch (e) {}
  });

  async function saveAPIConfig() {
    try {
      await api('PUT', '/api/config/api', localApiCfg);
      apiConfig.set({ ...localApiCfg });
      addToast('API 配置已保存', 'success');
    } catch (e) { addToast(e.message, 'error'); }
  }

  async function saveStoryConfig() {
    const cfg = { story: { ...localStoryCfg }, prompts: {}, skill_config: $config?.skill_config || null };
    try {
      await api('PUT', '/api/config', cfg);
      config.set(cfg);
      if (hasAccepted) {
        addToast('设定已保存，正在自动协调已有内容...', 'info');
        try { await api('POST', '/api/settings/reconcile', cfg.story); } catch (e) { addToast('协调请求失败: ' + e.message, 'error'); }
      } else {
        addToast('故事配置已保存', 'success');
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
    } else {
      $editingCharID = null;
      charName = charAge = charAppearance = charPersonality = charBackground = charMotivation = charAbilities = charNotes = '';
    }
  }

  function closeCharForm() {
    showCharForm = false;
    $editingCharID = null;
  }

  async function saveCharacter() {
    if (!charName.trim()) { addToast('角色名不能为空', 'error'); return; }
    const data = { name: charName.trim(), age: charAge, appearance: charAppearance, personality: charPersonality, background: charBackground, motivation: charMotivation, abilities: charAbilities, notes: charNotes };
    try {
      if ($editingCharID) {
        await api('PUT', '/api/characters/' + $editingCharID, data);
      } else {
        await api('POST', '/api/characters', data);
      }
      addToast('角色已保存', 'success');
      closeCharForm();
      settings.set(await api('GET', '/api/settings'));
    } catch (e) { addToast(e.message, 'error'); }
  }

  async function deleteCharacter(id) {
    if (!confirm('确认删除此角色？')) return;
    try {
      await api('DELETE', '/api/characters/' + id);
      addToast('角色已删除', 'success');
      settings.set(await api('GET', '/api/settings'));
    } catch (e) { addToast(e.message, 'error'); }
  }

  function openWvForm(item) {
    showWvForm = true;
    if (item) {
      $editingWvID = item.id;
      wvName = item.name || '';
      wvCategory = item.category || 'other';
      wvDescription = item.description || '';
      wvTags = item.tags || '';
    } else {
      $editingWvID = null;
      wvName = ''; wvCategory = 'other'; wvDescription = ''; wvTags = '';
    }
  }

  function closeWvForm() {
    showWvForm = false;
    $editingWvID = null;
  }

  async function saveWorldview() {
    if (!wvName.trim() || !wvDescription.trim()) { addToast('名称和描述不能为空', 'error'); return; }
    const data = { name: wvName.trim(), category: wvCategory, description: wvDescription.trim(), tags: wvTags };
    try {
      if ($editingWvID) {
        await api('PUT', '/api/worldview/' + $editingWvID, data);
      } else {
        await api('POST', '/api/worldview', data);
      }
      addToast('世界观条目已保存', 'success');
      closeWvForm();
      settings.set(await api('GET', '/api/settings'));
    } catch (e) { addToast(e.message, 'error'); }
  }

  async function deleteWorldview(id) {
    if (!confirm('确认删除此世界观条目？')) return;
    try {
      await api('DELETE', '/api/worldview/' + id);
      addToast('世界观条目已删除', 'success');
      settings.set(await api('GET', '/api/settings'));
    } catch (e) { addToast(e.message, 'error'); }
  }

  async function aiGenerateSettings() {
    try {
      await api('POST', '/api/settings/ai-generate');
      addToast('AI 设定生成中...', 'info');
    } catch (e) { addToast(e.message, 'error'); }
  }
</script>

<div class="space-y-4">
  <!-- API Config -->
  <div class="card bg-base-200 shadow-sm">
    <div class="card-body p-5">
      <h2 class="card-title text-base">API 配置</h2>
      <div class="grid grid-cols-2 gap-3">
        <div class="form-control">
          <label class="label py-1"><span class="label-text text-xs">API Base URL</span></label>
          <input type="text" class="input input-bordered input-sm" bind:value={localApiCfg.base_url} placeholder="https://api.example.com/v1/" />
        </div>
        <div class="form-control">
          <label class="label py-1"><span class="label-text text-xs">Model</span></label>
          <input type="text" class="input input-bordered input-sm" bind:value={localApiCfg.model} placeholder="gpt-4" />
        </div>
        <div class="form-control">
          <label class="label py-1"><span class="label-text text-xs">API Key</span></label>
          <input type="password" class="input input-bordered input-sm" bind:value={localApiCfg.api_key} placeholder="sk-..." />
        </div>
        <div class="form-control">
          <label class="label py-1"><span class="label-text text-xs">HTTP 超时（秒）</span></label>
          <input type="number" class="input input-bordered input-sm" bind:value={localApiCfg.http_timeout_seconds} />
        </div>
      </div>
      <div class="card-actions justify-end mt-2">
        <button class="btn btn-primary btn-sm" on:click={saveAPIConfig}>保存 API 配置</button>
      </div>
    </div>
  </div>

  <!-- Story Config -->
  <div class="card bg-base-200 shadow-sm">
    <div class="card-body p-5">
      <h2 class="card-title text-base">故事配置</h2>
      {#if hasAccepted}
        <div class="alert alert-warning text-xs py-2">
          <span>已有已确认章节，保存配置后将自动由 AI 协调设定与已有内容的兼容性。</span>
        </div>
      {/if}
      <div class="grid grid-cols-2 gap-3">
        <div class="form-control">
          <label class="label py-1"><span class="label-text text-xs">故事类型</span></label>
          <input type="text" class="input input-bordered input-sm" bind:value={localStoryCfg.type} placeholder="奇幻/都市/科幻..." />
        </div>
        <div class="form-control">
          <label class="label py-1"><span class="label-text text-xs">小说标题（留空由 AI 生成）</span></label>
          <input type="text" class="input input-bordered input-sm" bind:value={localStoryCfg.title} placeholder="留空则 AI 自动生成" />
        </div>
        <div class="form-control">
          <label class="label py-1"><span class="label-text text-xs">章节数量</span></label>
          <input type="number" class="input input-bordered input-sm" bind:value={localStoryCfg.chapter_count} />
        </div>
        <div class="form-control">
          <label class="label py-1"><span class="label-text text-xs">每章目标字数</span></label>
          <input type="number" class="input input-bordered input-sm" bind:value={localStoryCfg.target_words_per_chapter} />
        </div>
      </div>
      <div class="form-control">
        <label class="label py-1"><span class="label-text text-xs">写作风格</span></label>
        <textarea class="textarea textarea-bordered textarea-sm h-16" bind:value={localStoryCfg.writing_style} placeholder="描述你期望的写作风格..."></textarea>
      </div>
      <div class="form-control">
        <label class="label py-1"><span class="label-text text-xs">角色设定</span></label>
        <textarea class="textarea textarea-bordered textarea-sm h-16" bind:value={localStoryCfg.character_setting} placeholder="主要角色设定..."></textarea>
      </div>
      <div class="form-control">
        <label class="label py-1"><span class="label-text text-xs">世界观设定</span></label>
        <textarea class="textarea textarea-bordered textarea-sm h-16" bind:value={localStoryCfg.world_setting} placeholder="世界观设定..."></textarea>
      </div>
      <div class="form-control">
        <label class="label py-1"><span class="label-text text-xs">核心写作要求</span></label>
        <textarea class="textarea textarea-bordered textarea-sm h-16" bind:value={localStoryCfg.core_requirements} placeholder="可包含：故事主线走向、核心冲突、关键转折点..."></textarea>
      </div>
      <div class="card-actions justify-end mt-2">
        <button class="btn btn-primary btn-sm" on:click={saveStoryConfig}>保存故事配置</button>
      </div>
    </div>
  </div>

  <!-- Characters -->
  <div class="card bg-base-200 shadow-sm">
    <div class="card-body p-5">
      <!-- svelte-ignore a11y-click-events-have-key-events -->
      <!-- svelte-ignore a11y-no-static-element-interactions -->
      <div class="flex justify-between items-center cursor-pointer" on:click={() => charCollapse = !charCollapse}>
        <h2 class="card-title text-base">角色管理</h2>
        <span class="text-xs transition-transform" class:rotate-90={charCollapse}>▼</span>
      </div>
      {#if !charCollapse}
        <div class="grid grid-cols-[repeat(auto-fill,minmax(260px,1fr))] gap-3 mt-3">
          {#if chars.length === 0}
            <p class="text-sm text-base-content/50 col-span-full">暂无角色，点击下方按钮创建。</p>
          {:else}
            {#each chars as c}
              <div class="card card-compact bg-base-300 border border-base-content/10">
                <div class="card-body">
                  <h3 class="card-title text-sm">{c.name}</h3>
                  <p class="text-xs text-base-content/60 line-clamp-2">{c.personality || c.background || c.age || ''}</p>
                  <div class="card-actions justify-end gap-1">
                    <button class="btn btn-ghost btn-xs" on:click={() => openCharForm(c)}>编辑</button>
                    <button class="btn btn-error btn-xs" on:click={() => deleteCharacter(c.id)}>删除</button>
                  </div>
                </div>
              </div>
            {/each}
          {/if}
        </div>

        {#if showCharForm}
          <div class="bg-base-300 rounded-lg p-4 mt-3 space-y-2">
            <div class="grid grid-cols-2 gap-2">
              <div class="form-control">
                <label class="label py-0"><span class="label-text text-xs">名称</span></label>
                <input type="text" class="input input-bordered input-xs" bind:value={charName} />
              </div>
              <div class="form-control">
                <label class="label py-0"><span class="label-text text-xs">年龄</span></label>
                <input type="text" class="input input-bordered input-xs" bind:value={charAge} />
              </div>
            </div>
            <div class="form-control">
              <label class="label py-0"><span class="label-text text-xs">外貌</span></label>
              <textarea class="textarea textarea-bordered textarea-xs h-12" bind:value={charAppearance}></textarea>
            </div>
            <div class="form-control">
              <label class="label py-0"><span class="label-text text-xs">性格</span></label>
              <textarea class="textarea textarea-bordered textarea-xs h-12" bind:value={charPersonality}></textarea>
            </div>
            <div class="form-control">
              <label class="label py-0"><span class="label-text text-xs">背景</span></label>
              <textarea class="textarea textarea-bordered textarea-xs h-12" bind:value={charBackground}></textarea>
            </div>
            <div class="form-control">
              <label class="label py-0"><span class="label-text text-xs">动机</span></label>
              <textarea class="textarea textarea-bordered textarea-xs h-12" bind:value={charMotivation}></textarea>
            </div>
            <div class="form-control">
              <label class="label py-0"><span class="label-text text-xs">能力</span></label>
              <textarea class="textarea textarea-bordered textarea-xs h-12" bind:value={charAbilities}></textarea>
            </div>
            <div class="form-control">
              <label class="label py-0"><span class="label-text text-xs">备注</span></label>
              <textarea class="textarea textarea-bordered textarea-xs h-12" bind:value={charNotes}></textarea>
            </div>
            <div class="flex gap-2">
              <button class="btn btn-success btn-xs" on:click={saveCharacter}>保存角色</button>
              <button class="btn btn-ghost btn-xs" on:click={closeCharForm}>取消</button>
            </div>
          </div>
        {/if}

        <div class="flex gap-2 mt-3">
          <button class="btn btn-primary btn-xs" on:click={() => openCharForm(null)}>新建角色</button>
          <button class="btn btn-ghost btn-xs" on:click={aiGenerateSettings}>AI 自动生成</button>
        </div>
      {/if}
    </div>
  </div>

  <!-- Worldview -->
  <div class="card bg-base-200 shadow-sm">
    <div class="card-body p-5">
      <!-- svelte-ignore a11y-click-events-have-key-events -->
      <!-- svelte-ignore a11y-no-static-element-interactions -->
      <div class="flex justify-between items-center cursor-pointer" on:click={() => wvCollapse = !wvCollapse}>
        <h2 class="card-title text-base">世界观管理</h2>
        <span class="text-xs transition-transform" class:rotate-90={wvCollapse}>▼</span>
      </div>
      {#if !wvCollapse}
        <div class="tabs tabs-boxed tabs-xs bg-base-300 w-fit mt-3">
          {#each wvTabs as [cat, label]}
            <button class="tab tab-xs {$wvFilter === cat ? 'tab-active' : ''}" on:click={() => wvFilter.set(cat)}>
              {label}
            </button>
          {/each}
        </div>

        <div class="grid grid-cols-[repeat(auto-fill,minmax(260px,1fr))] gap-3 mt-3">
          {#if filteredWvs.length === 0}
            <p class="text-sm text-base-content/50 col-span-full">暂无世界观条目。</p>
          {:else}
            {#each filteredWvs as w}
              <div class="card card-compact bg-base-300 border border-base-content/10">
                <div class="card-body">
                  <h3 class="card-title text-sm">{w.name} <span class="text-xs font-normal text-base-content/50">[{catLabels[w.category] || w.category}]</span></h3>
                  <p class="text-xs text-base-content/60 line-clamp-2">{w.description}</p>
                  <div class="card-actions justify-end gap-1">
                    <button class="btn btn-ghost btn-xs" on:click={() => openWvForm(w)}>编辑</button>
                    <button class="btn btn-error btn-xs" on:click={() => deleteWorldview(w.id)}>删除</button>
                  </div>
                </div>
              </div>
            {/each}
          {/if}
        </div>

        {#if showWvForm}
          <div class="bg-base-300 rounded-lg p-4 mt-3 space-y-2">
            <div class="grid grid-cols-2 gap-2">
              <div class="form-control">
                <label class="label py-0"><span class="label-text text-xs">名称</span></label>
                <input type="text" class="input input-bordered input-xs" bind:value={wvName} />
              </div>
              <div class="form-control">
                <label class="label py-0"><span class="label-text text-xs">分类</span></label>
                <select class="select select-bordered select-xs" bind:value={wvCategory}>
                  <option value="geography">地理</option>
                  <option value="faction">势力</option>
                  <option value="rule">规则</option>
                  <option value="history">历史</option>
                  <option value="other">其他</option>
                </select>
              </div>
            </div>
            <div class="form-control">
              <label class="label py-0"><span class="label-text text-xs">描述</span></label>
              <textarea class="textarea textarea-bordered textarea-xs h-12" bind:value={wvDescription}></textarea>
            </div>
            <div class="form-control">
              <label class="label py-0"><span class="label-text text-xs">标签</span></label>
              <input type="text" class="input input-bordered input-xs" bind:value={wvTags} placeholder="逗号分隔" />
            </div>
            <div class="flex gap-2">
              <button class="btn btn-success btn-xs" on:click={saveWorldview}>保存</button>
              <button class="btn btn-ghost btn-xs" on:click={closeWvForm}>取消</button>
            </div>
          </div>
        {/if}

        <div class="mt-3">
          <button class="btn btn-primary btn-xs" on:click={() => openWvForm(null)}>新建世界观条目</button>
        </div>
      {/if}
    </div>
  </div>
</div>

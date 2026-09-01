<script>
  import { onMount } from 'svelte';
  import { api } from '../lib/api.js';
  import { apiConfig, apiProfiles, config, addToast, showConfirm, taskRunning, apiTestResult } from '../lib/stores.js';
  import { t } from '../lib/i18n/index.js';
  import { resolveChatCompletionsURL } from '../lib/apiUrl.js';

  const defaultCfg = { base_url: '', url_strict: false, model: '', api_key: '', http_timeout_seconds: 600, max_tokens: 32768, context_budget_tokens: 900000 };

  let profiles = [];
  let activeName = '';
  let selName = '';
  let localCfg = { ...defaultCfg };
  let cfgSnap = '';
  let creatingProfile = false;
  let newProfileName = '';

  let testingApi = false;
  let models = [];
  let fetchingModels = false;
  let modelsError = '';

  $: resolvedChatURL = resolveChatCompletionsURL(localCfg.base_url, !!localCfg.url_strict);
  $: selProfile = profiles.find(p => p.name === selName) || null;
  $: isActive = selName === activeName;

  // 模型下拉选项：拉取列表 + 当前手填值兜底
  $: modelOptions = (() => {
    const set = new Set(models);
    if (localCfg.model && !set.has(localCfg.model)) return [localCfg.model, ...models];
    return models;
  })();

  $: if ($apiProfiles) {
    profiles = $apiProfiles.profiles || [];
    activeName = $apiProfiles.active || '';
    if (!selName || !profiles.find(p => p.name === selName)) {
      selName = activeName || (profiles[0] && profiles[0].name) || '';
    }
  }

  // 选中配置 → 载入可编辑副本（与 store 内容不一致时刷新）
  $: if (selProfile) {
    const snap = JSON.stringify(selProfile.config);
    if (snap !== cfgSnap) {
      localCfg = { ...defaultCfg, ...selProfile.config, url_strict: !!selProfile.config.url_strict };
      cfgSnap = snap;
      models = [];
      modelsError = '';
      if (localCfg.base_url) fetchModels();
    }
  }

  // AI 写作提示词（项目级，存在 config.json 的 prompts 中）
  let promptsSnapshot = '';
  let localPrompts = {};
  $: if ($config?.prompts) {
    const snap = JSON.stringify($config.prompts);
    if (snap !== promptsSnapshot) {
      localPrompts = { ...$config.prompts };
      promptsSnapshot = snap;
    }
  }

  const PROMPT_GROUPS = [
    { key: 'outline', fields: [
      ['outline_generation', 'system.prompt.outline_generation'],
      ['outline_revision', 'system.prompt.outline_revision'],
      ['continuation_outline_generation', 'system.prompt.continuation_outline_generation'],
    ]},
    { key: 'arcs', fields: [
      ['book_overview', 'system.prompt.book_overview'],
      ['arc_skeleton', 'system.prompt.arc_skeleton'],
      ['arc_outline', 'system.prompt.arc_outline'],
      ['arc_chapter_outline', 'system.prompt.arc_chapter_outline'],
      ['arc_summary', 'system.prompt.arc_summary'],
      ['act_outline', 'system.prompt.act_outline'],
      ['act_chapter_outline', 'system.prompt.act_chapter_outline'],
      ['act_summary', 'system.prompt.act_summary'],
    ]},
    { key: 'writing', fields: [
      ['chapter_writing', 'system.prompt.chapter_writing'],
      ['chapter_revision', 'system.prompt.chapter_revision'],
      ['chapter_segment_revision', 'system.prompt.chapter_segment_revision'],
      ['transition_smoothing', 'system.prompt.transition_smoothing'],
      ['outline_consistency_check', 'system.prompt.outline_consistency_check'],
    ]},
    { key: 'summary', fields: [
      ['chapter_summary', 'system.prompt.chapter_summary'],
      ['memory_update', 'system.prompt.memory_update'],
    ]},
    { key: 'check', fields: [
      ['fact_check', 'system.prompt.fact_check'],
      ['foreshadow_outline_consistency', 'system.prompt.foreshadow_outline_consistency'],
      ['outline_character_check', 'system.prompt.outline_character_check'],
      ['writing_conflict_analysis', 'system.prompt.writing_conflict_analysis'],
      ['settings_reconciliation', 'system.prompt.settings_reconciliation'],
    ]},
    { key: 'foreshadow', fields: [
      ['foreshadow_planning', 'system.prompt.foreshadow_planning'],
      ['foreshadow_update', 'system.prompt.foreshadow_update'],
    ]},
    { key: 'postprocess', fields: [
      ['book_diagnosis', 'system.prompt.book_diagnosis'],
      ['book_consistency_check', 'system.prompt.book_consistency_check'],
      ['book_roadmap', 'system.prompt.book_roadmap'],
    ]},
    { key: 'import', fields: [
      ['import_meta_analysis', 'system.prompt.import_meta_analysis'],
      ['import_chapter_analysis', 'system.prompt.import_chapter_analysis'],
    ]},
  ];

  onMount(async () => {
    try { apiProfiles.set(await api('GET', '/api/config/api/profiles')); } catch (e) {}
    try { config.set(await api('GET', '/api/config')); } catch (e) {}
  });

  async function refreshProfiles() {
    const prof = await api('GET', '/api/config/api/profiles');
    apiProfiles.set(prof);
    return prof;
  }

  // —— 配置管理 ——
  async function createProfile() {
    const name = newProfileName.trim();
    if (!name) { addToast($t('system.api.nameRequired'), 'error'); return; }
    try {
      await api('POST', '/api/config/api/profiles', { name, config: { ...defaultCfg } });
      await refreshProfiles();
      selName = name;
      creatingProfile = false;
      newProfileName = '';
      addToast($t('system.api.created'), 'success');
    } catch (e) { addToast(e.message, 'error'); }
  }

  async function saveProfile() {
    if (!selName) return;
    try {
      const saved = await api('PUT', '/api/config/api/profiles/' + encodeURIComponent(selName), localCfg);
      await refreshProfiles();
      apiConfig.set(saved);
      addToast($t('system.api.saved'), 'success');
    } catch (e) { addToast(e.message, 'error'); }
  }

  async function setActive() {
    if (!selName || isActive) return;
    try {
      await api('POST', '/api/config/api/profiles/' + encodeURIComponent(selName) + '/select');
      await refreshProfiles();
      apiConfig.set(localCfg);
      addToast($t('system.api.activated', { name: selName }), 'success');
    } catch (e) { addToast(e.message, 'error'); }
  }

  function deleteProfile() {
    showConfirm($t('system.api.deleteConfirm', { name: selName }), async () => {
      try {
        await api('DELETE', '/api/config/api/profiles/' + encodeURIComponent(selName));
        const prof = await refreshProfiles();
        selName = prof.active || (prof.profiles[0] && prof.profiles[0].name) || '';
        addToast($t('system.api.deleted'), 'success');
      } catch (e) { addToast(e.message, 'error'); }
    });
  }

  // 影响连接测试的字段签名；配置改动后据此自动清除持久化的测试结果
  const apiTestSig = c => JSON.stringify([c.base_url, !!c.url_strict, c.model, c.api_key, c.http_timeout_seconds, c.max_tokens]);
  $: if ($apiTestResult && apiTestSig(localCfg) !== $apiTestResult.sig) apiTestResult.set(null);

  async function testAPIConfig() {
    testingApi = true;
    const sig = apiTestSig(localCfg);
    try {
      const res = await api('POST', '/api/config/api/test', localCfg);
      apiTestResult.set({ ok: true, model: res.model, sig });
      addToast($t('config.api.testOk', { model: res.model }), 'success');
    } catch (e) {
      apiTestResult.set({ ok: false, error: e.message, sig });
      addToast(e.message, 'error');
    } finally {
      testingApi = false;
    }
  }

  async function fetchModels() {
    if (!localCfg.base_url.trim()) { addToast($t('system.api.modelsNeedUrl'), 'error'); return; }
    fetchingModels = true;
    modelsError = '';
    try {
      const res = await api('POST', '/api/config/api/models', { config: localCfg });
      models = res.models || [];
      if (models.length === 0) modelsError = $t('system.api.modelsEmpty');
    } catch (e) {
      modelsError = e.message;
      addToast(e.message, 'error');
    } finally {
      fetchingModels = false;
    }
  }

  // —— 提示词管理 ——
  async function savePrompts() {
    if (!$config) return;
    try {
      const saved = await api('PUT', '/api/config', { ...$config, prompts: localPrompts });
      config.set(saved);
      addToast($t('system.prompts.saved'), 'success');
    } catch (e) { addToast(e.message, 'error'); }
  }

  function resetPrompts() {
    showConfirm($t('system.prompts.resetConfirm'), () => {
      const empty = {};
      Object.keys(localPrompts).forEach(k => empty[k] = '');
      localPrompts = empty;
    });
  }

  // —— Legado 阅读（书源）——
  // 无需后端配置：书源里的地址由服务端按"访问本站的地址"自动生成，
  // 这里只是把可粘贴的地址与书源 JSON 展示、复制给用户。
  $: origin = typeof window !== 'undefined' ? window.location.origin : '';
  $: legadoUrl = origin ? origin + '/api/legado/book-source.json' : '';
  let legadoJson = '';
  let loadingLegadoJson = false;

  onMount(async () => {
    try { apiProfiles.set(await api('GET', '/api/config/api/profiles')); } catch (e) {}
    try { config.set(await api('GET', '/api/config')); } catch (e) {}
    loadLegadoJson();
  });

  async function loadLegadoJson() {
    if (!origin) return;
    loadingLegadoJson = true;
    try {
      const r = await fetch('/api/legado/book-source.json');
      if (r.ok) legadoJson = await r.text();
    } catch (e) {
      legadoJson = '';
    } finally {
      loadingLegadoJson = false;
    }
  }

  async function copyLegadoUrl() {
    try {
      await navigator.clipboard.writeText(legadoUrl);
      addToast($t('system.legado.copied'), 'success');
    } catch (e) { /* clipboard may be unavailable over plain http */ }
  }
</script>

<div class="space-y-3">
  <!-- Legado 阅读（书源）：让手机上的开源阅读软件直接读本站的小说 -->
  <div class="card bg-base-200 shadow-sm">
    <div class="card-body p-4 gap-2">
      <h3 class="card-title text-base">{$t('system.legado.title')}</h3>
      <p class="text-xs text-base-content/45">{$t('system.legado.hint')}</p>
      <p class="text-xs text-base-content/40">
        <span class="text-base-content/50">{$t('system.legado.currentOrigin')}</span>
        <code class="font-mono text-primary/90 break-all">{origin || '—'}</code>
      </p>
      <div>
        <span class="text-xs text-base-content/50 mb-0.5 block">{$t('system.legado.pasteUrl.label')}</span>
        <div class="flex items-center gap-2 flex-wrap">
          <code class="font-mono text-sm bg-base-300 rounded-lg px-2 py-1 break-all flex-1">{legadoUrl}</code>
          <button class="btn btn-primary btn-xs" on:click={copyLegadoUrl} disabled={!legadoUrl}>{$t('system.legado.copyUrl')}</button>
          <a class="btn btn-outline btn-xs" href={legadoUrl} target="_blank" rel="noopener">{$t('system.legado.open')}</a>
        </div>
      </div>

      <details>
        <summary class="cursor-pointer text-xs text-base-content/60 select-none">{$t('system.legado.manual')}</summary>
        <pre class="mt-1 text-[11px] font-mono bg-base-300 rounded-lg p-2 overflow-auto max-h-64 leading-relaxed text-base-content/80 whitespace-pre-wrap break-all">
{#if loadingLegadoJson}
  <span class="loading loading-spinner loading-xs"></span>
{:else}
{legadoJson}
{/if}
        </pre>
      </details>

      <div class="text-xs text-base-content/50 space-y-0.5 mt-1">
        <span class="font-medium text-base-content/60">{$t('system.legado.tooltip')}</span>
        <div class="opacity-70 space-y-0.5">
          <div>{$t('system.legado.notes.title')}</div>
          <ul class="list-disc pl-4 space-y-0.5">
            <li>{$t('system.legado.notes.l1')}</li>
            <li>{$t('system.legado.notes.l2')}</li>
            <li>{$t('system.legado.notes.l3')}</li>
          </ul>
        </div>
      </div>
    </div>
  </div>

  <!-- API 配置（多配置管理） -->
  <div class="card bg-base-200 shadow-sm">
    <div class="card-body p-4 gap-2">
      <div class="flex justify-between items-center flex-wrap gap-2">
        <h3 class="card-title text-base">{$t('system.api.title')}</h3>
        <button class="btn btn-primary btn-xs" on:click={() => creatingProfile = !creatingProfile} disabled={$taskRunning}>
          {$t('system.api.new')}
        </button>
      </div>
      <p class="text-xs text-base-content/45">{$t('system.api.hint')}</p>

      {#if creatingProfile}
        <div class="flex gap-1.5 items-center bg-base-300 rounded-lg p-2">
          <input type="text" class="input input-sm flex-1" bind:value={newProfileName} placeholder={$t('system.api.newName')} disabled={$taskRunning} />
          <button class="btn btn-success btn-xs" on:click={createProfile} disabled={$taskRunning}>{$t('system.api.create')}</button>
          <button class="btn btn-ghost btn-xs" on:click={() => { creatingProfile = false; newProfileName = ''; }}>{$t('common.cancel')}</button>
        </div>
      {/if}

      {#if profiles.length === 0}
        <p class="text-xs text-base-content/40 py-2">{$t('system.api.none')}</p>
      {:else}
        <div class="flex items-center gap-2 flex-wrap">
          <select class="select select-sm w-full sm:w-64" bind:value={selName} disabled={$taskRunning} title={$t('system.api.select')}>
            {#each profiles as p}
              <option value={p.name}>{p.name}{p.name === activeName ? ' ✓' : ''}</option>
            {/each}
          </select>
          {#if isActive}
            <span class="badge badge-sm badge-success">{$t('system.api.active')}</span>
          {/if}
        </div>

        {#if selProfile}
          <div class="grid grid-cols-1 sm:grid-cols-2 gap-x-3 gap-y-1.5 mt-1">
            <div class="col-span-2">
              <span class="text-xs text-base-content/50 mb-0.5 block">{$t('config.api.baseUrl')}</span>
              <input type="text" class="input input-sm w-full" bind:value={localCfg.base_url} placeholder="https://api.openai.com/v1" disabled={$taskRunning || testingApi} />
              <label class="label cursor-pointer justify-start gap-2 py-1 px-0 min-h-0">
                <input type="checkbox" class="toggle toggle-xs" bind:checked={localCfg.url_strict} disabled={$taskRunning || testingApi} />
                <span class="label-text text-xs text-base-content/60">{$t('config.api.urlStrict')}</span>
              </label>
              <p class="text-xs text-base-content/45 mb-1">{$t('config.api.urlStrictHint')}</p>
              {#if resolvedChatURL}
                <p class="text-xs text-base-content/50 break-all">
                  {$t('config.api.resolvedUrl')}: <code class="font-mono text-primary/80">{resolvedChatURL}</code>
                </p>
              {/if}
            </div>
            <div>
              <span class="text-xs text-base-content/50 mb-0.5 block">{$t('system.api.modelSelect')}</span>
              {#if modelOptions.length > 0}
                <select class="select select-sm w-full" bind:value={localCfg.model} disabled={$taskRunning || testingApi}>
                  <option value="">{$t('system.api.modelPick')}</option>
                  {#each modelOptions as m}
                    <option value={m}>{m}</option>
                  {/each}
                </select>
              {:else}
                <input type="text" class="input input-sm w-full" bind:value={localCfg.model} placeholder="gpt-4" disabled={$taskRunning || testingApi} />
              {/if}
              <div class="flex items-center gap-1.5 mt-1">
                <button class="btn btn-outline btn-xs" on:click={fetchModels} disabled={$taskRunning || testingApi || fetchingModels}>
                  {#if fetchingModels}
                    <span class="loading loading-spinner loading-xs"></span>{$t('system.api.fetchingModels')}
                  {:else}
                    {$t('system.api.fetchModels')}
                  {/if}
                </button>
              </div>
              {#if modelsError}
                <p class="text-xs text-error/80 mt-0.5 break-all">{modelsError}</p>
              {/if}
            </div>
            <div>
              <span class="text-xs text-base-content/50 mb-0.5 block">{$t('config.api.key')}</span>
              <input type="password" class="input input-sm w-full" bind:value={localCfg.api_key} placeholder="sk-..." disabled={$taskRunning || testingApi} />
            </div>
            <div>
              <span class="text-xs text-base-content/50 mb-0.5 block">{$t('config.api.timeout')}</span>
              <input type="number" class="input input-sm w-full" bind:value={localCfg.http_timeout_seconds} disabled={$taskRunning || testingApi} />
            </div>
            <div>
              <span class="text-xs text-base-content/50 mb-0.5 block">{$t('config.api.maxTokens')}</span>
              <input type="number" class="input input-sm w-full" bind:value={localCfg.max_tokens} placeholder={$t('config.api.maxTokens.placeholder')} disabled={$taskRunning || testingApi} title={$t('config.api.maxTokens.tooltip')} />
            </div>
            <div class="col-span-2">
              <span class="text-xs text-base-content/50 mb-0.5 block">{$t('config.api.budget')}</span>
              <input type="number" class="input input-sm w-full" bind:value={localCfg.context_budget_tokens} placeholder="900000" disabled={$taskRunning || testingApi} title={$t('config.api.budget.tooltip')} />
            </div>
          </div>

          {#if $apiTestResult}
            <div class="text-xs rounded-md border px-2.5 py-1.5 {$apiTestResult.ok ? 'border-success/40 bg-success/10 text-success' : 'border-error/40 bg-error/10 text-error'}">
              {#if $apiTestResult.ok}
                ✓ {$t('config.api.testResultOk', { model: $apiTestResult.model })}
              {:else}
                ✕ {$t('config.api.testResultFail', { error: $apiTestResult.error })}
              {/if}
            </div>
          {/if}

          <div class="flex justify-end gap-2 flex-wrap">
            <button class="btn btn-xs {$apiTestResult ? ($apiTestResult.ok ? 'btn-success btn-outline' : 'btn-error btn-outline') : 'btn-outline'}" on:click={testAPIConfig} disabled={$taskRunning || testingApi}>
              {#if testingApi}
                <span class="loading loading-spinner loading-xs"></span>{$t('config.api.testing')}
              {:else}
                {$t('config.api.test')}
              {/if}
            </button>
            {#if !isActive}
              <button class="btn btn-accent btn-xs" on:click={setActive} disabled={$taskRunning || testingApi}>{$t('system.api.setActive')}</button>
            {/if}
            <button class="btn btn-ghost btn-xs text-error" on:click={deleteProfile} disabled={$taskRunning || testingApi || profiles.length <= 1}>{$t('system.api.delete')}</button>
            <button class="btn btn-primary btn-xs" on:click={saveProfile} disabled={$taskRunning || testingApi}>{$t('common.save')}</button>
          </div>
        {/if}
      {/if}
    </div>
  </div>

  <!-- AI 写作提示词 -->
  <div class="card bg-base-200 shadow-sm">
    <div class="card-body p-4 gap-2">
      <div class="flex justify-between items-center flex-wrap gap-2">
        <h3 class="card-title text-base">{$t('system.prompts.title')}</h3>
        <button class="btn btn-ghost btn-xs text-error" on:click={resetPrompts} disabled={$taskRunning}>{$t('system.prompts.resetAll')}</button>
      </div>
      <p class="text-xs text-base-content/45">{$t('system.prompts.hint')}</p>

      {#each PROMPT_GROUPS as group}
        <details class="bg-base-300 rounded-lg overflow-hidden">
          <summary class="cursor-pointer px-3 py-2 text-sm font-medium select-none flex items-center justify-between">
            {$t('system.prompts.group.' + group.key)}
          </summary>
          <div class="px-3 pb-3 space-y-2">
            {#each group.fields as [field, labelKey]}
              <div>
                <span class="text-xs text-base-content/50 mb-0.5 block">{$t(labelKey)}</span>
                <textarea class="textarea w-full h-32 text-xs font-mono leading-relaxed" bind:value={localPrompts[field]} disabled={$taskRunning} placeholder={$t('system.prompts.emptyHint')}></textarea>
              </div>
            {/each}
          </div>
        </details>
      {/each}

      <div class="flex justify-end">
        <button class="btn btn-primary btn-xs" on:click={savePrompts} disabled={$taskRunning || !$config}>{$t('common.save')}</button>
      </div>
    </div>
  </div>
</div>
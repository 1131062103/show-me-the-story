<script>
  import { currentPage } from './lib/router.js';
  import { progress, taskRunning, contextPage, toastStore, taskNotification } from './lib/stores.js';
  import { connectSSE } from './lib/sse.js';
  import { api } from './lib/api.js';

  async function stopTask() {
    try {
      await api('POST', '/api/task/stop');
    } catch (e) {}
  }
  import { onMount } from 'svelte';
  import Config from './pages/Config.svelte';
  import Outline from './pages/Outline.svelte';
  import Writing from './pages/Writing.svelte';
  import Relations from './pages/Relations.svelte';
  import Skills from './pages/Skills.svelte';
  import ChatPanel from './components/ChatPanel.svelte';
  import ConfirmModal from './components/ConfirmModal.svelte';
  import LogPanel from './components/LogPanel.svelte';

  let chatPanel;

  $: $contextPage = $currentPage;

  onMount(async () => {
    connectSSE();
    try {
      const p = await api('GET', '/api/progress');
      progress.set(p);
    } catch (e) {}
  });

  $: phaseNames = { outline: '大纲阶段', writing: '写作阶段' };
  $: phase = $progress ? (phaseNames[$progress.phase] || $progress.phase) : '未开始';

  async function sendToChat(text) {
    if (chatPanel) await chatPanel.sendMessageToChat(text);
  }
</script>

<div class="flex flex-col h-screen bg-base-300 text-base-content overflow-hidden">
  <!-- Header -->
  <header class="navbar bg-base-200 border-b border-base-content/10 px-6 min-h-[46px] shrink-0 gap-4">
    <span class="text-lg font-semibold">AI 小说生成器</span>
    <span class="badge badge-sm" class:badge-primary={$progress}>{phase}</span>
    {#if $taskRunning}
      <span class="badge badge-sm badge-warning gap-1">
        <span class="loading loading-spinner loading-xs"></span>
        AI 思考中
      </span>
    {/if}
  </header>

  <div class="flex flex-1 overflow-hidden">
    <!-- Left: Nav + Content -->
    <div class="flex flex-col w-1/2 min-w-[320px] border-r border-base-content/10 shrink-0">
      <!-- Nav -->
      <nav class="flex bg-base-200 border-b border-base-content/10 px-3 py-2 shrink-0 gap-1">
        {#each [
          ['config', '配置'],
          ['outline', '大纲'],
          ['writing', '写作'],
          ['relations', '图谱'],
          ['skills', '技能']
        ] as [page, label]}
          <button
            class="btn btn-ghost btn-sm text-sm px-4 {$currentPage === page ? 'btn-active border-b-2 border-primary rounded-none' : ''}"
            on:click={() => window.location.hash = '#' + page}
          >
            {label}
          </button>
        {/each}
      </nav>

      <!-- Content -->
      <main class="flex-1 overflow-y-auto p-4">
        {#if $currentPage === 'config'}
          <Config {sendToChat} />
        {:else if $currentPage === 'outline'}
          <Outline {sendToChat} />
        {:else if $currentPage === 'writing'}
          <Writing {sendToChat} />
        {:else if $currentPage === 'relations'}
          <Relations />
        {:else if $currentPage === 'skills'}
          <Skills />
        {/if}
      </main>
    </div>

    <!-- Right: Chat Panel -->
    <div class="flex-1 bg-base-200 overflow-hidden">
      <ChatPanel bind:this={chatPanel} contextPage={$currentPage} />
    </div>
  </div>

  <!-- Log Panel -->
  <LogPanel />

  <!-- Toasts -->
  <div class="fixed top-5 right-5 z-50 flex flex-col gap-2">
    {#each $toastStore as t (t.id)}
      <div class="alert alert-sm {t.type === 'success' ? 'alert-success' : t.type === 'error' ? 'alert-error' : 'alert-info'} toast-enter shadow-lg max-w-sm">
        <span>{t.msg}</span>
      </div>
    {/each}
  </div>

  <!-- Task Completion Overlay -->
  {#if $taskNotification}
    <!-- svelte-ignore a11y-click-events-have-key-events -->
    <!-- svelte-ignore a11y-no-static-element-interactions -->
    <div class="fixed inset-0 z-[100] bg-black/60 flex items-center justify-center" on:click={() => taskNotification.set(null)}>
      <div class="bg-base-200 rounded-xl shadow-2xl p-8 max-w-md mx-4 text-center border border-base-content/10 animate-bounce-in" on:click|stopPropagation>
        <div class="text-4xl mb-4">✓</div>
        <h3 class="text-xl font-bold mb-2">{$taskNotification.name}</h3>
        <p class="text-base-content/70 mb-6">{$taskNotification.message}</p>
        <button class="btn btn-primary" on:click={() => taskNotification.set(null)}>知道了</button>
      </div>
    </div>
  {/if}

  <ConfirmModal />
</div>

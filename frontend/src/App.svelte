<script>
  import { currentPage } from './lib/router.js';
  import { progress, taskRunning, toastStore } from './lib/stores.js';
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
  import Assistant from './pages/Assistant.svelte';
  import Skills from './pages/Skills.svelte';
  import LogPanel from './components/LogPanel.svelte';

  onMount(async () => {
    connectSSE();
    try {
      const p = await api('GET', '/api/progress');
      progress.set(p);
    } catch (e) {}
  });

  $: phaseNames = { outline: '大纲阶段', writing: '写作阶段' };
  $: phase = $progress ? (phaseNames[$progress.phase] || $progress.phase) : '未开始';
</script>

<div class="flex flex-col h-screen bg-base-300 text-base-content overflow-hidden">
  <!-- Header -->
  <header class="navbar bg-base-200 border-b border-base-content/10 px-6 min-h-[48px] shrink-0 gap-4">
    <span class="text-lg font-semibold">AI 小说生成器</span>
    <span class="badge badge-sm" class:badge-primary={$progress}>{phase}</span>
    {#if $taskRunning}
      <span class="badge badge-sm badge-warning gap-1">
        <span class="loading loading-spinner loading-xs"></span>
        任务运行中
      </span>
      <button class="btn btn-error btn-xs gap-1" on:click={stopTask}>
        ⏹ 停止
      </button>
    {/if}
  </header>

  <div class="flex flex-1 overflow-hidden">
    <!-- Nav -->
    <nav class="w-[140px] bg-base-200 border-r border-base-content/10 flex flex-col py-4 shrink-0">
      {#each [
        ['config', '配置'],
        ['outline', '大纲'],
        ['writing', '写作'],
        ['relations', '图谱'],
        ['assistant', '助理'],
        ['skills', '技能']
      ] as [page, label]}
        <button
          class="btn btn-ghost justify-start rounded-none text-sm {$currentPage === page ? 'btn-active border-r-2 border-primary' : ''}"
          on:click={() => window.location.hash = '#' + page}
        >
          {label}
        </button>
      {/each}
      <div class="flex-1"></div>
      {#if $taskRunning}
        <div class="px-4 text-xs text-warning animate-pulse">AI 思考中...</div>
      {/if}
    </nav>

    <!-- Main content -->
    <main class="flex-1 overflow-y-auto p-6">
      {#if $currentPage === 'config'}
        <Config />
      {:else if $currentPage === 'outline'}
        <Outline />
      {:else if $currentPage === 'writing'}
        <Writing />
      {:else if $currentPage === 'relations'}
        <Relations />
      {:else if $currentPage === 'assistant'}
        <Assistant />
      {:else if $currentPage === 'skills'}
        <Skills />
      {/if}
    </main>
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
</div>

<script>
  import { currentPage } from './lib/router.js';
  import { progress, taskRunning, contextPage, toastStore, currentProject, projectLanguage } from './lib/stores.js';
  import { connectSSE } from './lib/sse.js';
  import { api } from './lib/api.js';
  import { onMount } from 'svelte';
  import { t, uiLocale, setLocale } from './lib/i18n/index.js';
  import TaskTokenBadge from './components/TaskTokenBadge.svelte';
  import Projects from './pages/Projects.svelte';
  import Config from './pages/Config.svelte';
  import Outline from './pages/Outline.svelte';
  import Writing from './pages/Writing.svelte';
  import Relations from './pages/Relations.svelte';
  import Skills from './pages/Skills.svelte';
  import Foreshadows from './pages/Foreshadows.svelte';
  import Memory from './pages/Memory.svelte';
  import ChatPanel from './components/ChatPanel.svelte';
  import ConfirmModal from './components/ConfirmModal.svelte';

  let chatPanel;

  let appVersion = '';
  let latestVersion = '';
  let hasUpdate = false;
  const releasesURL = 'https://github.com/Nigh/show-me-the-story/releases';
  const latestReleaseURL = 'https://github.com/Nigh/show-me-the-story/releases/latest';

  // Mobile: chat drawer open state
  let mobileChatOpen = false;

  const navItems = [
    ['config', '⚙️', 'nav.config'],
    ['outline', '📝', 'nav.outline'],
    ['writing', '✍️', 'nav.writing'],
    ['foreshadows', '🔗', 'nav.foreshadows'],
    ['memory', '🧠', 'nav.memory'],
    ['relations', '🕸️', 'nav.relations'],
    ['skills', '🧩', 'nav.skills']
  ];

  $: $contextPage = $currentPage;

  // Close mobile chat when switching page
  $: if ($currentPage) mobileChatOpen = false;

  onMount(async () => {
    connectSSE();
    // Fetch app version
    try {
      const ver = await api('GET', '/api/version');
      appVersion = ver.version || 'dev';
    } catch (e) {}
    // Check for updates (skip for dev builds)
    if (appVersion && appVersion !== 'dev') {
      try {
        const resp = await fetch('https://api.github.com/repos/Nigh/show-me-the-story/releases/latest');
        if (resp.ok) {
          const data = await resp.json();
          latestVersion = data.tag_name || '';
          if (latestVersion && latestVersion !== appVersion) {
            hasUpdate = true;
          }
        }
      } catch (e) {}
    }
    // Check if a project is already selected
    try {
      const cur = await api('GET', '/api/projects/current');
      if (cur.name) {
        currentProject.set(cur.name);
        if (cur.language) {
          projectLanguage.set(cur.language);
          // First time opening this project this session: align UI with project language.
          // Subsequent toggles persist in localStorage.
          setLocale(cur.language);
        }
        try { const p = await api('GET', '/api/progress'); progress.set(p); } catch (e) {}
      }
    } catch (e) {}
  });

  $: phase = $progress
    ? ($progress.phase === 'outline' ? $t('app.phase.outline')
        : $progress.phase === 'writing' ? $t('app.phase.writing')
        : $progress.phase)
    : $t('app.phase.unstarted');
  $: chapterStats = (() => {
    const chs = $progress?.chapters || [];
    if (chs.length === 0) return '';
    const accepted = chs.filter(c => c.status === 'accepted').length;
    return $t('app.chapters.count', { accepted, total: chs.length });
  })();

  async function sendToChat(text) {
    if (chatPanel) await chatPanel.sendMessageToChat(text);
    // On mobile, open chat drawer when sending content into chat
    mobileChatOpen = true;
  }

  function backToProjects() {
    currentProject.set(null);
    mobileChatOpen = false;
  }

  function toggleLocale() {
    setLocale($uiLocale === 'en' ? 'zh' : 'en');
  }

  function goPage(page) {
    window.location.hash = '#' + page;
    mobileChatOpen = false;
  }
</script>

<div class="flex flex-col h-dvh max-h-dvh bg-base-300 text-base-content overflow-hidden">
  <!-- Header -->
  <header class="navbar bg-base-200 border-b border-base-content/10 px-2 sm:px-4 md:px-6 min-h-[46px] shrink-0 gap-1.5 sm:gap-2 md:gap-4 flex-wrap py-1">
    <span class="text-base sm:text-lg font-semibold shrink-0">{$t('app.title')}</span>
    {#if appVersion}
      <span class="badge badge-xs badge-ghost font-mono hidden sm:inline-flex">{appVersion}</span>
    {/if}
    {#if hasUpdate}
      <a href={latestReleaseURL} target="_blank" rel="noopener" class="badge badge-xs badge-warning gap-0.5 no-underline">
        {$t('app.newVersion')}
      </a>
    {/if}
    {#if $currentProject}
      <span class="badge badge-sm badge-outline max-w-[8rem] sm:max-w-none truncate">{$currentProject}</span>
      <span class="badge badge-sm badge-accent uppercase hidden sm:inline-flex" title={$projectLanguage === 'en' ? 'English' : '中文'}>
        {$projectLanguage === 'en' ? 'EN' : 'ZH'}
      </span>
      <button
        class="btn btn-ghost btn-xs gap-1 hidden sm:inline-flex"
        on:click={backToProjects}
        disabled={$taskRunning}
        title={$taskRunning ? $t('app.switchProject.disabled') : $t('app.switchProject.tooltip')}
      >
        {$t('app.switchProject')}
      </button>
      <button
        class="btn btn-ghost btn-xs sm:hidden"
        on:click={backToProjects}
        disabled={$taskRunning}
        title={$taskRunning ? $t('app.switchProject.disabled') : $t('app.switchProject.tooltip')}
      >
        ⇄
      </button>
      <span class="badge badge-sm hidden md:inline-flex" class:badge-primary={$progress}>{phase}</span>
      {#if chapterStats}
        <span class="badge badge-sm badge-ghost hidden lg:inline-flex">{chapterStats}</span>
      {/if}
      {#if $taskRunning}
        <span class="badge badge-sm badge-warning gap-1">
          <span class="loading loading-spinner loading-xs"></span>
          <span class="hidden sm:inline">{$t('app.aiThinking')}</span>
          <TaskTokenBadge className="badge badge-xs badge-warning font-mono border-0" />
        </span>
      {/if}
    {/if}
    <span class="flex-1"></span>
    {#if $currentProject}
      <!-- Mobile chat toggle -->
      <button
        class="btn btn-ghost btn-sm md:hidden gap-1"
        class:btn-primary={mobileChatOpen}
        on:click={() => mobileChatOpen = !mobileChatOpen}
        title={$t('app.chat.toggle')}
      >
        💬
        {#if $taskRunning}
          <span class="loading loading-spinner loading-xs"></span>
        {/if}
      </button>
    {/if}
    <button
      class="btn btn-ghost btn-xs gap-1"
      on:click={toggleLocale}
      title={$t('app.uiLang.label')}
    >
      {$uiLocale === 'en' ? $t('app.uiLang.en') : $t('app.uiLang.zh')}
    </button>
  </header>

  {#if !$currentProject}
    <!-- Project selection -->
    <main class="flex-1 overflow-y-auto p-4 sm:p-6">
      <Projects />
    </main>
  {:else}
    <div class="flex flex-1 overflow-hidden min-h-0 relative">
      <!-- Left: vertical nav (desktop only) -->
      <nav class="hidden md:flex flex-col w-44 shrink-0 bg-base-200 border-r border-base-content/10 py-3 px-2 gap-0.5">
        {#each navItems as [page, icon, labelKey]}
          <button
            class="btn btn-sm justify-start w-full gap-2 px-3 text-sm {$currentPage === page ? 'btn-primary font-medium' : 'btn-ghost'}"
            on:click={() => goPage(page)}
          >
            <span class="text-xs">{icon}</span>{$t(labelKey)}
          </button>
        {/each}
      </nav>

      <!-- Center: page content -->
      <main class="flex-1 min-w-0 overflow-y-auto p-3 sm:p-4 md:border-r border-base-content/10">
        {#if $currentPage === 'config'}
          <Config {sendToChat} />
        {:else if $currentPage === 'outline'}
          <Outline {sendToChat} />
        {:else if $currentPage === 'writing'}
          <Writing {sendToChat} />
        {:else if $currentPage === 'foreshadows'}
          <Foreshadows />
        {:else if $currentPage === 'memory'}
          <Memory />
        {:else if $currentPage === 'relations'}
          <Relations />
        {:else if $currentPage === 'skills'}
          <Skills />
        {/if}
      </main>

      <!-- Chat Panel: desktop right column / mobile bottom drawer (single instance) -->
      <!-- svelte-ignore a11y-click-events-have-key-events -->
      <!-- svelte-ignore a11y-no-static-element-interactions -->
      <div
        class="chat-shell bg-base-200 overflow-hidden
          md:relative md:flex md:flex-1 md:min-w-0 md:h-auto md:rounded-none md:border-0 md:shadow-none md:inset-auto
          {mobileChatOpen
            ? 'fixed inset-x-0 bottom-0 z-50 flex flex-col rounded-t-2xl shadow-2xl border-t border-base-content/10 mobile-chat-drawer'
            : 'hidden md:flex'}"
      >
        {#if mobileChatOpen}
          <div class="md:hidden flex items-center justify-between px-3 py-2 border-b border-base-content/10 shrink-0">
            <span class="text-sm font-medium">{$t('app.chat.toggle')}</span>
            <button class="btn btn-ghost btn-xs btn-circle" on:click={() => mobileChatOpen = false} aria-label={$t('common.close')}>✕</button>
          </div>
        {/if}
        <div class="flex-1 min-h-0 overflow-hidden flex flex-col w-full">
          <ChatPanel bind:this={chatPanel} contextPage={$currentPage} />
        </div>
      </div>

      {#if mobileChatOpen}
        <!-- svelte-ignore a11y-click-events-have-key-events -->
        <!-- svelte-ignore a11y-no-static-element-interactions -->
        <div
          class="md:hidden fixed inset-0 z-40 bg-black/40"
          on:click={() => mobileChatOpen = false}
          role="presentation"
        ></div>
      {/if}
    </div>

    <!-- Mobile bottom nav -->
    <nav class="md:hidden flex shrink-0 bg-base-200 border-t border-base-content/10 safe-area-bottom overflow-x-auto z-30">
      {#each navItems as [page, icon, labelKey]}
        <button
          class="flex-1 min-w-[3.25rem] flex flex-col items-center justify-center gap-0.5 py-1.5 px-0.5 text-[10px] leading-tight {$currentPage === page && !mobileChatOpen ? 'text-primary font-medium' : 'text-base-content/60'}"
          on:click={() => goPage(page)}
        >
          <span class="text-base leading-none">{icon}</span>
          <span class="truncate max-w-full">{$t(labelKey)}</span>
        </button>
      {/each}
      <button
        class="flex-1 min-w-[3.25rem] flex flex-col items-center justify-center gap-0.5 py-1.5 px-0.5 text-[10px] leading-tight {mobileChatOpen ? 'text-primary font-medium' : 'text-base-content/60'}"
        on:click={() => mobileChatOpen = !mobileChatOpen}
      >
        <span class="text-base leading-none">💬</span>
        <span class="truncate max-w-full">{$t('app.chat.toggle')}</span>
      </button>
    </nav>
  {/if}

  <!-- Toasts -->
  <div class="fixed top-3 right-3 sm:top-5 sm:right-5 z-[60] flex flex-col gap-2 max-w-[calc(100vw-1.5rem)]">
    {#each $toastStore as t (t.id)}
      <div class="alert alert-sm {t.type === 'success' ? 'alert-success' : t.type === 'error' ? 'alert-error' : 'alert-info'} toast-enter shadow-lg max-w-sm">
        <span>{t.msg}</span>
      </div>
    {/each}
  </div>

  <ConfirmModal />
</div>

<style>
  .mobile-chat-drawer {
    height: min(88dvh, 720px);
    max-height: 88dvh;
  }
  @media (min-width: 768px) {
    .mobile-chat-drawer {
      height: auto;
      max-height: none;
    }
  }
  .safe-area-bottom {
    padding-bottom: env(safe-area-inset-bottom, 0);
  }
</style>

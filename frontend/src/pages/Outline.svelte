<script>
  import { api } from '../lib/api.js';
  import { progress, config, streamingContent, streamingChapterIdx, taskRunning, addToast, showConfirm, outlineCharacterSuggestions, outlineCharacterShowSuggestions, settings } from '../lib/stores.js';
  import { t } from '../lib/i18n/index.js';
  import { onMount, tick } from 'svelte';
  import ConfigChangePanel from '../components/ConfigChangePanel.svelte';
  import OutlineChapterRow from '../components/OutlineChapterRow.svelte';

  const OUTLINE_FOCUS_KEY = 'showmethestory.outlineFocusChapter';

  // Once writing begins, the chapter outline is permanently preview-only.
  function isOutlineEditable(status) {
    return status === 'pending';
  }

  $: p = $progress;
  $: displayTitle = $config?.story?.title || p?.title || '';
  $: displaySynopsis = $config?.story?.story_synopsis || p?.story_synopsis || '';
  $: chapters = p?.chapters || [];
  $: arcs = p?.arcs || [];
  $: bookOverview = p?.book_overview || '';
  $: bookOverviewConfirmed = !!p?.book_overview_confirmed;
  $: hasOutline = chapters.length > 0 || arcs.length > 0;
  $: hasAccepted = chapters.some(c => c.status === 'accepted');
  $: inOutlinePhase = p?.phase === 'outline';
  $: editingChapter = chapters.find(ch => ch.num === editingNum);
  $: canEditOutline = !!editingChapter && isOutlineEditable(editingChapter.status) && !$taskRunning;

  $: statusMeta = {
    pending:  { label: $t('outline.status.pending'),  cls: 'badge-ghost' },
    writing:  { label: $t('outline.status.writing'),  cls: 'badge-warning' },
    review:   { label: $t('outline.status.review'),   cls: 'badge-info' },
    accepted: { label: $t('outline.status.accepted'), cls: 'badge-success' },
  };

  let reviseFeedback = '';
  let showRevise = false;

  // 章节编辑状态
  let editingNum = -1;
  let editTitle = '';
  let editOutline = '';

  // 顶层原生弹窗编辑（不受页面滚动容器裁切）
  let showOutlineEditor = false;
  let outlineEditorDialog;
  let editCharactersText = '';

  // 大纲树折叠展开状态（书 → 卷 → 幕 → 章）
  let bookExpanded = true;
  let expandedArcs = new Set();
  let expandedActs = new Set();
  let knownArcs = new Set();
  let knownActs = new Set();

  // 新出现的卷/幕默认展开，已折叠的不受数据更新影响
  $: if (arcs.length) {
    for (const arc of arcs) {
      if (!knownArcs.has(arc.id)) {
        knownArcs.add(arc.id);
        expandedArcs.add(arc.id);
      }
      for (const act of arc.acts || []) {
        const key = `${arc.id}:${act.id}`;
        if (!knownActs.has(key)) {
          knownActs.add(key);
          expandedActs.add(key);
        }
      }
    }
  }

  function toggleArc(id) {
    const next = new Set(expandedArcs);
    if (next.has(id)) next.delete(id); else next.add(id);
    expandedArcs = next;
  }

  function toggleAct(key) {
    const next = new Set(expandedActs);
    if (next.has(key)) next.delete(key); else next.add(key);
    expandedActs = next;
  }

  function expandAll() {
    bookExpanded = true;
    expandedArcs = new Set(arcs.map(a => a.id));
    expandedActs = new Set();
    for (const arc of arcs) for (const act of arc.acts || []) expandedActs.add(`${arc.id}:${act.id}`);
  }

  function collapseAll() {
    bookExpanded = false;
    expandedArcs = new Set();
    expandedActs = new Set();
  }

  function chaptersForArc(arc) {
    return chapters.filter(c => c.num >= arc.start_ch && c.num <= arc.end_ch);
  }

  function chaptersForAct(arc, act) {
    return chapters.filter(c => c.num >= act.start_ch && c.num <= act.end_ch);
  }

  // 卷 / 幕 / 概览 详情弹窗（点击树节点标题查看全文）
  let detailDialog;
  let detailNode = null;

  $: if (detailDialog) {
    if (detailNode && !detailDialog.open) detailDialog.showModal();
    if (!detailNode && detailDialog.open) detailDialog.close();
  }

  function arcHasWriting(arc) {
    return chaptersForArc(arc).some(c => c.status !== 'pending');
  }

  function actHasChapters(arc, act) {
    return chaptersForAct(arc, act).length > 0;
  }

  function buildArcSections(arc) {
    const s = [];
    if (arc.goal) s.push({ label: $t('outline.arcs.goalLabel'), text: arc.goal });
    if (arc.outline) s.push({ label: bookOverview ? $t('outline.arcs.planLabel') : $t('outline.arcs.chapterOutlineLabel'), text: arc.outline });
    if (arc.summary) s.push({ label: $t('outline.arcs.summaryLabel'), text: arc.summary });
    return s;
  }

  function buildActSections(arc, act) {
    const s = [];
    if (act.goal) s.push({ label: $t('outline.acts.goalLabel'), text: act.goal });
    if (act.outline) s.push({ label: $t('outline.acts.outlineLabel'), text: act.outline });
    if (act.summary) s.push({ label: $t('outline.acts.summaryLabel'), text: act.summary });
    return s;
  }

  function openBookDetail() {
    const confirmed = bookOverviewConfirmed;
    detailNode = {
      kind: 'book',
      title: `${displayTitle || $t('common.untitled')}`,
      badge: confirmed ? $t('outline.bookOverview.confirmed') : '',
      editable: !confirmed,
      sections: confirmed ? [{ label: $t('outline.bookOverview.title'), text: bookOverview }] : [],
      fTitle: '', fGoal: '', fCount: 0, fCountDisabled: true,
      fCountLabel: '',
      fOutline: bookOverview,
      fOutlineLabel: $t('outline.bookOverview.title'),
      save: saveBookEdit,
      confirm: confirmed ? null : { label: $t('outline.bookOverview.confirm'), fn: confirmBookOverview },
    };
  }

  function openArcDetail(arc, i) {
    const confirmed = !!arc.confirmed;
    detailNode = {
      kind: 'arc',
      title: arc.title,
      badge: confirmed ? $t('outline.arcs.planDone') : '',
      editable: !confirmed,
      sections: confirmed ? buildArcSections(arc) : (arc.summary ? [{ label: $t('outline.arcs.summaryLabel'), text: arc.summary }] : []),
      fTitle: arc.title || '',
      fGoal: arc.goal || '',
      fCount: arc.end_ch - arc.start_ch + 1,
      fCountDisabled: arcHasWriting(arc) || (bookOverview && arc.acts?.length > 0),
      fCountLabel: $t('outline.arcs.editCount'),
      fOutline: arc.outline || '',
      fOutlineLabel: bookOverview ? $t('outline.arcs.planLabel') : $t('outline.arcs.chapterOutlineLabel'),
      save: () => saveArcEdit(arc),
      confirm: bookOverview && !confirmed && arc.acts?.length ? { label: $t('outline.arcs.planConfirm'), fn: () => confirmArcOutline(arc) } : null,
    };
  }

  function openActDetail(arc, act, j) {
    const confirmed = !!act.confirmed;
    detailNode = {
      kind: 'act',
      title: act.title,
      badge: act.chapters_confirmed ? $t('outline.acts.chaptersDone') : act.confirmed ? $t('outline.acts.outlineDone') : '',
      editable: !confirmed,
      sections: confirmed ? buildActSections(arc, act) : (act.summary ? [{ label: $t('outline.acts.summaryLabel'), text: act.summary }] : []),
      fTitle: act.title || '',
      fGoal: act.goal || '',
      fCount: act.end_ch - act.start_ch + 1,
      fCountDisabled: confirmed || act.chapters_confirmed || actHasChapters(arc, act),
      fCountLabel: $t('outline.acts.editCount'),
      fOutline: act.outline || '',
      fOutlineLabel: $t('outline.acts.outlineLabel'),
      save: () => saveActEdit(arc, act),
      confirm: !confirmed ? { label: $t('outline.acts.confirm'), fn: () => confirmActOutline(arc, act) } : null,
    };
  }

  async function saveBookEdit() {
    try {
      await api('PUT', '/api/book-overview', { outline: detailNode.fOutline });
      progress.set(await api('GET', '/api/progress'));
      addToast($t('outline.toasts.bookOverviewEdited'), 'success');
      detailNode = null;
    } catch (e) { addToast(e.message, 'error'); }
  }

  async function saveArcEdit(arc) {
    try {
      await api('PUT', `/api/arcs/${arc.id}`, {
        title: detailNode.fTitle,
        goal: detailNode.fGoal,
        outline: detailNode.fOutline,
        chapter_count: Number(detailNode.fCount) || 0,
      });
      progress.set(await api('GET', '/api/progress'));
      addToast($t('outline.toasts.arcEdited'), 'success');
      detailNode = null;
    } catch (e) { addToast(e.message, 'error'); }
  }

  async function saveActEdit(arc, act) {
    try {
      await api('PUT', `/api/arcs/${arc.id}/acts/${act.id}`, {
        title: detailNode.fTitle,
        goal: detailNode.fGoal,
        outline: detailNode.fOutline,
        chapter_count: Number(detailNode.fCount) || 0,
      });
      progress.set(await api('GET', '/api/progress'));
      addToast($t('outline.toasts.actEdited'), 'success');
      detailNode = null;
    } catch (e) { addToast(e.message, 'error'); }
  }

  function closeDetail() { detailNode = null; }

  // Cast edit lines: "Name", "Name*", "Name|note", "Name*|note" (* = first appearance)
  function formatCharactersEdit(chars) {
    if (!chars?.length) return '';
    return chars.map(c => {
      let line = c.name || '';
      if (c.first_appearance) line += '*';
      if (c.note) line += '|' + c.note;
      return line;
    }).join('\n');
  }

  function parseCharactersEdit(text) {
    const out = [];
    const seen = new Set();
    for (const raw of (text || '').split('\n')) {
      const line = raw.trim();
      if (!line) continue;
      let namePart = line;
      let note = '';
      const bar = line.indexOf('|');
      if (bar >= 0) {
        namePart = line.slice(0, bar).trim();
        note = line.slice(bar + 1).trim();
      }
      let first = false;
      if (namePart.endsWith('*')) {
        first = true;
        namePart = namePart.slice(0, -1).trim();
      }
      if (!namePart || seen.has(namePart)) continue;
      seen.add(namePart);
      const entry = { name: namePart };
      if (first) entry.first_appearance = true;
      if (note) entry.note = note;
      out.push(entry);
    }
    return out;
  }

  // 导入续写（v3 流水线）
  let showImport = false;
  let importContent = '';
  let importPreview = null; // [{num,title,word_count,preview}]
  let importStatus = null;  // {active,total,cursor} 断点状态
  let continuationCount = 5;

  onMount(refreshImportStatus);
  $: if (!$taskRunning) refreshImportStatus();

  let outlineFocusTried = false;
  $: if (!outlineFocusTried && chapters.length > 0) {
    outlineFocusTried = true;
    focusChapterFromSession();
  }

  // Native dialog enters the browser's top layer, avoiding clipping by the page scroll container.
  $: if (outlineEditorDialog) {
    if (showOutlineEditor && !outlineEditorDialog.open) outlineEditorDialog.showModal();
    if (!showOutlineEditor && outlineEditorDialog.open) outlineEditorDialog.close();
  }

  async function focusChapterFromSession() {
    let raw;
    try { raw = sessionStorage.getItem(OUTLINE_FOCUS_KEY); } catch { return; }
    if (!raw) return;
    try { sessionStorage.removeItem(OUTLINE_FOCUS_KEY); } catch {}
    const num = parseInt(raw, 10);
    if (!num) return;
    const ch = chapters.find(c => c.num === num);
    if (!ch) return;
    // 展开目标章所在的卷/幕，保证行可见
    const ea = new Set(expandedArcs);
    const eA = new Set(expandedActs);
    for (const arc of arcs) {
      if (num >= arc.start_ch && num <= arc.end_ch) {
        ea.add(arc.id);
        for (const act of arc.acts || []) {
          if (num >= act.start_ch && num <= act.end_ch) eA.add(`${arc.id}:${act.id}`);
        }
      }
    }
    expandedArcs = ea;
    expandedActs = eA;
    startEdit(ch);
    await tick();
    const el = document.querySelector(`[data-outline-chapter="${num}"]`);
    if (el) el.scrollIntoView({ block: 'center', behavior: 'smooth' });
  }

  async function refreshImportStatus() {
    try {
      const st = await api('GET', '/api/import/status');
      importStatus = st?.active ? st : null;
    } catch { importStatus = null; }
  }

  async function generateOutline() {
    try {
      await api('POST', '/api/outline/generate');
      addToast($t('outline.toasts.outlineStarted'), 'info');
    } catch (e) { addToast(e.message, 'error'); }
  }

  // 卷（arc）操作
  let arcReqOpenId = -1;
  let arcRequirements = '';
  let showAppendArc = false;
  let appendArcTitle = '';
  let appendArcGoal = '';
  let appendArcCount = 20;

  function arcChapterCounts(arc) {
    const inRange = chapters.filter(c => c.num >= arc.start_ch && c.num <= arc.end_ch);
    return { outlined: inRange.length, total: arc.end_ch - arc.start_ch + 1 };
  }

  async function generateSkeleton() {
    try {
      await api('POST', '/api/arcs/skeleton');
      addToast($t('outline.toasts.skeletonStarted'), 'info');
    } catch (e) { addToast(e.message, 'error'); }
  }

  async function generateArcOutline(arc) {
    try {
      await api('POST', `/api/arcs/${arc.id}/outline`, { requirements: arcReqOpenId === arc.id ? arcRequirements.trim() : '' });
      addToast($t('outline.toasts.arcOutlineStarted'), 'info');
      arcReqOpenId = -1;
      arcRequirements = '';
    } catch (e) { addToast(e.message, 'error'); }
  }

  // 整书概览（书 → 卷 → 幕 → 章 四级大纲流程）
  async function generateBookOverview() {
    try {
      await api('POST', '/api/book-overview/generate');
      addToast($t('outline.toasts.bookOverviewStarted'), 'info');
    } catch (e) { addToast(e.message, 'error'); }
  }

  async function confirmBookOverview() {
    showConfirm($t('outline.toasts.bookOverviewConfirmAsk'), async () => {
      try {
        await api('POST', '/api/book-overview/confirm');
        progress.set(await api('GET', '/api/progress'));
        addToast($t('outline.toasts.bookOverviewConfirmed'), 'success');
        detailNode = null;
      } catch (e) { addToast(e.message, 'error'); }
    });
  }

  async function confirmArcOutline(arc) {
    showConfirm($t('outline.toasts.arcPlanConfirmAsk'), async () => {
      try {
        await api('POST', `/api/arcs/${arc.id}/outline-confirm`);
        progress.set(await api('GET', '/api/progress'));
        addToast($t('outline.toasts.arcPlanConfirmed'), 'success');
        detailNode = null;
      } catch (e) { addToast(e.message, 'error'); }
    });
  }

  async function generateActOutline(arc, act) {
    try {
      await api('POST', `/api/arcs/${arc.id}/acts/${act.id}/outline`);
      addToast($t('outline.toasts.actOutlineStarted'), 'info');
    } catch (e) { addToast(e.message, 'error'); }
  }

  async function confirmActOutline(arc, act) {
    showConfirm($t('outline.toasts.actOutlineConfirmAsk'), async () => {
      try {
        await api('POST', `/api/arcs/${arc.id}/acts/${act.id}/outline-confirm`);
        progress.set(await api('GET', '/api/progress'));
        addToast($t('outline.toasts.actOutlineConfirmed'), 'success');
        detailNode = null;
      } catch (e) { addToast(e.message, 'error'); }
    });
  }

  async function generateActChapters(arc, act) {
    try {
      await api('POST', `/api/arcs/${arc.id}/acts/${act.id}/chapters`);
      addToast($t('outline.toasts.actChaptersStarted'), 'info');
    } catch (e) { addToast(e.message, 'error'); }
  }

  async function confirmActChapters(arc, act) {
    showConfirm($t('outline.toasts.actChaptersConfirmAsk'), async () => {
      try {
        await api('POST', `/api/arcs/${arc.id}/acts/${act.id}/chapters-confirm`);
        progress.set(await api('GET', '/api/progress'));
        addToast($t('outline.toasts.actChaptersConfirmed'), 'success');
      } catch (e) { addToast(e.message, 'error'); }
    });
  }

  function actChapterCounts(arc, act) {
    const inRange = chapters.filter(c => c.num >= act.start_ch && c.num <= act.end_ch);
    return { outlined: inRange.length, total: act.end_ch - act.start_ch + 1 };
  }

  function actComplete(arc, act) {
    const inRange = chapters.filter(c => c.num >= act.start_ch && c.num <= act.end_ch);
    return inRange.length > 0 && inRange.every(c => c.status === 'accepted');
  }

  async function generateActSummary(arc, act) {
    try {
      await api('POST', `/api/arcs/${arc.id}/acts/${act.id}/summary`);
      addToast($t('outline.toasts.actSummaryStarted'), 'info');
    } catch (e) { addToast(e.message, 'error'); }
  }

  async function appendArc() {
    try {
      await api('POST', '/api/arcs/append', {
        title: appendArcTitle.trim(),
        goal: appendArcGoal.trim(),
        chapter_count: Number(appendArcCount) || 20,
      });
      addToast($t('outline.toasts.arcAppendStarted'), 'info');
      showAppendArc = false;
      appendArcTitle = '';
      appendArcGoal = '';
    } catch (e) { addToast(e.message, 'error'); }
  }

  async function confirmOutline() {
    showConfirm($t('outline.toasts.confirmAsk'), async () => {
      try {
        await api('POST', '/api/outline/confirm');
        progress.set(await api('GET', '/api/progress'));
        addToast($t('outline.toasts.outlineConfirmed'), 'success');
        window.location.hash = '#writing';
      } catch (e) { addToast(e.message, 'error'); }
    });
  }

  async function reviseOutline() {
    const fb = reviseFeedback.trim();
    if (!fb) { addToast($t('outline.toasts.reviseFeedbackRequired'), 'error'); return; }
    try {
      await api('POST', '/api/outline/revise', { feedback: fb });
      addToast($t('outline.toasts.reviseStarted'), 'info');
      reviseFeedback = '';
      showRevise = false;
    } catch (e) { addToast(e.message, 'error'); }
  }

  async function deleteOutline() {
    showConfirm($t('outline.toasts.deleteConfirm', { n: chapters.length }), async () => {
      try {
        await api('DELETE', '/api/outline');
        progress.set(await api('GET', '/api/progress'));
        addToast($t('outline.toasts.deleted'), 'success');
      } catch (e) { addToast(e.message, 'error'); }
    });
  }

  async function generateContinuation() {
    try {
      await api('POST', '/api/outline/generate-continuation', { chapter_count: Number(continuationCount) || 5 });
      addToast($t('outline.toasts.continuationStarted'), 'info');
    } catch (e) { addToast(e.message, 'error'); }
  }

  function startEdit(ch) {
    editingNum = ch.num;
    editTitle = ch.title;
    editOutline = ch.outline;
    editCharactersText = formatCharactersEdit(ch.characters);
    showOutlineEditor = true;
  }

  function cancelEdit() {
    editingNum = -1;
    showOutlineEditor = false;
  }

  async function saveEdit() {
    if (!canEditOutline) { addToast($t('outline.toasts.editLocked'), 'error'); return; }
    if (!editTitle.trim() || !editOutline.trim()) { addToast($t('outline.toasts.editRequired'), 'error'); return; }
    try {
      await api('PUT', '/api/outline/' + editingNum, {
        title: editTitle.trim(),
        outline: editOutline.trim(),
        characters: parseCharactersEdit(editCharactersText),
      });
      progress.set(await api('GET', '/api/progress'));
      addToast($t('outline.toasts.editSaved', { num: editingNum }), 'success');
      editingNum = -1;
      showOutlineEditor = false;
    } catch (e) { addToast(e.message, 'error'); }
  }

  async function previewImportSplit() {
    const content = importContent.trim();
    if (!content) { addToast($t('outline.toasts.importContentRequired'), 'error'); return; }
    try {
      const res = await api('POST', '/api/import/split', { content });
      importPreview = res.chapters || [];
    } catch (e) { addToast(e.message, 'error'); }
  }

  async function startImport() {
    try {
      await api('POST', '/api/import/start', { content: importContent.trim() });
      addToast($t('outline.toasts.importStarted'), 'info');
      showImport = false;
      importContent = '';
      importPreview = null;
    } catch (e) { addToast(e.message, 'error'); }
  }

  async function resumeImport() {
    try {
      await api('POST', '/api/import/resume');
      addToast($t('outline.toasts.importResumed'), 'info');
    } catch (e) { addToast(e.message, 'error'); }
  }

  async function confirmCharacterSuggestions() {
    const selected = $outlineCharacterSuggestions.filter(s => s._selected !== false);
    if (selected.length === 0) {
      addToast($t('outline.charSuggestions.noneSelected'), 'error');
      return;
    }
    try {
      await api('POST', '/api/outline/characters/confirm', { characters: selected });
      settings.set(await api('GET', '/api/settings'));
      outlineCharacterSuggestions.set([]);
      outlineCharacterShowSuggestions.set(false);
      addToast($t('outline.charSuggestions.adopted', { n: selected.length }), 'success');
    } catch (e) { addToast(e.message, 'error'); }
  }

  function dismissCharacterSuggestions() {
    outlineCharacterSuggestions.set([]);
    outlineCharacterShowSuggestions.set(false);
  }
</script>

<div class="space-y-3">
  {#if !hasOutline}
    <!-- 空状态 -->
    <div class="text-center py-14 text-base-content/50">
      <div class="text-5xl mb-3">📝</div>
      <p class="text-base mb-1">{$t('outline.empty.title')}</p>
      <p class="text-sm text-base-content/35 mb-6">{$t('outline.empty.hint')}</p>
      <div class="flex justify-center gap-2">
        <button class="btn btn-primary btn-sm" on:click={generateBookOverview} disabled={$taskRunning}>{$t('outline.btn.bookOverview')}</button>
        <button class="btn btn-ghost btn-sm" on:click={generateOutline} disabled={$taskRunning}>{$t('outline.btn.generate')}</button>
        <button class="btn btn-secondary btn-sm" on:click={generateSkeleton} disabled={$taskRunning}>{$t('outline.btn.skeleton')}</button>
        <button class="btn btn-ghost btn-sm" on:click={() => showImport = !showImport} disabled={$taskRunning}>{$t('outline.btn.import')}</button>
      </div>
      <p class="text-xs text-base-content/35 mt-2">{$t('outline.empty.overviewHint')}</p>
    </div>

    {#if showImport}
      <div class="card bg-base-200 shadow-sm">
        <div class="card-body p-4 gap-2">
          <h3 class="card-title text-base">{$t('outline.import.title')}</h3>
          <p class="text-xs text-base-content/50">{$t('outline.import.hint')}</p>
          <textarea class="textarea w-full h-48 text-sm font-serif" bind:value={importContent} on:input={() => importPreview = null} placeholder={$t('outline.import.placeholder')} disabled={$taskRunning}></textarea>
          <div class="flex justify-end gap-2">
            <button class="btn btn-ghost btn-xs" on:click={() => { showImport = false; importContent = ''; importPreview = null; }}>{$t('common.cancel')}</button>
            <button class="btn btn-primary btn-xs" on:click={previewImportSplit} disabled={$taskRunning || !importContent.trim()}>{$t('outline.import.preview')}</button>
          </div>

          {#if importPreview}
            <div class="bg-base-300 rounded-lg p-3 space-y-2">
              <div class="text-sm font-medium">{$t('outline.import.previewTitle', { n: importPreview.length })}</div>
              <div class="max-h-64 overflow-y-auto space-y-1">
                {#each importPreview as ch (ch.num)}
                  <div class="bg-base-100/50 rounded p-2 text-xs flex items-baseline gap-2">
                    <span class="font-bold text-base-content/40 w-8 shrink-0">{ch.num}</span>
                    <span class="font-medium shrink-0">{ch.title}</span>
                    <span class="text-base-content/40 shrink-0">{$t('outline.import.words', { n: ch.word_count })}</span>
                    <span class="text-base-content/50 truncate">{ch.preview}</span>
                  </div>
                {/each}
              </div>
              <p class="text-xs text-base-content/50">{$t('outline.import.startHint')}</p>
              <div class="flex justify-end">
                <button class="btn btn-success btn-xs" on:click={startImport} disabled={$taskRunning || importPreview.length === 0}>{$t('outline.import.start')}</button>
              </div>
            </div>
          {/if}
        </div>
      </div>
    {/if}
  {:else}
    {#if importStatus}
      <div class="alert alert-warning py-2 text-sm flex items-center justify-between">
        <span>{$t('outline.import.resumeBanner', { done: importStatus.cursor, total: importStatus.total })}</span>
        <button class="btn btn-primary btn-xs" on:click={resumeImport} disabled={$taskRunning}>{$t('outline.import.resume')}</button>
      </div>
    {/if}
    <ConfigChangePanel />

    {#if $outlineCharacterShowSuggestions && $outlineCharacterSuggestions.length > 0}
      <div class="card bg-base-200 border border-primary/30 shadow-sm">
        <div class="card-body py-4 gap-3">
          <h3 class="font-semibold">{$t('outline.charSuggestions.title', { n: $outlineCharacterSuggestions.length })}</h3>
          <p class="text-sm text-base-content/60">{$t('outline.charSuggestions.hint')}</p>
          <div class="space-y-2 max-h-72 overflow-y-auto">
            {#each $outlineCharacterSuggestions as s}
              <label class="flex gap-3 p-3 rounded-lg bg-base-300/50 cursor-pointer">
                <input type="checkbox" class="checkbox checkbox-sm mt-1" bind:checked={s._selected} />
                <div class="min-w-0 flex-1">
                  <div class="font-medium">{s.name}</div>
                  {#if s.description}
                    <div class="text-sm text-base-content/70 mt-1">{s.description}</div>
                  {/if}
                  <div class="text-xs text-base-content/50 mt-1">
                    {$t('outline.charSuggestions.line', { chapter: s.chapter_num, role: s.role || $t('outline.charSuggestions.noRole') })}
                  </div>
                </div>
              </label>
            {/each}
          </div>
          <div class="flex gap-2">
            <button class="btn btn-primary btn-sm" disabled={$taskRunning} on:click={confirmCharacterSuggestions}>{$t('outline.charSuggestions.adopt')}</button>
            <button class="btn btn-ghost btn-sm" on:click={dismissCharacterSuggestions}>{$t('outline.charSuggestions.dismiss')}</button>
          </div>
        </div>
      </div>
    {/if}

    <!-- 操作栏 -->
    <div class="card bg-base-200 shadow-sm">
      <div class="card-body p-4 gap-2">
        <div class="flex items-center gap-2 flex-wrap">
          <h3 class="text-base font-semibold flex-1 min-w-0 truncate">📖 {displayTitle || $t('common.untitled')}</h3>
          {#if inOutlinePhase && !bookOverview}
            <button class="btn btn-success btn-xs" on:click={confirmOutline} disabled={$taskRunning || chapters.length === 0}>{$t('outline.btn.confirm')}</button>
          {/if}
          <button class="btn btn-ghost btn-xs" on:click={() => showRevise = !showRevise} disabled={$taskRunning}>{$t('outline.btn.revise')}</button>
          {#if hasAccepted}
            <div class="join">
              <input type="number" min="1" max="50" class="input input-xs join-item w-14" bind:value={continuationCount} disabled={$taskRunning} />
              <button class="btn btn-primary btn-xs join-item" on:click={generateContinuation} disabled={$taskRunning}>{$t('outline.btn.continuation')}</button>
            </div>
          {:else if inOutlinePhase}
            <button class="btn btn-ghost btn-xs" on:click={generateOutline} disabled={$taskRunning}>{$t('outline.btn.regenerate')}</button>
          {/if}
          {#if !hasAccepted}
            <button class="btn btn-ghost btn-xs text-error" on:click={deleteOutline} disabled={$taskRunning}>{$t('outline.btn.deleteOutline')}</button>
          {/if}
        </div>

        {#if showRevise}
          <div class="bg-base-300 rounded-lg p-3 space-y-2">
            <textarea class="textarea textarea-sm w-full h-20 text-sm" bind:value={reviseFeedback} placeholder={$t('outline.revise.placeholder')} disabled={$taskRunning}></textarea>
            <div class="flex justify-between items-center">
              <span class="text-xs text-base-content/40">{$t('outline.revise.hint')}</span>
              <div class="flex gap-2">
                <button class="btn btn-ghost btn-xs" on:click={() => { showRevise = false; reviseFeedback = ''; }}>{$t('common.cancel')}</button>
                <button class="btn btn-primary btn-xs" on:click={reviseOutline} disabled={$taskRunning || !reviseFeedback.trim()}>{$t('outline.revise.submit')}</button>
              </div>
            </div>
          </div>
        {/if}

        {#if p.core_prompt}
          <div>
            <span class="text-xs text-base-content/50">{$t('outline.corePrompt')}</span>
            <div class="bg-base-300 rounded p-2 text-sm mt-0.5 max-h-24 overflow-y-auto">{p.core_prompt}</div>
          </div>
        {/if}
        {#if displaySynopsis}
          <div>
            <span class="text-xs text-base-content/50">{$t('outline.synopsis')}</span>
            <div class="bg-base-300 rounded p-2 text-sm mt-0.5 max-h-24 overflow-y-auto">{displaySynopsis}</div>
          </div>
        {/if}
      </div>
    </div>

    <!-- 大纲树状结构（书 → 卷 → 幕 → 章，点击节点查看详情，卷/幕可折叠） -->
    <div class="card bg-base-200 shadow-sm">
      <div class="card-body p-4 gap-2">
        <div class="flex items-center justify-between flex-wrap gap-2">
          <h4 class="text-sm font-semibold text-base-content/60">
            {#if bookOverview}{$t('outline.tree.title')}{:else}{$t('outline.arcs.title')}{/if}
            <span class="font-normal text-base-content/35">({chapters.length})</span>
          </h4>
          <div class="flex items-center gap-1.5">
            <button class="btn btn-ghost btn-xs" on:click={expandAll} disabled={$taskRunning}>{$t('outline.tree.expandAll')}</button>
            <button class="btn btn-ghost btn-xs" on:click={collapseAll} disabled={$taskRunning}>{$t('outline.tree.collapseAll')}</button>
            {#if arcs.length > 0}
              <button class="btn btn-ghost btn-xs" on:click={() => showAppendArc = !showAppendArc} disabled={$taskRunning}>{$t('outline.arcs.append')}</button>
            {/if}
          </div>
        </div>

        {#if showAppendArc}
          <div class="bg-base-300 rounded-lg p-3 space-y-2">
            <div class="flex gap-2">
              <input type="text" class="input input-sm flex-1" bind:value={appendArcTitle} placeholder={$t('outline.arcs.appendTitle')} disabled={$taskRunning} />
              <input type="number" min="1" max="100" class="input input-sm w-20" bind:value={appendArcCount} disabled={$taskRunning} title={$t('outline.arcs.appendCount')} />
            </div>
            <textarea class="textarea textarea-sm w-full h-16 text-sm" bind:value={appendArcGoal} placeholder={$t('outline.arcs.appendGoal')} disabled={$taskRunning}></textarea>
            <div class="flex justify-end gap-2">
              <button class="btn btn-ghost btn-xs" on:click={() => showAppendArc = false}>{$t('common.cancel')}</button>
              <button class="btn btn-primary btn-xs" on:click={appendArc} disabled={$taskRunning}>{$t('outline.arcs.appendSubmit')}</button>
            </div>
          </div>
        {/if}

        <div class="space-y-0.5">
          {#if bookOverview}
            <!-- 书级：整书概览 -->
            <div class="flex items-center gap-1.5 rounded-lg bg-base-300 px-2 py-1.5">
              <button class="btn btn-ghost btn-xs btn-square shrink-0 w-5 h-5 p-0" on:click={() => bookExpanded = !bookExpanded} aria-label={$t('outline.tree.toggle')}>
                {bookExpanded ? '▾' : '▸'}
              </button>
              <button class="flex-1 min-w-0 text-left" on:click={openBookDetail}>
                <span class="text-sm font-semibold truncate">📖 {displayTitle || $t('common.untitled')}</span>
                {#if bookOverviewConfirmed}
                  <span class="badge badge-xs badge-success ml-1 align-middle">{$t('outline.bookOverview.confirmed')}</span>
                {/if}
              </button>
              {#if !bookOverviewConfirmed}
                <button class="btn btn-success btn-xs shrink-0" on:click={confirmBookOverview} disabled={$taskRunning}>{$t('outline.bookOverview.confirm')}</button>
              {/if}
            </div>
            {#if !bookOverviewConfirmed}
              <p class="text-xs text-base-content/45 px-2 ml-8">{$t('outline.bookOverview.unlockedHint')}</p>
            {/if}

            {#if bookExpanded}
              <div class="ml-4 pl-2 border-l border-base-300 space-y-0.5">
                {#each arcs as arc, i (arc.id)}
                  <div class="flex items-center gap-1.5 rounded-lg bg-base-300/70 px-2 py-1.5">
                    <button class="btn btn-ghost btn-xs btn-square shrink-0 w-5 h-5 p-0" on:click={() => toggleArc(arc.id)} aria-label={$t('outline.tree.toggle')}>
                      {expandedArcs.has(arc.id) ? '▾' : '▸'}
                    </button>
                    <button class="flex-1 min-w-0 text-left flex items-center gap-2" on:click={() => openArcDetail(arc, i)}>
                      <span class="text-sm font-medium truncate">{arc.title}</span>
                      <span class="text-xs text-base-content/40 shrink-0">{$t('outline.arcs.range', { start: arc.start_ch, end: arc.end_ch })}</span>
                    </button>
                    {#if arc.confirmed}
                      <span class="badge badge-xs badge-success shrink-0">{$t('outline.arcs.planDone')}</span>
                    {/if}
                    <div class="shrink-0 flex items-center gap-1">
                      <button class="btn btn-primary btn-xs" on:click={() => generateArcOutline(arc)} disabled={$taskRunning || !bookOverviewConfirmed}>
                        {arc.acts?.length ? $t('outline.arcs.regenPlan') : $t('outline.arcs.genPlan')}
                      </button>
                      <button class="btn btn-success btn-xs" on:click={() => confirmArcOutline(arc)} disabled={$taskRunning || arc.confirmed || !arc.acts?.length}>
                        {$t('outline.arcs.planConfirm')}
                      </button>
                    </div>
                  </div>

                  {#if expandedArcs.has(arc.id)}
                    <div class="ml-4 pl-2 border-l border-base-300 space-y-0.5">
                      {#if arc.acts?.length}
                        {#each arc.acts as act, j (act.id)}
                          {@const actCounts = actChapterCounts(arc, act)}
                          <div class="flex items-center gap-1.5 rounded-lg bg-base-200/70 px-2 py-1.5">
                            <button class="btn btn-ghost btn-xs btn-square shrink-0 w-5 h-5 p-0" on:click={() => toggleAct(`${arc.id}:${act.id}`)} aria-label={$t('outline.tree.toggle')}>
                              {expandedActs.has(`${arc.id}:${act.id}`) ? '▾' : '▸'}
                            </button>
                            <button class="flex-1 min-w-0 text-left flex items-center gap-2" on:click={() => openActDetail(arc, act, j)}>
                              <span class="text-sm font-medium truncate">{act.title}</span>
                              <span class="text-xs text-base-content/40 shrink-0">{$t('outline.arcs.range', { start: act.start_ch, end: act.end_ch })}</span>
                            </button>
                            {#if act.confirmed}
                              <span class="badge badge-xs badge-info shrink-0">{$t('outline.acts.outlineDone')}</span>
                            {/if}
                            {#if act.chapters_confirmed}
                              <span class="badge badge-xs badge-success shrink-0">{$t('outline.acts.chaptersDone')}</span>
                            {/if}
                            <div class="shrink-0 flex items-center gap-1">
                              {#if !act.confirmed}
                                <button class="btn btn-primary btn-xs" on:click={() => generateActOutline(arc, act)} disabled={$taskRunning || !arc.confirmed}>
                                  {act.outline ? $t('outline.acts.regenOutline') : $t('outline.acts.genOutline')}
                                </button>
                                <button class="btn btn-success btn-xs" on:click={() => confirmActOutline(arc, act)} disabled={$taskRunning || !act.outline}>{$t('outline.acts.confirm')}</button>
                              {/if}
                              {#if act.confirmed && !act.chapters_confirmed}
                                <button class="btn btn-primary btn-xs" on:click={() => generateActChapters(arc, act)} disabled={$taskRunning}>
                                  {actCounts.outlined > 0 ? $t('outline.acts.regenChapters') : $t('outline.acts.genChapters')}
                                </button>
                                <button class="btn btn-success btn-xs" on:click={() => confirmActChapters(arc, act)} disabled={$taskRunning || actCounts.outlined === 0}>
                                  {$t('outline.acts.confirmChapters')}
                                </button>
                              {/if}
                              {#if actComplete(arc, act) && !act.summary}
                                <button class="btn btn-warning btn-xs" on:click={() => generateActSummary(arc, act)} disabled={$taskRunning}>{$t('outline.acts.genSummary')}</button>
                              {/if}
                            </div>
                          </div>

                          {#if expandedActs.has(`${arc.id}:${act.id}`)}
                            <div class="ml-4 pl-2 border-l border-base-300 space-y-0.5">
                              {#if actComplete(arc, act) && !act.summary}
                                <p class="text-xs text-warning px-1 pb-0.5">{$t('outline.acts.summaryPending')}</p>
                              {/if}
                              {#if act.confirmed && !act.chapters_confirmed}
                                <div class="flex items-center gap-2 px-1 pb-0.5">
                                  <span class="text-xs text-base-content/45">{$t('outline.acts.progress', { n: actCounts.outlined, total: actCounts.total })}</span>
                                  <div class="progress progress-primary h-1 flex-1 max-w-40" value={actCounts.outlined} max={actCounts.total}></div>
                                </div>
                              {/if}
                              {#each chaptersForAct(arc, act) as ch (ch.num)}
                                <OutlineChapterRow {ch} {statusMeta} editable={isOutlineEditable(ch.status) && !$taskRunning} onEdit={() => startEdit(ch)} />
                              {/each}
                            </div>
                          {/if}
                        {/each}
                      {:else}
                        {#each chaptersForArc(arc) as ch (ch.num)}
                          <OutlineChapterRow {ch} {statusMeta} editable={isOutlineEditable(ch.status) && !$taskRunning} onEdit={() => startEdit(ch)} />
                        {/each}
                      {/if}
                    </div>
                  {/if}
                {/each}
              </div>
            {/if}
          {:else if arcs.length > 0}
            <!-- v3：卷 → 章（无幕层） -->
            {#each arcs as arc, i (arc.id)}
              {@const counts = arcChapterCounts(arc)}
              <div class="flex items-center gap-1.5 rounded-lg bg-base-300/70 px-2 py-1.5">
                <button class="btn btn-ghost btn-xs btn-square shrink-0 w-5 h-5 p-0" on:click={() => toggleArc(arc.id)} aria-label={$t('outline.tree.toggle')}>
                  {expandedArcs.has(arc.id) ? '▾' : '▸'}
                </button>
                <button class="flex-1 min-w-0 text-left flex items-center gap-2" on:click={() => openArcDetail(arc, i)}>
                  <span class="text-sm font-medium truncate">{arc.title}</span>
                  <span class="text-xs text-base-content/40 shrink-0">{$t('outline.arcs.range', { start: arc.start_ch, end: arc.end_ch })}</span>
                </button>
                <span class="badge badge-xs {counts.outlined >= counts.total ? 'badge-success' : 'badge-ghost'} shrink-0">{$t('outline.arcs.outlined', { n: counts.outlined, total: counts.total })}</span>
                {#if arc.summary}
                  <span class="badge badge-xs badge-info shrink-0">{$t('outline.arcs.summaryDone')}</span>
                {/if}
                <div class="shrink-0 flex items-center gap-1">
                  <button class="btn btn-primary btn-xs" on:click={() => generateArcOutline(arc)} disabled={$taskRunning}>
                    {counts.outlined > 0 ? $t('outline.arcs.regenOutline') : $t('outline.arcs.genOutline')}
                  </button>
                  <button class="btn btn-ghost btn-xs" on:click={() => { arcReqOpenId = arcReqOpenId === arc.id ? -1 : arc.id; arcRequirements = ''; }} disabled={$taskRunning}>+</button>
                </div>
              </div>

              {#if arcReqOpenId === arc.id}
                <textarea class="textarea textarea-sm w-full h-14 text-sm mt-1 ml-8" bind:value={arcRequirements} placeholder={$t('outline.arcs.reqPlaceholder')} disabled={$taskRunning}></textarea>
              {/if}

              {#if expandedArcs.has(arc.id)}
                <div class="ml-4 pl-2 border-l border-base-300 space-y-0.5">
                  {#each chaptersForArc(arc) as ch (ch.num)}
                    <OutlineChapterRow {ch} {statusMeta} editable={isOutlineEditable(ch.status) && !$taskRunning} onEdit={() => startEdit(ch)} />
                  {/each}
                </div>
              {/if}
            {/each}
          {:else}
            <!-- v2：扁平章节列表 -->
            {#each chapters as ch (ch.num)}
              <OutlineChapterRow {ch} {statusMeta} editable={isOutlineEditable(ch.status) && !$taskRunning} onEdit={() => startEdit(ch)} />
            {/each}
          {/if}
        </div>

        {#if $streamingChapterIdx >= 0 && $streamingContent}
          <div class="bg-base-300 rounded p-3 mt-1 text-sm max-h-48 overflow-y-auto chapter-content">
            <div class="text-xs text-base-content/40 mb-1 flex items-center gap-1">
              <span class="loading loading-dots loading-xs"></span> {$t('outline.streamHint')}
            </div>
            {$streamingContent}
          </div>
        {/if}
      </div>
    </div>

    <dialog
      bind:this={outlineEditorDialog}
      class="outline-editor-dialog m-auto w-[calc(100vw-1.5rem)] max-w-3xl rounded-xl border border-base-content/20 bg-base-100 p-0 text-base-content shadow-2xl backdrop:bg-black/60 sm:w-[calc(100vw-2rem)]"
      on:cancel|preventDefault={cancelEdit}
      on:close={() => { showOutlineEditor = false; editingNum = -1; }}
      aria-label={$t('outline.chapter.chapterLabel', { num: editingNum })}
    >
      <div class="flex h-[min(82dvh,42rem)] max-h-[calc(100dvh-1.5rem)] flex-col">
        <div class="flex items-center gap-3 border-b border-base-300 px-4 py-3 sm:px-5 sm:py-4 shrink-0">
          <span class="text-sm font-bold text-base-content/50 shrink-0">{$t('outline.chapter.chapterLabel', { num: editingNum })}</span>
          {#if canEditOutline}
            <input type="text" class="input input-sm flex-1 min-w-0" bind:value={editTitle} placeholder={$t('outline.chapter.titlePlaceholder')} />
          {:else}
            <span class="font-medium flex-1 min-w-0 truncate">{editingChapter?.title}</span>
            <span class="badge badge-xs {statusMeta[editingChapter?.status]?.cls || 'badge-ghost'}">{statusMeta[editingChapter?.status]?.label || editingChapter?.status}</span>
          {/if}
          <button class="btn btn-ghost btn-xs btn-circle shrink-0" on:click={cancelEdit} aria-label={$t('common.close')}>✕</button>
        </div>
        <div class="min-h-0 flex-1 overflow-y-auto p-3 sm:p-5">
          {#if canEditOutline}
            <textarea class="textarea textarea-sm h-full min-h-56 w-full resize-none text-sm leading-6" bind:value={editOutline} placeholder={$t('outline.chapter.outlinePlaceholder')}></textarea>
            <div class="mt-3">
              <span class="text-xs text-base-content/50 mb-1 block">{$t('outline.chapter.castLabel')}</span>
              <textarea class="textarea textarea-sm w-full h-20 text-sm font-mono" bind:value={editCharactersText} placeholder={$t('outline.chapter.castPlaceholder')} disabled={$taskRunning}></textarea>
              <p class="text-[11px] text-base-content/35 mt-0.5">{$t('outline.chapter.castHint')}</p>
            </div>
          {:else}
            <div class="h-full min-h-56 whitespace-pre-wrap rounded-lg bg-base-200 p-3 text-sm leading-6">{editingChapter?.outline}</div>
            <p class="mt-2 text-xs text-base-content/50">{$t('outline.chapter.readonlyHint')}</p>
          {/if}
        </div>
        <div class="flex justify-end gap-2 border-t border-base-300 px-4 py-3 sm:px-5 sm:py-4 shrink-0">
          <button class="btn btn-ghost btn-sm" on:click={cancelEdit}>{canEditOutline ? $t('common.cancel') : $t('common.close')}</button>
          {#if canEditOutline}
            <button class="btn btn-success btn-sm" on:click={saveEdit}>{$t('common.save')}</button>
          {/if}
        </div>
      </div>
    </dialog>

    <!-- 卷 / 幕 / 整书概览 详情弹窗（确认前可编辑） -->
    <dialog
      bind:this={detailDialog}
      class="m-auto w-[calc(100vw-1.5rem)] max-w-3xl rounded-xl border border-base-content/20 bg-base-100 p-0 text-base-content shadow-2xl backdrop:bg-black/60 sm:w-[calc(100vw-2rem)]"
      on:cancel|preventDefault={closeDetail}
      on:close={() => { detailNode = null; }}
      aria-label={detailNode?.title || ''}
    >
      <div class="flex h-[min(82dvh,42rem)] max-h-[calc(100dvh-1.5rem)] flex-col">
        <div class="flex items-center gap-3 border-b border-base-300 px-4 py-3 sm:px-5 sm:py-4 shrink-0">
          <span class="text-sm font-semibold flex-1 min-w-0 truncate">{detailNode?.title}</span>
          {#if detailNode?.editable}
            <span class="badge badge-xs badge-warning shrink-0">{$t('outline.detail.editing')}</span>
          {/if}
          {#if detailNode?.badge}
            <span class="badge badge-xs badge-success shrink-0">{detailNode.badge}</span>
          {/if}
          <button class="btn btn-ghost btn-xs btn-circle shrink-0" on:click={closeDetail} aria-label={$t('common.close')}>✕</button>
        </div>
        <div class="min-h-0 flex-1 overflow-y-auto p-3 sm:p-5 space-y-4">
          {#if detailNode?.editable}
            <div class="space-y-3">
              {#if detailNode.kind !== 'book'}
                <div class="flex gap-2">
                  <div class="flex-1 min-w-0">
                    <div class="text-xs text-base-content/50 mb-1">{$t('outline.detail.titleLabel')}</div>
                    <input type="text" class="input input-sm w-full" bind:value={detailNode.fTitle} />
                  </div>
                  {#if !detailNode.fCountDisabled}
                    <div class="w-32 shrink-0">
                      <div class="text-xs text-base-content/50 mb-1">{detailNode.fCountLabel}</div>
                      <input type="number" min="1" max="500" class="input input-sm w-full" bind:value={detailNode.fCount} />
                    </div>
                  {/if}
                </div>
                <div>
                  <div class="text-xs text-base-content/50 mb-1">{$t('outline.detail.goalLabel')}</div>
                  <textarea class="textarea textarea-sm w-full h-20 text-sm" bind:value={detailNode.fGoal}></textarea>
                </div>
              {/if}
              <div>
                <div class="text-xs text-base-content/50 mb-1">{detailNode.fOutlineLabel}</div>
                <textarea class="textarea textarea-sm w-full min-h-56 text-sm leading-6" bind:value={detailNode.fOutline}></textarea>
              </div>
              {#if detailNode.sections?.length}
                <div class="space-y-4 border-t border-base-300 pt-2">
                  {#each detailNode.sections as s}
                    <div>
                      {#if s.label}
                        <div class="text-xs text-base-content/50 mb-1">{s.label}</div>
                      {/if}
                      <div class="whitespace-pre-wrap rounded-lg bg-base-200 p-3 text-sm leading-6">{s.text}</div>
                    </div>
                  {/each}
                </div>
              {/if}
            </div>
          {:else}
            {#if detailNode?.sections?.length}
              {#each detailNode.sections as s}
                <div>
                  {#if s.label}
                    <div class="text-xs text-base-content/50 mb-1">{s.label}</div>
                  {/if}
                  <div class="whitespace-pre-wrap rounded-lg bg-base-200 p-3 text-sm leading-6">{s.text}</div>
                </div>
              {/each}
            {:else}
              <p class="text-sm text-base-content/45">{$t('outline.detail.empty')}</p>
            {/if}
          {/if}
        </div>
        <div class="flex justify-end gap-2 border-t border-base-300 px-4 py-3 sm:px-5 sm:py-4 shrink-0">
          <button class="btn btn-ghost btn-sm" on:click={closeDetail}>{detailNode?.editable ? $t('common.cancel') : $t('common.close')}</button>
          {#if detailNode?.confirm}
            <button class="btn btn-success btn-sm" on:click={detailNode.confirm.fn} disabled={$taskRunning}>{detailNode.confirm.label}</button>
          {/if}
          {#if detailNode?.editable}
            <button class="btn btn-primary btn-sm" on:click={detailNode.save} disabled={$taskRunning}>{$t('common.save')}</button>
          {/if}
        </div>
      </div>
    </dialog>
  {/if}
</div>

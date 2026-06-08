import { writable, get } from 'svelte/store';

export const apiConfig = writable(null);
export const config = writable(null);
export const progress = writable(null);
export const settings = writable(null);
export const skills = writable([]);
export const taskRunning = writable(false);

export const currentPage = writable('config');
export const selectedChapter = writable(-1);

export const streamingContent = writable('');
export const streamingChapterIdx = writable(-1);

export const chatSessions = writable(null);
export const currentChatSession = writable(null);

export const continueAnalysis = writable(null);
export const editingChapterNum = writable(-1);
export const editingCharID = writable(null);
export const editingWvID = writable(null);
export const wvFilter = writable('all');

export const logEntries = writable([]);

export function addLog(entry) {
  logEntries.update(entries => {
    const next = [...entries, entry];
    return next.length > 500 ? next.slice(-500) : next;
  });
}

export function addToast(msg, type = 'info') {
  const id = Date.now();
  const unsub = toastStore.subscribe(() => {});
  toastStore.update(t => [...t, { id, msg, type }]);
  unsub();
  setTimeout(() => {
    toastStore.update(t => t.filter(x => x.id !== id));
  }, 3000);
}

export const toastStore = writable([]);

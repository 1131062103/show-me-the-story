package main

import "testing"

func TestRejectCurrentChapterDraftClearsReviewDraft(t *testing.T) {
	state := &Progress{
		Phase:               "writing",
		CurrentChapterIndex: 1,
		Chapters: []ChapterState{
			{Num: 1, Status: StatusAccepted, Content: "accepted", Summary: "summary"},
			{Num: 2, Status: StatusReview, Content: "draft", Summary: "draft summary"},
			{Num: 3, Status: StatusPending},
		},
		MemoryEntries: []MemoryEntry{
			{ID: 1, Chapter: 1, Content: "keep"},
			{ID: 2, Chapter: 2, Content: "drop"},
		},
	}

	num, err := RejectCurrentChapterDraft(state, t.TempDir())
	if err != nil {
		t.Fatalf("RejectCurrentChapterDraft() error = %v", err)
	}
	if num != 2 {
		t.Fatalf("rejected chapter = %d, want 2", num)
	}
	ch := state.Chapters[1]
	if ch.Status != StatusPending || ch.Content != "" || ch.Summary != "" {
		t.Fatalf("chapter after reject = %+v, want pending with empty content and summary", ch)
	}
	if state.CurrentChapterIndex != 1 {
		t.Fatalf("current index = %d, want 1", state.CurrentChapterIndex)
	}
	if len(state.MemoryEntries) != 1 || state.MemoryEntries[0].Chapter != 1 {
		t.Fatalf("memory entries = %+v, want only chapter 1 memory", state.MemoryEntries)
	}
}

func TestRejectCurrentChapterDraftRequiresReview(t *testing.T) {
	state := &Progress{
		Phase:               "writing",
		CurrentChapterIndex: 0,
		Chapters:            []ChapterState{{Num: 1, Status: StatusPending}},
	}

	if _, err := RejectCurrentChapterDraft(state, t.TempDir()); err != ErrNoReviewChapterToReject {
		t.Fatalf("RejectCurrentChapterDraft() error = %v, want %v", err, ErrNoReviewChapterToReject)
	}
}
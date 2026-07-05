package main

import "testing"

func TestMergeLockedParagraphsRestoresLockedText(t *testing.T) {
	original := "第一段原文。\n\n第二段原文。\n\n第三段原文。"
	revised := "第一段改写。\n\n第二段乱改。\n\n第三段改写。"

	got := mergeLockedParagraphs(original, revised, []int{2})
	want := "第一段改写。\n\n第二段原文。\n\n第三段改写。"
	if got != want {
		t.Fatalf("mergeLockedParagraphs() = %q, want %q", got, want)
	}
}

func TestSetChapterParagraphLocksFiltersInvalidNumbers(t *testing.T) {
	state := &Progress{Chapters: []ChapterState{{Num: 3, Content: "一\n\n二\n\n三"}}}

	if err := SetChapterParagraphLocks(state, 3, []int{3, 1, 9, -1}); err != nil {
		t.Fatalf("SetChapterParagraphLocks() error = %v", err)
	}
	got := state.Chapters[0].ParagraphLocks
	want := []int{1, 3}
	if len(got) != len(want) {
		t.Fatalf("locks = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("locks = %v, want %v", got, want)
		}
	}
}
package main

import "testing"

func TestEditChapterContentReplaceParagraphs(t *testing.T) {
	state := &Progress{Chapters: []ChapterState{{Num: 1, Content: "第一段。\n\n第二段。\n\n第三段。"}}}

	_, err := EditChapterContent(state, EditChapterContentRequest{
		ChapterNum:     1,
		Operation:      EditOpReplaceParagraphs,
		StartParagraph: 2,
		EndParagraph:   2,
		NewText:        "新的第二段。\n\n新增第三段。",
	})
	if err != nil {
		t.Fatalf("EditChapterContent() error = %v", err)
	}
	want := "第一段。\n\n新的第二段。\n\n新增第三段。\n\n第三段。"
	if state.Chapters[0].Content != want {
		t.Fatalf("content = %q, want %q", state.Chapters[0].Content, want)
	}
}

func TestEditChapterContentInsertAfterParagraph(t *testing.T) {
	state := &Progress{Chapters: []ChapterState{{Num: 1, Content: "第一段。\n\n第二段。"}}}

	_, err := EditChapterContent(state, EditChapterContentRequest{
		ChapterNum: 1,
		Operation:  EditOpInsertAfterParagraph,
		Paragraph:  1,
		NewText:    "插入段。",
	})
	if err != nil {
		t.Fatalf("EditChapterContent() error = %v", err)
	}
	want := "第一段。\n\n插入段。\n\n第二段。"
	if state.Chapters[0].Content != want {
		t.Fatalf("content = %q, want %q", state.Chapters[0].Content, want)
	}
}

func TestEditChapterContentDeleteParagraphs(t *testing.T) {
	state := &Progress{Chapters: []ChapterState{{Num: 1, Content: "第一段。\n\n第二段。\n\n第三段。"}}}

	_, err := EditChapterContent(state, EditChapterContentRequest{
		ChapterNum:     1,
		Operation:      EditOpDeleteParagraphs,
		StartParagraph: 2,
		EndParagraph:   2,
	})
	if err != nil {
		t.Fatalf("EditChapterContent() error = %v", err)
	}
	want := "第一段。\n\n第三段。"
	if state.Chapters[0].Content != want {
		t.Fatalf("content = %q, want %q", state.Chapters[0].Content, want)
	}
}

func TestEditChapterContentRejectsLockedParagraphReplacement(t *testing.T) {
	state := &Progress{Chapters: []ChapterState{{
		Num:            1,
		Content:        "第一段。\n\n第二段。\n\n第三段。",
		ParagraphLocks: []int{2},
	}}}

	_, err := EditChapterContent(state, EditChapterContentRequest{
		ChapterNum:     1,
		Operation:      EditOpReplaceParagraphs,
		StartParagraph: 2,
		EndParagraph:   2,
		NewText:        "锁定段改写。",
	})
	if err == nil {
		t.Fatal("EditChapterContent() expected locked paragraph error")
	}
	want := "第一段。\n\n第二段。\n\n第三段。"
	if state.Chapters[0].Content != want {
		t.Fatalf("content = %q, want %q", state.Chapters[0].Content, want)
	}
}

func TestEditChapterContentDeleteLinesAllowsEmptyNewText(t *testing.T) {
	state := &Progress{Chapters: []ChapterState{{Num: 1, Content: "一\n二\n三"}}}

	_, err := EditChapterContent(state, EditChapterContentRequest{
		ChapterNum: 1,
		Operation:  EditOpDeleteLines,
		StartLine:  2,
		EndLine:    2,
	})
	if err != nil {
		t.Fatalf("EditChapterContent() error = %v", err)
	}
	want := "一\n三"
	if state.Chapters[0].Content != want {
		t.Fatalf("content = %q, want %q", state.Chapters[0].Content, want)
	}
}

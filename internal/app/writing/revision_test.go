package writing

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"showmethestory/internal/app/runtime"
	"showmethestory/internal/domain/project"
)

func TestRevisePreservesLockedParagraphsAndRefreshesArtifact(t *testing.T) {
	original := "first original\n\nlocked original\n\nthird original"
	progress := &project.Progress{Phase: "writing", CurrentChapterIndex: 0, Chapters: []project.Chapter{{Num: 1, Title: "One", Content: original, Summary: "old", Status: project.StatusReview, ParagraphLocks: []int{2}}}}
	session, store := writingSession(t, progress)
	ai := &fakeAI{stream: []string{"first revised\n\nlocked changed\n\nthird revised"}, completes: []string{"new summary", `{"new_memories":[],"updates":[]}`}}
	events := &events{}
	service := New(Dependencies{Session: session, AI: ai, Events: events, Model: "model"})
	if err := service.Revise(context.Background(), "make it colder"); err != nil {
		t.Fatal(err)
	}
	chapter := session.Snapshot().Project.Progress.Chapters[0]
	want := "first revised\n\nlocked original\n\nthird revised"
	if chapter.Content != want || chapter.Summary != "new summary" || chapter.Status != project.StatusReview {
		t.Fatalf("chapter = %+v", chapter)
	}
	markdown, err := os.ReadFile(store.ChapterMarkdownPath(1))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(markdown), want) || !strings.Contains(string(markdown), "new summary") {
		t.Fatalf("markdown = %q", markdown)
	}
	if events.named("stream_start") != 1 || events.named("content_chunk") != 2 || events.named("progress_update") != 1 {
		t.Fatalf("events = %+v", events.values)
	}
}

func TestReviseSpecificOnlyChangesSelectedChapterAndKeepsCursor(t *testing.T) {
	progress := &project.Progress{Phase: "writing", CurrentChapterIndex: 1, Chapters: []project.Chapter{
		{Num: 1, Title: "First", Content: "first content", Summary: "first summary", Status: project.StatusAccepted},
		{Num: 2, Title: "Second", Content: "second content", Summary: "second summary", Status: project.StatusReview},
	}}
	session, _ := writingSession(t, progress)
	ai := &fakeAI{stream: []string{"first revised"}, completes: []string{"updated first summary", `{"new_memories":[],"updates":[]}`}}
	if err := New(Dependencies{Session: session, AI: ai, Model: "model"}).ReviseSpecific(context.Background(), 1, "improve the opening"); err != nil {
		t.Fatal(err)
	}
	got := session.Snapshot().Project.Progress
	if got.CurrentChapterIndex != 1 {
		t.Fatalf("cursor = %d, want 1", got.CurrentChapterIndex)
	}
	if got.Chapters[0].Content != "first revised" || got.Chapters[0].Summary != "updated first summary" || got.Chapters[0].Status != project.StatusReview {
		t.Fatalf("first = %+v", got.Chapters[0])
	}
	if got.Chapters[1].Content != "second content" || got.Chapters[1].Summary != "second summary" || got.Chapters[1].Status != project.StatusReview {
		t.Fatalf("second changed = %+v", got.Chapters[1])
	}
}

func TestReviseQuoteChangesOnlyMatchedParagraph(t *testing.T) {
	original := "first untouched\n\nsecond has target text\n\nthird untouched"
	progress := &project.Progress{Phase: "writing", Chapters: []project.Chapter{{Num: 1, Content: original, Status: project.StatusAccepted}}}
	session, _ := writingSession(t, progress)
	ai := &fakeAI{stream: []string{"second revised"}, completes: []string{"summary", `{"new_memories":[],"updates":[]}`}}
	if err := New(Dependencies{Session: session, AI: ai, Model: "model"}).ReviseSpecific(context.Background(), 1, "> target text\nmake it tense"); err != nil {
		t.Fatal(err)
	}
	if got := session.Snapshot().Project.Progress.Chapters[0].Content; got != "first untouched\n\nsecond revised\n\nthird untouched" {
		t.Fatalf("content = %q", got)
	}
	if len(ai.requests) == 0 || !strings.Contains(ai.requests[0].Messages[1].Content, "SegmentOriginal") && !strings.Contains(ai.requests[0].Messages[1].Content, "second has target text") {
		t.Fatalf("segment prompt = %+v", ai.requests[0])
	}
}

func TestReviseQuoteFallsBackToFullChapterWhenParagraphCountChanges(t *testing.T) {
	progress := &project.Progress{Phase: "writing", Chapters: []project.Chapter{{Num: 1, Content: "one target\n\ntwo", Status: project.StatusAccepted}}}
	session, _ := writingSession(t, progress)
	ai := &fakeAI{stream: []string{"unexpected\n\nextra", "full revised"}, completes: []string{"summary", `{"new_memories":[],"updates":[]}`}}
	if err := New(Dependencies{Session: session, AI: ai, Model: "model"}).ReviseSpecific(context.Background(), 1, "> target\nrevise"); err != nil {
		t.Fatal(err)
	}
	if got := session.Snapshot().Project.Progress.Chapters[0].Content; got != "full revised" {
		t.Fatalf("content = %q", got)
	}
	if len(ai.requests) < 2 {
		t.Fatalf("requests = %d, want segment then full revision", len(ai.requests))
	}
}

func TestStartReviseIsExclusiveAndCancellationLeavesChapterUntouched(t *testing.T) {
	progress := &project.Progress{Phase: "writing", Chapters: []project.Chapter{{Num: 1, Content: "original", Status: project.StatusReview}}}
	session, _ := writingSession(t, progress)
	started := make(chan struct{})
	ai := &fakeAI{streamErr: errors.New("blocked"), started: started}
	manager := runtime.NewTaskManager(nil)
	service := New(Dependencies{Session: session, Tasks: manager, AI: ai, Model: "model"})
	if err := service.StartRevise("improve"); err != nil {
		t.Fatal(err)
	}
	<-started
	if !errors.Is(service.StartRevise("again"), ErrTaskRunning) {
		t.Fatal("expected exclusive task error")
	}
	if !manager.Stop() {
		t.Fatal("expected active task")
	}
	deadline := time.Now().Add(time.Second)
	for manager.Running() && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if manager.Running() {
		t.Fatal("cancelled revision did not finish")
	}
	if got := session.Snapshot().Project.Progress.Chapters[0].Content; got != "original" {
		t.Fatalf("content after cancellation = %q", got)
	}
}

func TestPolishRequiresRulesAndPreservesCursor(t *testing.T) {
	progress := &project.Progress{Phase: "writing", CurrentChapterIndex: 1, Chapters: []project.Chapter{{Num: 1, Content: "old", Status: project.StatusAccepted}, {Num: 2, Content: "untouched", Status: project.StatusReview}}}
	session, _ := writingSession(t, progress)
	service := New(Dependencies{Session: session, AI: &fakeAI{}, Model: "model"})
	if !errors.Is(service.Polish(context.Background(), 0, ""), ErrPolishRulesRequired) {
		t.Fatal("expected missing polish rules")
	}
	ai := &fakeAI{stream: []string{"polished"}, completes: []string{`{"new_memories":[],"updates":[]}`}}
	if err := New(Dependencies{Session: session, AI: ai, Model: "model"}).Polish(context.Background(), 0, "remove cliches"); err != nil {
		t.Fatal(err)
	}
	got := session.Snapshot().Project.Progress
	if got.CurrentChapterIndex != 1 || got.Chapters[0].Content != "polished" || got.Chapters[0].Status != project.StatusReview || got.Chapters[1].Content != "untouched" {
		t.Fatalf("progress = %+v", got)
	}
}

package writing

import (
	"context"
	"errors"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"showmethestory/internal/app/runtime"
	"showmethestory/internal/domain/project"
	"showmethestory/internal/infra/fsstore"
	"showmethestory/internal/ports"
)

type fakeAI struct {
	mu        sync.Mutex
	stream    []string
	summary   string
	completes []string
	streamErr error
	started   chan struct{}
	requests  []ports.CompletionRequest
}

func (f *fakeAI) Complete(_ context.Context, request ports.CompletionRequest) (ports.CompletionResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.requests = append(f.requests, request)
	content := f.summary
	if len(f.completes) > 0 {
		content = f.completes[0]
		f.completes = f.completes[1:]
	}
	return ports.CompletionResult{Content: content}, nil
}
func (f *fakeAI) Stream(ctx context.Context, request ports.CompletionRequest, emit func(string)) (ports.CompletionResult, error) {
	f.mu.Lock()
	f.requests = append(f.requests, request)
	if f.started != nil {
		select {
		case <-f.started:
		default:
			close(f.started)
		}
	}
	if f.streamErr != nil {
		f.mu.Unlock()
		<-ctx.Done()
		return ports.CompletionResult{}, ctx.Err()
	}
	content := f.stream[0]
	if len(f.stream) > 1 {
		f.stream = f.stream[1:]
	}
	f.mu.Unlock()
	for _, part := range []string{"first ", "second"} {
		emit(part)
	}
	return ports.CompletionResult{Content: content}, nil
}
func (f *fakeAI) ListModels(context.Context) ([]ports.ModelInfo, error)   { return nil, nil }
func (f *fakeAI) ModelContextWindow(context.Context, string) (int, error) { return 0, nil }
func (f *fakeAI) IsFatalError(error) bool                                 { return false }

type events struct {
	mu     sync.Mutex
	values []event
}
type event struct {
	name string
	data any
}

func (e *events) Publish(name string, data any) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.values = append(e.values, event{name, data})
}
func (e *events) named(name string) int {
	e.mu.Lock()
	defer e.mu.Unlock()
	n := 0
	for _, v := range e.values {
		if v.name == name {
			n++
		}
	}
	return n
}
func (e *events) chunks() []map[string]any {
	e.mu.Lock()
	defer e.mu.Unlock()
	var out []map[string]any
	for _, v := range e.values {
		if v.name == "content_chunk" {
			out = append(out, v.data.(map[string]any))
		}
	}
	return out
}

func TestGenerateStreamsPersistsReviewAndMarkdown(t *testing.T) {
	session, store := writingSession(t, &project.Progress{Phase: "writing", Title: "Novel", CurrentChapterIndex: 1, Chapters: []project.Chapter{{Num: 1, Title: "One", Summary: "Prior", Content: "Prior ending"}, {Num: 2, Title: "Two", Outline: "A scene", Status: project.StatusPending}}})
	ai := &fakeAI{stream: []string{"generated prose"}, completes: []string{"new summary", `{"result":"PASS","issues":[]}`, `{"new_memories":[],"updates":[]}`}}
	events := &events{}
	service := New(Dependencies{Session: session, AI: ai, Events: events, Model: "model"})
	if err := service.Generate(context.Background()); err != nil {
		t.Fatal(err)
	}
	chapter := session.Snapshot().Project.Progress.Chapters[1]
	if chapter.Status != project.StatusReview || chapter.Content != "generated prose" || chapter.Summary != "new summary" {
		t.Fatalf("chapter = %+v", chapter)
	}
	markdown, err := os.ReadFile(store.ChapterMarkdownPath(2))
	if err != nil {
		t.Fatal(err)
	}
	if got := string(markdown); !strings.Contains(got, "# 第 2 章: Two") || !strings.Contains(got, "**本章摘要**：new summary") || !strings.Contains(got, "generated prose") {
		t.Fatalf("markdown = %q", got)
	}
	if events.named("stream_start") != 1 || events.named("content_chunk") != 2 || events.named("progress_update") != 2 {
		t.Fatalf("events = %+v", events.values)
	}
	for _, chunk := range events.chunks() {
		if chunk["chapter_idx"] != 1 {
			t.Fatalf("chunk payload = %+v", chunk)
		}
	}
}

func TestGenerateRetriesLengthAndIncludesFeedback(t *testing.T) {
	long := strings.Repeat("x", 2101)
	session, _ := writingSession(t, &project.Progress{Phase: "writing", CurrentChapterIndex: 0, Chapters: []project.Chapter{{Num: 1, Outline: "Scene", Status: project.StatusPending}}})
	ai := &fakeAI{stream: []string{long, "right-sized"}, completes: []string{"summary", `{"result":"PASS","issues":[]}`, `{"new_memories":[],"updates":[]}`}}
	service := New(Dependencies{Session: session, AI: ai, Model: "model"})
	if err := service.Generate(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(ai.requests) != 5 {
		t.Fatalf("calls = %d, want two streams plus consistency calls", len(ai.requests))
	}
	if !strings.Contains(ai.requests[1].Messages[1].Content, "重要：上一稿") {
		t.Fatalf("retry prompt = %q", ai.requests[1].Messages[1].Content)
	}
	if got := session.Snapshot().Project.Progress.Chapters[0].Content; got != "right-sized" {
		t.Fatalf("content = %q", got)
	}
}

func TestGenerateUpdatesForeshadowsAndMemoryAfterFactCheckPasses(t *testing.T) {
	progress := &project.Progress{Phase: "writing", Chapters: []project.Chapter{{Num: 1, Outline: "Scene"}}, Foreshadows: []project.Foreshadow{{ID: 7, Name: "Clue", Status: project.ForeshadowPlanted}}}
	session, _ := writingSession(t, progress)
	ai := &fakeAI{stream: []string{"prose"}, completes: []string{"summary", `{"result":"PASS","issues":[]}`, `{"updates":[{"id":7,"status":"progressing","event":"hint"}]}`, `{"new_memories":[{"content":"a lasting fact","category":"event","position":1}],"updates":[]}`}}
	if err := New(Dependencies{Session: session, AI: ai, Model: "model"}).Generate(context.Background()); err != nil {
		t.Fatal(err)
	}
	got := session.Snapshot().Project.Progress
	if got.Foreshadows[0].Status != project.ForeshadowProgressing || len(got.Foreshadows[0].Events) != 1 {
		t.Fatalf("foreshadow = %+v", got.Foreshadows[0])
	}
	if len(got.MemoryEntries) != 1 || got.MemoryEntries[0].Content != "a lasting fact" || got.MemoryMaxTokens == 0 {
		t.Fatalf("memory = %+v", got.MemoryEntries)
	}
}

func TestGeneratePersistsConflictAfterRepeatedFactCheckFailure(t *testing.T) {
	session, _ := writingSession(t, &project.Progress{Phase: "writing", Chapters: []project.Chapter{{Num: 1, Outline: "Scene"}}})
	ai := &fakeAI{stream: []string{"draft one", "draft two", "draft three", "draft four"}, completes: []string{
		"s1", `{"result":"FAIL","issues":["timeline"]}`,
		"s2", `{"result":"FAIL","issues":["timeline"]}`,
		"s3", `{"result":"FAIL","issues":["timeline"]}`,
		"s4", `{"result":"FAIL","issues":["timeline"]}`,
		`{"reconcilable":false,"summary":"outline clashes","root_cause":"outline_history"}`,
	}}
	err := New(Dependencies{Session: session, AI: ai, Model: "model"}).Generate(context.Background())
	var conflict *ConflictError
	if !errors.As(err, &conflict) {
		t.Fatalf("Generate() error = %v, want ConflictError", err)
	}
	got := session.Snapshot().Project.Progress
	if got.Chapters[0].Status != project.StatusWriting || got.PendingWritingConflict == nil || got.PendingWritingConflict.Summary != "outline clashes" {
		t.Fatalf("progress = %+v", got)
	}
}

func TestGenerateRejectsInvalidWritingTargetWithoutCallingAI(t *testing.T) {
	cases := []struct {
		name     string
		progress *project.Progress
		want     error
	}{
		{"phase", &project.Progress{Phase: "outline"}, ErrWritingPhase},
		{"cursor", &project.Progress{Phase: "writing", CurrentChapterIndex: 1, Chapters: []project.Chapter{{Num: 1}}}, ErrAllChaptersDone},
		{"accepted", &project.Progress{Phase: "writing", Chapters: []project.Chapter{{Num: 1, Status: project.StatusAccepted}}}, ErrAcceptedChapter},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			session, _ := writingSession(t, test.progress)
			ai := &fakeAI{stream: []string{"unused"}, summary: "unused"}
			err := New(Dependencies{Session: session, AI: ai, Model: "model"}).Generate(context.Background())
			if !errors.Is(err, test.want) {
				t.Fatalf("Generate() error = %v, want %v", err, test.want)
			}
			if len(ai.requests) != 0 {
				t.Fatalf("AI called for invalid target: %+v", ai.requests)
			}
		})
	}
}

func TestGenerateCancellationKeepsWritingCheckpoint(t *testing.T) {
	session, _ := writingSession(t, &project.Progress{Phase: "writing", CurrentChapterIndex: 0, Chapters: []project.Chapter{{Num: 1, Outline: "Scene", Status: project.StatusPending}}})
	started := make(chan struct{})
	ai := &fakeAI{streamErr: errors.New("blocked"), started: started}
	manager := runtime.NewTaskManager(nil)
	service := New(Dependencies{Session: session, Tasks: manager, AI: ai, Model: "model"})
	if err := service.StartGenerate(); err != nil {
		t.Fatal(err)
	}
	<-started
	if !manager.Stop() {
		t.Fatal("expected active task to stop")
	}
	deadline := time.Now().Add(time.Second)
	for manager.Running() && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if manager.Running() {
		t.Fatal("cancelled task did not complete")
	}
	if got := session.Snapshot().Project.Progress.Chapters[0].Status; got != project.StatusWriting {
		t.Fatalf("status = %q, want writing checkpoint", got)
	}
}

func TestStartGenerateAutoConfirmAcceptsAndContinues(t *testing.T) {
	progress := &project.Progress{Phase: "writing", CurrentChapterIndex: 0, Chapters: []project.Chapter{
		{Num: 1, Title: "One", Outline: "First scene", Status: project.StatusPending},
		{Num: 2, Title: "Two", Outline: "Second scene", Status: project.StatusPending},
	}}
	session, store := writingSession(t, progress)
	ai := &fakeAI{stream: []string{"first prose", "second prose"}, completes: []string{
		"first summary", `{"result":"PASS","issues":[]}`, `{"new_memories":[],"updates":[]}`,
		"second summary", `{"result":"PASS","issues":[]}`, `{"new_memories":[],"updates":[]}`,
	}}
	manager := runtime.NewTaskManager(nil)
	service := New(Dependencies{Session: session, Tasks: manager, AI: ai, Model: "model"})
	if err := service.StartGenerateAutoConfirm(func() bool { return true }); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(time.Second)
	for manager.Running() && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if manager.Running() {
		t.Fatal("auto-confirm generation did not complete")
	}
	got := session.Snapshot().Project.Progress
	if got.CurrentChapterIndex != 2 || got.Chapters[0].Status != project.StatusAccepted || got.Chapters[1].Status != project.StatusAccepted {
		t.Fatalf("progress = %+v", got)
	}
	if got.Chapters[1].Content != "second prose" || got.Chapters[1].Summary != "second summary" {
		t.Fatalf("second chapter = %+v", got.Chapters[1])
	}
	if _, err := os.Stat(store.ChapterMarkdownPath(2)); err != nil {
		t.Fatalf("second chapter markdown was not exported: %v", err)
	}
}

func TestSmoothTransitionsRewritesAcceptedPair(t *testing.T) {
	progress := &project.Progress{Phase: "writing", CurrentChapterIndex: 2, Chapters: []project.Chapter{
		{Num: 1, Title: "One", Content: "previous ending", Status: project.StatusAccepted},
		{Num: 2, Title: "Two", Outline: "Continue", Content: "old opening", Status: project.StatusAccepted},
	}}
	session, store := writingSession(t, progress)
	ai := &fakeAI{completes: []string{"revised opening"}}
	if err := New(Dependencies{Session: session, AI: ai, Model: "model"}).SmoothTransitions(context.Background()); err != nil {
		t.Fatal(err)
	}
	chapter := session.Snapshot().Project.Progress.Chapters[1]
	if chapter.Status != project.StatusAccepted || chapter.Content != "revised opening" {
		t.Fatalf("chapter = %+v", chapter)
	}
	markdown, err := os.ReadFile(store.ChapterMarkdownPath(2))
	if err != nil || !strings.Contains(string(markdown), "revised opening") {
		t.Fatalf("markdown = %q, err = %v", markdown, err)
	}
}

func TestSmoothTransitionsPreservesParagraphLocks(t *testing.T) {
	progress := &project.Progress{Phase: "writing", CurrentChapterIndex: 2, Chapters: []project.Chapter{
		{Num: 1, Title: "One", Content: "previous ending", Status: project.StatusAccepted},
		{Num: 2, Title: "Two", Outline: "Continue", Content: "locked opening\n\nunlocked continuation", ParagraphLocks: []int{1}, Status: project.StatusAccepted},
	}}
	session, store := writingSession(t, progress)
	ai := &fakeAI{completes: []string{"revised opening\n\nunlocked continuation"}}
	eventLog := &events{}
	if err := New(Dependencies{Session: session, AI: ai, Events: eventLog, Model: "model"}).SmoothTransitions(context.Background()); err != nil {
		t.Fatal(err)
	}
	chapter := session.Snapshot().Project.Progress.Chapters[1]
	if chapter.Status != project.StatusAccepted || chapter.Content != "locked opening\n\nunlocked continuation" || len(chapter.ParagraphLocks) != 1 || chapter.ParagraphLocks[0] != 1 {
		t.Fatalf("chapter = %+v", chapter)
	}
	if eventLog.named("progress_update") != 1 {
		t.Fatalf("progress events = %+v", eventLog.values)
	}
	markdown, err := os.ReadFile(store.ChapterMarkdownPath(2))
	if err != nil || !strings.Contains(string(markdown), "locked opening") {
		t.Fatalf("markdown = %q, err = %v", markdown, err)
	}
}

func TestStartGenerateIsExclusive(t *testing.T) {
	session, _ := writingSession(t, &project.Progress{Phase: "writing", CurrentChapterIndex: 0, Chapters: []project.Chapter{{Num: 1, Outline: "Scene"}}})
	started := make(chan struct{})
	ai := &fakeAI{streamErr: errors.New("blocked"), started: started}
	manager := runtime.NewTaskManager(nil)
	service := New(Dependencies{Session: session, Tasks: manager, AI: ai, Model: "model"})
	if err := service.StartGenerate(); err != nil {
		t.Fatal(err)
	}
	<-started
	if !errors.Is(service.StartGenerate(), ErrTaskRunning) {
		t.Fatal("expected exclusive task error")
	}
	manager.Stop()
	deadline := time.Now().Add(time.Second)
	for manager.Running() && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
}

func TestGenerateInjectsWritingRulesOnlyIntoChapterPrompt(t *testing.T) {
	session, _ := writingSession(t, &project.Progress{Phase: "writing", Chapters: []project.Chapter{{Num: 1, Outline: "Scene"}}})
	ai := &fakeAI{stream: []string{"prose"}, completes: []string{"summary", `{"result":"PASS","issues":[]}`, `{"new_memories":[],"updates":[]}`}}
	if err := New(Dependencies{Session: session, AI: ai, Model: "model", WritingRules: func() string { return "WRITING-SKILL-RULE" }}).Generate(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(ai.requests) == 0 || !strings.Contains(ai.requests[0].Messages[1].Content, "WRITING-SKILL-RULE") {
		t.Fatalf("chapter prompt did not contain writing rules: %#v", ai.requests)
	}
	for _, request := range ai.requests[1:] {
		for _, message := range request.Messages {
			if strings.Contains(message.Content, "WRITING-SKILL-RULE") {
				t.Fatalf("writing rules leaked into a non-chapter prompt: %#v", request)
			}
		}
	}
}

func writingSession(t *testing.T, progress *project.Progress) (*runtime.ProjectSession, *fsstore.Store) {
	t.Helper()
	store, err := fsstore.New(t.TempDir(), "novel")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(store.ProjectDir(), 0755); err != nil {
		t.Fatal(err)
	}
	config := `{"language":"zh","story":{"chapter_count":2,"target_words_per_chapter":1000}}`
	if err := os.WriteFile(store.ConfigPath(), []byte(config), 0644); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveProgress(context.Background(), progress); err != nil {
		t.Fatal(err)
	}
	session := &runtime.ProjectSession{}
	if err := session.Select(context.Background(), store); err != nil {
		t.Fatal(err)
	}
	return session, store
}

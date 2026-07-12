package outline

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
	mu       sync.Mutex
	results  []ports.CompletionResult
	err      error
	requests []ports.CompletionRequest
}

func (f *fakeAI) Complete(_ context.Context, request ports.CompletionRequest) (ports.CompletionResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.requests = append(f.requests, request)
	if f.err != nil {
		return ports.CompletionResult{}, f.err
	}
	result := f.results[0]
	if len(f.results) > 1 {
		f.results = f.results[1:]
	}
	return result, nil
}
func (f *fakeAI) Stream(context.Context, ports.CompletionRequest, func(string)) (ports.CompletionResult, error) {
	return ports.CompletionResult{}, errors.New("not implemented")
}
func (f *fakeAI) ListModels(context.Context) ([]ports.ModelInfo, error)   { return nil, nil }
func (f *fakeAI) ModelContextWindow(context.Context, string) (int, error) { return 0, nil }
func (f *fakeAI) IsFatalError(error) bool                                 { return false }

type eventRecorder struct {
	mu     sync.Mutex
	events []string
}

func (r *eventRecorder) Publish(name string, _ any) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, name)
}
func (r *eventRecorder) has(name string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, event := range r.events {
		if event == name {
			return true
		}
	}
	return false
}

func TestGeneratePersistsOutlineSnapshotAndLockedChapters(t *testing.T) {
	session := testSession(t, &project.Progress{Chapters: []project.Chapter{{Num: 9, Title: "Locked", Outline: "keep", OutlineLocked: true, Status: project.StatusPending}}})
	ai := &fakeAI{results: []ports.CompletionResult{{Content: validOutlineJSON("New title", 1, 2)}}}
	events := &eventRecorder{}
	service := New(Dependencies{Session: session, AI: ai, Events: events, Model: "test-model"})
	if err := service.Generate(context.Background()); err != nil {
		t.Fatal(err)
	}

	progress := session.Snapshot().Project.Progress
	if progress.Title != "New title" || progress.StoryConfigSnapshot == nil || progress.StoryConfigSnapshot.ChapterCount != 2 {
		t.Fatalf("metadata was not persisted: %+v", progress)
	}
	if len(progress.Chapters) != 3 || progress.Chapters[0].Num != 1 || progress.Chapters[1].Num != 2 || progress.Chapters[2].Title != "Locked" {
		t.Fatalf("chapters = %+v", progress.Chapters)
	}
	if progress.Chapters[0].Status != project.StatusPending || !events.has("progress_update") {
		t.Fatalf("generated chapter or event missing: %+v", progress.Chapters[0])
	}
	if len(ai.requests) != 1 || ai.requests[0].Model != "test-model" || len(ai.requests[0].Messages) != 2 {
		t.Fatalf("AI request = %+v", ai.requests)
	}
}

func TestGenerateRetriesShortOutlinesAndUsesRetryFeedback(t *testing.T) {
	session := testSession(t, nil)
	ai := &fakeAI{results: []ports.CompletionResult{{Content: `{"title":"Short","chapters":[{"num":1,"title":"One","outline":"short"}]}`}, {Content: validOutlineJSON("Full", 1)}}}
	service := New(Dependencies{Session: session, AI: ai, Model: "test-model"})
	if err := service.Generate(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(ai.requests) != 2 {
		t.Fatalf("AI calls = %d, want 2", len(ai.requests))
	}
	if !strings.Contains(ai.requests[1].Messages[1].Content, "不足") {
		t.Fatalf("retry prompt lacks short-outline feedback: %q", ai.requests[1].Messages[1].Content)
	}
}

func TestGenerateRejectsAcceptedChaptersWithoutCallingAI(t *testing.T) {
	session := testSession(t, &project.Progress{Chapters: []project.Chapter{{Num: 1, Status: project.StatusAccepted}}})
	ai := &fakeAI{results: []ports.CompletionResult{{Content: validOutlineJSON("unused", 1)}}}
	service := New(Dependencies{Session: session, AI: ai, Model: "test-model"})
	if !errors.Is(service.Generate(context.Background()), ErrAcceptedChapters) {
		t.Fatal("expected accepted-chapter protection")
	}
	if len(ai.requests) != 0 {
		t.Fatalf("AI called despite accepted chapter: %+v", ai.requests)
	}
}

func TestRevisePreservesAcceptedAndExplicitlyLockedChapters(t *testing.T) {
	accepted := project.Chapter{Num: 1, Title: "Accepted", Outline: "accepted outline", Status: project.StatusAccepted}
	locked := project.Chapter{Num: 2, Title: "Locked", Outline: "locked outline", OutlineLocked: true, Status: project.StatusPending}
	pending := project.Chapter{Num: 3, Title: "Before", Outline: "before", Status: project.StatusPending}
	session := testSession(t, &project.Progress{Phase: "outline", Chapters: []project.Chapter{accepted, locked, pending}})
	response := `{"title":"Revised","core_prompt":"new prompt","story_synopsis":"new synopsis","chapters":[` +
		`{"num":1,"title":"Changed accepted","outline":"` + strings.Repeat("情节", 50) + `"},` +
		`{"num":2,"title":"Changed locked","outline":"` + strings.Repeat("情节", 50) + `"},` +
		`{"num":3,"title":"After","outline":"` + strings.Repeat("情节", 50) + `"}]}`
	events := &eventRecorder{}
	service := New(Dependencies{Session: session, AI: &fakeAI{results: []ports.CompletionResult{{Content: response}}}, Events: events, Model: "test-model"})
	if err := service.Revise(context.Background(), "change chapter three"); err != nil {
		t.Fatal(err)
	}
	chapters := session.Snapshot().Project.Progress.Chapters
	if chapters[0].Title != accepted.Title || chapters[0].Outline != accepted.Outline || chapters[0].Status != accepted.Status || chapters[1].Title != locked.Title || chapters[1].Outline != locked.Outline || !chapters[1].OutlineLocked {
		t.Fatalf("locked chapters changed: %+v", chapters)
	}
	if chapters[2].Title != "After" || !events.has("progress_update") {
		t.Fatalf("unlocked chapter was not revised: %+v", chapters[2])
	}
}

func TestConfirmRequiresChaptersAndMovesToWritingWithoutChangingCursor(t *testing.T) {
	empty := New(Dependencies{Session: testSession(t, &project.Progress{Phase: "outline"})})
	if !errors.Is(empty.Confirm(context.Background()), ErrNoChapters) {
		t.Fatal("empty outline was confirmed")
	}
	session := testSession(t, &project.Progress{Phase: "outline", CurrentChapterIndex: 1, Chapters: []project.Chapter{{Num: 1, Status: project.StatusPending}}})
	events := &eventRecorder{}
	service := New(Dependencies{Session: session, Events: events})
	if err := service.Confirm(context.Background()); err != nil {
		t.Fatal(err)
	}
	progress := session.Snapshot().Project.Progress
	if progress.Phase != "writing" || progress.CurrentChapterIndex != 1 || !events.has("progress_update") {
		t.Fatalf("confirmation state = %+v", progress)
	}
}

func TestContinueAppendsPendingChaptersAndRejectsInvalidNumbers(t *testing.T) {
	existing := project.Chapter{Num: 1, Title: "Accepted", Outline: "keep", Content: "prose", Status: project.StatusAccepted}
	session := testSession(t, &project.Progress{Phase: "writing", Chapters: []project.Chapter{existing}})
	ai := &fakeAI{results: []ports.CompletionResult{{Content: `{"chapters":[{"num":2,"title":"Next","outline":"` + strings.Repeat("情节", 50) + `"},{"num":3,"title":"Later","outline":"` + strings.Repeat("情节", 50) + `"}]}`}}}
	events := &eventRecorder{}
	service := New(Dependencies{Session: session, AI: ai, Events: events, Model: "test-model"})
	if err := service.Continue(context.Background(), 2); err != nil {
		t.Fatal(err)
	}
	chapters := session.Snapshot().Project.Progress.Chapters
	if len(chapters) != 3 || chapters[0].Title != existing.Title || chapters[0].Outline != existing.Outline || chapters[0].Status != existing.Status || chapters[1].Status != project.StatusPending || chapters[2].Num != 3 || !events.has("progress_update") {
		t.Fatalf("continuation did not append pending chapters: %+v", chapters)
	}

	invalid := New(Dependencies{Session: testSession(t, &project.Progress{Chapters: []project.Chapter{existing}}), AI: &fakeAI{results: []ports.CompletionResult{{Content: `{"chapters":[{"num":1,"title":"Overwrite","outline":"` + strings.Repeat("情节", 50) + `"}]}`}}}, Model: "test-model"})
	if !errors.Is(invalid.Continue(context.Background(), 1), ErrInvalidContinuation) {
		t.Fatal("continuation accepted an existing chapter number")
	}
}

func TestStartGeneratePublishesTaskLifecycleAndProgress(t *testing.T) {
	session := testSession(t, nil)
	events := &eventRecorder{}
	manager := runtime.NewTaskManager(events)
	service := New(Dependencies{Session: session, Tasks: manager, AI: &fakeAI{results: []ports.CompletionResult{{Content: validOutlineJSON("Async", 1)}}}, Events: events, Model: "test-model"})
	if err := service.StartGenerate(); err != nil {
		t.Fatal(err)
	}
	if err := service.StartGenerate(); !errors.Is(err, ErrTaskRunning) {
		t.Fatalf("second start = %v, want task-running error", err)
	}
	deadline := time.Now().Add(time.Second)
	for manager.Running() && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if manager.Running() {
		t.Fatal("task did not finish")
	}
	if !events.has("task_start") || !events.has("task_end") || !events.has("progress_update") {
		t.Fatalf("events = %+v", events.events)
	}
}

func testSession(t *testing.T, progress *project.Progress) *runtime.ProjectSession {
	t.Helper()
	store, err := fsstore.New(t.TempDir(), "novel")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(store.ProjectDir(), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(store.ConfigPath(), []byte(`{"language":"zh","story":{"chapter_count":2,"target_words_per_chapter":1000}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if progress != nil {
		if err := store.SaveProgress(context.Background(), progress); err != nil {
			t.Fatal(err)
		}
	}
	session := &runtime.ProjectSession{}
	if err := session.Select(context.Background(), store); err != nil {
		t.Fatal(err)
	}
	return session
}

func validOutlineJSON(title string, numbers ...int) string {
	chapters := make([]string, 0, len(numbers))
	for _, number := range numbers {
		chapters = append(chapters, `{"num":`+stringNumber(number)+`,"title":"Chapter","outline":"`+strings.Repeat("情节", 50)+`"}`)
	}
	return `{"title":"` + title + `","core_prompt":"Prompt","story_synopsis":"Synopsis","chapters":[` + strings.Join(chapters, ",") + `]}`
}
func stringNumber(value int) string { return string(rune('0' + value)) }

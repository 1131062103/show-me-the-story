package foreshadow

import (
	"context"
	"sync"
	"testing"

	"showmethestory/internal/app/runtime"
	"showmethestory/internal/domain/project"
	"showmethestory/internal/infra/fsstore"
	"showmethestory/internal/ports"
)

type fakeAI struct {
	responses []string
	requests  []ports.CompletionRequest
}

func (f *fakeAI) Complete(_ context.Context, request ports.CompletionRequest) (ports.CompletionResult, error) {
	f.requests = append(f.requests, request)
	value := f.responses[0]
	f.responses = f.responses[1:]
	return ports.CompletionResult{Content: value}, nil
}
func (*fakeAI) Stream(context.Context, ports.CompletionRequest, func(string)) (ports.CompletionResult, error) {
	return ports.CompletionResult{}, nil
}
func (*fakeAI) ListModels(context.Context) ([]ports.ModelInfo, error)   { return nil, nil }
func (*fakeAI) ModelContextWindow(context.Context, string) (int, error) { return 0, nil }
func (*fakeAI) IsFatalError(error) bool                                 { return false }

type eventSink struct {
	mu    sync.Mutex
	names []string
}

func (e *eventSink) Publish(name string, _ any) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.names = append(e.names, name)
}

func selected(t *testing.T, progress *project.Progress) *runtime.ProjectSession {
	t.Helper()
	store, err := fsstore.New(t.TempDir(), "story")
	if err != nil {
		t.Fatal(err)
	}
	config := project.DefaultConfig()
	if err := store.SaveProject(context.Background(), &project.Project{Name: "story", Config: config, Settings: &project.ProjectSettings{}, PostProcess: project.DefaultPostProcessState(), Progress: progress}); err != nil {
		t.Fatal(err)
	}
	session := &runtime.ProjectSession{}
	if err := session.Select(context.Background(), store); err != nil {
		t.Fatal(err)
	}
	return session
}

func TestSuggestUsesConfiguredPromptAndPublishesSuggestions(t *testing.T) {
	progress := &project.Progress{Title: "Book", CorePrompt: "Core", Chapters: []project.Chapter{{Num: 1, Title: "Start", Outline: "A clue"}}}
	ai, events := &fakeAI{responses: []string{`{"foreshadows":[{"name":"Key","description":"Secret","plant_chapter":1,"target_chapter":2}]}`}}, &eventSink{}
	service := New(Dependencies{Session: selected(t, progress), AI: ai, Events: events, Model: "test"})
	if err := service.Suggest(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(ai.requests) != 1 || ai.requests[0].Messages[1].Content == "" {
		t.Fatalf("AI requests = %+v", ai.requests)
	}
	if len(events.names) != 1 || events.names[0] != "foreshadow_suggestions" {
		t.Fatalf("events = %v", events.names)
	}
}

func TestCheckOutlinePersistsReportAndPublishesConflict(t *testing.T) {
	progress := &project.Progress{Title: "Book", Chapters: []project.Chapter{{Num: 1, Title: "Start", Outline: "A clue", Status: project.StatusAccepted, Summary: "The clue appears"}}, Foreshadows: []project.Foreshadow{{ID: 1, Name: "Key", Description: "Secret", PlantChapter: 1, TargetChapter: 2, Status: project.ForeshadowPlanted}}}
	ai, events := &fakeAI{responses: []string{`{"has_conflicts":true,"conflicts":[{"foreshadow_id":1,"conflict_type":"missing_payoff"}],"summary":"No payoff"}`}}, &eventSink{}
	session := selected(t, progress)
	service := New(Dependencies{Session: session, AI: ai, Events: events, Model: "test"})
	if err := service.CheckOutline(context.Background()); err != nil {
		t.Fatal(err)
	}
	report := session.Snapshot().Project.Progress.LastForeshadowOutlineReport
	if report == nil || !report.HasConflicts || report.Summary != "No payoff" {
		t.Fatalf("report = %+v", report)
	}
	if len(events.names) != 2 || events.names[0] != "foreshadow_outline_conflicts" || events.names[1] != "progress_update" {
		t.Fatalf("events = %v", events.names)
	}
}

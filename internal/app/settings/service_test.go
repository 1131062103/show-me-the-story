package settings

import (
	"context"
	"testing"

	"showmethestory/internal/app/runtime"
	"showmethestory/internal/domain/project"
	"showmethestory/internal/infra/fsstore"
	"showmethestory/internal/ports"
)

type fakeAI struct{ responses []string }

func (f *fakeAI) Complete(_ context.Context, _ ports.CompletionRequest) (ports.CompletionResult, error) {
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

func selectedSession(t *testing.T, progress *project.Progress) (*runtime.ProjectSession, *fsstore.Store) {
	t.Helper()
	store, err := fsstore.New(t.TempDir(), "story")
	if err != nil {
		t.Fatal(err)
	}
	config := project.DefaultConfig()
	if err := store.SaveProject(context.Background(), &project.Project{Name: "story", Config: config, Progress: progress, Settings: &project.ProjectSettings{}, PostProcess: project.DefaultPostProcessState()}); err != nil {
		t.Fatal(err)
	}
	session := &runtime.ProjectSession{}
	if err := session.Select(context.Background(), store); err != nil {
		t.Fatal(err)
	}
	return session, store
}

func TestReconcilePreservesAcceptedAndLockedOutlinesAndProposesChanges(t *testing.T) {
	progress := &project.Progress{Chapters: []project.Chapter{
		{Num: 1, Title: "Accepted", Outline: "unchanged accepted", Status: project.StatusAccepted, Summary: "existing history"},
		{Num: 2, Title: "Locked", Outline: "unchanged locked", Status: project.StatusPending, OutlineLocked: true},
		{Num: 3, Title: "Pending", Outline: "old pending", Status: project.StatusPending},
	}}
	session, store := selectedSession(t, progress)
	ai := &fakeAI{responses: []string{
		`{"writing_style":"new style","explanation":"better continuity"}`,
		`{"chapters":[{"num":1,"title":"bad","outline":"bad"},{"num":2,"title":"bad","outline":"bad"},{"num":3,"title":"Revised","outline":"new pending"}]}`,
	}}
	service := New(Dependencies{Session: session, AI: ai, Model: "test"})
	next := project.StoryConfig{Title: "New title", Type: "fantasy", WritingStyle: "old style", WritingPOV: "first", StorySynopsis: "new synopsis"}
	if err := service.Reconcile(context.Background(), next); err != nil {
		t.Fatal(err)
	}

	value := session.Snapshot().Project
	if value.Config.Story != next {
		t.Fatalf("story = %+v, want %+v", value.Config.Story, next)
	}
	if value.Progress.Chapters[0].Outline != "unchanged accepted" || value.Progress.Chapters[1].Outline != "unchanged locked" {
		t.Fatalf("locked chapters changed: %+v", value.Progress.Chapters[:2])
	}
	if got := value.Progress.Chapters[2]; got.Title != "Revised" || got.Outline != "new pending" {
		t.Fatalf("pending chapter = %+v", got)
	}
	pending, err := store.LoadPendingConfigChanges(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(pending.Changes) != 1 || pending.Changes[0].Field != "writing_style" || pending.Changes[0].Proposed != "new style" {
		t.Fatalf("pending = %+v", pending)
	}
}

func TestReconcileKeepsSettingsWhenPendingOutlineRevisionFails(t *testing.T) {
	progress := &project.Progress{Chapters: []project.Chapter{{Num: 1, Title: "Pending", Outline: "old", Status: project.StatusPending}}}
	session, _ := selectedSession(t, progress)
	ai := &fakeAI{responses: []string{`{"explanation":"ok"}`, `not json`}}
	service := New(Dependencies{Session: session, AI: ai, Model: "test"})
	next := project.StoryConfig{Title: "Updated"}
	if err := service.Reconcile(context.Background(), next); err != nil {
		t.Fatal(err)
	}
	if got := session.Snapshot().Project.Config.Story.Title; got != "Updated" {
		t.Fatalf("title = %q", got)
	}
	if got := session.Snapshot().Project.Progress.Chapters[0].Outline; got != "old" {
		t.Fatalf("outline = %q", got)
	}
}

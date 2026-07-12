package continuation

import (
	"context"
	"testing"

	"showmethestory/internal/app/runtime"
	"showmethestory/internal/domain/project"
	"showmethestory/internal/infra/fsstore"
	"showmethestory/internal/ports"
)

type fakeAI struct{ response string }

func (f fakeAI) Complete(context.Context, ports.CompletionRequest) (ports.CompletionResult, error) {
	return ports.CompletionResult{Content: f.response}, nil
}
func (f fakeAI) Stream(context.Context, ports.CompletionRequest, func(string)) (ports.CompletionResult, error) {
	return ports.CompletionResult{}, nil
}
func (f fakeAI) ListModels(context.Context) ([]ports.ModelInfo, error)   { return nil, nil }
func (f fakeAI) ModelContextWindow(context.Context, string) (int, error) { return 0, nil }
func (f fakeAI) IsFatalError(error) bool                                 { return false }

func TestAnalyzeAndConfirmImportsAcceptedChapters(t *testing.T) {
	store, err := fsstore.NewAtProjectDir(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	value := &project.Project{Name: "story", Config: project.DefaultConfig(), Settings: &project.ProjectSettings{}, PostProcess: project.DefaultPostProcessState(), Progress: &project.Progress{Phase: "outline"}}
	if err := store.SaveProject(context.Background(), value); err != nil {
		t.Fatal(err)
	}
	session := &runtime.ProjectSession{}
	if err := session.Select(context.Background(), store); err != nil {
		t.Fatal(err)
	}
	service := New(Dependencies{Session: session, AI: fakeAI{response: `{"title":"Imported","story_type":"mystery","core_prompt":"retain facts","story_synopsis":"A case","writing_style":"spare","writing_pov":"first","chapters":[{"num":9,"title":"Start","outline":"A clue","summary":"A clue appears"}]}`}, Model: "test"})
	if err := service.Analyze(context.Background(), "Chapter 1\n\nProse"); err != nil {
		t.Fatal(err)
	}
	analysis := Analysis{Title: "Imported", StoryType: "mystery", CorePrompt: "retain facts", StorySynopsis: "A case", WritingStyle: "spare", WritingPOV: "first", Chapters: []Chapter{{Num: 9, Title: "Edited", Outline: "A clue", Summary: "A clue appears"}}}
	if err := service.Confirm(context.Background(), analysis); err != nil {
		t.Fatal(err)
	}
	result := session.Snapshot().Project
	if result.Progress.Phase != "outline" || result.Progress.CurrentChapterIndex != 1 || len(result.Progress.Chapters) != 1 || result.Progress.Chapters[0].Num != 1 || result.Progress.Chapters[0].Status != project.StatusAccepted {
		t.Fatalf("imported progress = %+v", result.Progress)
	}
	if result.Config.Story.Title != "Imported" || result.Progress.Chapters[0].Content != "Chapter 1\n\nProse" {
		t.Fatalf("imported project = %+v", result)
	}
}

func TestConfirmRequiresAnalysisAndOutlinePhase(t *testing.T) {
	service := New(Dependencies{})
	analysis := Analysis{Chapters: []Chapter{{Title: "One"}}}
	if err := service.Confirm(context.Background(), analysis); err != ErrNoAnalysis {
		t.Fatalf("Confirm without analysis = %v", err)
	}
}

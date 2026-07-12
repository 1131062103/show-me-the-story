package postprocess

import (
	"context"
	"os"
	"strings"
	"sync"
	"testing"

	"showmethestory/internal/app/runtime"
	"showmethestory/internal/app/writing"
	"showmethestory/internal/domain/project"
	"showmethestory/internal/infra/fsstore"
	"showmethestory/internal/ports"
)

type fakeAI struct {
	mu       sync.Mutex
	contents []string
	stream   []string
	requests []ports.CompletionRequest
}

func (f *fakeAI) Complete(_ context.Context, request ports.CompletionRequest) (ports.CompletionResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.requests = append(f.requests, request)
	if len(f.contents) == 0 {
		return ports.CompletionResult{}, nil
	}
	content := f.contents[0]
	f.contents = f.contents[1:]
	return ports.CompletionResult{Content: content}, nil
}
func (f *fakeAI) Stream(_ context.Context, request ports.CompletionRequest, emit func(string)) (ports.CompletionResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.requests = append(f.requests, request)
	if len(f.stream) == 0 {
		return ports.CompletionResult{}, nil
	}
	content := f.stream[0]
	f.stream = f.stream[1:]
	emit(content)
	return ports.CompletionResult{Content: content}, nil
}
func (f *fakeAI) ListModels(context.Context) ([]ports.ModelInfo, error)   { return nil, nil }
func (f *fakeAI) ModelContextWindow(context.Context, string) (int, error) { return 0, nil }
func (f *fakeAI) IsFatalError(error) bool                                 { return false }

type events struct {
	mu     sync.Mutex
	values []string
}

func (e *events) Publish(name string, _ any) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.values = append(e.values, name)
}
func (e *events) count(name string) int {
	e.mu.Lock()
	defer e.mu.Unlock()
	total := 0
	for _, value := range e.values {
		if value == name {
			total++
		}
	}
	return total
}

func TestAnalyzeRejectsIncompleteBookBeforeCallingAI(t *testing.T) {
	session := postprocessSession(t, []project.Chapter{{Num: 1, Content: "draft", Status: project.StatusReview}})
	ai := &fakeAI{}
	err := New(Dependencies{Session: session, AI: ai, Model: "model"}).Analyze(context.Background())
	if err != ErrBookIncomplete {
		t.Fatalf("Analyze() error = %v, want %v", err, ErrBookIncomplete)
	}
	if len(ai.requests) != 0 {
		t.Fatalf("AI calls = %d, want 0", len(ai.requests))
	}
}

func TestAnalyzeUsesSummaryModeAndPersistsReportsRoadmap(t *testing.T) {
	content := strings.Repeat("正文", 30)
	session := postprocessSession(t, []project.Chapter{{Num: 1, Title: "开端", Content: content, Summary: "主角离开故乡", Status: project.StatusAccepted}})
	ai := &fakeAI{contents: []string{
		"诊断报告", "核查报告",
		"```json\n{\"items\":[{\"chapter_num\":1,\"type\":\"logic\",\"priority\":\"P0\",\"feedback\":\"以最小改动修复时间线矛盾。\"}]}\n```",
	}}
	eventLog := &events{}
	service := New(Dependencies{Session: session, AI: ai, Events: eventLog, Model: "model", ContextBudgetTokens: 10})
	if err := service.Analyze(context.Background()); err != nil {
		t.Fatal(err)
	}

	state := session.Snapshot().Project.PostProcess
	if state.BundleMode != "summary_only" || state.DiagnosisReport != "诊断报告" || state.ConsistencyReport != "核查报告" {
		t.Fatalf("state = %#v", state)
	}
	if len(state.Roadmap) != 1 || state.Roadmap[0].Status != RoadmapStatusPending || !state.Roadmap[0].Selected {
		t.Fatalf("roadmap = %#v", state.Roadmap)
	}
	if state.DiagnosedAt == "" || state.ConsistencyAt == "" || state.RoadmapAt == "" {
		t.Fatalf("timestamps were not checkpointed: %#v", state)
	}
	if len(ai.requests) != 3 {
		t.Fatalf("AI calls = %d, want 3", len(ai.requests))
	}
	if strings.Contains(ai.requests[0].Messages[1].Content, content) {
		t.Fatalf("summary-only prompt leaked full prose: %q", ai.requests[0].Messages[1].Content)
	}
	if eventLog.count("postprocess_report") != 2 || eventLog.count("postprocess_roadmap") != 1 || eventLog.count("postprocess_update") < 3 {
		t.Fatalf("events = %#v", eventLog.values)
	}
}

func TestConsistencySplitsLongBooksAndChecksEveryVolume(t *testing.T) {
	long := strings.Repeat("x", volumeSplitRunes+10)
	session := postprocessSession(t, []project.Chapter{{Num: 1, Content: long, Status: project.StatusAccepted}})
	ai := &fakeAI{contents: []string{"first volume", "second volume"}}
	service := New(Dependencies{Session: session, AI: ai, Model: "model"})
	if err := service.Consistency(context.Background()); err != nil {
		t.Fatal(err)
	}
	state := session.Snapshot().Project.PostProcess
	if state.VolumeCount != 2 || !strings.Contains(state.ConsistencyReport, "Volume 1/2") || !strings.Contains(state.ConsistencyReport, "Volume 2/2") {
		t.Fatalf("consistency state = %#v", state)
	}
	if len(ai.requests) != 2 {
		t.Fatalf("AI calls = %d, want 2", len(ai.requests))
	}
}

func TestExecuteCheckpointsRoadmapAndRestoresAcceptedChapter(t *testing.T) {
	session := postprocessSession(t, []project.Chapter{{Num: 1, Title: "开端", Outline: "场景", Content: "原文", Summary: "旧摘要", Status: project.StatusAccepted}})
	if err := session.WithProject(context.Background(), func(value *project.Project) error {
		value.PostProcess.Roadmap = []project.RoadmapItem{{ID: "rm_1", ChapterNum: 1, Type: RoadmapTypeLogic, Priority: "P0", Feedback: "修复逻辑", Selected: true, Status: RoadmapStatusPending}}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	ai := &fakeAI{stream: []string{"修改后的正文"}, contents: []string{"新摘要", `{"new_memories":[],"updates":[]}`}}
	eventLog := &events{}
	writer := writing.New(writing.Dependencies{Session: session, AI: ai, Events: eventLog, Model: "model"})
	service := New(Dependencies{Session: session, AI: ai, Events: eventLog, Writing: writer, Model: "model"})
	if err := service.Execute(context.Background()); err != nil {
		t.Fatal(err)
	}
	state := session.Snapshot().Project
	item := state.PostProcess.Roadmap[0]
	if item.Status != RoadmapStatusDone || item.DiffOriginal != "原文" || item.DiffRevised != "修改后的正文" || state.Progress.Chapters[0].Status != project.StatusAccepted || state.PostProcess.LastExecuteAt == "" {
		t.Fatalf("execution result = item:%#v chapter:%#v", item, state.Progress.Chapters[0])
	}
	if eventLog.count("postprocess_item_done") != 1 || eventLog.count("postprocess_update") < 2 {
		t.Fatalf("events = %#v", eventLog.values)
	}
}

func TestParseRoadmapDefaultsAndOrdersItems(t *testing.T) {
	items, err := parseRoadmap(`[{"chapter_num":2,"feedback":"style"},{"chapter_num":1,"type":"transition","priority":"P0","feedback":"connect"}]`)
	if err != nil {
		t.Fatal(err)
	}
	sortRoadmap(items)
	if len(items) != 2 || items[0].ChapterNum != 1 || items[0].Status != RoadmapStatusPending || items[1].Type != RoadmapTypeStyle || items[1].Priority != "P1" {
		t.Fatalf("items = %#v", items)
	}
}

func postprocessSession(t *testing.T, chapters []project.Chapter) *runtime.ProjectSession {
	t.Helper()
	store, err := fsstore.New(t.TempDir(), "novel")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(store.ProjectDir(), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(store.ConfigPath(), []byte(`{"language":"zh","story":{"title":"故事","chapter_count":1,"target_words_per_chapter":1000}}`), 0644); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveProgress(context.Background(), &project.Progress{Phase: "writing", Title: "故事", Chapters: chapters}); err != nil {
		t.Fatal(err)
	}
	session := &runtime.ProjectSession{}
	if err := session.Select(context.Background(), store); err != nil {
		t.Fatal(err)
	}
	return session
}

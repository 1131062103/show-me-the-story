package runtime

import (
	"context"
	"os"
	"testing"

	"showmethestory/internal/domain/project"
	"showmethestory/internal/infra/fsstore"
)

func TestProjectSessionSelectClearAndSnapshot(t *testing.T) {
	store, err := fsstore.New(t.TempDir(), "novel")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(store.ProjectDir(), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(store.ConfigPath(), []byte(`{"language":"zh"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	var session ProjectSession
	if err := session.Select(context.Background(), store); err != nil {
		t.Fatal(err)
	}
	if !session.HasProject() || session.ProjectName() != "novel" {
		t.Fatal("selected project missing")
	}
	if snapshot := session.Snapshot(); snapshot == nil || snapshot.Store != store || snapshot.Project.Config.Language != "zh" {
		t.Fatal("invalid snapshot")
	}
	if err := session.WithProgress(context.Background(), func(progress *project.Progress) error {
		progress.Title = "Updated"
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if got := session.Snapshot().Project.Progress.Title; got != "Updated" {
		t.Fatalf("progress title = %q, want Updated", got)
	}
	if err := session.WithProject(context.Background(), func(value *project.Project) error {
		value.Settings.Characters = append(value.Settings.Characters, project.Character{Name: "Character"})
		value.PostProcess.DiagnosisReport = "diagnosed"
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if snapshot := session.Snapshot(); snapshot.Project.Settings.Characters[0].Name != "Character" || snapshot.Project.PostProcess.DiagnosisReport != "diagnosed" {
		t.Fatal("aggregate update was not retained")
	}
	session.Clear()
	if session.HasProject() || session.ProjectName() != "" || session.Snapshot() != nil {
		t.Fatal("clear did not reset selection")
	}
}

func TestTaskManagerSingleActiveTask(t *testing.T) {
	var tasks TaskManager
	task, ok := tasks.Start()
	if !ok || !tasks.Running() {
		t.Fatal("task did not start")
	}
	if _, ok := tasks.Start(); ok {
		t.Fatal("second task started")
	}
	if !tasks.Stop() || !tasks.Running() {
		t.Fatal("stopped task was released before completion")
	}
	select {
	case <-task.Context().Done():
	default:
		t.Fatal("task context was not canceled")
	}
	if tasks.Stop() {
		t.Fatal("canceled task twice")
	}
	task.Complete(false)
	if tasks.Running() {
		t.Fatal("completed task remains active")
	}
	if tasks.Stop() {
		t.Fatal("stopped inactive task")
	}
}

type capturedEvent struct {
	name string
	data any
}

type eventRecorder struct {
	events []capturedEvent
}

func (r *eventRecorder) Publish(name string, data any) {
	r.events = append(r.events, capturedEvent{name: name, data: data})
}

func TestTaskManagerPublishesLifecycleEvents(t *testing.T) {
	recorder := &eventRecorder{}
	tasks := NewTaskManager(recorder)
	task, ok := tasks.Start("outline")
	if !ok {
		t.Fatal("task did not start")
	}
	task.Complete(true)

	if len(recorder.events) != 2 {
		t.Fatalf("event count = %d, want 2", len(recorder.events))
	}
	if got := recorder.events[0]; got.name != "task_start" {
		t.Fatalf("start event = %#v", got)
	} else if data, ok := got.data.(map[string]string); !ok || data["task"] != "outline" {
		t.Fatalf("start data = %#v", got.data)
	}
	if got := recorder.events[1]; got.name != "task_end" {
		t.Fatalf("end event = %#v", got)
	} else if data, ok := got.data.(map[string]any); !ok || data["task"] != "outline" || data["success"] != true {
		t.Fatalf("end data = %#v", got.data)
	}
}

func TestTaskManagerAssignsDistinctTaskIDs(t *testing.T) {
	var tasks TaskManager
	first, ok := tasks.Start("outline")
	if !ok || first.ID() == "" || first.Name() != "outline" {
		t.Fatalf("first task = %#v, started = %v", first, ok)
	}
	first.Done(true)
	second, ok := tasks.Start("chapter")
	if !ok || second.ID() == first.ID() || second.Name() != "chapter" {
		t.Fatalf("second task = %#v, started = %v", second, ok)
	}
	second.Done(true)
}

func TestTaskManagerWaitsForChildWorkBeforeFinishing(t *testing.T) {
	var tasks TaskManager
	task, ok := tasks.Start("chapter")
	if !ok {
		t.Fatal("task did not start")
	}
	releaseChild := task.AddChild()
	task.Complete(true)
	if !tasks.Running() {
		t.Fatal("task finished while child work remains")
	}
	if _, ok := tasks.Start("replacement"); ok {
		t.Fatal("replacement started before child work ended")
	}

	releaseChild()
	if tasks.Running() {
		t.Fatal("task remains active after child work ended")
	}
	if _, ok := tasks.Start("replacement"); !ok {
		t.Fatal("replacement did not start after child work ended")
	}
}

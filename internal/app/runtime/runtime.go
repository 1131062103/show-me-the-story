// Package runtime owns selected-project and task lifecycle state.
package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"sync"

	"showmethestory/internal/domain/project"
	"showmethestory/internal/ports"
)

var ErrNoProject = errors.New("no project selected")

// SelectedProject is an immutable snapshot of the selected project's complete
// compatible aggregate. Store is path-bound and immutable.
type SelectedProject struct {
	Name    string
	Store   ports.ProjectStore
	Project *project.Project

	// Progress remains available for callers migrating from the progress-only
	// session snapshot. It always refers to Project.Progress in this snapshot.
	Progress *project.Progress
}

type ProjectSession struct {
	mu       sync.RWMutex
	selected *SelectedProject
}

// Select loads the complete aggregate before publishing it, so readers never
// observe a partially selected project.
func (s *ProjectSession) Select(ctx context.Context, store ports.ProjectStore) error {
	if store == nil {
		return errors.New("project store is required")
	}
	loaded, err := store.LoadProject(ctx)
	if err != nil {
		return err
	}
	snapshot := cloneProject(loaded)
	s.mu.Lock()
	s.selected = &SelectedProject{Name: loaded.Name, Store: store, Project: snapshot, Progress: snapshot.Progress}
	s.mu.Unlock()
	return nil
}

func (s *ProjectSession) Clear() { s.mu.Lock(); s.selected = nil; s.mu.Unlock() }
func (s *ProjectSession) HasProject() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.selected != nil
}
func (s *ProjectSession) ProjectName() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.selected == nil {
		return ""
	}
	return s.selected.Name
}
func (s *ProjectSession) Snapshot() *SelectedProject {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.selected == nil {
		return nil
	}
	copy := cloneProject(s.selected.Project)
	return &SelectedProject{Name: s.selected.Name, Store: s.selected.Store, Project: copy, Progress: copy.Progress}
}

// WithProject provides the only mutable aggregate access. It persists the
// candidate aggregate before publishing it to the session snapshot.
func (s *ProjectSession) WithProject(ctx context.Context, update func(*project.Project) error) error {
	if update == nil {
		return errors.New("project update is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.selected == nil {
		return ErrNoProject
	}
	candidate := cloneProject(s.selected.Project)
	if err := update(candidate); err != nil {
		return err
	}
	if err := s.selected.Store.SaveProject(ctx, candidate); err != nil {
		return err
	}
	published := cloneProject(candidate)
	s.selected.Name, s.selected.Project, s.selected.Progress = published.Name, published, published.Progress
	return nil
}

func (s *ProjectSession) WithProgress(ctx context.Context, update func(*project.Progress) error) error {
	if update == nil {
		return errors.New("progress update is required")
	}
	return s.WithProject(ctx, func(value *project.Project) error {
		if value.Progress == nil {
			value.Progress = &project.Progress{Phase: "outline"}
		}
		return update(value.Progress)
	})
}

func cloneProject(value *project.Project) *project.Project {
	if value == nil {
		return nil
	}
	data, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	var clone project.Project
	if err := json.Unmarshal(data, &clone); err != nil {
		panic(err)
	}
	if value.Progress != nil && clone.Progress != nil {
		for index := range clone.Progress.Chapters {
			if index >= len(value.Progress.Chapters) {
				break
			}
			clone.Progress.Chapters[index].Content = value.Progress.Chapters[index].Content
			clone.Progress.Chapters[index].Summary = value.Progress.Chapters[index].Summary
		}
	}
	clone.Name = value.Name
	return &clone
}

// TaskManager serializes asynchronous application work. A stopped task remains
// active until it completes, which prevents a replacement task from starting
// while cancelled work is still unwinding.
type TaskManager struct {
	mu        sync.RWMutex
	active    *Task
	nextID    uint64
	publisher ports.EventPublisher
}

// NewTaskManager creates a task manager that publishes task lifecycle events.
// The publisher may be nil when an application has no event transport.
func NewTaskManager(publisher ports.EventPublisher) *TaskManager {
	return &TaskManager{publisher: publisher}
}

// Start starts one task. name is optional to retain a context-compatible entry
// point for callers that do not yet expose task lifecycle events.
func (m *TaskManager) Start(name ...string) (*Task, bool) {
	taskName := ""
	if len(name) > 0 {
		taskName = name[0]
	}

	m.mu.Lock()
	if m.active != nil {
		m.mu.Unlock()
		return nil, false
	}
	m.nextID++
	ctx, cancel := context.WithCancel(context.Background())
	task := &Task{manager: m, ctx: ctx, cancel: cancel, id: strconv.FormatUint(m.nextID, 10), name: taskName}
	m.active = task
	publisher := m.publisher
	m.mu.Unlock()

	if publisher != nil {
		publisher.Publish("task_start", map[string]string{"task": taskName, "task_id": task.id})
	}
	return task, true
}

// Stop requests cancellation for the active task. It does not mark the task as
// complete; callers must call Complete after the root work and every child have
// stopped using the task context.
func (m *TaskManager) Stop() bool {
	m.mu.RLock()
	task := m.active
	m.mu.RUnlock()
	if task == nil {
		return false
	}
	return task.cancelOnce()
}

// StartChild records child work for the current task. The returned release
// function must be called when the child exits. It returns false when no task
// is active or its root work has already completed.
func (m *TaskManager) StartChild() (func(), bool) {
	m.mu.RLock()
	task := m.active
	if task == nil {
		m.mu.RUnlock()
		return nil, false
	}
	release, ok := task.startChild()
	m.mu.RUnlock()
	return release, ok
}

// Done completes the root work of the current task. It is safe to call more
// than once; only the first call has an effect.
func (m *TaskManager) Done(success bool) bool {
	m.mu.RLock()
	task := m.active
	m.mu.RUnlock()
	if task == nil {
		return false
	}
	task.Done(success)
	return true
}

// Running reports whether active work, including cancelled work that is still
// unwinding, remains associated with the manager.
func (m *TaskManager) Running() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.active != nil
}

// Task is a named, cancellable unit of work.
type Task struct {
	manager *TaskManager
	ctx     context.Context
	cancel  context.CancelFunc
	id      string
	name    string

	mu        sync.Mutex
	cancelled bool
	completed bool
	children  int
	success   bool
	finished  bool
}

// ID returns the identifier unique within this TaskManager's lifetime.
func (t *Task) ID() string { return t.id }

// Name returns the name supplied to Start.
func (t *Task) Name() string { return t.name }

// Context returns the context shared by the root task and all its children.
func (t *Task) Context() context.Context { return t.ctx }

// AddChild records work launched by the root task. The returned function must
// be called exactly once when that work exits. Calling it more than once is
// safe. Child work cannot be added after Done has been called.
func (t *Task) AddChild() func() {
	release, _ := t.startChild()
	return release
}

func (t *Task) startChild() (func(), bool) {
	t.mu.Lock()
	if t.completed || t.finished {
		t.mu.Unlock()
		return func() {}, false
	}
	t.children++
	t.mu.Unlock()

	var once sync.Once
	return func() {
		once.Do(func() {
			t.mu.Lock()
			t.children--
			finish := t.readyToFinishLocked()
			success := t.success
			t.mu.Unlock()
			if finish {
				t.manager.finish(t, success)
			}
		})
	}, true
}

// StartChild registers child work for this task. The returned release function
// is safe to defer and is idempotent.
func (t *Task) StartChild() func() { return t.AddChild() }

// Done marks root work as complete. The task remains active until each
// registered child calls its release function.
func (t *Task) Done(success bool) {
	t.mu.Lock()
	if t.completed {
		t.mu.Unlock()
		return
	}
	t.completed, t.success = true, success
	finish := t.readyToFinishLocked()
	t.mu.Unlock()
	if finish {
		t.manager.finish(t, success)
	}
}

// Complete is retained as a compatibility alias for Done.
func (t *Task) Complete(success bool) { t.Done(success) }

func (t *Task) cancelOnce() bool {
	t.mu.Lock()
	if t.cancelled || t.finished {
		t.mu.Unlock()
		return false
	}
	t.cancelled = true
	t.mu.Unlock()
	t.cancel()
	return true
}

func (t *Task) readyToFinishLocked() bool {
	if !t.completed || t.children != 0 || t.finished {
		return false
	}
	t.finished = true
	return true
}

func (m *TaskManager) finish(task *Task, success bool) {
	m.mu.Lock()
	if m.active != task {
		m.mu.Unlock()
		return
	}
	m.active = nil
	publisher := m.publisher
	m.mu.Unlock()
	if publisher != nil {
		publisher.Publish("task_end", map[string]any{"task": task.name, "task_id": task.id, "success": success})
	}
}

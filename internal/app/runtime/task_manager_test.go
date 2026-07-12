package runtime

import (
	"sync"
	"testing"
)

func TestTaskManagerStartChildAndDone(t *testing.T) {
	var tasks TaskManager
	task, ok := tasks.Start("agent")
	if !ok {
		t.Fatal("task did not start")
	}
	release, ok := tasks.StartChild()
	if !ok {
		t.Fatal("child did not start")
	}
	if !tasks.Done(true) || !tasks.Running() {
		t.Fatal("root completed before child work")
	}
	release()
	if tasks.Running() {
		t.Fatal("task remains active after root and child work completed")
	}
	if _, ok := tasks.StartChild(); ok {
		t.Fatal("child started without an active task")
	}
	if tasks.Done(true) {
		t.Fatal("completed inactive task")
	}
	task.Done(false) // A stale worker must not affect a later task.
}

func TestTaskManagerConcurrentStopDoneAndChildRelease(t *testing.T) {
	var tasks TaskManager
	task, ok := tasks.Start("concurrent")
	if !ok {
		t.Fatal("task did not start")
	}
	release := task.StartChild()

	var workers sync.WaitGroup
	for range 32 {
		workers.Add(1)
		go func() {
			defer workers.Done()
			tasks.Stop()
			tasks.Done(false)
			release()
		}()
	}
	workers.Wait()

	if tasks.Running() {
		t.Fatal("concurrently completed task remains active")
	}
	select {
	case <-task.Context().Done():
	default:
		t.Fatal("concurrent Stop did not cancel task context")
	}
}

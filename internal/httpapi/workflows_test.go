package httpapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"showmethestory/internal/app/agent"
	"showmethestory/internal/app/continuation"
	"showmethestory/internal/app/outline"
	"showmethestory/internal/app/runtime"
)

type fakeOutlineWorkflow struct {
	generated, revised, continued int
	confirmation                  error
}

func (f *fakeOutlineWorkflow) StartGenerate() error          { f.generated++; return nil }
func (f *fakeOutlineWorkflow) StartRevise(string) error      { f.revised++; return nil }
func (f *fakeOutlineWorkflow) StartContinue(int) error       { f.continued++; return nil }
func (f *fakeOutlineWorkflow) Confirm(context.Context) error { return f.confirmation }

type fakeContinuationWorkflow struct{ analyzed, confirmed int }

func (f *fakeContinuationWorkflow) StartAnalyze(string) error { f.analyzed++; return nil }
func (f *fakeContinuationWorkflow) Confirm(context.Context, continuation.Analysis) error {
	f.confirmed++
	return nil
}

type fakeWritingWorkflow struct{ generated, revised, specific, polished int }

func (f *fakeWritingWorkflow) StartGenerate() error                  { f.generated++; return nil }
func (f *fakeWritingWorkflow) StartRevise(string) error              { f.revised++; return nil }
func (f *fakeWritingWorkflow) StartReviseSpecific(int, string) error { f.specific++; return nil }
func (f *fakeWritingWorkflow) StartPolish(int, string) error         { f.polished++; return nil }

type richWritingWorkflow struct {
	fakeWritingWorkflow
	autoConfirm func() bool
	smoothed    int
}

func (f *richWritingWorkflow) StartGenerateAutoConfirm(enabled func() bool) error {
	f.generated++
	f.autoConfirm = enabled
	return nil
}
func (f *richWritingWorkflow) StartSmoothTransitions() error { f.smoothed++; return nil }

func TestRetiredSettingsRoutesPreserveLegacyGoneContracts(t *testing.T) {
	server := New(&runtime.ProjectSession{})
	for _, check := range []struct {
		path, code string
	}{
		{"/api/settings/ai-generate", "settings_ai_generate_moved"},
		{"/api/settings/polish", "settings_polish_moved"},
	} {
		recorder := httptest.NewRecorder()
		server.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, check.path, nil))
		if recorder.Code != http.StatusGone || !strings.Contains(recorder.Body.String(), check.code) {
			t.Errorf("POST %s = (%d, %s)", check.path, recorder.Code, recorder.Body.String())
		}
	}
}

type fakeForeshadowWorkflow struct{ suggested, checked int }

func (f *fakeForeshadowWorkflow) StartSuggest() error      { f.suggested++; return nil }
func (f *fakeForeshadowWorkflow) StartOutlineCheck() error { f.checked++; return nil }

type fakePostprocessWorkflow struct{ analyzed, consistent, roadmap, executed int }

func (f *fakePostprocessWorkflow) StartAnalyze() error     { f.analyzed++; return nil }
func (f *fakePostprocessWorkflow) StartConsistency() error { f.consistent++; return nil }
func (f *fakePostprocessWorkflow) StartRoadmap() error     { f.roadmap++; return nil }
func (f *fakePostprocessWorkflow) StartExecute() error     { f.executed++; return nil }

type fakeAgentWorkflow struct {
	started int
	err     error
}

func (f *fakeAgentWorkflow) Start(_ string, _ []agent.Step, _ int, done func(string, []agent.Step, error)) error {
	f.started++
	return f.err
}

func TestWorkflowRoutesUseInjectedServicesAndAsyncContract(t *testing.T) {
	session := selectedSession(t)
	outlineFake, writingFake, postFake := &fakeOutlineWorkflow{}, &fakeWritingWorkflow{}, &fakePostprocessWorkflow{}
	server := New(session, WithOutlineWorkflow(outlineFake), WithWritingWorkflow(writingFake), WithPostProcessWorkflow(postFake))
	checks := []struct {
		path, body string
		called     *int
	}{
		{"/api/outline/generate", "", &outlineFake.generated},
		{"/api/outline/revise", `{"feedback":"more tension"}`, &outlineFake.revised},
		{"/api/outline/generate-continuation", `{"chapter_count":2}`, &outlineFake.continued},
		{"/api/chapter/generate", "", &writingFake.generated},
		{"/api/chapter/revise", `{"feedback":"less exposition"}`, &writingFake.revised},
		{"/api/chapter/revise/1", `{"feedback":"tighten"}`, &writingFake.specific},
		{"/api/postprocess/diagnose", "", &postFake.analyzed},
		{"/api/postprocess/consistency", "", &postFake.consistent},
		{"/api/postprocess/roadmap", "", &postFake.roadmap},
		{"/api/postprocess/execute", "", &postFake.executed},
	}
	for _, check := range checks {
		recorder := httptest.NewRecorder()
		server.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, check.path, strings.NewReader(check.body)))
		if recorder.Code != http.StatusAccepted || recorder.Body.String() != "{\"status\":\"started\"}\n" || *check.called != 1 {
			t.Errorf("POST %s = (%d, %q), calls=%d", check.path, recorder.Code, recorder.Body.String(), *check.called)
		}
	}
}

func TestContinuationRoutesUseAnalysisThenConfirmation(t *testing.T) {
	workflow := &fakeContinuationWorkflow{}
	server := New(selectedSession(t), WithContinuationWorkflow(workflow))
	importResult := httptest.NewRecorder()
	server.ServeHTTP(importResult, httptest.NewRequest(http.MethodPost, "/api/continue/import", strings.NewReader(`{"content":"Chapter 1"}`)))
	if importResult.Code != http.StatusAccepted || workflow.analyzed != 1 {
		t.Fatalf("import = (%d, %s), analyzed=%d", importResult.Code, importResult.Body.String(), workflow.analyzed)
	}
	confirmResult := httptest.NewRecorder()
	server.ServeHTTP(confirmResult, httptest.NewRequest(http.MethodPost, "/api/continue/confirm", strings.NewReader(`{"chapters":[{"title":"One"}]}`)))
	if confirmResult.Code != http.StatusOK || workflow.confirmed != 1 {
		t.Fatalf("confirm = (%d, %s), confirmed=%d", confirmResult.Code, confirmResult.Body.String(), workflow.confirmed)
	}
}

func TestWritingWorkflowRoutesUseAutoConfirmAndTransitionService(t *testing.T) {
	workflow := &richWritingWorkflow{}
	server := New(selectedSession(t), WithWritingWorkflow(workflow))

	enable := httptest.NewRecorder()
	server.ServeHTTP(enable, httptest.NewRequest(http.MethodPut, "/api/autoconfirm", strings.NewReader(`{"enabled":true}`)))
	if enable.Code != http.StatusOK {
		t.Fatalf("enable auto-confirm = (%d, %s)", enable.Code, enable.Body.String())
	}
	generate := httptest.NewRecorder()
	server.ServeHTTP(generate, httptest.NewRequest(http.MethodPost, "/api/chapter/generate", nil))
	if generate.Code != http.StatusAccepted || workflow.autoConfirm == nil || !workflow.autoConfirm() {
		t.Fatalf("generate = (%d, %s), callback enabled = %t", generate.Code, generate.Body.String(), workflow.autoConfirm != nil && workflow.autoConfirm())
	}
	transitions := httptest.NewRecorder()
	server.ServeHTTP(transitions, httptest.NewRequest(http.MethodPost, "/api/chapters/smooth-transitions", nil))
	if transitions.Code != http.StatusAccepted || workflow.smoothed != 1 {
		t.Fatalf("smooth transitions = (%d, %s), calls = %d", transitions.Code, transitions.Body.String(), workflow.smoothed)
	}
}

func TestWorkflowRoutesValidateAndAdvertiseUnportedRoutes(t *testing.T) {
	server := New(selectedSession(t), WithOutlineWorkflow(&fakeOutlineWorkflow{}), WithWritingWorkflow(&fakeWritingWorkflow{}))
	for _, check := range []struct{ path, body, want string }{
		{"/api/outline/revise", `{}`, "missing_feedback"},
		{"/api/chapter/revise/zero", `{"feedback":"x"}`, "invalid_chapter_num"},
		{"/api/chapter/revise", `{}`, "missing_feedback"},
		{"/api/chapter/conflict-resolve", `{}`, "missing_action"},
		{"/api/chapters/smooth-transitions", `{}`, "not_implemented"},
	} {
		recorder := httptest.NewRecorder()
		server.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, check.path, strings.NewReader(check.body)))
		if recorder.Code != http.StatusBadRequest && recorder.Code != http.StatusNotImplemented {
			t.Errorf("POST %s status = %d: %s", check.path, recorder.Code, recorder.Body.String())
		}
		if !strings.Contains(recorder.Body.String(), check.want) {
			t.Errorf("POST %s = %s, want %s", check.path, recorder.Body.String(), check.want)
		}
	}
}

func TestTaskStopAndStatusUseTaskManager(t *testing.T) {
	manager := runtime.NewTaskManager(nil)
	server := New(selectedSession(t), WithTaskManager(manager))
	noTask := httptest.NewRecorder()
	server.ServeHTTP(noTask, httptest.NewRequest(http.MethodPost, "/api/task/stop", nil))
	if noTask.Code != http.StatusBadRequest || !strings.Contains(noTask.Body.String(), "no_task_running") {
		t.Fatalf("stop without task = (%d, %s)", noTask.Code, noTask.Body.String())
	}
	task, ok := manager.Start("test")
	if !ok {
		t.Fatal("start task")
	}
	stop := httptest.NewRecorder()
	server.ServeHTTP(stop, httptest.NewRequest(http.MethodPost, "/api/task/stop", nil))
	if stop.Code != http.StatusOK || !strings.Contains(stop.Body.String(), "stopping") {
		t.Fatalf("stop = (%d, %s)", stop.Code, stop.Body.String())
	}
	status := httptest.NewRecorder()
	server.ServeHTTP(status, httptest.NewRequest(http.MethodGet, "/api/status", nil))
	if !strings.Contains(status.Body.String(), `"is_task_running":true`) {
		t.Fatalf("status = %s", status.Body.String())
	}
	task.Done(false)
}

func TestWorkflowStartErrorUsesExistingConflictPayload(t *testing.T) {
	server := New(selectedSession(t), WithOutlineWorkflow(&failingOutline{}))
	recorder := httptest.NewRecorder()
	server.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/api/outline/generate", nil))
	if recorder.Code != http.StatusConflict || !strings.Contains(recorder.Body.String(), "task_running_wait") {
		t.Fatalf("start conflict = (%d, %s)", recorder.Code, recorder.Body.String())
	}
}

type failingOutline struct{ fakeOutlineWorkflow }

func (*failingOutline) StartGenerate() error { return outline.ErrTaskRunning }

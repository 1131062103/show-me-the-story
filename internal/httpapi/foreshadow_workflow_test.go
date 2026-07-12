package httpapi

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"showmethestory/internal/domain/project"
)

func TestForeshadowWorkflowRoutesStartInjectedService(t *testing.T) {
	session := selectedSession(t)
	if err := session.WithProgress(t.Context(), func(progress *project.Progress) error {
		progress.Chapters = []project.Chapter{{Num: 1, Title: "Start", Outline: "Clue"}}
		progress.Foreshadows = []project.Foreshadow{{ID: 1, Name: "Key", Status: project.ForeshadowPlanted}}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	workflow := &fakeForeshadowWorkflow{}
	server := New(session, WithForeshadowWorkflow(workflow))
	for _, test := range []struct {
		path  string
		calls *int
	}{{"/api/foreshadows/suggest", &workflow.suggested}, {"/api/foreshadows/outline-check", &workflow.checked}} {
		recorder := httptest.NewRecorder()
		server.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, test.path, nil))
		if recorder.Code != http.StatusAccepted || *test.calls != 1 {
			t.Errorf("POST %s = (%d, %s), calls=%d", test.path, recorder.Code, recorder.Body.String(), *test.calls)
		}
	}
}

func TestForeshadowWorkflowRoutesValidatePrerequisites(t *testing.T) {
	workflow := &fakeForeshadowWorkflow{}
	server := New(selectedSession(t), WithForeshadowWorkflow(workflow))
	for _, test := range []struct{ path, want string }{{"/api/foreshadows/suggest", "need_generate_outline_first"}, {"/api/foreshadows/outline-check", "no_foreshadows_to_check"}} {
		recorder := httptest.NewRecorder()
		server.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, test.path, nil))
		if recorder.Code != http.StatusBadRequest || !strings.Contains(recorder.Body.String(), test.want) {
			t.Errorf("POST %s = (%d, %s)", test.path, recorder.Code, recorder.Body.String())
		}
	}
}

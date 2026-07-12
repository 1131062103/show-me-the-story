package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"showmethestory/internal/domain/project"
)

func TestForeshadowConfirmAssignsIDsAndStartsOutlineCheck(t *testing.T) {
	session := selectedSession(t)
	if err := session.WithProgress(t.Context(), func(progress *project.Progress) error {
		progress.Foreshadows = []project.Foreshadow{{ID: 4, Name: "Existing", Status: project.ForeshadowPlanted}}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	workflow := &fakeForeshadowWorkflow{}
	server := New(session, WithForeshadowWorkflow(workflow))
	recorder := httptest.NewRecorder()
	server.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/api/foreshadows/confirm", strings.NewReader(`{"foreshadows":[{"name":"New","description":"A clue","plant_chapter":1,"target_chapter":3}]}`)))
	if recorder.Code != http.StatusOK {
		t.Fatalf("confirm = (%d, %s)", recorder.Code, recorder.Body.String())
	}
	var got []project.Foreshadow
	if err := json.NewDecoder(recorder.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[1].ID != 5 || got[1].Status != project.ForeshadowPlanted || got[1].Events == nil {
		t.Fatalf("foreshadows = %+v", got)
	}
	if workflow.checked != 1 {
		t.Fatalf("outline checks = %d", workflow.checked)
	}
}

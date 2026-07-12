package httpapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"showmethestory/internal/domain/project"
)

func TestPendingConfigChangesCanBeReadAppliedAndDiscarded(t *testing.T) {
	session := selectedSession(t)
	store := session.Snapshot().Store
	pending := &project.PendingConfigChanges{Changes: []project.ConfigFieldChange{
		{Field: "writing_style", Current: "old", Proposed: "new", Source: "reconcile"},
		{Field: "writing_pov", Current: "first", Proposed: "third", Source: "reconcile"},
	}}
	if err := store.SavePendingConfigChanges(context.Background(), pending); err != nil {
		t.Fatal(err)
	}
	server := New(session)

	get := httptest.NewRecorder()
	server.ServeHTTP(get, httptest.NewRequest(http.MethodGet, "/api/config/pending-changes", nil))
	if get.Code != http.StatusOK || !strings.Contains(get.Body.String(), `"writing_style"`) {
		t.Fatalf("GET pending = (%d, %s)", get.Code, get.Body.String())
	}

	apply := httptest.NewRecorder()
	server.ServeHTTP(apply, httptest.NewRequest(http.MethodPost, "/api/config/apply-changes", strings.NewReader(`{"fields":["writing_style"]}`)))
	if apply.Code != http.StatusOK {
		t.Fatalf("POST apply = (%d, %s)", apply.Code, apply.Body.String())
	}
	if got := session.Snapshot().Project.Config.Story.WritingStyle; got != "new" {
		t.Fatalf("style = %q", got)
	}
	remaining, err := store.LoadPendingConfigChanges(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(remaining.Changes) != 1 || remaining.Changes[0].Field != "writing_pov" {
		t.Fatalf("remaining = %+v", remaining)
	}

	discard := httptest.NewRecorder()
	server.ServeHTTP(discard, httptest.NewRequest(http.MethodDelete, "/api/config/pending-changes", nil))
	if discard.Code != http.StatusOK {
		t.Fatalf("DELETE pending = (%d, %s)", discard.Code, discard.Body.String())
	}
	remaining, err = store.LoadPendingConfigChanges(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(remaining.Changes) != 0 {
		t.Fatalf("remaining after discard = %+v", remaining)
	}
}

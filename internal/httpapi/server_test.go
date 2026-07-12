package httpapi

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"showmethestory/internal/app/runtime"
	"showmethestory/internal/domain/project"
	"showmethestory/internal/infra/fsstore"
)

func TestReadEndpointsRequireSelectedProject(t *testing.T) {
	server := New(&runtime.ProjectSession{})
	for _, path := range []string{"/api/config", "/api/progress", "/api/settings", "/api/postprocess", "/api/status"} {
		recorder := httptest.NewRecorder()
		server.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, path, nil))
		if recorder.Code != http.StatusBadRequest || recorder.Body.String() != "{\"error\":\"select_project_first\"}\n" {
			t.Errorf("GET %s = (%d, %s)", path, recorder.Code, recorder.Body.String())
		}
	}
}

func TestPutConfigRequiresSelectedProjectAndValidJSON(t *testing.T) {
	server := New(&runtime.ProjectSession{})
	for _, body := range []string{`{`, `{"language":"en"}`} {
		recorder := httptest.NewRecorder()
		server.ServeHTTP(recorder, httptest.NewRequest(http.MethodPut, "/api/config", strings.NewReader(body)))
		if recorder.Code != http.StatusBadRequest || recorder.Body.String() != "{\"error\":\"select_project_first\"}\n" {
			t.Errorf("PUT /api/config without selection = (%d, %s)", recorder.Code, recorder.Body.String())
		}
	}

	server = New(selectedSession(t))
	recorder := httptest.NewRecorder()
	server.ServeHTTP(recorder, httptest.NewRequest(http.MethodPut, "/api/config", strings.NewReader(`{`)))
	if recorder.Code != http.StatusBadRequest || recorder.Body.String() != "{\"error\":\"invalid_json\"}\n" {
		t.Fatalf("PUT /api/config invalid JSON = (%d, %s)", recorder.Code, recorder.Body.String())
	}
}

func TestSkillsListAndTogglePersistInProjectConfig(t *testing.T) {
	session := selectedSession(t)
	skills := func(*project.Config, string) []project.Skill {
		return []project.Skill{{ID: "polish", Name: "Polish", Category: "polish", Content: "Rule"}}
	}
	server := New(session, WithSkills(skills))
	list := httptest.NewRecorder()
	server.ServeHTTP(list, httptest.NewRequest(http.MethodGet, "/api/skills", nil))
	if list.Code != http.StatusOK || !strings.Contains(list.Body.String(), `"enabled":false`) {
		t.Fatalf("list = (%d, %s)", list.Code, list.Body.String())
	}
	toggle := httptest.NewRecorder()
	server.ServeHTTP(toggle, httptest.NewRequest(http.MethodPut, "/api/skills/polish/toggle", strings.NewReader(`{"enabled":true}`)))
	if toggle.Code != http.StatusOK || !session.Snapshot().Project.Config.SkillConfig.EnabledSkills["polish"] {
		t.Fatalf("toggle = (%d, %s)", toggle.Code, toggle.Body.String())
	}
	missing := httptest.NewRecorder()
	server.ServeHTTP(missing, httptest.NewRequest(http.MethodPut, "/api/skills/missing/toggle", strings.NewReader(`{"enabled":true}`)))
	if missing.Code != http.StatusNotFound || !strings.Contains(missing.Body.String(), "skill_not_found") {
		t.Fatalf("missing = (%d, %s)", missing.Code, missing.Body.String())
	}
}

func TestPutConfigNormalizesAndPersistsSelectedAggregate(t *testing.T) {
	session := selectedSession(t)
	server := New(session)
	body := `{"language":"en-US","story":{"title":"New title","chapter_count":0,"target_words_per_chapter":-1},"skill_config":{"enabled_skills":{"polish":true}}}`
	recorder := httptest.NewRecorder()
	server.ServeHTTP(recorder, httptest.NewRequest(http.MethodPut, "/api/config", strings.NewReader(body)))
	if recorder.Code != http.StatusOK {
		t.Fatalf("PUT /api/config status = %d: %s", recorder.Code, recorder.Body.String())
	}
	for _, fragment := range []string{`"language":"en"`, `"title":"New title"`, `"chapter_count":30`, `"target_words_per_chapter":2500`, `"polish":true`} {
		if !strings.Contains(recorder.Body.String(), fragment) {
			t.Errorf("response missing %s: %s", fragment, recorder.Body.String())
		}
	}

	getRecorder := httptest.NewRecorder()
	server.ServeHTTP(getRecorder, httptest.NewRequest(http.MethodGet, "/api/config", nil))
	if getRecorder.Code != http.StatusOK || !strings.Contains(getRecorder.Body.String(), `"title":"New title"`) {
		t.Fatalf("GET /api/config after update = (%d, %s)", getRecorder.Code, getRecorder.Body.String())
	}

	snapshot := session.Snapshot()
	if snapshot.Project.Config.Language != "en" || snapshot.Project.Config.Story.ChapterCount != 30 || snapshot.Project.Config.Story.TargetWordsPerChapter != 2500 || !snapshot.Project.Config.SkillConfig.EnabledSkills["polish"] {
		t.Fatalf("session config was not normalized: %#v", snapshot.Project.Config)
	}
	persisted, err := snapshot.Store.LoadProject(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if persisted.Config.Language != "en" || persisted.Config.Story.Title != "New title" || persisted.Config.Story.ChapterCount != 30 || persisted.Config.Story.TargetWordsPerChapter != 2500 || !persisted.Config.SkillConfig.EnabledSkills["polish"] {
		t.Fatalf("config was not persisted: %#v", persisted.Config)
	}
	rawConfig, err := os.ReadFile(snapshot.Store.ConfigPath())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(rawConfig), `"title": "New title"`) {
		t.Fatalf("config.json was not written: %s", rawConfig)
	}
}

func TestSettingsCRUDRoutesUpdateSelectedAggregate(t *testing.T) {
	session := selectedSession(t)
	server := New(session)
	entries := []struct{ path, body, id string }{
		{"/api/characters", `{"name":"Ada","personality":"curious"}`, "c_1"},
		{"/api/worldview", `{"category":"place","name":"Library","description":"Old"}`, "w_1"},
		{"/api/organizations", `{"name":"Guild","type":"group"}`, "o_1"},
		{"/api/relations", `{"source_id":"c_1","source_type":"character","target_id":"o_1","target_type":"organization","label":"member"}`, "r_1"},
	}
	for _, entry := range entries {
		recorder := httptest.NewRecorder()
		server.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, entry.path, strings.NewReader(entry.body)))
		if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), `"id":"`+entry.id+`"`) {
			t.Fatalf("POST %s = (%d, %s)", entry.path, recorder.Code, recorder.Body.String())
		}
	}
	updates := []struct{ path, body, fragment string }{
		{"/api/characters/c_1", `{"name":"Ada Lovelace"}`, `"name":"Ada Lovelace"`},
		{"/api/worldview/w_1", `{"tags":"secret"}`, `"tags":"secret"`},
		{"/api/organizations/o_1", `{"members":["c_1"]}`, `"members":["c_1"]`},
		{"/api/relations/r_1", `{"label":"founder"}`, `"label":"founder"`},
	}
	for _, update := range updates {
		recorder := httptest.NewRecorder()
		server.ServeHTTP(recorder, httptest.NewRequest(http.MethodPut, update.path, strings.NewReader(update.body)))
		if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), update.fragment) {
			t.Fatalf("PUT %s = (%d, %s)", update.path, recorder.Code, recorder.Body.String())
		}
	}
	getRecorder := httptest.NewRecorder()
	server.ServeHTTP(getRecorder, httptest.NewRequest(http.MethodGet, "/api/settings", nil))
	for _, fragment := range []string{`Ada Lovelace`, `"tags":"secret"`, `"members":["c_1"]`, `"label":"founder"`} {
		if getRecorder.Code != http.StatusOK || !strings.Contains(getRecorder.Body.String(), fragment) {
			t.Fatalf("GET /api/settings = (%d, %s)", getRecorder.Code, getRecorder.Body.String())
		}
	}
	for _, path := range []string{"/api/characters/c_1", "/api/worldview/w_1", "/api/organizations/o_1", "/api/relations/r_1"} {
		recorder := httptest.NewRecorder()
		server.ServeHTTP(recorder, httptest.NewRequest(http.MethodDelete, path, nil))
		if recorder.Code != http.StatusOK || recorder.Body.String() != "{\"status\":\"deleted\"}\n" {
			t.Fatalf("DELETE %s = (%d, %s)", path, recorder.Code, recorder.Body.String())
		}
	}
	persisted, err := session.Snapshot().Store.LoadProject(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(persisted.Settings.Characters) != 0 || len(persisted.Settings.Worldview) != 0 || len(persisted.Settings.Organizations) != 0 || len(persisted.Settings.Relations) != 0 {
		t.Fatalf("settings deletes not persisted: %#v", persisted.Settings)
	}
}

func TestSettingsCRUDValidationAndNoProject(t *testing.T) {
	server := New(&runtime.ProjectSession{})
	recorder := httptest.NewRecorder()
	server.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/api/characters", strings.NewReader(`{"name":"Ada"}`)))
	if recorder.Code != http.StatusBadRequest || recorder.Body.String() != "{\"error\":\"select_project_first\"}\n" {
		t.Fatalf("POST character without project = (%d, %s)", recorder.Code, recorder.Body.String())
	}
	server = New(selectedSession(t))
	for _, check := range []struct{ path, body, want string }{
		{"/api/characters", `{`, "invalid_json"}, {"/api/characters", `{}`, "character_name_empty"}, {"/api/worldview", `{"name":"place"}`, "worldview_field_empty"}, {"/api/organizations", `{}`, "organization_name_empty"}, {"/api/relations", `{"source_id":"c_1"}`, "relation_endpoints_empty"},
	} {
		recorder := httptest.NewRecorder()
		server.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, check.path, strings.NewReader(check.body)))
		if recorder.Code != http.StatusBadRequest || !strings.Contains(recorder.Body.String(), check.want) {
			t.Errorf("POST %s = (%d, %s), want %s", check.path, recorder.Code, recorder.Body.String(), check.want)
		}
	}
}

func TestSettingsCRUDRejectsInvalidPathID(t *testing.T) {
	server := New(selectedSession(t))
	request := httptest.NewRequest(http.MethodPut, "/api/characters/ignored", strings.NewReader(`{"name":"Ada"}`))
	request.SetPathValue("id", "../config.json")
	recorder := httptest.NewRecorder()
	server.putCharacter(recorder, request)
	if recorder.Code != http.StatusNotFound || recorder.Body.String() != "{\"error\":\"character_not_found\"}\n" {
		t.Fatalf("PUT character with traversal ID = (%d, %s)", recorder.Code, recorder.Body.String())
	}
}

func TestOutlineAndLockRoutesUpdateSelectedProgress(t *testing.T) {
	session := selectedSession(t)
	if err := session.WithProgress(context.Background(), func(progress *project.Progress) error {
		progress.Chapters = []project.Chapter{{Num: 1, Title: "Old", Outline: "Old outline", Status: project.StatusPending}, {Num: 2, Content: "First paragraph.\n\nSecond paragraph.\n\nThird paragraph."}}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	server := New(session)
	for _, update := range []struct{ path, body, fragment string }{
		{"/api/outline/1", `{"title":"New","outline":"New outline"}`, `"title":"New"`},
		{"/api/outline/1/lock", `{"locked":true}`, `"outline_locked":true`},
		{"/api/chapter/2/paragraph-locks", `{"locks":[3,1,1,99,0]}`, `"paragraph_locks":[1,3]`},
	} {
		recorder := httptest.NewRecorder()
		server.ServeHTTP(recorder, httptest.NewRequest(http.MethodPut, update.path, strings.NewReader(update.body)))
		if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), update.fragment) {
			t.Fatalf("PUT %s = (%d, %s)", update.path, recorder.Code, recorder.Body.String())
		}
	}
	lockedEdit := httptest.NewRecorder()
	server.ServeHTTP(lockedEdit, httptest.NewRequest(http.MethodPut, "/api/outline/1", strings.NewReader(`{"title":"Blocked"}`)))
	if lockedEdit.Code != http.StatusBadRequest || !strings.Contains(lockedEdit.Body.String(), "invalid_json") {
		t.Fatalf("locked outline edit = (%d, %s)", lockedEdit.Code, lockedEdit.Body.String())
	}
	persisted, err := session.Snapshot().Store.LoadProject(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if persisted.Progress.Chapters[0].Title != "New" || !persisted.Progress.Chapters[0].OutlineLocked || strings.Join(intsToStrings(persisted.Progress.Chapters[1].ParagraphLocks), ",") != "1,3" {
		t.Fatalf("progress changes not persisted: %#v", persisted.Progress.Chapters)
	}
}

func TestOutlineAndLockRoutesValidateInput(t *testing.T) {
	server := New(selectedSession(t))
	for _, check := range []struct{ path, body, want string }{
		{"/api/outline/zero", `{}`, "invalid_chapter_num"}, {"/api/outline/1", `{`, "invalid_json"}, {"/api/outline/1/lock", `{`, "invalid_json"}, {"/api/chapter/1/paragraph-locks", `{`, "invalid_json"},
	} {
		recorder := httptest.NewRecorder()
		server.ServeHTTP(recorder, httptest.NewRequest(http.MethodPut, check.path, strings.NewReader(check.body)))
		if recorder.Code != http.StatusBadRequest || !strings.Contains(recorder.Body.String(), check.want) {
			t.Errorf("PUT %s = (%d, %s)", check.path, recorder.Code, recorder.Body.String())
		}
	}

	server = New(&runtime.ProjectSession{})
	for _, check := range []struct{ path, body string }{
		{"/api/outline/1", `{"title":"Chapter","outline":"Outline"}`}, {"/api/outline/1/lock", `{"locked":true}`}, {"/api/chapter/1/paragraph-locks", `{"locks":[1]}`},
	} {
		recorder := httptest.NewRecorder()
		server.ServeHTTP(recorder, httptest.NewRequest(http.MethodPut, check.path, strings.NewReader(check.body)))
		if recorder.Code != http.StatusBadRequest || recorder.Body.String() != "{\"error\":\"select_project_first\"}\n" {
			t.Errorf("PUT %s without selection = (%d, %s)", check.path, recorder.Code, recorder.Body.String())
		}
	}
}

func intsToStrings(values []int) []string {
	result := make([]string, len(values))
	for i, value := range values {
		result[i] = strconv.Itoa(value)
	}
	return result
}

func TestChapterEditConfirmAndRejectRoutes(t *testing.T) {
	session := selectedSession(t)
	if err := session.WithProgress(context.Background(), func(progress *project.Progress) error {
		progress.Phase, progress.CurrentChapterIndex = "writing", 0
		progress.Chapters = []project.Chapter{{Num: 1, Title: "Test", Summary: "Original summary", Content: "First line\nSecond line", Status: project.StatusReview}}
		progress.MemoryEntries = []project.MemoryEntry{{ID: 1, Chapter: 1, Content: "remove"}, {ID: 2, Chapter: 2, Content: "keep"}}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	server := New(session)
	edit := httptest.NewRecorder()
	server.ServeHTTP(edit, httptest.NewRequest(http.MethodPost, "/api/chapter/edit", strings.NewReader(`{"num":1,"operation":"replace_lines","start_line":2,"end_line":2,"new_text":"Revised line"}`)))
	if edit.Code != http.StatusOK || !strings.Contains(edit.Body.String(), "Revised line") {
		t.Fatalf("POST chapter edit = (%d, %s)", edit.Code, edit.Body.String())
	}
	markdown, err := session.Snapshot().Store.LoadChapterMarkdown(context.Background(), 1)
	if err != nil {
		t.Fatalf("load edited chapter markdown: %v", err)
	}
	if !strings.Contains(string(markdown), "# 第 1 章: Test") || !strings.Contains(string(markdown), "Revised line") {
		t.Fatalf("edited markdown does not preserve legacy format: %s", markdown)
	}
	confirm := httptest.NewRecorder()
	server.ServeHTTP(confirm, httptest.NewRequest(http.MethodPost, "/api/chapter/confirm", nil))
	if confirm.Code != http.StatusOK || !strings.Contains(confirm.Body.String(), `"status":"accepted"`) {
		t.Fatalf("POST chapter confirm = (%d, %s)", confirm.Code, confirm.Body.String())
	}
	if err := session.WithProgress(context.Background(), func(progress *project.Progress) error {
		progress.CurrentChapterIndex = 0
		progress.Chapters[0].Status = project.StatusReview
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	reject := httptest.NewRecorder()
	server.ServeHTTP(reject, httptest.NewRequest(http.MethodPost, "/api/chapter/reject", nil))
	if reject.Code != http.StatusOK {
		t.Fatalf("POST chapter reject = (%d, %s)", reject.Code, reject.Body.String())
	}
	persisted, err := session.Snapshot().Store.LoadProject(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if persisted.Progress.Chapters[0].Content != "" || persisted.Progress.Chapters[0].Summary != "" || persisted.Progress.Chapters[0].Status != project.StatusPending {
		t.Fatalf("rejected chapter was not persisted: %#v", persisted.Progress.Chapters[0])
	}
	if len(persisted.Progress.MemoryEntries) != 1 || persisted.Progress.MemoryEntries[0].Chapter != 2 {
		t.Fatalf("rejection did not purge same-chapter memories: %#v", persisted.Progress.MemoryEntries)
	}
	if _, err := session.Snapshot().Store.LoadChapterMarkdown(context.Background(), 1); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("rejected chapter markdown still exists or returned wrong error: %v", err)
	}
}

func TestChapterEditRoutesValidateInputAndSelection(t *testing.T) {
	server := New(&runtime.ProjectSession{})
	for _, request := range []struct{ path, body string }{{"/api/chapter/edit", `{"num":1,"operation":"append","new_text":"x"}`}, {"/api/chapter/confirm", ""}, {"/api/chapter/reject", ""}} {
		recorder := httptest.NewRecorder()
		server.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, request.path, strings.NewReader(request.body)))
		if recorder.Code != http.StatusBadRequest || recorder.Body.String() != "{\"error\":\"select_project_first\"}\n" {
			t.Errorf("POST %s without selection = (%d, %s)", request.path, recorder.Code, recorder.Body.String())
		}
	}
	server = New(selectedSession(t))
	for _, request := range []struct{ body, want string }{{`{`, "invalid_json"}, {`{"num":1}`, "chapter_edit_op_required"}, {`{"num":1,"operation":"append"}`, "chapter_edit_text_required"}} {
		recorder := httptest.NewRecorder()
		server.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/api/chapter/edit", strings.NewReader(request.body)))
		if recorder.Code != http.StatusBadRequest || !strings.Contains(recorder.Body.String(), request.want) {
			t.Errorf("invalid chapter edit = (%d, %s)", recorder.Code, recorder.Body.String())
		}
	}
}

func TestChapterEditHonorsParagraphLocksAndPreservesReplaceAllLocks(t *testing.T) {
	session := selectedSession(t)
	if err := session.WithProgress(context.Background(), func(progress *project.Progress) error {
		progress.Chapters = []project.Chapter{{
			Num:            1,
			Content:        "One.\n\nTwo.\n\nThree.",
			ParagraphLocks: []int{2},
			Status:         project.StatusReview,
		}}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	server := New(session)
	locked := httptest.NewRecorder()
	server.ServeHTTP(locked, httptest.NewRequest(http.MethodPost, "/api/chapter/edit", strings.NewReader(`{"num":1,"operation":"replace_paragraphs","start_paragraph":2,"end_paragraph":2,"new_text":"Changed."}`)))
	if locked.Code != http.StatusBadRequest || !strings.Contains(locked.Body.String(), "chapter_edit_failed") {
		t.Fatalf("locked paragraph edit = (%d, %s)", locked.Code, locked.Body.String())
	}

	replaceAll := httptest.NewRecorder()
	server.ServeHTTP(replaceAll, httptest.NewRequest(http.MethodPost, "/api/chapter/edit", strings.NewReader(`{"num":1,"operation":"replace_all","new_text":"First.\n\nSecond."}`)))
	if replaceAll.Code != http.StatusOK {
		t.Fatalf("replace all = (%d, %s)", replaceAll.Code, replaceAll.Body.String())
	}
	chapter := session.Snapshot().Project.Progress.Chapters[0]
	if chapter.Content != "First.\n\nSecond." || len(chapter.ParagraphLocks) != 1 || chapter.ParagraphLocks[0] != 2 {
		t.Fatalf("replace all did not preserve valid locks: %#v", chapter)
	}
}

func TestDeleteChapterUsesWritingFrontierAndPurgesMemory(t *testing.T) {
	session := selectedSession(t)
	if err := session.WithProgress(context.Background(), func(progress *project.Progress) error {
		progress.Phase, progress.CurrentChapterIndex = "writing", 1
		progress.Chapters = []project.Chapter{{Num: 1, Content: "Accepted prose", Summary: "summary", Status: project.StatusAccepted}, {Num: 2, Status: project.StatusPending}}
		progress.MemoryEntries = []project.MemoryEntry{{Chapter: 1, Content: "remove"}, {Chapter: 2, Content: "keep"}}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := session.Snapshot().Store.SaveChapterMarkdown(context.Background(), 1, []byte("chapter one")); err != nil {
		t.Fatal(err)
	}

	server := New(session)
	recorder := httptest.NewRecorder()
	server.ServeHTTP(recorder, httptest.NewRequest(http.MethodDelete, "/api/chapter", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("DELETE /api/chapter = (%d, %s)", recorder.Code, recorder.Body.String())
	}
	persisted, err := session.Snapshot().Store.LoadProject(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	chapter := persisted.Progress.Chapters[0]
	if chapter.Content != "" || chapter.Summary != "" || chapter.Status != project.StatusPending || persisted.Progress.CurrentChapterIndex != 0 {
		t.Fatalf("frontier deletion did not reset chapter/index: %#v, index=%d", chapter, persisted.Progress.CurrentChapterIndex)
	}
	if len(persisted.Progress.MemoryEntries) != 1 || persisted.Progress.MemoryEntries[0].Chapter != 2 {
		t.Fatalf("frontier deletion did not purge memory: %#v", persisted.Progress.MemoryEntries)
	}
	if _, err := session.Snapshot().Store.LoadChapterMarkdown(context.Background(), 1); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("deleted markdown still exists or errored unexpectedly: %v", err)
	}
}

func TestDeleteChaptersFromClearsRangeAndPreservesLegacyMemory(t *testing.T) {
	session := selectedSession(t)
	if err := session.WithProgress(context.Background(), func(progress *project.Progress) error {
		progress.Phase, progress.CurrentChapterIndex = "writing", 1
		progress.Chapters = []project.Chapter{{Num: 1, Content: "first", Status: project.StatusAccepted}, {Num: 2, Content: "second", Summary: "two", Status: project.StatusReview}, {Num: 3, Content: "third", Summary: "three", Status: project.StatusAccepted}}
		progress.MemoryEntries = []project.MemoryEntry{{Chapter: 2, Content: "legacy range retention"}}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	for _, num := range []int{2, 3} {
		if err := session.Snapshot().Store.SaveChapterMarkdown(context.Background(), num, []byte("chapter")); err != nil {
			t.Fatal(err)
		}
	}

	server := New(session)
	recorder := httptest.NewRecorder()
	server.ServeHTTP(recorder, httptest.NewRequest(http.MethodDelete, "/api/chapters/from/2", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("DELETE /api/chapters/from/2 = (%d, %s)", recorder.Code, recorder.Body.String())
	}
	persisted, err := session.Snapshot().Store.LoadProject(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, chapter := range persisted.Progress.Chapters[1:] {
		if chapter.Content != "" || chapter.Summary != "" || chapter.Status != project.StatusPending {
			t.Fatalf("range chapter was not cleared: %#v", chapter)
		}
	}
	if persisted.Progress.CurrentChapterIndex != 1 || len(persisted.Progress.MemoryEntries) != 1 {
		t.Fatalf("range deletion changed cursor or legacy memory behavior: %#v", persisted.Progress)
	}
	for _, num := range []int{2, 3} {
		if _, err := session.Snapshot().Store.LoadChapterMarkdown(context.Background(), num); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("range markdown %d still exists or errored unexpectedly: %v", num, err)
		}
	}
}

func TestChapterDeletionRoutesValidateFrontierAndRange(t *testing.T) {
	server := New(&runtime.ProjectSession{})
	recorder := httptest.NewRecorder()
	server.ServeHTTP(recorder, httptest.NewRequest(http.MethodDelete, "/api/chapter", nil))
	if recorder.Code != http.StatusBadRequest || recorder.Body.String() != "{\"error\":\"select_project_first\"}\n" {
		t.Fatalf("DELETE /api/chapter without project = (%d, %s)", recorder.Code, recorder.Body.String())
	}

	session := selectedSession(t)
	server = New(session)
	for _, check := range []struct {
		path  string
		code  int
		error string
	}{{"/api/chapter", http.StatusBadRequest, "no_chapters_to_delete"}, {"/api/chapters/from/nope", http.StatusBadRequest, "invalid_chapter_num"}, {"/api/chapters/from/1", http.StatusNotFound, "chapter_n_not_found"}} {
		recorder := httptest.NewRecorder()
		server.ServeHTTP(recorder, httptest.NewRequest(http.MethodDelete, check.path, nil))
		if recorder.Code != check.code || !strings.Contains(recorder.Body.String(), check.error) {
			t.Errorf("DELETE %s = (%d, %s), want %d/%s", check.path, recorder.Code, recorder.Body.String(), check.code, check.error)
		}
	}
	if err := session.WithProgress(context.Background(), func(progress *project.Progress) error {
		progress.Chapters = []project.Chapter{{Num: 1, Status: project.StatusWriting}, {Num: 2, Status: project.StatusPending}}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	for _, check := range []struct{ path, error string }{{"/api/chapter", "writing_chapter_cannot_delete"}, {"/api/chapters/from/1", "writing_range_has_writing"}} {
		recorder := httptest.NewRecorder()
		server.ServeHTTP(recorder, httptest.NewRequest(http.MethodDelete, check.path, nil))
		if recorder.Code != http.StatusConflict || !strings.Contains(recorder.Body.String(), check.error) {
			t.Errorf("DELETE %s = (%d, %s)", check.path, recorder.Code, recorder.Body.String())
		}
	}
}

func TestForeshadowCRUDRoutesPersistCompatibleState(t *testing.T) {
	session := selectedSession(t)
	if err := session.WithProgress(context.Background(), func(progress *project.Progress) error {
		progress.Foreshadows = []project.Foreshadow{{ID: 3, Name: "Existing", Description: "Old", Status: project.ForeshadowPlanted}}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	server := New(session)
	created := httptest.NewRecorder()
	server.ServeHTTP(created, httptest.NewRequest(http.MethodPost, "/api/foreshadows", strings.NewReader(`{"name":"Key","description":"A clue","plant_chapter":2,"target_chapter":8}`)))
	if created.Code != http.StatusOK || !strings.Contains(created.Body.String(), `"id":4`) || !strings.Contains(created.Body.String(), `"status":"planted"`) || !strings.Contains(created.Body.String(), `"events":[]`) {
		t.Fatalf("POST foreshadow = (%d, %s)", created.Code, created.Body.String())
	}
	updated := httptest.NewRecorder()
	server.ServeHTTP(updated, httptest.NewRequest(http.MethodPut, "/api/foreshadows/4", strings.NewReader(`{"name":"Updated","status":"resolved","resolution":"Solved"}`)))
	if updated.Code != http.StatusOK || !strings.Contains(updated.Body.String(), `"name":"Updated"`) || !strings.Contains(updated.Body.String(), `"status":"resolved"`) {
		t.Fatalf("PUT foreshadow = (%d, %s)", updated.Code, updated.Body.String())
	}
	listed := httptest.NewRecorder()
	server.ServeHTTP(listed, httptest.NewRequest(http.MethodGet, "/api/foreshadows", nil))
	if listed.Code != http.StatusOK || !strings.Contains(listed.Body.String(), `"name":"Updated"`) {
		t.Fatalf("GET foreshadows = (%d, %s)", listed.Code, listed.Body.String())
	}
	deleted := httptest.NewRecorder()
	server.ServeHTTP(deleted, httptest.NewRequest(http.MethodDelete, "/api/foreshadows/4", nil))
	if deleted.Code != http.StatusOK || deleted.Body.String() != "{\"status\":\"deleted\"}\n" {
		t.Fatalf("DELETE foreshadow = (%d, %s)", deleted.Code, deleted.Body.String())
	}
	persisted, err := session.Snapshot().Store.LoadProject(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(persisted.Progress.Foreshadows) != 1 || persisted.Progress.Foreshadows[0].ID != 3 {
		t.Fatalf("foreshadow CRUD was not persisted: %#v", persisted.Progress.Foreshadows)
	}
}

func TestForeshadowRoutesValidateAndRequireProject(t *testing.T) {
	server := New(&runtime.ProjectSession{})
	for _, request := range []struct{ method, path, body string }{{http.MethodGet, "/api/foreshadows", ""}, {http.MethodPost, "/api/foreshadows", `{"name":"Key","description":"A clue"}`}, {http.MethodPut, "/api/foreshadows/1", `{}`}, {http.MethodDelete, "/api/foreshadows/1", ""}} {
		recorder := httptest.NewRecorder()
		server.ServeHTTP(recorder, httptest.NewRequest(request.method, request.path, strings.NewReader(request.body)))
		if recorder.Code != http.StatusBadRequest || recorder.Body.String() != "{\"error\":\"select_project_first\"}\n" {
			t.Errorf("%s %s without selection = (%d, %s)", request.method, request.path, recorder.Code, recorder.Body.String())
		}
	}
	server = New(selectedSession(t))
	for _, request := range []struct{ method, path, body, want string }{{http.MethodPost, "/api/foreshadows", `{`, "invalid_json"}, {http.MethodPost, "/api/foreshadows", `{}`, "foreshadow_name_required"}, {http.MethodPost, "/api/foreshadows", `{"name":"Key"}`, "foreshadow_desc_required"}, {http.MethodPut, "/api/foreshadows/nope", `{}`, "invalid_foreshadow_id"}, {http.MethodPut, "/api/foreshadows/1", `{}`, "foreshadow_not_found"}, {http.MethodDelete, "/api/foreshadows/1", "", "foreshadow_not_found"}} {
		recorder := httptest.NewRecorder()
		server.ServeHTTP(recorder, httptest.NewRequest(request.method, request.path, strings.NewReader(request.body)))
		if recorder.Code < http.StatusBadRequest || !strings.Contains(recorder.Body.String(), request.want) {
			t.Errorf("%s %s = (%d, %s), want %s", request.method, request.path, recorder.Code, recorder.Body.String(), request.want)
		}
	}
}

func TestStateParityRoutesPersistAndRespectSafety(t *testing.T) {
	session := selectedSession(t)
	if err := session.WithProgress(context.Background(), func(progress *project.Progress) error {
		progress.Phase = "writing"
		progress.Title = "Novel"
		progress.Chapters = []project.Chapter{{Num: 1, Title: "One", Status: project.StatusAccepted}}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	server := New(session)
	for _, request := range []struct{ method, path, body string }{
		{http.MethodPut, "/api/autoconfirm", `{"enabled":true}`},
		{http.MethodGet, "/api/autoconfirm", ""},
		{http.MethodDelete, "/api/outline", ""},
		{http.MethodDelete, "/api/progress", ""},
	} {
		recorder := httptest.NewRecorder()
		server.ServeHTTP(recorder, httptest.NewRequest(request.method, request.path, strings.NewReader(request.body)))
		if recorder.Code != http.StatusOK {
			t.Fatalf("%s %s = (%d, %s)", request.method, request.path, recorder.Code, recorder.Body.String())
		}
	}
	progress := session.Snapshot().Project.Progress
	if progress.Phase != "outline" || len(progress.Chapters) != 0 {
		t.Fatalf("reset progress = %#v", progress)
	}

	if err := session.WithProgress(context.Background(), func(progress *project.Progress) error {
		progress.Chapters = []project.Chapter{{Num: 1, Status: project.StatusWriting}}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	recorder := httptest.NewRecorder()
	server.ServeHTTP(recorder, httptest.NewRequest(http.MethodDelete, "/api/outline", nil))
	if recorder.Code != http.StatusConflict || !strings.Contains(recorder.Body.String(), "writing_chapter_present_delete") {
		t.Fatalf("DELETE /api/outline with draft = (%d, %s)", recorder.Code, recorder.Body.String())
	}
}

func selectedSession(t *testing.T) *runtime.ProjectSession {
	t.Helper()
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
	session := &runtime.ProjectSession{}
	if err := session.Select(context.Background(), store); err != nil {
		t.Fatal(err)
	}
	return session
}

func TestReadEndpointsServeSelectedAggregate(t *testing.T) {
	store, err := fsstore.NewAtProjectDir(filepath.Join("..", "infra", "fsstore", "testdata", "legacy-project"))
	if err != nil {
		t.Fatal(err)
	}
	session := &runtime.ProjectSession{}
	if err := session.Select(context.Background(), store); err != nil {
		t.Fatal(err)
	}
	server := New(session)

	checks := []struct{ path, fragment string }{
		{"/api/config", `"writing_pov":"第三人称限知"`},
		{"/api/progress", `"paragraph_locks":[1,3]`},
		{"/api/settings", `"name":"沈遥"`},
		{"/api/postprocess", `"book_complete":false`},
		{"/api/postprocess", `"state":{"diagnosis_report":"中段揭示偏慢。"`},
		{"/api/status", `"total_chapters":2`},
	}
	for _, check := range checks {
		recorder := httptest.NewRecorder()
		server.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, check.path, nil))
		if recorder.Code != http.StatusOK {
			t.Errorf("GET %s status = %d", check.path, recorder.Code)
			continue
		}
		if body := recorder.Body.String(); !strings.Contains(body, check.fragment) {
			t.Errorf("GET %s body missing %s: %s", check.path, check.fragment, body)
		}
		if contentType := recorder.Header().Get("Content-Type"); contentType != "application/json" {
			t.Errorf("GET %s Content-Type = %q", check.path, contentType)
		}
	}
}

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"showmethestory/internal/infra/apiconfig"
	"strings"
	"testing"

	"showmethestory/internal/domain/project"
)

func TestNewV2RuntimeComposesUnselectedProjectDependencies(t *testing.T) {
	dir := t.TempDir()
	runtime := newV2Runtime(dir, &apiconfig.Config{BaseURL: "https://example.test", APIKey: "key", Model: "model", HTTPTimeoutSeconds: 12})
	defer runtime.close()

	if runtime.session == nil || runtime.session.HasProject() {
		t.Fatal("v2 runtime must begin with an unselected project session")
	}
	if runtime.api == nil || runtime.tasks == nil || runtime.broadcaster == nil || runtime.events == nil {
		t.Fatal("v2 runtime did not compose API, task, and event dependencies")
	}
	if runtime.outline == nil || runtime.writing == nil || runtime.postprocess == nil || runtime.agent == nil {
		t.Fatal("v2 runtime did not compose application services")
	}
	store, err := runtime.projectStore("story")
	if err != nil {
		t.Fatalf("projectStore() error = %v", err)
	}
	if got, want := store.ProjectDir(), filepath.Join(dir, "storys", "story"); got != want {
		t.Fatalf("project store directory = %q, want %q", got, want)
	}

	response := httptest.NewRecorder()
	runtime.handler().ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/status", nil))
	if response.Code != http.StatusBadRequest {
		t.Fatalf("v2 status without selected project = %d, want %d", response.Code, http.StatusBadRequest)
	}
}

func TestV2GlobalRoutesCreateSelectAndPersistAPIConfig(t *testing.T) {
	runtime := newV2Runtime(t.TempDir(), apiconfig.Default())
	defer runtime.close()
	handler := runtime.handler()

	call := func(method, path string, body any) *httptest.ResponseRecorder {
		var data []byte
		if body != nil {
			data, _ = json.Marshal(body)
		}
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(method, path, bytes.NewReader(data)))
		return response
	}

	created := call(http.MethodPost, "/api/projects", map[string]string{"name": "story", "language": "en"})
	if created.Code != http.StatusOK {
		t.Fatalf("POST /api/projects = %d: %s", created.Code, created.Body.String())
	}
	projects := call(http.MethodGet, "/api/projects", nil)
	if projects.Code != http.StatusOK || !bytes.Contains(projects.Body.Bytes(), []byte(`"name":"story"`)) {
		t.Fatalf("GET /api/projects = %d: %s", projects.Code, projects.Body.String())
	}
	selected := call(http.MethodPost, "/api/projects/select", map[string]string{"name": "story"})
	if selected.Code != http.StatusOK || !runtime.session.HasProject() {
		t.Fatalf("POST /api/projects/select = %d: %s", selected.Code, selected.Body.String())
	}
	status := call(http.MethodGet, "/api/status", nil)
	if status.Code != http.StatusOK {
		t.Fatalf("GET /api/status after selection = %d: %s", status.Code, status.Body.String())
	}
	chatSessions := call(http.MethodGet, "/api/chat/sessions", nil)
	if chatSessions.Code != http.StatusOK {
		t.Fatalf("GET /api/chat/sessions after selection = %d: %s", chatSessions.Code, chatSessions.Body.String())
	}

	oldAPI, oldWriting := runtime.api, runtime.writing
	config := call(http.MethodPut, "/api/config/api", map[string]any{"base_url": "https://api.example.test", "api_key": "secret", "model": "test-model"})
	if config.Code != http.StatusOK {
		t.Fatalf("PUT /api/config/api = %d: %s", config.Code, config.Body.String())
	}
	if runtime.api == oldAPI || runtime.writing == oldWriting {
		t.Fatal("PUT /api/config/api did not replace v2 services using the provider")
	}
	stored, err := apiconfig.Load(filepath.Join(runtime.dataDir, "api.json"))
	if err != nil || stored.Model != "test-model" {
		t.Fatalf("persisted api config = %#v, err = %v", stored, err)
	}
	runtime.version = "v-test"
	version := call(http.MethodGet, "/api/version", nil)
	if version.Code != http.StatusOK || !bytes.Contains(version.Body.Bytes(), []byte(`"version":"v-test"`)) {
		t.Fatalf("GET /api/version = %d: %s", version.Code, version.Body.String())
	}
}

func TestV2EnabledWritingRulesFilterLanguageCategoryAndEnabledState(t *testing.T) {
	runtime := newV2Runtime(t.TempDir(), apiconfig.Default())
	defer runtime.close()
	ctx := context.Background()
	if _, err := runtime.CreateProject(ctx, "story", "en"); err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.SelectProject(ctx, "story"); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(runtime.dataDir, "skills"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(runtime.dataDir, "skills", "writing.md"), []byte("---\nid: root-writing\nname: Root Writing\ncategory: writing\nlang: en\n---\nROOT-WRITING-RULE"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(runtime.dataDir, "storys", "story", "skills"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(runtime.dataDir, "storys", "story", "skills", "disabled.md"), []byte("---\nid: disabled-writing\nname: Disabled Writing\ncategory: writing\nlang: en\n---\nDO-NOT-INJECT"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := runtime.session.WithProject(ctx, func(value *project.Project) error {
		value.Config.SkillConfig.EnabledSkills = map[string]bool{
			"root-writing":     true,
			"disabled-writing": false,
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	rules := runtime.enabledSkillRules("writing")
	if !strings.Contains(rules, "ROOT-WRITING-RULE") {
		t.Fatalf("enabled root writing skill missing: %q", rules)
	}
	for _, unwanted := range []string{"DO-NOT-INJECT", "中文小说家创作指南"} {
		if strings.Contains(rules, unwanted) {
			t.Fatalf("rules included unrelated, disabled, or wrong-language skill %q: %q", unwanted, rules)
		}
	}
}

func TestNewV2HTTPServerServesStaticAndV2API(t *testing.T) {
	runtime := newV2Runtime(t.TempDir(), apiconfig.Default())
	defer runtime.close()
	server, err := newHTTPServer(runtime.handler(), ":0")
	if err != nil {
		t.Fatalf("newHTTPServer() error = %v", err)
	}

	static := httptest.NewRecorder()
	server.Handler.ServeHTTP(static, httptest.NewRequest(http.MethodGet, "/", nil))
	if static.Code != http.StatusOK {
		t.Fatalf("GET / = %d, want %d", static.Code, http.StatusOK)
	}
	if got := static.Header().Get("Content-Type"); got != "text/html; charset=utf-8" {
		t.Fatalf("GET / content type = %q", got)
	}

	api := httptest.NewRecorder()
	server.Handler.ServeHTTP(api, httptest.NewRequest(http.MethodGet, "/api/status", nil))
	if api.Code != http.StatusBadRequest {
		t.Fatalf("GET /api/status = %d, want %d", api.Code, http.StatusBadRequest)
	}
}

package main

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"showmethestory/internal/app/agent"
	"showmethestory/internal/app/chat"
	"showmethestory/internal/app/continuation"
	"showmethestory/internal/app/foreshadow"
	"showmethestory/internal/app/outline"
	"showmethestory/internal/app/postprocess"
	"showmethestory/internal/app/runtime"
	settingsapp "showmethestory/internal/app/settings"
	"showmethestory/internal/app/writing"
	"showmethestory/internal/domain/project"
	"showmethestory/internal/httpapi"
	"showmethestory/internal/infra/apiconfig"
	"showmethestory/internal/infra/fsstore"
	"showmethestory/internal/infra/openai"
	"showmethestory/internal/infra/skills"
	"showmethestory/internal/infra/sse"
	"showmethestory/internal/ports"
)

// v2Runtime owns the process-wide dependencies. It starts with no selected
// project; a project store is attached when the user selects a project.
type v2Runtime struct {
	dataDir string
	version string
	// configUpdateMu serializes persistence and dependency replacement so the
	// in-memory provider snapshot always matches the api.json just written.
	configUpdateMu sync.Mutex
	configMu       sync.RWMutex
	apiCfg         *apiconfig.Config
	serverMu       sync.RWMutex
	server         http.Handler
	session        *runtime.ProjectSession
	api            *openai.Client
	tasks          *runtime.TaskManager
	broadcaster    *sse.Broadcaster
	events         http.Handler
	outline        *outline.Service
	continuation   *continuation.Service
	writing        *writing.Service
	postprocess    *postprocess.Service
	foreshadow     *foreshadow.Service
	settings       *settingsapp.Service
	agent          *agent.Service
	chat           *chat.Store
	polishRules    string
}

func newV2Runtime(dataDir string, apiCfg *apiconfig.Config) *v2Runtime {
	if apiCfg == nil {
		apiCfg = apiconfig.Default()
	}
	broadcaster := sse.NewBroadcaster()
	runtime := &v2Runtime{
		dataDir:     dataDir,
		session:     &runtime.ProjectSession{},
		tasks:       runtime.NewTaskManager(broadcaster),
		broadcaster: broadcaster,
		events:      sse.NewHandler(broadcaster),
		polishRules: "",
	}
	runtime.refreshServices(apiCfg)
	return runtime
}

// refreshServices atomically swaps the HTTP server and every service that owns
// an OpenAI client or model. The session, task manager, and event broadcaster
// remain shared, so selecting a project and connected SSE clients survive a
// global API configuration update.
func (r *v2Runtime) refreshServices(apiCfg *apiconfig.Config) {
	config := *apiCfg
	r.polishRules = r.enabledPolishRules()
	client := openai.New(openai.Config{
		BaseURL:   config.BaseURL,
		APIKey:    config.APIKey,
		URLStrict: config.URLStrict,
		Timeout:   time.Duration(config.HTTPTimeoutSeconds) * time.Second,
	})
	writingService := writing.New(writing.Dependencies{Session: r.session, Tasks: r.tasks, AI: client, Events: r.broadcaster, Model: config.Model, MaxTokens: config.MaxTokens, WritingRules: func() string { return r.enabledSkillRules("writing") }})
	outlineService := outline.New(outline.Dependencies{Session: r.session, Tasks: r.tasks, AI: client, Events: r.broadcaster, Model: config.Model, MaxTokens: config.MaxTokens})
	continuationService := continuation.New(continuation.Dependencies{Session: r.session, Tasks: r.tasks, AI: client, Events: r.broadcaster, Model: config.Model, MaxTokens: config.MaxTokens})
	postprocessService := postprocess.New(postprocess.Dependencies{Session: r.session, Tasks: r.tasks, AI: client, Events: r.broadcaster, Writing: writingService, Model: config.Model, MaxTokens: config.MaxTokens, ContextBudgetTokens: config.ContextBudgetTokens})
	foreshadowService := foreshadow.New(foreshadow.Dependencies{Session: r.session, Tasks: r.tasks, AI: client, Events: r.broadcaster, Model: config.Model, MaxTokens: config.MaxTokens})
	settingsService := settingsapp.New(settingsapp.Dependencies{Session: r.session, Tasks: r.tasks, AI: client, Events: r.broadcaster, Model: config.Model, MaxTokens: config.MaxTokens})
	agentService := agent.New(agent.Dependencies{Session: r.session, Tasks: r.tasks, AI: client, Events: r.broadcaster, Model: config.Model, MaxTokens: config.MaxTokens, WritingRules: func() string { return r.enabledSkillRules("writing") }})
	server := httpapi.New(r.session,
		httpapi.WithTaskManager(r.tasks),
		httpapi.WithEvents(r.events),
		httpapi.WithOutlineService(outlineService),
		httpapi.WithContinuationService(continuationService),
		httpapi.WithWritingService(writingService),
		httpapi.WithForeshadowService(foreshadowService),
		httpapi.WithSettingsService(settingsService),
		httpapi.WithPostProcessService(postprocessService),
		httpapi.WithAgentService(agentService),
		httpapi.WithChatStore(r.chat),
		httpapi.WithPolishRules(r.polishRules),
		httpapi.WithSkills(r.loadSkills),
		httpapi.WithSkillsChanged(func(context.Context) { r.refreshCurrentServices() }),
		httpapi.WithGlobalRoutes(r),
	)

	r.configMu.Lock()
	r.apiCfg = &config
	r.configMu.Unlock()
	r.serverMu.Lock()
	r.api, r.outline, r.continuation, r.writing, r.foreshadow, r.settings, r.postprocess, r.agent, r.server = client, outlineService, continuationService, writingService, foreshadowService, settingsService, postprocessService, agentService, server
	r.serverMu.Unlock()
}

// projectStore provides the filesystem store used when a project is selected.
// Keeping store creation here ensures all v2 routes resolve projects under the
// configured data directory rather than the process working directory.
func (r *v2Runtime) projectStore(projectName string) (*fsstore.Store, error) {
	store, err := fsstore.New(r.dataDir, projectName)
	if err != nil {
		return nil, httpapi.ErrProjectName
	}
	return store, nil
}

// loadSkills discovers root and project-local skill files while leaving the v2
// HTTP layer package-independent.
func (r *v2Runtime) loadSkills(config *project.Config, projectName string) []project.Skill {
	if config == nil {
		return []project.Skill{}
	}
	return skills.FilterByLanguage(skills.Merge(skills.LoadBuiltin(filepath.Join(r.dataDir, "skills")), skills.LoadProject(filepath.Join(r.dataDir, "storys", projectName))), config.Language)
}

func (r *v2Runtime) enabledPolishRules() string { return r.enabledSkillRules("polish") }

// enabledSkillRules returns only the selected category's enabled, project-language
// skills. Callers use it at prompt construction time so skill toggles and language
// changes take effect without rebuilding an in-flight service.
func (r *v2Runtime) enabledSkillRules(category string) string {
	snapshot := r.session.Snapshot()
	if snapshot == nil || snapshot.Project == nil || snapshot.Project.Config == nil || snapshot.Project.Config.SkillConfig == nil {
		return ""
	}
	var enabled []project.Skill
	for _, skill := range r.loadSkills(snapshot.Project.Config, snapshot.Name) {
		if skill.Category == category && snapshot.Project.Config.SkillConfig.EnabledSkills[skill.ID] {
			enabled = append(enabled, skill)
		}
	}
	return skills.Format(enabled)
}

func (r *v2Runtime) ListProjects(ctx context.Context) ([]httpapi.ProjectInfo, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(filepath.Join(r.dataDir, "storys"))
	if os.IsNotExist(err) {
		return []httpapi.ProjectInfo{}, nil
	}
	if err != nil {
		return nil, err
	}
	items := make([]httpapi.ProjectInfo, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		store, err := r.projectStore(entry.Name())
		if err != nil {
			continue
		}
		item := httpapi.ProjectInfo{Name: entry.Name(), Language: project.LangZH}
		loaded, err := store.LoadProject(ctx)
		if err == nil {
			if loaded.Config != nil {
				item.Language = project.NormalizeLanguage(loaded.Config.Language)
			}
			if loaded.Progress != nil {
				item.Phase, item.Title = loaded.Progress.Phase, loaded.Progress.Title
			}
		}
		if info, err := entry.Info(); err == nil {
			item.UpdatedAt = info.ModTime().Format(time.RFC3339)
		}
		items = append(items, item)
	}
	sort.Slice(items, func(i, j int) bool { return items[i].UpdatedAt > items[j].UpdatedAt })
	return items, nil
}

func (r *v2Runtime) CreateProject(ctx context.Context, name, language string) (httpapi.ProjectInfo, error) {
	store, err := r.projectStore(name)
	if err != nil {
		return httpapi.ProjectInfo{}, err
	}
	if _, err := os.Stat(store.ProjectDir()); err == nil {
		return httpapi.ProjectInfo{}, httpapi.ErrProjectExists
	} else if !os.IsNotExist(err) {
		return httpapi.ProjectInfo{}, err
	}
	config := project.DefaultConfigForLang(language)
	value := &project.Project{Name: name, Config: config, Settings: &project.ProjectSettings{}, PostProcess: project.DefaultPostProcessState()}
	if err := store.SaveProject(ctx, value); err != nil {
		return httpapi.ProjectInfo{}, err
	}
	return httpapi.ProjectInfo{Name: name, Language: config.Language}, nil
}

func (r *v2Runtime) SelectProject(ctx context.Context, name string) (httpapi.ProjectInfo, error) {
	store, err := r.projectStore(name)
	if err != nil {
		return httpapi.ProjectInfo{}, err
	}
	if info, err := os.Stat(store.ProjectDir()); err != nil || !info.IsDir() {
		return httpapi.ProjectInfo{}, httpapi.ErrProjectNotFound
	}
	if err := r.session.Select(ctx, store); err != nil {
		return httpapi.ProjectInfo{}, err
	}
	r.chat = chat.NewStore(filepath.Join(store.ProjectDir(), "sessions"))
	r.refreshCurrentServices()
	item := r.CurrentProject(ctx)
	return item, nil
}

func (r *v2Runtime) CurrentProject(context.Context) httpapi.ProjectInfo {
	snapshot := r.session.Snapshot()
	if snapshot == nil || snapshot.Project == nil {
		return httpapi.ProjectInfo{}
	}
	item := httpapi.ProjectInfo{Name: snapshot.Name, Language: project.LangZH}
	if snapshot.Project.Config != nil {
		item.Language = project.NormalizeLanguage(snapshot.Project.Config.Language)
	}
	if snapshot.Project.Progress != nil {
		item.Phase, item.Title = snapshot.Project.Progress.Phase, snapshot.Project.Progress.Title
	}
	return item
}

func (r *v2Runtime) DeleteProject(ctx context.Context, name string) error {
	if name == r.session.ProjectName() && name != "" {
		return httpapi.ErrProjectCurrent
	}
	store, err := r.projectStore(name)
	if err != nil {
		return err
	}
	if _, err := os.Stat(store.ProjectDir()); os.IsNotExist(err) {
		return httpapi.ErrProjectNotFound
	} else if err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return os.RemoveAll(store.ProjectDir())
}

func (r *v2Runtime) Version(context.Context) string { return r.version }

func (r *v2Runtime) GetAPIConfig(context.Context) (any, error) {
	r.configMu.RLock()
	defer r.configMu.RUnlock()
	if r.apiCfg == nil {
		return nil, errors.New("API configuration is unavailable")
	}
	copy := *r.apiCfg
	return &copy, nil
}

func (r *v2Runtime) PutAPIConfig(ctx context.Context, raw json.RawMessage) (any, error) {
	// A direct caller may update configuration concurrently even though the HTTP
	// route serializes mutations around active tasks. Keep disk and runtime state
	// ordered in that case as well.
	r.configUpdateMu.Lock()
	defer r.configUpdateMu.Unlock()

	var config apiconfig.Config
	if err := json.Unmarshal(raw, &config); err != nil {
		return nil, err
	}
	apiconfig.Normalize(&config)
	if err := apiconfig.Save(filepath.Join(r.dataDir, "api.json"), &config); err != nil {
		return nil, err
	}
	r.refreshServices(&config)
	return &config, nil
}

func (r *v2Runtime) ListModels(ctx context.Context, raw json.RawMessage) (any, error) {
	var config apiconfig.Config
	if err := json.Unmarshal(raw, &config); err != nil {
		return nil, err
	}
	apiconfig.Normalize(&config)
	models, err := apiconfig.ListModels(ctx, &config)
	if err != nil {
		return nil, err
	}
	return map[string]any{"models": models}, nil
}

func (r *v2Runtime) TestAPI(ctx context.Context, raw json.RawMessage) (any, error) {
	var config apiconfig.Config
	if err := json.Unmarshal(raw, &config); err != nil {
		return nil, err
	}
	if err := apiconfig.Validate(&config); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	response, err := apiconfig.NewClient(&config, 15*time.Second).Complete(ctx, ports.CompletionRequest{Model: config.Model, Messages: []ports.Message{{Role: "user", Content: "Hi"}}, MaxTokens: config.MaxTokens})
	if err != nil {
		return nil, err
	}
	sample := response.Content
	if len(sample) > 100 {
		sample = sample[:100] + "..."
	}
	return map[string]any{"success": true, "message": "连接成功", "model": config.Model, "sample": sample}, nil
}

func (r *v2Runtime) refreshCurrentServices() {
	r.configUpdateMu.Lock()
	defer r.configUpdateMu.Unlock()
	r.configMu.RLock()
	config := *r.apiCfg
	r.configMu.RUnlock()
	r.refreshServices(&config)
}

func (r *v2Runtime) handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		r.serverMu.RLock()
		server := r.server
		r.serverMu.RUnlock()
		server.ServeHTTP(w, request)
	})
}

func (r *v2Runtime) close() { r.broadcaster.Close() }

func startWebServer(apiCfg *apiconfig.Config, port, progDir, version string) {
	runtime := newV2Runtime(progDir, apiCfg)
	runtime.version = version
	defer runtime.close()

	server, err := newHTTPServer(runtime.handler(), port)
	if err != nil {
		log.Fatalf("嵌入静态文件失败: %v", err)
	}

	fmt.Printf(" [系统] AI 小说生成器 Web UI 启动中...\n")
	fmt.Printf(" [系统] 访问地址: http://localhost%s\n", port)
	fmt.Printf(" [系统] 程序目录: %s\n", progDir)
	fmt.Printf(" [系统] 项目目录: %s\n", filepath.Join(progDir, "storys"))

	go openBrowser(fmt.Sprintf("http://localhost%s", port))
	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		fmt.Fprintf(os.Stderr, " [错误] 服务器启动失败: %v\n", err)
		os.Exit(1)
	}
}

func newHTTPServer(api http.Handler, port string) (*http.Server, error) {
	staticFS, err := fs.Sub(staticFiles, "frontend/dist")
	if err != nil {
		return nil, err
	}
	mux := http.NewServeMux()
	mux.Handle("/api/", api)
	mux.Handle("/api", api)
	mux.Handle("/", staticHandler(staticFiles, staticFS))

	return &http.Server{
		Addr:         port,
		Handler:      recoveryMiddleware(corsMiddleware(loggingMiddleware(mux))),
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 0,
		IdleTimeout:  120 * time.Second,
	}, nil
}

func staticHandler(files embed.FS, staticFS fs.FS) http.Handler {
	fileServer := http.FileServer(http.FS(staticFS))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
		defer cancel()
		r = r.WithContext(ctx)
		if r.URL.Path == "/" || r.URL.Path == "/index.html" {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			data, err := files.ReadFile("frontend/dist/index.html")
			if err != nil {
				http.Error(w, "Internal Server Error", http.StatusInternalServerError)
				return
			}
			_, _ = w.Write(data)
			return
		}
		fileServer.ServeHTTP(w, r)
	})
}

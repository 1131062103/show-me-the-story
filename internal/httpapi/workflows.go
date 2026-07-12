package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
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
)

// Option configures optional v2 workflow dependencies.
type Option func(*workflowDependencies)

type outlineWorkflow interface {
	StartGenerate() error
	StartRevise(string) error
	StartContinue(int) error
	Confirm(context.Context) error
}
type writingWorkflow interface {
	StartGenerate() error
	StartRevise(string) error
	StartReviseSpecific(int, string) error
	StartPolish(int, string) error
}
type foreshadowWorkflow interface {
	StartSuggest() error
	StartOutlineCheck() error
}

// autoConfirmWritingWorkflow and transitionWritingWorkflow are deliberately
// optional so existing adapters and focused route fakes retain the base writing
// contract while the concrete v2 writing service provides the richer workflow.
type autoConfirmWritingWorkflow interface {
	StartGenerateAutoConfirm(func() bool) error
}
type transitionWritingWorkflow interface {
	StartSmoothTransitions() error
}
type postprocessWorkflow interface {
	StartAnalyze() error
	StartConsistency() error
	StartRoadmap() error
	StartExecute() error
}
type agentWorkflow interface {
	Start(string, []agent.Step, int, func(string, []agent.Step, error)) error
}
type settingsWorkflow interface {
	StartReconcile(project.StoryConfig) error
}
type continuationWorkflow interface {
	StartAnalyze(string) error
	Confirm(context.Context, continuation.Analysis) error
}

type workflowDependencies struct {
	tasks         *runtime.TaskManager
	events        http.Handler
	outline       outlineWorkflow
	writing       writingWorkflow
	foreshadow    foreshadowWorkflow
	postprocess   postprocessWorkflow
	agent         agentWorkflow
	settings      settingsWorkflow
	continuation  continuationWorkflow
	chat          *chat.Store
	polishRules   string
	skills        func(*project.Config, string) []project.Skill
	skillsChanged func(context.Context)
	global        GlobalRoutes
}

// WithTaskManager enables task status and cancellation endpoints.
func WithTaskManager(tasks *runtime.TaskManager) Option {
	return func(d *workflowDependencies) { d.tasks = tasks }
}

// WithEvents exposes the supplied SSE handler at /api/events.
func WithEvents(events http.Handler) Option {
	return func(d *workflowDependencies) { d.events = events }
}

// WithOutlineService registers the outline workflow service.
func WithOutlineService(service *outline.Service) Option { return WithOutlineWorkflow(service) }

// WithOutlineWorkflow registers an outline implementation, including test fakes.
func WithOutlineWorkflow(service outlineWorkflow) Option {
	return func(d *workflowDependencies) { d.outline = service }
}

// WithWritingService registers the chapter workflow service.
func WithWritingService(service *writing.Service) Option { return WithWritingWorkflow(service) }

// WithWritingWorkflow registers a chapter implementation, including test fakes.
func WithWritingWorkflow(service writingWorkflow) Option {
	return func(d *workflowDependencies) { d.writing = service }
}

// WithForeshadowService registers the foreshadow planning workflow service.
func WithForeshadowService(service *foreshadow.Service) Option {
	return WithForeshadowWorkflow(service)
}

// WithForeshadowWorkflow registers a foreshadow implementation, including test fakes.
func WithForeshadowWorkflow(service foreshadowWorkflow) Option {
	return func(d *workflowDependencies) { d.foreshadow = service }
}

// WithPostProcessService registers the full-book workflow service.
func WithPostProcessService(service *postprocess.Service) Option {
	return WithPostProcessWorkflow(service)
}

// WithPostProcessWorkflow registers a postprocess implementation, including test fakes.
func WithPostProcessWorkflow(service postprocessWorkflow) Option {
	return func(d *workflowDependencies) { d.postprocess = service }
}

// WithAgentService registers the assistant workflow service.
func WithAgentService(service *agent.Service) Option { return WithAgentWorkflow(service) }

// WithAgentWorkflow registers an assistant implementation, including test fakes.
func WithAgentWorkflow(service agentWorkflow) Option {
	return func(d *workflowDependencies) { d.agent = service }
}

// WithSettingsService registers AI-assisted settings reconciliation.
func WithSettingsService(service *settingsapp.Service) Option {
	return func(d *workflowDependencies) { d.settings = service }
}

// WithContinuationService registers existing-prose analysis and import.
func WithContinuationService(service *continuation.Service) Option {
	return WithContinuationWorkflow(service)
}

// WithContinuationWorkflow registers a continuation implementation, including test fakes.
func WithContinuationWorkflow(service continuationWorkflow) Option {
	return func(d *workflowDependencies) { d.continuation = service }
}

// WithChatStore registers the selected project's compatible chat-session store.
func WithChatStore(store *chat.Store) Option { return func(d *workflowDependencies) { d.chat = store } }

// WithPolishRules supplies the enabled polish-skill text used by /api/chapter/polish.
func WithPolishRules(rules string) Option {
	return func(d *workflowDependencies) { d.polishRules = rules }
}

// WithSkills supplies project-scoped, language-filtered skill discovery.
func WithSkills(load func(*project.Config, string) []project.Skill) Option {
	return func(d *workflowDependencies) { d.skills = load }
}

// WithSkillsChanged rebuilds services whose prompts depend on enabled skills.
func WithSkillsChanged(onChanged func(context.Context)) Option {
	return func(d *workflowDependencies) { d.skillsChanged = onChanged }
}

func (s *Server) registerWorkflowRoutes() {
	s.mux.HandleFunc("POST /api/settings/reconcile", s.postSettingsReconcile)
	// These legacy compatibility routes intentionally remain retired. Their
	// replacement is POST /api/settings/reconcile, so preserve the legacy 410
	// contract rather than treating them as an unimplemented v2 capability.
	s.mux.HandleFunc("POST /api/settings/ai-generate", s.settingsAIGenerateMoved)
	s.mux.HandleFunc("POST /api/settings/polish", s.settingsPolishMoved)
	s.mux.HandleFunc("POST /api/outline/generate", s.postOutlineGenerate)
	s.mux.HandleFunc("POST /api/outline/confirm", s.postOutlineConfirm)
	s.mux.HandleFunc("POST /api/outline/revise", s.postOutlineRevise)
	s.mux.HandleFunc("POST /api/outline/generate-continuation", s.postOutlineContinue)
	s.mux.HandleFunc("POST /api/outline/characters/confirm", s.postOutlineCharactersConfirm)
	s.mux.HandleFunc("POST /api/continue/import", s.postContinueImport)
	s.mux.HandleFunc("POST /api/continue/confirm", s.postContinueConfirm)
	s.mux.HandleFunc("POST /api/chapter/generate", s.postChapterGenerate)
	s.mux.HandleFunc("POST /api/chapter/revise", s.postChapterRevise)
	s.mux.HandleFunc("POST /api/chapter/revise/{num}", s.postChapterReviseSpecific)
	s.mux.HandleFunc("POST /api/chapter/polish", s.postChapterPolish)
	s.mux.HandleFunc("GET /api/chapter/conflict", s.getChapterConflict)
	s.mux.HandleFunc("POST /api/chapter/conflict-resolve", s.postChapterConflictResolve)
	s.mux.HandleFunc("POST /api/chapters/smooth-transitions", s.postChaptersSmoothTransitions)
	s.mux.HandleFunc("POST /api/foreshadows/suggest", s.postForeshadowsSuggest)
	s.mux.HandleFunc("POST /api/foreshadows/outline-check", s.postForeshadowOutlineCheck)
	s.mux.HandleFunc("PUT /api/postprocess/roadmap", s.putPostProcessRoadmap)
	s.mux.HandleFunc("DELETE /api/postprocess", s.deletePostProcess)
	s.mux.HandleFunc("POST /api/postprocess/diagnose", s.postPostProcessDiagnose)
	s.mux.HandleFunc("POST /api/postprocess/consistency", s.postPostProcessConsistency)
	s.mux.HandleFunc("POST /api/postprocess/roadmap", s.postPostProcessRoadmap)
	s.mux.HandleFunc("POST /api/postprocess/execute", s.postPostProcessExecute)
	s.mux.HandleFunc("POST /api/task/stop", s.postTaskStop)
	if s.workflows.events != nil {
		s.mux.Handle("GET /api/events", s.workflows.events)
	} else {
		s.mux.HandleFunc("GET /api/events", s.notImplemented)
	}
	s.mux.HandleFunc("GET /api/chat/sessions", s.getChatSessions)
	s.mux.HandleFunc("POST /api/chat/sessions", s.postChatSession)
	s.mux.HandleFunc("GET /api/chat/sessions/{id}", s.getChatSession)
	s.mux.HandleFunc("DELETE /api/chat/sessions/{id}", s.deleteChatSession)
	s.mux.HandleFunc("POST /api/chat/sessions/{id}/messages", s.postChatMessage)
}

func (s *Server) notImplemented(w http.ResponseWriter, r *http.Request) {
	writeError(w, http.StatusNotImplemented, "not_implemented")
}

func (s *Server) settingsAIGenerateMoved(w http.ResponseWriter, r *http.Request) {
	writeError(w, http.StatusGone, "settings_ai_generate_moved")
}

func (s *Server) settingsPolishMoved(w http.ResponseWriter, r *http.Request) {
	writeError(w, http.StatusGone, "settings_polish_moved")
}
func (s *Server) startResult(w http.ResponseWriter, err error) {
	if err == nil {
		writeJSON(w, http.StatusAccepted, map[string]string{"status": "started"})
		return
	}
	if errors.Is(err, outline.ErrTaskRunning) || errors.Is(err, writing.ErrTaskRunning) || errors.Is(err, foreshadow.ErrTaskRunning) || errors.Is(err, postprocess.ErrTaskRunning) || errors.Is(err, agent.ErrTaskRunning) || errors.Is(err, settingsapp.ErrTaskRunning) || errors.Is(err, continuation.ErrTaskRunning) {
		writeError(w, http.StatusConflict, "task_running_wait")
		return
	}
	writeError(w, http.StatusBadRequest, "workflow_start_failed")
}
func (s *Server) requireWorkflow(w http.ResponseWriter, ok bool) bool {
	if !ok {
		s.notImplemented(w, nil)
		return false
	}
	return true
}
func (s *Server) taskRunning() bool { return s.workflows.tasks != nil && s.workflows.tasks.Running() }
func (s *Server) rejectTask(w http.ResponseWriter) bool {
	if s.taskRunning() {
		writeError(w, http.StatusConflict, "task_running_wait")
		return true
	}
	return false
}

func (s *Server) postSettingsReconcile(w http.ResponseWriter, r *http.Request) {
	if !s.requireWorkflow(w, s.workflows.settings != nil) {
		return
	}
	var story project.StoryConfig
	if err := json.NewDecoder(r.Body).Decode(&story); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_json")
		return
	}
	s.startResult(w, s.workflows.settings.StartReconcile(story))
}

func (s *Server) postOutlineGenerate(w http.ResponseWriter, r *http.Request) {
	if s.requireWorkflow(w, s.workflows.outline != nil) {
		s.startResult(w, s.workflows.outline.StartGenerate())
	}
}
func (s *Server) postOutlineConfirm(w http.ResponseWriter, r *http.Request) {
	if !s.requireWorkflow(w, s.workflows.outline != nil) || s.rejectTask(w) {
		return
	}
	if err := s.workflows.outline.Confirm(r.Context()); err != nil {
		writeError(w, http.StatusBadRequest, "outline_confirm_failed")
		return
	}
	writeJSON(w, http.StatusOK, s.session.Snapshot().Project.Progress)
}
func feedback(r *http.Request) (string, error) {
	var body struct {
		Feedback string `json:"feedback"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		return "", err
	}
	if strings.TrimSpace(body.Feedback) == "" {
		return "", errors.New("missing")
	}
	return body.Feedback, nil
}
func (s *Server) postOutlineRevise(w http.ResponseWriter, r *http.Request) {
	if !s.requireWorkflow(w, s.workflows.outline != nil) {
		return
	}
	value, err := feedback(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "missing_feedback")
		return
	}
	s.startResult(w, s.workflows.outline.StartRevise(value))
}
func (s *Server) postOutlineContinue(w http.ResponseWriter, r *http.Request) {
	if !s.requireWorkflow(w, s.workflows.outline != nil) {
		return
	}
	var body struct {
		ChapterCount int `json:"chapter_count"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	if body.ChapterCount <= 0 {
		body.ChapterCount = 5
	}
	s.startResult(w, s.workflows.outline.StartContinue(body.ChapterCount))
}
func (s *Server) postContinueImport(w http.ResponseWriter, r *http.Request) {
	if !s.requireWorkflow(w, s.workflows.continuation != nil) {
		return
	}
	var body struct {
		Content string `json:"content"`
	}
	if json.NewDecoder(r.Body).Decode(&body) != nil || strings.TrimSpace(body.Content) == "" {
		writeError(w, http.StatusBadRequest, "missing_content")
		return
	}
	s.startResult(w, s.workflows.continuation.StartAnalyze(body.Content))
}

func (s *Server) postContinueConfirm(w http.ResponseWriter, r *http.Request) {
	if !s.requireWorkflow(w, s.workflows.continuation != nil) || s.rejectTask(w) {
		return
	}
	selected := s.selected(w)
	if selected == nil {
		return
	}
	if selected.Project.Progress != nil && selected.Project.Progress.Phase != "" && selected.Project.Progress.Phase != "outline" {
		writeError(w, http.StatusBadRequest, "continue_reset_first")
		return
	}
	var analysis continuation.Analysis
	if json.NewDecoder(r.Body).Decode(&analysis) != nil {
		writeError(w, http.StatusBadRequest, "invalid_json")
		return
	}
	if len(analysis.Chapters) == 0 {
		writeError(w, http.StatusBadRequest, "analysis_no_chapters")
		return
	}
	if err := s.workflows.continuation.Confirm(r.Context(), analysis); err != nil {
		switch {
		case errors.Is(err, continuation.ErrNoAnalysis):
			writeError(w, http.StatusBadRequest, "continue_analyze_first")
		case errors.Is(err, continuation.ErrResetRequired):
			writeError(w, http.StatusBadRequest, "continue_reset_first")
		case errors.Is(err, continuation.ErrNoChapters):
			writeError(w, http.StatusBadRequest, "analysis_no_chapters")
		default:
			writeError(w, http.StatusInternalServerError, "continue_import_failed")
		}
		return
	}
	writeJSON(w, http.StatusOK, s.session.Snapshot().Project.Progress)
}

func (s *Server) postOutlineCharactersConfirm(w http.ResponseWriter, r *http.Request) {
	if s.rejectTask(w) {
		return
	}
	var body struct {
		Characters []project.OutlineCharacterSuggestion `json:"characters"`
	}
	if json.NewDecoder(r.Body).Decode(&body) != nil || len(body.Characters) == 0 {
		writeError(w, http.StatusBadRequest, "invalid_json")
		return
	}
	var characters []project.Character
	if s.session == nil {
		writeError(w, http.StatusBadRequest, "select_project_first")
		return
	}
	err := s.session.WithProject(r.Context(), func(value *project.Project) error {
		if value.Settings == nil {
			value.Settings = &project.ProjectSettings{}
		}
		existing := make(map[string]bool, len(value.Settings.Characters))
		for _, character := range value.Settings.Characters {
			existing[character.Name] = true
		}
		for _, item := range body.Characters {
			name := strings.Trim(strings.TrimSpace(item.Name), "《》<>[]（）()")
			if name == "" || existing[name] {
				continue
			}
			notes := strings.TrimSpace(item.Description)
			if item.Role != "" {
				if notes != "" {
					notes += "；"
				}
				notes += item.Role
			}
			entry := project.Character{ID: nextSettingsID(value.Settings, "c"), Name: name, Background: notes, Notes: fmt.Sprintf("首次登场：第%d章", item.ChapterNum)}
			value.Settings.Characters = append(value.Settings.Characters, entry)
			characters = append(characters, entry)
			existing[name] = true
		}
		if value.Progress != nil && value.Progress.LastOutlineCharacterReport != nil {
			value.Progress.LastOutlineCharacterReport.HasSuggestions = false
			value.Progress.LastOutlineCharacterReport.Suggestions = nil
			value.Progress.LastOutlineCharacterReport.Summary = "已采纳建议并登记角色"
		}
		return nil
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "save_failed")
		return
	}
	writeJSON(w, http.StatusOK, characters)
}

func (s *Server) postChapterGenerate(w http.ResponseWriter, r *http.Request) {
	if !s.requireWorkflow(w, s.workflows.writing != nil) {
		return
	}
	if workflow, ok := s.workflows.writing.(autoConfirmWritingWorkflow); ok {
		s.startResult(w, workflow.StartGenerateAutoConfirm(s.autoConfirmEnabled))
		return
	}
	s.startResult(w, s.workflows.writing.StartGenerate())
}

func (s *Server) autoConfirmEnabled() bool {
	s.autoConfirmMu.RLock()
	defer s.autoConfirmMu.RUnlock()
	return s.autoConfirm
}

func (s *Server) postChaptersSmoothTransitions(w http.ResponseWriter, r *http.Request) {
	if !s.requireWorkflow(w, s.workflows.writing != nil) {
		return
	}
	workflow, ok := s.workflows.writing.(transitionWritingWorkflow)
	if !ok {
		s.notImplemented(w, r)
		return
	}
	s.startResult(w, workflow.StartSmoothTransitions())
}

func (s *Server) postForeshadowsSuggest(w http.ResponseWriter, r *http.Request) {
	if !s.requireWorkflow(w, s.workflows.foreshadow != nil) {
		return
	}
	selected := s.selected(w)
	if selected == nil || selected.Project.Progress == nil || len(selected.Project.Progress.Chapters) == 0 {
		writeError(w, http.StatusBadRequest, "need_generate_outline_first")
		return
	}
	s.startResult(w, s.workflows.foreshadow.StartSuggest())
}

func (s *Server) postForeshadowOutlineCheck(w http.ResponseWriter, r *http.Request) {
	if !s.requireWorkflow(w, s.workflows.foreshadow != nil) {
		return
	}
	selected := s.selected(w)
	if selected == nil || selected.Project.Progress == nil || len(selected.Project.Progress.Foreshadows) == 0 {
		writeError(w, http.StatusBadRequest, "no_foreshadows_to_check")
		return
	}
	s.startResult(w, s.workflows.foreshadow.StartOutlineCheck())
}
func (s *Server) postChapterRevise(w http.ResponseWriter, r *http.Request) {
	if !s.requireWorkflow(w, s.workflows.writing != nil) {
		return
	}
	value, err := feedback(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "missing_feedback")
		return
	}
	s.startResult(w, s.workflows.writing.StartRevise(value))
}
func (s *Server) postChapterReviseSpecific(w http.ResponseWriter, r *http.Request) {
	if !s.requireWorkflow(w, s.workflows.writing != nil) {
		return
	}
	number, err := strconv.Atoi(r.PathValue("num"))
	if err != nil || number <= 0 {
		writeError(w, http.StatusBadRequest, "invalid_chapter_num")
		return
	}
	value, err := feedback(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "missing_feedback")
		return
	}
	s.startResult(w, s.workflows.writing.StartReviseSpecific(number, value))
}
func (s *Server) postChapterPolish(w http.ResponseWriter, r *http.Request) {
	if !s.requireWorkflow(w, s.workflows.writing != nil) {
		return
	}
	selected := s.selected(w)
	if selected == nil || selected.Project.Progress == nil {
		return
	}
	idx := selected.Project.Progress.CurrentChapterIndex
	s.startResult(w, s.workflows.writing.StartPolish(idx, s.workflows.polishRules))
}
func (s *Server) getChapterConflict(w http.ResponseWriter, r *http.Request) {
	selected := s.selected(w)
	if selected != nil {
		writeJSON(w, http.StatusOK, map[string]any{"conflict": selected.Project.Progress.PendingWritingConflict})
	}
}

func (s *Server) postChapterConflictResolve(w http.ResponseWriter, r *http.Request) {
	if s.rejectTask(w) {
		return
	}
	var body struct {
		Action string `json:"action"`
	}
	if json.NewDecoder(r.Body).Decode(&body) != nil || body.Action == "" {
		writeError(w, http.StatusBadRequest, "missing_action")
		return
	}
	var result *project.Progress
	err := s.session.WithProgress(r.Context(), func(progress *project.Progress) error {
		conflict := progress.PendingWritingConflict
		if conflict == nil {
			return errors.New("no conflict")
		}
		if conflict.ChapterIndex < 0 || conflict.ChapterIndex >= len(progress.Chapters) {
			return errors.New("invalid conflict chapter")
		}
		switch body.Action {
		case "force_review":
			progress.Chapters[conflict.ChapterIndex].Status = project.StatusReview
			progress.PendingWritingConflict = nil
		case "dismiss", "retry":
			progress.PendingWritingConflict = nil
		default:
			return errInvalidProgressUpdate
		}
		result = progress
		return nil
	})
	if err != nil {
		switch {
		case errors.Is(err, errInvalidProgressUpdate):
			writeError(w, http.StatusBadRequest, "unsupported_action")
		case strings.Contains(err.Error(), "no conflict"):
			writeError(w, http.StatusBadRequest, "writing_conflict_none")
		case strings.Contains(err.Error(), "invalid conflict"):
			writeError(w, http.StatusBadRequest, "invalid_conflict_chapter_idx")
		default:
			writeError(w, http.StatusInternalServerError, "save_progress_failed")
		}
		return
	}
	if body.Action == "retry" {
		writeJSON(w, http.StatusAccepted, map[string]string{"status": "retry"})
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) postTaskStop(w http.ResponseWriter, r *http.Request) {
	if s.workflows.tasks == nil || !s.workflows.tasks.Stop() {
		writeError(w, http.StatusBadRequest, "no_task_running")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "stopping"})
}
func (s *Server) postPostProcessDiagnose(w http.ResponseWriter, r *http.Request) {
	if s.requireWorkflow(w, s.workflows.postprocess != nil) {
		s.startResult(w, s.workflows.postprocess.StartAnalyze())
	}
}
func (s *Server) postPostProcessConsistency(w http.ResponseWriter, r *http.Request) {
	if s.requireWorkflow(w, s.workflows.postprocess != nil) {
		s.startResult(w, s.workflows.postprocess.StartConsistency())
	}
}
func (s *Server) postPostProcessRoadmap(w http.ResponseWriter, r *http.Request) {
	if s.requireWorkflow(w, s.workflows.postprocess != nil) {
		s.startResult(w, s.workflows.postprocess.StartRoadmap())
	}
}
func (s *Server) postPostProcessExecute(w http.ResponseWriter, r *http.Request) {
	if s.requireWorkflow(w, s.workflows.postprocess != nil) {
		s.startResult(w, s.workflows.postprocess.StartExecute())
	}
}
func (s *Server) putPostProcessRoadmap(w http.ResponseWriter, r *http.Request) {
	if s.rejectTask(w) {
		return
	}
	var body struct {
		Roadmap        []project.RoadmapItem              `json:"roadmap"`
		ExecuteOptions *project.PostProcessExecuteOptions `json:"execute_options"`
	}
	if json.NewDecoder(r.Body).Decode(&body) != nil {
		writeError(w, http.StatusBadRequest, "invalid_json")
		return
	}
	if !s.updatePostprocess(w, r, func(state *project.PostProcessState) {
		if body.Roadmap != nil {
			state.Roadmap = body.Roadmap
		}
		if body.ExecuteOptions != nil {
			state.ExecuteOptions = body.ExecuteOptions
		}
	}) {
		return
	}
	s.getPostProcess(w, r)
}
func (s *Server) deletePostProcess(w http.ResponseWriter, r *http.Request) {
	if s.rejectTask(w) {
		return
	}
	if !s.updatePostprocess(w, r, func(state *project.PostProcessState) { *state = *project.DefaultPostProcessState() }) {
		return
	}
	s.getPostProcess(w, r)
}
func (s *Server) updatePostprocess(w http.ResponseWriter, r *http.Request, update func(*project.PostProcessState)) bool {
	if s.session == nil {
		writeError(w, http.StatusBadRequest, "select_project_first")
		return false
	}
	err := s.session.WithProject(r.Context(), func(value *project.Project) error {
		if value.PostProcess == nil {
			value.PostProcess = project.DefaultPostProcessState()
		}
		update(value.PostProcess)
		return nil
	})
	if err != nil {
		if errors.Is(err, runtime.ErrNoProject) {
			writeError(w, http.StatusBadRequest, "select_project_first")
		} else {
			writeError(w, http.StatusInternalServerError, "save_failed")
		}
		return false
	}
	return true
}

func (s *Server) chatStore(w http.ResponseWriter) *chat.Store {
	if s.selected(w) == nil {
		return nil
	}
	if s.workflows.chat == nil {
		s.notImplemented(w, nil)
		return nil
	}
	return s.workflows.chat
}
func (s *Server) getChatSessions(w http.ResponseWriter, r *http.Request) {
	store := s.chatStore(w)
	if store == nil {
		return
	}
	index, err := store.LoadIndex(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "load_session_list_failed")
		return
	}
	writeJSON(w, http.StatusOK, index)
}
func (s *Server) postChatSession(w http.ResponseWriter, r *http.Request) {
	if s.chatStore(w) == nil {
		return
	}
	now := time.Now().Format(time.RFC3339)
	writeJSON(w, http.StatusOK, &chat.Session{ID: chat.NewSession("").ID, Title: "新会话", Messages: []chat.Message{}, CreatedAt: now, UpdatedAt: now})
}
func (s *Server) getChatSession(w http.ResponseWriter, r *http.Request) {
	store := s.chatStore(w)
	if store == nil {
		return
	}
	value, err := store.Load(r.Context(), r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusNotFound, "chat_session_not_found")
		return
	}
	writeJSON(w, http.StatusOK, value)
}
func (s *Server) deleteChatSession(w http.ResponseWriter, r *http.Request) {
	if s.rejectTask(w) {
		return
	}
	store := s.chatStore(w)
	if store == nil {
		return
	}
	if err := store.Delete(r.Context(), r.PathValue("id")); err != nil {
		writeError(w, http.StatusInternalServerError, "delete_session_failed")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}
func (s *Server) postChatMessage(w http.ResponseWriter, r *http.Request) {
	store := s.chatStore(w)
	if store == nil {
		return
	}
	if !s.requireWorkflow(w, s.workflows.agent != nil) {
		return
	}
	var body struct {
		Content string `json:"content"`
	}
	if json.NewDecoder(r.Body).Decode(&body) != nil || strings.TrimSpace(body.Content) == "" {
		writeError(w, http.StatusBadRequest, "missing_content")
		return
	}
	id := r.PathValue("id")
	value, err := store.Load(r.Context(), id)
	if err != nil {
		if !chat.ValidSessionID(id) {
			writeError(w, http.StatusNotFound, "chat_session_not_found")
			return
		}
		value = &chat.Session{ID: id, Title: chat.Title(body.Content), CreatedAt: time.Now().Format(time.RFC3339)}
	}
	value.Messages = append(value.Messages, chat.Message{Role: "user", Content: body.Content, Timestamp: time.Now().Format(time.RFC3339)})
	value.UpdatedAt = time.Now().Format(time.RFC3339)
	if err := store.Save(r.Context(), value); err != nil {
		writeError(w, http.StatusInternalServerError, "save_session_failed")
		return
	}
	history := chatHistory(value.Messages[:len(value.Messages)-1])
	err = s.workflows.agent.Start(body.Content, history, 30, func(reply string, steps []agent.Step, runErr error) {
		for _, step := range steps[len(history):] {
			// Run includes the final assistant response in steps; reply carries that
			// same response for persistence below. Keep only one chat message.
			if step.Role == "assistant" && step.Content == reply {
				continue
			}
			value.Messages = append(value.Messages, stepMessage(step))
		}
		if reply != "" {
			value.Messages = append(value.Messages, chat.Message{Role: "assistant", Content: reply, Timestamp: time.Now().Format(time.RFC3339)})
		}
		value.UpdatedAt = time.Now().Format(time.RFC3339)
		_ = store.Save(context.Background(), value)
	})
	s.startResult(w, err)
}
func chatHistory(messages []chat.Message) []agent.Step {
	out := make([]agent.Step, 0, len(messages))
	for _, message := range messages {
		step := agent.Step{Role: message.Role, Content: message.Content, ToolResult: message.ToolResult, ToolResultKey: message.ToolResultKey, ToolResultArgs: message.ToolResultArgs}
		if len(message.ToolCalls) > 0 {
			call := message.ToolCalls[0]
			step.ToolCall = &call
		}
		out = append(out, step)
	}
	return out
}
func stepMessage(step agent.Step) chat.Message {
	message := chat.Message{Role: step.Role, Content: step.Content, ToolResult: step.ToolResult, ToolResultKey: step.ToolResultKey, ToolResultArgs: step.ToolResultArgs, Timestamp: time.Now().Format(time.RFC3339)}
	if step.ToolCall != nil {
		message.ToolCalls = []chat.ToolCall{*step.ToolCall}
	}
	return message
}

// Package httpapi exposes the v2 application runtime over HTTP.
package httpapi

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"

	"showmethestory/internal/app/foreshadow"
	"showmethestory/internal/app/runtime"
	"showmethestory/internal/domain/project"
)

// Server serves project-scoped v2 HTTP endpoints.
type Server struct {
	session       *runtime.ProjectSession
	mux           *http.ServeMux
	workflows     workflowDependencies
	autoConfirmMu sync.RWMutex
	autoConfirm   bool
}

// New creates a v2 HTTP server. Options inject optional workflow services so the
// read/write adapter remains usable while application workflows are composed.
func New(session *runtime.ProjectSession, options ...Option) *Server {
	server := &Server{session: session, mux: http.NewServeMux()}
	for _, option := range options {
		if option != nil {
			option(&server.workflows)
		}
	}
	server.mux.HandleFunc("GET /api/config", server.getConfig)
	server.mux.HandleFunc("PUT /api/config", server.putConfig)
	server.mux.HandleFunc("GET /api/config/pending-changes", server.getPendingConfigChanges)
	server.mux.HandleFunc("POST /api/config/apply-changes", server.postApplyConfigChanges)
	server.mux.HandleFunc("DELETE /api/config/pending-changes", server.deletePendingConfigChanges)
	server.mux.HandleFunc("GET /api/progress", server.getProgress)
	server.mux.HandleFunc("DELETE /api/progress", server.deleteProgress)
	server.mux.HandleFunc("DELETE /api/outline", server.deleteOutline)
	server.mux.HandleFunc("GET /api/autoconfirm", server.getAutoConfirm)
	server.mux.HandleFunc("PUT /api/autoconfirm", server.putAutoConfirm)
	server.mux.HandleFunc("PUT /api/outline/{num}", server.putChapterOutline)
	server.mux.HandleFunc("PUT /api/outline/{num}/lock", server.putChapterOutlineLock)
	server.mux.HandleFunc("PUT /api/chapter/{num}/paragraph-locks", server.putChapterParagraphLocks)
	server.mux.HandleFunc("POST /api/chapter/edit", server.postChapterEdit)
	server.mux.HandleFunc("POST /api/chapter/confirm", server.postChapterConfirm)
	server.mux.HandleFunc("POST /api/chapter/reject", server.postChapterReject)
	server.mux.HandleFunc("DELETE /api/chapter", server.deleteChapter)
	server.mux.HandleFunc("DELETE /api/chapters/from/{num}", server.deleteChaptersFrom)
	server.mux.HandleFunc("GET /api/foreshadows", server.getForeshadows)
	server.mux.HandleFunc("GET /api/foreshadows/roadmap", server.getForeshadowsRoadmap)
	server.mux.HandleFunc("POST /api/foreshadows/confirm", server.postForeshadowsConfirm)
	server.mux.HandleFunc("POST /api/foreshadows", server.postForeshadow)
	server.mux.HandleFunc("PUT /api/foreshadows/{id}", server.putForeshadow)
	server.mux.HandleFunc("DELETE /api/foreshadows/{id}", server.deleteForeshadow)
	server.mux.HandleFunc("GET /api/settings", server.getSettings)
	server.mux.HandleFunc("POST /api/characters", server.postCharacter)
	server.mux.HandleFunc("PUT /api/characters/{id}", server.putCharacter)
	server.mux.HandleFunc("DELETE /api/characters/{id}", server.deleteCharacter)
	server.mux.HandleFunc("POST /api/worldview", server.postWorldview)
	server.mux.HandleFunc("PUT /api/worldview/{id}", server.putWorldview)
	server.mux.HandleFunc("DELETE /api/worldview/{id}", server.deleteWorldview)
	server.mux.HandleFunc("POST /api/organizations", server.postOrganization)
	server.mux.HandleFunc("PUT /api/organizations/{id}", server.putOrganization)
	server.mux.HandleFunc("DELETE /api/organizations/{id}", server.deleteOrganization)
	server.mux.HandleFunc("POST /api/relations", server.postRelation)
	server.mux.HandleFunc("PUT /api/relations/{id}", server.putRelation)
	server.mux.HandleFunc("DELETE /api/relations/{id}", server.deleteRelation)
	server.mux.HandleFunc("GET /api/postprocess", server.getPostProcess)
	server.mux.HandleFunc("GET /api/status", server.getStatus)
	server.mux.HandleFunc("GET /api/skills", server.getSkills)
	server.mux.HandleFunc("PUT /api/skills/{id}/toggle", server.putSkillToggle)
	server.registerGlobalRoutes()
	server.registerWorkflowRoutes()
	return server
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) { s.mux.ServeHTTP(w, r) }

func (s *Server) selected(w http.ResponseWriter) *runtime.SelectedProject {
	if s.session == nil {
		writeError(w, http.StatusBadRequest, "select_project_first")
		return nil
	}
	snapshot := s.session.Snapshot()
	if snapshot == nil || snapshot.Project == nil {
		writeError(w, http.StatusBadRequest, "select_project_first")
		return nil
	}
	return snapshot
}

func (s *Server) getConfig(w http.ResponseWriter, r *http.Request) {
	if selected := s.selected(w); selected != nil {
		writeJSON(w, http.StatusOK, selected.Project.Config)
	}
}

func (s *Server) putConfig(w http.ResponseWriter, r *http.Request) {
	if s.session == nil || s.session.Snapshot() == nil {
		writeError(w, http.StatusBadRequest, "select_project_first")
		return
	}

	var config project.Config
	if err := json.NewDecoder(r.Body).Decode(&config); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_json")
		return
	}
	normalizeConfigForWrite(&config)

	if err := s.session.WithProject(r.Context(), func(value *project.Project) error {
		value.Config = &config
		return nil
	}); err != nil {
		if errors.Is(err, runtime.ErrNoProject) {
			writeError(w, http.StatusBadRequest, "select_project_first")
			return
		}
		writeError(w, http.StatusInternalServerError, "save_config_failed")
		return
	}
	writeJSON(w, http.StatusOK, &config)
}

// normalizeConfigForWrite keeps the established PUT /api/config defaults while
// keeping the v2 domain independent from prompt template definitions.
func normalizeConfigForWrite(config *project.Config) {
	if config.Story.ChapterCount <= 0 {
		config.Story.ChapterCount = 30
	}
	if config.Story.TargetWordsPerChapter <= 0 {
		config.Story.TargetWordsPerChapter = 2500
	}
	config.Normalize()
}

func (s *Server) getPendingConfigChanges(w http.ResponseWriter, r *http.Request) {
	selected := s.selected(w)
	if selected == nil {
		return
	}
	pending, err := selected.Store.LoadPendingConfigChanges(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "load_pending_config_failed")
		return
	}
	writeJSON(w, http.StatusOK, pending)
}

func (s *Server) postApplyConfigChanges(w http.ResponseWriter, r *http.Request) {
	if s.rejectTask(w) {
		return
	}
	var body struct {
		Fields []string `json:"fields"`
	}
	if json.NewDecoder(r.Body).Decode(&body) != nil || len(body.Fields) == 0 {
		writeError(w, http.StatusBadRequest, "invalid_json")
		return
	}
	selected := s.selected(w)
	if selected == nil {
		return
	}
	pending, err := selected.Store.LoadPendingConfigChanges(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "load_pending_config_failed")
		return
	}
	if len(pending.Changes) == 0 {
		writeError(w, http.StatusBadRequest, "no_pending_config_changes")
		return
	}
	apply := make(map[string]bool, len(body.Fields))
	for _, field := range body.Fields {
		apply[field] = true
	}
	if err := s.session.WithProject(r.Context(), func(value *project.Project) error {
		if value.Config == nil {
			return errors.New("missing config")
		}
		for _, change := range pending.Changes {
			if apply[change.Field] {
				setStoryConfigField(&value.Config.Story, change.Field, change.Proposed)
			}
		}
		if value.Progress != nil {
			if value.Config.Story.Title != "" {
				value.Progress.Title = value.Config.Story.Title
			}
			if value.Config.Story.StorySynopsis != "" {
				value.Progress.StorySynopsis = value.Config.Story.StorySynopsis
			}
			snapshot := value.Config.Story
			value.Progress.StoryConfigSnapshot = &snapshot
		}
		return nil
	}); err != nil {
		writeError(w, http.StatusInternalServerError, "save_config_failed")
		return
	}
	kept := pending.Changes[:0]
	for _, change := range pending.Changes {
		if !apply[change.Field] {
			kept = append(kept, change)
		}
	}
	pending.Changes = kept
	if err := selected.Store.SavePendingConfigChanges(r.Context(), pending); err != nil {
		writeError(w, http.StatusInternalServerError, "save_pending_config_failed")
		return
	}
	writeJSON(w, http.StatusOK, s.session.Snapshot().Project.Config)
}

func (s *Server) deletePendingConfigChanges(w http.ResponseWriter, r *http.Request) {
	if s.rejectTask(w) {
		return
	}
	selected := s.selected(w)
	if selected == nil {
		return
	}
	if err := selected.Store.SavePendingConfigChanges(r.Context(), &project.PendingConfigChanges{}); err != nil {
		writeError(w, http.StatusInternalServerError, "delete_pending_config_failed")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

func setStoryConfigField(story *project.StoryConfig, field, value string) {
	switch field {
	case "type":
		story.Type = value
	case "title":
		story.Title = value
	case "writing_style":
		story.WritingStyle = value
	case "writing_pov":
		story.WritingPOV = value
	case "story_synopsis":
		story.StorySynopsis = value
	}
}

func (s *Server) getProgress(w http.ResponseWriter, r *http.Request) {
	if selected := s.selected(w); selected != nil {
		writeJSON(w, http.StatusOK, selected.Project.Progress)
	}
}

func (s *Server) deleteProgress(w http.ResponseWriter, r *http.Request) {
	if s.rejectTask(w) {
		return
	}
	var deleted []int
	if !s.updateProgress(w, r, func(progress *project.Progress) error {
		for _, chapter := range progress.Chapters {
			deleted = append(deleted, chapter.Num)
		}
		*progress = project.Progress{Phase: "outline"}
		return nil
	}) {
		return
	}
	if !s.deleteChapterMarkdowns(w, r, deleted) {
		return
	}
	writeJSON(w, http.StatusOK, s.session.Snapshot().Project.Progress)
}

func (s *Server) deleteOutline(w http.ResponseWriter, r *http.Request) {
	if s.rejectTask(w) {
		return
	}
	var deleted []int
	err := s.session.WithProgress(r.Context(), func(progress *project.Progress) error {
		for _, chapter := range progress.Chapters {
			if chapter.Status == project.StatusWriting || chapter.Status == project.StatusReview {
				return errOutlineDeleteWithDraft
			}
			deleted = append(deleted, chapter.Num)
		}
		progress.Title = ""
		progress.CorePrompt = ""
		progress.StorySynopsis = ""
		progress.Chapters = nil
		progress.StoryConfigSnapshot = nil
		progress.CurrentChapterIndex = 0
		return nil
	})
	if errors.Is(err, errOutlineDeleteWithDraft) {
		writeError(w, http.StatusConflict, "writing_chapter_present_delete")
		return
	}
	if err != nil {
		if errors.Is(err, runtime.ErrNoProject) {
			writeError(w, http.StatusBadRequest, "select_project_first")
		} else {
			writeError(w, http.StatusInternalServerError, "save_progress_failed")
		}
		return
	}
	if !s.deleteChapterMarkdowns(w, r, deleted) {
		return
	}
	writeJSON(w, http.StatusOK, s.session.Snapshot().Project.Progress)
}

func (s *Server) getAutoConfirm(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]bool{"enabled": s.autoConfirmEnabled()})
}

func (s *Server) putAutoConfirm(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Enabled bool `json:"enabled"`
	}
	if json.NewDecoder(r.Body).Decode(&body) != nil {
		writeError(w, http.StatusBadRequest, "invalid_json")
		return
	}
	s.autoConfirmMu.Lock()
	s.autoConfirm = body.Enabled
	s.autoConfirmMu.Unlock()
	writeJSON(w, http.StatusOK, map[string]bool{"enabled": body.Enabled})
}

func chapterNumber(w http.ResponseWriter, r *http.Request) (int, bool) {
	num, err := strconv.Atoi(r.PathValue("num"))
	if err != nil || num <= 0 {
		writeError(w, http.StatusBadRequest, "invalid_chapter_num")
		return 0, false
	}
	return num, true
}

var (
	errInvalidProgressUpdate       = errors.New("invalid progress update")
	errChapterEditFailed           = errors.New("chapter edit failed")
	errNoChaptersToDelete          = errors.New("no chapters to delete")
	errWritingChapterCannotDelete  = errors.New("writing chapter cannot delete")
	errDeleteFrontierUnavailable   = errors.New("delete frontier unavailable")
	errChapterNumberNotFound       = errors.New("chapter number not found")
	errWritingChapterInDeleteRange = errors.New("writing chapter in delete range")
	errOutlineDeleteWithDraft      = errors.New("outline has writing chapter")
)

func (s *Server) updateProgress(w http.ResponseWriter, r *http.Request, update func(*project.Progress) error) bool {
	if s.session == nil {
		writeError(w, http.StatusBadRequest, "select_project_first")
		return false
	}
	if err := s.session.WithProgress(r.Context(), update); err != nil {
		if errors.Is(err, runtime.ErrNoProject) {
			writeError(w, http.StatusBadRequest, "select_project_first")
		} else if errors.Is(err, errInvalidProgressUpdate) {
			writeError(w, http.StatusBadRequest, "invalid_json")
		} else {
			writeError(w, http.StatusInternalServerError, "save_progress_failed")
		}
		return false
	}
	return true
}

func (s *Server) putChapterOutline(w http.ResponseWriter, r *http.Request) {
	num, ok := chapterNumber(w, r)
	if !ok {
		return
	}
	var body struct {
		Title   string `json:"title"`
		Outline string `json:"outline"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_json")
		return
	}
	if !s.updateProgress(w, r, func(progress *project.Progress) error {
		for i := range progress.Chapters {
			if progress.Chapters[i].Num != num {
				continue
			}
			if progress.Chapters[i].Status != project.StatusPending || progress.Chapters[i].OutlineLocked {
				return errInvalidProgressUpdate
			}
			progress.Chapters[i].Title, progress.Chapters[i].Outline = body.Title, body.Outline
			return nil
		}
		return errInvalidProgressUpdate
	}) {
		return
	}
	writeJSON(w, http.StatusOK, s.session.Snapshot().Project.Progress)
}

func (s *Server) putChapterOutlineLock(w http.ResponseWriter, r *http.Request) {
	num, ok := chapterNumber(w, r)
	if !ok {
		return
	}
	var body struct {
		Locked bool `json:"locked"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_json")
		return
	}
	if !s.updateProgress(w, r, func(progress *project.Progress) error {
		for i := range progress.Chapters {
			if progress.Chapters[i].Num == num {
				progress.Chapters[i].OutlineLocked = body.Locked
				return nil
			}
		}
		return errInvalidProgressUpdate
	}) {
		return
	}
	writeJSON(w, http.StatusOK, s.session.Snapshot().Project.Progress)
}

func (s *Server) putChapterParagraphLocks(w http.ResponseWriter, r *http.Request) {
	num, ok := chapterNumber(w, r)
	if !ok {
		return
	}
	var body struct {
		Locks []int `json:"locks"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_json")
		return
	}
	if !s.updateProgress(w, r, func(progress *project.Progress) error {
		for i := range progress.Chapters {
			if progress.Chapters[i].Num != num {
				continue
			}
			locks := make(map[int]bool, len(body.Locks))
			for _, lock := range body.Locks {
				if lock > 0 {
					locks[lock] = true
				}
			}
			paragraphs := 0
			for _, paragraph := range strings.Split(strings.TrimSpace(progress.Chapters[i].Content), "\n\n") {
				if strings.TrimSpace(paragraph) != "" {
					paragraphs++
				}
			}
			progress.Chapters[i].ParagraphLocks = progress.Chapters[i].ParagraphLocks[:0]
			for lock := 1; lock <= paragraphs; lock++ {
				if locks[lock] {
					progress.Chapters[i].ParagraphLocks = append(progress.Chapters[i].ParagraphLocks, lock)
				}
			}
			return nil
		}
		return errInvalidProgressUpdate
	}) {
		return
	}
	writeJSON(w, http.StatusOK, s.session.Snapshot().Project.Progress)
}

type chapterEditRequest struct {
	ChapterNum     int    `json:"num"`
	Operation      string `json:"operation"`
	StartLine      int    `json:"start_line,omitempty"`
	EndLine        int    `json:"end_line,omitempty"`
	OldText        string `json:"old_text,omitempty"`
	Line           int    `json:"line,omitempty"`
	StartParagraph int    `json:"start_paragraph,omitempty"`
	EndParagraph   int    `json:"end_paragraph,omitempty"`
	Paragraph      int    `json:"paragraph,omitempty"`
	NewText        string `json:"new_text"`
}

func (s *Server) postChapterEdit(w http.ResponseWriter, r *http.Request) {
	var request chapterEditRequest
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_json")
		return
	}
	if request.Operation == "" {
		writeError(w, http.StatusBadRequest, "chapter_edit_op_required")
		return
	}
	if request.NewText == "" && request.Operation != "replace_text" && request.Operation != "delete_lines" && request.Operation != "delete_paragraphs" {
		writeError(w, http.StatusBadRequest, "chapter_edit_text_required")
		return
	}
	var chapter project.Chapter
	var totalLines int
	if s.session == nil {
		writeError(w, http.StatusBadRequest, "select_project_first")
		return
	}
	if err := s.session.WithProgress(r.Context(), func(progress *project.Progress) error {
		for i := range progress.Chapters {
			if progress.Chapters[i].Num != request.ChapterNum {
				continue
			}
			if err := applyChapterEdit(&progress.Chapters[i], request); err != nil {
				return errChapterEditFailed
			}
			chapter = progress.Chapters[i]
			totalLines = len(strings.Split(chapter.Content, "\n"))
			return nil
		}
		return errChapterEditFailed
	}); err != nil {
		if errors.Is(err, runtime.ErrNoProject) {
			writeError(w, http.StatusBadRequest, "select_project_first")
		} else if errors.Is(err, errChapterEditFailed) {
			writeError(w, http.StatusBadRequest, "chapter_edit_failed")
		} else {
			writeError(w, http.StatusInternalServerError, "save_progress_failed")
		}
		return
	}
	snapshot := s.session.Snapshot()
	if snapshot == nil {
		writeError(w, http.StatusBadRequest, "select_project_first")
		return
	}
	markdown := fmt.Sprintf("# 第 %d 章: %s\n\n> **本章摘要**：%s\n\n---\n\n%s", chapter.Num, chapter.Title, chapter.Summary, chapter.Content)
	if err := snapshot.Store.SaveChapterMarkdown(r.Context(), chapter.Num, []byte(markdown)); err != nil {
		writeError(w, http.StatusInternalServerError, "save_progress_failed")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "total_lines": totalLines, "chapter": chapter})
}

func applyChapterEdit(chapter *project.Chapter, request chapterEditRequest) error {
	if chapter.Content == "" {
		return errInvalidProgressUpdate
	}
	lines := strings.Split(chapter.Content, "\n")
	paragraphs := chapterParagraphs(chapter.Content)
	switch request.Operation {
	case "replace_all":
		if strings.TrimSpace(request.NewText) == "" {
			return errInvalidProgressUpdate
		}
		chapter.Content = strings.TrimSpace(request.NewText)
		chapter.ParagraphLocks = normalizedParagraphLocks(chapter.Content, chapter.ParagraphLocks)
	case "replace_text":
		if request.OldText == "" {
			return errInvalidProgressUpdate
		}
		index := strings.Index(chapter.Content, request.OldText)
		if index < 0 {
			return errInvalidProgressUpdate
		}
		chapter.Content = chapter.Content[:index] + request.NewText + chapter.Content[index+len(request.OldText):]
	case "append":
		if !strings.HasSuffix(chapter.Content, "\n") {
			chapter.Content += "\n"
		}
		chapter.Content += request.NewText
	case "replace_lines", "delete_lines":
		if request.StartLine < 1 || request.EndLine < request.StartLine || request.EndLine > len(lines) {
			return errInvalidProgressUpdate
		}
		replacement := []string{}
		if request.Operation == "replace_lines" {
			replacement = strings.Split(request.NewText, "\n")
		}
		chapter.Content = strings.Join(append(append(lines[:request.StartLine-1], replacement...), lines[request.EndLine:]...), "\n")
	case "insert_after_line":
		if request.Line < 0 || request.Line > len(lines) {
			return errInvalidProgressUpdate
		}
		chapter.Content = strings.Join(append(append(lines[:request.Line], strings.Split(request.NewText, "\n")...), lines[request.Line:]...), "\n")
	case "replace_paragraphs", "delete_paragraphs":
		if request.StartParagraph < 1 || request.EndParagraph < request.StartParagraph || request.EndParagraph > len(paragraphs) || containsLockedParagraph(chapter.ParagraphLocks, request.StartParagraph, request.EndParagraph) {
			return errInvalidProgressUpdate
		}
		replacement := []string{}
		if request.Operation == "replace_paragraphs" {
			replacement = chapterParagraphs(request.NewText)
		}
		chapter.Content = strings.Join(append(append(paragraphs[:request.StartParagraph-1], replacement...), paragraphs[request.EndParagraph:]...), "\n\n")
	case "insert_after_paragraph":
		if request.Paragraph < 0 || request.Paragraph > len(paragraphs) {
			return errInvalidProgressUpdate
		}
		chapter.Content = strings.Join(append(append(paragraphs[:request.Paragraph], chapterParagraphs(request.NewText)...), paragraphs[request.Paragraph:]...), "\n\n")
	default:
		return errInvalidProgressUpdate
	}
	return nil
}
func chapterParagraphs(content string) []string {
	var result []string
	for _, item := range strings.Split(strings.TrimSpace(content), "\n\n") {
		if item = strings.TrimSpace(item); item != "" {
			result = append(result, item)
		}
	}
	return result
}
func normalizedParagraphLocks(content string, locks []int) []int {
	valid := map[int]bool{}
	for _, lock := range locks {
		valid[lock] = true
	}
	result := []int{}
	for n := 1; n <= len(chapterParagraphs(content)); n++ {
		if valid[n] {
			result = append(result, n)
		}
	}
	return result
}
func containsLockedParagraph(locks []int, start, end int) bool {
	for _, lock := range locks {
		if lock >= start && lock <= end {
			return true
		}
	}
	return false
}

func (s *Server) postChapterConfirm(w http.ResponseWriter, r *http.Request) {
	if !s.updateProgress(w, r, func(progress *project.Progress) error {
		if progress.Phase != "writing" || progress.CurrentChapterIndex < 0 || progress.CurrentChapterIndex >= len(progress.Chapters) || progress.Chapters[progress.CurrentChapterIndex].Status != project.StatusReview {
			return errInvalidProgressUpdate
		}
		progress.Chapters[progress.CurrentChapterIndex].Status = project.StatusAccepted
		progress.CurrentChapterIndex++
		return nil
	}) {
		return
	}
	writeJSON(w, http.StatusOK, s.session.Snapshot().Project.Progress)
}
func (s *Server) postChapterReject(w http.ResponseWriter, r *http.Request) {
	var rejected project.Chapter
	if !s.updateProgress(w, r, func(progress *project.Progress) error {
		if progress.Phase != "writing" || progress.CurrentChapterIndex < 0 || progress.CurrentChapterIndex >= len(progress.Chapters) || progress.Chapters[progress.CurrentChapterIndex].Status != project.StatusReview {
			return errInvalidProgressUpdate
		}
		chapter := &progress.Chapters[progress.CurrentChapterIndex]
		chapter.Content, chapter.Summary, chapter.Status = "", "", project.StatusPending
		rejected = *chapter
		memory := progress.MemoryEntries[:0]
		for _, entry := range progress.MemoryEntries {
			if entry.Chapter != rejected.Num {
				memory = append(memory, entry)
			}
		}
		progress.MemoryEntries = memory
		return nil
	}) {
		return
	}
	snapshot := s.session.Snapshot()
	if snapshot == nil {
		writeError(w, http.StatusBadRequest, "select_project_first")
		return
	}
	if err := snapshot.Store.DeleteChapterMarkdown(r.Context(), rejected.Num); err != nil {
		writeError(w, http.StatusInternalServerError, "save_progress_failed")
		return
	}
	writeJSON(w, http.StatusOK, snapshot.Project.Progress)
}

func (s *Server) deleteChapter(w http.ResponseWriter, r *http.Request) {
	var deletedNum int
	err := s.withProgressForDelete(r, func(progress *project.Progress) error {
		index, err := deleteFrontierChapterIndex(progress)
		if err != nil {
			return err
		}
		deletedNum = progress.Chapters[index].Num
		clearChapter(progress, index, true)
		if index < progress.CurrentChapterIndex || progress.CurrentChapterIndex >= len(progress.Chapters) {
			progress.CurrentChapterIndex = index
		}
		return nil
	})
	if err != nil {
		s.writeChapterDeleteError(w, err)
		return
	}
	selected := s.session.Snapshot()
	if selected == nil {
		writeError(w, http.StatusBadRequest, "select_project_first")
		return
	}
	if err := selected.Store.DeleteChapterMarkdown(r.Context(), deletedNum); err != nil {
		writeError(w, http.StatusInternalServerError, "save_progress_failed")
		return
	}
	writeJSON(w, http.StatusOK, selected.Project.Progress)
}

func (s *Server) deleteChaptersFrom(w http.ResponseWriter, r *http.Request) {
	num, ok := chapterNumber(w, r)
	if !ok {
		return
	}
	var deleted []int
	err := s.withProgressForDelete(r, func(progress *project.Progress) error {
		start := -1
		for i := range progress.Chapters {
			if progress.Chapters[i].Num == num {
				start = i
				break
			}
		}
		if start == -1 {
			return errChapterNumberNotFound
		}
		for i := start; i < len(progress.Chapters); i++ {
			if progress.Chapters[i].Status == project.StatusWriting {
				return errWritingChapterInDeleteRange
			}
		}
		for i := start; i < len(progress.Chapters); i++ {
			deleted = append(deleted, progress.Chapters[i].Num)
			clearChapter(progress, i, false)
		}
		if progress.CurrentChapterIndex >= start {
			progress.CurrentChapterIndex = start
		}
		return nil
	})
	if err != nil {
		s.writeChapterDeleteError(w, err)
		return
	}
	selected := s.session.Snapshot()
	if selected == nil {
		writeError(w, http.StatusBadRequest, "select_project_first")
		return
	}
	for _, chapterNum := range deleted {
		if err := selected.Store.DeleteChapterMarkdown(r.Context(), chapterNum); err != nil {
			writeError(w, http.StatusInternalServerError, "save_progress_failed")
			return
		}
	}
	writeJSON(w, http.StatusOK, selected.Project.Progress)
}

func (s *Server) withProgressForDelete(r *http.Request, update func(*project.Progress) error) error {
	if s.session == nil {
		return runtime.ErrNoProject
	}
	return s.session.WithProgress(r.Context(), update)
}

func (s *Server) deleteChapterMarkdowns(w http.ResponseWriter, r *http.Request, chapters []int) bool {
	snapshot := s.session.Snapshot()
	if snapshot == nil {
		writeError(w, http.StatusBadRequest, "select_project_first")
		return false
	}
	for _, chapterNum := range chapters {
		if err := snapshot.Store.DeleteChapterMarkdown(r.Context(), chapterNum); err != nil {
			writeError(w, http.StatusInternalServerError, "save_progress_failed")
			return false
		}
	}
	return true
}

func (s *Server) writeChapterDeleteError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, runtime.ErrNoProject):
		writeError(w, http.StatusBadRequest, "select_project_first")
	case errors.Is(err, errNoChaptersToDelete):
		writeError(w, http.StatusBadRequest, "no_chapters_to_delete")
	case errors.Is(err, errWritingChapterCannotDelete):
		writeError(w, http.StatusConflict, "writing_chapter_cannot_delete")
	case errors.Is(err, errDeleteFrontierUnavailable):
		writeError(w, http.StatusConflict, "delete_frontier_unavailable")
	case errors.Is(err, errChapterNumberNotFound):
		writeError(w, http.StatusNotFound, "chapter_n_not_found")
	case errors.Is(err, errWritingChapterInDeleteRange):
		writeError(w, http.StatusConflict, "writing_range_has_writing")
	default:
		writeError(w, http.StatusInternalServerError, "save_progress_failed")
	}
}

func deleteFrontierChapterIndex(progress *project.Progress) (int, error) {
	if len(progress.Chapters) == 0 {
		return -1, errNoChaptersToDelete
	}
	frontier := progress.CurrentChapterIndex
	if frontier >= len(progress.Chapters) {
		last := len(progress.Chapters) - 1
		if progress.Chapters[last].Status == project.StatusWriting {
			return -1, errWritingChapterCannotDelete
		}
		if chapterHasDeletableContent(progress.Chapters[last]) {
			return last, nil
		}
		return -1, errDeleteFrontierUnavailable
	}
	current := progress.Chapters[frontier]
	switch current.Status {
	case project.StatusWriting:
		return -1, errWritingChapterCannotDelete
	case project.StatusReview:
		return frontier, nil
	case project.StatusPending:
		if frontier > 0 && progress.Chapters[frontier-1].Status == project.StatusAccepted && chapterHasDeletableContent(progress.Chapters[frontier-1]) {
			return frontier - 1, nil
		}
	}
	return -1, errDeleteFrontierUnavailable
}

func chapterHasDeletableContent(chapter project.Chapter) bool {
	return chapter.Status == project.StatusAccepted || chapter.Status == project.StatusReview || chapter.Content != "" || chapter.Summary != ""
}

func clearChapter(progress *project.Progress, index int, purgeMemory bool) {
	chapter := &progress.Chapters[index]
	chapter.Content, chapter.Summary, chapter.Status = "", "", project.StatusPending
	if !purgeMemory {
		return
	}
	memory := progress.MemoryEntries[:0]
	for _, entry := range progress.MemoryEntries {
		if entry.Chapter != chapter.Num {
			memory = append(memory, entry)
		}
	}
	progress.MemoryEntries = memory
}

func (s *Server) getForeshadows(w http.ResponseWriter, r *http.Request) {
	selected := s.selected(w)
	if selected == nil {
		return
	}
	if selected.Project.Progress == nil || selected.Project.Progress.Foreshadows == nil {
		writeJSON(w, http.StatusOK, []project.Foreshadow{})
		return
	}
	writeJSON(w, http.StatusOK, selected.Project.Progress.Foreshadows)
}

func (s *Server) getForeshadowsRoadmap(w http.ResponseWriter, r *http.Request) {
	selected := s.selected(w)
	if selected == nil {
		return
	}
	progress := selected.Project.Progress
	if progress == nil {
		progress = &project.Progress{}
	}
	writeJSON(w, http.StatusOK, map[string]string{"markdown": foreshadow.RoadmapMarkdown(progress), "path": selected.Store.ForeshadowRoadmapPath()})
}

func (s *Server) postForeshadowsConfirm(w http.ResponseWriter, r *http.Request) {
	if s.rejectTask(w) {
		return
	}
	var body struct {
		Foreshadows []project.Foreshadow `json:"foreshadows"`
	}
	if json.NewDecoder(r.Body).Decode(&body) != nil {
		writeError(w, http.StatusBadRequest, "invalid_json")
		return
	}
	var created []project.Foreshadow
	if !s.updateProgress(w, r, func(progress *project.Progress) error {
		next := nextForeshadowID(progress.Foreshadows)
		created = make([]project.Foreshadow, len(body.Foreshadows))
		for i, item := range body.Foreshadows {
			item.ID = next + i
			item.Status = project.ForeshadowPlanted
			if item.Events == nil {
				item.Events = []project.ForeshadowEvent{}
			}
			created[i] = item
		}
		progress.Foreshadows = append(progress.Foreshadows, created...)
		return nil
	}) {
		return
	}
	progress := s.session.Snapshot().Project.Progress
	writeJSON(w, http.StatusOK, progress.Foreshadows)
	if s.workflows.foreshadow != nil {
		_ = s.workflows.foreshadow.StartOutlineCheck()
	}
}

func (s *Server) postForeshadow(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Name          string `json:"name"`
		Description   string `json:"description"`
		PlantChapter  int    `json:"plant_chapter"`
		TargetChapter int    `json:"target_chapter"`
	}
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_json")
		return
	}
	if request.Name == "" {
		writeError(w, http.StatusBadRequest, "foreshadow_name_required")
		return
	}
	if request.Description == "" {
		writeError(w, http.StatusBadRequest, "foreshadow_desc_required")
		return
	}
	foreshadow := project.Foreshadow{
		Name: request.Name, Description: request.Description,
		PlantChapter: request.PlantChapter, TargetChapter: request.TargetChapter,
		Status: project.ForeshadowPlanted, Events: []project.ForeshadowEvent{},
	}
	if err := s.updateForeshadows(r, func(progress *project.Progress) error {
		foreshadow.ID = nextForeshadowID(progress.Foreshadows)
		progress.Foreshadows = append(progress.Foreshadows, foreshadow)
		return nil
	}); err != nil {
		s.writeForeshadowError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, foreshadow)
}

func (s *Server) putForeshadow(w http.ResponseWriter, r *http.Request) {
	id, ok := foreshadowID(w, r)
	if !ok {
		return
	}
	var request struct {
		Name          string                   `json:"name"`
		Description   string                   `json:"description"`
		PlantChapter  int                      `json:"plant_chapter"`
		TargetChapter int                      `json:"target_chapter"`
		Status        project.ForeshadowStatus `json:"status"`
		Resolution    string                   `json:"resolution"`
	}
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_json")
		return
	}
	var result project.Foreshadow
	if err := s.updateForeshadows(r, func(progress *project.Progress) error {
		for i := range progress.Foreshadows {
			foreshadow := &progress.Foreshadows[i]
			if foreshadow.ID != id {
				continue
			}
			if request.Name != "" {
				foreshadow.Name = request.Name
			}
			if request.Description != "" {
				foreshadow.Description = request.Description
			}
			if request.PlantChapter > 0 {
				foreshadow.PlantChapter = request.PlantChapter
			}
			if request.TargetChapter > 0 {
				foreshadow.TargetChapter = request.TargetChapter
			}
			if request.Status != "" {
				foreshadow.Status = request.Status
			}
			if request.Resolution != "" {
				foreshadow.Resolution = request.Resolution
			}
			result = *foreshadow
			return nil
		}
		return errForeshadowNotFound
	}); err != nil {
		s.writeForeshadowError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) deleteForeshadow(w http.ResponseWriter, r *http.Request) {
	id, ok := foreshadowID(w, r)
	if !ok {
		return
	}
	if err := s.updateForeshadows(r, func(progress *project.Progress) error {
		for i := range progress.Foreshadows {
			if progress.Foreshadows[i].ID == id {
				progress.Foreshadows = append(progress.Foreshadows[:i], progress.Foreshadows[i+1:]...)
				return nil
			}
		}
		return errForeshadowNotFound
	}); err != nil {
		s.writeForeshadowError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

var errForeshadowNotFound = errors.New("foreshadow not found")

func foreshadowID(w http.ResponseWriter, r *http.Request) (int, bool) {
	id, err := strconv.Atoi(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_foreshadow_id")
		return 0, false
	}
	return id, true
}

func nextForeshadowID(values []project.Foreshadow) int {
	max := 0
	for _, value := range values {
		if value.ID > max {
			max = value.ID
		}
	}
	return max + 1
}

func (s *Server) updateForeshadows(r *http.Request, update func(*project.Progress) error) error {
	if s.session == nil {
		return runtime.ErrNoProject
	}
	return s.session.WithProgress(r.Context(), update)
}

func (s *Server) writeForeshadowError(w http.ResponseWriter, err error) {
	if errors.Is(err, runtime.ErrNoProject) {
		writeError(w, http.StatusBadRequest, "select_project_first")
	} else if errors.Is(err, errForeshadowNotFound) {
		writeError(w, http.StatusNotFound, "foreshadow_not_found")
	} else {
		writeError(w, http.StatusInternalServerError, "save_failed")
	}
}

func (s *Server) getSettings(w http.ResponseWriter, r *http.Request) {
	if selected := s.selected(w); selected != nil {
		writeJSON(w, http.StatusOK, selected.Project.Settings)
	}
}

var errSettingsNotFound = errors.New("settings entry not found")

func (s *Server) updateSettings(ctx *http.Request, update func(*project.ProjectSettings) error) error {
	if s.session == nil {
		return runtime.ErrNoProject
	}
	return s.session.WithProject(ctx.Context(), func(value *project.Project) error {
		if value.Settings == nil {
			value.Settings = &project.ProjectSettings{}
		}
		return update(value.Settings)
	})
}

func requireSettingsID(w http.ResponseWriter, r *http.Request, notFound string) bool {
	id := r.PathValue("id")
	if id == "" || id == "." || id == ".." || strings.ContainsAny(id, `/\\`) {
		writeError(w, http.StatusNotFound, notFound)
		return false
	}
	return true
}

func (s *Server) writeSettingsError(w http.ResponseWriter, err error, notFound string) {
	if errors.Is(err, runtime.ErrNoProject) {
		writeError(w, http.StatusBadRequest, "select_project_first")
	} else if errors.Is(err, errSettingsNotFound) {
		writeError(w, http.StatusNotFound, notFound)
	} else {
		writeError(w, http.StatusInternalServerError, "save_failed")
	}
}

func decodeSettingsBody[T any](w http.ResponseWriter, r *http.Request, value *T) bool {
	if err := json.NewDecoder(r.Body).Decode(value); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_json")
		return false
	}
	return true
}

func (s *Server) postCharacter(w http.ResponseWriter, r *http.Request) {
	var entry project.Character
	if !decodeSettingsBody(w, r, &entry) {
		return
	}
	if entry.Name == "" {
		writeError(w, http.StatusBadRequest, "character_name_empty")
		return
	}
	err := s.updateSettings(r, func(settings *project.ProjectSettings) error {
		entry.ID = nextSettingsID(settings, "c")
		settings.Characters = append(settings.Characters, entry)
		return nil
	})
	if err != nil {
		s.writeSettingsError(w, err, "character_not_found")
		return
	}
	writeJSON(w, http.StatusOK, entry)
}
func (s *Server) putCharacter(w http.ResponseWriter, r *http.Request) {
	if !requireSettingsID(w, r, "character_not_found") {
		return
	}
	var update project.Character
	if !decodeSettingsBody(w, r, &update) {
		return
	}
	var result project.Character
	err := s.updateSettings(r, func(settings *project.ProjectSettings) error {
		for i := range settings.Characters {
			if settings.Characters[i].ID == r.PathValue("id") {
				mergeCharacter(&settings.Characters[i], update)
				result = settings.Characters[i]
				return nil
			}
		}
		return errSettingsNotFound
	})
	if err != nil {
		s.writeSettingsError(w, err, "character_not_found")
		return
	}
	writeJSON(w, http.StatusOK, result)
}
func (s *Server) deleteCharacter(w http.ResponseWriter, r *http.Request) {
	s.deleteSettingsEntry(w, r, "character_not_found", func(settings *project.ProjectSettings, id string) bool {
		for i := range settings.Characters {
			if settings.Characters[i].ID == id {
				settings.Characters = append(settings.Characters[:i], settings.Characters[i+1:]...)
				return true
			}
		}
		return false
	})
}

func (s *Server) postWorldview(w http.ResponseWriter, r *http.Request) {
	var entry project.WorldviewEntry
	if !decodeSettingsBody(w, r, &entry) {
		return
	}
	if entry.Name == "" || entry.Description == "" {
		writeError(w, http.StatusBadRequest, "worldview_field_empty")
		return
	}
	err := s.updateSettings(r, func(settings *project.ProjectSettings) error {
		entry.ID = nextSettingsID(settings, "w")
		settings.Worldview = append(settings.Worldview, entry)
		return nil
	})
	if err != nil {
		s.writeSettingsError(w, err, "worldview_not_found")
		return
	}
	writeJSON(w, http.StatusOK, entry)
}
func (s *Server) putWorldview(w http.ResponseWriter, r *http.Request) {
	if !requireSettingsID(w, r, "worldview_not_found") {
		return
	}
	var update project.WorldviewEntry
	if !decodeSettingsBody(w, r, &update) {
		return
	}
	var result project.WorldviewEntry
	err := s.updateSettings(r, func(settings *project.ProjectSettings) error {
		for i := range settings.Worldview {
			if settings.Worldview[i].ID == r.PathValue("id") {
				mergeWorldview(&settings.Worldview[i], update)
				result = settings.Worldview[i]
				return nil
			}
		}
		return errSettingsNotFound
	})
	if err != nil {
		s.writeSettingsError(w, err, "worldview_not_found")
		return
	}
	writeJSON(w, http.StatusOK, result)
}
func (s *Server) deleteWorldview(w http.ResponseWriter, r *http.Request) {
	s.deleteSettingsEntry(w, r, "worldview_not_found", func(settings *project.ProjectSettings, id string) bool {
		for i := range settings.Worldview {
			if settings.Worldview[i].ID == id {
				settings.Worldview = append(settings.Worldview[:i], settings.Worldview[i+1:]...)
				return true
			}
		}
		return false
	})
}

func (s *Server) postOrganization(w http.ResponseWriter, r *http.Request) {
	var entry project.Organization
	if !decodeSettingsBody(w, r, &entry) {
		return
	}
	if entry.Name == "" {
		writeError(w, http.StatusBadRequest, "organization_name_empty")
		return
	}
	err := s.updateSettings(r, func(settings *project.ProjectSettings) error {
		entry.ID = nextSettingsID(settings, "o")
		settings.Organizations = append(settings.Organizations, entry)
		return nil
	})
	if err != nil {
		s.writeSettingsError(w, err, "organization_not_found")
		return
	}
	writeJSON(w, http.StatusOK, entry)
}
func (s *Server) putOrganization(w http.ResponseWriter, r *http.Request) {
	if !requireSettingsID(w, r, "organization_not_found") {
		return
	}
	var update project.Organization
	if !decodeSettingsBody(w, r, &update) {
		return
	}
	var result project.Organization
	err := s.updateSettings(r, func(settings *project.ProjectSettings) error {
		for i := range settings.Organizations {
			if settings.Organizations[i].ID == r.PathValue("id") {
				mergeOrganization(&settings.Organizations[i], update)
				result = settings.Organizations[i]
				return nil
			}
		}
		return errSettingsNotFound
	})
	if err != nil {
		s.writeSettingsError(w, err, "organization_not_found")
		return
	}
	writeJSON(w, http.StatusOK, result)
}
func (s *Server) deleteOrganization(w http.ResponseWriter, r *http.Request) {
	s.deleteSettingsEntry(w, r, "organization_not_found", func(settings *project.ProjectSettings, id string) bool {
		for i := range settings.Organizations {
			if settings.Organizations[i].ID == id {
				settings.Organizations = append(settings.Organizations[:i], settings.Organizations[i+1:]...)
				return true
			}
		}
		return false
	})
}

func (s *Server) postRelation(w http.ResponseWriter, r *http.Request) {
	var entry project.Relation
	if !decodeSettingsBody(w, r, &entry) {
		return
	}
	if entry.SourceID == "" || entry.TargetID == "" {
		writeError(w, http.StatusBadRequest, "relation_endpoints_empty")
		return
	}
	err := s.updateSettings(r, func(settings *project.ProjectSettings) error {
		entry.ID = nextSettingsID(settings, "r")
		settings.Relations = append(settings.Relations, entry)
		return nil
	})
	if err != nil {
		s.writeSettingsError(w, err, "relation_not_found")
		return
	}
	writeJSON(w, http.StatusOK, entry)
}
func (s *Server) putRelation(w http.ResponseWriter, r *http.Request) {
	if !requireSettingsID(w, r, "relation_not_found") {
		return
	}
	var update project.Relation
	if !decodeSettingsBody(w, r, &update) {
		return
	}
	var result project.Relation
	err := s.updateSettings(r, func(settings *project.ProjectSettings) error {
		for i := range settings.Relations {
			if settings.Relations[i].ID == r.PathValue("id") {
				mergeRelation(&settings.Relations[i], update)
				result = settings.Relations[i]
				return nil
			}
		}
		return errSettingsNotFound
	})
	if err != nil {
		s.writeSettingsError(w, err, "relation_not_found")
		return
	}
	writeJSON(w, http.StatusOK, result)
}
func (s *Server) deleteRelation(w http.ResponseWriter, r *http.Request) {
	s.deleteSettingsEntry(w, r, "relation_not_found", func(settings *project.ProjectSettings, id string) bool {
		for i := range settings.Relations {
			if settings.Relations[i].ID == id {
				settings.Relations = append(settings.Relations[:i], settings.Relations[i+1:]...)
				return true
			}
		}
		return false
	})
}

func (s *Server) deleteSettingsEntry(w http.ResponseWriter, r *http.Request, notFound string, delete func(*project.ProjectSettings, string) bool) {
	if !requireSettingsID(w, r, notFound) {
		return
	}
	err := s.updateSettings(r, func(settings *project.ProjectSettings) error {
		if !delete(settings, r.PathValue("id")) {
			return errSettingsNotFound
		}
		return nil
	})
	if err != nil {
		s.writeSettingsError(w, err, notFound)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

func mergeCharacter(to *project.Character, from project.Character) {
	if from.Name != "" {
		to.Name = from.Name
	}
	if from.Age != "" {
		to.Age = from.Age
	}
	if from.Appearance != "" {
		to.Appearance = from.Appearance
	}
	if from.Personality != "" {
		to.Personality = from.Personality
	}
	if from.Background != "" {
		to.Background = from.Background
	}
	if from.Motivation != "" {
		to.Motivation = from.Motivation
	}
	if from.Abilities != "" {
		to.Abilities = from.Abilities
	}
	if from.Notes != "" {
		to.Notes = from.Notes
	}
}
func mergeWorldview(to *project.WorldviewEntry, from project.WorldviewEntry) {
	if from.Name != "" {
		to.Name = from.Name
	}
	if from.Category != "" {
		to.Category = from.Category
	}
	if from.Description != "" {
		to.Description = from.Description
	}
	if from.Tags != "" {
		to.Tags = from.Tags
	}
}
func mergeOrganization(to *project.Organization, from project.Organization) {
	if from.Name != "" {
		to.Name = from.Name
	}
	if from.Type != "" {
		to.Type = from.Type
	}
	if from.Description != "" {
		to.Description = from.Description
	}
	if from.Members != nil {
		to.Members = from.Members
	}
}
func mergeRelation(to *project.Relation, from project.Relation) {
	if from.SourceID != "" {
		to.SourceID = from.SourceID
	}
	if from.SourceType != "" {
		to.SourceType = from.SourceType
	}
	if from.TargetID != "" {
		to.TargetID = from.TargetID
	}
	if from.TargetType != "" {
		to.TargetType = from.TargetType
	}
	if from.Label != "" {
		to.Label = from.Label
	}
}

func nextSettingsID(settings *project.ProjectSettings, prefix string) string {
	max := 0
	for _, id := range append(append(append(idsOfCharacters(settings.Characters), idsOfWorldview(settings.Worldview)...), idsOfOrganizations(settings.Organizations)...), idsOfRelations(settings.Relations)...) {
		if value, err := strconv.Atoi(strings.TrimPrefix(id, prefix+"_")); err == nil && strings.HasPrefix(id, prefix+"_") && value > max {
			max = value
		}
	}
	return fmt.Sprintf("%s_%d", prefix, max+1)
}
func idsOfCharacters(values []project.Character) []string {
	ids := make([]string, len(values))
	for i := range values {
		ids[i] = values[i].ID
	}
	return ids
}
func idsOfWorldview(values []project.WorldviewEntry) []string {
	ids := make([]string, len(values))
	for i := range values {
		ids[i] = values[i].ID
	}
	return ids
}
func idsOfOrganizations(values []project.Organization) []string {
	ids := make([]string, len(values))
	for i := range values {
		ids[i] = values[i].ID
	}
	return ids
}
func idsOfRelations(values []project.Relation) []string {
	ids := make([]string, len(values))
	for i := range values {
		ids[i] = values[i].ID
	}
	return ids
}

func (s *Server) getSkills(w http.ResponseWriter, r *http.Request) {
	selected := s.selected(w)
	if selected == nil {
		return
	}
	if s.workflows.skills == nil {
		s.notImplemented(w, r)
		return
	}
	type view struct {
		Skill   project.Skill `json:"skill"`
		Enabled bool          `json:"enabled"`
	}
	skills := s.workflows.skills(selected.Project.Config, selected.Name)
	result := make([]view, 0, len(skills))
	for _, skill := range skills {
		enabled := selected.Project.Config != nil && selected.Project.Config.SkillConfig != nil && selected.Project.Config.SkillConfig.EnabledSkills[skill.ID]
		result = append(result, view{Skill: skill, Enabled: enabled})
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) putSkillToggle(w http.ResponseWriter, r *http.Request) {
	if s.rejectTask(w) {
		return
	}
	selected := s.selected(w)
	if selected == nil {
		return
	}
	if s.workflows.skills == nil {
		s.notImplemented(w, r)
		return
	}
	id := r.PathValue("id")
	var body struct {
		Enabled bool `json:"enabled"`
	}
	if json.NewDecoder(r.Body).Decode(&body) != nil {
		writeError(w, http.StatusBadRequest, "invalid_json")
		return
	}
	found := false
	for _, skill := range s.workflows.skills(selected.Project.Config, selected.Name) {
		if skill.ID == id {
			found = true
			break
		}
	}
	if !found {
		writeError(w, http.StatusNotFound, "skill_not_found")
		return
	}
	if err := s.session.WithProject(r.Context(), func(value *project.Project) error {
		if value.Config == nil {
			return errors.New("missing config")
		}
		if value.Config.SkillConfig == nil {
			value.Config.SkillConfig = &project.SkillConfig{}
		}
		if value.Config.SkillConfig.EnabledSkills == nil {
			value.Config.SkillConfig.EnabledSkills = map[string]bool{}
		}
		value.Config.SkillConfig.EnabledSkills[id] = body.Enabled
		return nil
	}); err != nil {
		writeError(w, http.StatusInternalServerError, "save_config_failed")
		return
	}
	if s.workflows.skillsChanged != nil {
		s.workflows.skillsChanged(r.Context())
	}
	writeJSON(w, http.StatusOK, map[string]any{"id": id, "enabled": body.Enabled})
}

func (s *Server) getPostProcess(w http.ResponseWriter, r *http.Request) {
	if selected := s.selected(w); selected != nil {
		writeJSON(w, http.StatusOK, map[string]any{
			"book_complete": isBookFullyAccepted(selected.Project.Progress),
			"state":         selected.Project.PostProcess,
		})
	}
}

func isBookFullyAccepted(progress *project.Progress) bool {
	if progress == nil || len(progress.Chapters) == 0 {
		return false
	}
	for _, chapter := range progress.Chapters {
		if chapter.Status != project.StatusAccepted || chapter.Content == "" {
			return false
		}
	}
	return true
}
func (s *Server) getStatus(w http.ResponseWriter, r *http.Request) {
	if selected := s.selected(w); selected != nil {
		progress := selected.Project.Progress
		phase, title, total := "outline", "", 0
		if progress != nil {
			phase, title, total = progress.Phase, progress.Title, len(progress.Chapters)
		}
		language := "zh"
		if selected.Project.Config != nil {
			language = selected.Project.Config.Language
		}
		s.autoConfirmMu.RLock()
		autoConfirm := s.autoConfirm
		s.autoConfirmMu.RUnlock()
		writeJSON(w, http.StatusOK, map[string]any{"phase": phase, "title": title, "total_chapters": total, "is_task_running": s.taskRunning(), "auto_confirm": autoConfirm, "project_language": language})
	}
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
func writeError(w http.ResponseWriter, status int, code string) {
	writeJSON(w, status, map[string]string{"error": code})
}

// Package continuation imports existing prose into a selected project.
package continuation

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"sync"

	"showmethestory/internal/app/runtime"
	"showmethestory/internal/domain/project"
	"showmethestory/internal/ports"
)

var (
	ErrTaskRunning     = errors.New("another task is already running")
	ErrNoAIClient      = errors.New("AI client is required")
	ErrModelRequired   = errors.New("AI model is required")
	ErrNoAnalysis      = errors.New("analyze content first")
	ErrResetRequired   = errors.New("reset progress first")
	ErrNoChapters      = errors.New("analysis has no chapters")
	ErrAnalysisChanged = errors.New("analysis changed while import was pending")
)

type Chapter struct {
	Num     int    `json:"num"`
	Title   string `json:"title"`
	Outline string `json:"outline,omitempty"`
	Summary string `json:"summary,omitempty"`
	Content string `json:"content,omitempty"`
}

type Analysis struct {
	Title         string    `json:"title"`
	StoryType     string    `json:"story_type"`
	CorePrompt    string    `json:"core_prompt"`
	StorySynopsis string    `json:"story_synopsis"`
	WritingStyle  string    `json:"writing_style"`
	WritingPOV    string    `json:"writing_pov"`
	Chapters      []Chapter `json:"chapters"`
}

type Dependencies struct {
	Session   *runtime.ProjectSession
	Tasks     *runtime.TaskManager
	AI        ports.AIClient
	Events    ports.EventPublisher
	Model     string
	MaxTokens int
}

type Service struct {
	session   *runtime.ProjectSession
	tasks     *runtime.TaskManager
	ai        ports.AIClient
	events    ports.EventPublisher
	model     string
	maxTokens int

	mu              sync.Mutex
	pendingContent  string
	pendingAnalysis *Analysis
}

func New(deps Dependencies) *Service {
	return &Service{session: deps.Session, tasks: deps.Tasks, ai: deps.AI, events: deps.Events, model: deps.Model, maxTokens: deps.MaxTokens}
}

func (s *Service) StartAnalyze(content string) error {
	if strings.TrimSpace(content) == "" {
		return errors.New("content is required")
	}
	if s.tasks == nil {
		return errors.New("task manager is required")
	}
	task, ok := s.tasks.Start("continue_analysis")
	if !ok {
		return ErrTaskRunning
	}
	go func() {
		err := s.Analyze(task.Context(), content)
		task.Done(err == nil)
	}()
	return nil
}

func (s *Service) Analyze(ctx context.Context, content string) error {
	if s.session == nil || s.session.Snapshot() == nil {
		return runtime.ErrNoProject
	}
	if s.ai == nil {
		return ErrNoAIClient
	}
	if strings.TrimSpace(s.model) == "" {
		return ErrModelRequired
	}
	snapshot := s.session.Snapshot()
	if snapshot.Project.Config == nil {
		return errors.New("project configuration is required")
	}
	config := snapshot.Project.Config
	prompt := project.RenderPrompt(config.Prompts.ContentAnalysis, map[string]string{"ExistingContent": content})
	system := "你是一位专业的小说内容分析编辑。请严格输出JSON，不要输出任何额外文字或markdown代码块。"
	if project.NormalizeLanguage(config.Language) == project.LangEN {
		system = "You are a professional novel content analyst. Output strict JSON only, with no extra prose or markdown code fences."
	}
	result, err := s.ai.Complete(ctx, ports.CompletionRequest{Model: s.model, Messages: []ports.Message{{Role: "system", Content: system}, {Role: "user", Content: prompt}}, MaxTokens: s.maxTokens})
	if err != nil {
		return err
	}
	var analysis Analysis
	if err := json.Unmarshal([]byte(cleanJSON(result.Content)), &analysis); err != nil {
		return fmt.Errorf("parse analysis JSON: %w", err)
	}
	if len(analysis.Chapters) == 0 {
		return ErrNoChapters
	}
	s.mu.Lock()
	s.pendingContent, s.pendingAnalysis = content, &analysis
	s.mu.Unlock()
	if s.events != nil {
		s.events.Publish("continue_analysis", &analysis)
	}
	return nil
}

// Confirm imports an analysis supplied by the user after review. The pending
// content is deliberately held only in memory, matching legacy behavior.
func (s *Service) Confirm(ctx context.Context, analysis Analysis) error {
	if len(analysis.Chapters) == 0 {
		return ErrNoChapters
	}
	s.mu.Lock()
	content, pending := s.pendingContent, s.pendingAnalysis
	s.mu.Unlock()
	if content == "" || pending == nil {
		return ErrNoAnalysis
	}
	if s.session == nil {
		return runtime.ErrNoProject
	}
	if err := s.session.WithProject(ctx, func(value *project.Project) error {
		if value.Progress != nil && value.Progress.Phase != "" && value.Progress.Phase != "outline" {
			return ErrResetRequired
		}
		if value.Config == nil {
			return errors.New("project configuration is required")
		}
		progress := &project.Progress{
			Phase: "outline", Title: analysis.Title, CorePrompt: analysis.CorePrompt, StorySynopsis: analysis.StorySynopsis,
			Chapters: importedChapters(content, analysis.Chapters), CurrentChapterIndex: len(analysis.Chapters),
		}
		snapshot := project.StoryConfig{Type: analysis.StoryType, Title: analysis.Title, ChapterCount: len(progress.Chapters), TargetWordsPerChapter: value.Config.Story.TargetWordsPerChapter, WritingStyle: analysis.WritingStyle, WritingPOV: analysis.WritingPOV, StorySynopsis: analysis.StorySynopsis}
		progress.StoryConfigSnapshot = &snapshot
		value.Progress = progress
		value.Config.Story.Type, value.Config.Story.Title = analysis.StoryType, analysis.Title
		value.Config.Story.WritingStyle, value.Config.Story.WritingPOV = analysis.WritingStyle, analysis.WritingPOV
		value.Config.Story.StorySynopsis = analysis.StorySynopsis
		return nil
	}); err != nil {
		return err
	}
	s.mu.Lock()
	if s.pendingAnalysis != pending {
		s.mu.Unlock()
		return ErrAnalysisChanged
	}
	s.pendingContent, s.pendingAnalysis = "", nil
	s.mu.Unlock()
	if s.events != nil {
		snapshot := s.session.Snapshot()
		if snapshot != nil {
			s.events.Publish("progress_update", snapshot.Project.Progress)
		}
	}
	return nil
}

var chapterBoundary = regexp.MustCompile(`(?m)^[\s]*(第[一二三四五六七八九十百千\d]+章|Chapter\s+\d+|#\s+Chapter\s+\d+|第\d+章)`)

func importedChapters(content string, chapters []Chapter) []project.Chapter {
	segments := chapterBoundary.FindAllStringIndex(content, -1)
	parts := []string{strings.TrimSpace(content)}
	if len(segments) > 0 {
		parts = make([]string, 0, len(segments))
		for i, segment := range segments {
			end := len(content)
			if i+1 < len(segments) {
				end = segments[i+1][0]
			}
			if value := strings.TrimSpace(content[segment[0]:end]); value != "" {
				parts = append(parts, value)
			}
		}
	}
	result := make([]project.Chapter, 0, len(chapters))
	for i, chapter := range chapters {
		content := ""
		if i < len(parts) {
			content = parts[i]
		}
		result = append(result, project.Chapter{Num: i + 1, Title: chapter.Title, Outline: chapter.Outline, Summary: chapter.Summary, Content: content, Status: project.StatusAccepted})
	}
	return result
}
func cleanJSON(value string) string {
	value = strings.TrimSpace(value)
	value = strings.TrimPrefix(value, "```json")
	value = strings.TrimPrefix(value, "```")
	value = strings.TrimSuffix(value, "```")
	return strings.TrimSpace(value)
}

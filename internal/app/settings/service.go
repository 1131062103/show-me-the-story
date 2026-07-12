// Package settings implements AI-assisted story-setting reconciliation.
package settings

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"showmethestory/internal/app/runtime"
	"showmethestory/internal/domain/project"
	"showmethestory/internal/ports"
)

var (
	ErrTaskRunning      = errors.New("another task is already running")
	ErrNoAIClient       = errors.New("AI client is required")
	ErrModelRequired    = errors.New("AI model is required")
	ErrNoConfiguration  = errors.New("project configuration is required")
	ErrInvalidReconcile = errors.New("invalid reconciliation response")
)

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
}

func New(deps Dependencies) *Service {
	return &Service{session: deps.Session, tasks: deps.Tasks, ai: deps.AI, events: deps.Events, model: deps.Model, maxTokens: deps.MaxTokens}
}

func (s *Service) StartReconcile(next project.StoryConfig) error {
	if s.tasks == nil {
		return errors.New("task manager is required")
	}
	task, ok := s.tasks.Start("settings_reconciliation")
	if !ok {
		return ErrTaskRunning
	}
	go func() { task.Done(s.Reconcile(task.Context(), next) == nil) }()
	return nil
}

// Reconcile applies the user-provided settings, proposes AI adjustments for
// user review, and revises only pending, unlocked outline chapters.
func (s *Service) Reconcile(ctx context.Context, next project.StoryConfig) error {
	if s.session == nil {
		return runtime.ErrNoProject
	}
	if s.ai == nil {
		return ErrNoAIClient
	}
	if strings.TrimSpace(s.model) == "" {
		return ErrModelRequired
	}
	snapshot := s.session.Snapshot()
	if snapshot == nil || snapshot.Project == nil {
		return runtime.ErrNoProject
	}
	if snapshot.Project.Config == nil {
		return ErrNoConfiguration
	}

	result, err := s.reconcile(ctx, snapshot.Project.Config, snapshot.Project.Progress, next)
	if err != nil {
		return err
	}
	adjusted := applyResult(next, result)
	changes := conflicts(next, adjusted, result.Explanation)

	var revised *outlineResponse
	if hasUnlockedPending(snapshot.Project.Progress) {
		revised, err = s.revisePending(ctx, snapshot.Project.Config, snapshot.Project.Progress, next)
		if err != nil {
			// Settings changes must not be lost because optional pending-outline
			// regeneration failed, matching the established legacy behavior.
			revised = nil
		}
	}

	if err := s.session.WithProject(ctx, func(value *project.Project) error {
		if value.Config == nil {
			return ErrNoConfiguration
		}
		value.Config.Story = next
		if value.Progress == nil {
			value.Progress = &project.Progress{Phase: "outline"}
		}
		syncProgress(value.Progress, next)
		if revised != nil {
			applyPendingRevision(value.Progress, revised)
		}
		return nil
	}); err != nil {
		return fmt.Errorf("persist reconciled settings: %w", err)
	}

	if err := s.appendPending(ctx, changes); err != nil {
		return fmt.Errorf("save pending config changes: %w", err)
	}
	if s.events != nil {
		latest := s.session.Snapshot()
		if latest != nil && latest.Project != nil {
			s.events.Publish("progress_update", latest.Project.Progress)
		}
		if len(changes) > 0 {
			s.events.Publish("config_change_proposal", map[string]any{"changes": changes})
		}
		s.events.Publish("settings_reconciled", map[string]any{"explanation": result.Explanation, "changed_fields": changedFields(changes)})
	}
	return nil
}

type reconciliationResponse struct {
	Type          string `json:"type"`
	WritingStyle  string `json:"writing_style"`
	WritingPOV    string `json:"writing_pov"`
	StorySynopsis string `json:"story_synopsis"`
	Explanation   string `json:"explanation"`
}

type outlineResponse struct {
	Chapters []struct {
		Num     int    `json:"num"`
		Title   string `json:"title"`
		Outline string `json:"outline"`
	} `json:"chapters"`
}

func (s *Service) reconcile(ctx context.Context, config *project.Config, progress *project.Progress, next project.StoryConfig) (*reconciliationResponse, error) {
	prompt := project.RenderPrompt(config.Prompts.SettingsReconciliation, map[string]string{
		"NewType": next.Type, "NewWritingStyle": next.WritingStyle, "NewWritingPOV": next.WritingPOV,
		"NewStorySynopsis": next.StorySynopsis, "ExistingSummaries": acceptedSummaries(progress, config.Language),
	})
	result, err := s.ai.Complete(ctx, ports.CompletionRequest{Model: s.model, Messages: []ports.Message{{Role: "system", Content: reconciliationSystemPrompt(config.Language)}, {Role: "user", Content: prompt}}, MaxTokens: s.maxTokens})
	if err != nil {
		return nil, fmt.Errorf("reconcile settings: %w", err)
	}
	var response reconciliationResponse
	if err := json.Unmarshal([]byte(cleanJSON(result.Content)), &response); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidReconcile, err)
	}
	return &response, nil
}

func (s *Service) revisePending(ctx context.Context, config *project.Config, progress *project.Progress, next project.StoryConfig) (*outlineResponse, error) {
	prompt := project.RenderPrompt(config.Prompts.OutlineRevision, map[string]string{
		"CurrentOutline": pendingOutline(progress, config.Language),
		"LockedChapters": lockedOutline(progress, config.Language),
		"UserFeedback":   reconcileFeedback(next, config.Language),
	})
	result, err := s.ai.Complete(ctx, ports.CompletionRequest{Model: s.model, Messages: []ports.Message{{Role: "system", Content: outlineSystemPrompt(config.Language)}, {Role: "user", Content: prompt}}, MaxTokens: s.maxTokens})
	if err != nil {
		return nil, err
	}
	var response outlineResponse
	if err := json.Unmarshal([]byte(cleanJSON(result.Content)), &response); err != nil {
		return nil, fmt.Errorf("parse pending outline revision: %w", err)
	}
	return &response, nil
}

func (s *Service) appendPending(ctx context.Context, incoming []project.ConfigFieldChange) error {
	if len(incoming) == 0 {
		return nil
	}
	snapshot := s.session.Snapshot()
	if snapshot == nil {
		return runtime.ErrNoProject
	}
	pending, err := snapshot.Store.LoadPendingConfigChanges(ctx)
	if err != nil {
		return err
	}
	pending.Changes = mergeChanges(pending.Changes, incoming)
	return snapshot.Store.SavePendingConfigChanges(ctx, pending)
}

func applyResult(next project.StoryConfig, result *reconciliationResponse) project.StoryConfig {
	adjusted := next
	if result.Type != "" {
		adjusted.Type = result.Type
	}
	if result.WritingStyle != "" {
		adjusted.WritingStyle = result.WritingStyle
	}
	if result.WritingPOV != "" {
		adjusted.WritingPOV = result.WritingPOV
	}
	if result.StorySynopsis != "" {
		adjusted.StorySynopsis = result.StorySynopsis
	}
	return adjusted
}

func conflicts(current, proposed project.StoryConfig, reason string) []project.ConfigFieldChange {
	var changes []project.ConfigFieldChange
	for _, field := range project.ProtectedStoryFields {
		currentValue, proposedValue := storyValue(current, field), storyValue(proposed, field)
		if strings.TrimSpace(proposedValue) != "" && strings.TrimSpace(currentValue) != "" && strings.TrimSpace(currentValue) != strings.TrimSpace(proposedValue) {
			changes = append(changes, project.ConfigFieldChange{Field: field, Current: currentValue, Proposed: proposedValue, Source: "reconcile", Reason: reason})
		}
	}
	return changes
}

func mergeChanges(existing, incoming []project.ConfigFieldChange) []project.ConfigFieldChange {
	byField := map[string]project.ConfigFieldChange{}
	for _, change := range existing {
		byField[change.Field] = change
	}
	for _, change := range incoming {
		byField[change.Field] = change
	}
	merged := make([]project.ConfigFieldChange, 0, len(byField))
	for _, field := range project.ProtectedStoryFields {
		if change, ok := byField[field]; ok {
			merged = append(merged, change)
			delete(byField, field)
		}
	}
	for _, change := range byField {
		merged = append(merged, change)
	}
	return merged
}

func changedFields(changes []project.ConfigFieldChange) []string {
	result := make([]string, 0, len(changes))
	for _, change := range changes {
		result = append(result, change.Field)
	}
	return result
}

func storyValue(story project.StoryConfig, field string) string {
	switch field {
	case "type":
		return story.Type
	case "title":
		return story.Title
	case "writing_style":
		return story.WritingStyle
	case "writing_pov":
		return story.WritingPOV
	case "story_synopsis":
		return story.StorySynopsis
	}
	return ""
}

func syncProgress(progress *project.Progress, story project.StoryConfig) {
	if story.Title != "" {
		progress.Title = story.Title
	}
	if story.StorySynopsis != "" {
		progress.StorySynopsis = story.StorySynopsis
	}
	snapshot := story
	progress.StoryConfigSnapshot = &snapshot
}

func hasUnlockedPending(progress *project.Progress) bool {
	if progress == nil {
		return false
	}
	for _, chapter := range progress.Chapters {
		if chapter.Status == project.StatusPending && !chapter.OutlineLocked {
			return true
		}
	}
	return false
}

func applyPendingRevision(progress *project.Progress, response *outlineResponse) {
	for _, replacement := range response.Chapters {
		for i := range progress.Chapters {
			chapter := &progress.Chapters[i]
			if chapter.Num == replacement.Num && chapter.Status == project.StatusPending && !chapter.OutlineLocked {
				chapter.Title, chapter.Outline = replacement.Title, replacement.Outline
			}
		}
	}
}

func acceptedSummaries(progress *project.Progress, language string) string {
	if progress == nil {
		return noAccepted(language)
	}
	var out strings.Builder
	for _, chapter := range progress.Chapters {
		if chapter.Status != project.StatusAccepted || chapter.Summary == "" {
			continue
		}
		if project.NormalizeLanguage(language) == project.LangEN {
			fmt.Fprintf(&out, "Chapter %d %q summary: %s\n", chapter.Num, chapter.Title, chapter.Summary)
		} else {
			fmt.Fprintf(&out, "第%d章《%s》摘要: %s\n", chapter.Num, chapter.Title, chapter.Summary)
		}
	}
	if out.Len() == 0 {
		return noAccepted(language)
	}
	return out.String()
}
func noAccepted(language string) string {
	if project.NormalizeLanguage(language) == project.LangEN {
		return "(no confirmed chapters yet)"
	}
	return "尚无已确认章节。"
}
func pendingOutline(progress *project.Progress, language string) string {
	var out strings.Builder
	for _, chapter := range progress.Chapters {
		if chapter.Status == project.StatusPending && !chapter.OutlineLocked {
			writeOutline(&out, chapter, language)
		}
	}
	return out.String()
}
func lockedOutline(progress *project.Progress, language string) string {
	var out strings.Builder
	for _, chapter := range progress.Chapters {
		if chapter.Status == project.StatusAccepted || chapter.OutlineLocked {
			writeOutline(&out, chapter, language)
		}
	}
	if out.Len() == 0 {
		if project.NormalizeLanguage(language) == project.LangEN {
			return "(no locked chapters)"
		}
		return "无已锁定章节。"
	}
	return out.String()
}
func writeOutline(out *strings.Builder, chapter project.Chapter, language string) {
	if project.NormalizeLanguage(language) == project.LangEN {
		fmt.Fprintf(out, "Chapter %d %q: %s\n", chapter.Num, chapter.Title, chapter.Outline)
	} else {
		fmt.Fprintf(out, "第%d章《%s》: %s\n", chapter.Num, chapter.Title, chapter.Outline)
	}
}
func reconcileFeedback(story project.StoryConfig, language string) string {
	if project.NormalizeLanguage(language) == project.LangEN {
		return fmt.Sprintf("Story settings updated to: type=%s, writing_style=%s, writing_pov=%s, synopsis=%s. Adjust the pending chapter outlines so they stay consistent with the new settings and the existing chapters.", story.Type, story.WritingStyle, story.WritingPOV, story.StorySynopsis)
	}
	return fmt.Sprintf("故事设定已更新为：类型=%s，写作风格=%s，叙述视角=%s，故事梗概=%s。请根据新设定调整待定章节大纲，使其与新设定和已有章节保持一致。", story.Type, story.WritingStyle, story.WritingPOV, story.StorySynopsis)
}
func reconciliationSystemPrompt(language string) string {
	if project.NormalizeLanguage(language) == project.LangEN {
		return "You are a professional novel consistency reviewer. Return strict JSON only, without markdown fences."
	}
	return "你是一位专业的小说一致性审阅编辑。请只返回严格的JSON，不要使用Markdown代码块。"
}
func outlineSystemPrompt(language string) string {
	if project.NormalizeLanguage(language) == project.LangEN {
		return "You are a professional novel-planning editor. Output strict JSON exactly as requested — no extra prose, no markdown code fences."
	}
	return "你是一位专业的小说策划编辑。请严格按照要求的JSON格式输出，不要添加任何额外文字或markdown代码块标记。"
}
func cleanJSON(raw string) string {
	raw = strings.TrimSpace(raw)
	raw = strings.TrimPrefix(raw, "```json")
	raw = strings.TrimPrefix(raw, "```")
	return strings.TrimSpace(strings.TrimSuffix(raw, "```"))
}

// Package writing implements core streamed chapter-generation workflows.
package writing

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"showmethestory/internal/app/runtime"
	"showmethestory/internal/domain/project"
	"showmethestory/internal/ports"
)

const maxLengthAttempts = 2 // initial draft plus one length-directed regeneration

var (
	ErrTaskRunning       = errors.New("another task is already running")
	ErrNoAIClient        = errors.New("AI client is required")
	ErrModelRequired     = errors.New("AI model is required")
	ErrNoConfiguration   = errors.New("project configuration is required")
	ErrWritingPhase      = errors.New("project is not in the writing phase")
	ErrAllChaptersDone   = errors.New("all chapters are complete")
	ErrAcceptedChapter   = errors.New("current chapter is already accepted")
	ErrInvalidCursor     = errors.New("current chapter cursor is invalid")
	ErrEmptyChapterDraft = errors.New("AI returned an empty chapter draft")
)

// ConflictError leaves the generated draft in writing state and supplies the
// persisted conflict that requires an explicit user decision.
type ConflictError struct{ Conflict *project.WritingConflict }

func (e *ConflictError) Error() string {
	if e == nil || e.Conflict == nil {
		return "writing conflict requires review"
	}
	return e.Conflict.Summary
}

// Dependencies are the boundaries required to generate and persist a chapter.
type Dependencies struct {
	Session      *runtime.ProjectSession
	Tasks        *runtime.TaskManager
	AI           ports.AIClient
	Events       ports.EventPublisher
	Model        string
	MaxTokens    int
	WritingRules func() string
}

// Service generates a chapter's prose, then verifies and records its narrative
// consistency before the chapter enters review.
type Service struct {
	session      *runtime.ProjectSession
	tasks        *runtime.TaskManager
	ai           ports.AIClient
	events       ports.EventPublisher
	model        string
	maxTokens    int
	writingRules func() string
}

func New(deps Dependencies) *Service {
	return &Service{session: deps.Session, tasks: deps.Tasks, ai: deps.AI, events: deps.Events, model: deps.Model, maxTokens: deps.MaxTokens, writingRules: deps.WritingRules}
}

// StartGenerate starts exclusive asynchronous chapter generation.
func (s *Service) StartGenerate() error {
	return s.StartGenerateAutoConfirm(func() bool { return false })
}

// StartGenerateAutoConfirm starts chapter generation and, while autoConfirm is
// enabled, accepts each completed chapter and continues with the next one. The
// callback is evaluated between chapters so toggling the HTTP setting during a
// generation run takes effect without cancelling the in-flight chapter.
func (s *Service) StartGenerateAutoConfirm(autoConfirm func() bool) error {
	if s.tasks == nil {
		return errors.New("task manager is required")
	}
	if autoConfirm == nil {
		autoConfirm = func() bool { return false }
	}
	task, ok := s.tasks.Start("chapter_generation")
	if !ok {
		return ErrTaskRunning
	}
	go func() {
		err := s.generateLoop(task.Context(), autoConfirm)
		task.Done(err == nil)
	}()
	return nil
}

func (s *Service) generateLoop(ctx context.Context, autoConfirm func() bool) error {
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := s.Generate(ctx); err != nil {
			return err
		}
		if !autoConfirm() {
			return nil
		}
		if err := s.confirmCurrent(ctx); err != nil {
			return fmt.Errorf("auto-confirm chapter: %w", err)
		}
		s.publishProgress()
		snapshot := s.session.Snapshot()
		if snapshot == nil || snapshot.Project == nil || snapshot.Project.Progress == nil || snapshot.Project.Progress.CurrentChapterIndex >= len(snapshot.Project.Progress.Chapters) {
			return nil
		}
	}
}

func (s *Service) confirmCurrent(ctx context.Context) error {
	if s.session == nil {
		return runtime.ErrNoProject
	}
	return s.session.WithProgress(ctx, func(progress *project.Progress) error {
		idx, err := validateTarget(progress)
		if err != nil {
			return err
		}
		if progress.Chapters[idx].Status != project.StatusReview {
			return ErrAcceptedChapter
		}
		progress.Chapters[idx].Status = project.StatusAccepted
		progress.CurrentChapterIndex++
		return nil
	})
}

// Generate writes the chapter selected by CurrentChapterIndex. It checkpoints
// writing status before invoking the provider, so cancellation never leaves an
// apparently pending chapter whose generation had already begun.
func (s *Service) Generate(ctx context.Context) error {
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
	idx, err := validateTarget(snapshot.Project.Progress)
	if err != nil {
		return err
	}

	// Persist the durable writing checkpoint before any streamed output.
	if err := s.session.WithProject(ctx, func(value *project.Project) error {
		if value.Config == nil {
			return ErrNoConfiguration
		}
		current, err := validateTarget(value.Progress)
		if err != nil {
			return err
		}
		if current != idx {
			return ErrInvalidCursor
		}
		value.Progress.Chapters[idx].Status = project.StatusWriting
		return nil
	}); err != nil {
		return fmt.Errorf("checkpoint chapter writing: %w", err)
	}
	s.publishProgress()

	snapshot = s.session.Snapshot()
	content, summary, conflict, err := s.generateConsistent(ctx, snapshot.Project, idx)
	if err != nil {
		return fmt.Errorf("generate chapter: %w", err)
	}

	chapter := snapshot.Project.Progress.Chapters[idx]
	chapter.Content, chapter.Summary = content, summary
	if conflict == nil {
		chapter.Status = project.StatusReview
	} else {
		chapter.Status = project.StatusWriting
	}
	if err := s.session.WithProject(ctx, func(value *project.Project) error {
		current, err := validateTarget(value.Progress)
		if err != nil {
			return err
		}
		if current != idx || value.Progress.Chapters[idx].Status == project.StatusAccepted {
			return ErrAcceptedChapter
		}
		value.Progress.Chapters[idx].Content, value.Progress.Chapters[idx].Summary, value.Progress.Chapters[idx].Status = chapter.Content, chapter.Summary, chapter.Status
		value.Progress.PendingWritingConflict = conflict
		if conflict == nil {
			// These update loops are deliberately applied only after a fact-check pass.
			s.syncForeshadows(ctx, value, idx)
			s.syncMemory(ctx, value, idx)
		}
		return nil
	}); err != nil {
		return fmt.Errorf("persist generated chapter: %w", err)
	}
	if conflict != nil {
		s.publishProgress()
		return &ConflictError{Conflict: conflict}
	}

	latest := s.session.Snapshot()
	if err := latest.Store.SaveChapterMarkdown(ctx, chapter.Num, []byte(markdown(chapter))); err != nil {
		return fmt.Errorf("export chapter markdown: %w", err)
	}
	s.publishProgress()
	return nil
}

// StartSmoothTransitions launches a cancellable pass over adjacent accepted
// chapters. It deliberately uses the same exclusive task manager as writing.
func (s *Service) StartSmoothTransitions() error {
	if s.tasks == nil {
		return errors.New("task manager is required")
	}
	task, ok := s.tasks.Start("smooth_transitions")
	if !ok {
		return ErrTaskRunning
	}
	go func() {
		err := s.SmoothTransitions(task.Context())
		task.Done(err == nil)
	}()
	return nil
}

// SmoothTransitions minimally rewrites the opening of an accepted chapter only
// when its preceding accepted chapter does not naturally lead into it. Each
// successful rewrite is persisted immediately so cancellation preserves earlier
// completed work.
func (s *Service) SmoothTransitions(ctx context.Context) error {
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
	if snapshot == nil || snapshot.Project == nil || snapshot.Project.Config == nil || snapshot.Project.Progress == nil {
		return ErrNoConfiguration
	}
	targets := transitionTargets(snapshot.Project.Progress)
	if len(targets) == 0 {
		return errors.New("no transitions to optimize")
	}
	for _, idx := range targets {
		if err := ctx.Err(); err != nil {
			return err
		}
		current := s.session.Snapshot()
		if current == nil || current.Project == nil || current.Project.Config == nil || current.Project.Progress == nil {
			return runtime.ErrNoProject
		}
		if idx <= 0 || idx >= len(current.Project.Progress.Chapters) || !acceptedPair(current.Project.Progress, idx) {
			continue
		}
		progress, config := current.Project.Progress, current.Project.Config
		chapter := progress.Chapters[idx]
		previous := progress.Chapters[idx-1]
		opening, rest := splitOpening(chapter.Content, 1000)
		prompt := project.RenderPrompt(config.Prompts.TransitionSmoothing, map[string]string{
			"ChapterNum": fmt.Sprint(chapter.Num), "ChapterTitle": chapter.Title,
			"ChapterOutline": chapter.Outline, "PrevTail": tail(previous.Content, 800), "Opening": opening,
		})
		result, err := s.ai.Complete(ctx, ports.CompletionRequest{Model: s.model, Messages: []ports.Message{{Role: "system", Content: transitionPrompt(config.Language)}, {Role: "user", Content: prompt}}, MaxTokens: s.maxTokens})
		if err != nil {
			return fmt.Errorf("smooth chapter %d transition: %w", chapter.Num, err)
		}
		revised := stripMeta(result.Content)
		if noTransitionChange(revised) {
			continue
		}
		content := revised
		if rest != "" {
			content += "\n\n" + strings.TrimLeft(rest, "\n")
		}
		content = mergeLockedParagraphs(chapter.Content, content, chapter.ParagraphLocks)
		if err := s.session.WithProgress(ctx, func(value *project.Progress) error {
			if idx <= 0 || idx >= len(value.Chapters) || !acceptedPair(value, idx) {
				return ErrInvalidCursor
			}
			// Transition smoothing is permitted for accepted prose, but it must
			// never change the chapter's acceptance state or lock metadata.
			value.Chapters[idx].Content = content
			return nil
		}); err != nil {
			return fmt.Errorf("persist smoothed chapter %d: %w", chapter.Num, err)
		}
		latest := s.session.Snapshot()
		if err := latest.Store.SaveChapterMarkdown(ctx, chapter.Num, []byte(markdown(latest.Project.Progress.Chapters[idx]))); err != nil {
			return fmt.Errorf("export smoothed chapter %d: %w", chapter.Num, err)
		}
		s.publishProgress()
	}
	return nil
}

func transitionTargets(progress *project.Progress) []int {
	var targets []int
	for idx := 1; idx < len(progress.Chapters); idx++ {
		if acceptedPair(progress, idx) {
			targets = append(targets, idx)
		}
	}
	return targets
}

func acceptedPair(progress *project.Progress, idx int) bool {
	return idx > 0 && idx < len(progress.Chapters) && progress.Chapters[idx-1].Status == project.StatusAccepted && progress.Chapters[idx].Status == project.StatusAccepted && strings.TrimSpace(progress.Chapters[idx-1].Content) != "" && strings.TrimSpace(progress.Chapters[idx].Content) != ""
}

func tail(content string, max int) string {
	value := []rune(strings.TrimSpace(content))
	if len(value) <= max {
		return string(value)
	}
	value = value[len(value)-max:]
	if newline := strings.IndexRune(string(value), '\n'); newline >= 0 {
		return strings.TrimSpace(string(value[newline+1:]))
	}
	return string(value)
}

func splitOpening(content string, max int) (string, string) {
	value := []rune(content)
	if len(value) <= max {
		return content, ""
	}
	cut := max
	for i := max; i > 0; i-- {
		if value[i-1] == '\n' {
			cut = i
			break
		}
	}
	return string(value[:cut]), string(value[cut:])
}

func noTransitionChange(value string) bool {
	head := []rune(strings.TrimSpace(value))
	if len(head) > 30 {
		head = head[:30]
	}
	return len(head) == 0 || strings.Contains(strings.ToUpper(string(head)), "NO_CHANGE")
}

func transitionPrompt(language string) string {
	if project.NormalizeLanguage(language) == project.LangEN {
		return "You are a senior novel editor. Output only NO_CHANGE or the minimally revised opening prose."
	}
	return "你是一位资深小说编辑。只输出 NO_CHANGE 或最小化修订后的开头正文。"
}

const maxFactCheckAttempts = 4

func (s *Service) generateConsistent(ctx context.Context, value *project.Project, idx int) (string, string, *project.WritingConflict, error) {
	var issues []string
	for attempt := 0; attempt < maxFactCheckAttempts; attempt++ {
		content, err := s.generateWithLengthControl(ctx, value, idx, "")
		if err != nil {
			return "", "", nil, err
		}
		summary, err := s.generateSummary(ctx, value.Config, content)
		if err != nil || strings.TrimSpace(summary) == "" {
			if err == nil {
				err = errors.New("AI returned an empty chapter summary")
			}
			return "", "", nil, err
		}
		failed, found, err := s.factCheck(ctx, value, idx, content)
		if err != nil {
			return "", "", nil, err
		}
		if !failed {
			return content, strings.TrimSpace(summary), nil, nil
		}
		issues = uniqueIssues(issues, found)
		if attempt+1 < maxFactCheckAttempts {
			continue
		}
		analysis, err := s.analyzeConflict(ctx, value, idx, content, issues)
		if err != nil {
			return content, strings.TrimSpace(summary), s.conflict(value, idx, issues, nil), nil
		}
		if analysis.Reconcilable && strings.TrimSpace(analysis.ExtraConstraints) != "" {
			content, err = s.generateWithLengthControl(ctx, value, idx, analysis.ExtraConstraints)
			if err != nil {
				return "", "", nil, err
			}
			summary, err = s.generateSummary(ctx, value.Config, content)
			if err != nil || strings.TrimSpace(summary) == "" {
				if err == nil {
					err = errors.New("AI returned an empty chapter summary")
				}
				return "", "", nil, err
			}
			failed, found, err = s.factCheck(ctx, value, idx, content)
			if err != nil {
				return "", "", nil, err
			}
			if !failed {
				return content, strings.TrimSpace(summary), nil, nil
			}
			issues = uniqueIssues(issues, found)
		}
		return content, strings.TrimSpace(summary), s.conflict(value, idx, issues, analysis), nil
	}
	return "", "", nil, errors.New("unreachable fact-check state")
}

type conflictAnalysis struct {
	Reconcilable     bool                           `json:"reconcilable"`
	Summary          string                         `json:"summary"`
	RootCause        string                         `json:"root_cause"`
	ExtraConstraints string                         `json:"extra_constraints"`
	SuggestedActions []project.ConflictActionOption `json:"suggested_actions"`
}

func (s *Service) factCheck(ctx context.Context, value *project.Project, idx int, content string) (bool, []string, error) {
	p, c, ch := value.Progress, value.Config, value.Progress.Chapters[idx]
	prompt := project.RenderPrompt(c.Prompts.FactCheck, map[string]string{"ChapterContent": content, "HistorySummary": history(p, idx, c.Language), "ChapterOutline": ch.Outline, "OutlineConstraints": outlineConstraints(p, idx), "Memory": memory(p)})
	result, err := s.ai.Complete(ctx, ports.CompletionRequest{Model: s.model, Messages: []ports.Message{{Role: "system", Content: "You are a strict novel fact-checker. Return JSON only."}, {Role: "user", Content: prompt}}, MaxTokens: s.maxTokens})
	if err != nil {
		return false, nil, err
	}
	var response struct {
		Result string   `json:"result"`
		Issues []string `json:"issues"`
	}
	if err := json.Unmarshal([]byte(cleanJSON(result.Content)), &response); err != nil {
		return false, nil, fmt.Errorf("parse fact check: %w", err)
	}
	return strings.EqualFold(strings.TrimSpace(response.Result), "FAIL"), response.Issues, nil
}
func (s *Service) analyzeConflict(ctx context.Context, value *project.Project, idx int, content string, issues []string) (*conflictAnalysis, error) {
	p, c, ch := value.Progress, value.Config, value.Progress.Chapters[idx]
	excerpt := []rune(content)
	if len(excerpt) > 1200 {
		excerpt = append(append(excerpt[:600], []rune("\n...\n")...), excerpt[len(excerpt)-600:]...)
	}
	prompt := project.RenderPrompt(c.Prompts.WritingConflictAnalysis, map[string]string{"ChapterNum": fmt.Sprint(ch.Num), "ChapterTitle": ch.Title, "ChapterOutline": ch.Outline, "HistorySummary": history(p, idx, c.Language), "OutlineConstraints": outlineConstraints(p, idx), "Foreshadows": foreshadows(p, ch.Num), "FailedIssues": strings.Join(issues, "\n"), "ContentExcerpt": string(excerpt)})
	result, err := s.ai.Complete(ctx, ports.CompletionRequest{Model: s.model, Messages: []ports.Message{{Role: "system", Content: "You are a novel conflict analyst. Return JSON only."}, {Role: "user", Content: prompt}}, MaxTokens: s.maxTokens})
	if err != nil {
		return nil, err
	}
	var analysis conflictAnalysis
	if err := json.Unmarshal([]byte(cleanJSON(result.Content)), &analysis); err != nil {
		return nil, fmt.Errorf("parse writing conflict: %w", err)
	}
	return &analysis, nil
}
func (s *Service) conflict(value *project.Project, idx int, issues []string, analysis *conflictAnalysis) *project.WritingConflict {
	ch := value.Progress.Chapters[idx]
	summary := "Fact checking repeatedly failed; review is required"
	root := "other"
	reconcilable := false
	var actions []project.ConflictActionOption
	if analysis != nil {
		if analysis.Summary != "" {
			summary = analysis.Summary
		}
		root = analysis.RootCause
		reconcilable = analysis.Reconcilable
		actions = analysis.SuggestedActions
	}
	return &project.WritingConflict{ChapterIndex: idx, ChapterNum: ch.Num, ChapterTitle: ch.Title, Issues: issues, Summary: summary, RootCause: root, Reconcilable: reconcilable, SuggestedActions: ensureActions(actions, value.Config.Language)}
}
func ensureActions(actions []project.ConflictActionOption, language string) []project.ConflictActionOption {
	defaults := []project.ConflictActionOption{{ID: "edit_outline", Label: "Edit chapter outline"}, {ID: "adjust_foreshadow", Label: "Adjust foreshadows"}, {ID: "retry", Label: "Retry after edits"}, {ID: "force_review", Label: "Keep draft for review"}}
	if project.NormalizeLanguage(language) != project.LangEN {
		defaults = []project.ConflictActionOption{{ID: "edit_outline", Label: "修改本章大纲"}, {ID: "adjust_foreshadow", Label: "调整伏笔"}, {ID: "retry", Label: "修改后重试生成"}, {ID: "force_review", Label: "保留当前稿进入审核"}}
	}
	byID := map[string]project.ConflictActionOption{}
	for _, a := range actions {
		byID[a.ID] = a
	}
	for i, d := range defaults {
		if a, ok := byID[d.ID]; ok {
			if a.Label == "" {
				a.Label = d.Label
			}
			defaults[i] = a
		}
	}
	return defaults
}
func uniqueIssues(all, next []string) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, list := range [][]string{all, next} {
		for _, v := range list {
			v = strings.TrimSpace(v)
			if v != "" && !seen[v] {
				seen[v] = true
				out = append(out, v)
			}
		}
	}
	return out
}

func validateTarget(progress *project.Progress) (int, error) {
	if progress == nil || progress.Phase != "writing" {
		return 0, ErrWritingPhase
	}
	if progress.CurrentChapterIndex < 0 {
		return 0, ErrInvalidCursor
	}
	if progress.CurrentChapterIndex >= len(progress.Chapters) {
		return 0, ErrAllChaptersDone
	}
	idx := progress.CurrentChapterIndex
	if progress.Chapters[idx].Status == project.StatusAccepted {
		return 0, ErrAcceptedChapter
	}
	return idx, nil
}

func (s *Service) generateWithLengthControl(ctx context.Context, value *project.Project, idx int, constraints string) (string, error) {
	target := value.Config.Story.TargetWordsPerChapter
	if value.Progress.StoryConfigSnapshot != nil && value.Progress.StoryConfigSnapshot.TargetWordsPerChapter > 0 {
		target = value.Progress.StoryConfigSnapshot.TargetWordsPerChapter
	}
	min, max := lengthRange(target)
	feedback := ""
	best := ""
	bestDistance := int(^uint(0) >> 1)
	for attempt := 0; attempt < maxLengthAttempts; attempt++ {
		content, err := s.streamChapter(ctx, value, idx, strings.TrimSpace(strings.Join([]string{constraints, feedback}, "\n\n")))
		if err != nil {
			return "", err
		}
		if strings.TrimSpace(content) == "" {
			return "", ErrEmptyChapterDraft
		}
		length := proseUnits(content)
		distance := distanceFromRange(length, min, max)
		if best == "" || distance < bestDistance {
			best, bestDistance = content, distance
		}
		if distance == 0 {
			return content, nil
		}
		if attempt+1 < maxLengthAttempts {
			feedback = lengthFeedback(length, min, max, value.Config.Language)
		}
	}
	return best, nil
}

func (s *Service) streamChapter(ctx context.Context, value *project.Project, idx int, feedback string) (string, error) {
	progress, config := value.Progress, value.Config
	chapter := progress.Chapters[idx]
	target := config.Story.TargetWordsPerChapter
	if progress.StoryConfigSnapshot != nil && progress.StoryConfigSnapshot.TargetWordsPerChapter > 0 {
		target = progress.StoryConfigSnapshot.TargetWordsPerChapter
	}
	min, max := lengthRange(target)
	data := map[string]string{
		"Title": title(config, progress), "ChapterNum": fmt.Sprintf("%d", chapter.Num), "CorePrompt": progress.CorePrompt,
		"StorySynopsis": synopsis(config, progress), "HistorySummary": history(progress, idx, config.Language),
		"PreviousEnding": previousTail(progress, idx, config.Language), "ChapterTitle": chapter.Title, "ChapterOutline": chapter.Outline,
		"WritingStyle": config.Story.WritingStyle, "WritingPOV": config.Story.WritingPOV,
		"CharacterContext": characterContext(value.Settings, chapter.Outline), "WorldviewContext": worldviewContext(value.Settings, chapter.Outline),
		"TargetWords": fmt.Sprintf("%d", target), "TargetWordsMin": fmt.Sprintf("%d", min), "TargetWordsMax": fmt.Sprintf("%d", max),
		"Foreshadows": foreshadows(progress, chapter.Num), "Memory": memory(progress), "OutlineConstraints": outlineConstraints(progress, idx),
	}
	prompt := project.RenderPrompt(config.Prompts.ChapterWriting, data)
	if !strings.Contains(config.Prompts.ChapterWriting, "{{.TargetWordsMin}}") {
		prompt += "\n\n" + lengthRequirement(min, max, config.Language)
	}
	for placeholder, block := range map[string]string{"{{.OutlineConstraints}}": data["OutlineConstraints"], "{{.Foreshadows}}": data["Foreshadows"], "{{.Memory}}": data["Memory"]} {
		if block != "" && !strings.Contains(config.Prompts.ChapterWriting, placeholder) {
			prompt += "\n\n" + block
		}
	}
	if feedback != "" {
		prompt += "\n\n" + feedback
	}
	if s.writingRules != nil {
		if rules := strings.TrimSpace(s.writingRules()); rules != "" {
			prompt += "\n\n" + rules
		}
	}
	system := progress.CorePrompt
	if system == "" {
		system = authorPrompt(config.Language)
	}
	if s.events != nil {
		s.events.Publish("stream_start", map[string]any{"chapter_idx": idx})
	}
	result, err := s.ai.Stream(ctx, ports.CompletionRequest{Model: s.model, Messages: []ports.Message{{Role: "system", Content: system}, {Role: "user", Content: prompt}}, MaxTokens: s.maxTokens}, func(chunk string) {
		if s.events != nil && chunk != "" {
			s.events.Publish("content_chunk", map[string]any{"chapter_idx": idx, "text": chunk})
		}
	})
	if err != nil {
		return "", err
	}
	return stripMeta(result.Content), nil
}

func (s *Service) generateSummary(ctx context.Context, config *project.Config, content string) (string, error) {
	prompt := project.RenderPrompt(config.Prompts.ChapterSummary, map[string]string{"ChapterContent": content})
	result, err := s.ai.Complete(ctx, ports.CompletionRequest{Model: s.model, Messages: []ports.Message{{Role: "system", Content: summaryPrompt(config.Language)}, {Role: "user", Content: prompt}}, MaxTokens: s.maxTokens})
	if err != nil {
		return "", err
	}
	return result.Content, nil
}

func (s *Service) publishProgress() {
	if s.events == nil {
		return
	}
	if snapshot := s.session.Snapshot(); snapshot != nil && snapshot.Project != nil {
		s.events.Publish("progress_update", snapshot.Project.Progress)
	}
}

func lengthRange(target int) (int, int) {
	if target < 1 {
		target = 2500
	}
	tolerance := 1000
	if candidate := target * 15 / 100; candidate > tolerance {
		tolerance = candidate
	}
	min := target - tolerance
	if min < 1 {
		min = 1
	}
	return min, target + tolerance
}
func proseUnits(value string) int { return utf8.RuneCountInString(strings.TrimSpace(value)) }
func distanceFromRange(actual, min, max int) int {
	if actual < min {
		return min - actual
	}
	if actual > max {
		return actual - max
	}
	return 0
}
func lengthRequirement(min, max int, language string) string {
	if project.NormalizeLanguage(language) == project.LangEN {
		return fmt.Sprintf("Chapter prose must be %d–%d words. Stay entirely within this chapter's outline.", min, max)
	}
	return fmt.Sprintf("正文字数须严格控制在 %d–%d 字，只写本章大纲范围内的情节。", min, max)
}
func lengthFeedback(actual, min, max int, language string) string {
	if actual > max {
		if project.NormalizeLanguage(language) == project.LangEN {
			return fmt.Sprintf("IMPORTANT: Previous draft was %d words, above %d–%d. Regenerate more concisely without advancing the plot.", actual, min, max)
		}
		return fmt.Sprintf("重要：上一稿为 %d 字，超过 %d–%d 字。请精简重写，不得推进后续剧情。", actual, min, max)
	}
	if project.NormalizeLanguage(language) == project.LangEN {
		return fmt.Sprintf("IMPORTANT: Previous draft was %d words, below %d–%d. Regenerate with more scene, action, and dialogue.", actual, min, max)
	}
	return fmt.Sprintf("重要：上一稿为 %d 字，低于 %d–%d 字。请补充场景、动作与对话。", actual, min, max)
}
func title(config *project.Config, progress *project.Progress) string {
	if config.Story.Title != "" {
		return config.Story.Title
	}
	return progress.Title
}
func synopsis(config *project.Config, progress *project.Progress) string {
	if config.Story.StorySynopsis != "" {
		return config.Story.StorySynopsis
	}
	return progress.StorySynopsis
}
func authorPrompt(language string) string {
	if project.NormalizeLanguage(language) == project.LangEN {
		return "You are a professional novelist. Output only chapter prose, without titles or commentary."
	}
	return "你是一位专业小说作者。只输出章节正文，不要标题或说明。"
}
func summaryPrompt(language string) string {
	if project.NormalizeLanguage(language) == project.LangEN {
		return "You are a precise novel narrative-state analyst."
	}
	return "你是一位精准的小说叙事状态分析师。"
}
func markdown(chapter project.Chapter) string {
	return fmt.Sprintf("# 第 %d 章: %s\n\n> **本章摘要**：%s\n\n---\n\n%s", chapter.Num, chapter.Title, chapter.Summary, chapter.Content)
}
func stripMeta(content string) string {
	lines := strings.Split(strings.TrimSpace(content), "\n")
	for len(lines) > 0 && (strings.HasPrefix(strings.ToLower(strings.TrimSpace(lines[0])), "chapter ") || strings.HasPrefix(strings.TrimSpace(lines[0]), "第") && strings.Contains(strings.TrimSpace(lines[0]), "章")) {
		lines = lines[1:]
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
}

// Context construction is intentionally local and conservative. Dedicated v2
// foreshadow and memory services will own their update loops after generation.
func history(progress *project.Progress, idx int, language string) string {
	var out strings.Builder
	start := idx - 5
	if start < 0 {
		start = 0
	}
	for i := start; i < idx; i++ {
		if progress.Chapters[i].Summary != "" {
			fmt.Fprintf(&out, "[Chapter %d summary]: %s\n", progress.Chapters[i].Num, progress.Chapters[i].Summary)
		}
	}
	if out.Len() == 0 {
		if project.NormalizeLanguage(language) == project.LangEN {
			return "This is the opening of the story; no prior context."
		}
		return "当前为故事开端，无历史前情。"
	}
	return out.String()
}
func previousTail(progress *project.Progress, idx int, language string) string {
	if idx <= 0 {
		return ""
	}
	value := []rune(strings.TrimSpace(progress.Chapters[idx-1].Content))
	if len(value) > 800 {
		value = value[len(value)-800:]
	}
	if len(value) == 0 {
		return ""
	}
	if project.NormalizeLanguage(language) == project.LangEN {
		return "[Previous chapter ending]\n" + string(value)
	}
	return "【上一章结尾原文】\n" + string(value)
}
func characterContext(settings *project.ProjectSettings, outline string) string {
	if settings == nil {
		return ""
	}
	var out strings.Builder
	for _, c := range settings.Characters {
		if strings.Contains(outline, c.Name) || len(settings.Characters) == 1 {
			fmt.Fprintf(&out, "[%s]\nAppearance: %s\nPersonality: %s\nBackground: %s\n", c.Name, c.Appearance, c.Personality, c.Background)
		}
	}
	return out.String()
}
func worldviewContext(settings *project.ProjectSettings, outline string) string {
	if settings == nil {
		return ""
	}
	var out strings.Builder
	for _, w := range settings.Worldview {
		if strings.Contains(outline, w.Name) || len(settings.Worldview) == 1 {
			fmt.Fprintf(&out, "[%s] %s\n", w.Name, w.Description)
		}
	}
	return out.String()
}
func foreshadows(progress *project.Progress, chapterNum int) string {
	var out strings.Builder
	for _, f := range progress.Foreshadows {
		if f.Status == project.ForeshadowPlanted || f.Status == project.ForeshadowProgressing {
			fmt.Fprintf(&out, "[Foreshadow %d: %s] %s\n", f.ID, f.Name, f.Description)
		}
	}
	return out.String()
}
func memory(progress *project.Progress) string {
	var out strings.Builder
	for _, entry := range progress.MemoryEntries {
		fmt.Fprintf(&out, "[Ch.%d] %s\n", entry.Chapter, entry.Content)
	}
	return out.String()
}
func outlineConstraints(progress *project.Progress, idx int) string {
	var out strings.Builder
	for i, ch := range progress.Chapters {
		if i != idx && ch.Outline != "" {
			fmt.Fprintf(&out, "[Chapter %d] %s\n", ch.Num, ch.Outline)
		}
	}
	return out.String()
}

func cleanJSON(value string) string {
	value = strings.TrimSpace(value)
	value = strings.TrimPrefix(value, "```json")
	value = strings.TrimPrefix(value, "```")
	value = strings.TrimSuffix(value, "```")
	return strings.TrimSpace(value)
}

func (s *Service) syncForeshadows(ctx context.Context, value *project.Project, idx int) {
	progress := value.Progress
	if len(progress.Foreshadows) == 0 || ctx.Err() != nil {
		return
	}
	chapter := progress.Chapters[idx]
	var listed strings.Builder
	for _, item := range progress.Foreshadows {
		fmt.Fprintf(&listed, "#%d [%s] %s\n%s\n", item.ID, item.Status, item.Name, item.Description)
	}
	prompt := project.RenderPrompt(value.Config.Prompts.ForeshadowUpdate, map[string]string{"Title": title(value.Config, progress), "ChapterNum": fmt.Sprint(chapter.Num), "ChapterTitle": chapter.Title, "ChapterContent": chapter.Content, "HistorySummary": history(progress, idx, value.Config.Language), "Foreshadows": listed.String()})
	result, err := s.ai.Complete(ctx, ports.CompletionRequest{Model: s.model, Messages: []ports.Message{{Role: "system", Content: "You are a strict foreshadow tracker. Return JSON only."}, {Role: "user", Content: prompt}}, MaxTokens: s.maxTokens})
	if err != nil {
		return
	}
	var response struct {
		Updates []struct {
			ID         int                      `json:"id"`
			Status     project.ForeshadowStatus `json:"status"`
			Event      string                   `json:"event"`
			Resolution string                   `json:"resolution"`
		} `json:"updates"`
	}
	if json.Unmarshal([]byte(cleanJSON(result.Content)), &response) != nil {
		return
	}
	for _, update := range response.Updates {
		for i := range progress.Foreshadows {
			item := &progress.Foreshadows[i]
			if item.ID != update.ID {
				continue
			}
			if update.Status != "" {
				item.Status = update.Status
			}
			if update.Event != "" {
				item.Events = append(item.Events, project.ForeshadowEvent{Chapter: chapter.Num, Note: update.Event})
			}
			if update.Resolution != "" {
				item.Resolution = update.Resolution
			}
		}
	}
}

// removeChapterMemory drops obsolete facts extracted from a revised chapter before
// the memory update pass derives replacements from its new prose.
func removeChapterMemory(progress *project.Progress, chapterNum int) {
	filtered := progress.MemoryEntries[:0]
	for _, entry := range progress.MemoryEntries {
		if entry.Chapter != chapterNum {
			filtered = append(filtered, entry)
		}
	}
	progress.MemoryEntries = filtered
}

func (s *Service) syncMemory(ctx context.Context, value *project.Project, idx int) {
	progress := value.Progress
	if ctx.Err() != nil {
		return
	}
	chapter := progress.Chapters[idx]
	if progress.MemoryMaxTokens <= 0 {
		source := value.Config.Story
		if progress.StoryConfigSnapshot != nil {
			source = *progress.StoryConfigSnapshot
		}
		progress.MemoryMaxTokens = source.ChapterCount * source.TargetWordsPerChapter / 10
		if progress.MemoryMaxTokens < 2000 {
			progress.MemoryMaxTokens = 2000
		}
		if progress.MemoryMaxTokens > 20000 {
			progress.MemoryMaxTokens = 20000
		}
	}
	var existing strings.Builder
	for _, entry := range progress.MemoryEntries {
		fmt.Fprintf(&existing, "#%d [%s] Ch.%d: %s\n", entry.ID, entry.Category, entry.Chapter, entry.Content)
	}
	prompt := project.RenderPrompt(value.Config.Prompts.MemoryUpdate, map[string]string{"Title": title(value.Config, progress), "ChapterNum": fmt.Sprint(chapter.Num), "ChapterTitle": chapter.Title, "ChapterOutline": chapter.Outline, "ChapterContent": chapter.Content, "ExistingMemory": existing.String(), "MemoryMaxTokens": fmt.Sprint(progress.MemoryMaxTokens)})
	result, err := s.ai.Complete(ctx, ports.CompletionRequest{Model: s.model, Messages: []ports.Message{{Role: "system", Content: "You are a precise narrative memory manager. Return JSON only."}, {Role: "user", Content: prompt}}, MaxTokens: s.maxTokens})
	if err != nil {
		return
	}
	var response struct {
		New []struct {
			Content  string `json:"content"`
			Category string `json:"category"`
			Position int    `json:"position"`
		} `json:"new_memories"`
		Updates []struct {
			ID     int    `json:"id"`
			Action string `json:"action"`
		} `json:"updates"`
	}
	if json.Unmarshal([]byte(cleanJSON(result.Content)), &response) != nil {
		return
	}
	deleted := map[int]bool{}
	for _, update := range response.Updates {
		if update.Action == "delete" {
			deleted[update.ID] = true
		}
	}
	filtered := progress.MemoryEntries[:0]
	maxID := 0
	for _, entry := range progress.MemoryEntries {
		if entry.ID > maxID {
			maxID = entry.ID
		}
		if !deleted[entry.ID] {
			filtered = append(filtered, entry)
		}
	}
	progress.MemoryEntries = filtered
	for _, entry := range response.New {
		if strings.TrimSpace(entry.Content) == "" {
			continue
		}
		maxID++
		progress.MemoryEntries = append(progress.MemoryEntries, project.MemoryEntry{ID: maxID, Content: entry.Content, Category: entry.Category, Chapter: chapter.Num, Position: entry.Position})
	}
}

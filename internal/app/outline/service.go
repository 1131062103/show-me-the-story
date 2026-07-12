// Package outline implements outline-generation application workflows.
package outline

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

const maxGenerationAttempts = 2

var (
	ErrTaskRunning         = errors.New("another task is already running")
	ErrNoAIClient          = errors.New("AI client is required")
	ErrModelRequired       = errors.New("AI model is required")
	ErrNoConfiguration     = errors.New("project configuration is required")
	ErrAcceptedChapters    = errors.New("cannot regenerate outline while accepted chapters exist")
	ErrFeedbackRequired    = errors.New("revision feedback is required")
	ErrNoChapters          = errors.New("outline has no chapters")
	ErrOutlineNotActive    = errors.New("outline is not in the outline phase")
	ErrInvalidChapterCount = errors.New("chapter count must be positive")
	ErrOutlineChanged      = errors.New("outline changed while generation was in progress")
	ErrInvalidContinuation = errors.New("invalid continuation outline")
)

// Dependencies are the application boundaries required to generate an outline.
type Dependencies struct {
	Session   *runtime.ProjectSession
	Tasks     *runtime.TaskManager
	AI        ports.AIClient
	Events    ports.EventPublisher
	Model     string
	MaxTokens int
}

// Service generates an outline and commits it through the selected-project session.
type Service struct {
	session   *runtime.ProjectSession
	tasks     *runtime.TaskManager
	ai        ports.AIClient
	events    ports.EventPublisher
	model     string
	maxTokens int
}

func New(deps Dependencies) *Service {
	return &Service{
		session: deps.Session, tasks: deps.Tasks, ai: deps.AI, events: deps.Events,
		model: deps.Model, maxTokens: deps.MaxTokens,
	}
}

// StartGenerate begins asynchronous outline generation. Task lifecycle events are
// published by TaskManager; a progress_update is published after a successful save.
func (s *Service) StartGenerate() error {
	if s.tasks == nil {
		return errors.New("task manager is required")
	}
	task, ok := s.tasks.Start("outline_generation")
	if !ok {
		return ErrTaskRunning
	}
	go func() {
		err := s.Generate(task.Context())
		task.Done(err == nil)
	}()
	return nil
}

// Generate runs the workflow synchronously. It is useful to callers that already
// own an asynchronous task and to focused application tests.
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
	if hasAcceptedChapters(snapshot.Project.Progress) {
		return ErrAcceptedChapters
	}

	response, err := s.generate(ctx, snapshot.Project.Config, snapshot.Project.Settings)
	if err != nil {
		return fmt.Errorf("generate outline: %w", err)
	}

	if err := s.session.WithProject(ctx, func(value *project.Project) error {
		if value.Config == nil {
			return ErrNoConfiguration
		}
		if hasAcceptedChapters(value.Progress) {
			return ErrAcceptedChapters
		}
		if value.Progress == nil {
			value.Progress = &project.Progress{Phase: "outline"}
		}
		applyGeneratedOutline(value.Progress, response, value.Config.Story)
		return nil
	}); err != nil {
		return fmt.Errorf("persist outline: %w", err)
	}
	if s.events != nil {
		snapshot = s.session.Snapshot()
		if snapshot != nil && snapshot.Project != nil {
			s.events.Publish("progress_update", snapshot.Project.Progress)
		}
	}
	return nil
}

// StartRevise begins asynchronous outline revision using the supplied feedback.
func (s *Service) StartRevise(feedback string) error {
	if strings.TrimSpace(feedback) == "" {
		return ErrFeedbackRequired
	}
	return s.start("outline_revision", func(ctx context.Context) error { return s.Revise(ctx, feedback) })
}

// Revise updates only chapters that are neither accepted nor explicitly locked.
// The model still receives the full outline so that it can preserve plot continuity.
func (s *Service) Revise(ctx context.Context, feedback string) error {
	if strings.TrimSpace(feedback) == "" {
		return ErrFeedbackRequired
	}
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
	if snapshot.Project.Progress == nil || len(snapshot.Project.Progress.Chapters) == 0 {
		return ErrNoChapters
	}

	generated, err := s.revise(ctx, snapshot.Project.Config, snapshot.Project.Settings, snapshot.Project.Progress, feedback)
	if err != nil {
		return fmt.Errorf("revise outline: %w", err)
	}
	if err := s.session.WithProgress(ctx, func(progress *project.Progress) error {
		if len(progress.Chapters) == 0 {
			return ErrNoChapters
		}
		applyRevision(progress, generated)
		return nil
	}); err != nil {
		return fmt.Errorf("persist revised outline: %w", err)
	}
	s.publishProgress()
	return nil
}

// Confirm moves a non-empty outline into the writing phase. It deliberately
// leaves CurrentChapterIndex unchanged so confirmation cannot skip a chapter.
func (s *Service) Confirm(ctx context.Context) error {
	if s.session == nil {
		return runtime.ErrNoProject
	}
	if err := s.session.WithProgress(ctx, func(progress *project.Progress) error {
		if len(progress.Chapters) == 0 {
			return ErrNoChapters
		}
		if progress.Phase != "" && progress.Phase != "outline" {
			return ErrOutlineNotActive
		}
		progress.Phase = "writing"
		return nil
	}); err != nil {
		return fmt.Errorf("confirm outline: %w", err)
	}
	s.publishProgress()
	return nil
}

// StartContinue begins asynchronous generation of additional, pending chapters.
func (s *Service) StartContinue(chapterCount int) error {
	if chapterCount <= 0 {
		return ErrInvalidChapterCount
	}
	return s.start("continuation_outline", func(ctx context.Context) error { return s.Continue(ctx, chapterCount) })
}

// Continue appends validated pending chapters and never replaces existing ones.
func (s *Service) Continue(ctx context.Context, chapterCount int) error {
	if chapterCount <= 0 {
		return ErrInvalidChapterCount
	}
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
	if snapshot.Project.Progress == nil {
		return ErrNoChapters
	}

	startNum := nextChapterNumber(snapshot.Project.Progress.Chapters)
	chapters, err := s.continueOutline(ctx, snapshot.Project.Config, snapshot.Project.Settings, snapshot.Project.Progress, chapterCount, startNum)
	if err != nil {
		return fmt.Errorf("generate continuation outline: %w", err)
	}
	if err := validateContinuation(chapters, chapterCount, startNum); err != nil {
		return err
	}
	if err := s.session.WithProgress(ctx, func(progress *project.Progress) error {
		// The selected project may have changed while the AI request was in flight.
		// Revalidate the append boundary before persisting rather than overwriting it.
		if nextChapterNumber(progress.Chapters) != startNum {
			return ErrOutlineChanged
		}
		for _, generated := range chapters {
			progress.Chapters = append(progress.Chapters, project.Chapter{
				Num: generated.Num, Title: generated.Title, Outline: generated.Outline, Status: project.StatusPending,
			})
		}
		return nil
	}); err != nil {
		return fmt.Errorf("persist continuation outline: %w", err)
	}
	s.publishProgress()
	return nil
}

func (s *Service) start(name string, work func(context.Context) error) error {
	if s.tasks == nil {
		return errors.New("task manager is required")
	}
	task, ok := s.tasks.Start(name)
	if !ok {
		return ErrTaskRunning
	}
	go func() {
		err := work(task.Context())
		task.Done(err == nil)
	}()
	return nil
}

func (s *Service) publishProgress() {
	if s.events == nil || s.session == nil {
		return
	}
	if snapshot := s.session.Snapshot(); snapshot != nil && snapshot.Project != nil {
		s.events.Publish("progress_update", snapshot.Project.Progress)
	}
}

type response struct {
	Title         string    `json:"title"`
	CorePrompt    string    `json:"core_prompt"`
	StorySynopsis string    `json:"story_synopsis"`
	Chapters      []chapter `json:"chapters"`
}

type chapter struct {
	Num     int    `json:"num"`
	Title   string `json:"title"`
	Outline string `json:"outline"`
}

func (s *Service) generate(ctx context.Context, config *project.Config, settings *project.ProjectSettings) (*response, error) {
	data := outlinePromptData(config, settings)
	systemPrompt := outlineSystemPrompt(config.Language)
	minLength := outlineMinimumLength(config.Story.TargetWordsPerChapter)

	var last *response
	var short []int
	for attempt := 0; attempt < maxGenerationAttempts; attempt++ {
		prompt := finalizePrompt(config.Prompts.OutlineGeneration, project.RenderPrompt(config.Prompts.OutlineGeneration, data), config, settings)
		if attempt > 0 {
			prompt += retryFeedback(short, minLength, config.Language)
		}
		result, err := s.ai.Complete(ctx, ports.CompletionRequest{
			Model:     s.model,
			Messages:  []ports.Message{{Role: "system", Content: systemPrompt}, {Role: "user", Content: prompt}},
			MaxTokens: s.maxTokens,
		})
		if err != nil {
			return nil, err
		}
		if strings.TrimSpace(result.Content) == "" {
			return nil, errors.New("AI returned an empty outline")
		}
		parsed, err := parseResponse(result.Content)
		if err != nil {
			return nil, err
		}
		last = parsed
		short = shortChapters(parsed.Chapters, minLength)
		if len(short) == 0 {
			return parsed, nil
		}
	}
	return last, nil
}

func (s *Service) revise(ctx context.Context, config *project.Config, settings *project.ProjectSettings, progress *project.Progress, feedback string) (*response, error) {
	data := revisionPromptData(config, settings, progress, feedback)
	minimum := outlineMinimumLength(config.Story.TargetWordsPerChapter)
	var last *response
	var short []int
	for attempt := 0; attempt < maxGenerationAttempts; attempt++ {
		prompt := finalizePrompt(config.Prompts.OutlineRevision, project.RenderPrompt(config.Prompts.OutlineRevision, data), config, settings)
		if attempt > 0 {
			prompt += retryFeedback(short, minimum, config.Language)
		}
		result, err := s.ai.Complete(ctx, ports.CompletionRequest{
			Model: s.model, Messages: []ports.Message{{Role: "system", Content: outlineSystemPrompt(config.Language)}, {Role: "user", Content: prompt}}, MaxTokens: s.maxTokens,
		})
		if err != nil {
			return nil, err
		}
		if strings.TrimSpace(result.Content) == "" {
			return nil, errors.New("AI returned an empty outline")
		}
		parsed, err := parseResponse(result.Content)
		if err != nil {
			return nil, err
		}
		last = parsed
		short = shortChapters(unlockedChapters(parsed.Chapters, progress), minimum)
		if len(short) == 0 {
			return parsed, nil
		}
	}
	return last, nil
}

func (s *Service) continueOutline(ctx context.Context, config *project.Config, settings *project.ProjectSettings, progress *project.Progress, count, start int) ([]chapter, error) {
	snapshot := progress.StoryConfigSnapshot
	if snapshot == nil {
		snapshot = &config.Story
	}
	data := map[string]string{
		"Title": stateTitle(progress, config), "StoryType": snapshot.Type, "CorePrompt": progress.CorePrompt,
		"StorySynopsis": progress.StorySynopsis, "WritingStyle": snapshot.WritingStyle, "WritingPOV": snapshot.WritingPOV,
		"ExistingOutline": existingOutline(progress, config.Language), "NewChapterCount": fmt.Sprintf("%d", count), "StartNum": fmt.Sprintf("%d", start),
	}
	for key, value := range outlinePromptData(config, settings) {
		if _, exists := data[key]; !exists {
			data[key] = value
		}
	}
	minimum := outlineMinimumLength(config.Story.TargetWordsPerChapter)
	var last []chapter
	var short []int
	for attempt := 0; attempt < maxGenerationAttempts; attempt++ {
		prompt := finalizePrompt(config.Prompts.ContinuationOutlineGeneration, project.RenderPrompt(config.Prompts.ContinuationOutlineGeneration, data), config, settings)
		if attempt > 0 {
			prompt += retryFeedback(short, minimum, config.Language)
		}
		result, err := s.ai.Complete(ctx, ports.CompletionRequest{
			Model: s.model, Messages: []ports.Message{{Role: "system", Content: outlineSystemPrompt(config.Language)}, {Role: "user", Content: prompt}}, MaxTokens: s.maxTokens,
		})
		if err != nil {
			return nil, err
		}
		if strings.TrimSpace(result.Content) == "" {
			return nil, errors.New("AI returned an empty continuation outline")
		}
		var parsed struct {
			Chapters []chapter `json:"chapters"`
		}
		if err := json.Unmarshal([]byte(cleanJSON(result.Content)), &parsed); err != nil {
			return nil, fmt.Errorf("parse continuation outline JSON: %w", err)
		}
		if err := validateContinuation(parsed.Chapters, count, start); err != nil {
			return nil, err
		}
		last = parsed.Chapters
		short = shortChapters(last, minimum)
		if len(short) == 0 {
			return last, nil
		}
	}
	return last, nil
}

func revisionPromptData(config *project.Config, settings *project.ProjectSettings, progress *project.Progress, feedback string) map[string]string {
	data := outlinePromptData(config, settings)
	data["CurrentOutline"] = existingOutline(progress, config.Language)
	data["LockedChapters"] = lockedOutline(progress, config.Language)
	data["UserFeedback"] = feedback
	return data
}

func existingOutline(progress *project.Progress, language string) string {
	var result strings.Builder
	for _, item := range progress.Chapters {
		if project.NormalizeLanguage(language) == project.LangEN {
			fmt.Fprintf(&result, "Chapter %d \"%s\": %s\n", item.Num, item.Title, item.Outline)
		} else {
			fmt.Fprintf(&result, "第%d章《%s》: %s\n", item.Num, item.Title, item.Outline)
		}
		if item.Summary != "" {
			if project.NormalizeLanguage(language) == project.LangEN {
				fmt.Fprintf(&result, "Summary: %s\n", item.Summary)
			} else {
				fmt.Fprintf(&result, "摘要：%s\n", item.Summary)
			}
		}
	}
	return result.String()
}

func lockedOutline(progress *project.Progress, language string) string {
	var result strings.Builder
	for _, item := range progress.Chapters {
		if item.Status == project.StatusAccepted || item.OutlineLocked {
			if project.NormalizeLanguage(language) == project.LangEN {
				fmt.Fprintf(&result, "Chapter %d \"%s\": %s (locked; must not be changed)\n", item.Num, item.Title, item.Outline)
			} else {
				fmt.Fprintf(&result, "第%d章《%s》: %s（已锁定，不可修改）\n", item.Num, item.Title, item.Outline)
			}
		}
	}
	if result.Len() == 0 {
		if project.NormalizeLanguage(language) == project.LangEN {
			return "(no locked chapters)"
		}
		return "无已锁定章节。"
	}
	return result.String()
}

func unlockedChapters(chapters []chapter, progress *project.Progress) []chapter {
	locked := make(map[int]bool, len(progress.Chapters))
	for _, item := range progress.Chapters {
		locked[item.Num] = item.Status == project.StatusAccepted || item.OutlineLocked
	}
	result := make([]chapter, 0, len(chapters))
	for _, item := range chapters {
		if !locked[item.Num] {
			result = append(result, item)
		}
	}
	return result
}

func applyRevision(progress *project.Progress, revised *response) {
	for _, replacement := range revised.Chapters {
		for i := range progress.Chapters {
			current := &progress.Chapters[i]
			if current.Num != replacement.Num || current.Status == project.StatusAccepted || current.OutlineLocked {
				continue
			}
			current.Title, current.Outline = replacement.Title, replacement.Outline
		}
	}
	if revised.Title != "" {
		progress.Title = revised.Title
	}
	if revised.CorePrompt != "" {
		progress.CorePrompt = revised.CorePrompt
	}
	if revised.StorySynopsis != "" {
		progress.StorySynopsis = revised.StorySynopsis
	}
}

func nextChapterNumber(chapters []project.Chapter) int {
	max := 0
	for _, item := range chapters {
		if item.Num > max {
			max = item.Num
		}
	}
	return max + 1
}

func validateContinuation(chapters []chapter, count, start int) error {
	if len(chapters) != count {
		return fmt.Errorf("%w: expected %d chapters, got %d", ErrInvalidContinuation, count, len(chapters))
	}
	for i, item := range chapters {
		if item.Num != start+i || strings.TrimSpace(item.Title) == "" || strings.TrimSpace(item.Outline) == "" {
			return fmt.Errorf("%w: expected populated chapter %d", ErrInvalidContinuation, start+i)
		}
	}
	return nil
}

func stateTitle(progress *project.Progress, config *project.Config) string {
	if progress.Title != "" {
		return progress.Title
	}
	return config.Story.Title
}

func parseResponse(raw string) (*response, error) {
	raw = cleanJSON(raw)
	var parsed response
	if err := json.Unmarshal([]byte(raw), &parsed); err != nil {
		return nil, fmt.Errorf("parse outline JSON: %w", err)
	}
	return &parsed, nil
}

func applyGeneratedOutline(progress *project.Progress, generated *response, story project.StoryConfig) {
	locked := make(map[int]project.Chapter)
	for _, existing := range progress.Chapters {
		if existing.Status == project.StatusAccepted || existing.OutlineLocked {
			locked[existing.Num] = existing
		}
	}

	chapters := make([]project.Chapter, 0, len(generated.Chapters)+len(locked))
	seen := make(map[int]bool)
	for _, generatedChapter := range generated.Chapters {
		if existing, ok := locked[generatedChapter.Num]; ok {
			chapters = append(chapters, existing)
		} else {
			chapters = append(chapters, project.Chapter{Num: generatedChapter.Num, Title: generatedChapter.Title, Outline: generatedChapter.Outline, Status: project.StatusPending})
		}
		seen[generatedChapter.Num] = true
	}
	for number, existing := range locked {
		if !seen[number] {
			chapters = append(chapters, existing)
		}
	}
	progress.Title = generated.Title
	progress.CorePrompt = generated.CorePrompt
	progress.StorySynopsis = generated.StorySynopsis
	progress.Chapters = chapters
	snapshot := story
	progress.StoryConfigSnapshot = &snapshot
}

func hasAcceptedChapters(progress *project.Progress) bool {
	if progress == nil {
		return false
	}
	for _, chapter := range progress.Chapters {
		if chapter.Status == project.StatusAccepted {
			return true
		}
	}
	return false
}

func outlinePromptData(config *project.Config, settings *project.ProjectSettings) map[string]string {
	min, max := outlineLengthRange(config.Story.TargetWordsPerChapter)
	return map[string]string{
		"StoryType": config.Story.Type, "ChapterCount": fmt.Sprintf("%d", config.Story.ChapterCount),
		"TargetWords": fmt.Sprintf("%d", config.Story.TargetWordsPerChapter), "WritingStyle": config.Story.WritingStyle,
		"WritingPOV": config.Story.WritingPOV, "StorySynopsis": config.Story.StorySynopsis,
		"OutlineMinWords": fmt.Sprintf("%d", min), "OutlineMaxWords": fmt.Sprintf("%d", max),
		"CharacterList": characterList(settings, config.Language),
	}
}

func outlineLengthRange(target int) (int, int) {
	if target < 1 {
		target = 2500
	}
	min, max := target/20, target/8
	if min < 80 {
		min = 80
	}
	if max < 150 {
		max = 150
	}
	if max < min+20 {
		max = min + 20
	}
	return min, max
}
func outlineMinimumLength(target int) int { min, _ := outlineLengthRange(target); return min }

func characterList(settings *project.ProjectSettings, language string) string {
	en := project.NormalizeLanguage(language) == project.LangEN
	if settings == nil || len(settings.Characters) == 0 {
		if en {
			return "(No characters registered yet. You may introduce new characters only when necessary; mark each debut with \"first appearance\" and include a one-line role or relationship to the protagonist.)"
		}
		return "（尚未在角色管理中登记角色。仅因剧情需要方可引入新人物；须在其首次出场章节标注「首次登场」，并附一行身份或与主角关系说明。）"
	}
	var out strings.Builder
	if en {
		out.WriteString("Registered characters (prefer these; avoid duplicates):\n")
	} else {
		out.WriteString("已登记角色（优先使用，避免功能重复）：\n")
	}
	for _, character := range settings.Characters {
		out.WriteString("- " + character.Name)
		if character.Personality != "" {
			if en {
				out.WriteString(" — personality: " + character.Personality)
			} else {
				out.WriteString(" — 性格：" + character.Personality)
			}
		} else if character.Background != "" {
			if en {
				out.WriteString(" — background: " + truncateRunes(character.Background, 40))
			} else {
				out.WriteString(" — 背景：" + truncateRunes(character.Background, 40))
			}
		}
		out.WriteByte('\n')
	}
	if en {
		out.WriteString("\nOnly add unlisted characters when the plot requires it; mark \"first appearance\" in their debut chapter with a one-line description.")
	} else {
		out.WriteString("\n仅当剧情需要时方可新增未登记角色；在其首次出场章节标注「首次登场」并附一行说明。")
	}
	return out.String()
}

func finalizePrompt(template, rendered string, config *project.Config, settings *project.ProjectSettings) string {
	min, max := outlineLengthRange(config.Story.TargetWordsPerChapter)
	if strings.Contains(template, "{{.CharacterList}}") == false {
		rendered += "\n\n" + characterList(settings, config.Language)
	}
	if !strings.Contains(template, "{{.OutlineMinWords}}") {
		if project.NormalizeLanguage(config.Language) == project.LangEN {
			rendered += fmt.Sprintf("\n\nEach chapter outline must be %d–%d characters (not counting the chapter title). Outlines shorter than %d characters are unacceptable. Each chapter outline must cover, in order: opening scene/location; core conflict or goal; key turning point or revelation; characters appearing (with roles); how the chapter ends or what hook it leaves.", min, max, min)
		} else {
			rendered += fmt.Sprintf("\n\n每章 outline 字段正文须为 %d–%d 字（不含章节标题）。低于 %d 字视为不合格。每章大纲须依次包含：开场场景/地点；本章核心冲突或目标；关键转折或信息点；出场人物（及作用）；章末走向或悬念钩子。", min, max, min)
		}
	}
	return rendered
}

func retryFeedback(short []int, min int, language string) string {
	values := make([]string, len(short))
	for i, number := range short {
		values[i] = fmt.Sprintf("%d", number)
	}
	if project.NormalizeLanguage(language) == project.LangEN {
		return fmt.Sprintf("\n\nIMPORTANT: Chapters %s have outlines shorter than %d characters. Expand them with concrete plot beats (scene, conflict, turning point, characters, ending hook) and resubmit the full JSON.", strings.Join(values, ", "), min)
	}
	return fmt.Sprintf("\n\n重要：第 %s 章大纲不足 %d 字。请补充具体情节（场景、冲突、转折、人物、章末钩子）后重新输出完整 JSON。", strings.Join(values, ", "), min)
}

func outlineSystemPrompt(language string) string {
	if project.NormalizeLanguage(language) == project.LangEN {
		return "You are a professional novel-planning editor. Output strict JSON exactly as requested — no extra prose, no markdown code fences."
	}
	return "你是一位专业的小说策划编辑。请严格按照要求的JSON格式输出，不要添加任何额外文字或markdown代码块标记。"
}
func shortChapters(chapters []chapter, minimum int) []int {
	var short []int
	for _, chapter := range chapters {
		if utf8.RuneCountInString(strings.TrimSpace(chapter.Outline)) < minimum {
			short = append(short, chapter.Num)
		}
	}
	return short
}
func cleanJSON(raw string) string {
	raw = strings.TrimSpace(raw)
	if strings.HasPrefix(raw, "```json") {
		raw = strings.TrimPrefix(raw, "```json")
	} else if strings.HasPrefix(raw, "```") {
		raw = strings.TrimPrefix(raw, "```")
	}
	raw = strings.TrimSuffix(raw, "```")
	return strings.TrimSpace(raw)
}
func truncateRunes(value string, max int) string {
	runes := []rune(value)
	if len(runes) <= max {
		return value
	}
	return string(runes[:max]) + "..."
}

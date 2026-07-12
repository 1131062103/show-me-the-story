package writing

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"showmethestory/internal/app/runtime"
	"showmethestory/internal/domain/project"
	"showmethestory/internal/ports"
)

var (
	ErrRevisionFeedbackRequired = errors.New("revision feedback is required")
	ErrRevisionTargetInvalid    = errors.New("chapter is not available for revision")
	ErrRevisionChapterMissing   = errors.New("chapter does not exist")
	ErrRevisionContentEmpty     = errors.New("chapter content is empty")
	ErrPolishRulesRequired      = errors.New("polish rules are required")
)

// StartRevise starts a revision of the chapter at the current writing cursor.
func (s *Service) StartRevise(feedback string) error {
	return s.startRevisionTask("chapter_revision", func(ctx context.Context) error { return s.Revise(ctx, feedback) })
}

// StartReviseSpecific starts a revision of the chapter identified by its stable
// chapter number. It intentionally does not move the writing cursor.
func (s *Service) StartReviseSpecific(chapterNum int, feedback string) error {
	return s.startRevisionTask("specific_chapter_revision", func(ctx context.Context) error {
		return s.ReviseSpecific(ctx, chapterNum, feedback)
	})
}

// StartPolish starts a chapter polish operation using caller-supplied enabled
// skill rules. Skill discovery belongs to the transport/application boundary.
func (s *Service) StartPolish(chapterIdx int, rules string) error {
	return s.startRevisionTask("chapter_polish", func(ctx context.Context) error { return s.Polish(ctx, chapterIdx, rules) })
}

func (s *Service) startRevisionTask(name string, work func(context.Context) error) error {
	if s.tasks == nil {
		return errors.New("task manager is required")
	}
	task, ok := s.tasks.Start(name)
	if !ok {
		return ErrTaskRunning
	}
	go func() { task.Done(work(task.Context()) == nil) }()
	return nil
}

// Revise revises the current review/writing chapter without advancing the
// cursor. Unlike generation, a review chapter is a valid revision target.
func (s *Service) Revise(ctx context.Context, feedback string) error {
	if strings.TrimSpace(feedback) == "" {
		return ErrRevisionFeedbackRequired
	}
	if err := s.validateRevisionDependencies(); err != nil {
		return err
	}
	snapshot := s.session.Snapshot()
	if snapshot.Project.Progress == nil || snapshot.Project.Progress.Phase != "writing" {
		return ErrWritingPhase
	}
	idx := snapshot.Project.Progress.CurrentChapterIndex
	if idx < 0 || idx >= len(snapshot.Project.Progress.Chapters) {
		return ErrInvalidCursor
	}
	status := snapshot.Project.Progress.Chapters[idx].Status
	if status != project.StatusReview && status != project.StatusWriting {
		return ErrRevisionTargetInvalid
	}
	return s.reviseAt(ctx, idx, feedback, false)
}

// ReviseSpecific revises one generated chapter, including an accepted chapter,
// and leaves every other chapter and CurrentChapterIndex unchanged.
func (s *Service) ReviseSpecific(ctx context.Context, chapterNum int, feedback string) error {
	if strings.TrimSpace(feedback) == "" {
		return ErrRevisionFeedbackRequired
	}
	if err := s.validateRevisionDependencies(); err != nil {
		return err
	}
	snapshot := s.session.Snapshot()
	if snapshot == nil || snapshot.Project == nil || snapshot.Project.Progress == nil {
		return runtime.ErrNoProject
	}
	idx := -1
	for i, chapter := range snapshot.Project.Progress.Chapters {
		if chapter.Num == chapterNum {
			idx = i
			break
		}
	}
	if idx < 0 {
		return fmt.Errorf("%w: %d", ErrRevisionChapterMissing, chapterNum)
	}
	chapter := snapshot.Project.Progress.Chapters[idx]
	if strings.TrimSpace(chapter.Content) == "" {
		return fmt.Errorf("%w: %d", ErrRevisionContentEmpty, chapterNum)
	}
	if chapter.Status == project.StatusWriting {
		return ErrRevisionTargetInvalid
	}
	return s.reviseAt(ctx, idx, feedback, true)
}

func (s *Service) validateRevisionDependencies() error {
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
	return nil
}

func (s *Service) reviseAt(ctx context.Context, idx int, feedback string, specific bool) error {
	snapshot := s.session.Snapshot()
	content, err := s.reviseContent(ctx, snapshot.Project, idx, feedback)
	if err != nil {
		return fmt.Errorf("revise chapter: %w", err)
	}
	if strings.TrimSpace(content) == "" {
		return ErrEmptyChapterDraft
	}
	summary, err := s.generateSummary(ctx, snapshot.Project.Config, content)
	if err != nil {
		return fmt.Errorf("summarize revised chapter: %w", err)
	}
	if strings.TrimSpace(summary) == "" {
		return errors.New("AI returned an empty chapter summary")
	}

	var exported project.Chapter
	if err := s.session.WithProject(ctx, func(value *project.Project) error {
		if value.Config == nil || value.Progress == nil || idx < 0 || idx >= len(value.Progress.Chapters) {
			return ErrRevisionTargetInvalid
		}
		if !specific && value.Progress.CurrentChapterIndex != idx {
			return ErrInvalidCursor
		}
		chapter := &value.Progress.Chapters[idx]
		if specific && chapter.Status == project.StatusWriting {
			return ErrRevisionTargetInvalid
		}
		chapter.Content, chapter.Summary, chapter.Status = content, strings.TrimSpace(summary), project.StatusReview
		removeChapterMemory(value.Progress, chapter.Num)
		s.syncForeshadows(ctx, value, idx)
		s.syncMemory(ctx, value, idx)
		exported = *chapter
		return nil
	}); err != nil {
		return fmt.Errorf("persist revised chapter: %w", err)
	}
	latest := s.session.Snapshot()
	if err := latest.Store.SaveChapterMarkdown(ctx, exported.Num, []byte(markdown(exported))); err != nil {
		return fmt.Errorf("export revised chapter markdown: %w", err)
	}
	s.publishProgress()
	return nil
}

func (s *Service) reviseContent(ctx context.Context, value *project.Project, idx int, feedback string) (string, error) {
	chapter := value.Progress.Chapters[idx]
	if quotes, clean := extractQuotedSentences(feedback); len(quotes) > 0 {
		content, err := s.reviseSegment(ctx, value, idx, quotes, clean)
		if err == nil {
			return mergeLockedParagraphs(chapter.Content, content, chapter.ParagraphLocks), nil
		}
		if !errors.Is(err, errSegmentFallback) {
			return "", err
		}
	}
	lockInstructions := paragraphLockInstructions(chapter.Content, chapter.ParagraphLocks, value.Config.Language)
	prompt := project.RenderPrompt(value.Config.Prompts.ChapterRevision, map[string]string{
		"ChapterNum": fmt.Sprint(chapter.Num), "ChapterTitle": chapter.Title, "CorePrompt": value.Progress.CorePrompt,
		"HistorySummary": history(value.Progress, idx, value.Config.Language), "WritingStyle": value.Config.Story.WritingStyle,
		"WritingPOV": value.Config.Story.WritingPOV, "CharacterContext": characterContext(value.Settings, chapter.Outline),
		"WorldviewContext": worldviewContext(value.Settings, chapter.Outline), "OriginalContent": chapter.Content,
		"UserFeedback": feedback, "ParagraphLocks": lockInstructions,
	})
	if lockInstructions != "" && !strings.Contains(value.Config.Prompts.ChapterRevision, "{{.ParagraphLocks}}") {
		prompt += "\n\n" + lockInstructions
	}
	content, err := s.streamRevision(ctx, idx, authorSystem(value), prompt)
	if err != nil {
		return "", err
	}
	return mergeLockedParagraphs(chapter.Content, stripRevisionMeta(content, value.Config.Language), chapter.ParagraphLocks), nil
}

var quoteLine = regexp.MustCompile(`(?m)^[ \t]*>[ \t]?(.+?)\s*$`)
var errSegmentFallback = errors.New("quoted segment revision unavailable")

func extractQuotedSentences(feedback string) ([]string, string) {
	matches := quoteLine.FindAllStringSubmatch(feedback, -1)
	if len(matches) == 0 {
		return nil, feedback
	}
	seen, quotes := map[string]bool{}, []string{}
	for _, match := range matches {
		quote := strings.TrimSpace(match[1])
		if quote != "" && !seen[quote] {
			seen[quote] = true
			quotes = append(quotes, quote)
		}
	}
	if len(quotes) == 0 {
		return nil, feedback
	}
	return quotes, strings.TrimSpace(quoteLine.ReplaceAllString(feedback, ""))
}

func findParagraphsContaining(content string, quotes []string) ([]int, []string, string, bool) {
	separator := "\n\n"
	paragraphs := strings.Split(content, separator)
	if len(paragraphs) <= 1 && strings.Contains(content, "\n") {
		separator, paragraphs = "\n", strings.Split(content, "\n")
	}
	matched := map[int]bool{}
	for _, quote := range quotes {
		found := -1
		for i, paragraph := range paragraphs {
			if strings.Contains(paragraph, quote) {
				found = i
				break
			}
		}
		if found < 0 {
			return nil, nil, "", false
		}
		matched[found] = true
	}
	indices := []int{}
	for i := range paragraphs {
		if matched[i] {
			indices = append(indices, i)
		}
	}
	return indices, paragraphs, separator, true
}

func (s *Service) reviseSegment(ctx context.Context, value *project.Project, idx int, quotes []string, feedback string) (string, error) {
	chapter := value.Progress.Chapters[idx]
	indices, paragraphs, separator, ok := findParagraphsContaining(chapter.Content, quotes)
	if !ok {
		return "", errSegmentFallback
	}
	original := make([]string, 0, len(indices))
	for _, index := range indices {
		original = append(original, paragraphs[index])
	}
	if strings.TrimSpace(feedback) == "" {
		if project.NormalizeLanguage(value.Config.Language) == project.LangEN {
			feedback = "Improve the selected passage while preserving its facts and plot."
		} else {
			feedback = "请在保持事实与情节不变的前提下优化所选段落。"
		}
	}
	prompt := project.RenderPrompt(value.Config.Prompts.ChapterSegmentRevision, map[string]string{
		"ChapterNum": fmt.Sprint(chapter.Num), "ChapterTitle": chapter.Title, "CorePrompt": value.Progress.CorePrompt,
		"HistorySummary": history(value.Progress, idx, value.Config.Language), "WritingStyle": value.Config.Story.WritingStyle,
		"WritingPOV": value.Config.Story.WritingPOV, "CharacterContext": characterContext(value.Settings, chapter.Outline),
		"WorldviewContext": worldviewContext(value.Settings, chapter.Outline), "QuotedText": strings.Join(quotes, "\n"),
		"SegmentOriginal": strings.Join(original, "\n\n"), "UserFeedback": feedback,
	})
	result, err := s.streamRevision(ctx, idx, authorSystem(value), prompt)
	if err != nil {
		return "", err
	}
	newParagraphs := splitContentParagraphs(stripRevisionMeta(result, value.Config.Language))
	if len(newParagraphs) != len(original) {
		return "", errSegmentFallback
	}
	for i, index := range indices {
		paragraphs[index] = newParagraphs[i]
	}
	return strings.Join(paragraphs, separator), nil
}

func (s *Service) streamRevision(ctx context.Context, idx int, system, prompt string) (string, error) {
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
	return result.Content, nil
}

func authorSystem(value *project.Project) string {
	if strings.TrimSpace(value.Progress.CorePrompt) != "" {
		return value.Progress.CorePrompt
	}
	return authorPrompt(value.Config.Language)
}

func splitContentParagraphs(content string) []string {
	parts := strings.Split(strings.TrimSpace(content), "\n\n")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		if part = strings.TrimSpace(part); part != "" {
			out = append(out, part)
		}
	}
	return out
}
func paragraphLockInstructions(content string, locks []int, language string) string {
	paragraphs := splitContentParagraphs(content)
	if len(paragraphs) == 0 || len(locks) == 0 {
		return ""
	}
	locked := map[int]bool{}
	for _, lock := range locks {
		if lock > 0 {
			locked[lock] = true
		}
	}
	var out strings.Builder
	if project.NormalizeLanguage(language) == project.LangEN {
		out.WriteString("[Locked paragraphs]\nKeep these paragraphs exactly unchanged; do not split, move, delete, or paraphrase them.\n")
	} else {
		out.WriteString("【锁定段落】\n以下段落必须逐字保持原样，不得拆分、移动、删除或转述。\n")
	}
	for i, paragraph := range paragraphs {
		if locked[i+1] {
			fmt.Fprintf(&out, "P%d: %s\n", i+1, paragraph)
		}
	}
	return strings.TrimSpace(out.String())
}
func mergeLockedParagraphs(original, revised string, locks []int) string {
	if len(locks) == 0 {
		return revised
	}
	originalParagraphs, revisedParagraphs := splitContentParagraphs(original), splitContentParagraphs(revised)
	if len(originalParagraphs) == 0 || len(revisedParagraphs) == 0 {
		return original
	}
	locked := map[int]bool{}
	for _, lock := range locks {
		if lock > 0 {
			locked[lock] = true
		}
	}
	if len(revisedParagraphs) < len(originalParagraphs) {
		merged := make([]string, len(originalParagraphs))
		for i := range originalParagraphs {
			if locked[i+1] || i >= len(revisedParagraphs) {
				merged[i] = originalParagraphs[i]
			} else {
				merged[i] = revisedParagraphs[i]
			}
		}
		return strings.Join(merged, "\n\n")
	}
	for i := range originalParagraphs {
		if locked[i+1] {
			revisedParagraphs[i] = originalParagraphs[i]
		}
	}
	return strings.Join(revisedParagraphs, "\n\n")
}

func stripRevisionMeta(content, language string) string {
	lines := strings.Split(strings.TrimSpace(content), "\n")
	for len(lines) > 0 {
		line := strings.TrimSpace(lines[0])
		lower := strings.ToLower(line)
		meta := strings.HasPrefix(lower, "chapter ") || strings.HasPrefix(lower, "revised chapter") || strings.HasPrefix(lower, "here is") || strings.HasPrefix(line, "第") && strings.Contains(line, "章") || strings.HasPrefix(line, "以下")
		if !meta {
			break
		}
		lines = lines[1:]
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
}

// Polish streams a full chapter polish, preserving its review status and
// markdown artifact. The caller must pass the content of enabled polish skills.
func (s *Service) Polish(ctx context.Context, chapterIdx int, rules string) error {
	if err := s.validateRevisionDependencies(); err != nil {
		return err
	}
	if strings.TrimSpace(rules) == "" {
		return ErrPolishRulesRequired
	}
	snapshot := s.session.Snapshot()
	if snapshot.Project.Progress == nil || chapterIdx < 0 || chapterIdx >= len(snapshot.Project.Progress.Chapters) {
		return ErrRevisionTargetInvalid
	}
	chapter := snapshot.Project.Progress.Chapters[chapterIdx]
	if strings.TrimSpace(chapter.Content) == "" {
		return ErrRevisionContentEmpty
	}
	var prompt string
	if project.NormalizeLanguage(snapshot.Project.Config.Language) == project.LangEN {
		prompt = fmt.Sprintf("Polish the chapter below according to these rules. Output only the full revised chapter prose, without titles or commentary.\n\n## Polish rules\n\n%s\n\n## Chapter\n\n%s", rules, chapter.Content)
	} else {
		prompt = fmt.Sprintf("请根据以下规则润色章节正文。只输出修改后的完整正文，不要标题或说明。\n\n## 润色规则\n\n%s\n\n## 待处理正文\n\n%s", rules, chapter.Content)
	}
	content, err := s.streamRevision(ctx, chapterIdx, authorSystem(snapshot.Project), prompt)
	if err != nil {
		return fmt.Errorf("polish chapter: %w", err)
	}
	content = stripRevisionMeta(content, snapshot.Project.Config.Language)
	if strings.TrimSpace(content) == "" {
		return ErrEmptyChapterDraft
	}
	var exported project.Chapter
	if err := s.session.WithProject(ctx, func(value *project.Project) error {
		if chapterIdx >= len(value.Progress.Chapters) {
			return ErrRevisionTargetInvalid
		}
		updated := &value.Progress.Chapters[chapterIdx]
		updated.Content, updated.Status = content, project.StatusReview
		removeChapterMemory(value.Progress, updated.Num)
		s.syncForeshadows(ctx, value, chapterIdx)
		s.syncMemory(ctx, value, chapterIdx)
		exported = *updated
		return nil
	}); err != nil {
		return fmt.Errorf("persist polished chapter: %w", err)
	}
	latest := s.session.Snapshot()
	if err := latest.Store.SaveChapterMarkdown(ctx, exported.Num, []byte(markdown(exported))); err != nil {
		return fmt.Errorf("export polished chapter markdown: %w", err)
	}
	s.publishProgress()
	return nil
}

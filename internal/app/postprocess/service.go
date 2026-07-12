// Package postprocess implements whole-book diagnosis and revision-roadmap workflows.
package postprocess

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"showmethestory/internal/app/runtime"
	"showmethestory/internal/app/writing"
	"showmethestory/internal/domain/project"
	"showmethestory/internal/ports"
)

const (
	volumeSplitRunes     = 150000
	defaultContextBudget = 300000
	diffExcerptRunes     = 500

	RoadmapTypeLogic      = "logic"
	RoadmapTypeTransition = "transition"
	RoadmapTypeStyle      = "style"
	RoadmapTypeRhythm     = "rhythm"
	RoadmapTypeDialogue   = "dialogue"
	RoadmapTypePolish     = "polish"

	RoadmapStatusPending = "pending"
	RoadmapStatusRunning = "running"
	RoadmapStatusDone    = "done"
	RoadmapStatusFailed  = "failed"
	RoadmapStatusSkipped = "skipped"
)

var (
	ErrTaskRunning          = errors.New("another task is already running")
	ErrNoAIClient           = errors.New("AI client is required")
	ErrModelRequired        = errors.New("AI model is required")
	ErrNoConfiguration      = errors.New("project configuration is required")
	ErrBookIncomplete       = errors.New("all chapters must be accepted and non-empty before whole-book processing")
	ErrReportsRequired      = errors.New("a diagnosis or consistency report is required")
	ErrRoadmapEmpty         = errors.New("roadmap parsing produced no items")
	ErrNoSelectedRoadmap    = errors.New("no selected pending roadmap items")
	ErrWritingServiceNeeded = errors.New("writing service is required to execute a roadmap")
	ErrPolishRulesRequired  = errors.New("polish roadmap items require enabled polish rules")
)

// Dependencies are the application boundaries required by this service. The
// caller supplies the assembled enabled polish rules; skill discovery remains
// at the transport/application composition boundary.
type Dependencies struct {
	Session             *runtime.ProjectSession
	Tasks               *runtime.TaskManager
	AI                  ports.AIClient
	Events              ports.EventPublisher
	Writing             *writing.Service
	Model               string
	MaxTokens           int
	ContextBudgetTokens int
	PolishRules         string
}

type Service struct {
	session                  *runtime.ProjectSession
	tasks                    *runtime.TaskManager
	ai                       ports.AIClient
	events                   ports.EventPublisher
	writing                  *writing.Service
	model                    string
	maxTokens, contextBudget int
	polishRules              string
}

func New(deps Dependencies) *Service {
	return &Service{session: deps.Session, tasks: deps.Tasks, ai: deps.AI, events: deps.Events, writing: deps.Writing,
		model: deps.Model, maxTokens: deps.MaxTokens, contextBudget: deps.ContextBudgetTokens, polishRules: deps.PolishRules}
}

func (s *Service) StartAnalyze() error     { return s.start("postprocess_diagnose", s.Analyze) }
func (s *Service) StartConsistency() error { return s.start("postprocess_consistency", s.Consistency) }
func (s *Service) StartRoadmap() error     { return s.start("postprocess_roadmap", s.BuildRoadmap) }
func (s *Service) StartExecute() error     { return s.start("postprocess_execute", s.Execute) }

func (s *Service) start(name string, work func(context.Context) error) error {
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

// Analyze checkpoints each completed analysis stage, so a later cancellation or
// provider failure does not discard a useful report.
func (s *Service) Analyze(ctx context.Context) error {
	value, err := s.snapshotForBook()
	if err != nil {
		return err
	}
	bundle := s.bundle(value.Project)
	if err := s.updatePostProcess(checkpointContext(ctx), func(pp *project.PostProcessState) {
		pp.BundleMode, pp.VolumeCount = bundle.Mode, bundle.VolumeCount
		pp.TotalBookRunes, pp.EstimatedTokens = bundle.TotalRunes, bundle.EstimatedTokens
	}); err != nil {
		return fmt.Errorf("checkpoint book material: %w", err)
	}

	diagnosis, err := s.diagnose(ctx, value.Project, bundle)
	if err != nil {
		return err
	}
	if err := s.updatePostProcess(checkpointContext(ctx), func(pp *project.PostProcessState) {
		pp.DiagnosisReport, pp.DiagnosedAt = diagnosis, time.Now().Format(time.RFC3339)
	}); err != nil {
		return fmt.Errorf("save diagnosis: %w", err)
	}
	s.publish("postprocess_report", map[string]string{"type": "diagnosis", "content": diagnosis})

	consistency, err := s.consistency(ctx, value.Project, bundle)
	if err != nil {
		return err
	}
	if err := s.updatePostProcess(checkpointContext(ctx), func(pp *project.PostProcessState) {
		pp.ConsistencyReport, pp.ConsistencyAt = consistency, time.Now().Format(time.RFC3339)
	}); err != nil {
		return fmt.Errorf("save consistency report: %w", err)
	}
	s.publish("postprocess_report", map[string]string{"type": "consistency", "content": consistency})

	items, err := s.roadmap(ctx, value.Project.Config, diagnosis, consistency)
	if err != nil {
		return err
	}
	if err := s.updatePostProcess(checkpointContext(ctx), func(pp *project.PostProcessState) {
		pp.Roadmap, pp.RoadmapAt = items, time.Now().Format(time.RFC3339)
	}); err != nil {
		return fmt.Errorf("save roadmap: %w", err)
	}
	s.publishRoadmap()
	return nil
}

func (s *Service) Consistency(ctx context.Context) error {
	value, err := s.snapshotForBook()
	if err != nil {
		return err
	}
	bundle := s.bundle(value.Project)
	report, err := s.consistency(ctx, value.Project, bundle)
	if err != nil {
		return err
	}
	if err := s.updatePostProcess(checkpointContext(ctx), func(pp *project.PostProcessState) {
		pp.BundleMode, pp.VolumeCount = bundle.Mode, bundle.VolumeCount
		pp.TotalBookRunes, pp.EstimatedTokens = bundle.TotalRunes, bundle.EstimatedTokens
		pp.ConsistencyReport, pp.ConsistencyAt = report, time.Now().Format(time.RFC3339)
	}); err != nil {
		return fmt.Errorf("save consistency report: %w", err)
	}
	s.publish("postprocess_report", map[string]string{"type": "consistency", "content": report})
	return nil
}

func (s *Service) BuildRoadmap(ctx context.Context) error {
	if err := s.validateBase(); err != nil {
		return err
	}
	snapshot := s.session.Snapshot()
	pp := snapshot.Project.PostProcess
	if pp == nil || (strings.TrimSpace(pp.DiagnosisReport) == "" && strings.TrimSpace(pp.ConsistencyReport) == "") {
		return ErrReportsRequired
	}
	items, err := s.roadmap(ctx, snapshot.Project.Config, pp.DiagnosisReport, pp.ConsistencyReport)
	if err != nil {
		return err
	}
	if err := s.updatePostProcess(checkpointContext(ctx), func(state *project.PostProcessState) {
		state.Roadmap, state.RoadmapAt = items, time.Now().Format(time.RFC3339)
	}); err != nil {
		return fmt.Errorf("save roadmap: %w", err)
	}
	s.publishRoadmap()
	return nil
}

func (s *Service) diagnose(ctx context.Context, value *project.Project, b bundle) (string, error) {
	full, note := b.FullText, ""
	if b.Mode == "summary_only" {
		full = "(The prose exceeds the context budget. Diagnose from the chapter-summary index; mark issues needing close rereading.)"
		if project.NormalizeLanguage(value.Config.Language) != project.LangEN {
			full = "（正文超出上下文预算；本轮仅根据章节摘要索引诊断，需精读的问题请明确标注。）"
		}
	}
	prompt := project.RenderPrompt(value.Config.Prompts.BookDiagnosis, map[string]string{"SettingsText": b.SettingsText, "SummaryIndex": b.SummaryIndex, "FullText": full, "ModeNote": note})
	return s.complete(ctx, diagnosisSystem(value.Config.Language), prompt)
}

func (s *Service) consistency(ctx context.Context, value *project.Project, b bundle) (string, error) {
	volumes := splitRunes(b.FullText, volumeSplitRunes)
	reports := make([]string, 0, len(volumes))
	for i, volume := range volumes {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		note := ""
		if len(volumes) > 1 {
			note = fmt.Sprintf("(Volume %d/%d. Audit only this volume; mark possible cross-volume contradictions.)", i+1, len(volumes))
		}
		if len(volumes) > 1 && project.NormalizeLanguage(value.Config.Language) != project.LangEN {
			note = fmt.Sprintf("（全书第 %d/%d 卷；只核查本卷，跨卷矛盾请标注“可能跨卷”。）", i+1, len(volumes))
		}
		prompt := project.RenderPrompt(value.Config.Prompts.BookConsistencyCheck, map[string]string{"SettingsText": b.SettingsText, "SummaryIndex": b.SummaryIndex, "FullText": volume, "VolumeNote": note})
		report, err := s.complete(ctx, consistencySystem(value.Config.Language), prompt)
		if err != nil {
			return "", err
		}
		if len(volumes) > 1 {
			report = fmt.Sprintf("### Volume %d/%d\n\n%s", i+1, len(volumes), report)
		}
		reports = append(reports, report)
	}
	return strings.Join(reports, "\n\n---\n\n"), nil
}

func (s *Service) roadmap(ctx context.Context, config *project.Config, diagnosis, consistency string) ([]project.RoadmapItem, error) {
	if strings.TrimSpace(diagnosis) == "" && strings.TrimSpace(consistency) == "" {
		return nil, ErrReportsRequired
	}
	prompt := project.RenderPrompt(config.Prompts.BookRoadmap, map[string]string{"DiagnosisReport": diagnosis, "ConsistencyReport": consistency})
	raw, err := s.complete(ctx, roadmapSystem(config.Language), prompt)
	if err != nil {
		return nil, err
	}
	items, err := parseRoadmap(raw)
	if err != nil {
		return nil, err
	}
	if len(items) == 0 {
		return nil, ErrRoadmapEmpty
	}
	sortRoadmap(items)
	return items, nil
}

// Execute processes selected pending work chapter-by-chapter. Each state change
// is independently persisted before continuing, preserving completed batches if
// an AI request fails or the task is stopped.
func (s *Service) Execute(ctx context.Context) error {
	if err := s.validateBase(); err != nil {
		return err
	}
	if s.writing == nil {
		return ErrWritingServiceNeeded
	}
	snapshot := s.session.Snapshot()
	if snapshot.Project.PostProcess == nil {
		return ErrNoSelectedRoadmap
	}
	batches := pendingBatches(snapshot.Project.PostProcess.Roadmap)
	if len(batches) == 0 {
		return ErrNoSelectedRoadmap
	}
	for _, batch := range batches {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := s.markBatch(checkpointContext(ctx), batch.indices, RoadmapStatusRunning, "", "", ""); err != nil {
			return err
		}

		current := s.session.Snapshot()
		idx, chapter := findChapter(current.Project.Progress, batch.chapterNum)
		if idx < 0 {
			if err := s.markBatch(checkpointContext(ctx), batch.indices, RoadmapStatusFailed, "", "", "chapter does not exist"); err != nil {
				return err
			}
			continue
		}
		original := excerpt(chapter.Content, diffExcerptRunes)
		items := roadmapItems(current.Project.PostProcess.Roadmap, batch.indices)
		polishOnly, feedback := mergeFeedback(items, current.Project.PostProcess.ExecuteOptions, s.polishRules != "")
		var runErr error
		if polishOnly {
			if strings.TrimSpace(s.polishRules) == "" {
				runErr = ErrPolishRulesRequired
			} else {
				runErr = s.writing.Polish(ctx, idx, s.polishRules)
			}
		} else {
			runErr = s.writing.ReviseSpecific(ctx, batch.chapterNum, feedback)
		}

		latest := s.session.Snapshot()
		_, changed := findChapter(latest.Project.Progress, batch.chapterNum)
		revised := ""
		if changed.Content != "" {
			revised = excerpt(changed.Content, diffExcerptRunes)
		}
		status, message := RoadmapStatusDone, ""
		if runErr != nil {
			status, message = RoadmapStatusFailed, runErr.Error()
		} else if original == revised {
			status = RoadmapStatusSkipped
		}
		if err := s.finishBatch(checkpointContext(ctx), batch.indices, status, original, revised, message, runErr == nil, batch.chapterNum); err != nil {
			return err
		}
		if runErr != nil && ctx.Err() != nil {
			return ctx.Err()
		}
	}
	return s.updatePostProcess(checkpointContext(ctx), func(pp *project.PostProcessState) { pp.LastExecuteAt = time.Now().Format(time.RFC3339) })
}

func (s *Service) finishBatch(ctx context.Context, indices []int, status, original, revised, message string, accept bool, chapterNum int) error {
	var completed []project.RoadmapItem
	err := s.session.WithProject(ctx, func(value *project.Project) error {
		if value.PostProcess == nil {
			value.PostProcess = project.DefaultPostProcessState()
		}
		if accept {
			idx, _ := findChapter(value.Progress, chapterNum)
			if idx >= 0 {
				value.Progress.Chapters[idx].Status = project.StatusAccepted
			}
		}
		for _, i := range indices {
			if i < 0 || i >= len(value.PostProcess.Roadmap) {
				continue
			}
			item := &value.PostProcess.Roadmap[i]
			item.Status, item.DiffOriginal, item.DiffRevised, item.Error = status, original, revised, message
			completed = append(completed, *item)
		}
		return nil
	})
	if err != nil {
		return err
	}
	for _, item := range completed {
		s.publish("postprocess_item_done", item)
	}
	s.publishUpdate()
	return nil
}

func (s *Service) markBatch(ctx context.Context, indices []int, status, original, revised, message string) error {
	err := s.session.WithProject(ctx, func(value *project.Project) error {
		if value.PostProcess == nil {
			value.PostProcess = project.DefaultPostProcessState()
		}
		for _, i := range indices {
			if i >= 0 && i < len(value.PostProcess.Roadmap) {
				value.PostProcess.Roadmap[i].Status, value.PostProcess.Roadmap[i].DiffOriginal, value.PostProcess.Roadmap[i].DiffRevised, value.PostProcess.Roadmap[i].Error = status, original, revised, message
			}
		}
		return nil
	})
	if err == nil {
		s.publishUpdate()
	}
	return err
}

func (s *Service) snapshotForBook() (*runtime.SelectedProject, error) {
	if err := s.validateBase(); err != nil {
		return nil, err
	}
	snapshot := s.session.Snapshot()
	if snapshot.Project.Progress == nil || !fullyAccepted(snapshot.Project.Progress) {
		return nil, ErrBookIncomplete
	}
	return snapshot, nil
}
func (s *Service) validateBase() error {
	if s.session == nil || s.session.Snapshot() == nil {
		return runtime.ErrNoProject
	}
	if s.ai == nil {
		return ErrNoAIClient
	}
	if strings.TrimSpace(s.model) == "" {
		return ErrModelRequired
	}
	if s.session.Snapshot().Project.Config == nil {
		return ErrNoConfiguration
	}
	return nil
}
func (s *Service) complete(ctx context.Context, system, prompt string) (string, error) {
	result, err := s.ai.Complete(ctx, ports.CompletionRequest{Model: s.model, Messages: []ports.Message{{Role: "system", Content: system}, {Role: "user", Content: prompt}}, MaxTokens: s.maxTokens})
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(result.Content) == "" {
		return "", errors.New("AI returned an empty postprocess result")
	}
	return strings.TrimSpace(result.Content), nil
}
func (s *Service) updatePostProcess(ctx context.Context, update func(*project.PostProcessState)) error {
	err := s.session.WithProject(ctx, func(value *project.Project) error {
		if value.PostProcess == nil {
			value.PostProcess = project.DefaultPostProcessState()
		}
		update(value.PostProcess)
		return nil
	})
	if err == nil {
		s.publishUpdate()
	}
	return err
}
func (s *Service) publish(kind string, value any) {
	if s.events != nil {
		s.events.Publish(kind, value)
	}
}
func (s *Service) publishUpdate() {
	if snapshot := s.session.Snapshot(); snapshot != nil && snapshot.Project != nil {
		s.publish("postprocess_update", snapshot.Project.PostProcess)
	}
}
func (s *Service) publishRoadmap() {
	if snapshot := s.session.Snapshot(); snapshot != nil && snapshot.Project != nil {
		s.publish("postprocess_roadmap", snapshot.Project.PostProcess)
		s.publishUpdate()
	}
}

// bundle is deliberately summary-aware: diagnosis sends full prose only when it
// fits the configured context window, but consistency always reads every volume.
type bundle struct {
	SettingsText, SummaryIndex, FullText, Mode string
	TotalRunes, EstimatedTokens, VolumeCount   int
}

func (s *Service) bundle(value *project.Project) bundle {
	settings, summaries, full := settingsText(value), summaryIndex(value.Progress), fullBook(value.Progress)
	total := utf8.RuneCountInString(settings) + utf8.RuneCountInString(summaries) + utf8.RuneCountInString(full)
	budget := s.contextBudget
	if budget <= 0 {
		budget = defaultContextBudget
	}
	mode := "full"
	if estimate(utf8.RuneCountInString(settings)+utf8.RuneCountInString(summaries))+estimate(utf8.RuneCountInString(full)) > budget*65/100 {
		mode = "summary_only"
	}
	count := (utf8.RuneCountInString(full) + volumeSplitRunes - 1) / volumeSplitRunes
	if count < 1 {
		count = 1
	}
	return bundle{SettingsText: settings, SummaryIndex: summaries, FullText: full, TotalRunes: total, EstimatedTokens: estimate(total), Mode: mode, VolumeCount: count}
}
func estimate(runes int) int { return runes * 3 / 2 }
func fullyAccepted(progress *project.Progress) bool {
	if progress == nil || len(progress.Chapters) == 0 {
		return false
	}
	for _, ch := range progress.Chapters {
		if ch.Status != project.StatusAccepted || strings.TrimSpace(ch.Content) == "" {
			return false
		}
	}
	return true
}
func fullBook(progress *project.Progress) string {
	var b strings.Builder
	title := progress.Title
	if title == "" {
		title = "Untitled"
	}
	fmt.Fprintf(&b, "《%s》", title)
	for _, ch := range progress.Chapters {
		if ch.Content != "" {
			fmt.Fprintf(&b, "\n\n第 %d 章　%s\n\n%s", ch.Num, ch.Title, ch.Content)
		}
	}
	return b.String()
}
func summaryIndex(progress *project.Progress) string {
	var b strings.Builder
	for _, ch := range progress.Chapters {
		if ch.Content == "" {
			continue
		}
		summary := ch.Summary
		if summary == "" {
			summary = "（无摘要）"
		}
		fmt.Fprintf(&b, "第%d章《%s》| %s\n", ch.Num, ch.Title, summary)
	}
	return b.String()
}
func settingsText(value *project.Project) string {
	c, p, settings := value.Config, value.Progress, value.Settings
	var b strings.Builder
	title := c.Story.Title
	if title == "" {
		title = p.Title
	}
	fmt.Fprintf(&b, "标题：%s\n类型：%s\n写作风格：%s\n梗概：%s\n", title, c.Story.Type, c.Story.WritingStyle, c.Story.StorySynopsis)
	if p.CorePrompt != "" {
		fmt.Fprintf(&b, "核心提示词：%s\n", p.CorePrompt)
	}
	if settings != nil {
		for _, ch := range settings.Characters {
			fmt.Fprintf(&b, "· %s\n  性格：%s\n  背景：%s\n  能力：%s\n", ch.Name, ch.Personality, ch.Background, ch.Abilities)
		}
		for _, item := range settings.Worldview {
			fmt.Fprintf(&b, "· %s（%s）：%s\n", item.Name, item.Category, item.Description)
		}
		for _, item := range settings.Organizations {
			fmt.Fprintf(&b, "· %s（%s）：%s\n", item.Name, item.Type, item.Description)
		}
	}
	for _, item := range p.Foreshadows {
		fmt.Fprintf(&b, "· %s [第%d章→第%d章] %s — %s\n", item.Name, item.PlantChapter, item.TargetChapter, item.Status, item.Description)
	}
	return b.String()
}
func splitRunes(value string, max int) []string {
	runes := []rune(value)
	if len(runes) <= max {
		return []string{value}
	}
	out := make([]string, 0, (len(runes)+max-1)/max)
	for start := 0; start < len(runes); start += max {
		end := start + max
		if end > len(runes) {
			end = len(runes)
		}
		out = append(out, string(runes[start:end]))
	}
	return out
}
func excerpt(value string, max int) string {
	runes := []rune(strings.TrimSpace(value))
	if len(runes) <= max {
		return string(runes)
	}
	return string(runes[:max]) + "…"
}

func parseRoadmap(raw string) ([]project.RoadmapItem, error) {
	raw = cleanJSON(raw)
	var wrapper struct {
		Items []roadmapEntry `json:"items"`
	}
	if err := json.Unmarshal([]byte(raw), &wrapper); err == nil && len(wrapper.Items) > 0 {
		return mapEntries(wrapper.Items), nil
	}
	var entries []roadmapEntry
	if err := json.Unmarshal([]byte(raw), &entries); err != nil {
		return nil, fmt.Errorf("parse roadmap JSON: %w", err)
	}
	return mapEntries(entries), nil
}

type roadmapEntry struct {
	ChapterNum int    `json:"chapter_num"`
	Type       string `json:"type"`
	Priority   string `json:"priority"`
	Feedback   string `json:"feedback"`
	Selected   *bool  `json:"selected"`
}

func mapEntries(entries []roadmapEntry) []project.RoadmapItem {
	out := make([]project.RoadmapItem, 0, len(entries))
	for i, item := range entries {
		if item.ChapterNum <= 0 || strings.TrimSpace(item.Feedback) == "" {
			continue
		}
		typ, priority := item.Type, item.Priority
		if typ == "" {
			typ = RoadmapTypeStyle
		}
		if priority == "" {
			priority = "P1"
		}
		selected := item.Selected == nil || *item.Selected
		out = append(out, project.RoadmapItem{ID: fmt.Sprintf("rm_%d", i+1), ChapterNum: item.ChapterNum, Type: typ, Priority: priority, Feedback: strings.TrimSpace(item.Feedback), Selected: selected, Status: RoadmapStatusPending})
	}
	return out
}
func cleanJSON(value string) string {
	value = strings.TrimSpace(value)
	value = strings.TrimPrefix(value, "```json")
	value = strings.TrimPrefix(value, "```")
	value = strings.TrimSuffix(value, "```")
	return strings.TrimSpace(value)
}
func sortRoadmap(items []project.RoadmapItem) {
	priority := func(v string) int {
		switch v {
		case "P0":
			return 0
		case "P1":
			return 1
		case "P2":
			return 2
		default:
			return 3
		}
	}
	typ := func(v string) int {
		switch v {
		case RoadmapTypeTransition:
			return 0
		case RoadmapTypeLogic:
			return 1
		case RoadmapTypePolish, RoadmapTypeStyle:
			return 5
		default:
			return 3
		}
	}
	sort.SliceStable(items, func(i, j int) bool {
		a, b := priority(items[i].Priority)*100000+items[i].ChapterNum*10+typ(items[i].Type), priority(items[j].Priority)*100000+items[j].ChapterNum*10+typ(items[j].Type)
		return a < b
	})
}

type batch struct {
	chapterNum int
	indices    []int
}

func pendingBatches(items []project.RoadmapItem) []batch {
	byChapter := map[int][]int{}
	order := []int{}
	for i, item := range items {
		if !item.Selected || item.Status != RoadmapStatusPending {
			continue
		}
		if _, ok := byChapter[item.ChapterNum]; !ok {
			order = append(order, item.ChapterNum)
		}
		byChapter[item.ChapterNum] = append(byChapter[item.ChapterNum], i)
	}
	out := make([]batch, 0, len(order))
	for _, num := range order {
		out = append(out, batch{num, byChapter[num]})
	}
	return out
}
func roadmapItems(items []project.RoadmapItem, indices []int) []project.RoadmapItem {
	out := make([]project.RoadmapItem, 0, len(indices))
	for _, i := range indices {
		if i >= 0 && i < len(items) {
			out = append(out, items[i])
		}
	}
	return out
}
func mergeFeedback(items []project.RoadmapItem, options *project.PostProcessExecuteOptions, hasPolish bool) (bool, string) {
	if len(items) == 0 {
		return false, ""
	}
	allPolish := true
	for _, item := range items {
		if item.Type != RoadmapTypePolish {
			allPolish = false
			break
		}
	}
	if allPolish {
		return true, ""
	}
	parts := make([]string, 0, len(items))
	needsPolish, nonLogic := options != nil && options.IncludePolish && hasPolish, false
	for i, item := range items {
		parts = append(parts, fmt.Sprintf("【意见 %d · %s/%s】\n%s", i+1, typeLabel(item.Type), item.Priority, strings.TrimSpace(item.Feedback)))
		if item.Type == RoadmapTypePolish {
			needsPolish = true
		}
		if item.Type != RoadmapTypeLogic {
			nonLogic = true
		}
	}
	result := strings.Join(parts, "\n\n")
	if needsPolish && nonLogic && hasPolish {
		result += "\n\n【附加文风要求】修改完成后顺带去除 AI 套话，对话口语化，不改变情节。"
	}
	return false, result
}
func typeLabel(value string) string {
	switch value {
	case RoadmapTypeLogic:
		return "逻辑"
	case RoadmapTypeTransition:
		return "衔接"
	case RoadmapTypeStyle:
		return "文风"
	case RoadmapTypeRhythm:
		return "节奏"
	case RoadmapTypeDialogue:
		return "对话"
	case RoadmapTypePolish:
		return "润色"
	default:
		return value
	}
}
func findChapter(progress *project.Progress, number int) (int, project.Chapter) {
	if progress == nil {
		return -1, project.Chapter{}
	}
	for i, ch := range progress.Chapters {
		if ch.Num == number {
			return i, ch
		}
	}
	return -1, project.Chapter{}
}
func checkpointContext(ctx context.Context) context.Context { return context.WithoutCancel(ctx) }
func diagnosisSystem(lang string) string {
	if project.NormalizeLanguage(lang) == project.LangEN {
		return "You are a senior fiction editor."
	}
	return "你是一位资深小说总编辑。"
}
func consistencySystem(lang string) string {
	if project.NormalizeLanguage(lang) == project.LangEN {
		return "You are a strict novel fact-checker."
	}
	return "你是一位严谨的小说事实核查员。"
}
func roadmapSystem(lang string) string {
	if project.NormalizeLanguage(lang) == project.LangEN {
		return "You are a senior novel editor. Return JSON only."
	}
	return "你是一位资深小说编辑。只输出 JSON。"
}

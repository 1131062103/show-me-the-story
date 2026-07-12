// Package foreshadow implements AI-assisted planning and outline consistency checks.
package foreshadow

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"showmethestory/internal/app/runtime"
	"showmethestory/internal/domain/project"
	"showmethestory/internal/ports"
)

var (
	ErrTaskRunning   = errors.New("another task is already running")
	ErrNoAIClient    = errors.New("AI client is required")
	ErrModelRequired = errors.New("AI model is required")
	ErrNoProject     = errors.New("project is required")
	ErrNoOutline     = errors.New("generate an outline first")
	ErrNoForeshadows = errors.New("no foreshadows to check")
)

type Suggestion struct {
	Name          string `json:"name"`
	Description   string `json:"description"`
	PlantChapter  int    `json:"plant_chapter"`
	TargetChapter int    `json:"target_chapter"`
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
}

func New(deps Dependencies) *Service {
	return &Service{session: deps.Session, tasks: deps.Tasks, ai: deps.AI, events: deps.Events, model: deps.Model, maxTokens: deps.MaxTokens}
}

func (s *Service) StartSuggest() error {
	return s.start("foreshadow_suggest", s.Suggest)
}

func (s *Service) StartOutlineCheck() error {
	return s.start("foreshadow_outline_check", s.CheckOutline)
}

func (s *Service) start(name string, run func(context.Context) error) error {
	if s.tasks == nil {
		return errors.New("task manager is required")
	}
	task, ok := s.tasks.Start(name)
	if !ok {
		return ErrTaskRunning
	}
	go func() { task.Done(run(task.Context()) == nil) }()
	return nil
}

func (s *Service) Suggest(ctx context.Context) error {
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
	if snapshot == nil || snapshot.Project == nil || snapshot.Project.Progress == nil || snapshot.Project.Config == nil {
		return ErrNoProject
	}
	if len(snapshot.Project.Progress.Chapters) == 0 {
		return ErrNoOutline
	}
	progress, config := snapshot.Project.Progress, snapshot.Project.Config
	prompt := project.RenderPrompt(config.Prompts.ForeshadowPlanning, map[string]string{
		"Title": title(config, progress), "CorePrompt": progress.CorePrompt,
		"StorySynopsis": synopsis(config, progress), "Outline": fullOutline(progress, config.Language),
	})
	result, err := s.ai.Complete(ctx, ports.CompletionRequest{Model: s.model, Messages: []ports.Message{{Role: "system", Content: planningPrompt(config.Language)}, {Role: "user", Content: prompt}}, MaxTokens: s.maxTokens})
	if err != nil {
		return err
	}
	var response struct {
		Foreshadows []Suggestion `json:"foreshadows"`
	}
	if err := json.Unmarshal([]byte(cleanJSON(result.Content)), &response); err != nil {
		return fmt.Errorf("parse foreshadow suggestions: %w", err)
	}
	if s.events != nil {
		s.events.Publish("foreshadow_suggestions", response.Foreshadows)
	}
	return nil
}

func (s *Service) CheckOutline(ctx context.Context) error {
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
	if snapshot == nil || snapshot.Project == nil || snapshot.Project.Progress == nil || snapshot.Project.Config == nil {
		return ErrNoProject
	}
	if len(snapshot.Project.Progress.Foreshadows) == 0 {
		return ErrNoForeshadows
	}
	progress, config := snapshot.Project.Progress, snapshot.Project.Config
	prompt := project.RenderPrompt(config.Prompts.ForeshadowOutlineConsistency, map[string]string{
		"Title": title(config, progress), "Outline": fullOutline(progress, config.Language),
		"Foreshadows": formatForeshadows(progress.Foreshadows), "AcceptedSummaries": acceptedSummaries(progress, config.Language),
	})
	result, err := s.ai.Complete(ctx, ports.CompletionRequest{Model: s.model, Messages: []ports.Message{{Role: "system", Content: checkerPrompt(config.Language)}, {Role: "user", Content: prompt}}, MaxTokens: s.maxTokens})
	if err != nil {
		return err
	}
	var report project.ForeshadowOutlineReport
	if err := json.Unmarshal([]byte(cleanJSON(result.Content)), &report); err != nil {
		return fmt.Errorf("parse foreshadow outline report: %w", err)
	}
	if err := s.session.WithProgress(ctx, func(value *project.Progress) error {
		if len(value.Foreshadows) == 0 {
			return ErrNoForeshadows
		}
		value.LastForeshadowOutlineReport = &report
		return nil
	}); err != nil {
		return err
	}
	if s.events != nil {
		if report.HasConflicts {
			s.events.Publish("foreshadow_outline_conflicts", &report)
		}
		s.publishProgress()
	}
	return nil
}

func (s *Service) publishProgress() {
	if snapshot := s.session.Snapshot(); snapshot != nil && snapshot.Project != nil {
		s.events.Publish("progress_update", snapshot.Project.Progress)
	}
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
func planningPrompt(language string) string {
	if project.NormalizeLanguage(language) == project.LangEN {
		return "You are a senior narrative architect. Output strict JSON exactly as requested — no extra prose, no markdown code fences."
	}
	return "你是一位资深的小说叙事架构师。请严格按照要求的JSON格式输出，不要添加任何额外文字或markdown代码块标记。"
}
func checkerPrompt(language string) string {
	if project.NormalizeLanguage(language) == project.LangEN {
		return "You are a strict narrative-consistency editor. Output strict JSON exactly as requested — no extra prose. When unsure, treat as no conflict."
	}
	return "你是一位严谨的小说叙事一致性编辑。请严格按照要求的JSON格式输出，不要添加任何额外文字。拿不准时视为无冲突。"
}
func cleanJSON(value string) string {
	value = strings.TrimSpace(value)
	value = strings.TrimPrefix(value, "```json")
	value = strings.TrimPrefix(value, "```")
	value = strings.TrimSuffix(value, "```")
	return strings.TrimSpace(value)
}
func fullOutline(progress *project.Progress, language string) string {
	var out strings.Builder
	for _, chapter := range progress.Chapters {
		if project.NormalizeLanguage(language) == project.LangEN {
			fmt.Fprintf(&out, "Chapter %d: %s\n%s\n", chapter.Num, chapter.Title, chapter.Outline)
		} else {
			fmt.Fprintf(&out, "第%d章《%s》：%s\n", chapter.Num, chapter.Title, chapter.Outline)
		}
	}
	return out.String()
}
func acceptedSummaries(progress *project.Progress, language string) string {
	var out strings.Builder
	for _, chapter := range progress.Chapters {
		if chapter.Status == project.StatusAccepted && chapter.Summary != "" {
			if project.NormalizeLanguage(language) == project.LangEN {
				fmt.Fprintf(&out, "Chapter %d \"%s\": %s\n", chapter.Num, chapter.Title, chapter.Summary)
			} else {
				fmt.Fprintf(&out, "第%d章《%s》：%s\n", chapter.Num, chapter.Title, chapter.Summary)
			}
		}
	}
	if out.Len() == 0 {
		if project.NormalizeLanguage(language) == project.LangEN {
			return "(no confirmed chapters yet)"
		}
		return "尚无已确认章节。"
	}
	return out.String()
}
func formatForeshadows(items []project.Foreshadow) string {
	if len(items) == 0 {
		return "无"
	}
	var out strings.Builder
	for _, item := range items {
		fmt.Fprintf(&out, "#%d [%s] %s\n   描述: %s\n   埋设于: 第%d章", item.ID, item.Status, item.Name, item.Description, item.PlantChapter)
		if item.TargetChapter > 0 {
			fmt.Fprintf(&out, "，预计回收: 第%d章", item.TargetChapter)
		}
		out.WriteString("\n")
		for _, event := range item.Events {
			fmt.Fprintf(&out, "   - 第%d章: %s\n", event.Chapter, event.Note)
		}
		if item.Resolution != "" {
			fmt.Fprintf(&out, "   回收方式: %s\n", item.Resolution)
		}
		out.WriteString("\n")
	}
	return out.String()
}

// RoadmapMarkdown returns the user-visible compatible Foreshadows.md content.
func RoadmapMarkdown(progress *project.Progress) string {
	title := progress.Title
	if title == "" {
		title = "未命名小说"
	}
	var out strings.Builder
	fmt.Fprintf(&out, "# 伏笔路线图 — 《%s》\n\n> 更新时间：%s\n\n", title, time.Now().Format("2006-01-02 15:04:05"))
	if len(progress.Foreshadows) == 0 {
		out.WriteString("当前尚无伏笔记录。\n")
		return out.String()
	}
	active, resolved, abandoned := 0, 0, 0
	for _, item := range progress.Foreshadows {
		switch item.Status {
		case project.ForeshadowPlanted, project.ForeshadowProgressing:
			active++
		case project.ForeshadowResolved:
			resolved++
		case project.ForeshadowAbandoned:
			abandoned++
		}
	}
	fmt.Fprintf(&out, "## 概览\n\n- 总计 **%d** 条 | 活跃 **%d** | 已回收 **%d** | 已放弃 **%d**\n\n", len(progress.Foreshadows), active, resolved, abandoned)
	if warnings := roadmapWarnings(progress); len(warnings) > 0 {
		out.WriteString("## 超期告警\n\n")
		out.WriteString(strings.Join(warnings, "；"))
		out.WriteString("\n\n")
	}
	if maxChapter := roadmapMaxChapter(progress); maxChapter > 0 {
		out.WriteString("## 按章节时间线\n\n")
		for chapterNum := 1; chapterNum <= maxChapter; chapterNum++ {
			var lines []string
			for _, item := range progress.Foreshadows {
				if item.PlantChapter == chapterNum {
					lines = append(lines, fmt.Sprintf("- 🔵 **#%d %s** — 埋设（%s）", item.ID, item.Name, statusLabel(item.Status)))
				}
				if item.TargetChapter == chapterNum {
					lines = append(lines, fmt.Sprintf("- 🎯 **#%d %s** — 预计回收（%s）", item.ID, item.Name, statusLabel(item.Status)))
				}
				for _, event := range item.Events {
					if event.Chapter == chapterNum {
						lines = append(lines, fmt.Sprintf("- 📌 **#%d %s** — %s", item.ID, item.Name, event.Note))
					}
				}
			}
			if len(lines) > 0 {
				fmt.Fprintf(&out, "### 第 %d 章\n\n%s\n\n", chapterNum, strings.Join(lines, "\n"))
			}
		}
	}
	out.WriteString("## 伏笔详情\n\n")
	for _, item := range progress.Foreshadows {
		fmt.Fprintf(&out, "### #%d %s [%s]\n\n%s\n\n- 埋设章节：第 **%d** 章\n", item.ID, item.Name, statusLabel(item.Status), item.Description, item.PlantChapter)
		if item.TargetChapter > 0 {
			fmt.Fprintf(&out, "- 预计回收：第 **%d** 章\n", item.TargetChapter)
		}
		for _, event := range item.Events {
			fmt.Fprintf(&out, "  - 第 %d 章：%s\n", event.Chapter, event.Note)
		}
		if item.Resolution != "" {
			fmt.Fprintf(&out, "- 回收方式：%s\n", item.Resolution)
		}
		out.WriteString("\n")
	}
	return out.String()
}
func roadmapWarnings(progress *project.Progress) []string {
	current := progress.CurrentChapterIndex + 1
	var warnings []string
	for _, item := range progress.Foreshadows {
		if item.Status != project.ForeshadowResolved && item.Status != project.ForeshadowAbandoned && item.TargetChapter > 0 && current > item.TargetChapter+3 {
			warnings = append(warnings, fmt.Sprintf("伏笔 #%d \"%s\" 已超过预计回收章节 %d 章以上", item.ID, item.Name, item.TargetChapter))
		}
	}
	return warnings
}
func roadmapMaxChapter(progress *project.Progress) int {
	max := 0
	for _, chapter := range progress.Chapters {
		if chapter.Num > max {
			max = chapter.Num
		}
	}
	for _, item := range progress.Foreshadows {
		if item.PlantChapter > max {
			max = item.PlantChapter
		}
		if item.TargetChapter > max {
			max = item.TargetChapter
		}
		for _, event := range item.Events {
			if event.Chapter > max {
				max = event.Chapter
			}
		}
	}
	return max
}
func statusLabel(status project.ForeshadowStatus) string {
	switch status {
	case project.ForeshadowPlanted:
		return "已埋设"
	case project.ForeshadowProgressing:
		return "推进中"
	case project.ForeshadowResolved:
		return "已回收"
	case project.ForeshadowAbandoned:
		return "已放弃"
	}
	return string(status)
}

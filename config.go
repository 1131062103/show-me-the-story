package main

import (
	"encoding/json"
	"fmt"
	"os"
)

type APIConfig struct {
	APIKey              string       `json:"api_key"`
	BaseURL             string       `json:"base_url"`
	URLStrict           bool         `json:"url_strict,omitempty"` // true = 不自动插入 /v1，仅补 /chat/completions
	Model               string       `json:"model"`
	MaxTokens           int          `json:"max_tokens,omitempty"` // 0 = 模型默认；Agent 调用建议 ≥ 8192
	HTTPTimeoutSeconds  int          `json:"http_timeout_seconds"`
	ContextBudgetTokens int          `json:"context_budget_tokens"` // 全书优化上下文预算，默认 900000
	ActiveProfileID     string       `json:"active_profile_id,omitempty"`
	Profiles            []APIProfile `json:"profiles,omitempty"`
}

type APIProfile struct {
	ID                  string `json:"id"`
	Name                string `json:"name"`
	APIKey              string `json:"api_key"`
	BaseURL             string `json:"base_url"`
	URLStrict           bool   `json:"url_strict,omitempty"`
	Model               string `json:"model"`
	MaxTokens           int    `json:"max_tokens,omitempty"`
	HTTPTimeoutSeconds  int    `json:"http_timeout_seconds"`
	ContextBudgetTokens int    `json:"context_budget_tokens"`
}

type Config struct {
	Language    string        `json:"language"` // "zh" 或 "en"，影响 AI 提示词与生成内容；旧项目缺省视为 "zh"
	Story       StoryConfig   `json:"story"`
	Prompts     PromptsConfig `json:"prompts"`
	SkillConfig *SkillConfig  `json:"skill_config,omitempty"`
}

const (
	LangZH = "zh"
	LangEN = "en"
)

// NormalizeLanguage returns "zh" / "en"; unknown values fall back to "zh".
func NormalizeLanguage(lang string) string {
	switch lang {
	case LangEN, "en-US", "en-GB":
		return LangEN
	default:
		return LangZH
	}
}

type StoryConfig struct {
	Type                  string `json:"type"`
	Title                 string `json:"title"`
	ChapterCount          int    `json:"chapter_count"`
	TargetWordsPerChapter int    `json:"target_words_per_chapter"`
	WritingStyle          string `json:"writing_style"`
	WritingPOV            string `json:"writing_pov"` // 叙述视角，如第一人称女主、第三人称限知等
	StorySynopsis         string `json:"story_synopsis"`
}

type PromptsConfig struct {
	OutlineGeneration             string `json:"outline_generation"`
	ChapterWriting                string `json:"chapter_writing"`
	ChapterRevision               string `json:"chapter_revision"`
	ChapterSegmentRevision        string `json:"chapter_segment_revision"`
	ChapterSummary                string `json:"chapter_summary"`
	FactCheck                     string `json:"fact_check"`
	OutlineRevision               string `json:"outline_revision"`
	ForeshadowPlanning            string `json:"foreshadow_planning"`
	ForeshadowUpdate              string `json:"foreshadow_update"`
	ContentAnalysis               string `json:"content_analysis"`
	ContinuationOutlineGeneration string `json:"continuation_outline_generation"`
	SettingsReconciliation        string `json:"settings_reconciliation"`
	TransitionSmoothing           string `json:"transition_smoothing"`
	OutlineConsistencyCheck       string `json:"outline_consistency_check"`
	ForeshadowOutlineConsistency  string `json:"foreshadow_outline_consistency"`
	OutlineCharacterCheck         string `json:"outline_character_check"`
	WritingConflictAnalysis       string `json:"writing_conflict_analysis"`
	BookDiagnosis                 string `json:"book_diagnosis"`
	BookConsistencyCheck          string `json:"book_consistency_check"`
	BookRoadmap                   string `json:"book_roadmap"`
	MemoryUpdate                  string `json:"memory_update"`
}

func DefaultAPIConfig() *APIConfig {
	cfg := &APIConfig{
		HTTPTimeoutSeconds:  300,
		ContextBudgetTokens: defaultContextBudgetTokens,
	}
	normalizeAPIConfig(cfg)
	return cfg
}

func normalizeAPIConfig(cfg *APIConfig) {
	if cfg == nil {
		return
	}
	if cfg.HTTPTimeoutSeconds <= 0 {
		cfg.HTTPTimeoutSeconds = 300
	}
	if cfg.ContextBudgetTokens <= 0 {
		cfg.ContextBudgetTokens = defaultContextBudgetTokens
	}
	if len(cfg.Profiles) == 0 {
		cfg.ActiveProfileID = "default"
		cfg.Profiles = []APIProfile{profileFromAPIConfig(cfg, "default", "Default")}
		return
	}
	syncActiveProfileFromTopLevel(cfg)

	found := false
	for i := range cfg.Profiles {
		normalizeAPIProfile(&cfg.Profiles[i])
		if cfg.Profiles[i].ID == cfg.ActiveProfileID {
			applyAPIProfile(cfg, cfg.Profiles[i])
			found = true
		}
	}
	if !found {
		cfg.ActiveProfileID = cfg.Profiles[0].ID
		applyAPIProfile(cfg, cfg.Profiles[0])
	}
}

func syncActiveProfileFromTopLevel(cfg *APIConfig) {
	if cfg.ActiveProfileID == "" {
		if len(cfg.Profiles) > 0 {
			cfg.ActiveProfileID = cfg.Profiles[0].ID
		} else {
			cfg.ActiveProfileID = "default"
		}
	}
	for i := range cfg.Profiles {
		if cfg.Profiles[i].ID == cfg.ActiveProfileID {
			name := cfg.Profiles[i].Name
			cfg.Profiles[i] = profileFromAPIConfig(cfg, cfg.ActiveProfileID, name)
			normalizeAPIProfile(&cfg.Profiles[i])
			return
		}
	}
	cfg.Profiles = append(cfg.Profiles, profileFromAPIConfig(cfg, cfg.ActiveProfileID, cfg.ActiveProfileID))
}

func normalizeAPIProfile(p *APIProfile) {
	if p.ID == "" {
		p.ID = "default"
	}
	if p.Name == "" {
		p.Name = p.ID
	}
	if p.HTTPTimeoutSeconds <= 0 {
		p.HTTPTimeoutSeconds = 300
	}
	if p.ContextBudgetTokens <= 0 {
		p.ContextBudgetTokens = defaultContextBudgetTokens
	}
}

func profileFromAPIConfig(cfg *APIConfig, id, name string) APIProfile {
	return APIProfile{
		ID:                  id,
		Name:                name,
		APIKey:              cfg.APIKey,
		BaseURL:             cfg.BaseURL,
		URLStrict:           cfg.URLStrict,
		Model:               cfg.Model,
		MaxTokens:           cfg.MaxTokens,
		HTTPTimeoutSeconds:  cfg.HTTPTimeoutSeconds,
		ContextBudgetTokens: cfg.ContextBudgetTokens,
	}
}

func applyAPIProfile(cfg *APIConfig, p APIProfile) {
	cfg.APIKey = p.APIKey
	cfg.BaseURL = p.BaseURL
	cfg.URLStrict = p.URLStrict
	cfg.Model = p.Model
	cfg.MaxTokens = p.MaxTokens
	cfg.HTTPTimeoutSeconds = p.HTTPTimeoutSeconds
	cfg.ContextBudgetTokens = p.ContextBudgetTokens
}

func DefaultConfig() *Config {
	return DefaultConfigForLang(LangZH)
}

func DefaultConfigForLang(lang string) *Config {
	lang = NormalizeLanguage(lang)
	cfg := &Config{
		Language: lang,
		Story: StoryConfig{
		ChapterCount:          12,
		TargetWordsPerChapter: 5000,
		},
		SkillConfig: &SkillConfig{
			EnabledSkills: make(map[string]bool),
		},
	}
	cfg.Prompts.applyDefaults(lang)
	return cfg
}

func LoadAPIConfig(path string) (*APIConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			cfg := DefaultAPIConfig()
			if saveErr := saveAPIConfig(path, cfg); saveErr != nil {
				return nil, fmt.Errorf("创建默认API配置文件失败: %w", saveErr)
			}
			return cfg, nil
		}
		return nil, fmt.Errorf("读取API配置文件失败: %w", err)
	}

	var cfg APIConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("解析API配置文件失败: %w", err)
	}

	shouldDetectContextWindow := cfg.ContextBudgetTokens <= 0
	normalizeAPIConfig(&cfg)
	if shouldDetectContextWindow {
		// 先尝试从 API 获取模型的上下文窗口
		if window := FetchModelContextWindow(&cfg); window > 0 {
			cfg.ContextBudgetTokens = window
			syncActiveProfileFromTopLevel(&cfg)
		} else {
			cfg.ContextBudgetTokens = defaultContextBudgetTokens
		}
	}

	return &cfg, nil
}

func saveAPIConfig(path string, cfg *APIConfig) error {
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	return writeFileAtomic(path, data)
}

func LoadConfig(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			cfg := DefaultConfig()
			if saveErr := saveConfig(path, cfg); saveErr != nil {
				return nil, fmt.Errorf("创建默认配置文件失败: %w", saveErr)
			}
			return cfg, nil
		}
		return nil, fmt.Errorf("读取配置文件失败: %w", err)
	}

	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("解析配置文件失败: %w", err)
	}

	if cfg.Story.ChapterCount <= 0 {
		cfg.Story.ChapterCount = 12
	}
	if cfg.Story.TargetWordsPerChapter <= 0 {
		cfg.Story.TargetWordsPerChapter = 5000
	}

	cfg.Language = NormalizeLanguage(cfg.Language)

	// 保存 applyDefaults 前的 prompts 状态，用于判断是否有字段被填充
	oldPrompts := cfg.Prompts
	cfg.Prompts.applyDefaults(cfg.Language)
	// 如果有字段被填充（从空变为默认值），写回磁盘
	if cfg.Prompts != oldPrompts {
		saveConfig(path, &cfg)
	}

	if cfg.SkillConfig == nil {
		cfg.SkillConfig = &SkillConfig{
			EnabledSkills: make(map[string]bool),
		}
	} else {
		cfg.SkillConfig.applyDefaults()
	}

	return &cfg, nil
}

func saveConfig(path string, cfg *Config) error {
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	return writeFileAtomic(path, data)
}

// applyDefaults fills empty fields with the language-specific defaults.
// Existing non-empty fields are NEVER overwritten — this is what makes
// old projects (with persisted Chinese prompts) keep working after upgrade.
func (p *PromptsConfig) applyDefaults(lang string) {
	defaults := DefaultPromptsForLang(lang)
	if p.OutlineGeneration == "" {
		p.OutlineGeneration = defaults.OutlineGeneration
	}
	if p.ChapterWriting == "" {
		p.ChapterWriting = defaults.ChapterWriting
	}
	if p.ChapterRevision == "" {
		p.ChapterRevision = defaults.ChapterRevision
	}
	if p.ChapterSegmentRevision == "" {
		p.ChapterSegmentRevision = defaults.ChapterSegmentRevision
	}
	if p.ChapterSummary == "" {
		p.ChapterSummary = defaults.ChapterSummary
	}
	if p.FactCheck == "" {
		p.FactCheck = defaults.FactCheck
	}
	if p.OutlineRevision == "" {
		p.OutlineRevision = defaults.OutlineRevision
	}
	if p.ForeshadowPlanning == "" {
		p.ForeshadowPlanning = defaults.ForeshadowPlanning
	}
	if p.ForeshadowUpdate == "" {
		p.ForeshadowUpdate = defaults.ForeshadowUpdate
	}
	if p.ContentAnalysis == "" {
		p.ContentAnalysis = defaults.ContentAnalysis
	}
	if p.ContinuationOutlineGeneration == "" {
		p.ContinuationOutlineGeneration = defaults.ContinuationOutlineGeneration
	}
	if p.SettingsReconciliation == "" {
		p.SettingsReconciliation = defaults.SettingsReconciliation
	}
	if p.TransitionSmoothing == "" {
		p.TransitionSmoothing = defaults.TransitionSmoothing
	}
	if p.OutlineConsistencyCheck == "" {
		p.OutlineConsistencyCheck = defaults.OutlineConsistencyCheck
	}
	if p.ForeshadowOutlineConsistency == "" {
		p.ForeshadowOutlineConsistency = defaults.ForeshadowOutlineConsistency
	}
	if p.OutlineCharacterCheck == "" {
		p.OutlineCharacterCheck = defaults.OutlineCharacterCheck
	}
	if p.WritingConflictAnalysis == "" {
		p.WritingConflictAnalysis = defaults.WritingConflictAnalysis
	}
	if p.BookDiagnosis == "" {
		p.BookDiagnosis = defaults.BookDiagnosis
	}
	if p.BookConsistencyCheck == "" {
		p.BookConsistencyCheck = defaults.BookConsistencyCheck
	}
	if p.BookRoadmap == "" {
		p.BookRoadmap = defaults.BookRoadmap
	}
	if p.MemoryUpdate == "" {
		p.MemoryUpdate = defaults.MemoryUpdate
	}
}

func DefaultPromptsForLang(lang string) PromptsConfig {
	if NormalizeLanguage(lang) == LangEN {
		return DefaultPromptsEN
	}
	return DefaultPromptsZH
}

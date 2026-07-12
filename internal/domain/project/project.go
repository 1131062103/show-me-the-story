package project

const (
	LangZH = "zh"
	LangEN = "en"
)

func NormalizeLanguage(language string) string {
	switch language {
	case LangEN, "en-US", "en-GB":
		return LangEN
	default:
		return LangZH
	}
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
type SkillConfig struct {
	EnabledSkills map[string]bool `json:"enabled_skills"`
}

// ProtectedStoryFields cannot be silently replaced by AI-generated content.
var ProtectedStoryFields = []string{"type", "title", "writing_style", "writing_pov", "story_synopsis"}

type ConfigFieldChange struct {
	Field    string `json:"field"`
	Current  string `json:"current"`
	Proposed string `json:"proposed"`
	Source   string `json:"source"`
	Reason   string `json:"reason,omitempty"`
}

type PendingConfigChanges struct {
	Changes []ConfigFieldChange `json:"changes"`
}
type Config struct {
	Language    string        `json:"language"`
	Story       StoryConfig   `json:"story"`
	Prompts     PromptsConfig `json:"prompts"`
	SkillConfig *SkillConfig  `json:"skill_config,omitempty"`
}

// DefaultConfig returns a new Chinese-language project configuration.
func DefaultConfig() *Config {
	return DefaultConfigForLang(LangZH)
}

// DefaultConfigForLang returns a new project configuration with language-specific prompts.
func DefaultConfigForLang(language string) *Config {
	language = NormalizeLanguage(language)
	return &Config{
		Language: language,
		Story: StoryConfig{
			ChapterCount:          12,
			TargetWordsPerChapter: 5000,
		},
		Prompts: DefaultPromptsForLang(language),
		SkillConfig: &SkillConfig{
			EnabledSkills: map[string]bool{},
		},
	}
}

func (c *Config) Normalize() {
	c.Language = NormalizeLanguage(c.Language)
	if c.Story.ChapterCount <= 0 {
		c.Story.ChapterCount = 12
	}
	if c.Story.TargetWordsPerChapter <= 0 {
		c.Story.TargetWordsPerChapter = 5000
	}
	c.Prompts.applyDefaults(c.Language)
	if c.SkillConfig == nil {
		c.SkillConfig = &SkillConfig{EnabledSkills: map[string]bool{}}
	} else if c.SkillConfig.EnabledSkills == nil {
		c.SkillConfig.EnabledSkills = map[string]bool{}
	}
}

type Character struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Age         string `json:"age,omitempty"`
	Appearance  string `json:"appearance,omitempty"`
	Personality string `json:"personality,omitempty"`
	Background  string `json:"background,omitempty"`
	Motivation  string `json:"motivation,omitempty"`
	Abilities   string `json:"abilities,omitempty"`
	Notes       string `json:"notes,omitempty"`
}
type WorldviewEntry struct {
	ID          string `json:"id"`
	Category    string `json:"category"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Tags        string `json:"tags,omitempty"`
}
type Organization struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Type        string   `json:"type"`
	Description string   `json:"description"`
	Members     []string `json:"members,omitempty"`
}
type Relation struct {
	ID         string `json:"id"`
	SourceID   string `json:"source_id"`
	SourceType string `json:"source_type"`
	TargetID   string `json:"target_id"`
	TargetType string `json:"target_type"`
	Label      string `json:"label"`
}
type ProjectSettings struct {
	Characters    []Character      `json:"characters"`
	Worldview     []WorldviewEntry `json:"worldview"`
	Organizations []Organization   `json:"organizations"`
	Relations     []Relation       `json:"relations"`
}

type RoadmapItem struct {
	ID           string `json:"id"`
	ChapterNum   int    `json:"chapter_num"`
	Type         string `json:"type"`
	Priority     string `json:"priority"`
	Feedback     string `json:"feedback"`
	Selected     bool   `json:"selected"`
	Status       string `json:"status"`
	DiffOriginal string `json:"diff_original,omitempty"`
	DiffRevised  string `json:"diff_revised,omitempty"`
	Error        string `json:"error,omitempty"`
}
type PostProcessExecuteOptions struct {
	RunSmoothTransitionsFirst bool `json:"run_smooth_transitions_first"`
	IncludePolish             bool `json:"include_polish"`
}
type PostProcessState struct {
	DiagnosisReport   string                     `json:"diagnosis_report,omitempty"`
	ConsistencyReport string                     `json:"consistency_report,omitempty"`
	Roadmap           []RoadmapItem              `json:"roadmap,omitempty"`
	BundleMode        string                     `json:"bundle_mode,omitempty"`
	VolumeCount       int                        `json:"volume_count,omitempty"`
	TotalBookRunes    int                        `json:"total_book_runes,omitempty"`
	EstimatedTokens   int                        `json:"estimated_tokens,omitempty"`
	DiagnosedAt       string                     `json:"diagnosed_at,omitempty"`
	ConsistencyAt     string                     `json:"consistency_at,omitempty"`
	RoadmapAt         string                     `json:"roadmap_at,omitempty"`
	ExecuteOptions    *PostProcessExecuteOptions `json:"execute_options,omitempty"`
	LastExecuteAt     string                     `json:"last_execute_at,omitempty"`
}

func DefaultPostProcessState() *PostProcessState {
	return &PostProcessState{ExecuteOptions: &PostProcessExecuteOptions{RunSmoothTransitionsFirst: true}}
}

type Project struct {
	Name        string
	Config      *Config
	Progress    *Progress
	Settings    *ProjectSettings
	PostProcess *PostProcessState
}

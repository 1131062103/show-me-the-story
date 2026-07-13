// Package project contains the persisted domain models for a story project.
package project

const (
	StatusPending  = "pending"
	StatusWriting  = "writing"
	StatusReview   = "review"
	StatusAccepted = "accepted"
)

// Chapter holds the persisted workflow state and the chapter text loaded from
// its Markdown document. Content and Summary are intentionally not serialized
// into progress.json; Chapter_XX.md is their single on-disk source of truth.
type Chapter struct {
	Num            int    `json:"num"`
	Title          string `json:"title"`
	Outline        string `json:"outline"`
	OutlineLocked  bool   `json:"outline_locked,omitempty"`
	ParagraphLocks []int  `json:"paragraph_locks,omitempty"`
	Content        string `json:"content,omitempty"`
	Summary        string `json:"summary,omitempty"`
	Status         string `json:"status"`
}

type ForeshadowStatus string

const (
	ForeshadowPlanted     ForeshadowStatus = "planted"
	ForeshadowProgressing ForeshadowStatus = "progressing"
	ForeshadowResolved    ForeshadowStatus = "resolved"
	ForeshadowAbandoned   ForeshadowStatus = "abandoned"
)

type ForeshadowEvent struct {
	Chapter int    `json:"chapter"`
	Note    string `json:"note"`
}

type Foreshadow struct {
	ID            int               `json:"id"`
	Name          string            `json:"name"`
	Description   string            `json:"description"`
	PlantChapter  int               `json:"plant_chapter"`
	TargetChapter int               `json:"target_chapter"`
	Status        ForeshadowStatus  `json:"status"`
	Events        []ForeshadowEvent `json:"events"`
	Resolution    string            `json:"resolution"`
}

type ForeshadowOutlineConflict struct {
	ForeshadowID   int    `json:"foreshadow_id"`
	ForeshadowName string `json:"foreshadow_name"`
	ConflictType   string `json:"conflict_type"`
	Description    string `json:"description"`
	SuggestedFix   string `json:"suggested_fix"`
}

type ForeshadowOutlineReport struct {
	HasConflicts bool                        `json:"has_conflicts"`
	Conflicts    []ForeshadowOutlineConflict `json:"conflicts"`
	Summary      string                      `json:"summary"`
}

type ConflictActionOption struct {
	ID          string `json:"id"`
	Label       string `json:"label"`
	Description string `json:"description"`
}

type WritingConflict struct {
	ChapterIndex     int                    `json:"chapter_index"`
	ChapterNum       int                    `json:"chapter_num"`
	ChapterTitle     string                 `json:"chapter_title"`
	Issues           []string               `json:"issues"`
	Summary          string                 `json:"summary"`
	RootCause        string                 `json:"root_cause"`
	Reconcilable     bool                   `json:"reconcilable"`
	SuggestedActions []ConflictActionOption `json:"suggested_actions"`
}

type MemoryEntry struct {
	ID       int    `json:"id"`
	Content  string `json:"content"`
	Category string `json:"category"`
	Chapter  int    `json:"chapter"`
	Position int    `json:"position"`
}

// StoryConfig is the configuration snapshot written into progress.json.
type StoryConfig struct {
	Type                  string `json:"type"`
	Title                 string `json:"title"`
	ChapterCount          int    `json:"chapter_count"`
	TargetWordsPerChapter int    `json:"target_words_per_chapter"`
	WritingStyle          string `json:"writing_style"`
	WritingPOV            string `json:"writing_pov"`
	StorySynopsis         string `json:"story_synopsis"`
}

type OutlineCharacterSuggestion struct {
	Name        string `json:"name"`
	ChapterNum  int    `json:"chapter_num"`
	Description string `json:"description"`
	Role        string `json:"role,omitempty"`
}

type OutlineCharacterReport struct {
	HasSuggestions bool                         `json:"has_suggestions"`
	Suggestions    []OutlineCharacterSuggestion `json:"suggestions"`
	Summary        string                       `json:"summary"`
}

// Progress mirrors the established progress.json schema. Keep field names and
// JSON tags stable so existing projects can be opened without migration.
type Progress struct {
	Phase                       string                   `json:"phase"`
	Title                       string                   `json:"title"`
	CorePrompt                  string                   `json:"core_prompt"`
	StorySynopsis               string                   `json:"story_synopsis"`
	Chapters                    []Chapter                `json:"chapters"`
	CurrentChapterIndex         int                      `json:"current_chapter_index"`
	StoryConfigSnapshot         *StoryConfig             `json:"story_config_snapshot,omitempty"`
	Foreshadows                 []Foreshadow             `json:"foreshadows,omitempty"`
	LastForeshadowOutlineReport *ForeshadowOutlineReport `json:"last_foreshadow_outline_report,omitempty"`
	LastOutlineCharacterReport  *OutlineCharacterReport  `json:"last_outline_character_report,omitempty"`
	PendingWritingConflict      *WritingConflict         `json:"pending_writing_conflict,omitempty"`
	MemoryEntries               []MemoryEntry            `json:"memory_entries,omitempty"`
	MemoryMaxTokens             int                      `json:"memory_max_tokens,omitempty"`
}

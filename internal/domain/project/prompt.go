package project

import "strings"

// RenderPrompt substitutes literal {{.KeyName}} placeholders with their values.
func RenderPrompt(template string, data map[string]string) string {
	result := template
	for key, value := range data {
		result = strings.ReplaceAll(result, "{{."+key+"}}", value)
	}
	return result
}

// DefaultPromptsForLang returns the language-specific built-in prompt set.
func DefaultPromptsForLang(language string) PromptsConfig {
	if NormalizeLanguage(language) == LangEN {
		return DefaultPromptsEN
	}
	return DefaultPromptsZH
}

// applyDefaults fills only empty prompt overrides. Persisted non-empty values are
// deliberately retained so users' prompt customizations survive upgrades.
func (p *PromptsConfig) applyDefaults(language string) {
	defaults := DefaultPromptsForLang(language)
	for _, field := range []struct {
		value        *string
		defaultValue string
	}{
		{&p.OutlineGeneration, defaults.OutlineGeneration},
		{&p.ChapterWriting, defaults.ChapterWriting},
		{&p.ChapterRevision, defaults.ChapterRevision},
		{&p.ChapterSegmentRevision, defaults.ChapterSegmentRevision},
		{&p.ChapterSummary, defaults.ChapterSummary},
		{&p.FactCheck, defaults.FactCheck},
		{&p.OutlineRevision, defaults.OutlineRevision},
		{&p.ForeshadowPlanning, defaults.ForeshadowPlanning},
		{&p.ForeshadowUpdate, defaults.ForeshadowUpdate},
		{&p.ContentAnalysis, defaults.ContentAnalysis},
		{&p.ContinuationOutlineGeneration, defaults.ContinuationOutlineGeneration},
		{&p.SettingsReconciliation, defaults.SettingsReconciliation},
		{&p.TransitionSmoothing, defaults.TransitionSmoothing},
		{&p.OutlineConsistencyCheck, defaults.OutlineConsistencyCheck},
		{&p.ForeshadowOutlineConsistency, defaults.ForeshadowOutlineConsistency},
		{&p.OutlineCharacterCheck, defaults.OutlineCharacterCheck},
		{&p.WritingConflictAnalysis, defaults.WritingConflictAnalysis},
		{&p.BookDiagnosis, defaults.BookDiagnosis},
		{&p.BookConsistencyCheck, defaults.BookConsistencyCheck},
		{&p.BookRoadmap, defaults.BookRoadmap},
		{&p.MemoryUpdate, defaults.MemoryUpdate},
	} {
		if *field.value == "" {
			*field.value = field.defaultValue
		}
	}
}

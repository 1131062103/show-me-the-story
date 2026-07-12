package project

import (
	"strings"
	"testing"
)

func TestDefaultPromptsForLangSelectsBilingualTemplates(t *testing.T) {
	zh := DefaultPromptsForLang(LangZH)
	if zh.OutlineGeneration != DefaultPromptsZH.OutlineGeneration {
		t.Fatal("Chinese prompt selection did not return Chinese outline template")
	}
	if !strings.Contains(zh.OutlineGeneration, "【故事类型】{{.StoryType}}") {
		t.Fatalf("Chinese outline template changed unexpectedly: %q", zh.OutlineGeneration)
	}

	en := DefaultPromptsForLang("en-US")
	if en.ChapterWriting != DefaultPromptsEN.ChapterWriting {
		t.Fatal("English prompt selection did not return English chapter template")
	}
	if !strings.Contains(en.ChapterWriting, "Write the prose for chapter {{.ChapterNum}} of the novel \"{{.Title}}\".") {
		t.Fatalf("English chapter template changed unexpectedly: %q", en.ChapterWriting)
	}
}

func TestRenderPromptReplacesLiteralPlaceholders(t *testing.T) {
	template := DefaultPromptsForLang(LangEN).ChapterWriting
	rendered := RenderPrompt(template, map[string]string{
		"ChapterNum":  "7",
		"Title":       "The Test Novel",
		"TargetWords": "3000",
	})

	if !strings.Contains(rendered, "Write the prose for chapter 7 of the novel \"The Test Novel\".") {
		t.Fatalf("literal placeholders were not rendered: %q", rendered)
	}
	if !strings.Contains(rendered, "(target 3000)") {
		t.Fatalf("later literal placeholder was not rendered: %q", rendered)
	}
	if !strings.Contains(rendered, "{{.CorePrompt}}") {
		t.Fatal("unprovided literal placeholder should remain unchanged")
	}
}

func TestConfigNormalizationFillsOnlyEmptyPromptOverrides(t *testing.T) {
	config := &Config{
		Language: LangEN,
		Prompts: PromptsConfig{
			ChapterWriting: "custom chapter prompt",
		},
	}
	config.Normalize()

	if config.Prompts.ChapterWriting != "custom chapter prompt" {
		t.Fatalf("custom prompt was overwritten: %q", config.Prompts.ChapterWriting)
	}
	if config.Prompts.OutlineGeneration != DefaultPromptsEN.OutlineGeneration {
		t.Fatal("empty prompt did not receive the selected language default")
	}
	if config.Story.ChapterCount != 12 || config.Story.TargetWordsPerChapter != 5000 {
		t.Fatalf("story defaults = %#v, want default chapter count and word target", config.Story)
	}
}

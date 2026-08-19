package story

import (
	"os"
	"path/filepath"
	"showmethestory/internal/config"
	"testing"
)

func writeSkillFile(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestLoadExternalSkills(t *testing.T) {
	progDir := t.TempDir()

	writeSkillFile(t, filepath.Join(progDir, "skills"), "pacing.md", `---
name: 爽文节奏
description: 适用于都市玄幻题材的节奏控制技巧
lang: zh
---
正文内容
`)

	writeSkillFile(t, filepath.Join(progDir, "skills"), "with-id.md", `---
id: custom-id
name: 自定义
description: english description here
---
body
`)

	writeSkillFile(t, filepath.Join(progDir, "skills"), "ignore.txt", "not a skill")

	skills := LoadExternalSkills(progDir)
	if len(skills) != 2 {
		t.Fatalf("expected 2 external skills, got %d", len(skills))
	}

	byID := map[string]Skill{}
	for _, s := range skills {
		byID[s.ID] = s
		if s.Source != SkillSourceExternal {
			t.Errorf("source of %s = %q, want %q", s.ID, s.Source, SkillSourceExternal)
		}
	}

	pacing, ok := byID[ExternalSkillIDPrefix+"pacing"]
	if !ok {
		t.Fatalf("missing filename-fallback skill, got %v", byID)
	}
	if pacing.Name != "爽文节奏" {
		t.Errorf("name = %q, want 爽文节奏", pacing.Name)
	}
	if pacing.Lang != "zh" {
		t.Errorf("lang = %q, want zh", pacing.Lang)
	}

	if _, ok := byID[ExternalSkillIDPrefix+"custom-id"]; !ok {
		t.Errorf("missing id-based skill with ext prefix")
	}
}

func TestLoadExternalSkillsMissingDir(t *testing.T) {
	progDir := t.TempDir()
	if skills := LoadExternalSkills(progDir); len(skills) != 0 {
		t.Fatalf("expected no skills, got %d", len(skills))
	}
}

func TestSkillMatchesMessage(t *testing.T) {
	skill := Skill{
		Name:        "人性化去AI味",
		Description: "去除中文 AI 写作痕迹，优化句式与用词",
	}

	cases := []struct {
		msg   string
		match bool
	}{
		{"请用人性化去AI味处理这一章", true}, // name verbatim
		{"这章 AI 痕迹太重，帮我去除写作痕迹优化一下", true}, // description bigram overlap >= 2
		{"帮我生成第五章大纲", false},               // unrelated
		{"", false},                          // empty message
	}
	for _, c := range cases {
		if got := SkillMatchesMessage(skill, c.msg); got != c.match {
			t.Errorf("SkillMatchesMessage(%q) = %v, want %v", c.msg, got, c.match)
		}
	}
}

func TestFilterSkillsByMessage(t *testing.T) {
	skills := []Skill{
		{Name: "爽文节奏", Description: "都市玄幻节奏控制"},
		{Name: "古代文言", Description: "古代背景的文言风格"},
	}
	matched := FilterSkillsByMessage(skills, "我想写都市玄幻")
	if len(matched) != 1 || matched[0].Name != "爽文节奏" {
		t.Fatalf("expected only 爽文节奏 matched, got %+v", matched)
	}
}

func TestGetEnabledSkillsBySource(t *testing.T) {
	sc := &config.SkillConfig{EnabledSkills: map[string]bool{
		"a": true,
		"b": true,
		"c": false,
	}}
	skills := []Skill{
		{ID: "a", Source: "builtin"},
		{ID: "b", Source: SkillSourceExternal},
		{ID: "c", Source: "builtin"},
	}
	builtin := GetEnabledSkillsBySource(skills, sc, "builtin")
	if len(builtin) != 1 || builtin[0].ID != "a" {
		t.Fatalf("builtin filter = %+v, want [a]", builtin)
	}
	external := GetEnabledSkillsBySource(skills, sc, SkillSourceExternal)
	if len(external) != 1 || external[0].ID != "b" {
		t.Fatalf("external filter = %+v, want [b]", external)
	}
}
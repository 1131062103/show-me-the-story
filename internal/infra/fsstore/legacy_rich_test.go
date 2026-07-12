package fsstore

import (
	"context"
	"path/filepath"
	"testing"
)

func TestLoadProjectSupportsRichLegacyFixture(t *testing.T) {
	fixture := filepath.Join("testdata", "legacy-project")
	store, err := NewAtProjectDir(fixture)
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := store.LoadProject(context.Background())
	if err != nil {
		t.Fatalf("LoadProject() error = %v", err)
	}
	if loaded.Config.Language != "zh" || loaded.Config.Story.WritingPOV != "第三人称限知" || !loaded.Config.SkillConfig.EnabledSkills["dialogue-polish"] {
		t.Fatalf("config was not loaded compatibly: %#v", loaded.Config)
	}
	if len(loaded.Progress.Chapters) != 2 || loaded.Progress.Chapters[0].ParagraphLocks[1] != 3 || loaded.Progress.PendingWritingConflict == nil {
		t.Fatalf("progress was not loaded compatibly: %#v", loaded.Progress)
	}
	if loaded.Settings.Characters[0].Name != "沈遥" || loaded.Settings.Relations[0].TargetID != "o_1" {
		t.Fatalf("settings were not loaded compatibly: %#v", loaded.Settings)
	}
	if loaded.PostProcess.Roadmap[0].Priority != "high" || !loaded.PostProcess.ExecuteOptions.IncludePolish {
		t.Fatalf("postprocess was not loaded compatibly: %#v", loaded.PostProcess)
	}
}

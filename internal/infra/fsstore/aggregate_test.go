package fsstore

import (
	"context"
	"os"
	"testing"

	"showmethestory/internal/domain/project"
)

func TestStoreLoadAndSaveProjectAggregate(t *testing.T) {
	store, err := New(t.TempDir(), "novel")
	if err != nil {
		t.Fatal(err)
	}
	value := &project.Project{Config: &project.Config{Language: "en"}, Settings: &project.ProjectSettings{Characters: []project.Character{{ID: "c_1", Name: "Name"}}}, PostProcess: project.DefaultPostProcessState(), Progress: &project.Progress{Title: "Title"}}
	if err := store.SaveProject(context.Background(), value); err != nil {
		t.Fatal(err)
	}
	got, err := store.LoadProject(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != "novel" || got.Progress.Title != "Title" || got.Config.Language != "en" || got.Settings.Characters[0].Name != "Name" {
		t.Fatalf("loaded aggregate = %#v", got)
	}
	if _, err := os.Stat(store.SettingsPath()); err != nil {
		t.Fatal(err)
	}
}

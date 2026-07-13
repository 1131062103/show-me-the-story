package fsstore

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"showmethestory/internal/domain/project"
	"showmethestory/internal/ports"
)

func TestStoreUsesEstablishedProjectLayout(t *testing.T) {
	store, err := New(t.TempDir(), "my-story")
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	wantDir := filepath.Join(filepath.Dir(filepath.Dir(store.ProjectDir())), "storys", "my-story")
	if store.ProjectDir() != wantDir {
		t.Errorf("ProjectDir() = %q, want %q", store.ProjectDir(), wantDir)
	}
	if got, want := store.ProgressPath(), filepath.Join(wantDir, "progress.json"); got != want {
		t.Errorf("ProgressPath() = %q, want %q", got, want)
	}
	if got, want := store.ConfigPath(), filepath.Join(wantDir, "config.json"); got != want {
		t.Errorf("ConfigPath() = %q, want %q", got, want)
	}
	if got, want := store.SettingsPath(), filepath.Join(wantDir, "settings.json"); got != want {
		t.Errorf("SettingsPath() = %q, want %q", got, want)
	}
	if got, want := store.PostProcessPath(), filepath.Join(wantDir, "postprocess.json"); got != want {
		t.Errorf("PostProcessPath() = %q, want %q", got, want)
	}
	if got, want := store.SessionsDir(), filepath.Join(wantDir, "sessions"); got != want {
		t.Errorf("SessionsDir() = %q, want %q", got, want)
	}
	if got, want := store.ChapterMarkdownPath(3), filepath.Join(wantDir, "Chapter_03.md"); got != want {
		t.Errorf("ChapterMarkdownPath() = %q, want %q", got, want)
	}
}

func TestStorePersistsCompatibleProgressAtomically(t *testing.T) {
	store, err := New(t.TempDir(), "story")
	if err != nil {
		t.Fatal(err)
	}
	var _ ports.ProjectStore = store

	want := &project.Progress{
		Phase:               "writing",
		Title:               "A title",
		CurrentChapterIndex: 1,
		Chapters: []project.Chapter{{
			Num: 2, Title: "Chapter", Outline: "outline", OutlineLocked: true,
			ParagraphLocks: []int{1, 3}, Content: "prose", Summary: "summary", Status: project.StatusReview,
		}},
		Foreshadows: []project.Foreshadow{{
			ID: 1, Name: "clue", PlantChapter: 1, TargetChapter: 2, Status: project.ForeshadowPlanted,
		}},
	}
	chapterMarkdown := "# 第 2 章: Chapter\n\n> **本章摘要**：summary\n\n---\n\nprose"
	if err := store.SaveChapterMarkdown(context.Background(), 2, []byte(chapterMarkdown)); err != nil {
		t.Fatalf("SaveChapterMarkdown() error = %v", err)
	}
	if err := store.SaveProgress(context.Background(), want); err != nil {
		t.Fatalf("SaveProgress() error = %v", err)
	}

	got, err := store.LoadProgress(context.Background())
	if err != nil {
		t.Fatalf("LoadProgress() error = %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("loaded progress = %#v, want %#v", got, want)
	}
	data, err := os.ReadFile(store.ProgressPath())
	if err != nil {
		t.Fatal(err)
	}
	if string(data) == "" || strings.Contains(string(data), `"content"`) || strings.Contains(string(data), `"summary"`) {
		t.Fatalf("progress.json contains chapter text: %s", data)
	}
	markdown, err := store.LoadChapterMarkdown(context.Background(), 2)
	if err != nil || string(markdown) != chapterMarkdown {
		t.Fatalf("chapter markdown = %q, %v; SaveProgress must not overwrite chapter documents", markdown, err)
	}

	entries, err := os.ReadDir(store.ProjectDir())
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if entry.Name() != "progress.json" && entry.Name() != "Chapter_02.md" {
			t.Errorf("unexpected file left in project directory: %s", entry.Name())
		}
	}
}

func TestStoreMissingProgressAndChapterMarkdown(t *testing.T) {
	store, err := New(t.TempDir(), "story")
	if err != nil {
		t.Fatal(err)
	}
	progress, err := store.LoadProgress(context.Background())
	if err != nil || progress != nil {
		t.Fatalf("LoadProgress() = (%#v, %v), want (nil, nil)", progress, err)
	}
	_, err = store.LoadChapterMarkdown(context.Background(), 1)
	if !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("LoadChapterMarkdown() error = %v, want not exist", err)
	}
}

func TestStoreRejectsTraversalProjectName(t *testing.T) {
	for _, name := range []string{"", ".", "..", "../outside", "a/b", `a\\b`} {
		if _, err := New(t.TempDir(), name); err == nil {
			t.Errorf("New(%q) succeeded, want error", name)
		}
	}
}

func TestStoreHonorsCanceledContext(t *testing.T) {
	store, err := New(t.TempDir(), "story")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := store.SaveProgress(ctx, &project.Progress{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("SaveProgress() error = %v, want context canceled", err)
	}
}

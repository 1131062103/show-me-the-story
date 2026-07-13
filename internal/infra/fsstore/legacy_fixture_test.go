package fsstore

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestStoreRoundTripsLegacyProgressFixtureSemantically(t *testing.T) {
	projectDir := copyLegacyProjectFixture(t)
	store, err := NewAtProjectDir(projectDir)
	if err != nil {
		t.Fatalf("NewAtProjectDir() error = %v", err)
	}

	progress, err := store.LoadProgress(context.Background())
	if err != nil {
		t.Fatalf("LoadProgress() error = %v", err)
	}
	if progress == nil {
		t.Fatal("LoadProgress() returned nil progress")
	}
	if progress.Title != "雾港来信" || len(progress.Chapters) != 2 || !progress.Chapters[0].OutlineLocked {
		t.Fatalf("legacy chapter data was not decoded: %#v", progress)
	}
	if len(progress.Chapters[0].ParagraphLocks) != 2 || len(progress.Foreshadows) != 1 || len(progress.MemoryEntries) != 1 {
		t.Fatalf("legacy narrative state was not decoded: %#v", progress)
	}
	if progress.LastForeshadowOutlineReport == nil || progress.LastOutlineCharacterReport == nil || progress.PendingWritingConflict == nil {
		t.Fatalf("legacy reports or conflict were not decoded: %#v", progress)
	}

	chapter, err := store.LoadChapterMarkdown(context.Background(), 1)
	if err != nil {
		t.Fatalf("LoadChapterMarkdown() error = %v", err)
	}
	if string(chapter) != readFixtureFile(t, "Chapter_01.md") {
		t.Fatal("legacy chapter markdown was not preserved")
	}
}

func copyLegacyProjectFixture(t *testing.T) string {
	t.Helper()
	destination := filepath.Join(t.TempDir(), "legacy-project")
	if err := os.MkdirAll(destination, 0o755); err != nil {
		t.Fatalf("create fixture destination: %v", err)
	}
	for _, name := range []string{"progress.json", "Chapter_01.md"} {
		if err := os.WriteFile(filepath.Join(destination, name), []byte(readFixtureFile(t, name)), 0o644); err != nil {
			t.Fatalf("copy fixture file %s: %v", name, err)
		}
	}
	return destination
}

func readFixtureFile(t *testing.T, name string) string {
	t.Helper()
	return readFixtureTestFile(t, filepath.Join("testdata", "legacy-project", name))
}

func readFixtureTestFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(data)
}

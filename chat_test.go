package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestMigrateLegacyChatSessionsDir(t *testing.T) {
	baseDir := filepath.Join(t.TempDir(), "sessions")
	legacyDir := filepath.Join(baseDir, "sessions")
	if err := os.MkdirAll(legacyDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(legacyDir, "index.json"), []byte(`{"sessions":[]}`), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(legacyDir, "s_1.json"), []byte(`{"id":"s_1"}`), 0644); err != nil {
		t.Fatal(err)
	}

	if err := migrateLegacyChatSessionsDir(baseDir); err != nil {
		t.Fatalf("migrate legacy sessions: %v", err)
	}

	for _, name := range []string{"index.json", "s_1.json"} {
		if _, err := os.Stat(filepath.Join(baseDir, name)); err != nil {
			t.Fatalf("expected migrated %s: %v", name, err)
		}
	}
	if _, err := os.Stat(legacyDir); !os.IsNotExist(err) {
		t.Fatalf("expected legacy dir removed, got err=%v", err)
	}
}

func TestChatSessionsDirUsesProvidedDir(t *testing.T) {
	baseDir := filepath.Join(t.TempDir(), "sessions")
	if got := chatSessionsDir(baseDir); got != baseDir {
		t.Fatalf("chatSessionsDir() = %q, want %q", got, baseDir)
	}
}

func TestDeleteOrphanEmptyChatSessions(t *testing.T) {
	baseDir := filepath.Join(t.TempDir(), "sessions")
	if err := os.MkdirAll(baseDir, 0755); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(filepath.Join(baseDir, "s_empty.json"), []byte(`{"id":"s_empty","messages":[]}`), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(baseDir, "s_used.json"), []byte(`{"id":"s_used","messages":[{"role":"user","content":"hi","timestamp":"now"}]}`), 0644); err != nil {
		t.Fatal(err)
	}

	idx := &ChatSessionIndex{Sessions: []ChatSessionMeta{{ID: "s_used", MsgCount: 1}}}
	if err := deleteOrphanEmptyChatSessions(baseDir, idx); err != nil {
		t.Fatalf("delete orphan empty sessions: %v", err)
	}

	if _, err := os.Stat(filepath.Join(baseDir, "s_empty.json")); !os.IsNotExist(err) {
		t.Fatalf("expected orphan empty session removed, got err=%v", err)
	}
	if _, err := os.Stat(filepath.Join(baseDir, "s_used.json")); err != nil {
		t.Fatalf("expected indexed session kept: %v", err)
	}
}

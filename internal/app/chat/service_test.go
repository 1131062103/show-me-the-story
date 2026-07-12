package chat

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestStorePersistsLegacyCompatibleSessionAndIndex(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir)
	session := &Session{ID: "s_1", Title: "hello", CreatedAt: "2026-01-01T00:00:00Z", UpdatedAt: "2026-01-01T00:01:00Z", Messages: []Message{{Role: "user", Content: "hello", Timestamp: "2026-01-01T00:00:00Z"}, {Role: "assistant", ToolCalls: []ToolCall{{Name: "read_outline", Arguments: []byte("{}")}}, Timestamp: "2026-01-01T00:01:00Z"}}}
	if err := store.Save(context.Background(), session); err != nil {
		t.Fatal(err)
	}
	loaded, err := store.Load(context.Background(), session.ID)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Title != session.Title || len(loaded.Messages) != 2 || loaded.Messages[1].ToolCalls[0].Name != "read_outline" {
		t.Fatalf("loaded session = %#v", loaded)
	}
	index, err := store.LoadIndex(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(index.Sessions) != 1 || index.Sessions[0].MsgCount != 2 {
		t.Fatalf("index = %#v", index)
	}
	data, err := os.ReadFile(filepath.Join(dir, "s_1.json"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) == "" || !contains(string(data), `"tool_calls"`) {
		t.Fatalf("persisted session lost legacy fields: %s", data)
	}
	if err := store.Delete(context.Background(), session.ID); err != nil {
		t.Fatal(err)
	}
	index, err = store.LoadIndex(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(index.Sessions) != 0 {
		t.Fatalf("index after delete = %#v", index)
	}
}
func TestValidSessionIDRejectsPaths(t *testing.T) {
	for _, id := range []string{"", "../x", "a/b", "a:b", "a.b"} {
		if ValidSessionID(id) {
			t.Fatalf("accepted %q", id)
		}
	}
}
func contains(value, part string) bool {
	return len(value) >= len(part) && (value == part || stringContains(value, part))
}
func stringContains(value, part string) bool {
	for i := range value {
		if len(value)-i >= len(part) && value[i:i+len(part)] == part {
			return true
		}
	}
	return false
}

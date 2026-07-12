package skills

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadBuiltinReadsRootSkillsDirectory(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "root.md"), []byte("---\nid: root-skill\nname: Root Skill\ncategory: writing\nlang: en\n---\nFollow this rule."), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(dir, "references"), 0755); err != nil {
		t.Fatal(err)
	}

	items := LoadBuiltin(dir)
	if len(items) != 1 {
		t.Fatalf("LoadBuiltin() returned %d skills, want 1", len(items))
	}
	if got := items[0]; got.ID != "root-skill" || got.Source != "builtin" || got.Content != "Follow this rule." {
		t.Fatalf("LoadBuiltin() skill = %#v", got)
	}
}

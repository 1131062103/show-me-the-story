package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadAPIProfilesCreatesDefault(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "api.json")
	p, err := LoadAPIProfiles(path)
	if err != nil {
		t.Fatalf("LoadAPIProfiles: %v", err)
	}
	if p.Active != "default" {
		t.Fatalf("active = %q, want default", p.Active)
	}
	if _, ok := p.Profiles["default"]; !ok {
		t.Fatalf("default profile missing: %+v", p.Profiles)
	}
	cfg := p.ActiveConfig()
	if cfg.HTTPTimeoutSeconds != DefaultHTTPTimeoutSeconds {
		t.Fatalf("timeout = %d, want %d", cfg.HTTPTimeoutSeconds, DefaultHTTPTimeoutSeconds)
	}
}

func TestLoadAPIProfilesMigratesLegacy(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "api.json")
	legacy := `{"api_key":"k","base_url":"https://x.example.com/v1","url_strict":true,"model":"m","max_tokens":8192}`
	if err := os.WriteFile(path, []byte(legacy), 0644); err != nil {
		t.Fatal(err)
	}
	p, err := LoadAPIProfiles(path)
	if err != nil {
		t.Fatalf("LoadAPIProfiles: %v", err)
	}
	cfg := p.ActiveConfig()
	if cfg.Model != "m" || cfg.APIKey != "k" || !cfg.URLStrict {
		t.Fatalf("legacy config not preserved: %+v", cfg)
	}
	if cfg.HTTPTimeoutSeconds != DefaultHTTPTimeoutSeconds {
		t.Fatalf("timeout not defaulted: %d", cfg.HTTPTimeoutSeconds)
	}
	// Legacy file must have been rewritten to profiles layout.
	data, _ := os.ReadFile(path)
	if len(data) == 0 || !contains(data, "profiles") {
		t.Fatalf("file not migrated to profiles layout: %s", string(data))
	}
}

func TestAPIProfilesNormalizeMissingActive(t *testing.T) {
	p := &APIProfiles{
		Profiles: map[string]*APIConfig{
			"a": {MaxTokens: 1},
			"b": {MaxTokens: 2},
		},
		Active: "nope",
	}
	p.Normalize()
	if p.Active != "a" {
		t.Fatalf("active = %q, want a", p.Active)
	}
	if cfg := p.ActiveConfig(); cfg.MaxTokens != 1 {
		t.Fatalf("active config = %+v", cfg)
	}
}

func TestAPIProfilesNormalizeEmpty(t *testing.T) {
	p := &APIProfiles{Profiles: nil, Active: ""}
	p.Normalize()
	if _, ok := p.Profiles["default"]; !ok {
		t.Fatalf("default not created: %+v", p.Profiles)
	}
}

func contains(b []byte, sub string) bool {
	return len(b) > 0 && len(b) >= len(sub) && func() bool {
		for i := 0; i+len(sub) <= len(b); i++ {
			if string(b[i:i+len(sub)]) == sub {
				return true
			}
		}
		return false
	}()
}

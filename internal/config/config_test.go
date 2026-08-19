package config

import (
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

package apiconfig

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadMigratesLegacyTopLevelConfiguration(t *testing.T) {
	path := filepath.Join(t.TempDir(), "api.json")
	if err := os.WriteFile(path, []byte(`{"api_key":"key","base_url":"https://api.example.test","model":"story","http_timeout_seconds":12}`), 0644); err != nil {
		t.Fatal(err)
	}

	config, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if config.ActiveProfileID != "default" || len(config.Profiles) != 1 {
		t.Fatalf("legacy configuration profiles = %#v", config)
	}
	if profile := config.Profiles[0]; profile.APIKey != "key" || profile.BaseURL != config.BaseURL || profile.Model != "story" || profile.HTTPTimeoutSeconds != 12 {
		t.Fatalf("legacy profile = %#v", profile)
	}
}

func TestNormalizeSynchronizesTopLevelChangesToActiveProfile(t *testing.T) {
	config := &Config{
		APIKey:          "new-key",
		BaseURL:         "https://new.example.test",
		Model:           "new-model",
		ActiveProfileID: "preferred",
		Profiles: []Profile{{
			ID: "preferred", Name: "Preferred", APIKey: "old-key", BaseURL: "https://old.example.test", Model: "old-model", HTTPTimeoutSeconds: 45, ContextBudgetTokens: 1234,
		}},
	}

	Normalize(config)
	if profile := config.Profiles[0]; profile.APIKey != config.APIKey || profile.BaseURL != config.BaseURL || profile.Model != config.Model || profile.HTTPTimeoutSeconds != config.HTTPTimeoutSeconds || profile.ContextBudgetTokens != config.ContextBudgetTokens {
		t.Fatalf("active profile was not synchronized with top-level fields: %#v", config)
	}
}

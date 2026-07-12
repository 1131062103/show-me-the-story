// Package apiconfig persists and normalizes process-wide provider configuration.
package apiconfig

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"showmethestory/internal/infra/openai"
	"showmethestory/internal/ports"
)

const defaultContextBudgetTokens = 300000

// Config is the user-configured OpenAI-compatible provider and model.
type Config struct {
	APIKey              string    `json:"api_key"`
	BaseURL             string    `json:"base_url"`
	URLStrict           bool      `json:"url_strict,omitempty"`
	Model               string    `json:"model"`
	MaxTokens           int       `json:"max_tokens,omitempty"`
	HTTPTimeoutSeconds  int       `json:"http_timeout_seconds"`
	ContextBudgetTokens int       `json:"context_budget_tokens"`
	ActiveProfileID     string    `json:"active_profile_id,omitempty"`
	Profiles            []Profile `json:"profiles,omitempty"`
}

type Profile struct {
	ID                  string `json:"id"`
	Name                string `json:"name"`
	APIKey              string `json:"api_key"`
	BaseURL             string `json:"base_url"`
	URLStrict           bool   `json:"url_strict,omitempty"`
	Model               string `json:"model"`
	MaxTokens           int    `json:"max_tokens,omitempty"`
	HTTPTimeoutSeconds  int    `json:"http_timeout_seconds"`
	ContextBudgetTokens int    `json:"context_budget_tokens"`
}

func Default() *Config {
	config := &Config{HTTPTimeoutSeconds: 300, ContextBudgetTokens: defaultContextBudgetTokens}
	Normalize(config)
	return config
}

// Normalize applies compatibility defaults and projects the active profile onto
// the top-level fields retained by the existing API contract.
func Normalize(config *Config) {
	if config == nil {
		return
	}
	if config.HTTPTimeoutSeconds <= 0 {
		config.HTTPTimeoutSeconds = 300
	}
	if config.ContextBudgetTokens <= 0 {
		config.ContextBudgetTokens = defaultContextBudgetTokens
	}
	if len(config.Profiles) == 0 {
		config.ActiveProfileID = "default"
		config.Profiles = []Profile{profileFromConfig(config, "default", "Default")}
		return
	}
	syncActiveProfileFromTopLevel(config)
	found := false
	for i := range config.Profiles {
		normalizeProfile(&config.Profiles[i])
		if config.Profiles[i].ID == config.ActiveProfileID {
			applyProfile(config, config.Profiles[i])
			found = true
		}
	}
	if !found {
		config.ActiveProfileID = config.Profiles[0].ID
		applyProfile(config, config.Profiles[0])
	}
}

func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		config := Default()
		if err := Save(path, config); err != nil {
			return nil, fmt.Errorf("create default API configuration: %w", err)
		}
		return config, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read API configuration: %w", err)
	}
	var config Config
	if err := json.Unmarshal(data, &config); err != nil {
		return nil, fmt.Errorf("parse API configuration: %w", err)
	}
	needsContextWindow := config.ContextBudgetTokens <= 0
	Normalize(&config)
	if needsContextWindow && config.BaseURL != "" && config.Model != "" {
		client := NewClient(&config, 10*time.Second)
		if window, err := client.ModelContextWindow(context.Background(), config.Model); err == nil && window > 0 {
			config.ContextBudgetTokens = window
			syncActiveProfileFromTopLevel(&config)
		}
	}
	return &config, nil
}

func Save(path string, config *Config) error {
	data, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return err
	}
	return writeAtomic(path, data)
}

func Validate(config *Config) error {
	if config == nil || strings.TrimSpace(config.BaseURL) == "" {
		return fmt.Errorf("API Base URL is not configured")
	}
	if strings.TrimSpace(config.Model) == "" {
		return fmt.Errorf("model is not configured")
	}
	return nil
}

func NewClient(config *Config, timeout time.Duration) *openai.Client {
	if config == nil {
		config = Default()
	}
	return openai.New(openai.Config{BaseURL: config.BaseURL, APIKey: config.APIKey, URLStrict: config.URLStrict, Timeout: timeout})
}

func ListModels(ctx context.Context, config *Config) ([]ports.ModelInfo, error) {
	if config == nil || strings.TrimSpace(config.BaseURL) == "" {
		return nil, fmt.Errorf("API Base URL is not configured")
	}
	return NewClient(config, 15*time.Second).ListModels(ctx)
}

func syncActiveProfileFromTopLevel(config *Config) {
	if config.ActiveProfileID == "" {
		config.ActiveProfileID = "default"
		if len(config.Profiles) > 0 {
			config.ActiveProfileID = config.Profiles[0].ID
		}
	}
	for i := range config.Profiles {
		if config.Profiles[i].ID == config.ActiveProfileID {
			name := config.Profiles[i].Name
			config.Profiles[i] = profileFromConfig(config, config.ActiveProfileID, name)
			normalizeProfile(&config.Profiles[i])
			return
		}
	}
	config.Profiles = append(config.Profiles, profileFromConfig(config, config.ActiveProfileID, config.ActiveProfileID))
}

func normalizeProfile(profile *Profile) {
	if profile.ID == "" {
		profile.ID = "default"
	}
	if profile.Name == "" {
		profile.Name = profile.ID
	}
	if profile.HTTPTimeoutSeconds <= 0 {
		profile.HTTPTimeoutSeconds = 300
	}
	if profile.ContextBudgetTokens <= 0 {
		profile.ContextBudgetTokens = defaultContextBudgetTokens
	}
}

func profileFromConfig(config *Config, id, name string) Profile {
	return Profile{ID: id, Name: name, APIKey: config.APIKey, BaseURL: config.BaseURL, URLStrict: config.URLStrict, Model: config.Model, MaxTokens: config.MaxTokens, HTTPTimeoutSeconds: config.HTTPTimeoutSeconds, ContextBudgetTokens: config.ContextBudgetTokens}
}

func applyProfile(config *Config, profile Profile) {
	config.APIKey, config.BaseURL, config.URLStrict, config.Model, config.MaxTokens = profile.APIKey, profile.BaseURL, profile.URLStrict, profile.Model, profile.MaxTokens
	config.HTTPTimeoutSeconds, config.ContextBudgetTokens = profile.HTTPTimeoutSeconds, profile.ContextBudgetTokens
}

func writeAtomic(path string, data []byte) error {
	temporary := filepath.Clean(path) + ".tmp"
	if err := os.WriteFile(temporary, data, 0644); err != nil {
		return err
	}
	if err := os.Rename(temporary, path); err != nil {
		_ = os.Remove(temporary)
		return err
	}
	return nil
}

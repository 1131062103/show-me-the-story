// Package fsstore implements project persistence on the local filesystem.
package fsstore

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"showmethestory/internal/domain/project"
)

const (
	projectsDirName          = "storys"
	progressName             = "progress.json"
	configName               = "config.json"
	settingsName             = "settings.json"
	postProcessName          = "postprocess.json"
	sessionsDirName          = "sessions"
	roadmapName              = "Foreshadows.md"
	pendingConfigChangesName = "pending_config_changes.json"
)

// Store is a filesystem-backed store for a single project.
type Store struct {
	projectDir string
}

// New opens a project under dataDir/storys. projectName must be a single path
// component so callers cannot escape the projects directory.
func New(dataDir, projectName string) (*Store, error) {
	if err := validateProjectName(projectName); err != nil {
		return nil, err
	}
	return NewAtProjectDir(filepath.Join(dataDir, projectsDirName, projectName))
}

// NewAtProjectDir opens an already-resolved project directory. It is useful to
// bridge callers that currently retain a project directory rather than its name.
func NewAtProjectDir(projectDir string) (*Store, error) {
	if strings.TrimSpace(projectDir) == "" {
		return nil, errors.New("project directory is required")
	}
	return &Store{projectDir: filepath.Clean(projectDir)}, nil
}

func validateProjectName(name string) error {
	if name == "" || name == "." || name == ".." || filepath.Base(name) != name || strings.ContainsAny(name, `/\\`) {
		return fmt.Errorf("invalid project name %q", name)
	}
	return nil
}

func (s *Store) ProjectDir() string            { return s.projectDir }
func (s *Store) ProgressPath() string          { return filepath.Join(s.projectDir, progressName) }
func (s *Store) ConfigPath() string            { return filepath.Join(s.projectDir, configName) }
func (s *Store) SettingsPath() string          { return filepath.Join(s.projectDir, settingsName) }
func (s *Store) PostProcessPath() string       { return filepath.Join(s.projectDir, postProcessName) }
func (s *Store) SessionsDir() string           { return filepath.Join(s.projectDir, sessionsDirName) }
func (s *Store) ForeshadowRoadmapPath() string { return filepath.Join(s.projectDir, roadmapName) }

func (s *Store) PendingConfigChangesPath() string {
	return filepath.Join(s.projectDir, pendingConfigChangesName)
}

func (s *Store) LoadPendingConfigChanges(ctx context.Context) (*project.PendingConfigChanges, error) {
	data, err := readOptionalJSON(ctx, s.PendingConfigChangesPath())
	if err != nil {
		return nil, err
	}
	var value project.PendingConfigChanges
	if err := json.Unmarshal(data, &value); err != nil {
		return nil, fmt.Errorf("decode pending config changes: %w", err)
	}
	if value.Changes == nil {
		value.Changes = []project.ConfigFieldChange{}
	}
	return &value, nil
}

func (s *Store) SavePendingConfigChanges(ctx context.Context, value *project.PendingConfigChanges) error {
	if value == nil || len(value.Changes) == 0 {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := os.Remove(s.PendingConfigChangesPath()); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("delete pending config changes: %w", err)
		}
		return nil
	}
	return s.saveJSON(ctx, s.PendingConfigChangesPath(), value)
}

func (s *Store) ChapterMarkdownPath(chapterNum int) string {
	return filepath.Join(s.projectDir, fmt.Sprintf("Chapter_%02d.md", chapterNum))
}

// LoadProject reads the complete persisted project aggregate using the established
// file layout. An absent progress.json represents a project that has no outline yet.
func (s *Store) LoadProject(ctx context.Context) (*project.Project, error) {
	config, err := s.LoadConfig(ctx)
	if err != nil {
		return nil, err
	}
	settings, err := s.LoadProjectSettings(ctx)
	if err != nil {
		return nil, err
	}
	postProcess, err := s.LoadPostProcess(ctx)
	if err != nil {
		return nil, err
	}
	progress, err := s.LoadProgress(ctx)
	if err != nil {
		return nil, err
	}
	return &project.Project{
		Name:        filepath.Base(s.projectDir),
		Config:      config,
		Progress:    progress,
		Settings:    settings,
		PostProcess: postProcess,
	}, nil
}

func (s *Store) LoadConfig(ctx context.Context) (*project.Config, error) {
	data, err := readFileContext(ctx, s.ConfigPath())
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}
	var value project.Config
	if err := json.Unmarshal(data, &value); err != nil {
		return nil, fmt.Errorf("decode config: %w", err)
	}
	value.Normalize()
	return &value, nil
}
func (s *Store) LoadProjectSettings(ctx context.Context) (*project.ProjectSettings, error) {
	data, err := readOptionalJSON(ctx, s.SettingsPath())
	if err != nil {
		return nil, err
	}
	var value project.ProjectSettings
	if err := json.Unmarshal(data, &value); err != nil {
		return nil, fmt.Errorf("decode settings: %w", err)
	}
	return &value, nil
}
func (s *Store) LoadPostProcess(ctx context.Context) (*project.PostProcessState, error) {
	data, err := readOptionalJSON(ctx, s.PostProcessPath())
	if err != nil {
		return nil, err
	}
	value := project.DefaultPostProcessState()
	if err := json.Unmarshal(data, value); err != nil {
		return nil, fmt.Errorf("decode postprocess: %w", err)
	}
	if value.ExecuteOptions == nil {
		value.ExecuteOptions = project.DefaultPostProcessState().ExecuteOptions
	}
	return value, nil
}

// SaveProject atomically persists each document in a project aggregate.
func (s *Store) SaveProject(ctx context.Context, value *project.Project) error {
	if value == nil {
		return errors.New("project is required")
	}
	if err := s.saveJSON(ctx, s.ConfigPath(), value.Config); err != nil {
		return fmt.Errorf("save config: %w", err)
	}
	if err := s.saveJSON(ctx, s.SettingsPath(), value.Settings); err != nil {
		return fmt.Errorf("save settings: %w", err)
	}
	if err := s.saveJSON(ctx, s.PostProcessPath(), value.PostProcess); err != nil {
		return fmt.Errorf("save postprocess: %w", err)
	}
	if value.Progress == nil {
		return nil
	}
	return s.SaveProgress(ctx, value.Progress)
}

func (s *Store) saveJSON(ctx context.Context, path string, value any) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	return writeFileAtomic(ctx, path, data)
}

func readOptionalJSON(ctx context.Context, path string) ([]byte, error) {
	data, err := readFileContext(ctx, path)
	if errors.Is(err, fs.ErrNotExist) {
		return []byte("{}"), nil
	}
	return data, err
}

func (s *Store) LoadProgress(ctx context.Context) (*project.Progress, error) {
	data, err := readFileContext(ctx, s.ProgressPath())
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read progress: %w", err)
	}

	var progress project.Progress
	if err := json.Unmarshal(data, &progress); err != nil {
		return nil, fmt.Errorf("decode progress: %w", err)
	}
	return &progress, nil
}

func (s *Store) SaveProgress(ctx context.Context, progress *project.Progress) error {
	if progress == nil {
		return errors.New("progress is required")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	data, err := json.MarshalIndent(progress, "", "  ")
	if err != nil {
		return fmt.Errorf("encode progress: %w", err)
	}
	if err := writeFileAtomic(ctx, s.ProgressPath(), data); err != nil {
		return fmt.Errorf("save progress: %w", err)
	}
	return nil
}

func (s *Store) LoadChapterMarkdown(ctx context.Context, chapterNum int) ([]byte, error) {
	data, err := readFileContext(ctx, s.ChapterMarkdownPath(chapterNum))
	if err != nil {
		return nil, fmt.Errorf("read chapter %d markdown: %w", chapterNum, err)
	}
	return data, nil
}

func (s *Store) SaveChapterMarkdown(ctx context.Context, chapterNum int, content []byte) error {
	if err := writeFileAtomic(ctx, s.ChapterMarkdownPath(chapterNum), content); err != nil {
		return fmt.Errorf("save chapter %d markdown: %w", chapterNum, err)
	}
	return nil
}

func (s *Store) DeleteChapterMarkdown(ctx context.Context, chapterNum int) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := os.Remove(s.ChapterMarkdownPath(chapterNum)); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("delete chapter %d markdown: %w", chapterNum, err)
	}
	return nil
}

func readFileContext(ctx context.Context, path string) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return data, nil
}

func writeFileAtomic(ctx context.Context, path string, data []byte) (err error) {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}

	tmp, err := os.CreateTemp(filepath.Dir(path), ".tmp-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer func() {
		if err != nil {
			_ = os.Remove(tmpPath)
		}
	}()

	if err = tmp.Chmod(0o644); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err = tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err = tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err = tmp.Close(); err != nil {
		return err
	}
	if err = ctx.Err(); err != nil {
		return err
	}
	if err = os.Rename(tmpPath, path); err != nil {
		return err
	}

	dir, dirErr := os.Open(filepath.Dir(path))
	if dirErr == nil {
		dirErr = dir.Sync()
		closeErr := dir.Close()
		if dirErr == nil {
			dirErr = closeErr
		}
	}
	return dirErr
}

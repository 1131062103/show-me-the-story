// Package ports defines the boundaries used by application services.
package ports

import (
	"context"

	"showmethestory/internal/domain/project"
)

// ProjectStore persists one selected story project.
//
// A nil Progress returned by LoadProgress means progress.json has not been
// created yet, matching the behavior of the existing storage implementation.
type ProjectStore interface {
	ProjectDir() string
	ProgressPath() string
	ConfigPath() string
	SettingsPath() string
	PostProcessPath() string
	SessionsDir() string
	ChapterMarkdownPath(chapterNum int) string
	ForeshadowRoadmapPath() string

	LoadPendingConfigChanges(context.Context) (*project.PendingConfigChanges, error)
	SavePendingConfigChanges(context.Context, *project.PendingConfigChanges) error

	LoadProject(context.Context) (*project.Project, error)
	SaveProject(context.Context, *project.Project) error
	LoadConfig(context.Context) (*project.Config, error)
	LoadProjectSettings(context.Context) (*project.ProjectSettings, error)
	LoadPostProcess(context.Context) (*project.PostProcessState, error)
	LoadProgress(context.Context) (*project.Progress, error)
	SaveProgress(context.Context, *project.Progress) error
	LoadChapterMarkdown(context.Context, int) ([]byte, error)
	SaveChapterMarkdown(context.Context, int, []byte) error
	DeleteChapterMarkdown(context.Context, int) error
}

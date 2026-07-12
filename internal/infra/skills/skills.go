// Package skills loads root and project-local optional writing rules.
package skills

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"showmethestory/internal/domain/project"
)

// LoadBuiltin loads skills from the application data directory's skills folder.
func LoadBuiltin(dir string) []project.Skill {
	return loadDir(dir, "builtin")
}

func LoadProject(dir string) []project.Skill {
	return loadDir(filepath.Join(dir, "skills"), "project")
}

func loadDir(dir, source string) []project.Skill {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	result := make([]project.Skill, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".md") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, entry.Name()))
		if err != nil {
			continue
		}
		if skill, err := parse(string(data), source); err == nil {
			result = append(result, skill)
		}
	}
	return result
}

func Merge(builtin, local []project.Skill) []project.Skill {
	return append(append(make([]project.Skill, 0, len(builtin)+len(local)), builtin...), local...)
}

func FilterByLanguage(items []project.Skill, language string) []project.Skill {
	language = project.NormalizeLanguage(language)
	filtered := make([]project.Skill, 0, len(items))
	for _, item := range items {
		if item.Lang == "" || item.Lang == language {
			filtered = append(filtered, item)
		}
	}
	return filtered
}

func Format(items []project.Skill) string {
	if len(items) == 0 {
		return ""
	}
	english := false
	for _, item := range items {
		if item.Lang == project.LangEN {
			english = true
			break
		}
	}
	var result strings.Builder
	if english {
		result.WriteString("Strictly follow the skill rules below while writing:\n\n")
	} else {
		result.WriteString("以下技能规则在创作时必须严格遵守：\n\n")
	}
	for _, item := range items {
		fmt.Fprintf(&result, "## %s\n\n%s\n\n", item.Name, item.Content)
	}
	return result.String()
}

func parse(content, source string) (project.Skill, error) {
	skill := project.Skill{Source: source}
	parts := strings.SplitN(content, "---", 3)
	if len(parts) < 3 {
		return skill, fmt.Errorf("missing skill frontmatter")
	}
	for _, line := range strings.Split(strings.TrimSpace(parts[1]), "\n") {
		key, value, ok := strings.Cut(strings.TrimSpace(line), ":")
		if !ok {
			continue
		}
		switch strings.TrimSpace(key) {
		case "id":
			skill.ID = strings.TrimSpace(value)
		case "name":
			skill.Name = strings.TrimSpace(value)
		case "description":
			skill.Description = strings.TrimSpace(value)
		case "category":
			skill.Category = strings.TrimSpace(value)
		case "lang":
			skill.Lang = project.NormalizeLanguage(strings.TrimSpace(value))
		}
	}
	skill.Content = strings.TrimSpace(parts[2])
	if skill.ID == "" {
		return skill, fmt.Errorf("skill ID is required")
	}
	return skill, nil
}

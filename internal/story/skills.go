package story

import (
	"embed"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"showmethestory/internal/config"
	"showmethestory/internal/i18n"
	"strings"
)

//go:embed embeds/skills
var builtinSkillFiles embed.FS

// SkillSourceExternal marks skills loaded from the program-level skills/ dir.
const SkillSourceExternal = "external"

// ExternalSkillIDPrefix prefixes external skill IDs to keep them in a separate
// key space from builtin/project skills (enabled state is keyed by ID).
const ExternalSkillIDPrefix = "ext."

type Skill struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Category    string `json:"category"`
	Lang        string `json:"lang,omitempty"` // "zh", "en", or "" (language-agnostic)
	Content     string `json:"content"`
	Enabled     bool   `json:"enabled"`
	Source      string `json:"source"`
}

func LoadBuiltinSkills() []Skill {
	var skills []Skill

	entries, err := builtinSkillFiles.ReadDir("embeds/skills")
	if err != nil {
		fmt.Printf(" [警告] 读取内置技能目录失败: %v\n", err)
		return skills
	}

	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".md") {
			continue
		}

		data, err := builtinSkillFiles.ReadFile("embeds/skills/" + entry.Name())
		if err != nil {
			fmt.Printf(" [警告] 读取内置技能文件 %s 失败: %v\n", entry.Name(), err)
			continue
		}

		skill, err := parseSkillFile(string(data), "builtin", "")
		if err != nil {
			fmt.Printf(" [警告] 解析内置技能文件 %s 失败: %v\n", entry.Name(), err)
			continue
		}

		skills = append(skills, skill)
	}

	return skills
}

func LoadProjectSkills(dir string) []Skill {
	skillsDir := filepath.Join(dir, "skills")
	if _, err := os.Stat(skillsDir); os.IsNotExist(err) {
		return nil
	}

	var skills []Skill
	for _, f := range scanSkillDir(skillsDir) {
		data, err := os.ReadFile(f.path)
		if err != nil {
			continue
		}

		skill, err := parseSkillFile(string(data), "project", f.fallbackID)
		if err != nil {
			continue
		}

		skills = append(skills, skill)
	}

	return skills
}

// skillFile is a resolved skill source: a path plus the fallback ID derived
// from the enclosing directory (standard layout) or filename (legacy layout).
type skillFile struct {
	path       string
	fallbackID string
}

// scanSkillDir lists skill files under dir. Two layouts are supported, with
// the standard subdirectory layout taking precedence:
//
//	dir/<skill-name>/SKILL.md   (standard)
//	dir/<skill-name>.md         (legacy flat files)
//
// The fallback ID is the skill-name segment (subdirectory name or filename
// without .md), used when frontmatter has no explicit `id`.
func scanSkillDir(dir string) []skillFile {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}

	var files []skillFile
	for _, entry := range entries {
		if entry.IsDir() {
			path := filepath.Join(dir, entry.Name(), "SKILL.md")
			if _, err := os.Stat(path); err == nil {
				files = append(files, skillFile{path: path, fallbackID: entry.Name()})
			}
			continue
		}
		if strings.HasSuffix(entry.Name(), ".md") {
			files = append(files, skillFile{
				path:       filepath.Join(dir, entry.Name()),
				fallbackID: strings.TrimSuffix(entry.Name(), ".md"),
			})
		}
	}
	return files
}

// LoadExternalSkills loads standard-format skills from the program-level
// skills/ dir (progDir/skills). The standard layout is one subdirectory per
// skill containing a SKILL.md file:
//
//	skills/<skill-name>/SKILL.md
//
// Flat <skill-name>.md files are also accepted as a legacy fallback. Format
// follows the plain markdown convention: frontmatter with name/description and
// optional lang; no category, no capabilities. IDs are auto-prefixed with
// ExternalSkillIDPrefix so they never collide with builtin/project skills in
// the enabled-state key space. A file without an explicit id falls back to its
// skill-name segment.
func LoadExternalSkills(progDir string) []Skill {
	skillsDir := filepath.Join(progDir, "skills")
	if _, err := os.Stat(skillsDir); os.IsNotExist(err) {
		return nil
	}

	var skills []Skill
	for _, f := range scanSkillDir(skillsDir) {
		data, err := os.ReadFile(f.path)
		if err != nil {
			continue
		}

		skill, err := parseSkillFile(string(data), SkillSourceExternal, f.fallbackID)
		if err != nil {
			continue
		}

		skill.ID = ExternalSkillIDPrefix + skill.ID
		skills = append(skills, skill)
	}

	return skills
}

func parseSkillFile(content string, source string, fallbackID string) (Skill, error) {
	skill := Skill{Source: source}

	parts := strings.SplitN(content, "---", 3)
	if len(parts) < 3 {
		return skill, fmt.Errorf("invalid skill file format: missing frontmatter")
	}

	fields := parseSkillFrontmatter(parts[1])
	skill.ID = fields["id"]
	skill.Name = fields["name"]
	skill.Description = fields["description"]
	skill.Category = fields["category"]
	if lang := fields["lang"]; lang != "" {
		skill.Lang = i18n.NormalizeLanguage(lang)
	}
	if source == "" {
		skill.Source = fields["source"]
	}

	skill.Content = strings.TrimSpace(parts[2])

	if skill.ID == "" {
		skill.ID = fallbackID
	}
	if skill.Name == "" {
		skill.Name = fallbackID
	}

	if skill.ID == "" {
		return skill, fmt.Errorf("skill missing id")
	}

	return skill, nil
}

// parseSkillFrontmatter parses the YAML frontmatter block of a skill file into
// a flat string map. It supports simple `key: value` pairs as well as YAML
// block scalars (`key: |` / `key: >` with indented continuation lines, used
// for multi-line descriptions). Nested maps and quoted values are flattened to
// their first-line scalar, which is sufficient for the skill metadata fields.
func parseSkillFrontmatter(frontmatter string) map[string]string {
	fields := make(map[string]string)
	lines := strings.Split(frontmatter, "\n")

	var blockKey string
	var blockLines []string
	flushBlock := func() {
		if blockKey != "" {
			fields[blockKey] = strings.TrimSpace(strings.Join(blockLines, "\n"))
		}
		blockKey = ""
		blockLines = nil
	}

	for i := 0; i < len(lines); i++ {
		line := lines[i]
		if blockKey != "" {
			// Block scalar: keep collecting indented continuation lines until
			// a non-indented line or a closing blank line arrives.
			if line == "" || strings.HasPrefix(line, " ") || strings.HasPrefix(line, "\t") {
				blockLines = append(blockLines, strings.TrimSpace(line))
				continue
			}
			flushBlock()
		}

		trimmed := strings.TrimSpace(line)
		if trimmed == "" || !strings.Contains(trimmed, ":") {
			continue
		}
		kv := strings.SplitN(trimmed, ":", 2)
		key := strings.TrimSpace(kv[0])
		value := strings.TrimSpace(kv[1])

		switch value {
		case "|", ">", "|-", ">-", "|+", ">+":
			blockKey = key
			blockLines = nil
			continue
		}
		fields[key] = strings.Trim(value, `"'`)
	}
	flushBlock()
	return fields
}

func MergeSkills(builtin, project []Skill) []Skill {
	result := make([]Skill, 0, len(builtin)+len(project))
	result = append(result, builtin...)
	result = append(result, project...)
	return result
}

func LoadAllSkills(cfg *config.Config, progDir, projectDir string) []Skill {
	builtin := LoadBuiltinSkills()
	project := LoadProjectSkills(projectDir)
	external := LoadExternalSkills(progDir)
	merged := MergeSkills(builtin, project)
	merged = append(merged, external...)
	if cfg == nil {
		return merged
	}
	return FilterSkillsByLang(merged, cfg.Language)
}

// FilterSkillsByLang returns skills matching the project language.
// Skills with empty `lang` are language-agnostic and always returned.
func FilterSkillsByLang(skills []Skill, projectLang string) []Skill {
	projectLang = i18n.NormalizeLanguage(projectLang)
	out := make([]Skill, 0, len(skills))
	for _, s := range skills {
		if s.Lang == "" || s.Lang == projectLang {
			out = append(out, s)
		}
	}
	return out
}

func GetEnabledSkillsByCategory(skills []Skill, sc *config.SkillConfig, category string) []Skill {
	if sc == nil || sc.EnabledSkills == nil {
		return nil
	}

	var enabled []Skill
	for _, s := range skills {
		if sc.EnabledSkills[s.ID] && s.Category == category {
			enabled = append(enabled, s)
		}
	}
	return enabled
}

// GetEnabledSkillsBySource returns enabled skills restricted to the given
// sources (e.g. "builtin", "project", or story.SkillSourceExternal).
func GetEnabledSkillsBySource(skills []Skill, sc *config.SkillConfig, sources ...string) []Skill {
	if sc == nil || sc.EnabledSkills == nil {
		return nil
	}

	srcSet := make(map[string]bool, len(sources))
	for _, s := range sources {
		srcSet[s] = true
	}

	var enabled []Skill
	for _, s := range skills {
		if srcSet[s.Source] && sc.EnabledSkills[s.ID] {
			enabled = append(enabled, s)
		}
	}
	return enabled
}

var skillTokenRe = regexp.MustCompile(`[a-z0-9]{3,}`)

// skillTokenSet tokenizes a string into a set of significant tokens:
// latin words (3+ chars) plus CJK character bigrams. Used for on-demand
// external skill matching.
func skillTokenSet(s string) map[string]struct{} {
	set := make(map[string]struct{})
	lower := strings.ToLower(s)
	for _, w := range skillTokenRe.FindAllString(lower, -1) {
		set[w] = struct{}{}
	}
	var cjk []rune
	for _, r := range lower {
		if r >= 0x4e00 && r <= 0x9fff {
			cjk = append(cjk, r)
			if len(cjk) >= 2 {
				set[string(cjk[len(cjk)-2:])] = struct{}{}
			}
		} else {
			cjk = cjk[:0]
		}
	}
	return set
}

func sharedTokenCount(a, b string) int {
	sa := skillTokenSet(a)
	sb := skillTokenSet(b)
	count := 0
	for t := range sa {
		if _, ok := sb[t]; ok {
			count++
		}
	}
	return count
}

// SkillMatchesMessage reports whether the user message mentions the skill:
// the skill's name appears verbatim, or at least two distinctive tokens from
// its name/description appear in the message. Deterministic framework-level
// matching — the model does not decide which skills to load.
func SkillMatchesMessage(s Skill, message string) bool {
	if message == "" {
		return false
	}
	msg := strings.ToLower(message)
	name := strings.ToLower(strings.TrimSpace(s.Name))
	if name != "" && strings.Contains(msg, name) {
		return true
	}
	if sharedTokenCount(s.Description, msg) >= 2 {
		return true
	}
	return false
}

// FilterSkillsByMessage returns the skills whose name/description is mentioned
// in the given message. External skills are injected this way instead of
// always injecting every enabled skill into the assistant.
func FilterSkillsByMessage(skills []Skill, message string) []Skill {
	var matched []Skill
	for _, s := range skills {
		if SkillMatchesMessage(s, message) {
			matched = append(matched, s)
		}
	}
	return matched
}

func FormatSkillsContent(skills []Skill) string {
	if len(skills) == 0 {
		return ""
	}

	var sb strings.Builder
	// Detect language from skill set: if any skill is explicitly EN, use EN header.
	en := false
	for _, s := range skills {
		if s.Lang == i18n.LangEN {
			en = true
			break
		}
	}
	if en {
		sb.WriteString("Strictly follow the skill rules below while writing:\n\n")
	} else {
		sb.WriteString("以下技能规则在创作时必须严格遵守：\n\n")
	}
	for _, s := range skills {
		sb.WriteString(fmt.Sprintf("## %s\n\n%s\n\n", s.Name, s.Content))
	}
	return sb.String()
}

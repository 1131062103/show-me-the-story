package project

// Skill is a built-in or project-local writing rule. Content is returned to the
// UI but is only injected into an AI workflow when it is explicitly enabled.
type Skill struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Category    string `json:"category"`
	Lang        string `json:"lang,omitempty"`
	Content     string `json:"content"`
	Enabled     bool   `json:"enabled"`
	Source      string `json:"source"`
}

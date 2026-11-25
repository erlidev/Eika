package server

import (
	"time"

	"github.com/erlidev/eika/internal/skill"
)

// skillItem is one skill in the list, without its body or file list.
// SpanCount is the number of screen hits and WarningCount the parser's
// warnings; the detail has both in full.
type skillItem struct {
	Name         string    `json:"name"`
	Description  string    `json:"description"`
	Enabled      bool      `json:"enabled"`
	Broken       string    `json:"broken"`
	InstalledAt  time.Time `json:"installed_at"`
	SHA          string    `json:"sha"`
	FileCount    int       `json:"file_count"`
	WarningCount int       `json:"warning_count"`
	SpanCount    int       `json:"span_count"`
	Unscanned    bool      `json:"unscanned"`
}

// skillsResponse is every installed skill, by name.
type skillsResponse struct {
	Skills []skillItem `json:"skills"`
}

// skillBody is one skill in full: the stored SKILL.md body (frontmatter
// stripped), the file list and the install screen's result. Broken, when not
// empty, is why the skill cannot be enabled or loaded.
type skillBody struct {
	Name          string              `json:"name"`
	Description   string              `json:"description"`
	License       string              `json:"license"`
	Compatibility string              `json:"compatibility"`
	Metadata      map[string]string   `json:"metadata"`
	AllowedTools  string              `json:"allowed_tools"`
	Body          string              `json:"body"`
	Enabled       bool                `json:"enabled"`
	Broken        string              `json:"broken"`
	SHA           string              `json:"sha"`
	InstalledAt   time.Time           `json:"installed_at"`
	Files         []skill.FileInfo    `json:"files"`
	Warnings      []string            `json:"warnings"`
	Screen        *skill.ScreenResult `json:"screen"`
}

// skillInstallResponse answers an upload. Unchanged is true when the same
// tree was already installed and nothing was written.
type skillInstallResponse struct {
	Skill     skillBody `json:"skill"`
	Unchanged bool      `json:"unchanged"`
}

package server

import (
	"time"

	"github.com/erlidev/eika/internal/store"
)

// projectBody is project metadata, without user instructions.
type projectBody struct {
	ID   string `json:"id"`
	Slug string `json:"slug"`
	store.ProjectFields
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// projectsResponse lists project records in slug order.
type projectsResponse struct {
	Projects []projectBody `json:"projects"`
}

// createProjectRequest contains the stable slug and initial metadata.
type createProjectRequest struct {
	Slug string `json:"slug"`
	store.ProjectFields
}

func projectView(p store.Project) projectBody {
	return projectBody{ID: p.ID, Slug: p.Slug, ProjectFields: p.ProjectFields, CreatedAt: p.CreatedAt, UpdatedAt: p.UpdatedAt}
}

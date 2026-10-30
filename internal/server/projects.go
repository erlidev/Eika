package server

import (
	"context"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"github.com/erlidev/eika/internal/store"
)

// defaultBranch is the branch a project uses when the request names none.
const defaultBranch = "main"

// projectBody is one project on the wire.
type projectBody struct {
	ID            string    `json:"id"`
	Name          string    `json:"name"`
	Kind          string    `json:"kind"`
	RemoteURL     string    `json:"remote_url,omitempty"`
	HostPath      string    `json:"host_path,omitempty"`
	DefaultBranch string    `json:"default_branch"`
	CreatedAt     time.Time `json:"created_at"`
}

// createProjectRequest is the body of POST /api/projects. Kind is local: a
// project cloned from a remote is no longer supported.
type createProjectRequest struct {
	Name          string `json:"name"`
	Kind          string `json:"kind"`
	HostPath      string `json:"host_path"`
	DefaultBranch string `json:"default_branch"`
}

// updateProjectRequest is the body of PATCH /api/projects/{id}. An absent
// field is left alone. Where the code comes from does not change.
type updateProjectRequest struct {
	DefaultBranch *string `json:"default_branch"`
}

// projectsResponse is the body of GET /api/projects.
type projectsResponse struct {
	Projects []projectBody `json:"projects"`
}

// handleListProjects lists every project.
func (s *Server) handleListProjects(w http.ResponseWriter, r *http.Request) {
	projects, err := s.deps.Store.Projects(r.Context())
	if err != nil {
		s.fail(w, r, err)
		return
	}
	out := make([]projectBody, 0, len(projects))
	for _, p := range projects {
		out = append(out, asProject(p))
	}
	writeJSON(w, s.log, http.StatusOK, projectsResponse{Projects: out})
}

// handleCreateProject registers a project: a host directory that its
// workspaces bind-mount.
func (s *Server) handleCreateProject(w http.ResponseWriter, r *http.Request) {
	req, err := decodeJSON[createProjectRequest](r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	p, err := s.createProject(r.Context(), req)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.log.Info("project created", "project_id", p.ID, "name", p.Name, "kind", p.Kind)
	writeJSON(w, s.log, http.StatusCreated, asProject(p))
}

// createProject validates the request and records the project.
func (s *Server) createProject(ctx context.Context, req createProjectRequest) (store.Project, error) {
	name, err := checkName("name", req.Name)
	if err != nil {
		return store.Project{}, err
	}
	p := store.Project{
		Name:          name,
		Kind:          store.ProjectKind(req.Kind),
		HostPath:      strings.TrimSpace(req.HostPath),
		DefaultBranch: strings.TrimSpace(req.DefaultBranch),
	}
	if p.DefaultBranch == "" {
		p.DefaultBranch = defaultBranch
	}
	switch p.Kind {
	case store.ProjectRemote:
		return store.Project{}, invalidf("remote projects are no longer supported: Eika cannot clone a repository; use a local project")
	case store.ProjectLocal:
		if p.HostPath == "" {
			return store.Project{}, invalidf("host_path is required for a local project")
		}
		if !filepath.IsAbs(p.HostPath) {
			return store.Project{}, invalidf("host_path %s is not absolute", p.HostPath)
		}
	default:
		return store.Project{}, invalidf("kind must be %q", store.ProjectLocal)
	}
	return s.deps.Store.CreateProject(ctx, p)
}

// handleUpdateProject changes a project's default branch.
func (s *Server) handleUpdateProject(w http.ResponseWriter, r *http.Request) {
	req, err := decodeJSON[updateProjectRequest](r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	p, err := s.deps.Store.Project(r.Context(), r.PathValue("id"))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if req.DefaultBranch != nil {
		if p.DefaultBranch = strings.TrimSpace(*req.DefaultBranch); p.DefaultBranch == "" {
			s.fail(w, r, invalidf("default_branch must not be empty"))
			return
		}
	}
	updated, err := s.deps.Store.UpdateProject(r.Context(), p)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.log.Info("project updated", "project_id", updated.ID)
	writeJSON(w, s.log, http.StatusOK, asProject(updated))
}

// handleProject returns one project.
func (s *Server) handleProject(w http.ResponseWriter, r *http.Request) {
	p, err := s.deps.Store.Project(r.Context(), r.PathValue("id"))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, s.log, http.StatusOK, asProject(p))
}

// handleDeleteProject removes a project, its workspaces, and their
// containers.
func (s *Server) handleDeleteProject(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if _, err := s.deps.Store.Project(r.Context(), id); err != nil {
		s.fail(w, r, err)
		return
	}
	workspaces, err := s.deps.Store.Workspaces(r.Context(), id)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	for _, ws := range workspaces {
		if err := s.destroyWorkspace(r.Context(), ws); err != nil {
			s.fail(w, r, err)
			return
		}
	}
	if err := s.deps.Store.DeleteProject(r.Context(), id); err != nil {
		s.fail(w, r, err)
		return
	}
	s.log.Info("project deleted", "project_id", id)
	w.WriteHeader(http.StatusNoContent)
}

// asProject renders a stored project on the wire.
func asProject(p store.Project) projectBody {
	return projectBody{
		ID:            p.ID,
		Name:          p.Name,
		Kind:          string(p.Kind),
		RemoteURL:     p.RemoteURL,
		HostPath:      p.HostPath,
		DefaultBranch: p.DefaultBranch,
		CreatedAt:     p.CreatedAt,
	}
}

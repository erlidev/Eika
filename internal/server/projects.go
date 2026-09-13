package server

import (
	"context"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"
	"time"

	"github.com/erlidev/eika/internal/store"
	"github.com/erlidev/eika/internal/workspace/hub"
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

// createProjectRequest is the body of POST /api/projects.
type createProjectRequest struct {
	Name          string `json:"name"`
	Kind          string `json:"kind"`
	RemoteURL     string `json:"remote_url"`
	HostPath      string `json:"host_path"`
	DefaultBranch string `json:"default_branch"`
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

// handleCreateProject registers a project and prepares its hub repository. A
// remote project is mirrored into the hub; a local project keeps a host
// directory that its workspaces bind-mount.
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

// createProject validates the request, prepares the hub repository, and
// records the project.
func (s *Server) createProject(ctx context.Context, req createProjectRequest) (store.Project, error) {
	p := store.Project{
		Name:          strings.TrimSpace(req.Name),
		Kind:          store.ProjectKind(req.Kind),
		RemoteURL:     strings.TrimSpace(req.RemoteURL),
		HostPath:      strings.TrimSpace(req.HostPath),
		DefaultBranch: strings.TrimSpace(req.DefaultBranch),
	}
	if p.DefaultBranch == "" {
		p.DefaultBranch = defaultBranch
	}
	switch p.Kind {
	case store.ProjectRemote:
		if p.RemoteURL == "" {
			return store.Project{}, invalidf("remote_url is required for a remote project")
		}
	case store.ProjectLocal:
		if p.HostPath == "" {
			return store.Project{}, invalidf("host_path is required for a local project")
		}
		if !filepath.IsAbs(p.HostPath) {
			return store.Project{}, invalidf("host_path %s is not absolute", p.HostPath)
		}
	default:
		return store.Project{}, invalidf("kind must be %q or %q", store.ProjectRemote, store.ProjectLocal)
	}

	// Init validates the name as a repository directory and is a no-op on a
	// project that already has one, so it is also the name check.
	if _, err := s.deps.Hub.Init(ctx, p.Name); err != nil {
		return store.Project{}, err
	}
	if p.Kind == store.ProjectRemote {
		if err := s.deps.Hub.Mirror(ctx, p.Name, p.RemoteURL, hub.Credentials{}); err != nil {
			remote, reason := withoutCredentials(p.RemoteURL, err)
			s.log.Error("mirror remote", "project", p.Name, "remote", remote, "error", reason)
			return store.Project{}, invalidf("mirror %s: %s", remote, reason)
		}
	}
	return s.deps.Store.CreateProject(ctx, p)
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
// containers. The hub repository stays: it holds the branches the workspaces
// pushed, and deleting it would lose work that is nowhere else.
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

// withoutCredentials renders a remote URL and a failure about it with the
// userinfo removed. A user may paste a token into the URL, and git echoes the
// URL it was given, so neither an error body nor a log line may carry it
// through unchanged.
func withoutCredentials(raw string, err error) (remote, reason string) {
	reason = err.Error()
	u, parseErr := url.Parse(raw)
	if parseErr != nil {
		// The URL may still hold a credential, so it is described rather
		// than printed, and anything quoting it is dropped with it.
		return "[UNPARSEABLE]", "the remote url could not be parsed"
	}
	if u.User == nil {
		return raw, reason
	}
	u.User = nil
	remote = u.String()
	return remote, strings.ReplaceAll(reason, raw, remote)
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

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

// maxCredential bounds a remote username or password.
const maxCredential = 4096

// projectBody is one project on the wire.
type projectBody struct {
	ID             string `json:"id"`
	Name           string `json:"name"`
	Kind           string `json:"kind"`
	RemoteURL      string `json:"remote_url,omitempty"`
	RemoteUsername string `json:"remote_username,omitempty"`
	// RemotePasswordSet says a password is stored. The password itself never
	// leaves the harness.
	RemotePasswordSet bool      `json:"remote_password_set,omitempty"`
	HostPath          string    `json:"host_path,omitempty"`
	DefaultBranch     string    `json:"default_branch"`
	CreatedAt         time.Time `json:"created_at"`
}

// createProjectRequest is the body of POST /api/projects.
type createProjectRequest struct {
	Name           string `json:"name"`
	Kind           string `json:"kind"`
	RemoteURL      string `json:"remote_url"`
	RemoteUsername string `json:"remote_username"`
	RemotePassword string `json:"remote_password"`
	HostPath       string `json:"host_path"`
	DefaultBranch  string `json:"default_branch"`
}

// updateProjectRequest is the body of PATCH /api/projects/{id}. An absent
// field is left alone. Where the code comes from does not change.
type updateProjectRequest struct {
	RemoteUsername *string `json:"remote_username"`
	// RemotePassword replaces the stored password; the empty string removes
	// it along with the username.
	RemotePassword *string `json:"remote_password"`
	DefaultBranch  *string `json:"default_branch"`
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
	creds := hub.Credentials{
		Username: strings.TrimSpace(req.RemoteUsername),
		Password: strings.TrimSpace(req.RemotePassword),
	}
	switch p.Kind {
	case store.ProjectRemote:
		if p.RemoteURL == "" {
			return store.Project{}, invalidf("remote_url is required for a remote project")
		}
		if err := validateRemoteURL(p.RemoteURL); err != nil {
			return store.Project{}, err
		}
		if err := validateCredentials(creds); err != nil {
			return store.Project{}, err
		}
	case store.ProjectLocal:
		if creds != (hub.Credentials{}) {
			return store.Project{}, invalidf("remote credentials are only valid for a remote project")
		}
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
		if err := s.mirror(ctx, p, creds); err != nil {
			return store.Project{}, err
		}
	}
	var err error
	p.RemoteUsername = creds.Username
	if p.RemotePassword, err = s.deps.Secrets.Seal(creds.Password); err != nil {
		return store.Project{}, err
	}
	return s.deps.Store.CreateProject(ctx, p)
}

// handleUpdateProject changes a project's remote credentials or default
// branch. New credentials are used to fetch from the remote before they are
// stored, so a wrong token is refused here rather than at the next fetch.
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
	if req.RemoteUsername != nil || req.RemotePassword != nil {
		if p.Kind != store.ProjectRemote {
			s.fail(w, r, invalidf("remote credentials are only valid for a remote project"))
			return
		}
		// A request with a new password needs nothing stored, which is how a
		// password sealed under a lost key file is entered again.
		creds := hub.Credentials{Username: p.RemoteUsername}
		if req.RemotePassword == nil {
			if creds, err = s.projectCredentials(p); err != nil {
				s.fail(w, r, err)
				return
			}
		}
		if req.RemoteUsername != nil {
			creds.Username = strings.TrimSpace(*req.RemoteUsername)
		}
		if req.RemotePassword != nil {
			creds.Password = strings.TrimSpace(*req.RemotePassword)
			if creds.Password == "" {
				creds.Username = ""
			}
		}
		if err := validateCredentials(creds); err != nil {
			s.fail(w, r, err)
			return
		}
		if err := s.mirror(r.Context(), p, creds); err != nil {
			s.fail(w, r, err)
			return
		}
		p.RemoteUsername = creds.Username
		if p.RemotePassword, err = s.deps.Secrets.Seal(creds.Password); err != nil {
			s.fail(w, r, err)
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

// mirror fetches a remote project into the hub, reporting a failure without
// the credentials a URL or git's message could carry.
func (s *Server) mirror(ctx context.Context, p store.Project, creds hub.Credentials) error {
	if err := s.deps.Hub.Mirror(ctx, p.Name, p.RemoteURL, creds); err != nil {
		remote, reason := withoutCredentials(p.RemoteURL, err)
		reason = scrub(reason, creds.Password)
		s.log.Error("mirror remote", "project", p.Name, "remote", remote, "error", reason)
		return invalidf("mirror %s: %s", remote, reason)
	}
	return nil
}

// projectCredentials opens a project's stored remote credentials.
func (s *Server) projectCredentials(p store.Project) (hub.Credentials, error) {
	password, err := s.deps.Secrets.Open(p.RemotePassword)
	if err != nil {
		s.log.Error("open remote password", "project_id", p.ID, "error", err)
		return hub.Credentials{}, conflictf("the remote password of project %s cannot be read; enter it again in the project's settings", p.Name)
	}
	return hub.Credentials{Username: p.RemoteUsername, Password: password}, nil
}

// validateRemoteURL rejects credentials and other secret-bearing parts in a
// remote URL: the username and password have fields of their own.
func validateRemoteURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil {
		return invalidf("remote_url could not be parsed")
	}
	if u.User != nil {
		return invalidf("remote_url must not contain credentials; use remote_username and remote_password")
	}
	if u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || strings.Contains(raw, "#") {
		return invalidf("remote_url must not contain a query string or fragment")
	}
	return nil
}

// validateCredentials accepts a public remote's empty pair or a whole one.
func validateCredentials(creds hub.Credentials) error {
	if (creds.Username == "") != (creds.Password == "") {
		return invalidf("remote_username and remote_password must both be set, or neither")
	}
	if len(creds.Username) > maxCredential || len(creds.Password) > maxCredential {
		return invalidf("remote credentials must be at most %d bytes each", maxCredential)
	}
	return nil
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
	if err != nil {
		reason = err.Error()
	}
	u, parseErr := url.Parse(raw)
	if parseErr != nil {
		// The URL may still hold a credential, so it is described rather
		// than printed, and anything quoting it is dropped with it.
		return "[UNPARSEABLE]", "the remote url could not be parsed"
	}
	secrets := make([]string, 0, 8)
	if u.User != nil {
		secrets = append(secrets, u.User.String(), u.User.Username())
		if password, ok := u.User.Password(); ok {
			secrets = append(secrets, password, url.PathEscape(password))
		}
		u.User = nil
	}
	if u.RawQuery != "" || u.ForceQuery {
		secrets = append(secrets, u.RawQuery)
		for _, values := range u.Query() {
			for _, value := range values {
				secrets = append(secrets, value, url.QueryEscape(value))
			}
		}
		u.RawQuery = ""
		u.ForceQuery = false
	}
	if u.Fragment != "" || u.RawFragment != "" || strings.Contains(raw, "#") {
		secrets = append(secrets, u.Fragment, u.RawFragment, url.PathEscape(u.Fragment))
		u.Fragment = ""
		u.RawFragment = ""
	}
	remote = u.String()
	reason = strings.ReplaceAll(reason, raw, remote)
	for _, secret := range secrets {
		if secret != "" {
			reason = strings.ReplaceAll(reason, secret, "[REDACTED]")
		}
	}
	return remote, reason
}

// asProject renders a stored project on the wire.
func asProject(p store.Project) projectBody {
	remote, _ := withoutCredentials(p.RemoteURL, nil)
	return projectBody{
		ID:                p.ID,
		Name:              p.Name,
		Kind:              string(p.Kind),
		RemoteURL:         remote,
		RemoteUsername:    p.RemoteUsername,
		RemotePasswordSet: len(p.RemotePassword) > 0,
		HostPath:          p.HostPath,
		DefaultBranch:     p.DefaultBranch,
		CreatedAt:         p.CreatedAt,
	}
}

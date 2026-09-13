package server

import (
	"context"
	"net/http"
	"net/url"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/erlidev/eika/internal/store"
	"github.com/erlidev/eika/internal/workspace/hub"
)

// defaultBranch is the branch a project uses when the request names none.
const defaultBranch = "main"

// credentialEnvironment is the accepted name of an environment variable
// that holds a remote credential. The EIKA_ prefix keeps secret sources
// explicit and follows the configuration policy.
var credentialEnvironment = regexp.MustCompile(`^EIKA_[A-Za-z0-9_]+$`)

// projectBody is one project on the wire.
type projectBody struct {
	ID                string    `json:"id"`
	Name              string    `json:"name"`
	Kind              string    `json:"kind"`
	RemoteURL         string    `json:"remote_url,omitempty"`
	RemoteUsernameEnv string    `json:"remote_username_env,omitempty"`
	RemotePasswordEnv string    `json:"remote_password_env,omitempty"`
	HostPath          string    `json:"host_path,omitempty"`
	DefaultBranch     string    `json:"default_branch"`
	CreatedAt         time.Time `json:"created_at"`
}

// createProjectRequest is the body of POST /api/projects.
type createProjectRequest struct {
	Name              string `json:"name"`
	Kind              string `json:"kind"`
	RemoteURL         string `json:"remote_url"`
	RemoteUsernameEnv string `json:"remote_username_env"`
	RemotePasswordEnv string `json:"remote_password_env"`
	HostPath          string `json:"host_path"`
	DefaultBranch     string `json:"default_branch"`
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
		Name:              strings.TrimSpace(req.Name),
		Kind:              store.ProjectKind(req.Kind),
		RemoteURL:         strings.TrimSpace(req.RemoteURL),
		RemoteUsernameEnv: strings.TrimSpace(req.RemoteUsernameEnv),
		RemotePasswordEnv: strings.TrimSpace(req.RemotePasswordEnv),
		HostPath:          strings.TrimSpace(req.HostPath),
		DefaultBranch:     strings.TrimSpace(req.DefaultBranch),
	}
	if p.DefaultBranch == "" {
		p.DefaultBranch = defaultBranch
	}
	switch p.Kind {
	case store.ProjectRemote:
		if p.RemoteURL == "" {
			return store.Project{}, invalidf("remote_url is required for a remote project")
		}
		if err := validateRemoteCredentials(p); err != nil {
			return store.Project{}, err
		}
	case store.ProjectLocal:
		if p.RemoteUsernameEnv != "" || p.RemotePasswordEnv != "" {
			return store.Project{}, invalidf("remote credential environments are only valid for a remote project")
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
		creds := hub.Credentials{
			UsernameEnv: p.RemoteUsernameEnv,
			PasswordEnv: p.RemotePasswordEnv,
		}
		if err := s.deps.Hub.Mirror(ctx, p.Name, p.RemoteURL, creds); err != nil {
			remote, reason := withoutCredentials(p.RemoteURL, err)
			s.log.Error("mirror remote", "project", p.Name, "remote", remote, "error", reason)
			return store.Project{}, invalidf("mirror %s: %s", remote, reason)
		}
	}
	return s.deps.Store.CreateProject(ctx, p)
}

// validateRemoteCredentials rejects credentials in a URL and validates the
// matched environment references used instead.
func validateRemoteCredentials(p store.Project) error {
	u, err := url.Parse(p.RemoteURL)
	if err != nil {
		return invalidf("remote_url could not be parsed")
	}
	if u.User != nil {
		return invalidf("remote_url must not contain credentials; use remote credential environments")
	}
	if u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || strings.Contains(p.RemoteURL, "#") {
		return invalidf("remote_url must not contain a query string or fragment")
	}
	if (p.RemoteUsernameEnv == "") != (p.RemotePasswordEnv == "") {
		return invalidf("remote_username_env and remote_password_env must both be set")
	}
	for name, value := range map[string]string{
		"remote_username_env": p.RemoteUsernameEnv,
		"remote_password_env": p.RemotePasswordEnv,
	} {
		if value != "" && !credentialEnvironment.MatchString(value) {
			return invalidf("%s must name an EIKA_* environment variable", name)
		}
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
		RemoteUsernameEnv: p.RemoteUsernameEnv,
		RemotePasswordEnv: p.RemotePasswordEnv,
		HostPath:          p.HostPath,
		DefaultBranch:     p.DefaultBranch,
		CreatedAt:         p.CreatedAt,
	}
}

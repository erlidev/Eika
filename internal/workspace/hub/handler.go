package hub

import (
	"crypto/subtle"
	"net/http"
	"net/http/cgi"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Prefix is the path the hub is served under, so a workspace clones from
// <harness>/git/<project>.git.
const Prefix = "/git"

// Handler serves the hub over git's Smart HTTP protocol. A workspace
// authenticates with HTTP basic auth: its workspace id as the user and the
// token granted by Grant as the password.
func (h *Hub) Handler() http.Handler {
	backend := gitHTTPBackend()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if backend == "" {
			http.Error(w, "git-http-backend is not installed", http.StatusServiceUnavailable)
			return
		}
		user, token, ok := r.BasicAuth()
		if !ok || !h.authenticated(user, token) {
			w.Header().Set("WWW-Authenticate", `Basic realm="eika"`)
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		project, ok := projectOf(r.URL.Path)
		if !ok {
			http.Error(w, "no such project", http.StatusNotFound)
			return
		}
		// A workspace's token works on its own project and nothing else.
		if !h.permitted(user, project) {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		path, err := h.Path(project)
		if err != nil {
			http.Error(w, "no such project", http.StatusNotFound)
			return
		}
		if _, err := os.Stat(path); err != nil {
			http.Error(w, "no such project", http.StatusNotFound)
			return
		}
		(&cgi.Handler{
			Path: backend,
			Root: Prefix,
			Dir:  h.root,
			Env: []string{
				"GIT_PROJECT_ROOT=" + h.root,
				"GIT_HTTP_EXPORT_ALL=1",
				"REMOTE_USER=" + user,
			},
		}).ServeHTTP(w, r)
	})
}

// authenticated reports whether the workspace holds the token it was granted.
func (h *Hub) authenticated(workspaceID, token string) bool {
	h.mu.RLock()
	g, ok := h.grants[workspaceID]
	h.mu.RUnlock()
	return ok && subtle.ConstantTimeCompare([]byte(g.token), []byte(token)) == 1
}

// permitted reports whether the workspace's grant covers the project.
func (h *Hub) permitted(workspaceID, project string) bool {
	h.mu.RLock()
	g, ok := h.grants[workspaceID]
	h.mu.RUnlock()
	return ok && g.project == project
}

// projectOf extracts the project name from a request path of the shape
// /git/<project>.git/...
func projectOf(path string) (string, bool) {
	rest, ok := strings.CutPrefix(path, Prefix+"/")
	if !ok {
		return "", false
	}
	first, _, _ := strings.Cut(rest, "/")
	name, ok := strings.CutSuffix(first, ".git")
	if !ok || !projectName.MatchString(name) {
		return "", false
	}
	return name, true
}

// gitHTTPBackend locates the git-http-backend CGI program, returning an empty
// string when git is not installed.
func gitHTTPBackend() string {
	out, err := exec.Command("git", "--exec-path").Output()
	if err != nil {
		return ""
	}
	path := filepath.Join(strings.TrimSpace(string(out)), "git-http-backend")
	if _, err := os.Stat(path); err != nil {
		return ""
	}
	return path
}

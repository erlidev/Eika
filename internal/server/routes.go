package server

import (
	"net/http"
	"os"
	"path/filepath"

	"github.com/erlidev/eika/internal/workspace/hub"
)

// routes registers every route the server serves. It is the one place to look
// for the shape of the HTTP surface; docs/api/http.md describes each route.
func (s *Server) routes() {
	s.mux.HandleFunc("GET /healthz", s.handleHealth)
	s.mux.HandleFunc("GET /api/healthz", s.handleHealth)

	// The event stream needs nothing but the bus, so it is served even by a
	// harness that has no database; every other route reads or writes rows.
	api := http.NewServeMux()
	api.HandleFunc("GET /api/events", s.handleEvents)
	if s.deps.Store != nil {
		s.resourceRoutes(api)
	}
	s.mux.Handle("/api/", s.authenticated(api))

	// The hub authenticates workspaces itself, with a per-workspace token
	// scoped to one project, so it is mounted outside the bearer token.
	if s.deps.Hub != nil {
		s.mux.Handle(hub.Prefix+"/", s.deps.Hub.Handler())
	}
	// The frontend is the catch-all, so it is registered without a method:
	// a pattern with a method cannot be more general than "/api/".
	if s.opts.WebDir != "" {
		s.mux.Handle("/", s.handleWeb(s.opts.WebDir))
	}
}

// resourceRoutes registers the routes that read or write the database.
func (s *Server) resourceRoutes(api *http.ServeMux) {
	api.HandleFunc("GET /api/projects", s.handleListProjects)
	api.HandleFunc("POST /api/projects", s.handleCreateProject)
	api.HandleFunc("GET /api/projects/{id}", s.handleProject)
	api.HandleFunc("DELETE /api/projects/{id}", s.handleDeleteProject)

	api.HandleFunc("GET /api/workspaces", s.handleListWorkspaces)
	api.HandleFunc("POST /api/workspaces", s.handleCreateWorkspace)
	api.HandleFunc("GET /api/workspaces/{id}", s.handleWorkspace)
	api.HandleFunc("DELETE /api/workspaces/{id}", s.handleDeleteWorkspace)
	api.HandleFunc("POST /api/workspaces/{id}/start", s.handleStartWorkspace)
	api.HandleFunc("POST /api/workspaces/{id}/stop", s.handleStopWorkspace)
	api.HandleFunc("GET /api/workspaces/{id}/diff", s.handleWorkspaceDiff)

	api.HandleFunc("GET /api/sessions", s.handleListSessions)
	api.HandleFunc("POST /api/sessions", s.handleCreateSession)
	api.HandleFunc("GET /api/sessions/{id}", s.handleSession)
	api.HandleFunc("DELETE /api/sessions/{id}", s.handleDeleteSession)
	api.HandleFunc("GET /api/sessions/{id}/outline", s.handleSessionOutline)
	api.HandleFunc("GET /api/sessions/{id}/path", s.handleSessionPath)
	api.HandleFunc("POST /api/sessions/{id}/head", s.handleSetSessionHead)
	api.HandleFunc("POST /api/sessions/{id}/fork", s.handleForkSession)

	api.HandleFunc("POST /api/sessions/{id}/messages", s.handlePostMessage)
	api.HandleFunc("GET /api/sessions/{id}/run", s.handleSessionRun)
	api.HandleFunc("POST /api/runs/{id}/abort", s.handleAbortRun)
	api.HandleFunc("POST /api/questions/{id}/answer", s.handleAnswerQuestion)

	api.HandleFunc("GET /api/settings", s.handleSettings)
	api.HandleFunc("PUT /api/settings", s.handlePutSettings)
	api.HandleFunc("GET /api/models", s.handleModels)
}

// health is the body of a health check response.
type health struct {
	Status string `json:"status"`
}

// handleHealth reports that the process is up. The health checks are the only
// routes that never require authentication.
func (s *Server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, s.log, http.StatusOK, health{Status: "ok"})
}

// handleWeb serves the built frontend, falling back to index.html so that
// client-side routes resolve on a full page load.
func (s *Server) handleWeb(dir string) http.Handler {
	files := http.FileServer(http.Dir(dir))
	index := filepath.Join(dir, "index.html")
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		// Anything that is not a regular file is a client-side route, not a
		// static asset. Directories included: the UI never wants a listing.
		fi, err := os.Stat(filepath.Join(dir, filepath.Clean(r.URL.Path)))
		if err != nil || fi.IsDir() {
			http.ServeFile(w, r, index)
			return
		}
		files.ServeHTTP(w, r)
	})
}

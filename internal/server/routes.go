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
	// A terminal reaches the workspace host and nothing else, so it is served
	// whenever there is a host.
	if s.deps.Workspaces != nil {
		api.HandleFunc("GET /api/workspaces/{id}/terminal", s.handleTerminal)
	}
	if s.deps.Store != nil {
		// Setup and sign-in are how a browser gets a token, so they are the
		// routes under /api that need none.
		s.mux.HandleFunc("GET /api/auth/status", s.handleAuthStatus)
		s.mux.HandleFunc("POST /api/auth/setup", s.handleSetup)
		s.mux.HandleFunc("POST /api/auth/login", s.handleLogin)
		s.resourceRoutes(api)
	}
	s.mux.Handle("/api/", s.authenticated(api))

	// An OAuth authorization server fetches the harness's client metadata
	// document to learn who Eika is, with no token to present.
	if s.deps.MCP != nil {
		s.mux.HandleFunc("GET /oauth/client-metadata.json", s.handleClientMetadata)
	}
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
	api.HandleFunc("POST /api/auth/logout", s.handleLogout)
	api.HandleFunc("PUT /api/auth/password", s.handleChangePassword)

	api.HandleFunc("GET /api/projects", s.handleListProjects)
	api.HandleFunc("POST /api/projects", s.handleCreateProject)
	api.HandleFunc("GET /api/projects/{id}", s.handleProject)
	api.HandleFunc("PATCH /api/projects/{id}", s.handleUpdateProject)
	api.HandleFunc("DELETE /api/projects/{id}", s.handleDeleteProject)

	api.HandleFunc("GET /api/workspaces", s.handleListWorkspaces)
	api.HandleFunc("POST /api/workspaces", s.handleCreateWorkspace)
	api.HandleFunc("GET /api/workspaces/{id}", s.handleWorkspace)
	api.HandleFunc("DELETE /api/workspaces/{id}", s.handleDeleteWorkspace)
	api.HandleFunc("POST /api/workspaces/{id}/start", s.handleStartWorkspace)
	api.HandleFunc("POST /api/workspaces/{id}/stop", s.handleStopWorkspace)
	api.HandleFunc("POST /api/workspaces/{id}/merge", s.handleMergeWorkspace)
	api.HandleFunc("GET /api/workspaces/{id}/diff", s.handleWorkspaceDiff)
	api.HandleFunc("POST /api/workspaces/{id}/commit", s.handleCommitWorkspace)
	api.HandleFunc("POST /api/workspaces/{id}/push", s.handlePushWorkspace)
	api.HandleFunc("GET /api/workspaces/{id}/files", s.handleListFiles)
	api.HandleFunc("GET /api/workspaces/{id}/file", s.handleReadFile)
	api.HandleFunc("PUT /api/workspaces/{id}/file", s.handleWriteFile)
	api.HandleFunc("PUT /api/workspaces/{id}/sandbox", s.handleSetWorkspaceSandbox)
	api.HandleFunc("GET /api/workspaces/{id}/usage", s.handleWorkspaceUsage)
	api.HandleFunc("POST /api/workspaces/{id}/ports/{port}/preview", s.handleOpenPreview)

	api.HandleFunc("GET /api/sessions", s.handleListSessions)
	api.HandleFunc("POST /api/sessions", s.handleCreateSession)
	api.HandleFunc("GET /api/sessions/{id}", s.handleSession)
	api.HandleFunc("DELETE /api/sessions/{id}", s.handleDeleteSession)
	api.HandleFunc("GET /api/sessions/{id}/outline", s.handleSessionOutline)
	api.HandleFunc("GET /api/sessions/{id}/path", s.handleSessionPath)
	api.HandleFunc("POST /api/sessions/{id}/head", s.handleSetSessionHead)
	api.HandleFunc("POST /api/sessions/{id}/fork", s.handleForkSession)
	api.HandleFunc("PUT /api/sessions/{id}/tools", s.handleSetSessionTools)
	api.HandleFunc("GET /api/sessions/{id}/configuration", s.handleSessionConfiguration)
	api.HandleFunc("PUT /api/sessions/{id}/profile", s.handleSetSessionProfile)
	api.HandleFunc("PUT /api/sessions/{id}/overrides", s.handleSetSessionOverrides)
	api.HandleFunc("GET /api/sessions/{id}/context", s.handleSessionContext)
	api.HandleFunc("GET /api/sessions/{id}/requests", s.handleSessionRequests)
	api.HandleFunc("GET /api/sessions/{id}/requests/{request_id}", s.handleSessionRequest)
	api.HandleFunc("GET /api/sessions/{id}/agents", s.handleSessionAgents)
	api.HandleFunc("POST /api/subagents/{id}/abort", s.handleAbortSubagent)

	api.HandleFunc("POST /api/sessions/{id}/messages", s.handlePostMessage)
	api.HandleFunc("GET /api/sessions/{id}/run", s.handleSessionRun)
	api.HandleFunc("POST /api/runs/{id}/abort", s.handleAbortRun)
	api.HandleFunc("POST /api/questions/{id}/answer", s.handleAnswerQuestion)
	api.HandleFunc("GET /api/tools", s.handleListTools)

	api.HandleFunc("GET /api/providers", s.handleListProviders)
	api.HandleFunc("POST /api/providers", s.handleCreateProvider)
	api.HandleFunc("POST /api/providers/probe", s.handleProbeProvider)
	api.HandleFunc("PATCH /api/providers/{id}", s.handleUpdateProvider)
	api.HandleFunc("DELETE /api/providers/{id}", s.handleDeleteProvider)

	api.HandleFunc("GET /api/models", s.handleListModels)
	api.HandleFunc("POST /api/models", s.handleCreateModel)
	api.HandleFunc("POST /api/models/test", s.handleTestModel)
	api.HandleFunc("PATCH /api/models/{id}", s.handleUpdateModel)
	api.HandleFunc("DELETE /api/models/{id}", s.handleDeleteModel)

	api.HandleFunc("GET /api/profiles", s.handleListProfiles)
	api.HandleFunc("GET /api/profiles/inherited", s.handleInheritedProfile)
	api.HandleFunc("POST /api/profiles", s.handleCreateProfile)
	api.HandleFunc("PUT /api/profiles/{id}", s.handleUpdateProfile)
	api.HandleFunc("DELETE /api/profiles/{id}", s.handleDeleteProfile)

	if s.deps.Search != nil {
		api.HandleFunc("POST /api/search", s.handleSearch)
		api.HandleFunc("GET /api/search/status", s.handleSearchStatus)
		api.HandleFunc("PUT /api/search/keys/{name}", s.handlePutSearchKey)
	}

	if s.deps.MCP != nil {
		api.HandleFunc("GET /api/mcp/servers", s.handleListMCPServers)
		api.HandleFunc("POST /api/mcp/servers", s.handleCreateMCPServer)
		api.HandleFunc("GET /api/mcp/servers/{id}", s.handleMCPServer)
		api.HandleFunc("PATCH /api/mcp/servers/{id}", s.handleUpdateMCPServer)
		api.HandleFunc("DELETE /api/mcp/servers/{id}", s.handleDeleteMCPServer)
		api.HandleFunc("POST /api/mcp/servers/{id}/connect", s.handleConnectMCPServer)
		api.HandleFunc("POST /api/mcp/servers/{id}/authorize", s.handleAuthorizeMCPServer)
		api.HandleFunc("DELETE /api/mcp/servers/{id}/authorization", s.handleSignOutMCPServer)
		api.HandleFunc("POST /api/mcp/servers/{id}/resources/read", s.handleReadMCPResource)
		api.HandleFunc("POST /api/mcp/servers/{id}/prompts/get", s.handleGetMCPPrompt)
		api.HandleFunc("POST /api/mcp/oauth/callback", s.handleMCPCallback)
		api.HandleFunc("POST /api/elicitations/{id}/answer", s.handleAnswerElicitation)
	}

	api.HandleFunc("GET /api/settings", s.handleSettings)
	api.HandleFunc("PUT /api/settings", s.handlePutSettings)
	api.HandleFunc("GET /api/system", s.handleSystem)
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

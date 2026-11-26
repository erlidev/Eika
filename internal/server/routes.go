package server

import (
	"net/http"
	"os"
	"path/filepath"
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
		// Setup and sign-in are how a browser gets a token, so they are the
		// routes under /api that need none.
		s.mux.HandleFunc("GET /api/auth/status", s.handleAuthStatus)
		s.mux.HandleFunc("POST /api/auth/setup", s.handleSetup)
		s.mux.HandleFunc("POST /api/auth/login", s.handleLogin)
		s.resourceRoutes(api)
		s.liveRoutes(api)
	}
	s.mux.Handle("/api/", s.authenticated(api))

	// An OAuth authorization server fetches the harness's client metadata
	// document to learn who Eika is, with no token to present.
	if s.deps.MCP != nil {
		s.mux.HandleFunc("GET /oauth/client-metadata.json", s.handleClientMetadata)
	}
	// The frontend is the catch-all, so it is registered without a method:
	// a pattern with a method cannot be more general than "/api/".
	if s.opts.WebDir != "" {
		s.mux.Handle("/", s.handleWeb(s.opts.WebDir))
	}
}

// resourceRoutes registers the routes that read or write the database.
func (s *Server) resourceRoutes(api *http.ServeMux) {
	api.HandleFunc("GET /api/routines", s.handleRoutines)
	api.HandleFunc("POST /api/routines", s.handleCreateRoutine)
	api.HandleFunc("GET /api/routines/{id}", s.handleRoutine)
	api.HandleFunc("PATCH /api/routines/{id}", s.handleUpdateRoutine)
	api.HandleFunc("DELETE /api/routines/{id}", s.handleDeleteRoutine)
	api.HandleFunc("POST /api/routines/{id}/approve", s.handleRoutineEnable(true))
	api.HandleFunc("POST /api/routines/{id}/disable", s.handleRoutineEnable(false))
	api.HandleFunc("GET /api/routines/{id}/runs", s.handleRoutineRuns)
	api.HandleFunc("GET /api/wakeups", s.handleWakeups)
	api.HandleFunc("DELETE /api/wakeups/{id}", s.handleCancelWakeup)
	api.HandleFunc("GET /api/instructions", s.handleInstructions)
	api.HandleFunc("PUT /api/instructions", s.handlePutInstructions)
	api.HandleFunc("DELETE /api/instructions", s.handleDeleteInstructions)
	api.HandleFunc("GET /api/projects/{id}/instructions", s.handleProjectInstructions)
	api.HandleFunc("PUT /api/projects/{id}/instructions", s.handlePutProjectInstructions)
	api.HandleFunc("DELETE /api/projects/{id}/instructions", s.handleDeleteProjectInstructions)

	api.HandleFunc("GET /api/projects", s.handleListProjects)
	api.HandleFunc("POST /api/projects", s.handleCreateProject)
	api.HandleFunc("GET /api/projects/{id}", s.handleProject)
	api.HandleFunc("PATCH /api/projects/{id}", s.handleUpdateProject)

	api.HandleFunc("POST /api/auth/logout", s.handleLogout)
	api.HandleFunc("PUT /api/auth/password", s.handleChangePassword)

	api.HandleFunc("GET /api/computers", s.handleListComputers)

	api.HandleFunc("GET /api/memories", s.handleListMemories)
	api.HandleFunc("GET /api/memories/{name}", s.handleMemory)
	api.HandleFunc("PATCH /api/memories/{name}", s.handleEditMemory)
	api.HandleFunc("POST /api/memories/{name}/pin", s.handlePinMemory(true))
	api.HandleFunc("POST /api/memories/{name}/unpin", s.handlePinMemory(false))
	api.HandleFunc("POST /api/memories/{name}/archive", s.handleArchiveMemory)
	api.HandleFunc("DELETE /api/memories/{name}", s.handlePurgeMemory)
	api.HandleFunc("GET /api/inbox/count", s.handleInboxCount)
	api.HandleFunc("GET /api/inbox/memories", s.handleInboxMemories)
	api.HandleFunc("POST /api/inbox/memories/{name}/accept", s.handleAcceptMemory)
	api.HandleFunc("POST /api/inbox/memories/{name}/reject", s.handleRejectMemory)

	api.HandleFunc("POST /api/skills", s.handleInstallSkill)
	api.HandleFunc("GET /api/skills", s.handleListSkills)
	api.HandleFunc("GET /api/skills/{name}", s.handleSkill)
	api.HandleFunc("POST /api/skills/{name}/enable", s.handleSetSkillEnabled(true))
	api.HandleFunc("POST /api/skills/{name}/disable", s.handleSetSkillEnabled(false))
	api.HandleFunc("DELETE /api/skills/{name}", s.handleDeleteSkill)

	api.HandleFunc("GET /api/home", s.handleHomeSession)
	api.HandleFunc("GET /api/sessions", s.handleListSessions)
	api.HandleFunc("GET /api/sessions/{id}", s.handleSession)
	api.HandleFunc("PATCH /api/sessions/{id}", s.handleUpdateSession)
	api.HandleFunc("DELETE /api/sessions/{id}", s.handleDeleteSession)
	api.HandleFunc("GET /api/sessions/{id}/outline", s.handleSessionOutline)
	api.HandleFunc("GET /api/sessions/{id}/entries", s.handleSessionEntries)
	api.HandleFunc("GET /api/sessions/{id}/path", s.handleSessionPath)
	api.HandleFunc("POST /api/sessions/{id}/head", s.handleSetSessionHead)
	api.HandleFunc("GET /api/sessions/{id}/context", s.handleSessionContext)
	api.HandleFunc("GET /api/sessions/{id}/prediction", s.handleSessionPrediction)
	api.HandleFunc("GET /api/sessions/{id}/calls", s.handleSessionCalls)
	api.HandleFunc("GET /api/sessions/{id}/calls/{call_id}", s.handleSessionCall)

	api.HandleFunc("POST /api/sessions/{id}/messages", s.handlePostMessage)
	api.HandleFunc("GET /api/sessions/{id}/run", s.handleSessionRun)
	api.HandleFunc("POST /api/sessions/{id}/compact", s.handleCompact)
	api.HandleFunc("POST /api/sessions/{id}/cancel", s.handleCancelSubagent)
	api.HandleFunc("POST /api/runs/{id}/abort", s.handleAbortRun)
	api.HandleFunc("POST /api/questions/{id}/answer", s.handleAnswerQuestion)
	api.HandleFunc("GET /api/tools", s.handleListTools)

	api.HandleFunc("GET /api/guard", s.handleGuard)
	api.HandleFunc("GET /api/halt", s.handleHalt)
	api.HandleFunc("POST /api/halt", s.handleSetHalt)
	api.HandleFunc("POST /api/halt/resume", s.handleResume)
	api.HandleFunc("GET /api/blocked-hosts", s.handleBlockedHosts)
	api.HandleFunc("POST /api/blocked-hosts", s.handleAddBlockedHost)
	api.HandleFunc("DELETE /api/blocked-hosts/{host}", s.handleRemoveBlockedHost)
	api.HandleFunc("GET /api/trusted-sources", s.handleTrustedSources)
	api.HandleFunc("POST /api/trusted-sources", s.handleAddTrustedSource)
	api.HandleFunc("DELETE /api/trusted-sources/{host}", s.handleRemoveTrustedSource)
	api.HandleFunc("POST /api/withheld/{id}/trust", s.handleTrustWithheld)
	api.HandleFunc("GET /api/withheld", s.handleListWithheld)
	api.HandleFunc("GET /api/withheld/{id}", s.handleWithheld)

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

	api.HandleFunc("GET /api/agents", s.handleListAgents)
	api.HandleFunc("GET /api/agents/{name}", s.handleAgent)
	api.HandleFunc("PUT /api/agents/{name}", s.handlePutAgent)
	api.HandleFunc("DELETE /api/agents/{name}", s.handleResetAgent)

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

	api.HandleFunc("POST /api/notifications/test", s.handleTestNotification)
	api.HandleFunc("GET /api/settings", s.handleSettings)
	api.HandleFunc("PUT /api/settings", s.handlePutSettings)
	api.HandleFunc("GET /api/system", s.handleSystem)
	api.HandleFunc("GET /api/inference", s.handleInference)
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
		w.Header().Set("Cache-Control", "no-cache")
		switch r.URL.Path {
		case "/manifest.webmanifest":
			w.Header().Set("Content-Type", "application/manifest+json")
		case "/service-worker.js":
			w.Header().Set("Content-Type", "application/javascript")
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

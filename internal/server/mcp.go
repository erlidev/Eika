package server

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/erlidev/eika/internal/mcp"
	"github.com/erlidev/eika/internal/store"
)

// handleListMCPServers lists the configured MCP servers by name, with the
// state each is in.
func (s *Server) handleListMCPServers(w http.ResponseWriter, r *http.Request) {
	rows, err := s.deps.Store.MCPServers(r.Context())
	if err != nil {
		s.fail(w, r, err)
		return
	}
	out := mcpServersResponse{Servers: make([]mcpServerBody, 0, len(rows))}
	for _, row := range rows {
		out.Servers = append(out.Servers, s.asMCPServer(row))
	}
	writeJSON(w, s.log, http.StatusOK, out)
}

// handleCreateMCPServer adds a server. A remote one that is on starts
// connecting at once; a stdio one starts in a workspace when a run there
// first asks for its tools.
func (s *Server) handleCreateMCPServer(w http.ResponseWriter, r *http.Request) {
	req, err := decodeJSON[createMCPServerRequest](r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	row := store.MCPServer{
		Name:          strings.TrimSpace(req.Name),
		Kind:          strings.TrimSpace(req.Kind),
		URL:           strings.TrimSpace(req.URL),
		Command:       strings.TrimSpace(req.Command),
		Args:          req.Args,
		Enabled:       req.Enabled == nil || *req.Enabled,
		DisabledTools: toolList(req.DisabledTools),
		OAuthClientID: strings.TrimSpace(req.OAuthClientID),
	}
	if err := validateMCPServer(row, req.Headers, req.Env, req.OAuthClientSecret); err != nil {
		s.fail(w, r, err)
		return
	}
	if row.Headers, err = s.sealPairs(req.Headers); err == nil {
		if row.Env, err = s.sealPairs(req.Env); err == nil {
			row.OAuthClientSecret, err = s.deps.Secrets.Seal(req.OAuthClientSecret)
		}
	}
	if err != nil {
		s.fail(w, r, err)
		return
	}
	created, err := s.deps.Store.CreateMCPServer(r.Context(), row)
	if err != nil {
		if errors.Is(err, store.ErrConflict) {
			err = conflictf("an MCP server named %q already exists", row.Name)
		}
		s.fail(w, r, err)
		return
	}
	s.deps.MCP.Changed(r.Context(), created.ID)
	s.log.Info("mcp server created", "server_id", created.ID, "name", created.Name, "kind", created.Kind)
	writeJSON(w, s.log, http.StatusCreated, s.asMCPServer(created))
}

// handleMCPServer reports everything the harness knows of one server.
func (s *Server) handleMCPServer(w http.ResponseWriter, r *http.Request) {
	s.writeMCPDetails(w, r, r.PathValue("id"))
}

// handleUpdateMCPServer changes a server. Its connections end, and a remote
// server that is on connects again with the new configuration.
func (s *Server) handleUpdateMCPServer(w http.ResponseWriter, r *http.Request) {
	req, err := decodeJSON[updateMCPServerRequest](r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	row, err := s.deps.Store.MCPServer(r.Context(), r.PathValue("id"))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	moved := false
	if req.Name != nil {
		row.Name = strings.TrimSpace(*req.Name)
	}
	if req.URL != nil {
		next := strings.TrimSpace(*req.URL)
		moved = next != row.URL
		row.URL = next
	}
	if req.Command != nil {
		row.Command = strings.TrimSpace(*req.Command)
	}
	if req.Args != nil {
		row.Args = req.Args
	}
	if req.Enabled != nil {
		row.Enabled = *req.Enabled
	}
	if req.DisabledTools != nil {
		row.DisabledTools = toolList(req.DisabledTools)
	}
	if req.OAuthClientID != nil {
		row.OAuthClientID = strings.TrimSpace(*req.OAuthClientID)
	}
	// Headers belong to the server they were entered for: a server that
	// moved to another URL keeps them only when the request says so, as a
	// provider's key does.
	headers, err := s.mergePairs(row.Headers, req.Headers, moved)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	env, err := s.mergePairs(row.Env, req.Env, false)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	// A client secret belongs to its client id: clearing the id clears it.
	newSecret := req.OAuthClientSecret
	if newSecret == nil && row.OAuthClientID == "" && len(row.OAuthClientSecret) > 0 {
		newSecret = new(string)
	}
	secretValue, err := s.deps.Secrets.Open(row.OAuthClientSecret)
	if newSecret != nil {
		secretValue, err = strings.TrimSpace(*newSecret), nil
	}
	if err != nil {
		s.fail(w, r, s.unreadableMCP(row, err))
		return
	}
	if err := validateMCPServer(row, headers, env, secretValue); err != nil {
		s.fail(w, r, err)
		return
	}
	if req.Headers != nil || moved {
		if row.Headers, err = s.sealPairs(headers); err != nil {
			s.fail(w, r, err)
			return
		}
	}
	if req.Env != nil {
		if row.Env, err = s.sealPairs(env); err != nil {
			s.fail(w, r, err)
			return
		}
	}
	if newSecret != nil {
		if row.OAuthClientSecret, err = s.deps.Secrets.Seal(secretValue); err != nil {
			s.fail(w, r, err)
			return
		}
	}
	updated, err := s.deps.Store.UpdateMCPServer(r.Context(), row)
	if err != nil {
		if errors.Is(err, store.ErrConflict) {
			err = conflictf("an MCP server named %q already exists", row.Name)
		}
		s.fail(w, r, err)
		return
	}
	// A token is for the server it was issued to; one at another URL gets
	// it only by being authorized again.
	if moved {
		if err := s.forgetMCPTokens(r.Context(), updated.ID); err != nil {
			s.fail(w, r, err)
			return
		}
	}
	s.deps.MCP.Changed(r.Context(), updated.ID)
	s.log.Info("mcp server updated", "server_id", updated.ID, "moved", moved,
		"headers_changed", req.Headers != nil, "env_changed", req.Env != nil)
	writeJSON(w, s.log, http.StatusOK, s.asMCPServer(updated))
}

// handleDeleteMCPServer removes a server, its credentials, and its
// connections. A run already going keeps the tools it started with, whose
// calls now fail.
func (s *Server) handleDeleteMCPServer(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := s.deps.Store.DeleteMCPServer(r.Context(), id); err != nil {
		s.fail(w, r, err)
		return
	}
	s.deps.MCP.Removed(id)
	s.log.Info("mcp server deleted", "server_id", id)
	w.WriteHeader(http.StatusNoContent)
}

// handleConnectMCPServer drops a server's connection and connects it again,
// in the given workspace for a stdio server, and reports how that went.
func (s *Server) handleConnectMCPServer(w http.ResponseWriter, r *http.Request) {
	req, err := decodeJSON[mcpWorkspaceRequest](r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	id := r.PathValue("id")
	// A failed attempt is in the details, as the state and the log say it;
	// only a server that cannot be tried at all fails the request.
	if err := s.deps.MCP.Reconnect(r.Context(), id, req.WorkspaceID); err != nil {
		if status, _ := statusOf(err); status != http.StatusInternalServerError {
			s.fail(w, r, err)
			return
		}
		s.log.Info("mcp server did not connect", "server_id", id, "error", err)
	}
	s.writeMCPDetails(w, r, id)
}

// handleReadMCPResource reads one resource of a server for the user.
func (s *Server) handleReadMCPResource(w http.ResponseWriter, r *http.Request) {
	req, err := decodeJSON[readResourceRequest](r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if req.URI == "" || len(req.URI) > maxMCPResourceURI {
		s.fail(w, r, invalidf("uri is required and at most %d bytes", maxMCPResourceURI))
		return
	}
	res, err := s.deps.MCP.ReadResource(r.Context(), r.PathValue("id"), req.WorkspaceID, req.URI)
	if err != nil {
		s.fail(w, r, mcpFailure("read the resource", err))
		return
	}
	writeJSON(w, s.log, http.StatusOK, readResourceResponse{Contents: mcp.ResourceDetails(res.Contents)})
}

// handleGetMCPPrompt renders one prompt of a server with its arguments.
func (s *Server) handleGetMCPPrompt(w http.ResponseWriter, r *http.Request) {
	req, err := decodeJSON[getPromptRequest](r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if req.Name == "" || len(req.Name) > maxMCPToolName {
		s.fail(w, r, invalidf("name is required and at most %d bytes", maxMCPToolName))
		return
	}
	if len(req.Arguments) > maxMCPPromptArgs {
		s.fail(w, r, invalidf("a prompt takes at most %d arguments", maxMCPPromptArgs))
		return
	}
	res, err := s.deps.MCP.GetPrompt(r.Context(), r.PathValue("id"), req.WorkspaceID, req.Name, req.Arguments)
	if err != nil {
		s.fail(w, r, mcpFailure("get the prompt", err))
		return
	}
	writeJSON(w, s.log, http.StatusOK, mcp.PromptDetails(res))
}

// handleAnswerElicitation delivers the user's answer to the MCP tool call
// waiting for it.
func (s *Server) handleAnswerElicitation(w http.ResponseWriter, r *http.Request) {
	req, err := decodeJSON[elicitationAnswerRequest](r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	id := r.PathValue("id")
	if err := s.deps.MCP.Elicitations().Answer(id, mcp.ElicitResult{Action: req.Action, Content: req.Content}); err != nil {
		s.fail(w, r, err)
		return
	}
	s.log.Info("elicitation answered", "elicitation_id", id, "action", req.Action)
	w.WriteHeader(http.StatusNoContent)
}

// writeMCPDetails answers with everything the harness knows of a server.
func (s *Server) writeMCPDetails(w http.ResponseWriter, r *http.Request, id string) {
	row, err := s.deps.Store.MCPServer(r.Context(), id)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	d, err := s.deps.MCP.Details(r.Context(), id)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, s.log, http.StatusOK, s.asMCPDetails(row, d))
}

// mcpFailure reports a failure of an MCP server or its authorization
// server, whose message says what went wrong in terms the user can act on,
// as a request the API could not carry out. A failure of the harness itself
// stays internal.
func mcpFailure(action string, err error) error {
	var internal storeFailure
	if status, _ := statusOf(err); status != http.StatusInternalServerError ||
		errors.As(err, &internal) || errors.Is(err, context.Canceled) {
		return err
	}
	return invalidf("%s: %v", action, err)
}

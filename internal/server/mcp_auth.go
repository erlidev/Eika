package server

import (
	"net/http"
	"net/url"
	"path"
	"strings"

	"github.com/erlidev/eika/internal/mcp"
)

// handleAuthorizeMCPServer starts authorizing a remote server and returns
// where to send the browser. The authorization server sends it back to
// redirect_uri, whose page hands the answer to POST /api/mcp/oauth/callback.
func (s *Server) handleAuthorizeMCPServer(w http.ResponseWriter, r *http.Request) {
	req, err := decodeJSON[authorizeRequest](r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if err := s.checkRedirect(r, req.RedirectURI); err != nil {
		s.fail(w, r, err)
		return
	}
	id := r.PathValue("id")
	target, err := s.deps.MCP.BeginAuthorization(r.Context(), id, req.RedirectURI)
	if err != nil {
		s.fail(w, r, mcpFailure("authorize the server", err))
		return
	}
	s.log.Info("mcp authorization started", "server_id", id)
	writeJSON(w, s.log, http.StatusOK, authorizeResponse{AuthorizationURL: target})
}

// handleMCPCallback finishes an authorization with what the browser brought
// back, and connects the server with its new token.
func (s *Server) handleMCPCallback(w http.ResponseWriter, r *http.Request) {
	req, err := decodeJSON[oauthCallbackRequest](r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	cb := mcp.Callback{State: req.State, Code: req.Code, Error: req.Error, ErrorDescription: req.ErrorDescription}
	if req.Iss != nil {
		cb.Iss, cb.IssPresent = *req.Iss, true
	}
	id, err := s.deps.MCP.CompleteAuthorization(r.Context(), cb)
	if err != nil {
		s.fail(w, r, mcpFailure("finish the authorization", err))
		return
	}
	s.log.Info("mcp server authorized", "server_id", id)
	writeJSON(w, s.log, http.StatusOK, oauthCallbackResponse{ServerID: id})
}

// handleSignOutMCPServer revokes and forgets a server's tokens. The client
// registered with its authorization server is kept for next time.
func (s *Server) handleSignOutMCPServer(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := s.deps.MCP.SignOut(r.Context(), id); err != nil {
		s.fail(w, r, err)
		return
	}
	s.log.Info("mcp server signed out", "server_id", id)
	w.WriteHeader(http.StatusNoContent)
}

// handleClientMetadata serves the harness's OAuth Client ID Metadata
// Document, which an authorization server fetches to learn who Eika is. It
// is public, and exists only for a deployment with an https public_url.
func (s *Server) handleClientMetadata(w http.ResponseWriter, r *http.Request) {
	doc, ok := s.deps.MCP.ClientMetadataDocument()
	if !ok {
		s.fail(w, r, notFoundf("this deployment has no https public_url to identify itself by"))
		return
	}
	writeJSON(w, s.log, http.StatusOK, doc)
}

// checkRedirect accepts a redirect URI for an authorization only when it is
// the frontend's callback route at an address the harness serves the UI at:
// the one the request came to, an allowed origin, or the public URL. The
// authorization server sends the code there.
func (s *Server) checkRedirect(r *http.Request, raw string) error {
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return invalidf("redirect_uri must be an http or https URL")
	}
	if u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Path != mcp.CallbackPath {
		return invalidf("redirect_uri must be the %s page of the web UI, with no credentials, query, or fragment", mcp.CallbackPath)
	}
	host := strings.ToLower(u.Host)
	if host == strings.ToLower(r.Host) {
		return nil
	}
	if public, err := url.Parse(s.cfg.PublicURL); err == nil && s.cfg.PublicURL != "" && strings.EqualFold(public.Host, host) {
		return nil
	}
	for _, pattern := range s.cfg.AllowedOrigins {
		if ok, _ := path.Match(strings.ToLower(pattern), host); ok {
			return nil
		}
	}
	return invalidf("redirect_uri %s is not an address this harness serves the web UI at; add %s to allowed_origins", raw, u.Host)
}

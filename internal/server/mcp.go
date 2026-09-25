package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"maps"
	"net/http"
	"net/url"
	"path"
	"regexp"
	"runtime/debug"
	"slices"
	"strings"
	"time"

	"github.com/erlidev/eika/internal/config"
	"github.com/erlidev/eika/internal/event"
	"github.com/erlidev/eika/internal/executor/sandbox"
	"github.com/erlidev/eika/internal/mcp"
	"github.com/erlidev/eika/internal/mcp/oauth"
	"github.com/erlidev/eika/internal/secret"
	"github.com/erlidev/eika/internal/store"
	"github.com/erlidev/eika/internal/workspace"
)

// Bounds on what the MCP routes accept. A server name is part of every tool
// name its server offers, which Chat Completions cuts at 64 characters, so
// it is kept short enough to leave the tool's own name room.
const (
	maxMCPServerName  = 32
	maxMCPURL         = 2048
	maxMCPCommand     = 1024
	maxMCPArgs        = 64
	maxMCPArg         = 4096
	maxMCPPairs       = 64
	maxMCPPairName    = 256
	maxMCPPairValue   = 8192
	maxMCPToolList    = 512
	maxMCPToolName    = 256
	maxOAuthClientID  = 512
	maxOAuthSecret    = 4096
	maxMCPResourceURI = 4096
	maxMCPPromptArgs  = 64
)

// mcpServerNamePattern is what a server may be called: letters, digits,
// hyphens, and single underscores between them, so that the `__` in
// mcp__<server>__<tool> always marks where the server's name ends.
var mcpServerNamePattern = regexp.MustCompile(`^[A-Za-z0-9-]+(_[A-Za-z0-9-]+)*$`)

// envNamePattern is what an environment variable may be called.
var envNamePattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// headerNamePattern is an HTTP field name, RFC 9110's token.
var headerNamePattern = regexp.MustCompile("^[!#$%&'*+.^_`|~0-9A-Za-z-]+$")

// clientHeaders are the headers the MCP client sets itself, which a server's
// configuration may not replace.
var clientHeaders = []string{
	"accept", "connection", "content-length", "content-type", "host", "last-event-id",
	"mcp-method", "mcp-name", "mcp-protocol-version", "mcp-session-id", "transfer-encoding",
}

// mcpServerBody is one MCP server on the wire. Header and environment values
// and the OAuth client secret never leave the harness; their names do.
type mcpServerBody struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	// Kind is http for a remote server the harness connects to, stdio for a
	// command it starts in each workspace that uses it.
	Kind string `json:"kind"`
	URL  string `json:"url"`
	// HeaderNames are the headers every request carries, sorted.
	HeaderNames []string `json:"header_names"`
	Command     string   `json:"command"`
	Args        []string `json:"args"`
	// EnvNames are the variables the process gets beside the workspace's
	// own, sorted.
	EnvNames      []string `json:"env_names"`
	Enabled       bool     `json:"enabled"`
	DisabledTools []string `json:"disabled_tools"`
	// OAuthClientID is a client the user registered with the server's
	// authorization server by hand.
	OAuthClientID        string `json:"oauth_client_id"`
	OAuthClientSecretSet bool   `json:"oauth_client_secret_set"`
	// State is disabled, idle, connecting, connected, unauthorized, or
	// error; Error says why it is in the last two.
	State string `json:"state"`
	Error string `json:"error,omitempty"`
	// Workspaces are where a stdio server is running now.
	Workspaces []string  `json:"workspaces"`
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
}

// mcpServersResponse is the body of GET /api/mcp/servers.
type mcpServersResponse struct {
	Servers []mcpServerBody `json:"servers"`
}

// mcpServerDetails is the body of GET /api/mcp/servers/{id}: the server,
// what its latest connection learned and listed, its log, and its
// authorization.
type mcpServerDetails struct {
	Server mcpServerBody `json:"server"`
	// Connection is absent until a connection has succeeded.
	Connection        *mcpConnectionBody        `json:"connection,omitempty"`
	Tools             []mcpToolBody             `json:"tools"`
	ExcludedTools     []mcpExcludedToolBody     `json:"excluded_tools"`
	Resources         []mcpResourceBody         `json:"resources"`
	ResourceTemplates []mcpResourceTemplateBody `json:"resource_templates"`
	Prompts           []mcpPromptBody           `json:"prompts"`
	// ListErrors are the lists the server could not give, by method.
	ListErrors map[string]string `json:"list_errors"`
	// FetchedAt is when the lists were read.
	FetchedAt time.Time        `json:"fetched_at,omitzero"`
	Logs      []mcpLogLineBody `json:"logs"`
	Auth      mcpAuthBody      `json:"auth"`
}

// mcpConnectionBody is what connecting learned about a server.
type mcpConnectionBody struct {
	// Era is modern for a 2026-07-28 server, legacy for an initialize-based
	// one.
	Era string `json:"era"`
	// Transport is streamable_http, sse, or stdio.
	Transport       string `json:"transport"`
	ProtocolVersion string `json:"protocol_version"`
	// SupportedVersions are the versions a modern server says it speaks.
	SupportedVersions []string              `json:"supported_versions"`
	ServerInfo        mcpImplementationBody `json:"server_info"`
	Capabilities      mcpCapabilitiesBody   `json:"capabilities"`
	Instructions      string                `json:"instructions,omitempty"`
}

// mcpImplementationBody is how a server introduced itself.
type mcpImplementationBody struct {
	Name        string `json:"name"`
	Title       string `json:"title,omitempty"`
	Version     string `json:"version"`
	Description string `json:"description,omitempty"`
	WebsiteURL  string `json:"website_url,omitempty"`
}

// mcpCapabilitiesBody is what a server said it serves.
type mcpCapabilitiesBody struct {
	Tools                bool `json:"tools"`
	ToolsListChanged     bool `json:"tools_list_changed"`
	Resources            bool `json:"resources"`
	ResourcesSubscribe   bool `json:"resources_subscribe"`
	ResourcesListChanged bool `json:"resources_list_changed"`
	Prompts              bool `json:"prompts"`
	PromptsListChanged   bool `json:"prompts_list_changed"`
	Logging              bool `json:"logging"`
	Completions          bool `json:"completions"`
	// Experimental and Extensions name what the server declared beyond the
	// standard capabilities.
	Experimental []string `json:"experimental"`
	Extensions   []string `json:"extensions"`
}

// mcpToolBody is one tool a server lists.
type mcpToolBody struct {
	// Name is the server's; ExposedName is what the model calls it,
	// mcp__<server>__<tool>.
	Name         string                  `json:"name"`
	ExposedName  string                  `json:"exposed_name"`
	Title        string                  `json:"title,omitempty"`
	Description  string                  `json:"description,omitempty"`
	InputSchema  json.RawMessage         `json:"input_schema"`
	OutputSchema json.RawMessage         `json:"output_schema,omitempty"`
	Annotations  *mcpToolAnnotationsBody `json:"annotations,omitempty"`
	// Enabled is false for a tool in the server's disabled_tools.
	Enabled bool `json:"enabled"`
}

// mcpToolAnnotationsBody are a server's hints about a tool. They are the
// server's own claims; null is a hint it did not give.
type mcpToolAnnotationsBody struct {
	Title       string `json:"title,omitempty"`
	ReadOnly    *bool  `json:"read_only"`
	Destructive *bool  `json:"destructive"`
	Idempotent  *bool  `json:"idempotent"`
	OpenWorld   *bool  `json:"open_world"`
}

// mcpExcludedToolBody is a tool a server lists that the harness does not
// offer, and why.
type mcpExcludedToolBody struct {
	Name   string `json:"name"`
	Reason string `json:"reason"`
}

// mcpResourceBody is one resource a server lists.
type mcpResourceBody struct {
	URI         string `json:"uri"`
	Name        string `json:"name"`
	Title       string `json:"title,omitempty"`
	Description string `json:"description,omitempty"`
	MimeType    string `json:"mime_type,omitempty"`
	Size        *int64 `json:"size,omitempty"`
}

// mcpResourceTemplateBody is a family of resources a URI template
// addresses.
type mcpResourceTemplateBody struct {
	URITemplate string `json:"uri_template"`
	Name        string `json:"name"`
	Title       string `json:"title,omitempty"`
	Description string `json:"description,omitempty"`
	MimeType    string `json:"mime_type,omitempty"`
}

// mcpPromptBody is one prompt a server lists.
type mcpPromptBody struct {
	Name        string                  `json:"name"`
	Title       string                  `json:"title,omitempty"`
	Description string                  `json:"description,omitempty"`
	Arguments   []mcpPromptArgumentBody `json:"arguments"`
}

// mcpPromptArgumentBody is one argument a prompt takes.
type mcpPromptArgumentBody struct {
	Name        string `json:"name"`
	Title       string `json:"title,omitempty"`
	Description string `json:"description,omitempty"`
	Required    bool   `json:"required"`
}

// mcpLogLineBody is one line of a server's log.
type mcpLogLineBody struct {
	Time time.Time `json:"time"`
	// Source is stderr for a stdio server's own output, server for a log
	// notification, and eika for what the harness did.
	Source string `json:"source"`
	Level  string `json:"level"`
	Text   string `json:"text"`
}

// mcpAuthBody is a server's OAuth authorization, without the tokens.
type mcpAuthBody struct {
	// Challenged reports that the server asked for authorization; Challenge
	// is what it said.
	Challenged bool             `json:"challenged"`
	Challenge  *oauth.Challenge `json:"challenge,omitempty"`
	// Authorized reports that the harness holds an access token.
	Authorized      bool   `json:"authorized"`
	HasRefreshToken bool   `json:"has_refresh_token"`
	Issuer          string `json:"issuer,omitempty"`
	Resource        string `json:"resource,omitempty"`
	// ResourceMetadataURL is where the server's protected resource metadata
	// was found.
	ResourceMetadataURL string    `json:"resource_metadata_url,omitempty"`
	Scope               string    `json:"scope,omitempty"`
	ExpiresAt           time.Time `json:"expires_at,omitzero"`
	ClientID            string    `json:"client_id,omitempty"`
	// Registration is how the client came by its id: preregistered,
	// metadata_document, or dynamic.
	Registration string    `json:"registration,omitempty"`
	UpdatedAt    time.Time `json:"updated_at,omitzero"`
}

// createMCPServerRequest is the body of POST /api/mcp/servers.
type createMCPServerRequest struct {
	Name string `json:"name"`
	Kind string `json:"kind"`
	// URL, Headers, OAuthClientID, and OAuthClientSecret are an http
	// server's.
	URL     string            `json:"url"`
	Headers map[string]string `json:"headers"`
	// Command, Args, and Env are a stdio server's.
	Command string            `json:"command"`
	Args    []string          `json:"args"`
	Env     map[string]string `json:"env"`
	// Enabled defaults to true.
	Enabled           *bool    `json:"enabled"`
	DisabledTools     []string `json:"disabled_tools"`
	OAuthClientID     string   `json:"oauth_client_id"`
	OAuthClientSecret string   `json:"oauth_client_secret"`
}

// updateMCPServerRequest is the body of PATCH /api/mcp/servers/{id}. An
// absent field is left alone; the kind never changes.
type updateMCPServerRequest struct {
	Name *string `json:"name"`
	URL  *string `json:"url"`
	// Headers replaces the headers. A null value keeps the value stored
	// under that name, so a form can rename or drop one header without the
	// others' values.
	Headers map[string]*string `json:"headers"`
	Command *string            `json:"command"`
	Args    []string           `json:"args"`
	// Env replaces the environment, a null value keeping the stored one.
	Env           map[string]*string `json:"env"`
	Enabled       *bool              `json:"enabled"`
	DisabledTools []string           `json:"disabled_tools"`
	OAuthClientID *string            `json:"oauth_client_id"`
	// OAuthClientSecret replaces the stored secret; the empty string
	// removes it.
	OAuthClientSecret *string `json:"oauth_client_secret"`
}

// mcpWorkspaceRequest is the body of POST /api/mcp/servers/{id}/connect:
// the workspace a stdio server connects in, empty for a remote server.
type mcpWorkspaceRequest struct {
	WorkspaceID string `json:"workspace_id"`
}

// authorizeRequest is the body of POST /api/mcp/servers/{id}/authorize.
type authorizeRequest struct {
	// RedirectURI is the frontend's /mcp/callback on the address the
	// browser is at.
	RedirectURI string `json:"redirect_uri"`
}

// authorizeResponse is the body POST /api/mcp/servers/{id}/authorize
// answers with.
type authorizeResponse struct {
	// AuthorizationURL is where to send the browser.
	AuthorizationURL string `json:"authorization_url"`
}

// oauthCallbackRequest is the body of POST /api/mcp/oauth/callback: the
// query parameters the authorization server sent the browser back with.
type oauthCallbackRequest struct {
	State string `json:"state"`
	Code  string `json:"code"`
	// Iss is present exactly when the redirect carried one.
	Iss              *string `json:"iss"`
	Error            string  `json:"error"`
	ErrorDescription string  `json:"error_description"`
}

// oauthCallbackResponse is the body POST /api/mcp/oauth/callback answers
// with.
type oauthCallbackResponse struct {
	ServerID string `json:"server_id"`
}

// readResourceRequest is the body of POST
// /api/mcp/servers/{id}/resources/read.
type readResourceRequest struct {
	URI         string `json:"uri"`
	WorkspaceID string `json:"workspace_id"`
}

// readResourceResponse is the body POST /api/mcp/servers/{id}/resources/read
// answers with.
type readResourceResponse struct {
	Contents []mcp.ContentDetail `json:"contents"`
}

// getPromptRequest is the body of POST /api/mcp/servers/{id}/prompts/get.
type getPromptRequest struct {
	Name        string            `json:"name"`
	Arguments   map[string]string `json:"arguments"`
	WorkspaceID string            `json:"workspace_id"`
}

// elicitationAnswerRequest is the body of POST
// /api/elicitations/{id}/answer.
type elicitationAnswerRequest struct {
	// Action is accept, decline, or cancel.
	Action string `json:"action"`
	// Content is an accepted form's values by field name.
	Content json.RawMessage `json:"content"`
}

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

// validateMCPServer checks a server's configuration before it is stored.
func validateMCPServer(row store.MCPServer, headers, env map[string]string, clientSecret string) error {
	switch {
	case row.Name == "":
		return invalidf("name is required")
	case len(row.Name) > maxMCPServerName:
		return invalidf("name must be at most %d characters", maxMCPServerName)
	case !mcpServerNamePattern.MatchString(row.Name):
		return invalidf("name %q may hold letters, digits, hyphens, and single underscores between them, since it becomes part of the name of every tool the server offers", row.Name)
	case len(row.DisabledTools) > maxMCPToolList:
		return invalidf("disabled_tools may name at most %d tools", maxMCPToolList)
	}
	for _, name := range row.DisabledTools {
		if len(name) > maxMCPToolName {
			return invalidf("a disabled tool's name must be at most %d bytes", maxMCPToolName)
		}
	}
	switch row.Kind {
	case mcp.KindHTTP:
		if row.Command != "" || len(row.Args) > 0 || len(env) > 0 {
			return invalidf("an http server has a url, not a command, args, or env")
		}
		if err := validateMCPURL(row.URL); err != nil {
			return err
		}
		if err := validateHeaders(headers); err != nil {
			return err
		}
		if len(row.OAuthClientID) > maxOAuthClientID || len(clientSecret) > maxOAuthSecret {
			return invalidf("oauth_client_id must be at most %d bytes and oauth_client_secret at most %d", maxOAuthClientID, maxOAuthSecret)
		}
		if clientSecret != "" && row.OAuthClientID == "" {
			return invalidf("an oauth_client_secret needs the oauth_client_id it belongs to")
		}
	case mcp.KindStdio:
		if row.URL != "" || len(headers) > 0 || row.OAuthClientID != "" || clientSecret != "" {
			return invalidf("a stdio server has a command, not a url, headers, or an OAuth client; it takes its credentials from env")
		}
		if err := validateCommand(row.Command, row.Args); err != nil {
			return err
		}
		if err := validateEnv(env); err != nil {
			return err
		}
	default:
		return invalidf("kind must be %q or %q", mcp.KindHTTP, mcp.KindStdio)
	}
	return nil
}

// validateMCPURL checks a remote server's address.
func validateMCPURL(raw string) error {
	if raw == "" {
		return invalidf("url is required for an http server")
	}
	if len(raw) > maxMCPURL {
		return invalidf("url must be at most %d bytes", maxMCPURL)
	}
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return invalidf("url must be an http or https URL such as https://mcp.example.com/mcp")
	}
	if u.User != nil {
		return invalidf("url must not contain credentials; put them in headers")
	}
	if u.Fragment != "" {
		return invalidf("url must not have a fragment")
	}
	return nil
}

// validateHeaders checks the headers a remote server's requests carry.
func validateHeaders(headers map[string]string) error {
	if len(headers) > maxMCPPairs {
		return invalidf("a server may have at most %d headers", maxMCPPairs)
	}
	for name, value := range headers {
		lower := strings.ToLower(name)
		switch {
		case !headerNamePattern.MatchString(name) || len(name) > maxMCPPairName:
			return invalidf("header %q is not a valid header name", name)
		case slices.Contains(clientHeaders, lower) || strings.HasPrefix(lower, "mcp-param-"):
			return invalidf("header %s is set by the MCP client itself", name)
		case len(value) > maxMCPPairValue || strings.ContainsAny(value, "\r\n\x00"):
			return invalidf("the value of header %s must be one line of at most %d bytes", name, maxMCPPairValue)
		}
	}
	return nil
}

// validateCommand checks a stdio server's command line.
func validateCommand(command string, args []string) error {
	if command == "" {
		return invalidf("command is required for a stdio server")
	}
	if len(command) > maxMCPCommand || strings.ContainsRune(command, 0) {
		return invalidf("command must be at most %d bytes", maxMCPCommand)
	}
	if len(args) > maxMCPArgs {
		return invalidf("a stdio server takes at most %d args", maxMCPArgs)
	}
	for _, arg := range args {
		if len(arg) > maxMCPArg || strings.ContainsRune(arg, 0) {
			return invalidf("an arg must be at most %d bytes", maxMCPArg)
		}
	}
	return nil
}

// validateEnv checks a stdio server's environment.
func validateEnv(env map[string]string) error {
	if len(env) > maxMCPPairs {
		return invalidf("a server may have at most %d environment variables", maxMCPPairs)
	}
	for name, value := range env {
		if !envNamePattern.MatchString(name) || len(name) > maxMCPPairName {
			return invalidf("%q is not a valid environment variable name", name)
		}
		if len(value) > maxMCPPairValue || strings.ContainsRune(value, 0) {
			return invalidf("the value of %s must be at most %d bytes", name, maxMCPPairValue)
		}
	}
	return nil
}

// toolList is a disabled_tools list as it is stored: trimmed, without
// blanks or repeats, sorted.
func toolList(names []string) []string {
	out := []string{}
	for _, name := range names {
		if name = strings.TrimSpace(name); name != "" && !slices.Contains(out, name) {
			out = append(out, name)
		}
	}
	slices.Sort(out)
	return out
}

// sealPairs seals headers or an environment as the JSON of their map.
func (s *Server) sealPairs(pairs map[string]string) ([]byte, error) {
	if len(pairs) == 0 {
		return nil, nil
	}
	data, err := json.Marshal(pairs)
	if err != nil {
		return nil, fmt.Errorf("encode mcp server secrets: %w", err)
	}
	return s.deps.Secrets.Seal(string(data))
}

// mergePairs applies a PATCH's headers or environment to the sealed ones
// stored: absent keeps them (or, with drop, removes them), and a null value
// keeps the one stored under its name.
func (s *Server) mergePairs(sealed []byte, update map[string]*string, drop bool) (map[string]string, error) {
	if update == nil && drop {
		return map[string]string{}, nil
	}
	var stored map[string]string
	needStored := update == nil
	for _, v := range update {
		needStored = needStored || v == nil
	}
	if needStored {
		var err error
		if stored, err = openPairs(s.deps.Secrets, sealed); err != nil {
			return nil, conflictf("the stored values cannot be read, which happens when the harness's secret key file changes; enter every value again")
		}
	}
	if update == nil {
		return stored, nil
	}
	out := make(map[string]string, len(update))
	for name, v := range update {
		if v != nil {
			out[name] = *v
			continue
		}
		old, ok := stored[name]
		if !ok {
			return nil, invalidf("%s has no stored value to keep; give it one", name)
		}
		out[name] = old
	}
	return out, nil
}

// openPairs opens headers or an environment sealPairs sealed.
func openPairs(box *secret.Box, sealed []byte) (map[string]string, error) {
	plain, err := box.Open(sealed)
	if err != nil {
		return nil, err
	}
	out := map[string]string{}
	if plain == "" {
		return out, nil
	}
	if err := json.Unmarshal([]byte(plain), &out); err != nil {
		return nil, fmt.Errorf("decode mcp server secrets: %w", err)
	}
	return out, nil
}

// forgetMCPTokens drops a server's tokens and keeps its registered client.
func (s *Server) forgetMCPTokens(ctx context.Context, id string) error {
	backend := s.mcpBackend()
	creds, ok, err := backend.MCPCredentials(ctx, id)
	if err != nil || !ok {
		return err
	}
	creds.AccessToken, creds.RefreshToken, creds.ExpiresAt, creds.Scope = "", "", time.Time{}, ""
	return backend.SaveMCPCredentials(ctx, id, creds)
}

// unreadableMCP words a server secret the harness cannot open.
func (s *Server) unreadableMCP(row store.MCPServer, err error) error {
	s.log.Error("open mcp server secret", "server_id", row.ID, "error", err)
	return conflictf("the secrets of MCP server %s cannot be read, which happens when the harness's secret key file changes; enter them again", row.Name)
}

// asMCPServer renders a server on the wire with its state now.
func (s *Server) asMCPServer(row store.MCPServer) mcpServerBody {
	body := mcpServerBody{
		ID:                   row.ID,
		Name:                 row.Name,
		Kind:                 row.Kind,
		URL:                  row.URL,
		HeaderNames:          []string{},
		Command:              row.Command,
		Args:                 textList(row.Args),
		EnvNames:             []string{},
		Enabled:              row.Enabled,
		DisabledTools:        textList(row.DisabledTools),
		OAuthClientID:        row.OAuthClientID,
		OAuthClientSecretSet: len(row.OAuthClientSecret) > 0,
		Workspaces:           []string{},
		CreatedAt:            row.CreatedAt,
		UpdatedAt:            row.UpdatedAt,
	}
	if headers, err := openPairs(s.deps.Secrets, row.Headers); err == nil {
		body.HeaderNames = slices.Sorted(maps.Keys(headers))
	}
	if env, err := openPairs(s.deps.Secrets, row.Env); err == nil {
		body.EnvNames = slices.Sorted(maps.Keys(env))
	}
	st := s.deps.MCP.Status(row.ID)
	body.State, body.Error = st.State, st.Error
	if !row.Enabled {
		body.State, body.Error = mcp.StateDisabled, ""
	}
	body.Workspaces = append(body.Workspaces, st.Workspaces...)
	return body
}

// asMCPDetails renders what the pool knows of a server on the wire.
func (s *Server) asMCPDetails(row store.MCPServer, d mcp.Details) mcpServerDetails {
	out := mcpServerDetails{
		Server:            s.asMCPServer(row),
		Tools:             []mcpToolBody{},
		ExcludedTools:     []mcpExcludedToolBody{},
		Resources:         []mcpResourceBody{},
		ResourceTemplates: []mcpResourceTemplateBody{},
		Prompts:           []mcpPromptBody{},
		ListErrors:        map[string]string{},
		FetchedAt:         d.Catalog.FetchedAt,
		Logs:              []mcpLogLineBody{},
		Auth:              asMCPAuth(d.Auth),
	}
	// The details are one snapshot: the state beside the lists they explain.
	out.Server.State, out.Server.Error = d.State, d.Error
	out.Server.Workspaces = append([]string{}, d.Workspaces...)
	if info := d.Catalog.Info; info.Era != "" {
		out.Connection = &mcpConnectionBody{
			Era:               string(info.Era),
			Transport:         info.Transport,
			ProtocolVersion:   info.Version,
			SupportedVersions: textList(info.Supported),
			ServerInfo: mcpImplementationBody{
				Name:        info.Server.Name,
				Title:       info.Server.Title,
				Version:     info.Server.Version,
				Description: info.Server.Description,
				WebsiteURL:  info.Server.WebsiteURL,
			},
			Capabilities: asMCPCapabilities(info.Capabilities),
			Instructions: info.Instructions,
		}
	}
	names := mcp.ToolNames(row.Name, d.Catalog.Tools)
	for i, t := range d.Catalog.Tools {
		body := mcpToolBody{
			Name:         t.Name,
			ExposedName:  names[i],
			Title:        t.Title,
			Description:  t.Description,
			InputSchema:  t.InputSchema,
			OutputSchema: t.OutputSchema,
			Enabled:      !slices.Contains(row.DisabledTools, t.Name),
		}
		if len(body.InputSchema) == 0 {
			body.InputSchema = json.RawMessage(`{}`)
		}
		if a := t.Annotations; a != nil {
			body.Annotations = &mcpToolAnnotationsBody{
				Title:       a.Title,
				ReadOnly:    a.ReadOnlyHint,
				Destructive: a.DestructiveHint,
				Idempotent:  a.IdempotentHint,
				OpenWorld:   a.OpenWorldHint,
			}
		}
		out.Tools = append(out.Tools, body)
	}
	for _, t := range d.Catalog.Excluded {
		out.ExcludedTools = append(out.ExcludedTools, mcpExcludedToolBody{Name: t.Name, Reason: t.Reason})
	}
	for _, r := range d.Catalog.Resources {
		out.Resources = append(out.Resources, mcpResourceBody{
			URI: r.URI, Name: r.Name, Title: r.Title, Description: r.Description, MimeType: r.MimeType, Size: r.Size,
		})
	}
	for _, r := range d.Catalog.Templates {
		out.ResourceTemplates = append(out.ResourceTemplates, mcpResourceTemplateBody{
			URITemplate: r.URITemplate, Name: r.Name, Title: r.Title, Description: r.Description, MimeType: r.MimeType,
		})
	}
	for _, p := range d.Catalog.Prompts {
		body := mcpPromptBody{Name: p.Name, Title: p.Title, Description: p.Description, Arguments: []mcpPromptArgumentBody{}}
		for _, a := range p.Arguments {
			body.Arguments = append(body.Arguments, mcpPromptArgumentBody{
				Name: a.Name, Title: a.Title, Description: a.Description, Required: a.Required,
			})
		}
		out.Prompts = append(out.Prompts, body)
	}
	maps.Copy(out.ListErrors, d.Catalog.Errors)
	for _, l := range d.Logs {
		out.Logs = append(out.Logs, mcpLogLineBody{Time: l.Time, Source: l.Source, Level: l.Level, Text: l.Text})
	}
	return out
}

// asMCPCapabilities renders what a server said it serves.
func asMCPCapabilities(c mcp.ServerCapabilities) mcpCapabilitiesBody {
	out := mcpCapabilitiesBody{
		Tools:        c.Tools != nil,
		Resources:    c.Resources != nil,
		Prompts:      c.Prompts != nil,
		Logging:      len(c.Logging) > 0,
		Completions:  len(c.Completions) > 0,
		Experimental: slices.Sorted(maps.Keys(c.Experimental)),
		Extensions:   slices.Sorted(maps.Keys(c.Extensions)),
	}
	if c.Tools != nil {
		out.ToolsListChanged = c.Tools.ListChanged
	}
	if c.Resources != nil {
		out.ResourcesSubscribe, out.ResourcesListChanged = c.Resources.Subscribe, c.Resources.ListChanged
	}
	if c.Prompts != nil {
		out.PromptsListChanged = c.Prompts.ListChanged
	}
	out.Experimental, out.Extensions = textList(out.Experimental), textList(out.Extensions)
	return out
}

// asMCPAuth renders a server's authorization without its tokens.
func asMCPAuth(a mcp.AuthStatus) mcpAuthBody {
	out := mcpAuthBody{
		Challenged:          a.Challenged,
		Authorized:          a.Authorized,
		HasRefreshToken:     a.RefreshToken,
		Issuer:              a.Issuer,
		Resource:            a.Resource,
		ResourceMetadataURL: a.ResourceMetadataURL,
		Scope:               a.Scope,
		ExpiresAt:           a.ExpiresAt,
		ClientID:            a.ClientID,
		Registration:        a.Registration,
		UpdatedAt:           a.UpdatedAt,
	}
	if a.Challenged {
		challenge := a.Challenge
		out.Challenge = &challenge
	}
	return out
}

// textList is a list as it goes on the wire: an array, never null.
func textList(list []string) []string {
	if list == nil {
		return []string{}
	}
	return list
}

// storeFailure marks a failure of the database behind the MCP pool, which
// stays internal however the pool reports it.
type storeFailure struct{ err error }

// Error returns the store's message.
func (f storeFailure) Error() string { return f.err.Error() }

// Unwrap returns the store's error, so its sentinels still map.
func (f storeFailure) Unwrap() error { return f.err }

// mcpStore is the store the MCP pool reads its servers and keeps their
// credentials through: the database, with every secret sealed.
type mcpStore struct {
	store   *store.Store
	secrets *secret.Box
	log     *slog.Logger
}

// mcpBackend returns the server's view of the pool's store.
func (s *Server) mcpBackend() mcpStore {
	return mcpStore{store: s.deps.Store, secrets: s.deps.Secrets, log: s.log}
}

// MCPServers returns every configured server. One whose secrets cannot be
// opened is left out, so the others' tools still reach runs.
func (b mcpStore) MCPServers(ctx context.Context) ([]mcp.ServerConfig, error) {
	rows, err := b.store.MCPServers(ctx)
	if err != nil {
		return nil, storeFailure{err}
	}
	out := make([]mcp.ServerConfig, 0, len(rows))
	for _, row := range rows {
		cfg, err := b.config(row)
		if err != nil {
			b.log.Error("open mcp server secrets", "server_id", row.ID, "error", err)
			continue
		}
		out = append(out, cfg)
	}
	return out, nil
}

// MCPServer returns one configured server with its secrets open.
func (b mcpStore) MCPServer(ctx context.Context, id string) (mcp.ServerConfig, error) {
	row, err := b.store.MCPServer(ctx, id)
	if err != nil {
		return mcp.ServerConfig{}, storeFailure{err}
	}
	cfg, err := b.config(row)
	if err != nil {
		b.log.Error("open mcp server secrets", "server_id", row.ID, "error", err)
		return mcp.ServerConfig{}, conflictf("the secrets of MCP server %s cannot be read, which happens when the harness's secret key file changes; enter them again", row.Name)
	}
	return cfg, nil
}

// config opens a server row's secrets.
func (b mcpStore) config(row store.MCPServer) (mcp.ServerConfig, error) {
	headers, err := openPairs(b.secrets, row.Headers)
	if err != nil {
		return mcp.ServerConfig{}, err
	}
	env, err := openPairs(b.secrets, row.Env)
	if err != nil {
		return mcp.ServerConfig{}, err
	}
	clientSecret, err := b.secrets.Open(row.OAuthClientSecret)
	if err != nil {
		return mcp.ServerConfig{}, err
	}
	cfg := mcp.ServerConfig{
		ID:                row.ID,
		Name:              row.Name,
		Kind:              row.Kind,
		URL:               row.URL,
		Headers:           headers,
		Command:           row.Command,
		Args:              row.Args,
		Enabled:           row.Enabled,
		DisabledTools:     row.DisabledTools,
		OAuthClientID:     row.OAuthClientID,
		OAuthClientSecret: clientSecret,
	}
	for _, name := range slices.Sorted(maps.Keys(env)) {
		cfg.Env = append(cfg.Env, name+"="+env[name])
	}
	return cfg, nil
}

// MCPCredentials returns a server's credentials with their secrets open.
// Credentials the harness can no longer open count as none, so the user
// authorizes again rather than meeting an error on every request.
func (b mcpStore) MCPCredentials(ctx context.Context, serverID string) (mcp.Credentials, bool, error) {
	row, err := b.store.MCPCredentials(ctx, serverID)
	if errors.Is(err, store.ErrNotFound) {
		return mcp.Credentials{}, false, nil
	}
	if err != nil {
		return mcp.Credentials{}, false, storeFailure{err}
	}
	var sealed [3]string
	for i, v := range [][]byte{row.ClientSecret, row.AccessToken, row.RefreshToken} {
		if sealed[i], err = b.secrets.Open(v); err != nil {
			b.log.Error("open mcp credentials", "server_id", serverID, "error", err)
			return mcp.Credentials{}, false, nil
		}
	}
	creds := mcp.Credentials{
		Issuer:              row.Issuer,
		Resource:            row.Resource,
		ResourceMetadataURL: row.ResourceMetadataURL,
		Client: oauth.Client{
			ID:           row.ClientID,
			Secret:       sealed[0],
			AuthMethod:   row.ClientAuthMethod,
			Registration: row.ClientRegistration,
		},
		RedirectURI:  row.RedirectURI,
		AccessToken:  sealed[1],
		RefreshToken: sealed[2],
		Scope:        row.Scope,
		ExpiresAt:    row.ExpiresAt,
		UpdatedAt:    row.UpdatedAt,
	}
	if err := json.Unmarshal(row.Metadata, &creds.Server); err != nil {
		return mcp.Credentials{}, false, fmt.Errorf("decode authorization server metadata of %s: %w", serverID, err)
	}
	return creds, true, nil
}

// SaveMCPCredentials seals and stores a server's credentials.
func (b mcpStore) SaveMCPCredentials(ctx context.Context, serverID string, c mcp.Credentials) error {
	metadata, err := json.Marshal(c.Server)
	if err != nil {
		return fmt.Errorf("encode authorization server metadata of %s: %w", serverID, err)
	}
	row := store.MCPCredentials{
		ServerID:            serverID,
		Issuer:              c.Issuer,
		Resource:            c.Resource,
		ResourceMetadataURL: c.ResourceMetadataURL,
		Metadata:            metadata,
		ClientID:            c.Client.ID,
		ClientAuthMethod:    c.Client.AuthMethod,
		ClientRegistration:  c.Client.Registration,
		RedirectURI:         c.RedirectURI,
		Scope:               c.Scope,
		ExpiresAt:           c.ExpiresAt,
	}
	for dst, v := range map[*[]byte]string{&row.ClientSecret: c.Client.Secret, &row.AccessToken: c.AccessToken, &row.RefreshToken: c.RefreshToken} {
		if *dst, err = b.secrets.Seal(v); err != nil {
			return err
		}
	}
	if err := b.store.SetMCPCredentials(ctx, row); err != nil {
		return storeFailure{err}
	}
	return nil
}

// mcpLauncher starts stdio MCP servers in workspaces through the host's
// /process route: a stdio server is a process an agent's session asked
// for, so it runs where the agent's processes do.
type mcpLauncher struct {
	workspaces Workspaces
}

// Launch starts cmd in a running workspace.
func (l mcpLauncher) Launch(ctx context.Context, workspaceID string, cmd mcp.Command, stderr func(string)) (io.ReadWriteCloser, error) {
	ws, err := l.workspaces.Inspect(ctx, workspaceID)
	if err != nil {
		return nil, err
	}
	if ws.State != workspace.StateRunning {
		return nil, conflictf("workspace %s is %s, not running", workspaceID, ws.State)
	}
	return l.workspaces.Process(ctx, ws, sandbox.ProcessSpec{Command: cmd.Name, Args: cmd.Args, Env: cmd.Env}, stderr)
}

// mcpClient is how Eika introduces itself to an MCP server: by name, and
// by the version the build recorded.
func mcpClient() mcp.Implementation {
	version := "devel"
	if info, ok := debug.ReadBuildInfo(); ok && info.Main.Version != "" && info.Main.Version != "(devel)" {
		version = info.Main.Version
	}
	return mcp.Implementation{Name: "eika", Title: "Eika", Version: version}
}

// NewMCP builds the MCP pool over the servers st holds, their secrets
// sealed by secrets, starting stdio servers in workspaces through host and
// announcing every change on bus. The wiring passes the real host; a test
// passes its fake. The caller starts the pool and closes it after the
// server that uses it.
func NewMCP(cfg config.Config, st *store.Store, secrets *secret.Box, host Workspaces, bus event.Emitter, log *slog.Logger) *mcp.Pool {
	backend := mcpStore{store: st, secrets: secrets, log: log}
	opts := mcp.Options{
		Store:   backend,
		Emitter: bus,
		// No overall timeout: an event stream stays open as long as its
		// connection. Every request carries a deadline of its own.
		HTTPClient: &http.Client{},
		Client:     mcpClient(),
		PublicURL:  cfg.PublicURL,
		Logger:     log,
	}
	if host != nil {
		opts.Launcher = mcpLauncher{workspaces: host}
	}
	return mcp.NewPool(opts)
}

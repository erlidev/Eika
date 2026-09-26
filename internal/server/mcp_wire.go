package server

import (
	"encoding/json"
	"maps"
	"slices"
	"time"

	"github.com/erlidev/eika/internal/mcp"
	"github.com/erlidev/eika/internal/mcp/oauth"
	"github.com/erlidev/eika/internal/store"
)

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

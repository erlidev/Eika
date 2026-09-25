package mcp

import "encoding/json"

// The protocol types below are the Model Context Protocol's own, with its
// camelCase JSON names. They follow the 2026-07-28 schema, which is a
// superset of what the initialize-based revisions send for the same things,
// so one set of types reads both eras.

// Version is the modern revision the client speaks: stateless requests that
// carry their version in _meta.
const Version = "2026-07-28"

// legacyVersions are the initialize-based revisions the client falls back
// to, newest first. The first is what an initialize request offers.
var legacyVersions = []string{"2025-11-25", "2025-06-18", "2025-03-26", "2024-11-05"}

// Era says which family of revisions a server speaks.
type Era string

// The eras. A modern server takes per-request metadata and answers
// server/discover; a legacy one needs an initialize handshake first.
const (
	EraModern Era = "modern"
	EraLegacy Era = "legacy"
)

// The _meta keys the 2026-07-28 revision reserves.
const (
	metaProtocolVersion    = "io.modelcontextprotocol/protocolVersion"
	metaClientInfo         = "io.modelcontextprotocol/clientInfo"
	metaClientCapabilities = "io.modelcontextprotocol/clientCapabilities"
	metaServerInfo         = "io.modelcontextprotocol/serverInfo"
	metaSubscriptionID     = "io.modelcontextprotocol/subscriptionId"
	metaProgressToken      = "progressToken"
)

// The result types of a modern result. A result that names none is from an
// older server and is complete.
const (
	resultComplete      = "complete"
	resultInputRequired = "input_required"
)

// Implementation names a client or a server and its version.
type Implementation struct {
	Name        string `json:"name"`
	Title       string `json:"title,omitempty"`
	Version     string `json:"version"`
	Description string `json:"description,omitempty"`
	WebsiteURL  string `json:"websiteUrl,omitempty"`
	Icons       []Icon `json:"icons,omitempty"`
}

// Icon is a picture a server offers for itself or one of its primitives.
type Icon struct {
	Src      string   `json:"src"`
	MimeType string   `json:"mimeType,omitempty"`
	Sizes    []string `json:"sizes,omitempty"`
	Theme    string   `json:"theme,omitempty"`
}

// ServerCapabilities is what a server says it serves.
type ServerCapabilities struct {
	Experimental map[string]json.RawMessage `json:"experimental,omitempty"`
	Logging      json.RawMessage            `json:"logging,omitempty"`
	Completions  json.RawMessage            `json:"completions,omitempty"`
	Prompts      *ListCapability            `json:"prompts,omitempty"`
	Resources    *ResourcesCapability       `json:"resources,omitempty"`
	Tools        *ListCapability            `json:"tools,omitempty"`
	Extensions   map[string]json.RawMessage `json:"extensions,omitempty"`
}

// ListCapability is the tools or prompts capability.
type ListCapability struct {
	ListChanged bool `json:"listChanged,omitempty"`
}

// ResourcesCapability is the resources capability.
type ResourcesCapability struct {
	Subscribe   bool `json:"subscribe,omitempty"`
	ListChanged bool `json:"listChanged,omitempty"`
}

// clientCapabilities is what Eika declares: elicitation in both modes, and
// nothing that the 2026-07-28 revision deprecates (roots, sampling).
type clientCapabilities struct {
	Elicitation *elicitationCapability `json:"elicitation,omitempty"`
}

type elicitationCapability struct {
	Form struct{} `json:"form"`
	URL  struct{} `json:"url"`
}

// eikaCapabilities is the one capability set every request declares.
var eikaCapabilities = clientCapabilities{Elicitation: &elicitationCapability{}}

// Tool is one tool a server offers.
type Tool struct {
	Name         string           `json:"name"`
	Title        string           `json:"title,omitempty"`
	Description  string           `json:"description,omitempty"`
	InputSchema  json.RawMessage  `json:"inputSchema"`
	OutputSchema json.RawMessage  `json:"outputSchema,omitempty"`
	Annotations  *ToolAnnotations `json:"annotations,omitempty"`
	Icons        []Icon           `json:"icons,omitempty"`
}

// ToolAnnotations are a server's hints about a tool. They are untrusted: a
// server says what it likes about itself.
type ToolAnnotations struct {
	Title           string `json:"title,omitempty"`
	ReadOnlyHint    *bool  `json:"readOnlyHint,omitempty"`
	DestructiveHint *bool  `json:"destructiveHint,omitempty"`
	IdempotentHint  *bool  `json:"idempotentHint,omitempty"`
	OpenWorldHint   *bool  `json:"openWorldHint,omitempty"`
}

// Resource is one resource a server lists.
type Resource struct {
	URI         string       `json:"uri"`
	Name        string       `json:"name"`
	Title       string       `json:"title,omitempty"`
	Description string       `json:"description,omitempty"`
	MimeType    string       `json:"mimeType,omitempty"`
	Size        *int64       `json:"size,omitempty"`
	Annotations *Annotations `json:"annotations,omitempty"`
	Icons       []Icon       `json:"icons,omitempty"`
}

// ResourceTemplate is a family of resources addressed by a URI template.
type ResourceTemplate struct {
	URITemplate string       `json:"uriTemplate"`
	Name        string       `json:"name"`
	Title       string       `json:"title,omitempty"`
	Description string       `json:"description,omitempty"`
	MimeType    string       `json:"mimeType,omitempty"`
	Annotations *Annotations `json:"annotations,omitempty"`
	Icons       []Icon       `json:"icons,omitempty"`
}

// Annotations tell a client who a piece of content is for and how much it
// matters.
type Annotations struct {
	Audience     []string `json:"audience,omitempty"`
	Priority     *float64 `json:"priority,omitempty"`
	LastModified string   `json:"lastModified,omitempty"`
}

// ResourceContents is one part of a read resource: text or a base64 blob.
type ResourceContents struct {
	URI      string  `json:"uri"`
	MimeType string  `json:"mimeType,omitempty"`
	Text     *string `json:"text,omitempty"`
	Blob     *string `json:"blob,omitempty"`
}

// Prompt is one prompt template a server offers.
type Prompt struct {
	Name        string           `json:"name"`
	Title       string           `json:"title,omitempty"`
	Description string           `json:"description,omitempty"`
	Arguments   []PromptArgument `json:"arguments,omitempty"`
	Icons       []Icon           `json:"icons,omitempty"`
}

// PromptArgument is one argument a prompt takes.
type PromptArgument struct {
	Name        string `json:"name"`
	Title       string `json:"title,omitempty"`
	Description string `json:"description,omitempty"`
	Required    bool   `json:"required,omitempty"`
}

// PromptMessage is one message of a rendered prompt.
type PromptMessage struct {
	Role    string  `json:"role"`
	Content Content `json:"content"`
}

// Content is one block of a tool result or a prompt message: text, an image,
// audio, a link to a resource, or an embedded resource.
type Content struct {
	Type        string            `json:"type"`
	Text        string            `json:"text,omitempty"`
	Data        string            `json:"data,omitempty"`
	MimeType    string            `json:"mimeType,omitempty"`
	URI         string            `json:"uri,omitempty"`
	Name        string            `json:"name,omitempty"`
	Title       string            `json:"title,omitempty"`
	Description string            `json:"description,omitempty"`
	Resource    *ResourceContents `json:"resource,omitempty"`
	Annotations *Annotations      `json:"annotations,omitempty"`
}

// CallToolResult is what a tool call produced.
type CallToolResult struct {
	Content           []Content       `json:"content"`
	StructuredContent json.RawMessage `json:"structuredContent,omitempty"`
	IsError           bool            `json:"isError,omitempty"`
}

// ReadResourceResult is what reading a resource produced.
type ReadResourceResult struct {
	Contents []ResourceContents `json:"contents"`
}

// GetPromptResult is a rendered prompt.
type GetPromptResult struct {
	Description string          `json:"description,omitempty"`
	Messages    []PromptMessage `json:"messages"`
}

// discoverResult is the answer to server/discover.
type discoverResult struct {
	SupportedVersions []string           `json:"supportedVersions"`
	Capabilities      ServerCapabilities `json:"capabilities"`
	Instructions      string             `json:"instructions,omitempty"`
}

// initializeParams is the body of a legacy initialize request.
type initializeParams struct {
	ProtocolVersion string             `json:"protocolVersion"`
	Capabilities    clientCapabilities `json:"capabilities"`
	ClientInfo      Implementation     `json:"clientInfo"`
}

// initializeResult is a legacy server's answer to initialize.
type initializeResult struct {
	ProtocolVersion string             `json:"protocolVersion"`
	Capabilities    ServerCapabilities `json:"capabilities"`
	ServerInfo      Implementation     `json:"serverInfo"`
	Instructions    string             `json:"instructions,omitempty"`
}

// resultEnvelope reads the fields every result may carry beside its own.
type resultEnvelope struct {
	ResultType    string                     `json:"resultType,omitempty"`
	Meta          map[string]json.RawMessage `json:"_meta,omitempty"`
	NextCursor    string                     `json:"nextCursor,omitempty"`
	TTLMs         *int64                     `json:"ttlMs,omitempty"`
	InputRequests map[string]inputRequest    `json:"inputRequests,omitempty"`
	RequestState  *string                    `json:"requestState,omitempty"`
}

// inputRequest is one thing a server needs from the client before it can
// finish a request: an elicitation, in the methods Eika declares.
type inputRequest struct {
	Method string          `json:"method"`
	Params json.RawMessage `json:"params,omitempty"`
}

// ElicitRequest is a server asking the user for input: a form with a flat
// schema, or a URL to visit.
type ElicitRequest struct {
	// Mode is "form" or "url"; empty means form.
	Mode            string          `json:"mode,omitempty"`
	Message         string          `json:"message"`
	RequestedSchema json.RawMessage `json:"requestedSchema,omitempty"`
	URL             string          `json:"url,omitempty"`
}

// ElicitResult is the user's answer to an elicitation.
type ElicitResult struct {
	// Action is accept, decline, or cancel.
	Action  string          `json:"action"`
	Content json.RawMessage `json:"content,omitempty"`
}

// The elicitation actions.
const (
	ElicitAccept  = "accept"
	ElicitDecline = "decline"
	ElicitCancel  = "cancel"
)

// progressParams is the body of a notifications/progress notification.
type progressParams struct {
	ProgressToken json.RawMessage `json:"progressToken"`
	Progress      float64         `json:"progress"`
	Total         *float64        `json:"total,omitempty"`
	Message       string          `json:"message,omitempty"`
}

// logParams is the body of a notifications/message notification.
type logParams struct {
	Level  string          `json:"level"`
	Logger string          `json:"logger,omitempty"`
	Data   json.RawMessage `json:"data"`
}

// subscriptionFilter is what a subscriptions/listen request asks for, and
// what the acknowledgement says the server will send.
type subscriptionFilter struct {
	ToolsListChanged     bool `json:"toolsListChanged,omitempty"`
	PromptsListChanged   bool `json:"promptsListChanged,omitempty"`
	ResourcesListChanged bool `json:"resourcesListChanged,omitempty"`
}

// The notification methods the client acts on.
const (
	notifyToolsChanged     = "notifications/tools/list_changed"
	notifyPromptsChanged   = "notifications/prompts/list_changed"
	notifyResourcesChanged = "notifications/resources/list_changed"
	notifyProgress         = "notifications/progress"
	notifyMessage          = "notifications/message"
	notifyCancelled        = "notifications/cancelled"
	notifyInitialized      = "notifications/initialized"
	notifyAcknowledged     = "notifications/subscriptions/acknowledged"
)

/** Wire types for MCP servers, their contents, sign-in, and elicitations. */

/** MCPServerKind is how an MCP server is reached: over HTTP, or as a process in a workspace. */
export type MCPServerKind = "http" | "stdio";

/** MCPServerState is where an MCP server's connection stands. */
export type MCPServerState =
  "disabled" | "idle" | "connecting" | "connected" | "unauthorized" | "error";

/**
 * MCPServer is one configured MCP server. Header and environment values and
 * the OAuth client secret never leave the harness; their names do.
 */
export type MCPServer = {
  id: string;
  name: string;
  kind: MCPServerKind;
  /** url is an http server's endpoint; empty for stdio. */
  url: string;
  header_names: string[] | null;
  command: string;
  args: string[] | null;
  env_names: string[] | null;
  enabled: boolean;
  /** disabled_tools are server tool names left out of every run. */
  disabled_tools: string[] | null;
  /** oauth_client_id is a client registered by hand, if any. */
  oauth_client_id: string;
  oauth_client_secret_set: boolean;
  state: MCPServerState;
  /** error says why the server is unauthorized or in error. */
  error?: string;
  /** workspaces are where a stdio server runs now. */
  workspaces: string[] | null;
  created_at: string;
  updated_at: string;
};

/** CreateMCPServer is the body of POST /api/mcp/servers. */
export type CreateMCPServer = {
  name: string;
  kind: MCPServerKind;
  url?: string;
  headers?: Record<string, string>;
  command?: string;
  args?: string[];
  env?: Record<string, string>;
  enabled?: boolean;
  disabled_tools?: string[];
  oauth_client_id?: string;
  oauth_client_secret?: string;
};

/**
 * UpdateMCPServer is the body of PATCH /api/mcp/servers/{id}; an absent field
 * is left alone. `headers` and `env` replace the whole set, where a null value
 * keeps the value stored under that name.
 */
export type UpdateMCPServer = {
  name?: string;
  url?: string;
  headers?: Record<string, string | null>;
  command?: string;
  args?: string[];
  env?: Record<string, string | null>;
  enabled?: boolean;
  disabled_tools?: string[];
  /** oauth_client_id "" removes the hand-registered client with its secret. */
  oauth_client_id?: string;
  /** oauth_client_secret "" removes the stored secret. */
  oauth_client_secret?: string;
};

/** MCPImplementation is what a server says of itself. */
export type MCPImplementation = {
  name: string;
  title?: string;
  version: string;
  description?: string;
  website_url?: string;
};

/** MCPCapabilities is what a server declared it can do. */
export type MCPCapabilities = {
  tools: boolean;
  tools_list_changed: boolean;
  resources: boolean;
  resources_subscribe: boolean;
  resources_list_changed: boolean;
  prompts: boolean;
  prompts_list_changed: boolean;
  logging: boolean;
  completions: boolean;
  experimental: string[] | null;
  extensions: string[] | null;
};

/** MCPConnection is what the latest successful connection to a server learned. */
export type MCPConnection = {
  /** era is modern for the 2026-07-28 revision, legacy for an initialize-based one. */
  era: "modern" | "legacy";
  transport: "streamable_http" | "sse" | "stdio";
  protocol_version: string;
  supported_versions: string[] | null;
  server_info: MCPImplementation;
  capabilities: MCPCapabilities;
  instructions?: string;
};

/** MCPToolAnnotations are a server's own claims about a tool; null is unsaid. */
export type MCPToolAnnotations = {
  title?: string;
  read_only: boolean | null;
  destructive: boolean | null;
  idempotent: boolean | null;
  open_world: boolean | null;
};

/** MCPTool is one tool a server lists. */
export type MCPTool = {
  /** name is the server's own name for the tool. */
  name: string;
  /** exposed_name is what the model calls it: mcp__<server>__<tool>. */
  exposed_name: string;
  title?: string;
  description?: string;
  input_schema: unknown;
  output_schema?: unknown;
  annotations?: MCPToolAnnotations;
  /** enabled is false for a tool in the server's disabled_tools. */
  enabled: boolean;
};

/** MCPResource is one resource a server lists. */
export type MCPResource = {
  uri: string;
  name: string;
  title?: string;
  description?: string;
  mime_type?: string;
  size?: number;
};

/** MCPResourceTemplate is one parameterised resource a server lists. */
export type MCPResourceTemplate = {
  uri_template: string;
  name: string;
  title?: string;
  description?: string;
  mime_type?: string;
};

/** MCPPromptArgument is one argument a prompt takes. */
export type MCPPromptArgument = {
  name: string;
  title?: string;
  description?: string;
  required: boolean;
};

/** MCPPrompt is one prompt a server lists. */
export type MCPPrompt = {
  name: string;
  title?: string;
  description?: string;
  arguments: MCPPromptArgument[] | null;
};

/** MCPLogLine is one line of a server's log. */
export type MCPLogLine = {
  time: string;
  /** source is stderr for a stdio server's output, server for a log notification, eika for the harness. */
  source: "stderr" | "server" | "eika";
  level: string;
  text: string;
};

/** MCPChallenge is the Bearer challenge a server answered without a token. */
export type MCPChallenge = {
  resource_metadata?: string;
  scope?: string;
  error?: string;
  error_description?: string;
};

/** MCPAuth is where a server's OAuth authorization stands. Tokens never leave the harness. */
export type MCPAuth = {
  /** challenged says the server asked for authorization. */
  challenged: boolean;
  challenge?: MCPChallenge;
  /** authorized says an access token is stored. */
  authorized: boolean;
  has_refresh_token: boolean;
  issuer?: string;
  resource?: string;
  resource_metadata_url?: string;
  scope?: string;
  expires_at?: string;
  client_id?: string;
  registration?: "preregistered" | "metadata_document" | "dynamic";
  updated_at?: string;
};

/** MCPServerDetails is the body of GET /api/mcp/servers/{id}: everything known of one server. */
export type MCPServerDetails = {
  server: MCPServer;
  /** connection is absent before the first successful connection. */
  connection?: MCPConnection;
  tools: MCPTool[] | null;
  /** excluded_tools are listed but never offered, with why. */
  excluded_tools: { name: string; reason: string }[] | null;
  resources: MCPResource[] | null;
  resource_templates: MCPResourceTemplate[] | null;
  prompts: MCPPrompt[] | null;
  /** list_errors are the lists the server could not give, by method. */
  list_errors: Record<string, string> | null;
  /** fetched_at is when the lists were read. */
  fetched_at?: string;
  logs: MCPLogLine[] | null;
  auth: MCPAuth;
};

/**
 * ContentDetail is one block of an MCP tool result, a read resource, or a
 * rendered prompt, as the UI shows it.
 */
export type ContentDetail = {
  type: "text" | "image" | "audio" | "resource_link" | "resource";
  text?: string;
  mime_type?: string;
  uri?: string;
  name?: string;
  description?: string;
  /** data is base64, absent when the media were over the budget. */
  data?: string;
  /** size is the decoded size of data, or of what was left out. */
  size?: number;
  /** omitted says the media were left out for the budget. */
  omitted?: boolean;
};

/** MCPToolDetails are an MCP tool result's details. */
export type MCPToolDetails = {
  server: string;
  tool: string;
  content: ContentDetail[] | null;
  structured_content?: unknown;
  is_error?: boolean;
};

/** MCPPromptResult is the body of POST /api/mcp/servers/{id}/prompts/get. */
export type MCPPromptResult = {
  description?: string;
  messages: { role: "user" | "assistant"; content: ContentDetail }[] | null;
};

/** OAuthCallback is the body of POST /api/mcp/oauth/callback. */
export type OAuthCallback = {
  state: string;
  code: string;
  /** iss is sent exactly when the redirect carried one; absent and empty differ. */
  iss?: string;
  error?: string;
  error_description?: string;
};

/** ElicitationMode is how an MCP server asks: fields to fill in, or a page to visit. */
export type ElicitationMode = "form" | "url";

/** Elicitation is what an MCP server asked the user during a tool call. */
export type Elicitation = {
  id: string;
  session_id: string;
  run_id: string;
  call_id: string;
  /** server is the name of the MCP server that asks. */
  server: string;
  mode: ElicitationMode;
  message: string;
  /** requested_schema is the form's flat JSON Schema, in form mode. */
  requested_schema?: unknown;
  /** url is the page to visit, in url mode. */
  url?: string;
  asked_at: string;
};

/** ElicitationAction is how the user answered an elicitation. */
export type ElicitationAction = "accept" | "decline" | "cancel";

/** ElicitationAnswer is the body of POST /api/elicitations/{id}/answer. */
export type ElicitationAnswer = {
  action: ElicitationAction;
  /** content is an accepted form's values by field name. */
  content?: Record<string, unknown>;
};

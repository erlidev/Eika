/**
 * Wire types for the HTTP API. They mirror the Go structs in
 * `internal/server` exactly; the contract is documented in docs/api/http.md.
 * When a Go type changes, change these in the same commit.
 */

import type { EntryKind, Timings, Usage } from "@/api/events";

/** ErrorCode is the machine-readable half of an API error body. */
export type ErrorCode =
  | "invalid_request"
  | "unauthorized"
  | "forbidden"
  | "not_found"
  | "conflict"
  | "too_large"
  | "internal";

/** Project is a git repository Eika knows. */
export type Project = {
  id: string;
  name: string;
  kind: ProjectKind;
  remote_url?: string;
  /** remote_username is the user the hub authenticates to the upstream as. */
  remote_username?: string;
  /** remote_password_set says a password is stored; the password never leaves the harness. */
  remote_password_set?: boolean;
  host_path?: string;
  default_branch: string;
  created_at: string;
};

/** ProjectKind says where a project's code comes from. */
export type ProjectKind = "remote" | "local";

/** CreateProject is the body of POST /api/projects. */
export type CreateProject = {
  name: string;
  kind: ProjectKind;
  remote_url?: string;
  remote_username?: string;
  remote_password?: string;
  host_path?: string;
  default_branch?: string;
};

/** UpdateProject is the body of PATCH /api/projects/{id}; an absent field is left alone. */
export type UpdateProject = {
  remote_username?: string;
  /** remote_password replaces the stored one; "" removes the credentials. */
  remote_password?: string;
  default_branch?: string;
};

/** WorkspaceLifecycle is a workspace's lifecycle state. */
export type WorkspaceLifecycle = "creating" | "running" | "stopped" | "gone";

/** Workspace is one sandbox container holding a checkout of a project. */
export type Workspace = {
  id: string;
  project_id: string;
  name: string;
  branch: string;
  base_commit?: string;
  image: string;
  state: WorkspaceLifecycle;
  container_id?: string;
  parent_workspace_id?: string;
  created_at: string;
  updated_at: string;
};

/** CreateWorkspace is the body of POST /api/workspaces. */
export type CreateWorkspace = {
  project_id: string;
  name: string;
  branch?: string;
  image?: string;
  build_context?: string;
  dockerfile?: string;
  parent_workspace_id?: string;
};

/** WorkspaceDiff is the body of GET /api/workspaces/{id}/diff. */
export type WorkspaceDiff = {
  workspace_id: string;
  base_commit?: string;
  diff: string;
  status: string;
};

/** FileEntry is one file or directory of a workspace listing. */
export type FileEntry = {
  name: string;
  /** path is relative to the workspace root. */
  path: string;
  size: number;
  mode: number;
  mod_time: string;
  is_dir: boolean;
};

/** FileContent is the body of GET /api/workspaces/{id}/file. */
export type FileContent = {
  path: string;
  size: number;
  /** binary says the file holds NUL bytes; content is then empty. */
  binary: boolean;
  /** too_large says the file is over the 2 MiB limit; content is then empty. */
  too_large: boolean;
  content: string;
};

/** CommitRequest is the body of POST /api/workspaces/{id}/commit. */
export type CommitRequest = {
  message: string;
  /** paths limits the commit to these files; absent commits everything. */
  paths?: string[];
};

/** CommitResult is what a commit made. */
export type CommitResult = {
  commit: string;
};

/** PushRequest is the body of POST /api/workspaces/{id}/push. */
export type PushRequest = {
  /** upstream also pushes the branch from the hub to the project's remote. */
  upstream?: boolean;
};

/** PushResult is where a push put the workspace's branch. */
export type PushResult = {
  branch: string;
  commit: string;
  upstream_pushed: boolean;
};

/** SessionKind is who opened a session. */
export type SessionKind = "user" | "fork" | "agent";

/** Session is a tree of entries in one workspace, or in none for a chat. */
export type Session = {
  id: string;
  /** workspace_id is where the session's runs act; a chat has none. */
  workspace_id?: string;
  title: string;
  /** kind says whether the user opened it, a fork made it, or a run spawned it. */
  kind: SessionKind;
  head_entry_id?: string;
  parent_session_id?: string;
  /** tools are the tools the next run offers the model, sorted by name. */
  tools: string[];
  /** profile_id is the profile the session chose; absent, it runs with the default one. */
  profile_id?: string;
  /** overridden says the session sets something of its own over its profile. */
  overridden: boolean;
  created_at: string;
  updated_at: string;
};

/** Tool is one tool a run can offer the model, from GET /api/tools. */
export type Tool = {
  name: string;
  /** description is what the model is told the tool does. */
  description: string;
  /** needs_workspace keeps the tool out of a chat. */
  needs_workspace: boolean;
  /**
   * server names the MCP server whose tool it is; absent for a built-in tool
   * and for the resource tools, which reach every server of a run.
   */
  server?: string;
  /** tokens is the estimated size of the tool's definition, which offering it costs every request. */
  tokens: number;
};

/** CreateSession opens a session in a workspace, or a chat in none. */
export type CreateSession = { workspace_id: string; title: string } | { chat: true; title: string };

/** Role is the author of one provider message. */
export type Role = "system" | "user" | "assistant" | "tool";

/** MessageToolCall is one tool call a model asked for. */
export type MessageToolCall = {
  id: string;
  name: string;
  arguments: unknown;
  /** arguments_malformed marks `arguments` as the model's quoted malformed text. */
  arguments_malformed?: boolean;
};

/** MessageMetrics restores the measured context state with an assistant entry. */
export type MessageMetrics = {
  run_id: string;
  usage: Usage;
  context: Usage;
  generation_ms: number;
  context_window: number;
  /** timings is how fast the model call that produced this message ran. */
  timings?: Timings;
};

/** Message is one entry of a conversation, as the provider package stores it. */
export type Message = {
  role: Role;
  content?: string;
  /** reasoning is the model's thinking for this message, shown apart from its answer. */
  reasoning?: string;
  tool_calls?: MessageToolCall[];
  tool_call_id?: string;
  is_error?: boolean;
  /** metrics are stored UI metadata and are not sent to the provider. */
  metrics?: MessageMetrics;
};

/** Entry is one stored node of a session tree, with its message. */
export type Entry = {
  id: string;
  parent_id?: string;
  seq: number;
  kind: EntryKind;
  commit?: string;
  created_at: string;
  message: Message;
};

/** Node is one entry of a session outline: the tree without payloads. */
export type Node = {
  id: string;
  parent_id?: string;
  kind: EntryKind;
  preview: string;
  commit?: string;
  /**
   * resumable is whether a run can continue from this entry. An entry in the
   * middle of a turn leaves tool calls unanswered, so the harness refuses to
   * put the head or a fork there and the tree offers neither.
   */
  resumable: boolean;
  created_at: string;
};

/** SessionOutline is the body of GET /api/sessions/{id}/outline. */
export type SessionOutline = {
  session_id: string;
  head_entry_id?: string;
  nodes: Node[];
};

/** SessionPath is the body of GET /api/sessions/{id}/path. */
export type SessionPath = {
  session_id: string;
  entries: Entry[];
  messages: Message[];
};

/** RunState is where an agent run ended up. */
export type RunState = "running" | "done" | "error" | "aborted";

/** Run is one execution of the agent loop on a session. */
export type Run = {
  id: string;
  session_id: string;
  state: RunState;
  started_at: string;
  finished_at?: string;
  error?: string;
};

/** Question is an `ask_user` call a run is blocked on. */
export type Question = {
  id: string;
  session_id: string;
  run_id: string;
  call_id: string;
  question: string;
  options?: string[];
  allow_free_text: boolean;
  asked_at: string;
};

/** RunStatus is the body of GET /api/sessions/{id}/run. */
export type RunStatus = {
  session_id: string;
  active: boolean;
  run?: Run;
  pending_steering: string[];
  pending_follow_ups: string[];
  questions: Question[];
  /** elicitations are what MCP servers asked the user during this session's tool calls. */
  elicitations: Elicitation[];
};

/** MessageMode says what the harness does with a posted message. */
export type MessageMode = "run" | "steer" | "follow_up";

/** PostMessage is the body of POST /api/sessions/{id}/messages. */
export type PostMessage = {
  text: string;
  mode?: MessageMode;
  model?: string;
};

/** Settings is the settings table as one object of arbitrary JSON values. */
export type Settings = Record<string, unknown>;

/** SettingsDefaults are the values the harness uses until the settings name one. */
export type SettingsDefaults = {
  sandbox_image: string;
  subagent_max_depth: number;
  subagent_max_children: number;
  /** search_order is every web search provider in its default order. */
  search_order: string[];
  /** search_limits is every search quota bucket's default limit. */
  search_limits: Record<string, SearchLimit>;
};

/** SettingsState is the body of GET and PUT /api/settings. */
export type SettingsState = {
  settings: Settings;
  defaults: SettingsDefaults;
};

/**
 * ReasoningEffort is the Chat Completions reasoning_effort value; "" leaves it
 * to the endpoint. Compatible endpoints disagree on the vocabulary, so it is
 * any word of letters, digits, hyphens, and underscores the user configured,
 * bounded by `maxReasoningEffortLength`.
 */
export type ReasoningEffort = string;

/** maxReasoningEffortLength is the longest reasoning_effort the harness stores. */
export const maxReasoningEffortLength = 32;

/** effortNone is the reasoning effort that turns thinking off. */
export const effortNone = "none";

/**
 * ThinkingSwitch is the request field that carries the effort "none". The
 * standard reasoning_effort is what most endpoints take; chat_template_kwargs
 * and thinking are extensions some need instead, and only one is ever sent.
 */
export type ThinkingSwitch = "reasoning_effort" | "chat_template_kwargs" | "thinking";

/** Model is one model the user configured on a provider. */
export type Model = {
  id: string;
  provider_id: string;
  /** name is what Eika calls the model, unique across providers. */
  name: string;
  /** model is the identifier the provider's endpoint knows it by. */
  model: string;
  context_window: number;
  max_output: number;
  reasoning_effort?: ReasoningEffort;
  /** reasoning_efforts are the values this model offers, in cycling order. */
  reasoning_efforts: ReasoningEffort[];
  /** thinking_switch is the field that turns thinking off when the effort is "none". */
  thinking_switch: ThinkingSwitch;
  preserve_thinking: boolean;
  created_at: string;
  updated_at: string;
};

/** Models is the body of GET /api/models. */
export type Models = {
  models: Model[];
  /** default is the model a run uses when it names none; absent when there are no models. */
  default?: string;
};

/** CreateModel is the body of POST /api/models. */
export type CreateModel = {
  provider_id: string;
  /** name defaults to model. */
  name?: string;
  model: string;
  context_window: number;
  max_output: number;
  reasoning_effort?: ReasoningEffort;
  reasoning_efforts?: ReasoningEffort[];
  thinking_switch?: ThinkingSwitch;
  preserve_thinking?: boolean;
};

/** UpdateModel is the body of PATCH /api/models/{id}; an absent field is left alone. */
export type UpdateModel = Partial<Omit<CreateModel, "provider_id">>;

/** TestModel is the body of POST /api/models/test. */
export type TestModel = {
  provider_id: string;
  model: string;
  reasoning_effort?: ReasoningEffort;
  thinking_switch?: ThinkingSwitch;
  preserve_thinking?: boolean;
};

/** TestModelResult is what one small request to a model came back with. */
export type TestModelResult = {
  reply: string;
  stop_reason: string;
  latency_ms: number;
};

/** Provider is one model provider: an endpoint and whether it holds a key. */
export type Provider = {
  id: string;
  name: string;
  kind: string;
  base_url: string;
  api_key_set: boolean;
  /** api_key_hint is the last characters of a long key. */
  api_key_hint?: string;
  created_at: string;
  updated_at: string;
};

/** Providers is the body of GET /api/providers. */
export type Providers = {
  providers: Provider[];
  kinds: string[];
};

/** CreateProvider is the body of POST /api/providers. */
export type CreateProvider = {
  name: string;
  kind?: string;
  base_url: string;
  api_key?: string;
};

/** UpdateProvider is the body of PATCH /api/providers/{id}; an absent field is left alone. */
export type UpdateProvider = {
  name?: string;
  base_url?: string;
  /** api_key replaces the stored key; "" removes it. */
  api_key?: string;
};

/**
 * ProbeProvider is the body of POST /api/providers/probe: a stored provider,
 * an endpoint not saved yet, or a stored one with fields the form changed.
 */
export type ProbeProvider = {
  provider_id?: string;
  kind?: string;
  base_url?: string;
  api_key?: string;
};

/** ModelInfo is one model an endpoint reports; limits are 0 or absent when it does not say. */
export type ModelInfo = {
  id: string;
  context_window?: number;
  max_output?: number;
};

/** AuthStatus is the body of GET /api/auth/status. */
export type AuthStatus = {
  password_set: boolean;
};

/** SignIn is a new session: the bearer token and when it expires. */
export type SignIn = {
  token: string;
  expires_at: string;
};

/** SystemStatus is the body of GET /api/system. */
export type SystemStatus = {
  docker: { reachable: boolean; error?: string };
  sandbox_image: { name: string; present: boolean };
  providers: number;
  models: number;
  projects: number;
};

/** SearchLimit is a quota on one search bucket. An absent or zero field is unlimited. */
export type SearchLimit = {
  day?: number;
  month?: number;
};

/** SearchUsage is one bucket's counters, in UTC days and months. */
export type SearchUsage = {
  day: string;
  day_used: number;
  month: string;
  month_used: number;
  cooldown_until?: string;
  fail_streak?: number;
};

/** SearchBackendStatus is the health of one search backend. */
export type SearchBackendStatus = {
  name: string;
  /** web marks a provider in the web failover chain; the others are sources. */
  web: boolean;
  key?: string;
  key_required?: boolean;
  key_set: boolean;
  bucket?: string;
  /** state is "ready", "no API key", or why the bucket is blocked. */
  state: string;
  usage: SearchUsage;
  limit: SearchLimit;
  /** probe says whether a self-hosted backend answers. */
  probe?: string;
};

/** SearchKey is one search API key. The key itself never leaves the harness. */
export type SearchKey = {
  name: string;
  set: boolean;
  hint?: string;
  updated_at?: string;
};

/** SearchStatus is the body of GET /api/search/status. */
export type SearchStatus = {
  order: string[];
  backends: SearchBackendStatus[];
  cached_searches: number;
  keys: SearchKey[];
  cached_pages: number;
  searxng_url: string;
};

/** SearchRequest is the body of POST /api/search, and web_search's arguments. */
export type SearchRequest = {
  query: string;
  source?: string;
  count?: number;
};

/** SearchResult is one search hit. */
export type SearchResult = {
  title: string;
  url: string;
  description?: string;
};

/** SearchDetails describe how a search went: web_search's tool details. */
export type SearchDetails = {
  source: string;
  query: string;
  count: number;
  providers?: string[];
  attempts?: { provider: string; error: string }[];
  cached?: boolean;
  pool?: number;
  results?: SearchResult[];
  ms: number;
};

/** SearchOutcome is the body of POST /api/search. */
export type SearchOutcome = {
  text: string;
  is_error: boolean;
  details: SearchDetails;
};

/** FetchDetails describe how a fetch went: web_fetch's tool details. */
export type FetchDetails = {
  url: string;
  format: string;
  section?: string;
  filter?: string;
  section_matched?: boolean;
  final_url?: string;
  container?: string;
  content_type?: string;
  bytes?: number;
  body_truncated?: boolean;
  cached?: boolean;
  mode?: "full" | "truncated" | "outline";
  budget_truncated?: boolean;
  headings?: number;
  filter_outcome?: "ok" | "empty" | "error";
  ms: number;
};

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

/**
 * Sampling holds the parameters that steer how a model samples. A parameter
 * left out is not set at that layer: for what is sent, the endpoint's default.
 * An empty stop list sends none, which clears the list of a layer below.
 */
export type Sampling = {
  temperature?: number;
  top_p?: number;
  top_k?: number;
  min_p?: number;
  frequency_penalty?: number;
  presence_penalty?: number;
  seed?: number;
  stop?: string[];
  max_output?: number;
  reasoning_effort?: ReasoningEffort;
};

/** SamplingKey names one sampling parameter. */
export type SamplingKey = keyof Sampling;

/**
 * ConfigLayer is where a resolved value came from, top first: the run
 * request, the session's overrides, its profile, the model row, or the
 * defaults (the default model and profile, the built-in prompts, every tool,
 * and for a sampling parameter the endpoint's own).
 */
export type ConfigLayer = "request" | "session" | "profile" | "model" | "default";

/**
 * ProfileSettings is what a profile sets, and what a session overrides of
 * it. null, an absent model_id, and a sampling parameter left out are not
 * set, and fall through.
 */
export type ProfileSettings = {
  model_id?: string;
  /** workspace_prompt and chat_prompt replace the built-in base prompts, "" included. */
  workspace_prompt: string | null;
  chat_prompt: string | null;
  instructions: string | null;
  context_files: boolean | null;
  /** preserve_thinking replays earlier reasoning to the model, over the model's own switch. */
  preserve_thinking: boolean | null;
  sampling: Sampling;
};

/** Configuration is what a run resolves to, with the layer each value came from. */
export type Configuration = {
  profile_id: string;
  profile_name: string;
  /** model_id and model are empty when no model is configured. */
  model_id: string;
  model: string;
  workspace_prompt: string;
  chat_prompt: string;
  instructions: string;
  context_files: boolean;
  preserve_thinking: boolean;
  /** tools is the tool choice; null is every tool the session can run. */
  tools: string[] | null;
  sampling: Sampling;
  /** dropped_effort is an effort chosen above the model that the model does not offer. */
  dropped_effort?: string;
  /**
   * sources names the layer of each value: profile, model, workspace_prompt,
   * chat_prompt, instructions, context_files, preserve_thinking, tools, and
   * `sampling.<parameter>` for each parameter sent.
   */
  sources: Record<string, ConfigLayer> | null;
};

/** Profile is a named configuration of what a run sends. */
export type Profile = ProfileSettings & {
  id: string;
  name: string;
  description: string;
  /** tools is the tool choice, where `mcp__<server>__*` is every tool of a server; null is every tool. */
  tools: string[] | null;
  /** inherited is what the profile's unset values fall through to. */
  inherited: Configuration;
  created_at: string;
  updated_at: string;
};

/** Profiles is the body of GET /api/profiles. */
export type Profiles = {
  profiles: Profile[] | null;
  /** default is the id of the profile a session that chose none runs with. */
  default: string;
  /** prompts are the built-in base prompts. */
  prompts: { workspace: string; chat: string };
};

/** ProfileInput is the body of POST /api/profiles and PUT /api/profiles/{id}: the whole profile. */
export type ProfileInput = Partial<ProfileSettings> & {
  name: string;
  description?: string;
  tools?: string[] | null;
};

/** SessionConfiguration is what a session sets itself and what its next run resolves to. */
export type SessionConfiguration = {
  session_id: string;
  /** profile_id is the profile the session chose; absent, the default. */
  profile_id?: string;
  overrides: ProfileSettings;
  /** tools is the session's own tool choice, null when it has made none. */
  tools: string[] | null;
  /** resolved is what the next run uses when the message names no model. */
  resolved: Configuration;
  /** inherited is the same as if the session set nothing itself. */
  inherited: Configuration;
};

/** SectionKind names a part of the system prompt. */
export type SectionKind = "base" | "context_files" | "instructions";

/** ContextSection is one part of the system prompt, with its estimated size. */
export type ContextSection = {
  kind: SectionKind;
  text: string;
  tokens: number;
  /** files are the context files a context_files section renders. */
  files?: { path: string; text: string; tokens: number }[];
};

/** ToolSchema is one tool definition a request sends. */
export type ToolSchema = {
  name: string;
  description: string;
  schema: unknown;
  source: "builtin" | "mcp";
  tokens: number;
};

/** RequestParameters are what a request sends beside its content. */
export type RequestParameters = {
  model: string;
  sampling: Sampling;
  thinking_switch?: ThinkingSwitch;
  preserve_thinking: boolean;
};

/** ModelRequest is the record of one model call, without what it sent. */
export type ModelRequest = {
  id: string;
  session_id: string;
  run_id: string;
  entry_id?: string;
  /** model_id is absent once the model is deleted; model is its name at the time. */
  model_id?: string;
  model: string;
  message_tokens: number;
  /** input_tokens, output_tokens, and total_tokens are measured; 0 when not reported. */
  input_tokens: number;
  output_tokens: number;
  total_tokens: number;
  created_at: string;
};

/** ModelRequests is the body of GET /api/sessions/{id}/requests. */
export type ModelRequests = {
  session_id: string;
  requests: ModelRequest[] | null;
};

/**
 * ModelContext is one model request by section: the next one
 * (GET /api/sessions/{id}/context) or a recorded one
 * (GET /api/sessions/{id}/requests/{request_id}). Token counts are
 * estimates, four bytes to a token.
 */
export type ModelContext = {
  sections: ContextSection[] | null;
  tools: ToolSchema[] | null;
  messages: Message[] | null;
  message_tokens: number;
  /** message_sizes is the estimated size of each message, in order. */
  message_sizes: number[] | null;
  parameters: RequestParameters;
  /** sources names the layer of each parameter: model, thinking_switch, preserve_thinking, sampling.<name>. */
  sources: Record<string, ConfigLayer> | null;
  dropped_effort?: string;
  /**
   * context_files_unread says why a preview has no context files where a
   * run would read them: the workspace is not running.
   */
  context_files_unread?: string;
  /** request is the record this is; absent for the next request. */
  request?: ModelRequest;
  /** context_window is the model's window in tokens; 0 when the model is not known. */
  context_window: number;
  /** calibration is a measured call to scale the estimates to. */
  calibration?: { request_id: string; input_tokens: number; estimated_tokens: number };
};

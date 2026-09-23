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

/** Session is a tree of entries in one workspace. */
export type Session = {
  id: string;
  workspace_id: string;
  title: string;
  /** kind says whether the user opened it, a fork made it, or a run spawned it. */
  kind: SessionKind;
  head_entry_id?: string;
  parent_session_id?: string;
  created_at: string;
  updated_at: string;
};

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
  preserve_thinking?: boolean;
};

/** UpdateModel is the body of PATCH /api/models/{id}; an absent field is left alone. */
export type UpdateModel = Partial<Omit<CreateModel, "provider_id">>;

/** TestModel is the body of POST /api/models/test. */
export type TestModel = {
  provider_id: string;
  model: string;
  reasoning_effort?: ReasoningEffort;
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

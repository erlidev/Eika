/**
 * Wire types for the HTTP API. They mirror the Go structs in
 * `internal/server` exactly; the contract is documented in docs/api/http.md.
 * When a Go type changes, change these in the same commit.
 */

import type { EntryKind } from "@/api/events";

/** ErrorCode is the machine-readable half of an API error body. */
export type ErrorCode = "invalid_request" | "unauthorized" | "not_found" | "conflict" | "internal";

/** Project is a git repository Eika knows. */
export type Project = {
  id: string;
  name: string;
  kind: ProjectKind;
  remote_url?: string;
  /** remote_username_env names the variable holding the upstream username, never its value. */
  remote_username_env?: string;
  /** remote_password_env names the variable holding the upstream password, never its value. */
  remote_password_env?: string;
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
  remote_username_env?: string;
  remote_password_env?: string;
  host_path?: string;
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

/** Session is a tree of entries in one workspace. */
export type Session = {
  id: string;
  workspace_id: string;
  title: string;
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

/** Message is one entry of a conversation, as the provider package stores it. */
export type Message = {
  role: Role;
  content?: string;
  /** reasoning is opaque provider data Eika replays; it is not assistant content. */
  reasoning?: string;
  tool_calls?: MessageToolCall[];
  tool_call_id?: string;
  is_error?: boolean;
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

/** Model is one model the deployment configured. */
export type Model = {
  name: string;
  context_window: number;
  max_output: number;
};

/** Models is the body of GET /api/models. */
export type Models = {
  models: Model[];
  default?: string;
};

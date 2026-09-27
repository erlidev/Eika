/** Wire types for sessions, their entries, and the tools a run offers. */

import type { CompactionReason, EntryKind, Timings, Usage } from "@/api/events";

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

/**
 * CreateSession opens a session in a workspace, or a chat in none. Without a
 * title it is untitled: it shows a placeholder until its first run names it.
 */
export type CreateSession =
  { workspace_id: string; title?: string } | { chat: true; title?: string };

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
  /**
   * summary marks the user message that carries a compaction's summary in
   * place of the conversation before it.
   */
  summary?: boolean;
};

/** Compaction is what an entry of kind compaction records. */
export type Compaction = {
  /** summary takes the place of the conversation before the entry. */
  summary: string;
  /** kept is how many of the messages before the entry stay after the summary. */
  kept: number;
  /** tokens_before and tokens_after are the context's estimated size. */
  tokens_before: number;
  tokens_after: number;
  reason: CompactionReason;
  /** usage is what making the summary cost. */
  usage: Usage;
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
  /** compaction is present on an entry of kind compaction. */
  compaction?: Compaction;
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

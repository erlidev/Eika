/** Wire types for sessions, their entries, and the tools a run offers. */

import type { CompactionReason, EntryKind, Timings, Usage } from "@/api/events";

/** SessionKind is who opened a session. */
export type SessionKind = "user" | "home" | "subagent" | "routine";

/** SessionState is the lifecycle state set by the harness. */
export type SessionState = "idle" | "running" | "waiting" | "failed" | "cancelled";

/** Session is a tree of entries in one computer, or in none for a chat. */
export type Session = {
  owner: string;
  agent?: string;
  name?: string;
  project_id?: string;
  parent_session_id?: string;
  state: SessionState;
  brief?: string;
  result?: string;
  last_active_at: string;
  id: string;
  /** computer_id is where the session's runs act; a chat has none. */
  computer_id?: string;
  title: string;
  /** kind says who opened the session. */
  kind: SessionKind;
  head_entry_id?: string;
  /** tools are the tools the next run offers the model, sorted by name. */
  tools: string[];
  /** profile_id is the profile the session chose; absent, it runs with the default one. */
  profile_id?: string;
  /** overridden says the session sets something of its own over its profile. */
  overridden: boolean;
  /** pinned sessions are listed before the others. */
  pinned: boolean;
  /** archived sessions are set aside under an Archived heading; they run like any other. */
  archived: boolean;
  created_at: string;
  updated_at: string;
};

/** UpdateSession is the body of PATCH /api/sessions/{id}; an absent field is left alone. */
export type UpdateSession = {
  title?: string;
  pinned?: boolean;
  archived?: boolean;
};

/** Tool is one tool a run can offer the model, from GET /api/tools. */
export type Tool = {
  name: string;
  /** description is what the model is told the tool does. */
  description: string;
  /** needs_computer keeps the tool out of a chat. */
  needs_computer: boolean;
  /**
   * server names the MCP server whose tool it is; absent for a built-in tool
   * and for the resource tools, which reach every server of a run.
   */
  server?: string;
  /** tokens is the estimated size of the tool's definition, which offering it costs every request. */
  tokens: number;
};

/**
 * CreateSession opens a session in a computer, or a chat in none. Without a
 * title it is untitled: it shows a placeholder until its first run names it.
 */
export type CreateSession =
  { computer_id: string; title?: string } | { chat: true; title?: string };

/** Role is the author of one provider message. */
export type Role = "system" | "user" | "assistant" | "tool";

/** MessageToolCall is one tool call a model asked for. */
export type MessageToolCall = {
  /** id is Eika's own id for the call, the one events and results use. */
  id: string;
  /**
   * wire_id is the id the model server gave the call, kept to send back as the
   * server knows it. Absent on a call stored before Eika minted ids, whose one
   * id is both. A client does not read it.
   */
  wire_id?: string;
  name: string;
  arguments: unknown;
  /** arguments_malformed marks `arguments` as the model's quoted malformed text. */
  arguments_malformed?: boolean;
  /**
   * arguments_text is the exact text of the arguments, stored where the value
   * above would not give it back byte for byte. A client does not read it.
   */
  arguments_text?: string;
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

/**
 * MessageImage is one picture a user message carries, as the model is sent
 * it: `data` is the base64 file in `media_type`, image/png or image/jpeg.
 */
export type MessageImage = {
  media_type: string;
  data: string;
  width: number;
  height: number;
};

/** Message is one entry of a conversation, as the provider package stores it. */
export type Message = {
  /** An entry with no provider message has an empty role. */
  role: Role | "";
  content?: string;
  /**
   * context is the block the model reads at the end of a user message: the
   * time it was sent. It is not what the user wrote, so the transcript shows
   * `content` alone and the Context panel shows both.
   */
  context?: string;
  /** images are the pictures a user message carries beside its text. */
  images?: MessageImage[];
  /** reasoning is the model's thinking for this message, shown apart from its answer. */
  reasoning?: string;
  /**
   * reasoning_items are the reasoning items a Responses endpoint returned,
   * exactly as it sent them, so a later request can send them back. The UI
   * does not read them.
   */
  reasoning_items?: unknown[];
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
  channel?: string;
  /** text is present on preamble and notice entries. */
  text?: string;
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
   * put the head there and the tree does not offer it.
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

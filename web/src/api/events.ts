/**
 * Wire types for the event stream. The contract is documented in
 * docs/api/events.md and owned by internal/event; change both in one commit.
 */

/** EventType is the name of an event Eika streams. */
export type EventType =
  | "turn.start"
  | "message.delta"
  | "message.reset"
  | "tool.call"
  | "tool.output"
  | "tool.result"
  | "turn.end"
  | "run.error"
  | "question.asked"
  | "subagent.started"
  | "subagent.finished"
  | "workspace.state"
  | "session.message"
  | "bus.dropped";

/** EikaEvent is the envelope every streamed event uses. */
export type EikaEvent = {
  type: EventType;
  topic: string;
  time: string;
  payload?: unknown;
};

/** TurnStart is the payload of a turn.start event. */
export type TurnStart = {
  run_id: string;
  session_id: string;
  workspace_id?: string;
  message: string;
};

/** MessageDelta is the payload of a message.delta event. */
export type MessageDelta = {
  run_id: string;
  text: string;
};

/** MessageReset tells the client to discard text from a failed attempt. */
export type MessageReset = {
  run_id: string;
};

/** ToolCall is the payload of a tool.call event. */
export type ToolCall = {
  run_id: string;
  call_id: string;
  name: string;
  arguments: unknown;
  arguments_malformed?: boolean;
};

/** ToolOutput is the payload of a tool.output event. */
export type ToolOutput = {
  run_id: string;
  call_id: string;
  text: string;
};

/** ToolResult is the payload of a tool.result event. */
export type ToolResult = {
  run_id: string;
  call_id: string;
  name: string;
  content: string;
  is_error: boolean;
  details?: unknown;
  duration_ms: number;
};

/** Usage reports the tokens a turn cost. */
export type Usage = {
  input_tokens: number;
  output_tokens: number;
  total_tokens: number;
};

/** TurnEnd is the payload of a turn.end event. */
export type TurnEnd = {
  run_id: string;
  stop_reason?: string;
  usage: Usage;
};

/** RunError is the payload of a run.error event. */
export type RunError = {
  run_id: string;
  message: string;
  retryable: boolean;
};

/** QuestionAsked is the payload of a question.asked event. */
export type QuestionAsked = {
  run_id: string;
  session_id: string;
  call_id: string;
  question_id: string;
  question: string;
  options?: string[];
  allow_free_text: boolean;
};

/** SubagentStarted is the payload of a subagent.started event: a run spawned a child agent. */
export type SubagentStarted = {
  subagent_id: string;
  parent_session_id: string;
  child_session_id: string;
  child_workspace_id: string;
  name: string;
  branch: string;
  base_commit?: string;
  task: string;
};

/** SubagentFinished is the payload of a subagent.finished event: a child agent ended. */
export type SubagentFinished = {
  subagent_id: string;
  parent_session_id: string;
  child_session_id: string;
  child_workspace_id: string;
  name: string;
  branch: string;
  state: string;
  commit?: string;
  summary?: string;
  diff_stat?: string;
  error?: string;
};

/** WorkspaceState is the payload of a workspace.state event. */
export type WorkspaceState = {
  workspace_id: string;
  project_id?: string;
  state: string;
};

/** EntryKind is what one session entry holds. */
export type EntryKind = "user" | "assistant" | "tool_call" | "tool_result" | "system" | "event";

/** SessionMessage is the payload of a session.message event, which a replay sends. */
export type SessionMessage = {
  session_id: string;
  entry_id: string;
  parent_id?: string;
  kind: EntryKind;
  commit?: string;
  created_at: string;
  message: unknown;
};

/** BusDropped is the payload of a bus.dropped event: this client lost events. */
export type BusDropped = {
  dropped: number;
};

/** StreamRequest is a message a client sends on the event stream. */
export type StreamRequest =
  | { type: "subscribe"; topics: string[] }
  | { type: "session.replay"; session_id: string; since?: string };

/** globalTopic is the topic of events that belong to no workspace or session. */
export const globalTopic = "global";

/** workspaceTopic names the topic carrying one workspace's events. */
export function workspaceTopic(id: string): string {
  return `workspace:${id}`;
}

/** sessionTopic names the topic carrying one session's events. */
export function sessionTopic(id: string): string {
  return `session:${id}`;
}

const eventTypes: readonly EventType[] = [
  "turn.start",
  "message.delta",
  "message.reset",
  "tool.call",
  "tool.output",
  "tool.result",
  "turn.end",
  "run.error",
  "question.asked",
  "subagent.started",
  "subagent.finished",
  "workspace.state",
  "session.message",
  "bus.dropped",
];

/**
 * parseEvent narrows an untyped stream message to EikaEvent. The payload stays
 * unknown: a consumer narrows it once it has checked the type.
 */
export function parseEvent(body: unknown): EikaEvent {
  if (typeof body !== "object" || body === null) {
    throw new Error("parse event: body is not an object");
  }
  const record = body as Record<string, unknown>;
  const type = record.type;
  if (typeof type !== "string" || !eventTypes.includes(type as EventType)) {
    throw new Error(`parse event: unknown type ${String(type)}`);
  }
  const topic = record.topic;
  const time = record.time;
  if (typeof topic !== "string" || typeof time !== "string") {
    throw new Error("parse event: topic or time is missing");
  }
  return { type: type as EventType, topic, time, payload: record.payload };
}

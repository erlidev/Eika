/**
 * Wire types for the event stream. The contract is documented in
 * docs/api/events.md and owned by internal/event; change both in one commit.
 */

/** EventType is the name of an event Eika streams. */
export type EventType =
  | "turn.start"
  | "message.delta"
  | "reasoning.delta"
  | "message.reset"
  | "tool.call"
  | "tool.output"
  | "tool.result"
  | "turn.progress"
  | "turn.end"
  | "run.error"
  | "question.asked"
  | "subagent.started"
  | "subagent.finished"
  | "workspace.state"
  | "mcp.server"
  | "mcp.elicitation"
  | "session.title"
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

/** ReasoningDelta is the payload of a reasoning.delta event. */
export type ReasoningDelta = {
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

/**
 * Timings is how fast one model call ran: the tokens of each phase and the
 * milliseconds that phase took, so a reader divides one by the other. Reading
 * the prompt and generating the answer run at speeds orders of magnitude
 * apart and are never summed. A phase nobody measured is absent.
 */
export type Timings = {
  /** prompt_tokens and prompt_ms come only from an endpoint that reports them. */
  prompt_tokens?: number;
  prompt_ms?: number;
  decode_tokens?: number;
  decode_ms?: number;
  /** source says who timed the generation phase. */
  source?: "endpoint" | "harness";
};

/**
 * TurnProgress is the payload of a turn.progress event: the usage the endpoint
 * has reported for the turn so far and the time spent generating it. Both are
 * measured by the harness, so the difference between two of them is a true
 * decode rate.
 */
export type TurnProgress = {
  run_id: string;
  usage: Usage;
  /** context is the last model call's own prompt plus the response it produced. */
  context: Usage;
  /** generation_ms is the turn's time inside model responses, so far. */
  generation_ms: number;
  /** context_window is the configured window of the model that produced the usage. */
  context_window: number;
  /** timings is how fast the most recent model call ran, when anything measured it. */
  timings?: Timings;
};

/** TurnEnd is the payload of a turn.end event. */
export type TurnEnd = {
  run_id: string;
  stop_reason?: string;
  usage: Usage;
  /** context is how much of the model's window the conversation now fills. */
  context: Usage;
  /** generation_ms is the turn's total time inside model responses. */
  generation_ms: number;
  /** context_window is the configured window of the model that ran the turn. */
  context_window: number;
  /** timings is how fast the turn's last model call ran. */
  timings?: Timings;
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

/** MCPServerChanged is the payload of an mcp.server event: refetch the server's details. */
export type MCPServerChanged = {
  server_id: string;
  name: string;
  /** state is where the connection stands, or removed for a deleted server. */
  state: string;
  error?: string;
};

/** MCPElicitation is the payload of an mcp.elicitation event: an MCP server asks the user. */
export type MCPElicitation = {
  elicitation_id: string;
  run_id: string;
  session_id: string;
  call_id: string;
  server: string;
  mode: "form" | "url";
  message: string;
  requested_schema?: unknown;
  url?: string;
};

/** SessionTitle is the payload of a session.title event: an untitled session was named. */
export type SessionTitle = {
  session_id: string;
  /** workspace_id is absent for a chat. */
  workspace_id?: string;
  title: string;
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

/**
 * EventPayloads maps each event type to the payload it carries. It is the
 * table in docs/api/events.md, written once so that a consumer narrows a
 * payload by naming the type rather than by casting.
 */
export type EventPayloads = {
  "turn.start": TurnStart;
  "message.delta": MessageDelta;
  "reasoning.delta": ReasoningDelta;
  "message.reset": MessageReset;
  "tool.call": ToolCall;
  "tool.output": ToolOutput;
  "tool.result": ToolResult;
  "turn.progress": TurnProgress;
  "turn.end": TurnEnd;
  "run.error": RunError;
  "question.asked": QuestionAsked;
  "subagent.started": SubagentStarted;
  "subagent.finished": SubagentFinished;
  "workspace.state": WorkspaceState;
  "mcp.server": MCPServerChanged;
  "mcp.elicitation": MCPElicitation;
  "session.title": SessionTitle;
  "session.message": SessionMessage;
  "bus.dropped": BusDropped;
};

/**
 * payloadOf narrows an event's payload to the shape its type promises. It
 * answers null for a payload that is not an object, which a malformed frame
 * or a future field-less event can produce.
 */
export function payloadOf<K extends keyof EventPayloads>(
  e: EikaEvent,
  type: K,
): EventPayloads[K] | null {
  if (e.type !== type) return null;
  if (typeof e.payload !== "object" || e.payload === null) return null;
  return e.payload as EventPayloads[K];
}

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

/** eventTypes lists every EventType, for a check at runtime. */
export const eventTypes: readonly EventType[] = [
  "turn.start",
  "message.delta",
  "reasoning.delta",
  "message.reset",
  "tool.call",
  "tool.output",
  "tool.result",
  "turn.progress",
  "turn.end",
  "run.error",
  "question.asked",
  "subagent.started",
  "subagent.finished",
  "workspace.state",
  "mcp.server",
  "mcp.elicitation",
  "session.title",
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

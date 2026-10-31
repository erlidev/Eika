/**
 * Wire types for the event stream. The contract is documented in
 * docs/api/events.md and owned by internal/event; change both in one commit.
 */

import type { MessageImage } from "@/api/types/sessions";

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
  | "computer.state"
  | "mcp.server"
  | "mcp.elicitation"
  | "session.title"
  | "session.updated"
  | "session.message"
  | "compaction.start"
  | "compaction.end"
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
  computer_id?: string;
  message: string;
  /** images are the pictures the message carries, as the model is sent them. */
  images?: MessageImage[];
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

/**
 * Usage reports the tokens a model call or a turn cost, as the endpoint
 * stated them. A count the endpoint did not state is null, which is not 0: a
 * turn's count is null when any of its calls did not state it. input_tokens is
 * the whole prompt, cached tokens included; output_tokens is everything
 * generated, reasoning included.
 */
export type Usage = {
  input_tokens: number | null;
  output_tokens: number | null;
  total_tokens: number | null;
  /** cached_tokens is the part of the prompt served from the endpoint's cache. */
  cached_tokens: number | null;
  /** prefilled_tokens is the part of the prompt the endpoint processed, where it says so. */
  prefilled_tokens: number | null;
  /** reasoning_tokens is the part of output_tokens that was reasoning. */
  reasoning_tokens: number | null;
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

/** ComputerState is the payload of a computer.state event. */
export type ComputerState = {
  computer_id: string;
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
  /** computer_id is absent for a chat. */
  computer_id?: string;
  title: string;
};

/** SessionUpdated is the payload of a session.updated event: a session was renamed, pinned, or archived. */
export type SessionUpdated = {
  session_id: string;
  /** computer_id is absent for a chat. */
  computer_id?: string;
};

/** EntryKind is what one session entry holds. */
export type EntryKind =
  "user" | "assistant" | "tool_call" | "tool_result" | "system" | "event" | "compaction";

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

/**
 * CompactionReason is why a conversation was compacted: the user asked, the
 * next request would have left less of the window free than the reserve, or
 * the endpoint refused a request as larger than its window.
 */
export type CompactionReason = "manual" | "threshold" | "overflow";

/**
 * CompactionStart is the payload of a compaction.start event: the oldest part
 * of the conversation is being summarized.
 */
export type CompactionStart = {
  run_id: string;
  reason: CompactionReason;
  /** tokens_before is the estimated size of the context being compacted. */
  tokens_before: number;
};

/**
 * CompactionEnd is the payload of a compaction.end event. One that failed
 * carries error and changed nothing; one that succeeded is stored as an entry
 * of kind compaction.
 */
export type CompactionEnd = {
  run_id: string;
  reason: CompactionReason;
  tokens_before: number;
  /** tokens_after is the estimated size of the context that replaced it. */
  tokens_after?: number;
  /** summary now begins the context. */
  summary?: string;
  /** kept is how many messages stay after the summary. */
  kept?: number;
  /** usage is what making the summary cost. */
  usage: Usage;
  error?: string;
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
  "computer.state": ComputerState;
  "mcp.server": MCPServerChanged;
  "mcp.elicitation": MCPElicitation;
  "session.title": SessionTitle;
  "session.updated": SessionUpdated;
  "session.message": SessionMessage;
  "compaction.start": CompactionStart;
  "compaction.end": CompactionEnd;
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

/** globalTopic is the topic of events that belong to no computer or session. */
export const globalTopic = "global";

/** computerTopic names the topic carrying one computer's events. */
export function computerTopic(id: string): string {
  return `computer:${id}`;
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
  "computer.state",
  "mcp.server",
  "mcp.elicitation",
  "session.title",
  "session.updated",
  "session.message",
  "compaction.start",
  "compaction.end",
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

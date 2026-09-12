/**
 * Wire types for the event stream. The contract is documented in
 * docs/api/events.md and owned by internal/event; change both in one commit.
 */

/** EventType is the name of an event Eika streams. */
export type EventType =
  | "turn.start"
  | "message.delta"
  | "tool.call"
  | "tool.output"
  | "tool.result"
  | "turn.end"
  | "run.error"
  | "question.asked"
  | "subagent.started"
  | "subagent.finished"
  | "workspace.state";

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

/** ToolCall is the payload of a tool.call event. */
export type ToolCall = {
  run_id: string;
  call_id: string;
  name: string;
  arguments: unknown;
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

const eventTypes: readonly EventType[] = [
  "turn.start",
  "message.delta",
  "tool.call",
  "tool.output",
  "tool.result",
  "turn.end",
  "run.error",
  "question.asked",
  "subagent.started",
  "subagent.finished",
  "workspace.state",
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

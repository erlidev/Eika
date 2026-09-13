/**
 * The transcript reducer. It folds the event stream (docs/api/events.md) into
 * the list of items the session view renders, and it is a pure function so
 * that a scripted event sequence is the whole test.
 *
 * Two sources feed one transcript. `session.message` events, which a replay
 * sends, are the committed conversation and are keyed by entry id. Run events
 * (`turn.start`, `message.delta`, `tool.*`) are the turn in flight and are
 * keyed by its `run_id`. A turn that ends is sealed: its live items are
 * dropped as soon as the replay that follows delivers the stored entries, so
 * nothing is rendered twice.
 */

import { payloadOf } from "@/api/events";
import type { EikaEvent, SessionMessage, Usage } from "@/api/events";
import type { Message, Question } from "@/api/types";

/** UserItem is a message the user sent. */
export type UserItem = {
  kind: "user";
  key: string;
  runId: string;
  entryId?: string;
  text: string;
};

/** AssistantItem is model prose, accumulated from deltas while it streams. */
export type AssistantItem = {
  kind: "assistant";
  key: string;
  runId: string;
  entryId?: string;
  text: string;
  streaming: boolean;
};

/** ToolItem is one tool call with its streamed output and its result. */
export type ToolItem = {
  kind: "tool";
  key: string;
  runId: string;
  callId: string;
  name: string;
  arguments: unknown;
  /** output holds the chunks a running tool streamed, in arrival order. */
  output: string;
  /** content is what the model saw, present once the call finished. */
  content?: string;
  isError: boolean;
  details?: unknown;
  durationMs?: number;
  done: boolean;
};

/** ErrorItem is a run that failed. */
export type ErrorItem = {
  kind: "error";
  key: string;
  runId: string;
  message: string;
  retryable: boolean;
};

/** NoticeItem is a stored entry that is not part of the conversation. */
export type NoticeItem = {
  kind: "notice";
  key: string;
  runId: string;
  entryId?: string;
  text: string;
};

/** TranscriptItem is one renderable row of the session view. */
export type TranscriptItem = UserItem | AssistantItem | ToolItem | ErrorItem | NoticeItem;

/** TranscriptState is everything the session view derives from the stream. */
export type TranscriptState = {
  /** sessionId is the session this transcript belongs to. */
  sessionId: string;
  /** committed holds the stored entries a replay delivered, in path order. */
  committed: TranscriptItem[];
  /** entryIds are the entries already held, which is how a replay dedupes. */
  entryIds: readonly string[];
  /** lastEntryId is the newest entry seen; a replay resumes after it. */
  lastEntryId: string;
  /** live holds the turn in flight, which no entry covers yet. */
  live: TranscriptItem[];
  /** sealedTurns are turns that ended and whose live items the next replay replaces. */
  sealedTurns: readonly string[];
  /** activeTurnId is the streaming turn's run_id, empty when nothing streams. */
  activeTurnId: string;
  /** usage is the token cost of the last turn that ended. */
  usage?: Usage;
  /** stopReason is why the last turn stopped. */
  stopReason?: string;
  /** questions are the `ask_user` calls waiting for an answer. */
  questions: Question[];
  /**
   * toolDetails keeps what only the live stream carries, by call id: a tool
   * result's structured details and its duration. A stored entry holds the
   * text the model saw and nothing else, so a replay would otherwise lose a
   * command's exit code.
   */
  toolDetails: Record<string, { details?: unknown; durationMs: number }>;
  /** needsReplay is set when the client must ask for a replay to catch up. */
  needsReplay: boolean;
  /** dropped counts the events the connection lost, for the status bar. */
  dropped: number;
};

/** newTranscript returns the empty state for one session. */
export function newTranscript(sessionId: string): TranscriptState {
  return {
    sessionId,
    committed: [],
    entryIds: [],
    lastEntryId: "",
    live: [],
    sealedTurns: [],
    activeTurnId: "",
    questions: [],
    toolDetails: {},
    needsReplay: false,
    dropped: 0,
  };
}

/** items is the whole transcript in render order. */
export function items(state: TranscriptState): TranscriptItem[] {
  return [...state.committed, ...state.live];
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null;
}

/** asMessage narrows a stored entry payload to a provider message. */
function asMessage(payload: unknown): Message | null {
  if (!isRecord(payload)) return null;
  const role = payload.role;
  if (typeof role !== "string") return null;
  return payload as Message;
}

function replaceAt(list: TranscriptItem[], index: number, item: TranscriptItem): TranscriptItem[] {
  const next = list.slice();
  next[index] = item;
  return next;
}

/** findTool locates the tool item of a call id in a list. */
function findTool(list: TranscriptItem[], callId: string): number {
  return list.findIndex((item) => item.kind === "tool" && item.callId === callId);
}

/**
 * applyEvent folds one stream event into the transcript. An event for another
 * session, or one this build has no rule for, returns the state unchanged.
 */
export function applyEvent(state: TranscriptState, e: EikaEvent): TranscriptState {
  switch (e.type) {
    case "turn.start":
      return applyTurnStart(state, e);
    case "message.delta":
      return applyDelta(state, e);
    case "message.reset":
      return applyReset(state, e);
    case "tool.call":
      return applyToolCall(state, e);
    case "tool.output":
      return applyToolOutput(state, e);
    case "tool.result":
      return applyToolResult(state, e);
    case "turn.end":
      return applyTurnEnd(state, e);
    case "run.error":
      return applyRunError(state, e);
    case "question.asked":
      return applyQuestion(state, e);
    case "session.message":
      return applySessionMessage(state, e);
    case "bus.dropped":
      return applyDropped(state, e);
    default:
      return state;
  }
}

function applyTurnStart(state: TranscriptState, e: EikaEvent): TranscriptState {
  const p = payloadOf(e, "turn.start");
  if (!p) return state;
  const live = state.live.slice();
  if (p.message !== "") {
    live.push({ kind: "user", key: `${p.run_id}:user`, runId: p.run_id, text: p.message });
  }
  return { ...state, live, activeTurnId: p.run_id, stopReason: undefined };
}

function applyDelta(state: TranscriptState, e: EikaEvent): TranscriptState {
  const p = payloadOf(e, "message.delta");
  if (!p) return state;
  // Deltas accumulate into the turn's current assistant message, which is the
  // last live item when it is still streaming and a new one otherwise: a tool
  // call between two runs of prose starts a fresh paragraph.
  const last = state.live.at(-1);
  if (last?.kind === "assistant" && last.runId === p.run_id && last.streaming) {
    return {
      ...state,
      live: replaceAt(state.live, state.live.length - 1, { ...last, text: last.text + p.text }),
    };
  }
  const key = `${p.run_id}:assistant:${String(state.live.length)}`;
  return {
    ...state,
    live: [
      ...state.live,
      { kind: "assistant", key, runId: p.run_id, text: p.text, streaming: true },
    ],
  };
}

/**
 * applyReset drops the text of a model attempt that failed and is about to be
 * retried. Only the turn's own streaming prose goes: a tool call the attempt
 * already made is a fact of the session, not of the attempt.
 */
function applyReset(state: TranscriptState, e: EikaEvent): TranscriptState {
  const p = payloadOf(e, "message.reset");
  if (!p) return state;
  const live = state.live.filter(
    (item) => !(item.kind === "assistant" && item.runId === p.run_id && item.streaming),
  );
  return live.length === state.live.length ? state : { ...state, live };
}

function seal(list: TranscriptItem[]): TranscriptItem[] {
  const last = list.at(-1);
  if (last?.kind === "assistant" && last.streaming) {
    return replaceAt(list, list.length - 1, { ...last, streaming: false });
  }
  return list;
}

function applyToolCall(state: TranscriptState, e: EikaEvent): TranscriptState {
  const p = payloadOf(e, "tool.call");
  if (!p) return state;
  const live = seal(state.live);
  if (findTool(live, p.call_id) >= 0) return { ...state, live };
  return {
    ...state,
    live: [
      ...live,
      {
        kind: "tool",
        key: p.call_id,
        runId: p.run_id,
        callId: p.call_id,
        name: p.name,
        arguments: p.arguments,
        output: "",
        isError: false,
        done: false,
      },
    ],
  };
}

function applyToolOutput(state: TranscriptState, e: EikaEvent): TranscriptState {
  const p = payloadOf(e, "tool.output");
  if (!p) return state;
  const index = findTool(state.live, p.call_id);
  const item = state.live[index];
  if (index < 0 || item?.kind !== "tool") return state;
  return {
    ...state,
    live: replaceAt(state.live, index, { ...item, output: item.output + p.text }),
  };
}

function applyToolResult(state: TranscriptState, e: EikaEvent): TranscriptState {
  const p = payloadOf(e, "tool.result");
  if (!p) return state;
  // A finished call answers any question it was blocked on.
  const questions = state.questions.filter((q) => q.call_id !== p.call_id);
  const toolDetails = {
    ...state.toolDetails,
    [p.call_id]: { details: p.details, durationMs: p.duration_ms },
  };
  const index = findTool(state.live, p.call_id);
  const item = state.live[index];
  if (index < 0 || item?.kind !== "tool") return { ...state, questions, toolDetails };
  return {
    ...state,
    questions,
    toolDetails,
    live: replaceAt(state.live, index, {
      ...item,
      name: p.name,
      content: p.content,
      isError: p.is_error,
      details: p.details,
      durationMs: p.duration_ms,
      done: true,
    }),
  };
}

function applyTurnEnd(state: TranscriptState, e: EikaEvent): TranscriptState {
  const p = payloadOf(e, "turn.end");
  if (!p) return state;
  return {
    ...state,
    live: seal(state.live),
    activeTurnId: state.activeTurnId === p.run_id ? "" : state.activeTurnId,
    usage: p.usage,
    stopReason: p.stop_reason,
    // The turn's entries are written now; a replay fetches them and replaces
    // the live items this turn built.
    sealedTurns: state.sealedTurns.includes(p.run_id)
      ? state.sealedTurns
      : [...state.sealedTurns, p.run_id],
    needsReplay: true,
  };
}

function applyRunError(state: TranscriptState, e: EikaEvent): TranscriptState {
  const p = payloadOf(e, "run.error");
  if (!p) return state;
  return {
    ...state,
    live: [
      ...seal(state.live),
      {
        kind: "error",
        key: `${p.run_id}:error:${String(state.live.length)}`,
        runId: p.run_id,
        message: p.message,
        retryable: p.retryable,
      },
    ],
    activeTurnId: state.activeTurnId === p.run_id ? "" : state.activeTurnId,
  };
}

function applyQuestion(state: TranscriptState, e: EikaEvent): TranscriptState {
  const p = payloadOf(e, "question.asked");
  if (!p) return state;
  if (state.questions.some((q) => q.id === p.question_id)) return state;
  const question: Question = {
    id: p.question_id,
    session_id: p.session_id,
    run_id: p.run_id,
    call_id: p.call_id,
    question: p.question,
    allow_free_text: p.allow_free_text,
    asked_at: e.time,
    ...(p.options ? { options: p.options } : {}),
  };
  return { ...state, questions: [...state.questions, question] };
}

/** entryItems turns one stored entry into the items it renders as. */
function entryItems(p: SessionMessage): TranscriptItem[] {
  const message = asMessage(p.message);
  switch (p.kind) {
    case "user":
      return [
        {
          kind: "user",
          key: p.entry_id,
          runId: "",
          entryId: p.entry_id,
          text: message?.content ?? "",
        },
      ];
    case "assistant": {
      const out: TranscriptItem[] = [];
      if (message?.content) {
        out.push({
          kind: "assistant",
          key: p.entry_id,
          runId: "",
          entryId: p.entry_id,
          text: message.content,
          streaming: false,
        });
      }
      for (const call of message?.tool_calls ?? []) {
        out.push({
          kind: "tool",
          key: call.id,
          runId: "",
          callId: call.id,
          name: call.name,
          arguments: call.arguments,
          output: "",
          isError: false,
          done: false,
        });
      }
      return out;
    }
    default:
      return [
        {
          kind: "notice",
          key: p.entry_id,
          runId: "",
          entryId: p.entry_id,
          text: message?.content ?? "",
        },
      ];
  }
}

function applySessionMessage(state: TranscriptState, e: EikaEvent): TranscriptState {
  const p = payloadOf(e, "session.message");
  if (p?.session_id !== state.sessionId) return state;
  // Replay runs alongside the live stream, so the same entry can arrive
  // twice. An entry already held is a no-op.
  if (state.entryIds.includes(p.entry_id)) return state;

  // The sealed turns' entries are what is arriving, so their live items go.
  const live =
    state.sealedTurns.length === 0
      ? state.live
      : state.live.filter((item) => !state.sealedTurns.includes(item.runId));
  const sealedTurns = state.sealedTurns.length === 0 ? state.sealedTurns : [];

  const base = {
    ...state,
    live,
    sealedTurns,
    entryIds: [...state.entryIds, p.entry_id],
    lastEntryId: p.entry_id,
    needsReplay: false,
  };

  // A tool result completes the call its assistant entry already introduced
  // rather than adding a row of its own.
  if (p.kind === "tool_result") {
    const message = asMessage(p.message);
    const callId = message?.tool_call_id ?? "";
    const index = findTool(base.committed, callId);
    const item = base.committed[index];
    if (index >= 0 && item?.kind === "tool") {
      const known = base.toolDetails[callId];
      return {
        ...base,
        committed: replaceAt(base.committed, index, {
          ...item,
          content: message?.content ?? "",
          isError: message?.is_error ?? false,
          done: true,
          ...(known === undefined ? {} : { details: known.details, durationMs: known.durationMs }),
        }),
      };
    }
  }
  return { ...base, committed: [...base.committed, ...entryItems(p)] };
}

function applyDropped(state: TranscriptState, e: EikaEvent): TranscriptState {
  const p = payloadOf(e, "bus.dropped");
  if (!p) return state;
  // Events were lost, so what is on screen may be incomplete: ask for a
  // replay from the last entry that is known to be whole.
  return { ...state, dropped: state.dropped + p.dropped, needsReplay: true };
}

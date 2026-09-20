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
import type { EikaEvent, SessionMessage, TurnProgress, Usage } from "@/api/events";
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

/**
 * ReasoningItem is the model thinking: the reasoning it streamed before an
 * answer. It is its own row because it is not the answer, and the session
 * view shows it collapsed unless the reader asks for it.
 */
export type ReasoningItem = {
  kind: "reasoning";
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
export type TranscriptItem =
  UserItem | AssistantItem | ReasoningItem | ToolItem | ErrorItem | NoticeItem;

/**
 * Meter is what the status bar states about the turn: how full the model's
 * context window is and how fast it is decoding. Every number in it was
 * measured by the harness and reported in a turn.progress or turn.end event,
 * so nothing here is an estimate. It is absent until an endpoint reports
 * usage, because an endpoint that never does has nothing true to show.
 */
export type Meter = {
  /** runId is the turn the measurements belong to. */
  runId: string;
  /** context is the last model call's prompt plus its response. */
  context: Usage;
  /** usage is what the turn has cost so far, over every model call. */
  usage: Usage;
  /** generationMs is the time the turn spent inside model responses. */
  generationMs: number;
  /** contextWindow belongs to the model that produced this measurement. */
  contextWindow: number;
  /**
   * tokensPerSecond is the decode rate measured between the two most recent
   * comparable samples. It is absent until enough was measured to divide.
   */
  tokensPerSecond?: number;
  /** live is whether the turn this meter describes is still running. */
  live: boolean;
  /** source keeps a live measurement from being replaced by older replay entries. */
  source: "live" | "replay";
};

/** TranscriptState is everything the session view derives from the stream. */
export type TranscriptState = {
  /** sessionId is the session this transcript belongs to. */
  sessionId: string;
  /** committed holds the stored entries a replay delivered, in path order. */
  committed: TranscriptItem[];
  /** entryIds are the entries already held, which is how a replay dedupes. */
  entryIds: ReadonlySet<string>;
  /** lastEntryId is the newest entry seen; a replay resumes after it. */
  lastEntryId: string;
  /** live holds the turn in flight, which no entry covers yet. */
  live: TranscriptItem[];
  /** sealedTurns are turns that ended and whose live items the next replay replaces. */
  sealedTurns: readonly string[];
  /** activeTurnId is the streaming turn's run_id, empty when nothing streams. */
  activeTurnId: string;
  /** meter is the measured cost and decode rate of the newest turn. */
  meter?: Meter;
  /** stopReason is why the last turn stopped. */
  stopReason?: string;
  /** questions are the `ask_user` calls waiting for an answer. */
  questions: Question[];
  /**
   * toolResults keeps every `tool.result` by call id. It serves two cases at
   * once: a result that arrives before its `tool.call` still reaches the card
   * the call creates, and a stored entry, which holds only the text the model
   * saw, gets back the exit code and the duration a replay would have lost.
   */
  toolResults: Record<string, ToolResultRecord>;
  /** needsReplay is set when the client must ask for a replay to catch up. */
  needsReplay: boolean;
  /** dropped counts the events the connection lost, for the status bar. */
  dropped: number;
};

/** ToolResultRecord is everything a `tool.result` said about one call. */
export type ToolResultRecord = {
  name: string;
  content: string;
  isError: boolean;
  details?: unknown;
  durationMs: number;
};

/** newTranscript returns the empty state for one session. */
export function newTranscript(sessionId: string): TranscriptState {
  return {
    sessionId,
    committed: [],
    entryIds: new Set(),
    lastEntryId: "",
    live: [],
    sealedTurns: [],
    activeTurnId: "",
    questions: [],
    toolResults: {},
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
    case "reasoning.delta":
      return applyReasoning(state, e);
    case "message.reset":
      return applyReset(state, e);
    case "tool.call":
      return applyToolCall(state, e);
    case "tool.output":
      return applyToolOutput(state, e);
    case "tool.result":
      return applyToolResult(state, e);
    case "turn.progress":
      return applyProgress(state, e);
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
  // Prose that begins closes the reasoning that preceded it.
  const live = seal(state.live);
  const key = `${p.run_id}:assistant:${String(live.length)}`;
  return {
    ...state,
    live: [...live, { kind: "assistant", key, runId: p.run_id, text: p.text, streaming: true }],
  };
}

/**
 * applyReasoning accumulates the model's thinking into the turn's current
 * reasoning block, starting a new one when anything else came between.
 */
function applyReasoning(state: TranscriptState, e: EikaEvent): TranscriptState {
  const p = payloadOf(e, "reasoning.delta");
  if (!p) return state;
  const last = state.live.at(-1);
  if (last?.kind === "reasoning" && last.runId === p.run_id && last.streaming) {
    return {
      ...state,
      live: replaceAt(state.live, state.live.length - 1, { ...last, text: last.text + p.text }),
    };
  }
  const live = seal(state.live);
  const key = `${p.run_id}:reasoning:${String(live.length)}`;
  return {
    ...state,
    live: [...live, { kind: "reasoning", key, runId: p.run_id, text: p.text, streaming: true }],
  };
}

/**
 * meterOf folds one measurement into the status bar's meter. A rate needs two
 * comparable samples. The first streamed token can contain an unknown number
 * of tokens, so treating the first usage report as a difference from zero
 * would state a precise but false rate.
 */
function meterOf(previous: Meter | undefined, p: TurnProgress, live: boolean): Meter {
  const base =
    previous?.runId === p.run_id &&
    previous.usage.output_tokens <= p.usage.output_tokens &&
    previous.generationMs <= p.generation_ms
      ? previous
      : undefined;
  const tokens = p.usage.output_tokens - (base?.usage.output_tokens ?? 0);
  const ms = p.generation_ms - (base?.generationMs ?? 0);
  // Nothing new was measured, so the last measured rate still stands.
  const rate =
    base !== undefined && tokens > 0 && ms > 0 ? (tokens * 1000) / ms : base?.tokensPerSecond;
  return {
    runId: p.run_id,
    context: p.context,
    usage: p.usage,
    generationMs: p.generation_ms,
    contextWindow: p.context_window,
    ...(rate === undefined ? {} : { tokensPerSecond: rate }),
    live,
    source: "live",
  };
}

function applyProgress(state: TranscriptState, e: EikaEvent): TranscriptState {
  const p = payloadOf(e, "turn.progress");
  if (!p) return state;
  return { ...state, meter: meterOf(state.meter, p, true) };
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
    (item) =>
      !(
        (item.kind === "assistant" || item.kind === "reasoning") &&
        item.runId === p.run_id &&
        item.streaming
      ),
  );
  const next = live.length === state.live.length ? { ...state } : { ...state, live };
  // A retry starts a new response clock and usage counter. Keeping the failed
  // attempt as a rate baseline would divide values from different attempts.
  if (next.meter?.runId === p.run_id) delete next.meter;
  return next;
}

/**
 * seal closes the streaming block at the tail of a list. Prose and reasoning
 * both stream, and either ends when anything else in the turn begins.
 */
function seal(list: TranscriptItem[]): TranscriptItem[] {
  const last = list.at(-1);
  if ((last?.kind === "assistant" || last?.kind === "reasoning") && last.streaming) {
    return replaceAt(list, list.length - 1, { ...last, streaming: false });
  }
  return list;
}

function applyToolCall(state: TranscriptState, e: EikaEvent): TranscriptState {
  const p = payloadOf(e, "tool.call");
  if (!p) return state;
  const live = seal(state.live);
  if (findTool(live, p.call_id) >= 0) return { ...state, live };
  // The two events race: a tool that finishes before its call event is
  // dispatched would otherwise lose its content and its error flag.
  const result = state.toolResults[p.call_id];
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
        isError: result?.isError ?? false,
        done: result !== undefined,
        ...(result === undefined
          ? {}
          : { content: result.content, details: result.details, durationMs: result.durationMs }),
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
  const toolResults = {
    ...state.toolResults,
    [p.call_id]: {
      name: p.name,
      content: p.content,
      isError: p.is_error,
      details: p.details,
      durationMs: p.duration_ms,
    },
  };
  const index = findTool(state.live, p.call_id);
  const item = state.live[index];
  if (index < 0 || item?.kind !== "tool") return { ...state, questions, toolResults };
  return {
    ...state,
    questions,
    toolResults,
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
    meter: meterOf(state.meter, p, false),
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
  const meter = state.meter?.runId === p.run_id ? { ...state.meter, live: false } : state.meter;
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
    ...(meter === undefined ? {} : { meter }),
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
      if (message?.reasoning) {
        out.push({
          kind: "reasoning",
          key: `${p.entry_id}:reasoning`,
          runId: "",
          entryId: p.entry_id,
          text: message.reasoning,
          streaming: false,
        });
      }
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
  if (state.entryIds.has(p.entry_id)) return state;

  // The sealed turns' entries are what is arriving, so their live items go.
  const live =
    state.sealedTurns.length === 0
      ? state.live
      : state.live.filter((item) => !state.sealedTurns.includes(item.runId));
  const sealedTurns = state.sealedTurns.length === 0 ? state.sealedTurns : [];

  const message = asMessage(p.message);
  const metrics = message?.metrics;
  const mayRestoreMetrics =
    metrics !== undefined &&
    (state.activeTurnId === "" || state.activeTurnId === metrics.run_id) &&
    !(state.meter?.source === "live" && state.meter.runId !== metrics.run_id);
  const storedMeter: Meter | undefined = mayRestoreMetrics
    ? {
        runId: metrics.run_id,
        usage: metrics.usage,
        context: metrics.context,
        generationMs: metrics.generation_ms,
        contextWindow: metrics.context_window,
        live: false,
        source: "replay",
        ...(state.meter?.runId === metrics.run_id && state.meter.tokensPerSecond !== undefined
          ? { tokensPerSecond: state.meter.tokensPerSecond }
          : {}),
      }
    : undefined;

  const base = {
    ...state,
    live,
    sealedTurns,
    entryIds: new Set(state.entryIds).add(p.entry_id),
    lastEntryId: p.entry_id,
    needsReplay: false,
    ...(storedMeter === undefined ? {} : { meter: storedMeter }),
  };

  // A tool result completes the call its assistant entry already introduced
  // rather than adding a row of its own.
  if (p.kind === "tool_result") {
    const callId = message?.tool_call_id ?? "";
    const index = findTool(base.committed, callId);
    const item = base.committed[index];
    if (index >= 0 && item?.kind === "tool") {
      const known = base.toolResults[callId];
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

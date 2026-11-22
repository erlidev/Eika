import { describe, expect, it } from "vitest";

import type { EikaEvent, EventType } from "@/api/events";
import type { Entry } from "@/api/types";
import {
  applyEntries,
  applyEvent,
  applyOutline,
  cutOffText,
  items,
  newTranscript,
} from "@/features/session/transcript";
import type { TranscriptState } from "@/features/session/transcript";

const sessionID = "s1";

it("keeps abandoned entries out when pages are loaded again", () => {
  const time = "2026-01-01T00:00:00Z";
  const entries: Entry[] = [
    {
      id: "a",
      seq: 1,
      kind: "user" as const,
      created_at: time,
      message: { role: "user", content: "Keep" },
    },
    {
      id: "b",
      seq: 2,
      kind: "assistant" as const,
      created_at: time,
      message: { role: "assistant", content: "Leave" },
    },
  ];
  const nodes = [
    { id: "a", kind: "user" as const, preview: "", resumable: true, created_at: time },
    {
      id: "b",
      parent_id: "a",
      kind: "assistant" as const,
      preview: "",
      resumable: true,
      created_at: time,
    },
  ];
  let state = applyEntries(newTranscript(sessionID), entries);
  state = applyOutline(state, { session_id: sessionID, head_entry_id: "a", nodes });
  state = applyEntries(state, entries);
  expect(items(state)).toMatchObject([{ kind: "user", text: "Keep" }]);
  state = applyOutline(state, { session_id: sessionID, head_entry_id: "b", nodes });
  expect(items(state)).toMatchObject([{ text: "Keep" }, { text: "Leave" }]);
});

/** ev builds one envelope; the payloads are the shapes docs/api/events.md lists. */
function ev(type: EventType, payload: unknown, topic = `session:${sessionID}`): EikaEvent {
  return { type, topic, time: "2026-01-01T00:00:00Z", payload };
}

function fold(events: EikaEvent[], from = newTranscript(sessionID)): TranscriptState {
  return events.reduce(applyEvent, from);
}

/** A turn that answers with prose and one bash call, as the docs describe it. */
const turn: EikaEvent[] = [
  ev("turn.start", {
    run_id: "r1",
    session_id: sessionID,
    computer_id: "w1",
    message: "list the files",
  }),
  ev("message.delta", { run_id: "r1", text: "I will " }),
  ev("message.delta", { run_id: "r1", text: "run ls." }),
  ev("tool.call", { run_id: "r1", call_id: "c1", name: "bash", arguments: { command: "ls" } }),
  ev("tool.output", { run_id: "r1", call_id: "c1", text: "AGENTS.md\n" }),
  ev("tool.output", { run_id: "r1", call_id: "c1", text: "web\n" }),
  ev("tool.result", {
    run_id: "r1",
    call_id: "c1",
    name: "bash",
    content: "AGENTS.md\nweb",
    is_error: false,
    details: { exit_code: 0, timed_out: false },
    duration_ms: 12,
  }),
  ev("turn.end", {
    run_id: "r1",
    stop_reason: "stop",
    usage: {
      input_tokens: 10,
      output_tokens: 4,
      total_tokens: 14,
      cached_tokens: null,
      prefilled_tokens: null,
      reasoning_tokens: null,
    },
    context: {
      input_tokens: 10,
      output_tokens: 4,
      total_tokens: 14,
      cached_tokens: null,
      prefilled_tokens: null,
      reasoning_tokens: null,
    },
    generation_ms: 1000,
    context_window: 400_000,
  }),
];

describe("applyEvent", () => {
  it("accumulates deltas into one assistant message", () => {
    const state = fold(turn.slice(0, 3));
    const rendered = items(state);
    expect(rendered).toHaveLength(2);
    expect(rendered[0]).toMatchObject({ kind: "user", text: "list the files" });
    expect(rendered[1]).toMatchObject({
      kind: "assistant",
      text: "I will run ls.",
      streaming: true,
    });
    expect(state.activeTurnId).toBe("r1");
  });

  it("attaches tool output and the result to the call id", () => {
    const state = fold(turn.slice(0, 7));
    const tool = items(state).find((item) => item.kind === "tool");
    expect(tool).toMatchObject({
      kind: "tool",
      callId: "c1",
      name: "bash",
      output: "AGENTS.md\nweb\n",
      content: "AGENTS.md\nweb",
      done: true,
      isError: false,
      durationMs: 12,
    });
  });

  it("starts a new assistant message after a tool call", () => {
    const state = fold([...turn.slice(0, 7), ev("message.delta", { run_id: "r1", text: "Done." })]);
    const prose = items(state).filter((item) => item.kind === "assistant");
    expect(prose.map((item) => item.text)).toEqual(["I will run ls.", "Done."]);
  });

  it("ends the turn with its usage and stops streaming", () => {
    const state = fold(turn);
    expect(state.activeTurnId).toBe("");
    expect(state.meter?.usage).toEqual({
      input_tokens: 10,
      output_tokens: 4,
      total_tokens: 14,
      cached_tokens: null,
      prefilled_tokens: null,
      reasoning_tokens: null,
    });
    expect(state.stopReason).toBe("stop");
    expect(state.needsReplay).toBe(true);
    expect(items(state).every((item) => item.kind !== "assistant" || !item.streaming)).toBe(true);
  });

  it("replaces the sealed turn's live items with the replayed entries", () => {
    const replay = [
      ev("session.message", {
        session_id: sessionID,
        entry_id: "e1",
        kind: "user",
        created_at: "2026-01-01T00:00:00Z",
        message: { role: "user", content: "list the files" },
      }),
      ev("session.message", {
        session_id: sessionID,
        entry_id: "e2",
        parent_id: "e1",
        kind: "assistant",
        created_at: "2026-01-01T00:00:01Z",
        message: {
          role: "assistant",
          content: "I will run ls.",
          tool_calls: [{ id: "c1", name: "bash", arguments: { command: "ls" } }],
        },
      }),
      ev("session.message", {
        session_id: sessionID,
        entry_id: "e3",
        parent_id: "e2",
        kind: "tool_result",
        created_at: "2026-01-01T00:00:02Z",
        message: { role: "tool", content: "AGENTS.md\nweb", tool_call_id: "c1" },
      }),
    ];
    const state = fold([...turn, ...replay]);
    expect(state.live).toHaveLength(0);
    expect(state.needsReplay).toBe(false);
    expect(state.lastEntryId).toBe("e3");
    const rendered = items(state);
    expect(rendered.map((item) => item.kind)).toEqual(["user", "assistant", "tool"]);
    expect(rendered[2]).toMatchObject({ callId: "c1", content: "AGENTS.md\nweb", done: true });
  });

  it("keeps a result that arrived before the call it belongs to", () => {
    // The two events race, so a tool that finishes fast can be reported
    // before its call reaches the client.
    const state = fold([
      ev("turn.start", { run_id: "r1", session_id: sessionID, computer_id: "w1", message: "go" }),
      ev("tool.result", {
        run_id: "r1",
        call_id: "c1",
        name: "bash",
        content: "AGENTS.md\nweb",
        is_error: true,
        details: { exit_code: 2, timed_out: false },
        duration_ms: 12,
      }),
      ev("tool.call", { run_id: "r1", call_id: "c1", name: "bash", arguments: { command: "ls" } }),
    ]);
    const tool = items(state).find((item) => item.kind === "tool");
    expect(tool).toMatchObject({
      callId: "c1",
      content: "AGENTS.md\nweb",
      isError: true,
      details: { exit_code: 2, timed_out: false },
      durationMs: 12,
      done: true,
    });
  });

  it("keeps a tool result's exit code across the replay that commits it", () => {
    const replay = [
      ev("session.message", {
        session_id: sessionID,
        entry_id: "e2",
        kind: "assistant",
        created_at: "2026-01-01T00:00:01Z",
        message: {
          role: "assistant",
          content: "I will run ls.",
          tool_calls: [{ id: "c1", name: "bash", arguments: { command: "ls" } }],
        },
      }),
      ev("session.message", {
        session_id: sessionID,
        entry_id: "e3",
        parent_id: "e2",
        kind: "tool_result",
        created_at: "2026-01-01T00:00:02Z",
        message: { role: "tool", content: "AGENTS.md\nweb", tool_call_id: "c1" },
      }),
    ];
    const state = fold([...turn, ...replay]);
    const tool = items(state).find((item) => item.kind === "tool");
    expect(tool).toMatchObject({
      callId: "c1",
      details: { exit_code: 0, timed_out: false },
      durationMs: 12,
      done: true,
    });
  });

  it("shows the images a message carries, live and replayed", () => {
    const image = { media_type: "image/png", data: "AAAA", width: 1920, height: 1080 };
    const live = fold([
      ev("turn.start", { run_id: "r1", session_id: sessionID, message: "", images: [image] }),
    ]);
    expect(items(live)).toEqual([
      expect.objectContaining({ kind: "user", text: "", images: [image] }),
    ]);
    const replayed = fold([
      ev("session.message", {
        session_id: sessionID,
        entry_id: "e1",
        kind: "user",
        created_at: "2026-01-01T00:00:00Z",
        message: { role: "user", content: "what is this?", images: [image] },
      }),
    ]);
    expect(items(replayed)[0]).toMatchObject({
      kind: "user",
      text: "what is this?",
      images: [image],
    });
  });

  it("adds no row for a turn that starts with nothing new", () => {
    const state = fold([ev("turn.start", { run_id: "r1", session_id: sessionID, message: "" })]);
    expect(items(state)).toHaveLength(0);
  });

  it("adds no row for a stored message that carries only notices", () => {
    const state = fold([
      ev("session.message", {
        session_id: sessionID,
        entry_id: "e1",
        kind: "user",
        created_at: "2026-01-01T00:00:00Z",
        message: {
          role: "user",
          context: "<context>\nnotice: plumber (worker) reported: done\n</context>",
          notices: ["n1"],
        },
      }),
    ]);
    expect(items(state)).toHaveLength(0);
    expect(state.lastEntryId).toBe("e1");
  });

  it("shows a notice delivered by replay", () => {
    const state = fold([
      ev("session.message", {
        session_id: sessionID,
        entry_id: "notice-1",
        kind: "notice",
        created_at: "2026-01-01T00:00:00Z",
        message: { text: "plumber reported" },
      }),
    ]);
    expect(items(state)).toEqual([
      expect.objectContaining({ kind: "notice", text: "plumber reported" }),
    ]);
  });

  it("ignores an entry it already holds", () => {
    const message = ev("session.message", {
      session_id: sessionID,
      entry_id: "e1",
      kind: "user",
      created_at: "2026-01-01T00:00:00Z",
      message: { role: "user", content: "hello" },
    });
    const state = fold([message, message]);
    expect(items(state)).toHaveLength(1);
  });

  it("ignores an entry of another session", () => {
    const state = fold([
      ev(
        "session.message",
        {
          session_id: "other",
          entry_id: "x1",
          kind: "user",
          created_at: "2026-01-01T00:00:00Z",
          message: { role: "user", content: "not mine" },
        },
        "session:other",
      ),
    ]);
    expect(items(state)).toHaveLength(0);
  });

  it("discards the failed attempt's prose on message.reset", () => {
    const state = fold([
      ...turn.slice(0, 3),
      ev("message.reset", { run_id: "r1" }),
      ev("message.delta", { run_id: "r1", text: "Second attempt." }),
    ]);
    const prose = items(state).filter((item) => item.kind === "assistant");
    expect(prose.map((item) => item.text)).toEqual(["Second attempt."]);
    expect(items(state)[0]).toMatchObject({ kind: "user", text: "list the files" });
  });

  it("keeps a tool call the reset attempt already made", () => {
    const state = fold([...turn.slice(0, 7), ev("message.reset", { run_id: "r1" })]);
    expect(items(state).filter((item) => item.kind === "tool")).toHaveLength(1);
  });

  it("records a failed run and stops the turn", () => {
    const state = fold([
      ...turn.slice(0, 3),
      ev("run.error", { run_id: "r1", message: "provider refused", retryable: true }),
    ]);
    expect(state.activeTurnId).toBe("");
    expect(items(state).at(-1)).toMatchObject({
      kind: "error",
      message: "provider refused",
      retryable: true,
    });
  });

  it("holds a question until its call finishes", () => {
    const asked = ev("question.asked", {
      run_id: "r1",
      session_id: sessionID,
      call_id: "c9",
      question_id: "q1",
      question: "Which branch?",
      options: ["main", "dev"],
      allow_free_text: false,
    });
    const withQuestion = fold([
      ev("turn.start", { run_id: "r1", session_id: sessionID, message: "go" }),
      ev("tool.call", { run_id: "r1", call_id: "c9", name: "ask_user", arguments: {} }),
      asked,
      asked,
    ]);
    expect(withQuestion.questions).toHaveLength(1);
    expect(withQuestion.questions[0]).toMatchObject({ id: "q1", options: ["main", "dev"] });

    const answered = applyEvent(
      withQuestion,
      ev("tool.result", {
        run_id: "r1",
        call_id: "c9",
        name: "ask_user",
        content: "main",
        is_error: false,
        duration_ms: 900,
      }),
    );
    expect(answered.questions).toHaveLength(0);
  });

  it("asks for a replay when the connection dropped events", () => {
    const state = fold([ev("bus.dropped", { dropped: 7 }, "global")]);
    expect(state.dropped).toBe(7);
    expect(state.needsReplay).toBe(true);
  });
});

describe("reasoning", () => {
  /** A turn that thinks, answers, and is then stored, as a replay delivers it. */
  const thinking: EikaEvent[] = [
    ev("turn.start", { run_id: "r1", session_id: sessionID, message: "why?" }),
    ev("reasoning.delta", { run_id: "r1", text: "weighing " }),
    ev("reasoning.delta", { run_id: "r1", text: "the options" }),
    ev("message.delta", { run_id: "r1", text: "Because of the cap." }),
  ];

  it("accumulates reasoning into a block of its own", () => {
    const rendered = items(fold(thinking.slice(0, 3)));
    expect(rendered).toHaveLength(2);
    expect(rendered[1]).toMatchObject({
      kind: "reasoning",
      text: "weighing the options",
      streaming: true,
    });
  });

  it("seals the reasoning when the answer starts", () => {
    const rendered = items(fold(thinking));
    expect(rendered.map((i) => i.kind)).toEqual(["user", "reasoning", "assistant"]);
    expect(rendered[1]).toMatchObject({ kind: "reasoning", streaming: false });
    expect(rendered[2]).toMatchObject({ kind: "assistant", text: "Because of the cap." });
  });

  it("starts a new block when the model thinks again after answering", () => {
    const rendered = items(
      fold([...thinking, ev("reasoning.delta", { run_id: "r1", text: "one more thing" })]),
    );
    expect(rendered.map((i) => i.kind)).toEqual(["user", "reasoning", "assistant", "reasoning"]);
  });

  it("discards a failed attempt's reasoning on message.reset", () => {
    const rendered = items(fold([...thinking.slice(0, 3), ev("message.reset", { run_id: "r1" })]));
    expect(rendered.map((i) => i.kind)).toEqual(["user"]);
  });

  it("renders a stored message's reasoning before its answer", () => {
    const rendered = items(
      fold([
        ev("session.message", {
          session_id: sessionID,
          entry_id: "e1",
          kind: "assistant",
          created_at: "2026-01-01T00:00:00Z",
          message: {
            role: "assistant",
            reasoning: "weighing the options",
            content: "Because of the cap.",
          },
        }),
      ]),
    );
    expect(rendered.map((i) => i.kind)).toEqual(["reasoning", "assistant"]);
    expect(rendered[0]).toMatchObject({ text: "weighing the options", streaming: false });
  });
});

describe("a turn the endpoint cut off", () => {
  function ended(reason: string): EikaEvent {
    return ev("turn.end", {
      run_id: "r1",
      stop_reason: reason,
      usage: {
        input_tokens: 100,
        output_tokens: 10,
        total_tokens: 110,
        cached_tokens: null,
        prefilled_tokens: null,
        reasoning_tokens: null,
      },
      context: {
        input_tokens: 100,
        output_tokens: 10,
        total_tokens: 110,
        cached_tokens: null,
        prefilled_tokens: null,
        reasoning_tokens: null,
      },
      generation_ms: 1000,
      context_window: 400_000,
    });
  }

  it("keeps the text that did arrive and records why it stopped", () => {
    const state = fold([...turn.slice(0, 3), ended("length")]);
    expect(state.stopReason).toBe("length");
    expect(cutOffText(state.stopReason)).toContain("output limit");
    const last = [...state.committed, ...state.live].at(-1);
    expect(last).toMatchObject({ kind: "assistant", streaming: false });
  });

  it("says why a run stopped at one of its limits, and that nothing was lost", () => {
    for (const reason of ["max_calls", "max_wall_time"]) {
      const state = fold([...turn.slice(0, 3), ended(reason)]);
      expect(cutOffText(state.stopReason)).toContain("send a message to carry on");
    }
    expect(cutOffText("max_calls")).toContain("model calls");
    expect(cutOffText("max_wall_time")).toContain("time limit");
  });

  it("says nothing for a turn that stopped because the model was finished", () => {
    expect(cutOffText(fold([...turn.slice(0, 3), ended("stop")]).stopReason)).toBeUndefined();
  });

  it("clears the notice when the next turn starts", () => {
    const restart = ev("turn.start", {
      run_id: "r2",
      session_id: sessionID,
      computer_id: "w1",
      message: "carry on",
    });
    const state = fold([...turn.slice(0, 3), ended("length"), restart]);
    expect(cutOffText(state.stopReason)).toBeUndefined();
  });
});

describe("the meter", () => {
  /** progress is one turn.progress event carrying what the endpoint reported. */
  function progress(outputTokens: number, generationMs: number): EikaEvent {
    return ev("turn.progress", {
      run_id: "r1",
      usage: {
        input_tokens: 100,
        output_tokens: outputTokens,
        total_tokens: 100 + outputTokens,
        cached_tokens: null,
        prefilled_tokens: null,
        reasoning_tokens: null,
      },
      context: {
        input_tokens: 100,
        output_tokens: outputTokens,
        total_tokens: 100 + outputTokens,
        cached_tokens: null,
        prefilled_tokens: null,
        reasoning_tokens: null,
      },
      generation_ms: generationMs,
      context_window: 400_000,
    });
  }

  it("takes the newest sample as it came", () => {
    const state = fold([progress(10, 1000), progress(40, 2000)]);
    expect(state.meter?.live).toBe(true);
    expect(state.meter?.context.total_tokens).toBe(140);
    expect(state.meter?.generationMs).toBe(2000);
  });

  it("does not replace a live measurement with an older replay entry", () => {
    const live = fold([progress(10, 1000), progress(40, 2000)]);
    const state = applyEvent(
      live,
      ev("session.message", {
        session_id: sessionID,
        entry_id: "e-old-metrics",
        kind: "assistant",
        created_at: "2026-01-01T00:00:00Z",
        message: {
          role: "assistant",
          content: "older",
          metrics: {
            run_id: "r0",
            usage: {
              input_tokens: 20,
              output_tokens: 2,
              total_tokens: 22,
              cached_tokens: null,
              prefilled_tokens: null,
              reasoning_tokens: null,
            },
            context: {
              input_tokens: 20,
              output_tokens: 2,
              total_tokens: 22,
              cached_tokens: null,
              prefilled_tokens: null,
              reasoning_tokens: null,
            },
            generation_ms: 500,
            context_window: 8192,
          },
        },
      }),
    );
    expect(state.meter?.runId).toBe("r1");
  });
});

describe("compaction", () => {
  const usage = {
    input_tokens: 600,
    output_tokens: 400,
    total_tokens: 1000,
    cached_tokens: null,
    prefilled_tokens: null,
    reasoning_tokens: null,
  };
  const start = (runId: string, reason = "manual") =>
    ev("compaction.start", { run_id: runId, reason, tokens_before: 48_000 });
  const end = (runId: string, extra: Record<string, unknown> = {}) =>
    ev("compaction.end", {
      run_id: runId,
      reason: "manual",
      tokens_before: 48_000,
      tokens_after: 6_000,
      summary: "## Goal\nShip it.",
      kept: 2,
      usage,
      ...extra,
    });
  const entry = (id: string, summary: string) =>
    ev("session.message", {
      session_id: sessionID,
      entry_id: id,
      kind: "compaction",
      created_at: "2026-01-01T00:00:00Z",
      message: {
        summary,
        kept: 2,
        tokens_before: 48_000,
        tokens_after: 6_000,
        reason: "manual",
        usage,
      },
    });

  it("shows a compaction in progress, then its summary", () => {
    const running = fold([start("k1")]);
    expect(items(running).at(-1)).toMatchObject({
      kind: "compaction",
      running: true,
      tokensBefore: 48_000,
    });
    const done = fold([end("k1")], running);
    expect(items(done).at(-1)).toMatchObject({
      kind: "compaction",
      running: false,
      tokensAfter: 6_000,
      summary: "## Goal\nShip it.",
    });
  });

  it("asks for the stored entry of a compaction that is a run of its own", () => {
    const state = fold([start("k1"), end("k1")]);
    expect(state.needsReplay).toBe(true);
    const replayed = fold([entry("e9", "## Goal\nShip it.")], state);
    expect(replayed.live).toHaveLength(0);
    expect(items(replayed)).toHaveLength(1);
    expect(items(replayed)[0]).toMatchObject({
      kind: "compaction",
      entryId: "e9",
      summary: "## Goal\nShip it.",
      running: false,
    });
  });

  it("leaves a compaction inside a turn to the turn's own replay", () => {
    const state = fold([...turn.slice(0, 1), start("r1", "threshold"), end("r1")]);
    expect(state.needsReplay).toBe(false);
    expect(state.sealedTurns).toEqual([]);
    expect(items(state).map((item) => item.kind)).toEqual(["user", "compaction"]);
  });

  it("keeps a failed compaction on screen and asks for nothing", () => {
    const state = fold([start("k1"), end("k1", { error: "the endpoint is down", summary: "" })]);
    expect(state.needsReplay).toBe(false);
    expect(items(state).at(-1)).toMatchObject({
      kind: "compaction",
      running: false,
      error: "the endpoint is down",
    });
  });
});

describe("paginated transcript", () => {
  it("merges pages in write order and attaches a result when its older call loads", () => {
    const call = {
      id: "call",
      seq: 1,
      kind: "assistant" as const,
      created_at: "2026-01-01T00:00:01Z",
      message: {
        role: "assistant" as const,
        tool_calls: [{ id: "c", name: "bash", arguments: {} }],
      },
    };
    const result = {
      id: "result",
      seq: 2,
      kind: "tool_result" as const,
      created_at: "2026-01-01T00:00:02Z",
      message: { role: "tool" as const, tool_call_id: "c", content: "done" },
    };
    const newer = {
      id: "new",
      seq: 3,
      kind: "assistant" as const,
      created_at: "2026-01-01T00:00:03Z",
      message: { role: "assistant" as const, content: "answer" },
    };
    let state = applyEntries(newTranscript(sessionID), [result, newer]);
    expect(state.committed).toHaveLength(1);
    const answer = state.committed[0];
    state = applyEntries(state, [call, result]);
    expect(state.committed[1]).toBe(answer);
    expect(state.entries.map((e) => e.id)).toEqual(["call", "result", "new"]);
    expect(state.committed).toMatchObject([
      { kind: "tool", content: "done", done: true },
      { kind: "assistant", text: "answer" },
    ]);
  });

  it("keeps a live turn while pages overlap it and replaces it on catch-up", () => {
    const user = {
      id: "user",
      run_id: "r1",
      seq: 1,
      kind: "user" as const,
      created_at: "2026-01-01T00:00:01Z",
      message: { role: "user" as const, content: "hello" },
    };
    const answer = {
      id: "answer",
      run_id: "r1",
      seq: 2,
      kind: "assistant" as const,
      created_at: "2026-01-01T00:00:02Z",
      message: { role: "assistant" as const, content: "hi" },
    };
    let state = fold([
      ev("turn.start", { run_id: "r1", session_id: sessionID, message: "hello" }),
      ev("message.delta", { run_id: "r1", text: "hi" }),
    ]);
    state = applyEntries(state, [user, answer]);
    expect(items(state).map((item) => item.kind)).toEqual(["user", "assistant"]);
    state = fold(
      [
        ev("turn.end", {
          run_id: "r1",
          stop_reason: "stop",
          usage: {},
          context: {},
          generation_ms: 0,
          context_window: 0,
        }),
      ],
      state,
    );
    state = applyEntries(state, [user, answer], ["r1"]);
    expect(state.entries).toHaveLength(2);
    expect(items(state)).toMatchObject([
      { kind: "user", text: "hello" },
      { kind: "assistant", text: "hi" },
    ]);
  });
  it.each(["page first", "stream first"])("merges equal-time %s by turn ID", (order) => {
    const saved = {
      id: "current",
      run_id: "r2",
      seq: 1,
      kind: "user" as const,
      created_at: "2026-01-01T00:00:00Z",
      message: { role: "user" as const, content: "new" },
    };
    const start = ev("turn.start", { run_id: "r2", session_id: sessionID, message: "new" });
    let state = newTranscript(sessionID);
    if (order === "page first") {
      state = applyEntries(state, [saved]);
      state = applyEvent(state, start);
    } else {
      state = applyEvent(state, start);
      state = applyEntries(state, [saved]);
    }
    expect(items(state)).toMatchObject([{ kind: "user", text: "new" }]);
  });
});

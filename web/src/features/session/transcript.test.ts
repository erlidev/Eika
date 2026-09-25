import { describe, expect, it } from "vitest";

import type { EikaEvent, EventType } from "@/api/events";
import { applyEvent, cutOffText, items, newTranscript } from "@/features/session/transcript";
import type { TranscriptState } from "@/features/session/transcript";

const sessionID = "s1";

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
    workspace_id: "w1",
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
    usage: { input_tokens: 10, output_tokens: 4, total_tokens: 14 },
    context: { input_tokens: 10, output_tokens: 4, total_tokens: 14 },
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
    expect(state.meter?.usage).toEqual({ input_tokens: 10, output_tokens: 4, total_tokens: 14 });
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
      ev("turn.start", { run_id: "r1", session_id: sessionID, workspace_id: "w1", message: "go" }),
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

  it("ignores an event whose payload is not an object", () => {
    const before = fold(turn.slice(0, 2));
    expect(applyEvent(before, ev("message.delta", "nonsense"))).toBe(before);
  });

  it("ignores an event type it has no rule for", () => {
    const before = newTranscript(sessionID);
    expect(applyEvent(before, ev("subagent.started", { run_id: "r1" }))).toBe(before);
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
      usage: { input_tokens: 100, output_tokens: 10, total_tokens: 110 },
      context: { input_tokens: 100, output_tokens: 10, total_tokens: 110 },
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

  it("says nothing for a turn that stopped because the model was finished", () => {
    expect(cutOffText(fold([...turn.slice(0, 3), ended("stop")]).stopReason)).toBeUndefined();
  });

  it("clears the notice when the next turn starts", () => {
    const restart = ev("turn.start", {
      run_id: "r2",
      session_id: sessionID,
      workspace_id: "w1",
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
      usage: { input_tokens: 100, output_tokens: outputTokens, total_tokens: 100 + outputTokens },
      context: { input_tokens: 100, output_tokens: outputTokens, total_tokens: 100 + outputTokens },
      generation_ms: generationMs,
      context_window: 400_000,
    });
  }

  it("is absent until an endpoint reports usage", () => {
    expect(fold(turn.slice(0, 3)).meter).toBeUndefined();
  });

  it("takes the newest sample as it came", () => {
    const state = fold([progress(10, 1000), progress(40, 2000)]);
    expect(state.meter?.live).toBe(true);
    expect(state.meter?.context.total_tokens).toBe(140);
    expect(state.meter?.generationMs).toBe(2000);
  });

  it("drops the meter after a retry, whose measurements describe nothing on screen", () => {
    const state = fold([progress(40, 2000), ev("message.reset", { run_id: "r1" })]);
    expect(state.meter).toBeUndefined();
  });

  it("stops being live when the turn ends", () => {
    const ended = ev("turn.end", {
      run_id: "r1",
      stop_reason: "stop",
      usage: { input_tokens: 100, output_tokens: 10, total_tokens: 110 },
      context: { input_tokens: 100, output_tokens: 10, total_tokens: 110 },
      generation_ms: 1000,
      context_window: 400_000,
    });
    const state = fold([...turn.slice(0, 3), progress(10, 1000), ended]);
    expect(state.meter?.live).toBe(false);
    expect(state.meter?.generationMs).toBe(1000);
    expect(state.meter?.contextWindow).toBe(400_000);
  });

  it("takes the final totals when the turn ends", () => {
    const ended = ev("turn.end", {
      run_id: "r1",
      stop_reason: "stop",
      usage: { input_tokens: 100, output_tokens: 100, total_tokens: 200 },
      context: { input_tokens: 100, output_tokens: 100, total_tokens: 200 },
      generation_ms: 4000,
      context_window: 400_000,
    });
    const state = fold([progress(10, 1000), progress(40, 2000), ended]);
    expect(state.meter?.usage.total_tokens).toBe(200);
    expect(state.meter?.live).toBe(false);
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
            usage: { input_tokens: 20, output_tokens: 2, total_tokens: 22 },
            context: { input_tokens: 20, output_tokens: 2, total_tokens: 22 },
            generation_ms: 500,
            context_window: 8192,
          },
        },
      }),
    );
    expect(state.meter?.runId).toBe("r1");
  });

  it("restores context usage from a stored assistant entry", () => {
    const state = fold([
      ev("session.message", {
        session_id: sessionID,
        entry_id: "e-metrics",
        kind: "assistant",
        created_at: "2026-01-01T00:00:00Z",
        message: {
          role: "assistant",
          content: "done",
          metrics: {
            run_id: "r-stored",
            usage: { input_tokens: 90, output_tokens: 10, total_tokens: 100 },
            context: { input_tokens: 90, output_tokens: 10, total_tokens: 100 },
            generation_ms: 1200,
            context_window: 8192,
          },
        },
      }),
    ]);
    expect(state.meter).toMatchObject({
      runId: "r-stored",
      contextWindow: 8192,
      context: { total_tokens: 100 },
      live: false,
    });
  });
});

describe("generation speed", () => {
  const timings = {
    prompt_tokens: 1200,
    prompt_ms: 300,
    decode_tokens: 256,
    decode_ms: 3200,
    source: "endpoint" as const,
  };

  /** timed is one turn.progress carrying what measured the call's speed. */
  function timed(carried: unknown, run = "r1"): EikaEvent {
    return ev("turn.progress", {
      run_id: run,
      usage: { input_tokens: 1200, output_tokens: 256, total_tokens: 1456 },
      context: { input_tokens: 1200, output_tokens: 256, total_tokens: 1456 },
      generation_ms: 3200,
      context_window: 400_000,
      timings: carried,
    });
  }

  it("states the speed under the prose the model is writing", () => {
    const state = fold([...turn.slice(0, 3), timed(timings)]);
    const rendered = items(state);
    expect(rendered.at(-1)).toMatchObject({ kind: "assistant", timings });
    expect(state.meter?.timings).toEqual(timings);
  });

  it("leaves a message alone when the call measured nothing", () => {
    const state = fold([...turn.slice(0, 3), timed(undefined)]);
    expect(rendered(state)).not.toHaveProperty("timings");
  });

  it("does not stamp a later call's speed onto prose a tool already closed", () => {
    // turn.slice(0, 4) ends on the tool call, which seals the prose above it.
    const state = fold([...turn.slice(0, 4), timed(timings)]);
    const prose = items(state).find((item) => item.kind === "assistant");
    expect(prose).not.toHaveProperty("timings");
  });

  it("restores the speed of a stored answer on replay", () => {
    const state = fold([
      ev("session.message", {
        session_id: sessionID,
        entry_id: "e-timed",
        kind: "assistant",
        created_at: "2026-01-01T00:00:00Z",
        message: {
          role: "assistant",
          content: "done",
          metrics: {
            run_id: "r-stored",
            usage: { input_tokens: 1200, output_tokens: 256, total_tokens: 1456 },
            context: { input_tokens: 1200, output_tokens: 256, total_tokens: 1456 },
            generation_ms: 3200,
            context_window: 400_000,
            timings,
          },
        },
      }),
    ]);
    expect(items(state).at(-1)).toMatchObject({ kind: "assistant", timings });
    expect(state.meter?.timings).toEqual(timings);
  });

  /** rendered is the last item of a folded state. */
  function rendered(state: TranscriptState) {
    const last = items(state).at(-1);
    expect(last?.kind).toBe("assistant");
    return last as object;
  }
});

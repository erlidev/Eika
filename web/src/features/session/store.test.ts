/**
 * The store is a thin shell over the reducer, so the one thing worth testing
 * here is the thing the reducer cannot see: opening another session resets
 * the state. Zustand merges what `set` is given, so a field the reset leaves
 * out keeps the previous session's value and the status bar goes on
 * describing a session that is no longer on screen.
 */

import { beforeEach, describe, expect, it } from "vitest";

import type { EikaEvent } from "@/api/events";
import { useSessionStore } from "@/features/session/store";
import { newTranscript } from "@/features/session/transcript";

/** ended is the turn.end of a turn that measured something and stopped short. */
const ended: EikaEvent = {
  type: "turn.end",
  topic: "session:s1",
  time: "2026-01-01T00:00:00Z",
  payload: {
    run_id: "r1",
    stop_reason: "length",
    usage: { input_tokens: 100, output_tokens: 10, total_tokens: 110 },
    context: { input_tokens: 100, output_tokens: 10, total_tokens: 110 },
    generation_ms: 1000,
    context_window: 400_000,
  },
};

describe("opening a session", () => {
  beforeEach(() => {
    useSessionStore.setState({ ...newTranscript(""), model: "", draft: "" });
  });

  it("leaves nothing of the session that was open", () => {
    const store = useSessionStore.getState();
    store.open("s1");
    store.apply(ended);
    store.chooseModel("gpt-5");
    expect(useSessionStore.getState().meter?.runId).toBe("r1");

    store.open("s2");

    // Every field the previous session set has to be gone, not just the ones
    // the empty state happens to name.
    const after = useSessionStore.getState();
    expect(after.sessionId).toBe("s2");
    expect(after.meter).toBeUndefined();
    expect(after.stopReason).toBeUndefined();
    expect(after.model).toBe("");
    expect(after.draft).toBe("");
  });

  it("keeps the transcript when the session that is open opens again", () => {
    const store = useSessionStore.getState();
    store.open("s1");
    store.apply(ended);
    store.open("s1");
    expect(useSessionStore.getState().meter?.runId).toBe("r1");
  });
});

describe("rewinding", () => {
  beforeEach(() => {
    useSessionStore.setState({ ...newTranscript(""), model: "", draft: "" });
  });

  it("drops the transcript and asks for the path again", () => {
    const store = useSessionStore.getState();
    store.open("s1");
    store.apply(ended);
    store.chooseModel("gpt-5");
    store.edit("the message being rewound");

    useSessionStore.getState().rewound();

    // The entries the head no longer reaches must go, and a replay from the
    // start is what puts the branch that is current back on screen.
    const after = useSessionStore.getState();
    expect(after.sessionId).toBe("s1");
    expect(after.committed).toEqual([]);
    expect(after.lastEntryId).toBe("");
    expect(after.meter).toBeUndefined();
    expect(after.needsReplay).toBe(true);
    // What the user is about to send, and the model they chose, are not the
    // transcript and stay.
    expect(after.draft).toBe("the message being rewound");
    expect(after.model).toBe("gpt-5");
  });
});

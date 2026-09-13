import { describe, expect, it } from "vitest";

import {
  globalTopic,
  parseEvent,
  sessionTopic,
  workspaceTopic,
  type BusDropped,
  type SessionMessage,
  type ToolResult,
} from "@/api/events";

// A tool.result event exactly as the harness encodes it.
const sample = {
  type: "tool.result",
  topic: "session:s1",
  time: "2026-01-02T03:04:05Z",
  payload: {
    run_id: "run-1",
    call_id: "call-1",
    name: "bash",
    content: "ok",
    is_error: false,
    details: { exit_code: 0, timed_out: false },
    duration_ms: 12,
  },
};

describe("parseEvent", () => {
  it("accepts a harness event", () => {
    const event = parseEvent(JSON.parse(JSON.stringify(sample)));
    expect(event.type).toBe("tool.result");
    expect(event.topic).toBe("session:s1");
    const payload = event.payload as ToolResult;
    expect(payload.name).toBe("bash");
    expect(payload.is_error).toBe(false);
    expect(payload.duration_ms).toBe(12);
  });

  const rejected: [string, unknown][] = [
    ["null", null],
    ["a string", "turn.start"],
    ["an unknown type", { type: "nope", topic: "global", time: "now" }],
    ["a missing topic", { type: "turn.end", time: "now" }],
  ];
  for (const [name, body] of rejected) {
    it(`rejects ${name}`, () => {
      expect(() => parseEvent(body)).toThrow();
    });
  }

  it("accepts a replayed session message", () => {
    const event = parseEvent({
      type: "session.message",
      topic: "session:s1",
      time: "2026-01-02T03:04:05Z",
      payload: {
        session_id: "s1",
        entry_id: "e2",
        parent_id: "e1",
        kind: "assistant",
        commit: "abc123",
        created_at: "2026-01-02T03:04:05Z",
        message: { role: "assistant", content: "hi" },
      },
    });
    const payload = event.payload as SessionMessage;
    expect(payload.entry_id).toBe("e2");
    expect(payload.kind).toBe("assistant");
  });

  it("accepts a drop report", () => {
    const event = parseEvent({
      type: "bus.dropped",
      topic: "global",
      time: "2026-01-02T03:04:05Z",
      payload: { dropped: 12 },
    });
    expect((event.payload as BusDropped).dropped).toBe(12);
  });
});

describe("topics", () => {
  it("names the topics the harness routes on", () => {
    expect(globalTopic).toBe("global");
    expect(workspaceTopic("w1")).toBe("workspace:w1");
    expect(sessionTopic("s1")).toBe("session:s1");
  });
});

import { describe, expect, it } from "vitest";

import { parseEvent, type ToolResult } from "@/api/events";

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
});

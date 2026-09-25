import { describe, expect, it } from "vitest";

import type { ModelContext } from "@/api/types";
import { estimatedTotal, scale, segments, share } from "@/features/context/segments";

function request(over: Partial<ModelContext> = {}): ModelContext {
  return {
    sections: [
      { kind: "base", text: "You are Eika.", tokens: 4 },
      { kind: "instructions", text: "Be brief.", tokens: 3 },
    ],
    tools: [
      { name: "bash", description: "", schema: {}, source: "builtin", tokens: 40 },
      { name: "mcp__docs__search", description: "", schema: {}, source: "mcp", tokens: 25 },
      { name: "mcp__docs__read", description: "", schema: {}, source: "mcp", tokens: 15 },
    ],
    messages: [{ role: "user", content: "hi" }],
    message_tokens: 10,
    parameters: { model: "m", sampling: {}, preserve_thinking: false },
    sources: {},
    ...over,
  };
}

describe("segments", () => {
  it("splits a request into its parts in the bar's order, tools by where they come from", () => {
    expect(segments(request())).toEqual([
      { kind: "base", label: "Base prompt", tokens: 4 },
      { kind: "instructions", label: "Instructions", tokens: 3 },
      { kind: "builtin_tools", label: "Built-in tools", tokens: 40 },
      { kind: "mcp_tools", label: "MCP tools", tokens: 40 },
      { kind: "messages", label: "Messages", tokens: 10 },
    ]);
  });

  it("leaves out what a request does not have", () => {
    const empty = request({ sections: null, tools: null, messages: null, message_tokens: 0 });
    expect(segments(empty)).toEqual([]);
    expect(estimatedTotal(segments(request()))).toBe(97);
  });
});

describe("scale", () => {
  it("is the measured input over the estimate of the same call", () => {
    const measured = request({
      calibration: { request_id: "r1", input_tokens: 150, estimated_tokens: 100 },
    });
    expect(scale(measured)).toBe(1.5);
  });

  it("is undefined without a measured call", () => {
    expect(scale(request())).toBeUndefined();
    const unmeasured = request({
      calibration: { request_id: "r1", input_tokens: 0, estimated_tokens: 100 },
    });
    expect(scale(unmeasured)).toBeUndefined();
  });
});

describe("share", () => {
  it("is a percentage of the whole, and nothing of nothing", () => {
    expect(share(25, 100)).toBe(25);
    expect(share(5, 0)).toBe(0);
  });
});

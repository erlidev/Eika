import { describe, expect, it } from "vitest";

import type { Breakdown, ModelCall, ModelContext } from "@/api/types";
import {
  barTotal,
  editTargetOf,
  exactDrift,
  messageSizes,
  parameterTarget,
  requestJSON,
  requestSize,
  schemaParameters,
  segments,
  serverOf,
  share,
  sizedSections,
  sizedTools,
  toolGroupsOf,
  windowUse,
} from "@/features/context/segments";

/**
 * breakdown is the counted size of request(): its estimates (4, 3, 40, 25,
 * 15 and 10 tokens) are not what the endpoint counted, and nothing shown
 * comes from them. The parts add up to 82 and the endpoint measured 100.
 */
function breakdown(over: Partial<Breakdown> = {}): Breakdown {
  return {
    sections: [
      { kind: "base", tokens: 6 },
      { kind: "instructions", tokens: 2 },
    ],
    tools: [
      { name: "bash", tokens: 30 },
      { name: "mcp__docs__search", tokens: 20 },
      { name: "mcp__docs__read", tokens: 10 },
    ],
    messages: [{ tokens: 9, reasoning_tokens: 5 }],
    parts: 82,
    prompt_tokens: 100,
    prompt_source: "measured",
    remainder: 18,
    ...over,
  };
}

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
    message_sizes: [10],
    parameters: { model: "m", sampling: {}, preserve_thinking: false, image_input: false },
    sources: {},
    context_window: 0,
    breakdown: breakdown(),
    ...over,
  };
}

function call(over: Partial<ModelCall> = {}): ModelCall {
  return {
    id: "c1",
    session_id: "s",
    run_id: "r",
    model: "m",
    message_tokens: 10,
    prompt_tokens: 100,
    completion_tokens: 20,
    total_tokens: 120,
    cached_tokens: null,
    prefilled_tokens: null,
    reasoning_tokens: null,
    predicted_prompt_tokens: 100,
    prediction: "exact",
    drift: 0,
    context_window: 1000,
    queue_ms: null,
    slot: null,
    created_at: "2026-10-31T00:00:00Z",
    ...over,
  };
}

describe("segments", () => {
  it("splits a request into its counted parts in the bar's order, then what the template adds", () => {
    expect(segments(request())).toEqual([
      { kind: "base", label: "Base prompt", tokens: 6, approximate: false },
      { kind: "instructions", label: "Instructions", tokens: 2, approximate: false },
      { kind: "builtin_tools", label: "Built-in tools", tokens: 30, approximate: false },
      { kind: "mcp_tools", label: "MCP tools", tokens: 30, approximate: false },
      { kind: "messages", label: "Messages", tokens: 9, approximate: false },
      { kind: "reasoning", label: "Replayed reasoning", tokens: 5, approximate: false },
      { kind: "template", label: "Template and formatting", tokens: 18, approximate: false },
    ]);
  });

  it("marks a part approximate when any size in it is", () => {
    const parts = segments(
      request({
        breakdown: breakdown({
          tools: [
            { name: "bash", tokens: 30, approximate: true },
            { name: "mcp__docs__search", tokens: 20 },
            { name: "mcp__docs__read", tokens: 10 },
          ],
          messages: [{ tokens: 9, reasoning_tokens: 5, approximate: true }],
        }),
      }),
    );
    const approximate = Object.fromEntries(parts.map((p) => [p.kind, p.approximate]));
    expect(approximate).toMatchObject({
      base: false,
      builtin_tools: true,
      mcp_tools: false,
      messages: true,
      reasoning: true,
    });
  });

  it("leaves out what a request does not have, and the template line while nothing states it", () => {
    const empty = request({
      sections: null,
      tools: null,
      messages: null,
      breakdown: breakdown({
        sections: [],
        tools: [],
        messages: [],
        parts: 0,
        prompt_tokens: null,
        remainder: null,
      }),
    });
    expect(segments(empty)).toEqual([]);
  });

  it("keeps a negative remainder visible and gives it no width in the bar", () => {
    const parts = segments(request({ breakdown: breakdown({ prompt_tokens: 80, remainder: -2 }) }));
    expect(parts.at(-1)).toEqual({
      kind: "template",
      label: "Template and formatting",
      tokens: -2,
      approximate: false,
    });
    expect(barTotal(parts)).toBe(82);
  });

  it("is the whole request in the bar: the parts and the remainder", () => {
    expect(barTotal(segments(request()))).toBe(100);
  });
});

describe("counted sizes", () => {
  it("replace the estimates on sections, tools and messages", () => {
    expect(sizedSections(request()).map((s) => s.tokens)).toEqual([6, 2]);
    expect(sizedTools(request()).map((t) => [t.name, t.tokens, t.approximate])).toEqual([
      ["bash", 30, false],
      ["mcp__docs__search", 20, false],
      ["mcp__docs__read", 10, false],
    ]);
    expect(messageSizes(request())).toEqual([{ tokens: 9, reasoning_tokens: 5 }]);
  });
});

describe("requestSize", () => {
  it("is what the endpoint measured, when it did", () => {
    expect(requestSize(request())).toEqual({ tokens: 100, basis: "measured" });
  });

  it("is the next request's prediction and how it was reached", () => {
    const next = request({
      breakdown: breakdown({ prompt_tokens: 97, prompt_source: "predicted", remainder: 15 }),
      prediction: { tokens: 97, method: "exact" },
    });
    expect(requestSize(next)).toEqual({ tokens: 97, basis: "predicted", method: "exact" });
  });

  it("is a recorded call's own prediction when the endpoint stated no prompt", () => {
    const unmeasured = request({
      breakdown: breakdown({ prompt_tokens: null, remainder: null }),
      call: call({ prompt_tokens: null, predicted_prompt_tokens: 95, prediction: "anchored" }),
    });
    delete unmeasured.breakdown.prompt_source;
    expect(requestSize(unmeasured)).toEqual({ tokens: 95, basis: "predicted", method: "anchored" });
  });

  it("is only the parts when nothing was measured or predicted", () => {
    const bare = request({ breakdown: breakdown({ prompt_tokens: null, remainder: null }) });
    delete bare.breakdown.prompt_source;
    expect(requestSize(bare)).toEqual({ tokens: 82, basis: "parts" });
  });
});

describe("exactDrift", () => {
  it("is the drift of an exact prediction that was off", () => {
    expect(exactDrift(request({ call: call({ drift: 3 }) }))).toBe(3);
    expect(exactDrift(request({ call: call({ drift: -1 }) }))).toBe(-1);
  });

  it("is nothing for an exact prediction that was right, or a guess that was off", () => {
    expect(exactDrift(request({ call: call({ drift: 0 }) }))).toBeUndefined();
    expect(exactDrift(request({ call: call({ drift: null }) }))).toBeUndefined();
    expect(
      exactDrift(request({ call: call({ prediction: "anchored", drift: 4 }) })),
    ).toBeUndefined();
    expect(exactDrift(request())).toBeUndefined();
  });
});

describe("share", () => {
  it("is a percentage of the whole, and nothing of nothing", () => {
    expect(share(25, 100)).toBe(25);
    expect(share(5, 0)).toBe(0);
  });
});

describe("toolGroupsOf", () => {
  it("groups built-in tools, each server's by name, then the resource tools", () => {
    const tool = (name: string, source: "builtin" | "mcp", tokens: number) => ({
      name,
      description: "",
      schema: {},
      source,
      tokens,
      approximate: false,
    });
    const groups = toolGroupsOf([
      tool("bash", "builtin", 40),
      tool("mcp__linear__list", "mcp", 5),
      tool("mcp__docs__search", "mcp", 25),
      tool("mcp__docs__read", "mcp", 15),
      tool("mcp_read_resource", "mcp", 3),
    ]);
    expect(groups.map((g) => [g.id, g.tools.map((t) => t.name), g.tokens])).toEqual([
      ["builtin", ["bash"], 40],
      ["mcp:docs", ["mcp__docs__search", "mcp__docs__read"], 40],
      ["mcp:linear", ["mcp__linear__list"], 5],
      ["resources", ["mcp_read_resource"], 3],
    ]);
  });

  it("reads the server from a tool's name", () => {
    expect(serverOf("mcp__my-server__do_it")).toBe("my-server");
    expect(serverOf("mcp_list_resources")).toBeUndefined();
    expect(serverOf("bash")).toBeUndefined();
  });
});

describe("schemaParameters", () => {
  it("lists the parameters, required first, with readable types", () => {
    const params = schemaParameters({
      type: "object",
      properties: {
        timeout: { type: "integer", description: "Seconds." },
        ids: { type: "array", items: { type: "string" } },
        format: { type: "string", enum: ["markdown", "raw"] },
        command: { type: "string", description: "The command." },
        either: { type: ["string", "null"] },
      },
      required: ["command"],
    });
    expect(params).toEqual([
      { name: "command", type: "string", required: true, description: "The command." },
      { name: "timeout", type: "integer", required: false, description: "Seconds." },
      { name: "ids", type: "string[]", required: false, description: "" },
      {
        name: "format",
        type: "string",
        required: false,
        description: "",
        values: ["markdown", "raw"],
      },
      { name: "either", type: "string | null", required: false, description: "" },
    ]);
  });

  it("finds none in a schema without properties", () => {
    expect(schemaParameters({ type: "object" })).toEqual([]);
    expect(schemaParameters("nonsense")).toEqual([]);
  });
});

describe("windowUse", () => {
  it("splits a window into the request, the answer's room, and what is free", () => {
    expect(windowUse(1000, 500, 4000)).toEqual({
      used: 1000,
      reserved: 500,
      free: 2500,
      window: 4000,
      overflows: false,
    });
  });

  it("says when the request and its answer do not fit", () => {
    expect(windowUse(3000, 2000, 4000)).toMatchObject({ free: 0, overflows: true });
  });

  it("fills exactly what is used when the window is not known", () => {
    expect(windowUse(700, 500, 0)).toEqual({
      used: 700,
      reserved: 0,
      free: 0,
      window: 700,
      overflows: false,
    });
  });
});

describe("requestJSON", () => {
  it("joins the system prompt and lays the parameters beside the content", () => {
    const out = requestJSON(
      request({
        parameters: {
          model: "gpt-5",
          sampling: { temperature: 0.2 },
          thinking_switch: "reasoning_effort",
          preserve_thinking: true,
          image_input: false,
        },
      }),
    );
    expect(out).toMatchObject({
      model: "gpt-5",
      system: "You are Eika.\n\nBe brief.",
      temperature: 0.2,
      thinking_switch: "reasoning_effort",
      preserve_thinking: true,
      image_input: false,
      messages: [{ role: "user", content: "hi" }],
    });
    expect(out.tools).toEqual([
      { name: "bash", description: "", parameters: {} },
      { name: "mcp__docs__search", description: "", parameters: {} },
      { name: "mcp__docs__read", description: "", parameters: {} },
    ]);
  });
});

describe("edit targets", () => {
  it("names the setting behind each part and parameter", () => {
    expect(editTargetOf("base")).toEqual({ key: "chat_prompt", section: "prompt" });
    expect(editTargetOf("mcp_tools")).toEqual({ key: "tools", section: "tools" });
    expect(editTargetOf("messages")).toBeUndefined();
    expect(parameterTarget("sampling.temperature")?.section).toBe("sampling");
    expect(parameterTarget("sampling.max_output")?.section).toBe("model");
    expect(parameterTarget("preserve_thinking")?.section).toBe("model");
    expect(parameterTarget("thinking_switch")).toBeUndefined();
  });
});

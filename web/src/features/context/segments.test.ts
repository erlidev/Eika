import { describe, expect, it } from "vitest";

import type { ModelContext } from "@/api/types";
import {
  editTargetOf,
  estimatedTotal,
  parameterTarget,
  requestJSON,
  scale,
  schemaParameters,
  segments,
  serverOf,
  share,
  toolGroupsOf,
  windowUse,
} from "@/features/context/segments";

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
    parameters: { model: "m", sampling: {}, preserve_thinking: false },
    sources: {},
    context_window: 0,
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

describe("toolGroupsOf", () => {
  it("groups built-in tools, each server's by name, then the resource tools", () => {
    const tool = (name: string, source: "builtin" | "mcp", tokens: number) => ({
      name,
      description: "",
      schema: {},
      source,
      tokens,
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
        },
      }),
    );
    expect(out).toMatchObject({
      model: "gpt-5",
      system: "You are Eika.\n\nBe brief.",
      temperature: 0.2,
      thinking_switch: "reasoning_effort",
      preserve_thinking: true,
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
    expect(editTargetOf("base", true)).toEqual({ key: "chat_prompt", section: "prompt" });
    expect(editTargetOf("base", false)).toEqual({ key: "workspace_prompt", section: "prompt" });
    expect(editTargetOf("mcp_tools", false)).toEqual({ key: "tools", section: "tools" });
    expect(editTargetOf("messages", false)).toBeUndefined();
    expect(parameterTarget("sampling.temperature")?.section).toBe("sampling");
    expect(parameterTarget("sampling.max_output")?.section).toBe("model");
    expect(parameterTarget("preserve_thinking")?.section).toBe("model");
    expect(parameterTarget("thinking_switch")).toBeUndefined();
  });
});

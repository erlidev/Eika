import { describe, expect, it } from "vitest";

import type { Configuration, Tool } from "@/api/types";
import {
  choiceSummary,
  chosen,
  inheritedSampling,
  parseSampling,
  promptChange,
  toDraft,
  toolGroups,
  toSettings,
  withChoice,
} from "@/features/profiles/form";

const tools: Tool[] = [
  { name: "ask_user", description: "", needs_workspace: false },
  { name: "bash", description: "", needs_workspace: true },
  { name: "mcp__docs__read", description: "", needs_workspace: false, server: "docs" },
  { name: "mcp__docs__search", description: "", needs_workspace: false, server: "docs" },
  { name: "mcp__fs__list", description: "", needs_workspace: true, server: "fs" },
];

function configuration(over: Partial<Configuration> = {}): Configuration {
  return {
    profile_id: "p1",
    profile_name: "Default",
    model_id: "m1",
    model: "gpt-5",
    workspace_prompt: "",
    chat_prompt: "",
    instructions: "",
    context_files: true,
    tools: null,
    sampling: {},
    sources: {},
    ...over,
  };
}

describe("parseSampling", () => {
  it("reads each control as its kind, and leaves an empty one unset", () => {
    const draft = toDraft(
      {
        workspace_prompt: null,
        chat_prompt: null,
        instructions: null,
        context_files: null,
        sampling: {},
      },
      null,
    );
    const { sampling, problems } = parseSampling({
      ...draft.sampling,
      temperature: "0.5",
      seed: " 7 ",
      reasoning_effort: "high",
      stop: "END\n\nSTOP",
    });
    expect(problems).toEqual([]);
    expect(sampling).toEqual({
      temperature: 0.5,
      seed: 7,
      reasoning_effort: "high",
      stop: ["END", "STOP"],
    });
  });

  it("names a control whose text is not a number of its kind", () => {
    const draft = toDraft(
      {
        workspace_prompt: null,
        chat_prompt: null,
        instructions: null,
        context_files: null,
        sampling: {},
      },
      null,
    );
    const { problems } = parseSampling({ ...draft.sampling, top_k: "2.5", min_p: "a lot" });
    expect(problems.map((p) => p.key)).toEqual(["top_k", "min_p"]);
  });
});

describe("toSettings", () => {
  it("round-trips what a layer sets, the empty prompt included", () => {
    const settings = {
      model_id: "m2",
      workspace_prompt: "",
      chat_prompt: null,
      instructions: "Be brief.",
      context_files: false,
      sampling: { temperature: 0, stop: ["END"] },
    };
    expect(toSettings(toDraft(settings, ["bash"]))).toEqual({ settings, problems: [] });
  });
});

describe("inheritedSampling", () => {
  it("says the value an unset parameter falls through to and where it comes from", () => {
    const config = configuration({
      sampling: { max_output: 4096 },
      sources: { "sampling.max_output": "model" },
    });
    expect(inheritedSampling(config, "max_output")).toBe("4096 (the model)");
    expect(inheritedSampling(config, "temperature")).toBe("endpoint default");
  });
});

describe("tool choices", () => {
  it("leaves what needs a workspace out of a chat's choice", () => {
    const chat = toolGroups(tools, true);
    expect(chat.builtin.map((t) => t.name)).toEqual(["ask_user"]);
    expect(chat.servers.map((s) => s.server)).toEqual(["docs"]);
    expect(toolGroups(tools, false).servers.map((s) => s.server)).toEqual(["docs", "fs"]);
  });

  it("takes a server's tools through its entry", () => {
    const search = tools[3];
    if (search === undefined) throw new Error("no tool");
    expect(chosen(["mcp__docs__*"], search)).toBe(true);
    expect(chosen(["bash"], search)).toBe(false);
    expect(chosen(null, search)).toBe(true);
  });

  it("turns a whole server on and off, and one of its tools off", () => {
    const groups = toolGroups(tools, false);
    const all = withChoice(["mcp__docs__read"], "mcp__docs__*", true, groups);
    expect(all).toEqual(["mcp__docs__*"]);
    // Turning one tool off keeps the server's other tools by name.
    expect(withChoice(all, "mcp__docs__read", false, groups)).toEqual(["mcp__docs__search"]);
    expect(withChoice(all, "mcp__docs__*", false, groups)).toEqual([]);
    expect(withChoice([], "bash", true, groups)).toEqual(["bash"]);
  });

  it("words a choice in a line", () => {
    expect(choiceSummary(null)).toBe("every tool");
    expect(choiceSummary([])).toBe("no tools");
    expect(choiceSummary(["bash", "mcp__docs__*"])).toBe("1 tool and every tool of docs");
  });
});

describe("promptChange", () => {
  it("counts the lines a prompt adds to and drops from the built-in one", () => {
    expect(promptChange("a\nb", "a\nb")).toEqual({ same: true, added: 0, removed: 0 });
    expect(promptChange("a\nb", "a\nc\nd")).toEqual({ same: false, added: 2, removed: 1 });
  });
});

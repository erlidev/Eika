import { describe, expect, it } from "vitest";

import type { Configuration, Tool } from "@/api/types";
import {
  choiceSummary,
  chosen,
  inheritedSampling,
  isSet,
  matchesTool,
  parseSampling,
  problemSections,
  promptChange,
  samplingField,
  sliderPosition,
  sliderText,
  toDraft,
  toolGroups,
  toolsTokens,
  toSettings,
  withChoice,
} from "@/features/profiles/form";

const tools: Tool[] = [
  { name: "ask_user", description: "Ask the user", needs_workspace: false, tokens: 10 },
  { name: "bash", description: "Run a command", needs_workspace: true, tokens: 40 },
  {
    name: "mcp__docs__read",
    description: "Read a page",
    needs_workspace: false,
    server: "docs",
    tokens: 5,
  },
  {
    name: "mcp__docs__search",
    description: "Search the docs",
    needs_workspace: false,
    server: "docs",
    tokens: 7,
  },
  { name: "mcp__fs__list", description: "", needs_workspace: true, server: "fs", tokens: 3 },
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
    preserve_thinking: false,
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
        preserve_thinking: null,
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
        preserve_thinking: null,
        sampling: {},
      },
      null,
    );
    const { problems } = parseSampling({ ...draft.sampling, top_k: "2.5", min_p: "a lot" });
    expect(problems.map((p) => p.key)).toEqual(["min_p", "top_k"]);
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
      preserve_thinking: true,
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

describe("preserve_thinking", () => {
  it("is set by a draft that turns it either way, and unset by null", () => {
    const none = toDraft(
      {
        workspace_prompt: null,
        chat_prompt: null,
        instructions: null,
        context_files: null,
        preserve_thinking: null,
        sampling: {},
      },
      null,
    );
    expect(isSet(none)).toBe(false);
    expect(isSet({ ...none, preserve_thinking: false })).toBe(true);
  });
});

describe("toolsTokens", () => {
  it("adds up the tools a choice takes, a whole server's included", () => {
    const groups = toolGroups(tools, false);
    expect(toolsTokens(groups, null)).toBe(65);
    expect(toolsTokens(groups, ["bash", "mcp__docs__*"])).toBe(52);
    expect(toolsTokens(groups, [])).toBe(0);
  });
});

describe("matchesTool", () => {
  it("matches every word against the name and description, ignoring case", () => {
    const search = tools[3];
    if (search === undefined) throw new Error("no tool");
    expect(matchesTool(search, "")).toBe(true);
    expect(matchesTool(search, "DOCS search")).toBe(true);
    expect(matchesTool(search, "docs write")).toBe(false);
  });
});

describe("problemSections", () => {
  it("counts the problems of each tab", () => {
    expect(
      problemSections([
        { key: "temperature", message: "" },
        { key: "seed", message: "" },
        { key: "max_output", message: "" },
      ]),
    ).toEqual({ sampling: 2, model: 1 });
  });
});

describe("sliders", () => {
  const range = samplingField("temperature").range;
  if (range === undefined) throw new Error("temperature has no range");

  it("rest at the value set, else the one inherited, else the resting value, clamped", () => {
    expect(sliderPosition(range, "0.4", 0.9)).toBe(0.4);
    expect(sliderPosition(range, "", 0.9)).toBe(0.9);
    expect(sliderPosition(range, "", undefined)).toBe(range.rest);
    expect(sliderPosition(range, "not a number", undefined)).toBe(range.rest);
    expect(sliderPosition(range, "9", undefined)).toBe(range.max);
  });

  it("write a value rounded to the step", () => {
    expect(sliderText(range, 0.30000000000000004)).toBe("0.3");
    expect(sliderText(range, 1)).toBe("1");
  });
});

import { describe, expect, it } from "vitest";

import type { Tool } from "@/api/types";
import { chatTools, toolSummary, withTool } from "@/features/session/tools";

const tool = (name: string, needsWorkspace: boolean): Tool => ({
  name,
  description: `${name} does a thing.`,
  needs_workspace: needsWorkspace,
});

describe("chatTools", () => {
  it("offers only the tools that need no workspace", () => {
    const split = chatTools([
      tool("ask_user", false),
      tool("ls", true),
      tool("bash", true),
      tool("web_search", false),
    ]);
    expect(split.offered.map((t) => t.name)).toEqual(["ask_user", "web_search"]);
    expect(split.unavailable).toEqual(["bash", "ls"]);
  });
});

describe("withTool", () => {
  it("adds a tool in sorted order", () => {
    expect(withTool(["web_search"], "ask_user", true)).toEqual(["ask_user", "web_search"]);
  });

  it("removes a tool", () => {
    expect(withTool(["ask_user", "web_search"], "ask_user", false)).toEqual(["web_search"]);
  });

  it("names a tool once however often it is turned on", () => {
    expect(withTool(["web_search"], "web_search", true)).toEqual(["web_search"]);
  });

  it("turns the last tool off into an empty list", () => {
    expect(withTool(["web_fetch"], "web_fetch", false)).toEqual([]);
  });
});

describe("toolSummary", () => {
  it("keeps the first sentence of the first line", () => {
    expect(
      toolSummary("Search the web, Wikipedia, arXiv or GitHub. Returns titles, URLs and snippets."),
    ).toBe("Search the web, Wikipedia, arXiv or GitHub.");
  });

  it("stops at the end of the first line", () => {
    expect(toolSummary("Fetch a page by URL\nUse it to read a page.")).toBe("Fetch a page by URL");
  });

  it("does not cut inside a dotted name", () => {
    expect(toolSummary("Read example.com pages. More.")).toBe("Read example.com pages.");
  });
});

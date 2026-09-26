import { describe, expect, it } from "vitest";

import type { Tool } from "@/api/types";
import { outOfChat, toolSummary } from "@/features/session/tools";

const tool = (name: string, needsWorkspace: boolean): Tool => ({
  name,
  description: `${name} does a thing.`,
  needs_workspace: needsWorkspace,
  tokens: 10,
});

describe("outOfChat", () => {
  it("lists the built-in tools and the servers that need a workspace", () => {
    const out = outOfChat([
      tool("ask_user", false),
      tool("ls", true),
      tool("bash", true),
      { ...tool("mcp__github__get_file", false), server: "github" },
      { ...tool("mcp__fs__read_file", true), server: "fs" },
      { ...tool("mcp__fs__write_file", true), server: "fs" },
      tool("mcp_read_resource", false),
    ]);
    expect(out).toEqual({ tools: ["bash", "ls"], servers: ["fs"] });
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

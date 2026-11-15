import { describe, expect, it } from "vitest";

import { toolSummary } from "@/features/session/tools";

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

import { describe, expect, it } from "vitest";

import { estimateTokens } from "@/lib/tokens";

describe("estimateTokens", () => {
  it("counts four bytes to a token, rounding up", () => {
    expect(estimateTokens("")).toBe(0);
    expect(estimateTokens("abcd")).toBe(1);
    expect(estimateTokens("abcde")).toBe(2);
  });

  it("counts bytes, not characters", () => {
    // Each of these is three bytes of UTF-8.
    expect(estimateTokens("日本")).toBe(2);
  });
});

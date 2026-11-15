import { describe, expect, it } from "vitest";

import { maxTextBytes, textBytes, textProblem } from "@/lib/textLimit";

describe("textProblem", () => {
  it("counts bytes, not characters", () => {
    expect(textBytes("é")).toBe(2);
    expect(textProblem("a".repeat(maxTextBytes))).toBe("");
    expect(textProblem("é".repeat(maxTextBytes / 2))).toBe("");
    expect(textProblem("é".repeat(maxTextBytes / 2 + 1))).toContain("65,538 bytes");
  });

  it("refuses a NUL and allows empty text", () => {
    expect(textProblem("a\0b")).toContain("NUL");
    expect(textProblem("")).toBe("");
  });
});

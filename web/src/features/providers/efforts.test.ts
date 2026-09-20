import { describe, expect, it } from "vitest";

import { effortProblem, maxReasoningEfforts, nextEffort } from "@/features/providers/efforts";

describe("effortProblem", () => {
  it("accepts the words endpoints use", () => {
    for (const effort of ["none", "high", "xhigh", "think-harder", "ultra_2"]) {
      expect(effortProblem(effort, [])).toBeNull();
    }
  });

  it("refuses what the harness would refuse", () => {
    expect(effortProblem("", [])).toMatch(/type the word/i);
    expect(effortProblem("very high", [])).toMatch(/letters, digits/i);
    expect(effortProblem("x".repeat(33), [])).toMatch(/shorten/i);
    expect(effortProblem("high", ["high"])).toMatch(/already on the list/i);
    expect(
      effortProblem(
        "extra",
        Array.from({ length: maxReasoningEfforts }, (_, i) => `e${String(i)}`),
      ),
    ).toMatch(/at most/i);
  });

  it("ignores the spaces around a word", () => {
    expect(effortProblem("  high  ", [])).toBeNull();
    expect(effortProblem("  high  ", ["high"])).toMatch(/already on the list/i);
  });
});

describe("nextEffort", () => {
  it("cycles through the list and wraps", () => {
    const efforts = ["low", "medium", "high"];
    expect(nextEffort("low", efforts)).toBe("medium");
    expect(nextEffort("high", efforts)).toBe("low");
  });

  it("starts over for an effort the list no longer holds", () => {
    expect(nextEffort("gone", ["low", "high"])).toBe("low");
  });

  it("leaves the effort alone when there is nothing to cycle through", () => {
    expect(nextEffort("high", [])).toBe("high");
  });
});

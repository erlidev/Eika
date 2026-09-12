import { describe, expect, it } from "vitest";

import { formatDuration } from "@/lib/duration";

describe("formatDuration", () => {
  const cases: [number, string][] = [
    [0, "0ms"],
    [12, "12ms"],
    [999, "999ms"],
    [1000, "1.0s"],
    [1500, "1.5s"],
    [59_400, "59.4s"],
    [60_000, "1m 0s"],
    [125_000, "2m 5s"],
    [-1, "-"],
    [Number.NaN, "-"],
  ];

  for (const [ms, want] of cases) {
    it(`renders ${String(ms)} as ${want}`, () => {
      expect(formatDuration(ms)).toBe(want);
    });
  }
});

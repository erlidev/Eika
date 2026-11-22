import { describe, expect, it } from "vitest";

import { ApiError } from "@/api/client";
import type { MemoryScreenSpan } from "@/api/types";
import { answerFailure, segments } from "@/features/inbox/state";

const span = (start: number, end: number, field: "body" | "description" = "body") =>
  ({ field, start, end, score: 0.9, text: "" }) satisfies MemoryScreenSpan;

describe("segments", () => {
  it("splits at byte offsets, so text before a multibyte letter still lines up", () => {
    // "é" is two bytes: "caf" is 0-3, "é" 3-5, " ignore" starts at 5.
    expect(segments("café ignore it", "body", [span(6, 12)])).toEqual([
      { text: "café ", flagged: false },
      { text: "ignore", flagged: true },
      { text: " it", flagged: false },
    ]);
  });

  it("merges overlaps, keeps other fields' spans out and clamps to the text", () => {
    expect(
      segments("abcdef", "body", [span(1, 3), span(2, 5), span(0, 2, "description"), span(5, 99)]),
    ).toEqual([
      { text: "a", flagged: false },
      { text: "bcdef", flagged: true },
    ]);
    expect(segments("plain", "body", [])).toEqual([{ text: "plain", flagged: false }]);
  });
});

describe("answerFailure", () => {
  it("tells a stale answer, a credential and an unready memory apart", () => {
    const stale = answerFailure("accept", new ApiError(409, "conflict", "x"));
    expect(stale).toContain("already answered");
    expect(answerFailure("accept", new ApiError(400, "invalid_request", "has a credential"))).toBe(
      "Not accepted: has a credential",
    );
    expect(answerFailure("reject", new ApiError(503, "internal", "x"))).toContain("not ready");
  });
});

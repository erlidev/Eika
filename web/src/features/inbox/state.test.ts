import { describe, expect, it } from "vitest";

import { ApiError } from "@/api/client";
import { answerFailure } from "@/features/inbox/state";

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

import { afterEach, describe, expect, it, vi } from "vitest";

import { ApiError, parseApiError, request, unreachableMessage } from "@/api/client";

afterEach(() => {
  vi.unstubAllGlobals();
});

describe("parseApiError", () => {
  it("keeps the harness's own message", () => {
    const error = parseApiError(400, {
      error: { code: "invalid_request", message: "name must be 1 to 64 characters" },
    });
    expect(error.code).toBe("invalid_request");
    expect(error.message).toBe("name must be 1 to 64 characters");
  });

  it("replaces a bare internal error with what to do about it", () => {
    const error = parseApiError(500, { error: { code: "internal", message: "internal error" } });
    expect(error.code).toBe("internal");
    expect(error.message).toMatch(/Try again; if it keeps failing, the harness log/);
  });

  it.each([
    [502, /did not answer \(HTTP 502\)/],
    [500, /unexpected error/],
    [418, /refused the request \(HTTP 418\)/],
  ])("describes a %i without the documented body", (status, want) => {
    expect(parseApiError(status, "<html>").message).toMatch(want);
  });
});

describe("request", () => {
  it("says the harness could not be reached when fetch fails", async () => {
    vi.stubGlobal("fetch", vi.fn().mockRejectedValue(new TypeError("Failed to fetch")));
    await expect(request("/api/settings")).rejects.toThrow(unreachableMessage);
  });

  it("keeps an abort an abort", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn().mockRejectedValue(new DOMException("The operation was aborted.", "AbortError")),
    );
    await expect(request("/api/settings")).rejects.toMatchObject({ name: "AbortError" });
  });

  it("decodes the error shape", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn().mockResolvedValue(
        new Response(JSON.stringify({ error: { code: "conflict", message: "taken" } }), {
          status: 409,
        }),
      ),
    );
    await expect(request("/api/providers")).rejects.toEqual(new ApiError(409, "conflict", "taken"));
  });
});

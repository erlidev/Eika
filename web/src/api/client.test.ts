import { afterEach, describe, expect, it, vi } from "vitest";

import { ApiError, notJsonMessage, parseApiError, request, unreachableMessage } from "@/api/client";

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
    [404, /no such route \(HTTP 404\)/],
    [429, /turning away requests .*\(HTTP 429\)/],
    [418, /refused the request \(HTTP 418\) without saying why/],
  ])("describes a %i without the documented body", (status, want) => {
    expect(parseApiError(status, "<html>").message).toMatch(want);
  });
});

describe("request", () => {
  it("says the harness could not be reached when fetch fails", async () => {
    vi.stubGlobal("fetch", vi.fn().mockRejectedValue(new TypeError("Failed to fetch")));
    await expect(request("/api/settings")).rejects.toThrow(unreachableMessage);
  });

  it("says a success that is not JSON came from something else", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn().mockResolvedValue(new Response("<!doctype html><title>x</title>", { status: 200 })),
    );
    await expect(request("/api/settings")).rejects.toThrow(notJsonMessage);
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

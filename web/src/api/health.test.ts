import { afterEach, describe, expect, it, vi } from "vitest";

import { fetchHealth, parseHealth } from "@/api/health";

afterEach(() => {
  vi.unstubAllGlobals();
});

/** stubFetch replaces the global fetch with one canned response. */
function stubFetch(response: Response) {
  vi.stubGlobal("fetch", vi.fn().mockResolvedValue(response));
}

/** jsonResponse builds a JSON Response with the given status. */
function jsonResponse(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { "content-type": "application/json" },
  });
}

describe("parseHealth", () => {
  it("accepts the harness response", () => {
    expect(parseHealth({ status: "ok" })).toEqual({ status: "ok" });
  });

  const rejected: [string, unknown][] = [
    ["null", null],
    ["a string", "ok"],
    ["an object without status", { state: "ok" }],
    ["a non-string status", { status: 1 }],
  ];
  for (const [name, body] of rejected) {
    it(`rejects ${name}`, () => {
      expect(() => parseHealth(body)).toThrow();
    });
  }
});

describe("fetchHealth", () => {
  it("returns the parsed body", async () => {
    stubFetch(jsonResponse({ status: "ok" }));
    await expect(fetchHealth()).resolves.toEqual({ status: "ok" });
  });

  it("reports a failing status code", async () => {
    stubFetch(jsonResponse({ status: "ok" }, 503));
    await expect(fetchHealth()).rejects.toThrow("503");
  });

  it("reports a body it cannot parse", async () => {
    stubFetch(jsonResponse({ nope: true }));
    await expect(fetchHealth()).rejects.toThrow("parse health");
  });
});

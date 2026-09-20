import { describe, expect, it } from "vitest";

import type { Provider } from "@/api/types";
import {
  baseUrlProblem,
  keyWillBeCleared,
  probeInput,
  storedKeyApplies,
  updateInput,
  validate,
} from "@/features/providers/form";
import type { ProviderFormState } from "@/features/providers/form";

const stored: Provider = {
  id: "p1",
  name: "OpenAI",
  kind: "openai",
  base_url: "https://api.openai.com/v1",
  api_key_set: true,
  api_key_hint: "cdef",
  created_at: "2026-09-01T00:00:00Z",
  updated_at: "2026-09-01T00:00:00Z",
};

function form(over: Partial<ProviderFormState> = {}): ProviderFormState {
  return { name: "OpenAI", baseUrl: stored.base_url, apiKey: "", removeKey: false, ...over };
}

describe("a changed base URL", () => {
  const moved = form({ baseUrl: "https://proxy.example/v1" });

  it("clears the stored key when no new key is typed", () => {
    expect(keyWillBeCleared(stored, moved)).toBe(true);
    expect(storedKeyApplies(stored, moved)).toBe(false);
    expect(updateInput(moved)).toEqual({ name: "OpenAI", base_url: "https://proxy.example/v1" });
  });

  it("tests without the stored key", () => {
    // No api_key means the harness decides, and it keeps the stored key to
    // the stored URL.
    expect(probeInput(stored, moved)).toEqual({
      provider_id: "p1",
      base_url: "https://proxy.example/v1",
    });
  });

  it("asks for the new key when the endpoint needs one", () => {
    const problem = validate({ ...moved, provider: stored, takenNames: [], keyRequired: true });
    expect(problem).toEqual({
      field: "apiKey",
      message: "Enter the API key for the new base URL.",
    });
  });

  it("allows no key for an endpoint that needs none", () => {
    expect(validate({ ...moved, provider: stored, takenNames: [], keyRequired: false })).toBeNull();
  });

  it("keeps a newly typed key", () => {
    const withKey = { ...moved, apiKey: " sk-new " };
    expect(keyWillBeCleared(stored, withKey)).toBe(false);
    expect(updateInput(withKey)).toMatchObject({ api_key: "sk-new" });
    expect(probeInput(stored, withKey)).toMatchObject({ api_key: "sk-new" });
  });
});

describe("an unchanged base URL", () => {
  it("keeps and tests with the stored key", () => {
    const same = form({ baseUrl: ` ${stored.base_url} ` });
    expect(keyWillBeCleared(stored, same)).toBe(false);
    expect(storedKeyApplies(stored, same)).toBe(true);
    expect(validate({ ...same, provider: stored, takenNames: [], keyRequired: true })).toBeNull();
    expect(updateInput(same)).not.toHaveProperty("api_key");
  });

  it("removes the key only when asked", () => {
    const removing = form({ removeKey: true });
    expect(updateInput(removing)).toMatchObject({ api_key: "" });
    expect(probeInput(stored, removing)).toMatchObject({ api_key: "" });
  });
});

describe("validate", () => {
  const base = { ...form(), provider: undefined, takenNames: ["Taken"], keyRequired: false };

  it.each([
    [{ name: " " }, "name", /name/],
    [{ name: "Taken" }, "name", /Another provider/],
    [{ name: "x".repeat(65) }, "name", /64/],
    [{ baseUrl: "" }, "baseUrl", /base URL/],
    [{ keyRequired: true }, "apiKey", /needs an API key/],
  ])("rejects %j", (over, field, message) => {
    const problem = validate({ ...base, ...over });
    expect(problem?.field).toBe(field);
    expect(problem?.message).toMatch(message);
  });

  it("accepts a complete provider", () => {
    expect(validate(base)).toBeNull();
  });
});

describe("baseUrlProblem", () => {
  it.each([
    ["https://api.openai.com/v1", null],
    ["http://host.docker.internal:11434/v1", null],
    ["api.openai.com/v1", /http:\/\/ or https:\/\//],
    ["ftp://x.test", /http:\/\/ or https:\/\//],
    ["https://u:p@x.test/v1", /credentials/],
    ["https://x.test/v1?key=1", /query string/],
    ["https://x.test/v1#frag", /fragment/],
  ])("%s", (url, want) => {
    const got = baseUrlProblem(url);
    if (want === null) expect(got).toBeNull();
    else expect(got).toMatch(want);
  });
});

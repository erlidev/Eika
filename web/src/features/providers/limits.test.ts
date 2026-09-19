import { describe, expect, it } from "vitest";

import { suggestLimits, suggestModelName } from "@/features/providers/limits";
import { presetOf, uniqueProviderName } from "@/features/providers/presets";

describe("suggestLimits", () => {
  it("knows the common model families", () => {
    expect(suggestLimits("gpt-5-mini")).toEqual({ context_window: 400_000, max_output: 128_000 });
    expect(suggestLimits("gpt-4o")).toEqual({ context_window: 128_000, max_output: 16_384 });
    expect(suggestLimits("o3")).toEqual({ context_window: 200_000, max_output: 100_000 });
    expect(suggestLimits("claude-sonnet-4-5").context_window).toBe(200_000);
    expect(suggestLimits("gemini-2.5-pro").context_window).toBe(1_048_576);
  });

  it("looks past a vendor prefix", () => {
    expect(suggestLimits("openai/gpt-5")).toEqual(suggestLimits("gpt-5"));
    expect(suggestLimits("meta-llama/llama-3.3-70b").context_window).toBe(32_768);
  });

  it("prefers what the endpoint reports", () => {
    expect(
      suggestLimits("anything", { id: "anything", context_window: 65_536, max_output: 4_096 }),
    ).toEqual({ context_window: 65_536, max_output: 4_096 });
  });

  it("keeps the output limit inside a reported window", () => {
    const limits = suggestLimits("gpt-5", { id: "gpt-5", context_window: 8_192 });
    expect(limits.context_window).toBe(8_192);
    expect(limits.max_output).toBeLessThanOrEqual(8_192 / 4);
  });

  it("gives an unknown model a conservative default", () => {
    expect(suggestLimits("mystery-model")).toEqual({
      context_window: 128_000,
      max_output: 16_384,
    });
  });

  it("ignores a reported limit that is not a positive number", () => {
    expect(suggestLimits("gpt-4o", { id: "gpt-4o", context_window: 0, max_output: -1 })).toEqual(
      suggestLimits("gpt-4o"),
    );
  });
});

describe("suggestModelName", () => {
  it("uses the id when it is free", () => {
    expect(suggestModelName("gpt-5", "OpenAI", [])).toBe("gpt-5");
  });

  it("scopes a taken id under the provider", () => {
    expect(suggestModelName("gpt-5", "Open Router!", ["gpt-5"])).toBe("open-router/gpt-5");
    expect(suggestModelName("gpt-5", "OpenRouter", ["gpt-5", "openrouter/gpt-5"])).toBe(
      "openrouter/gpt-5-2",
    );
  });
});

describe("presets", () => {
  it("recognises a stored provider by its base URL", () => {
    expect(presetOf("https://api.openai.com/v1/")?.id).toBe("openai");
    expect(presetOf("https://example.test/v1")).toBeUndefined();
  });

  it("numbers a provider name that is taken", () => {
    expect(uniqueProviderName("OpenAI", [])).toBe("OpenAI");
    expect(uniqueProviderName("OpenAI", ["OpenAI", "OpenAI 2"])).toBe("OpenAI 3");
  });
});

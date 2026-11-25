import { describe, expect, it } from "vitest";

import { suggestLimits } from "@/features/providers/limits";

describe("suggestLimits", () => {
  it("uses the window the probe read when the model list states none", () => {
    // llama.cpp lists no context size on /v1/models; /props states n_ctx.
    expect(suggestLimits("qwen3.8-27b", { id: "qwen3.8-27b" }, 256_000).context_window).toBe(
      256_000,
    );
  });
  it("prefers the model list, then the family guess", () => {
    expect(
      suggestLimits("qwen3", { id: "qwen3", context_window: 65_536 }, 256_000).context_window,
    ).toBe(65_536);
    expect(suggestLimits("qwen3", { id: "qwen3" }).context_window).toBe(32_768);
  });
});

import { describe, expect, it } from "vitest";

import type { Provider } from "@/api/types";
import { endpointForm, endpointUpdate } from "@/features/providers/endpoint";

/** the bench as the harness probes it: two slots, no word on the cache. */
const bench: Provider = {
  id: "prov-1",
  name: "Bench",
  kind: "openai",
  base_url: "http://10.0.0.2:8091/v1",
  api_key_set: true,
  backend: "llamacpp",
  pool: "Bench",
  slots: 2,
  kv: "unknown",
  api: "chat",
  capabilities: {
    build: "b11942-338fce651",
    window: 131072,
    counting: "chat",
    cache_metrics: "reported",
    slot_hints: true,
  },
  overridden: [],
  probed_at: "2026-10-31T12:00:00Z",
  created_at: "2026-10-31T11:00:00Z",
  updated_at: "2026-10-31T12:00:00Z",
};

describe("endpointUpdate", () => {
  it("sends the api only when the user changed it", () => {
    expect(endpointUpdate(bench, { ...endpointForm(bench), api: "responses" })).toEqual({
      api: "responses",
    });
    expect(endpointUpdate(bench, endpointForm(bench))).toEqual({});
  });

  it("sends the cache and its size together", () => {
    const form = { ...endpointForm(bench), kv: "shared" as const, kvTokens: 131072 };
    expect(endpointUpdate(bench, form)).toEqual({ kv: "shared", kv_tokens: 131072 });
  });

  it("sends a pool, and an empty one when the provider's own name is typed back", () => {
    const set: Provider = { ...bench, pool: "rtx5090" };
    expect(endpointUpdate(bench, { ...endpointForm(bench), pool: " rtx5090 " })).toEqual({
      pool: "rtx5090",
    });
    expect(endpointUpdate(set, { ...endpointForm(set), pool: "Bench" })).toEqual({ pool: "" });
  });
});

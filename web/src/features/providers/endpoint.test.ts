import { describe, expect, it } from "vitest";

import type { Provider } from "@/api/types";
import {
  endpointForm,
  endpointProblem,
  endpointUpdate,
  hardwareSummary,
  kvSummary,
} from "@/features/providers/endpoint";

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

describe("hardwareSummary", () => {
  it("states backend, build, slots, window and cache", () => {
    expect(hardwareSummary(bench)).toBe(
      "llama.cpp b11942 · 2 slots · 131,072 window · cache not known",
    );
  });

  it("says when nothing has probed the endpoint", () => {
    expect(hardwareSummary({ ...bench, probed_at: undefined })).toBe("Not probed yet");
  });

  it("leaves out what the server did not state", () => {
    const hosted: Provider = {
      ...bench,
      backend: "openai",
      slots: 1,
      capabilities: { counting: "none", cache_metrics: "unknown", slot_hints: false },
    };
    expect(hardwareSummary(hosted)).toBe("Hosted API · 1 slot · cache not known");
  });
});

describe("kvSummary", () => {
  it("gives the size of a shared cache when it is known", () => {
    expect(kvSummary("shared", 131072)).toBe("shared 131,072");
    expect(kvSummary("shared")).toBe("shared, size not known");
    expect(kvSummary("shared", 0)).toBe("shared, size not known");
    expect(kvSummary("partitioned", 5)).toBe("partitioned");
  });
});

describe("endpointUpdate", () => {
  it("sends nothing for an untouched form, so nothing becomes an override", () => {
    expect(endpointUpdate(bench, endpointForm(bench))).toEqual({});
  });

  it("sends only the fields that changed", () => {
    const form = { ...endpointForm(bench), slots: 4 };
    expect(endpointUpdate(bench, form)).toEqual({ slots: 4 });
  });

  it("sends the cache and its size together", () => {
    const form = { ...endpointForm(bench), kv: "shared" as const, kvTokens: 131072 };
    expect(endpointUpdate(bench, form)).toEqual({ kv: "shared", kv_tokens: 131072 });
  });

  it("sends a new size for a cache that was already shared", () => {
    const shared: Provider = { ...bench, kv: "shared", kv_tokens: 1000 };
    const form = { ...endpointForm(shared), kvTokens: 2000 };
    expect(endpointUpdate(shared, form)).toEqual({ kv: "shared", kv_tokens: 2000 });
  });

  it("sends no size for a cache that is not shared", () => {
    const form = { ...endpointForm(bench), kv: "partitioned" as const };
    expect(endpointUpdate(bench, form)).toEqual({ kv: "partitioned" });
  });

  it("sends a pool, and an empty one when the provider's own name is typed back", () => {
    const set: Provider = { ...bench, pool: "rtx5090" };
    expect(endpointUpdate(bench, { ...endpointForm(bench), pool: " rtx5090 " })).toEqual({
      pool: "rtx5090",
    });
    expect(endpointUpdate(set, { ...endpointForm(set), pool: "Bench" })).toEqual({ pool: "" });
  });
});

describe("endpointProblem", () => {
  it("accepts the form the harness would", () => {
    expect(endpointProblem(endpointForm(bench))).toBeNull();
  });

  it("refuses what the harness refuses", () => {
    const form = endpointForm(bench);
    expect(endpointProblem({ ...form, slots: 0 })?.field).toBe("slots");
    expect(endpointProblem({ ...form, slots: 2.5 })?.field).toBe("slots");
    expect(endpointProblem({ ...form, slots: Number.NaN })?.field).toBe("slots");
    expect(endpointProblem({ ...form, kv: "shared", kvTokens: -1 })?.field).toBe("kvTokens");
    expect(endpointProblem({ ...form, pool: "x".repeat(65) })?.field).toBe("pool");
  });

  it("ignores a size on a cache that is not shared", () => {
    expect(endpointProblem({ ...endpointForm(bench), kv: "partitioned", kvTokens: -5 })).toBeNull();
  });
});

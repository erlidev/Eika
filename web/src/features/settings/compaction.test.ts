import { describe, expect, it } from "vitest";

import type { SettingsState } from "@/api/types";
import {
  compactionSetting,
  promptProblem,
  savedCompaction,
  tokensProblem,
} from "@/features/settings/compaction";

function state(stored: unknown): SettingsState {
  return {
    settings: stored === undefined ? {} : { compaction: stored },
    defaults: {
      sandbox_image: "eika-sandbox:latest",
      subagent_max_depth: 2,
      subagent_max_children: 4,
      workspace_idle_minutes: 15,
      sandbox_limits: { cpus: 0, memory_mb: 0, pids: 4096 },
      sandbox_egress: { mode: "open", allow: [] },
      search_order: [],
      search_limits: {},
      compaction: {
        auto: true,
        reserve_tokens: 16384,
        keep_recent_tokens: 20000,
        prompts: { summary: "Summarize.", update: "Update.", turn_prefix: "The turn." },
      },
    },
  };
}

describe("compactionSetting", () => {
  it("is the defaults with the built-in prompts when nothing is stored", () => {
    expect(compactionSetting(state(undefined))).toEqual({
      auto: true,
      reserve_tokens: 16384,
      keep_recent_tokens: 20000,
      prompts: { summary: "", update: "", turn_prefix: "" },
    });
  });

  it("reads what is stored over the defaults, field by field", () => {
    const got = compactionSetting(
      state({ auto: false, keep_recent_tokens: 4096, prompts: { update: "Fold it in." } }),
    );
    expect(got).toEqual({
      auto: false,
      reserve_tokens: 16384,
      keep_recent_tokens: 4096,
      prompts: { summary: "", update: "Fold it in.", turn_prefix: "" },
    });
  });

  it("ignores a stored value of the wrong shape", () => {
    expect(compactionSetting(state("on")).auto).toBe(true);
    expect(compactionSetting(state({ reserve_tokens: "big" })).reserve_tokens).toBe(16384);
  });
});

describe("the checks", () => {
  it("bounds the budgets as the harness does", () => {
    expect(tokensProblem(1024)).toBeUndefined();
    expect(tokensProblem(1 << 20)).toBeUndefined();
    expect(tokensProblem(1023)).toBeDefined();
    expect(tokensProblem(2.5e3)).toBeUndefined();
    expect(tokensProblem(2500.5)).toBeDefined();
    expect(tokensProblem(Number.NaN)).toBeDefined();
  });

  it("bounds a prompt's size in bytes", () => {
    expect(promptProblem("x".repeat(64 << 10))).toBeUndefined();
    expect(promptProblem("é".repeat(40 << 10))).toBeDefined();
  });

  it("saves a prompt of only whitespace as the built-in one", () => {
    const saved = savedCompaction({
      auto: true,
      reserve_tokens: 16384,
      keep_recent_tokens: 20000,
      prompts: { summary: "  \n", update: "Keep it.", turn_prefix: "" },
    });
    expect(saved.prompts).toEqual({ summary: "", update: "Keep it.", turn_prefix: "" });
  });
});

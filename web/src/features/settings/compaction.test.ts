import { describe, expect, it } from "vitest";

import type { SettingsState } from "@/api/types";
import { compactionSetting } from "@/features/settings/compaction";

function state(stored: unknown): SettingsState {
  return {
    settings: stored === undefined ? {} : { compaction: stored },
    classes: [],
    titles_model: "",
    defaults: {
      sandbox_image: "eika-sandbox:latest",
      computer_idle_minutes: 15,
      subagent_max_running: 4,
      maintenance_idle_seconds: 120,
      aging_seconds: 300,
      preempt_running: false,
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

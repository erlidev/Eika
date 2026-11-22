import { describe, expect, it } from "vitest";

import type { SettingsState } from "@/api/types";
import { readOrder, safeHref } from "@/features/search/search";

function state(settings: Record<string, unknown>): SettingsState {
  return {
    settings,
    classes: [],
    titles_model: "",
    defaults: {
      screen_required: false,
      screen_threshold: 0.9,
      sandbox_image: "eika-sandbox:latest",
      computer_idle_minutes: 15,
      compact_idle_fill: 60,
      compact_idle_minutes: 30,
      subagent_max_running: 4,
      maintenance_idle_seconds: 120,
      aging_seconds: 300,
      preempt_running: false,
      sandbox_limits: { cpus: 0, memory_mb: 0, pids: 4096 },
      sandbox_egress: { mode: "open", allow: [] },
      search_order: ["searxng", "exa", "marginalia"],
      search_limits: { searxng: {}, exa: { month: 900 }, marginalia: { day: 100 }, github: {} },
      compaction: {
        auto: true,
        reserve_tokens: 16384,
        keep_recent_tokens: 20000,
        prompts: { summary: "Summarize.", update: "Update.", turn_prefix: "Summarize the turn." },
      },
    },
  };
}

describe("readOrder", () => {
  it("uses the default until one is stored", () => {
    expect(readOrder(state({}))).toEqual(["searxng", "exa", "marginalia"]);
  });

  it("keeps a stored order and drops providers the harness does not know", () => {
    expect(readOrder(state({ search_order: ["marginalia", "google", 3, "searxng"] }))).toEqual([
      "marginalia",
      "searxng",
    ]);
  });

  it("keeps an empty order, which means web search is off", () => {
    expect(readOrder(state({ search_order: [] }))).toEqual([]);
  });
});

describe("safeHref", () => {
  it("links web URLs only", () => {
    expect(safeHref("https://tokio.rs/")).toBe("https://tokio.rs/");
    expect(safeHref("http://example.com/a b")).toBe("http://example.com/a%20b");
    expect(safeHref("javascript:alert(1)")).toBeUndefined();
    expect(safeHref("data:text/html,x")).toBeUndefined();
    expect(safeHref("/relative")).toBeUndefined();
  });
});

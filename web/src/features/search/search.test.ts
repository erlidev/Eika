import { describe, expect, it } from "vitest";

import type { SettingsState } from "@/api/types";
import {
  limitProblem,
  moveProvider,
  parseLimit,
  readLimits,
  readOrder,
  safeHref,
  toggleProvider,
  usageText,
} from "@/features/search/search";

function state(settings: Record<string, unknown>): SettingsState {
  return {
    settings,
    defaults: {
      sandbox_image: "eika-sandbox:latest",
      subagent_max_depth: 2,
      subagent_max_children: 4,
      sandbox_limits: { cpus: 0, memory_mb: 0, pids: 4096 },
      sandbox_egress: { mode: "open", allow: [] },
      search_order: ["searxng", "exa", "marginalia"],
      search_limits: { searxng: {}, exa: { month: 900 }, marginalia: { day: 100 }, github: {} },
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

describe("readLimits", () => {
  it("overrides the defaults bucket by bucket", () => {
    expect(readLimits(state({ search_limits: { exa: { month: 50 }, nope: { day: 1 } } }))).toEqual({
      searxng: {},
      exa: { month: 50 },
      marginalia: { day: 100 },
      github: {},
    });
  });

  it("reads zero as unlimited and ignores a malformed value", () => {
    expect(readLimits(state({ search_limits: { marginalia: { day: 0 } } })).marginalia).toEqual({});
    expect(readLimits(state({ search_limits: "lots" })).exa).toEqual({ month: 900 });
  });
});

describe("moveProvider", () => {
  it("swaps with the neighbour and stays in bounds", () => {
    expect(moveProvider(["a", "b", "c"], "b", -1)).toEqual(["b", "a", "c"]);
    expect(moveProvider(["a", "b", "c"], "b", 1)).toEqual(["a", "c", "b"]);
    expect(moveProvider(["a", "b", "c"], "a", -1)).toEqual(["a", "b", "c"]);
    expect(moveProvider(["a", "b", "c"], "c", 1)).toEqual(["a", "b", "c"]);
    expect(moveProvider(["a"], "z", 1)).toEqual(["a"]);
  });
});

describe("toggleProvider", () => {
  it("removes a provider, and puts it back at the end", () => {
    expect(toggleProvider(["a", "b", "c"], "b")).toEqual(["a", "c"]);
    expect(toggleProvider(["a", "c"], "b")).toEqual(["a", "c", "b"]);
  });
});

describe("usageText", () => {
  const usage = { day: "2026-09-18", day_used: 3, month: "2026-09", month_used: 12 };
  it("reads against the monthly quota, then the daily one", () => {
    expect(usageText({ usage, limit: { month: 900 } })).toBe("12/900 this month");
    expect(usageText({ usage, limit: { day: 100 } })).toBe("3/100 today");
    expect(usageText({ usage, limit: {} })).toBe("3 today, unlimited");
  });
});

describe("limitProblem", () => {
  it.each([
    ["", null],
    ["  ", null],
    ["1", null],
    [" 250 ", null],
    ["10000000", null],
  ])("accepts %j", (text, want) => {
    expect(limitProblem({ text })).toBe(want);
  });

  it.each([
    ["0", /at least 1/],
    ["-5", /at least 1/],
    ["abc", /whole number/],
    ["2.5", /whole number/],
    ["1e3", /whole number/],
    ["10000001", /at most 10,000,000/],
  ])("rejects %j rather than reading it as unlimited", (text, want) => {
    expect(limitProblem({ text })).toMatch(want);
  });

  it("rejects what the browser could not read as a number", () => {
    expect(limitProblem({ text: "", badInput: true })).toMatch(/whole number/);
  });
});

describe("parseLimit", () => {
  it("reads a whole number, and only an empty field as unlimited", () => {
    expect(parseLimit(" 250 ")).toBe(250);
    expect(parseLimit("")).toBeUndefined();
    expect(parseLimit("  ")).toBeUndefined();
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

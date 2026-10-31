import { describe, expect, it } from "vitest";

import type { ModelClass, ModelClassName } from "@/api/types";
import { joinNames, sharedWith, titlesStatus } from "@/features/settings/classes";

function cls(name: ModelClassName, model: string, pool?: string, via?: ModelClassName): ModelClass {
  return {
    class: name,
    kind: name === "decision" ? "decision" : "chat",
    model,
    ...(via === undefined ? {} : { via }),
    ...(pool === undefined ? {} : { provider_id: `p-${pool}`, pool }),
  };
}

describe("sharedWith", () => {
  const deep = cls("deep", "claude", "cloud");
  const main = cls("main", "qwen", "5090");
  const fast = cls("fast", "qwen", "5090", "main");
  const decision = cls("decision", "d3", "3090");
  const classes = [deep, main, fast, decision];

  it("names the classes served from one pool, fallbacks included", () => {
    expect(sharedWith(classes, main)).toEqual(["fast"]);
    expect(sharedWith(classes, fast)).toEqual(["main"]);
  });

  it("names nothing for a class alone in its pool", () => {
    expect(sharedWith(classes, deep)).toEqual([]);
    expect(sharedWith(classes, decision)).toEqual([]);
  });

  it("names nothing for a class with no model", () => {
    const none = cls("main", "");
    expect(sharedWith([none, cls("fast", "")], none)).toEqual([]);
  });

  it("counts a decision model on the same hardware as a chat model", () => {
    const chat = cls("main", "qwen", "box");
    expect(sharedWith([chat, cls("decision", "d3", "box")], chat)).toEqual(["decision"]);
  });
});

describe("joinNames", () => {
  it("writes a list as prose", () => {
    expect(joinNames([])).toBe("");
    expect(joinNames(["main"])).toBe("main");
    expect(joinNames(["main", "fast"])).toBe("main and fast");
    expect(joinNames(["deep", "main", "fast"])).toBe("deep, main and fast");
  });
});

describe("titlesStatus", () => {
  it("names the model that makes titles", () => {
    expect(titlesStatus([], "qwen")).toBe("Session titles are made by qwen.");
  });

  it("says titles are off when no class has a model", () => {
    expect(titlesStatus([cls("fast", ""), cls("main", "")], "")).toMatch(/off until/);
  });

  it("says why titles are skipped when fast falls back onto a hosted deep model", () => {
    const status = titlesStatus([cls("fast", "claude", "cloud", "main")], "");
    expect(status).toMatch(/skipped/);
    expect(status).toMatch(/claude/);
  });
});

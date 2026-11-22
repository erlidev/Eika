import { describe, expect, it } from "vitest";

import type { MemoryItem, Session } from "@/api/types";
import { listQuery, narrow, noFilters, sourceParts } from "@/features/memory/state";

const item = (name: string, over: Partial<MemoryItem> = {}): MemoryItem => ({
  name,
  description: "",
  type: "fact",
  state: "active",
  pinned: false,
  ...over,
});

describe("listQuery", () => {
  it("sends a list's filters as they are", () => {
    expect(listQuery({ ...noFilters, type: "person", state: "pending", pinned: true })).toEqual({
      type: "person",
      state: "pending",
      pinned: true,
    });
    expect(listQuery(noFilters)).toEqual({});
  });

  it("sends a search only what a search takes, and includes archived unless the state excludes them", () => {
    expect(listQuery({ ...noFilters, search: " tea ", type: "fact", state: "active" })).toEqual({
      q: "tea",
      type: "fact",
      include_archived: false,
    });
    expect(listQuery({ ...noFilters, search: "tea", state: "archived" }).include_archived).toBe(
      true,
    );
    expect(listQuery({ ...noFilters, search: "tea" }).include_archived).toBe(true);
  });
});

describe("narrow", () => {
  it("applies state and pinned to a search, and leaves a list alone", () => {
    const items = [item("a"), item("b", { state: "pending" }), item("c", { pinned: true })];
    expect(
      narrow(items, { ...noFilters, search: "x", state: "pending" }).map((m) => m.name),
    ).toEqual(["b"]);
    expect(narrow(items, { ...noFilters, search: "x", pinned: true }).map((m) => m.name)).toEqual([
      "c",
    ]);
    expect(narrow(items, { ...noFilters, state: "pending" })).toBe(items);
  });
});

describe("sourceParts", () => {
  const session = (id: string, kind: string) => ({ id, kind }) as Session;
  it("links a subagent and the home session, and leaves the rest as text", () => {
    const sessions = [session("home-main", "home")];
    expect(sourceParts("session:home-main entry:e1 session:gone", sessions)).toEqual([
      { text: "session:home-main", to: "/" },
      { text: "entry:e1" },
      { text: "session:gone" },
    ]);
    expect(sourceParts("session:sub-a", [session("sub-a", "subagent")])[0]?.to).toBe(
      "/activity/sub-a",
    );
  });
});

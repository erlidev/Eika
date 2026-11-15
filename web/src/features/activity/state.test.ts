import { describe, expect, it } from "vitest";

import type { Session } from "@/api/types";
import {
  boardGroups,
  firstLine,
  isActive,
  isSubagent,
  liveLine,
  projectsOf,
} from "@/features/activity/state";

/** agent is a subagent with only what a test names. */
function agent(input: Partial<Session> & { id: string }): Session {
  return {
    owner: "local",
    state: "idle",
    last_active_at: "2026-03-14T14:00:00Z",
    title: input.id,
    kind: "subagent",
    tools: [],
    overridden: false,
    pinned: false,
    archived: false,
    created_at: "2026-03-14T10:00:00Z",
    updated_at: "2026-03-14T10:00:00Z",
    ...input,
  };
}

describe("boardGroups", () => {
  const agents = [
    agent({ id: "a", state: "idle", last_active_at: "2026-03-14T10:00:00Z" }),
    agent({ id: "b", state: "failed" }),
    agent({ id: "c", state: "running", project_id: "p1" }),
    agent({ id: "d", state: "waiting", project_id: "p2" }),
    agent({ id: "e", state: "cancelled", last_active_at: "2026-03-14T14:30:00Z" }),
    agent({ id: "f", state: "idle", last_active_at: "2026-03-14T13:00:00Z" }),
  ];

  it("orders the groups waiting, running, idle, then failed and cancelled together", () => {
    expect(boardGroups(agents, "").map((g) => [g.id, g.agents.map((a) => a.id)])).toEqual([
      ["waiting", ["d"]],
      ["running", ["c"]],
      ["idle", ["f", "a"]],
      ["stopped", ["e", "b"]],
    ]);
  });

  it("leaves out groups with no subagent", () => {
    expect(boardGroups([agent({ id: "x", state: "running" })], "").map((g) => g.id)).toEqual([
      "running",
    ]);
    expect(boardGroups([], "")).toEqual([]);
  });

  it("narrows to one project, and an empty project keeps all", () => {
    expect(boardGroups(agents, "p2").flatMap((g) => g.agents.map((a) => a.id))).toEqual(["d"]);
    expect(boardGroups(agents, "").flatMap((g) => g.agents)).toHaveLength(agents.length);
  });

  it("does not reorder the list it was given", () => {
    const before = agents.map((a) => a.id);
    boardGroups(agents, "");
    expect(agents.map((a) => a.id)).toEqual(before);
  });
});

describe("projectsOf", () => {
  it("lists the projects subagents belong to, in the projects' order", () => {
    const projects = [{ id: "p1" }, { id: "p2" }, { id: "p3" }];
    const agents = [agent({ id: "a", project_id: "p3" }), agent({ id: "b", project_id: "p1" })];
    expect(projectsOf(agents, projects)).toEqual([{ id: "p1" }, { id: "p3" }]);
    expect(projectsOf([agent({ id: "c" })], projects)).toEqual([]);
  });
});

describe("liveLine", () => {
  it("says why a subagent waits, in the waiting colour", () => {
    expect(
      liveLine(agent({ id: "a", state: "waiting", waiting_reason: "the user's answer" })),
    ).toEqual({
      text: "Waiting on the user's answer",
      waiting: true,
    });
    expect(liveLine(agent({ id: "a", state: "waiting" }))).toEqual({
      text: "Waiting",
      waiting: true,
    });
  });

  it("falls back from the report to the brief, one line each", () => {
    const both = agent({
      id: "a",
      brief: "Find a plumber.\nBe quick.",
      result: "\n\nTwo found.\nMore.",
    });
    expect(liveLine(both)).toEqual({ text: "Two found.", waiting: false });
    expect(liveLine(agent({ id: "a", brief: "Find a plumber.\nBe quick." })).text).toBe(
      "Find a plumber.",
    );
    expect(liveLine(agent({ id: "a" })).text).toBe("");
  });

  it("does not show a stale wait once the subagent runs again", () => {
    const running = agent({ id: "a", state: "running", waiting_reason: "old", brief: "Brief." });
    expect(liveLine(running)).toEqual({ text: "Brief.", waiting: false });
  });
});

describe("firstLine, isActive and isSubagent", () => {
  it("takes the first line that has text", () => {
    expect(firstLine("  \n  hello  \nworld")).toBe("hello");
    expect(firstLine(undefined)).toBe("");
  });

  it("calls a run active while it runs or waits", () => {
    expect(
      (["idle", "running", "waiting", "failed", "cancelled"] as const).filter(isActive),
    ).toEqual(["running", "waiting"]);
  });

  it("picks subagents out of the sessions", () => {
    expect(isSubagent(agent({ id: "a" }))).toBe(true);
    expect(isSubagent(agent({ id: "a", kind: "home" }))).toBe(false);
  });
});

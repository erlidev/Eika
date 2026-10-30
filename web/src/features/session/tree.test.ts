import { describe, expect, it } from "vitest";

import type { Node } from "@/api/types";
import { rewindTarget } from "@/features/session/tree";

function node(id: string, parent?: string, resumable = true): Node {
  return {
    id,
    kind: "user",
    preview: id,
    resumable,
    created_at: "2026-03-14T15:00:00Z",
    ...(parent === undefined ? {} : { parent_id: parent }),
  };
}

describe("rewindTarget", () => {
  it("lands on the entry before the message", () => {
    expect(rewindTarget([node("a"), node("b", "a"), node("c", "b")], "c")).toBe("b");
  });

  it("lands on the session's start for the first message", () => {
    expect(rewindTarget([node("a")], "a")).toBe("");
  });

  it("offers nothing for an unknown entry or one after an unfinished turn", () => {
    expect(rewindTarget([node("a")], "z")).toBeNull();
    expect(rewindTarget([node("a", undefined, false), node("b", "a")], "b")).toBeNull();
  });
});

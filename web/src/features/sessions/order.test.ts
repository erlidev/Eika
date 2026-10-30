import { describe, expect, it } from "vitest";

import type { Session } from "@/api/types";
import { pinnedFirst, splitArchived } from "@/features/sessions/order";

function session(id: string, over: Partial<Session> = {}): Session {
  return {
    id,
    title: id,
    kind: "user",
    tools: [],
    overridden: false,
    pinned: false,
    archived: false,
    created_at: "2026-01-01T00:00:00Z",
    updated_at: "2026-01-01T00:00:00Z",
    ...over,
  };
}

const ids = (list: Session[]) => list.map((s) => s.id).join(", ");

describe("pinnedFirst", () => {
  it("moves pinned rows ahead and keeps the order within each group", () => {
    const list = [
      session("a"),
      session("b", { pinned: true }),
      session("c"),
      session("d", { pinned: true }),
    ];
    expect(ids(pinnedFirst(list))).toBe("b, d, a, c");
  });
});

describe("splitArchived", () => {
  it("separates the archived chats, each group pinned first", () => {
    const { active, archived } = splitArchived([
      session("a"),
      session("b", { archived: true }),
      session("c", { pinned: true }),
      session("d", { archived: true, pinned: true }),
    ]);
    expect(ids(active)).toBe("c, a");
    expect(ids(archived)).toBe("d, b");
  });
});

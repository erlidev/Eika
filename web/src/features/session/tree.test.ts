import { describe, expect, it } from "vitest";

import type { Node } from "@/api/types";
import { nextTreeIndex, treeRows } from "@/features/session/tree";

function node(id: string, parent?: string): Node {
  return {
    id,
    kind: "user",
    preview: id,
    created_at: "2026-03-14T15:00:00Z",
    ...(parent === undefined ? {} : { parent_id: parent }),
  };
}

// a - b - c - d (head), with a branch x - y off b.
const outline = [
  node("a"),
  node("b", "a"),
  node("c", "b"),
  node("d", "c"),
  node("x", "b"),
  node("y", "x"),
];

function shape(head = "d") {
  return treeRows(outline, head).map((r) => [r.node.id, r.level, r.parent, r.position, r.setSize]);
}

describe("treeRows", () => {
  it("keeps the conversation at one level and indents a branch under its entry", () => {
    expect(shape()).toEqual([
      ["a", 1, -1, 1, 4],
      ["b", 1, -1, 2, 4],
      ["x", 2, 1, 1, 2],
      ["y", 2, 1, 2, 2],
      ["c", 1, -1, 3, 4],
      ["d", 1, -1, 4, 4],
    ]);
  });

  it("follows the head onto the branch", () => {
    expect(shape("y").map(([id, level]) => [id, level])).toEqual([
      ["a", 1],
      ["b", 1],
      ["c", 2],
      ["d", 2],
      ["x", 1],
      ["y", 1],
    ]);
  });

  it("survives a cycle in a corrupt outline", () => {
    expect(treeRows([node("a", "b"), node("b", "a"), node("r")], "r")).toHaveLength(1);
  });
});

describe("nextTreeIndex", () => {
  const rows = treeRows(outline, "d");

  it.each([
    ["ArrowDown", 0, 1],
    ["ArrowDown", 5, 5],
    ["ArrowUp", 0, 0],
    ["ArrowUp", 3, 2],
    ["Home", 4, 0],
    ["End", 0, 5],
    ["ArrowRight", 1, 2],
    ["ArrowRight", 0, 0],
    ["ArrowLeft", 3, 1],
    ["ArrowLeft", 2, 1],
    ["ArrowLeft", 4, 4],
  ])("%s from row %i goes to row %i", (key, from, want) => {
    expect(nextTreeIndex(rows, from, key)).toBe(want);
  });

  it("leaves other keys alone", () => {
    expect(nextTreeIndex(rows, 0, "a")).toBeNull();
    expect(nextTreeIndex([], 0, "ArrowDown")).toBeNull();
  });
});

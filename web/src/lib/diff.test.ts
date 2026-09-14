import { describe, expect, it } from "vitest";

import { diffHunks, diffLines } from "@/lib/diff";

describe("diffLines", () => {
  it("marks an identical text as all context", () => {
    const lines = diffLines("a\nb\n", "a\nb\n");
    expect(lines.map((line) => line.kind)).toEqual(["context", "context"]);
  });

  it("finds a replaced line and numbers both sides", () => {
    const lines = diffLines("a\nb\nc\n", "a\nB\nc\n");
    expect(lines.map((line) => `${line.kind}:${line.text}`)).toEqual([
      "context:a",
      "remove:b",
      "add:B",
      "context:c",
    ]);
    expect(lines[1]?.oldLine).toBe(2);
    expect(lines[1]?.newLine).toBeUndefined();
    expect(lines[2]?.newLine).toBe(2);
    expect(lines[2]?.oldLine).toBeUndefined();
  });

  it("handles an empty old text as pure addition", () => {
    expect(diffLines("", "x\ny\n").map((line) => line.kind)).toEqual(["add", "add"]);
  });

  it("handles an empty new text as pure removal", () => {
    expect(diffLines("x\ny\n", "").map((line) => line.kind)).toEqual(["remove", "remove"]);
  });
});

describe("diffHunks", () => {
  it("returns nothing when the texts match", () => {
    expect(diffHunks("same\n", "same\n")).toEqual([]);
  });

  it("keeps only the context around a change", () => {
    const before = Array.from({ length: 20 }, (_, i) => `line ${String(i)}`).join("\n");
    const after = before.replace("line 10", "line ten");
    const hunks = diffHunks(before, after, 2);
    expect(hunks).toHaveLength(1);
    const hunk = hunks[0];
    expect(hunk?.lines).toHaveLength(6);
    expect(hunk?.oldStart).toBe(9);
  });

  it("splits changes that are far apart into separate hunks", () => {
    const before = Array.from({ length: 40 }, (_, i) => `line ${String(i)}`).join("\n");
    const after = before.replace("line 2", "two").replace("line 30", "thirty");
    expect(diffHunks(before, after, 2)).toHaveLength(2);
  });
});

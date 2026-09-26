import { describe, expect, it } from "vitest";

import { lineDiff } from "@/lib/lineDiff";

const kinds = (before: string, after: string) =>
  lineDiff(before, after).map((l) => `${l.kind[0] ?? ""}${l.text}`);

describe("lineDiff", () => {
  it("keeps equal texts as context", () => {
    expect(kinds("a\nb", "a\nb")).toEqual(["ca", "cb"]);
  });

  it("marks an added and a removed line", () => {
    expect(kinds("a\nb\nc", "a\nc\nd")).toEqual(["ca", "rb", "cc", "ad"]);
  });

  it("shows a changed line as a removal then an addition", () => {
    expect(kinds("keep\nold\nkeep", "keep\nnew\nkeep")).toEqual(["ckeep", "rold", "anew", "ckeep"]);
  });

  it("numbers each line in the text it belongs to", () => {
    const rows = lineDiff("a\nx", "y\na");
    expect(rows).toEqual([
      { kind: "add", text: "y", newLine: 1 },
      { kind: "context", text: "a", oldLine: 1, newLine: 2 },
      { kind: "remove", text: "x", oldLine: 2 },
    ]);
  });

  it("diffs against an empty text", () => {
    expect(kinds("", "a")).toEqual(["r", "aa"]);
  });
});

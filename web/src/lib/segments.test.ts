import { describe, expect, it } from "vitest";

import { segments } from "@/lib/segments";
import type { ByteSpan } from "@/lib/segments";

const span = (start: number, end: number, field: "body" | "description" = "body"): ByteSpan => ({
  field,
  start,
  end,
});

describe("segments", () => {
  it("splits at byte offsets, so text before a multibyte letter still lines up", () => {
    // "é" is two bytes: "caf" is 0-3, "é" 3-5, " ignore" starts at 5.
    expect(segments("café ignore it", "body", [span(6, 12)])).toEqual([
      { text: "café ", flagged: false },
      { text: "ignore", flagged: true },
      { text: " it", flagged: false },
    ]);
  });

  it("merges overlaps, keeps other fields' spans out and clamps to the text", () => {
    expect(
      segments("abcdef", "body", [span(1, 3), span(2, 5), span(0, 2, "description"), span(5, 99)]),
    ).toEqual([
      { text: "a", flagged: false },
      { text: "bcdef", flagged: true },
    ]);
    expect(segments("plain", "body", [])).toEqual([{ text: "plain", flagged: false }]);
  });

  it("marks a span at the very start and the very end of the text", () => {
    expect(segments("abcd", "body", [span(0, 2), span(2, 4)])).toEqual([
      { text: "abcd", flagged: true },
    ]);
    expect(segments("abcd", "body", [span(0, 1)])).toEqual([
      { text: "a", flagged: true },
      { text: "bcd", flagged: false },
    ]);
    expect(segments("abcd", "body", [span(3, 4)])).toEqual([
      { text: "abc", flagged: false },
      { text: "d", flagged: true },
    ]);
  });

  it("drops a span that is empty, negative or past the end", () => {
    expect(segments("abc", "body", [span(1, 1), span(-2, 2), span(3, 9)])).toEqual([
      { text: "abc", flagged: false },
    ]);
  });

  it("counts bytes over multi-byte text: CJK and an emoji", () => {
    // "日本" is 3 bytes each, the emoji 4: bytes 0-6, 6-10, then "!" at 10.
    expect(segments("日本😀!", "body", [span(3, 10)])).toEqual([
      { text: "日", flagged: false },
      { text: "本😀", flagged: true },
      { text: "!", flagged: false },
    ]);
  });
});

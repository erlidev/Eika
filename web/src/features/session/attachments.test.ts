import { describe, expect, it } from "vitest";

import { attachProblem, maxImageBytes, maxImages } from "@/features/session/attachments";
import type { DraftImage } from "@/features/session/attachments";

const png = (name: string, size = 1000) => ({ name, type: "image/png", size });

const attached = (n: number, size = 1000): DraftImage[] =>
  Array.from({ length: n }, (_, i) => ({
    id: String(i),
    name: `a${String(i)}.png`,
    mediaType: "image/png",
    data: "",
    size,
  }));

describe("attachProblem", () => {
  it("accepts the formats the harness reads", () => {
    const files = [
      png("a.png"),
      { name: "b.jpg", type: "image/jpeg", size: 1 },
      { name: "c.gif", type: "image/gif", size: 1 },
      { name: "d.webp", type: "image/webp", size: 1 },
    ];
    expect(attachProblem([], files)).toBeNull();
  });

  it.each([
    [[], [{ name: "photo.heic", type: "image/heic", size: 10 }], /photo.heic is not a PNG, JPEG/],
    [[], [png("huge.png", maxImageBytes + 1)], /huge.png is 21 MB, over the 20 MB/],
    [[], [png("empty.png", 0)], /empty.png is empty/],
    [attached(maxImages), [png("one-more.png")], /at most 10 images/],
    [
      attached(2, 15 * 1024 * 1024),
      [png("third.png", 5 * 1024 * 1024)],
      /come to 35 MB, over the 32 MB/,
    ],
  ])("refuses %#", (existing, files, want) => {
    expect(attachProblem(existing, files)).toMatch(want);
  });
});

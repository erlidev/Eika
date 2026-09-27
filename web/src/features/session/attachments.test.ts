import { describe, expect, it } from "vitest";

import {
  attachProblem,
  draftsOf,
  imageFiles,
  maxImageBytes,
  maxImages,
  queuedText,
  readImage,
} from "@/features/session/attachments";
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
    [[], [{ name: "notes.txt", type: "text/plain", size: 10 }], /notes.txt is not a PNG/],
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

describe("imageFiles", () => {
  it("keeps what claims to be an image, which a paste of text and a picture holds both of", () => {
    const files = [png("a.png"), { name: "b.txt", type: "text/plain", size: 1 }];
    expect(imageFiles(files).map((f) => f.name)).toEqual(["a.png"]);
  });
});

describe("queuedText", () => {
  it("counts the images of a queued message", () => {
    expect(queuedText({ text: "look", images: 0 })).toBe("look");
    expect(queuedText({ text: "look", images: 2 })).toBe("look (2 images)");
    expect(queuedText({ text: "", images: 1 })).toBe("1 image");
  });
});

describe("draftsOf", () => {
  it("turns sent images back into attachments with their sizes", () => {
    const drafts = draftsOf([
      { media_type: "image/png", data: "AAAA", width: 1, height: 1 },
      { media_type: "image/jpeg", data: "AAA=", width: 1, height: 1 },
    ]);
    expect(drafts.map((d) => [d.name, d.mediaType, d.size])).toEqual([
      ["image-1.png", "image/png", 3],
      ["image-2.jpg", "image/jpeg", 2],
    ]);
    expect(new Set(drafts.map((d) => d.id)).size).toBe(2);
  });
});

describe("readImage", () => {
  it("reads a file as base64", async () => {
    const file = new File([new Uint8Array([0x89, 0x50, 0x4e, 0x47])], "a.png", {
      type: "image/png",
    });
    const draft = await readImage(file);
    expect(draft).toMatchObject({
      name: "a.png",
      mediaType: "image/png",
      data: "iVBORw==",
      size: 4,
    });
  });
});

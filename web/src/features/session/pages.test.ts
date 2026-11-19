import { expect, it } from "vitest";
import type { Entry } from "@/api/types";
import { readCatchUp } from "@/features/session/pages";

it("reads back to the overlap even when more than one page arrived", async () => {
  const entry = (id: string): Entry => ({
    id,
    seq: Number(id),
    kind: "user",
    created_at: "2026-01-01T00:00:00Z",
    message: { role: "user", content: id },
  });
  const pages = [
    { entries: [entry("4"), entry("5")], next_before: "4" },
    { entries: [entry("2"), entry("3")], next_before: "2" },
    { entries: [entry("1")] },
  ];
  const seen: (string | undefined)[] = [];
  const result = await readCatchUp(
    (before) => {
      seen.push(before);
      const page = pages[seen.length - 1];
      if (!page) throw new Error("unexpected page");
      return Promise.resolve(page);
    },
    "2",
    false,
  );
  expect(seen).toEqual([undefined, "4"]);
  expect(result.entries.map((item) => item.id)).toEqual(["4", "5", "2", "3"]);
});

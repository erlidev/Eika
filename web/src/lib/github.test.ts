import { describe, expect, it } from "vitest";

import { compareUrl, githubRepo } from "@/lib/github";

describe("githubRepo", () => {
  it.each([
    ["https://github.com/erlidev/eika.git", "erlidev/eika"],
    ["https://github.com/erlidev/eika", "erlidev/eika"],
    ["https://github.com/erlidev/eika/", "erlidev/eika"],
    ["https://token@github.com/erlidev/eika.git", "erlidev/eika"],
    ["git@github.com:erlidev/eika.git", "erlidev/eika"],
    ["ssh://git@github.com/erlidev/eika.git", "erlidev/eika"],
  ])("reads %s", (url, want) => {
    expect(githubRepo(url)).toBe(want);
  });

  it.each([
    "https://gitlab.com/erlidev/eika.git",
    "https://github.example.com/erlidev/eika.git",
    "https://github.com/erlidev",
    "/srv/code/eika",
    "",
  ])("refuses %s", (url) => {
    expect(githubRepo(url)).toBeUndefined();
  });
});

describe("compareUrl", () => {
  it("keeps the slashes of a branch and escapes the rest", () => {
    expect(compareUrl("git@github.com:erlidev/eika.git", "eika/fix #1")).toBe(
      "https://github.com/erlidev/eika/compare/eika/fix%20%231?expand=1",
    );
  });

  it("has no link without a GitHub remote", () => {
    expect(compareUrl(undefined, "main")).toBeUndefined();
    expect(compareUrl("https://gitlab.com/a/b", "main")).toBeUndefined();
  });
});

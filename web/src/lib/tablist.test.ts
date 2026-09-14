import { describe, expect, it } from "vitest";

import { nextTabIndex } from "@/lib/tablist";

describe("nextTabIndex", () => {
  it("moves left and right", () => {
    expect(nextTabIndex(0, 3, "ArrowRight")).toBe(1);
    expect(nextTabIndex(1, 3, "ArrowLeft")).toBe(0);
  });

  it("wraps at both ends", () => {
    expect(nextTabIndex(2, 3, "ArrowRight")).toBe(0);
    expect(nextTabIndex(0, 3, "ArrowLeft")).toBe(2);
  });

  it("jumps to the first and the last tab", () => {
    expect(nextTabIndex(1, 3, "Home")).toBe(0);
    expect(nextTabIndex(1, 3, "End")).toBe(2);
  });

  it("leaves a key it does not handle alone", () => {
    expect(nextTabIndex(1, 3, "Enter")).toBeNull();
    expect(nextTabIndex(1, 3, "ArrowDown")).toBeNull();
  });

  it("handles an empty tablist", () => {
    expect(nextTabIndex(0, 0, "ArrowRight")).toBeNull();
  });
});

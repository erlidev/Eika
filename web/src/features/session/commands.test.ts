import { describe, expect, it } from "vitest";

import { compactCommand } from "@/features/session/commands";

describe("compactCommand", () => {
  it("reads the command with and without a focus", () => {
    expect(compactCommand("/compact")).toBe("");
    expect(compactCommand("  /compact  ")).toBe("");
    expect(compactCommand("/compact the retry tests")).toBe("the retry tests");
    expect(compactCommand("/compact\nthe plan\nand the tests")).toBe("the plan\nand the tests");
  });

  it("leaves a message alone", () => {
    expect(compactCommand("compact the logs")).toBeNull();
    expect(compactCommand("/compacted")).toBeNull();
    expect(compactCommand("please /compact")).toBeNull();
  });
});

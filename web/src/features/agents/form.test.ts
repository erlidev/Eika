import { describe, expect, it } from "vitest";

import { layerName, promptChange } from "@/features/agents/form";

describe("promptChange", () => {
  it("counts the lines a prompt adds to and drops from the built-in one", () => {
    expect(promptChange("a\nb", "a\nb")).toEqual({ same: true, added: 0, removed: 0 });
    expect(promptChange("a\nb", "a\nc\nd")).toEqual({ same: false, added: 2, removed: 1 });
  });

  it("counts a moved line as the change the diff shows", () => {
    const change = promptChange("a\nb\nc", "b\nc\na");
    expect(change.same).toBe(false);
    expect(change.added).toBe(1);
    expect(change.removed).toBe(1);
  });

  it("counts a duplicated line", () => {
    expect(promptChange("a\nb", "a\nb\nb")).toEqual({ same: false, added: 1, removed: 0 });
    expect(promptChange("a\nb\nb", "a\nb")).toEqual({ same: false, added: 0, removed: 1 });
  });
});

describe("layerName", () => {
  it("names the layer a value falls through to", () => {
    expect(layerName("default")).toBe("the default");
    expect(layerName("agent")).toBe("the agent");
  });
});

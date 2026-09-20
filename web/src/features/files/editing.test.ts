import { beforeEach, describe, expect, it } from "vitest";

import {
  afterSave,
  decideOpen,
  isDirty,
  parentDir,
  toggle,
  useFilesStore,
} from "@/features/files/editing";

describe("isDirty", () => {
  it("is clean until the user types", () => {
    expect(isDirty(undefined, undefined)).toBe(false);
    expect(isDirty({ path: "a" }, "text")).toBe(false);
  });

  it("is dirty when the draft differs from what is saved", () => {
    expect(isDirty({ path: "a", draft: "new" }, "old")).toBe(true);
  });

  it("is clean again when the draft is typed back to what is saved", () => {
    expect(isDirty({ path: "a", draft: "old" }, "old")).toBe(false);
  });
});

describe("decideOpen", () => {
  it("stays on the open file", () => {
    expect(decideOpen({ path: "a", draft: "x" }, "y", "a")).toBe("stay");
  });

  it("opens another file when nothing is unsaved", () => {
    expect(decideOpen(undefined, undefined, "a")).toBe("open");
    expect(decideOpen({ path: "a" }, "y", "b")).toBe("open");
  });

  it("asks before leaving unsaved changes", () => {
    expect(decideOpen({ path: "a", draft: "x" }, "y", "b")).toBe("confirm");
  });
});

describe("afterSave", () => {
  it("drops the draft that was saved", () => {
    expect(afterSave({ path: "a", draft: "x" }, "a", "x")).toEqual({ path: "a" });
  });

  it("keeps what was typed during the save", () => {
    expect(afterSave({ path: "a", draft: "xy" }, "a", "x")).toEqual({ path: "a", draft: "xy" });
  });

  it("leaves another file alone", () => {
    expect(afterSave({ path: "b", draft: "x" }, "a", "x")).toEqual({ path: "b", draft: "x" });
  });
});

describe("paths", () => {
  it("finds the parent directory", () => {
    expect(parentDir("go.mod")).toBe("");
    expect(parentDir("internal/webhook/client.go")).toBe("internal/webhook");
  });

  it("toggles a directory", () => {
    expect(toggle([], "a")).toEqual(["a"]);
    expect(toggle(["a", "b"], "a")).toEqual(["b"]);
  });
});

describe("useFilesStore", () => {
  beforeEach(() => {
    useFilesStore.setState({ byWorkspace: {} });
  });

  it("keeps each workspace's edit apart", () => {
    const { update } = useFilesStore.getState();
    update("ws-1", (f) => ({ ...f, open: { path: "a", draft: "x" } }));
    update("ws-2", (f) => ({ ...f, expanded: ["src"] }));
    expect(useFilesStore.getState().byWorkspace).toEqual({
      "ws-1": { expanded: [], open: { path: "a", draft: "x" } },
      "ws-2": { expanded: ["src"] },
    });
  });
});
